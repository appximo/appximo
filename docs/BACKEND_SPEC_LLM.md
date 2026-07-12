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
| [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md) | the complete human reference | — |

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

---

## 3. Custom handlers in Go — the core

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

### 3.2 The `Route`

```go
type Route struct {
	Method  string        // GET | POST | PUT | PATCH | DELETE
	Path    string        // must begin with "/api/"
	Handler func(Ctx) error

	RequireRole string        // optional: demand this exact JWT role (else 403)
	Public      bool          // optional: skip JWT + path-RBAC (pre-auth endpoint)
	Timeout     time.Duration // optional: per-endpoint deadline (default 5s)
}
```

- **`Path` must start with `/api/`** so it flows through the same middleware
  chain as generated routes (tenant → rate limit → JWT → RBAC). The first
  segment after `/api/` must **not** be a schema resource name.
- **`Timeout`** bounds the handler: its context (and the tenant transaction) is
  cancelled after `Timeout`, so a slow query or hung outbound call is aborted and
  the tx rolls back. Default 5s. Set it higher for legitimately long work, lower
  for tight endpoints. It bounds the **request** goroutine only — a `SafeGo`
  goroutine gets its own deadline (§6).
- **`RequireRole`** demands the caller's JWT `role` equals it (an *additional*
  check on top of path-RBAC below).
- **`Public`** — see §3.5. Public + RequireRole is a contradiction (rejected at
  boot); a Public path must be literal (no `{param}`).

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

// Insert / Update: declarative validation + field allowlist + row condition,
// exactly like the generated POST / PATCH. Update is PATCH semantics.
row, err := ctx.Insert("students", map[string]any{"full_name": "Ana"})
row, err := ctx.Update("students", id, map[string]any{"country": "MX"})
```

**Request binding** (1 MiB cap, like the generated routes):

```go
var body struct{ Msg string `json:"msg"` }
err := ctx.Bind(&body)                 // JSON-decode the body
err := ctx.BindResource("students", &dst) // + validate against the schema rules → *ValidationError (422)
```

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

### 3.5 Custom routes and RBAC (read this before you wire auth)

A custom route is authorized at **two** layers — understand both:

**Layer 1 — path RBAC (the middleware).** The engine treats the route's **first
`/api/` segment as a virtual resource** and evaluates the caller's role against
it with the HTTP-method action (`GET`→read, `POST`→create, `PUT`/`PATCH`→update,
`DELETE`→delete). Consequences:

- A **wildcard role** (`{"resources":"*","actions":["*"]}` — typically `admin`)
  reaches **every** custom route.
- A **restricted role** reaches a custom route only if its policy grants that
  segment. You grant it by listing the segment in the role's **role-global**
  `resources` array as a *virtual resource* (no schema table needed):
  `{"resources":["students","reports"],"actions":["read"]}` lets that role
  `GET /api/reports/...`. **The per-resource `permissions` form cannot grant a
  virtual segment** (it validates every key against real resources), so a role
  that uses `permissions` for fine-grained per-resource scoping cannot also be
  granted a custom route. Design accordingly:
  - **End users** read/write their own data through the **generated** routes
    (`GET /api/students` etc.), which the engine owner-scopes via the role's row
    condition — no custom route needed.
  - **Custom authenticated routes are for admin/ops/service roles** (wildcard or
    role-global), or are **Public**.
- A **`Public: true`** route skips path RBAC and JWT entirely — for pre-auth
  endpoints (registration, webhooks). Inside, `Claims()` is empty and the
  RBAC-aware helpers fail closed; anonymous writes go through `CreateUser` (which
  carries its own rules) or a deliberate `UnsafeTx`.

**Layer 2 — data RBAC (inside the handler).** `ctx.Query`/`Insert`/`Update`
re-evaluate the role against the **real** resource they touch, applying the row
condition and field allowlist. So even an admin handler reads exactly what the
role permits; a restricted role calling `ctx.Query("students", …)` gets only its
own rows.

> If a custom route returns `403` unexpectedly, it's almost always Layer 1: the
> caller's role doesn't grant the route's first path segment. Grant the segment
> (role-global), make the route `Public`, or call it with a wildcard role.

### 3.6 Worked examples (all compile — see examples/backend-guide/)

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

**(d) Fire-and-forget with `SafeGo`** — a best-effort external ping off the
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
