# Unrecognized-input audit — where the engine accepts something it does not understand

**Session:** SILENT-FAILURE-S1 (2026-08-01), Part A.
**Trigger:** ENG-14 — `?filter[x][is_null]=true` answered `200` with the full,
unfiltered list. Fixing the instance is a one-line regex change; the point of this
audit is the **class**.

**The class:** the engine inspects an input, does not recognize it, and continues
as if it had not been sent. It has now produced four production defects in four
different layers — the migration executor (ENG-13), the RBAC loader
(SEC-AUDIT-V1), the write path (ENG-12) and the query builder (ENG-14) — and each
was found by accident.

**The rule this audit applies** (now written down as
[ADR-024](../adr/ADR-024-unrecognized-input.md)): an input the engine *inspects*
and does not recognize must be rejected with a message that names it and lists the
alternatives. Tolerance is a legitimate choice; **silent** tolerance is not.

**Method.** Ten input surfaces swept in parallel by independent readers of the
source, each finding required to quote the dropping code and supply a runnable
probe; every SILENT claim then put through an adversarial verifier whose job was to
refute it. The findings that drove a code change in this session were additionally
**re-verified by hand against a running engine** — the same discipline the
migration honesty audit used: the verdict comes from the running system, not from
the reading.

---

## What is already right (the model to generalize from)

Three surfaces came back clean, and they are why the policy is stated the way it is:

- **Schema strict keys — the claim holds.** All **17 levels** of the grammar were
  enumerated and probed with an unknown key at each, through all three validation
  surfaces (`validate`, `validate-schema`, `validate --json`). Every level rejects,
  and both mechanisms (`CheckUnknownKeys` and the meta-schema's
  `additionalProperties:false`) agree. The two free-form levels
  (`state_machine.transitions` keys, `workflows.*.steps[].config`) are genuine user
  namespaces and are cross-checked where a check exists.
- **GraphQL rejects unknown fields, arguments and input keys** — spec behavior,
  confirmed live: `Cannot query field "ghostfield"`, `Unknown argument "ghostarg"`,
  and an unknown key inside a mutation `input` is a validation error.
- **The aggregate endpoint enforces the role's field allowlist** (`403`) and
  rejects unknown fields in `sum`/`group_by` — the list endpoint does not, which is
  finding **A-1** below.

The shape they share: **reject, name the input, list the alternatives.**

---

## Fixed in this session

### F-1 — A filter parameter that does not parse is dropped (ENG-14, and wider than reported)
`filterParamRe` decided what was VALID (`[a-z][a-z0-9_]*` for the field, `[a-z]+`
for the op) instead of what was a FILTER. Anything it failed to match was skipped
by the parse loop with no error.

Measured live, five spellings of one intent — **one rejected, four returning the
whole table**:

| request | before | after |
|---|---|---|
| `filter[title][is_null]=true` | `200`, all rows | `400` naming the op + the allowed set |
| `filter[title][not_null]=true` | `200`, all rows | `400` |
| `filter[title][NEQ]=x` (capital op) | `200`, all rows | `400` |
| `filter[Title][eq]=x` (capital field) | `200`, all rows | `400` naming the field + `available:` |
| `filter[a][b][c]=1` (malformed) | `200`, all rows | `400 malformed filter parameter` |
| `filter[title][totalnonsense]=x` | `400` ✓ | `400` ✓ |

**Fix:** the pattern is now permissive (`[^\[\]]+`) and the strictness moved into
code that can produce an error; any parameter starting with `filter[` must parse or
fail. Valid filters are unchanged (verified: `eq`, `partial`, `gte`, implicit-eq).

### F-2 — An unknown sort field or direction was silently dropped
`?sort=ghostfield` and `?order[ghostfield]=desc` returned rows in an arbitrary
order under a `200`; `?order=descending` sorted **ascending**. The engine's own
docs carried the warning *"multi-field sort and `sort=field:desc` are silently
ignored — verify result order, don't trust the param"* — a documented silence is
still a silence, and the warning existed **because** the behavior was untrustworthy.

Both now `400`, naming the field (with `available:`) or the direction (`use asc or
desc`). A test that asserted the old fallback
(`TestBuildQuery_SortUnknownFieldFallsBackToDefault`) encoded the defect and was
rewritten to the new contract.

### F-3 — A misspelled safety flag turned a preview into a real migration
The operator surface decodes permissively, so `{"dryrun": true}` and
`{"dry-run": true}` set `DryRun=false` and **applied**. Measured live against the
control plane: the correct spelling returned a preview; both misspellings returned
`{"status":"migration_queued"}`.

The additive policy limited the blast radius this time — the destructive drop was
still gated — but a non-destructive change (a new column, a type change over
populated data) would have been applied for real, to a tenant, by someone who asked
to look.

**Fix:** the three bodies carrying a `dry_run` (control-plane PUT, admin deploy,
admin rollback) now decode with `DisallowUnknownFields`. The `/admin` surface
already had a strict `decode()` helper — the discipline existed and simply had not
been applied to the paths where it matters most.

### F-4 — `appitools serve <path>` served a different app than the one named
Measured: `appitools serve /tmp/other.json` booted the `./schema.json` in the
working directory and served it. The operator pointed at one app and got another,
with nothing said.

**Fix:** `serve` takes no positional arguments and says so, pointing at `--schema`.

---

## Escalated privately — NOT in this document

One finding is an **exploitable information-disclosure vector**, not a
correctness bug. Per the session's rule it was verified live, reported directly to
the maintainer with the reproduction, and is deliberately **not** described here or
in any commit message. It is tracked as **SEC-5** in the backlog by ID only.

It is a member of this class with the sign flipped: input that is unrecognized
*for this caller* is silently **honored** rather than silently dropped.

---

## Left open — backlog, with evidence

The audit surfaced far more than one session should change: converting them all at
once would be a contract break wider than a release, and several need a design
decision rather than a patch. Each is filed with its file:line and a probe.

### Highest value (ENG-15 … ENG-20)

| ID | Finding | Why it matters |
|---|---|---|
| **ENG-15** | `?after`/`?before` (keyset) **silently discards** `?sort`, `?order[…]` and `?page` — and `meta.page` still echoes the page it ignored | the response actively asserts something false |
| **ENG-16** | Two `order[…]` parameters: the winner is chosen by **Go map iteration order**, so the applied sort flips between identical requests (measured 174/26 across 200 builds) | non-determinism in a response |
| **ENG-17** | A repeated parameter keeps only the FIRST value (`filter`, `page`, `per_page`, aggregate functions) — a request carrying a corrected value is served with the stale one | silent last-write-loses |
| **ENG-18** | An unknown **aggregate function** (`?median=amount`) is never looked at: `200` with the metric missing; `?count=false` turns COUNT **on** (presence-only) | the caller reads a total that is not the one they asked for |
| **ENG-19** | A `before_create`/`before_update` hook of type `webhook` is **validated, required to have a URL, and never dispatched** | the exact mirror of SEC-AUDIT-V2's after-hook finding, which was closed; this half was not |
| **ENG-20** | An unrecognized `$variable` in an RBAC row condition's `val` becomes a **string literal** — one typo from "the app shows zero rows forever", and invisible to the SCHEMA-5 warning, which only fires on an exact `$user_id` | authorization, silent |

### Configuration (OPS-13)
Nineteen environment variables whose parse failure silently reverts to a default:
`RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`, `APPITOOLS_AUTH_MIN_PASSWORD`,
`APPITOOLS_CONTROL_PORT`, `APPITOOLS_FILES_MAX_BYTES`, `DB_MAX_CONNS`,
`APPITOOLS_MAX_TX_OPS` and more. Measured: booting with `RATE_LIMIT_RPS=abc`
logs `rate limiter: 1000 RPS / 100 burst per tenant` and never mentions that the
operator's value was rejected. `envTruthy` maps **any** unrecognized value to
false, including on `APPITOOLS_AUTH_REQUIRE_VERIFIED` — a security toggle.
There is also **no inventory of the 60+ `APPITOOLS_*` variables**, so a misspelled
one is simply never read.

**Ready:** one `envInt`/`envBool`/`envDuration` helper that logs
`WARNING: RATE_LIMIT_RPS="abc" is not a number — using 1000` and a boot-time
inventory check that warns on an unknown `APPITOOLS_*` variable.

### Write bodies (ENG-21)
No write body in `pkg/codegen` uses `DisallowUnknownFields`, though the engine
already does in `pkg/userauth`, `pkg/platformadmin` and `pkg/fleet`. Unknown keys
in a `/api/transaction` envelope, in an operation, and in a `guard` are dropped.
The `422 unknown_field` guarantee on CREATE is not a key check at all — it is a
side effect of Postgres `42703`, and it evaporates for a role with a `fields`
allowlist and for a drift column the additive migration left behind.

### GraphQL (ENG-22)
`GET /graphql` silently discards the `variables` query parameter — a filtered query
returns the **unfiltered** result. A variable nested inside a `jsonb` inline literal
is written as `null`. A multi-field `order` argument keeps one field,
nondeterministically.

### The `count` flag and the aggregate's discarded parameters (ENG-23, ENG-24)
Added after the fixes landed, from the independent sweep's confirmed set (below).
`?count=false` and `?count=0` turn the total **on** — the flag is tested for
presence, never for value — on the list path and the aggregate path alike; `?count`
with `?include=` is dropped entirely; and a failed `COUNT(*)` is swallowed, so the
`200` simply has no total. Separately, the aggregate endpoint parses and validates
`page`/`per_page`/`sort`/`order`/cursors through `BuildQuery` and then discards
them, which after F-2 means `?count&sort=ghost` now `400`s over a parameter the
endpoint would never have honored.

### Schema values (SCHEMA-7)
Keys are strict; **values and key×type combinations are not**. `auto: true`
discards the declared `type` and creates a TIMESTAMPTZ. `enum` on a non-string
field loads and makes the field permanently unwritable. Role-global `actions` are
the only action list in the grammar not enumerated by the meta-schema, so a typo
becomes a permission that grants nothing. `hooks.<event>.timeout` is accepted at
every layer and read by no code.

---

## Not findings (checked; silence is correct)

- **HTTP headers the engine never reads.** Not inspected, so not ignored — and
  rejecting unknown headers would break every proxy, CDN and browser.
- **Unknown top-level query parameters** (`utm_source`, cache-busters). Same
  reasoning. The narrower rule the fixes adopt: a parameter under a prefix the
  engine **owns** (`filter[`, `order[`) must parse.
- **`extensions` in a GraphQL POST body** — the GraphQL-over-HTTP spec reserves it
  and expects servers to ignore what they do not use.
- **Arbitrary nested content in a `json`/`jsonb` value** — a schemaless document is
  the entire point of the type.
- **`workflows`** — documented as parsed-for-forward-compatibility with no
  executor. Listed as an exception in ADR-024 with the condition to revisit.

---

## Second pass — the naming axis, and the worst finding of the audit

The first pass asked "is it rejected?". The second asked "does the error say what
was wrong?", and found a population of surfaces that reject correctly and then
discard the evidence — plus one that does not reject at all. Everything below was
verified against a running engine before AND after the change.

### F-5 — A wrongly-typed CREATE value was silently coerced and reported as success

The headline. On an `int64` field, with the same value, on the same engine:

```
POST  /api/notes {"amount": 1.9}   → 201 Created      (stored: 1)
PATCH /api/notes/{id} {"amount": 1.9} → 422 field "amount" must be an integer
```

`schema.ValidateWrite` only enforces presence and the DECLARED rules
(enum/min/max/pattern/format), so a field with no declared rule had nothing
checking its value; the create handler had also discarded the resource schema
(`_, wrv := writeSurface(...)`), so it structurally had no types to check
against. Siblings were loud in the wrong way: `{"amount": true}` and `{"done": 1}`
both produced a masked **500**.

Now `validateCreateTypes` runs the same `validateFieldValue` the update path uses,
reporting every offending field at once in the S44 shape. Two constraints are
pinned by tests: an **undeclared** key still passes through to Postgres (the
ENG-12 contract — a migration can add a column without rebuilding the router), and
an explicit `null` is governed by `required`, not by the type check.

### F-6 — Four client typos returned 500 `internal error`

`db.IsBadInput` classified only SQLSTATE `22P02`, so only a bad *integer* took the
400 path. Measured, all as 500s from one mistyped query parameter:

| request | SQLSTATE | before | after |
|---|---|---|---|
| `?filter[due][gt]=notadate` | 22007 | 500 | 400 |
| `?filter[due][gt]=2026-13-45` | 22008 | 500 | 400 |
| `?filter[amount][gt]=<24 nines>` | 22003 | 500 | 400 |
| `?filter[ratio][gt]=1e999` | 22003 | 500 | 400 |
| `POST {"due":"not-a-date"}` | 22007 | 500 | 400 |

A 500 is worse than a bad message: it is logged as an engine fault, it burns the
SLO error budget, and any authenticated caller could trigger it at will. The set
is deliberately bounded to codes that were **observed** — `22001` was in the first
draft and removed before commit precisely because it was not.

### F-7 — Two RFC-legal requests were refused

`Authorization: bearer <valid token>` → `401 invalid token`, though RFC 9110 §11.1
makes the auth-scheme case-insensitive **and the engine's own generated OpenAPI
advertises `"scheme": "bearer"`**. And `Host: ACME.localhost` → `400 invalid
tenant`, though RFC 9110 §4.2.3 makes the host case-insensitive.

The Bearer fix is one shared `auth.BearerToken` replacing the same three lines
copy-pasted at **eight** call sites, because they had drifted in what they *did*
with a miss: `/api/*` answered 401, `/auth/refresh` answered **400**, `/auth/mfa/*`
answered a third wording, `/admin/observability` answered **403 "not authorized
for this tenant's observability"** — an authorization verdict flipping on header
capitalisation — and the response cache silently missed. Fixing only the visible
401 would have traded a loud bug for a silent cache cliff.

### F-8 — Rejected, but the error named nothing

`Basic …` → "invalid token" (the caller sent no token and is sent off to debug
one). An invalid tenant host → "invalid tenant" (not the label, not the rule).
A mistyped admin key → "invalid JSON body", discarding the decoder's own
`json: unknown field "rol"`. The aggregate's catch-all → one message for four
distinct mistakes. `?page=0` → served silently as page 1, while `?page=abc` had
always answered *"must be a positive integer"*: the engine's own message stated
the rule its silent path broke.

### A contradiction introduced by the FIRST pass, and removed by this one

The `available:` list added to the sort error last session was reused on the
filter error, where `id` is not accepted:

```
unknown filter field: id (available: amount, done, due, id, ratio, secret, status, title)
```

— naming `id` as available in the very sentence rejecting it. A caller who
believes an error message retries the same request. `availableFieldNames` now
takes the calling path's actual vocabulary. Whether `?filter[id]` *should* work is
a real gap, tracked as ENG-26 rather than fixed silently here.

### Deliberately not fixed, with the measurement that decided it

A full filter-value type check would be **stricter than Postgres**:
`?filter[done][eq]=yes` returns 200 today because Postgres accepts `yes` as a
boolean and Go's `strconv.ParseBool` does not. Being wrong in the safe direction
is still being wrong (ENG-25). The `per_page` over-cap clamp also stays: it is
documented, and `meta` reports the effective value.

## What the adversarial review caught in the FIX

The second-pass fix was reviewed by independent lenses told to REFUTE "this change
is safe". The regression lens returned **BROKEN**, and it was right. Recorded here
because the failures are more instructive than the successes:

**The fix violated the ADR's own rule, in the same commit that wrote it.**
`validateCreateTypes` rejected `{"amount": "7"}` — a JSON string for an `int64`.
Measured against the pre-fix binary: that returned `201` with `7` stored
*correctly*. Form-encoded clients, spreadsheet importers and several HTTP
libraries send every scalar as a string, so this was an un-versioned break of the
create contract, shipped under a rule that says **do not become stricter than the
layer you are protecting**. The rule had been applied carefully to filters (which
is why ENG-25 was deferred) and then broken on the write path two files away. A
string now defers to Postgres; what stays caught is the JSON *number* with a
fractional part, which is the case Postgres silently coerces.

**The fix left a hole exactly where the ADR says to look.** `POST /api/transaction`
had no type check, so `{"amount": 1.9}` still returned `200` and stored `1` — the
corruption, still open on the batch path, in the commit that closed it on the
single-op path. Worse, it made two shipped claims false at once: the promise in
`transaction.go` that a batch op is "validated EXACTLY like its single-op
counterpart", and ADR-024 rule 9, added in that same diff. A caller who hit the
new 422 and reached for the batch endpoint would have got the corruption instead.

**A safety justification was written on a false premise.** The admin `decode()`
began echoing the JSON decoder's message, justified by a comment asserting "this
route is already authenticated". It is not — `decode()` also serves
`/admin/auth/login` and the MFA challenge, reachable with no credentials, and
`encoding/json`'s type errors carry Go struct field names and declared types. Only
the `unknown field "rol"` form is surfaced now: it echoes a key the caller just
sent, which is the whole operator value, and discloses nothing.

**A better error message became a DoS amplifier.** The new tenant 400 reflected
the client-controlled Host *twice*. That path is pre-auth and un-rate-limited, and
Go accepts a 1 MiB header, so a 27-byte constant response became a ~2 MiB
amplifier drivable in a loop by an anonymous client. The label is now echoed
alone, truncated to 32 runes.

**And one fix was simply too strict.** `?count&sum=` was a working request
returning a count; the empty `sum=` made it a hard 400. The distinction that
matters for this ADR is **visibility**: a dropped filter is invisible (rows come
back and the caller cannot tell they are unfiltered), while a dropped aggregate
function is plain in the response, which has no `sum` key. Silence is only the
defect when the caller cannot see it. The empty value is now named only when the
request has nothing left to aggregate.

Two of these — the string scalars and the batch hole — were found by *diffing an
old binary against a new one across ~340 paired requests*, not by reading. That is
the technique worth keeping.

## The independent sweep, and what it says about the fix

A second, independent sweep ran over the same ten surfaces without access to this
document, and its confirmed set was reconciled against the findings above on
2026-08-01. Two results are worth recording, because they are the ones that would
have been embarrassing to miss:

**The ENG-14 fix reached the aggregate endpoint, and it was not a coincidence.**
The sweep reported the same silent-filter defect a second time, at
`pkg/query/aggregate.go`, as a separate finding. It is not separate:
`BuildAggregate` delegates to `BuildQuery` before doing anything of its own, so the
one fix covers both paths. Verified rather than assumed —
`?count&filter[status][is_null]=true` on the aggregate path returns the same
`400 … operator "is_null" not allowed for type "string" (allowed: eq, partial, start)`
as the list path. This is the difference the session was about: the fix changed
where validation happens, so every caller of that code inherited it, and no second
patch was needed.

**Nine confirmed findings were not in the first pass**, and are now recorded —
ENG-23, ENG-24, and the `?after`+`?before` conflict folded into ENG-15. None
changes a conclusion here; all are the same class in surfaces the first pass
sampled rather than exhausted. That is the honest scope of this document: it is a
sweep, not a proof of absence, and the checklist above is what makes the next pass
cheaper than this one.

## INPUT-CLASS-CLOSE-S1 (2026-08-01) — the seven review items closed, and the gate baked in

The adversarial review of the previous session's fix opened seven items
(ENG-25…ENG-31). This session closed all seven and converted the technique that
found the worst defects — diffing two binaries over paired requests — into repo
infrastructure. Everything below was verified by unit test, the full lane, AND
the binary-diff gate (63-case corpus, base = pre-session HEAD vs new; the diff
set was **stable across repeated runs** and every DIFF maps to an intended
change, enumerated in the session report).

- **ENG-27 (security, first).** An undeclared JWT role and a denied one now
  DIFFER IN THE SERVER LOG (`rbac.Policy.DenyDetail`, logged at the REST
  middleware, the transaction per-op deny and the GraphQL deny) and remain
  **byte-identical to the caller** — same status, body, content-type, and the
  same work on both deny paths (no timing/length channel). Naming the
  difference in the response would be a role-enumeration oracle (same family as
  SEC-5: a defence must not leak through its own error channel). A test pins
  both halves. `appitools token --schema <file>` now REFUSES an undeclared
  role, listing the declared set — the place the typo can be told the truth.
- **ENG-25.** `validateFilterValue` rejects, BEFORE any SQL exists, a filter
  value Postgres could not cast — naming the parameter, the value and the type.
  The acceptors reproduce **Postgres's grammar, never Go's** (`yes` is a bool;
  unique prefixes; whitespace; `Infinity`/`NaN`; PG16 literal forms), pinned in
  the conformance direction that matters (PG accepts ⇒ engine accepts) by a
  unit corpus AND a live cross-check against a real Postgres
  (`pkg/integration/filtervalue_conformance_test.go`, which ASKS the server).
  **`time` is the written exception**: its Postgres grammar (dozens of formats,
  `now`, `infinity`, `allballs`…) is not safely reproducible, so a garbage time
  filter stays an anonymous 400 — recorded here, visible in the gate corpus as
  `filter-time-garbage-documented-gap`.
- **ENG-26.** `?filter[id][eq]=<uuid>` works (the implicit PK filters as the
  uuid it is, `eq` only), closing the sort-accepts/filter-rejects asymmetry;
  `availableFieldNames` lists `id` truthfully again — the flag that existed
  only to keep the message honest is gone.
- **ENG-28.** `analyzeQuery` charges a fragment's cost at EVERY spread site
  (memoized, cycle-guarded). The measured ~46× bypass document is in the gate
  corpus and is now rejected; counted ≥ resolved (repeated same-alias spreads
  over-count — the safe direction). The AGENTS 2000 claim holds again.
- **ENG-29.** `CollectUpdate` returns the S44 `fields[]` shape — every
  violation at once (`type`/`unknown_field`/`read_only`/`required`), sorted —
  and all three callers (REST PUT/PATCH, GraphQL `update…`, batch op) emit it.
  One 422 contract for both verbs; the OpenAPI `ValidationErrorResponse` was
  already claiming this and is now true.
- **ENG-30.** Presence, not non-emptiness, gates `page`/`per_page`/`sort`/
  `order`: `?page=` (an empty form field) is the same named 400 as `?page=0`,
  and `?order=desc` with no `?sort=` — a direction naming no field, read by
  nothing — says so instead of vanishing.
- **ENG-31.** `validateFieldValue` treats `file` as the uuid column it is; a
  wrongly-typed file value names the field instead of surfacing as an unnamed
  FK/cast error downstream.

**The gate (Part B).** `scripts/binary-diff-gate.sh` + the growable corpus
`scripts/binary-diff/corpus.jsonl` (63 cases: the previous session's
before/after table, ADR-024's staples, one row per fix above, and rows that
deliberately pin OPEN items — ENG-15's discarded sort, ENG-17's first-value-
wins — so a future change to them is noticed). Harness lessons baked in: list
bodies are compared as SETS (the default `id ASC` order over random uuids
differs between two CORRECT instances), and cache hit/miss markers
(`cache-control`, `x-cache`) are excluded as volatile — both were measured
flipping between identical binaries.

**The `make test` decision (unambiguous).** The lane that lied is the LOCAL
`-short` unit lane: `pkg/integration` is gated by `testing.Short()`, so CI's
full `go test ./... -race` DOES run it — the defect shipped because sessions
treat `make test` as the verdict and nothing pushed had run CI. Resolution:
(a) the specific class is now covered IN the unit lane
(`TestValidateCreateTypes_EngineInjectedDefaultsAreNotCallerInput` exercises
the real `ApplyDefaults` pipeline with no DB), and (b) `make test` is
demoted in AGENTS.md and CONTRIBUTING.md to the fast inner loop: the bar for a
data-path change is unit + full lane + the binary-diff gate.

## NIGHT-SWEEP-S1 (2026-08-02) — the checklist run to completion

This document closed with "it is a sweep, not a proof of absence, and the
checklist above is what makes the next pass cheaper than this one." This session
ran that next pass to the END: **19 agents (9 surface probers + adversarial
verifiers for every non-trivial new claim) probed a LIVE pre-session engine**,
every probe an executable request with its recorded response, every new SILENT
claim independently re-verified before being believed. In the same night, the
nine open ENG items of the class (ENG-15/16/17/18/19/20/23/24 + ENG-34) were
closed, and every CONFIRMED new same-class finding was fixed. The per-surface
verdicts, so the next reader knows what is VERIFIED rather than sampled:

| Surface | Verdict after this pass |
|---|---|
| **List query params** (filters/sort/page/cursors/count/search/include) | **CLEAN.** ENG-15/16/17/18/23/24 closed; plus this pass's own finds fixed: empty/`,,,` `?include=`, search on a text-less resource, the unknown-relation error now lists alternatives. The builder refuses to guess: conflicts named, repeats named, meta states only what the query did. |
| **Single-record + subroute + SSE routes** | **CLEAN (was the pass's biggest new find, CONFIRMED).** They silently discarded the same engine-owned params the list route names (`GET /{id}?filter[…]` returned the row REGARDLESS — a caller could believe a conditional read). All now reject by name. |
| **Aggregate** | **CLEAN.** Owns its whole parameter namespace (the written exception-to-the-exception in ADR-024): unknown functions named with the set, unhonorable list params named with the reason, empty group_by/CSV entries named. |
| **Auth bodies** (`/auth/*`) | **CLEAN on the axes probed.** Strict decode held everywhere; the NAMING axis was the failure (flat "invalid JSON body" discarding the decoder's evidence — the same F-8 defect fixed on /admin and left here) — fixed, plus refresh's swallow-everything decode and the silent body-vs-header token conflict. LOW residue in OPS-16 (trailing JSON, MFA wording). |
| **Operator bodies** (control plane + /admin) | **CLEAN.** F-3's strict decode held on every body it claimed; the ONE lenient body left (tenant REGISTER, on both planes) is now strict. `plan` accepting anything is OPS-16(b) — a product decision, not a patch. |
| **Transaction** | **NARROWED.** Declared-but-irrelevant keys (guard on create, data on delete) and the masked-500 non-uuid id are fixed; the remaining hole is exactly ENG-21 (unknown keys in envelope/op/guard), re-probed and re-filed with fresh evidence. |
| **Files multipart** | **CLEAN.** The duplicate-`file`-part data loss (stored the first, 201, never read the second) is a named 400 with rollback; extra form fields are a WRITTEN exception now (browser forms carry them; same reasoning as unknown top-level query params). |
| **GraphQL** | **Structurally the best surface** (typed inputs name everything). Envelope unknown-key claim REFUTED by the verifier (GraphQL-over-HTTP tolerance). Remaining: ENG-22 (GET `variables` drop — re-verified live, exactly as filed) + NEW **ENG-35** (String variables coerce any scalar while Int variables are strict) — one GraphQL value-handling pass, deferred with the reason written. |
| **CLI arguments** | **The most compliant surface probed** (17/18 named rejections, 0 silent). One real find: no JWT-secret floor anywhere (`serve` boots on a 5-char secret while the docs say 32) — **SEC-6**, a product decision. |
| **The nine ENG items** | All nine reproduced on the frozen base binary exactly as filed (the before-table), and all nine verified CLOSED on the new binary — live probes, the 92-row gate corpus (31 intended DIFFs), unit + integration tests. |

**The honest scope, updated:** the known input surfaces have now been probed to
their checklists' end, with adversarial verification — this is no longer a
sample. What it still is NOT: a proof about surfaces that do not exist yet. The
guarantee below is what keeps a NEW surface honest, and ADR-024's NIGHT-SWEEP
extensions (one parameter one meaning; conflicts named, never adjudicated; meta
states only what the query did; the aggregate namespace rule) are now part of
the pattern a new surface must copy.

Deferred residue, each with its reason written: ENG-21, ENG-22+ENG-35 (contract
changes wider than a sweep), SEC-6 and OPS-16 (product decisions / LOW wording
items), OPS-13 and SCHEMA-7 (unchanged, still open).

## The guarantee

`pkg/query/builder_test.go` now holds the pattern every future surface should copy:
a test asserting that an unrecognized value is **rejected** and that the message
**names it** and **lists the alternatives** (`available:`, `use asc or desc`). That
is the input-layer equivalent of `TestIntegration_DeclaredEqualsApplied` — it does
not enumerate the ways input can be wrong, it pins the contract that being wrong
must be audible.
