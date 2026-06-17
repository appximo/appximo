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
- `pkg/migration` — tenant table provisioning/evolution. `ApplyTenantMigration`
  now drives the real migration engine (`pkg/schemadiff`): introspect the live
  schema → build the desired schema from the tenant JSON (`buildDesiredSchema`,
  the canonical bridge) → diff → apply through the production-safe executor
  (lock_timeout+retry, NOT VALID/VALIDATE, CONCURRENTLY indexes, data-preserving
  renames). v1 policy is **additive**: it creates/adds/alters/renames but NEVER
  drops (a removed field's column stays as drift, logged) — re-applying an
  unchanged schema is a true no-op, and a new tenant provisions identically to the
  old converger. A real NOT NULL is now enforced faithfully (fails loud + rolls
  back over populated data instead of the old silent NULL-accepting divergence).
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

Naming rules (enforced at load): resource **and** field names both match
`^[a-z][a-z0-9_]*$` — lowercase, start with a letter, `_` for multi-word names
(`order_items`). **`-` is NOT allowed in a resource name** (it is not a valid
GraphQL identifier; a hyphenated name used to pass `validate` then crash the
engine at boot). The `auth_` prefix is **reserved** (the per-tenant
authentication tables), so a resource cannot be named `auth_users`. An `id` UUID
primary key is implicit — don't declare it. The validator is **strict about keys**: any key
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
- `unique: true` — UNIQUE constraint. A write that collides (this field **or** a
  composite `unique` index) returns **`409 Conflict`** —
  `{"error":"field \"<field>\": value already exists"}` — on both REST (create &
  update) and GraphQL (in `errors[]`); the raw Postgres error is never exposed.
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

#### State machines (`state_machine`, G5)

A string status field can declare a **lifecycle**: which states a row may be created
in, and which moves are allowed between states. The engine forces it — a status is
no longer a free label the client advances arbitrarily.

```json
"status": {
  "type": "string",
  "enum": ["pending", "paid", "shipped", "delivered", "cancelled"],
  "default": "pending",
  "state_machine": {
    "initial": "pending",
    "transitions": {
      "pending":   ["paid", "cancelled"],
      "paid":      ["shipped"],
      "shipped":   ["delivered"],
      "delivered": [],
      "cancelled": []
    }
  }
}
```

- **`initial`** — the state(s) a row may be **created** in (a string or an array). A
  create whose status is not initial → `422`; you can't create a row already
  advanced. The field's `default` (if any) must be an initial state.
- **`transitions`** — per state, the states it may move to. On **update**, a status
  may only move along a declared transition; an undeclared move → `422` with a clear
  `invalid transition from "X" to "Y"`. A state with **no outgoing transitions
  (`[]`) is terminal — immutable** (it can never change to another state; this is
  how a fintech "posted" entry stays append-only). Re-sending the current value is a
  no-op (so a full-object PUT/PATCH that includes the unchanged status still works).
- **Race-safe.** The transition is enforced **inside the UPDATE's `WHERE`** (the move
  is allowed only if the row's CURRENT state permits it), so two concurrent updates
  can't both advance the same row — one wins, the other matches no row and fails. No
  read-modify-write window.
- **REST, GraphQL, and inside a `POST /api/transaction`** all enforce it (a batch op
  that violates a transition fails the WHOLE transaction). A field **without**
  `state_machine` is a free string, unchanged.
- **Validated at load:** `state_machine` only on a string/text field; at least one
  `initial`; every state coherent with `enum` when declared; a string `default` must
  be an initial state. Strict-key (`initial`/`transitions`).
- **Out of scope (documented):** per-transition RBAC ("only role X may move to
  shipped") — today the transition is validated structurally and the normal `update`
  RBAC governs WHO may update; in-place value rewriting is a transition, not arbitrary
  math. The single-op update path without a state machine is unchanged (measured
  `no_change`). Example: [examples/model-lab/state-machine.json](examples/model-lab/state-machine.json).

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

**`conditions` and `fields` are enforced on create too** (not only
read/update/delete): on `POST` / GraphQL `create…`, a role's field
allowlist drops any body field outside it, and a row `condition` field
is **forced** to the caller's resolved value — a body that supplies a
*different* value for it is rejected with `403`. So an owner-scoped role
(`user_id = $user_id`) can only create rows attributed to itself; it
cannot mass-assign another principal's id. REST and GraphQL share one
enforcement core (`codegen.EnforceCreateRBAC`), so both behave
identically. A role with neither a condition nor an allowlist creates
unrestricted (no added cost on the create hot path).

#### Per-resource conditions (`permissions`, G2)

The `conditions`/`actions`/`fields` above are **role-global**: the single
condition applies to *every* resource the role lists. To give one role a
**different condition (and actions and field allowlist) per resource**, declare a
`permissions` map instead of the role-global keys — each resource carries its own
grant:

```json
"rbac": { "roles": {
  "member": {
    "permissions": {
      "projects":  { "actions": ["read","create","update","delete"],
                     "conditions": { "field": "owner_id",   "op": "eq", "val": "$user_id" } },
      "documents": { "actions": ["read","create","update","delete"],
                     "conditions": { "field": "created_by", "op": "eq", "val": "$user_id" } },
      "tags":      { "actions": ["read"] },
      "posts":     { "actions": ["read","create","update","delete"],
                     "fields": ["id","title","status"],
                     "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                     "condition_actions": ["create","update","delete"] }
    }
  }
}}
```

- **Each resource is scoped by its OWN column** — `projects` by `owner_id`,
  `documents` by `created_by` — so a role can own-scope resources that don't share
  a column name. (Before, a row-scoped role could only span resources sharing one
  condition column.)
- **A resource with no `conditions`** (e.g. `tags`) is unscoped — the role reads
  every row.
- **`condition_actions`** scopes the condition to a subset of the actions; the
  actions *not* listed are unconditional. The example reads **all** posts but
  edits/deletes only its **own** ("read all, write own"). Omit `condition_actions`
  and the condition applies to every granted action (the safe default).
- **`fields`** is the per-resource response allowlist (a role may show different
  fields per resource). The condition `val` may be `$user_id`,
  `$external_client_id`, or a **literal** (e.g. `"published"` for a public role
  that reads only published rows).
- **Deny-by-default:** when `permissions` is present it is the SOLE source of truth
  — a resource absent from the map is `403`, the same as a role-global role that
  doesn't list it.
- The condition applies to **every operation that already honors conditions**:
  list/get (filters rows), aggregate (scopes the `COUNT`/`SUM`/…), create (forces /
  rejects the condition field — mass-assignment block), update/delete (only own
  rows), and relation embeds (`?include=`) — on **both REST and GraphQL** (all
  funnel through `rbac.Policy.Evaluate(resource, action)`).
- **Mutually exclusive with the role-global form** — a role uses one or the other,
  never both (validation rejects mixing).
- **Validated at load:** a `permissions` entry over an unknown resource, an unknown
  action, a `condition` field that **doesn't exist on that resource**, a
  `condition_actions` value not in `actions`, or a `fields` entry that doesn't
  exist — each rejects the schema with a clear error (never a masked `500` at
  runtime). Strict-key, like every other level.
- **Backward-compatible:** existing schemas that use the role-global form (the ERP
  demo, model-lab, quickstart) behave **identically** — the per-resource path is
  additive and the legacy serialization is byte-unchanged. Measured `no_change` on
  the RBAC read+write hot path. Example: [examples/model-lab/rbac-per-resource.json](examples/model-lab/rbac-per-resource.json).

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
- **Total count (opt-in)**: `?count=true` on a list adds `meta.total` +
  `meta.total_pages` (a `COUNT(*)` over the SAME filtered + RBAC-scoped set).
  **Off by default** — the plain list pays nothing and is byte-identical; turn it
  on only when you need the total (closes the old REST↔GraphQL asymmetry: GraphQL
  always returned `total`).
- Responses are `{"data": [...], "meta": {...}}`.

## Aggregation (G3)

`GET /api/{resource}/aggregate` runs `count` / `sum` / `avg` / `min` / `max` and
`group_by` over a resource. It is a **separate read path** (the list/CRUD SQL is
untouched) and is scoped **exactly like a list read of that resource**: the role's
row condition is injected into the `WHERE`, the same `filter[…]` apply, and the
tenant `search_path` is enforced — so a row-scoped role aggregates **only its own
rows** (no totals leak across principals), and a field outside the role's
allowlist **cannot be aggregated** (→ `403`, no leak via aggregates).

```
GET /api/orders/aggregate?count&sum=total,tax&avg=total&min=created&max=created&group_by=status&filter[status][eq]=paid
```

- **Functions** (a fixed allowlist — never arbitrary SQL): `count` (presence
  flag → `COUNT(*)`); `sum` / `avg` (numeric fields only); `min` / `max` (numeric
  **or** `time`). Each of `sum`/`avg`/`min`/`max` takes a comma-separated field
  list. `group_by` is a comma-separated field list (anything but `json`).
- Field and `group_by` names are validated against the schema; an unknown field,
  a function applied to an incompatible type, or no function requested → `400`.
- **Response — without `group_by`** (one overall object, only the requested keys):
  `{"count":17,"sum":{"total":4210.5},"avg":{"total":247.6},"min":{"created":"…"},"max":{"created":"…"}}`
- **Response — with `group_by`** (`groups`, each carrying its group fields + the
  aggregates): `{"groups":[{"status":"paid","count":12,"sum":{"total":3900}}, …]}`

**GraphQL:** the same surface is `<resource>Aggregate(filter, count, sum, avg,
min, max, group_by)` returning `AggregateResult`:

```graphql
{ ordersAggregate(count:true, sum:["total"], group_by:["status"]) {
    count                       # overall count (null when group_by is used)
    values { fn field value }   # overall sum/avg/min/max (value is a String)
    groups { key { field value } count values { fn field value } }
} }
```

Aggregate `value`s are **Strings** in GraphQL (one shape carries integers, floats
and timestamps without a custom scalar — parse by the field's known type). The
RBAC scope + field allowlist + filters apply identically to the REST endpoint.

## Atomic multi-resource transactions (G4)

`POST /api/transaction` runs several create/update/delete operations across
resources in **ONE Postgres transaction** — all-or-nothing. If any operation fails
(validation, RBAC, a constraint, a guard, a not-found), the WHOLE batch rolls back:
zero partial state. It is the seam for a transfer (debit + credit), a checkout
(order + lines + stock decrement), or any cross-resource invariant.

```json
POST /api/transaction
{
  "operations": [
    { "op": "create", "resource": "ledger_entries", "data": { "account_id": "…", "amount": -100, "ref": "x1" } },
    { "op": "create", "resource": "ledger_entries", "data": { "account_id": "…", "amount":  100, "ref": "x2" } }
  ]
}
```

- **Operations** (executed in order): `create` (`{op,resource,data}`), `update`
  (`{op,resource,id,data}` — PATCH/partial semantics), `delete`
  (`{op,resource,id}`).
- **Every operation is authorized and validated EXACTLY like its single-op
  counterpart**: per-resource RBAC (G2 — its own condition + field allowlist +
  create mass-assignment block), the declarative validators, and the
  `before_create`/`before_update` hooks all run. A row-scoped role can only write
  its own rows inside a batch; an operation the role may not perform fails the whole
  transaction with `403`.
- **Outbox events emit in the SAME transaction** — a resource with
  `events:[…]` enqueues its `{resource}.{created|updated|deleted}` event per op,
  atomically with the write (a rolled-back batch emits nothing).
- **Tenant-scoped**: the transaction runs in the request tenant's `search_path`
  (Host) and cannot cross tenants.
- **Optimistic-lock / conditional `guard`** (update & delete): extra predicates the
  row must satisfy, else the op matches no row and the batch fails — the
  compare-and-set tool for race-safe writes (e.g. decrement stock only if it hasn't
  changed):

  ```json
  { "op": "update", "resource": "products", "id": "…", "data": { "stock": 7 },
    "guard": [ { "field": "stock", "op": "eq", "value": 10 } ] }
  ```

  `op` ∈ `eq | ne | gt | gte | lt | lte`; the field must be a declared column and
  the value is type-checked + bound (never interpolated). An update/delete that
  matches no row (not found, excluded by the role's row condition, **or** a guard
  not met) fails the transaction.
- **Errors name the failing op** (never an opaque 500): a failure returns the
  failing op's status with `{ "error", "failed_operation": <index>, "op",
  "resource"[, "fields"] }`. A unique collision → `409`, an unknown field → `422`,
  forbidden → `403`, a bad op/resource → `400`.
- **Limit**: at most **100** operations per request (`APPITOOLS_MAX_TX_OPS`) →
  `400` over the cap; the 1 MiB body cap also applies.
- **Reserved**: a schema resource may not be named `transaction` (it would shadow
  this route).
- A committed batch **invalidates the tenant's response cache** (like a single-op
  write), so a read right after a transaction reflects it (no stale cached GET).
- **Not in v1** (documented): `after_*` webhooks and the SSE broadcast do NOT fire
  for batch ops (use the emitted **outbox events** to react); no GraphQL batch
  (REST only); no in-place arithmetic (`stock = stock - n`) — use a compare-and-set
  `guard`. The single-op `POST/PATCH/DELETE` path is **unchanged** (the batch is a
  separate path; measured `no_change`). A 2-op transaction measured ≈ 6 ms p50 vs ≈
  4 ms for one standalone write (the shared BEGIN/COMMIT is amortized). Example:
  [examples/model-lab/atomic-tx.json](examples/model-lab/atomic-tx.json).

## GraphQL

`POST /graphql`. Queries plus `create<Singular>` / `update<Singular>` /
`delete<Singular>` mutations (e.g. `createTask`, `updateTask`, `deleteTask`).
`update<Singular>(id, input)` is a PARTIAL update (PATCH semantics) sharing the
REST update core — same declarative validation, field-level RBAC allowlist,
row-level condition, and outbox emission (a resource with `events:["update"]`
emits `…​.updated` from the mutation, identically to REST PATCH). Its `input`
type has every non-auto field optional. `create<Singular>` and `delete<Singular>`
likewise share the REST create/delete cores (`codegen.RunInsert` / `RunDelete`): a
resource with `events:["create"]` / `["delete"]` emits `<resource>.created` /
`<resource>.deleted` from the mutation, byte-for-byte identical to REST POST /
DELETE (same topic + lean payload, same tx). With `update<Singular>`, **all three
GraphQL write mutations emit identically to their REST counterparts**. GraphQL always answers **HTTP 200**:
check the `errors` array in the body, never the status code. Validation
failures arrive as `errors[].extensions.fields` (same rule engine as
REST). Introspection is disabled in production (the `__schema`/`__type`
fields are rejected outside development; `__typename` is allowed);
GraphiQL only runs with `APPITOOLS_ENV=development`. The query analyzer
also bounds document size as an alias-amplification guard: at most **50
root selections** per operation and **2000 total selections** across the
whole document — over either limit the request is rejected (there is no
separate nesting-depth counter).

## OpenAPI spec + Swagger UI (API-PRODUCTIVA-V1)

The engine generates an **OpenAPI 3.0.3** document from the schema and serves it,
plus an interactive explorer — no flag needed:

- `GET /openapi.json` / `GET /openapi.yaml` — the full spec (unauthenticated; the
  contract is engine-global, the same for every tenant). Covers the schema-derived
  `/api/{resource}` CRUD + subresources **and** the always-present engine surface:
  the `/auth/*` endpoints (with `security: []` to mark the unauthenticated ones)
  and the `/api/files` store. The 422 body is modelled as `ValidationErrorResponse`
  (`{error, fields[]}`); list responses advertise `meta` only (no `links` — the
  live engine returns `{data, meta:{page,per_page,has_next,has_prev}}`, COUNT was
  dropped for performance).
- `GET /docs` — Swagger UI (loaded from a pinned CDN) pointed at `/openapi.json`,
  for interactive "Try it out" against the same origin.
- The CLI still prints the spec: `appitools openapi schema.json` (YAML) — same
  document the HTTP routes serve.

## CORS (API-PRODUCTIVA-V1)

CORS is **configurable instance infrastructure**, NOT a schema key. It is
**disabled by default** (no `Access-Control-*` headers emitted, no middleware in
the chain — zero cost). Enable it by listing browser origins:

- `APPITOOLS_CORS_ORIGINS` — comma-separated exact origins, or the single `*`.
  **Setting it ENABLES CORS**; empty keeps it off.
- `APPITOOLS_CORS_METHODS` (default `GET,POST,PUT,PATCH,DELETE,OPTIONS`),
  `APPITOOLS_CORS_HEADERS` (default `Authorization,Content-Type`),
  `APPITOOLS_CORS_EXPOSE_HEADERS` (default none),
  `APPITOOLS_CORS_CREDENTIALS` (`true`/`1`/`on`; default false),
  `APPITOOLS_CORS_MAX_AGE` (preflight cache seconds; default 600).
- **Scope**: only the browser-consumed routes — `/api/*`, `/auth/*`, `/graphql`,
  `/openapi*`. The control plane (`:9090`), `/admin`, `/metrics`, `/debug` are
  **never** given CORS (operation surfaces, same-origin / machine callers).
- **Preflight**: `OPTIONS` is answered `204` with the CORS headers BEFORE auth (a
  preflight has no token), so it never 401s. A disallowed origin gets no
  `Allow-Origin`. With credentials + `*`, the request origin is reflected (the
  Fetch spec forbids `*` with credentials). Measured `no_change` on the hot path.

## Auth cycle for an API consumer (API-PRODUCTIVA-V1)

The complete cycle a client uses (all tenant-aware via Host; the issued JWT is the
SAME one `/api/*` validates — HS256, 24 h TTL, stateless):

1. **Log in** — `POST /auth/login {email,password}` → `200 {user, token}` (or
   `{mfa_required, mfa_token}` if TOTP MFA is on → finish at `/auth/mfa/verify`).
   (Public **signup** is `POST /auth/signup`, enabled only when
   `APPITOOLS_AUTH_SIGNUP_ROLE` is set.)
2. **Use** the token: `Authorization: Bearer <token>` on every `/api/*` call.
3. **Expiry** — a request with an expired/invalid token gets a clear
   `401 {"error":"invalid token: …"}` (never a 500). That is the client's signal
   to refresh.
4. **Refresh** — `POST /auth/refresh` with the still-valid token (in the
   `Authorization` header **or** `{"token":"…"}` body) → `200 {token}` with a fresh
   expiry. Re-mint, not rotation (stateless); the old token works until its own
   `exp`. There is no separate long-lived refresh token.
5. **Log out** — the JWT is **stateless**: logout = the client **discards the
   token** (there is no server-side session or denylist to add per-request hot-path
   cost). For forced revocation an admin **suspends** the user/tenant (blocks new
   logins; already-issued tokens live to `exp` — the documented stateless trade-off).

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

## Authentication — password identity core (AUTH-CORE-V1)

The engine ships a **multi-tenant-aware password identity** core: signup, login
and token refresh, served on three unauthenticated-but-tenant-aware routes (no
schema declaration needed). It is auth-as-product, not a parallel token path —
**the JWT a login issues is byte-identical in shape to the one the engine already
validates** (same `user_id`/`role`/`tenant_id` claims, HS256, same `JWT_SECRET`).
Identity answers WHO you are; the schema RBAC still governs WHAT you may do.

- `POST /auth/signup` — `{ "email", "password" }` in the tenant's context (Host
  subdomain). Creates a user in the tenant's own schema, returns the user (never
  the hash) **and** a JWT (auto-login). `201`. Duplicate email **within the
  tenant** → `409`; the SAME email in another tenant is a different user and
  succeeds (the advantage — see below). A client-supplied `role` is **ignored**
  (a public endpoint never lets a caller pick its own role).
- `POST /auth/login` — `{ "email", "password" }` → `200 {user, token}`. Wrong
  password and unknown email return the **identical** `401 {"error":"invalid
  credentials"}` (anti-enumeration; the unknown-email path still runs an argon2
  verify so timing does not leak existence either). Throttled per (tenant, email)
  → `429` on brute-force.
- `POST /auth/refresh` — re-mints a fresh token from a still-valid one (token in
  the `Authorization: Bearer` header or `{"token"}` body). Tenant-checked (no
  cross-tenant refresh). Stateless: a deleted user's token stays valid until its
  `exp` (standard stateless-JWT trade-off).

**Per-tenant users, email unique PER SCHEMA.** Users live in
`tenant_<id>.auth_users` (the `auth_` prefix is reserved — `validate` rejects a
resource named `auth_*`, so it never collides with a schema resource). Email is `UNIQUE` on
`lower(email)` **within the tenant's schema**, not globally — so the same email
is a distinct account in tenant A and tenant B. This is the structural advantage
over Supabase, whose Auth cannot do multi-tenancy because its `email` is globally
unique. The table is created idempotently on first use, inside the tenant schema,
exactly like the rest of the tenant's data.

**Security.** Passwords are hashed with **argon2id** (pure Go, no CGO; m=19 MiB,
t=2, p=1 — the OWASP minimum, ~50–60 ms per signup/login on a 1-vCPU VPS). That
cost is intentional and paid ONLY on signup/login — never on the request hot
path (which validates an already-minted JWT). The hash is never returned or
logged. Login is rate-limited per identity (anti-brute-force) on top of the
per-tenant request limiter.

**Config.** Public signup is **disabled by default** (safe — no accidental
self-service accounts):

- `APPITOOLS_AUTH_SIGNUP_ROLE` (or `Config.AuthSignupRole`) — the role assigned
  to every public signup. **Setting it ENABLES public signup; leaving it empty
  keeps signup disabled** (`POST /auth/signup` → `403`). The role must be one the
  schema's RBAC declares, or the engine refuses to boot (a typo never becomes a
  silent misconfiguration). Login and refresh work regardless (they operate on
  already-created users).
- `APPITOOLS_AUTH_MIN_PASSWORD` (or `Config.AuthMinPasswordLength`) — minimum
  signup password length (default 8).

### Password reset + email verification (AUTH-EMAIL-V1)

Built ON the email consumer (`pkg/consumers`, `APPITOOLS_WORKER_MODE=email`) via the
transactional outbox — the first auth↔email integration. A request endpoint writes
a single-use token AND enqueues an `email.send` event **in one transaction** (token
and email are atomic); the email is delivered **async** by the worker, so the
request returns immediately. If no email worker is running the event waits durably
in the outbox and goes out when one starts.

- `POST /auth/reset/request` — `{ "email" }`. **Uniform** `200 {"message":"if that
  email is registered, a link has been sent"}` whether or not the email exists
  (anti-enumeration; a real email is enqueued only for a real user). Throttled per
  (tenant, email).
- `POST /auth/reset/confirm` — `{ "token", "new_password" }`. Consumes the token
  (single-use, ≤1 h old) and sets the new argon2id hash; **all other outstanding
  reset tokens for that user are invalidated** in the same tx. Invalid/expired/used
  token → `400`; a too-short password → `422`.
- `POST /auth/verify/request` — `{ "email" }`. Same uniform anti-enum response;
  enqueues a verification email for an existing, not-yet-verified user.
- `GET /auth/verify?token=…` (clickable email link) **or** `POST /auth/verify`
  `{ "token" }` — consumes the token (single-use, ≤24 h) and flips
  `email_verified` true. Invalid → `400`.

Tokens live in `tenant_<id>.auth_tokens` (per-tenant, isolated — a token of one
tenant is useless in another). Only the token's **SHA-256 hash** is stored; the
plain token rides the email link. The link origin is the request's tenant Host by
default (multi-tenant-correct), or `APPITOOLS_AUTH_BASE_URL` if set.

**Config (AUTH-EMAIL-V1):**

- `APPITOOLS_AUTH_REQUIRE_VERIFIED` (`true`/`1`/`on`) — block login for an
  unverified email (`403`). Default off (login unchanged).
- `APPITOOLS_AUTH_BASE_URL` — override the email-link origin (else derived from the
  request Host).
- `APPITOOLS_EMAIL_TOPIC` — outbox topic for the email events (default `email.send`);
  **must match the email worker's** `APPITOOLS_EMAIL_TOPIC`. Run the deliverer with
  `APPITOOLS_WORKER_MODE=email` (templates `verification` + `reset` ship built-in).

### Social login — OAuth2 (AUTH-OAUTH-V1)

Sign-in with **Google, GitHub, Microsoft**, multi-tenant-aware. A social login
yields the SAME engine JWT a password login does (one token contract); the
provider answers WHO you are, the schema RBAC still governs WHAT you may do.
Implemented with the standard authorization-code flow over `net/http` — **no new
dependency** (no goth/x-oauth2), CGO-free.

- `GET /auth/oauth` — lists the configured providers (`{"providers":["google",…]}`)
  so a frontend knows which buttons to show.
- `GET /auth/oauth/{provider}` — starts the flow: `302` to the provider with a
  **signed state** and minimal scopes (email + basic profile). The tenant is taken
  from the request Host here and **sealed into the state**.
- `GET /auth/oauth/{provider}/callback` — the provider redirects back here with
  `code` + `state`. The engine validates the state, exchanges the code, reads the
  provider's `{provider_user_id, email}`, resolves the user, and returns
  `200 {user, token}` (or `302` to `APPITOOLS_OAUTH_SUCCESS_REDIRECT#token=…` if set).

**Tenant lives in the SIGNED STATE, never the Host.** The callback's Host is the
fixed callback domain (one registered redirect URI per provider), not the tenant
subdomain — so the tenant CANNOT come from the Host. The state is a short-lived
(10 min) HS256-signed token (engine `JWT_SECRET`) carrying `{tenant, provider,
nonce}`; the signature is the **anti-CSRF** guard (an attacker cannot forge a valid
state) and the tamper-proof tenant carrier.

**Identity linking** (table `tenant_<id>.auth_identities`, `UNIQUE(provider,
provider_user_id)` per schema):

- The stable key is **`provider_user_id`** (not the email, which can change).
- Returning identity → logs in to its user.
- A NEW identity whose email already belongs to a user → the identity is **linked**
  to that user (no duplicate; one person, one account, several sign-in methods).
- A brand-new email → a user is **created** with NO password (`password_hash=''`,
  so it cannot password-login until a reset) and `email_verified=true` (the provider
  verified it) — **only if** auto-provisioning has a role.
- The SAME social account in tenant A and tenant B is two DISTINCT users (the
  per-schema-unique-email advantage holds for social login too).

**Config (a provider with no client id is simply NOT offered — never a boot error):**

- `APPITOOLS_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID` / `…_CLIENT_SECRET` — per
  provider credentials. Register the redirect URI
  `{callback}/auth/oauth/{provider}/callback` with each provider.
- `APPITOOLS_OAUTH_CALLBACK_URL` — the FIXED public origin the providers redirect
  back to (e.g. `https://auth.example.com`). Empty ⇒ derived from the request
  (fine in dev/single-domain; set it for multi-tenant prod).
- `APPITOOLS_OAUTH_DEFAULT_ROLE` — role for a user auto-created on first social
  login; empty falls back to `APPITOOLS_AUTH_SIGNUP_ROLE`. If BOTH are empty, a
  brand-new social email is rejected (`403`) while existing users still link/login.
  A set role must exist in the schema RBAC (else boot fails).
- `APPITOOLS_OAUTH_SUCCESS_REDIRECT` — optional; `302` to `<url>#token=<jwt>` for a
  browser SPA instead of returning JSON.

### Multi-factor auth — TOTP (AUTH-MFA-V1)

Optional, per-user **TOTP** second factor (Google Authenticator / Authy / 1Password
…), multi-tenant-aware. TOTP only (RFC 6238) — no SMS (no provider, no SIM-swap).
Implemented from the standard library (HMAC-SHA1 + base32, verified against the RFC
6238 test vectors) — **no new dependency**.

- `POST /auth/mfa/enable` *(needs a session JWT)* — generates a TOTP secret, stores
  it ENCRYPTED with `enabled=false`, returns `{secret, otpauth_uri}` **once** (the
  client renders the QR from the `otpauth://` URI; the engine ships no image encoder).
- `POST /auth/mfa/confirm` *(session JWT)* — `{ "code" }`. Validates the first TOTP
  code, flips `enabled=true`, and returns `{enabled:true, backup_codes:[…]}` **once**
  (10 one-time recovery codes; only their hashes are stored). Requiring a valid code
  before enabling means a mis-scanned secret can never lock the user out.
- `POST /auth/mfa/verify` *(no session — uses the login challenge)* —
  `{ "mfa_token", "code" }`. Accepts a current TOTP code (±1 step / ±30 s) OR a
  one-time backup code (consumed), then mints the FINAL engine JWT. Throttled per
  (tenant, user). Bad code → `401`.
- `POST /auth/mfa/disable` *(session JWT **and** a second factor)* —
  `{ "code" }` (TOTP/backup) **or** `{ "password" }`. The session JWT alone is NOT
  enough (a stolen access token can't strip MFA). Clears the secret + backup codes.

**Two-step login.** When a user has MFA enabled, `POST /auth/login` with the right
password returns `200 {"mfa_required":true,"mfa_token":"…"}` — **the final JWT is
withheld**. The client completes it at `/auth/mfa/verify`. The `mfa_token` is a
short-lived (5 min) HS256 token whose claim keys differ from the access token's, so
presented as a Bearer to `/api/` it carries no role → RBAC denies (it can only
finish the MFA step, never authorize CRUD). MFA applies to the password login;
social login is gated by the provider.

**Storage / security.** `tenant_<id>.auth_mfa` (per-user, the TOTP secret
**encrypted at rest** with AES-256-GCM — recoverable because the server re-derives
each code; key = `APPITOOLS_MFA_KEY` or the JWT secret) and
`tenant_<id>.auth_backup_codes` (hash only, one-time). Per-tenant, isolated — a
user's MFA in tenant A never affects tenant B. TOTP window is exactly ±1 step.
**Config:** `APPITOOLS_MFA_KEY` (secret-encryption key; falls back to `JWT_SECRET`
— rotating it invalidates enrollments), `APPITOOLS_MFA_ISSUER` (authenticator-app
label; default `Appitools`).

**Auth-as-product is now complete: password (signup/login/refresh) + reset/verify +
OAuth social login + TOTP MFA.**

## Admin API — platform super-admin + management (ADMIN-API-V1)

The backend of the admin panel. It is **not a second permission system** — it
INHERITS the schema RBAC, the schema-per-tenant isolation, auth-as-product, the
control plane, and the observability that already exist. It adds only the one
thing that did not exist: a **platform super-admin** (above the tenants) plus a
consolidated, authenticated `/admin/*` API. Two access levels, modelled as roles:

- **Platform super-admin** — lives in a SYSTEM schema (`appitools_system.platform_admins`),
  ABOVE every tenant. Not a tenant user. Authenticates with the SAME
  auth-as-product (password login + TOTP MFA) but against the system schema, and
  receives a **platform JWT** (claim `scope=platform`, signed with the same
  `JWT_SECRET`). A platform JWT carries no tenant identity, so presented to a
  tenant `/api/` route it is denied by RBAC (deny by default) — it can never act
  as a tenant without explicitly selecting one through this API.
- **Tenant admin** — just a tenant user whose schema RBAC role is broad
  (wildcard `resources`/`actions`). Nothing new; the isolation + RBAC already
  govern it.

The legacy `X-Admin-Key` still works for **machine-to-machine** callers (DevHub,
scripts) on the management routes — humans log in (auditable identity + MFA),
machines present the key. Two paths for two consumers; the key is NOT removed.

**Bootstrap the first super-admin** (no public super-admin signup — a super-admin
cannot be created by a super-admin that does not yet exist):

```bash
DATABASE_URL=… JWT_SECRET=… \
  appitools admin create --email me@example.com --password 'a-strong-passphrase'
```

**Routes** (all under `/admin/*` on the data plane; they do their OWN auth and are
off the CRUD/JWT hot path — measured `no_change`):

- Super-admin auth: `POST /admin/auth/login` (→ `{admin, token}`, or
  `{mfa_required, mfa_token}` when MFA is on), `POST /admin/auth/refresh`,
  `POST /admin/auth/mfa/{enable,confirm,verify,disable}` (TOTP, mirrors the tenant
  MFA; `enable/confirm/disable` need the platform token, `verify` completes the
  login challenge). The admin key is NOT accepted on `/admin/auth/*`.
- Tenants (platform token OR admin key): `GET /admin/tenants`,
  `POST /admin/tenants` (wraps the control plane — same schema validation),
  `GET /admin/tenants/{id}`, `POST /admin/tenants/{id}/suspend` /`/activate`,
  `DELETE /admin/tenants/{id}` (**destructive** — requires
  `{"confirm":"<tenant_id>"}` in the body; drops the tenant schema CASCADE).
- Tenant users (platform token OR admin key): `GET /admin/tenants/{id}/users`,
  `POST` (create with an admin-chosen role, validated against the RBAC),
  `PATCH /admin/tenants/{id}/users/{uid}` (`role` and/or `suspended`),
  `DELETE …/users/{uid}`. Data CRUD is NOT duplicated — the panel consumes the
  existing generated `/api/*` per tenant.
- Tenant data browse (read-only; platform token OR admin key): `GET
  /admin/tenants/{id}/resources` (the tenant's resources with each field's type,
  plus the tenant's RBAC role names — for the data + user UIs), `GET
  /admin/tenants/{id}/data/{resource}` (a page of records). The data endpoint
  REUSES the engine's validated query builder (`filter[…]`, `sort`/`order`,
  `per_page`, keyset `after` — same params as `/api`) over the tenant-scoped DB; it
  exists because the panel (one origin, platform JWT) cannot reach the Host-scoped
  `/api/{resource}`. Read-only in V1.2 (record edit is a documented increment).
- Observability (consolidated, correct authz): `GET /admin/observability/tenants/{id}`
  serves the SAME data as `/debug/tenant/{id}` — a platform super-admin sees ANY
  tenant, a tenant admin (valid tenant JWT, matching tenant, admin-grade role)
  sees ONLY its own; everyone else 403. The store already filters by `tenant_id`,
  so no cross-tenant leak. Payload: `latency` (cached/uncached percentile split),
  `slo` (multi-window burn rate + status), `anomaly_count` + **`anomalies`** (the
  recent z-score anomaly events — `{ts, latency_us, z_score}`, a small per-tenant
  ring), `errors` (deduplicated groups), `recent_traces` (in-memory, with per-stage
  span breakdown), and the optional `?history=<hours>` (per-minute snapshots —
  `{ts, p50_us, p95_us, burn_rate, error_ratio, slo_status}`) and `?traces=slow`
  (the persisted slow/errored traces of the last 24h, with full waterfall spans).
  These are read-only projections of data the engine already computes; the anomaly
  ring is recorded only on a detection (off the common request path).

**Tenant suspension** flips a control-plane flag and blocks NEW logins for that
tenant's users (enforced on the non-hot login path); already-issued JWTs live to
their `exp` (the documented stateless-JWT trade-off). It adds **no per-request
check to the CRUD/JWT hot path** — the p50 is preserved.

**Config:** `APPITOOLS_PLATFORM_SUPER_ADMIN_ROLE` (platform role marker; default
`platform_super_admin`), `APPITOOLS_PLATFORM_MFA_ISSUER` (authenticator label;
default `Appitools Platform`). The platform MFA secret is encrypted at rest with
`APPITOOLS_MFA_KEY` (falls back to `JWT_SECRET`), same as tenant MFA.

### Admin panel UI (ADMIN-UI-V1)

A SolidJS SPA, **embedded in the engine binary** (`//go:embed`, `pkg/adminui`) and
served at **`/admin`**. It consumes the ADMIN-API endpoints above. The panel is
**feature-complete for Phase 1**: **super-admin login (with MFA)**, **tenant
management**, **user management per tenant**, **read-only data navigation**, and
**observability** (ADMIN-UI-V2). A topbar **tenant selector** (persisted) sets the
context that Users + Data + Observability operate on (a future tenant-admin would
have a fixed tenant and no selector — documented extension point).

The **Observability** screen (ADMIN-UI-V2) is the visual face of the engine's
existing observability — it EXPOSES `GET /admin/observability/tenants/{id}`, it does
not re-implement anything. Three tabs: **Metrics** (ECharts line charts — p50/p95
latency over time and the SLO burn rate with the 6×/14.4× multi-window thresholds
overlaid, plus current percentile/SLO/anomaly stat cards), **Traces** (a normal
`DataTable` of recent + persisted-slow traces → click a row for a **span waterfall**:
each sequential stage is a bar positioned by cumulative offset and sized by
duration, with a side panel of the selected span's metadata; error traces tint red),
and **Issues** (the z-score **anomalies** table — when/latency/z-score — plus the
deduplicated error groups and the SLO summary). Charts are ECharts (canvas renderer,
tree-shaken, lazy-loaded in their own chunk so the rest of the SPA stays light),
theme-aware (colors re-resolved on the light/dark toggle), and data-ink high (no
chartjunk). Status uses the **double channel** (colour + icon + text). An opt-in
**Live** toggle on Metrics polls the snapshot every 5 s and updates the canvas in
place (true streaming SSE for metrics is a documented V2.1 increment — the obs API
is a JSON snapshot, not a stream).

- **Source**: `pkg/adminui/web/` (Solid + Vite). Stack: `@solidjs/router` (HASH
  routing), `@tanstack/solid-table` (headless sorting; a plain fixed-layout table —
  virtualization was removed to fix row-overlap and is re-added only past ~1000
  rows), `@ark-ui/solid` (accessible dialog), and `echarts` (canvas, tree-shaken,
  lazy-loaded only on the Observability route). The command palette (⌘K) is a small
  native component (cmdk-solid was skipped to keep the bundle/deps lean — same
  minimal-dependency ethos as the engine).
- **Routing is hash-based** (`/admin#/tenants`): client routes live in the URL
  fragment, so they NEVER collide with the `/admin/*` ADMIN-API-V1 routes. The Go
  server only serves `GET /admin` (the shell, `no-cache`) and `GET /admin/assets/*`
  (hashed bundles, `Cache-Control: immutable`). Vite `base` is `/admin/`.
- **Develop it**: `make admin-ui` (`cd pkg/adminui/web && npm install && npm run
  build`) → produces `web/dist`, then `make build`. Dev loop: `npm run dev` (Vite
  on :5174, proxies `/admin/*` to a local engine on :8080).
- **Build pattern — IMPORTANT (matches the devhub, NOT a committed dist):** the
  hashed assets `pkg/adminui/web/dist/assets/` are **gitignored**; only the built
  `dist/index.html` is committed (so `//go:embed web/dist` always resolves). This
  mirrors `tools/devhub/` exactly. Consequence: **the release/Docker/CI build MUST
  run `make admin-ui` before `go build`**, or the binary serves an empty shell (the
  engine logs a WARNING when only the placeholder is embedded). A bare `go build`
  from a fresh clone does NOT include the UI — same trade-off the devhub already
  lives with. (To make a bare `go build` ship the UI, un-ignore the assets; left as
  Miguel's call for consistency with the devhub.)
- **Light theme default** (better dense-data legibility), instant dark toggle (CSS
  variables, persisted in `localStorage` — this is a served app, not an artifact,
  so `localStorage` is fine). Status is shown with a **double channel** (colour +
  icon + text, WCAG 1.4.1), numbers use **tabular figures**, tables use
  hover-highlight (no zebra). No skeletons/optimistic UI — the backend is local
  (~ms), so rows render directly.

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
- Aggregation BEYOND `count`/`sum`/`avg`/`min`/`max` + `group_by` (e.g. `HAVING`,
  `DISTINCT`, expression aggregates, window functions) — the
  [Aggregation](#aggregation-g3) surface is exactly those functions over schema
  fields, nothing more. (`count` total IS now a thing: `?count=true` on a list, or
  the aggregate endpoint.)
- FK **constraints** / cascades. (Declarative relations DO exist — nested
  `?include=` embeds via `json_agg`+LATERAL, see
  [Declarative relations](#declarative-relations--nested-embeds-relations-adr-019)
  — but they create no FK constraint and no `ON DELETE` cascade; the field-level
  `relation` still only adds the read-only subresource route.)
- `workflows` schema block — parsed for forward compatibility, no executor.
- OTLP/OpenTelemetry export (observability is Prometheus `/metrics` + an
  internal trace ring).
- A hosted/SaaS version — self-hosted only.
