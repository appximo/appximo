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

func TestShouldPersistTrace(t *testing.T) {
	cases := []struct {
		name   string
		durUS  int32
		status uint16
		want   bool
	}{
		{"fast 200 → no", 1_000, 200, false},
		{"fast 204 → no", 800, 204, false},
		{"fast 304 → no", 900, 304, false},
		{"at threshold → no", 50_000, 200, false}, // strictly greater than
		{"slow 200 → yes", 60_000, 200, true},
		{"fast 401 → yes", 500, 401, true},
		{"fast 403 → yes", 700, 403, true},
		{"fast 422 → yes", 800, 422, true},
		{"fast 500 → yes", 1_200, 500, true},
		{"slow error → yes", 90_000, 503, true},
	}
	for _, c := range cases {
		got := observability.ShouldPersistTrace(observability.Sample{DurUS: c.durUS, Status: c.status})
		if got != c.want {
			t.Errorf("%s: ShouldPersistTrace(dur=%d,status=%d)=%v, want %v", c.name, c.durUS, c.status, got, c.want)
		}
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
	if g.TraceID != tv.TraceID || g.Route != tv.Route || g.TotalUS != tv.TotalUS || g.Status != tv.Status {
		t.Errorf("roundtrip mismatch: %+v", g)
	}
	if len(g.Spans) != 4 || g.Spans[2].Name != "query" || g.Spans[2].DurUS != 47200 {
		t.Errorf("spans not preserved: %+v", g.Spans)
	}

	// An error trace must round-trip its status so the UI can color it.
	if err := st.SaveSlowTrace("10", observability.TraceView{
		TraceID: "ffffffffffffffff", TS: nowUS, Route: "GET /api/guides", TotalUS: 800, Status: 401,
		Spans: []observability.Span{{Name: "jwt", DurUS: 44}},
	}); err != nil {
		t.Fatalf("SaveSlowTrace error trace: %v", err)
	}
	got2, _ := st.SlowTraces("10", 24)
	var found bool
	for _, x := range got2 {
		if x.TraceID == "ffffffffffffffff" {
			found = true
			if x.Status != 401 {
				t.Errorf("error trace status = %d, want 401", x.Status)
			}
		}
	}
	if !found {
		t.Error("error trace not returned")
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
