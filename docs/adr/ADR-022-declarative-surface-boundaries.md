# ADR-022 — Three boundaries of the declarative surface: row-condition operators, index predicates, per-transition RBAC

**Status:** Accepted (LOOSE-ENDS-SWEEP-S1).
**Relates to:** [ADR-020](ADR-020-product-vision-and-positioning.md) (the
declarative thesis), [ADR-021](ADR-021-custom-route-authorization.md) (custom-route
authorization), G5 (state machines), LIBRARY-GAPS-S1 (`jsonb` + index method).

## Context

Three items sat open across several session reports, each of the form "the schema
cannot express X — should it?". None had a written decision, so each was
re-litigated from memory whenever it came up. This ADR decides all three, because
they share one question: **what belongs in a declarative schema, and what belongs
in code?**

The engine's answer has a shape. The schema owns what can be **validated at load
and enforced identically on every path** (REST, GraphQL, the batch transaction).
Anything the engine would have to accept on trust — arbitrary SQL, an expression
it cannot type-check — belongs in code, where it is reviewed, compiled and
greppable.

---

## Decision 1 — Row-condition operators stay `eq`-only

**Status: closed. Not implemented.**

An RBAC row condition (`{"field": "user_id", "op": "eq", "val": "$user_id"}`) is
AND-ed into the WHERE of every read, and `op` may only be `eq` (SEC-AUDIT-V1 made
declaring anything else a load error, so "declared == applied" holds).

### Correcting the premise

This item was carried as "the limitation that forced the `user_id`
denormalization in the commerce backend". **That attribution is wrong**, and it
matters, because it would have sent the work in the wrong direction.

Re-reading that schema: the customer role must read the LINES of its own orders.
Ownership lives on the parent (`ordenes.user_id`); the child (`orden_lineas`) is
owned *through* the FK. No scalar operator expresses that — `in`, `neq`,
`gt`, `is_null` all compare a column of the row being filtered to a constant. What
that case needs is a **join or subquery** condition, which is Decision 1b below.
The denormalization is the correct answer to the *no-joins* rule, not a workaround
for a missing operator.

### Why `eq` is load-bearing, not lazy

A row condition is enforced on **create** as well as read: the condition field is
**forced** to the caller's resolved value, so an owner-scoped role cannot attribute
a row to another principal (`codegen.EnforceCreateRBAC` — the mass-assignment
block). "Force the value" is only meaningful for equality. What should `create` do
under `amount > 100` — reject a body that violates it? Silently set it? Both are
*different rules*, and the second is a security regression waiting to happen.

So adding operators is not "one more case in a switch": it requires defining a
SECOND create semantic (validate-instead-of-force) and applying it consistently
across the five places conditions are compiled (list/aggregate WHERE,
`AppendRowCondition` for get/update/delete, the `?include=` embed LATERAL, the
batch transaction, and create enforcement). The value does not currently justify
weakening a structural guarantee.

### What we do instead

- **Narrow by column** — the per-resource `permissions` form (G2) already scopes
  each resource by its own column, with `condition_actions` for "read all, write
  own".
- **Narrow by endpoint** — a custom route granted through the `routes` block
  (ADR-021) can apply any predicate it likes in its own SQL, reviewed as code.
- **Soft delete** — model it as a state machine terminal state or a boolean the
  role's condition equals (`archived = false` is an `eq`).

### Reconsider when

A concrete case appears that (a) cannot be expressed by per-resource conditions
or a custom route, AND (b) is a **read-only** scope. The implementable slice is
then: operators `in` / `neq` / `is_null`, permitted **only** on actions listed in
`condition_actions` that exclude `create`, validated at load for type
compatibility, with the create path continuing to require `eq`. Backlog: `RBAC-1`.

## Decision 1b — Join / subquery row conditions are rejected

**Status: closed. Will not implement in this form.**

"A customer may read the lines of their own orders" as a declarative condition
would compile to a correlated subquery injected into every read path — including
the `?include=` embed's LATERAL, where it would run per parent row.

Three reasons to refuse:

1. **Performance is unbounded and invisible.** The cost depends on the target
   table's size and indexes, neither of which the schema author sees when writing
   the condition. The engine's whole performance story is that the generated SQL
   is predictable.
2. **Auditability.** RBAC is compiled into SQL precisely so that "what the role
   may see" is one readable predicate. A nested EXISTS over another table, itself
   subject to that table's RBAC, is a policy nobody can verify by reading.
3. **It has a correct, cheap alternative.** Denormalize the ownership column onto
   the child. It turns the check into an index lookup, it is what the engine's
   `eq` condition was designed for, and it is what the commerce backend does —
   deliberately, now documented as the pattern rather than as a workaround
   (`docs/BACKEND_SPEC_LLM.md` §3.5).

**Reconsider when** a case needs ownership through a relation AND the denormalized
column is genuinely impossible to maintain (e.g. the parent's owner changes often
and the child count is large). The alternative to evaluate then is a materialized
ownership column maintained by the engine, not a runtime subquery.

---

## Decision 2 — Index predicates (partial indexes) stay out of the schema

**Status: closed. Not implemented. The previous reasoning was incomplete and is
corrected here.**

`indexes` entries take `fields`, `unique`, `method` and `opclass`
(LIBRARY-GAPS-S1). A `WHERE` predicate — a partial index — is not accepted.

### The old reason does not survive contact with the evidence

The stated reason was: "Postgres normalizes predicate text, so round-tripping one
through the diff would churn the index on every migration." That is true about
normalization but wrong about the conclusion. Measured on PostgreSQL 16:

```sql
CREATE INDEX idx_a ON t (id) WHERE estado = 'activa';
CREATE INDEX idx_b ON t (id) WHERE (estado = 'activa');
-- pg_get_expr(indpred) for BOTH:
--   (estado = 'activa'::text)
```

Normalization is **deterministic** — two spellings collapse to one canonical
form — so churn is solvable. Two workable strategies exist: compare the
canonical form after a first create, or record the DECLARED text in a
`COMMENT ON INDEX` and diff the declaration against the recorded declaration. The
churn objection alone would not justify refusing the feature, and this ADR
retracts it as the primary argument.

### The real reason: raw SQL from an authoring surface we do not control

A predicate is **arbitrary SQL text rendered into DDL**. Unlike `opclass` (a
closed allowlist of two literals) it cannot be enumerated. And the schema is not
only hand-written: it is generated by `ai-generate`, edited in Studio, and posted
to `/admin/engine/schema`. Accepting free-form SQL from that surface adds an
injection channel into DDL that no amount of escaping closes, because the content
IS meant to be SQL.

The engine has no SQL parser and adding one to validate predicates is a larger
project than the feature it would enable.

### What we do instead

Create the partial index in your own boot DDL through `Config.BeforeStart` — the
engine's migration is additive, so a column or index it does not know about is
reported as drift and never dropped. The commerce backend does exactly this for
its two partial indexes, and `migrations.go` there documents why per statement.

### Reconsider when

A **structured** predicate exists — not SQL text:

```json
{ "fields": ["expira_en"], "where": { "field": "estado", "op": "eq", "value": "activa" } }
```

That form is enumerable (closed op set), safely renderable (sanitized identifier,
bound-then-inlined literal), and diffable by STRUCTURE rather than text — which
sidesteps normalization entirely. **Ready** = the structured grammar validates at
load, renders through `pkg/schemadiff`, survives an introspect→diff→apply cycle
with an empty plan, and covers the `IS NOT NULL` + equality cases the real
backends actually used. Backlog: `SCHEMA-2`.

---

## Decision 3 — Per-transition RBAC is a custom route, not a schema key

**Status: closed. Not implemented as a schema key; the pattern is documented.**

A `state_machine` enforces which transitions are *possible*
(`pagada → enviada`), race-safely, inside the UPDATE's WHERE. It does not express
**who** may perform one: today the resource's `update` grant governs, so any role
that may update the order may move it to `enviada` **or** to `reembolsada`.

That is a real gap for a commerce or finance app — refunding is not shipping.

### Why not a schema key

The obvious shape is per-transition roles:

```json
"transitions": { "pagada": [{ "to": "enviada", "roles": ["operations"] }] }
```

It is implementable (the role would narrow the origin list before the WHERE is
built, so race-safety is preserved). It was rejected for now on cost and
coherence, not feasibility:

- The state machine is enforced in **three** write paths (REST update, GraphQL
  `update<Singular>`, and `POST /api/transaction`), plus Studio's state-machine
  designer and the LLM grammar. A partial implementation would mean "the schema
  says who may ship" being true on REST and false in a batch — worse than not
  having it.
- It creates a **second authorization surface**. Today "who may do what" is one
  place: `rbac`. Splitting a slice of it into a field's `state_machine` means a
  reviewer must read both to answer "who can refund?".

### The recommended pattern (available today, composes shipped features)

Model the privileged transition as its own **endpoint**, and authorize the
endpoint:

```json
"rbac": { "roles": {
  "operations": { "permissions": { "ordenes": { "actions": ["read","update"] } },
                  "routes": { "orders-ship": { "actions": ["create"] } } },
  "finance":    { "permissions": { "ordenes": { "actions": ["read"] } },
                  "routes": { "orders-refund": { "actions": ["create"] } } }
} }
```

```go
// POST /api/orders-refund — only a role granted this SEGMENT reaches the handler
// (ADR-021), and the transition itself stays race-safe because the guard is in
// the UPDATE's WHERE, exactly as the state machine does it.
app.Register(appximo.Route{Method: "POST", Path: "/api/orders-refund",
    Handler: func(ctx appximo.Ctx) error {
        tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
            `UPDATE ordenes SET estado = 'reembolsada'
              WHERE id = $1 AND estado = 'pagada'`, id)   // ← compare-and-set
        ...
    }})
```

This gives per-transition authorization **with one authorization surface** (the
role's grants), race-safety (the WHERE guard), and an audit point for the side
effects a privileged transition usually carries anyway — a refund issues a credit
note, a shipment notifies the carrier. Those never belonged in a schema key.

### Reconsider when

Two or more real backends need per-transition roles with **no** accompanying side
effect (pure state change). **Ready** = enforced identically on REST, GraphQL and
inside `POST /api/transaction`, expressible in Studio's designer, in the LLM
grammar, and benchmarked `no_change` on the update path. Backlog: `SCHEMA-3`.

---

## Consequences

- Three recurring questions now have written answers with reconsideration
  criteria, so they stop being re-litigated per session.
- One previously-stated reason (index-predicate churn) is corrected in the record
  rather than quietly kept; the feature stays closed for a *different*, stronger
  reason.
- The pattern each decision points at is a feature that already ships
  (per-resource `permissions`, `routes` grants, `BeforeStart`), which is the test
  of whether a "no" is honest: it must leave the user with a way to do the thing.

---

## Decision 5 — `is_null` is not a filter operator (yet), and the 400 must say so

**Added 2026-08-01 (SILENT-FAILURE-S1).** Closing ENG-14 turned
`?filter[x][is_null]=true` from "200 with the whole table" into a clean `400`, which
forced the question this ADR is the place for: should the operator exist?

**Not in that session, and the reason is scope rather than principle.** Adding an
operator is a capability: the type×operator matrix, GraphQL parity, the reference,
the LLM grammar that teaches generated schemas, and the AGENTS/CAPABILITIES tables
all move together. A pass whose subject was closing a defect class is the wrong
place to grow the surface — that is exactly the scope creep the class-closing
discipline exists to avoid.

**What is NOT decided here is that null-filtering should not exist.** Unlike this
ADR's other boundaries — row-condition operators (Decision 1), index predicates
(Decision 2), per-transition RBAC (Decision 3) — where the answer is *no, use this
other shape*, here **there is no other shape**: the declarative surface cannot
express "rows where this column is null" at all. The workaround is framework mode,
which today needs an unpublished module.

So this is a **deferral with a debt attached**, tracked as SCHEMA-6, and the
condition to revisit is simply the next session that can carry a capability. Until
then the rejection message should name the limitation rather than only listing the
operators that do exist — a `400` that closes a door without pointing anywhere is
half of the very problem ADR-024 is about.
