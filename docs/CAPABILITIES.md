# Appitools — Capability Inventory

Everything the engine does, one line each — verified against the code, not the docs.
Syntax details live in [AGENTS.md](../AGENTS.md); the running surface in
[EXPLORE.md](EXPLORE.md); deployment in [DEPLOY.md](DEPLOY.md). The honest
*not-yet* list is at the [bottom](#what-it-does-not-do) — read it too.

## The core: schema → API

- Compiles one JSON schema into a full API at boot — no handlers, models, or migrations.
- Generates REST per resource — `GET` (list + by id), `POST`, `PUT`, `PATCH`, `DELETE` on `/api/{resource}`.
- Generates GraphQL from the same schema — queries + `create`/`delete` mutations at `/graphql`.
- Generates an OpenAPI **3.0.3** spec — `appitools openapi schema.json`, importable into Swagger/Hoppscotch.
- Creates the tenant's Postgres tables automatically — idempotent DDL when a tenant registers.

## Multi-tenancy

- Registers tenants via the control plane (`POST :9090/tenants`), each with its own schema.
- Isolates every tenant in its own Postgres schema — `SET LOCAL search_path`, not a `WHERE`.
- Routes by Host-header subdomain — `acme.example.com` → schema `tenant_acme`, no table lookup.
- Rejects cross-tenant tokens — a tenant's token against another's Host → 401 `token tenant mismatch`.
- Isolates the response cache per tenant — one tenant never receives another's cached response.

## Dynamic schema

- Updates a tenant's schema live — `PUT /tenants/{id}/schema` validates, stores, applies additive DDL.
- Reloads without a restart — `POST /admin/tenants/{id}/reload`: new columns take effect immediately.
- Reload is column-level only — new resources, hooks, and roles still need a process restart.
- Propagates schema reloads across the process via Postgres `pg_notify` on `schema_updated`.
- Validates every schema on receipt — bad types, bad rules, unknown keys → error listing valid keys.

## Declarative validation

- Validates each write against schema rules — `required`, `enum`, `min`/`max`, `minLength`/`maxLength`, `pattern` (RE2), `format`.
- `format` is exactly one of `email | uuid | url | date` — no other format names.
- Returns **422 with every failing field at once** — each with its field, rule, and message.
- Rejects unknown fields with 422 `unknown_field` — never a 500, never silently dropped.
- Compiles rules, regexes, and enum sets once at schema load — the request path runs precompiled closures.

## Auth & authorization

- Requires a JWT (HS256 only) on every data-plane request — `exp` enforced, alg-confusion rejected.
- Declares RBAC in the schema — per role, per resource, per action, per field.
- Supports a response field allowlist per role via the `fields` key (`["id","title",...]`).
- Filters rows with dynamic conditions — `operator_id = $user_id` resolves to the JWT subject.
- Denies by default — no matching policy → 403; no token → 401; row excluded by a condition → 404.
- Compiles RBAC at boot — roles added via `PUT` + reload do not take effect until restart.
- Mints dev tokens from the CLI — `appitools token --tenant X --role Y`.

## Identity (auth-as-product)

- Serves in-engine signup/login/refresh at `POST /auth/*` — the issued JWT is the SAME one the engine validates (one token contract, no second path).
- Isolates users per tenant — they live in `tenant_<id>.auth_users`, so **email is unique per tenant, not globally** (the same email is a distinct account in two tenants — the structural edge over Supabase Auth).
- Hashes passwords with **argon2id** (pure Go, no CGO), paid only on signup/login — never the request hot path; anti-enumeration login (uniform 401, timing-equalized) + per-identity throttle.
- Opt-in public signup — off by default; `APPITOOLS_AUTH_SIGNUP_ROLE` enables it and pins the role (a client-supplied role is ignored).
- **Password reset + email verification** — single-use tokens delivered async via the outbox + email worker; `APPITOOLS_AUTH_REQUIRE_VERIFIED` can gate login on a verified email.
- **Social login (OAuth2)** — Google, GitHub, Microsoft; standard authorization-code flow, tenant carried in a signed state (not the Host), identity linked by stable provider id, no new dependency. A provider is offered only when its client id is set.
- **TOTP MFA (RFC 6238)** — opt-in per user, AES-256-GCM-encrypted secret, one-time backup codes, two-step login (an intermediate `mfa_token` that carries no role, so it can't authorize CRUD).
- **Stateless sessions** — no server-side session/denylist; logout = discard the token; forced revocation is via admin suspend (blocks new logins; issued JWTs live to `exp`). No refresh-token rotation, magic-link/passwordless, or passkeys yet.

## Queries

- Typed URL filters — `filter[field][op]=v`; ops are constrained per field type ([table](../AGENTS.md#field-types--the-complete-set)).
- Substring text search — `?search=` runs `ILIKE` across the resource's string/text fields (not a ranked full-text engine).
- Single-field sort — `?sort=field&order=asc|desc`, or `?order[field]=desc` (which wins if both are sent).
- Keyset pagination — `?after=<uuid>` / `?before=<uuid>`, `per_page` default 20 / max 100, no OFFSET.
- In-memory response cache — validated-token GETs served from RAM; `PUT`/`PATCH` and reload evict it.

## Real-time & events

- Per-resource SSE streams — `GET /api/{resource}/events`: `create`/`update`/`delete` as they commit.
- Applies RBAC at delivery — fields and rows your role can't see never reach your stream.
- Isolates streams per tenant — one tenant's events never cross into another's stream.
- Caps SSE connections per tenant — over the cap → 429; a comment ping every 25 s keeps proxies open.
- Dispatches signed webhooks — async `POST`, HMAC-SHA256 (`X-Appitools-Signature`), 3 retries with backoff.
- Guards webhooks against SSRF — HTTPS-only, loopback/private/link-local IPs refused (logged, never sent).

## Custom logic (extensions)

- JS hooks in a sandbox — `before`/`after_create`/`update` on Goja, watchdog-interrupted (80 ms soft / 500 ms hard).
- A JS exception in a hook yields a clean 422 — only a watchdog timeout produces a 500.
- JS built-in helpers — `isValidEmail`, `formatMoney`, plus Colombian DIAN `validateNIT` (mod-11) and `calculateCUFE` (SHA-384).
- WASM hooks — Wazero, no CGO, 16 MiB limit, `transform` entrypoint over a pre-loaded module.
- Hooks are compiled at boot — changing them via the control plane requires a restart (reload warns).

## Observability

- Prometheus `/metrics` (admin-gated) — request totals, a latency histogram, active-tenant count. ([EXPLORE.md](EXPLORE.md#metrics-metrics-prometheus))
- Embedded Trace Explorer at `/debug/traces` — each request as spans (`jwt`→`rbac`→`query`→`serialize`) in µs. ([EXPLORE.md](EXPLORE.md#the-trace-explorer-debugtraces))
- Per-tenant debug at `/debug/tenant/{id}` — p50/p95, grouped errors with symbolized stack traces, recent traces. ([EXPLORE.md](EXPLORE.md#per-tenant-state-debugtenantid))
- Persists observability snapshots in SQLite — `?history=<hours>` survives restarts.
- SLO burn-rate state — 5m/1h error ratios, `ok`/`warning`/`critical`, Slack alerts with cooldown.
- Per-tenant anomaly detector — EWMA z-score over request latencies.
- Built-in synthetic monitor — 60 s loop over `/health` + a canary derived from the loaded schema.
- Traceable version — `/health` and `appitools version` report the binary's build commit.

## Operations

- One static Go binary (~45 MB) — no CGO, any Linux, multi-arch (amd64/arm64). ([DEPLOY.md](DEPLOY.md))
- ~22 MB Docker image — `docker compose up` to a working API in ~9 s.
- Graceful shutdown — SIGTERM → `/readyz` 503 → drains in-flight requests → exits clean.
- Per-tenant rate limiting — token bucket (default 1000 RPS / 100 burst), over limit → 429 + `Retry-After`.
- Circuit breaker — if Postgres fails (≥10 req & ≥60% error), 503 + `Retry-After` instead of hanging.
- Per-tenant backup — `POST /admin/backup?tenant=X` runs `pg_dump` of that tenant's schema.
- Three health probes — `/healthz` (liveness), `/readyz` (readiness), `/health` (version).
- Hardening — security headers, sanitized identifiers, masked DB errors, 1 MB body cap, fuzzed parsers.

## CLI subcommands

- `serve`, `validate`, `token`, `openapi`, `graphql` (SDL), `generate`, `migrate`, `backup`, `init`, `version`.

## What it does NOT do

The capability list above without these limits would be marketing; together they're engineering.

- **No declarative relations** — `relation` adds only a read-only `GET /api/{res}/{id}/{rel}` subroute; no FK, no joins, no cascade.
- **No CORS middleware** — browser SPAs must be served same-origin ([workaround](DEPLOY.md#cors--current-status-important-for-spas)).
- **No GraphQL `update` mutation** — create/delete only; use REST `PUT`/`PATCH`.
- **No total count** in list responses — `count=true` is not a thing.
- **No `neq`/`in`/`like`/`is_null` filter ops** — unsupported ops → 400.
- **Multi-field sort and `sort=field:desc` are silently ignored** — verify result order.
- **No delete hooks** — only `before`/`after_create` and `before`/`after_update`.
- **POST/DELETE do not invalidate the response cache** — those entries expire by TTL only.
- **`default` values parse but are not applied** on insert.
- **`indexes` parse but create no DB index** — accepted with a `warnings` entry.
- **`workflows` block parses but has no executor.**
- **No shipped WASM business module** — only a test identity module; DIAN logic is a JS built-in, not WASM.
- **Webhooks can't reach localhost/LAN** — HTTPS-only + SSRF guard, always.
- **No OTLP/OpenTelemetry export** — Prometheus `/metrics` + an internal trace ring.
- **No tenant-list endpoint** — the control plane is get-by-id only.
- **Backup has no restore command and no scheduling.**
- **Single node** — no HA/clustering; scale is vertical.
- **No hosted/SaaS version** — self-hosted only, by design.

(The former "no visual schema editor" limit is closed: **Appitools Studio**
ships embedded at `/editor` — full schema-grammar design, per-tenant deploy
with migration preview + destructive gate, one-click engine restart, and a
tenant files manager. The schema remains plain JSON you can also write by hand.)
