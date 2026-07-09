# The Appitools mental model — one schema, many tenants

This document answers, with the code as the authority, the two questions every
new user of Appitools eventually asks:

1. *"If it's multi-tenant, why does `/docs` show one API for everyone?"*
2. *"I deployed a change from the editor — why do I (sometimes) need a restart?"*

Every claim below was verified against the source (file + symbol given — symbol
references survive edits; find them with your editor's go-to-symbol or grep) and,
where it matters, against a **running engine** (a live tenant was migrated and
probed; see [§4](#4-the-change-cycle--what-a-deploy-activates-live-and-what-needs-a-restart)).

## 1. The three artifacts

- **The schema** (`schema.json`) is the *definition of the application*:
  resources, fields, relations, RBAC, hooks, state machines. The visual editor
  (`/editor`) designs it; `appitools validate` checks it; it is the single input.
- **The engine** (the binary) *compiles* that schema at boot into a live server:
  REST routes, GraphQL types, RBAC middleware, validators, hooks, OpenAPI
  (`pkg/codegen.BuildRouter`, called once in `app.go` `buildRouter`).
- **A tenant** is an *instance with isolated data* on that structure: its own
  Postgres schema (`tenant_acme`), its own rows, its own users
  (`tenant_acme.auth_users`), addressed by Host subdomain.

The model is deliberately **"one API structure, N tenants with isolated data"**
— the Shopify shape (every shop has the same app structure, no shop sees
another's data), not the "every tenant is a different app" shape.

## 2. What is global (compiled once from the boot `--schema`)

Compiled at process start and identical for every tenant:

| Global artifact | Built where |
|---|---|
| REST routes (all CRUD + `/aggregate` + `/events` + relation subroutes) | `codegen.BuildRouter`, mounted once in app.go `buildRouter`; iterates boot `s.Resources` once |
| GraphQL types + resolvers | `gqlhandler.BuildHandler(a.schema, …)` (app.go `buildRouter`) |
| RBAC policy middleware | `rbac.RBACMiddleware(mustMarshal(a.schema.RBAC))` (app.go `buildRouter`) |
| Declarative validators (S44 rules, state machines, defaults) | `schema.CompileRules` per resource at the top of `codegen.BuildRouter` |
| Hooks (js/webhook/wasm config) | captured per-handler from boot `res.Hooks["before_create"]` etc. inside `codegen.BuildRouter` |
| OpenAPI spec + `/docs` | `codegen.GenerateOpenAPIJSON(a.schema, …)` once at boot (openapi_serve.go `registerOpenAPIRoutes`) |
| `/admin/served-resources` (the editor's restart hint) | boot resource list (`servedResources` in app.go `New`) |

**Nothing on the request hot path consults a per-tenant schema.** The
per-tenant stored schema (`public.tenants.json_schema`) is read only by the
admin/control-plane APIs (deploy, reload, editor load-back); the
`tenant.SchemaCache` is written by the migration worker and read only by the
admin reload endpoint — it has **zero effect on routing, validation or RBAC**.

That is why `/docs` shows one API: the OpenAPI document *is* the shared
structure, generated from the boot schema, the same for every tenant (it is
even served unauthenticated for exactly that reason — see the comment atop
openapi_serve.go `registerOpenAPIRoutes`).

## 3. What is per-tenant (isolated)

| Per-tenant | Mechanism |
|---|---|
| **All data** — tables and rows | Postgres schema-per-tenant; every query runs inside a tx with `SET LOCAL search_path TO tenant_<id>` (every `setPath` site in pkg/db/tenant.go `TenantDB`) |
| Users, tokens, identities, MFA | `tenant_<id>.auth_users` / `auth_tokens` / `auth_identities` / `auth_mfa` — email unique **per tenant** |
| The *physical* table set | migrations apply to ONE tenant's schema (controlplane/service.go `UpdateSchemaApproved` → `migration.ApplyTenantMigrationApproved`; provisioning in tenant_service.go `RegisterTenant`) |
| Stored schema copy | `public.tenants.json_schema` — the deploy/migration input, NOT a routing input |
| Rate-limit buckets, response-cache entries, observability rings/SLO, outbox `tenant_id` | keyed by tenant id from the Host middleware |
| Files (CAS blobs + `files` table) | tenant-prefixed paths + per-tenant metadata table |

Isolation is structural: the tenant comes from the Host subdomain, the
`search_path` is set `LOCAL` inside the transaction, and a cross-tenant token
is a 401. Two tenants can never see each other's rows — but they always share
the same *route surface*.

## 4. The change cycle — what a deploy activates live, and what needs a restart

A deploy (editor "Deploy", control-plane `PUT /tenants/{id}/schema`, or
`appitools migrate`) does four things: persists the schema to
`public.tenants.json_schema`, **appends it to the tenant's version history**
(`public.schema_history`, append-only — the base of "roll back to vN"; see
pkg/schemahistory), runs the **real migration** against that tenant's
tables (diff → production-safe DDL), and fires `pg_notify(schema_updated)`
which only **invalidates the response cache** (app.go `startCacheInvalidator`).
It never recompiles routes, GraphQL, RBAC, validators or hooks.

The consequences, **verified live** (engine booted with one schema, a tenant
migrated to a schema with a new column `notas`, then probed without restart):

| After deploying, without restart… | Live? | Why |
|---|---|---|
| New/renamed **table or column** exists in the tenant DB | ✅ | the migration engine ran |
| **Write** the new column (POST/PUT/PATCH, REST) | ✅ | the insert/update is body-driven; keys are NOT whitelisted against the boot schema — the DB is the source of truth, an unknown column's 42703 maps to a clean 422 `unknown_field` (the insert error path in `codegen.BuildRouter` → handlers/errors.go `WriteDBError`) |
| **Read** the new column (GET list/get, REST) | ✅ | `SELECT *` returns whatever columns the tenant table has |
| Declarative **validation** of the new column (`maxLength`, `pattern`, …) | ❌ | validators are boot-compiled — a 300-char value passed a deployed `maxLength: 200` in the live test |
| `filter[new_column]` | ❌ | the query builder validates filter fields against the boot resource (400 `unknown filter field`) |
| The new column in **GraphQL** | ❌ | types are boot-compiled (`Cannot query field "notas" on type …`) |
| The new column in **/docs** (OpenAPI) | ❌ | the spec is generated once at boot |
| RBAC that names the new column (allowlist/condition) | ❌ | policy parsed at boot |
| **Hook** changes | ❌ | hook config is captured in handler closures at boot; the reload endpoint honestly warns (app.go `reloadHandler` + `hooksDiffering`) |
| A **new resource** (routes, GraphQL type, /docs entry) | ❌ | no route exists at all — the editor detects this via `/admin/served-resources` and shows the restart banner (UI-F3-S1) |

So the honest one-liner is: **the migration is live; the definition is
compiled.** Raw CRUD of new columns works immediately (by design — the DB is
the source of truth for write keys); everything *derived from the schema
definition* (validation, filters, GraphQL, docs, RBAC, hooks, routes) is the
boot snapshot until the process restarts with the new schema.

**To see your change in `/docs`:** restart the engine with the edited schema —
**one click from the editor** since UI-F4-S2: the deploy result's "Restart
engine now" button persists the schema as the new boot schema and gracefully
self-restarts (see [§7](#7-is-compiled-at-boot-fundamental-or-evolvable)).
That single restart activates everything in the ❌ rows at once.

## 5. Deploy scope — one tenant, not all

The editor's Deploy (and `PUT /admin/tenants/{id}/schema`, and the control
plane's `PUT /tenants/{id}/schema`) migrates **exactly one tenant**
(controlplane/service.go `UpdateSchema` / `UpdateSchemaApproved`). There is no
fan-out in the editor or the admin API.

Concrete example: you have tenants `acme` and `demo`; you add resource
`invoices` in the editor and deploy to `acme` → the `invoices` **table** is
created in `tenant_acme` only. `tenant_demo` does not change. (And the
`/api/invoices` **route** doesn't exist for anyone until a restart.)

- Structural **divergence between tenants is possible** and tolerated: a tenant
  missing a table that the global routes serve gets a clean
  `400 "invalid tenant"` (42P01/3F000 classified in handlers/errors.go
  `WriteDBError`), never a raw error.
- Keeping N tenants in structural sync is the CLI's job:
  `appitools migrate --all-tenants --schema base.json` — the resumable,
  partial-failure-tolerant fan-out (`migration.RunFanout`, called from the
  `--all-tenants` branch of cmd_migrate.go). **RunFanout is reachable only from
  the CLI today**;
  the editor is per-tenant by design (divergence during a rollout is the
  expected transient state, and a mass destructive change must stay a
  deliberate, operator-driven act).

The intentional model is therefore: **same structure for all tenants; per-tenant
deploys are the rollout mechanism, not a per-tenant-app feature.** A tenant
"ahead" of the boot schema (extra columns) is usable for raw CRUD immediately;
a tenant "behind" (missing tables) degrades to clean 400s on those resources.

## 6. Automatic infrastructure vs declarable

Everything a resource *declares* lives in the schema. Everything else is
engine infrastructure that every resource/tenant gets automatically — there is
nothing to declare and therefore nothing for the editor to draw:

| Automatic (no schema key exists) | Declarable (schema keys) |
|---|---|
| **SSE** — `GET /api/{r}/events` is mounted unconditionally for every resource (the `sseHandler` mount in `codegen.BuildRouter`) | fields + all field properties (type, required, unique, auto, default, enum, min/max, minLength/maxLength, pattern, format) |
| Response cache (5 s TTL, role-gated) | `relation`, `on_delete`, `on_update`, `references`; resource-level `foreign_keys` |
| Rate limiting (env-configured) | `relations` block (has_many / belongs_to / many_to_many, `limit`) |
| Aggregation `GET /api/{r}/aggregate` (the aggregate route in `codegen.BuildRouter`) | `indexes` |
| `POST /api/transaction` (the transaction mount in `codegen.BuildRouter`) | `state_machine` |
| File store `/api/files` (unless a resource is named `files`) | `hooks` (before/after; js/webhook/wasm) |
| Auth `/auth/*` (env-gated features) | `events` (outbox opt-in — the one per-resource emission switch) |
| OpenAPI `/openapi.*` + `/docs`, CORS, observability | `rbac` (both forms), `renamed_from` (field + resource) |

**SSE specifically**: it is infrastructure, like the cache — there is no
per-resource opt-in/out or channel config anywhere in `pkg/schema`
(types.go/keys.go have no SSE key). The editor not showing SSE is correct, not
a parity gap. RBAC still applies to the stream at delivery time.

## 7. Is "compiled at boot" fundamental or evolvable?

It is a **deliberate performance choice, not an accident**: compiling routes,
validators, RBAC and GraphQL once means the request path executes only
precompiled closures — the property the public benchmark rests on. But it is
**evolvable**, in increasing order of cost:

1. **Graceful self-restart after deploy** — **IMPLEMENTED (UI-F4-S2)**:
   `POST /admin/engine/schema` (super-admin auth, same as the deploy) validates
   the schema, persists it ATOMICALLY as the boot schema (previous kept at
   `<schema>.bak`), then drains through the normal shutdown path (`/readyz`→503
   ~5 s, in-flight requests finish) and **re-execs** the process — supervisor-
   agnostic (same PID; works for a loose process, systemd and Docker alike).
   ~6 s of unavailability, verified live. An invalid schema is rejected with
   nothing written and NO restart; if the relaunch cannot load the schema, boot
   auto-restores the `.bak` (marker-gated rollback, restart.go). The editor's
   restart banner is now an **"Restart engine now" button** with an explicit
   consent step, progress (drain → relaunch) and a served-resources
   verification when the engine returns.
2. **Hot router swap** — **IMPLEMENTED per-app in the in-process fleet
   (`appitools fleet serve`, MT-STRUCT-S4)**: a deploy rebuilds one app's
   `BuildRouter` + GraphQL + RBAC + OpenAPI from the new schema into a fresh
   handler and swaps it behind the registry's `atomic.Pointer` — no process
   restart, the other apps untouched, in-flight requests finishing consistently
   on the old surface. The hard parts named here were handled: the middleware
   chain is rebuilt from an explicit `builtSurface` (schema + policy) rather than
   closing over boot globals; the SSE hub and response cache are shared across
   the swap (live connections survive; the cache gate is re-set atomically); the
   old closures are GC'd once their in-flight requests finish. Benched
   `no_change`; race-detector clean. In **single-engine** mode a deploy still
   uses the whole-process re-exec (item 1) — there are no sibling apps to
   protect, so the simpler path is retained. See docs/design/MT-STRUCT.md Stage 4.
3. **Per-tenant API surfaces** (different routes per tenant): this is the deep
   assumption. Routing, GraphQL type identity, the response cache keying, the
   OpenAPI contract and `/docs` all assume ONE structure. Making the surface
   per-tenant means per-tenant routers/GraphQL schemas in memory and a
   per-tenant contract — a different product shape (and a real memory cost per
   tenant). Nothing requires it for the current "one product, N tenants" thesis.

Today the practical answer for "I added a resource": deploy from the editor
(the tables are migrated), then click **"Restart engine now"** in the deploy
result — the engine persists the schema, drains, relaunches, and the editor
confirms the new resource is served. No terminal involved.

And for "I want the previous version back": Studio's **History** view lists
every deployed version (append-only, hash-identified) and rolls back to any of
them through the SAME preview → destructive-gate → apply machinery as a deploy
(`POST /admin/tenants/{id}/schema/rollback`) — what later versions added is
reverted as gated drops with measured impact, data already lost to an approved
forward drop is not recoverable, and the rollback itself is recorded as a new
version.
