# AGENTS.md — Appitools

Instructions for AI coding agents. Part 1 is for working **on this repo**
(contributing to the engine). The [Integration guide](#integration-guide--helping-a-user-adopt-appitools)
is for the other job: helping a user adopt Appitools in **their** project.
Every syntax claim in this file is audited against the engine source
(`pkg/schema`, `pkg/query`, `pkg/graphql`) — do not invent API surface
beyond what is listed here.

## What this is

Appitools compiles a JSON schema into a multi-tenant REST + GraphQL +
OpenAPI server **at boot** — Go 1.25, no CGO, one static binary,
PostgreSQL with schema-per-tenant isolation. There are no handlers,
models, or migration files to write: routes, SQL, validation, RBAC and
OpenAPI are derived from the schema at startup (`pkg/codegen.BuildRouter`),
and tables are created/extended idempotently when a tenant registers.

**If a task looks like "add an endpoint / model / migration", it is
almost always "add a resource or field to the schema JSON".** Code
changes are for engine behavior, not application surface.

## Commands

```bash
make build            # go build ./...
make test             # unit lane: -race -short, no Docker needed, ~7 s warm
make test-all         # + integration + e2e + resilience — needs Docker (testcontainers)
make test-integration # DB-backed suite alone (Docker)
make test-e2e         # full client scenarios (Docker)
make lint             # golangci-lint run ./...
go test -run 'TestBuildQuery' -race ./pkg/query/   # one test
```

Boot the engine locally (needs a reachable Postgres):

```bash
go build -o appitools ./cmd/appitools
DATABASE_URL='postgres://user:pass@localhost:5432/db' \
JWT_SECRET='a-secret-of-at-least-32-characters' ADMIN_KEY='dev-admin' \
  ./appitools serve --schema examples/quickstart/schema.json --port 8080
```

- All three env vars are hard-required — `serve` exits without them.
- Do NOT use `make run` / `go run ./cmd/appitools/main.go`: passing the
  file compiles *only* `main.go`, producing a binary with **zero
  subcommands** (no `serve`). Use the package path:
  `go run ./cmd/appitools serve …`.
- Other subcommands: `validate <schema>`, `token` (mint a dev JWT),
  `openapi`, `graphql` (SDL), `generate`, `migrate`, `backup`, `init`,
  `blueprints list` (lists schema files in a local `blueprints/` dir),
  `version` (prints the ldflags-injected build version; "dev" on a plain
  local build — releases and published images carry their tag).
- `tools/devhub/` is a local dev dashboard (systemd service on :3099).
  It is **not part of the engine** — never ship engine features there.
  `make devhub-run` is only for developing the devhub itself (stop the
  service first or :3099 is taken and the stale binary keeps serving).

## Conventions (this repo, non-obvious)

- SQLite is always `modernc.org/sqlite`, never `mattn/go-sqlite3` — the
  project is CGO-free end to end (same reason it uses Goja and Wazero,
  not v8 or native plugins).
- Compile work at schema load, never per request: regexes (RE2),
  validation rules (`pkg/schema/rules.go`), enum sets. The request path
  only executes precompiled closures.
- SQL identifiers go through `pgx.Identifier.Sanitize()`; values are
  always bound parameters; `search_path` is set only as `SET LOCAL`
  inside a transaction.
- The tenant comes from the Host header subdomain (`acme.example.com` →
  Postgres schema `tenant_acme`). It is a naming convention — the
  middleware does no tenant-table lookup.
- Validation is declarative in the schema (S44), not code in handlers.
  New validation capabilities go into `pkg/schema/rules.go` +
  `validator.go` so REST and GraphQL share them.
- Postgres errors are masked before reaching client response bodies.
- Linear git history on `main` — rebase, no merge commits.
- Secrets never enter git: `.env.example` is the template, real values
  live in `.env` (gitignored).
- Performance-sensitive engine changes are measured, not eyeballed:
  `make bench-protocol RUNS=10 LABEL=my-change` (warmup + N runs +
  statistical verdict; see `scripts/bench-protocol.sh`).

## Boundaries — do not

- **Never pin a security-relevant dependency downward.** CI runs
  govulncheck and blocks the build. Real incident: x/crypto pinned to
  v0.48 reintroduced GO-2026-5013 and broke the release.
- **Never `pkill -f` / `pgrep -f`** — `-f` matches the invoking shell's
  own command line, so it self-matches and can kill your own session.
  Use `-x <exact-binary-name>` or an explicit PID.
- Never expose the control plane (`:9090`) to the internet. It is the
  tenant-registration API, designed to stay on localhost / the internal
  network, gated only by `X-Admin-Key`.
- Never change a README feature or performance claim without
  re-verifying it against the running engine — every claim there was
  audited line by line before launch; keep that property.
- `testdata/` and `examples/logistics-api/` are test fixtures. The
  canonical public example is `examples/quickstart/schema.json`
  (`todo-api`, one `tasks` resource) — keep docs consistent with it and
  do not surface guides/logistics in user-facing material.

## Architecture orientation

One line per layer — navigate the code for the rest:

- `pkg/schema` — schema parsing, load-time validation, rule compilation.
- `pkg/codegen` — `BuildRouter`: builds the live chi router from the
  schema at boot. (`internal/handlers/` + the `generate` subcommand are
  the older write-files generator, kept for template tests.)
- `pkg/query` — URL params → validated SQL: filters, sort, keyset pagination.
- `pkg/graphql` — schema → GraphQL types and resolvers (queries + create/update/delete;
  update + relation embeds reuse the shared codegen update/include cores).
- `pkg/rbac` — JSON policies; row conditions appended via `query.AppendRowCondition`
  (shared by REST and GraphQL — fix authorization bugs there, once).
- `pkg/tenant` — Host-subdomain resolution + per-tenant schema cache.
- `pkg/extensions` — hooks: Goja JS sandbox, Wazero WASM, webhook dispatcher.
- `pkg/events` — SSE hub, post-commit fan-out.
- `pkg/migration` — idempotent DDL (`CREATE TABLE / ADD COLUMN IF NOT EXISTS`).
- `pkg/outbox` — transactional outbox (ADR-016 §Class 2): `Enqueue` writes a job
  in the caller's tx and emits `pg_notify(outbox_notify, <id>)` on commit.
- `pkg/worker` — the outbox consumer behind the SEPARATE `cmd/appitools-worker`
  binary (not a goroutine in the engine): LISTEN/NOTIFY wake-up + poll fallback,
  `SELECT … FOR UPDATE SKIP LOCKED`, at-least-once (Processors must be idempotent).
  Run it with `DATABASE_URL=… go run ./cmd/appitools-worker`; the end-to-end proof
  is `scripts/worker-e2e.sh`. A consumer that writes results BACK does so through
  the engine HTTP API (`worker.EngineClient`), never the tenant DB directly, so the
  write inherits the engine's validation + RBAC. It mints a fresh, SHORT-LIVED
  (60s), SCOPED service JWT per operation (`auth.GenerateTokenWithTTL`, shared
  `JWT_SECRET`) carrying the event's `tenant_id` and a **scoped service role** —
  never admin. `APPITOOLS_WORKER_WRITEBACK=on` enables the demo consumer (PATCHes
  the created row's status); off by default (echo).
- `pkg/consumers` — real business-logic Processors (kept OUT of `pkg/worker` so the
  core loop stays dependency-light; e.g. excelize lives only here). The first is
  the XLSX consumer (`XLSXProcessor`, FileJob pattern): on `{resource}.created` it
  fetches the job, STREAMS the referenced XLSX (excelize `f.Rows` iterator — never
  `GetRows`, never the whole file in RAM), computes an aggregate, and writes
  `{status, result}` back via the engine API. A corrupt/invalid file is a PERMANENT
  failure (job → `failed`, event acked — worker never crashes); a transient engine
  error keeps the row pending for retry. Idempotent: a job already terminal is
  skipped. The `file_ref` is resolved through `pkg/files`: set `APPITOOLS_FILES_DIR`
  and the consumer treats `file_ref` as a VFS `file_id`, streaming the
  content-addressed blob via `VFS.Get`; unset, it reads `file_ref` as a local path
  (back-compat). The second consumer is the EMAIL consumer (`EmailProcessor`): on an
  `email.send` event it renders an `html/template` (stdlib, auto-escaped; built-ins
  `verification`/`welcome`) and sends via an EXTERNAL SMTP provider (`SMTPSender`,
  net/smtp + STARTTLS, env-configured — Brevo/Resend/Mailgun/SES) with no engine
  write-back. At-least-once ⇒ a rare double-send is accepted for transactional mail
  (documented), mitigated by a deterministic Message-ID per outbox row. Select
  consumers via `APPITOOLS_WORKER_MODE=echo|writeback|xlsx|email` (default echo). A
  single-mode worker ACKS topics it doesn't own, so DON'T run two different modes
  against one outbox (silent event loss under SKIP LOCKED) — for multiple event
  types compose a `consumers.Router` (topic → Processor) in one dispatching worker
  and scale that (ADR-016 library model).
- `pkg/files` — content-addressable file store (FILES-V1), INSIDE the binary (no
  MinIO/sidecar; ~0 RAM at rest — it is streamed disk I/O). Blobs are keyed by
  SHA-256 at `<root>/<tenant>/<aa>/<bb>/<sha>` (dedup free within a tenant; the
  tenant prefix gives physical isolation); metadata lives in a per-tenant
  `tenant_<id>.files` table (idempotent `EnsureTable`). The `VFS` interface has a
  `Local` backend (this is it) and a documented `S3` contract for next session
  (presigned URL + 302 — the engine authorizes but never proxies the bytes).
  `Put` streams with `io.CopyBuffer` (64 KiB, NEVER `io.ReadAll`), hashing as it
  goes, atomic-renames into the CAS, and cleans the temp on an interrupted upload;
  `original_name` is metadata ONLY and never builds a path (path-traversal inert).
  Engine routes `POST /api/files` + `GET /api/files/{id}` flow through the shared
  chain; the download bypasses the response cache (a blob is never buffered/cached
  in RAM — same bypass as SSE).

Request flow: tenant (Host) → rate limit → response cache → JWT → RBAC →
handler (`pkg/codegen`) → query build / validation → hooks → pgx →
serialize. The SSE endpoint bypasses the response cache; other GETs flow
through it (so a "stale read" bug report usually means cache invalidation,
not the query).

## Commits & PRs

Conventional Commits, one logical change per commit: `feat:` `fix:`
`docs:` `test:` `chore:` `refactor:` (details in
[CONTRIBUTING.md](CONTRIBUTING.md)). License is Apache 2.0; contributions
are licensed under it — no separate CLA/DCO. PR gate: `go build ./...`,
`make test`, golangci-lint clean, and schema changes validated with
`appitools validate`.

---

# Integration guide — helping a user adopt Appitools

This half is for when a user wants Appitools to serve **their** API and
you are writing their schema and calling their endpoints. The surface
below is complete and verified — if something is not listed, the engine
does not support it (see [Does not exist](#does-not-exist--do-not-invent)).

## Writing a schema

One JSON file. `$schema` and `version` are **required** (load fails
without them). A complete, working example:

```json
{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"] },
        "due":    { "type": "time" }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin":  { "resources": "*", "actions": ["*"] },
      "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id", "title", "status"] }
    }
  }
}
```

Naming rules (enforced at load): resource names `^[a-z][a-z0-9-]*$`,
field names `^[a-z][a-z0-9_]*$`. An `id` UUID primary key is implicit —
don't declare it. The validator is **strict about keys**: any key
outside the documented surface — at any level — rejects the schema with
an error listing the valid keys for that level, so typos never become
silently dead config.

### Field types — the complete set

| `type` | Postgres column | Filter ops available |
|---|---|---|
| `string`, `text` | TEXT | `eq`, `partial` (`ILIKE %v%`), `start` (`ILIKE v%`) |
| `int`, `int64`, `float64` | INTEGER / BIGINT / DOUBLE PRECISION | `eq`, `gt`, `gte`, `lt`, `lte` |
| `time` | TIMESTAMPTZ | `eq`, `gt`, `gte`, `lt`, `lte`, `after`, `before` |
| `uuid` | UUID | `eq` |
| `bool` | BOOLEAN | `eq` |
| `json` | TEXT (stored as text) | none — not filterable |

### Field keys

- `required: true` — NOT NULL column; must be present on POST and PUT
  (PATCH validates only the fields sent).
- `unique: true` — UNIQUE constraint.
- `auto: true` — engine-managed `TIMESTAMPTZ DEFAULT now()` (for
  `created_at` / `updated_at`); exempt from the required check.
- `enum: ["a", "b"]` — string values only; writes outside the set → 422.
- `default: <value>` — applied **on create** (POST / GraphQL `create…`) when the
  field is OMITTED (a present key, even explicit `null`, is left as sent — like
  SQL `DEFAULT`). Literal of the field's type (`string`/`int`/`int64`/`float64`/
  `bool`/`uuid`/`json`); on a `time` field the value `"now"` is the one dynamic
  default (resolved to the insert moment), any other string is a literal
  timestamp. Type-checked at load (a default of the wrong type rejects the
  schema; `enum` defaults must be a member; `auto` fields may not declare one).
  Precedence with `required`: a required field **with** a default is satisfied by
  it when omitted; a required field **without** one still returns 422. Defaults
  are create-only — `PUT` (full replace) writes an omitted optional field as NULL.
- `relation: "<resource>"` — see [Relations](#relations).
- Validation rules (all optional, compiled at load; a bad rule rejects
  the schema with a clear error):
  - numeric fields: `min`, `max`
  - string/text fields: `minLength` / `maxLength` (rune count),
    `pattern` (RE2 regex, ≤ 200 chars), `format` — exactly one of
    `email | uuid | url | date`

A write that violates rules returns **422 with every failing field at
once**: `{"error":"validation_failed","fields":[{"field":"title","rule":"required","message":"is required"}]}`.

### Relations

```json
"customer_id": { "type": "uuid", "relation": "customers" }
```

generates one read-only route — `GET /api/orders/{id}/customer` (field
name minus `_id`) — returning the referenced record. That is **all** the
field-level `relation` does: no FK constraint, no joins in list queries, no
nested writes, no cascade.

### Declarative relations + nested embeds (`relations`, ADR-019)

For first-class nested reads, declare a `relations` block per resource
(sibling of `fields`). A relation is served **nested in one round-trip**
(`json_agg` + `LEFT JOIN LATERAL`, built in Postgres, streamed straight to
the client — no N+1) and ONLY when the caller opts in with `?include=`:

```json
"orders": {
  "fields": { "status": { "type": "string" }, "customer_id": { "type": "uuid" } },
  "relations": {
    "lines":    { "type": "has_many",     "target": "lines",    "fk": "order_id" },
    "customer": { "type": "belongs_to",   "target": "customers","fk": "customer_id" }
  }
},
"products": {
  "relations": {
    "orders": { "type": "many_to_many", "target": "orders",
                "through": "order_products", "fk": "product_id", "target_fk": "order_id" }
  }
}
```

- `type` ∈ `has_many | belongs_to | many_to_many`.
  - `has_many` — FK lives on the **target** (child) table (`child.<fk> = parent.id`).
  - `belongs_to` — FK lives on **this** (source) table (`target.id = source.<fk>`).
  - `many_to_many` — `through` (junction table) + `fk` (this side's id in it) +
    `target_fk` (the target's id in it).
- `limit` (optional, default **50**) bounds children per parent (top-N embed,
  a fan-out / DoS guard).
- **Request it** with `?include=lines,customer`; nest with a dot:
  `?include=lines.product` (max depth **2** — deeper → `400`).
- **Opt-in & free when unused:** WITHOUT `?include=` the SQL is byte-identical
  to before — the plain list/get path is unchanged (measured `no_change`).
  Serving a `has_many` embed of ~15 children measured **+0.01 ms** p50.
- **RBAC is compiled into the SQL:** a relation is embedded only if the role may
  `read` the target; the target's field allowlist scopes the embedded object and
  its row-level condition is injected into the embed `WHERE`. Asking to `include`
  a target the role cannot read → `403`. There is no path that returns a child
  the role may not see.
- **Auto FK index:** every declared relation's FK column gets a btree index at
  tenant registration (the embed is an index lookup, never a per-parent seq scan).
- **GraphQL:** the same relations are nested fields on the generated types
  (`{ orders { data { id lines { id qty } customer { name } } } }`), backed by the
  SAME single LATERAL query — no dataloader needed.
- Names are strict-validated at load (unknown `type`/`target`, missing
  `through`/`target_fk` for m2m, etc. all reject the schema).

### RBAC

Actions are exactly `read | create | update | delete | "*"`. **Deny by
default**: a role with no matching policy gets 403; a record excluded by
a row condition reads as 404 (not 403).

```json
"rbac": {
  "roles": {
    "admin":    { "resources": "*", "actions": ["*"] },
    "operator": {
      "resources": ["tasks"],
      "actions": ["read", "update"],
      "fields": ["id", "title", "status"],
      "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
    }
  }
}
```

`fields` is a response allowlist; `conditions` filters rows, with
`$user_id` resolving to the JWT subject. The JWT `role` claim selects
the policy.

### Hooks (lifecycle extensions)

Declared per resource under `hooks`. Events are exactly
`before_create | after_create | before_update | after_update` — there
are **no delete hooks**, and any other event name is rejected at
validation. Three hook types (all fields below verified against
`pkg/schema/types.go`):

```json
"hooks": {
  "before_create": {
    "type": "js",
    "script": "if (!data.title) { result.proceed = false; result.error = 'title required'; }"
  },
  "after_create": {
    "type": "webhook",
    "url": "https://erp.example.com/webhooks/task-created",
    "hmac_secret_env": "WEBHOOK_SECRET_TASKS"
  }
}
```

- `js` — Goja sandbox, watchdog-interrupted (80 ms soft / 500 ms hard).
  `data` is the record; setting `result.proceed = false` +
  `result.error` rejects the write with 422. Built-ins available:
  `validateNIT`, `calculateCUFE`, `isValidEmail`, `formatMoney`.
- `webhook` — async signed POST: headers `X-Appitools-Event` and
  `X-Appitools-Signature: sha256=<hmac>`; 3 retries with backoff.
  **`hmac_secret_env` is the NAME of an env var holding the secret**,
  not the secret itself (a `"secret"` key does not exist). Constraints
  that are easy to trip on: the dispatcher is **HTTPS-only and
  SSRF-guarded in every environment** — `http://` URLs and
  loopback/private/link-local IPs are refused (logged, never delivered),
  so a local/LAN receiver will never get called; test against a public
  HTTPS endpoint.
- `wasm` — `wasm_module` (pre-loaded module name) + `wasm_fn` (default
  `transform`), Wazero, 16 MiB limit.

**Hooks are compiled at boot from the `--schema` file** (same as routes
and GraphQL types). Declaring or changing hooks through the control
plane (`PUT /tenants/{id}/schema` + reload) does NOT wire them — the
reload response says so in a `warnings` field; a process restart is
required.

### Events (opt-in outbox emission)

A resource may opt into emitting a **transactional outbox event** on each
generated CRUD write by declaring an `events` array at the resource level
(sibling of `fields`/`hooks`):

```json
"tasks": {
  "fields": { "title": { "type": "string", "required": true } },
  "events": ["create", "update", "delete"]
}
```

- Values are exactly `create | update | delete` (present tense, the RBAC
  action vocabulary). Any other value rejects the schema at load. A
  resource that omits `events` emits nothing and pays **zero overhead** on
  the write path.
- The event is written to `public.outbox` **in the same transaction** as
  the CRUD write (`pkg/outbox`): if the write rolls back (e.g. a unique
  violation), the event never exists — and vice-versa. The engine fires
  `pg_notify(outbox_notify, <id>)` on commit; the separate
  `cmd/appitools-worker` consumes it (`SELECT … FOR UPDATE SKIP LOCKED`,
  at-least-once, idempotent).
- **Topic** is `{resource}.{created|updated|deleted}` — e.g. a POST to
  `tasks` emits `tasks.created`, PUT/PATCH `tasks.updated`, DELETE
  `tasks.deleted`. (PUT and PATCH both map to `updated`.)
- **Payload** is lean — `{"id", "tenant_id", "resource", "action"}` (the
  affected row's id + identity, never the full row). A consumer that needs
  more does its own `SELECT`; for a delete the row is already gone, so the
  id is all the event carries.
- A delete that matches no row (404) emits nothing.

### Indexes

Declare an `indexes` array per resource (sibling of `fields`). Each entry is one
index over one or more columns (composite when more than one), optionally
`unique`:

```json
"tasks": {
  "fields": { "status": { "type": "string" }, "owner_id": { "type": "uuid" } },
  "indexes": [
    { "fields": ["status"] },
    { "fields": ["owner_id", "status"] },
    { "fields": ["status"], "unique": true }
  ]
}
```

- Materialized at tenant registration as `CREATE [UNIQUE] INDEX IF NOT EXISTS`
  over the listed columns (idempotent). The index name is derived from the table
  and columns (`idx_<table>_<cols>` / `uniq_<table>_<cols>`).
- Every referenced column's existence is checked against `information_schema`
  first; an index naming a column that does not exist (yet) is **logged and
  skipped**, never a hard failure (columns can be added to the live table at
  runtime — same contract as relation FK indexes).
- Validated at load: at least one field per index, each a valid field name.
- Relation FK columns are auto-indexed separately (see
  [Declarative relations](#declarative-relations--nested-embeds-relations-adr-019));
  you do not need to declare those by hand.

## Running it and making the first call

Fastest path is the published Docker image — the four copy-paste
commands (register tenant → mint JWT → POST → filtered GET) are in the
[README quick start](README.md#quick-start-30-s-with-the-image-pull);
the production/TLS path is [docs/DEPLOY.md](docs/DEPLOY.md). Don't
re-derive those commands — they are verified verbatim.

Facts agents most often get wrong:

- **Tenant = Host header.** Every data-plane request needs
  `Host: acme.localhost` (or a real subdomain). Host of a different
  tenant → 401 `token tenant mismatch`; Host with no subdomain (bare
  IP/`localhost`) → 500 with empty body.
- **JWT**: HS256 only, `exp` required, `role` claim must match a schema
  role. Mint dev tokens with
  `appitools token --secret "$JWT_SECRET" --tenant acme --role admin`.
- **Two ports**: data plane `:8080`; control plane `:9090` (tenant
  registration via `X-Admin-Key`) — internal only, never proxied.
- **Health probes (all unauthenticated)**: `/healthz` (liveness, never
  touches Postgres), `/readyz` (readiness — flips to **503** while
  draining on SIGTERM), `/health` (returns `{"status":"ok","version":…}`
  with the build version). By contrast `/metrics` and `/debug/*` are
  admin-gated — the deep observability surface is mapped in
  [docs/EXPLORE.md](docs/EXPLORE.md).
- `/metrics` and `/debug/*` require `X-Admin-Key` even though the routes
  exist on the public listener.
- curl needs `-g` for filter brackets: `curl -g '...?filter[status][eq]=open'`.

## Calling the REST API — exact syntax

Per resource `tasks`: `GET|POST /api/tasks`,
`GET|PUT|PATCH|DELETE /api/tasks/{id}`, `GET /api/tasks/events` (SSE,
JWT + RBAC enforced, fields/rows filtered at delivery), plus relation
subroutes.

- **Filters**: `?filter[field]=v` (implies `eq`) or
  `?filter[field][op]=v` with ops from the type table above. Unknown
  field or type-incompatible op → 400.
- **Search**: `?search=term` runs a case-insensitive substring match
  (`ILIKE %term%`, `%`/`_` escaped) across **only** the resource's
  `string` and `text` fields, OR-ed together and AND-ed with any
  filters. It does **not** touch `int`/`int64`/`float64`/`time`/`uuid`/
  `bool`/`json` fields, and it is a no-op (ignored) on a resource with no
  string/text fields. It is a plain `ILIKE`, not a ranked/full-text
  search engine.
- **Sort**: `?sort=field&order=asc|desc` — **one field only**. The
  alternative `?order[field]=desc` also works and wins when both are
  sent. Anything else (`sort=field:desc`, multi-field) is **silently
  ignored** — verify result order, don't trust the param.
- **Pagination**: keyset — `?after=<uuid>` / `?before=<uuid>` with
  `?per_page=` (default 20, max 100). `?page=` exists but is
  OFFSET-based; prefer keyset.
- Responses are `{"data": [...], "meta": {...}}`.

## GraphQL

`POST /graphql`. Queries plus `create<Singular>` / `update<Singular>` /
`delete<Singular>` mutations (e.g. `createTask`, `updateTask`, `deleteTask`).
`update<Singular>(id, input)` is a PARTIAL update (PATCH semantics) sharing the
REST update core — same declarative validation, field-level RBAC allowlist,
row-level condition, and outbox emission (a resource with `events:["update"]`
emits `…​.updated` from the mutation, identically to REST PATCH). Its `input`
type has every non-auto field optional. GraphQL always answers **HTTP 200**:
check the `errors` array in the body, never the status code. Validation
failures arrive as `errors[].extensions.fields` (same rule engine as
REST). Introspection is disabled in production (the `__schema`/`__type`
fields are rejected outside development; `__typename` is allowed);
GraphiQL only runs with `APPITOOLS_ENV=development`. The query analyzer
also bounds document size as an alias-amplification guard: at most **50
root selections** per operation and **2000 total selections** across the
whole document — over either limit the request is rejected (there is no
separate nesting-depth counter).

## File store (FILES-V1)

The engine ships a content-addressable file store on two routes (no schema
declaration needed — they exist whenever no resource is literally named `files`):

- `POST /api/files` — multipart upload (form field `file`). Streamed to disk in
  64 KiB chunks (never buffered whole), de-duplicated by content hash. Returns
  `201 {"file_id","sha256","size"}`. Body capped by `APPITOOLS_FILES_MAX_BYTES`
  (default 256 MiB) → `413` on overflow. RBAC action is `create` on the `files`
  resource.
- `GET /api/files/{id}` — streams the blob back with its `Content-Type` and
  `Content-Disposition`. RBAC action is `read` on `files`. `404` if the id is
  unknown to the tenant (ids are tenant-scoped — no cross-tenant handle).

Both inherit the normal chain (tenant Host → JWT → RBAC), so a role needs the
`files` resource in its policy (`"resources": ["files", …]` or `"*"`). Blobs live
under `APPITOOLS_FILES_DIR` (default `/var/lib/appitools/files`), created lazily
on first upload. Use a `file_id` as a filejob's `file_ref` to feed the async XLSX
consumer (`APPITOOLS_FILES_DIR` must be set on the worker, pointing at the same
root). An S3 backend (presigned URL + 302) is the next increment; today the store
is local-disk only.

## Does not exist — do not invent

- Field type `number` → schema rejected; use `int`, `int64` or
  `float64` (the full type set is the table above — nothing else).
- Unknown schema keys → schema rejected listing the valid keys (e.g.
  `webhooks` instead of `hooks`, `secret` instead of `hmac_secret_env`).
  Nothing is silently ignored anymore.
- Writing a field the resource doesn't have → 422
  `{"error":"validation_failed","fields":[{"field":"…","rule":"unknown_field"}]}`
  (not a 500, not silently dropped).
- Hook events other than the four listed (no `on_create`, no
  `before_delete`/`after_delete`).
- Filter ops `neq`, `in`, `like`, `is_null` → 400.
- Multi-field sort or `sort=field:desc` → silently ignored.
- Total-count in list responses (`count=true` is not a thing).
- FK **constraints** / cascades. (Declarative relations DO exist — nested
  `?include=` embeds via `json_agg`+LATERAL, see
  [Declarative relations](#declarative-relations--nested-embeds-relations-adr-019)
  — but they create no FK constraint and no `ON DELETE` cascade; the field-level
  `relation` still only adds the read-only subresource route.)
- CORS headers — browser SPAs must be served same-origin
  ([workaround](docs/DEPLOY.md#cors--current-status-important-for-spas)).
- `workflows` schema block — parsed for forward compatibility, no executor.
- OTLP/OpenTelemetry export (observability is Prometheus `/metrics` + an
  internal trace ring).
- A hosted/SaaS version — self-hosted only.
