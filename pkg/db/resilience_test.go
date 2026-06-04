package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sony/gobreaker"

	"github.com/miguelangel/appitools/pkg/resilience"
)

func TestClassify_MapsUnavailableErrors(t *testing.T) {
	if classify(nil) != nil {
		t.Error("nil must stay nil")
	}
	for _, err := range []error{context.DeadlineExceeded, gobreaker.ErrOpenState, gobreaker.ErrTooManyRequests} {
		if !IsUnavailable(classify(err)) {
			t.Errorf("%v must classify as unavailable", err)
		}
	}
	if IsUnavailable(classify(errors.New("syntax error at or near SELECT"))) {
		t.Error("an ordinary query error must NOT be reported as unavailable (it is a real 500)")
	}
}

// With the breaker, a backend that keeps failing trips the circuit; further
// calls then return ErrOpenState immediately instead of hitting the (hung)
// database. This is what stops a Postgres outage from hanging the whole pool.
func TestBreaker_OpensAndFailsFast(t *testing.T) {
	tdb := &TenantDB{breaker: resilience.NewQueryBreaker("test")}
	failing := func() (any, error) { return nil, errors.New("db down") }

	// Drive enough failures to trip the breaker (>=10 requests, >=60% failures).
	for i := 0; i < 12; i++ {
		_, _ = tdb.exec(failing)
	}

	start := time.Now()
	_, err := tdb.exec(failing)
	elapsed := time.Since(start)

	if !errors.Is(err, gobreaker.ErrOpenState) {
		t.Fatalf("expected open circuit, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("open circuit must fail fast (<100ms), took %v", elapsed)
	}
	if !IsUnavailable(classify(err)) {
		t.Error("open-circuit error must map to ErrUnavailable → 503")
	}
}

// Without a breaker, exec is a pass-through (used by tests that construct a bare
// TenantDB); confirm it does not panic and returns the inner result.
func TestExec_NilBreakerPassthrough(t *testing.T) {
	tdb := &TenantDB{}
	res, err := tdb.exec(func() (any, error) { return 42, nil })
	if err != nil || res.(int) != 42 {
		t.Errorf("pass-through failed: res=%v err=%v", res, err)
	}
}

// TestWiring_TenantDBHasBreaker is a CI guard: it fails if NewTenantDB stops
// installing the circuit breaker (FIX 2). A refactor that returns a bare
// TenantDB would silently reintroduce the "DB outage hangs the pool" bug.
func TestWiring_TenantDBHasBreaker(t *testing.T) {
	if NewTenantDB(nil).breaker == nil {
		t.Fatal("WIRING REGRESSION: NewTenantDB no longer installs a circuit breaker (FIX 2 disconnected)")
	}
}
