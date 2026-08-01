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

## The guarantee

`pkg/query/builder_test.go` now holds the pattern every future surface should copy:
a test asserting that an unrecognized value is **rejected** and that the message
**names it** and **lists the alternatives** (`available:`, `use asc or desc`). That
is the input-layer equivalent of `TestIntegration_DeclaredEqualsApplied` — it does
not enumerate the ways input can be wrong, it pins the contract that being wrong
must be audible.
