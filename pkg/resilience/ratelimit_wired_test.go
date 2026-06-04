package resilience_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/tenant"
)

func withTenantID(r *http.Request, id string) *http.Request {
	ctx := tenant.WithContext(r.Context(), &tenant.TenantCtx{ID: id, PGSchema: "tenant_" + id})
	return r.WithContext(ctx)
}

// A configured limiter (10 RPS / 10 burst) lets through the burst then 429s the
// next request for that tenant, while a different tenant is unaffected.
func TestRateLimit_PerTenantIsolation(t *testing.T) {
	tl := resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: 10, Burst: 10})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := resilience.RateLimit(tl)(ok)

	call := func(tenantID string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, withTenantID(httptest.NewRequest(http.MethodGet, "/api/guides", nil), tenantID))
		return rec.Code
	}

	// Drain tenant "test"'s 10-token bucket — all allowed.
	for i := 0; i < 10; i++ {
		if code := call("test"); code != http.StatusOK {
			t.Fatalf("request %d for tenant test should be 200, got %d", i+1, code)
		}
	}
	// 11th request for "test" must be rate limited.
	if code := call("test"); code != http.StatusTooManyRequests {
		t.Errorf("11th request for tenant test should be 429, got %d", code)
	}

	// A different tenant has its own bucket → still served.
	if code := call("test2"); code != http.StatusOK {
		t.Errorf("tenant test2 must be unaffected by tenant test's limit, got %d", code)
	}
}

// Requests without a tenant (health checks) pass through untouched.
func TestRateLimit_NoTenantPassesThrough(t *testing.T) {
	tl := resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: 1, Burst: 1})
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := resilience.RateLimit(tl)(ok)

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil)) // no tenant ctx
		if rec.Code != http.StatusOK {
			t.Fatalf("no-tenant request must pass through, got %d", rec.Code)
		}
	}
}
