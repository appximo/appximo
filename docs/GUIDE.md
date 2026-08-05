# The Appximo guide

> In a hurry? **[QUICKSTART.md](QUICKSTART.md)** is the condensed first mile —
> install → schema → live API → first user → production, each step with a
> manual track and an AI-agent track. This guide is the depth behind it.

**You have a backend to build and you have never seen this project before. This
is the document to read.** It was not written from the source code — it was
distilled from five field journeys in which real apps were built, deployed,
broken and operated on this engine: a commerce platform with payments (backend,
then frontend, then production operations), a veterinary app authored by AI from
one ~120-word paragraph of plain Spanish, and a booking app built by an agent that had **no access
to this repository** — only the printed docs. Everything a newcomer hits, in
the order they hit it, is either taught here or fixed in the engine.

Every number in this guide carries its condition and its date. The engine
figures come from [docs/CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md)
— a session that re-measured the project's claims against a running engine,
corrected six, and marked what could not be re-verified; the production-journey
figures (the chaos matrix, the consumer app's numbers, the restore drill) come
from the dated field measurements of 2026-07-31 that the certification
references. Nothing below is aspirational.

**Index**

1. [What it is — and who it is NOT for](#1-what-it-is--and-who-it-is-not-for)
2. [From zero to a live API](#2-from-zero-to-a-live-api-the-path-that-works-today)
3. [Building with an AI agent — the intended path](#3-building-with-an-ai-agent--the-intended-path)
4. [The 90 %: what the schema gives you](#4-the-90--what-the-schema-gives-you)
5. [The 10 %: your own Go, in-process](#5-the-10--your-own-go-in-process)
6. [The frontend: one binary serves it](#6-the-frontend-one-binary-serves-it)
7. [To production: a $7–16 VPS with real HTTPS](#7-to-production-a-716-vps-with-real-https)
8. [Operating it: migrations, backups, observability](#8-operating-it-migrations-backups-observability)
9. [What it does NOT do](#9-what-it-does-not-do)
10. [Verify everything yourself](#10-verify-everything-yourself)

---

## 1. What it is — and who it is NOT for

Appximo compiles **one JSON schema into a multi-tenant REST + GraphQL +
OpenAPI server at boot**. Go 1.25, no CGO, one static binary (~64 MB release
build), against a PostgreSQL **you** control. There are no handlers, models or
migration files to write: routes, SQL, validation, RBAC, OpenAPI docs and the
per-tenant tables are all derived from the schema when the process starts. It
is a code generator without the generated code — nothing is scaffolded into
files you then maintain.

The design bet is the **90/10 thesis**: 90 % of a backend (CRUD, filters,
relations, auth, RBAC, migrations, pagination, docs) is a *finite* decision
space, so it can be declared in a validated JSON — cheap for a human, and
cheap for an AI, to write. The other 10 % (business logic, integrations,
orchestration) is unlimited, so it is **plain Go**, running *in the same
process* with the same transaction and the same RBAC (§5). What you deploy is
one binary that serves the API, the GraphQL endpoint, the interactive docs,
the admin panel, the visual schema editor, and your own frontend.

### Who it is for

- A **B2B product with tens to low hundreds of customer-tenants**, each needing
  physically isolated data (schema-per-tenant in Postgres — structural
  isolation, not a row filter that can have bugs).
- A business group running **N internal apps** on cheap hardware.
- A team that wants the API contract **generated and enforced from a schema
  file**, with an AI agent doing most of the authoring.
- Operators who want observability **inside the binary** (Prometheus metrics,
  request traces, per-tenant debug, SLO burn rate) instead of a sidecar fleet
  that doesn't fit a 1–2 GB VPS.

### Who it is NOT for — read this before investing an hour

Stated as bluntly as the range deserves ([ADR-020](adr/ADR-020-product-vision-and-positioning.md)
declares it; the 2026-08-01 certification re-confirmed it):

- **Consumer scale. There is no HA and no clustering.** One node, vertical
  scaling. If you need multi-node failover, this is not your tool.
- **Thousands of tenants on one instance.** The healthy range is tens to low
  hundreds; the Postgres catalog degrades past ~1,000–2,000 schemas per
  cluster. Past the range, the answer is another instance (the binary is cheap
  to multiply), not more schemas.
- **Zero-downtime deploys.** A binary swap costs a measured **~0.3–0.6 s of
  502s** under live traffic (auto-rollback included). If a sub-second blip per
  deploy is unacceptable, look elsewhere (backlog ENG-2).
- **The simple single-tenant app.** PocketBase wins that on packaging — one
  process, zero dependencies, embedded SQLite. Appximo needs a PostgreSQL
  and earns its weight only when isolation, multi-tenancy or in-process custom
  logic matter.
- **Anyone who refuses to touch a terminal ever.** The visual editor covers
  the schema; first boot, the first platform admin and production installs are
  CLI commands.

If none of the bullets above disqualifies you, the rest of this guide takes
you from an empty box to a working HTTPS app. What you need on your side:
a Linux box (or laptop), a reachable **PostgreSQL** (any recent version — the
project develops against 16 and runs 18 in production; an empty database is
enough, the engine bootstraps itself), and **Go 1.25** if you build from
source.

---

## 2. From zero to a live API (the path that works TODAY)

> **Availability.** Three paths work today: **download the binary from
> [GitHub Releases](https://github.com/appximo/appximo/releases)** (v0.1.1,
> linux/darwin × amd64/arm64, checksums published — the installer downloads
> and verifies it automatically when you omit `--binary`), **build from a
> checkout**, or the **published Docker image**
> (`neodevtrix/appximo`, multi-arch) with the
> self-contained `docker-compose.yml` at the repo root. The image itself is
> public on Docker Hub, but the compose file, the example schema and every doc
> live in the repository — so until it is published, both paths in practice
> require access to it (the clone URL below is a placeholder for that reason).

Everything in this section was **executed on 2026-08-02** against a fresh
empty database; the outputs shown are real.

### 2.1 Build the binary

```bash
git clone <the-appximo-repo> && cd appximo
go build -o appximo ./cmd/appximo     # Go 1.25; ~14 s warm, ~85 MB plain build
```

(The 64 MB figure is the *release* build — `CGO_ENABLED=0 -trimpath
-ldflags="-s -w"`, see `scripts/build-engine.sh`. A plain `go build` is ~85 MB
and reports version `dev` in `/health`. Both are fine for a first run.)

Docker alternative: `docker compose up -d` with the repo's compose file pulls
the published image plus a Postgres and needs only three variables in `.env` —
the compose file inlines the control-plane DDL, so it works with no other file
from the repo.

### 2.2 Write the schema — the whole backend definition

Save this as `schema.json` (the name every later command uses):

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"], "default": "open" },
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

`$schema` and `version` are required (load fails without them). An `id` UUID
primary key is implicit — never declare it. Check your work:

```bash
./appximo validate schema.json   # → "Schema válido ✓"  (the CLI's messages
                                   #    are Spanish-first today — that's the pass)
```

The validator is **strict about keys at every level**: a typo (`webhooks`
instead of `hooks`, a field type `number`) rejects the schema with an error
listing the valid keys — nothing is silently ignored. `validate --json` emits
the same verdict as a machine-readable report (one entry per error with
`path`, `rule`, `message`, `expected`, `fix`) — that report is what an AI
agent uses to self-correct (§3).

### 2.3 Boot it

Three environment variables are hard-required; `serve` exits without them:

```bash
DATABASE_URL='postgres://user:pass@localhost:5432/mydb?sslmode=disable' \
JWT_SECRET='a-secret-of-at-least-32-characters!!' \
ADMIN_KEY='choose-an-admin-key' \
  ./appximo serve --schema schema.json --port 8080
```

`serve` runs in the **foreground** — leave it running and open a second
terminal for everything below (and export the same three variables there:
§2.5 reads `$JWT_SECRET`). On a **fresh empty database** the engine bootstraps
its own control-plane tables — no SQL file to apply, no `psql` needed (the
boot log says `control plane: public.tenants schema ready`). Two listeners
come up:

- **`:8080` — the data plane.** REST, GraphQL, `/docs`, `/auth/*`, the
  embedded UIs. This is the public one.
- **`:9090` — the control plane.** Tenant registration and schema
  administration, gated by `X-Admin-Key`. **Never expose it to the internet**;
  it is designed for localhost / an internal network (in production the
  installer binds it accordingly and the firewall closes it).

### 2.4 The fact everyone gets wrong: the tenant IS the Host header

Appximo is multi-tenant by construction. Every data request carries a
`Host:` whose **first label names the tenant**: `acme.example.com` → Postgres
schema `tenant_acme`. There is no "default tenant" — a Host with no subdomain
(bare `localhost`, an IP) is an error. So the first real step is registering a
tenant, which creates its isolated schema and tables **at that moment**:

```bash
curl -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: choose-an-admin-key" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"demo\",\"display_name\":\"Demo\",\"email\":\"demo@example.com\",
       \"plan\":\"free\",\"schema\":$(cat schema.json)}"
# → {"id":"demo","pg_schema":"tenant_demo",...}
```

The tenant id rule is **`^[a-z][a-z0-9]{1,29}$`** — lowercase letter first,
then lowercase letters or digits, 2–30 chars. **No hyphens, no underscores, no
uppercase** — the id is simultaneously a Postgres schema name (forbids `-`)
and a DNS label (forbids `_`), so it is the intersection of both alphabets. A
bad id is rejected with a suggested valid one. In production, **the id must
equal the first label of the domain that serves the app** — tenant `acme` is
reachable only at `acme.your-domain.com` (§7.2). (`plan` is a metadata label
stored on the tenant — nothing in the engine branches on it today.)

Tenant creation is all-or-nothing: if any step fails, everything rolls back —
no zombie tenants.

### 2.5 First request

Mint a dev token for a role the schema declares, then talk to the API:

```bash
TOKEN=$(./appximo token --secret "$JWT_SECRET" --tenant demo --role admin \
        --schema schema.json | tail -1)

curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer $TOKEN" -H "Host: demo.localhost" \
  -H "Content-Type: application/json" \
  -d '{"title":"first task"}'
# → {"id":"d29325e1-…","title":"first task","status":"open","due":null}
#   ("open" arrived from the schema default)

curl -g "http://localhost:8080/api/tasks?filter[status][eq]=open&per_page=20" \
  -H "Authorization: Bearer $TOKEN" -H "Host: demo.localhost"
# → {"data":[…],"meta":{"page":1,"per_page":20,"has_next":false,"has_prev":false}}
```

Notes that save you the first hour:

- `curl -g` — filter brackets need globbing off.
- `--schema` on the `token` command makes it **refuse a role the schema does
  not declare**, listing the declared ones. Without it, a typo'd role mints
  fine and then every request is `403 forbidden` (deny-by-default treats an
  undeclared role exactly like a denied one, deliberately — naming the
  difference would be a role-enumeration oracle).
- No token → `401 {"error":"missing token"}`. A validation failure → **422
  listing every failing field at once**:

  ```json
  {"error":"validation_failed","fields":[
    {"field":"title","rule":"required","message":"is required"},
    {"field":"status","rule":"enum","message":"must be one of: open, done"}]}
  ```

- A filter the type doesn't support is a 400 that teaches:
  `operator "like" not allowed for type "string" (allowed: eq, partial, start)`.

### 2.6 What else is already running

With that one schema, the same process is serving — verified live:

| Surface | Where | Auth |
|---|---|---|
| REST CRUD + subroutes + SSE | `/api/tasks`, `/api/tasks/{id}`, `/api/tasks/events` | JWT + RBAC |
| GraphQL (queries + create/update/delete mutations) | `POST /graphql` | JWT + RBAC |
| OpenAPI 3.0.3 spec | `/openapi.json`, `/openapi.yaml` | none (contract is public) |
| Swagger UI ("Try it out") | `/docs` | none to load; requests need the token |
| GraphiQL explorer | `/graphiql` | only with `APPXIMO_ENV=development` or `APPXIMO_GRAPHQL_PLAYGROUND=on` |
| Visual schema editor (Studio) | `/editor` | canvas opens as-is; deploy actions need the platform super-admin below |
| Admin panel | `/admin` | platform super-admin — created once, from the terminal: `appximo admin create --email you@x.com --password '…'` (needs `DATABASE_URL` + `JWT_SECRET`; nothing else in this chapter requires it) |
| Health probes | `/healthz`, `/readyz`, `/health` | none |
| Prometheus metrics, trace explorer | `/metrics`, `/debug/*` | `X-Admin-Key` |

### 2.7 The first end user (not the same thing as the JWT you minted)

The dev token above is for you. Real users live in the engine's built-in
identity: `POST /auth/signup` / `login` / `refresh`, per-tenant users,
argon2id hashes, optional email verification, OAuth (Google/GitHub/Microsoft)
and TOTP MFA. One thing WILL surprise you, so it goes here:

**Public signup is disabled by default.** `POST /auth/signup` answers `403`
until you set `APPXIMO_AUTH_SIGNUP_ROLE=<role>` — the role every public
signup receives (it must exist in the schema's RBAC, or the engine refuses to
boot). This is a safe default, not a bug; every first-time builder trips on
it, including the AI agent in the §3 experiment. Login issues the **same JWT
shape** the engine validates everywhere — identity answers *who you are*, the
schema RBAC governs *what you may do*.

---

## 3. Building with an AI agent — the intended path

Appximo is designed so that **your own AI agent** (Claude Code, Cursor —
whatever you already pay for) can build the app: the schema, any custom Go,
and the frontend. The engine prints its own agent-facing contract; there is no
API key to buy from us and no repo access needed:

```bash
appximo spec           > spec.md            # 1. the schema grammar
appximo backend-spec   > backend-spec.md    # 2. custom Go handlers, hooks, jobs, auth
appximo frontend-spec  > frontend-spec.md   # 3. the API contract a UI consumes
appximo specs          # …or all three in one stream (~2,300 lines, one paste)
```

Paste them into your agent **in that order**, describe your app, and have the
agent run the correction loop: generate → `appximo validate --json
schema.json` → fix what the report names → repeat until `"valid": true`. The
report gives one machine-readable entry per problem (`path`, `rule`,
`expected`, `fix`), which is exactly what a model needs to self-correct — it
is the same oracle the built-in generator uses.

### The evidence: an agent with NO repo access built a working app

This is not a hoped-for workflow; it was **tested cold** (2026-08-02). A fresh
agent was given only four printed documents — the three specs above plus the
project README — and the CLI binary; no engine source, no other material. It
was asked to build "Cancha Ya", a court-booking
app: schema (state machine + per-resource RBAC + relations), a custom Go
route (`POST /api/confirmar` computing a price inside the engine's
transaction), and an embedded vanilla-JS frontend. The result, from its own
field diary (`FRICTION.md`):

- The schema **validated on the first attempt** against `validate --json`.
- The Go **compiled on the first attempt**; the server booted on the requested
  ports on the first attempt.
- Tenant → signup → booking → confirm → RBAC probes → state-machine probes:
  the whole flow worked as the docs said. Mass-assignment of a foreign
  `user_id` → `403`; another member confirming a foreign booking → `404`
  (anti-enumeration); an illegal lifecycle move → a clear `422`.
- Browser e2e at mobile viewport (390×844), strict console: **9/9 PASS, zero
  console errors**.
- **Zero blockers** — its summary: "nothing exceeded 20 minutes without a
  way out".

Its seven recorded frictions are worth reading because they are the seven
things you would otherwise hit, and each is now either fixed or taught:

| # | What it hit | State today |
|---|---|---|
| 1 | Guessed the Go types `ctx.Get`/`ctx.Query` rows carry | Documented since: rows are `map[string]any`; uuid/time arrive as `string`, ints as `int64`, `jsonb` as a decoded map (backend-spec §3.3) |
| 2 | Replaced the binary, new process said "serving" then died — the old one's graceful drain still held the port, and the log announced before binding | The engine now **binds first and announces after** (ENG-34); backend-spec warns: wait for the port to free before starting the replacement |
| 3 | Route grant opened the endpoint but the handler's `ctx.Update` still needed a data grant | By design (two-layer RBAC, §5.4); the spec's warning paragraph prevented a "mystery 403" before it happened |
| 4 | Re-confirming an already-confirmed booking returned 200, not 422 | Documented state-machine semantics: re-sending the current state is a no-op; if your endpoint must reject "already in X", check the previous state in the handler |
| 5 | Playwright `waitForFunction` broke — the static mount's CSP (correctly) has no `unsafe-eval` | frontend-spec §11 now says: use selector/locator waits, never string-eval helpers |
| 6 | HTML `required` on an input masks the server's 422 path in e2e | frontend-spec: declare `minLength: 1` in the schema; drop the native attribute on forms whose 422 path you want to see |
| 7 | No `psql` on the box to pre-check the DB | Never needed: the engine self-bootstraps the control plane and the boot log states it |

### The built-in alternative, measured

If you (or your users) have no agent, `appximo ai-generate "<description>"`
runs the same loop with a cheap hosted model (`ANTHROPIC_API_KEY` required,
default `claude-haiku-4-5`): measured on a 120-case stratified corpus,
**~90 % of schemas valid on the first try, 100 % convergence within the loop,
~$0.006 per schema**. The generation grammar teaches the constructs a business
description implies — lifecycles became state machines 3/3 after that lesson
(0/3 before, on the same Spanish paragraph). And the validator's **warnings
layer** answers a question validity cannot: a row condition comparing
`$user_id` against a foreign-key column — *valid, deployable, and certain to
return zero rows forever* — is named at generation, at validate, at deploy and
at boot, in the owner's words, with the fix.

Studio (§2.6) ties the two worlds together: its **"Copy AI context"** button
exports `spec` + the app's current schema, ready to paste into any assistant.

---

## 4. The 90 %: what the schema gives you

This section is the map, not the reference — exact syntax lives in
[SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md) and `appximo spec`. What matters
when you model:

### Types — there are exactly eleven

`string`, `text`, `int`, `int64`, `float64`, `bool`, `uuid`, `time`, `json`,
`jsonb`, `file`. A `number` type does not exist (the validator rejects it,
naming the valid set). **Money has no type on purpose:** use `int64` in the
currency's minor unit with the unit in the name (`price_cents`) — a
`float64` price is a rounding bug, and payment APIs already speak minor units.
Prefer `jsonb` over `json` for anything you might query (`jsonb` is a real
Postgres document; it is the only type a `gin` index may cover).

### Validation is declarative, and errors arrive all at once

`required`, `unique` (collision → clean `409`), `enum`, `min`/`max`,
`minLength`/`maxLength`, `pattern` (RE2), `format` (`email|uuid|url|date`),
`default` (applied on create when the key is omitted). Everything compiles at
schema load; a write that violates rules returns **422 with every failing
field**, on REST, GraphQL and batch identically. Unknown keys anywhere in the
schema are load errors listing the valid keys.

### State machines — the lifecycle is enforced, not suggested

```json
"status": { "type": "string", "enum": ["pending","paid","shipped"],
  "default": "pending",
  "state_machine": { "initial": "pending",
    "transitions": { "pending": ["paid"], "paid": ["shipped"], "shipped": [] } } }
```

Creates are only legal in an `initial` state; updates may only move along a
declared transition (`422` otherwise); a state with no outgoing transitions is
**terminal — immutable**. The guard runs **inside the UPDATE's WHERE**, so two
concurrent updates cannot both advance the same row — no read-modify-write
window. REST, GraphQL, batch transactions and custom handlers (`Ctx.Update`)
all enforce the same declaration.

### Relations are real foreign keys

`"customer_id": {"type":"uuid","relation":"customers","on_delete":"restrict"}`
creates a **real Postgres FK** (auto-indexed) plus a read subroute
(`GET /api/orders/{id}/customer`, RBAC of the *target* enforced). `on_delete`
∈ `restrict` (default — deleting a referenced row is a 409, never a silent
orphan) / `cascade` / `set_null`; `on_update` and `references` (FK to a
non-`id` unique column) exist; composite multi-column FKs use a resource-level
`foreign_keys` block. For **nested reads in one round-trip**, declare a
`relations` block and opt in per request with `?include=lines,customer`
(depth ≤ 2): the embed is built in Postgres (`LEFT JOIN LATERAL` + `json_agg`,
no N+1), the target's RBAC — row conditions and field allowlist — is compiled
into the SQL, and a request without `?include=` pays exactly nothing.

### RBAC — deny by default, enforced on create too

Two forms: role-global (`resources`/`actions`/`fields`/`conditions`) and
per-resource `permissions` (each resource with its own actions, condition and
field allowlist — "read all, write own" via `condition_actions`). Row
conditions are single-column equality against `$user_id`,
`$external_client_id` or a literal — **any other `$var` rejects the schema at
load**. The allowlist and condition are enforced on **create** as well: a
row-scoped role cannot attribute a row to another principal (`403`
mass-assignment block). A record excluded by a row condition reads as `404`,
not `403` — existence never leaks. Custom endpoints are granted with the
separate `routes` block (§5.4).

### The query surface (memorize the shape, not the list)

- **Filters:** `?filter[field]=v` or `?filter[field][op]=v`. Ops by type:
  strings `eq|partial|start`; numbers `eq|gt|gte|lt|lte`; `time` adds
  `after|before`; `uuid`/`bool`/`json`/`jsonb`/`file` are `eq` only — and every
  nullable column also takes `is_null` (`true` → IS NULL, `false` → IS NOT
  NULL; on `id` or a `required` field it is a 400 naming why). **Anything
  else — `neq`, `in`, `like`, a typo — is a 400 that names the
  operator and lists the allowed set.**
- **Search:** `?search=term` — case-insensitive substring across the
  resource's string/text fields. A plain ILIKE, not a ranked engine.
- **Sort:** `?sort=field&order=asc|desc` — **one field**. Multi-field sort
  does not exist; `sort=a,b` is a named 400.
- **Pagination:** keyset — `?after=<id>` / `?before=<id>` + `per_page`
  (default 20, max 100). `?page=` exists but is OFFSET; prefer keyset. A
  cursor request owns its shape: cursor+sort, cursor+page, cursor+count are
  named 400s.
- **Totals are opt-in:** `?count=true` adds `meta.total` (a real COUNT over
  the same filtered, RBAC-scoped set). The plain list never pays for a COUNT.
- **Aggregation:** `GET /api/{resource}/aggregate?count&sum=total&group_by=status`
  — `count/sum/avg/min/max` + `group_by`, same filters, same RBAC scope (a
  row-scoped role aggregates only its own rows; a field outside the allowlist
  is 403). Nothing beyond those five functions (no HAVING, no expressions).
- **Malformed input is never silently ignored.** A repeated parameter, an
  empty `?sort=`, an unknown aggregate function, a wrongly-typed filter value
  — each is a 400 naming exactly what and why ([ADR-024](adr/ADR-024-unrecognized-input.md):
  the engine either honors an input or says it cannot).

### Hooks, events, atomic batches, files

- **Hooks:** `before_create`/`before_update` run sandboxed JS (Goja,
  watchdog-limited) or WASM (Wazero) and can reject the write with a 422;
  `after_create`/`after_update` are **signed webhooks** (HMAC, retries,
  HTTPS-only + SSRF-guarded — they will refuse localhost/private IPs in every
  environment). There are no delete hooks.
- **Events:** a resource with `"events": ["create","update","delete"]` writes
  a lean event to a **transactional outbox in the same transaction** as the
  write — if the write rolls back, the event never existed. A separate worker
  binary consumes it (at-least-once, `SKIP LOCKED`); shipped consumers include
  XLSX processing and transactional email.
- **Atomic multi-resource writes:** `POST /api/transaction` runs up to 100
  create/update/delete ops in **one** Postgres transaction — each op
  authorized and validated exactly like its single-op counterpart, with
  optional compare-and-set `guard`s for race-safe decrements. All-or-nothing;
  the error names the failing op's index.
- **Files:** a content-addressable, tenant-isolated store (`POST /api/files`,
  local disk or any S3-compatible backend) with OWASP upload validation
  (extension allowlist + magic-byte sniff — the client's Content-Type is never
  trusted). A **`file` field** attaches an upload to a record with a real FK
  (bad id → 422; deleting an attached file → 409 or `set_null`), and can
  declare a per-field `accept`/`max_bytes` policy enforced at attach time.

---

## 5. The 10 %: your own Go, in-process

When the schema can't express it — a checkout that reserves stock, a webhook
that must verify a signature, a computed endpoint — you write a normal Go
`main` that imports the engine as a library, registers routes, and starts it.
`appximo backend-spec` is the complete contract; this is the shape:

```go
app, err := appximo.New(appximo.Config{Port: 8080, SchemaPath: "schema.json"})
app.Register(appximo.Route{
    Method: "POST", Path: "/api/checkout",
    Handler: func(ctx *appximo.Ctx) error {
        // ctx.Claims()  — the verified JWT identity
        // ctx.Tenant()  — resolved from the Host
        // ctx.Tx()      — a pgx transaction ALREADY scoped to this tenant's schema
        // ctx.Query / Get / Insert / Update — RBAC re-evaluated, state machines enforced
        // ctx.Enqueue   — an outbox job, atomic with your writes
        return ctx.JSON(200, result)
    },
})
err = app.Start()
```

### Why in-process is the differentiating half

Your handler runs **inside the same transaction** as the generated CRUD, with
the same RBAC applied, and zero network hops. The checkout that locks stock
`FOR UPDATE`, writes the order, its lines and a payment intent **commits or
rolls back as one unit** — the thing that, on a BaaS with edge functions,
becomes a distributed-consistency problem in your lap. The commerce field
report measured the consequence: 20 concurrent checkouts for the last unit →
exactly one wins, every time.

### 5.2 The module, honestly (read before `go mod init`)

**The Go module is not published yet.** `go get github.com/appximo/appximo`
fails — there is no public repo and no tag. The recipe that works **today** is
a local checkout plus a `replace`:

```
require github.com/appximo/appximo v0.0.0
replace github.com/appximo/appximo => /path/to/your/appximo-checkout
```

This builds on the machine that holds the checkout and **nowhere else** — not
in CI, not in a plain `docker build`. It is the single hardest wall left in
the third-party journey, it is a publishing decision rather than a code gap,
and it is tracked (backlog DOC-2). **The day the module is published, the
recipe collapses to `go get …@v1.0.0` and the `replace` line is deleted** —
every example in `backend-spec` is already written against the final import
path, so nothing else changes.

### 5.3 The safety rules the engine will hold you to

`backend-spec` Phase 0, learned from real incidents, enforced by review:

- **Never a bare `go` statement** — always `ctx.SafeGo` (panics in your
  goroutine would otherwise kill the process; SafeGo recovers and logs).
- **External side effects go after commit** — or better, through `ctx.Enqueue`
  and the outbox worker, so "the payment email exists iff the payment row
  does".
- The tenant transaction is a **single, non-concurrent connection** — don't
  fan out queries on it in parallel.
- Public routes (`Route.Public`) get their own conservative rate limit
  (5 rps/IP default — right for a signup endpoint, wrong for a catalogue;
  declare `Route.RateLimit` per route to change it). A presented-but-invalid
  token on a public route is a 401, never a silent downgrade to anonymous.

### 5.4 Custom routes and RBAC — the two-layer rule

A custom route is authorized by its first `/api/` segment as a **virtual
resource**, granted in the schema's `routes` block:

```json
"member": {
  "permissions": { "bookings": { "actions": ["read","create","update"],
      "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } } },
  "routes": { "confirmar": { "actions": ["create"] } }
}
```

The route grant opens **the endpoint**; the data your handler touches is
authorized **again** against the real resources (`ctx.Update` on `bookings`
needs `update` on `bookings`). This is the single most valuable thing to know
before writing a handler — it turns a would-be "mystery 403" into a design
decision you make up front (the §3 agent called the paragraph that warns about
it the most valuable line in the spec). Two consequences: a role's row
condition follows its data everywhere (your handler cannot leak another
user's rows by accident), and **a schema that grants `routes` refuses to boot
on the pure `serve` binary** — fail-closed, because nothing registers those
endpoints there. Custom routes appear in the served `/openapi.json`
(method, path, auth mode, required grant); their request/response *shapes* are
yours to document — the convention is a contract sheet in your repo
(`backend-spec` §3.6b shows the format).

In production, a consumer binary deploys through the same official path as the
engine (§7) by honoring a 5-line contract
([ADR-023](adr/ADR-023-deployable-binary-contract.md)): `appximo.ParseServeArgs`
implements it. A consumer deploy is **two artifacts** — your app binary
(serves) and the engine CLI (operates: tenants, migrations, tokens,
super-admin). `install.sh --cli=PATH` places it.

---

## 6. The frontend: one binary serves it

The engine serves your SPA from the same binary and origin — no second
deploy, no CORS, no separate web server:

```go
appximo.Config{ Static: []appximo.StaticMount{{ Path: "/", Dir: frontendFS }} }
```

`appximo frontend-spec` is the complete guide (stack recommendation with its
argument, the exact API contract a UI consumes, the error→screen-state table,
files/images, the browser-only traps). The load-bearing facts:

- **Recommended stack: SvelteKit + `adapter-static` as a pure SPA
  (`ssr=false`)** — SSR would break the one-binary model. The honest
  criterion: it is what a cheap AI writes correctly on the first try. A
  no-build vanilla SPA works too (`examples/frontend-guide/` is one, verified
  6/6 in a real mobile browser).
- **Static mounts own their CSP.** The default policy allows a same-origin
  SPA including the inline bootstrap script mainstream bundlers emit
  (`'unsafe-inline'` on script-src — permissive **by necessity**: the first
  strict draft blanked a real SvelteKit app in every browser). Tighten it per
  mount (`StaticMount.CSP`) if your bundle has no inline scripts.
- **CSP failures are invisible to curl.** A blank page with a perfect 200 is
  a browser-only failure mode — test with a browser (Playwright), and use
  selector waits, not `waitForFunction` (the CSP correctly has no
  `unsafe-eval`).
- **The empty-string trap:** HTML forms submit `""`, and `required` accepts
  an empty string (it checks presence, not content). Declare `minLength: 1`
  on fields a form must actually fill.
- **Images/files:** authed screens use short-lived signed URLs
  (`GET /api/files/{id}/url` — an `<img>` cannot send an Authorization
  header); a *public* image is a custom `Route.ByteServing` + `ctx.ServeFile`
  that authorizes by relationship (e.g. "this product is published") and
  streams with Range/ETag/sendfile.

The proof this composes: **https://tiendita.appximo.com** — storefront +
mobile back-office + Studio + admin panel + docs, one binary on a $16 VPS,
and the §3 experiment's agent shipped a working mobile frontend against these
docs without ever seeing the engine's source.

---

## 7. To production: a $7–16 VPS with real HTTPS

The official path is **native, not Docker**: one command on an empty Ubuntu
VPS installs PostgreSQL (tuned to the box), the binary under systemd
(hardened unit, `GOMEMLIMIT` set), Caddy as the TLS proxy, and a real
Let's Encrypt certificate:

```bash
sudo bash scripts/install.sh --domain=api.example.com --email=you@example.com
# (add --binary=./appximo to deploy a binary you built yourself — e.g. a
#  framework-mode consumer app; without it the installer downloads the
#  v0.1.1 release asset and verifies its checksum)
```

Box-to-HTTPS was walked end-to-end on a real DigitalOcean droplet, twice (the
engine, then a consumer binary): **~3 minutes**, first try. The summary prints
where the generated secrets live (`/etc/appximo/appximo.env`, 0600 — not
to stdout), the *detected* control-plane port, and the register-tenant
command.

What you must know that the installer cannot decide for you:

1. **DNS before TLS.** The domain must resolve to the box (an A record) before
   Caddy can obtain the certificate.
2. **Tenant = the domain's first label** (§2.4). A tenant `acme` on
   `example.com` is served at `acme.example.com` — one Caddy site per public
   tenant domain, or a wildcard. The installer writes the first site; more
   tenants on subdomains of the same apex are a Caddyfile line each.
3. **The control plane stays inside.** `:9090` is never proxied and never
   opened in the firewall. Registering tenants happens on the box (or over
   SSH).
4. **A second app on the same box:** `install.sh --app=NAME` namespaces
   everything (unit, service user, `/etc`, `/opt`, `/var/lib`, database,
   control port, its own Caddy site file). Verified staged side-by-side; the
   one honest caveat: it has **not yet run against a real multi-app box**
   (backlog OPS-11), so treat the first such install as a drill, with a
   snapshot.
5. **Updates:** `deploy-update.sh --binary=…` — atomic swap, health poll every
   250 ms, **auto-rollback** if the new binary fails its health check.
   Measured on the live shop under load (2026-07-31): a normal deploy costs
   **~0.3–0.6 s of 502s** (0.58 s at 50 rps; 0.28 s after the same day's
   tooling pass tightened the health polling), and a deliberately broken
   binary rolled itself back with **zero human help** (~17 s to verified
   recovery). This is honest sub-second-blip deployment, **not** zero-downtime
   (ENG-2).
6. **Backups:** `backup.sh` dumps per-app; schedule it (the runbook's
   `appximo-backup.timer` at 03:30 — the installer does not create it yet,
   ENG-3), and **rehearse restore**: `restore.sh` exists and the drill was
   executed on the live shop — schema dropped with the catalogue answering
   500, **restored in 1.8 s**, row counts identical, a new purchase completed.
   An unrehearsed backup is a hope, not a backup.

### What the box actually does under stress — measured, consumer app, 2026-07-31

Faults injected over SSH against the live storefront at 20 rps (the suite is
re-runnable, §10):

| Fault | User-visible outage | Recovered alone |
|---|---|---|
| SIGKILL the engine | 3.7 s of 502s | yes — systemd |
| SIGKILL Caddy | 0.95 s | yes — `Restart=` drop-in (this test *found* the missing `Restart=`; it ships fixed) |
| PostgreSQL stopped 15 s | 8.9 s of errors, engine survives, **0 restarts** | yes — pool reconnects |
| Connection-pool burst | zero failed requests | graceful |
| Full reboot | **38 s** to everything back — services, cert, data | yes |

Performance of a **real** consumer app on the $16 box (k6 from another
machine, over the internet, HTTPS): public catalogue (custom SQL, uncached)
**p50 6.6 ms / p95 18 ms at 150 rps**; generated CRUD with cache **p50
3.1 ms**; zero errors in every run; the whole stack (engine + Postgres +
Caddy) at **~186 MiB PSS under load** — the box is oversized for a boutique
workload. Engine-only reference numbers are in §9's benchmark note and
[BENCHMARKS.md](BENCHMARKS.md).

---

## 8. Operating it: migrations, backups, observability

### Changing a live schema — the model in one paragraph

Deploy a schema change through Studio, `PUT /admin/tenants/{id}/schema`, or
`appximo migrate` — all three run the same engine: **introspect the live
database → diff → apply through a production-safe executor** (lock timeouts +
retry, `NOT VALID`/`VALIDATE` FKs, `CONCURRENTLY` indexes, data-preserving
renames via `renamed_from`). The policy is **additive by default: it never
drops anything on its own** — a removed field's column stays as logged drift.
After every apply the engine **re-introspects and re-diffs**: anything
declared but not actually in the database makes the apply a *failure* in every
surface (CLI exits non-zero, the deploy answers 422 and restores the previous
schema). The migrator never grades its own work.

**What's hot and what needs a restart:** after a deploy, new *fields* are
readable AND writable immediately on REST (the write path validates against
the deployed schema merged with the boot one; GraphQL *mutations* of the new
field still wait for the restart — input types are boot-compiled). A new
*resource* — new routes, GraphQL types, docs — activates on restart, which is
**one click in Studio**:
the deploy result offers "Restart engine now" (validate → persist atomically
with a `.bak` → drain → re-exec, ~6 s, auto-restore if the new schema can't
load). Until then, calls to the new resource get an explanatory
`resource_not_loaded`, not a bare 404.

### Destructive changes — the approval gate ritual

Actually removing a column or table is a two-step, informed-consent flow:

```bash
appximo migrate --tenant acme --schema new.json --dry-run
# → the classified plan: safe ops, and each data-losing drop with its impact:
#   "empleados.telefono — rows_lost: 1,240 of 1,240"
appximo migrate --tenant acme --schema new.json --approve-drops "empleados.telefono"
# → ONLY the enumerated keys drop; everything else stays gated
```

There is no "drop everything" flag; a drop that *appeared* since the dry-run
carries an unapproved key and is gated automatically. The same flow fans out
to all tenants (`--all-tenants`) — sequential, per-tenant advisory locks,
resilient to partial failure (a broken tenant is logged and the rest
continue) and resumable (re-running is a no-op for converged tenants). Every
persisted schema lands in an append-only **version history** with
dry-run-gated rollback, and per-tenant **flow tests** (multi-step
request/assert scripts stored as data) can re-run as a post-deploy regression,
streaming PASS/FAIL per step.

### Observability — in the binary, no sidecar

`/metrics` (Prometheus), `/debug/traces` (per-request spans in µs:
jwt→rbac→query→serialize), `/debug/tenant/{id}` (p50/p95, grouped errors,
recent traces), SLO burn-rate with multi-window thresholds, per-tenant latency
anomaly detection (EWMA z-score) — all admin-gated, all visual in the
`/admin` panel's Observability screen (charts, span waterfalls, issues). The
snapshot history survives restarts (SQLite store). Health probes `/healthz`
(liveness), `/readyz` (flips 503 while draining), `/health` (version = the
build SHA when built by the official scripts).

### The defaults that will surprise you (by design)

- **Per-tenant rate limit: 1000 rps / 100 burst.** The first ceiling a
  single-tenant load test hits — well before CPU. Raise with
  `RATE_LIMIT_RPS`/`RATE_LIMIT_BURST` (the flagship benchmark declares
  exactly that).
- **Suspension, not revocation:** JWTs are stateless; suspending a user or
  tenant blocks new logins, already-issued tokens live to their `exp`.
- Response cache: GETs are served from RAM per tenant; every write path
  invalidates it. A "stale read" almost always means you expected an
  uncached surface (SSE and file downloads bypass it).

---

## 9. What it does NOT do

The honest list, current as of 2026-08-02 — each with where it is tracked.
If your project needs one of these today, factor that in *now*:

**Scale & availability**

- **No HA, no clustering, single node** — scaling is vertical; past the
  tenant range (tens to low hundreds healthy; Postgres catalog degrades
  ~1,000–2,000 schemas), the answer is another instance.
- **No zero-downtime binary upgrade** — a deploy costs ~0.3–0.6 s of 502s,
  measured; auto-rollback exists (ENG-2).

**Distribution — the honest state of "today"**

- **Released: v0.1.1** — binaries for linux/darwin (amd64/arm64) with
  checksums on GitHub Releases; the installer's download path is enabled.
- **The Go module is fetchable** — `go get github.com/appximo/appximo@v0.1.1`
  works from the public proxy (verified live 2026-08-05), so framework mode
  (§5) builds on any machine and in CI
  (DOC-2). The docs are already written for the published state.
- **Self-hosted only. No SaaS.** By design.

**Declarative surface (each deliberate, with its ADR)**

- No filtering by NULL (SCHEMA-6, ADR-022) — the 400 is honest but it is a
  dead end today.
- No `neq`/`in`/`nin`/`like`/`ilike` filter operators; no multi-field sort.
- No partial indexes in the schema (Postgres normalizes predicates → diff
  churn; put them in your own boot DDL, ADR-022).
- No `decimal`/`money` type (deliberate — int64 minor units, §4).
- No delete hooks; `workflows` parses but has no executor; no per-transition
  RBAC in the schema (the custom-route pattern covers it, ADR-021/022).
- Aggregation is exactly `count/sum/avg/min/max` + `group_by`.

**Operations**

- **No engine `restore` command** — `backup.sh`/`restore.sh` are scripts and
  the restore drill is proven (1.8 s), but it's a runbook, not a subcommand;
  the installer creates no backup timer yet (ENG-3).
- `install.sh --app` (second app on one box) has never run against a real
  multi-app server — staged verification only (OPS-11).
- The platform super-admin is created from a terminal only (`appximo admin
  create`).
- No OTLP/OpenTelemetry export (Prometheus + internal traces only, ENG-4).
- Webhooks refuse plain HTTP and private/loopback addresses — always
  (SSRF guard); test against a public HTTPS receiver.

**Benchmarks — what may NOT be claimed**

- The engine's flagship figure — **p50 1.60 ms at 2,000 rps sustained, 0
  errors in 597,461 requests** on a 2-vCPU $16 droplet (re-measured
  2026-08-01) — holds **only with the per-tenant limiter raised**
  (`RATE_LIMIT_RPS=3000`), as the benchmark doc declares; on defaults, ~half
  of a single-tenant 2,000 rps load is 429 by design. At 500 rps: p50
  1.53 ms; with the response cache fully bypassed (every request reaching
  Postgres): 2.44 ms. A filtered, sorted page over **1 M rows: ~3 ms**
  engine-side, ~4.2 ms through Caddy+TLS.
- **No comparative claims against other frameworks.** A NestJS comparison
  existed (2026-06-10) and its conditions are gone; it was deliberately NOT
  re-verified and is not cited here (OPS-12).

---

## 10. Verify everything yourself

The project's trust argument is not this guide — it is that every claim above
is re-checkable on your own hardware with tools that ship in the repo:

| What | How | What it proves |
|---|---|---|
| The zero-to-API path | §2, verbatim | the product's front door (this guide ran it on 2026-08-02; outputs above are real) |
| Your deploy, end to end | `scripts/verify-production/run-all.sh --target=https://your.domain --server-ssh=root@box` | TLS, headers, isolation, resilience — the same suite that certified the live demos |
| The whole API surface | `scripts/acceptance-test.sh` against a booted engine | CRUD, RBAC, validation, GraphQL, files — the generic layer is schema-aware |
| Performance, statistically | `scripts/bench-protocol.sh 10 my-label 2000 30s` (external loader; see BENCHMARKS.md §1 for the env) | warmup + N runs + bootstrap CIs; every run appends to `benchmarks/history.tsv` |
| Behavioral changes between two binaries | `scripts/binary-diff-gate.sh <base> <new>` — a 92-case paired-request corpus | the technique that has caught what green tests did not |
| The engine's own lanes | `make test` (unit, ~7 s) · `make test-all` (integration + e2e + resilience, Docker) | the same gates CI runs |

And the standing proof: two production apps built on the engine are live —
**[tiendita.appximo.com](https://tiendita.appximo.com)** (the commerce
storefront + back-office; its buy path is exercised by the regression suites)
and **[petfriendly.appximo.com](https://petfriendly.appximo.com)** (the
veterinary app whose schema was AI-generated from one paragraph of plain Spanish, §3).
Both report their build SHA at `/health`.

Where this guide's claims come from, if you want the receipts:
[CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md) (the measured
claims and their conditions) · [CAPABILITIES.md](CAPABILITIES.md) (the
one-line inventory + the not-yet list) · [BENCHMARKS.md](BENCHMARKS.md) (the
production-stack numbers) · [BACKLOG.md](BACKLOG.md) (every open limit, with
an ID) · the field journeys this guide was distilled from:
[AUTHORING_JOURNEY.md](AUTHORING_JOURNEY.md) and the commerce field report
(parts one–five).
