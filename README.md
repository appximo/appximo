# Appximo

> **A JSON schema in. A production multi-tenant REST + GraphQL + OpenAPI server out.**
> One ~64 MB static Go binary, on your own server. Apache 2.0.

[![CI](https://github.com/appximo/appximo/actions/workflows/ci.yml/badge.svg)](https://github.com/appximo/appximo/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/appximo/appximo?label=release)](https://github.com/appximo/appximo/releases)
[![Docker](https://img.shields.io/docker/v/neodevtrix/appximo?label=docker&color=2496ED&logo=docker)](https://hub.docker.com/r/neodevtrix/appximo)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue)](LICENSE)

You don't write handlers, models, or migrations. You write this:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true },
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

and the engine serves — per isolated tenant, from one process:

- `GET/POST/PUT/PATCH/DELETE /api/tasks` with typed filters, sort, keyset pagination
- a GraphQL endpoint with the same data (`{ tasks { data { id title } } }`)
- an OpenAPI 3.0 spec (`appximo openapi schema.json`)
- declarative validation (`422` listing **every** invalid field at once)
- live updates over SSE (`GET /api/tasks/events`)
- JWT auth + RBAC enforced on every request, deny by default — that `viewer`
  role really is read-only and never sees the `due` field

It's a code generator without the generated code: the schema is compiled at boot,
not scaffolded into files you then maintain.

## Quick start (~30 s with the image pull)

> **New here?** The full first-mile walkthrough — install (Linux/macOS/Windows),
> schema, first call, first user, custom Go, frontend, production with HTTPS,
> backup — with a manual track AND an AI-agent track for every step, is
> **[docs/QUICKSTART.md](docs/QUICKSTART.md)**.

> ⚠ The `neodevtrix/appximo` Docker image publishes automatically on every
> green CI run of `main` — if the pull fails, the image simply hasn't landed
> yet. From a clone, the two files are at the repo root — skip the downloads.
> No Docker? Build from source: `make install` (Go 1.25), then
> `appximo serve --schema examples/quickstart/schema.json`.

```bash
mkdir appximo && cd appximo
curl -O https://raw.githubusercontent.com/appximo/appximo/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/appximo/appximo/main/.env.example
cp .env.example .env          # set JWT_SECRET (≥32 chars), ADMIN_KEY, DB_PASSWORD
docker compose up -d          # multi-arch image (amd64+arm64), ~22 MB pull
curl localhost:8080/health    # {"status":"ok",...}
```

On our test box, `up -d` to healthy takes **~9 s** plus the image pull. The image
boots with **exactly the schema shown above**, so your first request is four
copy-paste commands against it (verified on a clean machine, virgin DB):

```bash
set -a; source .env; set +a

# 1. register a tenant (its isolated Postgres schema + tables are created now)
curl -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme\",\"email\":\"a@acme.com\",\"plan\":\"free\",\"schema\":$(docker compose exec engine cat /etc/appximo/schema.json)}"

# 2. mint a JWT for the "admin" role the schema defines (helper ships in the image)
TOKEN=$(docker compose exec engine appximo token --secret "$JWT_SECRET" --tenant acme --role admin 2>/dev/null | tail -1)

# 3. write a task
curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost" -H "Content-Type: application/json" \
  -d '{"title":"ship the launch","status":"open"}'

# 4. read it back, filtered (curl -g: brackets need globbing off)
curl -g "http://localhost:8080/api/tasks?filter[status][eq]=open&per_page=20" \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost"
```

To serve **your own** model, mount your schema over `/etc/appximo/schema.json`
(or `--schema yours.json` on the binary). Tenants are addressed by Host
subdomain: `acme.localhost` → Postgres schema `tenant_acme`.

## Building with an AI agent? Start with three commands

The engine prints its own agent-facing contract — no repo access needed:

```bash
appximo spec           # 1. the schema grammar (the declarative 90% of an app)
appximo backend-spec   # 2. custom Go handlers, hooks, auth, background jobs
appximo frontend-spec  # 3. the API contract a UI consumes, errors→screens, files
appximo specs          # …or all three at once (one paste = the whole contract)
```

Paste them into your own Claude Code / Cursor **in that order** (schema →
backend → frontend), describe your app, and the agent generates against the
real grammar, self-correcting with `appximo validate --json <schema>` as the
oracle. A running app additionally serves its complete live surface —
generated **and** custom routes — at `/openapi.json` (interactive at `/docs`).
The long-form docs behind each command:
[SCHEMA_SPEC_LLM](docs/SCHEMA_SPEC_LLM.md) ·
[BACKEND_SPEC_LLM](docs/BACKEND_SPEC_LLM.md) ·
[FRONTEND_SPEC_LLM](docs/FRONTEND_SPEC_LLM.md).

## See it live

Two real apps — schema + custom Go logic + embedded frontend, each one binary on
a $16/mo VPS — run in production today:

- **[tiendita.appximo.com](https://tiendita.appximo.com)** — a commerce
  storefront (catalog, cart, checkout with an atomic stock transaction, order
  tracking, image uploads).
- **[petfriendly.appximo.com](https://petfriendly.appximo.com)** — a
  pet-services app born from the AI authoring flow.

(The demo domains still carry the project's pre-rename name; they move to
`appximo.com` next.)

## Where it sits

Honest comparison — these are different tools that overlap on "I need an API":

| | **Appximo** | NestJS / Express / Rails | Supabase | PocketBase |
|---|---|---|---|---|
| You write | a JSON schema | application code | SQL + RLS policies + client code | collections config + Go/JS hooks |
| API surface | REST + GraphQL + OpenAPI, generated | whatever you build | PostgREST + client SDKs | REST + realtime |
| Multi-tenancy | first-class: schema-per-tenant isolation, subdomain routing | you build it | you build it (RLS) | one DB per app |
| Database | your PostgreSQL | any | bundled Postgres (its platform) | embedded SQLite |
| Runtime | one static Go binary, ~24 MB RSS idle | Node/Ruby + deps | a service fleet (or their cloud) | one Go binary |
| Custom logic | **Go in-process** (framework mode: `appximo.Route` + `Ctx`, same process & transaction) + sandboxed JS/WASM hooks | unlimited (it's your code) | edge functions, triggers | Go/JS hooks |

What they do **better**: frameworks give you unlimited logic with no ceremony —
Appximo's custom logic is a bounded framework surface (in-process Go routes
sharing the engine's transaction and RBAC, plus sandboxed hooks), not an
anything-goes app server. Supabase has auth providers, storage, realtime
channels and a massive ecosystem. PocketBase is even simpler to run (no Postgres
needed). Appximo's lane is: **several isolated tenants on one cheap box,
talking to a Postgres you control, with the API contract generated and enforced
from a schema file — plus the custom 10 % as Go in the same process and
transaction.**

## Performance

On a $16/mo 2-vCPU droplet with JWT + RBAC + multi-tenancy + validation + rate
limiting all active, measured from an external load generator over a real network:
**2,000 req/s sustained, p50 1.60 ms (CI95 [1.57, 1.67]), 0 errors in 597k requests**
— re-measured 2026-08-01 on the same hardware and reproducing the original figure
(1.58 ms [1.52, 1.62], 2026-06-10). With the response cache **fully bypassed**, every
request reaching PostgreSQL: **p50 2.44 ms at 500 req/s**.

> **Read this before reproducing it.** 2,000 req/s against a *single tenant* needs
> the per-tenant rate limiter raised — `RATE_LIMIT_RPS=3000 RATE_LIMIT_BURST=300`,
> the configuration the benchmark declares. On the **defaults (1000 rps / 100
> burst)** roughly half of a 2,000 rps single-tenant load is answered `429`, by
> design. The limit is per tenant, so real multi-tenant traffic is not affected the
> same way.

Full methodology — every limitation, the cache asymmetry, the statistical
treatment (Mann-Whitney, bootstrap CIs), and raw per-run data — ships in the
repo; reproduce it on your own hardware with `make bench-protocol`
([`scripts/bench-protocol.sh`](scripts/bench-protocol.sh): warmup + N runs +
a statistical verdict, not a one-shot number).

That benchmark measures the **engine**. For the **whole production stack** —
Caddy terminating real Let's Encrypt TLS → the engine under systemd → native
PostgreSQL, with a million rows — see [**docs/BENCHMARKS.md**](docs/BENCHMARKS.md):
the production layers cost about **+1.2 ms** p50 (re-measured 2026-08-01), the
box sustains **500 req/s** with every request reaching PostgreSQL (knee at 750,
2026-07), a filtered page over 1M rows answers in **~4.2 ms** end-to-end
(re-measured 2026-08-01), a real consumer app's whole stack runs at
**~186 MiB PSS under load** (2026-07-31; the original ~109 MiB idle figure
predates the box serving two apps and is kept in BENCHMARKS with its date), and
the resilience matrix (kill the engine, kill Caddy, stop PostgreSQL, deploy
under load, reboot) is measured rather than asserted. Better still, **don't
take those numbers**: [`scripts/verify-production/`](scripts/verify-production/)
runs the same suite against *your* server and prints *your* report.

## What's in the box (all verified by the test suite)

- **CRUD**: list/get/create/replace/patch/delete per resource; typed filters
  (`eq` everywhere; `partial`/`start` on strings; `gt/gte/lt/lte` on numbers and
  time; `after`/`before` on time), single-field sort (`?sort=created_at&order=desc`),
  keyset pagination (`?after=<uuid>`, no OFFSET)
- **Aggregation**: `count`/`sum`/`avg`/`min`/`max` + `group_by` per resource
  (`GET /api/{resource}/aggregate` and `<resource>Aggregate` in GraphQL), plus
  opt-in `?count=true` total on lists — all scoped by the SAME RBAC row condition,
  field allowlist and filters as a read (a row-scoped role aggregates only its own
  rows; a hidden field can't be summed)
- **Atomic transactions**: `POST /api/transaction` runs many create/update/delete
  ops across resources in **one Postgres transaction** — all-or-nothing (a transfer,
  a checkout). Every op is authorized (per-resource RBAC) and validated like its
  single-op counterpart, outbox events emit in the same tx, and an optimistic-lock
  `guard` (compare-and-set) gives race-safe conditional writes; failures name the
  offending operation. The single-op write path is unchanged
- **State machines**: a status field can declare its `state_machine` (initial
  state(s) + allowed transitions); the engine forces the lifecycle — create only in
  an initial state, update only along a declared transition, a terminal state is
  immutable (append-only). Enforced race-safely in the UPDATE itself, on REST,
  GraphQL, and inside a transaction
- **Queryable documents**: a `jsonb` field is a real Postgres document — declare
  `{"fields":["attributes"],"method":"gin","opclass":"jsonb_path_ops"}` and
  containment (`attributes @> {...}`) is an index lookup, not a sequential scan.
  Merchant-defined attributes without EAV, and without hand-written DDL
- **Declarative validation**: required/enum/type rules compiled from the schema;
  one `422` lists every failing field; field `default` values applied on insert
  (literals + `"now"` for time); a `unique`/composite-unique collision is a clean
  `409 Conflict` (REST create & update + GraphQL), never a raw DB error
- **Declarative relations**: `has_many` / `belongs_to` / `many_to_many` served
  nested in one round-trip (`json_agg` + `LATERAL`, no N+1) on opt-in `?include=`,
  RBAC compiled into the SQL; FK columns auto-indexed
- **Referential integrity (complete FK coverage)**: a field with `relation` creates
  a **real foreign key** with declarative `on_delete` **and `on_update`**
  (`restrict`/`cascade`/`set_null` — a blocked delete is a clean `409`, never a silent
  orphan), pointing at the target's `id` or a `unique` non-id column (`references`);
  genuine **composite** multi-column FKs are a resource-level `foreign_keys` block.
  All applied safely (`NOT VALID`/`VALIDATE`, no long lock) and enforced on REST +
  GraphQL — adding `on_update` to an existing schema causes zero migration churn
- **Multi-tenancy**: schema-per-tenant Postgres isolation (`SET LOCAL search_path`),
  subdomain → tenant routing, per-tenant rate limiting. One API structure for all
  tenants, compiled at boot: per-tenant migrations apply **live** (a new column is
  readable/writable immediately — the DB is the source of truth for write keys),
  while everything derived from the compiled definition — validation rules,
  filters, GraphQL fields, `/docs`, and **new resources** — activates on a
  **graceful self-restart** (`POST /admin/engine/schema`, super-admin-gated:
  validated + atomic boot-schema persist with a `.bak` rollback, drain via
  `/readyz`→503, re-exec — one click from the visual editor, ~6 s, no terminal;
  see [docs/MENTAL_MODEL.md](docs/MENTAL_MODEL.md))
- **RBAC**: JSON policies — per role, per resource, per action, per field, plus
  dynamic row conditions (`operator_id = $user_id`); deny by default. Field
  allowlists and row conditions are enforced on **create** as well as
  read/update/delete — an owner-scoped role can only create rows attributed to
  itself (no mass-assignment), on both REST and GraphQL. Row conditions are
  equality (`field = $user_id`), validated at load (a condition can't declare an
  operator the engine wouldn't apply), and enforced uniformly on **every** read
  path — including `?include=` embeds and the relation read subroute
  (`GET /api/{res}/{id}/{rel}` scopes the *referenced* resource, not just the
  parent). Conditions can be
  **role-global** or **per-resource** (a `permissions` map): one role can scope
  each resource by its **own** column (`projects.owner_id`, `documents.created_by`),
  leave some resources unscoped, and even "read all, write own" via
  `condition_actions` — unlocking workspace/owner scoping that role-global
  conditions couldn't express. A `routes` block grants the **custom endpoints** a
  Go backend registers (a virtual segment, boot-validated against the routes that
  actually exist), so "owner-scoped end users **plus** a custom action like
  checkout" is expressible at last
- **Auth (password identity)**: in-engine signup/login/refresh (`POST /auth/*`),
  multi-tenant-aware — users live in the tenant's own schema, so **email is unique
  per tenant, not globally** (the same email is a distinct account in two tenants).
  argon2id hashing, anti-enumeration, per-identity login throttling; the issued
  JWT is the same one the engine validates. Public signup is opt-in + role-gated.
  **Password reset + email verification** via single-use tokens, delivered async
  through the outbox + email worker (the email never blocks the request).
  **Social login** (OAuth2: Google / GitHub / Microsoft) — tenant carried in a
  signed state, identity linked by stable provider id, no new dependency.
  **TOTP MFA** (opt-in, RFC 6238) — encrypted secret, one-time backup codes,
  two-step login; the secret never password-degrades the request hot path
- **Admin panel**: a **platform super-admin** (in a system schema, above all
  tenants, with its own login + MFA) plus a consolidated `/admin/*` API to manage
  tenants, their users, and their observability — inheriting the schema RBAC +
  tenant isolation (not a second permission system); the `X-Admin-Key` still works
  for machine callers. Bootstrap with `appximo admin create`. A **SolidJS UI is
  embedded in the binary and served at `/admin`** — login + MFA, tenant management,
  per-tenant user management, read-only data navigation, and an **observability
  dashboard** (ECharts latency + SLO burn-rate charts, a trace-span waterfall, and
  the z-score anomaly + error views) (built with `make admin-ui` before `go build`)
- **Real-time**: per-resource SSE streams with RBAC applied at delivery
- **Webhooks**: HMAC-SHA256-signed, async, retries with backoff, SSRF-guarded
- **Extensions**: JS sandbox (Goja, watchdog-interrupted) with built-in helpers —
  including Colombian DIAN tax compliance (CUFE SHA-384, NIT mod-11) — plus a WASM
  runtime (Wazero, no CGO)
- **GraphQL**: queries (with nested relation embeds) + create/update/delete
  mutations, selection-count limits (alias-amplification guard), introspection
  off in production by default. **GraphiQL** — the visual schema explorer —
  is served at `/graphiql` (autocomplete, run queries/mutations, a Headers
  editor for testing with a real token) whenever introspection is allowed:
  dev, or the explicit `APPXIMO_GRAPHQL_PLAYGROUND=on` opt-in for
  production, per-app in the fleet
- **API contract, served**: an OpenAPI 3.0 spec generated from the schema —
  including the auth + file-store endpoints — served at `/openapi.json` (and
  `/openapi.yaml`), with **Swagger UI at `/docs`** for interactive exploration
- **CORS**: configurable cross-origin access for browser SPAs on another origin
  (`APPXIMO_CORS_ORIGINS`), disabled by default, scoped to `/api`,`/auth`,
  `/graphql`,`/openapi` — never the control plane or `/admin`
- **Safe schema evolution**: a real diff-based migration engine — renames preserve
  data, NOT NULL is enforced faithfully, all DDL runs under `lock_timeout`+retry with
  `CONCURRENTLY` indexes. Additive by default (it never drops); a **destructive drop**
  (removing a field/resource) needs a two-step **approval gate** — a dry-run reports
  exactly what would be lost (rows affected), and the drop runs only when you enumerate
  it explicitly. No accidental data loss; automated workers never auto-approve. Rolling a
  change out to **every tenant** is a **resumable fan-out** (`migrate --all-tenants`):
  one tenant at a time under its advisory lock, resilient to partial failure (a broken
  tenant is recorded and the rest continue) and resumable (re-run skips the converged
  ones, retries the failed) — the multi-tenant migration story Prisma and django-tenants
  don't have. Every deployed schema is recorded in an **append-only version history**,
  and **rollback to any prior version** re-deploys it through the same gate: a dry-run
  shows exactly what reverting destroys (measured rows lost), nothing drops without
  enumeration, and the rollback itself becomes a new version — browsable timeline +
  rollback UI in the visual editor ("History"). And the trust loop closes with
  **persisted flow tests**: multi-step scenarios (login as a role → create → attach →
  assert, state chained between steps) run server-side against the live app with live
  PASS/FAIL output — re-run after a deploy as a **regression suite whose verdict is
  anchored to the schema version** it ran against ("Flows" in the visual editor)
- **Ops**: Prometheus `/metrics`, per-request trace ring with stage breakdown,
  SLO burn-rate alerts (Slack), graceful drain on SIGTERM, circuit breaker
  (verified open/recover with toxiproxy), zero-downtime additive migrations —
  the full observable surface (trace explorer, per-tenant debug, health
  probes) is mapped in [docs/EXPLORE.md](docs/EXPLORE.md)
- **Security hardening**: HS256-pinned JWT (alg-confusion rejected), sanitized
  identifiers everywhere, masked DB errors, 1 MB body cap, fuzzed parsers
  (0 crashers), per-tenant cache isolation

Full capability inventory (and the honest not-yet list): [docs/CAPABILITIES.md](docs/CAPABILITIES.md).

## Production deploy

**The official path — an empty VPS to a live HTTPS API in one command**
([docs/PRODUCTION.md](docs/PRODUCTION.md)). The stack is native PostgreSQL + the
engine under systemd + Caddy (automatic Let's Encrypt TLS); Docker would eat
300–400 MB on a 1 GB box, so it's a documented variant, not the default.

```bash
curl -fsSL https://raw.githubusercontent.com/appximo/appximo/main/scripts/install.sh \
  | sudo bash -s -- --domain api.example.com --email you@example.com
```

The installer asks one thing — your domain — then generates every secret, writes
the systemd unit + Caddyfile, installs PostgreSQL and Caddy, and brings the API
up on HTTPS. Updates are one script ([`scripts/deploy-update.sh`](scripts/deploy-update.sh),
atomic swap + auto-rollback), backups another
([`scripts/backup.sh`](scripts/backup.sh), `pg_dump` + rotation).

Prefer containers or a PaaS? The Docker paths still work:

| Goal | Path |
|---|---|
| **Try it** in ~9 s | `docker compose up` — the [quick start](#quick-start-30-s-with-the-image-pull) above |
| **Production, Docker** | `docker-compose.prod.yml` + Caddy: automatic Let's Encrypt TLS, engine and Postgres on the internal network only |
| **Production, native (max throughput)** | the installer above, or [docs/PRODUCTION.md](docs/PRODUCTION.md) / [docs/DEPLOY.md](docs/DEPLOY.md) Level 3 by hand — the configuration the benchmark measured |

Full guide — updates, backups, framework mode, serving your frontend, the
complete env-var table, security checklist, troubleshooting:
[**docs/PRODUCTION.md**](docs/PRODUCTION.md).

## Status — what's real and what's missing

**Production-ready and test-backed** (`make test-all`: unit + integration + E2E +
resilience against real Postgres in Docker): everything in the feature list above.
It serves our own production workload today.

**Known limits, honestly:**

- **Single node.** No HA/clustering story; scale is vertical (the benchmark shows
  how far one cheap box goes).
- Observability is Prometheus + an internal trace ring — **no OTLP export**.
- No hosted/SaaS version. Self-hosted only, by design, for now.

Beyond the API surface, the binary also embeds **Appximo Studio** at `/editor`
— a visual schema designer (full ERD over the schema grammar, visual RBAC,
state machines, relations, plus a **Code view**: the raw schema in an assisted
JSON editor with live engine validation, every error on its line) that
deploys/migrates tenants, restarts the engine in one click when a new resource
needs it, and manages tenant files — plus the admin panel at `/admin`. Both are
static SPAs compiled into the binary (`make editor-ui admin-ui` before
`go build`; the published image ships them).

And you don't have to write the schema by hand: `appximo ai-generate
"<description>"` turns a natural-language app description into a valid schema
(validator-guided loop, ~$0.006/schema), or — with **your own agent** (Claude
Code, Cursor) — `appximo spec` prints the LLM-distilled grammar so *your*
subscription generates it and self-corrects against `appximo validate --json`,
at zero API cost. The flow: [docs/SCHEMA_SPEC_LLM.md](docs/SCHEMA_SPEC_LLM.md).

And you don't have to stop at the schema. For the logic a schema can't express —
custom endpoints with the tenant transaction + RBAC in-process, hooks, auth
flows, background jobs — `appximo backend-spec` prints the **agent guide for
building a complete backend**: the decision framework for where each piece of
logic goes, the whole custom-handler surface with **compiling examples**, and the
safety rules that keep the in-process model robust (a panicking background
goroutine can never take the process down). Paste `spec` + `backend-spec` into
your agent and it can build the whole backend. The guide, with its runnable
example: [docs/BACKEND_SPEC_LLM.md](docs/BACKEND_SPEC_LLM.md) +
[examples/backend-guide/](examples/backend-guide/).

The trilogy closes with the part users touch: `appximo frontend-spec` prints
the **agent guide for building a production frontend** — where the frontend
lives (embedded in the same binary via `Config.Static` by default: one
artifact, same origin, no CORS), the recommended stack and why (SvelteKit +
`adapter-static` as a pure SPA — no Node at runtime), the exact API contract a
UI consumes, the error→screen-state mapping (the multi-field 422, the
work-preserving 409, the honest 503), the files/images pattern end to end
(upload with progress → attach via a `file` field → display, including PUBLIC
images through a byte-serving custom route), and the traps only a real browser
reveals. Distilled from a production storefront, with a runnable no-build
example: [docs/FRONTEND_SPEC_LLM.md](docs/FRONTEND_SPEC_LLM.md) +
[examples/frontend-guide/](examples/frontend-guide/). Give an agent all three
— `spec`, `backend-spec`, `frontend-spec` (or `appximo specs`, which prints
the trilogy in one stream) — and it can build the full stack.

## Configuration

| Setting | Kind | Required | Description |
|---------|------|----------|-------------|
| `DATABASE_URL` | env | **yes** | PostgreSQL connection string |
| `JWT_SECRET` | env | **yes** | HS256 signing secret (≥ 32 chars — **enforced**: the engine refuses to boot with a shorter one) |
| `ADMIN_KEY` | env | **yes** | `X-Admin-Key` for `/metrics`, `/debug`, `/admin`, control plane |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | env | no | per-tenant token bucket (default 1000/100) |
| `APPXIMO_MAX_TX_OPS` | env | no | max operations per `POST /api/transaction` (default 100) |
| `APPXIMO_FILES_BACKEND` | env | no | file-store storage: `local` (default, this box's disk) or `s3` (R2/Spaces/MinIO/AWS). See [docs/FILES.md](docs/FILES.md) |
| `APPXIMO_FILES_S3_*` | env | with `s3` | `BUCKET`,`ENDPOINT`,`REGION`,`ACCESS_KEY`,`SECRET_KEY`,`FORCE_PATH_STYLE`,`PREFIX`,`SERVE` — provider-agnostic S3 config |
| `APPXIMO_FILES_DIR` / `APPXIMO_FILES_MAX_BYTES` / `APPXIMO_FILES_TOKEN_TTL` / `APPXIMO_FILES_ALLOWED_EXT` | env | no | local blob root; upload cap (256 MiB); signed-URL TTL (180 s); upload extension allowlist |
| `APPXIMO_PUBLIC_ROUTE_RPS` / `APPXIMO_PUBLIC_ROUTE_BURST` | env | no | dedicated rate limit for **public custom routes** (`appximo.Route{Public: true}` in the library model), per tenant+client IP; default 5 rps / burst 10 |
| `APPXIMO_AUTH_SIGNUP_ROLE` | env | no | role assigned to public signup; **set it to enable `POST /auth/signup`** (empty = signup disabled). Must be a schema role |
| `APPXIMO_AUTH_MIN_PASSWORD` | env | no | minimum signup password length (default 8) |
| `APPXIMO_AUTH_REQUIRE_VERIFIED` | env | no | block login until the user's email is verified (default off) |
| `APPXIMO_AUTH_BASE_URL` | env | no | origin for reset/verify email links (else derived from the request Host) |
| `APPXIMO_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID` / `…_CLIENT_SECRET` | env | no | enable social login per provider (unset = provider not offered) |
| `APPXIMO_OAUTH_CALLBACK_URL` / `APPXIMO_OAUTH_DEFAULT_ROLE` | env | no | fixed OAuth redirect origin; role for auto-created social users (falls back to signup role) |
| `APPXIMO_MFA_KEY` / `APPXIMO_MFA_ISSUER` | env | no | TOTP-secret encryption key (falls back to `JWT_SECRET`); authenticator-app issuer label |
| `APPXIMO_CORS_ORIGINS` | env | no | comma-separated browser origins allowed cross-origin (or `*`); **empty = CORS disabled** (safe default). Scoped to `/api`,`/auth`,`/graphql`,`/openapi` |
| `APPXIMO_CORS_METHODS` / `APPXIMO_CORS_HEADERS` / `APPXIMO_CORS_EXPOSE_HEADERS` / `APPXIMO_CORS_CREDENTIALS` / `APPXIMO_CORS_MAX_AGE` | env | no | CORS preflight tuning (see [docs/DEPLOY.md](docs/DEPLOY.md#cors--configurable-for-browser-spas-on-another-origin)) |
| `APPXIMO_GRAPHQL_PLAYGROUND` | env | no | allow GraphQL introspection + serve the GraphiQL explorer at `/graphiql` outside development; **empty = off** (the safe default — `APPXIMO_ENV=development` already enables both). Per-app in the fleet |
| `APPXIMO_PLATFORM_SUPER_ADMIN_ROLE` / `APPXIMO_PLATFORM_MFA_ISSUER` | env | no | admin API: platform super-admin role marker (default `platform_super_admin`); platform authenticator label. Bootstrap the first super-admin with `appximo admin create` |
| `OBS_DB_PATH` | env | no | observability SQLite path; default `/var/lib/appximo/obs.db` (persistent — survives restarts). See [docs/DEPLOY.md](docs/DEPLOY.md#observability-store-obs_db_path) |
| `DB_MAX_CONNS`, `GOMAXPROCS`, `SLACK_WEBHOOK_URL`, `REDIS_URL` | env | no | see [docs/DEPLOY.md](docs/DEPLOY.md) |
| `--schema` | flag | **yes** | path to the JSON schema |
| `--port` | flag | no | data-plane port (default 8080) |
| `--control-port` | flag | no | control-plane port (default 9090; also `APPXIMO_CONTROL_PORT`) — parameterized so several engines can share one box (`appximo fleet`, [docs/FLEET.md](docs/FLEET.md)) |

The control plane (tenant admin) listens on **9090** by default — keep it off the internet.

## Development (from a clone)

One command per task (`make help` lists them all):

```bash
make dev         # build the Studio SPA + engine, load dev secrets, serve on :8080
                 # boots a BLANK app — open http://localhost:8080/editor and
                 # load/paste your schema; or: make dev SCHEMA=mine.json PORT=9000
make dev-fast    # same, skipping the SPA rebuild (when you didn't touch the editor)
make stop        # stop the dev server by its exact PID (make stop PORT=9000)
make spec        # regenerate appximo-spec.md — the LLM grammar pack for your
                 # agent (docs/SCHEMA_SPEC_LLM.md)
make install     # install the version-stamped `appximo` CLI into /usr/local/bin
                 # (may need sudo) — then `appximo validate --json x.json` works anywhere
make fleet-init  # scaffold a working FLEET (N distinct apps on one port): manifest +
                 # generated secrets (gitignored) + starter schema + databases
make fleet       # serve every app on :8080 with the unified console at /fleet —
                 # per-app Studio//admin//docs by domain (docs/FLEET.md)
```

`make dev` reads the env-file at `DEV_ENV` (default `.env.dev`, gitignored)
for the three required vars — `DATABASE_URL`, `JWT_SECRET`, `ADMIN_KEY` — and
loads them only into the launched process; it tells you exactly what to create
if the file is missing.

## Testing

```bash
make test        # unit, -race, no Docker needed (~7 s warm)
make test-all    # + integration + E2E + resilience (real Postgres, toxiproxy)
```

## License & contributing

Apache 2.0 — [LICENSE](LICENSE) · [NOTICE](NOTICE). Issues and PRs welcome,
especially: benchmark-baseline improvements, DNS modules for the Caddy wildcard
setup, and schema features you're missing. Security reports:
[SECURITY.md](SECURITY.md).

---

*Dedicated to my MVC: Máximo, Valentina and Cristina — Model, View, Controller.*
