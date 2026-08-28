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
// Every non-nil error counts as a failure — the gobreaker default. Production
// callers use NewQueryBreakerWith, which decides what a failure IS.
func NewQueryBreaker(name string) *gobreaker.CircuitBreaker {
	return NewQueryBreakerWith(name, nil)
}

// NewQueryBreakerWith is NewQueryBreaker with an explicit definition of
// failure: isFailure(err) reports whether a non-nil error means the database
// could not serve the request. When nil, every error counts.
//
// WHY (ENG-49, MOTOR-TIPO-JSON-S1). The breaker exists to shed load when
// PostgreSQL is DOWN. With the default "every error is a failure", a unique
// violation, an unknown column (a plain 422), a class-22 value, a driver
// encode error — all produced by CLIENT INPUT, none an outage — were counted,
// and six 422s in a row opened the breaker: every write of the process (every
// tenant of the app) answered 503 for 8 s, renewably, to any caller with
// `create` on any resource. A statement the database REJECTED is proof the
// database is up. pkg/db passes the SAME predicate that already decides the
// 503 (timeouts, connection failures, class 08/53/57P0x), so "counted by the
// breaker" and "answered 503" can never disagree.
func NewQueryBreakerWith(name string, isFailure func(error) bool) *gobreaker.CircuitBreaker {
	st := gobreaker.Settings{
		Name:        name,
		MaxRequests: 2,
		Timeout:     8 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.Requests >= 10 &&
				float64(c.TotalFailures)/float64(c.Requests) >= 0.6
		},
	}
	if isFailure != nil {
		st.IsSuccessful = func(err error) bool { return err == nil || !isFailure(err) }
	}
	return gobreaker.NewCircuitBreaker(st)
}

// IsOpen is the hot-path state check — an O(1) mutex read with zero allocations.
// Call this before executing a query to decide whether to serve from cache or return 503.
func IsOpen(cb *gobreaker.CircuitBreaker) bool {
	return cb.State() == gobreaker.StateOpen
}
