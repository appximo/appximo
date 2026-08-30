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
	// Err marks the stage during which the request's error was recorded — the
	// bar in the waterfall where the failure happened (OBSERVABILIDAD-ERRORES-S1).
	// omitempty keeps the JSON of a healthy span byte-identical to before.
	Err bool `json:"err,omitempty"`
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
	// HasError/ErrMsg are set on ANY error response (4xx/5xx) via RecordError.
	// Capture is set only for 500s (carries the symbolized stack) via SetCapture.
	HasError bool
	ErrMsg   string
	Capture  *ErrorCapture
	// failPending flags the stage in flight when the error was recorded so the
	// NEXT Mark/Finish (which closes that stage) carries Err=true.
	failPending bool
	anyFailed   bool
	// UserID/Role are set by the JWT middleware once the token is validated,
	// so the request line and the trace can say WHO even though the claims
	// live in a derived context the logger never sees.
	UserID, Role string
	// LastSQL/LastSQLErr are the most recent FAILED statement seen by the
	// database tracer (QueryTracer) on this request — the exact query the
	// driver rejected, kept as data (never logged here) for the 5xx capture.
	// A handled failure (a 404 after a miss, a 409 after a unique violation)
	// leaves them set but harmless: only the 5xx writers read them.
	LastSQL    string
	LastSQLErr string
	pendingSQL string // the statement in flight (set by the tracer at start)
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
		t.spans[t.count] = Span{Name: name, DurUS: int32(now.Sub(t.last).Microseconds()), Err: t.failPending}
		t.count++
	}
	if t.failPending {
		t.anyFailed = true
	}
	t.failPending = false
	t.last = now
}

// MarkFailed closes the stage that just FAILED — called at the source (the
// driver tracer on a rejected statement, the route on a handler's error), so
// the waterfall marks the bar the failure happened in, not the next one.
func (t *SpanTracker) MarkFailed(name string) {
	t.failPending = true
	t.Mark(name)
}

// Failed reports whether some stage has already been marked as the failure.
func (t *SpanTracker) Failed() bool { return t.anyFailed || t.failPending }

// flagLast marks the most recently closed stage as the failing one when no
// stage has been marked yet — the shape of every middleware error path (mark
// the stage, then record the error).
func (t *SpanTracker) flagLast() {
	if t.anyFailed {
		return
	}
	if t.count > 0 {
		t.spans[t.count-1].Err = true
		t.anyFailed = true
		return
	}
	t.failPending = true
}

// Finish records a final "done" span (the tail since the last mark) and returns
// the recorded spans. The returned slice is backed by the tracker's array — no
// allocation — and must be copied if it needs to outlive the tracker.
func (t *SpanTracker) Finish() []Span {
	t.Mark("done")
	return t.spans[:t.count]
}

// RecordError marks the request as errored and stores the error message. Cheap
// (no stack): used for client errors (401/403/422) and as the message for 500s.
func (t *SpanTracker) RecordError(msg string) {
	t.HasError = true
	t.ErrMsg = msg
	t.flagLast()
}

// NoteFailedQuery remembers the statement the driver just rejected. Called by
// the QueryTracer on EVERY failed query; two string assignments, no allocation.
func (t *SpanTracker) NoteFailedQuery(sql string, err error) {
	t.LastSQL = sql
	t.LastSQLErr = err.Error()
}

// SetCapture attaches a symbolized error capture (the stack) to the request — used
// only for server errors (500). Implies HasError.
func (t *SpanTracker) SetCapture(c *ErrorCapture) {
	t.HasError = true
	t.flagLast()
	t.Capture = c
	if t.ErrMsg == "" && c != nil {
		t.ErrMsg = c.ErrMsg
	}
}

// TotalUS returns the sum of all recorded span durations.
// Spans returns the stages marked so far (a copy) — what a handler can
// publish to the client BEFORE the response is written (Server-Timing).
func (t *SpanTracker) Spans() []Span {
	out := make([]Span, t.count)
	copy(out, t.spans[:t.count])
	return out
}

// ElapsedUS is the engine time spent on the request so far: the marked
// stages plus the tail since the last mark. TotalUS counts only the marks.
func (t *SpanTracker) ElapsedUS() int64 {
	return int64(t.TotalUS()) + time.Since(t.last).Microseconds()
}

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
