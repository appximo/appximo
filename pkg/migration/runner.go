package migration

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/files"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// schemaHasFileFields reports whether any resource declares a `file` field
// (FILES-LINK-S1) — the trigger for ensuring the tenant files table exists
// before its FK is added.
func schemaHasFileFields(s *schema.APISchema) bool {
	for _, res := range s.Resources {
		for _, f := range res.Fields {
			if f.Type == "file" {
				return true
			}
		}
	}
	return false
}

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
// This is the PURELY ADDITIVE entry point: it gates EVERY drop (no approval is
// possible through it), exactly the v1 policy. It is used by tenant registration (a
// brand-new tenant has no drops anyway) and the Redis migration worker — an
// AUTOMATED process must NEVER drop data without prior, recorded human approval, so
// the worker can only ever create/add/alter/rename. To apply an explicitly approved
// destructive drop, use ApplyTenantMigrationApproved (the control-plane PUT / CLI
// path), which is the only place a drop is ever executed.
//
// v1 policy (documented):
//   - Provisioning a NEW tenant is identical to before: the diff against an empty
//     schema is all CreateTable, applied as it always was.
//   - Re-applying an UNCHANGED schema is a true no-op: the diff is empty, no DDL
//     runs (introspect is the only DB read).
//   - DROP operations are NEVER applied through THIS function — they are logged and
//     left as drift, exactly the converger's "never removes anything" contract. This
//     guarantees no data loss and that a minor modeling gap can never spuriously undo
//     a converger artifact. A field removed from the schema leaves its column in
//     place (docs/MIGRATION_DIAG.md case D, unchanged).
//   - Every NON-drop change IS applied faithfully: a rename preserves data, a real
//     NOT NULL is enforced (over populated data it fails loudly and rolls back
//     atomically — never the converger's silent NULL-accepting divergence), a type
//     change is a real ALTER … TYPE. Planning concerns (backfill/transformational)
//     are logged before applying.
//
// Tables physically present but NOT declared as resources — the engine's own
// auth_*/files tables, or a resource dropped from the schema — are excluded from
// the diff (managedSubset) so the additive migration only ever touches resource
// tables. (The approval-aware path additionally SURFACES a removed-resource table
// as a gated/approvable DropTable; see ApplyTenantMigrationApproved.)
//
// The signature is unchanged, so every additive call site (tenant registration, the
// Redis worker) is untouched.
func ApplyTenantMigration(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema) error {
	_, err := applyMigration(ctx, pool, pgSchema, s, nil, false)
	return err
}

// ApplyOutcome reports what a migration apply did with the data-losing drops in the
// plan: which approved drops it APPLIED, which it GATED (present but not approved,
// kept as drift), and which approval tokens matched NOTHING (a typo or an
// already-applied drop). It carries no error semantics — an apply that fails returns
// an error instead.
type ApplyOutcome struct {
	AppliedDrops       []string // destructive keys applied (explicitly approved)
	GatedDrops         []string // destructive keys present but NOT approved (drift)
	UnmatchedApprovals []string // approved keys that matched no destructive op
	// NoChange is true when NO DDL was applied — the schema was already converged, or
	// the only pending operations were gated drops. It is the noop signal the
	// multi-tenant orchestrator uses to distinguish an "already up to date" tenant
	// from one it actually migrated.
	NoChange bool
}

// ApplyTenantMigrationApproved is the APPROVAL-AWARE apply: it converges pgSchema to
// the desired schema and, for the data-losing drops (DropTable / DropColumn), applies
// ONLY those whose approval key (schemadiff.DestructiveKey) appears in `approved`.
// Every other drop stays gated as drift, exactly as the additive policy. With an
// empty `approved`, it is fail-safe: NOTHING is dropped (identical net effect to the
// additive path, plus visibility of what COULD be approved).
//
// This is the ONLY function that ever executes a destructive drop, and only by
// explicit, enumerated consent. It is wired to the control-plane PUT (after a
// dry-run preview) and the CLI `migrate --approve-drops`. It is NEVER reachable from
// the Redis worker (which must not auto-approve).
//
// Preview↔apply consistency is structural: the plan is recomputed FRESH against the
// live database here, and a drop is applied iff its key is approved. A destructive
// drop that appeared since the preview carries a different (un-approved) key, so it
// is gated automatically — a new, unreviewed drop can never slip through.
func ApplyTenantMigrationApproved(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, approved []string) (*ApplyOutcome, error) {
	return applyMigration(ctx, pool, pgSchema, s, approved, true)
}

// applyMigration is the shared core. includeOrphans makes a removed-resource table
// visible to the diff as a (gated/approvable) DropTable: false for the additive path
// (byte-identical to before — a removed resource is invisible drift), true for the
// approval-aware path (so a table can be previewed and approved for dropping).
func applyMigration(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, approved []string, includeOrphans bool) (*ApplyOutcome, error) {
	// A `file` field's FK targets the engine's per-tenant files table
	// (FILES-LINK-S1), which is otherwise created lazily on first upload — a
	// tenant that never uploaded would have nothing for the FK to reference, so
	// ensure it exists BEFORE planning/applying. Idempotent, and skipped entirely
	// for schemas with no file fields (zero change to every existing migration).
	if schemaHasFileFields(s) {
		if err := files.EnsureMetaTable(ctx, pool, strings.TrimPrefix(pgSchema, "tenant_")); err != nil {
			return nil, fmt.Errorf("ensure files table for %s: %w", pgSchema, err)
		}
	}
	plan, err := diffTenant(ctx, pool, pgSchema, s, includeOrphans)
	if err != nil {
		return nil, err
	}
	outcome := &ApplyOutcome{}
	if plan.Empty() {
		outcome.NoChange = true
		return outcome, nil // converged — nothing to do
	}

	// Surface every planning concern (backfill / destructive / transformational)
	// before touching the database — the up-front safety signal.
	logConcerns(pgSchema, plan)

	approvedSet := toSet(approved)
	applyPlan, gated, appliedKeys := partitionByPolicyApproved(plan, approvedSet)

	// Log + record the gated drops, distinguishing a data-losing drop awaiting
	// approval from a safe drop kept as additive drift.
	for _, op := range gated {
		if key, destructive := schemadiff.DestructiveKey(op); destructive {
			outcome.GatedDrops = append(outcome.GatedDrops, key)
			log.Printf("migration[%s]: GATED destructive drop (NOT approved — data preserved as drift; approve %q to apply): %s", pgSchema, key, op.String())
		} else {
			log.Printf("migration[%s]: SKIPPED (v1 is additive — never drops; data preserved as drift): %s", pgSchema, op.String())
		}
	}
	// Log + record the approved drops actually applied (the audit trail of consent).
	outcome.AppliedDrops = appliedKeys
	for _, key := range appliedKeys {
		log.Printf("migration[%s]: APPLYING APPROVED destructive drop (explicit, enumerated consent): %s", pgSchema, key)
	}
	// An approval that matched nothing (typo / already applied) is reported, not fatal.
	for _, k := range approved {
		if !appliedSetHas(appliedKeys, k) {
			outcome.UnmatchedApprovals = append(outcome.UnmatchedApprovals, k)
			log.Printf("migration[%s]: approval %q matched no destructive operation (typo, or already applied) — ignored", pgSchema, k)
		}
	}

	if applyPlan.Empty() {
		outcome.NoChange = true // only gated drops were pending; no DDL ran
		return outcome, nil
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
		return nil, fmt.Errorf("apply migration %s: %w", pgSchema, err)
	}
	applyForeignKeys(ctx, ex, pgSchema, fkAdds)
	return outcome, nil
}

// appliedSetHas reports whether key is in keys (small slices; linear is fine).
func appliedSetHas(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
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
//
// includeOrphans controls whether a table that exists physically but is no longer a
// declared resource (a removed resource) is brought into the diff as a DropTable:
// false for the additive path (a removed resource is invisible drift, the exact v1
// behavior); true for the approval-aware path (so the table can be previewed and,
// with explicit consent, dropped). The engine's own auth_*/files tables are NEVER
// brought in, in either mode (isEngineManagedTable).
func diffTenant(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, includeOrphans bool) (*schemadiff.Plan, error) {
	real, err := schemadiff.Introspect(ctx, pool, pgSchema)
	if err != nil {
		return nil, fmt.Errorf("introspect %s: %w", pgSchema, err)
	}
	desired := buildDesiredSchema(pgSchema, s)
	// Reconcile rename intent against the live DB BEFORE diffing: keep a rename only
	// while it is pending (old name present, new absent), and remember the table
	// rename sources so managedSubset does not drop them.
	keepSources := resolveRenames(desired, real)
	current := managedSubset(real, desired, keepSources, includeOrphans)
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

// managedSubset returns a view of real restricted to the tables the migration is
// allowed to reason about. It ALWAYS excludes the engine's own auth_*/files tables
// (isEngineManagedTable — the migration must never propose dropping them) and ALWAYS
// includes the declared resource tables (present in desired) plus any pending-rename
// source (keepSources).
//
// A table that is neither a resource nor engine-internal is a REMOVED resource (its
// resource was deleted from the schema; its table lingers physically). Whether to
// bring it into the diff as a DropTable depends on includeOrphans:
//   - false (additive path): EXCLUDE it — a removed resource is invisible drift, the
//     exact historical behavior (no DropTable is ever proposed for it).
//   - true (approval-aware path): INCLUDE it — the diff proposes a DropTable, which
//     the policy GATES by default (drift, as before) and applies only under explicit
//     approval. This is what makes a removed resource visible in a dry-run preview.
//
// Enums are not copied (the API schema does not manage enum types, and Diff ignores
// them regardless).
func managedSubset(real, desired *schemadiff.Schema, keepSources map[string]bool, includeOrphans bool) *schemadiff.Schema {
	out := schemadiff.NewSchema(real.Name)
	for name, tbl := range real.Tables {
		switch {
		case isEngineManagedTable(name):
			continue // never the engine's own auth_*/files tables
		case keepSources[name]:
			out.Tables[name] = tbl // pending rename source
		case desired.Tables[name] != nil:
			out.Tables[name] = tbl // a declared resource
		case includeOrphans:
			out.Tables[name] = tbl // a removed resource → DropTable (gated/approvable)
		}
	}
	return out
}

// isEngineManagedTable reports whether a tenant-schema table is owned by the engine
// itself (the auth_* identity tables and the files store), not by the user's schema.
// These are created/managed outside ApplyTenantMigration and the migration must never
// touch them — both names are RESERVED (a resource cannot be named auth_* or files),
// so this can never shadow a real resource table.
func isEngineManagedTable(name string) bool {
	return strings.HasPrefix(name, "auth_") || name == "files"
}

// partitionByPolicyApproved splits a plan into the ops to APPLY and the drops to
// GATE, honoring an explicit approval set for the DATA-LOSING drops. It is the SOLE
// place a drop is ever scheduled for execution, and the SAME classifier the dry-run
// preview uses — so a preview is faithful to the apply by construction.
//
// The policy:
//   - A non-drop op always applies (create/add/alter/rename, FK adds).
//   - A DESTRUCTIVE drop (DropTable/DropColumn, schemadiff.DestructiveKey) applies
//     ONLY if its key is in `approved`; otherwise it is gated (drift). This is the
//     fail-safe default: without enumerated consent, nothing is dropped.
//   - A SAFE drop (index/constraint/FK) is gated as additive drift — EXCEPT a foreign
//     key whose referenced table is itself an APPROVED table drop: that FK must be
//     removed for the DROP TABLE to succeed, and dropping an FK loses no data, so it
//     is applied as a necessary, safe CONSEQUENCE of the approved table drop (never an
//     independent constraint removal). This keeps an approved table drop self-coherent
//     even when a still-present table kept the referencing column.
func partitionByPolicyApproved(plan *schemadiff.Plan, approved map[string]bool) (apply *schemadiff.Plan, gated []schemadiff.Operation, appliedKeys []string) {
	// Which tables are being dropped by explicit approval — needed to un-gate the
	// incoming foreign keys that would otherwise block their DROP TABLE.
	approvedDropTables := make(map[string]bool)
	for _, op := range plan.Ops {
		if dt, ok := op.(schemadiff.DropTable); ok {
			if key, _ := schemadiff.DestructiveKey(op); approved[key] {
				approvedDropTables[dt.Table.Name] = true
			}
		}
	}

	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		if !isDropOp(op.Kind()) {
			keep = append(keep, op)
			continue
		}
		if key, destructive := schemadiff.DestructiveKey(op); destructive {
			if approved[key] {
				keep = append(keep, op)
				appliedKeys = append(appliedKeys, key)
			} else {
				gated = append(gated, op)
			}
			continue
		}
		// Safe drop: kept as drift, unless it is an incoming FK to an approved table
		// drop (a required, data-safe consequence — see the doc comment).
		if dfk, ok := op.(schemadiff.DropForeignKey); ok && approvedDropTables[dfk.FK.RefTable] {
			keep = append(keep, op)
			continue
		}
		gated = append(gated, op)
	}
	return &schemadiff.Plan{Ops: keep}, gated, appliedKeys
}

// logConcerns surfaces a plan's backfill/destructive/transformational concerns on
// PRE-EXISTING tables (the shared filter concernsOnExistingTables drops concerns about
// a table created in the same plan — no data risk on a brand-new, empty table). The
// dry-run preview formats the SAME concern set.
func logConcerns(pgSchema string, plan *schemadiff.Plan) {
	for _, c := range concernsOnExistingTables(plan) {
		log.Printf("migration[%s]: concern [%s]: %s", pgSchema, c.Risk, c.Message)
	}
}

// concernsOnExistingTables returns the plan's concerns (schemadiff.Validate) minus
// those acting on a table created in the same plan: a constraint/column added to a
// brand-new, empty tenant table is no data risk, so reporting it would fire a
// misleading "backfill" warning on every fresh-tenant provision. The concerns that
// remain are exactly the ones that matter over real data.
func concernsOnExistingTables(plan *schemadiff.Plan) []schemadiff.Concern {
	created := make(map[string]bool)
	for _, op := range plan.Ops {
		if ct, ok := op.(schemadiff.CreateTable); ok {
			created[ct.Table.Name] = true
		}
	}
	var out []schemadiff.Concern
	for _, c := range schemadiff.Validate(plan) {
		if t := opTable(c.Op); t != "" && created[t] {
			continue
		}
		out = append(out, c)
	}
	return out
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
