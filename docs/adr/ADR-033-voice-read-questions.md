# ADR-033 — Read questions by voice/text ("cuántas citas tiene el doctor Gómez hoy"): a model translates to a filter the engine already executes, never to SQL

**Status:** accepted (design: VOZ-VISUAL-S1, 2026-09-18) — **BUILT in
VOZ-PREGUNTAS-S1 (2026-09-19)** as designed, with the refinements recorded in
§Built below (each one found by provoking the live path, none by re-deriving
the design). The order agreed in VOZ-VISUAL-S1 holds: pictures (ADR-032) →
read questions (this) → writes with confirmation (VOZ-4) → reactive workflows
(VOZ-5).
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

## Built (VOZ-PREGUNTAS-S1, 2026-09-19) — what the build kept, what it refined, and why

`pkg/ask` (pure: grammar, vocabulary, translation loop, name matching,
composition) + `pkg/codegen/ask.go` (`POST /api/ask`, the executor) + the
receiver's question branch (`telegram_input.go`). Every decision above holds.
The refinements, each with the provocation that forced it:

1. **Execution goes through the engine's query builders, not a router
   self-call (§3 refined).** The handler runs the plan with
   `query.BuildQuery`/`BuildAggregate` given the role's `EvalResult`
   (row condition + field allowlist) — the exact code path of
   `GET /api/{resource}` and its `/aggregate`, and the same pattern
   `/api/summary` already uses. A re-entrant `ServeHTTP` from inside a
   handler would need the LIVE middleware chain (built in `app.go`, not in
   `BuildRouter`), i.e. global state shared by N in-process fleet apps. The
   guarantee is unchanged: zero authorization code of its own.
2. **A question needs an identity — the reserved `$public` role may read,
   not ask.** The binary-diff gate (whose engines inherited the session's
   `ANTHROPIC_API_KEY`) caught an ANONYMOUS caller being answered — and
   billed — through a schema that declares `rbac.public`. `/api/ask` is 403
   for `$public`; the gate now boots its engines without a key.
3. **The correction round may fix the plan, never answer a different
   question.** Live: a customer role asked «cuántos clientes tenemos»;
   `clientes` was not in its vocabulary; the "corrected" plan counted
   `ordenes` and answered «5 ordenes» with a straight face. A corrected plan
   whose resource is not the rejected one (up to a spelling fix) is
   `unclear`; the prompt also forbids substituting a related resource, and
   the same question now answers «No entendí» three runs out of three.
4. **One time literal: `"now"`.** «tenemos cupones vigentes?» produced a
   valid plan with `period: today` (coupons CREATED today → 0) — a wrong
   answer wearing a valid plan. A time field may now be compared to the
   present (`{"field":"vence_en","op":"gte","value":"now"}`, resolved by the
   engine); the same question answers «1 cupon · vence_en ≥ ahora · activo =
   true». Still never a date the model computed.
5. **`match` also on the resource's OWN text field** (not only a relation):
   «tengo un paciente Deisi Rodrigues?» on a resource whose name is a column
   of its own resolves against that column's values.
6. **The declared timezone — `APPXIMO_SUMMARY_TIMEZONE`.** The 58 runs UTC;
   «hoy» for a Bogotá owner after 7 pm was tomorrow, for the digest as much
   as for the questions (masked before because the scheduled digest fires
   in the morning). One `summary.Location()` for both; an invalid zone
   refuses to boot.
7. **Labels and amounts are chosen, not sorted.** `documento_numero`
   matched "numero" and every client came back «Ana Gómez 333946139»; the
   first money field alphabetically was `descuento_centavos` and every order
   line read «$ 0». A name-like field labels alone; `total/monto/valor/
   precio` wins as the amount.
8. **The picture reuses the census card** (`summary.Render` with a
   `Subtitle` and a neutral level) for grouped answers only — no new
   drawing path.
9. **Measured (Haiku 4.5, the tiendita's 14-resource schema, 23 live
   questions):** p50 0.9–1.0 s, max 1.8 s, ≈ US$ 0.003 per question; a
   correction round doubles it. The vocabulary prompt (~2 400 tokens) is
   below the model's prompt-cache minimum, so the cache does not engage on a
   small schema — a wider one crosses it and gets cheaper. The engine logs
   one line per question with tokens, cost and latency.
10. **Guards the design did not name:** a per-tenant questions-per-minute
    cap (`APPXIMO_ASK_PER_MINUTE`, 30 — a question is a paid call), a 500-
    character question cap, and the model's `reason` escaped and capped
    before it reaches a screen (prompt-injection hygiene: the plan is the
    only channel, and its free-text field is bounded).

**Verification run (ADR §Verification):** the ten owner questions over the
seeded tiendita — nine right with the engine's number, one honest
«0 ordenes · esta semana · estado = pagada» (the model read «vendimos» as a
state filter — the small print says so), none wrong; the field-that-does-
not-exist path refused-corrected-or-unclear (unit + integration, and live:
«cuántos empleados tenemos» → unclear); the row-scoped role got ITS total
(live and in Postgres); mangled names resolved or asked (24 real dictation
pairs in `names_test.go`, live «Yeison Ospina» → Jeison, «Gomes» → Gómez,
«Juan Peres» → Juan Pérez, «Gómez» → asks Ana/Luis, «Wilfredo Pacheco» →
not found); a write intent refused. Dictation by Miguel on a phone — the one
verification only he can run — is what the deployed bot is now waiting for.
