package observability

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestPrune_CapsSlowTraceRows verifies the slow_traces hard row cap: under an error
// flood that stays inside the 7-day window, Prune drops the oldest rows so the
// table stays bounded. Without it the table grew without bound between restarts.
func TestPrune_CapsSlowTraceRows(t *testing.T) {
	old := maxSlowTraceRows
	maxSlowTraceRows = 10
	defer func() { maxSlowTraceRows = old }()

	st, err := OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close() //nolint:errcheck

	nowUs := time.Now().UnixMicro()
	for i := 0; i < 50; i++ {
		if err := st.SaveSlowTrace("10", TraceView{
			TraceID: fmt.Sprintf("trace-%02d", i),
			TS:      nowUs + int64(i)*1000, // distinct, recent (inside the window)
			Status:  500,
			Route:   "/api/guides",
		}); err != nil {
			t.Fatalf("SaveSlowTrace: %v", err)
		}
	}

	if err := st.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	var n int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM slow_traces`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 10 {
		t.Fatalf("slow_traces not capped to 10: got %d", n)
	}

	// The retained rows must be the 10 newest (trace-40 .. trace-49).
	var minID string
	if err := st.db.QueryRow(`SELECT MIN(trace_id) FROM slow_traces`).Scan(&minID); err != nil {
		t.Fatalf("min id: %v", err)
	}
	if minID != "trace-40" {
		t.Fatalf("expected newest 10 kept (min trace-40), got min %q", minID)
	}
}
