# Build a production frontend on Appximo — the agent guide

You are an AI coding agent (Claude Code, Cursor, or similar). This document
teaches you to build a **production-quality frontend** on an Appximo backend:
where the frontend lives, what stack to use, the exact API contract you consume,
how every error maps to a screen state, how files and images work, and the traps
that only show up in a real browser.

Appximo compiles a JSON schema into a multi-tenant REST + GraphQL + OpenAPI
server at boot. By the time you write a frontend, **the API already exists** —
generated CRUD with filters/sort/pagination, auth endpoints, a file store, and
whatever custom routes the backend registered. Your job is the UI. You never
build an API client SDK from guesswork: the running engine serves its own
contract at `/openapi.json`, and this document tells you everything the spec
cannot (error semantics, screen states, the serving model).

This is the third of five printable documents; keep them straight:

| Doc | Teaches | Command |
|---|---|---|
| `appximo spec` / SCHEMA_SPEC_LLM.md | the **schema** (the declarative 90 %) | `appximo spec` |
| `appximo backend-spec` / BACKEND_SPEC_LLM.md | the **backend** (handlers, hooks, auth, jobs — the 10 %) | `appximo backend-spec` |
| **this doc** | the **frontend** (the part users touch) | `appximo frontend-spec` |
| `appximo backoffice-spec` / BACKOFFICE_SPEC_LLM.md | a **generated admin CRUD UI** driven by /openapi.json | `appximo backoffice-spec` |
| `appximo quickstart` / LIFECYCLE_SPEC_LLM.md | **operating** it (install → tenant → users → production) | `appximo quickstart` |

Everything below is distilled from a shipped reference storefront (a real
mobile-first shop + merchant back-office, SvelteKit, embedded in one binary,
running in production) — not from theory. The runnable minimal example is
**[examples/frontend-guide/](../examples/frontend-guide/)**. Do not invent API
surface: if an endpoint, parameter or operator is not listed here or in
`/openapi.json`, the engine does not serve it.

---

## 0. Step zero — inventory what YOUR backend serves (before any code)

An external-agent dry run of this document found one failure mode that beats
everything else: **guessing the surface**. Do this first, in order:

1. **`GET /openapi.json`** on the running backend — the machine contract for
   the WHOLE served surface: every generated route, `/auth/*`, `/api/files`,
   **and the backend's registered custom routes** (typically the whole
   anonymous storefront surface). A custom route appears as a normal path item
   flagged `x-appximo-custom-route: true`, carrying its method, its auth mode
   (`x-public: true` ⇒ no token needed, a valid one is recognized; otherwise
   Bearer + the RBAC grant named in its description), `x-required-role` when
   the route demands one, `x-byte-serving` for binary streams (images,
   downloads), and the author's one-line summary. What the OpenAPI does NOT
   carry for custom routes is request/response **shapes** — a Go handler
   declares none; shapes come from the contract sheet (step 2).
   ⚠ Probing is still not discovery: an unknown `/api/...` answers `401`, not
   `404` (auth runs before routing, deliberately) — the contract, not the
   probe, is the authority for what exists.
2. **Get the backend's contract sheet.** For everything `/openapi.json` cannot
   tell you, the backend author (human or agent) must hand you, in writing:
   - the **custom routes' shapes** — params, body and response per route (the
     OpenAPI names the routes; the sheet gives them shapes — the reference
     storefront keeps exactly this as a `STOREFRONT_API.md`; `backend-spec`
     §3.6b tells the backend agent to write one);
   - the **role matrix** — which roles exist and what each may do (drives
     which buttons you render; the JWT only tells you the caller's own role);
   - any **state machines** — states + legal transitions per status field
     (you mirror them in the UI, §6.6; the OpenAPI now PUBLISHES them —
     `x-appximo-initial` / `x-appximo-transitions` on the field's property
     schema, a terminal state present with an empty list — so read them from
     the contract instead of asking);
   - the **upload limits** (instance-wide max bytes/extensions AND any
     per-field `accept`/`max_bytes` policies — §7.4) and the **public rate
     budgets** (§9.5).
3. **Get working dev credentials.** Public signup is DISABLED by default
   (`POST /auth/signup` → 403), so on most backends you cannot mint your own
   first user: ask the operator to create one (they have an admin API/CLI for
   it), or to set `APPXIMO_AUTH_SIGNUP_ROLE` in the dev instance. Without a
   session you cannot exercise anything authenticated — including §11's
   acceptance run.

If you cannot obtain the contract sheet, say so and stop: every hour spent
reverse-engineering shapes from 422s is an hour the sheet would have saved.

---

## 1. Where does the frontend live? (the first decision)

Two deployment shapes. **Default to embedded** unless you have a named reason.

| | **Embedded in the binary** (default) | **Served apart** (CDN / another server) |
|---|---|---|
| How | your built SPA is `go:embed`-ded and served by `Config.Static` | any static host; the API stays on its own domain |
| Artifacts | **one binary** = frontend + API + admin + docs | two deploys, two lifecycles |
| Origin | same origin → **no CORS**, cookies/headers just work | cross-origin → configure engine CORS (§4.10) |
| Tenant | automatic — the SPA is served from the tenant's own domain, so every `fetch('/api/…')` carries the right Host | the frontend must call the right tenant domain explicitly |
| CSP | the static mount owns a correct SPA policy (§9.1) | yours to configure on the host |
| Cost | rebuild the binary to ship UI changes | frontend deploys independently |
| Runtime | no Node in production — Node is a **build-time** tool only | same (static files) |

**Embedded** is the product's flagship shape: one artifact, one deploy, one
origin, no CORS, and the tenant identity comes for free from the domain the
browser is already on. **Served apart** is right when a separate team owns the
frontend's deploy cadence, or when one frontend must talk to many tenant
domains (then CORS + explicit base URLs are the price).

**No Go toolchain? The flagship shape does not require one.** The distributed
engine binary serves your built frontend directly (PUBLIC-SURFACE-S1):

```bash
appximo serve --schema schema.json --static ./web/build --spa
```

`--static` is `[urlpath=]dir` and repeats (`--static /site=./dist` mounts a
sub-path); `--spa` turns on the client-routing fallback. Environment
equivalents for systemd/Docker: `APPXIMO_STATIC_DIR` (comma-separated specs),
`APPXIMO_STATIC_SPA`, `APPXIMO_STATIC_CSP` (verbatim policy, or `off`).
`appximo up --static ./web/build --spa` serves it from the first minute, and
`appximo init <name>` scaffolds the Go variant below with `Config.Static`
already wired (it compiles as generated: `go mod tidy && go build`). Same
mount validation, same CSP, same serving rules in every form — one
implementation behind all of them.

What embedded looks like in the backend's `main.go` (the backend agent usually
writes this; shown so you know the contract you are building into):

```go
//go:embed all:web/build
var frontendFS embed.FS            // `all:` — bundlers emit _-prefixed dirs the default pattern skips

dist, _ := fs.Sub(frontendFS, "web/build")
app, _ := appximo.New(appximo.Config{
    SchemaPath: "schema.json",
    Static: []appximo.StaticMount{{
        Path: "/",     // the SPA owns the root; /api, /auth, /admin, /editor, /docs stay the engine's
        FS:   dist,
        SPA:  true,    // unknown CLIENT routes fall back to index.html; /api/nope stays a real 404
    }},
})
```

Serving rules you can rely on: the index is always `no-cache` (it names the
hashed bundles); `assets/`, `_app/`, `static/` are `immutable, max-age=31536000`;
a missing file **with an extension** is a 404 (never the shell); engine prefixes
(`/api`, `/auth`, `/admin`, `/editor`, `/docs`, `/graphql`, `/openapi.json`,
`/healthz`…) are never shadowed by the mount; assets pay no tenant transaction,
no RBAC, no response cache.

---

## 2. The stack — recommendation and the real criterion

**Recommended: SvelteKit + `adapter-static`, as a pure SPA (`ssr = false`), CSS
by hand with design tokens (no CSS framework), Node used only to build.**

The exact, proven configuration:

```js
// svelte.config.js
import adapter from '@sveltejs/adapter-static';
export default {
  kit: {
    adapter: adapter({ pages: 'build', assets: 'build', fallback: 'index.html', precompress: false })
  }
};
```

```js
// src/routes/+layout.js — the whole app is client-rendered
export const ssr = false;
export const prerender = false;
export const csr = true;
```

Why this stack — the honest argument:

1. **No SSR, ever.** SSR requires a Node process at runtime, which breaks the
   one-binary model (and adds a second failure domain). `adapter-static` with a
   `fallback` emits exactly what `Config.Static{SPA: true}` serves: one shell +
   hashed assets. If a framework pushes you toward SSR by default, that default
   is wrong here.
2. **The frontend will be written by an AI, probably a cheap one.** This is the
   product's thesis applied to the UI: the criterion is not peak framework
   performance but *what a model writes correctly on the first try*. That favors
   boring, explicit, locally-readable code: plain `fetch`, visible state
   variables, CSS you can see. Svelte 5's template syntax keeps markup close to
   HTML and state transitions explicit (`$state`, `$derived`), which models
   handle well; a hand-rolled token stylesheet (~200 lines) avoids both a class
   soup the model must memorize and a dependency to version.
3. **Small and self-contained.** The reference storefront's full dependency list
   is five dev-dependencies (`svelte`, `@sveltejs/kit`, `adapter-static`,
   `vite-plugin-svelte`, `vite`). Nothing at runtime.

Viable alternatives, and what each trades away: **React + Vite** — the most
model-training data of any framework; heavier output, more ceremony per screen,
and you must resist the SSR-flavored templates (Next.js is NOT a fit — its value
is the server side you must not have). **SolidJS + Vite** — proven here too (the
engine's own admin panel); smaller community, less training data. **Vanilla JS +
Vite (or no build at all)** — right for small tools and for examples; at ~10+
screens hand-rolled routing and state outgrow it. All are fine; all must end in
the same shape: **static files, hashed assets, one `index.html` fallback, no
server rendering.**

One structural rule that pays off with any framework: put ALL API access in one
module (`lib/api.js`) and all money/date formatting next to it. The error
contract (§5) is implemented once, in that module, and every screen consumes it.

---

## 3. Development loop

The SPA is developed against a locally running backend:

```bash
# terminal 1 — the backend (engine or consumer binary), e.g. on :8099
DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… ./myapp serve --schema schema.json --port 8099

# terminal 2 — the SPA dev server with a proxy
npm run dev        # vite dev, with server.proxy sending /api,/auth → http://<tenant>.localhost:8099
```

Vite proxy config that preserves the tenant Host (the engine resolves the tenant
from it — §4.1):

```js
// vite.config.js
// FE1: the import is from '@sveltejs/kit/vite' — NOT from
// '@sveltejs/vite-plugin-svelte' (which §2's dependency list also installs;
// guessing that one is an immediate "does not provide an export named
// 'sveltekit'" build error).
import { sveltekit } from '@sveltejs/kit/vite';

export default {
  plugins: [sveltekit()],
  server: {
    proxy: Object.fromEntries(['/api', '/auth', '/openapi.json'].map((p) => [p, {
      target: 'http://127.0.0.1:8099',
      headers: { host: 'acme.localhost' }   // the tenant under development
    }]))
  }
};
```

`*.localhost` names resolve to 127.0.0.1 in modern browsers and OSes, so you can
also skip the proxy entirely and open `http://acme.localhost:8099` against the
real binary — that is the closest thing to production (same origin, real CSP,
real cache headers) and what the reference project's e2e suites do.

For production: `npm run build` **then** `go build` (order matters — §9.2).

---

## 4. The API contract

### 4.1 Tenant = Host. Always.

Every data-plane request resolves its tenant from the **Host header's first
label**: `acme.example.com` → tenant `acme` (Postgres schema `tenant_acme`).
There is no tenant parameter, no header to set from JS — when the SPA is served
from the tenant's own domain, `fetch('/api/…')` is already tenant-correct.
Facts that matter to a frontend:

- A tenant id matches `^[a-z][a-z0-9]{1,29}$` and **must equal the domain's
  first label** — `acme` answers only at `acme.<domain>`. A token for tenant A
  presented on tenant B's domain is a `401` whose body names the host that
  arrived, the tenant it implies, the tenant the token carries, and the address
  where the token would work. Show that message; it is written for humans.
- A bare host with no subdomain (`localhost`, an IP) is not a tenant — data
  routes fail. In dev, use `acme.localhost:PORT`.
- Host matching is case-insensitive.

### 4.2 Auth — what a frontend implements

The engine ships auth as a product (password login, refresh, signup, password
reset, email verification, OAuth, TOTP MFA). The frontend consumes it; nothing
here needs backend code.

**Login** — `POST /auth/login` `{"email","password"}`:

- `200 {"user": {...}, "token": "<jwt>"}` — the session. The JWT is HS256,
  ~24 h TTL, and carries `role`, `user_id`, `tenant_id` claims.
- `200 {"mfa_required": true, "mfa_token": "…"}` — the user has TOTP enabled.
  Collect the 6-digit code and finish with `POST /auth/mfa/verify`
  `{"mfa_token","code"}` → `200 {user, token}`. (A backup code goes in the same
  `code` field.) Handle this branch even if you think MFA is off.
- `401 {"error":"invalid credentials"}` — same body for wrong password and
  unknown email (deliberate anti-enumeration; do NOT tell the user which).
- `429` — throttled per (tenant, email). Show "too many attempts, wait a bit".
- `403` — login blocked (unverified email when the instance requires it, or a
  suspended account). Show the server's message.

**Session storage.** Keep `{token, email, role}` in `localStorage`, read the
`role` claim from the JWT payload (base64url-decode — display/affordance ONLY,
never a security boundary) and check `exp` client-side to avoid firing requests
you know will 401:

```js
function jwtPayload(token) {
  try { return JSON.parse(atob(token.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))); }
  catch { return {}; }
}
const isExpired = (token) => (jwtPayload(token).exp ?? 0) * 1000 < Date.now();
```

**Refresh** — `POST /auth/refresh` with the still-valid token (Authorization
header or `{"token":"…"}` body) → `200 {token}`. It re-mints (stateless); the
old token stays valid until its own `exp`. There is no separate refresh token.
Pattern: refresh opportunistically when `exp` is near (e.g. < 2 h), and treat
any `401` as the session ending.

**Logout** = discard the token client-side. There is no server session to kill.

**The 401 bounce** — wrap every authenticated call once:

```js
export async function papi(path, opts = {}) {          // panel/authenticated api
  try { return await api(path, { ...opts, token: auth.token }); }
  catch (e) {
    if (e instanceof ApiError && e.status === 401) { clearSession(); goto('/panel/login'); }
    throw e;
  }
}
```

**Signup** — `POST /auth/signup` `{"email","password"}` → `201 {user, token}`
(auto-login). It answers `403` when the instance has not enabled public signup
(`APPXIMO_AUTH_SIGNUP_ROLE` unset) — **which is the default**: build the
screen only if the product enables it, and get your own dev credentials
through the operator (§0.3), never by assuming signup works.
Duplicate email in this tenant → `409`. A `role` you send is ignored.

**Password reset / email verification** (all tenant-aware, all uniform-response
anti-enumeration): `POST /auth/reset/request {"email"}` → always `200` "if that
email is registered…"; the emailed link carries a token your reset screen posts
to `POST /auth/reset/confirm {"token","new_password"}` (`400` invalid/expired,
`422` too short). Verification: `POST /auth/verify/request`, link →
`GET/POST /auth/verify`.

**OAuth** — `GET /auth/oauth` lists configured providers
(`{"providers":["google",…]}`): render one button per entry, each a plain link
to `GET /auth/oauth/{provider}`. The engine redirects to the provider and back;
on success it either returns `{user, token}` or 302s to the configured SPA URL
with `#token=<jwt>` in the fragment — read it, store it, strip the fragment.

**Roles drive affordances, not security.** Hide/disable what the role cannot do
(read the role claim), but every decision is re-made server-side — a 403 must
never be treated as a bug.

### 4.3 The generated CRUD

Every schema resource `tasks` serves:

```
GET    /api/tasks              list — {"data":[…], "meta":{page, per_page, has_next, has_prev}}
POST   /api/tasks              create → 201 {…row…}
GET    /api/tasks/{id}         one row (404 if absent OR hidden from your role — indistinguishable, by design)
PUT    /api/tasks/{id}         full replace (omitted optional fields become NULL)
PATCH  /api/tasks/{id}         partial update (validates only what you send) — prefer this from UIs
DELETE /api/tasks/{id}         204
GET    /api/tasks/events       SSE change stream (§4.8)
GET    /api/tasks/aggregate    aggregation (§4.7)
GET    /api/tasks/{id}/customer   read-through of a relation field (customer_id → the referenced row)
```

All authenticated (`Authorization: Bearer <jwt>`) unless the backend registered
public custom routes. Response envelope for lists is always `{"data": [...],
"meta": {...}}`; single reads return the bare object. An `id` UUID primary key
is implicit on every resource. **Money fields are `int64` in minor units**
(`price_cents`) — format at the edge, never do float math:

```js
export const cop = (cents) =>
  new Intl.NumberFormat('es-CO', { style: 'currency', currency: 'COP', maximumFractionDigits: 0 })
    .format((cents ?? 0) / 100);
```

**Discover the surface, don't guess it**: `GET /openapi.json` (unauthenticated)
is the machine contract for every generated route + `/auth/*` + `/api/files`;
`/docs` is its Swagger UI. **Custom routes are NOT in it** — they come from the
backend's contract sheet (§0.2). On a storefront, the anonymous surface is
usually ALL custom routes: the generated CRUD is authenticated, so the public
catalogue/checkout/order-status endpoints are registered handlers whose shapes
only the backend author can tell you.

### 4.4 Filters, search, sort, pagination — the exact grammar

**Filters**: `?filter[field]=v` (implies `eq`) or `?filter[field][op]=v`.
The complete operator set, by field type — **this table is closed**:

| Field type | Operators |
|---|---|
| `string`, `text` | `eq`, `partial` (contains, case-insensitive), `start` (prefix), `is_null` |
| `int`, `int64`, `float64` | `eq`, `gt`, `gte`, `lt`, `lte`, `is_null` |
| `time` | `eq`, `gt`, `gte`, `lt`, `lte`, `after`, `before`, `is_null` |
| `uuid`, `bool`, `json`, `jsonb`, `file` | `eq`, `is_null` |
| `id` (the implicit PK) | `eq` (never `is_null` — a PK is never null) |

**Timing, from the engine (APP-PODER-S1):** every generated read answers
with a `Server-Timing` header — `query;dur=<ms>` (the database stage) and
`app;dur=<ms>` (engine time until the body starts), or `cache;desc="hit"`
when the response cache answered. Read it with
`res.headers.get('server-timing')` and show it next to your own round-trip
time: that is the honest "how long did the query take", independent of how
many rows you paint (the embedded `/app` footer does exactly this).

Facts an agent must not learn the hard way:

- **`neq`, `in`, `nin`, `like`, `ilike` DO NOT EXIST.** An unknown
  operator is a `400` naming it and listing the allowed set. Multi-value → make
  N requests or ask the backend for a custom route.
- **Filter by NULL with `?filter[field][is_null]=true`** (`false` → IS NOT NULL;
  the only accepted values are `true`/`1`/`false`/`0` — anything else is a named
  `400`). It works on every nullable column; on the implicit `id` or a
  `required` field it is a `400` saying the column can never be null. GraphQL:
  `filter: { field: { is_null: true } }` (every filterable type carries it;
  `uuid`/`bool`/`file`/`json`/`jsonb` fields take a `NullFilter` with only
  `is_null`).
- A wrongly-typed value (`filter[amount][gt]=abc`) is a `400` naming the
  parameter, the value and the expected type. An empty value on a non-text field
  is a `400`; on string/text it legitimately means the empty string.
- These are querystring characters (`[`, `]`) — `curl` needs `-g`; `fetch` and
  `URLSearchParams` handle them fine.

**Search**: `?search=term` — case-insensitive substring across the resource's
string/text fields only (OR-ed together, AND-ed with filters). It is a plain
ILIKE, not ranked full-text; fine for a filter box, not a search engine.

**Sort**: `?sort=field&order=asc|desc` — **one field only**. No `sort=a,b`, no
`sort=field:desc` (both are 400s that list the sortable fields). Unknown field,
empty `sort=`/`order=`, or `order` without `sort` — all named 400s. A 400 here
is YOUR bug, not the user's: fix the request.

**Pagination — keyset (prefer it)**: `?after=<last-id-of-page>` /
`?before=<first-id>` with `?per_page=` (default 20, max 100 — over the cap it
clamps and `meta` reports the effective value). Drive it from
`meta.has_next` + the last row's `id`.

The rules, all named 400s when broken: **a cursor cannot combine with
`sort`/`order`, `page`, `count`, or the other cursor** — cursor pagination
orders by `id`, so paginate user-sorted tables with `?page=` (offset-based,
exists, fine at back-office scale) and keep `after` cursors for default-order
feeds. A cursor response's `meta` carries **`per_page` + `has_next` only — no
`page`/`has_prev`** (a cursor request has no page number; code the pager off
`has_next`, not `page`). `?page=0`, negative, or empty `page`/`per_page` are
named 400s. **A repeated parameter is a 400** (`?per_page=20&per_page=100` —
send each parameter once; watch out for URL-building code that appends instead
of replacing).

**Count**: `?count=true` on a list adds `meta.total` + `meta.total_pages` (a
real COUNT over the same filtered, RBAC-scoped set). The flag is read **by
value**: `count=false`/`count=0` (and omitting it) mean OFF, bare `?count` and
`count=true`/`1` mean ON, anything else is a named 400. It composes with
`?include=`; it does NOT combine with a cursor (named 400 — the total would
cover only the rows past the cursor).

### 4.5 Embedded relations — `?include=`

Declared relations serve nested in ONE round-trip, opt-in per request:

```
GET /api/ordenes/{id}?include=lineas,cliente,direccion
GET /api/ordenes?include=lineas.producto        ← dot-nesting, max depth 2 (deeper → 400)
```

Each embedded child list is bounded (relation-declared `limit`, default 50).
RBAC is compiled in: asking for a target your role cannot read → `403`; rows and
fields of the embed are scoped exactly like a direct read. **This is the
screen-shaped read**: a detail page that needs the record + its children +
its parent should be one GET with `include`, not N fetches.

### 4.6 A word on writes

`POST` (create) applies schema defaults for omitted fields; `PATCH` validates
only what you send; `PUT` replaces (omitted optional → NULL — from a form UI you
almost always want PATCH). Send **numbers as JSON numbers** — `{"amount": "7"}`
is accepted on create (deferred to Postgres) but **rejected on update**; don't
rely on the leniency, `Number(input.value)` before sending. An explicit `null`
clears a nullable field. Unknown keys are a `422 unknown_field` (never silently
dropped). Fields with `state_machine` only move along declared transitions —
build the UI from the machine (offer only legal next states; §6.6) and still
handle the `422`.

**The empty-form-field trap** (found in a browser, invisible to curl):
`required` means *present and not null* — an empty text input submits `""`,
which **passes** `required` and creates a blank record with a 201. A schema
whose required text fields matter to a form must also declare `minLength: 1`
(then the empty submit is the normal `422` with rule `minLength`); ask the
backend for it, or strip empty-string keys from the payload before sending
(omitting the key is what triggers `required`). `appximo validate` now WARNS
on a required text field without a content rule (rule
`required_text_without_min_length`), so a schema that skips this is flagged at
authoring time, not discovered in a browser.

Multi-step writes that must land together (a checkout: order + lines + stock)
are **one `POST /api/transaction`** `{"operations":[{op,resource,data|id}…]}` —
all-or-nothing, each op RBAC-checked; on failure the body names the failing op
(`failed_operation` index + the same error shapes as single ops). Up to 100 ops.

### 4.7 Aggregation

```
GET /api/orders/aggregate?count&sum=total_cents&avg=total_cents&min=created_at&max=created_at
GET /api/orders/aggregate?count&group_by=status&filter[status][eq]=paid
```

Functions: `count`, `sum`/`avg` (numeric fields), `min`/`max` (numeric or
time); `group_by` any non-json field(s). Without `group_by`: one object with
just the requested keys (`{"count":17,"sum":{"total_cents":4210}}`). With:
`{"groups":[{"status":"paid","count":12,"sum":{…}},…]}`. Row-condition-scoped
like every read — a user-scoped role aggregates only its own rows. This is the
dashboard endpoint: a back-office home ("orders by status", "today's revenue")
should be 1–2 aggregate calls, zero custom backend code.

### 4.8 Live updates — SSE

`GET /api/{resource}/events` streams `data: {...}` frames on every committed
write to that resource (RBAC-filtered at delivery: rows/fields you couldn't
read don't reach you). It requires the `Authorization` header — and the
browser's `EventSource` API cannot send one. Use a fetch-reader instead:

```js
const res = await fetch('/api/ordenes/events', {
  headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
  signal: abortController.signal
});
const reader = res.body.getReader(); const dec = new TextDecoder();
let buf = '';
for (;;) {
  const { value, done } = await reader.read(); if (done) break;
  buf += dec.decode(value, { stream: true });
  let i; while ((i = buf.indexOf('\n\n')) >= 0) {
    const frame = buf.slice(0, i); buf = buf.slice(i + 2);
    const data = frame.split('\n').filter(l => l.startsWith('data:')).map(l => l.slice(5)).join('\n');
    if (data) handle(JSON.parse(data));
  }
}
```

Reconnect with backoff on error; re-fetch the list on reconnect (frames missed
while disconnected are gone). **Polling is often the right call anyway** — a
2-second poll of one endpoint is simpler, survives proxies, and is what the
reference storefront ships for payment confirmation (§6.5). Choose SSE when
several users watch the same screen (a kitchen display, a dispatch board).

### 4.9 GraphQL (exists; REST is the storefront default)

`POST /graphql` serves queries + `create/update/delete<Singular>` mutations over
the same data, validation and RBAC. Anything executable answers **HTTP 200 —
read `body.errors`**, not the status (fields errors arrive as
`errors[].extensions.fields`, same shape as REST's 422). A non-JSON body is 400;
over 1 MiB is 413. Introspection is off in production (enable GraphiQL
explicitly with `APPXIMO_GRAPHQL_PLAYGROUND=on`); document size is bounded
(50 root selections / 2000 total). Use it when a screen composes many resources
in one round trip and `?include=` can't express it; otherwise REST + include is
simpler to debug.

### 4.10 CORS — only for the served-apart shape

Same-origin (embedded) needs none of this. A frontend on another origin needs
the **backend operator** to set `APPXIMO_CORS_ORIGINS=https://app.example.com`
(setting it enables CORS; methods/headers have sane defaults; `OPTIONS`
preflight is answered before auth so it never 401s). Scope: `/api/*`, `/auth/*`,
`/graphql`, `/openapi*` only. If you see a CORS error in the console against an
embedded SPA, the bug is elsewhere (usually a wrong absolute URL — use relative
paths).

### 4.11 Health & meta

`/health` → `{"status":"ok","version":"<build>"}` (no auth) — a footer "server
vX" or a connectivity probe. `/openapi.json`, `/docs`, `/graphiql` (dev),
`/healthz`, `/readyz` exist on the same origin. `/metrics` and `/debug/*` are
admin-gated; not for frontends.

---

## 5. Errors are a UI contract — map every status to a screen state

The engine's error surface is uniform and named. Implement ONE error type in
your API module and let every screen switch on it. The reference
implementation (used by every pattern in §6):

```js
export class ApiError extends Error {
  constructor(status, body) {
    super(body?.error || `HTTP ${status}`);
    this.status = status;                 // 0 = network failure (fetch threw)
    this.body = body || {};
    this.fields = Array.isArray(body?.fields) ? body.fields : [];  // the 422 per-field list
  }
  get isNetwork() { return this.status === 0; }
  fieldMap() {                             // field name → first message, for form binding
    const m = {};
    for (const f of this.fields) if (!m[f.field]) m[f.field] = f.message;
    return m;
  }
}

export async function api(path, { method = 'GET', body, token, headers = {} } = {}) {
  const h = { ...headers };
  if (body !== undefined) h['Content-Type'] = 'application/json';
  if (token) h['Authorization'] = `Bearer ${token}`;
  let res;
  try {
    res = await fetch(path, { method, headers: h, body: body !== undefined ? JSON.stringify(body) : undefined });
  } catch {
    throw new ApiError(0, { error: 'Sin conexión. Revisá tu internet e intentá de nuevo.' });
  }
  let data = null;
  const text = await res.text();
  if (text) { try { data = JSON.parse(text); } catch { data = { error: text.slice(0, 200) }; } }
  if (!res.ok) throw new ApiError(res.status, data);
  return data;
}
```

The status → state table. "Same shape everywhere" is load-bearing: REST create,
REST update, batch ops and GraphQL all emit the identical validation error, so
one form component handles all of them.

| Status | Body shape | What it means | The screen state |
|---|---|---|---|
| **422** | `{"error":"validation_failed","fields":[{"field","rule","message"}…]}` — EVERY failing field at once; nested paths dotted (`direccion.ciudad`); file fields use rule `file_not_found` | the input is invalid | mark each field in place with its message, show a summary banner, **scroll to the first invalid field**, keep everything the user typed (§6.4) |
| **422** (no `fields`) | `{"error":"…"}` | a custom route's business validation | show the message near the action |
| **409** | `{"error":"field \"sku\": value already exists"}` · `{"error":"cannot delete: still referenced by \"orders\" record(s)"}` · custom (`stock insuficiente para X: disponible N, solicitado M`) | a conflict: uniqueness, referential restrict, optimistic-lock/state race, or business (out of stock) | honest, specific message; **never discard the user's work** — keep the form/cart editable so they can adjust and retry (§6.4) |
| **401** | `{"error":"invalid token: …"}` / `invalid credentials` / the tenant-mismatch explainer | not authenticated (missing/expired/invalid token, wrong tenant domain) | on an authenticated screen: clear the session, go to login (remember where they were). NEVER retry-loop a 401 |
| **403** | `{"error":"forbidden"}` (or a custom route's message) | authenticated but not allowed (role) | disable/hide the affordance; if reached anyway, a calm "your account can't do this" — not an error page. Fix the UI to not offer it |
| **404** | `{"error":"…"}` | absent — **or hidden by a row condition; you cannot tell which** (deliberate, anti-enumeration) | "not found" screen with a way back; don't speculate about permissions |
| **429** | `{"error":"…"}` (rate limit) | too many requests (per tenant, and per (tenant, IP) on public routes) | back off: pause polling ×2, disable the button briefly; show "un momento…" if user-triggered |
| **503** | `{"error":"…"}` + `Retry-After` header | the database is unavailable — the engine says so honestly | "the store is catching its breath" + auto-retry after `Retry-After` seconds (cap the retries); do NOT read it as a crash |
| **500** | `{"error":"internal error"}` (masked, never a raw DB error) | a genuine server bug | generic apology + retry button; log it |
| **400** | `{"error":"…"}` naming the parameter/operator/value | a malformed request — a FRONTEND bug (bad filter/sort/param) | fix your code; in production show a generic error, console.error the message |
| **413** | body too large (1 MiB JSON; file uploads: the configured cap) | payload too big | for uploads: show the limit and the file's size (§7.4) |
| **0** (thrown) | — | network failure, server unreachable | inline "no connection" + Retry button; on polling screens keep polling with backoff and a "conexión inestable" note (§6.5) |

Two writing rules the reference storefront follows: error copy is **in the
user's language and names what actually happened** ("Se nos adelantaron:
stock insuficiente para CAM-OXF-M: disponible 1, solicitado 2" — the server
message is written to be shown), and every destructive/paying action reports
what did NOT happen ("No se realizó ningún cobro — intentá de nuevo").

### 5.1 Localizing the 422s — the `rule` is the contract, build the map ONCE

The engine's `message` strings are **English only, by decision** (PUBLIC-
SURFACE-S1): one source of truth in the engine beats N half-translated
catalogs, and no server-chosen locale would match every user of every tenant
anyway. What IS designed for you is the **`rule` key: a closed, stable set** —
map it once to your app's language and interpolate the field's own limits from
the schema you already have (via `/openapi.json`: `minLength`, `maximum`,
`enum`…). The complete set a form can receive:

| `rule` | fires when | example UI copy (es) |
|---|---|---|
| `required` | the key is absent or null on POST/PUT | «Este campo es obligatorio» |
| `type` | value of the wrong JSON type | «Valor inválido» |
| `unknown_field` | a key the resource doesn't declare | (frontend bug — fix the payload) |
| `read_only` | `id`/`auto` field sent in a write body | (frontend bug — strip it) |
| `min` / `max` | numeric bounds | «Debe ser al menos {min}» |
| `minLength` / `maxLength` | string length (runes) | «Mínimo {minLength} caracteres» |
| `pattern` | regex mismatch | «Formato inválido» |
| `format` | email/uuid/url/date mismatch | «No parece un correo válido» |
| `enum` | value outside the declared set | «Elegí una opción de la lista» |
| `state` | invalid state-machine create/transition | «No se puede pasar de X a Y» |
| `file_not_found` | a `file` field names no existing upload | «El archivo ya no existe — subilo de nuevo» |
| `file_policy` | attached file violates `accept`/`max_bytes` | «Este campo acepta {accept}, máx {max}» |

Anything NOT in a `fields[]` entry (409 uniqueness, 403, custom-route
messages) is prose meant to be shown or mapped by status (§5 table). Treat an
unknown `rule` as generic invalid — new rules may appear, removals/renames
are treated as breaking.

---

## 6. The mandatory screen states

Every data screen has at least: **loading → (content | empty | error)**, and
forms add **submitting → (success | invalid | conflict)**. These are the six
patterns a production frontend cannot skip, each proven in the reference
storefront.

### 6.1 Loading — skeletons, not spinners-on-white

Render the layout's silhouette (grid of gray tiles sized like the real cards)
so the page doesn't jump. Mark the region `aria-busy="true"`.

### 6.2 Empty — a state, not an absence

Zero rows is a designed screen: icon, one honest sentence, one action
("Todavía no hay productos — el comerciante está montando su vitrina" /
"No hay nada para pagar" + a CTA back to the catalogue). An empty FILTERED list
is a different message ("nada coincide con tu búsqueda") with a clear-filters
affordance.

### 6.3 Network error — inline, with retry, work preserved

```svelte
{#if error}
  <div class="banner error" role="alert">
    <span>⚠️</span>
    <div>{error}
      <div><button class="btn small ghost" onclick={cargar}>Reintentar</button></div>
    </div>
  </div>
{:else if cargando} …skeleton… {:else} …content… {/if}
```

Never a blank screen, never `alert()`, never losing form state because a fetch
failed. Distinguish `isNetwork` (their connection) from a 5xx (your server) in
the copy.

### 6.4 The invalid form — 422 field-by-field, 409 keeping the work

```js
try {
  await api('/api/checkout', { method: 'POST', body });
} catch (e) {
  if (e instanceof ApiError && e.status === 422 && e.fields.length) {
    errores = e.fieldMap();                       // field → message, marks each input
    errorGeneral = 'Revisá los campos marcados.';
    setTimeout(() => document.querySelector('.input.invalid')
      ?.scrollIntoView({ block: 'center', behavior: 'smooth' }), 50);
  } else if (e instanceof ApiError && e.status === 409) {
    // someone bought it first / duplicate value — honest message, form/cart INTACT
    errorGeneral = `Se nos adelantaron: ${e.body.error}. Ajustá la cantidad o elegí otra presentación.`;
  } else {
    errorGeneral = e.isNetwork ? e.message
      : 'Algo falló al crear el pedido. No se realizó ningún cobro — intentá de nuevo.';
  }
}
```

The engine returns **every** failing field at once — surface them all in one
pass (mark inputs + per-field message + summary + scroll to first), don't drip
them one request at a time. Nested field names arrive dotted
(`direccion.linea1`) — key your error map by the same strings.

And remember the empty-form trap from §4.6: an empty text input submits `""`,
which **passes** `required` — a form that relies on the server catching blanks
needs the schema to declare `minLength: 1`, or must strip empty-string keys
from the payload before sending (omitting the key is what triggers `required`).

### 6.5 Waiting for the world — polling pending → confirmed

Any state that an external system settles (a payment via webhook, a job via
worker) is rendered as an honest *waiting* screen that polls, with: a capped
poll count, tolerance for network blips (keep polling, show "conexión
inestable — seguimos intentando"), and side effects **only on confirmation**
(the reference storefront clears the cart only when the order reaches `pagada`;
a declined payment keeps it so the customer retries). The skeleton:

```js
async function consultar() {
  try {
    pedido = await api(`/api/orden-publica?numero=…&email=…`);
    if (pagada) clearCart();                              // effect ON CONFIRMATION only
    if (esperando && polls++ < 60) timer = setTimeout(consultar, 2000);
  } catch (e) {
    if (e.status === 404) { notFound = true; return; }
    error = e.isNetwork ? e.message : 'No pudimos consultar tu pedido.';
    if (polls++ < 60) timer = setTimeout(consultar, 3000); // blip ≠ stop
  }
}
```

Poll every ~2 s, stop after ~2 min with a "still pending — we'll contact you"
state. Each terminal state is its own screen: confirmed (✓, next steps),
declined (✕, "no charge was made, your cart is intact", retry CTA), cancelled,
refunded.

### 6.6 Lifecycles in the UI — offer only legal moves

A `state_machine` field means the server enforces transitions. Mirror the
machine client-side to OFFER only the legal next states (a `SIGUIENTES` map:
`{pagada: ['preparando','cancelada'], …}`), render status as labeled chips, and
still handle the `422 invalid transition` / `409` conflict (two clerks racing) —
reload the row and re-render the buttons when it happens.

### 6.7 The mobile floor

The reference storefront is mobile-first and these are its non-negotiables:
tap targets ≥ 44 px; `inputmode`/`autocomplete` on every form field (`tel`,
`email`, `name`, `street-address` — the difference between typing and
autofilling on a phone); a sticky bottom CTA on purchase flows; horizontal
scroll only inside chip rows/tables (`-webkit-overflow-scrolling: touch`);
system font stack; test at 390×844. `role="alert"` on error banners,
`aria-busy` on loading regions, real `<label>`s.

---

## 7. Files and images

The engine ships a content-addressed, multi-tenant file store; a schema
resource attaches a file with a `file`-typed field (a UUID column + a real FK
to the tenant's files table). This section is the full frontend pattern:
upload → attach → display, for both authenticated screens and public ones.

### 7.1 The three-step flow

1. **Upload the bytes**: `POST /api/files` (multipart, form field `file`,
   `Authorization` required; RBAC action `create` on the `files` resource — a
   scoped role grants it with a `permissions` entry
   `"files": {"actions": ["read","create"]}`; ask the backend for it if
   uploads 403 — `appximo validate` WARNS when a role can write a file field
   but lacks this grant, rule `file_field_without_files_grant`) →
   `201 {"file_id","sha256","size"}`.
2. **Attach**: set the returned `file_id` as the value of the record's `file`
   field like any other field — `PATCH /api/productos/{id}
   {"imagen_id":"<file_id>"}`. A nonexistent or foreign-tenant id → `422`
   with rule `file_not_found`.
3. **Display**: reading the record returns the id; the bytes come from
   `GET /api/files/{id}` (authenticated) or a signed URL (§7.3) or a public
   route the backend registered (§7.5).

Replacing an image = upload new + PATCH the field (the old file stays in the
store — deleting the record never deletes files). Removing =
`PATCH {"imagen_id": null}`. Deleting the FILE itself
(`DELETE /api/files/{id}`) obeys the field's `on_delete`: `restrict` (default)
answers `409` while a record still references it; `set_null` clears the field
on its records.

### 7.2 Upload with progress — XHR, not fetch

`fetch` cannot report **upload** progress; a file-upload UI uses
`XMLHttpRequest`. The complete pattern with every state (progress, rejection,
size cap, network, abort):

```js
export function uploadFile(file, token, { onProgress } = {}) {
  const xhr = new XMLHttpRequest();
  const done = new Promise((resolve, reject) => {
    const form = new FormData();
    form.append('file', file, file.name);          // field name MUST be "file"
    xhr.open('POST', '/api/files');
    xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    xhr.upload.onprogress = (ev) => { if (ev.lengthComputable && onProgress) onProgress(ev.loaded / ev.total); };
    xhr.onload = () => {
      let body = null; try { body = JSON.parse(xhr.responseText); } catch { body = { error: xhr.responseText.slice(0, 200) }; }
      if (xhr.status === 201) resolve(body);
      else reject(new ApiError(xhr.status, body));  // 422 rejected type · 413 too big · 401 session
    };
    xhr.onerror = () => reject(new ApiError(0, { error: 'La subida se cortó. Revisá tu conexión e intentá de nuevo.' }));
    xhr.onabort = () => reject(new ApiError(0, { error: 'Subida cancelada.' }));
    xhr.send(form);
  });
  done.abort = () => xhr.abort();                  // wire this to a Cancel button
  return done;
}
```

The upload screen's states: **idle → picking → uploading (bar from
`onProgress`) → attach (the PATCH) → done**, with three distinct failures:
`422` (the server refused the FILE — extension not allowed, or content that
doesn't match its declared type; show the server's reason), `413` (too big —
show the limit), and network/abort (nothing was saved server-side; safe to
retry — an interrupted upload leaves nothing behind, verified). On the phone,
`<input type="file" accept="image/*">` opens camera/gallery directly.

### 7.3 Displaying protected files — signed URLs, because `<img>` can't send headers

An `<img src="/api/files/{id}">` fails with 401: image elements don't carry
`Authorization`. For authenticated screens (a back-office), mint a short-lived
signed URL and use THAT as `src`:

```js
const { url } = await papi(`/api/files/${id}/url`);   // 200 {"url","expires_in"}  (expires ~180 s)
img.src = url;                                        // no auth header needed on this URL
```

On the local backend the URL is **relative** (`/files/signed/<token>`) — it
drops into a same-origin `src` on any host and port; on S3 it is the
provider's absolute presigned URL. Both work verbatim as `img.src`. Mint per
render, never persist the URL (it expires); batch-mint for a list view. Any
invalid/expired signed URL is a uniform `404`.

**Pre-upload previews**: showing the picked file BEFORE uploading is
`URL.createObjectURL(file)` — a `blob:` URL, which `DefaultStaticCSP` allows
in `img-src` (a blob URL is same-origin and created by the document; the CSP
used to block it and the preview silently didn't render — the curl-blind CSP
class, visible only in the browser console).

### 7.4 The upload limits — instance-wide at upload, per-field at attach

Two layers, both the server's authority:

- **At upload** (`POST /api/files`): the instance-wide max size
  (`APPXIMO_FILES_MAX_BYTES`, default 256 MiB → `413`) and extension
  allowlist (→ `422`) — the backend operator's config.
- **At attach** (setting the file's id in a record's `file` field): the
  field's own declared policy (FILES-1) — `accept` (content-type families or
  exact types, checked against the SNIFFED stored type) and `max_bytes`. A
  violating attach is the standard `422 validation_failed` with
  `fields[{field, rule: "file_policy", message}]` — the message says exactly
  what the field accepts; surface it on the field like any other 422 (§6.4).
  Consequence for the UI: an upload can SUCCEED and the attach still fail —
  do the upload and the record write as one flow and map the attach 422 back
  to the file input.

Ask the backend for both layers (contract sheet, §0.2), mirror them
client-side for a friendly early error (check `file.size` and `file.type`
before uploading), and still handle the server's `413`/`422` (content sniffing
happens server-side — a `.jpg` that isn't a JPEG is rejected regardless of its
name, and the client's Content-Type is never trusted).

### 7.5 PUBLIC images — the storefront pattern

Everything above requires a token. A public storefront's product images must
render for ANONYMOUS visitors — and the engine's file routes are deliberately
authenticated (files are private by default). The pattern is a tiny custom
route in the backend that **authorizes by relationship** ("this file is the
image of a publicly visible product") and then serves the bytes through the
engine:

```go
// Backend (the backend agent writes this; shown so you can ask for it):
app.Register(appximo.Route{
    Method: "GET", Path: "/api/catalogo-imagen",
    Public:      true,                                  // anonymous — it's a storefront
    ByteServing: true,                                  // stream: bypass response cache + compression
    RateLimit:   &appximo.RateLimit{RPS: 200, Burst: 400},  // image-sized budget, like the catalogue
    Handler: func(ctx appximo.Ctx) error {
        id := ctx.Request().URL.Query().Get("id")
        var ok bool                                     // public IFF an ACTIVE product wears it
        if err := ctx.UnsafeTx().QueryRow(ctx.Context(),
            `SELECT EXISTS(SELECT 1 FROM productos WHERE imagen_id = $1 AND estado = 'activo')`,
            id).Scan(&ok); err != nil || !ok {
            return ctx.Error(404, "imagen no encontrada", err) // uniform miss — no oracle
        }
        // Content-addressed store ⇒ this id's bytes can never change: the URL
        // may be cached for a year with zero revalidation (FILES-2).
        return ctx.ServeFile(id, appximo.WithCacheControl(appximo.CacheControlImmutable))
    },
})
```

Frontend side: `<img src="/api/catalogo-imagen?id={imagen_id}">` — a stable
URL, no token. With the immutable cache policy above the browser fetches each
image ONCE (no per-view revalidation; a changed image is a new file id and
therefore a new URL — bust nothing by hand). Without it, the strong ETag still
makes revalidation a cheap 304. The route also answers `HEAD` (headers only —
link unfurlers, CDNs). The security property to preserve: only files
referenced by PUBLIC records are reachable; a draft product's image, or any
unattached upload, stays 404 to the world. Public paths must be literal (no
`{param}` on a Public route) — hence the query parameter.

### 7.6 The image element — always with a fallback

Records with a NULL file field are normal (an image is optional). Render a
designed placeholder — the reference storefront derives a deterministic
gradient + monogram from the SKU so tiles look intentional, not broken — and
also fall back **on load error** (a deleted file, a still-draft product):

```svelte
{#if producto.imagen_id && !failed}
  <img src="/api/catalogo-imagen?id={producto.imagen_id}" alt={producto.nombre}
       loading="lazy" onerror={() => (failed = true)} />
{:else}
  <ProductArt sku={producto.sku} nombre={producto.nombre} />   <!-- the placeholder -->
{/if}
```

`loading="lazy"` on grid images; give the container a fixed `aspect-ratio` so
the grid doesn't reflow as images arrive.

---

## 8. Anonymous storefront + authenticated back-office in ONE app

The reference storefront's architecture, worth copying:

- **Public READS need no Go at all — `rbac.public` in the schema (ADR-026).**
  Declare the resources anyone may read, with a row condition and a field
  allowlist, and the GENERATED endpoints serve them tokenless:

  ```json
  "rbac": { "roles": { … },
    "public": { "articulos": { "actions": ["read"],
        "conditions": {"field":"estado","op":"eq","val":"publicado"},
        "fields": ["id","titulo","cuerpo","portada"] },
      "files": { "actions": ["read"] } } }
  ```

  The SPA then calls `/api/articulos` bare (no Authorization header — an empty
  `Bearer ` is a 401, anonymity means NO header). `/openapi.json` marks these
  ops `security: []` + `x-public: true`. Read-only by design; anonymous
  callers can only see/filter/sort the allowlisted fields, drafts read as
  404, and the surface rides the public rate limiter. The `files` grant makes
  attached images servable via the signed-URL flow with no token (§7.5).
- **Public surface with LOGIC** (a checkout, server-computed prices, composite
  lookups) = the backend's `Route.Public` custom routes. No token,
  per-(tenant, IP) rate limits. The SPA calls them bare.
- **Authenticated surface** = the generated CRUD + custom panel routes, called
  through the `papi` wrapper (§4.2) with the stored JWT.
- **One public checkout serves guests AND logged-in customers**: `Route.Public`
  authentication is *optional* — no token = anonymous (identity fields
  required inline); a valid `Bearer` = claims populated (the order links to the
  account); an INVALID/expired token = `401`, never silently treated as
  anonymous. So: attach the token when you have one, and on a 401 from a
  public route, clear the dead session and retry anonymously (don't strand a
  buyer over an expired panel session).
- **The confirmation page works without login**: a composite-key public lookup
  (`?numero=…&email=…`, both must match, uniform 404) — because a guest has no
  token and the generated owner-scoped reads can structurally never serve them.
  If your product has guest flows, ask the backend for this endpoint shape.
- Role-gated UI inside the panel: read the role claim to decide which buttons
  exist (`empleado` dispatches; only `dueno` sees "reembolsar"), and treat any
  403 as a UI bug to fix, not an error to style.

---

## 9. The traps — what only shows up in a real browser (field-tested)

Each of these cost a real session real time. In order of damage:

1. **A blank page with a 200 is a CSP problem, and `curl` cannot see it.**
   The static mount serves a correct SPA policy — and STRICTER than the
   `DefaultStaticCSP` constant reads: at boot the engine inspects the mount's
   index and upgrades `script-src` per mount (field-verified): no inline
   scripts → `script-src 'self'` only; inline bootstraps (SvelteKit's shell)
   → each pinned by `'sha256-…'` hash, `'unsafe-inline'` dropped — injected
   inline script is blocked; only an unparseable shell keeps the permissive
   form, with the reason in the boot log. Consequence: editing an inline
   script in the shell REQUIRES a binary restart (the hash is computed at
   boot). If you override CSP
   (per mount via `StaticMount.CSP`) and the page renders blank while curl
   shows perfect HTML: open the BROWSER console — you'll find
   `Refused to execute inline script…`. **Never verify a frontend with curl**
   (it doesn't execute CSP, JS, or rendering); the acceptance bar is a real
   browser (§11).
2. **A bare `go build` ships an empty shell.** Hashed assets
   (`build/_app/…`) are conventionally gitignored; only `index.html` is
   committed so `go:embed` resolves. Consequence: the SPA **must be built
   before the binary** — `npm run build && go build` (the canonical consumer
   build script does this and refuses to ship a contract-violating binary).
   Symptom of getting it wrong: the app "deploys fine" and serves a page with
   404s for every bundle.
3. **`go:embed` needs `all:`** (`//go:embed all:web/build`) — bundlers emit
   `_app/`-style underscore-prefixed directories that the default embed pattern
   silently skips. Symptom: index loads, every asset 404s.
4. **Know what needs a restart.** A **field** added by a schema deploy is
   readable AND writable hot (no restart). A **new resource**, new custom
   route, RBAC change, or hook change activates only when the binary restarts
   with the new schema/code (the error for a deployed-but-not-loaded resource
   says so: `resource_not_loaded`). For the frontend this means: shipping a UI
   for a new resource is coupled to a backend restart; a UI for a new FIELD is
   not.
5. **Public-route rate limits are strict by default** (5 rps / burst 10 per
   tenant+IP). A storefront page firing a dozen image/catalogue requests trips
   it instantly unless the backend declared a per-route budget
   (`Route.RateLimit{RPS: 200, Burst: 400}` on read routes). If you see 429s on
   page load, that's the conversation to have with the backend — don't
   retry-hammer.
6. **`localStorage` sessions expire silently.** Check `exp` before navigating
   into an authenticated area, bounce to login on any 401 (`papi` pattern), and
   keep the session small ({token, email, role}) — never cache server data in
   localStorage as a substitute for refetching.
7. **Polling etiquette**: cap it (count + backoff), stop when the tab is done
   (`clearTimeout` on unmount), and only poll endpoints sized for it (the
   order-status route budget is ~30 rps per tenant+IP for exactly this).
8. **`Host` is load-bearing in every dev tool — and Node's `fetch` cannot set
   it.** Browsers on `tenant.localhost` are fine (they resolve `.localhost`
   internally, RFC 6761). From Node the same hostname can be `ENOTFOUND`
   (the OS resolver doesn't know `.localhost` — measured on Windows), and
   `fetch`/undici SILENTLY IGNORES a hand-set `host` header (you get a 401,
   not an error). The escape that actually works from Node (field-verified on
   Windows): the `node:http` module against `127.0.0.1` with the explicit
   header —

   ```js
   const http = require('node:http');
   http.get({ host: '127.0.0.1', port: 8080, path: '/api/posts',
              headers: { Host: 'tenant.localhost', Authorization: `Bearer ${tok}` } }, res => { … });
   ```

   (A proxy that forwards the inbound Host also works; `fetch` with
   `headers: {host}` does NOT.) A `500`/`401` that only happens from a script
   and never from the browser is almost always a missing tenant Host.
9. **Don't hand-build filter strings**: `URLSearchParams` + a tiny helper;
   remember `curl -g` when reproducing (brackets are glob characters in curl).
10. **Money is integers.** All math in minor units, `Intl.NumberFormat` only at
    render. If you ever see a `.5` in a price computation, a float leaked in.
11. **PCI note for checkouts**: keep the payment page free of third-party
    scripts (analytics, chat) — with a hosted gateway widget that keeps the
    merchant in SAQ A; one stray script moves them to SAQ A-EP.

---

## 10. The build & embed recipe (copy this)

For the recommended stack, end to end:

```
web/                       ← the SvelteKit app (SPA)
  svelte.config.js         ← adapter-static, fallback: 'index.html'  (§2)
  src/routes/+layout.js    ← ssr = false; prerender = false
  src/lib/api.js           ← ApiError + api() + cop()  (§5)
  package.json             ← "build": "vite build"
main.go                    ← go:embed all:web/build + Config.Static{Path:"/", SPA:true}
```

Build order, always: `cd web && npm run build` → `go build` (or the project's
`build.sh` / `scripts/build-consumer.sh`, which does both and injects the
version). Commit `web/build/index.html`, gitignore `web/build/_app/` — and
accept the consequence (trap #2).

Dev loop options: Vite dev server + proxy with a tenant Host (§3), or rebuild
& run the binary and open `http://<tenant>.localhost:<port>` (production-
faithful; what the reference e2e suites use).

**If you don't own a backend Go project, you can still ship the one-binary
shape — do NOT fall back to a tarball or a side server.** Field-verified
(second evaluation, 24/24 in a real browser): the framework is a public Go
module, so the whole "backend project" is ~40 lines you can write yourself:

```bash
appximo init myapp        # scaffolds exactly that main.go, compilable as generated
# — or by hand —
go mod init myapp && go get github.com/appximo/appximo@latest
```

The main.go is the §1 snippet plus `ParseServeArgs` + `app.Start()` — nothing
else. Build order is MANDATORY: the SPA first, then the binary
(`cd web && npm run build`, then `go mod tidy && go build`) — the embed
compiles whatever is in `web/build` at that moment. Real measured cost: first
build ≈ 2m36s cold (the dependency graph downloads once; warm rebuilds are
seconds) and the binary ≈ 80 MB — normal for this framework, not a mistake.

**No Go toolchain at all?** The engine binary serves the built tree itself:
`appximo serve --schema schema.json --static ./web/build --spa` (§1) — same
mounts, same CSP, no compilation. The served-apart shape (§1, with
`APPXIMO_CORS_ORIGINS`, §4.10) remains the fallback when a separate host is a
requirement — a constraint someone named, never a default you retreat to.

---

## 11. Verify like a user — the browser is the bar (CONTRACT, not advice)

The reference project's rule, learned twice — and then a third time, the
expensive way: an agent shipped a UI **broken on phones for weeks** (the
document measured 753 px on a 390 px screen; buttons untouchable) **with every
API test green**. Its own conclusion: *an agent that generates UI and only
tests with curl is delivering blind.* curl does not execute CSP, JS, layout or
rendering. So this section is not a pyramid of suggestions: it is the
**definition of done** for any UI built from this spec. A delivery that has
not passed steps 2–4 below is not finished, whatever the API tests say.

**The procedure — run all four, in order, before calling any UI done:**

1. **API contract**: curl/httpie against the endpoints (fast, for the data
   layer — necessary, and NOT sufficient for anything visual).
2. **The mobile layout gate (hard pass/fail, 390×844).** Run this against
   EVERY screen your UI has; exit 0 is the bar. It catches exactly the class
   of failure invisible to any API test: horizontal overflow and untouchable
   controls. (Verified against the reference storefront — and its stricter
   draft immediately caught real sub-24 px controls, which is the point.)

   ```js
   const { chromium } = require('playwright');
   const SCREENS = ['/', '/producto/EXAMPLE', '/carrito', '/panel']; // ← YOUR screens, all of them
   const BASE = 'http://acme.localhost:8099';
   (async () => {
     const browser = await chromium.launch();
     const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
     const failures = [];
     for (const path of SCREENS) {
       await page.goto(BASE + path, { waitUntil: 'load' });
       await page.waitForTimeout(800); // let the SPA settle
       const r = await page.evaluate(() => {
         const doc = document.documentElement;
         const overflowX = doc.scrollWidth - doc.clientWidth;      // MUST be 0
         const wide = [...document.querySelectorAll('*')]
           .filter(el => el.getBoundingClientRect().width > doc.clientWidth + 1)
           .slice(0, 3).map(el => el.tagName + '.' + (el.className || '').toString().slice(0, 30));
         const tiny = [...document.querySelectorAll('a,button,[role=button],input[type=submit]')]
           .map(el => ({ el, b: el.getBoundingClientRect(), d: getComputedStyle(el).display }))
           // WCAG 2.2 target-size (24×24) — inline text links are exempt by the spec
           .filter(({ b, d }) => d !== 'inline' && b.width > 0 && b.height > 0 && (b.width < 24 || b.height < 24))
           .slice(0, 3).map(({ el }) => el.tagName + ':' + (el.textContent || '').trim().slice(0, 20));
         return { overflowX, wide, tiny };
       });
       if (r.overflowX > 0) failures.push(`${path}: ${r.overflowX}px horizontal overflow (wide: ${r.wide.join(', ')})`);
       if (r.tiny.length) failures.push(`${path}: touch targets under 24px: ${r.tiny.join(', ')}`);
     }
     if (failures.length) { console.error('LAYOUT GATE FAILED:\n' + failures.join('\n')); process.exit(1); }
     console.log('layout gate: PASS on', SCREENS.length, 'screens');
     await browser.close();
   })();
   ```

3. **Browser e2e (the acceptance bar)**: Playwright at the SAME mobile
   viewport, driving the REAL binary serving the REAL build — walk the money
   paths (browse → detail → cart → checkout → declined AND approved → panel
   login → the operational flow), assert on visible text and screenshots, and
   listen for `console.error` / `pageerror` events (a CSP violation or a JS
   crash fails the run even when the page "loads").
4. **The states checklist**: for each screen, force loading / empty / network
   error / 422 / 409 / 503 (a proxy or the real backend can produce each) and
   check §6's behaviors: work preserved, retry present, copy honest.

What each layer is blind to — why all four are mandatory: curl cannot see
CSP/JS/layout (step 1 alone shipped the 753 px document); the layout gate
cannot see broken flows (a perfectly-sized dead button passes it); the e2e
cannot see the failure states unless you force them (step 4). Other UI
surfaces built on this engine (the back-office pattern of
`appximo backoffice-spec`) inherit THIS section as their verification bar —
it is defined once, here.

A minimal Playwright skeleton (mobile, console-strict):

```js
import { chromium, devices } from 'playwright';
const browser = await chromium.launch();
const page = await (await browser.newContext({ ...devices['iPhone 12'] })).newPage();
const errors = [];
page.on('pageerror', (e) => errors.push(String(e)));
// Chrome logs EVERY non-2xx response as a console error — a flow that
// deliberately exercises a 422/409 would fail its own check. Filter the
// network-tab noise; keep real JS errors and pageerror strict.
page.on('console', (m) => {
  if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) errors.push(m.text());
});
await page.goto('http://acme.localhost:8099/');
// … walk the flow, assert visible text …
if (errors.length) throw new Error('console errors: ' + errors.join(' | '));
```

Two traps a first e2e run hits:

- **The static mount's CSP has no `'unsafe-eval'`** (correctly), and
  Playwright's string-eval helpers (`page.waitForFunction(...)`,
  `page.evaluate('...')` with a string) violate it — sometimes only
  intermittently, which reads as flake. Use selector/locator waits
  (`waitForSelector`, `expect(locator)`) throughout.
- **Native HTML `required` masks the server's 422 path**: the browser blocks
  the empty submit before the request exists, so that form never exercises the
  §6.4 mapping in your test. Leave native validation attributes off the form
  whose 422 handling you're asserting (keep the schema's `minLength: 1` — the
  server stays the authority).

---

## 12. References

- **This doc**: `appximo frontend-spec` prints it — paste into your agent.
- **The runnable minimal example**: [examples/frontend-guide/](../examples/frontend-guide/)
  (one binary: schema + backend + a no-build SPA exercising login, CRUD with
  the 422/409 mapping, upload → attach → display with public images).
- **The schema**: `appximo spec` · **the backend**: `appximo backend-spec`
  (the §7.5 public-image route, `Route`/`Ctx` and `Config.Static` live there).
- **The machine contract**: `GET /openapi.json` on the running backend; `/docs`
  to browse it.
- **The full-size reference**: the production storefront this doc distills —
  storefront + back-office, SvelteKit, one binary (`Config.Static`), mock
  payment gateway with real signed webhooks; its e2e suites are the model for
  §11.
