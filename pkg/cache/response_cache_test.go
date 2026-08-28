package cache

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/tenant"
)

const testToken = "test-bearer-token-for-cache-tests"

// withTenant injects a TenantCtx into the request context.
func withTenant(r *http.Request, id string) *http.Request {
	ctx := tenant.WithContext(r.Context(), &tenant.TenantCtx{
		ID:       id,
		PGSchema: "tenant_" + id,
	})
	return r.WithContext(ctx)
}

// withAuth adds a Bearer token header and pre-populates the JWT claims cache
// so the cache middleware's auth guard lets the request through to cache logic.
func withAuth(r *http.Request) *http.Request {
	auth.SetCachedClaims("", testToken, &auth.Claims{Role: "super_admin", TenantID: "acme"})
	r.Header.Set("Authorization", "Bearer "+testToken)
	return r
}

// echoJSON returns an HTTP handler that responds with a JSON body at the given status.
func echoJSON(body string, code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body)) //nolint:errcheck
	}
}

func TestCacheMiss(t *testing.T) {
	rc := New(5 * time.Second)
	handler := rc.Middleware(echoJSON(`{"data":[]}`, http.StatusOK))

	req := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("X-Cache") != "" {
		t.Error("X-Cache must not be set on a miss")
	}
}

func TestCacheHit(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		return withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme"))
	}

	// First request — miss
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, makeReq())

	// Second request — should hit cache
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, makeReq())

	if calls != 1 {
		t.Errorf("inner handler should be called once, got %d", calls)
	}
	if rec2.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT on second request")
	}
	if rec2.Body.String() != `{"data":[]}` {
		t.Errorf("body mismatch: %q", rec2.Body.String())
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	rc := New(50 * time.Millisecond) // very short TTL
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		return withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme")
	}

	handler.ServeHTTP(httptest.NewRecorder(), makeReq()) // miss, caches
	time.Sleep(100 * time.Millisecond)                   // let TTL expire
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq()) // should miss again after expiry

	if calls != 2 {
		t.Errorf("expected 2 handler calls after TTL expiry, got %d", calls)
	}
	if rec.Header().Get("X-Cache") == "HIT" {
		t.Error("X-Cache must not be HIT after TTL expiry")
	}
}

func TestCacheNoCacheHeader(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme")
		r.Header.Set("Cache-Control", "no-cache")
		return r
	}

	handler.ServeHTTP(httptest.NewRecorder(), makeReq())
	handler.ServeHTTP(httptest.NewRecorder(), makeReq())

	if calls != 2 {
		t.Errorf("Cache-Control: no-cache must bypass cache; expected 2 calls, got %d", calls)
	}
}

func TestCacheIfNoneMatchBypass(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme")
		r.Header.Set("If-None-Match", `"abc123"`)
		return r
	}

	handler.ServeHTTP(httptest.NewRecorder(), makeReq())
	handler.ServeHTTP(httptest.NewRecorder(), makeReq())

	if calls != 2 {
		t.Errorf("If-None-Match must bypass cache; expected 2 calls, got %d", calls)
	}
}

func TestCacheNonGETNotCached(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"x"}`)) //nolint:errcheck
	}))

	for i := 0; i < 3; i++ {
		req := withTenant(httptest.NewRequest(http.MethodPost, "/api/guides", nil), "acme")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	if calls != 3 {
		t.Errorf("POST must not be cached; expected 3 calls, got %d", calls)
	}
}

func TestCacheNon200NotCached(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad"}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		return withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme")
	}

	handler.ServeHTTP(httptest.NewRecorder(), makeReq())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makeReq())

	if calls != 2 {
		t.Errorf("non-200 responses must not be cached; expected 2 calls, got %d", calls)
	}
	if rec.Header().Get("X-Cache") == "HIT" {
		t.Error("X-Cache must not be HIT for a non-200 response")
	}
}

// TestCacheStampedeSingleflight verifies that a burst of concurrent requests
// for the same (tenant, key) arriving on an expired/empty cache collapses into
// exactly ONE backend call. Without singleflight all N goroutines would hit
// Postgres simultaneously (the stampede that produced a 3.2s uncached p95).
func TestCacheStampedeSingleflight(t *testing.T) {
	const token = "test-token-stampede"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)

	var backendHits int64
	started := make(chan struct{}) // closed by the (single) leader when it enters
	release := make(chan struct{}) // unblocks the leader once all waiters parked
	var once sync.Once

	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&backendHits, 1)
		once.Do(func() { close(started) })
		<-release // hold the flight open so concurrent callers must wait on it
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[1,2,3]}`)) //nolint:errcheck
	}))

	const n = 10
	var wg sync.WaitGroup
	recs := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		recs[i] = httptest.NewRecorder()
		go func(idx int) {
			defer wg.Done()
			r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?filter[status]=delivered", nil), "acme")
			r.Header.Set("Authorization", "Bearer "+token)
			handler.ServeHTTP(recs[idx], r)
		}(i)
	}

	<-started                         // leader is inside the handler
	time.Sleep(50 * time.Millisecond) // give the other 9 time to park in singleflight
	close(release)                    // let the leader finish
	wg.Wait()

	if got := atomic.LoadInt64(&backendHits); got != 1 {
		t.Fatalf("expected exactly 1 Postgres hit under stampede, got %d", got)
	}

	// Every caller must have received the shared 200 response intact.
	for i, rec := range recs {
		if rec.Code != http.StatusOK {
			t.Errorf("caller %d: expected 200, got %d", i, rec.Code)
		}
		if rec.Body.String() != `{"data":[1,2,3]}` {
			t.Errorf("caller %d: body mismatch: %q", i, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("caller %d: Content-Type mismatch: %q", i, ct)
		}
	}

	// And the result must now be cached: a follow-up request is a HIT with no new backend call.
	rec := httptest.NewRecorder()
	r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?filter[status]=delivered", nil), "acme")
	r.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(rec, r)
	if rec.Header().Get("X-Cache") != "HIT" {
		t.Errorf("post-stampede request should be a cache HIT, got X-Cache=%q", rec.Header().Get("X-Cache"))
	}
	if got := atomic.LoadInt64(&backendHits); got != 1 {
		t.Errorf("cached follow-up must not hit backend; total hits=%d", got)
	}
}

func TestCacheTenantIsolation(t *testing.T) {
	const tokenAcme = "test-token-acme"
	const tokenBeta = "test-token-beta"
	auth.SetCachedClaims("", tokenAcme, &auth.Claims{Role: "super_admin", TenantID: "acme"})
	auth.SetCachedClaims("", tokenBeta, &auth.Claims{Role: "super_admin", TenantID: "beta"})

	makeReq := func(tenantID, token string) *http.Request {
		r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), tenantID)
		r.Header.Set("Authorization", "Bearer "+token)
		return r
	}

	rc := New(5 * time.Second)
	calls := map[string]int{}
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		calls[tc.ID]++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tenant":"` + tc.ID + `"}`)) //nolint:errcheck
	}))

	// Prime both tenants' caches
	handler.ServeHTTP(httptest.NewRecorder(), makeReq("acme", tokenAcme))
	handler.ServeHTTP(httptest.NewRecorder(), makeReq("beta", tokenBeta))

	// Second requests — should each hit their own cache
	recA := httptest.NewRecorder()
	recB := httptest.NewRecorder()
	handler.ServeHTTP(recA, makeReq("acme", tokenAcme))
	handler.ServeHTTP(recB, makeReq("beta", tokenBeta))

	if calls["acme"] != 1 || calls["beta"] != 1 {
		t.Errorf("each tenant should hit handler once; got acme=%d beta=%d", calls["acme"], calls["beta"])
	}

	// Responses must contain correct tenant data
	if recA.Body.String() != `{"tenant":"acme"}` {
		t.Errorf("acme got wrong body: %q", recA.Body.String())
	}
	if recB.Body.String() != `{"tenant":"beta"}` {
		t.Errorf("beta got wrong body: %q", recB.Body.String())
	}
}

func TestCacheInvalidate(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	makeReq := func() *http.Request {
		return withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides", nil), "acme"))
	}

	handler.ServeHTTP(httptest.NewRecorder(), makeReq()) // miss, populates cache
	rc.Invalidate("acme")
	handler.ServeHTTP(httptest.NewRecorder(), makeReq()) // must miss again after invalidation

	if calls != 2 {
		t.Errorf("after Invalidate, next request must hit handler; expected 2, got %d", calls)
	}
}

func TestCacheMaxPerTenant(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	// Fill up to the limit with 1000 distinct URLs.
	for i := 0; i < maxPerTenant; i++ {
		req := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?page="+strconv.Itoa(i), nil), "acme"))
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	callsAtLimit := calls

	// One more unique URL — should be dropped (over limit)
	overReq := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?overflow=true", nil), "acme"))
	handler.ServeHTTP(httptest.NewRecorder(), overReq)
	// Re-request the overflow key — should hit handler again (was not cached)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?overflow=true", nil), "acme")))

	if rec.Header().Get("X-Cache") == "HIT" {
		t.Error("overflow entry must not be cached when bucket is full")
	}
	_ = callsAtLimit
}

func TestCacheQueryStringDistinct(t *testing.T) {
	rc := New(5 * time.Second)
	calls := 0
	handler := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`)) //nolint:errcheck
	}))

	req1 := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?page=1", nil), "acme"))
	req2 := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?page=2", nil), "acme"))

	handler.ServeHTTP(httptest.NewRecorder(), req1)
	handler.ServeHTTP(httptest.NewRecorder(), req2)

	if calls != 2 {
		t.Errorf("different query strings must use different cache keys; expected 2 calls, got %d", calls)
	}
}

// TestInvalidateDropsInFlightStore — the invalidate/refresh race
// (PUBLIC-SURFACE-S1 Part F): a store whose backend ran BEFORE an Invalidate
// must not land AFTER it (it would resurrect pre-write data until TTL —
// observed in the field as a deleted row still listed while the uncached
// aggregate was already correct). The epoch captured before the backend runs
// makes the late store a no-op; a store with the CURRENT epoch still lands.
func TestInvalidateDropsInFlightStore(t *testing.T) {
	rc := New(time.Minute)
	item := rc.buildItem(200, http.Header{}, []byte(`{"data":["stale"]}`))

	asOf := rc.epoch("t1")
	rc.Invalidate("t1") // the write's invalidation lands mid-flight
	rc.set("t1", "/api/tasks", item, asOf)
	if got := rc.get("t1", "/api/tasks"); got != nil {
		t.Fatal("a store that raced an Invalidate must be dropped, not resurrect pre-write data")
	}

	// The non-racing store (current epoch) still works.
	rc.set("t1", "/api/tasks", item, rc.epoch("t1"))
	if got := rc.get("t1", "/api/tasks"); got == nil {
		t.Fatal("a store with the current epoch must land")
	}
}

// APP-PODER-S1: the handler's Server-Timing survives the singleflight miss
// path (the item is rendered from memory) and a HIT says so instead of
// replaying a duration no query paid on this request.
func TestServerTimingReplayedOnMissAndMarkedOnHit(t *testing.T) {
	rc := New(time.Minute)
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Server-Timing", "query;dur=1.50, app;dur=2.00")
	item := rc.buildItem(http.StatusOK, h, []byte(`{"ok":true}`))
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)

	miss := httptest.NewRecorder()
	writeItem(miss, req, item, false)
	if got := miss.Header().Get("Server-Timing"); got != "query;dur=1.50, app;dur=2.00" {
		t.Fatalf("miss must replay the handler's Server-Timing, got %q", got)
	}
	hit := httptest.NewRecorder()
	writeItem(hit, req, item, true)
	if got := hit.Header().Get("Server-Timing"); got != `cache;desc="hit"` {
		t.Fatalf("hit must say cache, got %q", got)
	}
}
