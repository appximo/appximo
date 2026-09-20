# ADR-037 — Writing by voice: create and update, never delete; nothing executes without an unambiguous confirmation of exactly what will be written; every write goes through the engine's own write cores

**Status:** accepted (VOZ-ESCRITURAS-S1, 2026-09-20)
**Drivers:** ADR-033 made the bot answer questions from the owner's own
words; the natural next sentence an owner says is not a question — «anotá
llamar a Fabián para arreglar el techo, urgente, para mañana» — and the
channel answered «por acá solo leo». The risk of the next step is different
in kind from reading: a wrong count is corrected by the next question; a
wrong row is a fact in the database that other people act on. So the
frontiers of ADR-033 (generic, deterministic, RBAC as the hard boundary) stay,
and one more is added above them: **the owner reads exactly what will be
written and says yes, or nothing happens.**

## Context

The question layer (pkg/ask) already had everything a write needs except
the write itself: a closed plan grammar the model must speak, a validator
that rejects anything outside the role's vocabulary, a name matcher that
resolves «Gomes» to the row Ana Gómez through the engine's own search, a
deterministic parser for the shapes the schema alone can settle, a spend
ledger with caps, a trace and a history. The engine, on its side, already had
ONE write path that authorizes, validates, runs hooks, enforces state
machines and emits outbox events identically for every door: the cores the
batch transaction runs (`prepareTxOp` / `execPreparedOp`, ADR-024, ADR-027).

The design question was therefore not "how to write" but "what may be
written from a dictated sentence, and what must be true before it is".

## Decisions

### 1. Two forms, no third: `create` and `update`. Delete does not exist by voice

The grammar gains exactly two kinds. `create` carries `data` (the fields the
owner said); `update` carries `where` (the read filter grammar, `match`
allowed, identifying ONE row) and `data` (the changes). The model is taught
both forms ONLY when the asking role may write something — a read-only role's
prompt has no write section at all, so the model cannot even plan one.

**Delete is not built.** Not "confirmed twice", not "soft": «borrá», «eliminá»,
«quitá» (and send/upload verbs) are refused deterministically by the parser
on every vocabulary, before any model call, and the model's own `write` kind
covers the rest. Cancelling IS allowed when it is a declared state
(`estado → cancelada`) — that is a transition the schema wrote, and it can be
read back. An update that would EMPTY a field (`null`, `""`) is rejected by
the validator as a covert delete. The reason is the one the brief stated: a
row that disappears by a misheard sentence is not worth the convenience, and
the value the owner asked for (a task marked done, a person added) never needs
it.

### 2. Nothing is written without a confirmation of exactly what will be written

Every write becomes a **pending** — one per (tenant, role, user), five
minutes, in memory — and the reply is the contract: resource, every value,
every matched name shown as the row it resolved to («persona: Fabián Gómez»,
«vence en: mañana (dom 20 sep)»), the transition as «pendiente → hecha». Then
«¿Confirmás? (sí / no)».

- **The yes is exact.** A closed list of confirmation words after
  normalization (`sí`, `dale`, `ok`, `confirmo`, `listo`, `de acuerdo`…).
  «sí pero mejor el viernes», «creo que sí», «sí, urgente» are NOT a yes:
  the pending is cancelled, the owner is told in one line, and the sentence
  is processed as a new message (it may re-plan — a new pending, nothing
  written). Provoked live: P1.
- **`no` / `cancelar` cancels.** Any other message while a confirmation
  waits cancels it too, and is answered — a question does not get stuck
  behind a stale pending (P14). A second write replaces the first and says
  so (P15).
- **Expiry and identity.** A pending that expired executes nothing. The
  confirm-by-id door (`{"pending_id","answer"}`) requires the id to belong to
  the SAME identity (tenant|role|user): another user's id, a replayed id, an
  unknown id are all «nothing pendiente» — never an execution, never an
  oracle (P9, P13). A token without a subject (a bare `appximo token`) can
  read but is told it cannot write: a write must know who confirms.
- **Two doors, one pending.** Telegram shows the confirmation with inline
  **✅ Sí / ✖ No** buttons (and numbered buttons for a pick); a press
  travels as `{pending_id, answer}` and the keyboard is removed from the
  message so a second press is impossible. The owner may equally TYPE sí/no.
  Over HTTP (Siri) the reply carries `pending_id`, `stage`, `expires_in`;
  the shortcut asks the owner and posts the answer as `{"q":"sí"}` (the
  pending is per identity) or by id.
- **A confirmation costs nothing.** `source: confirm`, no model, ~5–40 ms.

### 3. The write goes through the engine's write cores — there is no third path

A confirmed write is executed by `txWriter` (pkg/codegen/askwrite.go),
which is the batch transaction's own `prepareTxOp` + `execPreparedOp` bound
to the caller's tenant and identity: per-resource RBAC (a read-only `demo`
role is 403 exactly as on the API), the compiled validators (the same 422
with the same fields), `EnforceCreateRBAC` / `EnforceUpdateRBAC` (the owner-
scoped role cannot write another principal's row), the before hooks, the
state-machine guard inside the UPDATE's WHERE, the outbox event in the same
transaction, the read-cache invalidation. The voice layer never touches a
table. **What the API refuses, the voice refuses identically, and says so:**
a 403/409/422 comes back in the owner's words ending with «No escribí nada»,
never a success face (P8 and the integration test where the row moved under
a pending).

Consequences that come for free: a create emits `tareas.created`, a workflow
declared on that event runs in `appximo-worker` (the agenda example notifies
through the digest executor already shipped: `enqueue summary.telegram`), and
the daily digest counts the row like any other.

### 4. Names are matched BEFORE the confirmation, and never stored as text in a relation

A dictated name in a relation field is `{"match": "<as said>"}` — a literal
or an id is a validation error the model gets back once. The engine resolves
it through the SAME matcher the questions use (`Match` + `Decide` over the
engine's own `?search=`, ADR-033 §3), so the confirmation names the ROW:

- one strong match → used and shown («Entendí "Gomes" como Ana Gómez» is
  the same wording the questions use);
- several («Fabi» → Fabián Gómez / Fabiana Torres) → a numbered pick, a
  stage of the same pending (P3, C4); a pick never picks the first row
  silently;
- none → the owner is OFFERED to create it, which confirms too (P4): «sí»
  creates the person (its own write, its own confirmation), then the task's
  confirmation shows «Rocío Paz (nuevo)», then the task is written with the
  new id. The offer is made only when the target can be born from its name
  alone (a label field + every other required field defaulted); otherwise
  the owner is told to load it first.

The same applies to an update's `where`: an ambiguous name is a pick, not
«repetí la pregunta» — a write must end in ONE row.

### 5. A required field the owner did not say is ASKED, never invented

The model is told (rule W1) to put in `data` only what the owner said, and
to still emit the create when a REQUIRED field is missing. The engine walks
the resource's required fields (those without a default, not engine-owned)
and asks for the first missing one — one question at a time, bounded at four
— with a hint fitting its type (enum options, «¿cuándo?», «¿cuánto?»); the
owner's next message is coerced deterministically (an enum by the matcher,
money in pesos → cents, «el viernes a las 3» → a time token, a name → the
matcher). A value that does not fit is re-asked with the reason. Measured
live: the first model answered `write` (gave up) when a required field was
missing; after the rule was made explicit it emits the partial create and
the engine asks (P2).

A value equal to the field's declared default is dropped from a create: the
model tends to fill the state field unprompted («estado: pendiente»), and
the confirmation must show only what the owner determined — the default is
written anyway.

### 6. Time is a token the engine resolves; the model never computes a date

A time field takes `now | today | tomorrow | day_after_tomorrow | yesterday |
next_week | next_<weekday> | end_of_month`, optionally `" HH:MM"`, or an ISO
date the owner literally said. `next_<weekday>` is the NEXT occurrence,
strictly after today. The engine resolves it in the app's timezone
(`APPXIMO_SUMMARY_TIMEZONE`) and the confirmation shows the words AND the
date («mañana (dom 20 sep)», «el viernes (vie 25 sep) a las 15:00»). A
date-only token is the start of that day.

### 7. The parser settles ONE write shape itself: a state transition of one row

«marcá como hecha la tarea de Fabián», «cancelá el pedido de Marta», «pasá a
pagada la orden de Ana»: a transition verb (or a state-named verb whose first
five letters are a prefix of exactly one declared state), one updatable
resource with a state field, one target state, a row identified by a name or
a value — every word consumed, else not sure. It costs nothing and answers in
milliseconds (measured 13–18 ms), and it is the most frequent write an owner
says. Everything else (a create with free text) is the model's — and stays
cheap: **US$ 0.0023 per create (mean of 5 live), p50 789 ms**, one call;
a correction round doubles it. Write plans are NOT cached (a cached create is
a footgun for no saving worth having); a repeated transition costs nothing
anyway.

### 8. Cost, trace, history and caps apply to writes as to questions

A write's model call is counted against the tenant's and the user's daily
caps and the per-minute cap (ADR-035/036); at the cap the model is off and
the parser-settled transition keeps working. The `⚙︎` trace line names the
source («modelo», «confirmación (sin modelo)»). The history keeps the SHAPE
of a write, never its text: `[create tareas: persona_id, titulo, vence_en]`
— a title is data the owner dictated, and the redacted policy cannot read
names out of free text.

### 9. Demo mode stays read-only

`APPXIMO_APP_DEMO_ROLES` names roles whose /app simulates writes; the RBAC is
the boundary there, and it is the boundary here: the `demo` role of both
demos reads only, so its vocabulary has no write form, the parser refuses the
verb before any model call, and had a plan reached the executor the engine
would 403 it (P6, verified on the 58 after the deploy).

## What is deliberately not built (registered)

- Delete by voice (§1). A per-transition RBAC («only the owner may mark
  paid») — the normal `update` grant governs, as on the API.
- Writes of `json`/`jsonb`/`file`/`uuid` fields by voice; multi-row updates
  («cancelá todas las tareas de Fabián»); a create of two resources in one
  sentence (the offer-to-create covers the one case that matters: the
  relation's target).
- The pending store is in-process memory: a restart forgets a pending
  (safe direction — a write that was not confirmed did not happen); in a
  multi-PROCESS fleet each process has its own (the in-process fleet shares
  one per app). Registered as VOZ-11.
- The Telegram receiver mints one identity per authorized chat
  (`telegram:summary`, the configured role): a pending is per chat, not per
  human — one chat, one owner, by the receiver's own access model.
- «sí pero mejor el viernes» costs a model call as a new question (US$
  0.0023) and usually answers «no entendí»; a cheaper reading (cancel and
  stop) was refused because a sentence after a confirmation is often a new
  order.
