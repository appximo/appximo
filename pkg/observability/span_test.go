package observability

import "testing"

// APP-PODER-S1: the stages a handler has marked are readable BEFORE the
// response is written (Server-Timing), and ElapsedUS includes the tail.
func TestSpanTracker_SpansAndElapsedBeforeFinish(t *testing.T) {
	tr := NewSpanTracker()
	tr.Mark("validate")
	tr.Mark("query")
	got := tr.Spans()
	if len(got) != 2 || got[0].Name != "validate" || got[1].Name != "query" {
		t.Fatalf("Spans() must return the marks so far in order, got %v", got)
	}
	got[0].Name = "mutated"
	if tr.Spans()[0].Name != "validate" {
		t.Fatalf("Spans() must be a copy")
	}
	if tr.ElapsedUS() < int64(tr.TotalUS()) {
		t.Fatalf("ElapsedUS must be >= TotalUS (it adds the tail since the last mark)")
	}
}
