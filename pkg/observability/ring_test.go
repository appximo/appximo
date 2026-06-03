package observability

import (
	"sync"
	"testing"
)

// TestTenantRing_CircularOverwrite verifies that writing more than ringSize
// samples overwrites the oldest entries: after ringSize+1 writes the buffer holds
// samples 1..ringSize (sample 0 evicted) and the most recent is the last written.
func TestTenantRing_CircularOverwrite(t *testing.T) {
	var r TenantRing
	total := ringSize + 1 // one past a full lap → forces a single overwrite
	for i := 0; i < total; i++ {
		r.Record(Sample{Start: int64(i), Status: uint16(i)})
	}

	snap := r.Snapshot()
	if len(snap) != ringSize {
		t.Fatalf("expected snapshot len %d, got %d", ringSize, len(snap))
	}
	// Most recent first → the last written sample (Start == total-1).
	if snap[0].Start != int64(total-1) {
		t.Errorf("newest sample: want Start=%d, got %d", total-1, snap[0].Start)
	}
	// Oldest retained should be Start==1 (Start==0 was overwritten).
	if got := snap[len(snap)-1].Start; got != 1 {
		t.Errorf("oldest retained sample: want Start=1, got %d", got)
	}
	// Sample 0 must no longer be present anywhere in the buffer.
	for _, s := range snap {
		if s.Start == 0 {
			t.Fatalf("evicted sample (Start=0) still present after overwrite")
		}
	}
}

// TestTenantRing_SnapshotOrder verifies snapshots are ordered most-recent → oldest
// for a partially filled buffer (no wraparound).
func TestTenantRing_SnapshotOrder(t *testing.T) {
	var r TenantRing
	const n = 5
	for i := 0; i < n; i++ {
		r.Record(Sample{Start: int64(i)})
	}

	snap := r.Snapshot()
	if len(snap) != n {
		t.Fatalf("expected %d samples, got %d", n, len(snap))
	}
	for i := 0; i < n; i++ {
		want := int64(n - 1 - i) // 4,3,2,1,0
		if snap[i].Start != want {
			t.Errorf("position %d: want Start=%d, got %d", i, want, snap[i].Start)
		}
	}
}

// TestTenantRing_ConcurrentRecord exercises Record from many goroutines. Run with
// -race; concurrent writers each claim a distinct slot atomically, so there must be
// no data race and the head must advance by exactly the number of writes.
func TestTenantRing_ConcurrentRecord(t *testing.T) {
	var r TenantRing
	const goroutines = 64
	const perG = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				r.Record(Sample{Start: int64(base + i)})
			}
		}(g * perG)
	}
	wg.Wait()

	if r.head != goroutines*perG {
		t.Errorf("head: want %d, got %d", goroutines*perG, r.head)
	}
	// Buffer is saturated; snapshot must be exactly full and self-consistent.
	if snap := r.Snapshot(); len(snap) != ringSize {
		t.Errorf("snapshot len: want %d, got %d", ringSize, len(snap))
	}
}

// TestTenantRing_SnapshotEmpty verifies an untouched ring returns a non-nil empty
// slice (marshals to JSON [], not null).
func TestTenantRing_SnapshotEmpty(t *testing.T) {
	var r TenantRing
	snap := r.Snapshot()
	if snap == nil {
		t.Fatal("snapshot of empty ring must be non-nil")
	}
	if len(snap) != 0 {
		t.Fatalf("snapshot of empty ring must be empty, got len %d", len(snap))
	}
}
