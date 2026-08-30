package observability

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// QueryTracer is the pgx.QueryTracer the engine's pool runs with
// (OBSERVABILIDAD-ERRORES-S1). It does ONE thing and does it for every route,
// generated or custom, because it hangs off the driver and not off a handler:
// when a statement FAILS, it notes the exact SQL and the driver's message on the
// request's SpanTracker, so a 5xx capture can say WHICH query broke — the
// piece the field report ("EL 500 MUDO") had to guess. Bound parameter VALUES
// are never recorded (they can be personal data; the SQL template names the
// column, which is what locates the bug).
//
// Cost on the happy path: one context lookup per query start (returns ctx
// unchanged — no allocation) and one nil check + one error check at the end.
type QueryTracer struct{}

// TraceQueryStart remembers the statement about to run on the request's
// tracker (a string assignment — pgx hands the SQL in Start and only the
// verdict in End) and returns ctx unchanged: no allocation on the way in.
func (QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t := SpanTrackerFromCtx(ctx); t != nil {
		t.pendingSQL = data.SQL
	}
	return ctx
}

// TraceQueryEnd notes a failed statement on the request's tracker.
func (QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err == nil {
		return
	}
	if t := SpanTrackerFromCtx(ctx); t != nil {
		t.NoteFailedQuery(t.pendingSQL, data.Err)
		// The failing stage is THIS query — mark it here, at the source, for
		// every route (generated, Ctx, or a raw UnsafeTx statement alike).
		t.MarkFailed("query")
	}
}
