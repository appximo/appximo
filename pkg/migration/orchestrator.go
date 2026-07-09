package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// This file implements the RESUMABLE MULTI-TENANT MIGRATION ORCHESTRATOR — the
// fan-out that applies a schema change to N tenants (N Postgres schemas) safely. It
// is the differentiator over Prisma (no native multi-tenant fan-out) and
// django-tenants (fragile under partial failure): Appitools is schema-per-tenant
// with an IDEMPOTENT diff (re-applying a converged tenant is a no-op) and a
// per-tenant advisory lock, so the fan-out is resilient and resumable by
// construction.
//
// Properties:
//   - Each tenant is applied INDEPENDENTLY (its own search_path, its own atomic
//     batches via the Executor). A failure in tenant K leaves K in its previous
//     state (the Executor rolls back per batch) and does NOT touch the others.
//   - PARTIAL FAILURE is resilient: a broken tenant is RECORDED (with its error) and
//     the fan-out CONTINUES with the rest — one bad tenant never blocks the healthy
//     ones. The run returns a per-tenant report.
//   - RESUMABLE: re-running the fan-out is safe — already-migrated tenants produce an
//     empty diff (noop, skipped for free), and the ones that failed are retried. The
//     idempotent diff makes "resume" == "run again".
//   - ADDITIVE BY DEFAULT: like the worker, the orchestrator NEVER auto-approves a
//     destructive drop. With no approved drops it is purely additive (drops gated in
//     every tenant). A mass destructive change requires explicit, enumerated approval
//     (ApprovedDrops), and a dry-run shows the AGGREGATE impact first.
//   - SEQUENTIAL (v1): one tenant at a time, under its advisory lock — simple and
//     safe on a small box. Bounded parallelism is a documented future increment.

// FanoutStatus is the per-tenant outcome of a fan-out.
type FanoutStatus string

const (
	// FanoutApplied — DDL was applied to this tenant (in a dry-run: WOULD be applied).
	FanoutApplied FanoutStatus = "applied"
	// FanoutNoop — already converged (empty diff), or only gated drops were pending.
	FanoutNoop FanoutStatus = "noop"
	// FanoutFailed — the tenant's migration errored (or its lock could not be acquired).
	// The tenant is left in its previous state and will be retried on a resume.
	FanoutFailed FanoutStatus = "failed"
)

// fanout lock policy: try the per-tenant advisory lock a few times before giving up
// (the tenant is then reported failed and retried on resume).
const (
	fanoutLockAttempts = 3
	fanoutLockBackoff  = 1 * time.Second
)

// TenantFanoutResult is the outcome for one tenant.
type TenantFanoutResult struct {
	TenantID     string       `json:"tenant_id"`
	PGSchema     string       `json:"pg_schema"`
	Status       FanoutStatus `json:"status"`
	AppliedDrops []string     `json:"applied_drops,omitempty"`
	GatedDrops   []string     `json:"gated_drops,omitempty"`
	Error        string       `json:"error,omitempty"`
	DurationMS   int64        `json:"duration_ms"`
	// Preview is populated on a dry run (the classified plan + per-drop impact).
	Preview *Preview `json:"preview,omitempty"`
}

// FanoutResult summarizes a fan-out run.
type FanoutResult struct {
	RunID          string               `json:"run_id"`
	DryRun         bool                 `json:"dry_run"`
	Total          int                  `json:"total"`
	Applied        int                  `json:"applied"`
	Noop           int                  `json:"noop"`
	Failed         int                  `json:"failed"`
	MissingTenants []string             `json:"missing_tenants,omitempty"` // requested ids not in public.tenants
	Results        []TenantFanoutResult `json:"results"`
}

// FanoutOptions configures a fan-out.
type FanoutOptions struct {
	// Schema is the desired schema applied to EVERY targeted tenant.
	Schema *schema.APISchema
	// TenantIDs is an explicit subset; empty means ALL tenants in public.tenants.
	// The caller (CLI) must opt into "all" deliberately — it never defaults silently.
	TenantIDs []string
	// ApprovedDrops are destructive-drop keys approved for EVERY targeted tenant
	// (a mass drop). Empty (the default) is additive: every drop is gated.
	ApprovedDrops []string
	// DryRun classifies + measures impact without applying or persisting anything.
	DryRun bool
	// OnTenant, if set, is called after each tenant completes (for CLI streaming).
	OnTenant func(TenantFanoutResult)
}

// DestructiveImpact is the AGGREGATE impact of one approved/gated drop across all
// tenants in a dry-run — "how much data would be lost, in how many tenants".
type DestructiveImpact struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Tenants  int    `json:"tenants"`   // tenants where this drop appears
	RowsLost int64  `json:"rows_lost"` // total rows lost across those tenants
}

// AggregateDestructive sums the data-losing drops across every tenant's preview (use
// after a dry-run). It is the informed-consent surface for a MASS destructive change.
func (r *FanoutResult) AggregateDestructive() []DestructiveImpact {
	byKey := map[string]*DestructiveImpact{}
	var order []string
	for _, tr := range r.Results {
		if tr.Preview == nil {
			continue
		}
		for _, d := range tr.Preview.Destructive {
			imp, ok := byKey[d.Key]
			if !ok {
				imp = &DestructiveImpact{Key: d.Key, Kind: d.Kind}
				byKey[d.Key] = imp
				order = append(order, d.Key)
			}
			imp.Tenants++
			imp.RowsLost += d.RowsLost
		}
	}
	out := make([]DestructiveImpact, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out
}

// RunFanout applies opts.Schema to the targeted tenants, one at a time, recording a
// per-tenant result and CONTINUING past any failure. The only error it returns is a
// setup failure (enumerating the tenants); a per-tenant migration failure is captured
// in the result, never propagated as the function's error — that is the resilience
// contract. A re-run resumes: converged tenants are noops, failed ones retry.
func RunFanout(ctx context.Context, pool *pgxpool.Pool, opts FanoutOptions) (*FanoutResult, error) {
	if opts.Schema == nil {
		return nil, fmt.Errorf("RunFanout: a schema is required")
	}
	tenants, missing, err := enumerateTenants(ctx, pool, opts.TenantIDs)
	if err != nil {
		return nil, fmt.Errorf("enumerate tenants: %w", err)
	}
	runID := "fanout-" + time.Now().UTC().Format("20060102T150405Z")
	res := &FanoutResult{RunID: runID, DryRun: opts.DryRun, MissingTenants: missing}

	mode := "apply"
	if opts.DryRun {
		mode = "dry-run"
	}
	log.Printf("migration: fan-out %s [%s] over %d tenant(s) (additive=%t)",
		runID, mode, len(tenants), len(opts.ApprovedDrops) == 0)

	for _, t := range tenants {
		tr := applyToTenant(ctx, pool, t, opts, runID)
		res.Results = append(res.Results, tr)
		res.Total++
		switch tr.Status {
		case FanoutApplied:
			res.Applied++
		case FanoutNoop:
			res.Noop++
		case FanoutFailed:
			res.Failed++
		}
		if opts.OnTenant != nil {
			opts.OnTenant(tr)
		}
	}
	log.Printf("migration: fan-out %s done — %d applied, %d noop, %d failed (of %d)",
		runID, res.Applied, res.Noop, res.Failed, res.Total)
	return res, nil
}

// applyToTenant runs the fan-out for ONE tenant: dry-run previews, apply locks →
// migrates → persists → logs. It never panics out; every failure becomes a failed
// result so the orchestrator can continue.
func applyToTenant(ctx context.Context, pool *pgxpool.Pool, t tenantRow, opts FanoutOptions, runID string) TenantFanoutResult {
	start := time.Now()
	tr := TenantFanoutResult{TenantID: t.id, PGSchema: t.pgSchema}
	finish := func(status FanoutStatus, errStr string) TenantFanoutResult {
		tr.Status = status
		tr.Error = errStr
		tr.DurationMS = time.Since(start).Milliseconds()
		return tr
	}

	// ── dry run: classify + measure impact, mutate nothing (no lock, read-only) ──
	if opts.DryRun {
		pv, err := PreviewTenantMigration(ctx, pool, t.pgSchema, opts.Schema, opts.ApprovedDrops)
		if err != nil {
			return finish(FanoutFailed, err.Error())
		}
		tr.Preview = pv
		if pv.Empty {
			return finish(FanoutNoop, "")
		}
		return finish(FanoutApplied, "") // "would apply"
	}

	// ── apply: serialize on the per-tenant advisory lock (vs the worker / a PUT) ──
	var outcome *ApplyOutcome
	acquired, fnErr := withTenantLock(ctx, pool, t.pgSchema, func() error {
		o, err := ApplyTenantMigrationApproved(ctx, pool, t.pgSchema, opts.Schema, opts.ApprovedDrops)
		if err != nil {
			return err
		}
		// Persist the applied schema so the tenant record reflects it (and the
		// schema_updated trigger invalidates its cache) — only AFTER a successful
		// migration, so a failed tenant keeps its old record, consistent with its
		// unchanged tables.
		if err := persistTenantSchema(ctx, pool, t.id, opts.Schema, runID); err != nil {
			return fmt.Errorf("persist schema: %w", err)
		}
		outcome = o
		return nil
	})
	if !acquired {
		msg := "could not acquire the tenant's migration advisory lock (another migration in progress) — will retry on resume"
		logFanout(ctx, pool, t.id, "failed", runID, msg)
		return finish(FanoutFailed, msg)
	}
	if fnErr != nil {
		logFanout(ctx, pool, t.id, "failed", runID, fnErr.Error())
		return finish(FanoutFailed, fnErr.Error())
	}

	tr.AppliedDrops = outcome.AppliedDrops
	tr.GatedDrops = outcome.GatedDrops
	status := FanoutApplied
	detail := "applied"
	if outcome.NoChange {
		status = FanoutNoop
		detail = "no-op (already converged)"
	}
	logFanout(ctx, pool, t.id, "ok", runID, detail)
	return finish(status, "")
}

// ── helpers ─────────────────────────────────────────────────────────────────────

type tenantRow struct{ id, pgSchema string }

// enumerateTenants lists the targeted tenants from public.tenants (ordered, so a run
// is deterministic). With ids empty it returns ALL tenants; otherwise only those that
// exist, plus the requested ids that were NOT found (so a typo surfaces, not silently).
func enumerateTenants(ctx context.Context, pool *pgxpool.Pool, ids []string) (rows []tenantRow, missing []string, err error) {
	q := `SELECT id, pg_schema FROM public.tenants`
	var args []any
	if len(ids) > 0 {
		q += ` WHERE id = ANY($1)`
		args = append(args, ids)
	}
	q += ` ORDER BY id`
	rs, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rs.Close()
	found := map[string]bool{}
	for rs.Next() {
		var r tenantRow
		if err := rs.Scan(&r.id, &r.pgSchema); err != nil {
			return nil, nil, err
		}
		rows = append(rows, r)
		found[r.id] = true
	}
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		if !found[id] {
			missing = append(missing, id)
		}
	}
	return rows, missing, nil
}

// withTenantLock acquires the per-tenant advisory lock on a dedicated connection,
// runs fn, and releases the lock. It returns (false, nil) when the lock is held by
// another process after fanoutLockAttempts — the caller then reports the tenant
// failed (retried on resume). This is the SAME lock the Redis worker uses, so a
// fan-out and the worker never migrate the same tenant concurrently.
func withTenantLock(ctx context.Context, pool *pgxpool.Pool, pgSchema string, fn func() error) (acquired bool, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()

	key := tenantLockKey(pgSchema)
	var locked bool
	for attempt := 0; attempt < fanoutLockAttempts; attempt++ {
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil {
			return false, err
		}
		if locked {
			break
		}
		if attempt == fanoutLockAttempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(fanoutLockBackoff):
		}
	}
	if !locked {
		return false, nil
	}
	defer func() {
		// Always release on a fresh context so a cancelled run still unlocks.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		conn.Exec(uctx, "SELECT pg_advisory_unlock($1)", key) //nolint:errcheck
	}()
	return true, fn()
}

// persistTenantSchema writes the applied schema to the tenant record. The
// schema_updated trigger (migrations/001_control_plane.sql) fires pg_notify on this
// UPDATE, so the engine's caches invalidate exactly as they do for a per-tenant PUT.
// It also appends the schema to the tenant's version history (VERSION-S1) — the
// history mirrors json_schema on EVERY persist path; a fan-out re-run over a
// converged tenant is an unchanged-hash no-op, so resuming never spams versions.
func persistTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenantID string, s *schema.APISchema, runID string) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err = pool.Exec(ctx,
		`UPDATE public.tenants SET json_schema = $2, updated_at = now() WHERE id = $1`,
		tenantID, b); err != nil {
		return err
	}
	if _, _, histErr := schemahistory.Append(ctx, pool, tenantID, b, schemahistory.SourceFanout, runID); histErr != nil {
		log.Printf("WARNING: schema history append for tenant %q (fan-out %s) failed: %v", tenantID, runID, histErr)
	}
	return nil
}

// logFanout records one tenant's fan-out outcome in public.migration_log (the
// existing per-tenant migration audit table), stamping the run id in `changes` so a
// run's rows are groupable. status is "ok" (applied/noop) or "failed". Best-effort —
// a logging failure never fails the migration.
func logFanout(ctx context.Context, pool *pgxpool.Pool, tenantID, status, runID, detail string) {
	changes := "fanout:" + runID
	var errCol string
	if status == "failed" {
		errCol = detail
		changes += " | failed"
	} else if detail != "" {
		changes += " | " + detail
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.migration_log (tenant_id, status, changes, error, started_at, finished_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), now(), now())`,
		tenantID, status, changes, errCol); err != nil {
		log.Printf("migration: fan-out %s: log for tenant %q: %v", runID, tenantID, err)
	}
}
