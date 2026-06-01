package cache

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/tenant"
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
	auth.SetCachedClaims(testToken, &auth.Claims{Role: "super_admin", TenantID: "acme"})
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
	time.Sleep(100 * time.Millisecond)                    // let TTL expire
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

func TestCacheTenantIsolation(t *testing.T) {
	const tokenAcme = "test-token-acme"
	const tokenBeta = "test-token-beta"
	auth.SetCachedClaims(tokenAcme, &auth.Claims{Role: "super_admin", TenantID: "acme"})
	auth.SetCachedClaims(tokenBeta, &auth.Claims{Role: "super_admin", TenantID: "beta"})

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
