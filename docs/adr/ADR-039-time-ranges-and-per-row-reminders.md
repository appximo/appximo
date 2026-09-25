# ADR-039 — Time ranges with no-overlap, per-row reminders, and the agenda by voice

**Status:** accepted (MOTOR-AGENDA-S1, 2026-09-21). Builds on ADR-019 (relations), ADR-024 (named rejections), ADR-031 (workflows), ADR-033/035/037/038 (the voice channel).

## Context

The first real user of the whole voice front will be an agenda: «agendame un
compromiso de 4 a 5, y si después intento poner otro encima, que me diga que
ya tengo algo». Before this session the engine could not express three things
an agenda needs, measured live on `64326f0` (`evidencia/MOTOR-AGENDA-S1/auditoria/`):

1. **No range, no overlap.** Two `time` fields are two unrelated columns: two
   overlapping bookings both answered `201`; `?filter[inicio][overlaps]=` was a
   named 400 (the operator did not exist); `docs/MODEL_LAB.md` G8 had recorded
   the gap as the invariant that blocks every booking archetype.
2. **No per-row moment.** A workflow fired on a fixed cron or on a row event;
   «15 minutos antes de esta cita» is a different instant for every row and had
   no trigger.
3. **No agenda in the voice.** The model already produced `inicio`/`fin` as
   two time tokens for «de 4 a 5» (US$ 0,0023), but nothing checked the
   collision before the confirmation, «qué tengo mañana» fell to the model and
   came back «No entendí» (the period grammar had no future), and the
   deterministic parser settled no schedule.

A prior investigation fixed the domain design (task and event are distinct
entities; `tstzrange` half-open `[)` + `EXCLUDE USING gist` with `btree_gist`;
non-blocking rows excluded by a partial condition; advise before writing and
let the database be the net; IANA zone names, never offsets; recurrences are
v2). This ADR records how the ENGINE takes it, generically — not one domain
word in Go.

## Decision

### 1. A range is a NAME over two time fields, not a column type

```json
"ranges": { "horario": { "start": "inicio", "end": "fin", "default_duration": "1h",
  "timezone_field": "zona",
  "no_overlap": { "scope": ["dueno_id"], "when": { "field": "ocupa", "op": "eq", "val": true } } } }
```

The API JSON stays the two natural fields. Every existing door already
understands two `time` columns — filters, sort, `?fields=`, GraphQL scalars,
OpenAPI, the `/app` datetime inputs, Studio's type list, `explain`, the
generator. A `tstzrange` column would have exposed a Postgres literal in the
JSON, demanded a compound widget on every door, and touched ~30 type switches.
What a range ADDS is the invariant, the constraint, two filter operators on
the range NAME, the conflicts endpoint and the voice vocabulary. In Postgres
the block is `tstzrange(start, end, '[)')` inside the constraint and the
predicates: half-open, so 4–5 and 5–6 never overlap (verified live). A NULL
bound is an *unscheduled* row — excluded from the constraint and the filters
(a NULL bound in `tstzrange` is unbounded and would collide with everything).

### 2. `no_overlap` is a real EXCLUDE constraint; the API names the row

`EXCLUDE USING gist (<scope> WITH =, tstzrange(start,end,'[)') WITH &&) WHERE
(start IS NOT NULL AND end IS NOT NULL [AND <when>])`. `btree_gist` (trusted
since PG13, so the database owner installs it — the engine does at tenant
provisioning, `install.sh` at setup, `fleet-audit` checks it) makes the `=` on
a uuid/text scope column indexable beside `&&`. Correctness under concurrency
comes from the database: twenty simultaneous inserts of one free slot →
exactly one `201`, nineteen `409` naming the winner (F4). An application
pre-check cannot promise that.

The violation (`23P01`) enters the ONE write-error ladder (`handlers.
ClassifyWriteError`, ENG-42) as `WriteErrRangeConflict`, and every renderer —
REST create/update, `/api/transaction`, GraphQL, `Ctx.Insert/Update` — answers
`409 {"error":"time_range_conflict","range","existing":{start,end},
"conflicts":[…]}`. The colliding row is resolved by an exact-match lookup on
the key Postgres reports in the Detail (scope values + the range), scoped by
the caller's row condition and field allowlist, so a conflict is never a row
the role could not otherwise read. Error path only; the write hot path is
untouched. The CHECK `start < end` (`23514`) is the S44 422 `rule:
"range_order"` on the end field, named before SQL when both bounds travel.

Symbols embed a hash of the rule's definition (`excl_<table>_<range>_<8hex>`),
so a changed rule is a REPLACEMENT (old dropped + new added in one
transaction, like an FK whose definition changed) and an unchanged rule diffs
as unchanged with no canonical-text comparison — the introspector now reads
`contype = 'x'`.

### 3. Adding the rule over rows that already overlap is refused, naming the pairs

The EXCLUDE has no `NOT VALID` form. The runner applies exclusions apart from
the rest of the plan, after a pre-check that lists the colliding pairs
(`SELECT a.id, b.id … WHERE tstzrange(a) && tstzrange(b) AND scope AND when`):
the safe operations land, the rule comes back in `Unapplied` worded with the
pairs, `Partial()` is true, the control plane / `/admin` PUT answers **422**
with those words (the control plane used to mask a partial apply as `500
internal error`; fixed here) and the schema is NOT persisted over a database
that does not enforce it (ENG-13). The dry-run shows the same `[blocked]`
concern. Nothing is half-applied.

### 4. Per-row reminders are a leader sweep with an exactly-once ledger

`"trigger": {"type": "time", "resource": "eventos", "field": "inicio",
"before": "15m", "when": {…}}` (or `after`; optional `grace`). The worker's
LEADER — the same `pg_try_advisory_lock` holder as the cron scheduler, so one
process sweeps — asks the engine every tick for the rows whose moment is
entering the window (`GET /api/{resource}?filter[field][gt]=…&[lte]=…` as the
workflow's role: RBAC decides what it sees), evaluates `when` in Go, and for
each candidate CLAIMS `(tenant, workflow, row, due instant)` in
`public.workflow_reminders` with `INSERT … ON CONFLICT DO NOTHING` **in the
same transaction as the run's `enqueue` steps**:

- crash before commit → no claim, no outbox row: the next sweep finds the row
  again (never lost);
- crash after commit → the claim exists: the next sweep skips it (never
  duplicated);
- a row whose time MOVES → the old instant is never seen again (the sweep
  reads current rows), the new one fires once; a row rescheduled after its
  reminder fired gets a new reminder for its new time;
- a row that stops matching `when` (cancelled) → never a candidate;
- past `grace` → skipped silently by design (a "15 minutes before" that would
  arrive after the meeting started is noise). Default grace = the offset for
  `before`, 1h for `after`.

A failed run rolls its claim back (at-least-once for a FAILED run, the
workflow doctrine) and is visible in `public.workflow_runs` (`trigger =
time:eventos.inicio`); `/admin/workflows` renders `time:eventos.inicio -15m`;
`/metrics` carries `appximo_workflow_reminders_fired_24h` / `_failed_24h`.
The ledger is pruned after 30 days.

Rejected: a goroutine timer per row (lost on restart, one process's memory,
unbounded); an outbox row scheduled at write time (the outbox has no
"deliver at", and a rescheduled row would need its old row cancelled — a
second bookkeeping the ledger already is, minus the atomic claim).

The natural step is `enqueue` of `message.telegram` with `data.text` — a new
shipped consumer sends that text through the same bot client as the digest.

### 5. The agenda by voice

- The vocabulary marks the range: `[time range horario: inicio..fin, default
  duration 1h, no overlap]`. A bare period on a range resource means what is
  SCHEDULED then (`overlaps`); `at` narrows to one instant (`contains`); kind
  `free` lists the gaps. The period grammar gains the FUTURE (`tomorrow`,
  `day_after_tomorrow`, `next_week`, `next_<weekday>`).
- The deterministic parser settles the reads («qué tengo mañana», «tengo algo
  mañana a las 4», «cuándo estoy libre el jueves» — the resource implied by
  the one agenda resource when none is named) and a SECOND write shape beside
  the state transition: «agenda reunión con Fabián mañana de 4 a 5» → a create
  with both bounds as tokens and the person matched, US$ 0. A bare hour 1–6 is
  the afternoon («a las 4» = 16:00), 7–12 the morning; «de la tarde / pm» adds
  twelve; the confirmation always prints the resolved hour. «a las 10» alone
  lasts the range's `default_duration` and the confirmation says so.
- The confirmation runs the same conflict query the API exposes and SAYS the
  collision: «⚠️ Ya tienes «reunión con Fabián» de 16:00 a 17:00. … ¿Igual lo
  agendo?». **A yes on an invertible rule (`eq` on a bool) writes the row as
  NOT blocking** (`ocupa: no`, shown in the confirmation) so it never blocks
  what comes next — the reading the investigation suggested, kept because
  the alternative (a yes that the database then refuses) is a promise that
  fails. A rule that cannot be inverted (`estado ≠ cancelado`) cannot be
  satisfied by the write, so the owner is asked for another time instead of
  a yes that would 409. The database constraint remains the net either way.
- A stray answer with a future day («sí pero mejor el lunes») stays a
  zero-cost discard where there is no agenda resource; with one, the day is
  executable and reaches the model — the trade written in VOZ-AHORRO-S2 kept
  on the side of not discarding a real question.

### 6. Polymorphism and many-to-many are NOT built here

The app's design needs reminders and attachments "of a task or an event".
Reminders are declarative per resource (a `time` workflow each), so no
reminders table points anywhere; attachments are `file` fields on each
resource. The pattern for a genuinely polymorphic link is two nullable FKs
(`tarea_id`, `evento_id`) with a `before_create` js hook enforcing exactly one
— documented, not built. Many-to-many reads work through a junction resource
+ `relations.many_to_many` (verified); the voice writes ONE row per
confirmation, so «llamar a Fabián» associates through a `belongs_to` FK today
— a junction row by voice is registered as an open item.

## Consequences

- Recurrences (v2, RRULE + materialized window) materialize into rows with
  start/end: nothing here closes that door — the constraint, the filters and
  the reminders apply to materialized rows unchanged.
- A range-free schema is byte-identical on every path (the range code is
  reached only through `res.Ranges`); the write hot path adds no query.
- Studio preserves `ranges` and a `time` trigger through its round-trip
  (Code view); a visual panel for them is registered (with VOZ-13/AUTO-11).
- The binary-diff gate's schema cannot declare `ranges` (the base binary
  rejects the key at load, the same limit VOZ-AHORRO-S2 hit with `aliases`);
  the behaviors are pinned by unit + integration tests and the lab logs.
