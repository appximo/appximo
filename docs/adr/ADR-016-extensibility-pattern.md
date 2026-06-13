# ADR-016: Extensibility pattern — Appitools as a Go library, two extension classes

**Status:** Accepted (design) — implementation pending.
The v1 engine is frozen. Extensibility enters via the standard pipeline:
baseline → change → Mann-Whitney verdict on the 105/58 stack before merge.

---

## Context and problem

The engine is declarative: a JSON schema compiles to routes, SQL, validation,
and RBAC at boot. The two existing escape hatches — the Goja JS sandbox
(watchdog: 80 ms soft / 500 ms hard) and Wazero WASM (16 MiB) — cover short
calculations and transformations. They are not designed for heavy business logic,
custom endpoints, or multi-entity atomic transactions.

Production applications regularly need logic the schema cannot express:
submitting a tax declaration (create `Taxpayer` + `Declaration` atomically,
validate against an external service, enqueue an email), processing an upload,
running a payment workflow. Without a structured extension model, developers are
forced to fork the engine or maintain a sidecar — both options destroy the
"single binary, schema-driven" value proposition.

The reference model is **API Platform** (PHP/Symfony): a declarative scaffolding
layer (attributes on entities → full REST/GraphQL API) combined with a clean
descent to the language when custom logic is needed. State Processors inherit
the authenticated user, the serialised request, and the database connection —
already scoped to the right tenant/context. Appitools replicates this pattern
in Go.

---

## Decision 1: Appitools as a Go library (import, not fork)

The developer writes their own `main.go`, imports `appitools`, registers custom
routes and handlers, and compiles a **single static binary** (`CGO_ENABLED=0 go
build`). They do not fork the engine.

```go
package main

import (
    "log"
    "appitools"
)

func main() {
    app, err := appitools.New(appitools.Config{
        Schema:   "schema.json",
        Port:     8080,
    })
    if err != nil {
        log.Fatal(err)
    }

    // Register a custom synchronous handler (Class 1).
    app.Register(appitools.Route{
        Method:   "POST",
        Path:     "/api/declarations/submit",
        Handler:  SubmitDeclaration,
    })

    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

The **pure binary** that ships today is literally a `main.go` that imports the
library and registers zero custom handlers. The custom binary and the pure binary
are two `main.go` files over the same library — not two runtime modes or build
flags.

This is the same model as **PocketBase**, whose prebuilt executable is built from
`examples/base/main.go`. The developer either uses the prebuilt binary as-is or
imports PocketBase as a library and compiles their own.

### Go plugins (`.so`) are discarded

| Reason | Detail |
|---|---|
| Requires CGO | `plugin.Open` is not available with `CGO_ENABLED=0`; the project is CGO-free end to end. |
| Non-portable | `.so` plugins work on Linux, FreeBSD, and macOS only — not on all target platforms or in scratch Docker images. |
| Toolchain mismatch | A plugin compiled with `go1.24` cannot be loaded by a binary built with `go1.25`; the error (`plugin was built with a different version of package`) appears at runtime. |
| No hot-reload | Plugins must be loaded at startup; downloading and loading at runtime is not safe and not what the model needs. |

---

## Decision 2: Two extension classes and their boundary

The boundary criterion: **does the user wait for the response?**

### Class 1 — Synchronous, in-process handler

Runs inside the HTTP request/response cycle. Receives an `appitools.Ctx` (see
Decision 3) that already has the tenant, authenticated identity, and a
transaction scoped to the right search path.

Use when: the user waits for the result — a validated multi-entity create, a
synchronous calculation, a file processed in under a second.

Analogue: an API Platform **State Processor**.

```go
func SubmitDeclaration(ctx appitools.Ctx) error {
    var body DeclarationInput
    if err := ctx.Bind(&body); err != nil {
        return err
    }

    tx := ctx.Tx()
    taxpayerID, err := ctx.Insert(tx, "taxpayers", map[string]any{
        "nit":  body.NIT,
        "name": body.Name,
    })
    if err != nil {
        return err
    }

    _, err = ctx.Insert(tx, "declarations", map[string]any{
        "taxpayer_id": taxpayerID,
        "period":      body.Period,
        "total":       body.Total,
    })
    if err != nil {
        return err
    }

    // Enqueue the confirmation email atomically in the same transaction.
    return ctx.Enqueue(tx, "send_declaration_email", map[string]any{
        "taxpayer_id": taxpayerID,
    })
}
```

### Class 2 — Asynchronous, external worker

The user does NOT wait. Work is durable (outbox pattern), processed by a
separate goroutine or process.

Pattern:
1. A Class 1 handler (or a generated route's hook) calls `ctx.Enqueue` —
   writes a row to the `outbox` table inside the **same transaction** as the
   business write. If the transaction rolls back, the job is never enqueued.
2. The engine emits `pg_notify('outbox', ...)` after commit.
3. A worker process `LISTEN`s; on notification it selects the job with
   `SELECT … FOR UPDATE SKIP LOCKED` (prevents duplicate delivery under
   multiple workers) and processes it.
4. Jobs are **at-least-once**: the worker must be idempotent
   (idempotency key stored alongside the job).

Analogue: an API Platform **Messenger handler**.

### The bridge

A Class 1 handler calls `ctx.Enqueue` in its **transaction**. If the handler
returns an error (transaction rolls back), the enqueue never happens. This gives
atomic "create entity + schedule background work" without a two-phase commit.

```
POST /api/declarations/submit
  └─ Class 1 handler (SubmitDeclaration)
        ├─ INSERT into taxpayers     ─┐
        ├─ INSERT into declarations   ├─ same pgx.Tx, same search_path
        └─ INSERT into outbox        ─┘
              └─ NOTIFY outbox  ← fired after Tx commits by the engine
                    └─ Class 2 worker: send email
```

---

## Decision 3: the `appitools.Ctx` interface (inherited context)

A Class 1 handler receives one argument — `appitools.Ctx` — which carries the
full request context already resolved. The handler never re-authenticates, never
re-scopes the tenant, never resolves the role.

```go
type Ctx interface {
    // Identity — already verified by the middleware chain.
    Claims()    jwt.MapClaims   // full JWT claims
    Tenant()    string          // e.g. "acme" (from Host subdomain)
    Role()      string          // JWT "role" claim
    Allowlist(resource string) []string // field allowlist for this role

    // Database — transaction already scoped to the tenant search_path.
    Tx() pgx.Tx

    // UnsafeTx bypasses RBAC helpers but tenant isolation still holds.
    // Explicitly named to make audits easy (grep UnsafeTx).
    UnsafeTx() pgx.Tx

    // RBAC-aware helpers — inject row filter, validate allowlist,
    // project permitted fields. Use these by default.
    Query(tx pgx.Tx, resource string, filters map[string]any) ([]map[string]any, error)
    Insert(tx pgx.Tx, resource string, data map[string]any) (uuid.UUID, error)
    Update(tx pgx.Tx, resource string, id uuid.UUID, data map[string]any) error

    // Request helpers.
    Bind(dst any) error                      // JSON decode + 1 MB cap
    BindResource(resource string, dst any) error // Bind + schema validation

    // Response helpers.
    JSON(status int, body any) error
    Error(status int, code string, msg string) error

    // Outbox — enqueue a job in the current transaction (atomic).
    Enqueue(tx pgx.Tx, jobType string, payload map[string]any) error

    // Standard library context (cancellation, deadlines).
    Context() context.Context
}
```

Key design points:
- `Claims()`, `Tenant()`, `Role()`, `Allowlist()` return what the middleware
  already resolved — no second parse, no second DB lookup.
- `Tx()` is the transaction created by the middleware with the tenant
  `search_path` already applied (see Decision 4). The handler commits or rolls
  back by returning `nil` or an `error`; the middleware owns the transaction
  lifecycle.
- `UnsafeTx()` returns the same transaction but signals to the reader (and to
  grep) that RBAC helpers are being bypassed.

---

## Decision 4: security — multi-tenant isolation and RBAC (central, not a footnote)

### A — Tenant isolation that cannot be broken by a handler

The handler never receives the connection pool. It receives a `pgx.Tx` that was
opened by the middleware and whose `search_path` was set to the tenant schema
via `set_config`:

```go
// Inside the middleware, before the handler is called:
tx, err := pool.Begin(ctx)
_, err = tx.Exec(ctx,
    "SELECT set_config('search_path', $1, true)",
    "tenant_" + tenant,
)
// Hand the tx to the handler via Ctx.
```

Why `set_config` and not `SET LOCAL search_path = …`?

- `SET LOCAL search_path = $1` is a **syntax error** — `SET` does not accept
  bind parameters. The only safe workaround would be to concatenate the string,
  which reintroduces identifier injection.
- `set_config('search_path', $1, true)` passes the schema name as **data**,
  not as SQL text. `$1` is the bind parameter; Postgres never executes the
  schema name as SQL.
- The `true` argument makes the change **transaction-local**: it reverts
  automatically on `COMMIT` or `ROLLBACK`. The connection returns to the pool
  with a clean `search_path`.

The risk of not doing this is documented in the pgx issue tracker (pgx #2007):
with a session-level `SET search_path`, the path leaks to the next request that
reuses the pooled connection — data from tenant A becomes visible to tenant B.
`set_config` + `true` closes this class of bug structurally.

A handler that uses `ctx.UnsafeTx()` directly issues SQL via a transaction that
still has the correct `search_path` applied. The bypass is of **RBAC helpers**,
not of multi-tenant isolation. There is no API that exposes the raw pool.

### B — RBAC with inverted default (safer than PocketBase and API Platform)

Two query paths, one of which requires explicit intent to use:

| Path | RBAC | How to use |
|---|---|---|
| `ctx.Query / Insert / Update` | **Applied**: injects row filter, validates allowlist, projects permitted fields | Default — works out of the box |
| `ctx.UnsafeTx()` + raw SQL | **Bypassed** | Must type "Unsafe"; searchable by `grep UnsafeTx` |

In **PocketBase**, `app.FindAllRecords(collection, ...)` does **not** apply API
rules. A developer who uses the library method to read records inside an
`OnRecordBeforeCreateRequest` hook gets all rows, bypassing visibility rules —
accidentally and silently. The audit trail requires hunting for every call site.

In **API Platform**, `StateProcessor` implementations call the entity manager
directly; persisting or loading an entity bypasses security voters unless the
developer explicitly calls the security layer.

Appitools inverts the default: bypassing RBAC is opt-in and named.
`grep UnsafeTx` in a code review is a complete audit of all bypass sites.

### C — Custom routes in the same middleware chain

Routes registered with `app.Register` go through the **identical middleware
chain** as generated routes:

```
Host header → tenant resolution
→ rate limit (per-tenant token bucket)
→ response cache (for GETs)
→ JWT verification (HS256, exp required)
→ RBAC check (role from JWT claim)
→ custom handler (via Ctx)
```

The handler does not need to re-implement any of these layers. A custom route
registered without an explicit RBAC rule gets the same "deny by default"
behavior as generated routes.

Custom routes are:
- Validated at boot for path collisions against generated routes
- Emitted in the OpenAPI spec (`/api` prefix, with the registered schema if
  provided)
- Registered at boot only, not dynamically post-startup (same constraint as
  PocketBase; no concurrency hazard on the chi router)

### D — Input validation

`ctx.BindResource(resource, dst)` validates the decoded body against the
**compiled** schema rules for the named resource (the same rules that REST
and GraphQL handlers use). For endpoints not tied to a schema resource,
`ctx.Bind(dst)` decodes and applies the 1 MB body cap.

---

## Decision 5: semver contract of the extension interface

The public extension surface — `appitools.Ctx`, `Claims`, `Route`, `Config`,
`Handler`, `New`, `Register` — is **frozen at the major version boundary**.
Breaking changes require a major version bump.

Internal components — the schema compiler, chi router, middleware chain, outbox
format, tenant resolver — may change in minor and patch releases.

**Motivation (PocketBase lesson):** PocketBase broke its hook API in v0.23:
`OnBeforeServe` → `OnServe`, `echo.Context` → `RequestEvent`. Applications that
imported PocketBase as a library had to update every hook site on upgrade.
Appitools must stabilise `Ctx` **before** promoting the library model as stable
in user-facing documentation. Until that point, the library interface is
`experimental` and the semver guarantee does not apply.

---

## Consequences

### Positive

- The **API Platform model in Go**: declarative scaffolding + descent to the
  language with the full context already resolved — tenant, identity, transaction.
  The handler writes business logic, not infrastructure.
- Multi-tenant isolation is **structural**, not advisory: the handler has no API
  path to the raw pool or another tenant's data.
- RBAC is **opt-out** (with a named, grep-able method), not opt-in — the
  PocketBase/API Platform accidental-bypass class of bug is eliminated.
- The custom handler reuses the same middleware chain that generated routes pay.
  There is no incremental infrastructure overhead for adding a custom endpoint.
- CGO-free, single binary: the developer compiles exactly the same way as the
  pure binary, with the same toolchain and the same `make build` command.

### Costs and constraints

- **Go compilation loop** vs interpreted languages (PHP, JS): changing a hook
  requires a recompile. Mitigated by the existing Goja sandbox for short
  calculations that do not require recompile. Class 1 handlers are for logic
  that is complex enough that a compile step is appropriate.
- **`Ctx` must be stabilised carefully** before being promoted as stable. Until
  then, the library API is experimental and any version can break it.
- **`pgx` prepared statement caching** under schema-per-tenant: `pgx` v5 caches
  prepared statements per connection by OID; with many tenant schemas, statement
  OIDs can collide across connections. Mitigation: set
  `DefaultQueryExecMode: pgx.QueryExecModeSimpleProtocol` on the pool
  (or `QueryExecModeDescribeExec`) when `search_path` changes per transaction.
  This is a known pgx operational gotcha for schema-per-tenant setups.

---

## Data the implementation must measure (not assume)

The investigations did not find a benchmark for the overhead of
`BeginTx` + `set_config` on a 1–2 vCPU VPS. The claim is sub-millisecond by
inference.

However: the hot path **already** pays `BeginTx` + `set_config` on every
generated-route request. Custom handlers registered via `app.Register` go
through the same middleware — the incremental overhead of a Class 1 handler over
a generated route is **zero by construction** (same transaction already open,
`set_config` already executed).

This should be verified empirically with the standard pipeline
(`bench-protocol.sh`, Mann-Whitney, 10 runs, CV < 5 %, threshold
`max(0.5 ms, 3 % × median_A)`) comparing a generated route vs an equivalent
Class 1 handler before merge.

---

## Suggested implementation order (when the engine unfreezes)

1. Extract engine internals into `pkg/engine` with stable exported surface
   (`appitools.New`, `Config`, `Route`, `Handler`, `Ctx`).
2. Refactor `cmd/appitools/main.go` to be the zero-handler consumer of the
   library (validates the model; no feature change).
3. Implement `Ctx` with the full interface — `Tx`, `UnsafeTx`, `Query`,
   `Insert`, `Update`, `Bind`, `BindResource`, `Enqueue`, `JSON`, `Error`.
4. Wire `app.Register` into the chi router at boot, in the standard middleware
   chain, with collision detection and OpenAPI emission.
5. Implement the outbox table + `ctx.Enqueue` + `pg_notify` + worker scaffolding.
6. CI gate: no-regression test comparing generated route vs Class 1 handler
   latency (Mann-Whitney, must return `no_change`).
7. Mark `Ctx` as `experimental` in godoc; promote to stable only after at least
   one full release cycle without breaking changes.

---

## Alternatives discarded

| Alternative | Reason discarded |
|---|---|
| Fork the engine per application | Destroys the "import and upgrade" model; merging engine fixes back is manual forever. |
| Go plugins (`.so`) | Requires CGO (violates CGO-free constraint); non-portable; toolchain mismatch errors at runtime; no hot-reload. |
| Give the handler the raw pool | Cross-tenant data access via `SET search_path` at session level; documented pgx #2007 leak. |
| `ctx.DB()` raw without an Unsafe marker | Accidental RBAC bypass (the PocketBase/API Platform pattern); auditing requires reviewing every call site. |
| Goja/Wasm for heavy business logic | Watchdog 80 ms is incompatible with multi-step workflows, external HTTP calls, and long-running calculations. |
| Separate sidecar process for custom logic | Adds operational complexity (two binaries, two deployments, inter-process auth); violates "single binary" constraint. |
