package resilience_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/sony/gobreaker"
)

// --- Circuit Breaker ---

func TestNewQueryBreaker_ClosedByDefault(t *testing.T) {
	cb := resilience.NewQueryBreaker("test")
	if resilience.IsOpen(cb) {
		t.Fatal("new circuit breaker must start closed")
	}
}

func TestQueryBreaker_OpensAfterThreshold(t *testing.T) {
	cb := resilience.NewQueryBreaker("open-test")
	// Trigger 10 failures (100% failure rate ≥ 60%) to open the breaker.
	for i := 0; i < 10; i++ {
		cb.Execute(func() (any, error) { //nolint:errcheck
			return nil, errors.New("db error")
		})
	}
	if !resilience.IsOpen(cb) {
		t.Fatal("circuit breaker should be open after 10 consecutive failures")
	}
}

func TestQueryBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := resilience.NewQueryBreaker("closed-test")
	called := false
	_, err := cb.Execute(func() (any, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("execute must call fn when circuit is closed")
	}
}

// BenchmarkCBHotPath measures the overhead of the IsOpen state check.
// Hot path = mutex-guarded state read. Must be <500 ns/op, 0 allocs.
func BenchmarkCBHotPath(b *testing.B) {
	cb := resilience.NewQueryBreaker("bench")
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = resilience.IsOpen(cb)
	}
}

// --- Rate Limiter ---

func TestTenantLimiter_AllowsUnderLimit(t *testing.T) {
	tl := resilience.NewTenantLimiter()
	if !tl.Allow("tenant-a", resilience.TierFree) {
		t.Fatal("first request must always be allowed")
	}
}

func TestTenantLimiter_BlocksWhenExhausted(t *testing.T) {
	tl := resilience.NewTenantLimiter()
	// Exhaust the entire burst budget (100 tokens) for TierFree.
	for i := 0; i < 100; i++ {
		tl.Allow("tenant-burst", resilience.TierFree)
	}
	if tl.Allow("tenant-burst", resilience.TierFree) {
		t.Fatal("request beyond burst capacity must be denied")
	}
}

func TestTenantLimiter_IsolatedPerTenant(t *testing.T) {
	tl := resilience.NewTenantLimiter()
	for i := 0; i < 100; i++ {
		tl.Allow("tenant-a", resilience.TierFree)
	}
	if !tl.Allow("tenant-b", resilience.TierFree) {
		t.Fatal("tenant-b must not be affected by tenant-a exhaustion")
	}
}

// --- Query Timeout ---

func TestWithQueryTimeout_SuccessOnFirstAttempt(t *testing.T) {
	called := 0
	err := resilience.WithQueryTimeout(context.Background(), func(_ context.Context) error {
		called++
		return nil
	})
	if err != nil || called != 1 {
		t.Fatalf("err=%v called=%d, want nil/1", err, called)
	}
}

func TestWithQueryTimeout_RetriesOnDeadlineExceeded(t *testing.T) {
	called := 0
	err := resilience.WithQueryTimeout(context.Background(), func(_ context.Context) error {
		called++
		if called == 1 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if called != 2 {
		t.Fatalf("expected 2 calls, got %d", called)
	}
}

func TestWithQueryTimeout_NoRetryOnNonTimeout(t *testing.T) {
	called := 0
	sentinelErr := errors.New("non-timeout error")
	err := resilience.WithQueryTimeout(context.Background(), func(_ context.Context) error {
		called++
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if called != 1 {
		t.Fatalf("expected 1 call (no retry), got %d", called)
	}
}

// --- RateLimit Middleware ---

// withTenant wraps the handler with TenantMiddleware so the rate-limiter sees
// a tenant in the request context. Host header format: "<tenantID>.example.com".
func withTenant(h http.Handler) http.Handler {
	return tenant.TenantMiddleware(h)
}

func TestRateLimitMiddleware_Returns429WhenExhausted(t *testing.T) {
	tl := resilience.NewTenantLimiter()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withTenant(resilience.RateLimitMiddleware(tl, resilience.TierFree)(inner))

	// Exhaust the free-tier burst budget (100 tokens) for "testcorp".
	for i := 0; i < 100; i++ {
		tl.Allow("testcorp", resilience.TierFree)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	req.Host = "testcorp.example.com" // TenantMiddleware extracts "testcorp"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("want 429, got %d", w.Code)
	}
}

func TestRateLimitMiddleware_PassesThroughWithoutTenant(t *testing.T) {
	tl := resilience.NewTenantLimiter()
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := resilience.RateLimitMiddleware(tl, resilience.TierFree)(inner)

	// No Host header → no tenant in context → middleware must pass through.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if !called {
		t.Error("expected handler to be called when no tenant in context")
	}
}

// --- CircuitBreaker Middleware ---

func TestCircuitBreakerMiddleware_Returns503WhenOpen(t *testing.T) {
	cb := resilience.NewQueryBreaker("mw-test")
	for i := 0; i < 10; i++ {
		cb.Execute(func() (any, error) { return nil, errors.New("err") }) //nolint:errcheck
	}

	handler := resilience.CircuitBreakerMiddleware(cb)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503 when circuit open, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") != "8" {
		t.Errorf("want Retry-After: 8, got %q", w.Header().Get("Retry-After"))
	}
}

// BenchmarkRateLimiterAllow measures per-request overhead of Allow().
func BenchmarkRateLimiterAllow(b *testing.B) {
	tl := resilience.NewTenantLimiter()
	tl.Allow("bench-tenant", resilience.TierPro) // warm up
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tl.Allow("bench-tenant", resilience.TierPro)
	}
}

// Ensure gobreaker import is used (type assertion in tests).
var _ = gobreaker.StateOpen
