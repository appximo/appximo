# Build a complete backend with Appximo — the agent guide

You are an AI coding agent (Claude Code, Cursor, or similar). This document
teaches you to build a **complete, secure backend** with Appximo: not only the
declarative schema, but the **custom Go handlers, hooks, auth and background
jobs** that a real product needs — and to do it **safely**, following the
in-process safety model the engine enforces.

Appximo is a Go engine that compiles a JSON schema into a multi-tenant
REST + GraphQL + OpenAPI server at boot. Most of an app is *declarative* (the
schema). The rest — logic that spans resources, calls external systems, or runs
in one transaction — is a **custom handler**: plain Go, imported as a library,
compiled into the same static binary, running **in-process** with the tenant's
transaction and RBAC already resolved. That in-process model is the differential
(no network hop, one transaction, the engine's own validation + RBAC), and this
guide is how you wield it without the footguns.

Four companion documents; keep them straight:

| Doc | Teaches | Command |
|---|---|---|
| **`appximo spec`** / [SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md) | the **schema** (declarative surface) | `appximo spec` |
| **this doc** / `appximo backend-spec` | the **backend** (handlers + hooks + auth + jobs) | `appximo backend-spec` |
| **`appximo frontend-spec`** / [FRONTEND_SPEC_LLM.md](FRONTEND_SPEC_LLM.md) | the **frontend** (the API contract a UI consumes, screen states, files) | `appximo frontend-spec` |
| **`appximo backoffice-spec`** / [BACKOFFICE_SPEC_LLM.md](BACKOFFICE_SPEC_LLM.md) | a **generated admin CRUD UI** driven by /openapi.json | `appximo backoffice-spec` |
| **`appximo quickstart`** / [LIFECYCLE_SPEC_LLM.md](LIFECYCLE_SPEC_LLM.md) | **operating** it (install → tenant → users → production) | `appximo quickstart` |
| [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md) | the complete human reference | — |

(`appximo specs` prints all five at once — one paste gives an agent the
whole contract.)

Everything below is audited against the engine source and demonstrated by a
compiling, runnable example: **[examples/backend-guide/](../examples/backend-guide/)**
(`schema.json` + `main.go`). Every code block here is faithful to that example.
Do not invent API surface beyond what is listed — if a method is not here, the
Ctx does not have it.

---

## 1. Where does each piece of logic go?

The first decision for any feature: which layer owns it. Get this right and most
of the backend is declarative; only genuine logic becomes code.

| Put it in… | When the logic is… | Cost / guarantees |
|---|---|---|
| **the schema** (resources, fields, validation, RBAC, relations, state machines, events, indexes) | structural: shape, types, constraints, who-can-do-what, lifecycle | zero code; compiled at boot; the default — reach for it first |
| **a hook** (`before_create`/`before_update`, `js`/`wasm`) | pure per-record validation or transformation, no I/O | sandboxed, watchdog-timed; **no HTTP, no DB, no auth** — see §4 |
| **a custom handler** (`app.Register(Route{…})`) | logic needing a transaction, cross-resource writes, external calls, computation, or a pre-auth endpoint | in-process; the tenant tx + RBAC resolved for you; this doc |
| **a background job** (`Ctx.SafeGo` or the outbox worker) | work that happens *after* responding | goroutine = at-most-once, non-durable; outbox = durable, retryable — see §6 |
| **an external service** | almost never | a network hop you must secure and operate yourself |

Two principles decide the ambiguous cases:

1. **Default in-process.** If it needs the transaction, RBAC, or the request's
   data, it belongs in a handler — not a separate service. The whole point is
   that a handler runs *inside* the engine with everything resolved.
2. **External side effects go after the commit; durability decides how.** Never
   call an external system *inside* the transaction (a slow/failed call would
   hold or poison the tx). If the side effect must survive a crash and be
   retried (a payment, a provisioning call), **enqueue it to the outbox**
   (`Ctx.Enqueue`) — it commits atomically with your write and a worker delivers
   it. If it is best-effort and losing it is acceptable (a metric ping, a cache
   warm), fire it with `Ctx.SafeGo` after you respond.

---

## 2. The schema (declarative surface)

The schema is generated and validated with the **other** flow — run `appximo
spec`, generate against it, and self-correct with `appximo validate --json`
until valid ([SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md)). This doc assumes you have
a valid schema; it covers only the code that sits on top.

One rule that matters for handlers: **resource CRUD is already generated.** A
resource `students` gives you `GET/POST/PUT/PATCH/DELETE /api/students` with
filters, sort, pagination, validation and RBAC — for free. Do **not** write a
custom handler for plain CRUD; write one only for logic the schema can't express.
Custom routes may not live under a resource's `/api/<resource>` prefix (it's
owned by the generated routes — a collision is rejected at boot).

Four schema facts that change how you write handlers:

**Generated reads are `SELECT *`.** The list/get path selects every column of the
table, not the declared field list. That is load-bearing: a column that exists in
the database but *not* in the schema still comes back on `GET /api/products`.
Writes are the opposite — an undeclared key is rejected `422 unknown_field` — so
an undeclared column is **readable but only writable from your own SQL**. This
used to be the escape hatch for anything the grammar couldn't express (see
"undeclared columns" below).

**Money is `int64` in minor units.** There is no `decimal`/`money` type, and
`float64` money is a rounding bug on a timer. Store cents (or the currency's
smallest unit) in an `int64` and put the unit in the name — `price_cents`,
`total_cents` — so it is impossible to misread at a call site. Most payment APIs
(Stripe, Wompi, Adyen) speak minor units too, so money never converts. Format for
display at the edge, never in the database.

**Documents go in `jsonb`, not `json`.** `jsonb` is a real Postgres jsonb column:
containment (`@>`) works and an `indexes` entry can declare
`"method": "gin"` (with `"opclass": "jsonb_path_ops"` when the index only ever
answers `@>`). `json` is stored as TEXT — nothing you can query or index. Use
`jsonb` for merchant-defined attributes, settings blobs, raw webhook payloads
you may need to search. pgx decodes a `jsonb` column straight into a Go
`map[string]any` and encodes a Go map back — no manual marshalling.

**What a `json` (or `jsonb`) field ACCEPTS and RETURNS — the same on every
door (ADR-028, MOTOR-TIPO-JSON-S1).** Both types hold a JSON VALUE:

- **Write** (REST POST/PUT/PATCH, GraphQL, `/api/transaction`, `Ctx.Insert`/
  `Ctx.Update`): any JSON value — an object `{"nit":"900"}`, an array
  `[1,2,3]`, a number, a boolean, nested as deep as you like — is stored as
  the value. A **string is read as JSON TEXT** (the document's source, the
  same convention Postgres and pgx use for `'…'::jsonb`): `"{\"nit\":\"900\"}"`
  is the object, `"123"` is the number 123. A string that is not valid JSON
  (`"hola mundo"`, `""`, `"[1,"`) is a **422** `{"field":"data","rule":"type",
  "message":"field \"data\" must be a JSON value; a string is read as JSON
  text and this one is not valid JSON"}` — on both types (it used to be a
  500 on `json` for an object and an anonymous 400 on `jsonb` for a bad
  string). `null` is NULL, governed by `required`. There is no way to store
  free text in a `json` field: declare `string`/`text` for that.
- **Read over HTTP**: the value comes back NATIVELY — `"data": {"nit":"900"}`,
  never `"data": "{\"nit\":\"900\"}"` — on REST list/get/create/update
  responses, relation subroutes, `?include=` embeds, GraphQL (a `json` field
  is the `JSON` scalar, like `jsonb`), SSE events, batch results, the admin
  data browse and the after-hook webhook payload. Round trip: what you write
  is what you read.
- **Storage**: `json` is TEXT holding canonical compact JSON (Go's encoding:
  keys sorted, numbers through float64 — the HTTP path's ~2^53 limit, which
  is also what a `jsonb` read gives you); `jsonb` is the Postgres document.
  `?filter[<json>][eq]=` compares that canonical text — fragile by nature;
  real document queries are `jsonb` + `@>`.
- **Canonicalization — byte identity is NOT reachable through the API, and
  that is not data loss (verified with real requests, MIGRACION-CONFIANZA-S1).**
  What you send is decoded into Go values and re-encoded, on the way in AND
  on the way out, for BOTH types. Concretely — sent
  `{"zeta":1,"alpha":{"y":2,"x":1},"ratio":0.01000000000000000020816681711721685132943093776702880859375,"big":12345678901234567890,"dec":1.50,"list":[3,1,2]}`,
  stored and returned:
  `{"alpha":{"x":1,"y":2},"big":12345678901234567000,"dec":1.5,"list":[3,1,2],"ratio":0.01,"zeta":1}`.
  Three transformations: (1) **object keys come back sorted** (nested ones
  too; array ORDER is kept); (2) **floats are re-rendered in shortest
  round-trip form** — `0.01000…0859375` IS the float64 `0.01`, same IEEE-754
  bits, printed shorter; `1.50` → `1.5` (the trailing zero is not a value);
  (3) **integers beyond 2^53 lose digits** — `12345678901234567890` →
  `12345678901234567000` (ENG-50: the only one of the three that IS a loss;
  it applies in both directions on both types — `jsonb` is decoded to
  float64 on read too). Whitespace is never kept. `jsonb` additionally
  applies Postgres's own normalization (duplicate keys collapse, its own key
  order), which the read path re-sorts anyway.
  **The exact-fidelity door, if you need one:** write the document as a
  JSON-TEXT STRING on a `json` (TEXT) field — `{"data": "{\"big\": 12345678901234567890, \"dec\": 1.50}"}`
  — the engine validates and COMPACTS it but keeps its numeric text and key
  order, and the read path emits the stored text verbatim; only whitespace
  differs. That is the migration path for documents with big integers or
  exact decimals; it does not exist for `jsonb`.
  **Parity note for migrations:** a migrator that checks "source == Appximo"
  must canonicalize BOTH sides before comparing — parse each side, sort keys
  recursively, and compare VALUES (numbers as float64, or as `Decimal` when
  the source has them and the target used the string door), never bytes. In
  Python: `json.dumps(json.loads(x), sort_keys=True, separators=(",", ":"))`
  on both; in Go: `json.Unmarshal` into `any` + `reflect.DeepEqual`; in
  PostgreSQL: cast both to `jsonb` and compare with `=` (jsonb equality is
  value equality). Diffs that survive that are real.
- **Library read**: `ctx.Query`/`QueryOne` return a `json` column as the
  stored **`string`** (unchanged — unmarshal it yourself when you need the
  document) and a `jsonb` column as the decoded `map[string]any`/`[]any`.
  `ctx.Insert`/`ctx.Update` take the same values the HTTP doors take:
  `{"data": map[string]any{...}}` and `{"data": "{\"k\":1}"}` both store the
  object; a non-JSON string is a `*ValidationError` with the 422 fields.
- Before ADR-028 (v0.1.9 and earlier) a `json` field accepted ONLY a string
  and returned it escaped; a client that parsed the string must stop parsing.

### 2b. Writing from OUTSIDE the binary — loading data through the API

An external process (a migration, an importer, a script) has three write
doors and no fourth:

| Door | Per request | Atomicity | Cost measured (dev box, 1 vCPU, local Postgres) |
|---|---|---|---|
| `POST /api/<resource>` | one row | one row | 100 sequential POSTs ≈ 1.06 s (~10.6 ms each, connection reuse off) |
| **`POST /api/transaction`** | up to **100** create/update/delete ops, any mix of resources | **all-or-nothing** — one Postgres transaction | 100 creates ≈ **50–70 ms** (~0.6 ms/row); 100 creates carrying a 3 KB `json` document each (120 KB body) ≈ 100 ms |
| a custom Go route + `ctx.Insert`/`UnsafeTx` (§3) | whatever you write | your transaction | in-process, no HTTP per row |

**`POST /api/transaction` has existed since 2026-06 and is in every published
version (v0.1.8, v0.1.9, v0.1.10 and later — verified request by request).**
It was missing from the served `/openapi.json` until MIGRACION-CONFIANZA-S1,
which is why an agent reading the contract could conclude it did not exist;
it is published there now (`x-appximo-transaction: true`, with the request/
response components). The full contract is AGENTS.md §Atomic multi-resource
transactions and SCHEMA_REFERENCE §10.3; the shape:

```bash
curl -X POST https://acme.example.com/api/transaction \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{
  "operations": [
    {"op": "create", "resource": "declarations", "data": {"code": "D-1", "data": {"nit": "900123"}}},
    {"op": "create", "resource": "declarations", "data": {"code": "D-2", "data": {"nit": "900124"}}},
    {"op": "update", "resource": "products", "id": "…", "data": {"stock": 7},
     "guard": [{"field": "stock", "op": "eq", "value": 10}]}
  ]}'
# 200 {"results":[{…row…},{…row…},{…row…}]}
# any failure → the failing op's status + {"error","failed_operation":<index>,"op","resource"[,"fields"]}
```

- **Every op is authorized and validated exactly like its single-op twin**
  (per-resource RBAC incl. row conditions and the create mass-assignment
  block, declarative rules, state machines, `before_*` hooks); a role that
  may not perform one op fails the whole batch with `403` naming the index.
  A read-only role gets `403 forbidden` on op 0 — that is the batch working,
  not the endpoint missing. `GET /api/transaction` is `405`.
- **Limits, and they are real:** 100 ops per request (`APPXIMO_MAX_TX_OPS`;
  101 → `400 too many operations: 101 (max 100)`), the 1 MiB body cap
  (`413`), one tenant per request (Host). Governed fields (`id`, `auto`
  timestamps) are `422 read_only` inside a batch too — an import that must
  keep its source ids/timestamps declares `import` on the resource
  (AGENTS.md §Importing rows).
- **What does NOT exist (do not design around it):** a `COPY`/bulk-import
  endpoint, a file-driven importer, streaming writes, or memory backpressure
  that makes a small box HOLD a firehose. A 46k-row migration is ~460
  batches of 100 — minutes, not hours — but plan the load: batch, pause on
  `503`/`429`, and give the box swap (docs/PRODUCTION.md §Prerequisites).
  Under host memory pressure the engine refuses NEW writes with a `503` that
  names `MemAvailable+SwapFree` and the floor (`APPXIMO_MEMORY_GUARD_MIN_MB`);
  reads continue. That is degradation, not capacity.
- Not in v1 for batch ops: `after_*` webhooks and SSE do not fire (react to
  the outbox events instead); no GraphQL batch; no in-place arithmetic (use
  a compare-and-set `guard`).

### 2c. Reading from OUTSIDE the binary — `?fields=`: ask for the columns you will use

**The problem it solves is not bandwidth, it is disk.** A `json`/`jsonb`/`text`
column past ~2 KB lives in Postgres's TOAST storage; a `SELECT *` detoasts (and
decompresses) it for EVERY row of the page even when the caller only paints a
title and a status. A migrated system measured it on its very first screen:
`GET /api/declarations` — 20 rows, each with a large `data` document — was
**~940 KB per page and a p99 of 3.8 s**, for a list that never shows the
document. A projection applied in Go after the read would save the bytes on the
wire and none of the seconds: the TOAST is read before Go sees the row. **So
the engine pushes the selection down to the `SELECT` list** — a column that is
not asked for is not read — and says so honestly wherever it cannot.

**Syntax.** `?fields=id,nit,anio,estado` — one engine-owned parameter, a
comma-separated list of field names, on the routes that return rows of ONE
resource:

```
GET /api/declarations?fields=nit,anio,estado&filter[estado][eq]=radicada&per_page=20
GET /api/declarations/{id}?fields=nit,estado
GET /api/declarations/{id}/contador?fields=nombre,email      ← fields of the TARGET (contadores)
GET /api/declarations?fields=nit&include=contador             ← the root is projected; the embed is whole
```

It is the same shape as every other engine-owned list parameter (`sum=a,b`,
`group_by=a,b`, `include=a,b.c`) and the convention of every REST API that
has it (JSON:API `fields[type]`, Google APIs `fields=`, Stripe `expand`):
a name the caller reads in the contract and pastes, no new grammar.

**The rules — each one is a case the engine used to answer wrong somewhere
else, so they are the SAME rules (ADR-024 / ENG-14…ENG-30), not new ones:**

- **`id` always comes back**, asked for or not. Without it the references, the
  cursors and every generic client (the embedded `/app`) break; a projection
  that could drop the primary key is a footgun with no use.
- **A name that is not a declared field is a `400` naming it and listing the
  available set** — `unknown field in fields: datax (available: anio, created_at,
  data, estado, id, nit)` — exactly like `?sort=ghost` and `?filter[ghost]=`.
  Never silently ignored: a dropped name would hand the caller a page MISSING
  a column they asked for under a `200`, indistinguishable from "that column is
  empty". (It is a 400 and not a 422 on purpose: in this engine `422
  validation_failed` is the WRITE-BODY contract — `{"error","fields":[…]}` on
  create/update — and every query-parameter error is a `400` with a message;
  one client parser per class.)
- **A name the role's RBAC allowlist hides is OMITTED** — the allowlist wins,
  exactly as it does on a read without `fields=` (the column is simply not in
  the response; that hidden-attempt contract is registered as RBAC-2). It is
  deliberately NOT the `403` of `?filter[hidden]=` / `?sort=hidden`: that one
  defends against a VALUE oracle (a hidden column revealed by match/no-match),
  and a projection reveals nothing. A 403 would also break every role-agnostic
  client — the contract does not publish allowlists, so the embedded `/app`
  cannot know which of its columns a role may see before asking (it broke
  exactly so in the browser). `fields=` can never widen what a role reads —
  only narrow it.
- **Empty and malformed values are named `400`s:** `?fields=` (an empty form
  field), `?fields=a,,b` and `?fields=a,` ("empty entry in the field list —
  remove the extra comma"), `?fields=a&fields=b` (a repeated parameter — the
  engine will not guess which you meant). Whitespace around a name is trimmed;
  a repeated NAME (`fields=nit,nit`) is a set and is simply deduplicated.
- **The universe is the tenant's DEPLOYED surface**, like filters and sort: a
  column added by a hot migration is selectable without a restart.
- **Filters, search and sort are unaffected** — they live in the `WHERE`/
  `ORDER BY`, and Postgres orders by a column that is not in the select list
  without complaint. `?fields=nit&sort=anio` works.

**Where it applies — every door that returns rows of a resource, from ONE
implementation (`query.ParseFields` + the projected select list):**

| Door | `?fields=` | Notes |
|---|---|---|
| `GET /api/{res}` (list, page and cursor) | yes | the case that motivated it |
| `GET /api/{res}/{id}` | yes | a detail is usually where you WANT everything — but a label lookup by id, a status poll, a relation resolver are details too |
| `GET /api/{res}/{id}/{relation}` (subroute) | yes | names fields of the TARGET resource; the target's allowlist decides the 403 |
| `?include=` (list and get) | yes, on the ROOT | the embedded objects stay whole — there is no nested syntax (`fields[lines]=`), documented, not silent: a `fields=` entry never reaches an embed |
| GraphQL `{ res { data { a b } } }` / `{ singular(id) { a b } }` | **automatic** | the selection set IS the projection and is pushed into the SQL since MOTOR-FIELDS-S1 — before, GraphQL selected fields in Go over `SELECT *`, so it read the TOAST exactly like REST; a hidden field selected by a scoped role still resolves `null` (unchanged contract, the same omission REST applies) |
| `ctx.Query` (library) | `QueryOpts.Fields` | same validation (unknown → error naming it), the allowlist's omission |
| `/admin/tenants/{id}/data/{res}` (admin browse) | yes | same builder |
| `GET /api/{res}/aggregate` | no | returns no rows; a `fields=` there is an unknown function, a named 400 as before |
| `GET /api/{res}/events` (SSE) | no | the event carries the row that changed, allowlist-scoped; a projection of a push is a different feature |
| `POST /api/transaction`, writes | no | write doors return the written row |

**What it is NOT.** It does not change the default: a request without
`fields=` is byte-identical to before (`SELECT *`, same plan, same bytes — the
binary-diff gate and the frozen ABBA say so). It is not a per-field
`select: false` in the schema, and heavy fields are NOT dropped from
collections by default — see the proposal below. It does not project embeds
(`?include=`), it does not exist on aggregates or SSE, and it is not sparse
WRITES (a `PATCH` already is).

**How to know it worked — the plan, not the intuition.** The `Server-Timing`
header on every generated read carries the database stage (`query;dur=…`);
compare the same page with and without `fields=`. In the database,
`EXPLAIN (ANALYZE, BUFFERS)` of the two statements shows the difference as
buffers: the projected query touches the heap pages only; the `SELECT *`
touches the heap AND the TOAST relation (`pg_statio_user_tables.toast_blks_*`
moves). The integration test `pkg/integration/fields_test.go` pins all three
at once: the SQL the engine emitted (through a pgx tracer), the TOAST buffers
of that SQL, and the bytes on the wire.

**Measured (MOTOR-FIELDS-S1, the report's case rebuilt: 46,119 rows, a ~52 KB
`json` document each, 20 per page, dev box 1 vCPU + local Postgres —
docs/BENCHMARKS.md §4b):** page 1 is **961,702 B / query 53 ms** without
`fields=` and **3,059 B / 1.2 ms** with `fields=nit,anio,estado,contador_id`;
10 pages read **1,300 TOAST blocks vs 0**; under 10 rps of random pages the
p50/p95/p99 go from **174 ms / 2.25 s / 2.8 s** (575 MB received in a
minute — the report's regime) to **20 ms / 81 ms / 175 ms** (2.2 MB). What the
projection does NOT remove: the OFFSET of a deep page (the last page still
costs ~59 ms with `fields=`: the index scan walks 46k entries) and the
`COUNT(*)` — different features (keyset cursors, `?count=`).

**PROPOSED, NOT BUILT — omitting heavy fields from collections by default.**
The report's alternative ("or exclude large fields from collections by
default") is a CONTRACT BREAK: every existing client that lists a resource and
reads `row.data` would get `undefined` after an upgrade, silently — the exact
class ADR-024 forbids. It also cannot be decided by the engine: "heavy" is not
a type (`text` can be 3 bytes or 3 MB; a `jsonb` of attrs is what a catalogue
list SHOWS). So the migration path, if a second app asks for it, is a
DECLARATION on the field, never a flipped default:

```json
"data": { "type": "json", "list": "on_request" }
```

— list and subroute reads omit the field unless `fields=` names it; the
detail keeps it; `/openapi.json` publishes `x-appximo-list: "on_request"` on
the property so a generic client knows why a column is absent; `validate`
warns when a resource has no such declaration on a `json`/`jsonb`/`text`
field and a list page exceeds a size the author can set. Opt-in per schema
means the author breaks their own clients on purpose, at a version of their
choosing, and the contract says it. Registered as SCHEMA-8 in docs/BACKLOG.md;
`?fields=` is the half that is safe to ship today and the half a client can
adopt without touching the schema.

**Undeclared columns: still supported, no longer the default answer.** Creating
your own column with `BeforeStart` DDL works — the engine's migration is additive
and reports a column it does not know about as `gated_drops` (drift) rather than
dropping it. Reach for it only for what the grammar genuinely cannot express:
**CHECK constraints, generated columns, partial indexes** (an index `WHERE`
predicate is deliberately absent: it is arbitrary SQL rendered into DDL, and the
schema is also written by `ai-generate` and edited in Studio — see
[ADR-022](adr/ADR-022-declarative-surface-boundaries.md) for the full reasoning
and the structured-predicate design that would reopen it).
Anything the grammar *can* express — columns, btree/gin indexes, foreign keys,
unique constraints — belongs in `schema.json`, where the migration engine owns it.

---

## 3. Custom handlers in Go — the core

### 3.0 Getting the dependency — READ THIS FIRST

**The Appximo module IS published** (since v0.1.1, 2026-08-05):
`go get github.com/appximo/appximo@v0.1.1` fetches from the public proxy —
verified live. A plain `go mod tidy` against the import works; no `replace`
needed. (Historical note: before publication the only working recipe was a
local checkout plus a `replace`, and an agent guessing a version produced a
project that did not build — docs/AUTHORING_JOURNEY.md 5-7.)

**The checkout + `replace` recipe below remains valid** for developing against
an unreleased engine tree:

```bash
git clone <your-appximo-checkout> /path/to/appximo   # or use the one you already have
mkdir mybackend && cd mybackend
go mod init example.com/mybackend
```

```go.mod
module example.com/mybackend

go 1.25

require github.com/appximo/appximo v0.0.0

replace github.com/appximo/appximo => /path/to/appximo
```

```bash
go mod tidy      # now resolves: the replace points at real source on disk
go build -o mybackend .
```

What this costs you, stated plainly:

- **The path is absolute and machine-specific.** The project builds on the machine
  that holds the checkout and nowhere else — not on a teammate's laptop, not in CI,
  not in a plain `docker build` (the checkout is outside the build context).
- The Appximo checkout must be on the SAME Go version line (1.25) as your project.
- `v0.0.0` is a placeholder: the `replace` wins, so the version string is never
  resolved. Do not spend effort choosing it.

**When the module is published** (a public repo, or a private one plus
`GOPRIVATE`), the recipe collapses to the normal one and the `replace` line is
deleted:

```bash
go get github.com/appximo/appximo@v1.0.0   # a real tag
```

with, for a private repo:

```bash
export GOPRIVATE=github.com/appximo/*
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

Nothing else in this document changes: the import path, the API and every example
below are already written against the final path. This section is the only part of
the 10 % path that is blocked on a decision rather than on code — it is tracked as
**DOC-2** in `docs/BACKLOG.md`.

### 3.1 The program shape

A backend is a `main` that imports `github.com/appximo/appximo`, builds the
engine, registers routes, and starts it. The pure `appximo serve` binary is
exactly this with zero registered routes — your custom binary boots identically.

```go
package main

import (
	"log"

	"github.com/appximo/appximo"
)

func main() {
	app, err := appximo.New(appximo.Config{
		SchemaPath: "schema.json",
		// DSN / JWTSecret / AdminKey / Env / Port fall back to DATABASE_URL /
		// JWT_SECRET / ADMIN_KEY / APPXIMO_ENV / 8080. All three secrets are
		// required (from Config or env) or New returns an error.
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Register(appximo.Route{
		Method: "POST", Path: "/api/hello",
		Handler: func(ctx appximo.Ctx) error {
			return ctx.JSON(200, map[string]any{"hello": ctx.Tenant()})
		},
	}); err != nil {
		log.Fatal(err) // a bad route is a boot-time error, never a runtime surprise
	}

	if err := app.Start(); err != nil { // blocks; graceful drain on SIGTERM
		log.Fatal(err)
	}
}
```

> **Replacing a running binary in dev:** the graceful drain holds the LISTENER
> for a few seconds after SIGTERM (in-flight requests finish; `/readyz` flips
> to 503), so `kill <pid> && ./new-binary` races the port: the new process can
> fail `bind: address already in use`. Since ENG-34 the engine **binds first
> and announces after**, so a lost race prints ONLY the bind error — never a
> `serving on :PORT` for a port it does not hold (it used to, and a chained
> start looked alive while the OLD binary kept answering `/health`). Still:
> wait for the port to free
> (`until ! ss -ltn | grep -q :8620; do sleep 0.3; done`) before starting the
> replacement, and verify you're on the new build via `/health`'s version.
> (Production has this solved: `deploy-update.sh` does the atomic swap.)

`New` → `Register` (any number, **before** `Start`) → `Start`. `Register` after
`Start` is an error (routes are wired at boot).

**Boot work — `Config.BeforeStart`.** For anything that must be true before the
first request (your own DDL, seeds, a warm-up), use the boot hook rather than
opening a second pool from `DATABASE_URL` — which drifts from the engine's own
configuration:

```go
app, err := appximo.New(appximo.Config{
	SchemaPath: "schema.json",
	// Runs after the pool is open and the schema compiled, BEFORE the listener
	// accepts anything. A non-nil error ABORTS the boot — an app whose invariants
	// failed to install must not serve traffic.
	BeforeStart: func(ctx context.Context, pool *pgxpool.Pool) error {
		// The pool is NOT tenant-scoped: set the search_path yourself,
		// transaction-locally, as DATA — never by string concatenation.
		tx, err := pool.Begin(ctx)
		if err != nil { return err }
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", pgSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE variants ADD CONSTRAINT chk_stock
		    CHECK (reserved >= 0 AND reserved <= stock)`); err != nil {
			return err
		}
		return tx.Commit(ctx)
	},
})
```

`app.Pool()` exposes the same pool if you need it outside the hook. Do **not**
close it — its lifetime belongs to the App. Inside a request, always prefer `Ctx`
(whose transaction already carries the tenant's search_path and the role's row
filter). See [examples/backend-guide/main.go](../examples/backend-guide/main.go)
`ensureInvariants` for the full loop over tenants.

**Per-tenant work — `Config.OnTenantProvisioned`.** `BeforeStart` covers the
tenants that exist AT BOOT; a tenant registered while the app is LIVE (the
normal flow of a multi-tenant SaaS) needs the same DDL. Set the per-tenant twin
— it runs inside every registration, after the engine provisions the tenant's
tables, all-or-nothing (your error rolls the registration back):

```go
OnTenantProvisioned: func(ctx context.Context, pool *pgxpool.Pool, tenantID, pgSchema string) error {
	return applyMyDDL(ctx, pool, pgSchema) // same idempotent DDL BeforeStart applies
},
```

Without it, a fresh tenant is missing your DDL until a restart — a 500 on any
endpoint that depends on it (the exact bug PROD-JOURNEY-1B measured).

**The deployable contract — `appximo.ParseServeArgs`.** For the binary to be
installable/updatable by the official production tooling (ADR-023), main()
starts with:

```go
var version, revision = "dev", "unknown" // -ldflags -X main.version=… (scripts/build-consumer.sh)

args := appximo.ParseServeArgs("myapp", version, revision,
	appximo.ServeArgs{Port: 8099, ControlPort: 9099})
// wire args.SchemaPath/args.Port/args.ControlPort + Version: version into Config
```

It implements `myapp version` (the installer's identity check), accepts the
unit's `serve --schema … --port …`, and fails LOUD on any misplaced argument
(plain `flag.Parse` silently discards everything after a bare word).

### 3.2 The `Route`

```go
type Route struct {
	Method  string        // GET | POST | PUT | PATCH | DELETE
	Path    string        // must begin with "/api/"
	Handler func(Ctx) error

	Description string        // optional: one-line summary published in /openapi.json
	RequireRole string        // optional: demand this exact JWT role (else 403)
	Public      bool          // optional: skip JWT + path-RBAC (pre-auth endpoint)
	Timeout     time.Duration // optional: per-endpoint deadline (default 5s)
	RateLimit   *RateLimit    // optional: this endpoint's own throttle {RPS, Burst}
	ByteServing bool          // optional: this GET streams a file (Ctx.ServeFile) — see below
}
```

- **`Path` must start with `/api/`** so it flows through the same middleware
  chain as generated routes (tenant → rate limit → JWT → RBAC). The first
  segment after `/api/` must **not** be a schema resource name.
- **Every registered route is published in the served `/openapi.json`**
  (ENG-33) — method, path, auth mode (`x-public: true` for a Public route,
  otherwise Bearer + the RBAC segment/action it demands), `x-required-role`,
  `x-byte-serving` — flagged `x-appximo-custom-route: true`. **`Description`**
  is the one thing only you can add: a one-line summary shown in the contract
  and in `/docs`. Set it on every route — it costs a string and it is what an
  external agent reads first. Shapes (request/response bodies) are deliberately
  NOT published — a Go handler declares none; they live in your contract sheet
  (§3.6b).
- **Every custom GET also serves HEAD** (ENG-32): same auth, same RBAC
  (read), headers only. `Ctx.ServeFile` answers HEAD natively (Content-Length,
  ETag, no byte copy) — link unfurlers and CDNs probe with HEAD before GET.
- **`Timeout`** bounds the handler: its context (and the tenant transaction) is
  cancelled after `Timeout`, so a slow query or hung outbound call is aborted and
  the tx rolls back. Default 5s. Set it higher for legitimately long work, lower
  for tight endpoints. It bounds the **request** goroutine only — a `SafeGo`
  goroutine gets its own deadline (§6).
- **`RequireRole`** demands the caller's JWT `role` equals it (an *additional*
  check on top of path-RBAC below).
- **`Public`** — see §3.5. Public + RequireRole is a contradiction (rejected at
  boot); a Public path must be literal (no `{param}`).
- **`RateLimit`** — this endpoint's own bucket, per (tenant, client IP). A public
  route with no declaration gets the engine's conservative default (5 rps / burst
  10), which is right for a public **write** endpoint (registration, a webhook)
  and far too low for a public **read** one: a storefront catalogue trips it under
  ordinary traffic. Declare `&appximo.RateLimit{RPS: 200, Burst: 400}` on that
  route instead of raising `APPXIMO_PUBLIC_ROUTE_RPS` process-wide, which would
  also loosen the endpoints that want the strict default. Both values must be > 0
  (a half-filled struct is rejected at boot). Set on an authenticated route, it
  adds a per-(tenant, IP) limit on top of the per-tenant one — useful for an
  expensive report or export.
- **`ByteServing`** — declares a GET route that streams a binary body via
  `Ctx.ServeFile` (a public product image, an authorized file download). It
  routes the response AROUND the response cache and the compression wrapper —
  which would otherwise buffer the whole blob in RAM, strip
  Content-Disposition/Accept-Ranges on a cache hit, and suppress sendfile.
  GET only; literal path (pass the file id as a query parameter). See
  `Ctx.ServeFile` below and the public-image worked example in
  `appximo frontend-spec` §7.5.

### 3.3 The `Ctx` — every method

The handler's single argument. Identity, tenant, and a **tenant-scoped
transaction** are already resolved — you write business logic, never
infrastructure.

**Identity** (already verified by the chain — never re-parse a JWT):

```go
ctx.Claims()  // Claims{UserID, Role, TenantID, ExternalClientID}
ctx.Tenant()  // tenant id, e.g. "acme" (from the Host subdomain)
ctx.Role()    // the JWT "role" claim
ctx.Allowlist("students") // (fields []string, mayRead bool) — the role's read projection
```

**Database** — the transaction is opened with the tenant's `search_path`
already set, so unqualified table names resolve to *this* tenant's schema. There
is **no** API that exposes the raw pool; isolation is by construction.

```go
ctx.Tx()       // pgx.Tx, tenant-scoped. Return nil from the handler → COMMIT; return err → ROLLBACK.
ctx.UnsafeTx() // the SAME tx, named so `grep UnsafeTx` audits every RBAC-bypass site
               // What you give up on this path (field report, atina): schema
               // defaults, declarative rules, the governed-field rule, the
               // role's row condition, and the engine's number/uuid handling —
               // pgx scans a uuid into [16]byte, not string; scan into a
               // pgtype.UUID or `string` explicitly. Prefer ctx.Insert/Update.
```

**RBAC-aware data helpers** — apply the role's row filter, validate against the
compiled schema rules, and project the permitted fields. **Prefer these.**

```go
// Query: filters are equality predicates (validated + bound); the role's row
// condition is ALWAYS applied on top — you cannot widen what the role may see.
rows, err := ctx.Query("students", appximo.QueryOpts{
	Filters: map[string]any{"country": "CO"},
	Limit:   50, OrderBy: "created_at", Desc: true,
})

// Get: ONE row by id, with the role's row condition applied and its field
// allowlist projected. (nil, nil) when the row is absent OR hidden from this
// role — the two are indistinguishable on purpose.
row, err := ctx.Get("students", id)

// Insert / Update run the SAME body preparation the generated POST / PATCH
// runs (codegen.PrepareCreate / PrepareUpdate — one shared function, not a
// second implementation): schema DEFAULTS for omitted fields, the declarative
// rules (required, enum, min/max, length, pattern, format), the value TYPE
// check, and the state-machine INITIAL states on create. Plus the field
// allowlist and the row condition. Update is PATCH semantics and enforces the
// declared transitions in SQL.
//
// ENGINE-GOVERNED FIELDS: the implicit `id` and every `auto` timestamp are
// rejected 422 read_only in the data map — on Insert AND Update, exactly like
// the generated endpoints (WRITE-ASYMMETRY-S1; one shared source, so passing a
// client body through verbatim is safe). The one exception: a resource whose
// schema declares `"import": {"roles": [...]}` accepts them on Insert from the
// granted roles (data migration / restores). Update never accepts them.
//
// THE IDENTITY COLUMN (MOTOR-AUTORIZACION-S1): for a role whose row condition
// is bound to the caller ($user_id / $external_client_id), the condition
// column is the server's on Insert AND Update — Insert forces it to the
// caller; Update REJECTS a data map that sets it to anything but the caller's
// own id (another id, nil) with the same 403 the generated PATCH answers. A
// custom route is not a way around it. A handler that must TRANSFER a record
// to another user runs as an unscoped role, or does it on UnsafeTx — a
// deliberate, greppable decision, never the client's body passed through.
row, err := ctx.Insert("students", map[string]any{"full_name": "Ana"})
row, err := ctx.Update("students", id, map[string]any{"country": "MX"})

// Numbers: pass what Go computed. int, int8..int64, uint*, float32/64 and
// json.Number are all accepted, so a value you READ from a row (int64 from the
// driver) can be written straight back. No float64 casts.
row, err := ctx.Insert("orders", map[string]any{"total_cents": int64(45_000)})
```

> **What is deliberately NOT identical** — these follow from a custom handler
> running INSIDE your transaction, not from a gap:
> - **`before_create` / `before_update` hooks do not run.** A js/wasm hook is
>   the schema's way to gate the GENERATED write; your handler IS the gate for
>   the writes it makes. If a resource's invariant must hold on every path,
>   put it in the schema (rules, state machine, constraints), not in a hook.
> - **After-hooks, the SSE broadcast and the outbox event fire on the generated
>   path only.** They are post-COMMIT, and your transaction has not committed
>   while your handler runs. Need an event? `ctx.Enqueue(topic, payload)` —
>   it writes to the outbox in YOUR transaction, so it is atomic with the row.
> - **Precision**: the HTTP path decodes JSON numbers into float64 (the JSON
>   limit, ~2^53). `ctx.Insert` stores the exact `int64` you passed. The
>   library path is the more precise of the two, on purpose.

**Row types** (so you never write defensive converters): every row is a
`map[string]any` with pgx's native Go values — `uuid` (and the implicit `id`)
arrives as a canonical **`string`**; `int` as **`int32`**, `int64` as
**`int64`**, `float64` as `float64`; `bool` as `bool`; `string`/`text` as
`string`; `time` (and `auto` timestamps) as **`time.Time`**; `jsonb` as a
decoded **`map[string]any`** (or `[]any` for an array document); `json` as the
stored JSON text, a `string` (ADR-028 — the HTTP surfaces return it natively,
the library hands you the text); a `file` field as its file-id
`string`; SQL NULL as `nil`. A numeric you'll do arithmetic on: assert the type
above directly (e.g. `row["precio_hora_centavos"].(int64)` for an `int64`
field) — no switch needed.

> **`QueryOpts.Filters` takes DECLARED FIELDS ONLY — `id` is not one of them.**
> The implicit primary key is not a declared field, so
> `ctx.Query("students", QueryOpts{Filters: map[string]any{"id": x}})` fails with
> `unknown filter field: id`. **Use `ctx.Get(resource, id)`** — it is the
> sanctioned lookup-by-id and keeps the row rule. Reaching for `ctx.UnsafeTx()`
> and a hand-written `SELECT … WHERE id = $1` is the wrong fix: it silently drops
> the role's row condition, so a caller can read a row the REST API would hide.

> ### RETURN the engine's write errors VERBATIM. Do not wrap them.
>
> `ctx.Insert` / `ctx.Update` return `*appximo.ValidationError`, which the
> middleware renders as the SAME per-field 422 the generated endpoints produce:
> `{"error":"validation_failed","fields":[{"field":"email","rule":"format",
> "message":"must be a valid email"}]}`. **`return err` is the right handler
> code**, and it is better than anything you would write:
>
> ```go
> row, err := ctx.Insert("students", data)
> if err != nil {
>     return err          // ✅ the caller gets every failing field, named
> }
> // ❌ return ctx.Error(422, "could not create the student", err)
> //    — one opaque sentence; the per-field detail the engine ALREADY computed
> //      is thrown away, and the form UI has nothing to highlight.
> ```
>
> A real evaluator wrapped these in generic messages early on and spent the rest
> of the build blind to the very 422 the engine was handing them. Wrap ONLY when
> you are adding information the engine could not have (which principal, which
> business rule) — and then keep the cause: `errors.As` the
> `*appximo.ValidationError` and merge its `Fields` into your response rather
> than replacing them.
>
> **The database-level failures are typed too (ENG-42)** — you never see a raw
> pgx error for the cases a form must distinguish, and `return err` renders
> each one as the generated endpoint's exact response:
>
> | error | means | rendered |
> |---|---|---|
> | `*appximo.ValidationError` | a field broke its rules; also an unknown field (`rule:"unknown_field"`) or a `file` id that references no file (`rule:"file_not_found"`) | 422, per-field |
> | `*appximo.UniqueViolationError` | that value is taken (`unique:true` or a unique index); `.Field` names the column | 409 `field "code": value already exists` |
> | `*appximo.ForeignKeyConflictError` | a reference to a row that does not exist, or a change a RESTRICT FK refuses | 409, safe message |
> | `*appximo.InvalidTransitionError` | the state machine refused the move | 422, same as PATCH |
> | `appximo.ErrUpdateConflict` | the row changed concurrently (re-read and retry) | 409 |
>
> The two a form UI always needs: **409 unique** = "change that value";
> **422 per-field** = "complete/fix these fields". Both come classified from the
> SAME source the generated path renders (one SQLSTATE ladder,
> `handlers.ClassifyWriteError`), so branching with `errors.As` is safe:
>
> ```go
> row, err := ctx.Insert("orders", data)
> var uve *appximo.UniqueViolationError
> if errors.As(err, &uve) {
>     // optional: your own wording — uve.Field names the column
>     return ctx.Error(409, "ya existe un pedido con ese código", err)
> }
> if err != nil {
>     return err // ✅ everything else: the engine's own 422/409/403 shape
> }
> ```
>
> An error the engine has NOT observed being caused by input stays raw and masks
> as 500 — deliberately. In a handler the value may be one YOUR code computed,
> and a 400 blaming the caller for your bug would point at the wrong party.

> **Ctx validates against the tenant's DEPLOYED schema (ENG-43)** — same seam as
> the generated routes: a column added by a hot migration is immediately
> writable through `ctx.Insert`/`Update`/`BindResource` WITH its declared rules
> compiled, and filterable through `ctx.Query`, no restart. What still needs a
> restart is unchanged (a NEW resource, hooks, GraphQL input types).

`Update` also enforces a declared **state machine**, with the exact semantics of
the generated PATCH (the guard lives in the UPDATE's WHERE — race-safe, terminal
states immutable, re-sending the current value is a no-op — so a transition
endpoint hit twice answers 200 idempotently; if yours must REJECT "it was
already in X", read the row first and check the previous state yourself). An illegal move
returns `*appximo.InvalidTransitionError` (→ the same 422 if you return it);
a concurrent-change conflict returns `appximo.ErrUpdateConflict` (→ 409). So a
custom route that advances a lifecycle needs NO transition table of its own —
restrict WHO may move (per-transition RBAC) in the handler, and let the engine
own WHAT moves exist:

```go
row, err := ctx.Update("orders", id, map[string]any{"status": "shipped"})
var ite *appximo.InvalidTransitionError
if errors.As(err, &ite) {
	return ctx.Error(409, "ese pedido ya no puede pasar a enviado: "+ite.Message, err)
}
```

**Request binding** (1 MiB cap — `appximo.MaxBodyBytes` — like the generated
routes):

```go
var body struct{ Msg string `json:"msg"` }
err := ctx.Bind(&body)                 // JSON-decode the body
err := ctx.BindResource("students", &dst) // + validate against the schema rules → *ValidationError (422)

raw, err := ctx.RawBody()              // the EXACT bytes, same cap. USE THIS FOR WEBHOOKS.
```

`RawBody` returns the body byte-for-byte as sent. **A signature is computed over
those bytes**: parsing and re-serializing changes key order and whitespace and
breaks every signature, so `Bind` is the wrong tool for a signed payload — verify
over `RawBody` first, then decode. The body is buffered once, so `RawBody` and
`Bind` compose in any order (a plain `io.ReadAll(ctx.Request().Body)` would leave
`Bind` with an empty stream, and would make you re-implement the size cap). Over
the cap → `appximo.ErrBodyTooLarge`, which maps to `413` if you return it.

**Outbox** — enqueue a durable job **inside the current transaction** (atomic
with your write; a handler error rolls it back too):

```go
id, err := ctx.Enqueue("email.send", map[string]any{"template": "welcome", "to": email})
```

**Create a user** — an identity in *this* tenant's `auth_users`, in the
handler's transaction (rolls back with everything else). The role comes from your
code, never the request; email is normalized + checked, password argon2id-hashed
and length-checked; an **empty password** creates an invitation user (can't
password-login until a reset). See §5.

```go
user, err := ctx.CreateUser(email, password, "student") // CreatedUser{ID, Email, Role, EmailVerified}

// MintToken: the session for that identity — byte-shape identical to what
// POST /auth/login issues (same claims, same secret, standard 24 h TTL), so
// it works on every generated /api route. userID must be non-empty (an empty
// identity matches no $user_id condition and reads as a guest everywhere) and
// the role must be schema-declared (ErrUnknownRole). This is how a custom
// registration endpoint auto-logs-in, like the engine's own /auth/signup.
token, err := ctx.MintToken(user.ID, user.Role)
switch {
case err == nil:
case errors.Is(err, appximo.ErrEmailTaken):     // duplicate in this tenant → your 409
case errors.Is(err, appximo.ErrInvalidEmail),
     errors.Is(err, appximo.ErrWeakPassword):   // → your 422
case errors.Is(err, appximo.ErrUnknownRole):    // role not in the schema RBAC
}
```

**Serve a stored file** — stream one of THIS tenant's files (the engine file
store — the same one `/api/files/{id}` serves) as the response, with Range,
strong ETag/304 and sendfile. The route must declare `ByteServing: true`
(ServeFile refuses otherwise, loudly). The handler decides WHO may fetch —
this is how a storefront serves product images to ANONYMOUS visitors while
every other file stays private (authorize by relationship, then serve):

```go
app.Register(appximo.Route{
	Method: "GET", Path: "/api/catalogo-imagen",
	Public: true, ByteServing: true,
	RateLimit: &appximo.RateLimit{RPS: 200, Burst: 400}, // image-sized budget
	Handler: func(ctx appximo.Ctx) error {
		id := ctx.Request().URL.Query().Get("id")
		var ok bool
		if err := ctx.UnsafeTx().QueryRow(ctx.Context(),
			`SELECT EXISTS(SELECT 1 FROM productos WHERE imagen_id = $1 AND estado = 'activo')`,
			id).Scan(&ok); err != nil || !ok {
			return ctx.Error(404, "not found", err) // uniform miss — no oracle
		}
		// The store is content-addressed: this id's BYTES can never change, so
		// a URL that embeds the id may be cached forever (FILES-2). A changed
		// image is a new id — and a new URL.
		return ctx.ServeFile(id, appximo.WithCacheControl(appximo.CacheControlImmutable))
	},
})
```

Facts: call it once, INSTEAD of `JSON` (an `Error` before it still wins — the
error path keeps its response); the metadata lookup reads committed state, not
the handler's tx; `ErrFileNotFound` is the exported uniform-miss sentinel.
Cache policy (FILES-2): no option ⇒ no Cache-Control (browsers revalidate —
cheap 304s via the strong ETag); `WithCacheControl(...)` declares one, sent
ONLY on the successful stream (never on the 404 path).
`CacheControlImmutable` (`public, max-age=31536000, immutable`) is safe
whenever the URL embeds the file id; do NOT use it on a URL that can start
serving a DIFFERENT file (a mutable `/api/logo` pointer) — there the default
revalidation is correct. HEAD on the route answers headers-only for free
(ENG-32).

**Safe goroutine** — the ONLY sanctioned way to start a goroutine (§6):

```go
ctx.SafeGo(func(bg context.Context) { /* fire-and-forget, panic-safe */ })
```

**Response** (buffered; flushed after commit, so a commit failure becomes a 500,
not a false 200):

```go
return ctx.JSON(201, map[string]any{"id": id})        // success
return ctx.Error(409, "email already registered", err) // error + a non-nil error to return
```

**Escape hatches:**

```go
ctx.Request()  // *http.Request (headers, query, etc.)
ctx.Context()  // context.Context — the handler's deadline-bounded context; PASS IT to every DB / HTTP call
```

Always pass `ctx.Context()` to your queries and outbound calls — that's how
`Route.Timeout` actually cancels them.

### 3.4 The safety rules (non-negotiable)

These are the Phase-0 rules the engine's in-process model depends on. Follow them
in every handler you write:

1. **Never `go func(){…}()` — always `ctx.SafeGo`.** A raw goroutine that panics
   crashes the **entire multi-tenant process** (Go's `recover()` cannot reach
   another goroutine). `SafeGo` wraps it in recover + a structured log + a metric,
   so a bad background task is a logged incident, not an outage. (For in-request
   parallelism use `appximo.SafeParallel` — §6 — which is bounded and
   panic-safe too.)
2. **External side effects go after the commit.** Never call an external API
   *inside* the transaction. Durable + retryable → `Enqueue` (outbox). Best-effort
   → `SafeGo` after you respond.
3. **A public route treats the caller as hostile.** Validate *every* input, take
   the role from your code (never the request), and use
   `json.Decoder.DisallowUnknownFields()` on sensitive bodies. The engine already
   applies a dedicated, tighter rate limit to public routes.
4. **Tenant scoping is automatic — never filter it by hand.** The tx is
   search_path-scoped and `ctx.Query`/`Insert`/`Update` apply the role's
   condition. Don't add a `WHERE tenant_id = …`; there is no cross-tenant pool to
   leak from.
5. **The transaction is a single connection — not concurrency-safe.** Do not run
   `ctx.Tx()` queries concurrently (not even reads). Parallelise external I/O or
   CPU with `SafeParallel`; serialise DB work on the tx. The tx also holds its
   row locks until your handler RETURNS — every millisecond you spend inside it
   is a millisecond other requests may wait on those rows.
6. **One statement for N rows — never a query per element.** The moment your
   handler loops over items and runs a statement inside the loop, you have
   written the request that works in a 5-row test and dies on the first real
   batch: each statement is a full round trip, so 3 small queries per item over
   400 items is 1,200 sequential round trips — at ~120 ms each that is a
   **two-minute request**, far past any `Route.Timeout`, holding the tenant
   transaction (rule 5) open the whole way down. Batch the READ with
   `= ANY($1)` and the WRITE with `unnest()` — §3.4b has the compiling,
   verified pattern. If you are about to write `for _, item := range items {`
   with a query inside: stop and read §3.4b first.

### 3.4b Batch patterns — resolving N records in one statement

The rule-6 shapes, from the shipped example (`POST /api/reprice` in
`examples/backend-guide/main.go` — it compiles and is verified live: a batch of
N updates runs as exactly TWO statements regardless of N).

**Read a whole set at once** — validate every id in ONE query, then check the
result in Go. pgx binds a Go slice directly to a Postgres array; no string
building, no loop:

```go
rows, err := ctx.UnsafeTx().Query(ctx.Context(),
    `SELECT id FROM courses WHERE id = ANY($1::uuid[])`, ids)
// scan into a set, then report the FIRST missing id with its index —
// a batch error message must say which element failed, not just "bad input".
```

**Write a whole set at once** — `unnest()` turns parallel arrays into a row set
your statement joins against. One UPDATE applies the entire batch:

```go
ids    := make([]string, len(items)) // $1
prices := make([]int64,  len(items)) // $2 — parallel arrays, same order
tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
    `UPDATE courses SET price_cents = u.price
       FROM unnest($1::uuid[], $2::bigint[]) AS u(id, price)
      WHERE courses.id = u.id`, ids, prices)
```

The same shape covers bulk INSERT (`INSERT INTO t (a, b) SELECT * FROM
unnest($1::uuid[], $2::bigint[])`) and bulk DELETE (`WHERE id = ANY($1)`).
When it applies: any handler whose input is a LIST — imports, reprices,
reorderings, bulk state changes. Cap the batch size explicitly (the example
uses 1–1000) so a hostile caller cannot hand you a million-element array.

**The other three ways to kill the 10%** (each already demonstrated in the
worked examples — follow the pointers, they compile):

- **Loading a table into memory.** Always bound reads: the public catalogue
  example (§3.6) queries with `ORDER BY … LIMIT 50`; `ctx.Query` takes
  `PerPage`. A handler that reads "all rows" works until a tenant has real
  data, then eats the process's RAM.
- **Filtering on an unindexed column.** If your handler's WHERE uses a column
  repeatedly, declare it in the resource's `indexes` block (§2) — the schema
  owns indexes; a hand-run `CREATE INDEX` on the tenant schema is drift the
  migration engine does not know about. A missing index turns your one good
  batched statement into a sequential scan per call.
- **Network calls inside the loop (or the tx).** Rule 2 already bans external
  calls inside the transaction; a loop makes it N times worse. Durable →
  `Enqueue` once per item (cheap: outbox rows in the same tx); best-effort →
  ONE `SafeGo` after the response, never one goroutine per element.

### 3.5 RBAC — the biggest design force in this document

**Read this before you write a line of schema.** RBAC shape decides your data
model, not just your auth code: get it wrong and you discover, three resources in,
that the only way to express "customers see their own orders *and* can check out"
is to denormalize a column onto every table. Everything else in this guide is
recoverable; this is the one that isn't.

#### The three role shapes

| Shape | Looks like | Use it for |
|---|---|---|
| **wildcard** | `{"resources":"*","actions":["*"]}` | admins/ops. Reaches every resource **and every custom route**. |
| **role-global** | `{"resources":["a","b"],"actions":["read"],"conditions":{…}}` | one rule across a set of resources that share the *same* ownership column. |
| **per-resource** | `{"permissions":{"orders":{…},"invoices":{…}}}` | the realistic case: each resource scoped by **its own** column. |

**Role-global carries ONE condition across every resource it lists.** That
condition is injected into the WHERE of *each* of them, so the column must exist on
**all** of them — the engine now rejects the schema at load if it doesn't, naming
the offending resources. If your resources are owned through different columns
(`projects.owner_id`, `documents.created_by`), role-global cannot express it; use
`permissions`.

```json
"member": {
  "permissions": {
    "projects":  { "actions": ["read","create","update"],
                   "conditions": { "field": "owner_id",   "op": "eq", "val": "$user_id" } },
    "documents": { "actions": ["read","update"],
                   "conditions": { "field": "created_by", "op": "eq", "val": "$user_id" } },
    "tags":      { "actions": ["read"] },
    "posts":     { "actions": ["read","update","delete"],
                   "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                   "condition_actions": ["update","delete"] }
  }
}
```

`tags` is unscoped (every row). `posts` is "read all, write own" via
`condition_actions`. A resource absent from `permissions` is **denied**.

**A row condition is single-column equality — there are no joins.** `op` must be
`eq`. So "a customer may read the LINES of their own orders" is not expressible as
`order_lines JOIN orders ON …`; the ownership column has to be **on the row being
filtered**. Denormalizing `user_id` onto the child table is the correct answer, not
a hack — it is how the filter becomes an index lookup instead of a join the
authorizer would have to plan. Budget for it in the data model.

#### Custom routes: the `routes` grant

A custom route is authorized by its **first `/api/` segment**, treated as a
*virtual resource*, with the action derived from the method (`GET`→read,
`POST`→create, `PUT`/`PATCH`→update, `DELETE`→delete). `POST /api/checkout` is
"create on `checkout`".

A role grants it with a **`routes`** block, which is **orthogonal** to
`resources`/`permissions` — different namespace (registered endpoints, not tables),
so a role may declare both:

```json
"customer": {
  "permissions": {
    "orders":      { "actions": ["read"], "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } },
    "order_lines": { "actions": ["read"], "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
  },
  "routes": { "checkout": { "actions": ["create"] } }
}
```

**This is the shape for "owner-scoped end users + a custom action endpoint"** — a
customer with their own orders *and* `POST /api/checkout`. It is the single most
common real requirement, and before the `routes` block it was inexpressible: a
`permissions` role could not reach any custom route.

Rules worth knowing before you use it:

- **No `conditions`, no `fields`.** A route segment has no rows and no columns;
  declaring either is a load error. The **data** a handler touches is authorized
  separately — `ctx.Query`/`Insert`/`Update` re-evaluate the role against the real
  resources (Layer 2 below).
- **Boot-validated in YOUR binary.** A grant for a segment none of your
  registered routes serves — or an action no registered method provides —
  **fails the boot**, with the registered segments listed. Dead authorization
  config never becomes a mystery 403.
- **Authoritative for the segments it names**: adding `routes` to a wildcard role
  *narrows* those segments. A segment it doesn't name falls through to the role's
  normal evaluation, so deny-by-default is untouched.
- A key that names a real resource is rejected — use `permissions` for that.
- The **stock `appximo serve`/`up` binary registers no custom routes**, so your
  grants are INERT there — and it **boots anyway, with a warning** naming each
  one (OPS-26). One schema file serves the whole journey: prototype it with
  `appximo up` today, compile it into your binary tomorrow, and the same grants
  activate — no second schema to maintain.

Full rationale, alternatives considered, and the security tests:
[ADR-021](adr/ADR-021-custom-route-authorization.md).

#### Layer 2 — data RBAC inside the handler

`ctx.Query`/`Insert`/`Update` re-evaluate the caller's role against the **real**
resource they touch, applying its row condition and field allowlist. A route grant
opens the **endpoint**, never the data: a customer calling
`ctx.Query("orders", …)` inside `/api/checkout` still gets only their own rows,
`ctx.Insert` still forces the condition column to their own id (no
mass-assignment), and `ctx.Update` still refuses to reassign it (a row is never
given to another user through a handler either). Two independent layers, and
you want both.

#### `Public: true` — no token REQUIRED, identity still welcome

For pre-auth endpoints (registration, webhooks, a storefront). Path RBAC skips
the route entirely, and authentication is **optional, not ignored** — three
branches, exactly:

| The request carries… | The handler sees |
|---|---|
| no `Authorization` header | `Claims()` zero — anonymous; the RBAC-aware helpers fail closed |
| a **valid** Bearer | `Claims()` **populated** — identity as *input* (personalize, link the write to the user); data RBAC (`ctx.Query`/`Insert`/`Update`) applies the role normally |
| a present but invalid / expired / wrong-tenant Bearer | nothing — the request is **401**ed before the handler (sent credentials never silently degrade to anonymous) |

So ONE public checkout serves both guests and logged-in customers: branch on
`ctx.Claims().UserID == ""`. Anonymous work goes through `CreateUser` (which
carries its own rules) or a deliberate, greppable `UnsafeTx`. Treat every input
as hostile — §3.4 rule 3.

> **Debugging a 403 on a custom route:** it is almost always the grant. Check, in
> order: does the role declare the segment under `routes`? Is the action right for
> the method? Is the role wildcard (then it should already work)? Is the route
> `Public` (then RBAC isn't involved at all)?

### 3.6 Worked examples (all compile — see examples/backend-guide/)

(a) an admin-scoped route · (b) a public registration in one transaction ·
(c) bounded parallel fan-out · **(d) a payment webhook** · (e) fire-and-forget.

**(a) An authenticated, admin-scoped route** — cross-resource read; `ctx.Query`
still enforces RBAC on the real resources.

```go
app.Register(appximo.Route{
	Method: "GET", Path: "/api/ops/overview", RequireRole: "admin",
	Handler: func(ctx appximo.Ctx) error {
		students, err := ctx.Query("students", appximo.QueryOpts{Limit: 1000})
		if err != nil {
			return ctx.Error(500, "students lookup failed", err)
		}
		enrollments, err := ctx.Query("enrollments", appximo.QueryOpts{Limit: 1000})
		if err != nil {
			return ctx.Error(500, "enrollments lookup failed", err)
		}
		return ctx.JSON(200, map[string]any{
			"tenant": ctx.Tenant(), "students": len(students), "enrollments": len(enrollments),
		})
	},
})
```

**(b) A public registration endpoint (the Hotmart pattern)** — validate an
external license, check business data, create the user + related records + a
follow-up job, **all in one transaction**. Any failure rolls back everything —
the user included. This is the in-process differential: no network hop, one tx,
the engine's validation + RBAC still in force.

```go
app.Register(appximo.Route{
	Method: "POST", Path: "/api/register", Public: true, Timeout: 15 * time.Second,
	Handler: func(ctx appximo.Ctx) error {
		var body struct {
			Email, Password, FullName, License, CourseID string
			Amount                                       float64
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		if body.Email == "" || body.FullName == "" || body.CourseID == "" {
			return ctx.Error(422, "email, full_name and course_id are required", nil)
		}

		// 1. Business gate: verify the purchase against the external platform.
		//    Plain Go http — no sandbox. The bounded context aborts a hung provider.
		ok, err := verifyLicense(ctx.Context(), body.License)
		if err != nil {
			return ctx.Error(502, "license service unavailable", err)
		}
		if !ok {
			return ctx.Error(403, "license not valid", nil)
		}

		// 2. Business data check — no identity yet, so a deliberate UnsafeTx.
		var published bool
		err = ctx.UnsafeTx().QueryRow(ctx.Context(),
			`SELECT published FROM courses WHERE id = $1`, body.CourseID).Scan(&published)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ctx.Error(404, "course not found", nil)
		case err != nil:
			return ctx.Error(500, "course lookup failed", err)
		case !published:
			return ctx.Error(409, "course is not open for enrollment", nil)
		}

		// 3. Create the identity — role from THIS code, never the request.
		user, err := ctx.CreateUser(body.Email, body.Password, "student")
		switch {
		case err == nil:
		case errors.Is(err, appximo.ErrEmailTaken):
			return ctx.Error(409, "email already registered", err)
		case errors.Is(err, appximo.ErrInvalidEmail), errors.Is(err, appximo.ErrWeakPassword):
			return ctx.Error(422, err.Error(), err)
		default:
			return ctx.Error(500, "registration failed", err)
		}

		// 4. Profile + enrollment in the SAME transaction.
		if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO students (user_id, full_name) VALUES ($1, $2)`, user.ID, body.FullName); err != nil {
			return ctx.Error(500, "profile creation failed", err)
		}
		if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO enrollments (user_id, course_id, amount, external_ref) VALUES ($1, $2, $3, $4)`,
			user.ID, body.CourseID, body.Amount, "lic-"+body.License); err != nil {
			return ctx.Error(500, "enrollment failed", err)
		}

		// 5. Follow-up work, atomic with the user (durable, async, non-blocking).
		if _, err := ctx.Enqueue("email.send", map[string]any{
			"template": "welcome", "to": user.Email, "user_id": user.ID,
		}); err != nil {
			return ctx.Error(500, "enqueue failed", err)
		}
		// 6. Auto-login — mint the session for the identity just created (the
		// same token /auth/login would issue). Without this the client has to
		// call /auth/login again with the password it just sent you.
		token, err := ctx.MintToken(user.ID, user.Role)
		if err != nil {
			return ctx.Error(500, "session mint failed", err)
		}
		return ctx.JSON(201, map[string]any{"user_id": user.ID, "email": user.Email, "token": token})
	},
})
```

**(c) Heavy compute — bounded, panic-safe parallel fan-out** with
`appximo.SafeParallel`. The tasks do **external I/O only** (never the tx — a
single connection is not concurrency-safe); `Route.Timeout` bounds the batch.

```go
app.Register(appximo.Route{
	Method: "POST", Path: "/api/reports/ratings", RequireRole: "admin", Timeout: 8 * time.Second,
	Handler: func(ctx appximo.Ctx) error {
		var body struct{ CourseIDs []string `json:"course_ids"` }
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		ratings := make([]float64, len(body.CourseIDs))
		tasks := make([]func(context.Context) error, len(body.CourseIDs))
		for i, id := range body.CourseIDs {
			i, id := i, id
			tasks[i] = func(fctx context.Context) error {
				r, err := fetchRating(fctx, id)
				if err != nil {
					return err
				}
				ratings[i] = r // each task writes its OWN slot — no shared mutation
				return nil
			}
		}
		// At most 8 concurrent (backpressure); a task panic becomes an error, never
		// a crash; ctx cancellation (Route.Timeout) aborts the rest.
		if err := appximo.SafeParallel(ctx.Context(), 8, tasks...); err != nil {
			return ctx.Error(502, "ratings service failed", err)
		}
		out := make(map[string]float64, len(ratings))
		for i, id := range body.CourseIDs {
			out[id] = ratings[i]
		}
		return ctx.JSON(200, map[string]any{"ratings": out})
	},
})
```

**(d) A payment webhook** — the single most security-sensitive handler most
products ever write, and the one shape that is easiest to get wrong. Six rules, in
this order:

1. **Verify the signature over the RAW bytes, BEFORE parsing.** `ctx.RawBody()`
   gives them under the engine's own cap. Parsing first and re-serializing changes
   key order and whitespace — the #1 documented payment-integration bug.
2. **Idempotency is a UNIQUE constraint, not an `if`.** Gateways deliver
   at-least-once and retries arrive *concurrently*; "SELECT then INSERT" races with
   itself. Insert first, let the database reject the duplicate.
3. **The webhook is the source of truth**, not the browser redirect (the customer
   closes the tab; some methods settle minutes later).
4. **Answer 200 only after the state is committed.** A 200 tells the gateway to
   stop retrying, so it must mean "recorded". `ctx.JSON` is buffered until *after*
   the commit — a commit failure becomes a 500, never a false 200. That is this
   rule, enforced by the engine.
5. **Handle out-of-order events**: compare the gateway's own timestamp, not arrival
   order, before overwriting newer state.
6. **Never retry business logic on a terminal decline.** Release what you held and
   stop; the customer starts a new payment.

```go
app.Register(appximo.Route{
	Method: "POST", Path: "/api/webhooks/payments", Public: true, Timeout: 15 * time.Second,
	Handler: func(ctx appximo.Ctx) error {
		// Rule 1 — raw bytes first, cap enforced by the engine.
		raw, err := ctx.RawBody()
		if err != nil {
			if errors.Is(err, appximo.ErrBodyTooLarge) {
				return ctx.Error(413, "payload too large", err)
			}
			return ctx.Error(400, "unreadable body", err)
		}
		if !validSignature(raw, ctx.Request().Header.Get("X-Signature")) {
			return ctx.Error(401, "invalid signature", nil)
		}

		// Only NOW is it safe to interpret the bytes.
		var event struct {
			ID     string `json:"id"`
			Ref    string `json:"external_ref"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return ctx.Error(400, "invalid event", err)
		}
		if event.ID == "" || event.Ref == "" {
			return ctx.Error(422, "id and external_ref are required", nil)
		}

		// Rule 2 — the DATABASE decides whether this is a duplicate.
		// webhook_events.event_id is `"unique": true` in the schema.
		tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO webhook_events (event_id, provider, payload)
			 VALUES ($1, 'payments', $2) ON CONFLICT (event_id) DO NOTHING`,
			event.ID, string(raw))
		if err != nil {
			return ctx.Error(500, "could not record the event", err)
		}
		if tag.RowsAffected() == 0 {
			// Already applied — 200 so the gateway stops retrying. A success, not an error.
			return ctx.JSON(200, map[string]any{"status": "duplicate", "event_id": event.ID})
		}

		// Rules 3 + 6 — apply the change in THIS transaction. The guard in the
		// WHERE makes the transition race-safe and a terminal state unrevivable.
		if event.Status == "refunded" {
			if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`UPDATE enrollments SET status = 'refunded'
				  WHERE external_ref = $1 AND status = 'active'`, event.Ref); err != nil {
				return ctx.Error(500, "could not apply the refund", err)
			}
		}

		// Side effects go to the OUTBOX, never an inline call: enqueued in the same
		// transaction (it exists iff the event was recorded), delivered by the
		// worker — so a slow provider can't hold this transaction open.
		if _, err := ctx.Enqueue("email.send", map[string]any{
			"template": "payment_" + event.Status, "external_ref": event.Ref,
		}); err != nil {
			return ctx.Error(500, "enqueue failed", err)
		}
		// Rule 4 — flushed after the commit, by the engine.
		return ctx.JSON(200, map[string]any{"status": "processed", "event_id": event.ID})
	},
})
```

The signature check itself must be **constant-time** — a byte-by-byte compare
leaks the signature through timing:

```go
func validSignature(raw []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(os.Getenv("WEBHOOK_SECRET")))
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimPrefix(header, "sha256=")), []byte(want))
}
```

Two more things a real integration needs: **compare the amount** the event reports
against what you charged (a correctly-signed event for the wrong amount is a
misroute or tampering — refuse it), and **record rejected events** for audit.

**(e) Fire-and-forget with `SafeGo`** — a best-effort external ping off the
response path. The goroutine gets a fresh context (no request values) with its
own deadline, which `fn` must honor; a panic is recovered, never a crash. This is
at-most-once — for durable work, `Enqueue` instead.

```go
app.Register(appximo.Route{
	Method: "POST", Path: "/api/track", Public: true,
	Handler: func(ctx appximo.Ctx) error {
		var body struct{ Event string `json:"event"` }
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		event := body.Event
		ctx.SafeGo(func(bg context.Context) { _ = pingAnalytics(bg, event) })
		return ctx.JSON(202, map[string]any{"accepted": true})
	},
})
```

**(f) The guest-order lookup (the storefront confirmation page)** — every
storefront needs it, and the generated reads can never serve it: a guest's
order has no `user_id`, so the owner-scoped `GET /api/orders` is structurally
blind to its own buyer. Yet the confirmation page must answer "did my payment
land?" — and it POLLS, because the gateway confirms asynchronously by webhook.
The pattern (proven in the commerce reference app):

1. **Composite lookup key, both parts required**: the human-visible order
   number (printed on screens, shared over WhatsApp — NOT a bearer secret) plus
   the email typed at checkout. Either one alone is unusable.
2. **Uniform 404** for every miss — wrong number, right number + wrong email,
   no such order. No oracle for "that order exists but the email is wrong".
3. **`Public: true` + a polling-sized `RateLimit`** (a confirmation page polls
   every ~2 s for a couple of minutes; ~30 rps per tenant+IP covers a family on
   one Wi-Fi without opening an enumeration lane).
4. **Return a lean projection**, not the row: status, totals, line summaries,
   the latest payment attempt's state. The page narrates pending → paid →
   (or declined) from exactly this.

```go
app.Register(appximo.Route{
	Method: "GET", Path: "/api/order-status",
	Public:    true,
	RateLimit: &appximo.RateLimit{RPS: 30, Burst: 60},
	Handler: func(ctx appximo.Ctx) error {
		q := ctx.Request().URL.Query()
		number, email := q.Get("number"), q.Get("email")
		if number == "" || email == "" {
			return ctx.Error(422, "number and email are required", nil)
		}
		var status string
		var total int64
		err := ctx.UnsafeTx().QueryRow(ctx.Context(), `
			SELECT o.status, o.total_cents
			  FROM orders o JOIN customers c ON c.id = o.customer_id
			 WHERE o.number = $1 AND lower(c.email) = lower($2)`, number, email).
			Scan(&status, &total)
		if err != nil { // pgx.ErrNoRows and everything else: ONE uniform miss
			return ctx.Error(404, "order not found", nil)
		}
		return ctx.JSON(200, map[string]any{"number": number, "status": status, "total_cents": total})
	},
})
```

### 3.6b Write the contract sheet — the OpenAPI names your routes, the sheet gives them shapes

The served `/openapi.json` lists every route the app serves — generated AND
custom (ENG-33): a frontend agent can enumerate your endpoints, see which are
`Public`, which role/action each demands, and read your `Route.Description`.
What it can NOT see is **shapes**: a Go handler declares no request/response
schema, and the engine refuses to publish a guess. So every custom route still
goes into a short **contract sheet** the frontend consumes — per route, the
params, body and response shapes, and its rate budget; plus the role matrix,
any state machines, and the upload limits (none of which fit in an OpenAPI the
engine can derive). The reference storefront keeps this as `STOREFRONT_API.md`
next to the code, updated in the same commit as the route it describes. The
division of labor: **/openapi.json is the authority for EXISTENCE, the sheet is
the authority for SHAPES.** Ten minutes of writing here saves the frontend a
day of reverse-engineering 422s. (Probing is still not discovery: an unknown
`/api/...` answers `401` — auth runs before routing, deliberately.)

### 3.7 Serving your frontend from the same binary

`Config.Static` mounts a file tree — one binary that is backend **and** frontend
**and** admin **and** docs. Your custom binary carries the stock surfaces too:
the admin panel (`/admin`) and Studio (`/editor`) ship as prebuilt assets inside
the module (ADR-025), so a plain `go build` of your backend serves them — no
engine repo, no npm. `Config.Static` is NOT a `Route`: a Route must live under
`/api/` and runs inside a per-request tenant transaction, which is exactly wrong
for a `.js` file.

```go
//go:embed all:web/dist
var frontend embed.FS

dist, _ := fs.Sub(frontend, "web/dist")
app, err := appximo.New(appximo.Config{
	SchemaPath: "schema.json",
	Static: []appximo.StaticMount{{
		Path: "/",   // or "/app"
		FS:   dist,  // or os.DirFS("/var/www/app")
		SPA:  true,  // client-side routing: /orders/42 → index.html
	}},
})
```

| Behaviour | Rule |
|---|---|
| the index | always `no-cache` — it names the current hashed bundles |
| `assets/`, `_app/`, `static/` | `immutable, max-age=31536000` (override with `ImmutablePrefixes`) |
| a missing file **with** an extension | `404` — never the shell, so a deleted bundle can't return HTML |
| an unknown client route | the shell, but ONLY with `SPA: true` (opt-in: a static site should 404) |
| `/api/…`, `/admin`, `/editor`, `/docs`, … | always the engine's — mounting on one is a **boot error** |
| a tenant transaction / RBAC / response cache | none of them run for an asset |
| path traversal | impossible: the tree is an `fs.FS`, which cannot open outside its root |
| Content-Security-Policy | the mount OWNS it, both forms: `appximo.DefaultStaticCSP` (same-origin SPA policy) unless overridden per mount with `CSP:` (verbatim) or disabled with `CSP: appximo.CSPOff` — the API keeps its own strict policy |
| `index.html` | required only with `SPA: true` (it IS the fallback); an assets-only mount needs none (its root 404s) |

⚠ **PCI (SAQ A):** if the app takes card payments through a hosted widget or
iframe, keep the **checkout** page free of third-party scripts (analytics, chat,
tag managers). One extra script there moves the merchant from SAQ A to SAQ A-EP.

**Your binary also observes itself.** The stock surfaces include the engine's
own resource collector (ADR-030): `/admin` → Resources shows RAM / CPU / GC /
goroutines / pool / PSI from ONE out-of-band goroutine and a deterministic
**bottleneck verdict** under load — `db_bound` ("the database, not Appximo"),
`pool_exhausted`, `cpu_throttled` (the plan's quota), `cpu_saturated`,
`gc_pressure`, `memory_pressure`, `lock_contention`, `healthy` — with the
evidence behind it, and `/admin/resources/snapshot` exports a run as JSON.
Your custom routes are counted like generated ones (the request tap runs for
every non-infra request); the `query` stage your handler's `ctx.Query`/`Get`
mark is what the `db_bound` rule reads, so a handler that spends its time in
an external HTTP call reads as "the handler itself" — which is the truth.
A mutex your handlers contend on shows up as `lock_contention` from
`/sync/mutex/wait/total`; nothing to instrument. Knobs: `APPXIMO_SELFMON=off`,
`APPXIMO_SELFMON_INTERVAL`, `APPXIMO_SELFMON_P99_MS` (docs/PRODUCTION.md).

In the in-process fleet a mount belongs to the app that declares it; the
manifest-driven fleet declares none. Runnable example:
[examples/fullstack/](../examples/fullstack/).

---

## 4. Hooks — declarative per-record validation

A hook runs on the CRUD path of a resource, declared in the **schema** (not code
you register). Use a hook when the logic is **pure validation/transformation of
one record** with **no I/O**; use a **handler** when it needs a transaction,
external calls, or cross-resource work.

- Events: `before_create`, `before_update` (may modify the record or abort the
  write), `after_create`, `after_update`. There are **no delete hooks**.
- **Before-hooks** are `js` (Goja sandbox, watchdog-timed) or `wasm` (Wazero).
  In a `js` hook: `data` is the record, `user` is the actor
  (`user.user_id`/`role`/`tenant_id`); set `result.proceed = false` +
  `result.error` to reject with 422. Built-ins: `validateNIT`, `calculateCUFE`,
  `isValidEmail`, `formatMoney`. **No `fetch`, no DB, no network** — a hook that
  needs those is a `before_*` handler's job, or move the logic to a custom route.
- **After-hooks must be `webhook`** (a signed async POST, HTTPS-only,
  SSRF-guarded). A `js`/`wasm` after-hook is rejected at load.

```json
"hooks": {
  "before_create": {
    "type": "js",
    "script": "if (!data.title) { result.proceed = false; result.error = 'title required'; }"
  },
  "after_create": {
    "type": "webhook",
    "url": "https://erp.example.com/webhooks/created",
    "hmac_secret_env": "WEBHOOK_SECRET"
  }
}
```

Hooks are **compiled at boot** from the schema — changing them needs a restart,
not just a schema deploy. When the choice is close ("validate a NIT" → hook;
"validate a NIT *and* call the tax authority" → custom handler), the presence of
I/O decides.

---

## 5. Auth — the identity model

Appximo ships auth-as-product: signup / login / refresh, password reset +
email verification, OAuth social login, and TOTP MFA — all multi-tenant-aware,
issuing the **same JWT** the engine validates. Full operator detail is in the
[README auth sections](../README.md#auth-cycle-for-an-api-consumer-api-productiva-v1);
for building a backend, know this:

- **Users live in `tenant_<id>.auth_users`** with a fixed shape (id, email,
  password hash, role, verified flag, timestamps). Email is unique **per tenant**
  (the same email is a distinct account in two tenants — the structural advantage
  over globally-unique-email systems). You do **not** add columns to `auth_users`.
- **Custom profile fields go in a normal resource** linked by `user_id` — e.g. a
  `students` resource with `user_id` (`uuid`, `unique`) plus `full_name`,
  `country`, etc. The registration handler in §3.6(b) creates both the identity
  (`CreateUser`) and the profile row, atomically.
- **Three ways to create a user:**
  1. **Public signup** — `POST /auth/signup`, enabled only when
     `APPXIMO_AUTH_SIGNUP_ROLE` is set (the role every signup gets). Off by
     default. Login is throttled per (tenant, email) at **5/min** — the
     brute-force guard; `APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE` /
     `_BURST` raise it ONLY for a deliberately shared read-only demo
     identity (raising it weakens every account's guard by the same factor;
     the engine warns at boot).
  2. **Admin API** — `POST /admin/tenants/{id}/users` (platform super-admin or
     admin key), with an admin-chosen role.
  3. **`Ctx.CreateUser` in a custom handler** — the flexible path: run your own
     business logic (validate a purchase, check an allowlist), then create the
     user in the same transaction as the related records. This is how a custom
     registration flow (§3.6b) works, and it can run on a `Public` route because
     creating the user *is* the point.
  (OAuth also auto-provisions a user on first social login when a default role is
  configured.)
- **The `role` claim in the JWT selects the RBAC policy.** A user's role must be
  one the schema declares. Your handlers read it via `ctx.Role()` / `ctx.Claims()`
  and never need to re-verify the token — the chain already did.
- **MFA / OAuth / reset / verify** are engine features you enable by config; they
  need no handler code. Reset + verify are delivered through the **outbox + email
  worker** (`APPXIMO_WORKER_MODE=email`) — the same async pattern your handlers
  use for `Enqueue("email.send", …)`.

---

## 6. Jobs and async processing

Two mechanisms, chosen by **durability**:

| Need | Use | Guarantees |
|---|---|---|
| best-effort, post-response side effect (metric, cache warm, notify) — losing it is OK | **`ctx.SafeGo(fn)`** | at-most-once, non-durable, panic-safe, fresh-root context with its own deadline |
| must survive a crash and be retried (payment capture, provisioning, email) | **`ctx.Enqueue(topic, payload)`** + the worker | at-least-once, durable, transactional (commits with your write) |
| parallel sub-work the **response needs** (fan-out to N services) | **`appximo.SafeParallel(ctx.Context(), limit, tasks…)`** | bounded concurrency (backpressure), panic-safe, waits for all, returns the first error |

Rules:

- **`SafeGo` is fire-and-forget.** It runs concurrently with the commit, so its
  work must **not depend on the transaction having committed** and must **not
  touch `ctx.Tx()`** (that tx is closing). Its context is a **fresh root** —
  it carries **no request values** (capture what you need by value), with an
  independent deadline (`APPXIMO_SAFEGO_TIMEOUT` / `Config.SafeGoTimeoutSeconds`,
  default 30s). Honor that deadline: **`fn` must return once `ctx` is `Done`** —
  the deadline cancels the context, it can't forcibly stop a goroutine, so a task
  that ignores cancellation leaks.
- **The outbox is the durable path.** `Enqueue` writes a job in *your*
  transaction and fires a notify on commit; the separate `appximo-worker`
  consumes it (`SELECT … FOR UPDATE SKIP LOCKED`, at-least-once — make processors
  idempotent). This is how you do "after commit, reliably": provision an account,
  charge a card, send a receipt. See [ADR-016](adr/ADR-016-extensibility-pattern.md).
- **`SafeParallel` is the in-request fan-out.** Use it when the handler must wait
  for several external calls or CPU tasks. It caps concurrency (never spawn
  unbounded goroutines) and recovers a task panic into an error — a raw
  `errgroup` does **not** recover and would crash the process. Tasks must not
  share the handler's tx (single connection); parallelise external I/O or CPU.
- **Long operations** (a report over minutes): return `202 Accepted` with a job
  id from `Enqueue`, let the worker do the work and write the result back through
  the engine API, and have the client poll a status resource.

### 6.1 The worker, in framework mode

`Enqueue` writes the job; a **separate process** drains it. Separate on purpose: a
slow or crashing consumer must never hold a request open, pin a pool connection, or
take the API down. You do not need the shipped `appximo-worker` binary —
`pkg/worker` is a library and a consumer is ~40 lines
([examples/backend-guide/worker/main.go](../examples/backend-guide/worker/main.go)):

```go
func main() {
	log := zerolog.New(os.Stderr).With().Timestamp().Logger()
	dsn := os.Getenv("DATABASE_URL")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DEDICATED connections, never a pool: a LISTEN connection must be permanent,
	// and a pool would rotate it out from under the listener.
	connect := func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, dsn) }

	// A Router dispatches by topic — ONE worker, N event types.
	router := consumers.NewRouter(log)
	router.Handle("email.send", worker.ProcessorFunc(sendEmail))
	router.Handle("enrollments.created", worker.ProcessorFunc(onEnrollmentCreated))

	if err := worker.New(connect, router, worker.Config{}, log).Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal().Err(err).Msg("worker exited")
	}
}
```

The rules that decide whether a consumer is correct:

- **Compose a `Router`, don't run two single-topic workers.** A worker ACKs topics
  it does not own, so two *different* single-topic workers against one outbox
  silently drop each other's events under `SKIP LOCKED`. Scale by running **N
  identical** copies of the same Router instead — the drain uses
  `SELECT … FOR UPDATE SKIP LOCKED`, so instances never collide on a row.
- **Delivery is at-least-once ⇒ Processors must be idempotent.** Make the side
  effect idempotent at the destination (a provider idempotency key, an upsert on a
  unique column), not with an `if already_done` that races with itself.
- **`return nil` ACKs; `return err` retries with backoff.** Return an error only
  for *transient* failures. A permanent one (a malformed payload, a rejected
  address) should be recorded and acked — otherwise it is retried until it
  exhausts `max_attempts`, burning the queue on a job that can never succeed.
- **Write results back through the ENGINE API** (`worker.NewEngineClient` mints a
  short-lived, scoped service JWT), not straight into the tenant's tables — that
  way the write inherits the engine's validation and RBAC instead of bypassing
  both. Never `admin`; give the worker a minimal role in the schema.
- **Schema events cost no handler code**: `"events": ["create"]` on a resource
  makes the engine write `<resource>.created` to the outbox inside the same
  transaction as the INSERT. The payload is lean (`{id, tenant_id, resource,
  action}`) — a consumer that needs the row reads it back.

---

## 7. Building a backend with an agent — end to end

The full loop, and the Hotmart case as the worked example:

1. **Describe the app.** "A course platform: students (profile linked to the auth
   user), courses, enrollments with an active→refunded lifecycle; public
   registration that verifies a purchase license; admins see an ops overview;
   fire an analytics event on activity."
2. **Generate the schema.** Run `appximo spec`, generate against it, and
   self-correct with `appximo validate --json schema.json` until valid. The
   result is [examples/backend-guide/schema.json](../examples/backend-guide/schema.json):
   `students` / `courses` / `enrollments` (state machine + `create` event), and
   roles `admin` (wildcard) + `student` (per-resource `permissions`,
   owner-scoped by `user_id`).
3. **Write the handlers** (this doc). For the Hotmart flow you need exactly one
   custom route — `POST /api/register` (§3.6b) — because registration must verify
   an external license and create the user + profile + enrollment + welcome email
   atomically, which the schema alone can't express. Add `POST /api/reports/ratings`
   (admin fan-out), `GET /api/ops/overview` (admin), `POST /api/track` (public
   fire-and-forget) as needed. Everything else — students/courses/enrollments
   CRUD — is the **generated** routes; don't write handlers for them.
4. **Compile.** `go build ./...` — a bad route or a wrong Ctx call is a
   compile/boot error, never a runtime surprise. The full program is
   [examples/backend-guide/main.go](../examples/backend-guide/main.go).
5. **Run it.**
   ```bash
   DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… \
     go run ./examples/backend-guide --schema examples/backend-guide/schema.json
   ```
6. **Deploy** — the binary is the deploy unit; register tenants through the
   control plane / admin API and mint tokens as usual (README quick start).

The result: a student registers through your public endpoint (external license
verified, user + profile + enrollment + welcome email created in one
transaction), reads their own enrollments through the generated owner-scoped
routes, admins get a cross-resource overview and a parallel ratings report, and
every background effect is either durable (outbox) or safely fire-and-forget
(`SafeGo`) — with a panic in any of it recovered, never an outage.

---

## 8. References

- **Schema:** `appximo spec`, [SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md),
  [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md).
- **This doc:** `appximo backend-spec` prints it — paste it into your agent.
- **The compiling example:** [examples/backend-guide/](../examples/backend-guide/).
- **The library contract & extensibility model:**
  [ADR-016](adr/ADR-016-extensibility-pattern.md).
- **Auth & the multi-tenant model:** [README](../README.md),
  [docs/MENTAL_MODEL.md](MENTAL_MODEL.md).
- **Working inside this repo:** [AGENTS.md](../AGENTS.md).
