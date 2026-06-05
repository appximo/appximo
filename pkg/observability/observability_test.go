package observability

import (
	"errors"
	"strings"
	"testing"
)

// ── TenantHistogram ──────────────────────────────────────────────────────────

func TestTenantHistogram_RecordAndSnapshot(t *testing.T) {
	th := NewTenantHistogram()

	// No data yet → nil snapshot.
	if th.Snapshot("acme") != nil {
		t.Fatal("expected nil snapshot for unknown tenant")
	}

	// Record a batch of uncached values.
	for i := int64(1); i <= 100; i++ {
		th.Record("acme", i, false)
	}

	snap := th.Snapshot("acme")
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if snap.Count != 100 {
		t.Fatalf("count: got %d want 100", snap.Count)
	}
	if snap.P50Us < 45 || snap.P50Us > 55 {
		t.Fatalf("p50=%v out of expected range [45,55]", snap.P50Us)
	}
	if snap.P99Us < 95 {
		t.Fatalf("p99=%v should be >= 95", snap.P99Us)
	}
}

func TestTenantHistogram_TenantIsolation(t *testing.T) {
	th := NewTenantHistogram()
	th.Record("a", 10, false)
	th.Record("b", 999, false)

	snapA := th.Snapshot("a")
	snapB := th.Snapshot("b")

	if snapA.P99Us > 20 {
		t.Fatalf("tenant a should have small latencies, got p99=%v", snapA.P99Us)
	}
	if snapB.P99Us < 500 {
		t.Fatalf("tenant b should have large latencies, got p99=%v", snapB.P99Us)
	}
}

func TestTenantHistogram_CachedVsUncached(t *testing.T) {
	th := NewTenantHistogram()

	// Cache hits: fast (100µs each).
	for i := 0; i < 50; i++ {
		th.Record("acme", 100, true)
	}
	// Cache misses: slow (5000µs each).
	for i := 0; i < 50; i++ {
		th.Record("acme", 5000, false)
	}

	snap := th.FullSnapshot("acme")

	if snap.Cached == nil {
		t.Fatal("expected non-nil Cached snapshot")
	}
	if snap.Uncached == nil {
		t.Fatal("expected non-nil Uncached snapshot")
	}
	if snap.Cached.Count != 50 {
		t.Fatalf("cached count: got %d want 50", snap.Cached.Count)
	}
	if snap.Uncached.Count != 50 {
		t.Fatalf("uncached count: got %d want 50", snap.Uncached.Count)
	}
	// Cached p50 should be significantly lower than uncached p50.
	if snap.Cached.P50Us >= snap.Uncached.P50Us {
		t.Fatalf("cached p50 (%v) should be < uncached p50 (%v)",
			snap.Cached.P50Us, snap.Uncached.P50Us)
	}
}

func TestTenantHistogram_FullSnapshotNilWhenEmpty(t *testing.T) {
	th := NewTenantHistogram()
	snap := th.FullSnapshot("ghost")
	if snap.Cached != nil || snap.Uncached != nil {
		t.Fatal("expected both buckets nil for unknown tenant")
	}
}

// ── AnomalyDetector ──────────────────────────────────────────────────────────

func TestAnomalyDetector_FirstSampleNotAnomaly(t *testing.T) {
	d := NewAnomalyDetector()
	anomalous, z := d.Observe("acme", 5.0)
	if anomalous {
		t.Fatalf("first sample should never be anomalous, z=%v", z)
	}
}

func TestAnomalyDetector_LargeSpike(t *testing.T) {
	d := NewAnomalyDetector()
	// Warm up with stable latency.
	for i := 0; i < 50; i++ {
		d.Observe("acme", 2.0)
	}
	// A 10x spike should be anomalous.
	anomalous, z := d.Observe("acme", 200.0)
	if !anomalous {
		t.Fatalf("spike should be anomalous, z=%v", z)
	}
}

func TestAnomalyDetector_TenantIsolation(t *testing.T) {
	d := NewAnomalyDetector()
	for i := 0; i < 50; i++ {
		d.Observe("a", 2.0)
		d.Observe("b", 100.0)
	}
	// Each tenant's own stable state should not be anomalous.
	anom_a, _ := d.Observe("a", 2.0)
	anom_b, _ := d.Observe("b", 100.0)
	if anom_a {
		t.Fatal("stable sample for tenant a should not be anomalous")
	}
	if anom_b {
		t.Fatal("stable sample for tenant b should not be anomalous")
	}
}

func TestAnomalyDetector_Counter(t *testing.T) {
	d := NewAnomalyDetector()

	// No anomalies yet → count is 0.
	if d.GetCount("acme") != 0 {
		t.Fatal("expected 0 anomaly count before any increment")
	}

	// Manually increment (simulating what the middleware does on detected anomalies).
	d.IncrCounter("acme")
	d.IncrCounter("acme")
	d.IncrCounter("acme")

	if d.GetCount("acme") != 3 {
		t.Fatalf("anomaly count: got %d want 3", d.GetCount("acme"))
	}
	// Different tenant should remain at 0.
	if d.GetCount("other") != 0 {
		t.Fatal("expected 0 for tenant that was never incremented")
	}
}

// ── ErrorStore ───────────────────────────────────────────────────────────────

func TestErrorStore_RecordAndCount(t *testing.T) {
	es := NewErrorStore()
	err := errors.New("connection timeout")

	// Repeated occurrences of the same error from the same call site (the real
	// production pattern: one fixed Record sink) must dedup into one group with an
	// incrementing count — not a new group per occurrence.
	for i := 0; i < 3; i++ {
		es.Record("acme", err)
	}

	es.mu.RLock()
	groups := es.groups["acme"]
	es.mu.RUnlock()
	if len(groups) != 1 {
		t.Fatalf("expected 1 error group, got %d", len(groups))
	}
	for _, g := range groups {
		if g.Count.Load() != 3 {
			t.Fatalf("count: got %d want 3", g.Count.Load())
		}
	}
}

// TestErrorStore_FingerprintSeparatesCallSites verifies that the fingerprint
// includes call-site PCs (its documented purpose). The same error message
// recorded from two distinct call sites produces two groups. This regressed
// silently before: binary.Write rejected uintptr, so the PCs never entered the
// hash and every same-message error collapsed into a single group.
func TestErrorStore_FingerprintSeparatesCallSites(t *testing.T) {
	es := NewErrorStore()
	err := errors.New("connection timeout")

	es.Record("acme", err) // call site A
	es.Record("acme", err) // call site B (different line ⇒ different PCs)

	es.mu.RLock()
	groups := es.groups["acme"]
	es.mu.RUnlock()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups for 2 distinct call sites, got %d", len(groups))
	}
}

func TestErrorStore_DifferentErrorsGetSeparateGroups(t *testing.T) {
	es := NewErrorStore()
	es.Record("acme", errors.New("timeout"))
	es.Record("acme", errors.New("not found"))

	es.mu.RLock()
	groups := es.groups["acme"]
	es.mu.RUnlock()
	if len(groups) != 2 {
		t.Fatalf("expected 2 error groups, got %d", len(groups))
	}
}

func TestErrorStore_TenantIsolation(t *testing.T) {
	es := NewErrorStore()
	es.Record("a", errors.New("err"))
	es.Record("b", errors.New("err"))

	es.mu.RLock()
	ga := es.groups["a"]
	gb := es.groups["b"]
	es.mu.RUnlock()
	if len(ga) != 1 || len(gb) != 1 {
		t.Fatal("each tenant should have independent error groups")
	}
}

func TestErrorStore_TopN(t *testing.T) {
	es := NewErrorStore()
	errA := errors.New("auth failure")
	errB := errors.New("db timeout")
	errC := errors.New("rate limit")

	// errB most frequent (5×), errA second (3×), errC once.
	for i := 0; i < 5; i++ {
		es.Record("acme", errB)
	}
	for i := 0; i < 3; i++ {
		es.Record("acme", errA)
	}
	es.Record("acme", errC)

	top := es.TopN("acme", 2)
	if len(top) != 2 {
		t.Fatalf("TopN(2): got %d entries, want 2", len(top))
	}
	// First entry must be errB (count 5).
	if top[0]["message"] != errB.Error() {
		t.Fatalf("top[0] message: got %q want %q", top[0]["message"], errB.Error())
	}
	if top[0]["count"].(int64) != 5 {
		t.Fatalf("top[0] count: got %v want 5", top[0]["count"])
	}
	// Second must be errA (count 3).
	if top[1]["message"] != errA.Error() {
		t.Fatalf("top[1] message: got %q want %q", top[1]["message"], errA.Error())
	}
	// TopN for unknown tenant returns nil.
	if es.TopN("ghost", 5) != nil {
		t.Fatal("TopN for unknown tenant should return nil")
	}
}

func TestErrorStore_TopN_IncludesStack(t *testing.T) {
	es := NewErrorStore()
	es.Record("acme", errors.New("stack test error"))

	top := es.TopN("acme", 1)
	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}
	stack, ok := top[0]["stack"].([]Frame)
	if !ok {
		t.Fatalf("stack field missing or wrong type: %T", top[0]["stack"])
	}
	if len(stack) == 0 {
		t.Fatal("stack should not be empty")
	}
}

func TestRecord_StackTraceIsReadable(t *testing.T) {
	es := NewErrorStore()

	triggerError := func() error {
		return errors.New("test stack trace")
	}
	es.Record("acme", triggerError())

	top := es.TopN("acme", 1)
	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}

	stack, ok := top[0]["stack"].([]Frame)
	if !ok {
		t.Fatalf("stack field missing or wrong type: %T", top[0]["stack"])
	}
	if len(stack) == 0 {
		t.Fatal("stack must not be empty")
	}

	// Must include the test function somewhere in the stack.
	found := false
	for _, f := range stack {
		if strings.Contains(f.Function, "TestRecord") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("stack must include the calling test function; got: %v", stack)
	}

	// Must not expose runtime.goexit or similar.
	for _, f := range stack {
		if strings.Contains(f.Function, "runtime.goexit") {
			t.Errorf("stack must not include runtime.goexit: %v", f)
		}
	}

	// File paths must be relative, not absolute.
	for _, f := range stack {
		if strings.HasPrefix(f.File, "/") {
			t.Errorf("file path must be relative, got: %s", f.File)
		}
	}
}
