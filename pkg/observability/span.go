package observability

import (
	"context"
	"time"
)

// maxSpans is the fixed capacity of a SpanTracker. A request that wants to mark
// more stages than this silently drops the extras (no panic, no allocation).
const maxSpans = 8

// Span is one timed stage within a request. Name is a constant literal (e.g.
// "jwt"), so storing it never allocates.
type Span struct {
	Name  string `json:"name"`
	DurUS int32  `json:"dur_us"`
}

// SpanTracker records up to maxSpans stage durations for a single request. It is
// a fixed array (not a slice) so Mark never allocates and the tracker never
// escapes beyond the one heap allocation made by NewSpanTracker. It is NOT safe
// for concurrent Mark calls — but a request is handled by one goroutine through
// the middleware chain, so the spans are marked sequentially.
type SpanTracker struct {
	spans [maxSpans]Span
	count int
	last  time.Time
}

// NewSpanTracker starts a tracker whose clock begins now.
func NewSpanTracker() *SpanTracker {
	return &SpanTracker{last: time.Now()}
}

// Mark records the elapsed time since the previous Mark (or since creation) as a
// span named name. Once maxSpans are recorded, further marks are ignored (the
// clock still advances), so a runaway caller can never overflow or panic.
func (t *SpanTracker) Mark(name string) {
	now := time.Now()
	if t.count < maxSpans {
		t.spans[t.count] = Span{Name: name, DurUS: int32(now.Sub(t.last).Microseconds())}
		t.count++
	}
	t.last = now
}

// Finish records a final "done" span (the tail since the last mark) and returns
// the recorded spans. The returned slice is backed by the tracker's array — no
// allocation — and must be copied if it needs to outlive the tracker.
func (t *SpanTracker) Finish() []Span {
	t.Mark("done")
	return t.spans[:t.count]
}

// TotalUS returns the sum of all recorded span durations.
func (t *SpanTracker) TotalUS() int32 {
	var sum int32
	for i := 0; i < t.count; i++ {
		sum += t.spans[i].DurUS
	}
	return sum
}

// spanCtxKey keys the SpanTracker pointer in a context.
type spanCtxKey struct{}

// WithSpanTracker returns a context carrying a fresh SpanTracker.
func WithSpanTracker(ctx context.Context) context.Context {
	return context.WithValue(ctx, spanCtxKey{}, NewSpanTracker())
}

// SpanTrackerFromCtx returns the SpanTracker in ctx, or nil if none is set.
// Callers must nil-check before calling Mark.
func SpanTrackerFromCtx(ctx context.Context) *SpanTracker {
	t, _ := ctx.Value(spanCtxKey{}).(*SpanTracker)
	return t
}
