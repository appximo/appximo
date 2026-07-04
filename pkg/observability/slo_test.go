package observability

import (
	"context"
	"math"
	"sync"
	"testing"
)

// mockAlerter records every alert it receives. Shared by slo_test and alerter_test.
type mockAlerter struct {
	mu     sync.Mutex
	alerts []Alert
}

func (m *mockAlerter) Send(_ context.Context, a Alert) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alerts = append(m.alerts, a)
	return nil
}

func (m *mockAlerter) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.alerts)
}

func (m *mockAlerter) last() Alert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alerts[len(m.alerts)-1]
}

// seed appends n identical minute buckets to tenant tid.
func seed(e *SLOEngine, tid string, n int, count, errs uint32) {
	for i := 0; i < n; i++ {
		e.appendBucket(tid, MinuteBucket{Count: count, Errors: errs})
	}
}

func newTestEngine(a Alerter) *SLOEngine {
	return NewSLOEngine(NewRings(), NewTenantHistogram(), a)
}

// 1) burn rate computed correctly from known errors.
func TestSLO_BurnRateComputed(t *testing.T) {
	e := newTestEngine(&mockAlerter{})
	// 5 buckets × (1000 reqs, 5 errors) → ratio 0.005 over the 5m window.
	// budget = 1-0.999 = 0.001 → burn = 0.005/0.001 = 5x.
	// 5x < 6x warning < 14.4x critical → status stays ok.
	seed(e, "t", 5, 1000, 5)

	snap := e.Snapshot("t")
	if math.Abs(snap.ErrorRatio5m-0.005) > 1e-9 {
		t.Errorf("error_ratio_5m: want 0.005, got %v", snap.ErrorRatio5m)
	}
	if math.Abs(snap.BurnRate5m-5.0) > 0.05 {
		t.Errorf("burn_rate_5m: want ~5.0, got %v", snap.BurnRate5m)
	}
	if snap.Status != statusOK {
		t.Errorf("status: want ok (5x < 6x warn), got %q", snap.Status)
	}
}

// 2) critical fires when burn > 14.4x in BOTH 5m and 1h windows.
func TestSLO_CriticalFires(t *testing.T) {
	mock := &mockAlerter{}
	e := newTestEngine(mock)
	// 60 buckets × (100 reqs, 2 errors) → ratio 0.02 → burn 20x in 5m and 1h.
	seed(e, "t", 60, 100, 2)

	if got := e.Snapshot("t").Status; got != statusCritical {
		t.Fatalf("status: want critical, got %q", got)
	}
	e.evaluateAndAlert(context.Background(), "t")
	if mock.count() != 1 {
		t.Fatalf("want 1 alert, got %d", mock.count())
	}
	if a := mock.last(); a.Level != LevelCritical || a.TenantID != "t" {
		t.Errorf("alert: want critical/t, got %s/%s", a.Level, a.TenantID)
	}
}

// 3) warning fires when burn > 6x in BOTH 30m and 6h windows (but below critical).
func TestSLO_WarningFires(t *testing.T) {
	mock := &mockAlerter{}
	e := newTestEngine(mock)
	// 360 buckets × (100 reqs, 1 error) → ratio 0.01 → burn 10x.
	// 10x < 14.4x → not critical; 10x > 6x in 30m and 6h → warning.
	seed(e, "t", 360, 100, 1)

	if got := e.Snapshot("t").Status; got != statusWarning {
		t.Fatalf("status: want warning, got %q", got)
	}
	e.evaluateAndAlert(context.Background(), "t")
	if mock.count() != 1 {
		t.Fatalf("want 1 alert, got %d", mock.count())
	}
	if a := mock.last(); a.Level != LevelWarning {
		t.Errorf("alert level: want warning, got %s", a.Level)
	}
}

// 4) cooldown: a second alert of the same (tenant, level) within the window is suppressed.
func TestSLO_CooldownSuppressesSecond(t *testing.T) {
	mock := &mockAlerter{}
	// Default 15min cooldown — both calls happen immediately, so the 2nd is suppressed.
	e := newTestEngine(mock)
	seed(e, "t", 60, 100, 2) // critical

	e.evaluateAndAlert(context.Background(), "t")
	e.evaluateAndAlert(context.Background(), "t")
	if mock.count() != 1 {
		t.Fatalf("cooldown should suppress the 2nd alert; want 1, got %d", mock.count())
	}
}

// 5) no errors → status ok, no alert.
func TestSLO_NoErrorsNoAlert(t *testing.T) {
	mock := &mockAlerter{}
	e := newTestEngine(mock)
	seed(e, "t", 60, 100, 0)

	if got := e.Snapshot("t").Status; got != statusOK {
		t.Errorf("status: want ok, got %q", got)
	}
	e.evaluateAndAlert(context.Background(), "t")
	if mock.count() != 0 {
		t.Errorf("want 0 alerts, got %d", mock.count())
	}
}

// computeBucket: only samples within the last minute count; >=500 or slow → error.
func TestSLO_ComputeBucketWindowAndErrorRule(t *testing.T) {
	now := int64(100 * usPerMinute)
	cfg := DefaultSLOConfig() // LatencySLOms = 100 → 100_000us threshold
	samples := []Sample{
		{Start: now - 1_000_000, DurUS: 5000, Status: 200},    // in window, ok
		{Start: now - 2_000_000, DurUS: 5000, Status: 503},    // in window, error (5xx)
		{Start: now - 3_000_000, DurUS: 200_000, Status: 200}, // in window, error (slow)
		{Start: now - 120_000_000, DurUS: 5000, Status: 200},  // 2 min ago → excluded
	}
	b := computeBucket(samples, cfg, now)
	if b.Count != 3 {
		t.Errorf("count: want 3 (last-minute only), got %d", b.Count)
	}
	if b.Errors != 2 {
		t.Errorf("errors: want 2 (one 5xx + one slow), got %d", b.Errors)
	}
}
