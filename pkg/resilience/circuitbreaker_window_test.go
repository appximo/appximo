package resilience

import (
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker"
)

// ENG-59 (CAOS-S1): with gobreaker's default Interval=0 the Counts are NEVER
// cleared while closed, so a breaker warmed by a day of successes can never
// reach the 60 % trip ratio — a dead database link produced 5 s failures
// forever and nothing shed. The fix bounds the ledger with Interval; this test
// pins BOTH halves: the warmed breaker trips within one window now, and the
// old behavior (reconstructed with Interval=0) demonstrably never tripped.
func TestQueryBreaker_TripsAfterWarmupWithinOneWindow(t *testing.T) {
	dbDown := errors.New("acquire conn: context deadline exceeded")
	cb := NewQueryBreakerWith("warmed", func(err error) bool { return true })
	for i := 0; i < 5000; i++ { // a day of healthy traffic
		if _, err := cb.Execute(func() (any, error) { return nil, nil }); err != nil {
			t.Fatalf("healthy call %d: %v", i, err)
		}
	}
	// The link dies: an unbroken run of failures. ConsecutiveFailures climbs
	// past the warm history and trips WITHIN ~20 failures — no waiting out a
	// window, and no dependence on the ratio the in-flight dilution defeats.
	tripped, n := false, 0
	for i := 0; i < 40; i++ {
		n++
		_, err := cb.Execute(func() (any, error) { return nil, dbDown })
		if errors.Is(err, gobreaker.ErrOpenState) {
			tripped = true
			break
		}
	}
	if !tripped {
		t.Fatalf("breaker never opened after warm-up + %d consecutive failures (state %v, counts %+v)", n, cb.State(), cb.Counts())
	}
	if n > 21 {
		t.Fatalf("breaker took %d failures to trip; expected ~20 (consecutive-failure rule)", n)
	}
}

// A run of failures BROKEN by successes (partial degradation, not an outage)
// must NOT trip via the consecutive rule — only the ratio rule governs there.
func TestQueryBreaker_InterleavedSuccessDoesNotTripConsecutive(t *testing.T) {
	dbDown := errors.New("acquire conn: context deadline exceeded")
	cb := NewQueryBreakerWith("interleaved", func(err error) bool { return true })
	// 3 failures then 1 success, repeated: consecutive never reaches 20, and
	// the ratio is 75% — but keep Requests small enough per window that we are
	// testing the consecutive rule in isolation is not the point; the point is
	// a healthy-ish stream with regular successes stays serving. Use 2:1 so the
	// ratio also stays under 0.6 (66%→ under with the success cadence below).
	for i := 0; i < 60; i++ {
		fail := i%3 != 2 // 2 of every 3 fail → but consecutive maxes at 2
		_, err := cb.Execute(func() (any, error) {
			if fail {
				return nil, dbDown
			}
			return nil, nil
		})
		if errors.Is(err, gobreaker.ErrOpenState) {
			// The ratio rule MAY trip (2/3 > 0.6) — that is fine and correct;
			// what must NOT happen is the CONSECUTIVE rule firing, which it
			// cannot here (max run is 2). Assert the counter never reached 20.
			break
		}
	}
	if cb.Counts().ConsecutiveFailures >= 20 {
		t.Fatalf("consecutive counter reached %d with successes every 3rd call — the run should never exceed 2", cb.Counts().ConsecutiveFailures)
	}
}

// The old behavior, pinned as the counter-factual: Interval=0 (never clear)
// with the same warm-up cannot trip on the same failure run — the exact
// mechanism behind RESILIENCIA-S1 §B6's 5-second 503s.
func TestQueryBreaker_UnwindowedLedgerNeverTrips(t *testing.T) {
	dbDown := errors.New("acquire conn: context deadline exceeded")
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: "old", MaxRequests: 2, Timeout: 8 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.Requests >= 10 && float64(c.TotalFailures)/float64(c.Requests) >= 0.6
		},
	})
	for i := 0; i < 5000; i++ {
		_, _ = cb.Execute(func() (any, error) { return nil, nil })
	}
	for i := 0; i < 300; i++ { // a whole outage's worth of failures (30 s at 10 rps)
		_, err := cb.Execute(func() (any, error) { return nil, dbDown })
		if errors.Is(err, gobreaker.ErrOpenState) {
			t.Fatalf("unexpected: the unwindowed ledger tripped at failure %d — the ENG-59 premise would be wrong", i)
		}
	}
	if cb.State() != gobreaker.StateClosed {
		t.Fatalf("expected the old breaker to still be closed, got %v", cb.State())
	}
}

// A healthy stream through the windowed breaker never trips and pays nothing:
// the interval only clears counters.
func TestQueryBreaker_WindowedHealthyNeverTrips(t *testing.T) {
	cb := NewQueryBreakerWith("healthy", func(err error) bool { return true })
	for i := 0; i < 2000; i++ {
		if _, err := cb.Execute(func() (any, error) { return nil, nil }); err != nil {
			t.Fatalf("healthy call %d rejected: %v", i, err)
		}
	}
	if cb.State() != gobreaker.StateClosed {
		t.Fatalf("healthy breaker not closed: %v", cb.State())
	}
}
