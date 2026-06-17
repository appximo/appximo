package migration

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// ApplyTenantMigration converges the resource tables in pgSchema to the tenant's
// schema, using the real migration engine (pkg/schemadiff): introspect the live
// state, build the desired state, diff into a typed plan, and apply it through the
// production-safe executor (lock_timeout + retry, NOT VALID/VALIDATE, CONCURRENTLY
// partitioning, data-preserving renames).
//
// This REPLACES the historical converger (CREATE TABLE / ADD COLUMN IF NOT EXISTS),
// which lost data on a rename, silently discarded NOT NULL, no-op'd a type change
// and took locks unguarded (docs/MIGRATION_DIAG.md). The engine now migrates for
// real: it detects what changed and applies it safely.
//
// v1 policy (documented):
//   - Provisioning a NEW tenant is identical to before: the diff against an empty
//     schema is all CreateTable, applied as it always was.
//   - Re-applying an UNCHANGED schema is a true no-op: the diff is empty, no DDL
//     runs (introspect is the only DB read).
//   - DROP operations are NEVER applied — they are logged and left as drift,
//     exactly the converger's "never removes anything" contract. This guarantees
//     no data loss (strictly not worse than before) and that a minor modeling gap
//     can never spuriously undo a converger artifact. A field removed from the
//     schema leaves its column in place (docs/MIGRATION_DIAG.md case D, unchanged).
//   - Every NON-drop change IS applied faithfully: a rename preserves data, a real
//     NOT NULL is enforced (over populated data it fails loudly and rolls back
//     atomically — never the converger's silent NULL-accepting divergence), a type
//     change is a real ALTER … TYPE. Planning concerns (backfill/transformational)
//     are logged before applying.
//
// Tables physically present but NOT declared as resources — the engine's own
// auth_*/files tables, or a resource dropped from the schema — are excluded from
// the diff (managedSubset) so the migration only ever touches resource tables.
//
// The signature is unchanged, so every call site (tenant registration, PUT schema,
// the Redis worker, the `migrate` CLI) is untouched.
func ApplyTenantMigration(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema) error {
	plan, err := diffTenant(ctx, pool, pgSchema, s)
	if err != nil {
		return err
	}
	if plan.Empty() {
		return nil // converged — nothing to do
	}

	// Surface every planning concern (backfill / destructive / transformational)
	// before touching the database — the up-front signal a future approval gate uses.
	logConcerns(pgSchema, plan)

	applyPlan, skipped := partitionByPolicy(plan)
	for _, op := range skipped {
		log.Printf("migration[%s]: SKIPPED (v1 is additive — never drops; data preserved as drift): %s", pgSchema, op.String())
	}
	if applyPlan.Empty() {
		return nil
	}

	ex := &schemadiff.Executor{Pool: pool, Schema: pgSchema}
	if err := ex.Apply(ctx, applyPlan); err != nil {
		return fmt.Errorf("apply migration %s: %w", pgSchema, err)
	}
	return nil
}

// diffTenant computes the typed plan that converges pgSchema to s: introspect the
// real state, build the desired state from the tenant JSON, restrict the real
// state to the managed (resource) tables, and diff. Exposed (unexported, same
// package) so tests can assert no-op convergence without observing DDL side effects.
func diffTenant(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema) (*schemadiff.Plan, error) {
	real, err := schemadiff.Introspect(ctx, pool, pgSchema)
	if err != nil {
		return nil, fmt.Errorf("introspect %s: %w", pgSchema, err)
	}
	desired := buildDesiredSchema(pgSchema, s)
	current := managedSubset(real, desired)
	plan, err := schemadiff.Diff(desired, current)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", pgSchema, err)
	}
	return plan, nil
}

// managedSubset returns a view of real keeping only the tables the API schema
// declares (those present in desired). Tables that exist physically but are not
// resources — the engine's own auth_*/files tables, or a resource removed from the
// schema — are excluded, so the diff never proposes dropping a table the migration
// does not own. Enums are not copied (the API schema does not manage enum types,
// and Diff ignores them regardless).
func managedSubset(real, desired *schemadiff.Schema) *schemadiff.Schema {
	out := schemadiff.NewSchema(real.Name)
	for name, tbl := range real.Tables {
		if _, ok := desired.Tables[name]; ok {
			out.Tables[name] = tbl
		}
	}
	return out
}

// partitionByPolicy splits an ordered plan into the ops to APPLY and the DROP ops
// to SKIP. v1 is purely additive/in-place — it creates, adds, alters and renames,
// but never drops — preserving the converger's "removes nothing" guarantee while
// applying everything else through the safe executor. Skipped ops keep their data
// as drift (the columns/indexes simply remain).
func partitionByPolicy(plan *schemadiff.Plan) (apply *schemadiff.Plan, skipped []schemadiff.Operation) {
	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		if isDropOp(op.Kind()) {
			skipped = append(skipped, op)
			continue
		}
		keep = append(keep, op)
	}
	return &schemadiff.Plan{Ops: keep}, skipped
}

// logConcerns surfaces a plan's backfill/destructive/transformational concerns,
// EXCEPT those acting on a table created in the same plan: a constraint or column
// added to a brand-new, empty tenant table is no data risk, so logging it would
// fire a misleading "backfill" warning on every fresh-tenant provision. Concerns on
// pre-existing tables (the cases that actually matter) are still surfaced.
func logConcerns(pgSchema string, plan *schemadiff.Plan) {
	created := make(map[string]bool)
	for _, op := range plan.Ops {
		if ct, ok := op.(schemadiff.CreateTable); ok {
			created[ct.Table.Name] = true
		}
	}
	for _, c := range schemadiff.Validate(plan) {
		if t := opTable(c.Op); t != "" && created[t] {
			continue
		}
		log.Printf("migration[%s]: concern [%s]: %s", pgSchema, c.Risk, c.Message)
	}
}

// opTable returns the table an operation targets (the desired/post-rename name).
func opTable(op schemadiff.Operation) string {
	switch o := op.(type) {
	case schemadiff.CreateTable:
		return o.Table.Name
	case schemadiff.DropTable:
		return o.Table.Name
	case schemadiff.RenameTable:
		return o.To
	case schemadiff.AddColumn:
		return o.Table
	case schemadiff.DropColumn:
		return o.Table
	case schemadiff.AlterColumn:
		return o.Table
	case schemadiff.RenameColumn:
		return o.Table
	case schemadiff.AddPrimaryKey:
		return o.Table
	case schemadiff.DropPrimaryKey:
		return o.Table
	case schemadiff.AddForeignKey:
		return o.Table
	case schemadiff.DropForeignKey:
		return o.Table
	case schemadiff.AddUnique:
		return o.Table
	case schemadiff.DropUnique:
		return o.Table
	case schemadiff.AddCheck:
		return o.Table
	case schemadiff.DropCheck:
		return o.Table
	case schemadiff.AddIndex:
		return o.Table
	case schemadiff.DropIndex:
		return o.Table
	}
	return ""
}

// isDropOp reports whether an op kind removes a schema object (table, column,
// index or constraint) — the operations v1 gates rather than applies.
func isDropOp(k schemadiff.OpKind) bool {
	switch k {
	case schemadiff.OpDropTable, schemadiff.OpDropColumn, schemadiff.OpDropIndex,
		schemadiff.OpDropUnique, schemadiff.OpDropCheck, schemadiff.OpDropPrimaryKey,
		schemadiff.OpDropForeignKey:
		return true
	}
	return false
}

// fkIndexTarget names one (table, column) pair to index for a declared relation.
type fkIndexTarget struct{ table, column string }

// relationIndexTargets returns the (table, column) pairs that must be indexed for
// resName's declared relations:
//
//	has_many     → FK on the TARGET (child) table     (child.<fk> = parent.id)
//	belongs_to   → FK on THIS (source) table          (target.id  = source.<fk>)
//	many_to_many → both FK columns on the THROUGH table
func relationIndexTargets(resName string, res schema.ResourceSchema) []fkIndexTarget {
	var out []fkIndexTarget
	for _, rel := range res.Relations {
		switch rel.Type {
		case schema.RelationHasMany:
			out = append(out, fkIndexTarget{rel.Target, rel.FK})
		case schema.RelationBelongsTo:
			out = append(out, fkIndexTarget{resName, rel.FK})
		case schema.RelationManyToMany:
			out = append(out, fkIndexTarget{rel.Through, rel.FK})
			out = append(out, fkIndexTarget{rel.Through, rel.TargetFK})
		}
	}
	return out
}
