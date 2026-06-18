package migration

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// This file implements the DRY-RUN preview: it computes the plan that would converge
// a tenant to a new schema and classifies it WITHOUT applying anything — what will be
// applied safely, what is gated pending approval (the data-losing drops, each with
// its measured impact), and what is left as additive drift. It is the informed-
// consent surface: an operator (or the future AI layer) sees exactly what will happen,
// and what data a drop would destroy, BEFORE approving. The preview reuses the same
// classifier (partitionByPolicyApproved) the apply uses, so it is faithful by
// construction — what the dry-run reports for a given approval set is exactly what the
// apply does with it.

// DestructiveOp is one data-losing drop in a preview, with the IMPACT of applying it
// (how much data is destroyed) and the KEY an operator enumerates to approve it.
type DestructiveOp struct {
	Key       string `json:"key"`        // approval token: "<table>" or "<table>.<column>"
	Kind      string `json:"kind"`       // "table" | "column"
	Table     string `json:"table"`      // the (post-rename) table name
	Column    string `json:"column"`     // "" for a table drop
	RowsLost  int64  `json:"rows_lost"`  // rows whose data is destroyed (table: all rows; column: rows with a non-null value)
	TableRows int64  `json:"table_rows"` // total rows in the table (context)
	Approved  bool   `json:"approved"`   // whether this op's key is in the supplied approval set
	Summary   string `json:"summary"`    // human one-liner with the impact
}

// Preview is the classified result of a dry run.
type Preview struct {
	PGSchema string `json:"pg_schema"`
	// Empty is true when the schema is already converged — nothing to do.
	Empty bool `json:"empty"`
	// Apply are the SAFE operations that will run (creates, adds, alters, renames, FK
	// adds, and any FK drop required as a consequence of an approved table drop). The
	// data-losing drops are NOT here — they live in Destructive with their impact.
	Apply []string `json:"apply,omitempty"`
	// Destructive are the data-losing drops with impact and approval status. A drop is
	// applied only when Approved is true; otherwise it is gated (drift).
	Destructive []DestructiveOp `json:"destructive,omitempty"`
	// Drift are the SAFE drops (index/constraint removals) left as additive drift —
	// not approvable in v1, they simply stay.
	Drift []string `json:"drift,omitempty"`
	// Concerns are backfill/transformational risks on PRE-EXISTING tables (e.g. a NOT
	// NULL added over populated data), surfaced before applying.
	Concerns []string `json:"concerns,omitempty"`
	// UnmatchedApprovals are supplied approval tokens that matched no destructive op
	// in this plan (a typo, or an already-applied drop).
	UnmatchedApprovals []string `json:"unmatched_approvals,omitempty"`
}

// HasDestructive reports whether the preview contains any data-losing drop (whether
// or not it is approved) — the signal that an apply needs informed consent.
func (p *Preview) HasDestructive() bool { return len(p.Destructive) > 0 }

// PendingDestructive reports whether any data-losing drop is present but NOT approved
// — i.e. an apply with this approval set would still leave a gated drop.
func (p *Preview) PendingDestructive() bool {
	for _, d := range p.Destructive {
		if !d.Approved {
			return true
		}
	}
	return false
}

// PreviewTenantMigration computes and classifies the migration plan that would
// converge pgSchema to s, WITHOUT applying anything. `approved` is the (optional) set
// of destructive-drop keys being considered — pass nil to see every drop gated (the
// "what could I approve" view), or a set to see exactly what an apply with that set
// would do. For each data-losing drop it queries the live table for the impact (rows
// that would be lost).
func PreviewTenantMigration(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, approved []string) (*Preview, error) {
	// The preview always includes orphan (removed-resource) tables so they are visible
	// as approvable DropTables.
	plan, err := diffTenant(ctx, pool, pgSchema, s, true)
	if err != nil {
		return nil, err
	}
	pv := &Preview{PGSchema: pgSchema}
	if plan.Empty() {
		pv.Empty = true
		return pv, nil
	}

	approvedSet := toSet(approved)
	applyPlan, gated, appliedKeys := partitionByPolicyApproved(plan, approvedSet)
	appliedSet := toSet(appliedKeys)

	// Resolve a desired/post-rename table name to its CURRENT physical name, so an
	// impact count on a table renamed in the SAME plan reads the right (old) table.
	physical := map[string]string{}
	for _, op := range plan.Ops {
		if rt, ok := op.(schemadiff.RenameTable); ok {
			physical[rt.To] = rt.From
		}
	}
	physName := func(t string) string {
		if f, ok := physical[t]; ok {
			return f
		}
		return t
	}

	// Apply list — the SAFE ops that will run (destructive drops are reported below).
	for _, op := range applyPlan.Ops {
		if schemadiff.IsDestructive(op) {
			continue
		}
		pv.Apply = append(pv.Apply, op.String())
	}

	// Destructive ops — every data-losing drop in the plan, with its measured impact
	// and whether it is approved (and thus part of Apply).
	for _, op := range plan.Ops {
		if !schemadiff.IsDestructive(op) {
			continue
		}
		key, _ := schemadiff.DestructiveKey(op)
		d, err := describeDestructive(ctx, pool, pgSchema, op, key, physName, appliedSet[key])
		if err != nil {
			return nil, err
		}
		pv.Destructive = append(pv.Destructive, d)
	}

	// Drift — the gated SAFE drops (the gated destructive ones are in pv.Destructive).
	for _, op := range gated {
		if schemadiff.IsDestructive(op) {
			continue
		}
		pv.Drift = append(pv.Drift, op.String())
	}

	// Concerns on pre-existing tables (backfill / type change).
	for _, c := range concernsOnExistingTables(plan) {
		pv.Concerns = append(pv.Concerns, fmt.Sprintf("[%s] %s", c.Risk, c.Message))
	}

	// Approval tokens that matched no destructive op.
	for _, k := range approved {
		if !appliedSet[k] {
			pv.UnmatchedApprovals = append(pv.UnmatchedApprovals, k)
		}
	}
	return pv, nil
}

// describeDestructive measures the impact of one data-losing drop against the live
// table and returns a DestructiveOp. For a column drop, RowsLost is the count of rows
// holding a non-null value (the data destroyed); for a table drop it is the total row
// count.
func describeDestructive(ctx context.Context, pool *pgxpool.Pool, pgSchema string, op schemadiff.Operation, key string, physName func(string) string, approved bool) (DestructiveOp, error) {
	switch o := op.(type) {
	case schemadiff.DropColumn:
		phys := physName(o.Table)
		total, err := countRowsAll(ctx, pool, pgSchema, phys)
		if err != nil {
			return DestructiveOp{}, fmt.Errorf("impact count for %s: %w", key, err)
		}
		lost, err := countRowsNonNull(ctx, pool, pgSchema, phys, o.Column.Name)
		if err != nil {
			return DestructiveOp{}, fmt.Errorf("impact count for %s: %w", key, err)
		}
		return DestructiveOp{
			Key: key, Kind: "column", Table: o.Table, Column: o.Column.Name,
			RowsLost: lost, TableRows: total, Approved: approved,
			Summary: fmt.Sprintf("DROP COLUMN %s.%s — %d of %d row(s) hold a value that will be permanently lost",
				o.Table, o.Column.Name, lost, total),
		}, nil
	case schemadiff.DropTable:
		total, err := countRowsAll(ctx, pool, pgSchema, o.Table.Name)
		if err != nil {
			return DestructiveOp{}, fmt.Errorf("impact count for %s: %w", key, err)
		}
		return DestructiveOp{
			Key: key, Kind: "table", Table: o.Table.Name,
			RowsLost: total, TableRows: total, Approved: approved,
			Summary: fmt.Sprintf("DROP TABLE %s — %d row(s) will be permanently deleted", o.Table.Name, total),
		}, nil
	default:
		return DestructiveOp{}, fmt.Errorf("describeDestructive: not a destructive op: %T", op)
	}
}

// countRowsAll returns the total row count of pgSchema.table.
func countRowsAll(ctx context.Context, pool *pgxpool.Pool, pgSchema, table string) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + pgx.Identifier{pgSchema, table}.Sanitize()
	if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// countRowsNonNull returns how many rows of pgSchema.table hold a non-null value in
// col — the rows whose data a DROP COLUMN would destroy.
func countRowsNonNull(ctx context.Context, pool *pgxpool.Pool, pgSchema, table, col string) (int64, error) {
	var n int64
	q := "SELECT count(*) FROM " + pgx.Identifier{pgSchema, table}.Sanitize() +
		" WHERE " + pgx.Identifier{col}.Sanitize() + " IS NOT NULL"
	if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// toSet turns a key slice into a lookup set; nil for an empty slice (a nil map reads
// as "absent" for every key, the fail-safe default — no approval means no drop).
func toSet(keys []string) map[string]bool {
	if len(keys) == 0 {
		return nil
	}
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}
