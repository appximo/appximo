package resilience

import (
	"errors"
	"testing"
)

// TestBreaker_StateChangeIsRecorded pins AUTO-4's closure: a breaker that opens
// is no longer mute — the transition lands in the registry (and, wired from it,
// in appximo_breaker_state and the structured log).
func TestBreaker_StateChangeIsRecorded(t *testing.T) {
	cb := NewQueryBreaker("test-breaker-auto4")

	// 10 failures in one window: 100% ≥ 60% at ≥10 requests → the breaker opens.
	boom := errors.New("connection refused")
	for i := 0; i < 10; i++ {
		_, _ = cb.Execute(func() (any, error) { return nil, boom })
	}
	if !IsOpen(cb) {
		t.Fatal("breaker should be open after 10 straight failures")
	}

	var st *BreakerStatus
	for _, s := range BreakerSnapshot() {
		if s.Name == "test-breaker-auto4" {
			cp := s
			st = &cp
		}
	}
	if st == nil {
		t.Fatal("open breaker missing from BreakerSnapshot — the state change was not recorded")
	}
	if st.State != "open" || st.Opens != 1 || st.LastOpened.IsZero() {
		t.Fatalf("recorded state wrong: %+v", st)
	}
	if stateNum(st.State) != 2 {
		t.Fatalf("gauge value for open = %v, want 2", stateNum(st.State))
	}
}
