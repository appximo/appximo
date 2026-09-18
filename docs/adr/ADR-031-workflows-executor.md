# ADR-031 — The `workflows` block executes: an executor in the worker, leader-elected by Postgres, expressions in expr-lang, the schema as the only source of truth

**Status:** accepted (AUTOMATIZACION-S1, 2026-09-18)
**Drivers:** decision A-68 (the internal ADR-012's written trigger fired: three
apps re-implement trigger→condition→action by hand in Go, one client asked
three times, and 34 real `factura.emitir` rows sat on a production box as the
exact "on invoice → validate → send → notify" pipeline the original deferral
named) and A-70 (the automation front's technical bases, decided by research
and not re-litigated here).

> **The dangling ADR-012 citation, reconciled.** Public docs
> (SCHEMA_REFERENCE §workflows, ADR-024) cited "ADR-012" as the origin of "no
> executor here", but the public `docs/adr/` starts at ADR-016 — ADR-012 lives
> only in the maintainer's pre-public internal log. A public reader followed
> the citation to nothing. This ADR is now the public anchor: ADR-012 deferred
> the executor "until a client with a real multi-step pipeline asks"; that
> trigger fired; this document records the design that replaced the deferral.

## Context

`workflows` was parsed for forward compatibility, strict-keyed, and executed
NOTHING — a promise visible in `validate`, Studio and SCHEMA_REFERENCE, honored
by nobody. The outbox subsystem beneath it was the engine's best-built and
worst-delivered piece: transactional enqueue, at-least-once delivery, and not
one metric, route or subcommand (closed in this same session — see the
AUTOMATIZACION-S1 register entries).

## Decision

### 1. The executor lives in `appximo-worker`, never on the request path

Event-triggered workflows are outbox consumers (the resource's declared
`events` are the trigger's transport); cron-triggered workflows run on a
scheduler inside the same worker process. The engine's request path gains
ZERO new work. Steps act **through the engine HTTP API** with a short-lived
scoped service JWT (the SERVICE-JWT-V1 doctrine), so every write a workflow
makes inherits validation, RBAC and the write-path guarantees — a workflow
can never do to a row what its role could not do through the front door.

### 2. The schema is the ONLY source of truth

Workflows are declared in the tenant's deployed schema
(`public.tenants.json_schema`) — the same document Studio edits, `validate`
guards and the deploy gate migrates. The worker re-reads it on a poll
(default 60 s), so a deploy reaches execution with no restart and no second
store. The graveyard the research walked: Hasura disables its served console
to avoid divergence, Windmill declares "Git always wins", n8n documents data
loss importing flows, Directus names flow import their top breakage — every
tool that kept two sources of truth had one side lose, always. Here the
second side does not exist.

### 3. Expressions are `expr-lang/expr` — never JavaScript

Sandboxed, statically typed, NOT Turing-complete, termination guaranteed,
~70 ns/op, pure Go, no CGO. Conditions (`condition` step) and computed values
(strings starting with `=` in `data`/`id`) compile **at schema load** — the
validator (`pkg/schema/workflows_validate.go`) compiles every expression and
every cron spec, so a workflow that validates is a workflow that runs.
DECLARED == EXECUTABLE, the engine's oldest rule, applied to the newest block.

### 4. Leader election is `pg_try_advisory_lock` — no new infrastructure

N workers may run; ONE runs the cron scheduler. Leadership is a session-level
advisory lock (`workflows.LeaderLockKey`) on a dedicated connection: losing
the connection loses the lock and another worker takes over within a retry
interval (15 s). The Postgres every install already has is the coordinator;
there is no Redis, no etcd, no raft to operate. Event-triggered workflows
need no leader at all — `FOR UPDATE SKIP LOCKED` already partitions rows
across workers.

### 5. Missed runs, overlap and DST — the K8s/Temporal vocabulary, written down

- **Catch-up = at most one.** Schedules persist in `public.workflow_cron`
  (`next_run`, `last_run`). A due schedule runs ONCE however late — a worker
  down for three days does not replay 72 hourly runs (K8s CronJob's shape).
  `next_run` is advanced BEFORE the run starts, so a crash mid-run never
  double-fires an occurrence; the interrupted run stays visible as a
  `running` row that never finished.
- **Overlap is declared.** `overlap: "skip"` (default) records an occurrence
  that would overlap the previous still-executing run as a
  `skipped_overlap` row — a visible fact, not a silent nothing.
  `"allow"` lets runs overlap (the steps must tolerate it).
- **DST is a real hazard and gets a written policy** (Temporal documents it
  without ornament; most schedulers hope). The schedule is evaluated in the
  workflow's DECLARED timezone — default **UTC, where DST does not exist**.
  With a declared zone: on spring-forward, an occurrence falling in the
  nonexistent hour resolves to the next valid instant and (with catch-up)
  runs once, late, never lost; on fall-back, `next_run` is an ABSOLUTE
  instant — it fires once, and the next occurrence is computed strictly
  after "now", so the repeated wall-clock hour can never fire twice. Pinned
  by `TestNextAfter_DST` on the America/New_York 2026 transitions.
- **Event-run retries ride the outbox.** A failed event-triggered run
  returns its error to the consumer: the row retries with the worker's
  backoff and finally parks `state='failed'` carrying the run's error.
  At-least-once therefore applies to runs — steps must be idempotent (the
  same doctrine every consumer already carries). A failed CRON run is
  recorded and alerted; its retry is the next occurrence (Temporal's cron
  semantics — a cron is a schedule, not a queue).

### 6. Observable from day one — the outbox's lesson, applied

Every run is a row in `public.workflow_runs` (status, error, per-step
detail, duration). `GET /admin/workflows` shows, per (tenant, workflow):
trigger, last run with its error, next run, 24 h run/failure counts, plus
the recent failed runs. `/metrics` carries `appximo_workflow_runs_24h`,
`appximo_workflow_failed_24h` and `appximo_workflow_overdue_seconds` (how
far past due the most-overdue schedule is — a growing value means NO
scheduler is firing, i.e. the worker is down), and the alerter fires on
overdue schedules and failed runs. The outbox shipped blind and stayed blind
for six weeks with 34 stranded events; the executor refuses to repeat that.

### 7. v1 surface (deliberately small)

Triggers: `event` (create/update/delete on a resource that DECLARES that
event — otherwise a load error naming the fix) and `cron` (5-field +
descriptors, optional IANA `timezone`). Steps, sequential: `condition`,
`update`, `create`, `webhook` (one signed attempt through the same
SSRF-guarded HTTPS-only dispatcher as hooks; run-level retries via the
outbox), `enqueue` (emit an outbox event — the composition primitive). An
`enqueue` of a topic that triggers a workflow is a load error (a declared
infinite loop). Trigger `http` is NOT in v1: an HTTP-triggered pipeline is a
custom route whose handler enqueues an event (the seam already exists and is
authenticated/rate-limited); building a second HTTP surface inside the
worker would duplicate auth, RBAC and rate limiting for zero new power.

## Consequences

- The `workflows` key set changed while it was still a dead letter (`ref`/
  `next`/`path` removed; `timezone`/`overlap`/`role` added; semantic
  validation added). No schema in the wild executes workflows (there was no
  executor), so the tightening breaks only never-run declarations — the
  validator names every offending key with the valid set.
- `appximo-worker` gains mode `auto` (the shipped default): workflows +
  email delivery when SMTP is configured, claims topic-scoped, everything
  else left pending and visible. A-67 ships this binary with the installer.
- Two new deps, both pure Go, MIT: `expr-lang/expr` (decided by A-70's
  research) and `robfig/cron/v3` (the standard cron parser; writing a cron
  grammar by hand is how schedulers get DST wrong — this one is 15 years of
  other people's 3 a.m. incidents).
