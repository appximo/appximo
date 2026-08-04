package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/observability"
	"github.com/appximo/appximo/pkg/tenant"
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
	// secHeaders are the SECURITY headers captured from the handler chain, so a
	// cache HIT carries the same posture as a MISS (SEC-1).
	//
	// The engine's chain sets security headers in two places: SecurityHeaders
	// runs OUTSIDE the cache (so its headers are re-applied on every request and
	// survive a hit for free), while StrictCSP runs INSIDE the cached group — so
	// before this, a cached response silently lost its Content-Security-Policy.
	// The header was present on a `Cache-Control: no-cache` bypass and absent on
	// every cached read: the same URL answered with two different security
	// postures depending on cache state.
	//
	// An ALLOWLIST, not the whole header set, and that is deliberate: replaying
	// every captured header would replay per-request values too. `X-Trace-Id` is
	// the clear example — serving one request's trace id to a thousand later
	// callers would corrupt the trace ring and make an incident unreadable.
	// Content-Length/Encoding/Vary are recomputed per encoding, and Date must be
	// the response's own. So the cache replays exactly the headers whose whole
	// purpose is to be identical for every caller.
	secHeaders []headerKV
	plain      []byte
	gzipped    []byte
	gzipLen    string // strconv.Itoa(len(gzipped)), precomputed once for the Content-Length header
	expiresAt  time.Time
}

// headerKV is one captured header. A slice of pairs rather than an http.Header
// map: it is built once per cached item, read on every hit, and never mutated —
// so the flat form is both cheaper to range over and impossible to alias.
type headerKV struct{ k, v string }

// cachedSecurityHeaders is the allowlist replayed onto a cache hit. It is the
// security-header family: every one of them is a property of the RESPONSE'S
// CONTENT, identical for every caller of the same URI, which is exactly what
// makes them safe to cache. Adding a header here is a deliberate act; anything
// per-request must NOT be added (see the note on cacheItem.secHeaders).
var cachedSecurityHeaders = []string{
	"Content-Security-Policy",
	"Content-Security-Policy-Report-Only",
	"X-Content-Type-Options",
	"X-Frame-Options",
	"Referrer-Policy",
	"Strict-Transport-Security",
	"Permissions-Policy",
	"Cross-Origin-Opener-Policy",
	"Cross-Origin-Embedder-Policy",
	"Cross-Origin-Resource-Policy",
	"X-Xss-Protection",
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
	// another's data on a HIT (the RBAC-bypass-via-cache vulnerability). Held
	// behind an atomic.Pointer (MT-STRUCT-S4): a per-app hot-swap re-sets the
	// gate to the newly-deployed policy's cacheability WHILE the old surface's
	// cache middleware may be reading it concurrently — a plain field would be a
	// data race, and a stale gate would keep caching a role that the new schema
	// just gave a row condition (a narrow RBAC-bypass regression). A nil pointer
	// means "cache everything" — the default used by tests with no RBAC policy.
	roleCacheable atomic.Pointer[roleGate]

	// jwtSecret scopes this cache's claims-cache lookups to the OWNING app's
	// JWT secret (MT-STRUCT-S3): the claims cache is keyed by (secret, token),
	// so this cache can only see validations performed with ITS app's secret —
	// a token validated by another in-process app never short-circuits here.
	// Empty (the test default) matches claims seeded with an empty secret.
	jwtSecret string

	// bypassStatic reports paths that never enter the cache (see
	// SetBypassMatcher). Set once at boot, read-only afterwards; nil for every
	// app that declares no static mount, so the hot path pays one nil check.
	bypassStatic func(path string) bool
}

// SetJWTSecret scopes the cache's claims-cache lookups to the app's JWT secret
// (see the jwtSecret field). Called once at boot, before serving.
func (rc *ResponseCache) SetJWTSecret(s string) { rc.jwtSecret = s }

// roleGate wraps the cacheability predicate so it can live behind an
// atomic.Pointer (a bare func value is not atomically swappable).
type roleGate struct{ fn func(role string) bool }

// SetBypassMatcher declares which paths the cache must never buffer or store
// (LOOSE-ENDS-SWEEP-S1) — user-declared static mounts. A frontend's assets are
// already cached by the browser (immutable hashed filenames) and range-served by
// http.ServeContent; putting them through captureWriter would buffer every JS
// chunk and image into RAM, per tenant, for nothing. Same reasoning as the
// built-in /api/files/ and /admin/ bypasses, but a mount is per-app (and a root
// mount is not expressible as a prefix), so it is a predicate set at boot.
// nil — the default — leaves the hot path byte-identical.
func (rc *ResponseCache) SetBypassMatcher(fn func(path string) bool) { rc.bypassStatic = fn }

// SetRoleCacheGate installs a predicate that decides, per role, whether the
// response cache may store and serve that role's responses. Roles that fail the
// gate bypass the cache entirely so their RBAC conditions run on every request.
// Atomic (MT-STRUCT-S4): safe to re-call on a hot-swap while requests read it.
func (rc *ResponseCache) SetRoleCacheGate(fn func(role string) bool) {
	rc.roleCacheable.Store(&roleGate{fn: fn})
}

// cacheable reports whether responses for role may be cached. With no gate set,
// every role is cacheable (test default). With a gate, an unknown/empty role is
// treated as NOT cacheable, which fails safe.
func (rc *ResponseCache) cacheable(role string) bool {
	g := rc.roleCacheable.Load()
	if g == nil || g.fn == nil {
		return true
	}
	return g.fn(role)
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
		// SSE event streams (GET /api/{resource}/events, S45) are infinite
		// responses: they must never be buffered by captureWriter, stored, nor
		// singleflight-collapsed (refresh would merge N subscribers into one
		// stream). Without this bypass the subscriber gets ": connected" + EOF
		// — and the truncated preamble would be CACHED. A resource literally
		// named "events" only loses list-caching here, never correctness.
		if strings.HasSuffix(r.URL.Path, "/events") {
			next.ServeHTTP(w, r)
			return
		}
		// File downloads (GET /api/files/{id} and the signed-token route
		// GET /files/signed/{token}, FILES-V1/V2) stream an unbounded binary
		// blob: buffering it into captureWriter — let alone storing it — would put
		// the whole file in RAM (and, for a cacheable role, cache it). Bypass for
		// the same reason SSE does, on the same hot path (one HasPrefix per GET).
		if strings.HasPrefix(r.URL.Path, "/api/files/") || strings.HasPrefix(r.URL.Path, "/files/signed/") {
			next.ServeHTTP(w, r)
			return
		}
		// Admin routes (/admin/*) do their OWN auth (platform token / admin key /
		// signed download token) and are off the hot path; when Studio is reached
		// via a tenant subdomain they would otherwise be buffered by captureWriter
		// — including the files manager's binary downloads (UI-F5-S1). Never cache
		// nor buffer them.
		if strings.HasPrefix(r.URL.Path, "/admin/") {
			next.ServeHTTP(w, r)
			return
		}
		// User-declared static mounts (LOOSE-ENDS-SWEEP-S1): a frontend's bundles
		// are browser-cached by content hash and range-served; buffering them here
		// would put every chunk in RAM per tenant for no benefit.
		if rc.bypassStatic != nil && rc.bypassStatic(r.URL.Path) {
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

		// Must use the SAME parser as the JWT middleware. When these disagree the
		// cache silently stops working for whatever the middleware accepts and this
		// does not — a pure performance cliff with no error anywhere (ADR-024).
		tokenStr, _ := auth.BearerToken(r.Header.Get("Authorization"))

		// Serve from response cache only when this token was recently validated by JWT.
		// No Authorization header → no short-circuit (JWT will reject protected paths).
		// Unknown token → no short-circuit (JWT will validate it on the way through).
		// Known token + cached response → skip JWT HMAC, DB, AND gzip entirely.
		if tokenStr != "" {
			if claims, ok := auth.GetCachedClaims(rc.jwtSecret, tokenStr); ok {
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
			if claims, ok := auth.GetCachedClaims(rc.jwtSecret, tokenStr); ok {
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
		secHeaders:  captureSecurityHeaders(header),
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
	// SEC-1: re-apply the security headers the handler chain set, so a HIT and a
	// MISS answer with the same posture. Set (not Add) so re-applying a header the
	// outer SecurityHeaders middleware already wrote is idempotent.
	for _, kv := range item.secHeaders {
		h.Set(kv.k, kv.v)
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

// captureSecurityHeaders extracts the allowlisted security headers from a
// handler's response so they can be replayed on a cache hit (SEC-1). Returns nil
// when none are present, so a cached item costs nothing extra on a chain that
// sets none.
func captureSecurityHeaders(header http.Header) []headerKV {
	var out []headerKV
	for _, k := range cachedSecurityHeaders {
		if v := header.Get(k); v != "" {
			out = append(out, headerKV{k, v})
		}
	}
	return out
}
