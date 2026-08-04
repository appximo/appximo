# Model Lab — which modern app archetypes can the engine model today?

> **Diagnostic, not a change to the engine.** This maps what the Appximo schema
> can and cannot express for the data patterns behind the apps people actually
> build today, with **live evidence** (schemas that validate, tenants registered,
> rows populated, real queries run). It is the map that defines what the engine
> needs **before** the visual editor / AI layer — because the AI will only be able
> to generate what the engine can model.
>
> Engine version under test: `0c29240` (API-PRODUCTIVA-V1). Date: 2026-06-16.
> Method, per archetype: design a serious schema → `appximo validate` →
> `serve` with that schema → register a tenant → populate rows → run the
> domain-critical REST + GraphQL queries → record what worked and what didn't.
> The six schemas live in [`examples/model-lab/`](../examples/model-lab/); the
> reproduction harness is in [Appendix A](#appendix-a--reproduce).

## TL;DR

| Archetype | Verdict | One-line |
|---|---|---|
| **SaaS / productivity** (Notion/Trello/Linear) | 🟢 yes | Nesting, tags, assignees, threads model cleanly; **owner/workspace isolation** (per-resource RBAC, G2), dashboards (aggregation, G3), and **task-status workflows** (state machine, G5) all work. A non-id workspace **claim variable** is the only remaining scoping nicety. |
| **E-commerce / marketplace** | 🟢 mostly yes | The catalog (variants, category tree, m2m) is excellent; **checkout is atomic** (order + lines + guarded stock decrement, G4) and the **order lifecycle is enforced** (pending→paid→shipped→delivered, no illegal jumps, G5). The only remaining gap is order **totals** as a computed field (G7). |
| **Social / content** | 🟢 mostly yes | The graph (follows, threaded comments, posts) is excellent; **public-read + owner-write now works in ONE role** (per-resource RBAC + `condition_actions`, G2) and counts have aggregation (G3). The feed-join and polymorphic likes (G11) remain. |
| **Booking / reservations** (Airbnb/Calendly) | 🟡 partial | Listings/bookings/embeds work, payments are atomic (G4), and the **reservation lifecycle is enforced** (pending→confirmed→cancelled, G5); but the **defining invariant — no double-booking** — is still unmeetable (no time-range/overlap exclusion, G8). |
| **Messaging / chat** | 🟡 partial (improved) | Conversations/messages/participants/keyset streams work; **per-resource scoping landed (G2)** so each resource carries its own condition, but **membership-by-subquery** (messages of conversations I'm in) still needs the participant denormalized onto the row; per-conversation realtime remains (G3 covers unread counts). |
| **Fintech / wallet** | 🟡 partial (close) | **Derived balance** (SUM, G3), **atomic transfer** (G4), **idempotency keys** (clean `409`, G6), and **append-only immutability** (a `posted` ledger entry is a terminal state — frozen, G5) all work now. The one remaining gap for a true ledger is **exact decimal money** (G10 — `float64` only). |

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
panic: appximo/graphql: failed to build schema:
  Names must match /^[_a-zA-Z][_a-zA-Z0-9]*$/ but "cart-item" does not.
```

- `appximo validate` **passes** (it never builds the GraphQL schema).
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

### ✅ G2 — RBAC `conditions` is role-global (one condition per role, applied to *all* its resources) — **RESOLVED (FIX-G2)**

> **Resolved.** A role may now declare a **`permissions` map** (resource → its own
> `actions` + `conditions` + `fields`) instead of the role-global keys, so each
> resource is scoped by its **OWN** column (`projects.owner_id`,
> `documents.created_by`) — and `condition_actions` scopes a condition to a subset
> of the actions, expressing **"read all, write own"** in one role. A resource with
> no condition is unscoped; a literal `val` (e.g. `"published"`) gives public-read
> of a row subset; a per-resource `fields` allowlist scopes the response per
> resource. Every operation that already honored conditions
> (read/create/update/delete/aggregate, REST + GraphQL) uses the **correct
> resource's** condition — verified live with a no-leak-between-resources test. The
> condition `field` is validated to **exist on that resource** at load (no masked
> `500`). Backward-compatible: the role-global form is unchanged (mutually
> exclusive with `permissions`), and the ERP demo behaves identically; measured
> `no_change` on the RBAC hot path. This is the **biggest modeling unlock** —
> workspace/owner scoping and public-read+owner-write become expressible.
>
> **Two pieces remain for the fullest scoping** (smaller, separate follow-ups, NOT
> G2): (a) a condition value that resolves an arbitrary **JWT claim** (e.g.
> `$workspace_id`) — today `val` resolves `$user_id`/`$external_client_id`/literal,
> so workspace isolation works when the scoping value is the caller's id or a
> literal; (b) **membership/subquery** conditions (chat: "messages in conversations
> I participate in") — the condition is a single `field op val`, not a subquery, so
> participation that needs a join still requires denormalizing the participant onto
> the row. The original finding is preserved below.

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

### ✅ G4 — No multi-resource atomic transaction; no `SELECT FOR UPDATE` / optimistic-lock version — **RESOLVED (FIX-G4)**

> **Resolved.** `POST /api/transaction` runs N create/update/delete operations
> across resources in **one Postgres transaction** — all-or-nothing. Any failure
> (validation, RBAC, a constraint, a guard, a not-found) rolls the WHOLE batch back:
> zero partial state (verified live — a transfer's second leg colliding on a unique
> ref reverts the first leg; an oversell whose stock guard fails creates no order).
> Every op is authorized (per-resource RBAC, G2 — its own condition + field
> allowlist + create mass-assignment block) and validated exactly like its single-op
> counterpart, and outbox events emit in the SAME tx. An optimistic-lock **`guard`**
> (compare-and-set: `eq|ne|gt|gte|lt|lte` predicates the row must satisfy) makes
> conditional/race-safe writes expressible — the locking tool. The single-op write
> path is untouched (measured `no_change`); a 2-op transaction measured ≈ 6 ms p50.
> This unblocks the **commerce/finance core**: a transfer's two legs are now atomic,
> and a checkout's order + lines + stock decrement are all-or-nothing.
>
> **Remaining** (smaller follow-ups, NOT G4): pessimistic `SELECT … FOR UPDATE` and
> in-place arithmetic (`stock = stock - n`) — today the compare-and-set `guard`
> covers race-safety with a client read; a declarative "operation in the schema"
> (Option A — a named `transfer` the engine composes) can be built ON this generic
> primitive. The original finding is preserved below.

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
(`appximo.New` + `Ctx.Tx`) or a single-consumer outbox worker — i.e. write code.

### ✅ G5 — No state-machine / status-transition enforcement — **RESOLVED (FIX-G5)**

> **Resolved.** A string status field declares a **`state_machine`** (`initial`
> state(s) + per-state `transitions`); the engine FORCES the lifecycle: a row may
> only be created in an initial state (an advanced state at create → `422`), and an
> update may only move along a DECLARED transition (an undeclared jump → `422`
> "invalid transition from X to Y"). A state with no outgoing transitions (`[]`) is
> **terminal — immutable** (it can never change to another state), which gives the
> fintech **append-only** intent (a posted ledger entry's status is frozen). It is
> enforced **race-safely inside the UPDATE's WHERE** (the move depends on the row's
> CURRENT state, so two concurrent updates can't both advance it — no
> read-modify-write window), on REST, GraphQL, and inside a `POST /api/transaction`
> (a batch op that violates a transition fails the whole tx). Validated at load
> (string field, coherent with `enum`, default an initial state). A field without a
> state machine is a free string (measured `no_change`). The original finding is
> preserved below.

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

### SaaS / productivity — 🟢 yes (the best fit)
**Models well:** workspace → project → list → task → comment nesting (depth-2
embeds), task↔tag m2m, assignee `belongs_to`, threaded comments (self-ref),
status filter, priority+due sort, search. Closest to a "yes" because it is
CRUD-with-clear-ownership.
**Now works:** owner-scoped per-resource RBAC — each resource scoped by its own
column, some resources shared, "read all / write own" — is expressible (G2 ✅);
board/dashboard rollups have aggregation (G3 ✅); task-status **workflows are
enforced** (todo→doing→done, state machine, G5 ✅). **Remaining:** scoping by a
non-id workspace claim needs a `$workspace_id`-style variable (minor).

### E-commerce / marketplace — 🟢 mostly yes
**Models well:** sellers, products, SKU-level variants (composite-unique
`(product_id,color,size)`), a **self-referencing category tree**, product↔category
m2m, carts/orders/lines/reviews; rich validation (slug/SKU/order-number patterns);
owner-scoped `customer` role; the storefront nested read in one round-trip.
**Now works:** **checkout is atomic** — order + lines + a guarded stock decrement
in one `POST /api/transaction`, all-or-nothing, with a compare-and-set guard that
prevents oversell (G4 ✅); the **order lifecycle is enforced** — pending→paid→
shipped→delivered, no illegal jumps (state machine, G5 ✅); a seller can own-scope
its products while reading the shared catalog (per-resource RBAC, G2 ✅);
facets/revenue have aggregation (G3 ✅); one-review-per-buyer collision is a clean
`409` (G6 ✅). **Remaining:** order totals as a computed field (G7).

### Social / content — 🟢 mostly yes
**Models well:** profiles, posts, **threaded comments (self-ref)**, **follows as a
self-referencing user↔user m2m** (both directions embed), media. The graph is the
strength.
**Now works:** "public-read + owner-write" is one role — a member reads all posts
but edits/deletes only its own via per-resource `condition_actions` (G2 ✅);
like/comment counts have aggregation (G3 ✅); duplicate-follow collision is now a
clean `409` (G6 ✅). **Remaining:** "home feed = posts by people I follow" is not a
single query (no join across the follow set); a "like on a post *or* a comment"
needs two resources (G11, polymorphic).

### Booking / reservations — 🟡 partial
**Models well:** hosts, listings, availability slots, bookings, payments, reviews,
amenities m2m; host/guest row-scoping; public catalog with a field allowlist;
nested embeds; date-window filtering with `gte`/`lte`.
**Now works:** payment + booking are atomic (G4 ✅); the **reservation lifecycle is
enforced** — pending→confirmed→cancelled, no illegal jumps (state machine, G5 ✅);
occupancy via aggregation (G3 ✅); slot/idempotency collisions are a clean `409`
(G6 ✅). **Still breaks:** **double-booking is not preventable** — the defining
invariant (G8, verified two overlapping bookings both `201`; no time-range/overlap
exclusion).

### Messaging / chat — 🟡 partial (improved)
**Models well:** users, conversations, participants (m2m), messages with a sorted
**keyset-paginated stream**, read receipts, reactions, attachments; per-resource
SSE exists; JS hooks enforce message rules.
**Improved:** per-resource RBAC (G2 ✅) lets each resource carry its own condition
column, and unread counts have aggregation (G3 ✅); reaction/participant uniqueness
collisions are now `409` (G6 ✅). **Still breaks:** true participation scoping
("messages of conversations I'm in") is a **subquery/membership** test, not a single
`field op val` — so it works only by denormalizing the participant onto each row (a
`member` then sees its own messages, not co-participants'); SSE is per-resource,
**not per-conversation** (no server-side conversation filter on the stream).

### Fintech / wallet — 🟡 partial (close)
**Models well:** accounts, immutable-intent ledger entries, transfers, holds,
idempotency keys, audit events; owner-scoped `account_holder`; auditor field
allowlist; ledger embeds.
**Now works (the defining needs):** account **balance** is a `SUM` the API computes
(G3 ✅); a **transfer's two legs are atomic** — debit + credit in one
`POST /api/transaction`, both-or-neither (G4 ✅, verified the first leg reverts when
the second collides); **idempotency keys** are a clean `409`, not a `500` (a unique
`ref`, G6 ✅); **append-only immutability** is enforced — a `posted` ledger entry is
a TERMINAL state in its `state_machine`, so its status can never change again (G5 ✅,
verified posted→void/pending both `422`). **Remaining for a true ledger:** **money is
`float64`**, not exact decimal (G10). That one type is now the only thing between the
engine and being a system of record for money — the movement is atomic, the balance
derivable, and posted entries frozen.

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
4. ~~**G2 — Per-resource RBAC conditions**~~ ✅ **DONE (FIX-G2).** A `permissions`
   map gives each resource its own condition/actions/fields, with
   `condition_actions` for read-all/write-own. Workspace/owner scoping and
   public-read+owner-write are now expressible — SaaS and Social move to 🟢. The
   remaining scoping pieces (a `$workspace_id`-style **claim variable** and
   **membership/subquery** conditions for chat) are smaller, separate follow-ups.
5. ~~**G4 — A declarative multi-resource atomic write**~~ ✅ **DONE (FIX-G4).**
   `POST /api/transaction` runs N ops in ONE transaction, all-or-nothing, per-op
   RBAC + validation + outbox, with a compare-and-set `guard` for race-safety.
   Unblocks the commerce/finance core (checkout, transfer). The last remaining big
   gap is G5.
6. ~~**G5 — Status-transition enforcement**~~ ✅ **DONE (FIX-G5).** A field's
   `state_machine` (initial + transitions) forces the lifecycle on create and update,
   race-safely, with terminal states immutable — unblocks order/booking/payment
   workflows and append-only ledgers. **All six high-impact gaps (G1–G6) are now
   closed.**

Then, as depth (none blocking for most apps): **computed/derived fields** (totals,
counts, balances), **time-range type + overlap exclusion** (booking double-booking,
G8), a **decimal/money type** (fintech exactness, G10), **polymorphic relations**,
and **full-text search**.

**Bottom line for the AI layer:** **all six highest-impact gaps (G1–G6) are now
closed** — G1 (robust names), G6 (idempotency/dedup `409`), G3 (aggregation —
counts, balances, dashboards), G2 (per-resource RBAC — workspace/owner scoping,
public-read+owner-write), G4 (atomic multi-resource transactions — transfer,
checkout), and **G5 (state-machine transitions — order/booking/payment lifecycles
forced, terminal states immutable for append-only ledgers)**. The engine now models
all three layers of an app — **shape** (resources, relations, types), **permissions**
(per-resource RBAC, multi-tenant isolation), AND **behavior** (validation, derived
aggregates, atomic multi-write invariants, state-transition rules). SaaS and Social
are 🟢 yes; e-commerce is 🟢; fintech and booking are one *type/constraint* away from
yes (exact-decimal money G10; time-range overlap G8). The remaining items are
**depth, not blockers** for most apps: computed/derived fields (G7), a decimal/money
type + array/geo (G10), time-range exclusion (G8), polymorphic relations and
full-text search (G11), and the smaller scoping follow-ups under G2 (a
`$workspace_id`-style claim variable; membership/subquery conditions). With shape +
permissions + behavior all expressible, an AI layer can now reliably *generate* the
variety of real apps people expect — which is the next frontier (schema-for-AI +
the visual editor).

---

## Appendix A — reproduce

Each archetype was tested with a generic driver (registers a fresh tenant, mints a
per-role token, runs an ordered populate recipe with id-capture, then the query
plan). To re-run one:

```bash
# boot the engine with the archetype schema, then drive it
set -a; source .env.dev; set +a
./appximo-dev serve --schema examples/model-lab/<archetype>.json --port 8080 &
# register tenant + populate + query (driver + per-archetype plan are session scratch)
```

The committed schemas use **concatenated** resource names (`cartitems`, not
`cart-items`) to avoid G1; the original multi-word names are the evidence for that
finding. All six **validate** (`appximo validate examples/model-lab/<a>.json`)
and **boot**.
