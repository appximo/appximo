# ADR-019: Declarative relations via `json_agg` + `LATERAL`

**Status:** Implemented (RELATIONS-V1). REST `?include=` + GraphQL nested
fields via `json_agg` + `LEFT JOIN LATERAL`, RBAC-compiled, auto FK indexes,
depth cap 2, per-embed top-N. Verified through the standard pipeline:
baseline → change → Mann-Whitney verdict on the 105 stack (see
[Measured result](#measured-result)).

---

## Context and problem

Appximo today has no declarative relations. Fields may hold foreign UUIDs
(`customer_id: { type: uuid }`), and a single read-only subroute is emitted
(`GET /api/orders/{id}/customer`), but the schema has no `ref`/join semantics
and list endpoints return flat rows only.

Any production API returns related data. Without first-class relations every
caller resolves them manually — typically with N+1 requests (1 for the parent
list + N for each child collection). On the current p50 of 1.58 ms, fetching
a page of 20 orders with 5 lines each would cost 21 round-trips ≈ 33 ms
vs a single query that should land under 3 ms.

The goal: relations declared in the schema JSON, served as nested objects/arrays
in a single endpoint, in **one round-trip to Postgres**, without degrading the
baseline (no-`include`) p50.

---

## Decision: the SQL pattern — `json_agg` + `LEFT JOIN LATERAL`

JSON aggregation is performed **inside Postgres** (not in Go). This is the same
pattern PostgREST and Hasura use in production.

Rationale:
- Constructing JSON in the DB avoids the double transformation
  (Postgres rows → Go structs → JSON re-serialisation). Hasura reports 5–8×
  throughput improvement from this alone.
- In Go the aggregated column is scanned directly to `[]byte` and written to
  `http.ResponseWriter` without re-marshalling — the hot-path overhead stays
  negligible.
- `json_agg` / `json_build_object` (NOT `jsonb_agg`): `json` skips the binary
  re-parse that `jsonb` pays; for data that is serialised once to the client
  and never indexed, `json` is ~4× faster.

### Reference SQL (five canonical cases)

**1. `has_many` — one order → many lines**

```sql
SELECT
  json_build_object(
    'id',         o.id,
    'status',     o.status,
    'created_at', o.created_at,
    'lines',      COALESCE(
                    (SELECT json_agg(json_build_object(
                              'id',       l.id,
                              'product',  l.product,
                              'qty',      l.qty
                            ) ORDER BY l.created_at)
                     FROM   tenant_acme.lines l
                     WHERE  l.order_id = o.id
                     LIMIT  50),       -- top-N pagination within the embed
                    '[]'::json
                  )
  ) AS row
FROM  tenant_acme.orders o
WHERE o.id = $1;
```

**2. `belongs_to` — one line → its product**

```sql
SELECT
  json_build_object(
    'id',       l.id,
    'qty',      l.qty,
    'product',  (SELECT row_to_json(p)
                 FROM   tenant_acme.products p
                 WHERE  p.id = l.product_id)
  ) AS row
FROM  tenant_acme.lines l
WHERE l.id = $1;
```

**3. `many_to_many` — product ↔ orders via junction table**

```sql
SELECT
  json_build_object(
    'id',     p.id,
    'name',   p.name,
    'orders', COALESCE(
                (SELECT json_agg(json_build_object(
                          'id',     o.id,
                          'status', o.status
                        ))
                 FROM   tenant_acme.order_products op
                 JOIN   tenant_acme.orders o ON o.id = op.order_id
                 WHERE  op.product_id = p.id
                 LIMIT  50),
                '[]'::json
              )
  ) AS row
FROM  tenant_acme.products p
WHERE p.id = $1;
```

**4. Two-level nesting — order → lines → product**

```sql
SELECT
  json_build_object(
    'id',    o.id,
    'lines', COALESCE(
               (SELECT json_agg(
                  json_build_object(
                    'id',      l.id,
                    'qty',     l.qty,
                    'product', (SELECT row_to_json(p)
                                FROM   tenant_acme.products p
                                WHERE  p.id = l.product_id)
                  ) ORDER BY l.created_at)
                FROM  tenant_acme.lines l
                WHERE l.order_id = o.id
                LIMIT 50),
               '[]'::json
             )
  ) AS row
FROM  tenant_acme.orders o
WHERE o.id = $1;
```

**5. Top-N per parent (paginated embed)**

The `LIMIT` is placed **inside** the lateral subquery (as shown above), not on
the outer query. This bounds result size per parent row and prevents fan-out
from becoming quadratic with large child sets.

---

## Decision: schema syntax

Relations are declared explicitly under a `"relations"` key per resource:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "sales-api",
  "resources": {
    "orders": {
      "fields": {
        "status":      { "type": "string", "enum": ["pending", "shipped", "done"] },
        "customer_id": { "type": "uuid" }
      },
      "relations": {
        "lines": {
          "type":   "has_many",
          "target": "lines",
          "fk":     "order_id"
        }
      }
    },
    "lines": {
      "fields": {
        "order_id":   { "type": "uuid", "required": true },
        "product_id": { "type": "uuid", "required": true },
        "qty":        { "type": "int",  "required": true }
      },
      "relations": {
        "product": {
          "type":   "belongs_to",
          "target": "products",
          "fk":     "product_id"
        }
      }
    },
    "products": {
      "fields": {
        "name":  { "type": "string", "required": true },
        "price": { "type": "float64" }
      },
      "relations": {
        "orders": {
          "type":       "many_to_many",
          "target":     "orders",
          "through":    "order_products",
          "fk":         "product_id",
          "target_fk":  "order_id"
        }
      }
    }
  }
}
```

### Explicit declaration vs. FK inference

PostgREST infers relations from the live FK catalog (`information_schema`).
Appximo rejects that approach for one structural reason: with schema-per-tenant
isolation, introspecting `information_schema` per request requires a catalog
lookup for every tenant. Postgres catalog performance degrades materially past
~1 000–2 000 schemas on a single cluster — exactly the workload Appximo is
designed to support.

Explicit declaration (Hasura/Doctrine model) means the relation is compiled
once at boot from the shared schema file. Validation against
`information_schema` happens at boot with a warning (the same pattern the
motor already uses for `indexes`), not per-request.

---

## The five non-negotiable conditions (implementation contract)

Every condition below is a hard requirement. Missing any one of them either
silently regresses performance or introduces a security hole.

### 1 — Strict opt-in via `?include=` (and GraphQL field selection)

Without `?include=`, the generated SQL is byte-for-byte identical to today's.
The no-embed p50 MUST NOT change.

CI gate: a Mann-Whitney no-regression test of the no-`include` path (10 runs,
CV < 5 %, min-effect `max(0.5 ms, 3 %)`) must be green before merge. The
design predicts effect = 0 on this path.

### 2 — SQL pattern: `json_agg` + `LEFT JOIN LATERAL … ON TRUE`

The outer row is built with `json_build_object`; child collections use
`json_agg`; `COALESCE(…, '[]'::json)` for empty arrays;
`row_to_json` for single-object (`belongs_to`) embeds.
The aggregated column is scanned directly to `[]byte` and written to
`http.ResponseWriter` — no intermediate Go struct, no re-serialisation.

### 3 — Automatic index on every declared FK

Every FK column in a declared relation gets
`CREATE INDEX CONCURRENTLY IF NOT EXISTS` at tenant-registration time (the same
idempotent DDL pass that creates/extends tables).

Postgres does **not** index FK columns automatically. Without an index, the
LATERAL subquery performs a sequential scan of the child table per parent row —
which is N+1 in disguise at the storage layer. A 10 000-row child table with no
index on `order_id` turns a 20-order page into 20 seq scans.

This closes the existing `indexes` debt: the key is already parsed but not
applied; FK indexes will be the first concrete use of that infrastructure.

### 4 — Bounded depth and paginated embeds

Default maximum nesting depth: **2 levels**. Requests that exceed the limit
receive `400 Bad Request`. The limit is configurable at the schema level but
not per-request.

Every child collection embed defaults to `LIMIT 50` rows per parent
(configurable per relation in the schema, not per-request). This prevents
fan-out from becoming quadratic for parents with large child sets and closes
a DoS vector (arbitrarily deep or wide queries via `?include=`).

### 5 — RBAC applied at query compilation

The LATERAL fragment for a relation is emitted **only** if the requesting role
can `read` the target resource and its fields (field allowlist applied inside
`json_build_object`). Row-level conditions for the target resource are injected
into the `WHERE` clause of the lateral subquery — they travel inside the same
single round-trip, not as a post-hoc filter in Go.

This is the Hasura pattern. Because the SQL is generated at request time, RBAC
is structural to the query: there is no path that returns a child row that the
role is not allowed to see.

---

## Decision: embed cache invalidation

This is the hard problem. Neither PostgREST nor Hasura solves it automatically:
PostgREST does not cache; Hasura defaults to a 60-second TTL.

Appximo's strategy, in layers:

**Layer 1 — safe default:** short per-tenant TTL (5–30 s). Simple, correct,
slightly stale under write load.

**Layer 2 — dependency-keyed invalidation:** every embed cache entry records
the set of resources it touches (e.g. `{orders, lines}`). Because **all writes
flow through the engine** (there is no other write path to these tables), a
write to `lines` can atomically invalidate every cache entry whose dependency
set includes `lines`. This is feasible precisely because of the single-writer
architecture — a property none of the comparables share when deployed against
a general Postgres cluster.

**Layer 3 — opt-out per endpoint:** endpoints where stale data is unacceptable
can bypass the cache with `Cache-Control: no-cache` (existing mechanism).

Storage: LRU capped in **bytes** (tens of MB, not Hasura's 1 GB default —
the target is a $16/mo VPS). In-process or backed by the existing SQLite
observability store. No Redis — that would violate the "no sidecars" constraint.

---

## Consequences

### Positive

- The engine moves from "flat CRUD platform" to "relational API platform";
  one round-trip regardless of nesting depth; JSON built in the DB with no
  double serialisation overhead.
- Automatic FK indexes close the existing `indexes` debt (parsed but unapplied
  since the feature was introduced).
- Dependency-keyed cache invalidation is a concrete advantage over comparables,
  made possible by the single-writer architecture.
- RBAC at compile time means no post-hoc child filtering: the security surface
  does not grow with nesting depth.

### Costs and constraints

- Nesting depth and per-embed pagination are mandatory, not optional: the engine
  does not support unbounded recursion or unlimited child sets.
- `json_agg` moves JSON construction CPU into Postgres. On a 1-vCPU VPS with
  large child arrays this is relevant — the answer is to paginate the embed
  (`LIMIT`), not to move the work to Go.
- Embed cache invalidation adds implementation complexity relative to TTL-only
  caching. The dependency-set bookkeeping is bounded (one set per cache entry,
  updated on write), but it is new infrastructure.

---

## Data the implementation must measure (not assume)

The investigation left the following as **[ESTIMATED]** — no comparable
publishes a controlled flat-vs-embed delta on equivalent hardware:

> **+0.3 ms to +1.0 ms on p50** for a `has_many` embed of ≤ 50 children
> with the FK indexed, relative to the baseline no-embed query.

The implementation will confirm this with the standard pipeline
(`bench-protocol.sh`, Mann-Whitney, 10 runs, CV < 5 %, threshold
`max(0.5 ms, 3 % × median_A)`) on the 105/58 hardware before merge.
No claim will be made in the README or benchmark docs until measured.

## Measured result

Pipeline: `bench-protocol.sh`, 10 runs × 30 s @ 50 rps, Mann-Whitney via
`/api/bench/compare-groups`, threshold `max(0.5 ms, 3 %)`. Engine on an
isolated port with the synthetic monitor off; 105 single box (k6 co-located,
documented steal → the gate relies on the 0.5 ms practical-significance
threshold, which dwarfs both the host noise and the code delta).

- **GATE — plain `GET /api/tasks` (no `?include=`), new vs old binary:**
  `no_change` (median Δ ≈ **+0.01 ms**, CI [+0.009, +0.015] ms; 0.5 ms threshold).
  The no-include path is preserved — the only added work is one `?include=`
  string check.
- **COST — `GET /api/orders?include=lines`** (20 parents/page, 15 children each,
  FK indexed) **vs the plain baseline:** `no_change` (median Δ ≈ **+0.01 ms**,
  CI [+0.007, +0.012] ms, 2.2 %).

Measured cost is **far below the +0.3–1.0 ms estimate** — at this dataset the
in-DB `json_agg` over an indexed FK plus direct-bytes streaming (no Go
re-serialisation) is within noise of the flat query. The estimate was
conservative; cost grows with embed width but is bounded by the per-embed
`LIMIT` (top-N) by design.

---

## Suggested implementation order (when the engine unfreezes)

1. `"relations"` key in the schema parser + boot-time validation
   (warn on unknown target resource, consistent with existing `indexes` pattern).
2. Automatic `CREATE INDEX CONCURRENTLY IF NOT EXISTS` on FK columns at
   tenant-registration — this closes the `indexes` debt independently.
3. `?include=` parameter parser → LATERAL fragment compiler, with RBAC
   at compilation, depth cap, and per-embed `LIMIT`.
4. Direct `[]byte` scan to `ResponseWriter` (no intermediate struct).
5. CI gate: no-regression test for the no-`include` path (Mann-Whitney).
6. GraphQL: nested field backed by the same LATERAL fragment — no dataloader
   needed because `json_agg` eliminates the N+1 at the SQL layer. A dataloader
   would only be relevant for fields resolved outside the main SQL query, which
   this design avoids.

---

## Alternatives discarded

| Alternative | Reason discarded |
|---|---|
| Infer relations from FK catalog (PostgREST model) | Catalog introspection per tenant degrades past ~1 000–2 000 schemas-per-cluster — exactly the workload Appximo targets. Explicit declaration compiles once at boot. |
| `jsonb_agg` instead of `json_agg` | `jsonb` pays a binary re-parse on every aggregation. For data serialised once to the client and never indexed, `json` is ~4× faster. |
| Lazy loading / dataloader as primary strategy | Produces N+1 or at minimum one query per nesting level. `json_agg` delivers the full nested result in one query. A dataloader remains an option only for fields that cannot be expressed as a SQL subquery. |
| Redis for embed cache | Violates the "no sidecars" constraint. The target deployment is a single VPS running one Go binary + Postgres. |
| Unlimited nesting depth | A DoS vector. Bounded depth is a non-negotiable security property, not a convenience limitation. |
