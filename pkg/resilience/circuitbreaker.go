// Package resilience provides circuit breaker, rate limiting, and query timeout utilities.
package resilience

import (
	"time"

	"github.com/sony/gobreaker"
)

// NewQueryBreaker returns a circuit breaker tuned for PostgreSQL query protection.
//
// Opens when ≥10 requests have a ≥60% failure rate.
// Transitions open→half-open after 8 s; allows 2 probe requests in half-open.
func NewQueryBreaker(name string) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 2,
		Timeout:     8 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.Requests >= 10 &&
				float64(c.TotalFailures)/float64(c.Requests) >= 0.6
		},
	})
}

// IsOpen is the hot-path state check — an O(1) mutex read with zero allocations.
// Call this before executing a query to decide whether to serve from cache or return 503.
func IsOpen(cb *gobreaker.CircuitBreaker) bool {
	return cb.State() == gobreaker.StateOpen
}
