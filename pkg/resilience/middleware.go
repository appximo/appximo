package resilience

import (
	"encoding/json"
	"net/http"

	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/sony/gobreaker"
)

// RateLimitMiddleware returns a chi-compatible middleware that enforces per-tenant
// rate limits. Tenants not found in context are skipped (e.g., health checks).
// Returns 429 Too Many Requests when the token bucket is empty.
func RateLimitMiddleware(tl *TenantLimiter, tier Tier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tc := tenant.FromCtx(r.Context())
			if tc == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !tl.Allow(tc.ID, tier) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				json.NewEncoder(w).Encode(map[string]string{"error": "rate limit exceeded"}) //nolint:errcheck
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// CircuitBreakerMiddleware wraps a handler with a gobreaker circuit breaker.
// When the circuit is open it returns 503 with Retry-After: 8.
// Failures are counted when the handler writes a 5xx response.
func CircuitBreakerMiddleware(cb *gobreaker.CircuitBreaker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsOpen(cb) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "8")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"error": "service temporarily unavailable"}) //nolint:errcheck
				return
			}

			// Wrap to capture the response status code.
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			cb.Execute(func() (any, error) { //nolint:errcheck
				next.ServeHTTP(rec, r)
				if rec.status >= 500 {
					return nil, errUpstream
				}
				return nil, nil
			})
		})
	}
}

var errUpstream = http.ErrHandlerTimeout // sentinel — any non-nil error signals failure

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}
