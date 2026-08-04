package observability_test

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/appximo/appximo/pkg/observability"
)

func TestNewTraceID_Format(t *testing.T) {
	id := observability.NewTraceID()
	if len(id) != 16 {
		t.Fatalf("trace id length = %d, want 16", len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("trace id %q is not valid hex: %v", id, err)
	}
}

func TestNewTraceID_Unique(t *testing.T) {
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := observability.NewTraceID()
		if _, dup := seen[id]; dup {
			t.Fatalf("collision after %d ids: %q", i, id)
		}
		seen[id] = struct{}{}
	}
}

func TestTraceCtx_RoundTrip(t *testing.T) {
	ctx := observability.WithTraceID(context.Background(), "deadbeefcafef00d")
	if got := observability.TraceIDFromCtx(ctx); got != "deadbeefcafef00d" {
		t.Fatalf("TraceIDFromCtx = %q", got)
	}
	if got := observability.TraceIDFromCtx(context.Background()); got != "" {
		t.Fatalf("empty ctx should yield \"\", got %q", got)
	}
}

func TestSpanTracker_MarksAndTotal(t *testing.T) {
	tr := observability.NewSpanTracker()
	names := []string{"jwt", "rbac", "cache_miss", "query", "serialize", "gzip"}
	for _, n := range names {
		tr.Mark(n)
	}
	spans := tr.Finish() // adds "done"
	if len(spans) != len(names)+1 {
		t.Fatalf("got %d spans, want %d (6 marks + done)", len(spans), len(names)+1)
	}
	for i, n := range names {
		if spans[i].Name != n {
			t.Errorf("span[%d].Name = %q, want %q", i, spans[i].Name, n)
		}
	}
	if spans[len(spans)-1].Name != "done" {
		t.Errorf("last span = %q, want \"done\"", spans[len(spans)-1].Name)
	}
	// TotalUS must equal the sum of the recorded span durations.
	var sum int32
	for _, s := range spans {
		sum += s.DurUS
	}
	if tr.TotalUS() != sum {
		t.Errorf("TotalUS()=%d, sum of spans=%d", tr.TotalUS(), sum)
	}
}

func TestSpanTracker_OverflowSafe(t *testing.T) {
	tr := observability.NewSpanTracker()
	// 10 marks into an [8] array must not panic.
	for i := 0; i < 10; i++ {
		tr.Mark("stage")
	}
	spans := tr.Finish() // Finish's "done" is also dropped (already full)
	if len(spans) != 8 {
		t.Fatalf("overflow: got %d spans, want capped at 8", len(spans))
	}
}

func TestSpanTrackerFromCtx_NilSafe(t *testing.T) {
	if observability.SpanTrackerFromCtx(context.Background()) != nil {
		t.Fatal("expected nil tracker for empty context")
	}
	ctx := observability.WithSpanTracker(context.Background())
	if observability.SpanTrackerFromCtx(ctx) == nil {
		t.Fatal("expected a tracker after WithSpanTracker")
	}
}

func BenchmarkSpanTrackerMark(b *testing.B) {
	tr := observability.NewSpanTracker()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr.Mark("jwt")
	}
}

func BenchmarkNewTraceID(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = observability.NewTraceID()
	}
}
