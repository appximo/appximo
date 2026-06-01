package observability

import (
	"errors"
	"testing"
)

// ── TenantHistogram ──────────────────────────────────────────────────────────

func TestTenantHistogram_RecordAndSnapshot(t *testing.T) {
	th := NewTenantHistogram()

	// No data yet → nil snapshot.
	if th.Snapshot("acme") != nil {
		t.Fatal("expected nil snapshot for unknown tenant")
	}

	// Record a batch of values.
	for i := int64(1); i <= 100; i++ {
		th.Record("acme", i)
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
	th.Record("a", 10)
	th.Record("b", 999)

	snapA := th.Snapshot("a")
	snapB := th.Snapshot("b")

	if snapA.P99Us > 20 {
		t.Fatalf("tenant a should have small latencies, got p99=%v", snapA.P99Us)
	}
	if snapB.P99Us < 500 {
		t.Fatalf("tenant b should have large latencies, got p99=%v", snapB.P99Us)
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

// ── ErrorStore ───────────────────────────────────────────────────────────────

func TestErrorStore_RecordAndCount(t *testing.T) {
	es := NewErrorStore()
	err := errors.New("connection timeout")

	es.Record("acme", err)
	es.Record("acme", err)
	es.Record("acme", err)

	// Verify dedup: same error recorded once in groups.
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
