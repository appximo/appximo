package observability_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/observability"
)

func TestCaptureError_UsefulFrames(t *testing.T) {
	c := observability.CaptureError(context.Background(), errors.New("boom"))
	if len(c.Stack) == 0 {
		t.Fatal("expected at least one stack frame")
	}
	if c.ErrMsg != "boom" {
		t.Errorf("ErrMsg = %q, want boom", c.ErrMsg)
	}
	sawTest := false
	for _, f := range c.Stack {
		if strings.HasPrefix(f.Function, "runtime.") || strings.Contains(f.Function, "net/http.") {
			t.Errorf("stack should not contain infra frame: %s", f.Function)
		}
		if strings.Contains(f.Function, "observability_test.") {
			sawTest = true
		}
	}
	if !sawTest {
		t.Errorf("expected a frame from the test (the caller); got %+v", c.Stack)
	}
}

func recurse(n int, ctx context.Context, err error) observability.ErrorCapture {
	if n > 0 {
		return recurse(n-1, ctx, err)
	}
	return observability.CaptureError(ctx, err)
}

func TestCaptureError_MaxFrames(t *testing.T) {
	c := recurse(40, context.Background(), errors.New("deep"))
	if len(c.Stack) == 0 {
		t.Fatal("expected frames")
	}
	if len(c.Stack) > 10 {
		t.Fatalf("expected at most 10 frames, got %d", len(c.Stack))
	}
}

func TestCaptureError_FingerprintReuse(t *testing.T) {
	err := errors.New("repeated error")
	// Both calls are from the SAME source line (the loop body) → identical call
	// site → same fingerprint → the symbolized slice is reused (same backing
	// array), proving no re-symbolization on repeat occurrences.
	var caps [2]observability.ErrorCapture
	for i := 0; i < 2; i++ {
		caps[i] = observability.CaptureError(context.Background(), err)
	}
	if len(caps[0].Stack) == 0 || len(caps[1].Stack) == 0 {
		t.Fatal("expected frames")
	}
	if &caps[0].Stack[0] != &caps[1].Stack[0] {
		t.Errorf("expected reused (cached) frames for the same call site + message")
	}
}

func TestCaptureError_TraceIDFromCtx(t *testing.T) {
	ctx := observability.WithTraceID(context.Background(), "a1b2c3d4e5f60718")
	c := observability.CaptureError(ctx, errors.New("x"))
	if c.TraceID != "a1b2c3d4e5f60718" {
		t.Errorf("TraceID = %q, want a1b2c3d4e5f60718", c.TraceID)
	}
	if c.Timestamp == 0 {
		t.Error("expected a non-zero Timestamp")
	}
}

func BenchmarkCaptureError_FirstOccurrence(b *testing.B) {
	ctx := observability.WithTraceID(context.Background(), "abcdef0123456789")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A distinct message each iteration forces a fresh fingerprint → cache
		// miss → full symbolization (the worst case).
		_ = observability.CaptureError(ctx, errors.New("first-"+strconv.Itoa(i)))
	}
}

func BenchmarkCaptureError_SubsequentOccurrence(b *testing.B) {
	ctx := observability.WithTraceID(context.Background(), "abcdef0123456789")
	err := errors.New("steady error")
	_ = observability.CaptureError(ctx, err) // warm the symbolization cache
	b.ReportAllocs()
	b.ResetTimer()
	var sink observability.ErrorCapture
	for i := 0; i < b.N; i++ {
		sink = observability.CaptureError(ctx, err) // same fingerprint → cache hit
	}
	_ = sink
}

// BenchmarkCaptureError_NoError models the happy path (HTTP 200): the request
// records spans but NEVER calls CaptureError, so capture costs nothing.
func BenchmarkCaptureError_NoError(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tr := observability.NewSpanTracker()
		tr.Mark("jwt")
		tr.Mark("rbac")
		tr.Mark("query")
		_ = tr.Finish() // no error, no CaptureError
	}
}
