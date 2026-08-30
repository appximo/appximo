// Package resilience provides circuit breaker, rate limiting, and query timeout utilities.
package resilience

import (
	"time"

	"github.com/sony/gobreaker"
)

// NewQueryBreaker returns a circuit breaker tuned for PostgreSQL query protection.
//
// Opens when EITHER ≥10 requests in the current window have a ≥60% failure
// rate (sustained partial degradation) OR 20 failures arrive CONSECUTIVELY
// with no success between (a hard outage — ENG-59, CAOS-S1).
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
		// Interval bounds the ledger the trip ratio is computed over (ENG-59,
		// CAOS-S1). gobreaker's default Interval=0 NEVER clears Counts in the
		// closed state, so after a day of normal traffic the 60 % failure
		// ratio was UNREACHABLE — a black-holed database (link down, packets
		// dropped) produced slow 5 s failures that could never outnumber the
		// lifetime's successes, and every request waited the full query
		// deadline for its 503 (measured: p50 5.00 s per failure over a 30 s
		// outage; a REFUSED connection only tripped it on boxes with a young
		// count ledger). 10 s = 2× the query timeout: even when every failure
		// takes the full deadline, one window holds enough of them to trip,
		// and a healthy window's counts never carry stale history. Under
		// legitimate saturation this trips nothing: with admission control
		// bounding in-flight (ENG-52), timeouts measured ZERO at the tipping
		// point — a 60 % failure ratio over 10 s means the database is not
		// serving, not that it is busy.
		Interval: 10 * time.Second,
		// TWO trip conditions. The RATIO rule (below) catches SUSTAINED PARTIAL
		// failure. But a black-holed database (link down / packets dropped, not
		// refused) defeats it — measured p50 5.00 s per failure even windowed
		// (CAOS-S1): every request hangs the full query deadline, so at 10 rps
		// ~50 are IN FLIGHT at once, Requests (counted when a call STARTS)
		// races ahead while TotalFailures (counted 5 s later on completion)
		// lags, and completed-failures / started-requests never crosses 0.6.
		// Those completions are an unbroken run of failures with NO success
		// between them, so ConsecutiveFailures climbs monotonically — the
		// signal that says "the database is not answering AT ALL". It trips in
		// ~20 failures (~2 s), after which every new request gets an immediate
		// 503 instead of waiting 5 s. 20 (not 10) leaves headroom so a brief
		// pool-full blip under legitimate load — where waiters RESOLVE into
		// successes (CENTINELA-C-S1: 121 waiters/tick served fine) and reset
		// the counter — never trips it; only a real outage produces 20 in a row.
		ReadyToTrip: func(c gobreaker.Counts) bool {
			if c.ConsecutiveFailures >= 20 {
				return true
			}
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
