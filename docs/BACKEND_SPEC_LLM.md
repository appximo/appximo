# Build a complete backend with Appitools — the agent guide

You are an AI coding agent (Claude Code, Cursor, or similar). This document
teaches you to build a **complete, secure backend** with Appitools: not only the
declarative schema, but the **custom Go handlers, hooks, auth and background
jobs** that a real product needs — and to do it **safely**, following the
in-process safety model the engine enforces.

Appitools is a Go engine that compiles a JSON schema into a multi-tenant
REST + GraphQL + OpenAPI server at boot. Most of an app is *declarative* (the
schema). The rest — logic that spans resources, calls external systems, or runs
in one transaction — is a **custom handler**: plain Go, imported as a library,
compiled into the same static binary, running **in-process** with the tenant's
transaction and RBAC already resolved. That in-process model is the differential
(no network hop, one transaction, the engine's own validation + RBAC), and this
guide is how you wield it without the footguns.

Two companion documents; keep them straight:

| Doc | Teaches | Command |
|---|---|---|
| **`appitools spec`** / [SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md) | the **schema** (declarative surface) | `appitools spec` |
| **this doc** / `appitools backend-spec` | the **backend** (handlers + hooks + auth + jobs) | `appitools backend-spec` |
| **`appitools frontend-spec`** / [FRONTEND_SPEC_LLM.md](FRONTEND_SPEC_LLM.md) | the **frontend** (the API contract a UI consumes, screen states, files) | `appitools frontend-spec` |
| [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md) | the complete human reference | — |

(`appitools specs` prints all three at once — one paste gives an agent the
whole contract.)

Everything below is audited against the engine source and demonstrated by a
compiling, runnable example: **[examples/backend-guide/](../examples/backend-guide/)**
(`schema.json` + `main.go`). Every code block here is faithful to that example.
Do not invent API surface beyond what is listed — if a method is not here, the
Ctx does not have it.

---

## 1. Where does each piece of logic go?

The first decision for any feature: which layer owns it. Get this right and most
of the backend is declarative; only genuine logic becomes code.

| Put it in… | When the logic is… | Cost / guarantees |
|---|---|---|
| **the schema** (resources, fields, validation, RBAC, relations, state machines, events, indexes) | structural: shape, types, constraints, who-can-do-what, lifecycle | zero code; compiled at boot; the default — reach for it first |
| **a hook** (`before_create`/`before_update`, `js`/`wasm`) | pure per-record validation or transformation, no I/O | sandboxed, watchdog-timed; **no HTTP, no DB, no auth** — see §4 |
| **a custom handler** (`app.Register(Route{…})`) | logic needing a transaction, cross-resource writes, external calls, computation, or a pre-auth endpoint | in-process; the tenant tx + RBAC resolved for you; this doc |
| **a background job** (`Ctx.SafeGo` or the outbox worker) | work that happens *after* responding | goroutine = at-most-once, non-durable; outbox = durable, retryable — see §6 |
| **an external service** | almost never | a network hop you must secure and operate yourself |

Two principles decide the ambiguous cases:

1. **Default in-process.** If it needs the transaction, RBAC, or the request's
   data, it belongs in a handler — not a separate service. The whole point is
   that a handler runs *inside* the engine with everything resolved.
2. **External side effects go after the commit; durability decides how.** Never
   call an external system *inside* the transaction (a slow/failed call would
   hold or poison the tx). If the side effect must survive a crash and be
   retried (a payment, a provisioning call), **enqueue it to the outbox**
   (`Ctx.Enqueue`) — it commits atomically with your write and a worker delivers
   it. If it is best-effort and losing it is acceptable (a metric ping, a cache
   warm), fire it with `Ctx.SafeGo` after you respond.

---

## 2. The schema (declarative surface)

The schema is generated and validated with the **other** flow — run `appitools
spec`, generate against it, and self-correct with `appitools validate --json`
until valid ([SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md)). This doc assumes you have
a valid schema; it covers only the code that sits on top.

One rule that matters for handlers: **resource CRUD is already generated.** A
resource `students` gives you `GET/POST/PUT/PATCH/DELETE /api/students` with
filters, sort, pagination, validation and RBAC — for free. Do **not** write a
custom handler for plain CRUD; write one only for logic the schema can't express.
Custom routes may not live under a resource's `/api/<resource>` prefix (it's
owned by the generated routes — a collision is rejected at boot).

Four schema facts that change how you write handlers:

**Generated reads are `SELECT *`.** The list/get path selects every column of the
table, not the declared field list. That is load-bearing: a column that exists in
the database but *not* in the schema still comes back on `GET /api/products`.
Writes are the opposite — an undeclared key is rejected `422 unknown_field` — so
an undeclared column is **readable but only writable from your own SQL**. This
used to be the escape hatch for anything the grammar couldn't express (see
"undeclared columns" below).

**Money is `int64` in minor units.** There is no `decimal`/`money` type, and
`float64` money is a rounding bug on a timer. Store cents (or the currency's
smallest unit) in an `int64` and put the unit in the name — `price_cents`,
`total_cents` — so it is impossible to misread at a call site. Most payment APIs
(Stripe, Wompi, Adyen) speak minor units too, so money never converts. Format for
display at the edge, never in the database.

**Documents go in `jsonb`, not `json`.** `jsonb` is a real Postgres jsonb column:
containment (`@>`) works and an `indexes` entry can declare
`"method": "gin"` (with `"opclass": "jsonb_path_ops"` when the index only ever
answers `@>`). `json` is stored as TEXT — exact bytes preserved, but nothing you
can query or index. Use `jsonb` for merchant-defined attributes, settings blobs,
raw webhook payloads you may need to search. pgx decodes a `jsonb` column straight
into a Go `map[string]any` and encodes a Go map back — no manual marshalling.

**Undeclared columns: still supported, no longer the default answer.** Creating
your own column with `BeforeStart` DDL works — the engine's migration is additive
and reports a column it does not know about as `gated_drops` (drift) rather than
dropping it. Reach for it only for what the grammar genuinely cannot express:
**CHECK constraints, generated columns, partial indexes** (an index `WHERE`
predicate is deliberately absent: it is arbitrary SQL rendered into DDL, and the
schema is also written by `ai-generate` and edited in Studio — see
[ADR-022](adr/ADR-022-declarative-surface-boundaries.md) for the full reasoning
and the structured-predicate design that would reopen it).
Anything the grammar *can* express — columns, btree/gin indexes, foreign keys,
unique constraints — belongs in `schema.json`, where the migration engine owns it.

---

## 3. Custom handlers in Go — the core

### 3.0 Getting the dependency — READ THIS FIRST

**The Appitools module is not published.** `github.com/miguelangel/appitools` is a
private repository with no release tag, so `go get github.com/miguelangel/appitools`
and a bare `go mod tidy` **fail** — there is no version to fetch. An agent that
guesses a version (`v0.1.0`) will produce a project that does not build, which is
exactly what happened the first time this document was used
(docs/AUTHORING_JOURNEY.md 5-7).

**The recipe that works today** is a local checkout plus a `replace`:

```bash
git clone <your-appitools-checkout> /path/to/appitools   # or use the one you already have
mkdir mybackend && cd mybackend
go mod init example.com/mybackend
```

```go.mod
module example.com/mybackend

go 1.25

require github.com/miguelangel/appitools v0.0.0

replace github.com/miguelangel/appitools => /path/to/appitools
```

```bash
go mod tidy      # now resolves: the replace points at real source on disk
go build -o mybackend .
```

What this costs you, stated plainly:

- **The path is absolute and machine-specific.** The project builds on the machine
  that holds the checkout and nowhere else — not on a teammate's laptop, not in CI,
  not in a plain `docker build` (the checkout is outside the build context).
- The Appitools checkout must be on the SAME Go version line (1.25) as your project.
- `v0.0.0` is a placeholder: the `replace` wins, so the version string is never
  resolved. Do not spend effort choosing it.

**When the module is published** (a public repo, or a private one plus
`GOPRIVATE`), the recipe collapses to the normal one and the `replace` line is
deleted:

```bash
go get github.com/miguelangel/appitools@v1.0.0   # a real tag
```

with, for a private repo:

```bash
export GOPRIVATE=github.com/miguelangel/*
git config --global url."git@github.com:".insteadOf "https://github.com/"
```

Nothing else in this document changes: the import path, the API and every example
below are already written against the final path. This section is the only part of
the 10 % path that is blocked on a decision rather than on code — it is tracked as
**DOC-2** in `docs/BACKLOG.md`.

### 3.1 The program shape

A backend is a `main` that imports `github.com/miguelangel/appitools`, builds the
engine, registers routes, and starts it. The pure `appitools serve` binary is
exactly this with zero registered routes — your custom binary boots identically.

```go
package main

import (
	"log"

	"github.com/miguelangel/appitools"
)

func main() {
	app, err := appitools.New(appitools.Config{
		SchemaPath: "schema.json",
		// DSN / JWTSecret / AdminKey / Env / Port fall back to DATABASE_URL /
		// JWT_SECRET / ADMIN_KEY / APPITOOLS_ENV / 8080. All three secrets are
		// required (from Config or env) or New returns an error.
	})
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Register(appitools.Route{
		Method: "POST", Path: "/api/hello",
		Handler: func(ctx appitools.Ctx) error {
			return ctx.JSON(200, map[string]any{"hello": ctx.Tenant()})
		},
	}); err != nil {
		log.Fatal(err) // a bad route is a boot-time error, never a runtime surprise
	}

	if err := app.Start(); err != nil { // blocks; graceful drain on SIGTERM
		log.Fatal(err)
	}
}
```

`New` → `Register` (any number, **before** `Start`) → `Start`. `Register` after
`Start` is an error (routes are wired at boot).

**Boot work — `Config.BeforeStart`.** For anything that must be true before the
first request (your own DDL, seeds, a warm-up), use the boot hook rather than
opening a second pool from `DATABASE_URL` — which drifts from the engine's own
configuration:

```go
app, err := appitools.New(appitools.Config{
	SchemaPath: "schema.json",
	// Runs after the pool is open and the schema compiled, BEFORE the listener
	// accepts anything. A non-nil error ABORTS the boot — an app whose invariants
	// failed to install must not serve traffic.
	BeforeStart: func(ctx context.Context, pool *pgxpool.Pool) error {
		// The pool is NOT tenant-scoped: set the search_path yourself,
		// transaction-locally, as DATA — never by string concatenation.
		tx, err := pool.Begin(ctx)
		if err != nil { return err }
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", pgSchema); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `ALTER TABLE variants ADD CONSTRAINT chk_stock
		    CHECK (reserved >= 0 AND reserved <= stock)`); err != nil {
			return err
		}
		return tx.Commit(ctx)
	},
})
```

`app.Pool()` exposes the same pool if you need it outside the hook. Do **not**
close it — its lifetime belongs to the App. Inside a request, always prefer `Ctx`
(whose transaction already carries the tenant's search_path and the role's row
filter). See [examples/backend-guide/main.go](../examples/backend-guide/main.go)
`ensureInvariants` for the full loop over tenants.

**Per-tenant work — `Config.OnTenantProvisioned`.** `BeforeStart` covers the
tenants that exist AT BOOT; a tenant registered while the app is LIVE (the
normal flow of a multi-tenant SaaS) needs the same DDL. Set the per-tenant twin
— it runs inside every registration, after the engine provisions the tenant's
tables, all-or-nothing (your error rolls the registration back):

```go
OnTenantProvisioned: func(ctx context.Context, pool *pgxpool.Pool, tenantID, pgSchema string) error {
	return applyMyDDL(ctx, pool, pgSchema) // same idempotent DDL BeforeStart applies
},
```

Without it, a fresh tenant is missing your DDL until a restart — a 500 on any
endpoint that depends on it (the exact bug PROD-JOURNEY-1B measured).

**The deployable contract — `appitools.ParseServeArgs`.** For the binary to be
installable/updatable by the official production tooling (ADR-023), main()
starts with:

```go
var version, revision = "dev", "unknown" // -ldflags -X main.version=… (scripts/build-consumer.sh)

args := appitools.ParseServeArgs("myapp", version, revision,
	appitools.ServeArgs{Port: 8099, ControlPort: 9099})
// wire args.SchemaPath/args.Port/args.ControlPort + Version: version into Config
```

It implements `myapp version` (the installer's identity check), accepts the
unit's `serve --schema … --port …`, and fails LOUD on any misplaced argument
(plain `flag.Parse` silently discards everything after a bare word).

### 3.2 The `Route`

```go
type Route struct {
	Method  string        // GET | POST | PUT | PATCH | DELETE
	Path    string        // must begin with "/api/"
	Handler func(Ctx) error

	Description string        // optional: one-line summary published in /openapi.json
	RequireRole string        // optional: demand this exact JWT role (else 403)
	Public      bool          // optional: skip JWT + path-RBAC (pre-auth endpoint)
	Timeout     time.Duration // optional: per-endpoint deadline (default 5s)
	RateLimit   *RateLimit    // optional: this endpoint's own throttle {RPS, Burst}
	ByteServing bool          // optional: this GET streams a file (Ctx.ServeFile) — see below
}
```

- **`Path` must start with `/api/`** so it flows through the same middleware
  chain as generated routes (tenant → rate limit → JWT → RBAC). The first
  segment after `/api/` must **not** be a schema resource name.
- **Every registered route is published in the served `/openapi.json`**
  (ENG-33) — method, path, auth mode (`x-public: true` for a Public route,
  otherwise Bearer + the RBAC segment/action it demands), `x-required-role`,
  `x-byte-serving` — flagged `x-appitools-custom-route: true`. **`Description`**
  is the one thing only you can add: a one-line summary shown in the contract
  and in `/docs`. Set it on every route — it costs a string and it is what an
  external agent reads first. Shapes (request/response bodies) are deliberately
  NOT published — a Go handler declares none; they live in your contract sheet
  (§3.6b).
- **Every custom GET also serves HEAD** (ENG-32): same auth, same RBAC
  (read), headers only. `Ctx.ServeFile` answers HEAD natively (Content-Length,
  ETag, no byte copy) — link unfurlers and CDNs probe with HEAD before GET.
- **`Timeout`** bounds the handler: its context (and the tenant transaction) is
  cancelled after `Timeout`, so a slow query or hung outbound call is aborted and
  the tx rolls back. Default 5s. Set it higher for legitimately long work, lower
  for tight endpoints. It bounds the **request** goroutine only — a `SafeGo`
  goroutine gets its own deadline (§6).
- **`RequireRole`** demands the caller's JWT `role` equals it (an *additional*
  check on top of path-RBAC below).
- **`Public`** — see §3.5. Public + RequireRole is a contradiction (rejected at
  boot); a Public path must be literal (no `{param}`).
- **`RateLimit`** — this endpoint's own bucket, per (tenant, client IP). A public
  route with no declaration gets the engine's conservative default (5 rps / burst
  10), which is right for a public **write** endpoint (registration, a webhook)
  and far too low for a public **read** one: a storefront catalogue trips it under
  ordinary traffic. Declare `&appitools.RateLimit{RPS: 200, Burst: 400}` on that
  route instead of raising `APPITOOLS_PUBLIC_ROUTE_RPS` process-wide, which would
  also loosen the endpoints that want the strict default. Both values must be > 0
  (a half-filled struct is rejected at boot). Set on an authenticated route, it
  adds a per-(tenant, IP) limit on top of the per-tenant one — useful for an
  expensive report or export.
- **`ByteServing`** — declares a GET route that streams a binary body via
  `Ctx.ServeFile` (a public product image, an authorized file download). It
  routes the response AROUND the response cache and the compression wrapper —
  which would otherwise buffer the whole blob in RAM, strip
  Content-Disposition/Accept-Ranges on a cache hit, and suppress sendfile.
  GET only; literal path (pass the file id as a query parameter). See
  `Ctx.ServeFile` below and the public-image worked example in
  `appitools frontend-spec` §7.5.

### 3.3 The `Ctx` — every method

The handler's single argument. Identity, tenant, and a **tenant-scoped
transaction** are already resolved — you write business logic, never
infrastructure.

**Identity** (already verified by the chain — never re-parse a JWT):

```go
ctx.Claims()  // Claims{UserID, Role, TenantID, ExternalClientID}
ctx.Tenant()  // tenant id, e.g. "acme" (from the Host subdomain)
ctx.Role()    // the JWT "role" claim
ctx.Allowlist("students") // (fields []string, mayRead bool) — the role's read projection
```

**Database** — the transaction is opened with the tenant's `search_path`
already set, so unqualified table names resolve to *this* tenant's schema. There
is **no** API that exposes the raw pool; isolation is by construction.

```go
ctx.Tx()       // pgx.Tx, tenant-scoped. Return nil from the handler → COMMIT; return err → ROLLBACK.
ctx.UnsafeTx() // the SAME tx, named so `grep UnsafeTx` audits every RBAC-bypass site
```

**RBAC-aware data helpers** — apply the role's row filter, validate against the
compiled schema rules, and project the permitted fields. **Prefer these.**

```go
// Query: filters are equality predicates (validated + bound); the role's row
// condition is ALWAYS applied on top — you cannot widen what the role may see.
rows, err := ctx.Query("students", appitools.QueryOpts{
	Filters: map[string]any{"country": "CO"},
	Limit:   50, OrderBy: "created_at", Desc: true,
})

// Get: ONE row by id, with the role's row condition applied and its field
// allowlist projected. (nil, nil) when the row is absent OR hidden from this
// role — the two are indistinguishable on purpose.
row, err := ctx.Get("students", id)

// Insert / Update: declarative validation + field allowlist + row condition,
// exactly like the generated POST / PATCH. Update is PATCH semantics.
row, err := ctx.Insert("students", map[string]any{"full_name": "Ana"})
row, err := ctx.Update("students", id, map[string]any{"country": "MX"})
```

> **`QueryOpts.Filters` takes DECLARED FIELDS ONLY — `id` is not one of them.**
> The implicit primary key is not a declared field, so
> `ctx.Query("students", QueryOpts{Filters: map[string]any{"id": x}})` fails with
> `unknown filter field: id`. **Use `ctx.Get(resource, id)`** — it is the
> sanctioned lookup-by-id and keeps the row rule. Reaching for `ctx.UnsafeTx()`
> and a hand-written `SELECT … WHERE id = $1` is the wrong fix: it silently drops
> the role's row condition, so a caller can read a row the REST API would hide.

`Update` also enforces a declared **state machine**, with the exact semantics of
the generated PATCH (the guard lives in the UPDATE's WHERE — race-safe, terminal
states immutable, re-sending the current value is a no-op). An illegal move
returns `*appitools.InvalidTransitionError` (→ the same 422 if you return it);
a concurrent-change conflict returns `appitools.ErrUpdateConflict` (→ 409). So a
custom route that advances a lifecycle needs NO transition table of its own —
restrict WHO may move (per-transition RBAC) in the handler, and let the engine
own WHAT moves exist:

```go
row, err := ctx.Update("orders", id, map[string]any{"status": "shipped"})
var ite *appitools.InvalidTransitionError
if errors.As(err, &ite) {
	return ctx.Error(409, "ese pedido ya no puede pasar a enviado: "+ite.Message, err)
}
```

**Request binding** (1 MiB cap — `appitools.MaxBodyBytes` — like the generated
routes):

```go
var body struct{ Msg string `json:"msg"` }
err := ctx.Bind(&body)                 // JSON-decode the body
err := ctx.BindResource("students", &dst) // + validate against the schema rules → *ValidationError (422)

raw, err := ctx.RawBody()              // the EXACT bytes, same cap. USE THIS FOR WEBHOOKS.
```

`RawBody` returns the body byte-for-byte as sent. **A signature is computed over
those bytes**: parsing and re-serializing changes key order and whitespace and
breaks every signature, so `Bind` is the wrong tool for a signed payload — verify
over `RawBody` first, then decode. The body is buffered once, so `RawBody` and
`Bind` compose in any order (a plain `io.ReadAll(ctx.Request().Body)` would leave
`Bind` with an empty stream, and would make you re-implement the size cap). Over
the cap → `appitools.ErrBodyTooLarge`, which maps to `413` if you return it.

**Outbox** — enqueue a durable job **inside the current transaction** (atomic
with your write; a handler error rolls it back too):

```go
id, err := ctx.Enqueue("email.send", map[string]any{"template": "welcome", "to": email})
```

**Create a user** — an identity in *this* tenant's `auth_users`, in the
handler's transaction (rolls back with everything else). The role comes from your
code, never the request; email is normalized + checked, password argon2id-hashed
and length-checked; an **empty password** creates an invitation user (can't
password-login until a reset). See §5.

```go
user, err := ctx.CreateUser(email, password, "student") // CreatedUser{ID, Email, Role, EmailVerified}
switch {
case err == nil:
case errors.Is(err, appitools.ErrEmailTaken):     // duplicate in this tenant → your 409
case errors.Is(err, appitools.ErrInvalidEmail),
     errors.Is(err, appitools.ErrWeakPassword):   // → your 422
case errors.Is(err, appitools.ErrUnknownRole):    // role not in the schema RBAC
}
```

**Serve a stored file** — stream one of THIS tenant's files (the engine file
store — the same one `/api/files/{id}` serves) as the response, with Range,
strong ETag/304 and sendfile. The route must declare `ByteServing: true`
(ServeFile refuses otherwise, loudly). The handler decides WHO may fetch —
this is how a storefront serves product images to ANONYMOUS visitors while
every other file stays private (authorize by relationship, then serve):

```go
app.Register(appitools.Route{
	Method: "GET", Path: "/api/catalogo-imagen",
	Public: true, ByteServing: true,
	RateLimit: &appitools.RateLimit{RPS: 200, Burst: 400}, // image-sized budget
	Handler: func(ctx appitools.Ctx) error {
		id := ctx.Request().URL.Query().Get("id")
		var ok bool
		if err := ctx.UnsafeTx().QueryRow(ctx.Context(),
			`SELECT EXISTS(SELECT 1 FROM productos WHERE imagen_id = $1 AND estado = 'activo')`,
			id).Scan(&ok); err != nil || !ok {
			return ctx.Error(404, "not found", err) // uniform miss — no oracle
		}
		// The store is content-addressed: this id's BYTES can never change, so
		// a URL that embeds the id may be cached forever (FILES-2). A changed
		// image is a new id — and a new URL.
		return ctx.ServeFile(id, appitools.WithCacheControl(appitools.CacheControlImmutable))
	},
})
```

Facts: call it once, INSTEAD of `JSON` (an `Error` before it still wins — the
error path keeps its response); the metadata lookup reads committed state, not
the handler's tx; `ErrFileNotFound` is the exported uniform-miss sentinel.
Cache policy (FILES-2): no option ⇒ no Cache-Control (browsers revalidate —
cheap 304s via the strong ETag); `WithCacheControl(...)` declares one, sent
ONLY on the successful stream (never on the 404 path).
`CacheControlImmutable` (`public, max-age=31536000, immutable`) is safe
whenever the URL embeds the file id; do NOT use it on a URL that can start
serving a DIFFERENT file (a mutable `/api/logo` pointer) — there the default
revalidation is correct. HEAD on the route answers headers-only for free
(ENG-32).

**Safe goroutine** — the ONLY sanctioned way to start a goroutine (§6):

```go
ctx.SafeGo(func(bg context.Context) { /* fire-and-forget, panic-safe */ })
```

**Response** (buffered; flushed after commit, so a commit failure becomes a 500,
not a false 200):

```go
return ctx.JSON(201, map[string]any{"id": id})        // success
return ctx.Error(409, "email already registered", err) // error + a non-nil error to return
```

**Escape hatches:**

```go
ctx.Request()  // *http.Request (headers, query, etc.)
ctx.Context()  // context.Context — the handler's deadline-bounded context; PASS IT to every DB / HTTP call
```

Always pass `ctx.Context()` to your queries and outbound calls — that's how
`Route.Timeout` actually cancels them.

### 3.4 The safety rules (non-negotiable)

These are the Phase-0 rules the engine's in-process model depends on. Follow them
in every handler you write:

1. **Never `go func(){…}()` — always `ctx.SafeGo`.** A raw goroutine that panics
   crashes the **entire multi-tenant process** (Go's `recover()` cannot reach
   another goroutine). `SafeGo` wraps it in recover + a structured log + a metric,
   so a bad background task is a logged incident, not an outage. (For in-request
   parallelism use `appitools.SafeParallel` — §6 — which is bounded and
   panic-safe too.)
2. **External side effects go after the commit.** Never call an external API
   *inside* the transaction. Durable + retryable → `Enqueue` (outbox). Best-effort
   → `SafeGo` after you respond.
3. **A public route treats the caller as hostile.** Validate *every* input, take
   the role from your code (never the request), and use
   `json.Decoder.DisallowUnknownFields()` on sensitive bodies. The engine already
   applies a dedicated, tighter rate limit to public routes.
4. **Tenant scoping is automatic — never filter it by hand.** The tx is
   search_path-scoped and `ctx.Query`/`Insert`/`Update` apply the role's
   condition. Don't add a `WHERE tenant_id = …`; there is no cross-tenant pool to
   leak from.
5. **The transaction is a single connection — not concurrency-safe.** Do not run
   `ctx.Tx()` queries concurrently (not even reads). Parallelise external I/O or
   CPU with `SafeParallel`; serialise DB work on the tx.

### 3.5 RBAC — the biggest design force in this document

**Read this before you write a line of schema.** RBAC shape decides your data
model, not just your auth code: get it wrong and you discover, three resources in,
that the only way to express "customers see their own orders *and* can check out"
is to denormalize a column onto every table. Everything else in this guide is
recoverable; this is the one that isn't.

#### The three role shapes

| Shape | Looks like | Use it for |
|---|---|---|
| **wildcard** | `{"resources":"*","actions":["*"]}` | admins/ops. Reaches every resource **and every custom route**. |
| **role-global** | `{"resources":["a","b"],"actions":["read"],"conditions":{…}}` | one rule across a set of resources that share the *same* ownership column. |
| **per-resource** | `{"permissions":{"orders":{…},"invoices":{…}}}` | the realistic case: each resource scoped by **its own** column. |

**Role-global carries ONE condition across every resource it lists.** That
condition is injected into the WHERE of *each* of them, so the column must exist on
**all** of them — the engine now rejects the schema at load if it doesn't, naming
the offending resources. If your resources are owned through different columns
(`projects.owner_id`, `documents.created_by`), role-global cannot express it; use
`permissions`.

```json
"member": {
  "permissions": {
    "projects":  { "actions": ["read","create","update"],
                   "conditions": { "field": "owner_id",   "op": "eq", "val": "$user_id" } },
    "documents": { "actions": ["read","update"],
                   "conditions": { "field": "created_by", "op": "eq", "val": "$user_id" } },
    "tags":      { "actions": ["read"] },
    "posts":     { "actions": ["read","update","delete"],
                   "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                   "condition_actions": ["update","delete"] }
  }
}
```

`tags` is unscoped (every row). `posts` is "read all, write own" via
`condition_actions`. A resource absent from `permissions` is **denied**.

**A row condition is single-column equality — there are no joins.** `op` must be
`eq`. So "a customer may read the LINES of their own orders" is not expressible as
`order_lines JOIN orders ON …`; the ownership column has to be **on the row being
filtered**. Denormalizing `user_id` onto the child table is the correct answer, not
a hack — it is how the filter becomes an index lookup instead of a join the
authorizer would have to plan. Budget for it in the data model.

#### Custom routes: the `routes` grant

A custom route is authorized by its **first `/api/` segment**, treated as a
*virtual resource*, with the action derived from the method (`GET`→read,
`POST`→create, `PUT`/`PATCH`→update, `DELETE`→delete). `POST /api/checkout` is
"create on `checkout`".

A role grants it with a **`routes`** block, which is **orthogonal** to
`resources`/`permissions` — different namespace (registered endpoints, not tables),
so a role may declare both:

```json
"customer": {
  "permissions": {
    "orders":      { "actions": ["read"], "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } },
    "order_lines": { "actions": ["read"], "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
  },
  "routes": { "checkout": { "actions": ["create"] } }
}
```

**This is the shape for "owner-scoped end users + a custom action endpoint"** — a
customer with their own orders *and* `POST /api/checkout`. It is the single most
common real requirement, and before the `routes` block it was inexpressible: a
`permissions` role could not reach any custom route.

Rules worth knowing before you use it:

- **No `conditions`, no `fields`.** A route segment has no rows and no columns;
  declaring either is a load error. The **data** a handler touches is authorized
  separately — `ctx.Query`/`Insert`/`Update` re-evaluate the role against the real
  resources (Layer 2 below).
- **Boot-validated.** A grant for a segment no route registers — or an action no
  registered method provides — **fails the boot**, with the registered segments
  listed. Dead authorization config never becomes a mystery 403.
- **Authoritative for the segments it names**: adding `routes` to a wildcard role
  *narrows* those segments. A segment it doesn't name falls through to the role's
  normal evaluation, so deny-by-default is untouched.
- A key that names a real resource is rejected — use `permissions` for that.
- The **pure `appitools serve` binary registers no custom routes**, so a schema
  with a `routes` grant only boots in the binary that registers them.

Full rationale, alternatives considered, and the security tests:
[ADR-021](adr/ADR-021-custom-route-authorization.md).

#### Layer 2 — data RBAC inside the handler

`ctx.Query`/`Insert`/`Update` re-evaluate the caller's role against the **real**
resource they touch, applying its row condition and field allowlist. A route grant
opens the **endpoint**, never the data: a customer calling
`ctx.Query("orders", …)` inside `/api/checkout` still gets only their own rows, and
`ctx.Insert` still forces the condition column to their own id (no
mass-assignment). Two independent layers, and you want both.

#### `Public: true` — no token REQUIRED, identity still welcome

For pre-auth endpoints (registration, webhooks, a storefront). Path RBAC skips
the route entirely, and authentication is **optional, not ignored** — three
branches, exactly:

| The request carries… | The handler sees |
|---|---|
| no `Authorization` header | `Claims()` zero — anonymous; the RBAC-aware helpers fail closed |
| a **valid** Bearer | `Claims()` **populated** — identity as *input* (personalize, link the write to the user); data RBAC (`ctx.Query`/`Insert`/`Update`) applies the role normally |
| a present but invalid / expired / wrong-tenant Bearer | nothing — the request is **401**ed before the handler (sent credentials never silently degrade to anonymous) |

So ONE public checkout serves both guests and logged-in customers: branch on
`ctx.Claims().UserID == ""`. Anonymous work goes through `CreateUser` (which
carries its own rules) or a deliberate, greppable `UnsafeTx`. Treat every input
as hostile — §3.4 rule 3.

> **Debugging a 403 on a custom route:** it is almost always the grant. Check, in
> order: does the role declare the segment under `routes`? Is the action right for
> the method? Is the role wildcard (then it should already work)? Is the route
> `Public` (then RBAC isn't involved at all)?

### 3.6 Worked examples (all compile — see examples/backend-guide/)

(a) an admin-scoped route · (b) a public registration in one transaction ·
(c) bounded parallel fan-out · **(d) a payment webhook** · (e) fire-and-forget.

**(a) An authenticated, admin-scoped route** — cross-resource read; `ctx.Query`
still enforces RBAC on the real resources.

```go
app.Register(appitools.Route{
	Method: "GET", Path: "/api/ops/overview", RequireRole: "admin",
	Handler: func(ctx appitools.Ctx) error {
		students, err := ctx.Query("students", appitools.QueryOpts{Limit: 1000})
		if err != nil {
			return ctx.Error(500, "students lookup failed", err)
		}
		enrollments, err := ctx.Query("enrollments", appitools.QueryOpts{Limit: 1000})
		if err != nil {
			return ctx.Error(500, "enrollments lookup failed", err)
		}
		return ctx.JSON(200, map[string]any{
			"tenant": ctx.Tenant(), "students": len(students), "enrollments": len(enrollments),
		})
	},
})
```

**(b) A public registration endpoint (the Hotmart pattern)** — validate an
external license, check business data, create the user + related records + a
follow-up job, **all in one transaction**. Any failure rolls back everything —
the user included. This is the in-process differential: no network hop, one tx,
the engine's validation + RBAC still in force.

```go
app.Register(appitools.Route{
	Method: "POST", Path: "/api/register", Public: true, Timeout: 15 * time.Second,
	Handler: func(ctx appitools.Ctx) error {
		var body struct {
			Email, Password, FullName, License, CourseID string
			Amount                                       float64
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		if body.Email == "" || body.FullName == "" || body.CourseID == "" {
			return ctx.Error(422, "email, full_name and course_id are required", nil)
		}

		// 1. Business gate: verify the purchase against the external platform.
		//    Plain Go http — no sandbox. The bounded context aborts a hung provider.
		ok, err := verifyLicense(ctx.Context(), body.License)
		if err != nil {
			return ctx.Error(502, "license service unavailable", err)
		}
		if !ok {
			return ctx.Error(403, "license not valid", nil)
		}

		// 2. Business data check — no identity yet, so a deliberate UnsafeTx.
		var published bool
		err = ctx.UnsafeTx().QueryRow(ctx.Context(),
			`SELECT published FROM courses WHERE id = $1`, body.CourseID).Scan(&published)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return ctx.Error(404, "course not found", nil)
		case err != nil:
			return ctx.Error(500, "course lookup failed", err)
		case !published:
			return ctx.Error(409, "course is not open for enrollment", nil)
		}

		// 3. Create the identity — role from THIS code, never the request.
		user, err := ctx.CreateUser(body.Email, body.Password, "student")
		switch {
		case err == nil:
		case errors.Is(err, appitools.ErrEmailTaken):
			return ctx.Error(409, "email already registered", err)
		case errors.Is(err, appitools.ErrInvalidEmail), errors.Is(err, appitools.ErrWeakPassword):
			return ctx.Error(422, err.Error(), err)
		default:
			return ctx.Error(500, "registration failed", err)
		}

		// 4. Profile + enrollment in the SAME transaction.
		if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO students (user_id, full_name) VALUES ($1, $2)`, user.ID, body.FullName); err != nil {
			return ctx.Error(500, "profile creation failed", err)
		}
		if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO enrollments (user_id, course_id, amount, external_ref) VALUES ($1, $2, $3, $4)`,
			user.ID, body.CourseID, body.Amount, "lic-"+body.License); err != nil {
			return ctx.Error(500, "enrollment failed", err)
		}

		// 5. Follow-up work, atomic with the user (durable, async, non-blocking).
		if _, err := ctx.Enqueue("email.send", map[string]any{
			"template": "welcome", "to": user.Email, "user_id": user.ID,
		}); err != nil {
			return ctx.Error(500, "enqueue failed", err)
		}
		return ctx.JSON(201, map[string]any{"user_id": user.ID, "email": user.Email})
	},
})
```

**(c) Heavy compute — bounded, panic-safe parallel fan-out** with
`appitools.SafeParallel`. The tasks do **external I/O only** (never the tx — a
single connection is not concurrency-safe); `Route.Timeout` bounds the batch.

```go
app.Register(appitools.Route{
	Method: "POST", Path: "/api/reports/ratings", RequireRole: "admin", Timeout: 8 * time.Second,
	Handler: func(ctx appitools.Ctx) error {
		var body struct{ CourseIDs []string `json:"course_ids"` }
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		ratings := make([]float64, len(body.CourseIDs))
		tasks := make([]func(context.Context) error, len(body.CourseIDs))
		for i, id := range body.CourseIDs {
			i, id := i, id
			tasks[i] = func(fctx context.Context) error {
				r, err := fetchRating(fctx, id)
				if err != nil {
					return err
				}
				ratings[i] = r // each task writes its OWN slot — no shared mutation
				return nil
			}
		}
		// At most 8 concurrent (backpressure); a task panic becomes an error, never
		// a crash; ctx cancellation (Route.Timeout) aborts the rest.
		if err := appitools.SafeParallel(ctx.Context(), 8, tasks...); err != nil {
			return ctx.Error(502, "ratings service failed", err)
		}
		out := make(map[string]float64, len(ratings))
		for i, id := range body.CourseIDs {
			out[id] = ratings[i]
		}
		return ctx.JSON(200, map[string]any{"ratings": out})
	},
})
```

**(d) A payment webhook** — the single most security-sensitive handler most
products ever write, and the one shape that is easiest to get wrong. Six rules, in
this order:

1. **Verify the signature over the RAW bytes, BEFORE parsing.** `ctx.RawBody()`
   gives them under the engine's own cap. Parsing first and re-serializing changes
   key order and whitespace — the #1 documented payment-integration bug.
2. **Idempotency is a UNIQUE constraint, not an `if`.** Gateways deliver
   at-least-once and retries arrive *concurrently*; "SELECT then INSERT" races with
   itself. Insert first, let the database reject the duplicate.
3. **The webhook is the source of truth**, not the browser redirect (the customer
   closes the tab; some methods settle minutes later).
4. **Answer 200 only after the state is committed.** A 200 tells the gateway to
   stop retrying, so it must mean "recorded". `ctx.JSON` is buffered until *after*
   the commit — a commit failure becomes a 500, never a false 200. That is this
   rule, enforced by the engine.
5. **Handle out-of-order events**: compare the gateway's own timestamp, not arrival
   order, before overwriting newer state.
6. **Never retry business logic on a terminal decline.** Release what you held and
   stop; the customer starts a new payment.

```go
app.Register(appitools.Route{
	Method: "POST", Path: "/api/webhooks/payments", Public: true, Timeout: 15 * time.Second,
	Handler: func(ctx appitools.Ctx) error {
		// Rule 1 — raw bytes first, cap enforced by the engine.
		raw, err := ctx.RawBody()
		if err != nil {
			if errors.Is(err, appitools.ErrBodyTooLarge) {
				return ctx.Error(413, "payload too large", err)
			}
			return ctx.Error(400, "unreadable body", err)
		}
		if !validSignature(raw, ctx.Request().Header.Get("X-Signature")) {
			return ctx.Error(401, "invalid signature", nil)
		}

		// Only NOW is it safe to interpret the bytes.
		var event struct {
			ID     string `json:"id"`
			Ref    string `json:"external_ref"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return ctx.Error(400, "invalid event", err)
		}
		if event.ID == "" || event.Ref == "" {
			return ctx.Error(422, "id and external_ref are required", nil)
		}

		// Rule 2 — the DATABASE decides whether this is a duplicate.
		// webhook_events.event_id is `"unique": true` in the schema.
		tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
			`INSERT INTO webhook_events (event_id, provider, payload)
			 VALUES ($1, 'payments', $2) ON CONFLICT (event_id) DO NOTHING`,
			event.ID, string(raw))
		if err != nil {
			return ctx.Error(500, "could not record the event", err)
		}
		if tag.RowsAffected() == 0 {
			// Already applied — 200 so the gateway stops retrying. A success, not an error.
			return ctx.JSON(200, map[string]any{"status": "duplicate", "event_id": event.ID})
		}

		// Rules 3 + 6 — apply the change in THIS transaction. The guard in the
		// WHERE makes the transition race-safe and a terminal state unrevivable.
		if event.Status == "refunded" {
			if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`UPDATE enrollments SET status = 'refunded'
				  WHERE external_ref = $1 AND status = 'active'`, event.Ref); err != nil {
				return ctx.Error(500, "could not apply the refund", err)
			}
		}

		// Side effects go to the OUTBOX, never an inline call: enqueued in the same
		// transaction (it exists iff the event was recorded), delivered by the
		// worker — so a slow provider can't hold this transaction open.
		if _, err := ctx.Enqueue("email.send", map[string]any{
			"template": "payment_" + event.Status, "external_ref": event.Ref,
		}); err != nil {
			return ctx.Error(500, "enqueue failed", err)
		}
		// Rule 4 — flushed after the commit, by the engine.
		return ctx.JSON(200, map[string]any{"status": "processed", "event_id": event.ID})
	},
})
```

The signature check itself must be **constant-time** — a byte-by-byte compare
leaks the signature through timing:

```go
func validSignature(raw []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(os.Getenv("WEBHOOK_SECRET")))
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(strings.TrimPrefix(header, "sha256=")), []byte(want))
}
```

Two more things a real integration needs: **compare the amount** the event reports
against what you charged (a correctly-signed event for the wrong amount is a
misroute or tampering — refuse it), and **record rejected events** for audit.

**(e) Fire-and-forget with `SafeGo`** — a best-effort external ping off the
response path. The goroutine gets a fresh context (no request values) with its
own deadline, which `fn` must honor; a panic is recovered, never a crash. This is
at-most-once — for durable work, `Enqueue` instead.

```go
app.Register(appitools.Route{
	Method: "POST", Path: "/api/track", Public: true,
	Handler: func(ctx appitools.Ctx) error {
		var body struct{ Event string `json:"event"` }
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		event := body.Event
		ctx.SafeGo(func(bg context.Context) { _ = pingAnalytics(bg, event) })
		return ctx.JSON(202, map[string]any{"accepted": true})
	},
})
```

**(f) The guest-order lookup (the storefront confirmation page)** — every
storefront needs it, and the generated reads can never serve it: a guest's
order has no `user_id`, so the owner-scoped `GET /api/orders` is structurally
blind to its own buyer. Yet the confirmation page must answer "did my payment
land?" — and it POLLS, because the gateway confirms asynchronously by webhook.
The pattern (proven in the commerce reference app):

1. **Composite lookup key, both parts required**: the human-visible order
   number (printed on screens, shared over WhatsApp — NOT a bearer secret) plus
   the email typed at checkout. Either one alone is unusable.
2. **Uniform 404** for every miss — wrong number, right number + wrong email,
   no such order. No oracle for "that order exists but the email is wrong".
3. **`Public: true` + a polling-sized `RateLimit`** (a confirmation page polls
   every ~2 s for a couple of minutes; ~30 rps per tenant+IP covers a family on
   one Wi-Fi without opening an enumeration lane).
4. **Return a lean projection**, not the row: status, totals, line summaries,
   the latest payment attempt's state. The page narrates pending → paid →
   (or declined) from exactly this.

```go
app.Register(appitools.Route{
	Method: "GET", Path: "/api/order-status",
	Public:    true,
	RateLimit: &appitools.RateLimit{RPS: 30, Burst: 60},
	Handler: func(ctx appitools.Ctx) error {
		q := ctx.Request().URL.Query()
		number, email := q.Get("number"), q.Get("email")
		if number == "" || email == "" {
			return ctx.Error(422, "number and email are required", nil)
		}
		var status string
		var total int64
		err := ctx.UnsafeTx().QueryRow(ctx.Context(), `
			SELECT o.status, o.total_cents
			  FROM orders o JOIN customers c ON c.id = o.customer_id
			 WHERE o.number = $1 AND lower(c.email) = lower($2)`, number, email).
			Scan(&status, &total)
		if err != nil { // pgx.ErrNoRows and everything else: ONE uniform miss
			return ctx.Error(404, "order not found", nil)
		}
		return ctx.JSON(200, map[string]any{"number": number, "status": status, "total_cents": total})
	},
})
```

### 3.6b Write the contract sheet — the OpenAPI names your routes, the sheet gives them shapes

The served `/openapi.json` lists every route the app serves — generated AND
custom (ENG-33): a frontend agent can enumerate your endpoints, see which are
`Public`, which role/action each demands, and read your `Route.Description`.
What it can NOT see is **shapes**: a Go handler declares no request/response
schema, and the engine refuses to publish a guess. So every custom route still
goes into a short **contract sheet** the frontend consumes — per route, the
params, body and response shapes, and its rate budget; plus the role matrix,
any state machines, and the upload limits (none of which fit in an OpenAPI the
engine can derive). The reference storefront keeps this as `STOREFRONT_API.md`
next to the code, updated in the same commit as the route it describes. The
division of labor: **/openapi.json is the authority for EXISTENCE, the sheet is
the authority for SHAPES.** Ten minutes of writing here saves the frontend a
day of reverse-engineering 422s. (Probing is still not discovery: an unknown
`/api/...` answers `401` — auth runs before routing, deliberately.)

### 3.7 Serving your frontend from the same binary

`Config.Static` mounts a file tree — one binary that is backend **and** frontend
**and** admin **and** docs. It is NOT a `Route`: a Route must live under `/api/`
and runs inside a per-request tenant transaction, which is exactly wrong for a
`.js` file.

```go
//go:embed all:web/dist
var frontend embed.FS

dist, _ := fs.Sub(frontend, "web/dist")
app, err := appitools.New(appitools.Config{
	SchemaPath: "schema.json",
	Static: []appitools.StaticMount{{
		Path: "/",   // or "/app"
		FS:   dist,  // or os.DirFS("/var/www/app")
		SPA:  true,  // client-side routing: /orders/42 → index.html
	}},
})
```

| Behaviour | Rule |
|---|---|
| the index | always `no-cache` — it names the current hashed bundles |
| `assets/`, `_app/`, `static/` | `immutable, max-age=31536000` (override with `ImmutablePrefixes`) |
| a missing file **with** an extension | `404` — never the shell, so a deleted bundle can't return HTML |
| an unknown client route | the shell, but ONLY with `SPA: true` (opt-in: a static site should 404) |
| `/api/…`, `/admin`, `/editor`, `/docs`, … | always the engine's — mounting on one is a **boot error** |
| a tenant transaction / RBAC / response cache | none of them run for an asset |
| path traversal | impossible: the tree is an `fs.FS`, which cannot open outside its root |
| Content-Security-Policy | the mount OWNS it, both forms: `appitools.DefaultStaticCSP` (same-origin SPA policy) unless overridden per mount with `CSP:` (verbatim) or disabled with `CSP: appitools.CSPOff` — the API keeps its own strict policy |
| `index.html` | required only with `SPA: true` (it IS the fallback); an assets-only mount needs none (its root 404s) |

⚠ **PCI (SAQ A):** if the app takes card payments through a hosted widget or
iframe, keep the **checkout** page free of third-party scripts (analytics, chat,
tag managers). One extra script there moves the merchant from SAQ A to SAQ A-EP.

In the in-process fleet a mount belongs to the app that declares it; the
manifest-driven fleet declares none. Runnable example:
[examples/fullstack/](../examples/fullstack/).

---

## 4. Hooks — declarative per-record validation

A hook runs on the CRUD path of a resource, declared in the **schema** (not code
you register). Use a hook when the logic is **pure validation/transformation of
one record** with **no I/O**; use a **handler** when it needs a transaction,
external calls, or cross-resource work.

- Events: `before_create`, `before_update` (may modify the record or abort the
  write), `after_create`, `after_update`. There are **no delete hooks**.
- **Before-hooks** are `js` (Goja sandbox, watchdog-timed) or `wasm` (Wazero).
  In a `js` hook: `data` is the record, `user` is the actor
  (`user.user_id`/`role`/`tenant_id`); set `result.proceed = false` +
  `result.error` to reject with 422. Built-ins: `validateNIT`, `calculateCUFE`,
  `isValidEmail`, `formatMoney`. **No `fetch`, no DB, no network** — a hook that
  needs those is a `before_*` handler's job, or move the logic to a custom route.
- **After-hooks must be `webhook`** (a signed async POST, HTTPS-only,
  SSRF-guarded). A `js`/`wasm` after-hook is rejected at load.

```json
"hooks": {
  "before_create": {
    "type": "js",
    "script": "if (!data.title) { result.proceed = false; result.error = 'title required'; }"
  },
  "after_create": {
    "type": "webhook",
    "url": "https://erp.example.com/webhooks/created",
    "hmac_secret_env": "WEBHOOK_SECRET"
  }
}
```

Hooks are **compiled at boot** from the schema — changing them needs a restart,
not just a schema deploy. When the choice is close ("validate a NIT" → hook;
"validate a NIT *and* call the tax authority" → custom handler), the presence of
I/O decides.

---

## 5. Auth — the identity model

Appitools ships auth-as-product: signup / login / refresh, password reset +
email verification, OAuth social login, and TOTP MFA — all multi-tenant-aware,
issuing the **same JWT** the engine validates. Full operator detail is in the
[README auth sections](../README.md#auth-cycle-for-an-api-consumer-api-productiva-v1);
for building a backend, know this:

- **Users live in `tenant_<id>.auth_users`** with a fixed shape (id, email,
  password hash, role, verified flag, timestamps). Email is unique **per tenant**
  (the same email is a distinct account in two tenants — the structural advantage
  over globally-unique-email systems). You do **not** add columns to `auth_users`.
- **Custom profile fields go in a normal resource** linked by `user_id` — e.g. a
  `students` resource with `user_id` (`uuid`, `unique`) plus `full_name`,
  `country`, etc. The registration handler in §3.6(b) creates both the identity
  (`CreateUser`) and the profile row, atomically.
- **Three ways to create a user:**
  1. **Public signup** — `POST /auth/signup`, enabled only when
     `APPITOOLS_AUTH_SIGNUP_ROLE` is set (the role every signup gets). Off by
     default.
  2. **Admin API** — `POST /admin/tenants/{id}/users` (platform super-admin or
     admin key), with an admin-chosen role.
  3. **`Ctx.CreateUser` in a custom handler** — the flexible path: run your own
     business logic (validate a purchase, check an allowlist), then create the
     user in the same transaction as the related records. This is how a custom
     registration flow (§3.6b) works, and it can run on a `Public` route because
     creating the user *is* the point.
  (OAuth also auto-provisions a user on first social login when a default role is
  configured.)
- **The `role` claim in the JWT selects the RBAC policy.** A user's role must be
  one the schema declares. Your handlers read it via `ctx.Role()` / `ctx.Claims()`
  and never need to re-verify the token — the chain already did.
- **MFA / OAuth / reset / verify** are engine features you enable by config; they
  need no handler code. Reset + verify are delivered through the **outbox + email
  worker** (`APPITOOLS_WORKER_MODE=email`) — the same async pattern your handlers
  use for `Enqueue("email.send", …)`.

---

## 6. Jobs and async processing

Two mechanisms, chosen by **durability**:

| Need | Use | Guarantees |
|---|---|---|
| best-effort, post-response side effect (metric, cache warm, notify) — losing it is OK | **`ctx.SafeGo(fn)`** | at-most-once, non-durable, panic-safe, fresh-root context with its own deadline |
| must survive a crash and be retried (payment capture, provisioning, email) | **`ctx.Enqueue(topic, payload)`** + the worker | at-least-once, durable, transactional (commits with your write) |
| parallel sub-work the **response needs** (fan-out to N services) | **`appitools.SafeParallel(ctx.Context(), limit, tasks…)`** | bounded concurrency (backpressure), panic-safe, waits for all, returns the first error |

Rules:

- **`SafeGo` is fire-and-forget.** It runs concurrently with the commit, so its
  work must **not depend on the transaction having committed** and must **not
  touch `ctx.Tx()`** (that tx is closing). Its context is a **fresh root** —
  it carries **no request values** (capture what you need by value), with an
  independent deadline (`APPITOOLS_SAFEGO_TIMEOUT` / `Config.SafeGoTimeoutSeconds`,
  default 30s). Honor that deadline: **`fn` must return once `ctx` is `Done`** —
  the deadline cancels the context, it can't forcibly stop a goroutine, so a task
  that ignores cancellation leaks.
- **The outbox is the durable path.** `Enqueue` writes a job in *your*
  transaction and fires a notify on commit; the separate `appitools-worker`
  consumes it (`SELECT … FOR UPDATE SKIP LOCKED`, at-least-once — make processors
  idempotent). This is how you do "after commit, reliably": provision an account,
  charge a card, send a receipt. See [ADR-016](adr/ADR-016-extensibility-pattern.md).
- **`SafeParallel` is the in-request fan-out.** Use it when the handler must wait
  for several external calls or CPU tasks. It caps concurrency (never spawn
  unbounded goroutines) and recovers a task panic into an error — a raw
  `errgroup` does **not** recover and would crash the process. Tasks must not
  share the handler's tx (single connection); parallelise external I/O or CPU.
- **Long operations** (a report over minutes): return `202 Accepted` with a job
  id from `Enqueue`, let the worker do the work and write the result back through
  the engine API, and have the client poll a status resource.

### 6.1 The worker, in framework mode

`Enqueue` writes the job; a **separate process** drains it. Separate on purpose: a
slow or crashing consumer must never hold a request open, pin a pool connection, or
take the API down. You do not need the shipped `appitools-worker` binary —
`pkg/worker` is a library and a consumer is ~40 lines
([examples/backend-guide/worker/main.go](../examples/backend-guide/worker/main.go)):

```go
func main() {
	log := zerolog.New(os.Stderr).With().Timestamp().Logger()
	dsn := os.Getenv("DATABASE_URL")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// DEDICATED connections, never a pool: a LISTEN connection must be permanent,
	// and a pool would rotate it out from under the listener.
	connect := func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, dsn) }

	// A Router dispatches by topic — ONE worker, N event types.
	router := consumers.NewRouter(log)
	router.Handle("email.send", worker.ProcessorFunc(sendEmail))
	router.Handle("enrollments.created", worker.ProcessorFunc(onEnrollmentCreated))

	if err := worker.New(connect, router, worker.Config{}, log).Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal().Err(err).Msg("worker exited")
	}
}
```

The rules that decide whether a consumer is correct:

- **Compose a `Router`, don't run two single-topic workers.** A worker ACKs topics
  it does not own, so two *different* single-topic workers against one outbox
  silently drop each other's events under `SKIP LOCKED`. Scale by running **N
  identical** copies of the same Router instead — the drain uses
  `SELECT … FOR UPDATE SKIP LOCKED`, so instances never collide on a row.
- **Delivery is at-least-once ⇒ Processors must be idempotent.** Make the side
  effect idempotent at the destination (a provider idempotency key, an upsert on a
  unique column), not with an `if already_done` that races with itself.
- **`return nil` ACKs; `return err` retries with backoff.** Return an error only
  for *transient* failures. A permanent one (a malformed payload, a rejected
  address) should be recorded and acked — otherwise it is retried until it
  exhausts `max_attempts`, burning the queue on a job that can never succeed.
- **Write results back through the ENGINE API** (`worker.NewEngineClient` mints a
  short-lived, scoped service JWT), not straight into the tenant's tables — that
  way the write inherits the engine's validation and RBAC instead of bypassing
  both. Never `admin`; give the worker a minimal role in the schema.
- **Schema events cost no handler code**: `"events": ["create"]` on a resource
  makes the engine write `<resource>.created` to the outbox inside the same
  transaction as the INSERT. The payload is lean (`{id, tenant_id, resource,
  action}`) — a consumer that needs the row reads it back.

---

## 7. Building a backend with an agent — end to end

The full loop, and the Hotmart case as the worked example:

1. **Describe the app.** "A course platform: students (profile linked to the auth
   user), courses, enrollments with an active→refunded lifecycle; public
   registration that verifies a purchase license; admins see an ops overview;
   fire an analytics event on activity."
2. **Generate the schema.** Run `appitools spec`, generate against it, and
   self-correct with `appitools validate --json schema.json` until valid. The
   result is [examples/backend-guide/schema.json](../examples/backend-guide/schema.json):
   `students` / `courses` / `enrollments` (state machine + `create` event), and
   roles `admin` (wildcard) + `student` (per-resource `permissions`,
   owner-scoped by `user_id`).
3. **Write the handlers** (this doc). For the Hotmart flow you need exactly one
   custom route — `POST /api/register` (§3.6b) — because registration must verify
   an external license and create the user + profile + enrollment + welcome email
   atomically, which the schema alone can't express. Add `POST /api/reports/ratings`
   (admin fan-out), `GET /api/ops/overview` (admin), `POST /api/track` (public
   fire-and-forget) as needed. Everything else — students/courses/enrollments
   CRUD — is the **generated** routes; don't write handlers for them.
4. **Compile.** `go build ./...` — a bad route or a wrong Ctx call is a
   compile/boot error, never a runtime surprise. The full program is
   [examples/backend-guide/main.go](../examples/backend-guide/main.go).
5. **Run it.**
   ```bash
   DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… \
     go run ./examples/backend-guide --schema examples/backend-guide/schema.json
   ```
6. **Deploy** — the binary is the deploy unit; register tenants through the
   control plane / admin API and mint tokens as usual (README quick start).

The result: a student registers through your public endpoint (external license
verified, user + profile + enrollment + welcome email created in one
transaction), reads their own enrollments through the generated owner-scoped
routes, admins get a cross-resource overview and a parallel ratings report, and
every background effect is either durable (outbox) or safely fire-and-forget
(`SafeGo`) — with a panic in any of it recovered, never an outage.

---

## 8. References

- **Schema:** `appitools spec`, [SCHEMA_SPEC_LLM.md](SCHEMA_SPEC_LLM.md),
  [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md).
- **This doc:** `appitools backend-spec` prints it — paste it into your agent.
- **The compiling example:** [examples/backend-guide/](../examples/backend-guide/).
- **The library contract & extensibility model:**
  [ADR-016](adr/ADR-016-extensibility-pattern.md).
- **Auth & the multi-tenant model:** [README](../README.md),
  [docs/MENTAL_MODEL.md](MENTAL_MODEL.md).
- **Working inside this repo:** [AGENTS.md](../AGENTS.md).
