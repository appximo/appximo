package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/tenant"
)

const maxPerTenant = 1000

// cacheItem stores a response in BOTH representations so a HIT can be served
// without ever touching the gzip middleware again:
//   - plain   : the handler's raw bytes (served to clients that don't accept gzip)
//   - gzipped : gzip(BestSpeed) of plain, served verbatim with Content-Encoding: gzip
//
// gzipped is nil when the body is empty, non-200, or compression didn't shrink it.
// Pre-compressing once on the (rare) miss removes per-HIT gzip CPU, which the
// S30 profile showed at 58.6% of total — almost all of it spent recompressing
// identical cached bodies on every hit.
type cacheItem struct {
	status      int
	contentType string
	etag        string
	plain       []byte
	gzipped     []byte
	gzipLen     string // strconv.Itoa(len(gzipped)), precomputed once for the Content-Length header
	expiresAt   time.Time
}

// ResponseCache is a thread-safe in-memory response cache keyed by
// (tenantID, requestURI) with a fixed TTL.
// Items are served on exact-match GET requests and evicted at TTL.
type ResponseCache struct {
	mu      sync.RWMutex
	tenants map[string]map[string]*cacheItem // tenantID → requestURI → item
	ttl     time.Duration

	// sf collapses concurrent cache-miss refreshes for the same (tenant, key)
	// into a single backend call. Without it, every goroutine that arrives in
	// the window between TTL expiry and the next write hits Postgres at once
	// (the "cache stampede" measured as a 3.2s uncached p95 under 2000 RPS).
	sf singleflight.Group

	// roleCacheable reports whether a role's responses may be shared through the
	// cache. Roles with row-level conditions or field restrictions produce
	// per-user/per-role responses, so caching them would let one user receive
	// another's data on a HIT (the RBAC-bypass-via-cache vulnerability). nil
	// means "cache everything" — the default used by tests with no RBAC policy.
	roleCacheable func(role string) bool
}

// SetRoleCacheGate installs a predicate that decides, per role, whether the
// response cache may store and serve that role's responses. Roles that fail the
// gate bypass the cache entirely so their RBAC conditions run on every request.
func (rc *ResponseCache) SetRoleCacheGate(fn func(role string) bool) {
	rc.roleCacheable = fn
}

// cacheable reports whether responses for role may be cached. With no gate set,
// every role is cacheable (test default). With a gate, an unknown/empty role is
// treated as NOT cacheable, which fails safe.
func (rc *ResponseCache) cacheable(role string) bool {
	if rc.roleCacheable == nil {
		return true
	}
	return rc.roleCacheable(role)
}

// New returns a ResponseCache with the given TTL and starts a background
// goroutine that purges expired items every 30 seconds.
func New(ttl time.Duration) *ResponseCache {
	rc := &ResponseCache{
		tenants: make(map[string]map[string]*cacheItem),
		ttl:     ttl,
	}
	go rc.cleanupLoop()
	return rc
}

// Invalidate removes all cached entries for tenantID.
// Call this when a schema_updated pg_notify arrives for that tenant.
func (rc *ResponseCache) Invalidate(tenantID string) {
	rc.mu.Lock()
	delete(rc.tenants, tenantID)
	rc.mu.Unlock()
}

// Middleware returns an http.Handler that sits after authentication/RBAC
// in the chain and short-circuits identical GET requests with cached bodies.
//
// Rules:
//   - Only GET requests are cached.
//   - Requests with Cache-Control: no-cache bypass the cache entirely.
//   - Requests with If-None-Match bypass the cache (let ETag/304 logic handle them).
//   - Only HTTP 200 responses are stored.
//   - At most maxPerTenant (1000) entries per tenant; new entries are dropped when full.
func (rc *ResponseCache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Cache-Control") == "no-cache" {
			next.ServeHTTP(w, r)
			return
		}
		// Let CachedGet handle conditional requests — don't interfere.
		if r.Header.Get("If-None-Match") != "" {
			next.ServeHTTP(w, r)
			return
		}

		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			next.ServeHTTP(w, r)
			return
		}

		hdr := r.Header.Get("Authorization")
		tokenStr := ""
		if strings.HasPrefix(hdr, "Bearer ") {
			tokenStr = strings.TrimPrefix(hdr, "Bearer ")
		}

		// Serve from response cache only when this token was recently validated by JWT.
		// No Authorization header → no short-circuit (JWT will reject protected paths).
		// Unknown token → no short-circuit (JWT will validate it on the way through).
		// Known token + cached response → skip JWT HMAC, DB, AND gzip entirely.
		if tokenStr != "" {
			if claims, ok := auth.GetCachedClaims(tokenStr); ok {
				// The cache runs BEFORE JWTMiddleware and a HIT short-circuits it,
				// so the cache must replicate JWT's tenant cross-check itself.
				// Otherwise a validated token for tenant A, replayed against
				// Host=b.<domain> (tc.ID="b"), would be served tenant B's cached
				// entry — a cross-tenant data leak. A mismatched (or empty/cross-
				// tenant) token falls through to the full JWT+RBAC chain, which
				// rejects it.
				if claims.TenantID != tc.ID {
					next.ServeHTTP(w, r)
					return
				}
				// Roles with row-level conditions or field restrictions must
				// never be served from or stored in the cache: the cache key is
				// (tenant, URI) and a HIT short-circuits before RBAC runs, so a
				// shared entry would leak one user's rows to another. Bypass the
				// cache entirely and let the full RBAC chain run every time.
				if !rc.cacheable(claims.Role) {
					next.ServeHTTP(w, r)
					return
				}
				key := r.URL.RequestURI()
				if item := rc.get(tc.ID, key); item != nil {
					if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
						t.Mark("cache_hit")
					}
					writeItem(w, r, item, true) // HIT
					return
				}
				// Validated token + cache miss (TTL expired): collapse the
				// refresh through singleflight so a burst of identical requests
				// triggers exactly one backend call. Safe to share the result
				// because the token was already validated — the same trust model
				// as the HIT path above.
				item := rc.refresh(r, tc.ID, key, next)
				if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
					t.Mark("cache_miss")
				}
				writeItem(w, r, item, false)
				return
			}
		}

		key := r.URL.RequestURI() // path + query string

		// No validated token: let the request flow through individually so JWT
		// validates it, forwarding the response untouched (the gzip middleware
		// compresses it as usual). These are NOT collapsed — sharing a response
		// across unvalidated tokens would let an invalid token piggyback on a
		// valid 200. We still populate the cache (with a pre-gzipped copy) so the
		// next request — now token-validated — gets the gzip-free HIT path.
		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)
		if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
			t.Mark("cache_miss")
		}

		if cw.status == http.StatusOK && cw.buf.Len() > 0 {
			// Only store if the now-validated role is cacheable. JWT has run
			// inside next, so a 200 means the token is in the claims cache; a
			// condition/field-restricted role (or an unknown/empty role) must
			// not have its response cached.
			role := ""
			if claims, ok := auth.GetCachedClaims(tokenStr); ok {
				role = claims.Role
			}
			if rc.cacheable(role) {
				plain := append([]byte(nil), cw.buf.Bytes()...)
				rc.set(tc.ID, key, rc.buildItem(cw.status, cw.Header(), plain))
			}
		}
	})
}

// refresh executes the cache-miss handler under singleflight: the first caller
// for a given (tenantID, key) runs next and populates the cache; concurrent
// callers block and share the identical *cacheItem. The handler runs against an
// in-memory buffer (not any caller's ResponseWriter) so the captured result can
// be replayed — in whichever encoding each caller accepts — to every waiter.
func (rc *ResponseCache) refresh(r *http.Request, tenantID, key string, next http.Handler) *cacheItem {
	res, _, _ := rc.sf.Do(tenantID+"\x00"+key, func() (any, error) {
		rec := &bufferedResponse{header: make(http.Header), status: http.StatusOK}
		next.ServeHTTP(rec, r)
		plain := append([]byte(nil), rec.body.Bytes()...)
		item := rc.buildItem(rec.status, rec.header, plain)
		if item.status == http.StatusOK && len(plain) > 0 {
			rc.set(tenantID, key, item)
		}
		return item, nil
	})
	// The returned item is immutable once built; singleflight guarantees the
	// producing function returned before Do does, so concurrent reads are safe.
	return res.(*cacheItem)
}

// buildItem captures a handler response into a cacheItem, pre-compressing the
// body with gzip BestSpeed (fast, not BestCompression) for 200 responses.
func (rc *ResponseCache) buildItem(status int, header http.Header, plain []byte) *cacheItem {
	item := &cacheItem{
		status:      status,
		contentType: header.Get("Content-Type"),
		etag:        header.Get("Etag"),
		plain:       plain,
		expiresAt:   time.Now().Add(rc.ttl),
	}
	if status == http.StatusOK && len(plain) > 0 {
		item.gzipped = gzipBytes(plain)
		if item.gzipped != nil {
			item.gzipLen = strconv.Itoa(len(item.gzipped))
		}
	}
	return item
}

// writeItem serves a cached item, choosing the pre-compressed body when the
// client accepts gzip. Setting Content-Encoding makes the downstream chi
// Compress middleware skip recompression (it bails out when the header is
// already present), so a HIT never pays for gzip again.
func writeItem(w http.ResponseWriter, r *http.Request, item *cacheItem, hit bool) {
	h := w.Header()
	if item.contentType != "" {
		h.Set("Content-Type", item.contentType)
	}
	if item.etag != "" {
		h.Set("Etag", item.etag)
	}
	if hit {
		h.Set("X-Cache", "HIT")
	}

	if item.gzipped != nil && acceptsGzip(r) {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Set("Content-Length", item.gzipLen)
		w.WriteHeader(item.status)
		w.Write(item.gzipped) //nolint:errcheck
		if t := observability.SpanTrackerFromCtx(r.Context()); t != nil {
			t.Mark("gzip")
		}
		return
	}

	// Plain body: leave it to the gzip middleware (if any) to compress on the
	// wire for clients that accept it. No Content-Encoding set here.
	w.WriteHeader(item.status)
	w.Write(item.plain) //nolint:errcheck
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// gzipPool reuses gzip.Writers across requests. Level is fixed at BestSpeed.
var gzipPool = sync.Pool{
	New: func() any {
		zw, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return zw
	},
}

// gzipBytes returns gzip(BestSpeed) of plain, or nil if compression failed or
// did not actually shrink the body (tiny payloads can grow under gzip framing).
func gzipBytes(plain []byte) []byte {
	var buf bytes.Buffer
	zw := gzipPool.Get().(*gzip.Writer)
	zw.Reset(&buf)
	if _, err := zw.Write(plain); err != nil {
		zw.Reset(io.Discard)
		gzipPool.Put(zw)
		return nil
	}
	if err := zw.Close(); err != nil {
		zw.Reset(io.Discard)
		gzipPool.Put(zw)
		return nil
	}
	gzipPool.Put(zw)
	if buf.Len() >= len(plain) {
		return nil
	}
	return buf.Bytes()
}

func (rc *ResponseCache) get(tenantID, key string) *cacheItem {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	bucket := rc.tenants[tenantID]
	if bucket == nil {
		return nil
	}
	item := bucket[key]
	if item == nil || time.Now().After(item.expiresAt) {
		return nil
	}
	return item
}

func (rc *ResponseCache) set(tenantID, key string, item *cacheItem) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	bucket := rc.tenants[tenantID]
	if bucket == nil {
		bucket = make(map[string]*cacheItem)
		rc.tenants[tenantID] = bucket
	}
	if len(bucket) >= maxPerTenant {
		return // drop new entry rather than evict; TTL will free space
	}
	bucket[key] = item
}

func (rc *ResponseCache) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		rc.purgeExpired()
	}
}

func (rc *ResponseCache) purgeExpired() {
	now := time.Now()
	rc.mu.Lock()
	defer rc.mu.Unlock()
	for tid, bucket := range rc.tenants {
		for key, item := range bucket {
			if now.After(item.expiresAt) {
				delete(bucket, key)
			}
		}
		if len(bucket) == 0 {
			delete(rc.tenants, tid)
		}
	}
}

// captureWriter intercepts Write calls to capture the response body for caching
// while simultaneously forwarding to the real ResponseWriter.
// Header() and WriteHeader() are forwarded immediately so headers and status
// reach the client normally on a cache miss.
type captureWriter struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (cw *captureWriter) WriteHeader(code int) {
	cw.status = code
	cw.ResponseWriter.WriteHeader(code)
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	cw.buf.Write(b)
	return cw.ResponseWriter.Write(b)
}

// bufferedResponse is an http.ResponseWriter that records status, headers and
// body into memory without forwarding to any client connection. It backs the
// singleflight refresh path, where one handler invocation is replayed to many
// waiting callers. Once the producing handler returns it is read-only and safe
// to share across goroutines.
type bufferedResponse struct {
	header      http.Header
	status      int
	body        bytes.Buffer
	wroteHeader bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) {
	if !b.wroteHeader {
		b.status = code
		b.wroteHeader = true
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if !b.wroteHeader {
		b.WriteHeader(http.StatusOK)
	}
	return b.body.Write(p)
}
