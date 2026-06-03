package logging

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// PGXQueryTracer implements pgx.QueryTracer and logs SQL statements at DEBUG
// level WITHOUT their bound parameter values ($1, $2, …) to prevent accidental
// exfiltration of PII or sensitive data into structured logs.
type PGXQueryTracer struct{}

// TraceQueryStart is called before a query executes.
// Only the SQL template is logged — args are intentionally omitted.
func (PGXQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	Log.Debug().Str("sql", data.SQL).Msg("pgx query start")
	return ctx
}

// TraceQueryEnd is called after a query completes or fails.
func (PGXQueryTracer) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		Log.Error().Err(data.Err).Msg("pgx query error")
	}
}
