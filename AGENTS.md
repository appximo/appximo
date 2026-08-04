# AGENTS.md — Appximo

Instructions for AI coding agents. Part 1 is for working **on this repo**
(contributing to the engine). The [Integration guide](#integration-guide--helping-a-user-adopt-appximo)
is for the other job: helping a user adopt Appximo in **their** project.
Every syntax claim in this file is audited against the engine source
(`pkg/schema`, `pkg/query`, `pkg/graphql`) — do not invent API surface
beyond what is listed here.

## What this is

Appximo compiles a JSON schema into a multi-tenant REST + GraphQL +
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
go build -o appximo ./cmd/appximo
DATABASE_URL='postgres://user:pass@localhost:5432/db' \
JWT_SECRET='a-secret-of-at-least-32-characters' ADMIN_KEY='dev-admin' \
  ./appximo serve --schema examples/quickstart/schema.json --port 8080
```

- All three env vars are hard-required — `serve` exits without them.
- Do NOT use `make run` / `go run ./cmd/appximo/main.go`: passing the
  file compiles *only* `main.go`, producing a binary with **zero
  subcommands** (no `serve`). Use the package path:
  `go run ./cmd/appximo serve …`.
- Other subcommands: `validate <schema>` (SEMANTIC, the Go authority — load +
  cross-reference checks; `--json` emits the UNIFIED LLM-friendly report —
  structural + semantic — with path/rule/message/expected/got/fix/source per error,
  the AI correction loop, see docs/AI_SCHEMA_GENERATION.md), `validate-schema <schema>`
  (STRUCTURAL — validates against the embedded formal JSON Schema meta-schema,
  `pkg/schema/appximo.schema.json`; engine-free, the deterministic net for
  AI-generated schemas), `meta-schema` (prints that meta-schema for IDE `$schema` /
  tooling), `token` (mint a dev JWT),
  `openapi`, `graphql` (SDL), `generate`, `migrate`, `backup`, `init`,
  `ai-generate "<description>"` (the AI democratization loop: a natural-language
  app description → LLM-generated schema → self-correct from `ValidateReport`'s
  actionable errors → a VALID schema, printing the ECONOMIC instrumentation —
  iterations / tokens / approx cost. **DEFAULT = the plain validator-guided loop
  (AI-F2-S4, decided with real data):** the cheap model + the validator oracle reach
  ~90% first-try / 100% convergence / ~$0.006 per schema (K=3). **ARCHIVED /
  EXPERIMENTAL** — constrained decoding (AI-F1-S1 `--structured` envelope, AI-F2-S2
  `--array-ir`): both are opt-in and do NOT engage on the real Anthropic API (the
  strict-outputs subset needs additionalProperties:false on every object → open maps
  inexpressible, and caps union params at 16 → the field grammar exceeds it), so they
  silently fall back to plain. Kept for measurement (`ai-eval`) and because the
  array-IR transforms (`pkg/aigen/ir.go`: lossless `MapToIR`/`IRToMap`) are REUSED BY
  THE VISUAL EDITOR (the structured, index-addressable schema representation a UI
  needs) — not for production decoding. `pkg/aigen`, key from `ANTHROPIC_API_KEY`, raw
  `/v1/messages` so no new dependency; default model `claude-haiku-4-5`; see
  docs/AI_SCHEMA_GENERATION.md §The decision (AI-F2-S4)),
  `ai-eval` (the AI-F2-S1 measurement instrument: runs the embedded stratified
  NL→schema gold test set — `pkg/aigen/eval/corpus`, simple/media/compleja, scaled to
  **40/stratum = 120** in AI-F2-S3 — through a paired ablation (plain vs
  structured-envelope vs **array-IR**, 3 arms) and emits rigorous statistics: `p_sem`
  with **Wilson** CIs, **empirical** E[iterations] (never the geometric 1/p_sem — the
  loop isn't i.i.d.), **McNemar** (exact <25 discordants else Edwards χ²) +
  **Cochran-Q**/**Holm** for >2 arms, flagging underpowered comparisons as
  INCONCLUSIVE. SIMULATED + deterministic by default (no key); `--live` measures a real
  model (temp 0, retry-with-backoff on rate limits), `--sample N` bounds it to a
  stratified subsample. **AI-F2-S3 LIVE FINDING (Haiku):** the structured-envelope and
  array-IR arms FALL BACK TO PLAIN on the real API (the strict-outputs subset rejects
  the envelope's open objects and the IR's >16 union params — the instrument prints
  `⚠ … engaged in only 0/N …`), so all 3 arms run plain; plain itself gets ~90%
  first-try / 100% convergence / ~$0.006/schema with the cheap model (thesis holds,
  the constrained-decoding foundation does NOT engage as designed). The gate every
  future technique passes; see docs/AI_SCHEMA_GENERATION.md §The first real measurement),
  `spec` (JSON-EDITOR-S3: prints the schema grammar DISTILLED FOR AN LLM — closed
  sets, strict keys, file field, state machines, hooks, events, both RBAC forms,
  full FK coverage, two engine-validated worked examples, and the correction
  loop. The EXTERNAL-agent front: paste it into your own Claude Code/Cursor,
  generate, self-correct with `validate --json` as the oracle — the same
  validator-guided loop as `ai-generate` but on YOUR subscription, zero product
  API cost. SAME grammar source as the internal loop (`pkg/aigen` `GrammarCore`,
  divergence pinned by `spec_test.go`); the flow: docs/SCHEMA_SPEC_LLM.md),
  `backend-spec` (LIBRARY-HARDEN-S1: the COMPANION to `spec` for building a
  COMPLETE backend, not just the schema. Prints docs/BACKEND_SPEC_LLM.md
  (embedded via `//go:embed` in `backendspec.go`, single source): the
  decision framework — schema vs hook vs custom Go handler vs job (SafeGo /
  outbox) vs external service — the whole `Ctx` surface with COMPILING examples
  (mirrored by examples/backend-guide/), the Phase-0 safety RULES (never a raw
  `go` — always `Ctx.SafeGo`; external side effects post-commit; public-route
  hardening; the tenant tx is a single non-concurrent connection), hooks, auth,
  and the async model. Paste `spec` + `backend-spec` into your own Claude
  Code/Cursor and the agent can write handlers/hooks/jobs safely — the
  in-process power made agent-accessible; LIBRARY-GAPS-S1 added the four seams a
  REAL backend needed — `Ctx.RawBody()` (the exact request bytes under the engine's
  own cap, for verifying a webhook signature BEFORE parsing; composes with Bind
  because the body is buffered once), `Config.BeforeStart` + `App.Pool()` (boot DDL
  / seeds on the ENGINE'S pool, before the listener opens; an error aborts the
  boot), `Route.RateLimit` (a per-endpoint throttle — the public default of 5 rps
  is right for a registration endpoint and wrong for a catalogue), and the RBAC
  `routes` grant. LIBRARY-GAPS-S2 closed the 1-B frontend field report:
  **static mounts own their CSP** (both forms serve `DefaultStaticCSP` — a
  same-origin SPA policy; override per mount with `StaticMount.CSP`, disable
  with `CSPOff`; before, a root mount inherited the API's `default-src 'none'`
  and a real SPA rendered BLANK in every browser, invisible to curl — and an
  assets-only non-SPA mount no longer requires an index.html), **`Route.Public`
  is optionally authenticated** (no token → Claims zero; valid Bearer → Claims
  POPULATED, identity as input, path-RBAC still skipped; invalid/expired/
  foreign-tenant Bearer → 401, never a silent downgrade to anonymous), and
  **`Ctx.Update` enforces declared state machines** via the SAME
  `codegen.AppendStateTransitionGuard` the generated PATCH/GraphQL/batch paths
  use (illegal move → `*InvalidTransitionError`, the identical 422; concurrent
  change → `ErrUpdateConflict`, 409 — a custom transition route re-states no
  table),
  `frontend-spec` (FRONTEND-SPEC-S1: the THIRD doc of the agent trilogy —
  `spec` teaches the schema, `backend-spec` the handlers, this one the
  FRONTEND. Prints docs/FRONTEND_SPEC_LLM.md (embedded via `//go:embed` in
  `frontendspec.go`, single source), distilled from the shipped commerce
  storefront, not theory: the embedded-vs-apart decision (default: `go:embed`
  + `Config.Static`, one binary, same origin, no CORS), the recommended stack
  WITH its argument (SvelteKit + adapter-static as a pure SPA, `ssr=false` —
  SSR breaks the one-binary model; the real criterion is what a cheap AI
  writes correctly first try), the complete API contract a UI consumes
  (tenant = Host, auth incl. the `mfa_required` branch, the EXACT filter
  grammar with the does-not-exist list, keyset pagination + the cursor⊘sort
  caveat, `?include=`, aggregates as the dashboard endpoint, SSE via fetch-
  reader because EventSource can't send Authorization), the error→screen-state
  contract (the multi-field 422 with scroll-to-first, the work-preserving 409,
  401 vs 403, the honest 503+Retry-After, network=status 0), the six mandatory
  screen states, the FILES pattern (upload with XHR progress → attach via the
  `file` field → display: signed URLs for authed screens because `<img>` can't
  send headers, and PUBLIC serving via a `ByteServing` route + `Ctx.ServeFile`
  that authorizes by relationship), the empty-string-passes-`required` form
  trap (declare `minLength: 1`), and the browser-only traps (CSP blank-200,
  the gitignored-hashed-assets empty shell, `go:embed all:`). Runnable proof:
  examples/frontend-guide/ — a no-build vanilla SPA + one ServeFile route,
  verified 6/6 in a real mobile-viewport browser. The same session added the
  library seam the pattern needs: **`Ctx.ServeFile(fileID)`** (streams a
  tenant file through the SAME `files.Store` as `/api/files/{id}` — Range,
  strong ETag/304, sendfile; uniform 404 for malformed/unknown/foreign ids —
  DELIVERED AFTER COMMIT like every custom-route response) + **`Route.
  ByteServing`** (GET-only, literal path; routes the response around the
  response cache AND the compression wrapper, which would otherwise buffer
  the blob in RAM, strip Content-Disposition/Accept-Ranges on a hit, and
  suppress sendfile — ServeFile refuses to run without it, loudly; FILES-2:
  `ServeFile(id, WithCacheControl(...))` declares the response cache policy —
  `CacheControlImmutable` is safe whenever the URL embeds the file id, because
  the store is content-addressed; sent only on the success path, never on the
  404)),
  `specs` (THIRD-PARTY-READY-S1: prints the WHOLE trilogy —
  spec + backend-spec + frontend-spec — in one stream with banners, for the
  one-paste agent priming; pure concatenation of the three single sources, so
  it can never diverge. The root `appximo --help` and each spec's header now
  name the trilogy — the discoverability fix: nobody has to be told the three
  commands exist),
  `blueprints list` (lists schema files in a local `blueprints/` dir),
  `version` (prints the ldflags-injected build version; "dev" on a plain
  local build — releases and published images carry their tag),
  `fleet run|serve|status` (ONE server, N DISTINCT apps from a `fleet.json`
  manifest, TWO runtimes. `run` = MT-STRUCT-S1 multi-process: one engine
  process per app, supervised (restart-on-EXIT-only, reconciled with the
  engine's same-PID self-restart), behind a Host-routing reverse proxy (pure
  transport; inbound Host preserved for tenant resolution) — total isolation,
  ~25–50 MB PSS/app. `serve` = MT-STRUCT-S3/S4/S5 IN-PROCESS (Option B): N full App
  instances compiled in one process (~1 MB/app; 2 apps = 88 MB RSS total),
  dispatched by Host through the lock-free registry — the app is resolved
  BEFORE auth, so JWT/RBAC/data/cache/SSE are all the resolved app's own
  (security-reviewed: 18-vector cross-app matrix, zero leakage; the claims
  cache is keyed by (secret, token) so one app's validation never
  short-circuits another's; unmatched Host → clean 404, never an arbitrary
  app; a deploy HOT-SWAPS just that app (S4): POST /admin/engine/schema
  recompiles one app's router from the new schema and atomically swaps the
  registry entry, reusing the pool/infra, NO process restart and the other apps
  untouched — benched no_change on both the read path and other apps during a
  swap (66 swaps, 0 errors, unchanged p50), race-clean, in-flight requests
  finish on the old surface; single-engine keeps the graceful whole-process
  re-exec; per-app env in-process limited to Config-mapped keys, others loudly
  warned. S5: the UNIFIED FLEET CONSOLE at /fleet on the process-level Host —
  fleet overview (per app: domains, LIVE resources, tenants, hot-swap count,
  links into that app's own Studio//admin//docs on its domain) + observability
  by (app, tenant) (each app's per-tenant snapshots namespaced under the app —
  zero obs re-keying, zero hot-path change); gated by the FLEET-OPERATOR key
  (manifest operator_key / APPXIMO_FLEET_OPERATOR_KEY, validated distinct
  from every app credential; wrong/missing key = uniform 404; empty = console
  disabled; the fleet key opens NO app API and vice versa). The editor reads
  `activation: hot_swap|restart` from /admin/served-resources and words the
  deploy banner accordingly (hot-swap = no downtime, only that app). DevHub
  navigates N apps by registering each app's control port in its multi-server
  registry — no engine coupling).
  Both: per-app DATABASE_URL/JWT_SECRET/ADMIN_KEY REQUIRED — a shared
  JWT_SECRET across apps is rejected at load; fresh databases bootstrapped
  automatically with the canonical control-plane DDL. Benches: S1 ports-only
  (no hot-path change); S2 registry + S3 per-app chains both measured
  `no_change` (Mann-Whitney, max(0.5ms,3%) gate). See docs/FLEET.md +
  docs/design/MT-STRUCT.md).
- `tools/devhub/` is a local dev dashboard (systemd service on :3099).
  It is **not part of the engine** — never ship engine features there.
  `make devhub-run` is only for developing the devhub itself (stop the
  service first or :3099 is taken and the stale binary keeps serving).
- `pkg/editorui/` is the **visual schema editor** (Appximo Studio, UI-F0-S1):
  a static Svelte 5 SPA (plain Vite, `pkg/editorui/web/`) `go:embed`-served at
  **`/editor`** — a graphical ERD over the schema, no AI, zero Node in prod. Build
  it with `make editor-ui` BEFORE `go build` (same committed-`index.html` /
  gitignored-assets pattern as the admin UI); a bare build serves an empty shell
  (logged). It edits/exports the same schema JSON the engine consumes (round-trip
  faithful, `appximo validate`-clean). It is the schema-authoring FACE of the
  product — engine features still live in the schema/engine, never in the editor.
  **Deploy from the editor (UI-F1-S1):** a "Deploy" button signs in as a platform
  super-admin (token held in MEMORY only, never persisted) and provisions a new
  tenant or migrates an existing one via the `/admin/tenants/{id}/schema` routes —
  with the dry-run migration preview + destructive-approval gate shown in the UI. It
  INVOKES the engine's migration path, never reimplements it — closing the
  design→deploy→running loop. **One-click engine restart (UI-F4-S2):** when a
  deploy contains a NEW resource (boot-compiled routes/GraphQL/docs can't serve
  it yet), the result step offers "Restart engine now" — `POST
  /admin/engine/schema` (same super-admin auth) validates + ATOMICALLY persists
  the schema as the new boot schema (previous kept at `<schema>.bak`), drains
  through the normal shutdown (`/readyz`→503) and RE-EXECS the process
  (supervisor-agnostic, same PID, ~6 s); an invalid schema is a 422 with NOTHING
  written and no restart, and a relaunch that can't load the schema auto-restores
  the `.bak` (marker-gated, restart.go). The editor polls `/readyz` and verifies
  the new resources are served before declaring it live. **Relations authoring
  (UI-F4-S3):** the `relations` block (?include= embeds) is fully AUTHORABLE in
  the entity panel — kind (has_many/belongs_to/many_to_many) with per-kind
  conditional fields faithful to `validateRelations` (fk = a column ON the
  target / an OWN column / a column of the `through` junction; through +
  target_fk m2m-only; `limit`), every target/column a dropdown over EXISTING
  entities/fields, live issues mirroring the validator. This closed the last
  parity gap from AUDIT-F1-S1: the editor authors 100% of the effective schema
  surface. **Visual RBAC (UI-F2-S1):** a "Roles" button opens a
  full editor for the engine's RBAC grammar — both forms (per-resource `permissions`
  + legacy role-global), row conditions (field-dropdown of real fields + `id`, op
  fixed at `eq`, val `$user_id`/`$external_client_id`/literal), condition_actions,
  field allowlists, deny-by-default. It can only produce schemas the validator
  accepts (op=eq, existing fields), round-trips faithfully, and the roles enforce
  real security on deploy (row-level filter, field allowlist, 403). Architecture +
  how-it-grows: `pkg/editorui/web/ARCHITECTURE.md`.

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
- **Every change to the data path is verified with the binary-diff gate,
  because `make test` green is NOT a guarantee for it.** Proven 2026-08-01: a
  create-path type check that rejected the engine's own `default:"now"`
  injection — every POST omitting the field answered 422 for a field the caller
  never sent — shipped with `make test` green, because the DB-backed test
  covering it is skipped by the `-short` unit lane (CI's full lane runs it; the
  local fast lane does not). Two of that commit's four defects were found by
  DIFFING the old binary against the new one over paired requests, not by
  reading code. The technique is now infrastructure:
  `scripts/binary-diff-gate.sh <base-bin> <new-bin>` boots both binaries on
  scratch DBs, fires `scripts/binary-diff/corpus.jsonl` (a growable DATA file —
  add a case per contract you touch) at both, and reports every behavioral
  difference. **Every reported DIFF must be explained in the session report,
  case by case — an unexplained diff is a defect.** The bar for a data-path
  change is: unit test + the full lane (`go test ./...` without `-short`, or
  `make test-all`) + this gate. `make test` alone is the fast inner loop, not
  the verdict.
- **Verification sessions clean up their tenants.** A tenant created to probe
  a feature is deleted when the session ends: `appximo tenant delete <id>
  --yes` (DROP SCHEMA CASCADE + every control-plane row — history, flows,
  policies, outbox — no orphans; `appximo tenant list` is the inventory).
  Leftover test tenants pollute the editor's deploy modal and read as real
  apps. Do NOT delete `nimbus` or `acme` on the dev box — they are the
  bench/cert targets (`api-cert.sh` and `tests/performance/erp_*.js` default
  to nimbus; `sustained_2krps.js` defaults to acme).

## The open-item rule (non-negotiable)

**[docs/BACKLOG.md](docs/BACKLOG.md) is the single register of open items.** A
session that leaves anything undone — a deferred feature, a known limitation, a
decision not to build something — **records it there** with its origin, its
impact, and either a "ready" criterion (OPEN) or a written justification plus the
condition to reconsider (CLOSED, normally an ADR).

An item never lives only in a chat report. That is how the project accumulated a
dozen "I left it on purpose" notes scattered across sessions, each re-litigated
from memory, until LOOSE-ENDS-SWEEP-S1 had to consolidate them. There are exactly
three states — OPEN, CLOSED, DONE. "Pending, we'll see" is not one of them.

Before starting work, read the backlog: the item you are about to build may
already have a decision, and the reasoning may be the thing you need.

## The handoff-package rule (non-negotiable)

> `nuevo_chat_web/` is the maintainer's internal handoff package; it is **not
> part of the public repository**. If you are reading this from the public repo
> and the directory is absent, this section does not apply to you — it governs
> the maintainer's own working clone.

**`nuevo_chat_web/` is a LIVING artifact, not a snapshot.** It is the
strategic context — the plan, the decisions and their reasoning, the current phase,
the architect's role and tone, the servers — that lives in the *conversation* and
therefore evaporates when a chat gets too long. HANDOFF-PACKAGE-S1 captured it into
files precisely because it had already been lost once.

**At the end of EVERY session the agent MUST:**

1. Update **`nuevo_chat_web/04_ESTADO_ACTUAL.md`** — what this session did,
   what changed state, what the next session should pick up.
2. Update **`docs/BACKLOG.md`** — items opened, closed, or moved to DONE (the
   open-item rule above already requires this; it is restated because the two
   files are updated together or not at all).
3. Update **`nuevo_chat_web/03_DECISIONES_Y_PORQUE.md`** *if and only if* an
   architectural decision was made, reversed, or its reasoning changed — with the
   **why**, not just the what. A decision recorded without its reasoning gets
   re-litigated by the next chat, which is the exact failure this package prevents.
4. Bump the "última actualización" date in
   **`nuevo_chat_web/00_LEEME_PRIMERO.md`**.

`nuevo_chat_web/_COMO_MANTENER.md` maps change-type → which file to touch.
Skipping this is the same class of error as leaving an open item out of the
backlog: it does not fail today, it fails the next session silently.

The package lives **inside the repo**, so it is versioned with the code: it ships in
the session's own commit, it pushes with everything else, and
`git log -p -- nuevo_chat_web/` is the history of how the project's strategic context
evolved, session by session. Update it in the same diff as the work it describes —
not as a separate chore.

## Boundaries — do not

- **Never leave an item undone without recording it in docs/BACKLOG.md.** A
  finding that exists only in a session report is a finding the next session will
  rediscover from scratch (see The open-item rule above).
- **Never end a session without updating the handoff package** (see The
  handoff-package rule above). Strategic context that exists only in the chat is
  context that will be lost when that chat ages out — it already was, once.
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
  back over populated data instead of the old silent NULL-accepting divergence). A
  data-losing **drop** (DropTable/DropColumn) is gated by default but applicable
  through a controlled **approval gate** (`ApplyTenantMigrationApproved` /
  `PreviewTenantMigration`, MIG-F1-S3): a dry-run shows each drop's row-loss impact,
  and a drop runs only when its key is explicitly enumerated. `RunFanout` (MIG-F1-S4)
  is the **resumable multi-tenant orchestrator**: it applies a schema to N tenants,
  one at a time under each tenant's advisory lock, RESILIENT to partial failure (a
  broken tenant is recorded in `public.migration_log` and the rest continue) and
  RESUMABLE (re-running is a no-op for converged tenants — the idempotent diff makes
  "resume" == "run again"); additive by default, it never auto-approves a drop. See
  [Evolving a schema safely](#evolving-a-schema-safely--the-destructive-approval-gate).
- `pkg/outbox` — transactional outbox (ADR-016 §Class 2): `Enqueue` writes a job
  in the caller's tx and emits `pg_notify(outbox_notify, <id>)` on commit.
- `pkg/worker` — the outbox consumer behind the SEPARATE `cmd/appximo-worker`
  binary (not a goroutine in the engine): LISTEN/NOTIFY wake-up + poll fallback,
  `SELECT … FOR UPDATE SKIP LOCKED`, at-least-once (Processors must be idempotent).
  Run it with `DATABASE_URL=… go run ./cmd/appximo-worker`; the end-to-end proof
  is `scripts/worker-e2e.sh`. A consumer that writes results BACK does so through
  the engine HTTP API (`worker.EngineClient`), never the tenant DB directly, so the
  write inherits the engine's validation + RBAC. It mints a fresh, SHORT-LIVED
  (60s), SCOPED service JWT per operation (`auth.GenerateTokenWithTTL`, shared
  `JWT_SECRET`) carrying the event's `tenant_id` and a **scoped service role** —
  never admin. `APPXIMO_WORKER_WRITEBACK=on` enables the demo consumer (PATCHes
  the created row's status); off by default (echo).
- `pkg/consumers` — real business-logic Processors (kept OUT of `pkg/worker` so the
  core loop stays dependency-light; e.g. excelize lives only here). The first is
  the XLSX consumer (`XLSXProcessor`, FileJob pattern): on `{resource}.created` it
  fetches the job, STREAMS the referenced XLSX (excelize `f.Rows` iterator — never
  `GetRows`, never the whole file in RAM), computes an aggregate, and writes
  `{status, result}` back via the engine API. A corrupt/invalid file is a PERMANENT
  failure (job → `failed`, event acked — worker never crashes); a transient engine
  error keeps the row pending for retry. Idempotent: a job already terminal is
  skipped. The `file_ref` is resolved through `pkg/files`: set `APPXIMO_FILES_DIR`
  and the consumer treats `file_ref` as a VFS `file_id`, streaming the
  content-addressed blob via `VFS.Get`; unset, it reads `file_ref` as a local path
  (back-compat). The second consumer is the EMAIL consumer (`EmailProcessor`): on an
  `email.send` event it renders an `html/template` (stdlib, auto-escaped; built-ins
  `verification`/`welcome`) and sends via an EXTERNAL SMTP provider (`SMTPSender`,
  net/smtp + STARTTLS, env-configured — Brevo/Resend/Mailgun/SES) with no engine
  write-back. At-least-once ⇒ a rare double-send is accepted for transactional mail
  (documented), mitigated by a deterministic Message-ID per outbox row. Select
  consumers via `APPXIMO_WORKER_MODE=echo|writeback|xlsx|email` (default echo). A
  single-mode worker ACKS topics it doesn't own, so DON'T run two different modes
  against one outbox (silent event loss under SKIP LOCKED) — for multiple event
  types compose a `consumers.Router` (topic → Processor) in one dispatching worker
  and scale that (ADR-016 library model).
- `pkg/files` — content-addressable file store with INTERCHANGEABLE backends
  (FILES-V2, the PocketBase pattern): a thin owned `Backend` interface
  (Put/Get/Delete/Stat/List/Serve/SignedURL over validated CAS keys
  `<tenant>/<aa>/<bb>/<sha>`) under a shared `Store` that owns everything that
  must be identical across drivers — metadata AUTHORITATIVE in the per-tenant
  `tenant_<id>.files` table, tenancy checks, SHA-256 streaming hash (64 KiB
  `io.CopyBuffer`, NEVER `io.ReadAll`) + dedup, and the OWASP upload validation
  (extension ALLOWLIST + magic-byte sniff of the first 512 bytes — the client
  Content-Type is never trusted — + `original_name` sanitized at rest, metadata
  only, never a path; rejection → 422). Drivers: `LocalBackend` (direct disk,
  deliberately NOT gocloud fileblob — `http.ServeContent` over `*os.File` gives
  Range/strong content-hash ETag/sendfile zero-copy, MEASURED live on the
  shipped path: 94% of nginx throughput at comparable CPU, 16.5k sendfile
  calls straced. FILES-BENCH found chi Compress's wrapper (no io.ReaderFrom)
  suppressing sendfile; FILES-FIX-SENDFILE routes the byte-serving routes
  around the wrapper (`middleware.SelectiveCompress` +
  `files.IsByteServingPath`, pinned by tests — JSON stays compressed); see
  docs/FILES.md §Performance. Atomic temp+rename, interrupted uploads leave
  nothing) and `S3Backend` (gocloud.dev
  s3blob — R2/Spaces/MinIO/AWS by config: endpoint/region/creds/bucket/
  forcePathStyle; automatic multipart; serve = 302 to a short-lived presigned
  URL by DEFAULT — the FILES-V1 contract: authorize, never proxy — or `proxy`
  mode). Signed access: `GET /api/files/{id}/url` mints a short-lived (~180 s)
  URL — native presigned on S3; on local an engine HMAC-token URL
  (`GET /files/signed/{token}`, JWT-skipped, tenant-bound, role re-checked at
  serve, ANY failure a uniform 404). Routes `POST /api/files`,
  `GET|DELETE /api/files/{id}`, `GET /api/files/{id}/url` flow through the
  shared chain (RBAC create/read/delete on `files`); downloads bypass the
  response cache (same bypass as SSE). BOTH drivers pass one conformance suite
  (S3 against real MinIO: `go test -tags integration -run TestS3 ./pkg/files/`).
  Operator doc + the three setups (local / R2 / MinIO): docs/FILES.md.

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
`appximo validate`. **A change to the data path additionally requires the
full lane (no `-short`) and the binary-diff gate** — see the convention above;
`make test` green has already shipped a broken POST once.

---

# Integration guide — helping a user adopt Appximo

This half is for when a user wants Appximo to serve **their** API and
you are writing their schema and calling their endpoints. The surface
below is complete and verified — if something is not listed, the engine
does not support it (see [Does not exist](#does-not-exist--do-not-invent)).

## Writing a schema

One JSON file. `$schema` and `version` are **required** (load fails
without them). A complete, working example:

```json
{
  "$schema": "https://appximo.com/schema/v1",
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

**Validation answers "may this run?"; WARNINGS answer "will this do what you meant?"**
(SCHEMA-5). `schema.Warnings` is a separate, NON-BLOCKING layer for schemas that are
valid, deployable and almost certainly wrong. Its first rule: a row condition
comparing `$user_id` (or `$external_client_id`) against a column declared as a
`relation` — the JWT subject and a foreign key to another resource are different id
spaces, so the rule matches **zero rows forever** with no error at any layer (measured
in production: the app just shows nothing). It is a warning, not an error, because the
pattern is legal when the FK genuinely holds login ids — and it is SUPPRESSED when the
relation points at a column named `user_id`/`auth_user_id`/`login_id`, i.e. once you
apply the suggested fix. Warnings surface in `appximo validate`, `validate --json`
(`warnings[]`, with `valid` untouched, so the AI correction loop can act on them), the
control-plane / `/admin` deploy response, engine boot, and `ai-generate`'s report.

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
| `json` | TEXT (stored as text) | `eq` only (exact match on the stored text) |
| `jsonb` | JSONB (a real document) | `eq` only — but `@>` containment via a `gin` index |
| `file` | UUID + a real FK to the tenant's `files` table | `eq` |

**`json` vs `jsonb`** (LIBRARY-GAPS-S1): `json` is stored as TEXT — exact bytes,
nothing queryable. `jsonb` is a real Postgres document: it is the only type a `gin`
index may cover (`{"fields":["attrs"],"method":"gin","opclass":"jsonb_path_ops"}`),
which turns `attrs @> '{"brand":"Acme"}'` into an index lookup. **Prefer `jsonb`**
for anything you might query; pgx decodes it into a Go map and encodes one back.
Neither can be a `group_by` key. In GraphQL `jsonb` is the `JSON` scalar (the
document itself), `json` stays `String`.

**Money has no type of its own** — and shouldn't: use `int64` in the currency's
MINOR unit with the unit in the name (`price_cents`, `total_cents`). `float64`
money is a rounding bug, and most payment APIs already speak minor units.

A **`file` field** (FILES-LINK-S1) attaches an uploaded file to a RECORD,
first-class: it stores the `file_id` that `POST /api/files` returns, and the
engine enforces (via the FK, per tenant) that the value references an existing
file — a nonexistent or other-tenant id is a **422**
`{"error":"validation_failed","fields":[{"field":"<name>","rule":"file_not_found",…}]}`
on REST and GraphQL. Flow: upload → take `file_id` → set it like any field value
(create/update); read returns the id, the bytes come from `GET /api/files/{id}`
/ the signed URL. `on_delete` (of the FILE): `restrict` (default — deleting a
still-attached file is a 409) or `set_null` (deleting the file nulls the field);
`cascade` is rejected at load. Deleting the RECORD never deletes the file.
`relation`/`references`/`on_update`/`enum`/`default`/`auto` are all rejected on
a file field. In GraphQL the field is an `ID`.

A file field may declare a **per-field attach policy** (FILES-1,
THIRD-PARTY-READY-S1): `accept` (a content-type family `image`|`audio`|`video`|
`text`, the alias `pdf`, or an exact type like `application/zip`; a single
string or an array) and `max_bytes` (> 0). Enforced at **attach time** on
REST create/update, GraphQL mutations, `/api/transaction` and `Ctx.Insert/
Update` — the referenced file's STORED metadata (magic-byte-sniffed content
type + size, never the client's declaration) is checked, and a violation is
the standard 422 with `rule: "file_policy"` naming what the field accepts.
Both keys are file-field-only (rejected elsewhere); existence stays the FK's
verdict (`file_not_found`); the instance env knobs remain the outer bound at
upload. A resource with no policy-declaring file field pays a field scan and
zero queries.

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
- `renamed_from: "<old_name>"` — declares the field's PREVIOUS column name
  (MIG-F1-S2). The migration engine renames the column with `ALTER TABLE …
  RENAME COLUMN` (metadata-only — the **data stays in the renamed column**,
  accessible under the new name, and the column's FK / unique / index follow it)
  instead of the old converger's drop+add that stranded the data. The old name
  must NOT still be a declared field (validated at load). Once applied the intent
  is **inert** — re-provisioning with `renamed_from` still present is a no-op (the
  old column no longer exists), so it is safe to leave in the schema. A table
  (resource) rename is the same key at the resource level:
  `"clients": { "renamed_from": "customers", "fields": {…} }`.
- Validation rules (all optional, compiled at load; a bad rule rejects
  the schema with a clear error):
  - numeric fields: `min`, `max`
  - string/text fields: `minLength` / `maxLength` (rune count),
    `pattern` (RE2 regex, ≤ 200 chars), `format` — exactly one of
    `email | uuid | url | date`

A write that violates rules returns **422 with every failing field at
once**: `{"error":"validation_failed","fields":[{"field":"title","rule":"required","message":"is required"}]}`.

Values are **type-checked against the declared type on create as well as update**
(`"rule":"type"`), reported together with the declared-rule failures so one
response still carries every failing field. Before ADR-024 only update was
checked: `POST {"amount": 1.9}` on an `int64` field returned **201** and stored
`1` — silent truncation reported as success — while `PATCH` with the same value
returned a clean 422.

Update answers the **same S44 shape** (ENG-29): a PATCH/PUT violation — wrong
type, unknown field, `id`/auto field in the body, missing required on PUT — is
`{"error":"validation_failed","fields":[…]}` with every offending field at once
(rules: `type`, `unknown_field`, `read_only`, `required`), identical on REST,
the GraphQL `update…` mutation (in `errors[].extensions.fields`) and a batch
transaction op. It used to be a single flat string, so a client could not parse
both verbs with one code path and the documented `ValidationErrorResponse` was
false for updates.

The one remaining create/update difference is deliberate:

- **A JSON string defers to Postgres on create, and is rejected on update.**
  `POST {"amount": "7"}` → `201` (Postgres parses it; form-encoded and
  spreadsheet-derived clients send every scalar this way, and refusing them would
  be stricter than the database — ADR-024). `PATCH {"amount": "7"}` → `422`. The
  create path has always been the more permissive of the two; the type check
  preserved that rather than widening or narrowing it.

A key that is **not** a declared field still passes through to Postgres (so a
column added by a migration is writable without a restart, ENG-12) and comes back
as the `unknown_field` 422; an explicit `null` is governed by `required`, not by
the type check. `time` values remain validated by Postgres rather than in Go — a
documented leniency, and the one place the two verbs genuinely agree on a
wrongly-typed value (both `400`).

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
  math. The documented pattern is a custom route per privileged transition (granted
  via the RBAC `routes` block), and since LIBRARY-GAPS-S2 that route is CHEAP:
  `Ctx.Update` enforces the declared state machine itself (same guard, same 422),
  so the handler only decides WHO may move — never re-states WHAT moves exist.
  The single-op update path without a state machine is unchanged (measured
  `no_change`). Example: [examples/model-lab/state-machine.json](examples/model-lab/state-machine.json).

### Relations

```json
"customer_id": { "type": "uuid", "relation": "customers", "on_delete": "restrict" }
```

generates one read-only route — `GET /api/orders/{id}/customer` (field
name minus `_id`) — returning the referenced record, AND a **real Postgres
foreign key** on the column (MIG-F1-S1): `customer_id` → `customers.id`. The
subroute enforces the role's RBAC on the **referenced** resource (SEC-AUDIT-V1):
`read` on the target is required (else `403`), the target's row condition scopes the
result (a hidden row is `404`), and the target's field allowlist applies — the same
scoping `GET /api/customers` and the `?include=` embeds apply, so the subroute never
exposes a row/field the role could not otherwise read.

- **`on_delete`** declares the referential action when the referenced (parent)
  row is deleted: `restrict` | `cascade` | `set_null`. **Unset defaults to
  `restrict`** — the safe choice: a delete of a still-referenced row is rejected
  with **`409 Conflict`** (`{"error":"cannot delete: still referenced by \"orders\"
  record(s)"}`, REST and GraphQL) instead of silently orphaning its children.
  `cascade` deletes the children; `set_null` nulls the FK (the column must be
  nullable — `set_null` on a `required` field is rejected at load). The FK column
  is auto-indexed (the RESTRICT check is an index lookup).
- The field-level `relation` is the source of the FK; a `many_to_many` junction's
  FK columns are themselves field-level relations, so the junction gets integrity
  too. It still does **no joins in list queries and no nested writes** (those are
  the opt-in `?include=` embeds below).
- **`on_update`** (MIG-F1-S5) declares the action when the referenced row's KEY
  changes: `restrict` | `cascade` | `set_null` (same vocabulary as `on_delete`).
  **Unset defaults to NO ACTION** — deliberately, NOT `restrict`: an FK created
  without `ON UPDATE` already carries NO ACTION in Postgres, so adding `on_update`
  to a schema generates **zero churn** on existing tenants (a re-provision stays a
  no-op). `set_null` requires a nullable column.
- **`references`** (MIG-F1-S5) points the FK at a target column **other than `id`**:
  `{ "type":"string", "relation":"users", "references":"email" }`. **Unset defaults
  to `id`** (total retrocompat). The named column must be `id` or a **`unique`**
  column of the target (Postgres requires a unique/PK destination) and
  type-compatible — both checked at load. The read subroute follows the FK to that
  column.
- **Existing tenants**: the FK is added `NOT VALID` (protecting all NEW writes
  immediately) then `VALIDATE`d; if pre-existing rows are inconsistent (historical
  orphans), VALIDATE is skipped and the FK is left `NOT VALID` (forward-protected,
  logged) rather than breaking provisioning. A cascade/set-null does NOT fire the
  child's outbox `…deleted`/`…updated` event (Postgres handles it below the engine).

#### Composite foreign keys (`foreign_keys`, MIG-F1-S5)

A single-column FK is the field-level `relation` above (1 column → the target's `id`
or a `unique` column). A **multi-column** FK — `(col_a, col_b)` referencing a
target's **composite** PK/unique — does not fit the field-level model, so it is a
resource-level `foreign_keys` array (sibling of `fields`):

```json
"orders": {
  "fields": { "region_code": { "type": "string" }, "branch_code": { "type": "string" } },
  "foreign_keys": [
    { "columns": ["region_code", "branch_code"], "target": "branches",
      "ref_columns": ["region_code", "branch_code"],
      "on_delete": "cascade", "on_update": "restrict" }
  ]
}
```

- `columns` (source columns on this resource) and `ref_columns` (target columns)
  must have the **same length**; `ref_columns` must together form the target's
  **primary key or a `unique` constraint/index** (a composite `unique` index in the
  target's `indexes` block — Postgres requires a unique destination). Both, plus
  per-position **type compatibility**, are validated at load.
- `on_delete` defaults to `restrict` (safe); `on_update` defaults to NO ACTION
  (no-churn). `set_null` on either requires every source column nullable.
- The source columns are auto-indexed (a composite btree) so the referential check
  is an index lookup. A violation is the same clean **`409`** as a single FK.

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
`$user_id` resolving to the JWT subject (or `$external_client_id`, or a
literal). The JWT `role` claim selects the policy. **Those are the only two
variables**: any other `$…` val (`$userid`, `$tenant_id`) **rejects the schema
at load** listing them (ENG-20 — it used to be compared as the literal text, so
one typo produced "zero rows forever" with no error anywhere, invisible even to
the SCHEMA-5 warning). A bare `user_id`/`external_client_id` (the `$`
forgotten) is legal — a literal — but raises a WARNING suggesting the variable.

A row `condition`'s **`op` must be `eq`** (or omitted) — row conditions are
enforced as equality, so a non-eq operator is **rejected at schema load**
(SEC-AUDIT-V1) rather than silently ignored ("declared == applied"; richer
operators are a future increment). Field/condition existence is validated at load
for **both** the role-global and per-resource forms: `conditions.field` and each
`fields` entry must name a real column of the role's resources, or the schema is
rejected (a typo is caught at load, never a runtime error).

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

#### A role-global condition must exist on EVERY listed resource (LIBRARY-GAPS-S1)

A role-global `conditions` is injected into the WHERE of **every** resource the
role lists. Validation used to accept a column present on only ONE of them (a
documented limitation), so this validated and then broke at request time on the
resource that lacked it — the engine's own shipped `testdata/logistics` fixture
carried exactly that bug (`operario` scoped by `operator_id` over `incidents`,
which has no such column → `GET /api/incidents` answered `422 unknown_field`,
blaming the CALLER for a schema misconfiguration). It is now a **load error**
naming the resources that lack the column, with the fix pointing at per-resource
`permissions`. A virtual custom-route segment in the list is skipped (not a table).
The `fields` allowlist keeps the union rule — it is a projection filter, so a field
missing on one resource simply projects nothing there.

#### Granting CUSTOM routes: the `routes` block (LIBRARY-GAPS-S1, ADR-021)

A custom route (an endpoint a Go backend registers with `appximo.Route`) is
authorized by its **first `/api/` segment** as a VIRTUAL resource. A role grants one
with `routes`, a map of segment → `{actions}`:

```json
"cliente": {
  "permissions": { "ordenes": { "actions": ["read"],
      "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } } },
  "routes": { "checkout": { "actions": ["create"] } }
}
```

- **Orthogonal to `resources`/`permissions`** (a different namespace: endpoints,
  not tables), so a role may declare both. That is the whole point — before it, a
  role using per-resource `permissions` could reach NO custom route, making
  "owner-scoped end users + a custom action endpoint" inexpressible.
- **No `conditions`, no `fields`** — a segment has no rows and no columns;
  declaring either is a load error explaining why. The DATA a handler touches is
  authorized separately (`Ctx.Query`/`Insert`/`Update` re-evaluate the role against
  the real resources).
- **Authoritative but never widening:** a listed segment is decided by its entry
  (so it can NARROW a wildcard role); an unlisted one falls through to normal
  evaluation, so deny-by-default is intact.
- **Boot-validated against the REGISTERED routes:** a grant for a segment nothing
  serves — or an action no registered method provides — fails the boot with the
  registered segments listed (also checked on `POST /admin/engine/schema`, so a
  Studio deploy gets a clean 422 instead of a restart+rollback). Consequence: the
  pure `serve` binary, which registers no custom routes, refuses to boot a schema
  that grants one.
- Hot path: one `len()` on a nil map for every role without `routes` — measured
  `no_change`.

### Hooks (lifecycle extensions)

Declared per resource under `hooks`. Events are exactly
`before_create | after_create | before_update | after_update` — there
are **no delete hooks**, and any other event name is rejected at
validation. **After-hooks (`after_create`/`after_update`) must be `webhook`**:
a `js`/`wasm` after-hook is rejected at load (a sandboxed hook runs post-commit
with no way to change the row or reach out — it would be a silent no-op; put
js/wasm logic in a `before_*` hook). **Before-hooks must be `js` or `wasm`** —
the exact mirror (ENG-19): a `webhook` before-hook used to VALIDATE (its URL was
even required) while the runner never dispatched it — a declared guard rail that
silently never ran. A before-hook must decide the write synchronously, which an
async signed POST cannot, so the type is rejected at load; notify externally
with an after-webhook or the events outbox. Three hook types (all fields below
verified against `pkg/schema/types.go`):

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
  `data` is the record; `user` is the actor (`user.user_id`/`role`/`tenant_id`
  from the JWT claims); setting `result.proceed = false` + `result.error`
  rejects the write with 422. Built-ins available: `validateNIT`,
  `calculateCUFE`, `isValidEmail`, `formatMoney`. (`before_*` only.)
- `webhook` — async signed POST: headers `X-Appximo-Event` (the real event:
  `after_create` or `after_update`) and
  `X-Appximo-Signature: sha256=<hmac>`; 3 retries with backoff.
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
  `cmd/appximo-worker` consumes it (`SELECT … FOR UPDATE SKIP LOCKED`,
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
  and columns (`idx_<table>_<cols>` / `uniq_<table>_<cols>`), with a method suffix
  for a non-btree index (`idx_<table>_<cols>_gin`).
- **`method`** (LIBRARY-GAPS-S1): `btree` (default — an omitted key is
  byte-identical to before) or `gin`. A `gin` index is accepted ONLY over `jsonb`
  columns and NEVER with `unique`; both are rejected at load, never a Postgres
  error mid-migration. **`opclass`** (gin only, closed allowlist): `jsonb_ops`
  (default) or `jsonb_path_ops` (smaller/faster, indexes ONLY containment). The
  opclass is excluded from the migration diff (it cannot be introspected back), so
  declaring one causes no churn.
- A partial index (a `WHERE` predicate) does NOT exist and is deliberate: Postgres
  normalizes predicate text, so round-tripping one would drop+recreate the index on
  every migration. Create it in your own boot DDL (`Config.BeforeStart`); the
  additive migration leaves it alone.
- Every referenced column's existence is checked against `information_schema`
  first; an index naming a column that does not exist (yet) is **logged and
  skipped**, never a hard failure (columns can be added to the live table at
  runtime — same contract as relation FK indexes).
- Validated at load: at least one field per index, each a valid field name.
- Relation FK columns are auto-indexed separately (see
  [Declarative relations](#declarative-relations--nested-embeds-relations-adr-019));
  you do not need to declare those by hand.

### Evolving a schema safely — the destructive approval gate

When you change a tenant's schema (control-plane `PUT /tenants/{id}/schema`, or the
`migrate` CLI), the engine diffs the new schema against the live database and applies
the change through the production-safe migration engine. The policy is **additive by
default**: it creates/adds/alters/renames but NEVER drops — a removed field's column
or a removed resource's table stays as **drift** (logged, no data lost). To actually
remove something, you go through a two-step **approval gate** so a data-losing drop
can never happen by accident.

A **destructive** operation is one that loses data: dropping a **column** (a removed
field) or a **table** (a removed resource). Each has a stable approval **key**:

- a column → `"<table>.<column>"` (e.g. `"empleados.telefono"`)
- a table  → `"<table>"` (e.g. `"proyectos"`)

(Removing an *index* or a *constraint* loses no row data; those are not approvable in
v1 — they simply stay as additive drift.)

**Step 1 — dry-run (informed consent).** Ask what the change would do, applying
nothing:

```
PUT /tenants/{id}/schema   { "schema": {…}, "dry_run": true }
```

returns a classified plan: `apply` (the safe ops that will run), `destructive` (each
data-losing drop with its **impact** — `rows_lost` of `table_rows`, the rows whose
data would be destroyed — and `approved:false`), `drift` (safe drops left as drift),
and `concerns` (backfill/type-change risks on existing data). It writes nothing.

**Step 2 — apply with enumerated approval.** Re-send WITHOUT `dry_run`, listing the
exact keys you approve:

```
PUT /tenants/{id}/schema   { "schema": {…}, "approved_drops": ["empleados.telefono"] }
```

ONLY the enumerated drops execute (response: `applied_drops`); every other drop stays
gated (`gated_drops`). A key that matches nothing is reported (`unmatched_approvals`),
never silently assumed. There is **no global "drop everything" flag** — approval must
name each operation, so you confirm exactly what you understood from the dry-run.

**CLI** mirrors this: `appximo migrate --tenant <id> --schema <file> --dry-run`
prints the same plan + impact; `--approve-drops "empleados.telefono,proyectos"`
applies the enumerated drops. Without `--approve-drops`, destructive drops are gated.

Guarantees:

- **Default-gated, fail-safe.** No approval ⇒ nothing is dropped (identical net effect
  to the additive default). Zero data loss by accident.
- **The worker never auto-approves.** Tenant registration and the async Redis
  migration worker use the purely additive path — an automated process can only add;
  a data-losing drop requires explicit, recorded human consent through `PUT` / the CLI.
- **Preview↔apply is faithful.** The plan is recomputed against the live DB at apply
  time, and a drop runs iff its key is approved. A destructive that appeared since the
  dry-run carries a different (un-approved) key, so it is gated automatically — a new,
  unreviewed drop can never slip through between preview and apply.
- An approved table drop also removes any foreign key that still references it (a
  required, data-safe consequence), so the `DROP TABLE` succeeds rather than failing on
  a dangling reference.
- **DECLARED == APPLIED, verified against the DATABASE (ENG-13).** After every apply
  the engine RE-INTROSPECTS the live schema and re-diffs: anything the schema declares
  that the database does not have comes back in `ApplyOutcome.Unapplied`, and
  `Partial()` is true. **A partial apply is a FAILURE in every surface** — the CLI
  prints what did not land and exits non-zero WITHOUT persisting the schema, the
  control plane / `/admin` PUT restores the tenant's previous schema and answers 422,
  and the fan-out marks the tenant failed (so a re-run retries it). The migrator never
  grades its own work: the verdict comes from the catalog, not the log.
- **An FK whose DEFINITION changed is a REPLACEMENT, not a drop.** Changing
  `references` / `on_delete` / `on_update` on an EXISTING relation emits a
  DropForeignKey + AddForeignKey over the same `(table, columns)`; the drop is un-gated
  (dropping a constraint loses no row data) and both halves run in ONE transaction, so
  a failure rolls back to the old constraint and the column is never left unprotected.
  Before this, the drop was gated as drift and the add collided with the surviving
  constraint's generated name (`42710`) — the change was a **silent no-op** while the
  tenant's recorded schema claimed the new shape, and the stale FK then BLOCKED the
  data migration the new schema required. The whole class is audited in
  [docs/audits/MIGRATION_HONESTY_AUDIT.md](docs/audits/MIGRATION_HONESTY_AUDIT.md).
- **A `renamed_from` that cannot run is reported, not swallowed.** When BOTH the old
  and new names already exist the rename cannot happen; it now surfaces as a blocked
  rename (a `[blocked]` concern in the dry-run, and `Unapplied` on apply) instead of
  leaving the values in the old column while the schema claims they moved.
- **An FK left `NOT VALID`** (added over rows that already violate it — new writes ARE
  checked, historical rows are not) is reported in `ApplyOutcome.UnvalidatedFKs` and
  printed by the CLI in plain language, not only logged.

#### Version history + rollback (VERSION-S1)

Every persisted tenant schema (register / deploy / rollback / fan-out) is recorded in
**`public.schema_history`** — append-only, one row per DISTINCT schema (canonical-hash
dedup: re-applying an unchanged schema adds no version), backfilled at boot for
pre-versioning tenants. Read it via `GET /admin/tenants/{id}/schema/history` (and
`…/history/{version}` for one full schema). **Rollback** is
`POST /admin/tenants/{id}/schema/rollback` with `{version, dry_run, approved_drops}` —
a RE-DEPLOY of the stored version through the same dry-run → destructive-gate → apply
flow above (NOT a second engine): what later versions added reverts as gated drops
(measured `rows_lost`, enumerate to approve), data already lost to an approved forward
drop is NOT recovered, and the applied rollback appends a NEW version whose content is
the target's (the trail is never rewritten). Studio's **History** view is the UI: the
timeline, view any version, and the rollback with the same honest preview.

#### Flow tests + post-deploy regression (FLOWTEST-S1)

A tenant can persist **multi-step flow tests** — "flows as data" (the api-cert model
formalized): each step is a request (method/path/body/headers or a multipart upload)
plus assertions (`status`, and `exists|eq|contains` over dot-paths) plus **captures**
that flow state into later steps (`{{token}}` from a login, `{{cita_id}}` from a
create; `{{run_id}}` is unique per run). A flow authenticates as a TENANT user — a
real `POST /auth/login` step, or a flow-level `role` that pre-mints a tenant JWT —
never the super-admin, so it exercises the real RBAC. CRUD at
`/admin/tenants/{id}/flows` (+`/{fid}`); `POST …/flows/run` (suite) or
`…/flows/{fid}/run` executes against the app's LIVE router in-process (the full
tenant→JWT→RBAC chain, current surface even after a hot-swap), streams each step's
PASS/FAIL over SSE, and persists the verdict in `public.flow_runs` **anchored to the
schema version** it ran against (`GET …/flows/runs`). The golden loop: deploy →
re-run the suite → green means the change broke none of the known flows; red names
the exact step with expected-vs-got. Studio's **Flows** view authors and runs them
("Run regression flows" on the deploy result).

#### Applying a change to ALL tenants — the resumable fan-out

Everything above migrates ONE tenant. To roll a schema change out to **every** tenant
(or a subset), the `migrate` CLI fans out:

```
appximo migrate --all-tenants  --schema base.json --dry-run   # plan + AGGREGATE impact, applies nothing
appximo migrate --all-tenants  --schema base.json             # apply to every tenant
appximo migrate --tenants a,b  --schema base.json             # apply to a subset
```

It enumerates `public.tenants` and migrates each one **sequentially**, under that
tenant's advisory lock (the same lock the Redis worker uses, so the two never collide).
The properties that make it safe at scale — the differentiator over Prisma (no native
multi-tenant fan-out) and django-tenants (fragile on partial failure):

- **Resilient to partial failure.** If tenant K fails (e.g. a NOT NULL add over its
  existing rows), it is left in its previous state (the Executor rolls back per batch —
  never half-applied), the failure is recorded in `public.migration_log`, and the
  fan-out **continues** with the rest. One broken tenant never blocks the healthy ones.
  The command exits non-zero and the summary names the failed tenants.
- **Resumable.** Re-run the SAME command to resume: already-migrated tenants produce an
  empty diff (a no-op, skipped for free) and only the failed ones retry. The idempotent
  diff makes "resume" == "run again" — no separate state machine.
- **Additive by default; never auto-approves a drop.** With no `--approve-drops`, every
  destructive drop is gated in every tenant (zero data loss). A MASS destructive change
  requires `--approve-drops` (the enumerated keys apply to EVERY targeted tenant), and
  the `--dry-run` reports the **aggregate** impact first (rows lost × tenants).
- **Tracked.** Each tenant's outcome (applied / no-op / failed, with the error) is
  written to `public.migration_log`, stamped with the run id, so the run state is
  consultable and a resume is informed.

A successful per-tenant apply also persists the schema to that tenant's record (so a
fan-out is the "propagate this schema to N tenants" operation; scope custom tenants out
with `--tenants`). Bounded parallelism is a documented future increment — v1 is
sequential (safe on a small box).

## Running it and making the first call

Fastest path is the published Docker image — the four copy-paste
commands (register tenant → mint JWT → POST → filtered GET) are in the
[README quick start](README.md#quick-start-30-s-with-the-image-pull).
The **official production path** is the one-command installer
`scripts/install.sh` (empty VPS → HTTPS app: native PostgreSQL + systemd +
Caddy — Docker is a documented variant, not the default), fully walked in
[docs/PRODUCTION.md](docs/PRODUCTION.md); updates are
`scripts/deploy-update.sh` (atomic swap + auto-rollback), backups
`scripts/backup.sh`. The lower-level three-tier path (compose / native by
hand) is [docs/DEPLOY.md](docs/DEPLOY.md). Two production facts worth
knowing: `appximo serve` **self-bootstraps the control-plane tables** on a
fresh empty database (no manual `migrations/001_control_plane.sql`), and
`GOMEMLIMIT` auto-detects an explicit cgroup limit (90%) when unset — set it
explicitly on a bare small box (the installer does). Don't re-derive the
first-call commands — they are verified verbatim.

Facts agents most often get wrong:

- **Tenant = Host header.** Every data-plane request needs
  `Host: acme.localhost` (or a real subdomain). Host of a different
  tenant → 401 `token tenant mismatch`; Host with no subdomain (bare
  IP/`localhost`) → 500. The host is matched
  **case-insensitively** (RFC 9110 §4.2.3), so `ACME.localhost` resolves to the
  same tenant and the same `tenant_acme` schema; an invalid label is a 400 that
  names the label, the host and the rule (ADR-024 — it used to be a bare
  `invalid tenant`, and an upper-case host was refused outright).
- **`Authorization: Bearer <token>` — the scheme is case-insensitive**
  (RFC 9110 §11.1), so `bearer`/`BEARER` are accepted, as is more than one space
  before the token. This matters because the engine's own generated OpenAPI
  advertises `"scheme": "bearer"`, and a lowercase scheme used to be answered
  `401 invalid token`. A header carrying a *different* scheme (`Basic …`) now says
  so instead of blaming the token.
- **Tenant id rule: `^[a-z][a-z0-9]{1,29}$`** — a lowercase letter, then lowercase
  letters or digits, 2–30 chars. **No hyphens, underscores, uppercase or spaces.** It
  is the INTERSECTION of two alphabets because the id is used as both at once: the
  Postgres schema `tenant_<id>` (which forbids hyphens — a hyphenated id used to
  register and then fail every query) and the **Host subdomain** (which forbids
  underscores — an underscored id used to register, create its tables, and then answer
  `400 invalid tenant` on every request, while Studio's modal actively recommended
  one). One rule, three layers: the control plane rejects it with a 400 + a suggested
  fix that is itself valid, and Studio's deploy modal and the admin console mirror it
  live. **The id must also EQUAL the first label of the domain that serves the app** —
  a tenant `acme` is only reachable at `acme.<your-domain>`; creating one through
  `/admin` warns when it does not match, and a mismatched token now gets a `401` that
  names the host that arrived, the tenant it implies, the tenant the token carries and
  the address the token would work at. And tenant creation is **all-or-nothing**: if any step fails (including
  the migration, which runs after the registration tx), everything is rolled
  back — a failed create leaves no zombie tenant behind.
- **One API structure, N tenants with isolated data.** Routes, GraphQL types,
  RBAC, validators, hooks and `/docs` are compiled ONCE from the boot `--schema`
  and are identical for every tenant; only the DATA (and each tenant's physical
  table set, via per-tenant migrations) is per-tenant. A deploy (editor, control-
  plane `PUT`, or `migrate` — since CONSUMER-PATH-S1 ALL three persist the schema
  and notify the running engine) migrates ONE tenant's tables live, and a new
  column becomes **readable** hot (`SELECT *`) **and — since AUTHORING-GAPS-S1
  (ENG-12) — WRITABLE hot on REST**: the write path resolves the resource from
  the tenant's DEPLOYED schema (falling back to boot when nothing is deployed —
  `pkg/codegen/deployed.go`), and the new field's declarative rules are compiled
  when that schema loads. Verified live on a field asserted absent from the boot
  file: `PATCH` → 200, same PID, no restart (AI-JOURNEY-S1 had measured the
  OPPOSITE — the write path then validated only against boot — and that
  restriction is what AUTHORING-GAPS-S1 removed; docs/AUTHORING_JOURNEY.md 5-6
  records both halves). **Still restart-gated, and the engine now says so:** a
  NEW RESOURCE (its routes/GraphQL type/docs don't exist in the process — the
  API answers `resource_not_loaded` with the reason and the fix, not a bare
  404), GraphQL input types (a new field is not an argument the boot-compiled
  mutation parses), and everything else derived from the schema DEFINITION —
  filters/sort on the new field, `/docs`, RBAC, hooks — activates on a restart
  with the new schema — one click from the editor since UI-F4-S2 (graceful
  self-restart via `POST /admin/engine/schema`). The full verified model:
  [docs/MENTAL_MODEL.md](docs/MENTAL_MODEL.md).
- **JWT**: HS256 only, `exp` required, `role` claim must match a schema
  role. Mint dev tokens with
  `appximo token --secret "$JWT_SECRET" --tenant acme --role admin`.
  Add `--schema <file>` and the command **refuses a role the schema does not
  declare**, listing the declared ones (ENG-27) — an undeclared role is denied
  everything with the SAME `403 forbidden` a permitted-but-denied role gets
  (deliberately: naming the difference in the response would be a role-
  enumeration oracle), and the distinction appears only in the server log
  (`rbac: denied … role %q is not declared by any schema role`). Note the
  default `--role super_admin` is a convention many schemas do not declare.
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
  field or type-incompatible op → 400. **`id` (the implicit primary key) is
  filterable** — `?filter[id][eq]=<uuid>`, `eq` only — consistent with
  `?sort=id` and the cursors (ENG-26).
- **Search**: `?search=term` runs a case-insensitive substring match
  (`ILIKE %term%`, `%`/`_` escaped) across **only** the resource's
  `string` and `text` fields, OR-ed together and AND-ed with any
  filters. It does **not** touch `int`/`int64`/`float64`/`time`/`uuid`/
  `bool`/`json` fields, and it is a no-op (ignored) on a resource with no
  string/text fields. It is a plain `ILIKE`, not a ranked/full-text
  search engine.
- **Sort**: `?sort=field&order=asc|desc` — **one field only**. The
  alternative `?order[field]=desc` also works and wins when both are
  sent. An unknown sort field, or a direction that is not `asc`/`desc`, is a
  **400 naming it** (ADR-024) — both used to be silently ignored, which is why
  this line carried a "verify result order, don't trust the param" warning. The
  warning is gone because the behavior that needed it is gone. An **empty**
  `?sort=`/`?order=`, and `?order=desc` with no `?sort=` (a direction naming no
  field), are also named 400s now (ENG-30) — they used to default silently.
  Multi-field sort is still unsupported, and **two `order[…]` parameters are a
  400 naming both** (ENG-16 — the winner used to be Go map iteration order,
  measured flipping 174/26 between identical requests; same rule on the GraphQL
  `order` argument).
- **A repeated engine-owned parameter is a 400 naming it** (ENG-17):
  `?per_page=20&per_page=100` used to serve 20 — the FIRST value won and the
  caller's appended correction was silently dropped. Applies to every parameter
  the engine reads (`page`, `per_page`, `sort`, `order`, `order[…]`,
  `filter[…]`, `search`, `after`, `before`, `count`, `include`, the aggregate
  functions); unknown top-level parameters keep their tolerance.
- **Pagination**: keyset — `?after=<uuid>` / `?before=<uuid>` with
  `?per_page=` (default 20, max 100). `?page=` exists but is
  OFFSET-based; prefer keyset. **A cursor request owns its shape** (ENG-15):
  `after`+`before` together, cursor+`sort`/`order[…]`, cursor+`page` and
  cursor+`count` are each a **400 naming the conflict** (they used to be
  silently discarded — and `meta.page` still echoed the page it ignored), and an
  empty `?after=`/`?before=` is a named 400 instead of a silent full list. A
  cursor response's `meta` carries `per_page` + `has_next` only — **no
  `page`/`has_prev`** (a cursor request has no page number; meta never reports a
  page the query did not use). A **non-positive** `page`/`per_page` is a **400
  naming the value** — `?page=0` and `?page=-4` used to be served silently as
  page 1, even though the engine's own message for `?page=abc` already said
  *"must be a positive integer"* (ADR-024). An **empty** `?page=`/`?per_page=` —
  what an empty form field produces — is the same named 400 (ENG-30); it used to
  be silently served as the default. Over the cap it still **clamps**,
  because "max 100" is documented and `meta` reports the effective value —
  reported tolerance, not silence.
- **Filter values**: an empty value on a non-text field
  (`?filter[amount][gte]=`) is a **400 naming the field and its type**; it is
  legitimate on `string`/`text` (it asks for the empty string). A wrongly-typed
  value is a **400 naming the parameter, the value and the expected type**
  (ENG-25): `filter[amount][gt]: "abc" is not a valid int64 value`. The
  acceptors reproduce **Postgres's** input grammar, never Go's — `yes` is a
  boolean, integers tolerate whitespace/sign/PG16 literal forms, floats accept
  `Infinity`/`NaN` — pinned by a live conformance test so no working request is
  rejected. The one type NOT validated in Go is **`time`** (its Postgres grammar
  is too wide to reproduce safely): a garbage time value stays an anonymous 400,
  the documented exception.
- **Total count (opt-in)**: `?count=true` on a list adds `meta.total` +
  `meta.total_pages` (a `COUNT(*)` over the SAME filtered + RBAC-scoped set).
  **Off by default** — the plain list pays nothing and is byte-identical; turn it
  on only when you need the total. The flag is read **by value** (ENG-23):
  `count=false`/`count=0` are OFF (they used to turn the total ON — the test was
  presence-only), bare `?count` and `count=true`/`1` are ON, anything else is a
  400 naming the value. It **works with `?include=`** (the embed path used to
  drop it), a **failed COUNT is an error response**, never a 200 with the total
  silently missing, and count+cursor is a named 400 (the COUNT would cover only
  the rows past the cursor — a total that silently means something else).
  GraphQL matches this: its `meta.total` /
  `meta.total_pages` are **lazy** — the `COUNT(*)` runs only when those fields are
  selected (SEC-AUDIT-V2), so a GraphQL list that doesn't ask for the total pays no
  COUNT either.
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

- **Functions** (a fixed allowlist — never arbitrary SQL): `count` (bare
  `?count`, or by value — `count=false` is OFF → `COUNT(*)`); `sum` / `avg`
  (numeric fields only); `min` / `max` (numeric
  **or** `time`). Each of `sum`/`avg`/`min`/`max` takes a comma-separated field
  list. `group_by` is a comma-separated field list (anything but `json`).
- **The aggregate endpoint owns its query-parameter namespace** (ENG-18/24,
  ADR-024): here the parameter NAME is the requested function, so an unknown
  one (`?median=x`, `?summ=x`) is a **400 listing the valid set** — it used to
  answer 200 with the metric silently absent. The list parameters an aggregate
  cannot honor (`page`, `per_page`, `sort`, `order`, `order[…]`, `after`,
  `before`, `include`) are a 400 saying so — they used to be VALIDATED and then
  thrown away, so `?count&sort=ghost` 400'd over a parameter the endpoint never
  honors while `?count&sort=status` was accepted-and-ignored. The general
  unknown-top-level tolerance (utm tags, cache-busters) does NOT apply on this
  endpoint — an authenticated aggregate XHR is never a decorated link. An empty
  `?group_by=` is a 400 (it used to silently change the response SHAPE), and an
  empty entry in an active field list (`sum=a,`) names the extra comma; the
  wholly empty `?count&sum=` keeps its reviewed tolerance (visible in the
  response, which simply has no `sum` key).
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
  a bad `file` reference → `422` `file_not_found`, any other FK violation (bad
  relation reference / RESTRICT delete) → `409`, forbidden → `403`, a bad
  op/resource → `400`.
- **Limit**: at most **100** operations per request (`APPXIMO_MAX_TX_OPS`) →
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
GraphQL write mutations emit identically to their REST counterparts**. GraphQL answers **HTTP 200 for anything it can execute** — check the `errors`
array in the body, not the status code. The exceptions are transport-level and
cannot produce a GraphQL response at all: a body that is not valid JSON is a
**400** `{"error":"invalid JSON body"}` and one over the 1 MiB cap is a **413**
(before ADR-024 both were a 413, so an 18-byte malformed body was reported as
too large). Those carry an `error` string and NO `errors` array, so a client
that only inspects `body.errors` must still check for a non-2xx status. Validation
failures arrive as `errors[].extensions.fields` (same rule engine as
REST). Introspection is disabled in production (the `__schema`/`__type`
fields are rejected outside development; `__typename` is allowed);
GraphiQL only runs with `APPXIMO_ENV=development`. The query analyzer
also bounds document size as an alias-amplification guard: at most **50
root selections** per operation and **2000 total selections** across the
whole document — over either limit the request is rejected (there is no
separate nesting-depth counter). A **fragment's cost is charged at every
spread site** (ENG-28): the counter used to walk each fragment body once
globally, so one 45-field fragment spread across 50 root aliases counted ~95
while the executor resolved a measured **~46× the cap** (~92,500 selections,
21.4 MB from one request). Counted ≥ resolved now — the 2000 number holds —
at the price of over-counting repeated same-alias spreads, which the executor
merges (the safe direction).

**GraphiQL — the visual explorer (GRAPHQL-EXPLORER-S1).** `/graphiql` is the
GraphQL equivalent of REST's `/docs`: schema browser (introspection-driven),
autocomplete, run queries/mutations in place, a Headers editor for a real
`Authorization: Bearer <token>` (every request — including GraphiQL's own
schema-fetch on load — goes through the same JWT+RBAC chain as any other
request; there is no anonymous introspection). It is mounted, and
introspection allowed, in the SAME two cases: `APPXIMO_ENV=development`,
or the explicit opt-in `APPXIMO_GRAPHQL_PLAYGROUND=on` — for exploring
GraphQL in production without the broader dev flag (which also enables
pprof). Both are **per-app** in the in-process fleet (`appximo fleet
serve`): one app can expose GraphiQL while a sibling in the same process
stays locked down. The CDN build is version-pinned (`graphiql@3.9.0` — the
last version shipping a standalone UMD bundle; 4.x+ dropped it for
ESM/import-maps), the same discipline as `/docs`'s Swagger UI.

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
- **The served spec also lists every REGISTERED CUSTOM ROUTE** (ENG-33,
  THIRD-PARTY-READY-S1): method, path, the optional `Route.Description` as
  summary, auth mode (`x-public: true` + `security: []` for a Public route;
  otherwise Bearer + the RBAC segment/action named in the description),
  `x-required-role`, `x-byte-serving`, all flagged
  `x-appximo-custom-route: true`. Request/response SHAPES are deliberately
  NOT published (a Go handler declares none — the app's contract sheet stays
  the authority for shapes; the OpenAPI is the authority for EXISTENCE). The
  CLI `appximo openapi <schema>` prints the schema-derived half only (a
  schema file has no registered routes); the RUNNING app's /openapi.json is
  the complete surface. The probe semantics are deliberate and unchanged: an
  unknown `/api/...` still answers 401 (auth runs before routing) — with the
  contract now public, the probe is no longer the discovery mechanism, and
  re-ordering auth after a second route-matching pass would be a drift-prone
  duplicate of the router for zero information the contract doesn't already
  give.
- Every custom **GET** route also answers **HEAD** (ENG-32): same handler, same
  auth (a Public GET's HEAD skips auth too), RBAC maps HEAD→read,
  `http.ServeContent` serves HEAD natively (headers + Content-Length/ETag, no
  body). Generated routes are unchanged (GET-only, as before).
- `GET /docs` — Swagger UI (loaded from a pinned CDN) pointed at `/openapi.json`,
  for interactive "Try it out" against the same origin.
- The CLI still prints the spec: `appximo openapi schema.json` (YAML) — same
  document the HTTP routes serve.

## CORS (API-PRODUCTIVA-V1)

CORS is **configurable instance infrastructure**, NOT a schema key. It is
**disabled by default** (no `Access-Control-*` headers emitted, no middleware in
the chain — zero cost). Enable it by listing browser origins:

- `APPXIMO_CORS_ORIGINS` — comma-separated exact origins, or the single `*`.
  **Setting it ENABLES CORS**; empty keeps it off.
- `APPXIMO_CORS_METHODS` (default `GET,POST,PUT,PATCH,DELETE,OPTIONS`),
  `APPXIMO_CORS_HEADERS` (default `Authorization,Content-Type`),
  `APPXIMO_CORS_EXPOSE_HEADERS` (default none),
  `APPXIMO_CORS_CREDENTIALS` (`true`/`1`/`on`; default false),
  `APPXIMO_CORS_MAX_AGE` (preflight cache seconds; default 600).
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
   `APPXIMO_AUTH_SIGNUP_ROLE` is set.)
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

## File store (FILES-V2 — local disk or any S3-compatible, by config)

The engine ships a content-addressable, multi-tenant file store (no schema
declaration needed — the routes exist whenever no resource is literally named
`files`). Storage is a **swappable backend**: `APPXIMO_FILES_BACKEND=local`
(default — blobs on this VPS under `APPXIMO_FILES_DIR`, served by the engine
with Range/ETag/sendfile) or `=s3` (any S3-compatible provider — Cloudflare R2 /
DO Spaces / MinIO / AWS — via `APPXIMO_FILES_S3_{BUCKET,ENDPOINT,REGION,
ACCESS_KEY,SECRET_KEY,FORCE_PATH_STYLE,PREFIX,SERVE}`). Tenancy, RBAC, metadata
and upload validation are IDENTICAL on both. Full doc + setups:
[docs/FILES.md](docs/FILES.md).

- `POST /api/files` — multipart upload (form field `file`). Streamed in 64 KiB
  chunks (never buffered whole), de-duplicated by content hash, and validated
  OWASP-style: extension ALLOWLIST (default curated list; override
  `APPXIMO_FILES_ALLOWED_EXT`, `*` disables) + magic-byte check (a `.jpg`
  containing PHP source, or a declared `image/*` that isn't, → `422`; the
  client Content-Type is never trusted) + name sanitized at rest. Returns
  `201 {"file_id","sha256","size"}`. Body capped by `APPXIMO_FILES_MAX_BYTES`
  (default 256 MiB) → `413`. RBAC action: `create` on `files`.
- `GET /api/files/{id}` — the blob, with its stored `Content-Type` and
  `attachment` disposition. Local backend: proxied via `http.ServeContent`
  (Range → `206`, strong content-hash `ETag` → `304`). S3 backend: `302` to a
  short-lived presigned URL by default (the engine authorizes, the bucket
  serves), or streamed through the engine with `APPXIMO_FILES_S3_SERVE=proxy`.
  RBAC: `read` on `files`. `404` if the id is unknown to the tenant (ids are
  tenant-scoped — no cross-tenant handle).
- `GET /api/files/{id}/url` — mints a short-lived signed download URL
  (`APPXIMO_FILES_TOKEN_TTL`, default 180 s): `200 {"url","expires_in"}`.
  S3 → native presigned; local → an engine token URL `GET
  /files/signed/{token}` that needs NO Authorization header (for `<img>`/share
  links) — the HMAC token is tenant-bound and role-re-checked at serve, and any
  invalid/expired/foreign token is a uniform `404` (anti-fingerprinting). RBAC:
  `read` on `files`.
- `DELETE /api/files/{id}` — removes the file (`204`); the blob is deleted only
  when no other upload references the same content. RBAC: `delete` on `files`.

All inherit the normal chain (tenant Host → JWT → RBAC), so a role needs the
`files` resource in its policy — `"resources": ["files", …]`, `"*"`, or a
per-resource `permissions` entry `"files": { "actions": ["read","create"] }`
(actions only — conditions/fields on the built-in store are rejected at load;
FRONTEND-SPEC-S1 closed the asymmetry where only the role-global form could
grant it). Local
blobs live under `APPXIMO_FILES_DIR` (default `/var/lib/appximo/files`),
created lazily on first upload. Use a `file_id` as a filejob's `file_ref` to
feed the async XLSX consumer (`APPXIMO_FILES_DIR` must be set on the worker,
pointing at the same root — the worker resolves refs through the same VFS).

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

- `APPXIMO_AUTH_SIGNUP_ROLE` (or `Config.AuthSignupRole`) — the role assigned
  to every public signup. **Setting it ENABLES public signup; leaving it empty
  keeps signup disabled** (`POST /auth/signup` → `403`). The role must be one the
  schema's RBAC declares, or the engine refuses to boot (a typo never becomes a
  silent misconfiguration). Login and refresh work regardless (they operate on
  already-created users).
- `APPXIMO_AUTH_MIN_PASSWORD` (or `Config.AuthMinPasswordLength`) — minimum
  signup password length (default 8).

### Password reset + email verification (AUTH-EMAIL-V1)

Built ON the email consumer (`pkg/consumers`, `APPXIMO_WORKER_MODE=email`) via the
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
default (multi-tenant-correct), or `APPXIMO_AUTH_BASE_URL` if set.

**Config (AUTH-EMAIL-V1):**

- `APPXIMO_AUTH_REQUIRE_VERIFIED` (`true`/`1`/`on`) — block login for an
  unverified email (`403`). Default off (login unchanged).
- `APPXIMO_AUTH_BASE_URL` — override the email-link origin (else derived from the
  request Host).
- `APPXIMO_EMAIL_TOPIC` — outbox topic for the email events (default `email.send`);
  **must match the email worker's** `APPXIMO_EMAIL_TOPIC`. Run the deliverer with
  `APPXIMO_WORKER_MODE=email` (templates `verification` + `reset` ship built-in).

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
  `200 {user, token}` (or `302` to `APPXIMO_OAUTH_SUCCESS_REDIRECT#token=…` if set).

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

- `APPXIMO_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID` / `…_CLIENT_SECRET` — per
  provider credentials. Register the redirect URI
  `{callback}/auth/oauth/{provider}/callback` with each provider.
- `APPXIMO_OAUTH_CALLBACK_URL` — the FIXED public origin the providers redirect
  back to (e.g. `https://auth.example.com`). Empty ⇒ derived from the request
  (fine in dev/single-domain; set it for multi-tenant prod).
- `APPXIMO_OAUTH_DEFAULT_ROLE` — role for a user auto-created on first social
  login; empty falls back to `APPXIMO_AUTH_SIGNUP_ROLE`. If BOTH are empty, a
  brand-new social email is rejected (`403`) while existing users still link/login.
  A set role must exist in the schema RBAC (else boot fails).
- `APPXIMO_OAUTH_SUCCESS_REDIRECT` — optional; `302` to `<url>#token=<jwt>` for a
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
each code; key = `APPXIMO_MFA_KEY` or the JWT secret) and
`tenant_<id>.auth_backup_codes` (hash only, one-time). Per-tenant, isolated — a
user's MFA in tenant A never affects tenant B. TOTP window is exactly ±1 step.
**Config:** `APPXIMO_MFA_KEY` (secret-encryption key; falls back to `JWT_SECRET`
— rotating it invalidates enrollments), `APPXIMO_MFA_ISSUER` (authenticator-app
label; default `Appximo`).

**Auth-as-product is now complete: password (signup/login/refresh) + reset/verify +
OAuth social login + TOTP MFA.**

## Admin API — platform super-admin + management (ADMIN-API-V1)

The backend of the admin panel. It is **not a second permission system** — it
INHERITS the schema RBAC, the schema-per-tenant isolation, auth-as-product, the
control plane, and the observability that already exist. It adds only the one
thing that did not exist: a **platform super-admin** (above the tenants) plus a
consolidated, authenticated `/admin/*` API. Two access levels, modelled as roles:

- **Platform super-admin** — lives in a SYSTEM schema (`appximo_system.platform_admins`),
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
  appximo admin create --email me@example.com --password 'a-strong-passphrase'
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
- Tenant schema deploy (platform token OR admin key; UI-F1-S1): `GET
  /admin/tenants/{id}/schema` (the stored schema — the visual editor loads it back
  onto the canvas) and `PUT /admin/tenants/{id}/schema` with
  `{schema, dry_run, approved_drops}` — the SAME diff→preview→approved-apply
  migration path as the control-plane `PUT /tenants/{id}/schema` (it delegates to
  `PreviewSchema`/`UpdateSchemaApproved`), so the editor's "Deploy" gets the dry-run
  preview + destructive-approval gate for free. **It is the editor's deploy seam**:
  the SPA is browser-served and cannot reach the internal control plane (:9090), so
  it deploys/migrates through `/admin`. Unlike the masked public surfaces, an apply
  failure here returns the engine's ACTIONABLE error (e.g. a NOT NULL added over
  populated data → `422`), since this is the authenticated super-admin path.
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

**Config:** `APPXIMO_PLATFORM_SUPER_ADMIN_ROLE` (platform role marker; default
`platform_super_admin`), `APPXIMO_PLATFORM_MFA_ISSUER` (authenticator label;
default `Appximo Platform`). The platform MFA secret is encrypted at rest with
`APPXIMO_MFA_KEY` (falls back to `JWT_SECRET`), same as tenant MFA.

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
- A `decimal`/`money` type. Money is `int64` in the currency's MINOR unit
  (`price_cents`) — documented, deliberate, and what payment APIs speak.
- A PARTIAL index (an `indexes` entry with a `WHERE` predicate), or an index method
  beyond `btree`/`gin`. The predicate is left out because Postgres normalizes its
  text, so the diff would churn the index on every migration.
- Unknown schema keys → schema rejected listing the valid keys (e.g.
  `webhooks` instead of `hooks`, `secret` instead of `hmac_secret_env`).
  Nothing is silently ignored anymore.
- Writing a field the resource doesn't have → 422
  `{"error":"validation_failed","fields":[{"field":"…","rule":"unknown_field"}]}`
  (not a 500, not silently dropped).
- Hook events other than the four listed (no `on_create`, no
  `before_delete`/`after_delete`).
- Filter ops beyond the type table → **400 naming the operator and listing the
  allowed set** (`neq`, `in`, `nin`, `like`, `ilike`, `is_null`, an uppercase
  `EQ`, a malformed `filter[a][b][c]` — all of them, verified live 2026-08-01).
  ENG-14 used to make some of these silent: the op pattern matched `[a-z]+`, so an
  op containing `_` failed the regex and the WHOLE parameter was dropped with no
  error. That is fixed — the pattern now decides only what IS a filter, and
  validation produces the error (ADR-024).
  **There is still NO way to filter by NULL** in the declarative surface; the 400 is
  honest but it is a dead end (backlog SCHEMA-6, ADR-022 Decision 5).
- Multi-field sort (`sort=a,b`) or `sort=field:desc` → **400 naming the value and
  listing the sortable fields** (`unknown sort field: title:desc (available: …)`).
  Neither syntax exists; since ADR-024 the engine says so instead of ignoring it
  and returning an arbitrary order. Use `?sort=field&order=desc`.
- Aggregation BEYOND `count`/`sum`/`avg`/`min`/`max` + `group_by` (e.g. `HAVING`,
  `DISTINCT`, expression aggregates, window functions) — the
  [Aggregation](#aggregation-g3) surface is exactly those functions over schema
  fields, nothing more. (`count` total IS now a thing: `?count=true` on a list, or
  the aggregate endpoint.)
- FK coverage is now **complete** (MIG-F1-S5): `on_delete` AND `on_update`
  (restrict/cascade/set_null), single-column FKs to the target's `id` OR a `unique`
  non-`id` column (`references`), and **composite** multi-column FKs (the resource-
  level `foreign_keys` block) — all in [Relations](#relations). What still does NOT
  exist: a FK referencing a column that is neither a PK nor `unique` (Postgres
  forbids it — rejected at load), and `MATCH PARTIAL`.
- `workflows` schema block — parsed for forward compatibility, no executor.
- OTLP/OpenTelemetry export (observability is Prometheus `/metrics` + an
  internal trace ring).
- A hosted/SaaS version — self-hosted only.
