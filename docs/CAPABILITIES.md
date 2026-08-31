# Appximo — Capability Inventory

Everything the engine does, one line each — verified against the code, not the docs.
Syntax details live in [AGENTS.md](../AGENTS.md); the running surface in
[EXPLORE.md](EXPLORE.md); deployment in [DEPLOY.md](DEPLOY.md). The honest
*not-yet* list is at the [bottom](#what-it-does-not-do) — read it too.

## The core: schema → API

- Compiles one JSON schema into a full API at boot — no handlers, models, or migrations.
- Generates REST per resource — `GET` (list + by id), `POST`, `PUT`, `PATCH`, `DELETE` on `/api/{resource}`.
- Generates GraphQL from the same schema — queries + `create`/`update`/`delete` mutations at `/graphql`.
- Generates an OpenAPI **3.0.3** spec — `appximo openapi schema.json`, importable into Swagger/Hoppscotch.
- Creates the tenant's Postgres tables automatically — idempotent DDL when a tenant registers.
- `file` field type — attaches an uploaded file to a **record** with real referential
  integrity (a `file_id` column with a per-tenant FK to the file store; bad reference →
  422, deleting an attached file → 409 or `set_null`; see [FILES.md](FILES.md#attaching-files-to-records--the-file-field-type-files-link-s1)).

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
- **Records every deployed schema as a version** (VERSION-S1) — `public.schema_history`, append-only,
  written by every persist path (register / deploy / rollback / fan-out), deduped by canonical hash
  (an unchanged re-deploy adds no version); pre-versioning tenants are backfilled at boot.
- **Rolls back to any prior version** — `POST /admin/tenants/{id}/schema/rollback` re-deploys the
  stored version through the SAME diff→gate→apply migration engine: dry-run shows what reverting
  destroys (gated drops with measured rows lost), only enumerated drops execute, and the rollback
  appends a new version. Browsable timeline + rollback UI in Studio ("History").
- **Persists multi-step FLOW TESTS per tenant** (FLOWTEST-S1) — flows-as-data (steps =
  request + assertions + captured variables chained between steps, the api-cert model
  formalized), stored in `public.flow_tests`, authored/run from Studio ("Flows"). Flows
  authenticate as TENANT users (a real `/auth/login` step or a declared role) — real RBAC.
- **Runs post-deploy regression** — `POST /admin/tenants/{id}/flows/run` executes the suite
  against the LIVE router (full chain, in-process) streaming each step's PASS/FAIL over SSE;
  every run is persisted in `public.flow_runs` ANCHORED to the schema version it ran against.
  Deploy → re-run → the exact failing step with expected-vs-got, or all green — trust with
  evidence ("Run regression flows" on the deploy result).
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
- Mints dev tokens from the CLI — `appximo token --tenant X --role Y`.

## Identity (auth-as-product)

- Serves in-engine signup/login/refresh at `POST /auth/*` — the issued JWT is the SAME one the engine validates (one token contract, no second path).
- Isolates users per tenant — they live in `tenant_<id>.auth_users`, so **email is unique per tenant, not globally** (the same email is a distinct account in two tenants — the structural edge over Supabase Auth).
- Hashes passwords with **argon2id** (pure Go, no CGO), paid only on signup/login — never the request hot path; anti-enumeration login (uniform 401, timing-equalized) + per-identity throttle.
- Opt-in public signup — off by default; `APPXIMO_AUTH_SIGNUP_ROLE` enables it and pins the role (a client-supplied role is ignored).
- **Password reset + email verification** — single-use tokens delivered async via the outbox + email worker; `APPXIMO_AUTH_REQUIRE_VERIFIED` can gate login on a verified email.
- **Social login (OAuth2)** — Google, GitHub, Microsoft; standard authorization-code flow, tenant carried in a signed state (not the Host), identity linked by stable provider id, no new dependency. A provider is offered only when its client id is set.
- **TOTP MFA (RFC 6238)** — opt-in per user, AES-256-GCM-encrypted secret, one-time backup codes, two-step login (an intermediate `mfa_token` that carries no role, so it can't authorize CRUD).
- **Stateless sessions** — no server-side session/denylist; logout = discard the token; forced revocation is via admin suspend (blocks new logins; issued JWTs live to `exp`). No refresh-token rotation, magic-link/passwordless, or passkeys yet.

## Queries

- Typed URL filters — `filter[field][op]=v`; ops are constrained per field type ([table](../AGENTS.md#field-types--the-complete-set)).
- Substring text search — `?search=` runs `ILIKE` across the resource's string/text fields (not a ranked full-text engine).
- Single-field sort — `?sort=field&order=asc|desc`, or `?order[field]=desc` (which wins if both are sent).
- Keyset pagination — `?after=<uuid>` / `?before=<uuid>`, `per_page` default 20 / max 100, no OFFSET.
- Field selection — `?fields=a,b` projects the SQL `SELECT` itself (an unlisted TOASTed column is never read); `id` always; unknown → 400, hidden by the role → omitted (the allowlist). GraphQL's selection set is pushed down the same way.
- In-memory response cache — validated-token GETs served from RAM; `PUT`/`PATCH` and reload evict it.
- `Server-Timing` on every generated read — the engine's stage durations (`query`, `count`, `app`; `cache;desc="hit"` on a cache hit), the number the embedded `/app` prints in its footer.
- Atomic batches from outside the binary — `POST /api/transaction`, up to 100 create/update/delete ops in ONE Postgres transaction, each op validated and authorized like its single-row twin; published in `/openapi.json` (the bulk door a migration uses: ~460 batches for 46k rows).

## Real-time & events

- Per-resource SSE streams — `GET /api/{resource}/events`: `create`/`update`/`delete` as they commit.
- Applies RBAC at delivery — fields and rows your role can't see never reach your stream.
- Isolates streams per tenant — one tenant's events never cross into another's stream.
- Caps SSE connections per tenant — over the cap → 429; a comment ping every 25 s keeps proxies open.
- Dispatches signed webhooks — async `POST`, HMAC-SHA256 (`X-Appximo-Signature`), 3 retries with backoff.
- Guards webhooks against SSRF — HTTPS-only, loopback/private/link-local IPs refused (logged, never sent).

## Custom logic (extensions)

- JS hooks in a sandbox — `before`/`after_create`/`update` on Goja, watchdog-interrupted (80 ms soft / 500 ms hard).
- A JS exception in a hook yields a clean 422 — only a watchdog timeout produces a 500.
- JS built-in helpers — `isValidEmail`, `formatMoney`, plus Colombian DIAN `validateNIT` (mod-11) and `calculateCUFE` (SHA-384).
- WASM hooks — Wazero, no CGO, 16 MiB limit, `transform` entrypoint over a pre-loaded module.
- Hooks are compiled at boot — changing them via the control plane requires a restart (reload warns).
- Custom byte-serving routes — `Route.ByteServing` + `Ctx.ServeFile(fileID)`: a handler
  streams a tenant file (public product image, authorized download) through the same
  store as `/api/files/{id}` — Range/strong ETag/sendfile — with the response cache and
  compression bypassed, and a uniform 404 for malformed/unknown/foreign ids.

## Observability

- Prometheus `/metrics` (admin-gated) — request totals, a latency histogram, active-tenant count. ([EXPLORE.md](EXPLORE.md#metrics-metrics-prometheus))
- Embedded Trace Explorer at `/debug/traces` — each request as spans (`jwt`→`rbac`→`query`→`serialize`) in µs. ([EXPLORE.md](EXPLORE.md#the-trace-explorer-debugtraces))
- Per-tenant debug at `/debug/tenant/{id}` — p50/p95, grouped errors with symbolized stack traces, recent traces. ([EXPLORE.md](EXPLORE.md#per-tenant-state-debugtenantid))
- Persists observability snapshots in SQLite — `?history=<hours>` survives restarts.
- SLO burn-rate state — 5m/1h error ratios, `ok`/`warning`/`critical`, Slack alerts with cooldown.
- Per-tenant anomaly detector — EWMA z-score over request latencies.
- Built-in synthetic monitor — 60 s loop over `/health` + a canary derived from the loaded schema.
- Traceable version — `/health` and `appximo version` report the binary's build commit.

## Operations

- One static Go binary — ~64 MB release build (`scripts/build-engine.sh`; a plain `go build` is ~85 MB) — no CGO, any Linux, multi-arch (amd64/arm64). ([DEPLOY.md](DEPLOY.md))
- ~41 MB Docker image (compressed pull) — `docker compose up` to a working API in ~9 s.
- Graceful shutdown — SIGTERM → `/readyz` 503 → drains in-flight requests → exits clean.
- Per-tenant rate limiting — token bucket, default DERIVED from the box: 350 rps × vCPU (GOMAXPROCS), 100 burst — 70 % of the measured per-core clean ceiling (docs/BENCHMARKS.md §4e; `RATE_LIMIT_RPS`/`RATE_LIMIT_BURST` override); over limit → 429 + `Retry-After`.
- Admission control — a cap on in-flight data-plane requests (`APPXIMO_MAX_INFLIGHT`, auto = max(32, 4×(vCPU+pool))); past the box's real ceiling the excess is a cheap early `429 Retry-After` instead of the seconds-scale timeouts a tip used to produce (measured on the customer box at the tipping point: goodput +20 %, p50 1 728 → 36 ms, timeouts 79 013 → 0 — BENCHMARKS §4e).
- Circuit breaker — if Postgres is UNAVAILABLE (connection refused / timeouts — never a client error, ENG-49), 503 + `Retry-After` instead of hanging. Two trip rules over a 10 s ledger (ENG-59, CAOS-S1): ≥ 60 % failures of ≥ 10 requests, OR **20 consecutive failures** — the signal of a black-holed database (link down, packets dropped), where every request used to wait the full 5 s query deadline for its 503 (measured p50 5.00 s → 0.00 s per failed request over a 30 s outage; recovery +0.1 s).
- Host memory guard — while `MemAvailable + SwapFree` is under a floor (`APPXIMO_MEMORY_GUARD_MIN_MB`, default max(32 MiB, 2 % of RAM)), data-plane WRITES answer an explained 503 + `Retry-After`; reads continue. Degradation, not capacity: it stops a bulk load from making the kernel OOM-kill a shared PostgreSQL; it does not make a swapless 1 GB box absorb one.
- The installer verifies what it installed — binary sha256, `/health` version locally AND through Caddy, the schema on disk — and refuses to reuse another app's schema on a re-run over an existing `--app` (MIGRACION-CONFIANZA-S1).
- Per-tenant backup — `POST /admin/backup?tenant=X` runs `pg_dump` of that tenant's schema.
- A backup that RESTORES (RESILIENCIA-S1) — `scripts/backup.sh` writes one SET (dump + uploads + secrets + a manifest of exact per-table counts), scheduled nightly by the installer's timer, optional off-box copy with the secrets encrypted; `scripts/restore.sh --app=X --set=PREFIX` restores and VERIFIES counts/files/FKs/sequences/tenants, every stage timed — measured 13.6 s for a 251 k-row database, ~4 min + DNS from an empty machine (docs/PRODUCTION.md §4). `pg_amcheck` runs inside every backup (OPS-44): every heap page and btree index verified, 0.9 s per 124 MB; a finding fails the backup naming the relation.
- The engine watches its own box (CENTINELA-C-S1 + RESILIENCIA layer 5) — a collector out of the request path reads the runtime, the cgroup, PSI, the pool, disk and the last backup's status every tick; a deterministic attribution verdict (`cpu_throttled` … `healthy`), 21 `appximo_selfmon_*` gauges, `/admin` → Resources; a failed/stale backup or a low disk alerts through the same alerter as the SLO.
- A 500 explains itself (OBSERVABILIDAD-ERRORES-S1) — message and call-site stack on the trace, the exact failed statement from the driver, identity on every trace, the request line at `level:"error"`, fingerprint groups (route + normalized message + top frame) with a first-occurrence alert. A GraphQL resolver failure that would be a 5xx on REST reaches the same trace as a 500 with the wire status kept beside it (ENG-56, DEPLOY-FLOTA-S1).
- A verified deploy (DEPLOY-FLOTA-S1) — `scripts/deploy-app.sh` runs the whole protocol from outside the box: backup set first, atomic swap, `/health` version through the proxy, an authenticated read, a write probe that rolls back by construction, AUTOMATIC rollback re-verified from outside on any failure, `fleet-audit.sh` at the end. `scripts/fleet-audit.sh` says per app what is MISSING on a box (timer, set completeness, off-box, unit policy, swap, checksums, PostgreSQL restart policy).
- PostgreSQL survives its own death — the installer adds a `Restart=on-failure` drop-in (Ubuntu ships `Restart=no`; an OOM-killed PostgreSQL used to stay down for every app on the box) and enables `data_checksums` on a fresh cluster (a corrupt page is an ERROR on read, not silent bad data — and the nightly backup + amcheck is the guaranteed full scan, because checksums fire only on the page being read).
- Three health probes — `/healthz` (liveness), `/readyz` (readiness), `/health` (version).
- Hardening — security headers, sanitized identifiers, masked DB errors, 1 MB body cap, fuzzed parsers.

## CLI subcommands

- `serve`, `validate` (`--json` for the unified report), `validate-schema`, `meta-schema`,
  `token`, `openapi`, `graphql` (SDL), `generate`, `migrate`, `backup`, `init`,
  `ai-generate`, `ai-eval`, `blueprints`, `admin`, `fleet`, `version`.
- The agent docs, five of them — `spec` (the schema grammar for an LLM),
  `backend-spec` (handlers/hooks/auth/jobs), `frontend-spec` (the UI: stack,
  API contract, error→screen-state mapping, files/images incl. public serving,
  browser-only traps — distilled from the production storefront),
  `backoffice-spec` (a CRUD admin UI generated from `/openapi.json` — zero
  resource-specific screens, powered by the `x-appximo-*` contract
  extensions), and `quickstart` (the OPERATE side: install → tenant → users →
  evolve → production — the two steps the first field evaluation had to
  reverse-engineer, printable now). Paste them into your own agent and it can
  build AND run the full stack at zero product API cost.
  `specs` prints all five in one stream (one paste); the root `--help`, the
  README, `/docs` and the installer's closing summary all point at it, and a
  running app's `/openapi.json` lists its REGISTERED custom routes too
  (method/path/auth mode/`x-public`; shapes stay in the app's contract sheet)
  plus the Part-F vendor extensions (`x-appximo-references`, `x-appximo-file`,
  `x-appximo-transitions`, `x-appximo-virtual-resources`).

## What it does NOT do

The capability list above without these limits would be marketing; together they're
engineering. Re-verified against the running engine and the field reports on
2026-08-01 — see [CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md) §4.

- **Rollback is honest, not magic.** The engine keeps an append-only schema
  version history (`public.schema_history`, VERSION-S1) and "roll back to vN" is
  a re-deploy of that stored version through the migration engine — so what
  later versions ADDED is reverted as gated destructive drops (dry-run shows the
  measured rows lost; each drop must be enumerated), and data already destroyed
  by an approved forward drop is NOT recoverable (physics, not policy). A
  rollback appends a new version; the trail is never rewritten.
- **No `neq`/`in`/`nin`/`like`/`ilike` filter ops** — an unsupported operator is a
  400 that names it and lists the allowed set (ADR-024).
- **Filtering by NULL exists since HOUSEKEEPING-S1 (2026-08-05):**
  `?filter[field][is_null]=true|false` renders `IS NULL` / `IS NOT NULL` on every
  nullable column, REST + GraphQL + aggregation (SCHEMA-6 closed; ADR-022
  Decision 5 updated). On the implicit `id` or a `required` (NOT NULL) field it
  is a 400 naming why — a filter that could never match is refused, not served.
- **No multi-field sort.** One sort field only; an unknown field, an invalid
  direction, or two `order[…]` parameters are each a 400 that names the problem
  (ENG-16, closed in NIGHT-SWEEP-S1).
- **No delete hooks** — only `before`/`after_create` and `before`/`after_update`.
- **`workflows` block parses but has no executor.**
- **No shipped WASM business module** — only a test identity module; DIAN logic is a JS built-in, not WASM.
- **Webhooks can't reach localhost/LAN** — HTTPS-only + SSRF guard, always.
- **No OTLP/OpenTelemetry export** — Prometheus `/metrics` + an internal trace ring.
- **Backup and restore are scripts, not engine subcommands** (`scripts/backup.sh`
  / `scripts/restore.sh`, installed and scheduled by the installer — docs/PRODUCTION.md §4).
  The restore is timed and verified count-for-count (13.6 s on 251 k rows), but it
  runs from a shell on the box, not from `appximo restore` (backlog **ENG-3**).
- **One box is one box.** No HA, no failover: if the host dies, the app is down
  until someone acts (the measured recovery on a new machine is ~4 min + the DNS
  TTL, from the off-box set). Checksums and amcheck detect corruption; they do
  not prevent it, and the guaranteed detector runs on the backup's cadence.
- **No zero-downtime binary upgrade** (backlog **ENG-2**). `deploy-update.sh` swaps
  the binary atomically and auto-rolls-back, but the restart costs a measured ~0.5 s
  of `502`s under live traffic. There is no socket handover.
- **Single node** — no HA/clustering; scale is vertical.
- **The Go module is not published.** Writing custom handlers (the "10 %" path)
  requires a local checkout plus a `replace` directive in your `go.mod`: `go get
  github.com/appximo/appximo` does not work, so a project using the framework
  mode does not build on a teammate's machine or in CI. This is a publishing
  decision, not a code gap — see `docs/BACKEND_SPEC_LLM.md` §3.0.
- **`install.sh --app=NAME` has run on real multi-app boxes** (LXD, two apps side
  by side, and the demo box) — what has NOT run live is the migration of a
  pre-existing hand-written monolithic Caddyfile into `import sites/*.caddy`
  (backlog **OPS-11**): on such a box the installer's Caddy step is left alone on
  purpose and the site block is edited by hand.
- **The default per-tenant rate limit is DERIVED from the box: 350 rps × vCPU
  (GOMAXPROCS), 100 burst** — 70 % of the measured per-core ceiling of the
  canonical uncached read (docs/BENCHMARKS.md §4e; before MOTOR-PRODUCCION-S2
  it was a hand-set 1000 unrelated to capacity). A single-tenant load test
  above it gets `429`s; cached traffic is throttled the same as expensive
  traffic (a rate cannot tell them apart). Raise it with `RATE_LIMIT_RPS` /
  `RATE_LIMIT_BURST`. Separately, **admission control** caps in-flight
  requests (`APPXIMO_MAX_INFLIGHT`, auto by default): past the box's real
  ceiling the excess gets a cheap early `429 Retry-After` instead of the
  5-second timeouts a tip used to produce.
- **No hosted/SaaS version** — self-hosted only, by design.
- **No `COPY` / file import.** The bulk write door is `/api/transaction` (100 ops per request, 1 MiB per request). Minutes for tens of thousands of rows; a `COPY`-class door is registered (MIG-FRONT #1), not built.
- **JSON numbers pass through float64** on every door: integers past 2^53 lose digits (ENG-50); a `json` field is a JSON VALUE (ADR-028) stored as canonical text — keys sorted, `1.50` → `1.5`. The exact door is a JSON-text STRING on a `json` field.
- **`?fields=` projects the root row only** — `?include=` embeds and SSE events stay whole. `?page=` is OFFSET (deep pages cost; cursors don't).

(Former limits now closed and documented elsewhere: declarative relations + real
FKs (`?include=`), CORS (`APPXIMO_CORS_ORIGINS`), GraphQL `update` mutation,
opt-in list totals (`?count=true`), `default` values applied on insert, `indexes`
materialized as real DB indexes, write-path response-cache invalidation, and the
tenant list on the admin API (`GET /admin/tenants`).)

(The former "no visual schema editor" limit is closed: **Appximo Studio**
ships embedded at `/editor` — full schema-grammar design, per-tenant deploy
with migration preview + destructive gate, one-click engine restart, and a
tenant files manager. The schema remains plain JSON you can also write by hand.)
