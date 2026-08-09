# Ctx ↔ generated write path — the parity audit

**CTX-PARITY-S1, 2026-08-09.** Companion to
[DUPLICATED_RULES_AUDIT.md](DUPLICATED_RULES_AUDIT.md) and
[MIGRATION_HONESTY_AUDIT.md](MIGRATION_HONESTY_AUDIT.md), and the same method:
audit the CLASS, not the instance.

## Why this exists

The engine has two write paths that the documentation presents as equivalent:

1. the **generated** path — the REST `POST` / `PATCH` / `PUT` handlers built in
   `pkg/codegen/builder.go`, which the GraphQL mutations and the
   `/api/transaction` batch already share cores with;
2. the **library** path — `Ctx.Insert` / `Ctx.Update` (`ctx.go`), what a
   consumer's custom Go handler calls.

`docs/BACKEND_SPEC_LLM.md` told consumers that `Ctx.Insert` validates *"exactly
like the generated POST"*. A third-party field report (VecinGo: 18 resources, 8
state machines, 13 custom handlers, in production) proved that false for schema
defaults. **The bug report named two divergences. The audit found five**, one of
them security-relevant and unreported — which is the whole argument for auditing
the class: the two the user hit were the two that happened to be *visible*.

## The table

Every behaviour the generated create/update applies, and what the library path
did about it BEFORE this session. `→` marks what changed.

| # | Behaviour | Generated | Ctx (before) | Verdict |
|---|---|---|---|---|
| 1 | Body cap + JSON decode | 1 MiB → 413 | `Ctx.Bind` has the same cap (`MaxBodyBytes`) | same |
| 2 | Empty body | 400 `empty body` | `ValidationError{rule:"empty"}` → 422 | **differs, kept** — a handler's data is not an HTTP body; a 400 about "the body" would be a lie |
| 3 | **Schema defaults** (`ApplyDefaults`) | applied, BEFORE validation | **absent** | **DIVERGED → fixed** |
| 4 | Declarative rules (`ValidateWrite`) | applied | applied | same |
| 5 | **Value type check** (`validateCreateTypes`) | applied | **absent** | **DIVERGED → fixed** |
| 6 | **State-machine initial states** | applied | **absent** | **DIVERGED → fixed** |
| 7 | **Numeric acceptance** | float64 only (all JSON gives) | float64 only — rejected `int64` | **DIVERGED → fixed** |
| 8 | `before_create` / `before_update` hooks | run | not run | **differs, documented** — see "Deliberate" |
| 9 | **RBAC on create** (`EnforceCreateRBAC`): allowlist + FORCE the row-condition column + reject a foreign principal | all three | allowlist ONLY | **DIVERGED → fixed (security-relevant)** |
| 10 | File attach policy (`CheckFilePolicies`) | applied | applied | same |
| 11 | Row condition on update | in the `WHERE` | `query.AppendRowCondition` | same |
| 12 | State-machine transition guard | `AppendStateTransitionGuard` | same function (ENG-7) | same |
| 13 | Outbox event (`events:[…]`) | emitted in the same tx | **not emitted** | **differs, documented** — `ctx.Enqueue` is the sanctioned equivalent |
| 14 | SSE broadcast, `after_*` webhooks | fired post-commit | not fired | **differs, inherent** — the handler's tx has not committed |
| 15 | Unique violation | 409 `field "x": value already exists` | raw driver error | **open (ENG-42)** |
| 16 | Unknown column | 422 `unknown_field` (42703) | raw driver error | **open (ENG-42)** |
| 17 | Deployed-schema surface (ENG-12) | `writeSurface` — a hot-migrated column's rules compile | boot schema | **open (ENG-43)** |

### The chain that made #3 expensive

Not "a missing default" — a row **outside its own declared lifecycle**:

```
ctx.Insert("citas", {...})        // estado omitted, relying on the schema default
  → default NOT applied           // (3)
  → row lands with estado = NULL
  → next transition: UPDATE ... WHERE estado IN ('solicitada')   // the guard
  → 0 rows → "invalid transition from \"\""
```

The write succeeded, reported success, and produced a record the engine's own
rules could never advance. Nothing failed at write time — the cost landed one
step later, in the transition, pointing at the wrong place.

### #9, the one nobody reported

A role scoped by `conditions: {field: owner_id, val: $user_id}`:

| | `POST /api/notes` | `ctx.Insert("notes", …)` (before) |
|---|---|---|
| `{"body":"mine"}` | 201, `owner_id` forced to the caller | 201, **`owner_id` = NULL** |
| `{"body":"x","owner_id":"user-mallory"}` | **403** | **201, attributed to mallory** |

A custom route was a way *around* a rule `/api` enforces. Pinned now by
`TestParity_InsertEnforcesRowConditionOnCreate`.

## How it was closed: one source, two callers

Not by patching the second path — the same cure as
`AppendStateTransitionGuard` (ENG-7) and `FieldDef.ReferencedColumn`:

- **`codegen.PrepareCreate`** — defaults → declarative rules + value types
  (collected together, so one response still carries every failing field) →
  initial states, in the generated POST's exact order. **Both** paths call it.
- **`codegen.PrepareUpdate`** — the PATCH-semantics counterpart.
- **`codegen.EnforceCreateRBAC`** — already existed and was already shared with
  GraphQL; `Ctx.Insert` now calls it too.
- **`schema.AsFloat64` / `schema.IsIntegral`** — the ONE decision about what
  counts as a number, consumed by `pkg/schema`'s rules and by codegen's type
  check, so they cannot drift.

A step added to `PrepareCreate` now reaches both paths by construction. That is
the property; the fixes are a consequence of it.

## Deliberate, permanent differences

These are not gaps to close later — closing them would be wrong:

- **Hooks (#8).** A `before_create` hook is the schema's way to gate the
  GENERATED write. A custom handler *is* the gate for what it writes; running a
  sandboxed hook inside it would mean a handler cannot write without the schema's
  permission, which inverts the library's purpose. Invariants that must hold on
  every path belong in the schema (rules, state machines, constraints), where
  both paths already enforce them.
- **Post-commit effects (#13, #14).** `after_*` webhooks and the SSE broadcast
  fire after COMMIT; a handler is mid-transaction. `ctx.Enqueue` is the
  equivalent that is atomic with the row, and is what `backend-spec` teaches.
- **Numeric precision.** The HTTP path decodes JSON numbers into float64 (~2^53).
  `ctx.Insert` stores the exact `int64`. The library path is the more precise of
  the two, on purpose: the fix was to accept Go's numbers, not to make them lossy.

## The anti-drift instrument

`ctx_parity_integration_test.go` runs the **same payload through both paths and
asserts identical rows**, plus a read→write round trip of values the engine
itself returned, plus the RBAC-on-create matrix. Before this session it failed on
every case; it passes now. Closing a future divergence means **adding a row to
that table**, not writing a new test — which is what keeps the audit honest
after the session that wrote it.

## Still open

Filed in [BACKLOG.md](../BACKLOG.md): **ENG-42** (error shape — unique violation
and unknown column reach a handler as raw driver errors instead of the engine's
409/422 vocabulary) and **ENG-43** (`Ctx` resolves the BOOT schema, so a
hot-migrated column's rules are not compiled for the library path the way
`writeSurface` compiles them for the generated one).
