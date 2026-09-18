# ADR-033 — Read questions by voice/text ("cuántas citas tiene el doctor Gómez hoy"): a model translates to a filter the engine already executes, never to SQL

**Status:** accepted as DESIGN (VOZ-VISUAL-S1, 2026-09-18) — **NOT BUILT**. This
document exists so the session that builds step 2 of the voice plan does not
re-derive it. Building it is item VOZ-3 in the backlog; the order agreed in
VOZ-VISUAL-S1 is: pictures (done, ADR-032) → read questions (this) → writes
with confirmation (VOZ-4) → reactive workflows (VOZ-5).
**Drivers:** A-70 (the voice plan, five steps, on the existing engine), A-72
(step 1's frontiers: generic, deterministic, RBAC as the hard boundary,
read-only), the two known risks from the research that fed A-70 (dictation
mangles proper names; a model that does not understand must say so).

## Context

Step 1 answers three fixed commands (`resumen`, `estado`, `ayuda`) with a
digest the engine composes deterministically. The next thing an owner says to
the bot is a QUESTION about their data: "cuántas citas tiene el doctor Gómez
hoy", "qué pedidos están sin pagar", "cuánto vendimos esta semana". Every one
of those is a READ the engine already knows how to execute — a filtered list,
a count, an aggregate — over resources and fields the schema declares. What
is missing is the translation from free text to that read.

Two things are settled before any design:

1. **A language model enters here, for the first time in the voice plan** —
   free text → intent is exactly the job a model does and a template cannot.
   Step 1 deliberately stayed model-free (A-72); step 2 is where the model
   earns its place.
2. **The model never generates SQL, never sees the database, never invents a
   number.** It produces a STRUCTURED read request over the schema's own
   vocabulary; the engine validates it and executes it through the same
   doors a browser uses. The RBAC of the asking role applies unchanged.

## Decision

### 1. The model's output is a read PLAN, not a query

The model receives (a) the schema's vocabulary — resource names, field names
with types and enums, state machines, relations — as the ONLY things it may
name, and (b) the question. It returns ONE of:

```json
{ "kind": "count",     "resource": "citas", "filters": [ { "field": "fecha", "op": "gte", "value": "2026-09-18T00:00:00-05:00" }, { "field": "optometra_id", "op": "eq", "value": "<resolved id>" } ] }
{ "kind": "list",      "resource": "pedidos", "filters": [ { "field": "estado", "op": "eq", "value": "pendiente_pago" } ], "limit": 10 }
{ "kind": "aggregate", "resource": "ordenes", "fn": "sum", "field": "total_cents", "filters": [ … ] }
{ "kind": "unclear",   "reason": "no sé a qué recurso se refiere «los rojos»" }
```

The plan's grammar is a CLOSED SET that maps 1:1 onto what the engine already
serves: `filters` are the REST filter grammar (`eq/gt/gte/lt/lte/partial/start/
is_null/after/before` — the type table in AGENTS.md); `aggregate` is
`GET /api/{resource}/aggregate` (`count/sum/avg/min/max`, `group_by`); `list`
is `GET /api/{resource}` with `per_page`. There is no operator, function or
join in the plan that the engine does not have. **No SQL, no expression
language, no free-form string reaches the database.**

### 2. The engine validates the plan against the schema — and REJECTS, never adapts

Before executing, the engine checks every name in the plan against the
schema: the resource exists, each field exists on it, each operator is valid
for the field's type, each enum/state value is a declared member. A plan that
names something that does not exist is REFUSED with the exact reason
("`citas` has no field `doctor`; it has `optometra_id`, `fecha`, `estado`")
and that reason goes BACK to the model for one correction round — the same
validator-oracle loop `ai-generate` already runs for schemas (AI-F2-S4). A
second failure is answered to the owner as "no entendí" (below), never as a
guess. This is the load-validation doctrine of the whole engine ("declared ==
executable", strict keys, no silent drops) applied to a runtime plan.

### 3. Execution goes through the front door, as the asking role

The validated plan is executed by SELF-CALLING the live router (the pattern
step 1's receiver already uses: a short-lived JWT for the configured tenant
and role, a fresh chi RouteContext, the real tenant→JWT→RBAC chain). So:

- the role's row condition scopes every answer (a row-scoped owner counts only
  its own rows — the digest's guarantee, inherited);
- a resource or field the role may not read is a 403/omitted exactly as it is
  for a browser, so a question can never widen what the role sees;
- the response cache, the aggregate endpoint's field allowlist, the `?fields=`
  projection — all of it applies unchanged. The question layer adds ZERO new
  authorization code.

### 4. Proper names are resolved SERVER-SIDE against what exists

Dictation in Spanish mangles proper names ("doctor Gómez" arrives as "doctor
Gomes", "Gómez" as "Gome", a product name as three plausible words). The
model must therefore never put a proper name into a filter VALUE directly. A
plan may carry an ENTITY REFERENCE instead:

```json
{ "field": "optometra_id", "op": "eq", "ref": { "resource": "optometras", "match": "Gómez" } }
```

The engine resolves the reference by searching the referenced resource's
`string`/`text` fields with the engine's own `?search=` (ILIKE, accent-
insensitive normalization applied to both sides) and:

- exactly one match → its id substitutes the reference;
- several matches → the answer ASKS ("¿cuál Gómez: Ana Gómez o Luis Gómez?")
  and stops — never picks;
- no match → "no encuentro ningún optometra que se llame Gómez" — never a
  count of zero presented as an answer to the question asked.

The resolution is RBAC-scoped too (the search runs as the asking role).

### 5. "No entendí" is a first-class answer

The plan grammar includes `unclear`. The system prompt instructs the model to
return it whenever the question does not map cleanly onto the schema's
vocabulary, names a resource or metric the schema does not have, or is
ambiguous between two readings. The engine turns it into a short Spanish
reply naming what IS askable ("Puedo contar, listar o sumar sobre: citas,
pacientes, formulas, productos…"). **A guessed answer to a misunderstood
question is worse than no answer** — it is a wrong number with a confident
face, and the owner has no way to tell.

### 6. The answer is composed deterministically — the model never writes the number

The executed read yields JSON the engine already produces (a count, a list, an
aggregate). The reply text is a TEMPLATE over that JSON in the schema's own
words ("7 citas hoy con Ana Gómez", "3 pedidos en pendiente_pago: …"), the
same way the digest is composed. The model's only job is text → plan. If a
later increment wants nicer prose, the model may REWORD a reply that already
contains the numbers — it may never produce them.

### 7. Where it runs, what it costs

The receiver (`telegram_input.go`) gains one branch: an unrecognized message
that is not a command goes to the question path instead of the help text.
The model call is off the request hot path (the receiver's own goroutine),
bounded by a timeout (≤ 8 s; the 5 s budget for the digest does not apply to
a question — the owner is waiting for an answer, not a screen), and uses the
same `pkg/aigen` transport (raw `/v1/messages`, `ANTHROPIC_API_KEY`, cheap
model by default) so no new dependency enters. Cost per question with the
cheap model is the same order as a schema generation round (~$0.005); the
instrumentation `ai-generate` already prints (tokens, cost) is reused.
Without an API key the question path is DISABLED and the bot says so in the
help text — never a silent failure.

## Limits (written now so they are not rediscovered)

- **Read only.** No plan kind writes. A question that implies a write ("cancelá
  el pedido de Ana") is answered as unclear-for-now with a pointer to the next
  step (VOZ-4, writes with confirmation).
- **One resource per plan.** No joins, no "citas de pacientes de Bogotá"
  across two resources — unless the schema declares the relation, in which
  case a later increment may allow a filter through a `belongs_to` relation
  resolved to an id (same reference mechanism as §4). v1 does not.
- **Time expressions** ("hoy", "esta semana", "el mes pasado") are resolved by
  the ENGINE from a small closed vocabulary into concrete timestamps in the
  app's timezone — the model emits the token (`"today"`, `"this_week"`), never
  a date it computed (models get dates wrong).
- **No memory across questions** in v1 (each question stands alone). A
  follow-up ("¿y ayer?") is unclear until context is designed.
- **The model sees vocabulary, not data.** No row is ever sent to the model:
  privacy by construction, and the reason the model cannot leak a number it
  never saw.

## Verification the build session must run

1. Ten real questions over the tiendita, asked by Miguel by dictation on a
   phone; each answered right, or "no entendí" — never a wrong number.
2. A field that does not exist in the plan → rejected, corrected once, or
   "no entendí"; never executed.
3. A row-scoped role asking a total → gets ITS total (RBAC audit, like the
   digest's).
4. A mangled proper name → resolved against what exists, or a disambiguation
   question; never a zero.
5. A question that implies a write → refused with the pointer.

## Consequences

The first model enters the product at the narrowest possible seam: text →
a closed plan over declared names, validated like a schema, executed like a
browser, worded like the digest. Everything that could make it dangerous — SQL,
data exposure, invented numbers, guessed names — is structurally out of reach
rather than discouraged by a prompt. The price is expressiveness: v1 answers
one-resource questions with one operator per filter. That is the right price
for the first question an owner asks a machine about their own business.
