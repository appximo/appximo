package observability

import "testing"

// RecordAnomaly must both bump the counter and retain the event (newest first),
// per tenant in isolation — the data the admin panel renders.
func TestAnomalyDetector_RecordAndRecent(t *testing.T) {
	d := NewAnomalyDetector()

	if got := d.RecentAnomalies("t1", 10); got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v", got)
	}

	d.RecordAnomaly("t1", 1000, 4.2)
	d.RecordAnomaly("t1", 2000, 5.0)

	if c := d.GetCount("t1"); c != 2 {
		t.Fatalf("count = %d, want 2", c)
	}
	got := d.RecentAnomalies("t1", 10)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Newest first.
	if got[0].ZScore != 5.0 || got[0].LatencyUS != 2000 {
		t.Fatalf("newest-first wrong: %+v", got[0])
	}
	if got[0].TS == 0 {
		t.Fatalf("event TS not stamped")
	}

	// Per-tenant isolation: t2 never saw an anomaly.
	if got := d.RecentAnomalies("t2", 10); len(got) != 0 {
		t.Fatalf("t2 should be empty, got %d", len(got))
	}
}

// The ring keeps only the most recent anomalyRingSize events, newest first.
func TestAnomalyDetector_RingWraps(t *testing.T) {
	d := NewAnomalyDetector()
	for i := 0; i < anomalyRingSize+5; i++ {
		d.RecordAnomaly("t", float64(i), float64(i))
	}
	got := d.RecentAnomalies("t", anomalyRingSize+100)
	if len(got) != anomalyRingSize {
		t.Fatalf("len = %d, want %d", len(got), anomalyRingSize)
	}
	// Most recent recorded had z == anomalyRingSize+4.
	if got[0].ZScore != float64(anomalyRingSize+4) {
		t.Fatalf("newest z = %v, want %v", got[0].ZScore, float64(anomalyRingSize+4))
	}
	// And n bounds the request.
	if got := d.RecentAnomalies("t", 3); len(got) != 3 {
		t.Fatalf("bounded len = %d, want 3", len(got))
	}
}
