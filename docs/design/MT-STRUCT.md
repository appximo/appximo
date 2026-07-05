# Multi-structure tenancy — one server, N distinct APIs (feasibility & design)

> **Status:** design + feasibility, DESIGN-MT-STRUCT-V1. Nothing here is built.
> The deliverable is this document: the two (three) architectures evaluated
> against the real codebase, **three measured numbers**, the full homologation
> map, honest risks, a verdict, and a staged plan. Every code claim cites a file
> + symbol; every number was measured on the 105 box (1.9 GB RAM, 1 usable vCPU)
> and is reproducible from `scratch_heapmeasure/` (kept out of the commit — see
> [Reproducing the numbers](#reproducing-the-numbers)).

## 0. The question

Today Appitools is **multi-tenant of DATA**: one boot schema compiles one API
structure, and N tenants share it with isolated data (the Shopify shape —
[docs/MENTAL_MODEL.md](../MENTAL_MODEL.md)). This document asks whether — and how
— one server can serve **N genuinely DIFFERENT APIs** (the optician's CRM *and*
another client's ERP *and* an e-commerce, each with its own resources, GraphQL
types, RBAC and OpenAPI), **without losing the record performance** and while
**homologating the whole surface** (admin, observability/trace, Studio, DevHub,
control plane, config) so all of it works with N apps.

AUDIT-F1-S1 flagged this as *the deep assumption* ("it would be another
product"). MENTAL_MODEL §7 item 3 names it precisely: *"Routing, GraphQL type
identity, the response-cache keying, the OpenAPI contract and `/docs` all assume
ONE structure."* This session decides that assumption with evidence.

## 1. Taxonomy (naming it, and using it consistently)

The two dimensions must never be conflated again:

| Term | Definition | Isolation mechanism | Analogy |
|---|---|---|---|
| **App** (a.k.a. *structure*) | One schema → one compiled surface: its own REST routes, GraphQL types, RBAC policy, validators, hooks, OpenAPI. **This is the new axis.** | *(today: the whole process)* | a distinct product |
| **Tenant** (a.k.a. *data instance*) | One isolated data instance **inside an app**: its own Postgres schema `tenant_<id>`, rows, users. **This exists today.** | `SET LOCAL search_path` per request | a shop on Shopify |
| **Fleet** | The set of apps served by one server/process. | *(new)* | the mall |

So the model becomes **two-level**: a request resolves first to an **app**
(which structure am I hitting?) and then to a **tenant** (whose data?). Today
only the second level exists; the first is fixed at boot to a single app.

A concrete example used throughout: the fleet holds three apps —
`crm` (the optician), `erp`, `shop`. `crm` has tenants `optica-norte` and
`optica-sur`; `erp` has tenant `acme`; `shop` has tenants `store-a`, `store-b`.
Two axes, independent.

## 2. The starting point, verified against the code

One process = one boot schema, compiled **once**, into artifacts that are global
and identical for every tenant. Verified in [app.go](../../app.go) `buildRouter`:

| Global artifact | Where it is compiled (once, from `a.schema`) |
|---|---|
| REST routes (CRUD + `/aggregate` + `/events` + subroutes + `/transaction`) | `codegen.BuildRouter(a.schema, …)` — [app.go](../../app.go) `buildRouter`, mounted at `/` |
| GraphQL types + resolvers | `gqlhandler.BuildHandler(a.schema, …)` — same function |
| RBAC middleware | `rbac.RBACMiddleware(mustMarshal(a.schema.RBAC))` — closes over the boot policy |
| JWT middleware | `auth.JWTMiddleware(a.cfg.JWTSecret, …)` — closes over **one** secret |
| Validators / state machines / defaults | `schema.CompileRules` per resource, at the top of `codegen.BuildRouter` |
| Hooks (js/webhook/wasm) | captured per-handler from boot `res.Hooks[…]` inside `codegen.BuildRouter` |
| OpenAPI + `/docs` | `registerOpenAPIRoutes` → `codegen.GenerateOpenAPIJSON(a.schema, …)` |
| Response cache, SSE hub, rate limiter, obs rings | process-wide singletons on the `App` struct ([app.go](../../app.go) `App`) |

**The property the benchmark rests on:** the request hot path executes only
precompiled closures — no per-request schema consultation
([AGENTS.md](../../AGENTS.md) "Compile work at schema load, never per request";
MENTAL_MODEL §2 "Nothing on the request hot path consults a per-tenant schema").

Two incidental facts that matter for the design, both verified:

- The control plane binds **`:9090` hardcoded** ([app.go](../../app.go) `New`,
  `cpSrv.Addr = ":9090"`) and dev pprof binds **`:6060` hardcoded**
  ([app.go](../../app.go) `buildRouter`). **Two engine processes cannot co-exist
  on one box today** without colliding on `:9090` (observed live: the 2nd–5th
  engines logged a bind error on the control plane while their data planes still
  served). This is a precondition for Option A.
- Observability rings are keyed by **tenant id only** (`Rings.Record(tenantID,
  …)`, [pkg/observability/ring.go](../../pkg/observability/ring.go)). Any
  multi-app world must key by **(app, tenant)** or the tenant-id namespace must
  become app-qualified.

## 3. The three measured numbers

The whole A-vs-B comparison reduces to three facts. All measured, not estimated.

### Number 1 — RSS per engine process (the cost of Option A)

Five engines booted simultaneously, each a **different** schema, each its own
port, sharing the one dev Postgres. Measured from `/proc/<pid>/smaps_rollup`:

| App (schema) | resources | RSS | **PSS** (fair-shared) | Private-dirty | Shared (binary text) |
|---|---:|---:|---:|---:|---:|
| quickstart | 1 | 76.4 MB | 51.3 MB | 45.0 MB | 31.2 MB |
| logistics | ~4 | 77.0 MB | 51.6 MB | 45.4 MB | 31.5 MB |
| erp-demo | 6 | 48.3 MB | 23.0 MB | 16.7 MB | 31.3 MB |
| saas | 11 | 62.9 MB | 37.3 MB | 31.0 MB | 31.7 MB |
| ecommerce | 10 | 64.3 MB | 39.0 MB | 32.8 MB | 31.4 MB |
| **5 engines total** | | **329 MB** | **202 MB** | | 31 MB (shared once) |

Three findings the raw table makes obvious:

1. **RSS does NOT track schema size.** The 1-resource app and the 11-resource
   app cost about the same. The per-process cost is a **fixed ~45 MB runtime
   baseline** — Go runtime + Wazero JIT arena ("WASM runtime (Capa 3) enabled"
   in every boot log) + the two embedded SPAs (`/admin`, `/editor`) + pgxpool +
   the obs SQLite handle — **not** the compiled schema surface (Number 2 shows
   that surface is < 1 MB).
2. **~31 MB is SHARED binary text**, identical across every engine (`Shared_Clean`
   — the OS maps the same read-only `.text`/`.rodata` once). So the *marginal*
   RSS of one more Option-A process is the **private-dirty ~15–45 MB**, not the
   full ~77 MB. PSS (202 MB for 5) is the honest "real RAM used".
3. **Idle is worst-case; load reclaims.** After 4,000 requests the saas engine's
   PSS *fell* 37.3 → 18.3 MB — GC released un-touched arena. Steady-state private
   settles toward ~18–30 MB. So a live Option-A fleet costs **~25–50 MB PSS per
   app**.

> **Finding for Miguel (flag, not a silent edit):** the README comparison table
> says *"~24 MB RSS idle"* and one memory note says *~8.9 MB*. Today's
> full-feature binary (WASM + two embedded SPAs + admin + obs) idles at **~46–77 MB
> RSS / ~50 MB PSS** on this box. Neither older figure reproduces. Worth
> re-measuring the README claim under its original conditions before the next
> launch — AGENTS.md forbids changing a README perf claim without a live
> re-measure, and this *is* that re-measure contradicting it.

### Number 2 — heap per compiled app in ONE process (the cost of Option B)

`scratch_heapmeasure/main.go` compiles the **full** app surface (RBAC policy +
`BuildRouter` + GraphQL `BuildHandler` + `GenerateOpenAPIJSON`) N=100 times,
keeps them all alive, and measures the `HeapAlloc` delta after `runtime.GC()`.
Shared singletons (TenantDB, HookRunner, cache, SSE hub) are built once and
excluded, exactly as they would be process-wide in Option B.

| Schema | resources | **heap / app** | heapSys / app | per resource | OpenAPI doc retained |
|---|---:|---:|---:|---:|---:|
| quickstart | 1 | **79 KB** | 126 KB | 79 KB | 35 KB |
| erp-demo | 6 | **422 KB** | 671 KB | 70 KB | 163 KB |
| saas | 11 | **706 KB** | 1.17 MB | 64 KB | 295 KB |
| ecommerce | 10 | **926 KB** | 1.47 MB | 93 KB | 255 KB |

**~65–90 KB of heap per resource; ~0.1–1 MB for a typical app**, of which a large
slice is the retained OpenAPI JSON document (the biggest single per-app object —
worth generating lazily/on-demand rather than retaining, a cheap Stage-3 win).
`heapSys` (closer to RSS impact) is ~1.5× `heapAlloc`.

**The asymmetry that decides everything:** Option A re-pays the ~45 MB runtime
baseline **per app**; Option B pays it **once** and adds **~1 MB per app**. On
this 1.9 GB box:

- Option A caps at roughly **20–30 apps** before RAM pressure (baseline-bound).
- Option B's compiled surface caps in the **hundreds–thousands** (< 1 MB each);
  the real ceiling becomes **Postgres pool connections**, not compiled memory.

### Number 3 — the Host→app dispatch cost (Option B's only hot-path addition)

Option B adds, in front of the existing chain, one step: parse the Host label,
look the app up in a `map[appKey]*compiledApp` held behind an `atomic.Pointer`
(swap-safe). Microbenchmarked (`scratch_heapmeasure/dispatch_test.go`):

| Operation | ns/op | allocs |
|---|---:|---:|
| Host-label parse **alone** (tenant middleware already does this today) | 13.7 | 0 |
| `atomic.Load` + parse + map lookup, **4 apps** | 42.8 | 0 |
| same, **50 apps** | 50.2 | 0 |
| same, **500 apps** | 44.1 | 0 |

**Marginal cost ≈ 30 ns, zero allocations, flat from 4 to 500 apps.** Against the
published **p50 of 1.58 ms** (1,580,000 ns) that is **~0.002 %** — below the
noise floor of `bench-protocol`. The dispatch itself does **not** materially
touch the hot path. (The hot-path *risk* in Option B is elsewhere — §6.2 — not
the lookup.)

## 4. The two architectures, against the code

### Option A — multi-process, orchestrated (N engines + a supervisor)

N engine processes (one boot schema each, one port each) behind a front router
(Host → port), plus an **orchestrator** (`appitools fleet`, a new binary mode)
that provisions/starts/stops/restarts apps, assigns ports, and federates the
surface.

**What it costs, from the code:**

- **Almost no engine change.** Each app is today's engine, unmodified. The only
  blockers are the two hardcoded ports (`:9090`, `:6060`) — they must become
  per-app (`--control-port`, and pprof off or per-app). `OBS_DB_PATH`,
  `APPITOOLS_FILES_DIR` already parameterize per process.
- **The hot path is untouched by construction** — every app is the same compiled
  engine that the benchmark measured. **No `bench-protocol` needed for the engine
  core.** This is Option A's single biggest advantage.
- **Isolation is free and total:** an app that crashes, OOMs, or restarts cannot
  touch another (separate address spaces, separate pools). The existing graceful
  self-restart (UI-F4-S2) already works **per app** with zero change.
- **Postgres:** one server, N Postgres schemas (or N databases) — the data
  isolation already exists; nothing new.
- **The memory bill is Number 1:** ~25–50 MB PSS **per app**, baseline re-paid
  every process. ~20–30 apps on this box.
- **The surface is FEDERATED, not unified:** the orchestrator must aggregate N
  control planes / N obs endpoints into one admin view (each app has its own
  `:909x`). "One panel" is extra work, not a freebie.
- **New moving parts:** a supervisor (spawn, health, restart-on-crash, port
  registry) and a front proxy. Operationally heavier; conceptually simple.

### Option B — multi-structure in one process (N apps compiled in memory)

One process loads N schemas, compiles N `compiledApp`s, and dispatches by Host to
the right one. A registry `map[appKey]*compiledApp` behind an `atomic.Pointer`
allows deploying one app (recompile + pointer swap) without restarting the
process — the natural evolution of self-restart (MENTAL_MODEL §7.2) from
whole-process to per-app.

**What it costs, from the code — the exact inventory to multiply/parameterize:**

Everything in the §2 table that today closes over `a.schema` or `a.cfg` must move
from a boot-global closure to a **per-app field looked up by Host**:

1. **The router mount** — `codegen.BuildRouter` output becomes per-app; the outer
   chi mux dispatches to `app.router` after resolving Host.
2. **The middleware chain is the hard part.** Today JWT (`auth.JWTMiddleware`,
   one secret) and RBAC (`rbac.RBACMiddleware`, boot policy) are `r.Use(...)`
   closures over boot state. In B they must resolve the app **first** (from Host)
   and then apply *that app's* secret and policy. If config is per-app (§7), the
   **JWT secret itself is app-dependent**, so app resolution must precede JWT
   validation — an ordering change to the chain.
3. **Validators / hooks** — captured in `codegen.BuildRouter` closures per app
   (fine, they're already per-router; just N routers).
4. **OpenAPI / `/docs`** — per app; `/openapi.json` must serve the app resolved
   from Host (or move under an app-qualified path).
5. **GraphQL** — `BuildHandler` per app; `/graphql` dispatches by Host. GraphQL
   **type identity** is per app (a `Task` in `crm` ≠ a `Task` in `erp`) — fine,
   separate handlers, but introspection/schema caching is per app.
6. **Response cache** — keying must become **(app, tenant)**, not tenant alone,
   or two apps' identical tenant ids collide. `pkg/cache` keys need an app prefix.
7. **SSE hub** — per-app channels (or (app, tenant, resource) keys); a hot-swap
   must not drop live connections on the old app (MENTAL_MODEL §7.2 names this).
8. **Observability** — rings/SLO/anomaly keyed by (app, tenant) (Number-2 §2
   finding). `servedResources` becomes per-app.
9. **The registry + hot-swap** — `atomic.Pointer[map[appKey]*compiledApp]`;
   in-flight requests straddle a swap (old pointer still valid until the request
   ends — the same straddle problem self-restart avoids by draining).

**Strengths:** one process; baseline paid once (~1 MB/app → hundreds of apps);
"one panel" is natural (one admin, one obs store, filter by app); per-app
hot-swap deploy without a full restart; the dispatch is proven negligible
(Number 3).

**Weaknesses:** it **touches the deep assumption** — items 2, 6, 7 above are on
or adjacent to the hot path, so **every one carries `bench-protocol`**; shared
process ⇒ **blast radius** (a panic in one app's handler is caught by
`chimiddleware.Recoverer`, but an OOM or a runaway Goja/Wazero hook starves all
apps); and the middleware-reorder (item 2) is genuinely subtle security code
(JWT/RBAC) — the highest-care change in the plan.

### Option C — the honest hybrid (recommended shape)

Neither pure A nor pure B is the whole answer. The recommendation (§8) is
**B as the target, with A available as an operational escape hatch**:

- **B is the default runtime** — most apps are small (a CRM, a shop); the memory
  economics (Number 2) and "one panel" make co-location the right default, and
  the dispatch cost is nil (Number 3).
- **A is the isolation pressure-valve** — a noisy, regulated, or
  resource-hungry app can be *pinned to its own process* behind the **same front
  proxy** (Host → that process instead of the in-process registry). Same fleet
  concept, same taxonomy; the app just runs alone. This buys back A's total
  isolation exactly where it's worth its ~45 MB.
- **A is also the honest MVP** — because A needs almost no engine change, it can
  ship "N distinct apps on one box" *first* (parameterize `:9090` + a supervisor
  + a proxy), delivering the capability while B's careful in-process work is
  staged and benched. B then subsumes the common case; A stays for isolation.

## 5. The homologation surface — piece by piece, A vs B

For each existing surface, what must change. This is the "make it all work with
N apps" map.

| Surface | Today | In **A** (federated) | In **B** (in-process) |
|---|---|---|---|
| **Admin API** (`/admin/*`, [pkg/platformadmin](../../pkg/platformadmin)) | assumes one engine/one schema | orchestrator federates N control planes; a new "apps" resource (list/start/stop/restart) above tenants | gains an **app** dimension: `/admin/apps`, and every tenant route becomes `/admin/apps/{app}/tenants/{id}`; one service, app-scoped |
| **Admin UI** (`/admin`, SolidJS) | tenant selector | **app selector** → then the existing tenant selector (two-level); each app's admin proxied | **app selector** in the topbar; Users/Data/Obs operate on (app, tenant); one SPA |
| **Studio / editor** (`/editor`) | Deploy targets a **tenant** | Deploy targets **(app process, tenant)**; restart = restart *that process* | Deploy targets **(app, tenant)**; `served-resources` per app; restart = **per-app pointer swap** (no process restart) |
| **served-resources** ([app.go](../../app.go) `New`) | boot-global list | per-process (already) | **per-app** map; the restart hint is per app |
| **Observability / trace** ([pkg/observability](../../pkg/observability)) | keyed by tenant | each app's obs scraped separately; dashboards aggregate | rings/SLO/anomaly keyed by **(app, tenant)**; obs store gains an app column; dashboards filter by app |
| **Control plane** (`:9090`) | one, hardcoded | **per-app port** (must parameterize); orchestrator is the single external entry | one control plane, app-qualified routes |
| **DevHub** (`:3099`, not engine) | one server/one engine | navigates N app processes | navigates N apps in one engine (one scrape, group by app) |
| **Config / secrets** (§7) | process env | per-process env (trivial) | **per-app config object** resolved by Host — the middleware-ordering change |
| **Front router** | none (single port) | **required** (Host → port) | the in-process registry **is** the router; a proxy only needed for the A escape-hatch apps |

The pattern: **A pushes the app dimension into an orchestrator and a proxy
(engine mostly unchanged, surface federated); B pushes it into the engine
(surface unified, engine changed on/near the hot path).**

## 6. Honest risks

### 6.1 Option A risks

- **Memory ceiling** (~20–30 apps/box, Number 1) — fine for "a few clients",
  wrong for "hundreds of small apps".
- **Federation is real work** — "one panel" over N control planes / N obs stores;
  a supervisor with health, restart-on-crash, and a port registry is a new
  operational component to get right.
- **Front-proxy hop** — an extra network hop per request (measurable later;
  BLOQUE-B already measured a Caddy hop at +0.48 ms keepalive — real but modest).

### 6.2 Option B risks

- **The deep assumption is touched.** Items 2/6/7 of §4-B are on or beside the
  hot path. **Mandatory `bench-protocol` on every such stage** (Stages 2–4). The
  dispatch is proven safe (Number 3); the *middleware rework* is the unproven
  part.
- **JWT/RBAC reorder is security-critical.** Resolving the app (and thus the JWT
  secret and RBAC policy) before validation is subtle; a bug is a cross-app
  auth hole. Highest-care change; needs its own security review (the project has
  the SEC-AUDIT precedent).
- **Blast radius.** One process = all apps. `Recoverer` catches a panic, but a
  Wazero/Goja runaway hook, an OOM, or a `GOMAXPROCS`-starving app degrades
  everyone. Mitigation: the Option-C escape hatch (pin the risky app to its own
  process) + per-app resource limits.
- **Hot-swap correctness** — in-flight requests and live SSE connections straddle
  a pointer swap (MENTAL_MODEL §7.2). Must drain per app, not per process — more
  intricate than the whole-process self-restart already shipped.

## 7. Config/secrets granularity — decided

**Per-app, not shared**, as the default:

- **`JWT_SECRET` per app.** If two apps shared a secret, a token minted for app
  X's tenant would verify on app Y (same signature); with per-app RBAC roles that
  can silently grant access if role names collide. Per-app secret **scopes token
  validity to one app** — the safe default. (Consequence in B: app resolution
  must precede JWT validation — §4-B item 2.)
- **OAuth providers, files backend, MFA key per app.** Each app is a product with
  its own identity providers and storage. (MFA key already derives from
  `JWT_SECRET`.) Files may share a backend with an **app-prefixed key**
  (`<app>/<tenant>/…`) to reuse one bucket.
- **Rate limits, CORS origins per app.**

In A this is free (separate env per process). In B it becomes a **per-app
`Config`** on each `compiledApp`, resolved by Host before the chain runs.

## 8. Verdict

**Adopt Option C: Option B (in-process multi-structure) as the target
architecture, with Option A retained as the isolation escape hatch and as the
shippable MVP.**

Why, from the evidence:

1. **The vision implies many apps.** "The optician's CRM *and* an ERP *and* an
   e-commerce…" is an open-ended fleet, not two big tenants. Number 1 vs Number 2
   is decisive at scale: **~45 MB/app (A) vs ~1 MB/app (B)**. B is the only option
   that scales to the vision's implied N on one cheap box.
2. **The one hot-path fear about B is disproven.** The Host dispatch is ~30 ns /
   0 allocs / flat to 500 apps (Number 3) — ~0.002 % of p50. The *remaining* B
   risk (middleware rework) is bounded, benchable engineering, not a fundamental
   wall.
3. **B gives "one panel" natively** — one admin, one obs store, one binary — which
   is most of the homologation goal for free, where A must federate.
4. **But B's careful in-process work must not block shipping the capability, and
   isolation is sometimes worth its cost.** A ships "N apps on one box" almost
   immediately (parameterize `:9090` + supervisor + proxy) and remains the right
   home for a noisy/regulated app. So A is not thrown away — it is Stage 1 and the
   permanent escape hatch.

The record performance is protected by construction: A touches nothing on the hot
path; B touches it only where `bench-protocol` gates every step, and the one
piece already measurable (dispatch) passes with three orders of magnitude to
spare.

## 9. Staged plan (the winning path)

Each stage ships independently, breaks nothing before it, and **benches where it
touches the hot path** — the project's standing rule.

- **Stage 0 — this document + taxonomy.** App / tenant / fleet named and adopted
  (§1). *Verification: this doc; no code.* ✅ (this session)

- **Stage 1 — Option A MVP + isolation escape hatch.** Parameterize the
  control-plane port (`--control-port`, default `:9090`) and make pprof per-app/
  off; add `appitools fleet` (spawn/stop/restart/health + a port registry) and a
  front proxy (Host → port). Federated admin is thin (list apps, proxy through).
  *Hot path: untouched (each app is today's engine). No engine `bench-protocol`
  needed.* *Verification: 3 apps up behind the proxy, each serving its own
  schema, one crashing/restarting without touching the others (live).*
  ✅ **(MT-STRUCT-S1** — `pkg/fleet` + `appitools fleet run|status`, docs/FLEET.md.
  Verified live with 3 apps: per-domain APIs, cross-app JWT rejected (401),
  crash isolation + ~1 s respawn, per-app self-restart with same PID and the
  supervisor untouched, per-app DB auto-bootstrap. Engine diff was ports-only.**)**

- **Stage 2 — in-process registry foundation (B, part 1).** Introduce the
  `App`/`compiledApp` type and the `atomic.Pointer[map[appKey]*compiledApp]`
  registry + the Host→app dispatch, **loaded with exactly ONE app** (a behavioral
  no-op vs today). *Hot path: TOUCHED (the dispatch).* ***`bench-protocol`
  mandatory*** — must confirm the predicted ~no-change (Number 3 says it will).
  *Verification: bench green (p50 no_change) + the full existing suite green with
  the single-app registry.*
  ✅ **(MT-STRUCT-S2** — `registry.go` (`Registry`/`compiledApp`, lock-free
  atomic.Pointer reads, zero-alloc Resolve) wired as the server handler with the
  boot app as sole entry. Microbench on the shipped single-app path: **2.7 ns/op,
  0 allocs** (domain walks for future N apps: 44–124 ns, 0 allocs). E2E
  `bench-protocol` (k6 RATE=50×20 s, 6+6 INTERLEAVED runs, engine restarted per
  run, devhub Mann-Whitney): **verdict `no_change`** — p=0.185, median-diff CI
  **[−6.3 µs, +2.4 µs]** vs the 0.5 ms min-effect; per-run p50 medians
  A=0.585 ms / B=0.596 ms. Suite green + acceptance **39/0** on the registry
  binary — behavior identical. JWT/RBAC untouched (S3's job).**)**

- **Stage 3 — per-app middleware + multi-app compile (B, part 2).** Rebuild the
  chain so JWT/RBAC/cache/validators resolve the **per-request app from context**
  (per-app secret, per-app policy, (app,tenant) cache key) instead of boot
  closures; compile N apps at boot from a fleet manifest; make OpenAPI/GraphQL/
  served-resources per app; generate the OpenAPI doc lazily (Number 2 win).
  *Hot path: TOUCHED (JWT/RBAC/cache).* ***`bench-protocol` + a security review***
  (the auth reorder). *Verification: two different schemas served from one process
  on two Hosts, RBAC/JWT correctly isolated per app (a token for app X rejected on
  app Y), bench green.*
  ✅ **(MT-STRUCT-S3** — `appitools fleet serve` (`ServeFleet`, multiapp.go): the
  SAME fleet.json served IN-PROCESS. Mechanism refinement over this plan's
  sketch: instead of a per-request "app from context" indirection, S3 runs **N
  full `App` instances in one process** — each chain already CLOSES OVER its own
  secret/policy/pool/caches/SSE/obs — registered by domain in the S2 Registry, so
  "app resolved before JWT, with that app's secret" holds by construction at
  zero added per-request cost. One REAL cross-app hole found and fixed in review:
  the package-global claims cache was keyed by token only — a token validated by
  app X would have short-circuited app Y's signature check; now keyed by
  **(secret, token)** (`pkg/auth/claims_cache.go` + regression tests). Unmatched
  Hosts get a process-level 404 (+ health probes), never an arbitrary app.
  **Security matrix: 18/18 vectors PASS live** (2 apps, same resource name +
  same tenant id: JWT cross-app 401 on REST+GraphQL with caches hot, RBAC
  deny-by-default per app, data/cache/SSE/admin-keys/control-planes/signed file
  URLs all isolated, tenant isolation intact). **Bench (3 arms interleaved ×6,
  k6 RATE=50, Mann-Whitney): baseline-vs-S3-single AND baseline-vs-S3-multi both
  `no_change`** (CIs [−13.0,−3.7] µs and [−11.9,−1.9] µs — bounded at
  microseconds vs the 0.5 ms gate; medians 0.623/0.609/0.634 ms). 2 apps in one
  process: **88 MB RSS total** (vs ~154 MB as two Option-A processes). Deploy
  self-restart in fleet-serve = whole-process relaunch (per-app hot-swap is S4);
  per-app env keys not mappable in-process are LOUDLY warned. Suite 0 FAIL +
  acceptance 39/0.**)**

- **Stage 4 — per-app hot-swap deploy.** Evolve self-restart into a per-app
  pointer swap: deploy/migrate/restart ONE app (drain that app's in-flight + SSE,
  recompile, swap) without touching the others. *Hot path: TOUCHED (the swap).*
  ***`bench-protocol`.*** *Verification: live — deploy a new resource to app X,
  swap, confirm X serves it while app Y is undisturbed and never 503s.*

- **Stage 5 — homologate the surface.** Admin UI app selector; obs keyed by
  (app, tenant) with an app filter; Studio deploy chooses the app;
  served-resources per app; DevHub groups by app; per-app config UI. *Hot path:
  untouched (admin/obs are off it).* *Verification: the §5 table, each row live.*

- **(Ongoing) Option A as escape hatch.** Keep the Stage-1 path: any app can be
  pinned to its own process behind the same proxy for total isolation.

## Reproducing the numbers

The measurement harness lived in `scratch_heapmeasure/` during DESIGN-MT-STRUCT-V1
and is intentionally **not committed** (it imports internal packages purely to
measure). To reproduce:

- **Number 1 (RSS/process):** boot several engines with different
  `examples/*/schema.json` on distinct `--port`s (set `OBS_DB_PATH` per process,
  `APPITOOLS_ENV=production` to avoid the `:6060`/`:9090` collisions), then read
  `/proc/<pid>/smaps_rollup` (`Pss`, `Private_Dirty`, `Shared_Clean`).
- **Number 2 (heap/app):** a small program that calls `codegen.BuildRouter` +
  `gqlhandler.BuildHandler` + `codegen.GenerateOpenAPIJSON` + the RBAC
  marshal/unmarshal N times over a loaded schema, keeps them alive, and diffs
  `runtime.MemStats.HeapAlloc` around a `runtime.GC()`.
- **Number 3 (dispatch):** a Go benchmark of `atomic.Pointer` load + Host-label
  parse + `map[string]*slot` lookup at 4/50/500 entries.

All three were run on the 105 box, Go 1.25.11, 2026-07-05.
