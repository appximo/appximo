# The authoring journey — field report (PART FIVE)

**What this is.** The commerce repo's `docs/GAPS.md` holds parts ONE to FOUR: the
field reports of *building* a backend (1-A), *building its frontend* (1-B), and
*operating it in production* (PART THREE/FOUR). Those were written from inside a
Go project. This file is the fifth part and it lives in the ENGINE repo on
purpose, because its subject is not commerce: it is **the authoring cycle for
someone who does not write Go** — one sentence in Spanish → a generated schema →
a live app → the owner changing it by AI and by mouse → those changes reaching
production.

**Method (AI-JOURNEY-S1, 2026-08-01).** Everything below was executed. Where the
user's own AI assistant was part of the flow, it was simulated **honestly**: a
separate agent with NO access to this repository, given exactly the artifacts a
real user has (`appitools spec`, `appitools backend-spec`, their own schema) and
nothing else. Its mistakes are the product's mistakes. Every time the operator
had to use knowledge that only comes from reading the source, it is written down
as friction — that is the point of the exercise.

---

## 5-1. Generation: valid on the first try, but the business rules are silently missing

**Wanted:** a veterinary appointment app, described the way its owner would
describe it (`scratchpad/frase.txt`, ~120 words of plain Spanish: pets, owners,
appointments with vet and reason, "first requested, then the receptionist
confirms, then attended or cancelled", each vet seeing only their own
appointments, vaccination records).

**Got:** `appitools ai-generate` produced a **valid** 5-resource schema in
**2 iterations, 16 s, $0.0234** — well-modelled tables (owners/pets/
appointments/vaccinations/veterinarians), real FKs with sensible `on_delete`,
indexes, `relations` for embeds, `format: email`, phone patterns. The generated
model is honestly good; the thesis that a cheap model plus the validator oracle
produces a usable schema **holds for structure**.

**But two of the owner's explicit rules are absent, and nothing says so:**

1. **The lifecycle became a plain enum.** The owner said "first requested, then
   confirmed, then attended or cancelled" — the schema has
   `status: {enum: [...], default: "requested"}` and **no `state_machine`**.
   Nothing stops a cancelled appointment from becoming attended.
2. **The row-level rule was mis-modelled in a way that is worse than missing.**
   "Each vet sees only their own appointments" produced
   `conditions: {field: "veterinarian_id", op: "eq", val: "$user_id"}` — which
   compares the JWT subject against a **foreign key to the `veterinarians`
   table**. Those are different id spaces: the condition validates, deploys, and
   then silently returns **zero rows** for every vet. The schema is *valid* and
   *wrong*, and the failure appears only in production, as "the app shows me
   nothing".

**Re-asking in natural language did not fix #1.** A second run, with the owner
adding "the system must not let steps be skipped… a requested appointment can
only go to confirmed or cancelled…", cost 3 iterations / $0.0327 and **still
produced no `state_machine`** — because the generator's compact grammar
(`pkg/aigen/prompt.go`) never teaches the construct. A user can rephrase forever
and never reach a feature the prompt does not mention.

**What should exist:** the generation grammar must cover the constructs a
business description routinely implies — **state machines** above all (a
lifecycle is the single most common thing a non-technical owner describes), and
per-resource `permissions`. And the generator should return a **"what I could
not express" note** next to the schema: "you described a lifecycle; I modelled
it as a plain list of values" is the difference between a user who knows what
to fix and one who finds out in production. On the identity mismatch, the
validator can go further than a note: a row condition comparing `$user_id`
against a column that is a **relation to another resource** is almost certainly
a modelling error and deserves a load-time warning.

## 5-2. Evaluating the result requires exactly the knowledge the user lacks

`appitools validate` answers *"is this valid?"*. Nobody answers *"does this
model what I asked?"* — and the two gaps above were found by an operator who
knows the grammar by heart. A non-programmer would have deployed a schema that
looks complete, and discovered the missing lifecycle the first time someone
cancelled and then attended an appointment.

**What should exist:** a **plain-language read-back** of the schema (Studio can
render it: "Appointments move: requested → confirmed → attended. A vet can only
see rows where veterinarian_id equals their user id."). Reading a paragraph in
their own words is the only review a non-programmer can actually perform.

## 5-3. Deploying a SECOND app on a server that already has one destroys the first

Measured statically on the 58 (which serves the tienda) rather than by breaking
it: **every path `install.sh` writes is a fixed constant** — the unit
`appitools.service`, `/etc/appitools/appitools.env`, `/etc/appitools/schema.json`,
`/opt/appitools/bin/appitools`, `/var/lib/appitools`, and the **whole**
`/etc/caddy/Caddyfile`. On that box all six are occupied by the first app. A
second `install.sh` run would replace the unit, overwrite the secrets (breaking
the first app's JWTs and pointing it at another database) and rewrite the
Caddyfile — **taking the running app down**. The pre-flight only guards the
port; it backs the Caddyfile up but never warns that the install is destructive
for a *different* app.

So "one VPS, two apps" — the normal case for anyone with a server and two ideas
— has **no supported path today**: it is a hand-written second unit, second env
dir, second Caddy block. That is tribal knowledge, and it is now
backlog **OPS-10** with a concrete shape (`install.sh --app=NAME` namespacing
everything, a Caddy block appended instead of overwritten, and an uninstall
scoped to one app).

*(The live HTTPS deployment of the generated app is the one part of this journey
that did not run: it needs a DNS A record that did not exist during the session.
Everything else was exercised against a real, running instance of the generated
app.)*

## 5-4. Modification by AI: one round, and the friction is the CONTEXT, not the model

The owner's assistant (an agent with no repo access, given only `appitools spec`
and the current schema) was asked, in the owner's words, to add "reprogramada"
with a reason and to enforce the lifecycle. It produced a **valid schema in one
round** — adding the `rescheduled` state, the `reschedule_reason` field, a
complete and correct `state_machine`, and a `before_update` js hook to force a
reason. `appitools validate`: **0 errors**.

Two observations that matter more than the success:

- **`spec` is enough for the schema surface.** Everything the assistant needed
  was in the 313-line document. This is the cheapest, most effective piece of
  the product.
- **The user must know to paste it.** Nothing in Studio, in the CLI's output, or
  in the app itself says "when you ask an AI to change your app, give it
  `appitools spec` first". The whole flow hinges on a step that exists only in
  `docs/SCHEMA_SPEC_LLM.md` — which the user has no reason to have read.
  A "Copy AI context" button in Studio (spec + current schema, one click) would
  make the product's best feature discoverable.

The assistant also flagged, unprompted, that the engine has no automatic
transitions and no conditional-required — correct on both counts, and evidence
that the spec teaches boundaries, not just syntax.

## 5-5. Modification by mouse: Studio is good, and honest about what it cannot do

Driven with a real browser as a non-programmer would (`scratchpad/studio-*.mjs`,
screenshots in `scratchpad/studio-shots/`):

**What works well.** Studio loads the schema the app is actually serving, the
ERD is legible (FK edges, `REQ`/`AUTO`/`FK`/`SM` badges), clicking an entity
opens a panel with fields/indexes/relations and a `+ add`, the field editor
exposes exactly the grammar (types as a closed dropdown, validation rules per
type, and *"time fields take no length/range/pattern rules"* rendered inline —
teaching the grammar while you use it). The Roles modal shows the real RBAC with
a row-filter field picker. **Adding a field with the mouse and deploying it
worked end to end, with zero data loss** (counts identical before/after), and
the deploy wizard's three steps (Target → Review → Live) show the exact
migration (`ADD COLUMN pets.weight_kg double precision`) before applying.

**The destructive gate is genuinely legible to a non-programmer.** Deleting a
field and deploying shows: *"⚠ DESTRUCTIVE — THESE DESTROY DATA. Each must be
approved explicitly. Unchecked drops are skipped (the column/table stays as
drift — nothing is lost). DROP COLUMN pets.breed — 0 of 4 row(s) hold a value
that will be permanently lost"*, and the only button offered is **"Apply safe
changes"**. No SQL knowledge required to understand the stakes. This is the
best-designed piece of the authoring flow.

**Where it costs the user:**

- **The interface is in English** (measured: 19 English UI words to 2 Spanish)
  while the product's target user — and the whole generation flow — is Spanish.
  The owner who wrote the description in Spanish meets `Auto-layout`,
  `Delete field`, `Apply safe changes`.
- **"+ add" is the only affordance for adding a field** and it is a small
  secondary label next to the section title; an automated pass looking for
  "Add field" found nothing. Discoverable, but only just.
- **The deploy modal teaches a broken rule**: the tenant-id help text says
  *"lowercase letters, digits or `_` … no hyphens"*, and an id with `_`
  registers and is then **unroutable** (400 on every request — backlog
  **ENG-11**). The one place the product instructs a beginner is wrong.
- **Deploying requires a platform super-admin that only the CLI can create.**
  The modal says so honestly ("Bootstrap one with `appitools admin create`"),
  but that is a terminal command with a database URL — for a non-programmer,
  the visual tool stops at the exact moment it becomes useful.

## 5-6. Getting the change into production: one restart, honestly announced

After the Studio deploy, the new column was **readable** (`GET` returns it) but
**not writable**: `PATCH` answered `422 unknown field` until the engine
restarted. This is real engine behavior (the write path validates against the
BOOT-compiled resource) and it is now backlog **ENG-12**.

Two things worth separating:

- **Studio told the truth.** Its result step says the definition change *"needs
  an engine restart to take effect"* and offers **"Restart engine now"** — which
  worked on a CONSUMER binary too (self re-exec, `/readyz` back in ~1 s, writes
  then accepted, **counts unchanged**: 5 owners / 4 pets / 3 appointments / 1
  vaccination / 5 vets before and after).
- **The documentation lied — twice.** `docs/MENTAL_MODEL.md` claimed writes were
  hot because "keys are not whitelisted" (true before strict validation
  shipped), and CONSUMER-PATH-S1 replaced that with "write keys follow the
  deployed schema" — verified against a field that was already in the boot
  schema, an invalid test. Both are corrected in this session's commits. The
  lesson generalizes: **a claim about hot behavior must be tested with a field
  the running process has never seen.**

## 5-7. The 10 %: `backend-spec` carries an agent to a working handler in two rounds

The owner's assistant (again: no repo access, only `spec` + `backend-spec` + the
schema) was asked for something the schema cannot express — *"when the vet marks
a vaccination appointment as attended, record the vaccine on the pet's file in
the same movement; either both or neither"*. It produced a full Go project
(`go.mod`, `main.go`, the handler, an adjusted schema).

**What it got right from the document alone:** `ParseServeArgs` and the
deployable contract; one transaction via `ctx.Tx()`; `ctx.Update` so the
**state-machine guard** is the engine's, not re-stated (illegal move → 422); the
`routes` RBAC grant for the virtual `attend` segment — including the note that
the schema then only boots in THAT binary; no raw goroutines; no manual tenant
filtering. Live verification on the running app: attending a `requested`
appointment → **422 with a human message and zero vaccination rows**; attending
a `confirmed` one → **200, appointment `attended`, exactly one vaccination
written**. Atomicity holds.

**What the document did not give it, in its own words:**

1. **How to obtain the dependency.** `backend-spec` says to import
   `github.com/miguelangel/appitools` but never says how to get it. The agent
   guessed `v0.1.0`; `go mod tidy` failed (private repo, no release tag), and
   the project only built after an operator added `replace … => /root/appitools`
   — knowledge a third party does not have. **This is the single hardest wall in
   the whole journey**: the 10 % path currently requires a repository the user
   cannot fetch.
2. **`QueryOpts.Filters` does not accept `id`.** The agent's first version
   loaded the appointment with `ctx.Query(Filters:{"id":…})` and got a runtime
   500 (`unknown filter field: id`). Told only the error message, it recovered
   correctly on the second round (`ctx.UnsafeTx()` + an explicit re-check of "is
   this vet's appointment", preserving the row rule the helper would have
   applied), and asked for what would have saved the round: a documented
   **`ctx.Get(resource, id)`**, or at minimum a stated rule that filters are
   declared fields only, with the sanctioned lookup-by-id pattern.
3. Smaller gaps it had to guess: the exact `Config` field names, whether `time`
   values go as `time.Time` or RFC3339 strings, the types coming out of
   `ctx.Query`, and whether `ctx.Update` fires `before_update` hooks.

The **cost of the jump to framework mode** is otherwise low and worth stating:
one `go.mod`, one `main.go` of ~40 lines, `build-consumer.sh` for the binary —
the app keeps its schema, its Studio, its `/docs`. The jump is not conceptual,
it is **logistical**: you need Go installed and the module fetchable.

---

## The verdict: where the promise breaks

The promise under test is *"anyone with little can take advantage of it"* — the
90 % declarative, the 10 % in code, an AI doing most of the typing.

**It holds, remarkably, for the middle of the journey.** Generating a
structurally sound schema from a Spanish paragraph costs ~$0.02 and 16 seconds.
Changing it by AI with `spec` works in one round. Changing it with the mouse
works, deploys safely, and the destructive gate is understandable without SQL.
An agent with only `backend-spec` wrote a correct, atomic, RBAC-respecting
handler. None of that is hypothetical: it all ran.

**It breaks at three specific points, in this order of damage:**

1. **The generated schema is silently incomplete on business RULES** (5-1). It
   models the nouns and misses the verbs — the lifecycle and the ownership rule,
   the two things a non-technical owner is most likely to describe and least
   likely to verify. And a `$user_id` condition pointed at a foreign key
   produces an app that returns nothing, with no error anywhere.
2. **The first and last miles need a terminal.** Creating the deploy
   super-admin, obtaining the Go module, running the installer — the visual tool
   stops precisely where the user cannot continue alone. And a second app on the
   same server has no supported path at all (5-3, OPS-10).
3. **The product's best asset is undiscoverable.** `spec`/`backend-spec` are
   what make "your own AI edits your app" real, and nothing in the product tells
   the user they exist.

**None of this is architectural.** The gaps are a prompt that must teach state
machines, a read-back in plain language, a "copy AI context" button, one
installer flag, one alphabet for tenant ids, and a published module. That is the
shape of the work the master guide (Phase 3) has to either fix or teach —
and it is now enumerated, with IDs, in `docs/BACKLOG.md`.
