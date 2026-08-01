# Migration honesty audit — where the migrator could report success and be wrong

**Session:** AUTHORING-GAPS-S1 (2026-08-01), Part A.
**Trigger:** ENG-13 — a `references` change on an existing relation was reported
applied and was not (`docs/AUTHORING_JOURNEY.md` 5-8). Fixing the instance is
cheap; the point of this audit is the **class**: every place in the migration
path where an operation can be skipped, ignored or half-applied **without the
result saying so**.

The rule the audit applies: an operation that does not run must be visible in the
**outcome the caller gets**, not only in a server log the app owner will never
read. "Deliberately tolerant" is fine — tolerance without a report is not.

---

## The class-level guarantee (the finding that made the rest cheap)

Every individual skip is a hole to be found one by one. The general fix is not to
enumerate them: it is to stop trusting the migrator's own account of itself.

`migration.verifyApplied` re-introspects the database **after** an apply and
re-computes the diff. Anything non-drop still pending is a declaration the
database does not honor. It lands in `ApplyOutcome.Unapplied`, and
`ApplyOutcome.Partial()` is what every reporter now checks.

This catches whatever caused the gap — including tolerant paths nobody has
written yet. Drops are excluded on purpose: v1 is additive, so an unapproved drop
remaining is the documented policy, not a lie.

`TestIntegration_DeclaredEqualsApplied` walks a schema through provisioning, a
repointed relation, a new resource, a new index, a new relation and a renamed
column, and after each step asserts the database has everything the schema
declares.

---

## Findings

### 1. FK definition change → silent no-op — **FIXED** (the reported instance)
`partitionByPolicyApproved` gated the `DropForeignKey` (a safe drop → additive
drift) while keeping the `AddForeignKey`. Both constraint names are derived from
the table and columns, so the add collided with the surviving constraint
(`42710 constraint already exists`), `applyForeignKeys` logged `skipped` and
continued.

**Fix:** an FK the same plan re-adds over the same `(table, columns)` is a
**replacement**, not a removal. Its drop is un-gated (dropping a constraint loses
no row data) and the pair runs in ONE transaction (`Executor.ExecBatch`), so a
failure rolls back to the old FK instead of leaving the column unprotected.
Pinned by `TestIntegration_ENG13_ReferencesChangeActuallyApplies` and
`…_OnDeleteChangeActuallyApplies`, both asserting against `pg_constraint`.

### 2. A blocked `renamed_from` → silent no-op — **FIXED** (found by the audit)
`resolveRenames` cleared the rename intent whenever it could not run. Two very
different situations shared that branch: *already applied* (correct, inert) and
**both names already exist** — where the rename cannot happen, the values stay in
the OLD column, and the schema claims they moved.

**Fix:** the second case comes back as a blocked-rename warning, reported in
`ApplyOutcome.Unapplied` (so the apply is PARTIAL) and in the dry-run's
`Concerns` as `[blocked]`. `TestIntegration_BlockedRenameIsReported`.

### 3. Persist-before-migrate → the record claimed what the database refused — **FIXED**
`controlplane.updateSchemaSourced` wrote `tenants.json_schema` (and appended a
history version) BEFORE applying the DDL. A migration error returned an error but
**left the new schema persisted**: the tenant record described a database it did
not have, and the running engine validated writes against it.

**Fix:** the previous schema is captured first and RESTORED when the apply fails
or comes back partial, and the history version is appended **only after the
migration actually applied** — so a version in the trail is a version the database
really took.

### 4. A gated drop erased its own approvability — **FIXED** (found by the audit)
ENG-9 decides "operator-removed field (approvable drop)" vs "consumer-owned column
(external, never proposed)" from `tenants.json_schema` + `schema_history`. A
deploy that GATED a drop still persisted the new schema — erasing the last record
that the gated column was ever declared. On the next run the column classified as
external, `--approve-drops` reported **`no-op`** and the column stayed.

This was live on `main` as a **failing test** (`TestFanout_DestructiveGatedThenMassApproved`,
"mass approval should apply to both, got applied=0") — the class biting in the
repository's own suite.

**Fix:** `schemahistory.EnsureSeeded` records the schema being replaced before any
overwrite, on both persist paths (control plane and fan-out).

### 5. A failed FK `ADD` → **now surfaced**
Still deliberately tolerant (one bad FK must not abort a whole schema's
migration), but no longer invisible: the constraint's absence is caught by the
post-apply verification, so the apply is PARTIAL and the CLI exits non-zero.

### 6. An FK left `NOT VALID` → **now reported**
Adding an FK over rows that already violate it leaves it `NOT VALID` — new writes
are checked, historical rows are not. Correct behavior, previously log-only. Now
in `ApplyOutcome.UnvalidatedFKs` and printed by the CLI in plain language.

### 7. Fan-out reported a partial tenant as applied → **FIXED**
`RunFanout` marked a tenant `applied` on any non-error outcome. A partial apply is
now a FAILED tenant: its schema is not persisted, it is recorded in
`public.migration_log`, the run exits non-zero, and a re-run retries exactly it —
the existing resumable contract, now covering this case.

---

## Left open (recorded in `docs/BACKLOG.md`, not fixed here)

- **MIG-1 — a gin index's `opclass` change is a silent no-op.** The opclass is
  deliberately excluded from the diff key (the introspector cannot read it back),
  so changing `jsonb_ops` → `jsonb_path_ops` on an existing index does nothing and
  says nothing. Narrow, documented, and the fix (drop+recreate on declaration
  change) needs a way to tell "declared different" from "cannot introspect".
- **MIG-2 — a `schema_history` append failure is log-only.** The deploy proceeds
  and the trail gets a gap; `EnsureSeeded` reduces the blast radius but the write
  itself is still best-effort with no signal to the caller.

## Not findings (checked, behavior is honest)

- `Executor.Apply` — transactional batches fail atomically; `CONCURRENTLY`
  statements return their error. No partial application inside a batch.
- `splitExternalDrops` — consumer-owned objects are reported as `ExternalDrift` in
  the outcome and `External` in the preview, never silently dropped from view.
- Gated drops — reported per-key in the outcome, the preview and the CLI, with the
  exact `--approve-drops` token.
- `addDeclaredIndexes` / `addRelationIndexes` skipping an index whose column is
  absent — unreachable through a valid schema (load-time validation rejects an
  index over an unknown field); defensive only.
- The Redis migration worker giving up after 4 attempts — it is a redundant
  additive convergence behind a synchronous apply that already ran; its outcome is
  recorded in `public.migration_log`.
