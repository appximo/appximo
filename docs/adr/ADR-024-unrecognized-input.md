# ADR-024 — Unrecognized input is rejected, not ignored

**Status:** accepted · **Date:** 2026-08-01 · **Session:** SILENT-FAILURE-S1
**Supersedes nothing. Constrains:** every input surface of the engine.

---

## Context

The engine has shipped the same defect four times, in four different layers, and
each time it was found by accident rather than by a test:

| Where | What was ignored | What the caller saw |
|---|---|---|
| the migration executor (ENG-13) | an FK whose definition changed | `✓ applied` — and the database kept the old constraint |
| the RBAC loader (SEC-AUDIT-V1) | a row condition with a non-`eq` operator | a policy that read as restrictive and enforced nothing |
| the write path (ENG-12) | a field the running process was not booted with | `422 unknown field` for a column that existed |
| the query builder (ENG-14) | `?filter[x][is_null]=true` | **`200` with the full, unfiltered list** |

They look unrelated. They are one class: **the engine inspects an input, does not
recognize it, and continues as if it had not been sent.** The audit for this ADR
([docs/audits/UNRECOGNIZED_INPUT_AUDIT.md](../audits/UNRECOGNIZED_INPUT_AUDIT.md))
swept ten input surfaces and found the pattern alive in most of them.

**Why silence is worse than an error here specifically.** An error costs the
caller one retry. Silence costs them a wrong belief: they filtered and got
everything, they previewed and it applied, they set a rate limit and got the
default, they sorted and the order is arbitrary. Nothing in the response
distinguishes "your input was honored and matched nothing" from "your input was
discarded". The failure is only discovered in production, and there is nothing to
grep for — which is exactly how all four instances above survived.

The project already does this right in two places, and they are the model:

- **Schema strict keys.** An unknown key at any level rejects the schema *and the
  error lists the valid keys for that level*. The audit verified this claim at all
  17 levels of the grammar, through all three validation surfaces: it holds.
- **The 422 multi-field validation error.** Every invalid field at once, each with
  its rule — so one round trip tells the caller everything that is wrong.

Both share a shape worth naming: **reject, name the offending input, list the
alternatives.**

## Decision

> **Any input the engine INSPECTS but does not recognize is rejected with a 4xx
> that NAMES the offending input and LISTS the valid alternatives.**

Concretely, for code written from today:

1. **A parameter that announces its intent must parse or fail.** If a query
   parameter starts with `filter[`, it is a filter: it either resolves to a known
   field and a valid operator, or it is a `400`. It is never skipped. The
   discipline in code: **a pattern decides what an input IS, never what is
   VALID** — validation belongs in code that can produce an error. A regex that
   decides validity silently discards everything it does not match, which is how
   ENG-14 happened.
2. **A request body with a safety flag is decoded strictly**
   (`json.Decoder.DisallowUnknownFields`). A misspelled `dry_run` must not become
   an apply.
3. **A configuration value that fails to parse is a boot error or a WARNING that
   names the rejected value** — never a silent fall-back to a default. The
   operator asked for something specific.
4. **An enumerated value (an operator, an action, a direction, a mode) is checked
   against its set, and the error prints the set.** `use asc or desc` is worth
   more than `invalid`.
5. **When tolerance is genuinely correct, the tolerance is REPORTED.** This is the
   rule ADR-013's migration-honesty work established and it generalizes: being
   permissive is a legitimate design choice; being permissive *and quiet* is not.

## The second axis: rejecting is not enough if the error says nothing

The rule above has two halves, and the first pass only enforced one. A follow-up
sweep found a whole population of surfaces that **reject correctly and then throw
the evidence away** — and one that was worse than either:

| Surface | Rejected? | Named? |
|---|---|---|
| `Authorization: Basic …` | yes, 401 | "invalid token" — the caller sent no token, and goes off to debug one |
| an invalid tenant host | yes, 400 | "invalid tenant" — not the label, not the host, not the rule |
| a mistyped key on `/admin/*` | yes, 400 | the decoder had produced `json: unknown field "rol"`; it was replaced with "invalid JSON body" |
| an aggregate request | yes, 400 | one message for four different mistakes |
| a wrongly-typed filter value | yes, 400 | "invalid request" — with three filters, which one? |
| **a wrongly-typed CREATE value** | **no — `201`** | the value was silently truncated and stored |

The last row is the one that reframes the policy. `POST {"amount": 1.9}` on an
`int64` field returned **201 Created** and stored `1`, while `PATCH` with the same
value returned a clean `422` naming the field. That is not a message defect: it is
silent data corruption, produced by the same instinct — accept, adjust, continue.

Three sharper statements of the same rule came out of that sweep:

7. **An error must name what is actually wrong, not what is merely nearby.**
   "invalid token" for a `Basic` credential is not terse, it is *misleading*: it
   points at the token, so the caller rotates credentials instead of reading their
   own header.
8. **When the engine states a rule, it must enforce that rule.** `?page=abc` had
   always answered *"must be a positive integer"* — and `?page=0` was silently
   served as page 1. The engine's own error message described the contract its
   silent path broke. Wherever a message and a code path disagree, one of them is
   a bug; find out which.
9. **Two paths that accept the same input must answer it the same way.** Create and
   update, REST and GraphQL, list and aggregate. An asymmetry is a bug in whichever
   path is wrong, and it is discovered by comparing them, not by reading either.

And a limit worth writing down, because it is the reason one obvious fix was NOT
made: **do not become stricter than the layer you are protecting.** Rejecting a
wrongly-typed filter value in Go would reject `?filter[done][eq]=yes`, which
returns `200` today because Postgres accepts `yes` as a boolean and
`strconv.ParseBool` does not. Being wrong in the safe direction is still being
wrong (backlog ENG-25).

## The exceptions, each with its reason

An exception with no written reason is forbidden by this ADR. These are the ones
that exist:

| Exception | Why it is legitimate |
|---|---|
| **HTTP headers the engine never reads** | The engine does not *inspect* them, so it cannot be said to ignore them. Rejecting unknown headers would break every proxy, CDN, tracing agent and browser on the internet — HTTP is explicitly designed for intermediaries to add headers. The class only applies to headers the engine looks at and then does not understand. |
| **Unknown top-level query parameters** (`?utm_source=…`, `?_=1699…`) | Same argument, one layer down: they are not inspected, and rejecting them would break cache-busters, analytics tags and link decorations that every browser and email client adds. **Narrower rule instead:** a parameter that collides with a *prefix the engine owns* (`filter[`, `order[`) must parse. That is where intent is unambiguous. |
| **`workflows`** in the schema | Documented as parsed-for-forward-compatibility with no executor (ADR-012). It is a promise about a future version, and the promise is written down in the capability list. Reconsider when the executor ships: then an unknown key inside it becomes a normal strict-key error. |
| **`state_machine.transitions` keys and `workflows.*.steps[].config`** | Genuinely user-defined name spaces — the states are the author's vocabulary, and a step's config belongs to the step type. Both are cross-checked where a check is possible (`transitions` states against `enum`). |
| **Opclass on an existing gin index** (MIG-1) | Postgres cannot introspect it back, so the engine cannot tell "unchanged" from "changed". Already tracked as a known, documented gap rather than silently accepted — the distinction this ADR cares about. |

**Not an exception, deliberately:** "we have always been permissive here" and "a
client might be relying on it". Those are consequences to stage (below), not
reasons to keep silence.

## Consequences

**This is a breaking change for clients that today send input the engine ignores.**
A caller doing `?sort=nonexistent_field` gets a `400` where it used to get an
arbitrarily-ordered `200`. That is the point — but it has to be staged honestly:

- The engine's own contract docs (`AGENTS.md`, `README.md`) carried the warning
  *"multi-field sort and `sort=field:desc` are silently ignored — verify result
  order, don't trust the param"*. A documented silence is still a silence; the
  documentation existed **because** the behavior was untrustworthy. Both the
  behavior and the warning are removed together.
- Surfaces are converted **one at a time, each with its own test**, not in a
  single sweep. This session converts the list query parameters (filters, sort,
  order) and the operator bodies that carry a safety flag. The rest are enumerated
  in the audit with their evidence and left as backlog items, because several of
  them (GraphQL argument coercion, multipart fields, the full config surface)
  change a contract wider than one release should.
- A client that genuinely needs to pass through unknown parameters keeps the
  top-level namespace, which stays permissive by the exception above.

## How it is enforced

1. **A test per surface** asserting that an unrecognized value is rejected AND
   that the message names it. `TestBuildQuery_SortUnknownFieldIsRejected` and
   `TestBuildQuery_InvalidSortDirectionIsRejected` are the pattern: they assert
   the error contains the offending value *and* the word `available:`.
2. **The audit document is the checklist.** Every surface it lists is either
   converted, or carries a backlog ID and a written reason. A new input surface is
   expected to add its row.
3. **`make lint` is a gate** (OPS-3, wired into CI in this session), so the
   `errcheck` half of the same instinct — an error value dropped on the floor —
   cannot grow back silently either.

---

**Related:** ADR-022 (declarative surface boundaries — where a capability is
deliberately absent), [MIGRATION_HONESTY_AUDIT](../audits/MIGRATION_HONESTY_AUDIT.md)
(the same rule applied to operations rather than inputs).
