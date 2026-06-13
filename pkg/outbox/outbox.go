// Package outbox implements the transactional outbox pattern (ADR-016 §Class 2).
// Events are written inside the caller's transaction so a business-write rollback
// atomically prevents the event from being enqueued — no two-phase commit needed.
//
// The table lives in the shared public schema (same as public.tenants) so one
// pool serves all tenants. The worker (future session) polls with
// SELECT … FOR UPDATE SKIP LOCKED and processes rows per topic.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnsureTable creates public.outbox idempotently. Safe to call at every boot.
func EnsureTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.outbox (
    id         BIGSERIAL   PRIMARY KEY,
    tenant_id  TEXT        NOT NULL,
    topic      TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    state      TEXT        NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ,
    attempts   INT         NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON public.outbox (created_at) WHERE state = 'pending';
`)
	if err != nil {
		return fmt.Errorf("outbox: ensure table: %w", err)
	}
	return nil
}

// Enqueue inserts an event into public.outbox within tx.
// Because the INSERT runs inside the caller's transaction, a rollback also rolls
// back the enqueue — the event never reaches the worker unless the business write
// commits. Returns the generated row id on success.
func Enqueue(ctx context.Context, tx pgx.Tx, tenantID, topic string, payload any) (int64, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("outbox: marshal payload: %w", err)
	}
	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO public.outbox (tenant_id, topic, payload) VALUES ($1, $2, $3) RETURNING id`,
		tenantID, topic, string(b),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("outbox: insert: %w", err)
	}
	return id, nil
}
