package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists runs and cron schedules — the observability the outbox never
// had, built in from day one. Tables live in public (shared, one per install),
// ensured idempotently by BOTH the engine boot and the worker boot.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool (the enqueue step shares it).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// EnsureTables creates the run + cron tables idempotently (the outbox pattern).
func EnsureTables(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS public.workflow_runs (
    id          BIGSERIAL   PRIMARY KEY,
    tenant_id   TEXT        NOT NULL,
    workflow    TEXT        NOT NULL,
    trigger     TEXT        NOT NULL,
    status      TEXT        NOT NULL, -- running | ok | failed | skipped_overlap
    error       TEXT,
    detail      JSONB,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_recent
    ON public.workflow_runs (started_at DESC);
CREATE INDEX IF NOT EXISTS idx_workflow_runs_wf
    ON public.workflow_runs (tenant_id, workflow, started_at DESC);
CREATE TABLE IF NOT EXISTS public.workflow_cron (
    workflow  TEXT        NOT NULL,
    tenant_id TEXT        NOT NULL,
    next_run  TIMESTAMPTZ,
    last_run  TIMESTAMPTZ,
    PRIMARY KEY (workflow, tenant_id)
);
-- VOZ-DELTA-S1: the spec the stored next_run was computed from, so a deploy
-- that CHANGES a cron re-arms the schedule instead of waiting for the old
-- next_run to pass (up to a day for a daily cron — found by provocation).
ALTER TABLE public.workflow_cron ADD COLUMN IF NOT EXISTS spec TEXT;
`+ensureReminderTable)
	if err != nil {
		return fmt.Errorf("workflows: ensure tables: %w", err)
	}
	return nil
}

// StartRun records a run beginning; returns the run id.
func (s *Store) StartRun(ctx context.Context, tenant, workflow, trigger string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO public.workflow_runs (tenant_id, workflow, trigger, status)
		VALUES ($1, $2, $3, 'running') RETURNING id`,
		tenant, workflow, trigger).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("workflows: start run: %w", err)
	}
	return id, nil
}

// FinishRun records the outcome. detail is the per-step summary (marshaled).
func (s *Store) FinishRun(ctx context.Context, id int64, status, errMsg string, detail any) error {
	var dj []byte
	if detail != nil {
		dj, _ = json.Marshal(detail)
	}
	var errVal *string
	if errMsg != "" {
		if len(errMsg) > 1000 {
			errMsg = errMsg[:1000] + "…"
		}
		errVal = &errMsg
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE public.workflow_runs
		SET status = $2, error = $3, detail = $4, finished_at = now()
		WHERE id = $1`, id, status, errVal, dj)
	if err != nil {
		return fmt.Errorf("workflows: finish run: %w", err)
	}
	return nil
}

// RecordSkip records a run that did NOT start (overlap policy "skip"): the
// skipped occurrence is a visible row, not a silent nothing.
func (s *Store) RecordSkip(ctx context.Context, tenant, workflow, trigger, reason string) {
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO public.workflow_runs (tenant_id, workflow, trigger, status, error, finished_at)
		VALUES ($1, $2, $3, 'skipped_overlap', $4, now())`,
		tenant, workflow, trigger, reason)
}

// CronRow is one persisted schedule.
type CronRow struct {
	Workflow string
	Tenant   string
	NextRun  *time.Time
	LastRun  *time.Time
}

// UpsertSchedule ensures a (workflow, tenant) schedule row exists; a new row's
// next_run is initialized to next (no catch-up for a workflow first seen).
func (s *Store) UpsertSchedule(ctx context.Context, workflow, tenant string, next time.Time) error {
	return s.UpsertScheduleSpec(ctx, workflow, tenant, "", next)
}

// UpsertScheduleSpec is UpsertSchedule with the cron SPEC (expr + timezone)
// recorded beside next_run: an existing row keeps its next_run while the
// spec is unchanged (never re-armed by a mere restart), and is RE-ARMED to
// `next` the moment the deployed spec differs — a changed "0 7 * * *" takes
// effect on the next tick, not after the stale next_run passes. An empty
// spec (legacy callers/tests) means "do not compare".
func (s *Store) UpsertScheduleSpec(ctx context.Context, workflow, tenant, spec string, next time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO public.workflow_cron (workflow, tenant_id, next_run, spec)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (workflow, tenant_id) DO UPDATE
		   SET next_run = EXCLUDED.next_run, spec = EXCLUDED.spec
		 WHERE EXCLUDED.spec IS NOT NULL
		   AND public.workflow_cron.spec IS DISTINCT FROM EXCLUDED.spec
		   AND public.workflow_cron.spec IS NOT NULL`, workflow, tenant, next, spec)
	if err != nil {
		return err
	}
	// A row that predates the spec column (NULL) adopts the current spec
	// without re-arming (the stored next_run was computed from it).
	_, err = s.pool.Exec(ctx, `UPDATE public.workflow_cron SET spec = NULLIF($3,'') WHERE workflow=$1 AND tenant_id=$2 AND spec IS NULL`, workflow, tenant, spec)
	return err
}

// PruneSchedules deletes schedule rows not in keep (workflow removed from the
// schema, or the tenant is gone). keep entries are "workflow\x00tenant".
func (s *Store) PruneSchedules(ctx context.Context, keep map[string]bool) error {
	rows, err := s.pool.Query(ctx, `SELECT workflow, tenant_id FROM public.workflow_cron`)
	if err != nil {
		return err
	}
	type key struct{ wf, tenant string }
	var stale []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.wf, &k.tenant); err != nil {
			rows.Close()
			return err
		}
		if !keep[k.wf+"\x00"+k.tenant] {
			stale = append(stale, k)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, k := range stale {
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM public.workflow_cron WHERE workflow = $1 AND tenant_id = $2`, k.wf, k.tenant); err != nil {
			return err
		}
	}
	return nil
}

// DueSchedules returns the rows whose next_run is at or before now.
func (s *Store) DueSchedules(ctx context.Context, now time.Time) ([]CronRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT workflow, tenant_id, next_run, last_run
		FROM public.workflow_cron
		WHERE next_run IS NOT NULL AND next_run <= $1
		ORDER BY next_run`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronRow
	for rows.Next() {
		var r CronRow
		if err := rows.Scan(&r.Workflow, &r.Tenant, &r.NextRun, &r.LastRun); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Advance moves a schedule past a firing: last_run = firedAt, next_run = next.
// Done BEFORE the run starts, so a crash mid-run never double-fires the same
// occurrence (the missed RUN is visible as a 'running' row that never finished).
func (s *Store) Advance(ctx context.Context, workflow, tenant string, firedAt, next time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.workflow_cron SET last_run = $3, next_run = $4
		WHERE workflow = $1 AND tenant_id = $2`, workflow, tenant, firedAt, next)
	return err
}
