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
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotifyChannel is the Postgres LISTEN/NOTIFY channel the engine signals after a
// successful Enqueue and that the worker (cmd/appximo-worker) LISTENs on. The
// NOTIFY is only a wake-up HINT: the worker still polls public.outbox, which is
// the durable source of truth, because a notification emitted while the worker is
// down is lost forever (NOTIFY is ephemeral, not queued). Keep this constant the
// single source of truth shared by engine and worker so the two never drift.
const NotifyChannel = "outbox_notify"

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

	// Wake the worker. pg_notify is transactional in Postgres: the signal is
	// delivered only if (and when) this tx commits, so a rolled-back business
	// write never wakes a worker for a row that doesn't exist — the same
	// atomicity guarantee as the INSERT above, for free.
	//
	// The payload is the row id ONLY. NOTIFY caps payloads at 8000 bytes, and the
	// worker SELECTs the full row itself, so a large business payload never rides
	// the notification channel. The id is also all the worker needs: it drains
	// every pending row on each wake regardless of which id triggered it.
	if _, err := tx.Exec(ctx,
		`SELECT pg_notify($1, $2)`, NotifyChannel, strconv.FormatInt(id, 10),
	); err != nil {
		return 0, fmt.Errorf("outbox: notify: %w", err)
	}
	return id, nil
}
