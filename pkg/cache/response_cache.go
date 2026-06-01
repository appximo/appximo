package cache

import (
	"bytes"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/tenant"
)

const maxPerTenant = 1000

type cacheItem struct {
	status      int
	contentType string
	body        []byte
	expiresAt   time.Time
}

// ResponseCache is a thread-safe in-memory response cache keyed by
// (tenantID, requestURI) with a fixed TTL.
// Items are served on exact-match GET requests and evicted at TTL.
type ResponseCache struct {
	mu      sync.RWMutex
	tenants map[string]map[string]*cacheItem // tenantID → requestURI → item
	ttl     time.Duration
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
		// Known token + cached response → skip JWT HMAC and DB entirely.
		if tokenStr != "" {
			if _, ok := auth.GetCachedClaims(tokenStr); ok {
				key := r.URL.RequestURI()
				if item := rc.get(tc.ID, key); item != nil {
					w.Header().Set("Content-Type", item.contentType)
					w.Header().Set("X-Cache", "HIT")
					w.WriteHeader(item.status)
					w.Write(item.body) //nolint:errcheck
					return
				}
			}
		}

		key := r.URL.RequestURI() // path + query string

		cw := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)

		// Only cache successful, non-empty responses.
		if cw.status == http.StatusOK && cw.buf.Len() > 0 {
			body := make([]byte, cw.buf.Len())
			copy(body, cw.buf.Bytes())
			rc.set(tc.ID, key, &cacheItem{
				status:      cw.status,
				contentType: cw.Header().Get("Content-Type"),
				body:        body,
				expiresAt:   time.Now().Add(rc.ttl),
			})
		}
	})
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
