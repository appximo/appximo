package observability_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/observability"
)

func openTempStore(t *testing.T) *observability.ObsStore {
	t.Helper()
	st, err := observability.OpenStore(filepath.Join(t.TempDir(), "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSlowTraceThreshold_Is50ms(t *testing.T) {
	if observability.SlowTraceThresholdUS != 50_000 {
		t.Fatalf("SlowTraceThresholdUS = %d, want 50000", observability.SlowTraceThresholdUS)
	}
}

func TestSaveSlowTrace_RoundTrip(t *testing.T) {
	st := openTempStore(t)
	nowUS := time.Now().UnixMicro()
	tv := observability.TraceView{
		TraceID: "a1b2c3d4e5f6a7b8",
		TS:      nowUS,
		Route:   "GET /api/guides",
		TotalUS: 48200,
		Status:  200,
		Spans: []observability.Span{
			{Name: "jwt", DurUS: 44},
			{Name: "rbac", DurUS: 11},
			{Name: "query", DurUS: 47200},
			{Name: "serialize", DurUS: 410},
		},
	}
	if err := st.SaveSlowTrace("10", tv); err != nil {
		t.Fatalf("SaveSlowTrace: %v", err)
	}

	got, err := st.SlowTraces("10", 24)
	if err != nil {
		t.Fatalf("SlowTraces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d traces, want 1", len(got))
	}
	g := got[0]
	if g.TraceID != tv.TraceID || g.Route != tv.Route || g.TotalUS != tv.TotalUS {
		t.Errorf("roundtrip mismatch: %+v", g)
	}
	if len(g.Spans) != 4 || g.Spans[2].Name != "query" || g.Spans[2].DurUS != 47200 {
		t.Errorf("spans not preserved: %+v", g.Spans)
	}
	// A different tenant sees nothing.
	if other, _ := st.SlowTraces("99", 24); len(other) != 0 {
		t.Errorf("tenant 99 should have 0 traces, got %d", len(other))
	}
}

func TestSlowTraces_24hWindow(t *testing.T) {
	st := openTempStore(t)
	recentUS := time.Now().UnixMicro()
	oldUS := time.Now().Add(-25 * time.Hour).UnixMicro()
	_ = st.SaveSlowTrace("10", observability.TraceView{TraceID: "1111111111111111", TS: recentUS, Route: "r", TotalUS: 60000})
	_ = st.SaveSlowTrace("10", observability.TraceView{TraceID: "2222222222222222", TS: oldUS, Route: "r", TotalUS: 60000})

	got, err := st.SlowTraces("10", 24)
	if err != nil {
		t.Fatalf("SlowTraces: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "1111111111111111" {
		t.Fatalf("24h window should return only the recent trace, got %d: %+v", len(got), got)
	}
}

func TestPrune_RemovesOldSlowTraces(t *testing.T) {
	st := openTempStore(t)
	recentUS := time.Now().UnixMicro()
	oldUS := time.Now().Add(-8 * 24 * time.Hour).UnixMicro() // beyond 7-day retention
	_ = st.SaveSlowTrace("10", observability.TraceView{TraceID: "aaaaaaaaaaaaaaaa", TS: recentUS, Route: "r", TotalUS: 60000})
	_ = st.SaveSlowTrace("10", observability.TraceView{TraceID: "bbbbbbbbbbbbbbbb", TS: oldUS, Route: "r", TotalUS: 60000})

	if err := st.Prune(); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// Query a wide window so only retention (not the 24h window) filters results.
	got, err := st.SlowTraces("10", 24*30)
	if err != nil {
		t.Fatalf("SlowTraces: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "aaaaaaaaaaaaaaaa" {
		t.Fatalf("Prune should drop the 8-day-old trace, left %d: %+v", len(got), got)
	}
}
