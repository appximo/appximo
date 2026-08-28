# ADR-029 — Field selection (`?fields=`) is pushed down to the SQL `SELECT`, on every row-returning read; heavy fields are NOT omitted by default

**Status:** accepted (MOTOR-FIELDS-S1, 2026-08-28)
**Drivers:** finding #5 of a real migration's report (MIG-FRONT, Symfony →
v0.1.10, 46,119 tax declarations with the form's data in one `json`
column): `GET /api/declarations` returned the whole `data` column of every
row although it paginated — **~940 KB per page of 20, p99 3,821 ms** — for a
list that shows a NIT, a year and a status. It is the first screen any client
of a migrated system sees. The report asked for `?fields=` "or excluding large
fields from collections by default".

## Context

The engine's list statement was `SELECT * FROM <table> … LIMIT $1 OFFSET $2`
on every read door: REST list/get/subroute, the `?include=` wrappers (whose
root `json_build_object` named every column), the admin browse, `ctx.Query`
— and GraphQL, which selected fields in Go over the same `SELECT *`. The
role's field allowlist was applied AFTER the read (`FilterFields`).

Where the time goes: a `json`/`jsonb`/`text` value past ~2 KB is stored out
of line (TOAST, compressed). A `SELECT *` that is sent to a client detoasts
and decompresses it for every row — the server's output function must — so
the cost of a 50 KB document is paid per row per page whether or not anyone
reads it. Trimming columns in Go after the read would cut the bytes on the
wire and none of that: **the disk read happens before Go sees the row.**
Measured on the rebuilt case (46,119 rows, ~52 KB each, dev box): a page of
20 is 1.4 MB, ~190 TOAST blocks and ~100 ms of query stage; the same page
projected to four columns is 1.8 KB, 0 TOAST blocks, ~4 ms.

Two possible answers:

1. **A request-time projection** — the caller names the columns; the engine
   puts them in the `SELECT` list. Opt-in per request; the default statement
   is untouched.
2. **A schema-time exclusion** — heavy fields (by type, or declared) are
   omitted from collections unless asked for.

## Decision

**1. `?fields=a,b,c` — one engine-owned parameter, and the projection reaches
the SQL.** `SELECT "id", "a", "b", "c" FROM …`. Not a post-read trim, ever:
the whole reason is the disk. The same validated column list is used on every
door that returns rows of ONE resource — list (page and cursor), get-by-id,
the relation subroute (fields of the TARGET), the ROOT of an `?include=` read
(the base subquery carries the requested fields plus the FK/order columns the
wrapper's joins need — `includeBuilder.baseRefs` — and the root object names
the requested fields only; embeds are never projected: there is no nested
syntax), the admin data browse and `ctx.Query` (`QueryOpts.Fields`).
**GraphQL pushes its selection set into the SQL automatically** — its
resolvers used to run `SELECT *` and select in Go, so `data { id nit }` read
the TOAST exactly like the REST list; the walker turns the selection into the
projection, drops hidden fields (they still resolve `null`, the unchanged
contract) and falls back to `SELECT *` for any selection it cannot vouch for.
Not on aggregates (no rows; the endpoint owns its namespace — a named 400),
SSE (a push of the changed row) or writes.

**2. The rules are the engine's existing rules for a NAMED field, re-applied
— not new ones.** `id` always comes back (without it references, cursors and
every generic client break). An unknown name is a **400 naming it and listing
the available set**, exactly like `?sort=ghost` — never silently dropped,
which would hand the caller a page missing a column under a 200. A name the
role's allowlist hides is **omitted** — the allowlist wins, as on every read
(RBAC-2 registers that hidden-attempt contract). It is deliberately NOT the
`403` of `?filter[hidden]=`/`?sort=hidden`: that defense exists against a
VALUE oracle (a hidden column revealed by match/no-match), and a projection
reveals nothing; a 403 would also break every role-agnostic client — the
contract does not publish allowlists, so the embedded `/app` cannot know
which of its columns a role may see before asking (the first cut WAS a 403
and the browser pass on a scoped role broke on it). `fields=` can narrow a
role's view and never widen it. `?fields=` empty, an empty entry, a repeated parameter — named 400s
(ENG-30/24/17). A repeated NAME is a set. The universe is the tenant's
deployed surface (a hot-migrated column is selectable). It is a **400 and not
a 422** on purpose: in this engine `422 validation_failed` is the write-body
contract; every query-parameter error is a 400 with a message — one client
parser per class.

**3. Heavy fields are NOT omitted from collections by default — proposed as
a declaration, not built.** Option 2 as a flipped default is a CONTRACT
BREAK: every client that lists a resource and reads `row.data` would get
`undefined` after an upgrade, silently — the exact class ADR-024 forbids.
And "heavy" is not a type the engine can decide (`text` can be 3 bytes or
3 MB; a `jsonb` of attrs is what a catalogue list SHOWS). If a second app
asks for it, the path is a per-field declaration
(`"data": {"type":"json","list":"on_request"}`) that lists/subroutes honour,
the detail keeps, `/openapi.json` publishes (`x-appximo-list`) and the author
adopts at a version of their choosing — registered as SCHEMA-8. `?fields=`
is the half a client can adopt without touching the schema.

**4. The `/app` asks for what it paints.** Every list/board/CSV/label request
of the embedded back-office carries `?fields=` with its visible columns (plus
the state field and the title candidates); a row that must be whole (the
form, the detail) is re-fetched by id first. The footer's «consulta N ms»
(Server-Timing) is where the gain shows.

## Consequences

- Without `fields=` the SQL is byte-identical to before (the binary-diff gate
  reports diffs only on the new parameter; the frozen ABBA on the plain list
  is `no_change`). The plain path pays one map lookup for the parameter.
- The include wrappers take a `BaseSelect` callback instead of a base string,
  so the base subquery can be projected to fields ∪ join/order columns; the
  old string-taking functions remain as wrappers (nil projection).
- `/openapi.json` publishes the `fields` parameter on list, get and subroute
  operations with the available names; the contract is discoverable.
- The proof is pinned by `pkg/integration/fields_test.go`: the SQL the engine
  emitted (pgx tracer), the TOAST blocks that SQL reads with its rows consumed
  (`pg_statio_user_tables`, forced flush in the same transaction — an `EXPLAIN
  ANALYZE` cannot show this, it discards the rows and detoasts nothing), and
  the bytes on the wire. Numbers: docs/BENCHMARKS.md §Field selection.
- What this does NOT fix: the OFFSET cost of deep pages and the `COUNT(*)` are
  not the document; a client that keeps sending `SELECT *`-equivalent
  requests (no `fields=`) keeps paying — the parameter is opt-in by design.
