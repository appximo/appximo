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
real user has (`appximo spec`, `appximo backend-spec`, their own schema) and
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

**Got:** `appximo ai-generate` produced a **valid** 5-resource schema in
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

`appximo validate` answers *"is this valid?"*. Nobody answers *"does this
model what I asked?"* — and the two gaps above were found by an operator who
knows the grammar by heart. A non-programmer would have deployed a schema that
looks complete, and discovered the missing lifecycle the first time someone
cancelled and then attended an appointment.

**What should exist:** a **plain-language read-back** of the schema (Studio can
render it: "Appointments move: requested → confirmed → attended. A vet can only
see rows where veterinarian_id equals their user id."). Reading a paragraph in
their own words is the only review a non-programmer can actually perform.

## 5-3. Deploying a SECOND app on a server that already has one: 8 manual steps

**Executed** on the 58, which already serves the tienda, for
`petfriendly.appitools.com` (A record straight to the box). The installer could
NOT be used: **every path it writes is a fixed constant** — the unit
`appximo.service`, `/etc/appximo/appximo.env`, `/etc/appximo/schema.json`,
`/opt/appximo/bin/appximo`, `/var/lib/appximo`, and the **whole**
`/etc/caddy/Caddyfile` — and on that box all six belong to the first app. Running
it again would have replaced the unit, overwritten the secrets (breaking the
tienda's JWTs and pointing it at another database) and rewritten the Caddyfile,
**taking the running shop down**. Its pre-flight guards only the PORT.

So the second app went in by hand. The exact cost, counted:

| # | Step the installer would have done | What it took by hand |
|---|---|---|
| 1 | database + role | `CREATE ROLE vetapp` + `CREATE DATABASE vetapp` |
| 2 | dirs + service user | `/etc/vetapp`, `/opt/vetapp/bin`, `/var/lib/vetapp/{files,obs}`, `useradd vetapp`, chown |
| 3 | binary + boot schema | `install -m0755` ×2 |
| 4 | env file with its OWN secrets | 9-line env written by hand (JWT, admin key, DB URL, files dir, obs path, GOMEMLIMIT, **its own control port 9098**), `chmod 600` |
| 5 | systemd unit | 25-line unit copied from the tienda's and edited (user, env file, port 8091, ReadWritePaths) |
| 6 | Caddy site | **append** a second block (the installer overwrites) + `caddy validate` + `systemctl reload` |
| 7 | tenant registration | `curl` to its own control plane `:9098` |
| 8 | platform super-admin | `appximo-cli admin create` — using the CLI that was on the box **only because the tienda had installed it** |

**Result: it works and it coexists.** `petfriendly.appitools.com` got its own
**real Let's Encrypt certificate** (issuer `C = US, O = Let's Encrypt`, subject
`CN = petfriendly.appitools.com`) within ~12 s of the Caddy reload, both apps
serve on 443 with separate binaries, databases, secrets and control planes, and
the tienda never blinked — verified at the end with a **complete purchase**
(catalogue → checkout → mock payment → `pagada`) plus `/`, `/panel`, `/editor`,
`/admin` all 200.

But the eight steps are exactly the tribal knowledge the product exists to
remove, and step 8 is worse than it looks: a first-time user on a fresh box
would not have `appximo-cli` at all. Backlog **OPS-10**.

## 5-3b. The tenant id is not a name — it is the domain's first label

Registering the tenant as `clinica` (the clinic's name) on a box whose domain is
`petfriendly.appitools.com` produced, at request time, **`401 token tenant
mismatch`** — a message that names neither the cause nor the fix. The tenant id
must EQUAL the first DNS label of the domain that serves it. Nothing warns at
registration, when both facts (the tenant id and the Caddy site) are already
known. Merged into **ENG-11** (the tenant-id alphabet item): the same
registration call could check the box's configured domains and say
*"this tenant will only be reachable at clinica.<domain>; your configured domain
is petfriendly.appitools.com"*.

## 5-4. Modification by AI: one round, and the friction is the CONTEXT, not the model

The owner's assistant (an agent with no repo access, given only `appximo spec`
and the current schema) was asked, in the owner's words, to add "reprogramada"
with a reason and to enforce the lifecycle. It produced a **valid schema in one
round** — adding the `rescheduled` state, the `reschedule_reason` field, a
complete and correct `state_machine`, and a `before_update` js hook to force a
reason. `appximo validate`: **0 errors**.

Two observations that matter more than the success:

- **`spec` is enough for the schema surface.** Everything the assistant needed
  was in the 313-line document. This is the cheapest, most effective piece of
  the product.
- **The user must know to paste it.** Nothing in Studio, in the CLI's output, or
  in the app itself says "when you ask an AI to change your app, give it
  `appximo spec` first". The whole flow hinges on a step that exists only in
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
  The modal says so honestly ("Bootstrap one with `appximo admin create`"),
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
   `github.com/appximo/appximo` but never says how to get it. The agent
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
## 5-8. The change that the engine ACCEPTED and silently did not apply

The most serious finding of the session, and it only appeared because the whole
cycle ran **on a live app with data**.

Asked to fix the zero-rows bug (5-1), the owner's assistant produced a correct
plan: add `veterinarians.user_id` (unique) as the catalogue↔login bridge, and
repoint the FK with `"references": "user_id"` on `appointments.veterinarian_id`
and `vaccinations.veterinarian_id`. Valid schema, sound reasoning, and it even
warned about the data backfill.

What happened when it was deployed to production (`appximo-cli migrate`):

- The dry-run **listed the four operations** including
  `ADD FOREIGN KEY appointments (veterinarian_id) -> veterinarians (user_id)`,
  and flagged three backfill concerns.
- The apply printed **✓ for every table and ✓ "schema persisted"**.
- The database still had `FOREIGN KEY (veterinarian_id) REFERENCES
  veterinarians(id)`. **The FK change never happened.**

Root cause, from the migration's own log: the additive policy leaves
`DROP FOREIGN KEY …veterinarian_id` as drift (it never drops), so the new FK is
added **under the same generated name** and Postgres refuses with
`42710 constraint already exists`; the engine logs
`foreign key add failed, skipped (schema unprotected for this relation)` and
**continues** — deliberate behavior (one bad FK must not abort a migration), but
the net effect is that a `references`/`on_delete` change on an EXISTING relation
is a **silent no-op**, while the tenant's recorded schema now claims the new
shape. Declared and applied have diverged, invisibly.

The consequence for the owner is not academic: her fix became **impossible to
complete**. Backfilling `appointments.veterinarian_id` to the login id failed
with `violates foreign key constraint … Key (veterinarian_id)=(…) is not present
in table "veterinarians"` — the stale FK actively blocks the data change the new
schema requires. Unblocking it took raw SQL (`ALTER TABLE … DROP CONSTRAINT`
twice), then the migration re-run cleanly created the correct FK.

**Backlog ENG-13.** And note what saved the day here was a human with psql — the
exact resource the target user does not have.

**The end state, verified in production** (after the manual unblock, the
backfill and one restart): the vet logs in and sees **2 of 2** of her
appointments (was 0 of 2), and the state machine is enforced — `requested →
attended` is `422 invalid transition`, `requested → confirmed → attended` both
`200`, and `attended → requested` is `422` (terminal). The authoring loop
— describe in Spanish → generate → deploy → discover a bug → ask the AI → deploy
the fix → verify — **does close**. It just needs SQL in the middle.


---

## The verdict: where the promise breaks (as written on 2026-08-01, BEFORE the fixes below)


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
2. **The first and last miles need a terminal — and now we know the middle can
   too.** Creating the deploy super-admin, obtaining the Go module, the eight
   hand-written steps for a second app on one server (5-3), the tenant id that
   must equal the DNS label (5-3b) — and, worst of all, a schema change the
   engine ACCEPTS and silently does not apply, whose recovery needs raw SQL
   (5-8). Anywhere on the path, a moment can arrive where only psql helps.
3. **The product's best asset is undiscoverable.** `spec`/`backend-spec` are
   what make "your own AI edits your app" real, and nothing in the product tells
   the user they exist.

**Almost none of this is architectural.** The gaps are a prompt that must teach
state machines, a read-back in plain language, a "copy AI context" button, one
installer flag, one alphabet for tenant ids, and a published module. The one
exception — and the one to fix first — is **ENG-13**: a migration that reports
success while silently skipping the change is worse than a migration that
fails, because it is invisible until someone reads `pg_constraint`. That is the
shape of the work the master guide (Phase 3) has to either fix or teach —
and it is now enumerated, with IDs, in `docs/BACKLOG.md`.

---

# PART FIVE-B — how each finding closed (AUTHORING-GAPS-S1, 2026-08-01)

The session that answered this report treated it as **one pattern, not eight
findings**: *the engine accepts and carries on in silence.* Every fix had to turn a
"valid and wrong" into a "loud and actionable", worded for someone who knows
neither SQL nor Go. What follows is item by item, with what was measured.

| # | Finding | How it closed |
|---|---|---|
| **5-1** | The generated schema silently misses the business RULES | **Closed, measured.** The generation grammar now teaches **state machines** and the per-resource RBAC form, plus the identity-vs-foreign-key rule. Same original Spanish paragraph, 3 runs per arm: **before 0/3 produced a state machine** (2–3 iterations, ~$0.035); **after 3/3** (1–2 iterations, ~$0.016). Teaching the construct made the loop *cheaper*, because it stopped needing correction rounds. The identity bug the generator still makes is now **named** rather than shipped (below). |
| **5-2** | Nobody answers "does this model what I asked?" | **Partly closed.** The engine cannot yet read a schema back in plain language (still open — Studio's job), but the single most damaging silent-wrong case now *does* have an answer: `schema.Warnings` is a layer whose question is "will this do what you meant?", separate from the validator's "may this run?". It fires in five places, and the message is written in the owner's terms, not the engine's. |
| **5-3** | A second app on one server = 8 manual steps | **Closed.** `install.sh --app=NAME` namespaces unit, service user, `/etc`, `/opt`, `/var/lib`, database + role, control port and a per-app Caddy **site file** the main Caddyfile only imports — so installing an app appends a site instead of overwriting the box's. Default unchanged. And a second install for a different domain WITHOUT `--app` now refuses, printing the exact side-by-side command. Verified with two apps staged side by side; a live third-app install on the 58 is deliberately not done (**OPS-11**). |
| **5-3b** | The tenant id must equal the domain's first label | **Closed.** One alphabet everywhere (`^[a-z][a-z0-9]{1,29}$` — the intersection of what Postgres and DNS each accept), and `401 token tenant mismatch` now names the host that arrived, the tenant it implies, the tenant the token carries and the address the token WOULD work at. Creating a tenant through `/admin` warns when the id does not match the app's domain — the moment both facts are known. |
| **5-4** | `spec` works; the user is never told it exists | **Closed.** Studio has a **"Copy AI context"** button (`GET /editor/ai-context`): `appximo spec` plus this app's current schema, one click, ready to paste into any assistant. |
| **5-5** | Studio is good; three specific costs | **Partly closed.** The deploy modal no longer teaches the broken tenant-id rule — it teaches the one that works, and shows the address the tenant will answer at. The English-only interface and the discoverability of "+ add" are untouched. |
| **5-6** | A new column is readable but NOT writable until restart | **Closed, and the test was designed so it could not repeat the earlier self-deception.** The write path now validates against the tenant's DEPLOYED schema merged with the boot one (a union — a deploy can only ever *add* to what was accepted). Verified live on a field asserted ABSENT from the file the process booted with: `PATCH pets.weight_kg` → **200, same PID, no restart**. A NEW RESOURCE still needs a restart — and now says so (`resource_not_loaded`, with the reason and the fix) instead of a bare 404. |
| **5-7** | The 10 %: the module cannot be obtained; `ctx.Get` missing | **Half closed, half escalated.** `Ctx.Get(resource, id)` exists, keeps the row rule, and the doc now states that `QueryOpts.Filters` takes declared fields only — the round the agent lost is gone. The dependency is now documented **honestly** at the top of `backend-spec` (the local checkout + `replace`, its costs spelled out, and exactly what changes when the module is published) — but it remains a **product blocker**, escalated to the decisions Miguel owns. No amount of documentation makes a private module fetchable. |
| **5-8** | A change the engine ACCEPTED and silently did not apply | **Closed, and generalized.** An FK whose definition changed is now a **replacement**: its drop is un-gated and runs in the same transaction as the new constraint, so the name collision that made the change a no-op cannot occur, and a failure rolls back to the old FK rather than leaving the column unprotected. More important than the instance: **the migrator no longer gets to grade its own work.** After every apply the engine re-introspects the database and reports anything declared-but-missing; a partial apply is a failure in every surface (the CLI exits non-zero and does not persist the schema, the control plane restores the previous one, the fan-out marks the tenant failed). The audit that produced this found **three more members of the same class**, all fixed — including one that was live in the repo's own test suite as a red test. See [docs/audits/MIGRATION_HONESTY_AUDIT.md](audits/MIGRATION_HONESTY_AUDIT.md). |

## The journey, re-walked (the proof)

The critical stretch was executed again end to end, on a live app with data, using
only the product:

1. `ai-generate` from the **original Spanish paragraph** → a valid schema **with the
   state machine**, and the engine printing the identity warning next to it.
2. The tenant id with an underscore was **refused at registration**, with the working
   id suggested.
3. Boot: the warning again, in the log.
4. Seeded 2 appointments for a vet → the vet saw **0 of 2** (the bug, reproduced).
5. Applied the fix the warning itself describes (`references: "user_id"`), plus a
   brand-new field, and deployed with `appximo migrate`.
6. The dry-run listed the FK replacement; the apply **actually applied it** —
   confirmed by reading `pg_constraint`, not the migrator's log — and reported
   honestly that the constraint was left unvalidated over pre-existing rows.
7. The backfill that used to be **blocked by the stale FK** went through the API.
8. `PATCH` on the brand-new field: **200, no restart**.
9. The vet sees **2 of 2**. The state machine holds: `requested→attended` 422,
   `requested→confirmed→attended` 200/200, `attended→requested` 422.

**Zero `psql` was needed to make anything work.** The only SQL in the run was the
`pg_constraint` query used to *verify* — the measuring instrument, not the fix.

---

## The verdict, updated (2026-08-01)

The promise under test is *"anyone with little can take advantage of it"*.

**The middle of the journey held before, and it still does** — but the two ends
moved. Of the three break points this report named:

1. **"The generated schema is silently incomplete on business RULES."**
   **Substantially fixed.** The lifecycle is produced (3/3, and cheaper than
   before), and the ownership rule — the failure that produced an app showing
   nothing, with no error anywhere — is now *named at five layers*, in the owner's
   words, with the fix spelled out. What remains is the plain-language read-back
   (5-2): a schema can still be wrong in ways nothing checks. But the specific,
   measured, silent-zero-rows failure no longer reaches production unannounced.

2. **"The first and last miles need a terminal — and the middle can too."**
   **The middle is fixed; the miles are shorter, not gone.** The worst case — a
   change the engine accepted and silently did not apply, recoverable only with raw
   SQL — is closed, along with three siblings the audit found, and the engine no
   longer grades its own homework. A second app on one server is one flag instead of
   eight manual steps. The tenant id has one alphabet and the 401 explains itself.
   What still needs a terminal: creating the first super-admin, and **obtaining the
   Go module** — which is not a code problem but a publishing decision, and is now
   the single hardest wall left in the whole journey.

3. **"The product's best asset is undiscoverable."**
   **Fixed for the schema half.** One click in Studio copies `spec` + the current
   schema. `backend-spec` is still something you have to know to run.

**Where the promise breaks today**, in order of damage:

1. **The 10 % path is unreachable off this machine.** Not a gap in the docs — the
   docs are now honest — but a private module nobody else can fetch. Everything else
   in this report was fixable in code; this one is a decision.
2. **Nobody reads the schema back to the owner in her own words.** The engine can now
   catch one specific class of "valid but wrong". It cannot yet say, in a paragraph,
   what the app it just built actually does — which is the only review a
   non-programmer can perform.
3. **The last manual steps of the first mile.** Creating the platform super-admin is
   still a terminal command with a database URL, at exactly the moment the visual
   tool becomes useful.

Everything above is enumerated, with IDs and Ready criteria, in
[docs/BACKLOG.md](BACKLOG.md).
