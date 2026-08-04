package appximo

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/appximo/appximo/pkg/db"
)

// ENG-10 (CONSUMER-PATH-S1): a custom handler that wraps a DB-unavailable cause
// in a 5xx via Ctx.Error must yield the HONEST 503 + Retry-After — the same
// classification the generated routes apply. Measured live on the 58: with
// PostgreSQL stopped, custom routes answered 151×500 while the generated
// surface answered 503.
func TestCtxError_ReclassifiesDBUnavailableTo503(t *testing.T) {
	t.Parallel()

	// A RAW, unclassified transport error — what a handler's own SQL on the
	// tenant tx actually returns when PostgreSQL is down (it never passes
	// through pkg/db's classify).
	c := &requestCtx{}
	_ = c.Error(500, "error consultando el catálogo", fmt.Errorf("query: %w", syscall.ECONNREFUSED))
	if c.status != 503 {
		t.Fatalf("raw ECONNREFUSED cause: status = %d, want 503", c.status)
	}
	if !c.retryAfter {
		t.Fatal("raw ECONNREFUSED cause: retryAfter not set")
	}
	rec := httptest.NewRecorder()
	c.flush(rec)
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want \"1\"", got)
	}
	if rec.Code != 503 {
		t.Fatalf("flushed status = %d, want 503", rec.Code)
	}

	// The already-classified sentinel (an error that DID pass through pkg/db).
	c2 := &requestCtx{}
	_ = c2.Error(500, "x", fmt.Errorf("%w: conn closed", db.ErrUnavailable))
	if c2.status != 503 {
		t.Fatalf("classified sentinel: status = %d, want 503", c2.status)
	}

	// A 4xx is the handler's DELIBERATE classification of a caller problem —
	// never overridden, even with an unavailable cause attached.
	c3 := &requestCtx{}
	_ = c3.Error(404, "not found", syscall.ECONNREFUSED)
	if c3.status != 404 || c3.retryAfter {
		t.Fatalf("4xx must be kept: status = %d retryAfter = %v", c3.status, c3.retryAfter)
	}

	// A 5xx whose cause is NOT a DB-unavailability stays what the handler chose.
	c4 := &requestCtx{}
	_ = c4.Error(500, "boom", errors.New("some app bug"))
	if c4.status != 500 || c4.retryAfter {
		t.Fatalf("non-DB 500 must be kept: status = %d retryAfter = %v", c4.status, c4.retryAfter)
	}

	// No cause at all (handler chose 500 with nil error) — kept.
	c5 := &requestCtx{}
	_ = c5.Error(500, "boom", nil)
	if c5.status != 500 {
		t.Fatalf("nil-cause 500 must be kept: status = %d", c5.status)
	}
}
