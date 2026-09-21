package workflows

// Per-row relative reminders — the "time" trigger (MOTOR-AGENDA-S1, ADR-039).
//
// A cron fires at a fixed wall-clock moment and an event fires when a row
// changes; neither can say "fifteen minutes before THIS row's inicio", which
// is a different instant for every row. The time trigger is a SWEEP: every
// scheduler tick the leader (one worker, the same pg_try_advisory_lock the
// cron scheduler holds) asks the engine, per tenant and per time workflow,
// for the rows whose moment is entering the window — GET /api/{resource}
// filtered on the time field, as the workflow's role, so RBAC decides what
// it may see — evaluates the trigger's `when` on each row, and for each
// candidate CLAIMS the (tenant, workflow, row, due instant) in
// public.workflow_reminders. The claim and the run's `enqueue` steps commit
// in ONE transaction:
//
//	crash before commit → nothing claimed, nothing enqueued: the next sweep
//	                      finds the row again (never lost);
//	crash after commit  → the claim exists: the next sweep skips the row
//	                      (never duplicated).
//
// The due instant is part of the key, so a row whose time MOVES gets exactly
// one new firing at its new moment (the old one is never seen again — the
// sweep only reads current rows) and a row that stops matching `when` (a
// cancelled event) is never a candidate. A row rescheduled AFTER its
// reminder fired gets a new reminder for the new moment — a new appointment
// deserves a new warning. Rows more than `grace` past their due instant are
// skipped, silently by design: a "15 minutes before" that would arrive after
// the meeting started is noise, not a reminder (default grace = the offset
// for `before`, 1h for `after`).
//
// Rejected alternatives: a goroutine timer per row (lost on restart, one
// process's memory, unbounded); an outbox row scheduled per row at write
// time (the outbox has no "deliver at" and a rescheduled row would need its
// old row cancelled — a second bookkeeping the ledger already is, minus the
// race-safety of the claim).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
)

// TimeEntry is one (tenant, time workflow) pair the sweeper drives.
type TimeEntry struct {
	Tenant   string
	Workflow *Workflow
}

// TimeEntries returns the current (tenant, time workflow) pairs.
func (s *Source) TimeEntries() []TimeEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]TimeEntry(nil), s.timeEntries...)
}

// TimeLister is what the sweeper needs from the schema source.
type TimeLister interface {
	TimeEntries() []TimeEntry
}

// ensureReminderTable is appended to EnsureTables: the exactly-once ledger.
const ensureReminderTable = `
CREATE TABLE IF NOT EXISTS public.workflow_reminders (
    tenant_id TEXT        NOT NULL,
    workflow  TEXT        NOT NULL,
    row_id    TEXT        NOT NULL,
    due_at    TIMESTAMPTZ NOT NULL,
    fired_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    run_id    BIGINT,
    PRIMARY KEY (tenant_id, workflow, row_id, due_at)
);
CREATE INDEX IF NOT EXISTS idx_workflow_reminders_fired ON public.workflow_reminders (fired_at DESC);
`

// reminderRetention prunes the ledger: a claim older than this can no longer
// collide with a live due instant (grace is hours at most).
const reminderRetention = 30 * 24 * time.Hour

// ClaimReminder inserts the ledger row on tx; claimed=false means another
// sweep (or this one, before a crash) already fired this exact instant.
func (s *Store) ClaimReminder(ctx context.Context, tx pgx.Tx, tenant, workflow, rowID string, dueAt time.Time, runID int64) (claimed bool, err error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO public.workflow_reminders (tenant_id, workflow, row_id, due_at, run_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, workflow, row_id, due_at) DO NOTHING`, tenant, workflow, rowID, dueAt, runID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ReminderFired reports whether the ledger already holds this instant (a
// read-only pre-check so a claimed row is not even fetched twice).
func (s *Store) ReminderFired(ctx context.Context, tenant, workflow, rowID string, dueAt time.Time) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_reminders WHERE tenant_id=$1 AND workflow=$2 AND row_id=$3 AND due_at=$4`,
		tenant, workflow, rowID, dueAt).Scan(&n)
	return n > 0, err
}

// PruneReminders forgets claims older than the retention.
func (s *Store) PruneReminders(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM public.workflow_reminders WHERE fired_at < now() - $1::interval`, reminderRetention.String())
	return err
}

// txKey carries the claim transaction into the run so `enqueue` steps write
// their outbox rows on it (atomic with the claim). Absent = own tx (cron and
// event runs are unchanged).
type txKey struct{}

// RunClaimed runs wf inside ONE transaction with the reminder claim: begin →
// claim (ON CONFLICT DO NOTHING) → if not claimed, roll back and return
// (false, nil) → run the steps with the tx in context → commit. The run row
// in workflow_runs is recorded outside the tx (it is a log, never the truth
// of the claim). A failed run rolls the claim back too, so the next sweep
// retries it — at-least-once for a FAILED run, exactly-once for a
// successful one; the enqueue side effect is inside the same commit.
func (e *Executor) RunClaimed(ctx context.Context, tenant string, wf *Workflow, trigger string, env map[string]any, rowID string, dueAt time.Time) (fired bool, err error) {
	tx, err := e.Store.Pool().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	runID, err := e.Store.StartRun(ctx, tenant, wf.Name, trigger)
	if err != nil {
		return false, err
	}
	claimed, err := e.Store.ClaimReminder(ctx, tx, tenant, wf.Name, rowID, dueAt, runID)
	if err != nil {
		_ = e.Store.FinishRun(context.WithoutCancel(ctx), runID, "failed", "claim: "+err.Error(), nil)
		return false, err
	}
	if !claimed {
		// Already fired (by this sweep before a crash, or by a leader that
		// died after commit). Not a run at all: drop the run row's promise.
		_ = e.Store.FinishRun(context.WithoutCancel(ctx), runID, "skipped_duplicate", "reminder already fired for this instant", nil)
		return false, nil
	}
	results, runErr := e.runSteps(context.WithValue(ctx, txKey{}, tx), tenant, wf, env)
	if runErr != nil {
		_ = e.Store.FinishRun(context.WithoutCancel(ctx), runID, "failed", runErr.Error(), results)
		return false, runErr // tx rolls back: claim released, nothing enqueued
	}
	if err := tx.Commit(ctx); err != nil {
		_ = e.Store.FinishRun(context.WithoutCancel(ctx), runID, "failed", "commit: "+err.Error(), results)
		return false, err
	}
	if ferr := e.Store.FinishRun(context.WithoutCancel(ctx), runID, "ok", "", results); ferr != nil {
		e.Log.Warn().Err(ferr).Int64("run", runID).Msg("workflows: could not record run outcome")
	}
	e.Log.Info().Str("tenant", tenant).Str("workflow", wf.Name).Str("trigger", trigger).Str("row", rowID).
		Time("due_at", dueAt).Int64("run", runID).Msg("workflows: reminder fired")
	return true, nil
}

// enqueueOn writes the outbox row on the claim transaction when one rides
// the context, else in its own small tx (the pre-existing behavior).
func (e *Executor) enqueueOn(ctx context.Context, tenant, topic string, payload any) error {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok && tx != nil {
		if _, err := outbox.Enqueue(ctx, tx, tenant, topic, payload); err != nil {
			return fmt.Errorf("enqueue %s: %w", topic, err)
		}
		return nil
	}
	return e.enqueue(ctx, tenant, topic, payload)
}

// Sweeper is the per-tick reminder pass, run by the leader from the
// scheduler's tick (so it inherits the leader lock and never runs twice).
type Sweeper struct {
	Src   TimeLister
	Exec  *Executor
	Store *Store
	// PageSize bounds one API page (the engine caps at 100).
	PageSize int
	// Now is injectable for tests.
	Now func() time.Time
}

// Sweep runs one pass over every (tenant, time workflow). Errors are logged
// per entry; one broken tenant never blocks the others.
func (s *Sweeper) Sweep(ctx context.Context) {
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	for _, e := range s.Src.TimeEntries() {
		if err := s.sweepOne(ctx, e, now); err != nil {
			s.Exec.Log.Warn().Err(err).Str("tenant", e.Tenant).Str("workflow", e.Workflow.Name).Msg("workflows: reminder sweep failed for this workflow (next tick retries)")
		}
	}
	if err := s.Store.PruneReminders(ctx); err != nil {
		s.Exec.Log.Warn().Err(err).Msg("workflows: reminder prune failed")
	}
}

// window is the band of FIELD values whose due instant is fireable now:
// due = field - offset (before) or field + offset (after); fireable when
// now - grace < due <= now.
func (w *Workflow) reminderWindow(now time.Time) (from, to time.Time) {
	off := w.Offset
	if w.Before {
		off = -off
	}
	// field = due - off  ⇒ field ∈ (now - grace - off, now - off]
	return now.Add(-w.Grace).Add(-off), now.Add(-off)
}

// DueAt is the row's due instant for this workflow.
func (w *Workflow) DueAt(fieldValue time.Time) time.Time {
	if w.Before {
		return fieldValue.Add(-w.Offset)
	}
	return fieldValue.Add(w.Offset)
}

func (s *Sweeper) sweepOne(ctx context.Context, e TimeEntry, now time.Time) error {
	wf := e.Workflow
	from, to := wf.reminderWindow(now)
	client := s.Exec.Clients(wf.Role)
	size := s.PageSize
	if size <= 0 || size > 100 {
		size = 100
	}
	after := ""
	for page := 0; page < 50; page++ { // 5 000 rows per tick per workflow — a hard ceiling, logged
		q := url.Values{}
		q.Set("filter["+wf.Field+"][gt]", from.Format(time.RFC3339Nano))
		q.Set("filter["+wf.Field+"][lte]", to.Format(time.RFC3339Nano))
		q.Set("per_page", fmt.Sprint(size))
		if after != "" {
			q.Set("after", after)
		}
		status, body, err := client.Do(ctx, e.Tenant, http.MethodGet, "/api/"+wf.Resource+"?"+q.Encode(), nil)
		if err != nil {
			return fmt.Errorf("list %s: %w", wf.Resource, err)
		}
		if status != http.StatusOK {
			return fmt.Errorf("list %s: engine answered %d: %s", wf.Resource, status, truncate(body))
		}
		var pageOut struct {
			Data []map[string]any `json:"data"`
			Meta struct {
				HasNext bool `json:"has_next"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &pageOut); err != nil {
			return fmt.Errorf("list %s: decode: %w", wf.Resource, err)
		}
		for _, rec := range pageOut.Data {
			s.fireIfDue(ctx, e, rec, now)
		}
		if !pageOut.Meta.HasNext || len(pageOut.Data) == 0 {
			return nil
		}
		last, _ := pageOut.Data[len(pageOut.Data)-1]["id"].(string)
		if last == "" {
			return nil
		}
		after = last
	}
	s.Exec.Log.Warn().Str("tenant", e.Tenant).Str("workflow", wf.Name).Msg("workflows: reminder sweep hit the 5 000-row ceiling in one tick — the rest fires on the next tick")
	return nil
}

func (s *Sweeper) fireIfDue(ctx context.Context, e TimeEntry, rec map[string]any, now time.Time) {
	wf := e.Workflow
	id, _ := rec["id"].(string)
	raw, _ := rec[wf.Field].(string)
	if id == "" || raw == "" {
		return
	}
	fieldAt, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		if fieldAt, err = time.Parse(time.RFC3339, raw); err != nil {
			return
		}
	}
	if wf.When != nil && !schema.WhenHolds(*wf.When, rec) {
		return // the row does not match the trigger's condition (cancelled, etc.)
	}
	due := wf.DueAt(fieldAt).UTC()
	if due.After(now) || now.Sub(due) > wf.Grace {
		return
	}
	if fired, err := s.Store.ReminderFired(ctx, e.Tenant, wf.Name, id, due); err != nil || fired {
		return
	}
	env := map[string]any{
		"event": map[string]any{
			"type":     "time",
			"id":       id,
			"resource": wf.Resource,
			"field":    wf.Field,
			"due_at":   due,
			"at":       fieldAt.UTC(),
			"before":   wf.Before,
			"offset":   wf.Offset.String(),
		},
		"tenant": e.Tenant,
		"now":    now,
		"record": rec,
	}
	trigger := "time:" + wf.Resource + "." + wf.Field
	if _, err := s.Exec.RunClaimed(ctx, e.Tenant, wf, trigger, env, id, due); err != nil && !errors.Is(err, context.Canceled) {
		s.Exec.Log.Warn().Err(err).Str("tenant", e.Tenant).Str("workflow", wf.Name).Str("row", id).Msg("workflows: reminder run failed (claim released; the next sweep retries while inside grace)")
	}
}
