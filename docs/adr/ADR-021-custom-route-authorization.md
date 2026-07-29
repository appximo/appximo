# ADR-021 — Authorizing custom routes: the `routes` grant

**Status:** Accepted (LIBRARY-GAPS-S1)
**Supersedes:** nothing. **Extends:** [ADR-016](ADR-016-extensibility-pattern.md) (the
library/extensibility model), G2 (per-resource `permissions`).

## Context

The engine authorizes a **custom route** — an endpoint a Go backend registers with
`(*App).Register` — by treating the first `/api/` segment as a **virtual resource**
and evaluating the caller's role against it with the HTTP-method action
(`GET`→read, `POST`→create, …). `POST /api/checkout` is therefore authorized as
"create on `checkout`".

That worked for two of the three role shapes:

| Role shape | Reaches a custom route? |
|---|---|
| wildcard (`"resources": "*"`) | yes — the wildcard matches any segment |
| role-global (`"resources": ["orders", "checkout"]`) | yes — list the segment as if it were a resource |
| **per-resource (`"permissions"`, G2)** | **no** — every key is validated against a real resource; a virtual segment is `unknown_resource` |

The third row is the problem, and it is not a corner case. A commerce backend built
on the framework (`/root/commerce`, the field report in its `docs/GAPS.md`) hit it
head-on: a customer needs **owner-scoped orders** (per-resource `permissions`, so
each resource is scoped by its own column) **and** `POST /api/checkout` (a custom
route — it reserves stock under `FOR UPDATE` and writes an order, its lines, the
reservations and a payment intent in one transaction). Those two requirements were
mutually exclusive:

> "For commerce this is not a corner case, it is *the* case. […] It changes your
> data model, not just your code." — the field report, ranked #1 by cost.

The workaround was to fall back to the role-global form and **denormalize a
`user_id` column onto every resource the role touches** so one condition is valid
everywhere. A schema shape dictated by an authorization limitation.

Two secondary facts shaped the decision:

1. **A schema is validated standalone.** `appitools validate`, Studio's Code view,
   the meta-schema and the AI correction loop all run with no Go program in sight.
   Whatever we add must be checkable *without* the route set — with the parts that
   need the route set checked later, at boot.
2. **A row condition over a virtual segment is meaningless.** There are no rows.
   Injecting `WHERE user_id = $1` for `checkout` would target a table that does not
   exist.

## Decision

Add a **`routes`** block to a role: a map of **custom-route segment → `{actions}`**.

```json
"cliente": {
  "permissions": {
    "ordenes": { "actions": ["read"],
                 "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } },
    "pagos":   { "actions": ["read"],
                 "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
  },
  "routes": { "checkout": { "actions": ["create"] } }
}
```

### The rules

1. **Orthogonal to `resources` / `permissions`.** A role may declare `routes`
   alongside either form, because they govern **different namespaces**: real tables
   vs registered endpoints. This is what makes "owner-scoped user + custom action"
   expressible at last. (`resources` and `permissions` remain mutually exclusive
   with each other, unchanged.)
2. **Authoritative for the segments it names.** For a segment listed in `routes`,
   that entry decides. It can only **narrow** a wildcard role, never widen one; a
   segment *not* listed falls through to the role's normal evaluation, so
   deny-by-default is untouched. This is the engine's existing "declared ==
   applied" rule (the same principle that rejects a non-`eq` row-condition
   operator instead of silently ignoring it).
3. **No `conditions`, no `fields`.** A virtual segment has no rows to filter and no
   columns to project. Declaring either is a **load error with an explanation**,
   never a silent no-op. The data the handler touches is authorized separately and
   normally: `Ctx.Query`/`Insert`/`Update` re-evaluate the role against the **real**
   resources, condition and field allowlist included.
4. **Two validation layers, each checking what it can see.**
   - *Schema* (`schema.Validate`, standalone): segment shape, at least one known
     action, and no collision with a declared resource name.
   - *Boot* (`appitools.validateRouteGrants`, in `Start` — and in the deploy path
     `POST /admin/engine/schema`): every granted segment must be **registered**, and
     every concrete action must correspond to a registered method on it. A grant
     nothing serves is dead authorization config — exactly the thing that later
     reads as "the RBAC says they can, so why the 403?" — so it fails the boot with
     the registered segments listed.

### Hot path

`Evaluate`/`Allows` gain one `len(rp.Routes) == 0` check before the existing logic.
For every role that declares no routes — which is every role written before this
ADR — that is a length read on a nil map, and the decision path is byte-identical.
Measured: `no_change` (see below).

## Alternatives considered

**(a) Allow an unknown `permissions` key when it matches a registered route.**
Rejected. It makes the *same* key mean two different things depending on context,
and it breaks standalone validation: `appitools validate schema.json` would report
`unknown_resource` for a schema that is perfectly valid at boot. The schema is the
artifact — CLI, Studio, meta-schema and the AI loop all judge it alone. A key whose
validity depends on a Go program that is not present is not a schema key.

**(b) A `resources`-style flat list (`"routes": ["checkout"]`).** Rejected: it
grants every method on the segment. `GET /api/reports` and `DELETE /api/reports`
are not the same privilege, and the middleware already derives an action per
method — a flat list would throw away resolution the engine already has.

**(c) Leave it: tell people to use the role-global form.** This is the status quo
that cost a real user a data-model change, and it is strictly worse than (a) or
(b): the role-global form forces ONE condition across every resource, which is the
very limitation `permissions` was introduced (G2) to remove.

**(d) Infer the grant from `RequireRole` on the route.** Rejected: it moves
authorization out of the schema into Go code, splitting the policy across two
places. The schema is where "who may do what" lives.

## Consequences

**Positive**

- The shape that was inexpressible — owner-scoped end users *plus* a custom action
  endpoint — is now direct. `/root/commerce` expresses `cliente` with per-resource
  ownership and a `checkout` grant, with no role-global fallback.
- Dead authorization config fails at **boot**, not as a runtime 403.
- Studio/CLI validation still works on the schema alone; the boot layer adds what
  only it can see.
- The deploy path (`POST /admin/engine/schema`) applies the same cross-check, so a
  bad grant is a clean `422` instead of persist → restart → `.bak` rollback.

**Negative / accepted**

- One more authorization surface to reason about. Mitigated by the narrowness (no
  conditions, no fields — it is *only* an action list) and by explicit negative
  tests (`pkg/rbac/route_grant_test.go`): a role that does not declare a segment
  gets 403; a wildcard role is unaffected on segments it does not name; a route
  grant never widens resource access.
- A schema that grants a route is now **binary-specific**: the pure `appitools
  serve` binary, which registers no custom routes, refuses to boot it. That is
  deliberate — the alternative is a policy that can never match.
- The `routes` key is authoritative, so adding one to a wildcard role *narrows*
  that segment. Documented; it is the "what you wrote is what applies" trade.

## Related change: role-global conditions are now fail-closed

The same session closed the field report's #2 finding, which is the mirror image of
this one. A role-global `conditions` is injected into the WHERE of **every**
resource the role lists, but validation only checked the column existed on *at
least one* (its own comment called this "a documented limitation"). A schema like

```json
"cliente": { "resources": ["ordenes", "productos"],
             "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
```

validated, and then failed at request time on `productos`. Verified live against
the engine's own shipped `testdata/logistics` fixture, which carried exactly this
bug: `GET /api/incidents` as `operario` returned

```
422 {"error":"validation_failed","fields":[{"field":"operator_id","rule":"unknown_field"}]}
```

— a schema misconfiguration reported as if the *caller* had sent a bad field.

The condition field must now exist on **every** resource the role lists, named
precisely in the error, with the fix pointing at per-resource `permissions`. Virtual
custom-route segments in a role-global list are skipped (they are not tables). The
three shipped logistics fixtures and one AI gold-corpus schema carried the latent
bug and were corrected to the per-resource form.

The `fields` allowlist keeps the union rule: it is a projection filter, so a field
missing on one resource simply projects nothing there — fail-closed already.

## Verification

- `pkg/rbac/route_grant_test.go` — positive grant, and the negatives: undeclared
  action, undeclared segment, role with no `routes`, unknown role, no widening of
  resource access, wildcard unaffected, and a full regression matrix for policies
  without `routes`.
- `pkg/schema/route_grants_test.go` — schema-level shape, resource collision,
  `conditions`/`fields` rejection, unknown-key rejection, and the fail-closed
  role-global condition.
- `route_grants_boot_test.go` — the boot cross-check, including the deploy path.
- Hot path: `make bench-protocol` on the RBAC-carrying read/write path —
  **`no_change`** (numbers in the session report).
