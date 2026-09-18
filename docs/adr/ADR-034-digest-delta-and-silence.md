# ADR-034 — The digest becomes a habit: a delta against yesterday from ONE remembered snapshot, and an automatic send that speaks only when something changed — with a heartbeat so silence is never mistaken for death

**Status:** accepted (VOZ-DELTA-S1, 2026-09-18)
**Drivers:** ADR-032's own closing verdict ("a pretty dashboard you will look
at for three days"): the picture said WHAT there is, not WHAT CHANGED, and the
sixteen invoices stuck since July came out red every morning, identical. A
channel that repeats itself stops being read, and everything built on top of
it (A-70) dies with it. Two operational facts made the automatic send
fictional besides: the 58 ran no worker, and the stuck invoices would have
shouted hourly until somebody decided.

## Context

`GET /api/summary` counted the present. The owner's question is "¿qué cambió
desde ayer?" — three invoices that arrived today weigh nothing like sixteen
that have waited two months, and a morning with nothing new deserves no
message. Both need memory: something to compare against. The temptation is a
history table and a dashboard of trends; the need is one baseline.

## Decisions

### 1. Memory is ONE row per day, and the delta uses exactly one of them

`public.summary_snapshots (tenant_id, role, day, taken_at, level, attention
jsonb, scheduled_at, decision, silent_streak)` — primary key `(tenant, role,
day)`. What it stores is small on purpose: per resource, the attention counts
by state (the states declared `pending`, or the inferred initial ones), their
total and whether they were inferred; the traffic light; and, for the
scheduled run only, when it evaluated, what it decided and how many silent
runs in a row that makes.

- **The baseline** is the most recent row from a PREVIOUS day. Every digest
  — manual `resumen` or scheduled — compares against it and upserts today's
  row. A manual request never touches the scheduled fields (a 15:00
  `resumen` cannot overwrite what 07:00 decided); the scheduled run writes
  them.
- **No history.** On every upsert, rows older than the baseline day are
  deleted: at most two rows per (tenant, role) exist — today and yesterday.
  The heartbeat does not need a history either: `silent_streak` is an
  integer carried forward. What a history would buy (trends, weekly charts)
  is a different product question with a different cost; this session
  refused to build it because the last row is enough for both the delta and
  the silence.
- **Per role**, because the digest is per role: a row-scoped role's counts
  are its own, so its baseline must be too.
- **Cost:** one SELECT and one UPSERT per digest, plus one COUNT per
  attention state for "arrived today" — the endpoint stays at tens of
  milliseconds; nothing on the CRUD path changes (binary-diff gate: every
  CRUD case SAME; ABBA by rule).

### 2. What the delta says, and what "new" means

Per resource: `+3 desde ayer`, `−2 desde ayer`, `igual que ayer`, `nuevo
desde ayer` (it had no attention yesterday) — and, separately, **`N llegaron
hoy`**: rows in an attention state whose create timestamp is today. That is
the distinction at the heart of the session: sixteen invoices pending with
`+3 desde ayer · 3 llegaron hoy` reads as "three new ones"; the same sixteen
with `igual que ayer` reads as "the old stock, still there". A resource that
is exactly as yesterday and did not move is FOLDED into one small grey line
(`⏸ Igual que ayer: pagos (pendiente: 2) · …`) in the text and an "IGUAL QUE
AYER" line under the bands in the picture — it does not get a big red row
again. A resource whose attention dropped to zero says `✅ ya no espera nada
(ayer esperaban 5)`.

**The traffic light now answers "is there NEWS to attend?"**, not "is there
stock?": red = declared attention with novelty (grew since yesterday, or rows
arrived today; or the first digest, with no comparison); amber = attention
exists but nothing new (the old stock), or only inferred attention; green =
nothing waits. The headline carries the comparison (`23 esperan acción · +1
desde ayer · 2 llegaron hoy`; `Nada que atender — ayer esperaban 26`; on the
first digest `primer resumen, sin comparación todavía` — never a `+16`
invented against a zero that never existed).

### 3. What counts as a change (the silence rule)

`changed` is true when, against the baseline: any resource's attention TOTAL
differs; a resource's attention states redistributed (same total, different
states); rows ARRIVED today in an attention state; or the traffic light went
UP (more to attend) or landed on GREEN (good news — "ayer esperaban 26" is
worth a message). Plain motion (`9 nuevos`, `9 actualizados`) is NOT a change
by itself: every day has updates, and a digest that fires on them is the
noise this rule exists to remove.

One rule was found by provocation and written down: **red → amber is NOT a
change.** The first digest is red (no comparison, so novelty is assumed); the
next morning with the same stock is amber. Counting that drop as "the light
changed" sent one message too many — the same red every morning, in a
different colour. A light that goes DOWN because the novelty aged is silence.

### 4. The policy is the owner's, declared in the schema

```json
"summary": { "resources": [...], "notify": "changes", "quiet_days": 7 }
```

- `notify: "changes"` (the default): the scheduled digest goes out only when
  `changed` is true. `"always"`: the daily report regardless — some owners
  want the morning paper even when nothing happened.
- `quiet_days` (default 7; `0` = never): the **heartbeat**. After that many
  consecutive silent scheduled runs, one short message goes out — the digest
  plus `🔕 7 días sin novedad. Sigo acá — todo igual que la última vez.` — and
  the streak resets. A quiet channel must be distinguishable from a dead
  one, and a heartbeat every week is the cheapest proof that costs no
  attention the other six days.
- Validated at load (`summary_notify_invalid`, `summary_quiet_days_negative`),
  strict-keyed, Studio round-trips it, `spec` teaches it, `explain` unaffected.

**The engine decides, the worker obeys.** `GET /api/summary?mode=scheduled`
computes the digest, applies the policy, records the decision on today's row
and answers `should_send` + `send_reason` (`changes` | `always` | `heartbeat`
| `silent`). The scheduled consumer sends picture + text when told to and
acknowledges the outbox row in silence otherwise — a silent morning is a
processed event with its reason on record, not a retry and not a failure. A
worker facing an engine that predates the field treats the answer as
"always" (the historical behavior; never a silent drop by accident).

### 5. Silence is provable in three places; the manual command never falls silent

- `estado` (the census) ends with the last scheduled evaluation: `⏰ Último
  parte automático: 2026-09-18 07:00 — callado a propósito, 3 días sin
  novedad.` (or `— enviado (changes)`, or `nunca corrió todavía (¿corre el
  worker? ¿está el workflow?)`).
- The heartbeat (§4).
- The workflow observability that already existed: `GET /admin/workflows`
  runs, `appximo_workflow_overdue_seconds` growing when the scheduler stops
  firing — provoked: a worker that ran once and died left the gauge at 105 s
  and climbing two minutes later.
- The manual `resumen` ALWAYS answers, changed or not — silence is a property
  of the automatic send, never of the owner's question.

### 6. The worker reaches production through the deploy path, not only the installer

The 58's apps predate A-67 and were never re-installed; the fleet's deploy
tool (`deploy-app.sh`) swapped binaries and carried no worker — so the
scheduled digest was fiction on every box deployed that way, and would be on
the next one. `deploy-app.sh --worker-binary=PATH` now installs the worker
beside the engine, writes the same unit `install.sh` writes when the box has
none, adds the worker's env keys when missing, enables it and verifies it is
ACTIVE (a worker that does not come up is exit 3 with the journal in the
output; the engine deploy stands). The tenant's deployed schema must declare
the workflow (`resumen_matinal`) — the worker reads `public.tenants.json_schema`,
not the boot file — so a schema change is deployed to the tenant with
`migrate`/the admin PUT as always.

## Consequences

- The JSON of `/api/summary` gains `baseline`, `changed`, `change_reasons`,
  `should_send`, `send_reason`, `silent_streak`, `last_scheduled`; the text and
  the picture gain the delta words and the folded "igual que ayer" line.
- The worker's engine client sends `Cache-Control: no-cache` on every call
  (the receiver's self-call too): a scheduled evaluation is a side effect and
  must never be answered from the 5-second response cache. The cache
  middleware honors the header per request — no hot-path change.
- Deliberately not built: a snapshot history and trends (§1); a per-resource
  notify policy; a tenant timezone for "today" (the cron declares its own).
