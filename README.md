# Appitools

> **A JSON schema in. A production multi-tenant REST + GraphQL + OpenAPI server out.**
> One ~60 MB static Go binary, on your own server. Apache 2.0.

[![CI](https://github.com/miguel09acosta/appitools/actions/workflows/ci.yml/badge.svg)](https://github.com/miguel09acosta/appitools/actions/workflows/ci.yml)
[![Docker](https://img.shields.io/docker/v/neodevtrix/appitools-engine?label=docker&color=2496ED&logo=docker)](https://hub.docker.com/r/neodevtrix/appitools-engine)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](go.mod)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue)](LICENSE)
<!-- Activate after the first tagged release (git tag v0.1.0 && git push --tags):
[![Release](https://img.shields.io/github/v/release/miguel09acosta/appitools?label=release)](https://github.com/miguel09acosta/appitools/releases)
-->

You don't write handlers, models, or migrations. You write this:

```json
{
  "$schema": "https://appitools.dev/schema/v1",
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
- an OpenAPI 3.0 spec (`appitools openapi schema.json`)
- declarative validation (`422` listing **every** invalid field at once)
- live updates over SSE (`GET /api/tasks/events`)
- JWT auth + RBAC enforced on every request, deny by default — that `viewer`
  role really is read-only and never sees the `due` field

It's a code generator without the generated code: the schema is compiled at boot,
not scaffolded into files you then maintain.

## Quick start (~30 s with the image pull)

> ⚠ The `curl` URLs work once this repo is public. From a clone, the two files
> are at the repo root — skip the downloads.

```bash
mkdir appitools && cd appitools
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/.env.example
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
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme\",\"email\":\"a@acme.com\",\"plan\":\"free\",\"schema\":$(docker compose exec engine cat /etc/appitools/schema.json)}"

# 2. mint a JWT for the "admin" role the schema defines (helper ships in the image)
TOKEN=$(docker compose exec engine appitools token --secret "$JWT_SECRET" --tenant acme --role admin 2>/dev/null | tail -1)

# 3. write a task
curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost" -H "Content-Type: application/json" \
  -d '{"title":"ship the launch","status":"open"}'

# 4. read it back, filtered (curl -g: brackets need globbing off)
curl -g "http://localhost:8080/api/tasks?filter[status][eq]=open&per_page=20" \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost"
```

To serve **your own** model, mount your schema over `/etc/appitools/schema.json`
(or `--schema yours.json` on the binary). Tenants are addressed by Host
subdomain: `acme.localhost` → Postgres schema `tenant_acme`.

## Where it sits

Honest comparison — these are different tools that overlap on "I need an API":

| | **Appitools** | NestJS / Express / Rails | Supabase | PocketBase |
|---|---|---|---|---|
| You write | a JSON schema | application code | SQL + RLS policies + client code | collections config + Go/JS hooks |
| API surface | REST + GraphQL + OpenAPI, generated | whatever you build | PostgREST + client SDKs | REST + realtime |
| Multi-tenancy | first-class: schema-per-tenant isolation, subdomain routing | you build it | you build it (RLS) | one DB per app |
| Database | your PostgreSQL | any | bundled Postgres (its platform) | embedded SQLite |
| Runtime | one static Go binary, ~24 MB RSS idle | Node/Ruby + deps | a service fleet (or their cloud) | one Go binary |
| Custom logic | sandboxed JS (Goja) + WASM (Wazero), watchdog-timed | unlimited (it's your code) | edge functions, triggers | Go/JS hooks |

What they do **better**: frameworks give you unlimited logic — Appitools' escape
hatches are sandboxed hooks, not a general backend. Supabase has auth providers,
storage, realtime channels and a massive ecosystem. PocketBase is even simpler to
run (no Postgres needed). Appitools' lane is: **several isolated tenants on one
cheap box, talking to a Postgres you control, with the API contract generated
and enforced from a schema file.**

## Performance

On a $16/mo 2-vCPU droplet with JWT + RBAC + multi-tenancy + validation + rate
limiting all active, measured from an external load generator over a real network:
**2,000 req/s sustained, p50 1.58 ms (CI95 [1.52, 1.62]), 0 errors in 600k requests** —
server-side, 99.98% of requests completed in under 5 ms. Head-to-head against a
deliberately lean NestJS+Prisma baseline on the same box: **~4.8× faster at the
median** (with Appitools' default response cache), **~2.7× with the cache fully
bypassed**; the NestJS baseline saturates between 500 and 750 req/s.

Full methodology — including every limitation, the cache asymmetry, statistical
treatment (Mann-Whitney, bootstrap CIs), and raw per-run data — in
[**BENCHMARK_PUBLIC.md**](context-docs/BENCHMARK_PUBLIC.md). Reproduce it:
[`benchmark-lab/`](benchmark-lab/) + `make bench-protocol`. PRs that make the
baseline faster are welcome; we'll publish updated numbers.

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
  conditions couldn't express
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
  for machine callers. Bootstrap with `appitools admin create`. A **SolidJS UI is
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
  off in production
- **API contract, served**: an OpenAPI 3.0 spec generated from the schema —
  including the auth + file-store endpoints — served at `/openapi.json` (and
  `/openapi.yaml`), with **Swagger UI at `/docs`** for interactive exploration
- **CORS**: configurable cross-origin access for browser SPAs on another origin
  (`APPITOOLS_CORS_ORIGINS`), disabled by default, scoped to `/api`,`/auth`,
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
  rollback UI in the visual editor ("History")
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

Three paths, walked through in [docs/DEPLOY.md](docs/DEPLOY.md):

| Goal | Path |
|---|---|
| **Try it** in ~9 s | `docker compose up` — the [quick start](#quick-start-30-s-with-the-image-pull) above |
| **Production, simple** | `docker-compose.prod.yml` + Caddy: automatic Let's Encrypt TLS, tenant subdomains, engine and Postgres on the internal network only |
| **Production, max throughput** | native binary (from [GitHub Releases](https://github.com/miguel09acosta/appitools/releases), SHA256-checksummed) + dockerized Postgres + reverse proxy with upstream keepalive — the configuration the benchmark measured |

## Status — what's real and what's missing

**Production-ready and test-backed** (`make test-all`: unit + integration + E2E +
resilience against real Postgres in Docker): everything in the feature list above.
It serves our own production workload today.

**Known limits, honestly:**

- **Single node.** No HA/clustering story; scale is vertical (the benchmark shows
  how far one cheap box goes).
- Observability is Prometheus + an internal trace ring — **no OTLP export**.
- No hosted/SaaS version. Self-hosted only, by design, for now.

Beyond the API surface, the binary also embeds **Appitools Studio** at `/editor`
— a visual schema designer (full ERD over the schema grammar, visual RBAC,
state machines, relations) that deploys/migrates tenants, restarts the engine
in one click when a new resource needs it, and manages tenant files — plus the
admin panel at `/admin`. Both are static SPAs compiled into the binary
(`make editor-ui admin-ui` before `go build`; the published image ships them).

## Configuration

| Setting | Kind | Required | Description |
|---------|------|----------|-------------|
| `DATABASE_URL` | env | **yes** | PostgreSQL connection string |
| `JWT_SECRET` | env | **yes** | HS256 signing secret (≥ 32 chars) |
| `ADMIN_KEY` | env | **yes** | `X-Admin-Key` for `/metrics`, `/debug`, `/admin`, control plane |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | env | no | per-tenant token bucket (default 1000/100) |
| `APPITOOLS_MAX_TX_OPS` | env | no | max operations per `POST /api/transaction` (default 100) |
| `APPITOOLS_FILES_BACKEND` | env | no | file-store storage: `local` (default, this box's disk) or `s3` (R2/Spaces/MinIO/AWS). See [docs/FILES.md](docs/FILES.md) |
| `APPITOOLS_FILES_S3_*` | env | with `s3` | `BUCKET`,`ENDPOINT`,`REGION`,`ACCESS_KEY`,`SECRET_KEY`,`FORCE_PATH_STYLE`,`PREFIX`,`SERVE` — provider-agnostic S3 config |
| `APPITOOLS_FILES_DIR` / `APPITOOLS_FILES_MAX_BYTES` / `APPITOOLS_FILES_TOKEN_TTL` / `APPITOOLS_FILES_ALLOWED_EXT` | env | no | local blob root; upload cap (256 MiB); signed-URL TTL (180 s); upload extension allowlist |
| `APPITOOLS_AUTH_SIGNUP_ROLE` | env | no | role assigned to public signup; **set it to enable `POST /auth/signup`** (empty = signup disabled). Must be a schema role |
| `APPITOOLS_AUTH_MIN_PASSWORD` | env | no | minimum signup password length (default 8) |
| `APPITOOLS_AUTH_REQUIRE_VERIFIED` | env | no | block login until the user's email is verified (default off) |
| `APPITOOLS_AUTH_BASE_URL` | env | no | origin for reset/verify email links (else derived from the request Host) |
| `APPITOOLS_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID` / `…_CLIENT_SECRET` | env | no | enable social login per provider (unset = provider not offered) |
| `APPITOOLS_OAUTH_CALLBACK_URL` / `APPITOOLS_OAUTH_DEFAULT_ROLE` | env | no | fixed OAuth redirect origin; role for auto-created social users (falls back to signup role) |
| `APPITOOLS_MFA_KEY` / `APPITOOLS_MFA_ISSUER` | env | no | TOTP-secret encryption key (falls back to `JWT_SECRET`); authenticator-app issuer label |
| `APPITOOLS_CORS_ORIGINS` | env | no | comma-separated browser origins allowed cross-origin (or `*`); **empty = CORS disabled** (safe default). Scoped to `/api`,`/auth`,`/graphql`,`/openapi` |
| `APPITOOLS_CORS_METHODS` / `APPITOOLS_CORS_HEADERS` / `APPITOOLS_CORS_EXPOSE_HEADERS` / `APPITOOLS_CORS_CREDENTIALS` / `APPITOOLS_CORS_MAX_AGE` | env | no | CORS preflight tuning (see [docs/DEPLOY.md](docs/DEPLOY.md#cors--configurable-for-browser-spas-on-another-origin)) |
| `APPITOOLS_PLATFORM_SUPER_ADMIN_ROLE` / `APPITOOLS_PLATFORM_MFA_ISSUER` | env | no | admin API: platform super-admin role marker (default `platform_super_admin`); platform authenticator label. Bootstrap the first super-admin with `appitools admin create` |
| `OBS_DB_PATH` | env | no | observability SQLite path; default `/var/lib/appitools/obs.db` (persistent — survives restarts). See [docs/DEPLOY.md](docs/DEPLOY.md#observability-store-obs_db_path) |
| `DB_MAX_CONNS`, `GOMAXPROCS`, `SLACK_WEBHOOK_URL`, `REDIS_URL` | env | no | see [docs/DEPLOY.md](docs/DEPLOY.md) |
| `--schema` | flag | **yes** | path to the JSON schema |
| `--port` | flag | no | data-plane port (default 8080) |
| `--control-port` | flag | no | control-plane port (default 9090; also `APPITOOLS_CONTROL_PORT`) — parameterized so several engines can share one box (`appitools fleet`, [docs/FLEET.md](docs/FLEET.md)) |

The control plane (tenant admin) listens on **9090** by default — keep it off the internet.

## Testing

```bash
make test        # unit, -race, no Docker needed (~7 s warm)
make test-all    # + integration + E2E + resilience (real Postgres, toxiproxy)
```

## License & contributing

Apache 2.0 — [LICENSE](LICENSE) · [NOTICE](NOTICE). Issues and PRs welcome,
especially: benchmark-baseline improvements, DNS modules for the Caddy wildcard
setup, and schema features you're missing.

*Show HN thread: (link pending launch)*
