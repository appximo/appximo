package appximo

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunSafe_RecoversPanic is the core Phase-0 guarantee (LIBRARY-HARDEN-S1):
// runSafe — the body of every Ctx.SafeGo goroutine — RECOVERS a panic instead of
// letting it propagate. If it did not, this call would crash the test binary
// (an unrecovered panic terminates the process), so the test's mere completion
// is part of the proof; the onPanic assertion confirms the hook fired.
func TestRunSafe_RecoversPanic(t *testing.T) {
	var panicHookCalled atomic.Bool
	// Called directly (not in a goroutine): a non-recovered panic would crash
	// the test process — reaching the next line proves the recover worked.
	runSafe(context.Background(), 0, "acme", "req-1",
		func() { panicHookCalled.Store(true) },
		func(ctx context.Context) { panic("boom in background goroutine") })
	if !panicHookCalled.Load() {
		t.Fatal("onPanic hook was not invoked on a recovered panic")
	}
}

// TestRunSafe_NilHookDoesNotCrash: a bare test/library build has no metric hook.
func TestRunSafe_NilHookDoesNotCrash(t *testing.T) {
	runSafe(context.Background(), 0, "acme", "r", nil,
		func(ctx context.Context) { panic("boom") })
	// reaching here means the nil hook path recovered cleanly
}

// TestRunSafe_AppliesDeadline: a positive timeout gives fn a bounded context so
// a fire-and-forget task can never leak forever.
func TestRunSafe_AppliesDeadline(t *testing.T) {
	var hadDeadline bool
	runSafe(context.Background(), 50*time.Millisecond, "t", "r", nil,
		func(ctx context.Context) { _, hadDeadline = ctx.Deadline() })
	if !hadDeadline {
		t.Fatal("expected the SafeGo context to carry a deadline")
	}
}

// TestRunSafe_ZeroTimeoutNoDeadline: a zero timeout means no artificial deadline.
func TestRunSafe_ZeroTimeoutNoDeadline(t *testing.T) {
	var hadDeadline bool
	runSafe(context.Background(), 0, "t", "r", nil,
		func(ctx context.Context) { _, hadDeadline = ctx.Deadline() })
	if hadDeadline {
		t.Fatal("expected no deadline when timeout is 0")
	}
}

// TestSafeParallel_RecoversPanic: a panicking task becomes an error, never a
// process crash (a raw errgroup would panic straight through and kill everyone).
func TestSafeParallel_RecoversPanic(t *testing.T) {
	err := SafeParallel(context.Background(), 2,
		func(ctx context.Context) error { return nil },
		func(ctx context.Context) error { panic("task blew up") })
	if err == nil {
		t.Fatal("expected an error surfaced from the recovered panic")
	}
}

// TestSafeParallel_PropagatesTaskError: the first real error is returned.
func TestSafeParallel_PropagatesTaskError(t *testing.T) {
	sentinel := errors.New("task failed")
	err := SafeParallel(context.Background(), 4,
		func(ctx context.Context) error { return nil },
		func(ctx context.Context) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// TestSafeParallel_AllTasksRun: every task executes and success returns nil.
func TestSafeParallel_AllTasksRun(t *testing.T) {
	var ran atomic.Int32
	tasks := make([]func(context.Context) error, 10)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) error { ran.Add(1); return nil }
	}
	if err := SafeParallel(context.Background(), 3, tasks...); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ran.Load() != 10 {
		t.Fatalf("expected all 10 tasks to run, got %d", ran.Load())
	}
}

// TestSafeParallel_RespectsLimit: at most `limit` tasks run at once (backpressure).
func TestSafeParallel_RespectsLimit(t *testing.T) {
	const limit = 3
	var inFlight, maxSeen atomic.Int32
	tasks := make([]func(context.Context) error, 20)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) error {
			cur := inFlight.Add(1)
			for {
				m := maxSeen.Load()
				if cur <= m || maxSeen.CompareAndSwap(m, cur) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
			return nil
		}
	}
	if err := SafeParallel(context.Background(), limit, tasks...); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := maxSeen.Load(); got > limit {
		t.Fatalf("concurrency exceeded the limit: saw %d in flight, limit %d", got, limit)
	}
}

func TestParseMemLimit(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"512MiB", 512 << 20, false},
		{"1GiB", 1 << 30, false},
		{"2KiB", 2 << 10, false},
		{"2GB", 2_000_000_000, false},
		{"500MB", 500_000_000, false},
		{"1073741824", 1073741824, false},
		{"notanumber", 0, true},
		{"12XiB", 0, true},
	}
	for _, c := range cases {
		got, err := parseMemLimit(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseMemLimit(%q): expected error, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseMemLimit(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseMemLimit(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
