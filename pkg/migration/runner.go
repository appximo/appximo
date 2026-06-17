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

	// Foreign-key additions are applied with a TRANSITION-TOLERANT policy (see
	// applyForeignKeys), separate from the rest of the plan: the ADD … NOT VALID
	// commits to protect every NEW write immediately, and VALIDATE is best-effort
	// over pre-existing data. The remaining ops go through the standard executor
	// (which fails atomically, as it must). FKs reference tables created by the
	// non-FK plan, so they are applied AFTER it.
	nonFK, fkAdds := splitForeignKeyAdds(applyPlan)

	ex := &schemadiff.Executor{Pool: pool, Schema: pgSchema}
	if err := ex.Apply(ctx, nonFK); err != nil {
		return fmt.Errorf("apply migration %s: %w", pgSchema, err)
	}
	applyForeignKeys(ctx, ex, pgSchema, fkAdds)
	return nil
}

// splitForeignKeyAdds separates AddForeignKey ops from the rest of a plan so the
// FKs can be applied with the transition-tolerant policy while everything else
// goes through the standard (fail-atomic) executor.
func splitForeignKeyAdds(plan *schemadiff.Plan) (nonFK *schemadiff.Plan, fkAdds []schemadiff.Operation) {
	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		if op.Kind() == schemadiff.OpAddForeignKey {
			fkAdds = append(fkAdds, op)
			continue
		}
		keep = append(keep, op)
	}
	return &schemadiff.Plan{Ops: keep}, fkAdds
}

// applyForeignKeys adds each foreign key with a policy made for the transition of
// EXISTING tenants (their FK columns predate the constraint):
//
//  1. `ADD CONSTRAINT … NOT VALID` is committed FIRST — the FK immediately enforces
//     every NEW insert/update/delete (forward protection), under lock_timeout+retry.
//  2. `VALIDATE CONSTRAINT` is attempted as a SEPARATE step. On a CONSISTENT tenant
//     (or any brand-new/empty table) it succeeds and the FK is fully validated. On a
//     tenant carrying historical orphan rows it FAILS — and the FK is LEFT NOT VALID:
//     it still guards forward, the drift is logged for an operator to fix (then VALIDATE
//     manually), and provisioning is NOT broken.
//
// This is the documented v1 policy. A hard failure of the ADD itself (not a VALIDATE
// failure) is logged and skipped so one problematic FK never aborts the whole
// migration of the rest of the schema.
func applyForeignKeys(ctx context.Context, ex *schemadiff.Executor, pgSchema string, fkAdds []schemadiff.Operation) {
	for _, op := range fkAdds {
		stmts, err := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{op}})
		if err != nil || len(stmts) == 0 {
			log.Printf("migration[%s]: foreign key render failed, skipped: %s: %v", pgSchema, op.String(), err)
			continue
		}
		// stmts[0] = ADD CONSTRAINT … NOT VALID ; stmts[1] = VALIDATE CONSTRAINT.
		if err := ex.Exec(ctx, stmts[0].SQL); err != nil {
			log.Printf("migration[%s]: foreign key add failed, skipped (schema unprotected for this relation): %s: %v", pgSchema, op.String(), err)
			continue
		}
		for _, st := range stmts[1:] {
			if err := ex.Exec(ctx, st.SQL); err != nil {
				log.Printf("migration[%s]: foreign key left NOT VALID — pre-existing rows violate it (NEW writes ARE protected; fix the orphan data then VALIDATE manually): %s: %v",
					pgSchema, op.String(), err)
			}
		}
	}
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
	// Reconcile rename intent against the live DB BEFORE diffing: keep a rename only
	// while it is pending (old name present, new absent), and remember the table
	// rename sources so managedSubset does not drop them.
	keepSources := resolveRenames(desired, real)
	current := managedSubset(real, desired, keepSources)
	plan, err := schemadiff.Diff(desired, current)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", pgSchema, err)
	}
	return plan, nil
}

// resolveRenames reconciles the declared rename intent (RenamedFrom, set by
// buildDesiredSchema from the schema's renamed_from) against the LIVE database, so
// the diff emits an ALTER RENAME only for a rename that is actually PENDING — and is
// otherwise inert:
//
//   - A table/column rename whose OLD name IS present in real and whose NEW name is
//     NOT yet present is a pending rename: the intent is kept, and (for a table) the
//     old name is recorded in keepSources so managedSubset keeps the rename source.
//   - Otherwise — the rename is already applied (the new name exists), or the old
//     name is absent — the intent is CLEARED, so the diff matches by the current name
//     (a no-op once renamed) instead of erroring on a missing rename source, and a
//     rename onto an already-existing target is skipped (left as drift) rather than
//     failing.
//
// This makes a renamed_from declaration safe to LEAVE in the schema after it is
// applied: re-provisioning is a clean no-op.
func resolveRenames(desired, real *schemadiff.Schema) (keepSources map[string]bool) {
	keepSources = make(map[string]bool)
	for _, dt := range desired.Tables {
		if dt.RenamedFrom != "" {
			_, oldExists := real.Tables[dt.RenamedFrom]
			_, newExists := real.Tables[dt.Name]
			if oldExists && !newExists {
				keepSources[dt.RenamedFrom] = true // pending table rename
				// Postgres rewrites every FK that targets a renamed table, so align
				// the real model's reftables old→new — otherwise a FK pointing at the
				// renamed table would diff as drop+add (a spurious duplicate).
				rewriteRefTable(real, dt.RenamedFrom, dt.Name)
			} else {
				dt.RenamedFrom = "" // already applied / inapplicable → inert
			}
		}
		// Column renames are checked against whichever real table this desired table
		// maps to: the rename source if the table itself is being renamed, else its
		// own current name.
		realTbl := real.Tables[dt.Name]
		if dt.RenamedFrom != "" {
			realTbl = real.Tables[dt.RenamedFrom]
		}
		for _, col := range dt.Columns {
			if col.RenamedFrom == "" {
				continue
			}
			if realTbl == nil {
				col.RenamedFrom = ""
				continue
			}
			_, oldCol := realTbl.Columns[col.RenamedFrom]
			_, newCol := realTbl.Columns[col.Name]
			if oldCol && !newCol {
				// Pending column rename. Postgres preserves the column's FK / unique /
				// index through ALTER RENAME COLUMN, so rewrite the real model's
				// constraint/index column refs old→new — otherwise they would diff as
				// drop+add (a spurious duplicate on the renamed column).
				rewriteColumnRefs(realTbl, col.RenamedFrom, col.Name)
			} else {
				col.RenamedFrom = "" // already applied / inapplicable → inert
			}
		}
	}
	return keepSources
}

// rewriteRefTable repoints every foreign key in s that REFERENCES table old to
// reference new (mirrors Postgres updating FK references when a table is renamed).
func rewriteRefTable(s *schemadiff.Schema, old, new string) {
	for _, tbl := range s.Tables {
		for _, fk := range tbl.FKs {
			if fk.RefTable == old {
				fk.RefTable = new
			}
		}
	}
}

// rewriteColumnRefs repoints a table's PK / FK / unique / index column references
// from old to new (mirrors Postgres preserving them across ALTER RENAME COLUMN).
func rewriteColumnRefs(tbl *schemadiff.Table, old, new string) {
	repl := func(cols []string) {
		for i, c := range cols {
			if c == old {
				cols[i] = new
			}
		}
	}
	if tbl.PK != nil {
		repl(tbl.PK.Columns)
	}
	for _, fk := range tbl.FKs {
		repl(fk.Columns)
	}
	for _, u := range tbl.Uniques {
		repl(u.Columns)
	}
	for _, idx := range tbl.Indexes {
		repl(idx.Columns)
	}
}

// managedSubset returns a view of real keeping only the tables the API schema
// declares (those present in desired) PLUS any table that is the source of a pending
// rename (keepSources). Tables that exist physically but are not resources — the
// engine's own auth_*/files tables, or a resource removed from the schema — are
// excluded, so the diff never proposes dropping a table the migration does not own.
// Enums are not copied (the API schema does not manage enum types, and Diff ignores
// them regardless).
func managedSubset(real, desired *schemadiff.Schema, keepSources map[string]bool) *schemadiff.Schema {
	out := schemadiff.NewSchema(real.Name)
	for name, tbl := range real.Tables {
		if _, ok := desired.Tables[name]; ok || keepSources[name] {
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
