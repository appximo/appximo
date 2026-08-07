package observability

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStore_OpenCreatesTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "obs.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// The table exists if a query against it succeeds (empty result, no error).
	if _, err := store.History("nobody", 24); err != nil {
		t.Fatalf("History on fresh db should succeed, got %v", err)
	}
}

func TestStore_FlushHistoryRoundtrip(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Now().Unix()
	snaps := []Snapshot{
		{TenantID: "10", TS: now - 10, P50US: 100, P95US: 200, ErrorRatio: 0.01, BurnRate: 10, SLOStatus: "warning"},
		{TenantID: "10", TS: now - 5, P50US: 110, P95US: 220, ErrorRatio: 0.02, BurnRate: 20, SLOStatus: "critical"},
		{TenantID: "20", TS: now - 5, P50US: 50, P95US: 80, ErrorRatio: 0, BurnRate: 0, SLOStatus: "ok"},
	}
	for _, s := range snaps {
		if err := store.Flush(s.TenantID, s); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}

	hist, err := store.History("10", 24)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("tenant 10 history: want 2, got %d", len(hist))
	}
	// Newest first.
	if hist[0].TS != now-5 || hist[0].SLOStatus != "critical" {
		t.Errorf("ordering/values wrong: %+v", hist[0])
	}
	if hist[1].P95US != 200 || hist[1].ErrorRatio != 0.01 {
		t.Errorf("roundtrip values wrong: %+v", hist[1])
	}
}

func TestStore_PruneDropsOldKeepsRecent(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Now().Unix()
	old := Snapshot{TenantID: "10", TS: now - 8*86400, SLOStatus: "ok"} // 8 days → pruned
	recent := Snapshot{TenantID: "10", TS: now - 3600, SLOStatus: "ok"} // 1h → kept
	if err := store.Flush("10", old); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush("10", recent); err != nil {
		t.Fatal(err)
	}

	if err := store.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	// History over a wide window (240h = 10 days) should now only see the recent row.
	hist, err := store.History("10", 240)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("after prune: want 1, got %d", len(hist))
	}
	if hist[0].TS != now-3600 {
		t.Errorf("wrong row survived prune: %+v", hist[0])
	}
}

func TestStore_EmptyPathUsesPersistentDefault(t *testing.T) {
	// An empty path resolves to the persistent default — NOT /tmp (R1: the history
	// must survive a restart out of the box). Asserted on the pure resolver so the
	// test never writes to the real system path.
	got := resolveObsDBPath("")
	if got != defaultObsDBPath() {
		t.Fatalf("resolveObsDBPath(\"\") = %q, want %q", got, defaultObsDBPath())
	}
	if got == "/tmp/obs.db" || strings.HasPrefix(got, "/tmp/") {
		t.Errorf("default obs path %q is ephemeral (under /tmp) — R1 regression", got)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("default obs path %q should be absolute", got)
	}
	// An explicit path is honored verbatim.
	if p := resolveObsDBPath("/data/obs.db"); p != "/data/obs.db" {
		t.Errorf("resolveObsDBPath(explicit) = %q, want /data/obs.db", p)
	}
}

func TestStore_ConcurrentFlushNoRace(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Now().Unix()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				// Distinct (tenant_id, ts) keys avoid PK collisions between goroutines.
				_ = store.Flush("t", Snapshot{TenantID: "t", TS: now + int64(base*1000+i), SLOStatus: "ok"})
			}
		}(g)
	}
	wg.Wait()

	hist, err := store.History("t", 24*365) // wide window to count everything just written
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 8*50 {
		t.Errorf("want %d rows, got %d", 8*50, len(hist))
	}
}
