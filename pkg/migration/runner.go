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
	// ExternalDrift are consumer-owned objects (no schema version ever declared
	// them — ENG-9) present in the database but out of migration scope: never
	// proposed as drops, never approvable, left untouched.
	ExternalDrift []string
	// Unapplied lists DECLARED operations that are STILL PENDING after the apply,
	// computed by RE-INTROSPECTING the live database — never by trusting the
	// executor's own log (ENG-13). A non-empty Unapplied means declared and applied
	// have DIVERGED and the apply is PARTIAL: it must never be reported as success.
	//
	// This is the class-level guard, not a per-operation one: whatever the reason an
	// operation did not land (a tolerated foreign-key failure, a rename skipped
	// because its target already existed, a future tolerant path nobody has written
	// yet), the verification sees the DATABASE and reports the gap.
	Unapplied []string
	// UnvalidatedFKs lists foreign keys that were added but left NOT VALID because
	// pre-existing rows violate them. They DO protect every new write; the historical
	// rows need fixing, then a manual VALIDATE. Not a divergence (the constraint
	// exists), so it does not make the apply partial — but it is reported, never
	// only logged.
	UnvalidatedFKs []string
	// NoChange is true when NO DDL was applied — the schema was already converged, or
	// the only pending operations were gated drops. It is the noop signal the
	// multi-tenant orchestrator uses to distinguish an "already up to date" tenant
	// from one it actually migrated.
	NoChange bool
}

// Partial reports whether the apply left DECLARED changes unapplied — i.e. the
// tenant's schema now claims a shape the database does not have. Every reporter
// (CLI, control plane, admin API, fan-out) must treat a partial apply as a
// FAILURE, never as ✓.
func (o *ApplyOutcome) Partial() bool { return o != nil && len(o.Unapplied) > 0 }

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
	plan, blockedRenames, err := diffTenant(ctx, pool, pgSchema, s, includeOrphans)
	if err != nil {
		return nil, err
	}
	outcome := &ApplyOutcome{}
	// A rename the database cannot take (both names already present) is reported as
	// a divergence, not swallowed: the schema says the values moved, and they did not.
	for _, w := range blockedRenames {
		log.Printf("migration[%s]: RENAME NOT DONE — %s", pgSchema, w)
	}
	outcome.Unapplied = append(outcome.Unapplied, blockedRenames...)
	// ENG-9: objects no deployed schema version ever declared are consumer-owned —
	// reported as external drift, never proposed or approvable (see external.go).
	if owned, ok := loadOwnedObjects(ctx, pool, pgSchema); ok {
		var external []schemadiff.Operation
		plan, external = splitExternalDrops(plan, owned)
		logExternal(pgSchema, external)
		for _, op := range external {
			outcome.ExternalDrift = append(outcome.ExternalDrift, op.String())
		}
	}
	if plan.Empty() {
		outcome.NoChange = true
		// Approval keys with nothing to match (including keys naming EXTERNAL
		// objects, which are out of the plan by design) are reported, not lost.
		outcome.UnmatchedApprovals = append(outcome.UnmatchedApprovals, approved...)
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
	nonFK, fkAdds := splitForeignKeyOps(applyPlan)

	ex := &schemadiff.Executor{Pool: pool, Schema: pgSchema}
	if err := ex.Apply(ctx, nonFK); err != nil {
		return nil, fmt.Errorf("apply migration %s: %w", pgSchema, err)
	}
	outcome.UnvalidatedFKs = applyForeignKeys(ctx, ex, pgSchema, fkAdds)

	// ENG-13: verify against the DATABASE, not against the executor's log. Anything
	// declared that is still pending after the apply is a divergence — reported here
	// so no caller can print ✓ over it.
	outcome.Unapplied = verifyApplied(ctx, pool, pgSchema, s, includeOrphans)
	for _, u := range outcome.Unapplied {
		log.Printf("migration[%s]: NOT APPLIED — the schema declares this and the database does NOT have it (declared ≠ applied): %s", pgSchema, u)
	}
	return outcome, nil
}

// verifyApplied re-introspects the live database after an apply and returns the
// DECLARED operations that are still pending — the "declared == applied" guarantee.
//
// It is deliberately blind to HOW the migration ran: it re-computes the same diff
// from the real database and keeps every NON-drop operation left in it. A drop that
// remains is expected (v1 is additive: unapproved drops stay as drift by design), so
// drops are excluded; anything else means the engine claimed a change it did not
// make.
//
// A verification that cannot run (introspection error) returns nothing rather than a
// false alarm — the apply itself already succeeded, and inventing a divergence would
// be its own kind of lie. The failure is logged.
func verifyApplied(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, includeOrphans bool) []string {
	plan, blockedRenames, err := diffTenant(ctx, pool, pgSchema, s, includeOrphans)
	if err != nil {
		log.Printf("migration[%s]: post-apply verification could not run (%v) — the apply itself succeeded; re-run to verify", pgSchema, err)
		return nil
	}
	pendingRenames := blockedRenames
	pending := pendingRenames
	for _, op := range plan.Ops {
		if isDropOp(op.Kind()) {
			continue // additive policy: an unapproved drop stays as drift, by design
		}
		pending = append(pending, op.String())
	}
	return pending
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

// fkApply is one foreign key to put in place: the ADD, plus — when the FK is being
// REPLACED because its definition changed (ref column, ref table, referential
// action) — the DROP of the old constraint it supersedes. The two halves of a
// replacement are applied together, atomically (ENG-13).
type fkApply struct {
	add  schemadiff.Operation  // AddForeignKey
	drop *schemadiff.Operation // the DropForeignKey it replaces, if any
}

// splitForeignKeyOps separates the foreign-key work from the rest of a plan so the
// FKs can be applied with the transition-tolerant policy while everything else goes
// through the standard (fail-atomic) executor.
//
// It also PAIRS each add with the drop it replaces, matching on (table, columns): a
// relation is identified by the column(s) it constrains, so an add and a drop over
// the same columns are the two halves of ONE change of shape, not two independent
// operations. Pairing them matters twice: the drop must run in the same transaction
// (or a failed add would leave the table unprotected), and it must run BEFORE the
// add (Postgres derives both constraint names from the table and columns, so the new
// one collides with the old — the exact `42710 constraint already exists` that made
// a `references` change a silent no-op).
func splitForeignKeyOps(plan *schemadiff.Plan) (nonFK *schemadiff.Plan, fkOps []fkApply) {
	// Index the plan's FK drops by (table, columns) so an add can claim its pair.
	drops := map[string]schemadiff.Operation{}
	for _, op := range plan.Ops {
		if d, ok := op.(schemadiff.DropForeignKey); ok {
			drops[fkColumnsKey(d.Table, d.FK.Columns)] = op
		}
	}
	claimed := map[string]bool{}
	for _, op := range plan.Ops {
		a, ok := op.(schemadiff.AddForeignKey)
		if !ok {
			continue
		}
		item := fkApply{add: op}
		key := fkColumnsKey(a.Table, a.FK.Columns)
		if d, has := drops[key]; has && !claimed[key] {
			claimed[key] = true
			paired := d
			item.drop = &paired
		}
		fkOps = append(fkOps, item)
	}

	keep := make([]schemadiff.Operation, 0, len(plan.Ops))
	for _, op := range plan.Ops {
		switch d := op.(type) {
		case schemadiff.AddForeignKey:
			continue // handled by applyForeignKeys
		case schemadiff.DropForeignKey:
			if claimed[fkColumnsKey(d.Table, d.FK.Columns)] {
				continue // the drop half of a replacement — applied with its add
			}
		}
		keep = append(keep, op)
	}
	return &schemadiff.Plan{Ops: keep}, fkOps
}

// fkColumnsKey identifies a relation by the table and column(s) it constrains — the
// identity under which a foreign key is REPLACED rather than removed and re-added.
func fkColumnsKey(table string, cols []string) string {
	return table + "(" + strings.Join(cols, ",") + ")"
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
// A REPLACEMENT (an FK whose definition changed — ENG-13) carries its old
// constraint's DROP, and the drop + the `ADD … NOT VALID` run in ONE transaction:
// either the relation ends up with the new shape or it keeps the old one. It can
// never end up with neither.
//
// This is the documented v1 policy. A hard failure of the ADD itself (not a VALIDATE
// failure) is logged and skipped so one problematic FK never aborts the whole
// migration of the rest of the schema — but it is NOT silent: the post-apply
// verification (verifyApplied) sees the missing constraint in the database and marks
// the whole apply PARTIAL, so no caller reports it as success.
//
// It returns the foreign keys left NOT VALID (added, protecting new writes, with
// pre-existing rows that violate them) so the outcome can report them.
func applyForeignKeys(ctx context.Context, ex *schemadiff.Executor, pgSchema string, fkOps []fkApply) (unvalidated []string) {
	for _, item := range fkOps {
		op := item.add
		stmts, err := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{op}})
		if err != nil || len(stmts) == 0 {
			log.Printf("migration[%s]: foreign key render failed, skipped: %s: %v", pgSchema, op.String(), err)
			continue
		}
		// stmts[0] = ADD CONSTRAINT … NOT VALID ; stmts[1] = VALIDATE CONSTRAINT.
		// A replacement drops the superseded constraint in the SAME transaction, so
		// the constraint name is free when the ADD runs and a failure rolls back to
		// the old foreign key rather than leaving the column unprotected.
		add := []string{stmts[0].SQL}
		if item.drop != nil {
			dropStmts, derr := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{*item.drop}})
			if derr != nil || len(dropStmts) == 0 {
				log.Printf("migration[%s]: foreign key replace render failed, skipped: %s: %v", pgSchema, (*item.drop).String(), derr)
				continue
			}
			add = append([]string{dropStmts[0].SQL}, add...)
			log.Printf("migration[%s]: REPLACING foreign key (its definition changed — the old constraint is dropped and the new one added atomically; no row data is lost): %s", pgSchema, op.String())
		}
		if err := ex.ExecBatch(ctx, add...); err != nil {
			log.Printf("migration[%s]: foreign key add failed, skipped (schema unprotected for this relation): %s: %v", pgSchema, op.String(), err)
			continue
		}
		for _, st := range stmts[1:] {
			if err := ex.Exec(ctx, st.SQL); err != nil {
				log.Printf("migration[%s]: foreign key left NOT VALID — pre-existing rows violate it (NEW writes ARE protected; fix the orphan data then VALIDATE manually): %s: %v",
					pgSchema, op.String(), err)
				unvalidated = append(unvalidated, op.String())
			}
		}
	}
	return unvalidated
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
func diffTenant(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema, includeOrphans bool) (*schemadiff.Plan, []string, error) {
	real, err := schemadiff.Introspect(ctx, pool, pgSchema)
	if err != nil {
		return nil, nil, fmt.Errorf("introspect %s: %w", pgSchema, err)
	}
	desired := buildDesiredSchema(pgSchema, s)
	// Reconcile rename intent against the live DB BEFORE diffing: keep a rename only
	// while it is pending (old name present, new absent), and remember the table
	// rename sources so managedSubset does not drop them. A rename that CANNOT run
	// (both names already exist) comes back as a blocked-rename warning instead of
	// being dropped in silence — the schema would otherwise claim the data moved
	// while it sat in the old column (ENG-13's class).
	keepSources, blockedRenames := resolveRenames(desired, real)
	current := managedSubset(real, desired, keepSources, includeOrphans)
	plan, err := schemadiff.Diff(desired, current)
	if err != nil {
		return nil, nil, fmt.Errorf("diff %s: %w", pgSchema, err)
	}
	return plan, blockedRenames, nil
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
func resolveRenames(desired, real *schemadiff.Schema) (keepSources map[string]bool, blocked []string) {
	keepSources = make(map[string]bool)
	for _, dt := range desired.Tables {
		if dt.RenamedFrom != "" {
			_, oldExists := real.Tables[dt.RenamedFrom]
			_, newExists := real.Tables[dt.Name]
			if oldExists && newExists {
				// Both names exist: the rename cannot run, and staying quiet would
				// leave the rows in the OLD table while the schema says they moved.
				blocked = append(blocked, fmt.Sprintf(
					"table %q was to be renamed to %q, but %q already exists — the rename was NOT done and the data is still in %q",
					dt.RenamedFrom, dt.Name, dt.Name, dt.RenamedFrom))
			}
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
			if oldCol && newCol {
				blocked = append(blocked, fmt.Sprintf(
					"column %q.%q was to be renamed to %q, but %q already exists — the rename was NOT done and the values are still in %q",
					realTbl.Name, col.RenamedFrom, col.Name, col.Name, col.RenamedFrom))
			}
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
	return keepSources, blocked
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
//   - A SAFE drop (index/constraint/FK) is gated as additive drift — EXCEPT two
//     foreign-key cases, both of which lose no row data and both of which are a
//     CONSEQUENCE of a change already decided, never an independent loss of integrity:
//     (a) an FK whose referenced table is itself an APPROVED table drop — the FK must
//     go for the DROP TABLE to succeed, keeping an approved table drop self-coherent
//     even when a still-present table kept the referencing column; and
//     (b) an FK that the SAME plan re-adds over the same (table, columns) — the
//     relation is being RE-POINTED (a changed `references` / `on_delete` /
//     `on_update`), so the drop is the first half of a replacement (ENG-13). Gating it
//     was the bug: the add then collided with the surviving constraint's name, failed
//     with `42710 constraint already exists`, and the change silently never happened
//     while the tenant's recorded schema claimed it had.
func partitionByPolicyApproved(plan *schemadiff.Plan, approved map[string]bool) (apply *schemadiff.Plan, gated []schemadiff.Operation, appliedKeys []string) {
	// Which tables are being dropped by explicit approval — needed to un-gate the
	// incoming foreign keys that would otherwise block their DROP TABLE.
	approvedDropTables := make(map[string]bool)
	// Which (table, columns) the plan re-adds a foreign key over — the relations
	// being REPLACED rather than removed.
	replacedFKs := make(map[string]bool)
	for _, op := range plan.Ops {
		if dt, ok := op.(schemadiff.DropTable); ok {
			if key, _ := schemadiff.DestructiveKey(op); approved[key] {
				approvedDropTables[dt.Table.Name] = true
			}
		}
		if a, ok := op.(schemadiff.AddForeignKey); ok {
			replacedFKs[fkColumnsKey(a.Table, a.FK.Columns)] = true
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
		// drop, or the drop half of an FK replacement (both required, data-safe
		// consequences — see the doc comment).
		if dfk, ok := op.(schemadiff.DropForeignKey); ok {
			if approvedDropTables[dfk.FK.RefTable] || replacedFKs[fkColumnsKey(dfk.Table, dfk.FK.Columns)] {
				keep = append(keep, op)
				continue
			}
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
