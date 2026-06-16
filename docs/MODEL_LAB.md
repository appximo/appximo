# Model Lab — which modern app archetypes can the engine model today?

> **Diagnostic, not a change to the engine.** This maps what the Appitools schema
> can and cannot express for the data patterns behind the apps people actually
> build today, with **live evidence** (schemas that validate, tenants registered,
> rows populated, real queries run). It is the map that defines what the engine
> needs **before** the visual editor / AI layer — because the AI will only be able
> to generate what the engine can model.
>
> Engine version under test: `0c29240` (API-PRODUCTIVA-V1). Date: 2026-06-16.
> Method, per archetype: design a serious schema → `appitools validate` →
> `serve` with that schema → register a tenant → populate rows → run the
> domain-critical REST + GraphQL queries → record what worked and what didn't.
> The six schemas live in [`examples/model-lab/`](../examples/model-lab/); the
> reproduction harness is in [Appendix A](#appendix-a--reproduce).

## TL;DR

| Archetype | Verdict | One-line |
|---|---|---|
| **SaaS / productivity** (Notion/Trello/Linear) | 🟡 partial (closest to yes) | Nesting, tags, assignees, threads model cleanly; **per-workspace isolation** is broken by role-global RBAC, and dashboards need aggregation. |
| **E-commerce / marketplace** | 🟡 partial | The catalog (variants, category tree, m2m) is excellent; the **commerce core** (checkout atomicity, inventory, order totals, status workflow) lives outside the engine. |
| **Social / content** | 🟡 partial | The graph (follows, threaded comments, posts) is excellent; **the feed, like-counts, public-read+owner-write, and polymorphic likes** are gaps. |
| **Booking / reservations** (Airbnb/Calendly) | 🟡 partial | Listings/bookings/embeds work; the **defining invariant — no double-booking** — is unmeetable (no time-range/overlap), and payments aren't atomic. |
| **Messaging / chat** | 🟡 partial | Conversations/messages/participants/keyset streams work; **participation-scoped access** and **per-conversation realtime** and unread counts are gaps. |
| **Fintech / wallet** | 🔴 no | Ledger/accounts model fine, but **every defining need** — derived balance (SUM), atomic transfer, append-only immutability, idempotency without a 500, exact money — is unmet. Not a system of record for money. |

**The engine is a superb declarative CRUD + relations + RBAC + multi-tenant
core.** It models the *shape* of every archetype. What it consistently cannot do
is the *behavior* that turns a data model into an application: derived values,
aggregations, cross-row/cross-resource invariants, atomic multi-write
transactions, and state-transition rules. Those live in client or worker code
today.

---

## Cross-cutting engine findings (the gap catalog, prioritized)

Each gap below was **observed live** this session. "Blocks N/6" = how many
archetypes it materially blocks.

### ✅ G1 — Hyphenated resource names crash the engine at boot (GraphQL) — **RESOLVED (FIX-G1-G6)**

> **Resolved.** Resource names now match `^[a-z][a-z0-9_]*$` (the same charset as
> field names): `_` is allowed (readable multi-word names like `order_items` that
> are valid GraphQL identifiers and boot end-to-end), and `-` is **rejected at
> `validate`** with a clear message — so `validate` and `serve` now agree (a name
> that validates always boots). The `auth_` prefix is reserved to keep
> underscored names from colliding with the per-tenant auth tables. The original
> finding is preserved below.

The single most surprising finding. Resource names **were** validated against
`^[a-z][a-z0-9-]*$` — **hyphen allowed, underscore forbidden**. GraphQL field
names must match `^[_a-zA-Z][_a-zA-Z0-9]*$` — **underscore allowed, hyphen
forbidden**. The GraphQL builder uses the raw (singularized) resource name as a
query-field name, so any multi-word resource name (`cart-items`, `order-lines`,
`ledger-entries`, `availability-slots`, `post-likes`, `task-tags`, …) makes
`serve` **panic at boot**:

```
panic: appitools/graphql: failed to build schema:
  Names must match /^[_a-zA-Z][_a-zA-Z0-9]*$/ but "cart-item" does not.
```

- `appitools validate` **passes** (it never builds the GraphQL schema).
- The crash is **total** (the whole instance, every tenant — the GraphQL handler
  is built once at boot from the boot schema).
- **5 of the 6** archetypes, written with natural names, hit this. Only `chat`
  (single-word resources) booted as first written. The canonical `examples/erp-demo`
  avoids it only because it happens to use no multi-word resource names.
- **There is no good multi-word name**: hyphen crashes GraphQL, underscore is
  rejected by the schema validator. The only safe form is a concatenated word
  (`cartitems`, `orderlines`). The committed lab schemas use that form.

**Severity: blocks (critical).** Engine fix is small (sanitize GraphQL field
names, or allow `_` in resource names) and `validate` should reject hyphenated
resource names until then. Blocks **5/6** as naturally written.

### 🔴 G2 — RBAC `conditions` is role-global (one condition per role, applied to *all* its resources)

A `RolePolicy` carries a single `conditions`; it is appended as
`AND <field> = $user_id` to **every** resource the role lists. Consequences seen
live:

- **Owner-scoping works when the column is shared** — a `seller` sees only its
  own products, a `host` only its own listings, an `account_holder` only its own
  accounts (verified: returns only the caller's rows; an excluded row reads as
  `404`).
- **But the role cannot also read shared/unowned data.** A `seller` role that
  lists `categories` would inject `AND seller_id = $1` against a table with no
  such column → masked error. So the seller role had to **drop** the shared
  catalog; the storefront must use a *separate* unscoped role. A `seller` reading
  buyer orders "for their products" is simply not expressible.
- **"Public-read + owner-write in one role" is impossible** (social): the
  `member` row condition filters reads too, so a logged-in member sees **only its
  own** posts (verified: member token returns its 1 post, not the other two).
  Social needs a *second* `reader` role for browsing — and a single JWT carries
  one `role` claim, so one user can't be both.
- **"Membership/participation scoping" is impossible** (chat, saas): a chat
  `member` (condition `user_id=$user_id`) sees only messages it **sent**, not the
  conversations it participates in (verified: 200 on own message, 404 on a
  co-participant's message in the same conversation). A saas `member` listing its
  workspace returns `[]` (verified) because the condition forces `user_id` match
  on shared workspace/project/task rows.
- **Workaround = denormalize the condition column onto every touched resource**
  (social put `user_id` on all 7 member-accessible resources, including `media`
  and `postlikes`) — lossy and impossible for genuinely shared rows.

**Severity: blocks.** The single most archetype-shaping limitation. Blocks
clean modeling in **~5/6** (ecommerce, social, saas, chat; booking only survives
because every owned resource happens to share the column). Per-resource
conditions would be the biggest single unlock.

### ✅ G3 — No aggregation anywhere; REST has no total count — **RESOLVED (FIX-G3)**

> **Resolved.** `count`/`sum`/`avg`/`min`/`max` + `group_by` are now served per
> resource — `GET /api/{resource}/aggregate` and `<resource>Aggregate` in GraphQL
> — plus opt-in `?count=true` on lists (adds `meta.total`/`total_pages`, closing
> the REST↔GraphQL asymmetry). Every aggregate is scoped by the SAME RBAC row
> condition + field allowlist + filters as a read: a row-scoped role aggregates
> only its own rows (no totals leak across principals) and a field outside the
> role's allowlist cannot be aggregated (`403`). Functions come from a fixed
> allowlist over schema fields (no arbitrary SQL). This unblocks balances (SUM),
> counts, dashboards and facets in all six archetypes. The original finding is
> preserved below.

No `count` / `sum` / `avg` / `min` / `max` / `group-by` **used to exist** in the API. The REST
list `meta` is `{page, per_page, has_next, has_prev}` — **no total** (verified;
dropped "for performance"). This blocks, in **6/6** archetypes: account balances
(fintech, a SUM of ledger entries), like/comment counts (social), category
facets and revenue dashboards (ecommerce), board/burndown rollups (saas), unread
badges (chat), occupancy (booking).

- **Asymmetry found:** GraphQL `PageMeta` *does* expose `total` and `total_pages`
  and returns a **real count** (verified: a filtered GraphQL `listings` query
  returned `meta:{total:1}`). So a bare count is reachable via GraphQL but not
  REST. There is still no `sum`/`avg`/`group-by` and no aggregate field anywhere
  (verified: `ledgerEntriesAggregate` → "Cannot query field").
- **Workaround:** denormalized counter columns (social ships `like_count`,
  `comment_count`) kept in sync by a worker, or client-side paging.

**Severity: degrades→blocks** (blocks fintech outright). Universal (6/6).

### 🟠 G4 — No multi-resource atomic transaction; no `SELECT FOR UPDATE` / optimistic-lock version

Every write is its own transaction. There is no declarative "write order + N
lines + decrement stock, all-or-nothing", and no row-locking / version column.
Observed consequences:

- **Checkout is not atomic** (ecommerce): order, each order-line and each stock
  decrement are independent writes; a partial checkout or an oversell race is
  possible. `min:0` rejects a *negative literal* (verified `422`) but is **not** a
  concurrency-safe `stock = stock - qty WHERE stock >= qty`.
- **Transfers are not atomic** (fintech): the two ledger legs are independent
  inserts; a crash between them leaves money created/destroyed.

**Severity: blocks** where money/inventory invariants matter (ecommerce, fintech;
booking payments). **Workaround:** a custom Go handler on the library surface
(`appitools.New` + `Ctx.Tx`) or a single-consumer outbox worker — i.e. write code.

### 🟠 G5 — No state-machine / status-transition enforcement

An `enum` permits any member → any member. There is no "allowed transitions"
rule, and update hooks see only the new body. Verified illegal jumps accepted:
order `pending → delivered` (skips paid/shipped, `200`); a fintech entry's status
freely changed; a booking status freely changed. Status is a free label the
client is trusted to advance correctly. **Severity: degrades.** Affects **5/6**
(ecommerce, booking, fintech, saas, chat).

### ✅ G6 — Unique-constraint violations return `500 internal error`, not `409` — **RESOLVED (FIX-G1-G6)**

> **Resolved.** The create path (REST `POST` + GraphQL `createX`) now maps a
> Postgres `unique_violation` (SQLSTATE 23505) to **`409 Conflict`** with a
> consumable `field "<field>": value already exists` message (the raw DB error is
> never exposed), mirroring what the update path already did. So a duplicate
> idempotency key / handle / SKU / one-per-user guard is now a designed conflict,
> not a server error. The original finding is preserved below.

The schema-derived CRUD path **used to** not map Postgres `unique_violation`. Both a
field `unique:true` collision and a composite `indexes`-`unique` collision return
**`500 {"error":"internal error"}`** (verified twice each: ecommerce review,
social duplicate-follow, chat participant/reaction, fintech idempotency-key +
ledger-key, booking amenity-code + slot index). This breaks every "do it once"
guard — idempotency keys, one-like-per-user, one-review-per-buyer, unique
handles/SKUs/emails — turning an expected conflict into a server error. (The auth
signup path *does* return `409`, but that is a separate hand-written handler, not
the generated CRUD path.) **Severity: degrades** (high annoyance, easy fix —
mirror the auth handler's `unique_violation`→`409` mapping). Affects **~5/6**.

### 🟡 G7 — No computed/derived fields; no cross-field or cross-resource validation; no FK integrity

Order totals, account balances, like counts are plain columns the client sends.

- A `before_create` JS hook can enforce **intra-row** arithmetic (verified:
  ecommerce `total == subtotal + shipping + tax` → `422`) — but it sees only the
  row's own body, never another resource, so it cannot verify `subtotal == Σ
  order-lines` (lines are a separate resource written afterward).
- No FK constraint and no cross-resource validation: a customer can attach a
  cart-item to **another buyer's** `cart_id` (the engine forces `customer_id` to
  the caller but never checks the referenced cart's owner). Verified the engine
  has no surface to express it.

**Severity: degrades.** Affects ecommerce, social, saas, fintech.

### 🟡 G8 — No time-range / interval type, no overlap/exclusion constraint

There is no range type and no exclusion constraint, so **double-booking is not
preventable** (verified: two bookings, same listing+slot, identical overlapping
dates → both `201`). "Find free slots in a window" needs client logic over plain
`gte`/`lte` time bounds. **Severity: blocks** the defining invariant of booking;
minor elsewhere.

### 🟡 G9 — `uuid` and `bool` fields are not filterable in GraphQL (REST allows `uuid eq`)

GraphQL filter inputs exist only for string/text (`{exact,partial,start}`), time
(`{after,before,gte,lte}`) and numeric (`{gte,lte}`). `uuid`/`bool` get no filter
input, so a GraphQL list **cannot be filtered by a foreign key** (verified:
`filter:{host_id:{exact:…}}` → "Unknown field"). You must use the nested relation
embed instead. REST does allow `filter[fk][eq]`. **Severity: minor**
(REST↔GraphQL asymmetry). Affects all archetypes' GraphQL FK filtering.

### 🟡 G10 — No money/decimal type (float64 only), no array, no geo, no date-only

`float64` is the only numeric — money is stored lossily in floating point (every
archetype handling money: ecommerce, booking, fintech). No array type (tags,
amenities modeled as m2m resources or `json`), no geo/point (booking stores
`latitude`/`longitude` as two `float64`, can't do nearest-N/within-radius), no
date-only (everything is `timestamptz`). **Severity: degrades** for fintech
(exactness), minor elsewhere.

### 🟡 G11 — No polymorphic relations; no full-text search; no built-in soft delete; self-ref limited to include-depth 2

- **Polymorphic**: a "like" on a post *or* a comment can't be one resource; social
  split it into `postlikes` + `commentlikes` (chat similar for reactions).
- **Search** is `ILIKE` substring over string/text only — not ranked full-text
  (fine for small sets, not a search engine).
- **Soft delete**: you can add a `bool`/`time` column but the engine won't
  auto-exclude it; every read must filter by hand.
- **Self-referencing** trees embed fine but only to `?include` **depth 2**
  (verified: a category breadcrumb beyond depth 2 → `400 "include nesting exceeds
  max depth 2"`), so an arbitrary-depth ancestor path needs N calls.

**Severity: minor / by-design.**

---

## What the engine already models *well* (the strengths, verified)

These carried every archetype's "shape" cleanly and are the foundation to build on:

- **Relational graph in one declarative file, no N+1.** `has_many`,
  `belongs_to`, **and `many_to_many`** embed nested in a single `LATERAL` +
  `json_agg` round-trip on opt-in `?include=` (REST) and as nested GraphQL fields.
  Verified across all six: a product with variants + reviews + m2m categories in
  one call; an order with its lines; a conversation with members + messages +
  reactions (depth-2).
- **Self-referencing relations work first-class.** A category tree
  (`categories → categories`), threaded comments (`comments → comments`), and a
  follow graph (`users ↔ users` m2m) all embed correctly — the engine explicitly
  allows a relation to target its own resource.
- **Declarative validation is rich and shared by REST + GraphQL.**
  `required`/`enum`/`min`/`max`/`minLength`/`maxLength`/`pattern`/`format`,
  returning `422` with **every** failing field at once. Verified (e.g. enum
  rejects a bad message kind; pattern rejects a bad amenity code).
- **RBAC is genuinely enforced, including on create.** Deny-by-default (`403`),
  field allowlists (verified: an `auditor` never sees `memo`/`metadata`),
  row-scoping (owner sees only own; excluded row → `404`), and a real
  **create-time mass-assignment block** — forcing the condition column to the
  caller and rejecting a foreign value with `403` (verified in ecommerce, social,
  chat, fintech). This is the standout feature.
- **Automatic audit timestamps.** An `auto` `created_at` is set on insert; an
  `auto` `updated_at` **re-stamps on every PATCH** (verified: changed on update,
  `created_at` did not).
- **Lifecycle JS hooks** enforce intra-row business rules synchronously (verified:
  chat "group conversations require a title" / "non-empty body" → `422`).
- **Keyset pagination, single-field sort, typed filters, substring search**, and
  the **opt-in outbox events** (the async seam where the logic the engine can't do
  synchronously belongs) all work as documented.
- **Schema-per-tenant isolation** held throughout — every archetype ran as its own
  tenant with its own Postgres schema.

---

## Per-archetype detail

Schemas: [ecommerce](../examples/model-lab/ecommerce.json) ·
[social](../examples/model-lab/social.json) ·
[saas](../examples/model-lab/saas.json) ·
[booking](../examples/model-lab/booking.json) ·
[chat](../examples/model-lab/chat.json) ·
[fintech](../examples/model-lab/fintech.json).

### SaaS / productivity — 🟡 partial (the best fit)
**Models well:** workspace → project → list → task → comment nesting (depth-2
embeds), task↔tag m2m, assignee `belongs_to`, threaded comments (self-ref),
status filter, priority+due sort, search. Closest to a "yes" because it is
CRUD-with-clear-ownership.
**Breaks:** per-workspace isolation is impossible with role-global conditions (a
`member` listing its workspace returns `[]`); no aggregation for board/dashboard
rollups; status workflow unenforced. Gaps: G2, G3, G5.

### E-commerce / marketplace — 🟡 partial
**Models well:** sellers, products, SKU-level variants (composite-unique
`(product_id,color,size)`), a **self-referencing category tree**, product↔category
m2m, carts/orders/lines/reviews; rich validation (slug/SKU/order-number patterns);
owner-scoped `customer` role; the storefront nested read in one round-trip.
**Breaks:** order totals are client-supplied (G7); checkout + inventory decrement
are non-atomic and racy (G4); status workflow unenforced (G5); a seller can't read
the shared catalog through its row-scoped role (G2); no facets/revenue
aggregation (G3); one-review-per-buyer collision is a `500` (G6).

### Social / content — 🟡 partial
**Models well:** profiles, posts, **threaded comments (self-ref)**, **follows as a
self-referencing user↔user m2m** (both directions embed), media. The graph is the
strength.
**Breaks:** "home feed = posts by people I follow" is not a single query (no join
across the follow set); like/comment counts need denormalized counters (G3);
"public-read + owner-write" can't be one role (G2); a "like on a post *or* a
comment" needs two resources (G11, polymorphic); duplicate-follow collision is a
`500` (G6).

### Booking / reservations — 🟡 partial
**Models well:** hosts, listings, availability slots, bookings, payments, reviews,
amenities m2m; host/guest row-scoping; public catalog with a field allowlist;
nested embeds; date-window filtering with `gte`/`lte`.
**Breaks:** **double-booking is not preventable** — the defining invariant (G8,
verified two overlapping bookings both `201`); "free slots with remaining capacity"
needs client logic (G3, no aggregation); payment + booking aren't atomic (G4);
status workflow unenforced (G5); slot/idempotency collisions are `500` (G6).

### Messaging / chat — 🟡 partial
**Models well:** users, conversations, participants (m2m), messages with a sorted
**keyset-paginated stream**, read receipts, reactions, attachments; per-resource
SSE exists; JS hooks enforce message rules.
**Breaks:** participation-scoped reading is impossible — a `member` sees only
messages it *sent*, not its conversations (G2); SSE is per-resource, **not
per-conversation** (no server-side conversation filter on the stream); unread
counts need aggregation (G3); reaction/participant uniqueness collisions are `500`
(G6).

### Fintech / wallet — 🔴 no
**Models well (shape only):** accounts, immutable-intent ledger entries, transfers,
holds, idempotency keys, audit events; owner-scoped `account_holder`; auditor field
allowlist; ledger embeds.
**Breaks (the defining needs):** account **balance** is a SUM the API cannot
compute (G3, verified no aggregate field); a **transfer's two legs are not atomic**
(G4); **append-only/immutability is not enforced** — a posted entry can be PATCHed
(G5/G7, verified `200`); **idempotency keys collide into a `500`** instead of a
clean conflict (G6); **money is `float64`**, not exact decimal (G10). The engine
cannot be the system of record for money. A wallet would have to run all
money-movement in custom transactional handlers, leaving the engine as a
read/projection layer.

---

## Recommendation — what to close first (impact × tractability)

Ordered to unlock the most modern apps per unit of engine work:

1. ~~**G1 — Fix the hyphen→GraphQL boot panic**~~ ✅ **DONE (FIX-G1-G6).** Resource
   names now allow `_` (and reject `-`) so they are valid GraphQL identifiers;
   `validate` rejects what `serve` can't build. `auth_` reserved.
2. ~~**G6 — Map `unique_violation` → `409`**~~ ✅ **DONE (FIX-G1-G6).** The create
   path (REST + GraphQL) now returns a clean `409` on a unique collision, matching
   the update path.
3. ~~**G3 — An aggregation surface**~~ ✅ **DONE (FIX-G3).** `count`/`sum`/`avg`/
   `min`/`max` + `group_by` on REST + GraphQL, RBAC-scoped and filter-aware, plus
   opt-in list `?count=true`. Unblocks counts/balances/dashboards in **6/6**.
4. **G2 — Per-resource RBAC conditions** *(medium-large).* The biggest *modeling*
   unlock: workspace/participation scoping and public-read+owner-write become
   expressible. Turns several 🟡 into near-🟢.
5. **G4 — A declarative multi-resource atomic write (or a documented `Ctx.Tx`
   recipe)** *(large).* Unblocks the commerce/finance core (checkout, transfer).
6. **G5 — Status-transition enforcement** *(medium).* Allowed-transitions per enum
   field; unblocks order/booking/payment workflows in 5/6.

Then, as depth: **computed/derived fields** (totals, counts, balances),
**time-range type + overlap exclusion** (booking), a **decimal/money type**
(fintech exactness), **polymorphic relations**, and **full-text search**.

**Bottom line for the AI layer:** the engine is already a strong target for "lay
out the data, relations, validation, RBAC, and multi-tenant isolation of an app."
G1/G6 are closed (robust examples + idempotency/dedup), and **G3 is closed**
(aggregation — counts, balances, dashboards now work on both surfaces). The
remaining big modeling unlock is **G2** (the role-global condition →
per-resource conditions), which is what turns workspace/participation-scoped
archetypes from "partial" toward "yes" — and only with the engine able to model
these patterns can an AI reliably *generate* the variety of apps people expect.

---

## Appendix A — reproduce

Each archetype was tested with a generic driver (registers a fresh tenant, mints a
per-role token, runs an ordered populate recipe with id-capture, then the query
plan). To re-run one:

```bash
# boot the engine with the archetype schema, then drive it
set -a; source /root/.appitools-secrets-dev; set +a
./appitools-dev serve --schema examples/model-lab/<archetype>.json --port 8080 &
# register tenant + populate + query (driver + per-archetype plan are session scratch)
```

The committed schemas use **concatenated** resource names (`cartitems`, not
`cart-items`) to avoid G1; the original multi-word names are the evidence for that
finding. All six **validate** (`appitools validate examples/model-lab/<a>.json`)
and **boot**.
