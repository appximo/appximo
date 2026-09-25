# Appximo — the open-item register

**This file is the ONLY place an open item lives.** Not a chat report, not a
session summary, not somebody's memory. If work is left undone, it is written
here with a reason and a definition of "ready"; if it is decided against, it is
written here with a link to the ADR that says why.

Since **CENTRO-MANDO-S2 (2026-09-17)** the register has two halves that a test
keeps in sync (`cmd/appximo/backlog_items_test.go`):

- **This file** — the narrative: one `### <ID>` per open item, with its origin,
  its evidence and its Ready criterion. English.
- **[backlog/items.json](backlog/items.json)** — the STRUCTURED form the
  command center orders by: per item `que_es` / `por_que_importa` /
  `que_lo_destraba` / `costo` / `dano` / `prioridad` (damage × cost, never
  date) / `decide` (miguel|agente) / `depende_de` / `bloquea` / `frente`.
  Spanish (it is rendered to the owner). Hand-edited together with this file —
  the test fails if an item exists in one and not the other, or misses a field.

**Closed and done live in [BACKLOG_ARCHIVO.md](BACKLOG_ARCHIVO.md)** (the
CLOSED table with its reasoning, every DONE session section, the review
history). This file holds only what is OPEN.

## How to use it

**Any session that leaves something undone adds a row — in BOTH halves.** An
item has exactly one of three states — there is no fourth, and "pending, we'll
see" is not a state:

| State | Means |
|---|---|
| **OPEN** | Not done. Has an impact, and a **Ready** criterion precise enough that a future session knows when it is finished. Lives here + items.json. |
| **CLOSED** | Decided against, with a written justification and the condition to reconsider. Lives in BACKLOG_ARCHIVO.md. |
| **DONE** | Implemented and verified. Moves to BACKLOG_ARCHIVO.md (delete its items.json row). |

IDs are stable and never reused: `ENG-*` engine, `SCHEMA-*` schema grammar,
`RBAC-*` authorization, `OPS-*` operations/tooling, `DOC-*` documentation,
`COMMERCE-*` the commerce backend, **`AUTO-*` the automation front
(worker/outbox/workflows/voice — consolidated by CENTRO-MANDO-S2)**, and
**`DEC-*` decisions that only Miguel can take** (the old "Requires a decision
from Miguel" table, given stable IDs).

**Last reviewed: 2026-09-19 (VOZ-TRAZABILIDAD-S1).** Review history + all DONE
session sections: [BACKLOG_ARCHIVO.md](BACKLOG_ARCHIVO.md).

## OPEN

### MIG-FRONT — The "receive a system that already exists" front (six findings from a real migration, a PRODUCT decision pending Miguel)

**Origin:** MIGRACION-CONFIANZA-S1 (2026-08-28). An external agent migrated a
real system to Appximo v0.1.10 — Symfony 7.2 / API Platform, 23 tables,
46,119 tax declarations, 1.2 GB — in five sessions and wrote a nine-point
report (internal: `FEEDBACK_APPXIMO_MIGRACION-RT.md`). Every finding came from
ONE path the engine had never exercised: **receiving an existing system**.
Building new apps (atina, VecinGo, the vitrinas) never touches it. Three of
the nine were closed in that session (the installer, the validator false
positive, the `/api/transaction` documentation — DONE table below); the six
below are **registered, not built**: opening this front is a product
decision, not a sprint. Each carries the report's evidence and a one-line
reading of whose problem it is (engine / documentation / environment).

| # | Finding (report's severity) | Evidence from the report | Reading |
|---|---|---|---|
| **#1 Bulk import** (blocking) | "The only write path is one row per request; 46,119 declarations are 46,119 HTTP requests." Half of it was FALSE — `/api/transaction` exists in every version, 100 ops/request, measured 100 creates ≈ 50–70 ms on the dev box — and is now published in `/openapi.json` and backend-spec §2b. What remains true: no `COPY`, no file importer, no streaming door. 46k rows = ~460 batches = minutes. | **Documentation first, engine second.** The cost of a real import with batches is minutes; a `COPY`-class door is worth building only if a customer's volume makes minutes unacceptable. Decision: Miguel. |
| **#2 Memory backpressure** (blocking, "the most expensive") | Postgres on a 957 MiB box shared by five apps died `oom-kill` during the load; the engine "kept accepting writes until the kernel killed the database". The migrator's own correction: the box had NO SWAP; with 2 GB of swap the same load was absorbed. | **Environmental, with an engine seam.** The engine did not cause the OOM and no engine change makes 957 MiB hold five apps + a firehose — that must never be promised. The seam that WAS the engine's — no notion of host pressure — is closed minimally (D-ter: `APPXIMO_MEMORY_GUARD_MIN_MB`, writes 503 under `MemAvailable+SwapFree`), and the installer warns about swap. What stays open: a host-derived pool/concurrency limit instead of the fixed 10 connections (`DB_MAX_CONNS`) — an engine item, low priority until a second box shows it. |
| **#4 Historical timestamps** (high for migrations) | `created_at`/`updated_at` declared `auto` reject the value with 422; 4,715 users and every declaration got the migration date. The agent identified `import` (WRITE-ASYMMETRY-S1) as the legitimate door and did not use it because it changes the schema. | **Documentation.** The door exists by design (`"import": {"roles": [...], "fields": ["id","created_at"]}` — create-only, role-named, greppable) — it is the DECLARED way to bring history in, and the report reads as if it did not exist. Put it in backend-spec §2b and the lifecycle spec's migration checklist; no engine work unless a migration needs update-side history (which `import` refuses on purpose). |
| **#5 Heavy fields in lists** (medium, performance) — **DONE in MOTOR-FIELDS-S1 (2026-08-28)** | `GET /api/declarations` returns the whole `data` column per row: ~940 KB per page of 20, p99 3,821 ms. | **Built:** `?fields=a,b` pushed down to the SQL `SELECT` on every row-returning read (list/get/subroute/include root/admin browse/`ctx.Query`) and GraphQL's selection set pushed the same way (ADR-029). Unknown → 400 naming it; hidden by the allowlist → omitted (the read contract). Measured on the rebuilt case (46,119 × 52 KB): page 961 KB / 53 ms → 3 KB / 1.2 ms; 10 rps p99 2.8 s → 175 ms (BENCHMARKS §4b). The `/app` sends it. The default-omission alternative is registered as SCHEMA-8 (a declaration, not a flipped default). |
| **#6 `Ctx` limits for custom routes** (medium) | (a) no response-header setter → no `ETag` from a handler, and the response cache skips custom routes; (b) no event subscription to invalidate an in-memory cache; (c) a `Public` route leaves the caller anonymous so `ctx.Query` is `forbidden` unless `rbac.public` is declared, which also opens the generated endpoints → they used `UnsafeTx` with hand-enumerated columns. | **Engine, three small seams.** (a) `Ctx.SetHeader`/`Ctx.ETag` + opt-in caching for custom GETs; (b) an in-process `OnChange(resource, fn)` fed by the same post-commit fan-out SSE uses; (c) a `Route.AsRole` (run `ctx.Query` as a NAMED read-only role for a public handler) — this one is the clean answer to "public catalogue endpoint without opening `rbac.public`". Each is a session with the parity table and the gate; none blocks a migration. |
| **#8 Schema expressivity** (low) | Partial unique indexes (`UNIQUE (code) WHERE form_year IS NULL`), composite primary keys (`PRIMARY KEY (form_id, field_id)`), `rate_limit` not in the schema grammar. | **Decided / documented.** Partial indexes are CLOSED by ADR-022 D2 (structured form reopens as SCHEMA-2); a composite PK is by design (the implicit `id` + a composite `unique` index expresses the constraint; the FK side is `foreign_keys`); `rate_limit` per resource is a defensible schema key — a small SCHEMA item if a second app asks. The migration note: these are the three things a DDL-for-DDL migration cannot express, so write them down in backend-spec §2 as the known translation table. |

**Ready (for the front as a whole):** Miguel decides whether "receive an
existing system" is a product path. #5 (`?fields=`) is DONE regardless (it
was the one that cost a client's first screen). If yes, the remaining order
by damage is #4 (documentation of `import`), #6c (`Route.AsRole`), #1 (a
`COPY`-class door only with a customer number), #6a/#6b, #8. If no, the
documentation halves (#1, #4, #8) are still worth doing — they cost a page
and they are what the next migrator reads first.

### ENG-54 — `cpu_saturated` silences itself exactly when the queue is longest: the rule's floor scales with the p99 it is judging

- **Deferred in MOTOR-PRODUCCION-S2 (2026-08-30), deliberately.** The session
  had the clean laboratory and fresh numbers, but the rule stands: the
  threshold is not touched without re-running ALL EIGHT provocations (the
  relative floor exists so the lock provocation keeps reading
  `lock_contention`), and that harness is its own evening. A wrong verdict is
  worse than a missing one; an unverified fix is worse than both. Unchanged.

- **Origin:** CAPACIDAD-USL-S1 (2026-08-29). The saturation sequence across
  the ladder inverts: `cpu_saturated` dominates from 150 to 380 rps, and then
  **`pool_exhausted` takes over from 420 to 700 rps** — where the CPU is more
  saturated, not less.
- **CONFIRMED against a live engine (2026-08-29, same session).** 900 rps on
  the canonical read, signals read straight out of the verdict:

  | request p99 | sched p99 | its threshold | ratio | cpu_busy_fraction | PSI cpu | verdict |
  |--:|--:|--:|--:|--:|--:|:--|
  | 1 138.7 ms | 469.8 ms | 56.9 ms | 8.25 | 0.36 | 17 % | cpu_saturated |
  | 1 466.4 ms | 29.4 ms | 73.3 ms | **0.40** | **1.07** | 33 % | pool_exhausted |
  | 1 597.4 ms | 29.4 ms | 79.9 ms | **0.37** | **0.95** | 39 % | pool_exhausted |
  | 2 621.4 ms | 58.7 ms | 131.1 ms | **0.45** | **1.10** | 60 % | pool_exhausted |

  The scheduler latency PLATEAUS around 25–60 ms (the runnable queue is
  bounded by GOMAXPROCS and the in-flight cap) while the threshold — 5 % of
  the request p99 — keeps growing with the queue. Past roughly 600 ms of p99
  the ratio can no longer reach 1, so the rule cannot fire. **The engine was
  reporting `pool_exhausted` while `cpu_busy_fraction` was 0.91–1.10, i.e.
  with the CPU pegged.**
- **Mechanism:** `attribution.go` computes
  `schedFloor := math.Max(t.SchedLatP99Ms, 0.05*p99)`. The relative half was
  added for a good reason (a 1 ms scheduler wait next to a 690 ms p99 caused
  by a mutex must not be called CPU), but it means that at a p99 of 5 000 ms
  the floor is **250 ms** — a scheduler latency the Go runtime will never
  report — so the rule cannot fire at deep saturation and the verdict falls
  through to the pool, which is a SYMPTOM of the CPU wall rather than a
  second wall.
- **Impact: medium.** The verdict is what an operator acts on. Being told
  "the pool is exhausted" at the exact load where the answer is "you are out
  of CPU" sends them to `DB_MAX_CONNS`, which moves the queue and not the
  ceiling.
- **Ready:** cap the relative term (e.g. `min(0.05*p99, 20–50 ms)`), or let a
  `cpu_busy_fraction` at/above 1.0 corroborate on its own when the pool is
  also queueing — the CPU being 100 % busy is not ambiguous. **Then re-run
  the eight provocations of CENTINELA-C-S1**, the lock one above all: the
  relative floor exists because a 1.0–1.5 ms scheduler wait next to a 690 ms
  p99 caused by a mutex must NOT be called CPU, and any fix has to keep that
  provocation reading `lock_contention`. Do not change the ranking without
  that suite — which is why this session recorded the defect with its numbers
  instead of patching it blind.

### SCHEMA-9 — `?search=` is an unindexable full scan across every text column, and the engine offers it with no guard

- **Origin:** CAPACIDAD-USL-S1 (2026-08-29). Measured on a 20 000-row table
  whose `descripcion` averages 1.7 KB (a third of the rows TOASTed):
  `?search=abc&count=true` costs **899 ms of PostgreSQL CPU per request** and
  sustains **≈ 1 rps** on one vCPU. Six concurrent are already past the 5 s
  statement timeout. `?search=` is an `ILIKE '%term%'` OR-ed across every
  `string`/`text` column (`pkg/query`), so no btree index can serve it, and
  `count=true` adds a `COUNT(*)` over the same predicate.
- **This is not hypothetical:** it is the exact shape that took the live
  tiendita demo down at 120 rps on 2026-08-29 (`query p99 5013.5 ms`), and
  the `db_bound` verdict the self-monitor gave there was correct.
- **Impact: high for anyone who puts a search box on a table with a large
  text column** — which the generic `/app` does by default.
- **Ready:** one of, in order of preference — (a) a declarative trigram
  index (`{"fields":["descripcion"],"method":"gin","opclass":"gin_trgm_ops"}`
  plus `CREATE EXTENSION pg_trgm`, and `?search=` planned to use it); (b) a
  per-resource `searchable: [...]` allowlist so a heavy column can be left
  out, with the default being the small columns only; (c) at minimum, a
  SCHEMA-5 **warning** at load when a resource has a `text` field and no
  index that `?search=` could use, plus a line in `docs/BENCHMARKS.md §4d`
  (written) and in `frontend-spec`. (c) is a session; (a) is the real fix.

### OPS-38 — The lab token's scopes are narrower than the lab's design assumed: no tags, no snapshots

- **Origin:** LAB-CAPACIDAD-S2 (2026-08-29), the first live run. Verified
  against the API: creating a droplet WITH a tag 403s on `tag:create`, and
  POST/GET `/v2/tags` 403 too — so lab droplets exist UNTAGGED and the
  destroy guard's second factor degraded (loudly, by design) from "prefix AND
  tag" to "prefix AND lab-size fingerprint" (c-4/c-2/s-2vcpu-2gb; tests
  cover both modes); listing is by name prefix over the full droplet list.
  Snapshotting the provisioned target also 403s (droplet actions are outside
  the granted scopes), so every `lab up` reinstalls from scratch (~8 min —
  which also re-exercises install.sh, so it is tolerable, not free).
- **Impact: low-medium.** The leash still holds (prefix + fingerprint +
  protected-IP belt + cap + reaper), but the tag factor is the stronger
  second factor and the reinstall costs ~8 min per `up`.
- **Ready:** Miguel adds `tag:create` (+ ideally `tag:read`) to the token —
  the code already tries tags first and uses them when granted — and decides
  on snapshot scopes (droplet action + image read/delete) vs accepting the
  reinstall. `lab up`'s output names both degradations when they happen.

### OPS-39 — The isolated lab loses the service-demand cross-check: engine/PG CPU per request is not readable cross-box

- **Origin:** LAB-CAPACIDAD-S2 (2026-08-29). `tools/capacity`'s CPU
  accounting reads local /proc — on the generator box that now measures the
  GENERATOR (exactly what the saturation gate needs), but the engine's and
  PostgreSQL's CPU-seconds per request (the `X_max = C/D` generator-free
  bound, §4d) are zero in remote runs, and the fit report prints an empty
  service-demand block.
- **Impact: low.** The USL no longer carries the co-resident-generator
  pollution the cross-check existed to bound; but a second, assumption-free
  estimate is still worth having, and Module C already computes CPU on the
  target.
- **Ready:** `capacity` derives per-request engine CPU from the target's own
  self-monitor (`/admin/resources?since=` already travels with every run —
  the ticks carry CPU) and prints the service-demand block from that when
  /proc yields nothing; suppress the misleading zero block meanwhile.

### OPS-36 — The capacity procedure exists but runs only on our box: it needs a customer-side form

- **Origin:** CAPACIDAD-USL-S1 (2026-08-29). `tools/capacity` +
  `docs/BENCHMARKS.md §4d` make the ladder, the USL fit, the trust gate and
  the user translation reproducible in one command, and they were run
  end-to-end on the 105. What is NOT done is the part that answers a customer:
  the procedure assumes a laboratory instance, a seeded dataset and a raised
  rate limit, all of which a customer's production box does not have.
- **Impact: medium.** Without it the answer to "does it hold my business?"
  stays "here is what it held on ours, with these assumptions".
- **Ready:** a documented safe form of the sweep against a customer
  instance — a read-only ladder bounded well below the estimated ceiling, run
  against a staging copy or in a maintenance window, that stops at the first
  level where the p50 leaves its baseline instead of driving to collapse; plus
  the service-demand-only variant, which needs no ladder at all (it reads
  CPU-seconds per request off `/admin/resources` under whatever real traffic
  exists and divides). The second is the one that is safe on production and
  should come first.

### OPS-35 — Mann-Whitney does not test the tail: a claim about the p99 needs a permutation test on Δp99, and `compare-groups` does not run one yet
- **Origin:** CENTINELA-C-S1 (2026-08-29), from the Centinela research report
  §Bloque 2 and decision A-54. The house's ABBA verdict is Mann-Whitney U on
  the pooled samples with the max(0.5 ms, 3 %) gate — correct for what it
  gates (the median: stochastic dominance), and BLIND to a change that moves
  only the tail. A p99 promise ("< 1 % of p99") is not measurable on this box
  at all (≈ 0.016 ms against a 0.5 ms EMD), and the overhead of the resource
  collector was therefore stated on allocs/op, CPU-seconds and RSS with the
  p99 as an upper bound (docs/BENCHMARKS.md §4c).
- **Shipped this session:** `tools/devhub/stats.PermutationQuantileDiff(a, b,
  p, resamples)` — a two-sided permutation test on the difference of the
  p-th percentile (deterministic RNG, +1 correction), pinned by a test with
  identical medians and a 2 % tail shift; the methodology note in
  docs/BENCHMARKS.md §7 and in the Centinela specs (C §6, B §6).
- **Impact:** medium for the measurement discipline: every "no_change"
  verdict so far is a MEDIAN verdict. Nothing published claims a p99 delta,
  so nothing is wrong today — the gap is that the protocol cannot yet SAY
  "and the tail did not move either".
- **Ready:** `POST /api/bench/compare-groups` (DevHub) reports, next to the
  Mann-Whitney verdict, `p99_delta_ms` + `p99_perm_p` (≥ 2000 resamples) and
  a bootstrap CI of the Δp99; `scripts/bench-protocol.sh`'s summary prints
  them; docs/BENCHMARKS.md §7 states the rule "a median verdict and a tail
  verdict are two verdicts". Runs on the DevHub's stored k6 samples — no new
  data collection.

### OPS-34 — Two builds of the SAME source measure ~9 % apart on the 105: the ABBA base must be built like the new binary
- **Origin:** MIGRACION-CONFIANZA-S1. The frozen ABBA on the PATCH protocol
  read +10–15 % for the new binary with base-vs-base identical (4.689 /
  4.685 ms). Attribution and a bisection showed the base commit REBUILT from
  a clean worktree measuring 5.101 ms — the new family's number — while the
  original base file (built in the main tree: buildinfo `v0.1.11+dirty` +
  vcs stamps, 24 MB of differing bytes vs the rebuild) kept measuring 4.64–
  4.69 across three arms. Same code, two layouts, ~9 % apart on this 1-vCPU
  box. The session's verdict was recovered by comparing same-session builds
  (guard ON 5.036 vs rebuilt base 5.101 → −1.3 %).
- **Impact:** medium for the measurement discipline: a base binary built
  earlier / elsewhere can turn a `no_change` into a false `regression` (or
  hide a real one), and the protocol's max(0.5 ms, 3 %) gate is below this
  artifact's size.
- **Ready:** `scripts/bench-protocol.sh` / the ABBA recipe document and
  enforce "build BASE and NEW in the same session, from the same kind of
  tree (both worktrees, or both the main tree), with the same version-stamp
  shape"; ideally the ABBA script builds both from commits itself. A note in
  docs/BENCHMARKS.md about the artifact size measured here.

### ENG-51 — Custom routes carry no `Server-Timing`

**Origin:** APP-PODER-S1. Every GENERATED read (list, get, subroute, embed,
aggregate) sets `Server-Timing` from the span tracker right before writing
its body; a consumer's custom route (`appximo.Route` handler, e.g. the
tiendita's `/api/catalogo`) ends with `ctx.JSON(...)` and sets nothing, so a
UI built on custom routes cannot show the engine's time. Low: the panel is
generic and reads generated routes; the header is a read-side courtesy.

**Ready:** `Ctx.JSON` (and the other terminal helpers) set the same header
from the tracker, or a documented `Ctx.ServerTiming()` a handler calls
before its own write — pinned by a library integration test.

**CENTINELA-C-S1 (2026-08-29) widened the impact:** the self-monitor's
`db_bound` rule reads the request's `query`/`count` spans, and a custom
route marks none (`ctx.Query`/`Get`/`UnsafeTx` run inside the handler
without a span) — so on the tiendita's `/api/catalogo` the query stage reads
0 ms and the verdict can only ever say `pool_exhausted` ("undersized"), never
"the queries hold the connections / the database is slow". Verified live: the
100 rps k6 read `pool_exhausted` with query99 = 0.0. The same fix closes
both: `Ctx`'s data helpers mark a `query` span on the request's tracker
(one line each), which also gives custom routes their `Server-Timing`.

### ENG-50 — JSON number fidelity beyond float64, in both directions (`json`/`jsonb`)

**Origin:** MOTOR-TIPO-JSON-S1 (ADR-028, alternative "preserve the client's
exact bytes", deferred). Every HTTP door decodes the request body into Go
values (`encoding/json` → float64), so a number inside a `json`/`jsonb`
document loses precision past ~2^53 (`12345678901234567890` → `…67000`) and
a decimal literal is re-rendered (`1.50` → `1.5`) before it reaches the
column; on the way OUT pgx decodes a `jsonb` document into Go values with the
same loss. A `json` field written as a JSON-text STRING keeps its numeric text
(compacted verbatim) — the one path that is exact today.

**Impact:** low for the known apps (money is `int64` minor units by doctrine;
ids are strings); real for a document store carrying big integers or exact
decimals. Documented in backend-spec §2 and SCHEMA_REFERENCE §3 as the ~2^53
limit.

**Ready:** an engine-wide decision — decode bodies with `UseNumber` (and
teach the type checks `json.Number`) or capture `json.RawMessage` for
json/jsonb fields on the way in, AND scan json/jsonb columns as
`json.RawMessage` on the way out (a pgx type-map change that also changes
`Ctx.Query`'s documented row types) — measured on the write and read
protocols. Not a `json`-only fix: half of it is worthless without the other.

### ENG-48 — The platform-admin login throttle has no knob either (same shape as ENG-47)

**Origin:** MOTOR-AUTORIZACION-S1 Part C, the sweep for limits "in the same
situation" as ENG-47. `platformadmin.loginThrottle` bounds `/admin/auth/login`
and the admin MFA verify at 5/min per admin email, hard-coded
(`newLoginThrottle(5, 5)` — `pkg/platformadmin/throttle.go`). Deliberately
NOT touched this session: a platform admin is a privileged credential no demo
shares, so today's default is the right default and loosening it has no
legitimate driver yet.

**Impact:** low. It would bite only an operator who scripts many admin logins
per minute (e.g. a CI job minting platform tokens in a loop — `X-Admin-Key`
covers that path today).

**Ready:** `APPXIMO_PLATFORM_LOGIN_ATTEMPTS_PER_MINUTE` / `_BURST` wired like
ENG-47's knob (same default, same boot warning above the default, same
refusal on a non-integer), or an ADR saying the admin throttle stays fixed
and why.

### RBAC-2 — A field allowlist DROPS a client's field silently — the "hidden attempt" contract

**Origin:** MOTOR-AUTORIZACION-S1 audit, finding 2 (generalized). For a role
with a `fields` allowlist, a write body carrying a field outside it is
dropped and the write answers 200/201 — on create (`EnforceCreateRBAC`) and
update (`CollectUpdate`'s `writable`). The IDENTITY column is now exempt
(explicit 403, ADR-027); every other non-allowlisted field keeps the
documented silent drop (AGENTS.md, SCHEMA_REFERENCE §RBAC: "dropped silently
— not an error").

**Impact:** not an escalation — the field is never written — but the
project's own rule ("an attempt is rejected, never swallowed": ADR-024, the
NIGHT-SWEEP class) says a client that believes it wrote `secret_flag` and got
200 has been lied to, and the log shows nothing. Changing it is a CONTRACT
change: a generic UI that PUTs a whole object back with fields the role may
only read would start failing.

**Ready:** decide in its own increment — (a) reject with 422
`rule: "forbidden_field"` naming the field (and make `/app` + the backoffice
guide send only writable fields), or (b) keep the drop and echo it
(`meta.dropped_fields` / a `Warning` header) so the attempt is visible; or an
ADR that keeps the silent drop with the reasoning. Measured with the
binary-diff gate either way.

### OPS-33 — An env value with spaces must be QUOTED, or the scripts that source it break

**Origin:** TIENDITA-VITRINA-S1, found hours before it would have bitten.
APP-VITRINA-S1 added `APPXIMO_APP_BANNER_TEXT=← Volver a La Tiendita` to
`/etc/appitools/appitools.env` **unquoted**. systemd's `EnvironmentFile=`
parses that fine, so the engine was correct and nothing looked wrong. But
`redate-demo.sh` — the `ExecStartPost=` of the nightly `demo-reset.service` —
does `. "$ENV_FILE"` in bash, where the line becomes `APPXIMO_APP_BANNER_TEXT=←`
followed by an attempt to RUN `Volver` → `command not found`, exit 127.

**Impact:** the nightly golden reset of the tiendita demo would have started
FAILING at its first run after that change (the restore itself ran; the
re-dating step did not, and the unit went to `failed`). The last successful run
was 2026-08-26 04:15; the next was due 2026-08-27 04:15. This session quoted
both env files (`.pre-quote` backups kept), restarted both services and ran the
whole unit end to end — `Result=success`, golden restored, dates re-anchored.

**Ready:** the class, not the instance. Either (a) `install.sh` and the
deploy docs write env values quoted and say why, or (b) the scripts that only
need `DATABASE_URL` stop sourcing the whole env file. A test that writes a
value with a space and asserts both consumers survive.

- **Recurred (VOZ-DELTA-S1, 2026-09-18):** ALERTAS-TELEGRAM-S1 wrote
  `APPXIMO_ALERT_APP_NAME=La Tiendita` unquoted into the tiendita's env; the
  nightly `demo-reset.service` would have failed again at `redate-demo.sh` (it
  did, on the first manual run this session: `Tiendita: command not found`,
  exit 127). Quoted on the box (`/root/appitools-env.pre-quote`), reset re-run
  end to end. The fix this item asks for — a check in `install.sh`/`fleet-audit.sh`
  that every value with spaces is quoted — is still not built; until it is,
  every session that adds an env value must quote it.
### COMMERCE-11 — The tiendita's six product photos weigh 1.55 MB (one of them 676 KB)

**Origin:** TIENDITA-VITRINA-S1's image audit. The photos are licence-verified,
brand-free and face-free (the A-21 filter holds), but they are served at their
ORIGINAL size — `rua-lan.jpg` is 676 KB, `jea-sli.jpg` 290 KB, `som-vue.jpg`
299 KB — for tiles that render ~260 px wide. Measured live: the SPA itself is
168 KB; a desktop first paint pulls 768–1 035 KB of photos on top, and the
number swings run to run with the lazy-loading scheduler.

**Impact:** the demo's weight is dominated by images nobody sees at full size.
Lazy loading already spares mobile (0 photos at `load`), so this is a desktop
and scroll cost, not a blocker.

**Why it was not fixed here:** replacing the photos means re-uploading to the
file store, re-pointing `productos.imagen_id`, and **regenerating the golden
dump** — and this session was explicitly told to leave the golden intact.

**Ready:** the six source JPEGs downscaled to ~1 000 px / ≤120 KB, re-seeded
through `seed.sh` (which is idempotent on photos), a NEW `golden-demo.dump`
regenerated by the documented procedure, and the before/after transfer
measured with `scripts/medir-carga.mjs`. Own photography (FOTO-PENDIENTE)
would close it better, and is Miguel's call.

### OPS-31 — Indexation steps that need Miguel's Google account (Search Console, GBP)

- **Origin:** FRENTE-COMERCIAL-S1 (2026-08-26), Part A.1. An external SEO
  critique found neither `appximo.com` nor `appximo.github.io/appximo` in
  any query, including hyper-specific ones; a WebSearch from the dev box on
  2026-08-26 confirmed it (the "appximo" SERP is owned by Appium/AppSumo/
  Apimo and a typo-squatter page that reports the domain as "expired one or
  more times before"; the Wayback Machine has **zero** captures of the
  domain, so that claim cannot be checked).
- **What the session could do, and did:** verified there is NO `noindex`,
  `nofollow` or `X-Robots-Tag` on either property (the suspect #1 was
  innocent); added `robots.txt` (Allow all + Sitemap), `sitemap.xml`,
  `<link rel="canonical">`, `og:url`, `meta robots` and JSON-LD
  (`ProfessionalService` with phone + areaServed Pereira/Dosquebradas on the
  landing; `Service` on conjuntos) to both properties; created the first
  crawlable category-intent page (`conjuntos.html`, ~720 words of prose).
- **What needs the account (exact steps, not attempted):**
  1. Google Search Console → Add property → **Domain** `appximo.com` → verify
     by the DNS TXT record Google prints (Cloudflare DNS → Records → TXT `@`).
     Then Add property → **URL prefix** `https://appximo.github.io/appximo/`
     → verify by HTML tag (add the `<meta name="google-site-verification">`
     to `site/index.html`, publish to gh-pages) or by the HTML file (commit
     it to gh-pages root).
  2. In each property: Sitemaps → submit `https://appximo.com/sitemap.xml`
     and `https://appximo.github.io/appximo/sitemap.xml`.
  3. URL Inspection → paste `https://appximo.com/`, `/conjuntos.html`,
     `/caso.html` and the site URL → **Request indexing** for each (one at
     a time; the quota is ~10/day).
  4. Pages report (Indexing → Pages) after 3–7 days: every URL must show
     "Indexed"; anything under "Discovered – currently not indexed" is the
     new-domain crawl budget, re-request weekly.
  5. Google Business Profile → business.google.com → create "Appximo"
     (category: *Software company* / *Custom software development*),
     service area Pereira + Dosquebradas, phone +57 311 517 5472, website
     appximo.com; verification is by postcard/phone/video as Google decides.
     This is the local-intent lever the critique ranks above everything
     else for "desarrollo de software a la medida Pereira".
  6. Bing Webmaster Tools → import from Search Console (one click; Bing
     feeds DuckDuckGo and ChatGPT search).
- **Ready when:** both properties show the sitemap "Success" and the three
  landing URLs "Indexed" in Search Console, and `site:appximo.com` returns
  them.

### OPS-32 — The subordinate secondary path has nowhere to deliver an email

- **Origin:** FRENTE-COMERCIAL-S1 A.3.3. The research prescribes a
  low-friction secondary CTA (PDF of the case in exchange for an email)
  visually subordinate to WhatsApp. The landing is static GitHub Pages: no
  endpoint receives a form, and the engine's demos are not the landing's
  origin. The session shipped the OTHER documented secondary — "mire primero
  un sistema funcionando, sin registrarse" (a text link under the hero CTA
  → `#pruebe`) — and no email capture.
- **Options (decision is Miguel's):** (a) a hosted form (Tally/Formspree
  free tier: ~5 min, an account in Miguel's name, the email lands in his
  inbox); (b) a `Route.Public` on one of the 58's engines
  (`POST /api/leads` with `Route.RateLimit`, CORS to appximo.com) — ~1 h,
  owned, but couples the landing to a demo box; (c) a `mailto:` link
  (zero infra, fewer conversions, no PDF). The PDF itself (the case in
  Spanish) does not exist yet either.
- **Ready when:** a visitor can leave an email and receives the case, and
  the address lands somewhere Miguel reads.

### DOC-3 — The batch/N+1 patterns are in backend-spec but not where an agent hits the problem

- **Origin:** atina evaluation, finding 2 (FIELD_FEEDBACK_RESPONSE §4). The
  builder's agent wrote a row-by-row recompute (~1,300 round trips), then
  rediscovered `= ANY($1)` + `unnest()` on its own — the exact §3.4b of
  backend-spec, which it did not open because it was not looking for a
  section called "batch patterns".
- **Disposition:** put a one-line pointer + the N+1 warning at the `Ctx`
  reference entries for `Query`, `Get`, `Insert` and `Update` (the place the
  agent IS when it writes the loop), and in the `UnsafeTx` callout. Docs
  only; the embedded spec changes → `spec_test`/build re-run.
- **Ready when:** the four `Ctx` entries carry the pointer and a fresh-agent
  run with a recompute task reaches §3.4b without being told.

### OPS-30 — A CLI token with an empty tenant is a cross-tenant wildcard on the data plane

- **Origin:** FRESH-AGENT-GAPS-S1 (2026-08-21), surfaced by the Part-A
  implicit-requirement class audit, **hand-verified live**.
- **What:** `pkg/auth/middleware.go` skips the tenant-match check when the
  token's `TenantID` is empty (`claims.TenantID != "" && claims.TenantID != tc.ID`).
  So `appximo token --role admin` **without `--tenant`** mints a token that
  authenticates against EVERY tenant, not one. Verified: an empty-tenant admin
  token answered `GET /api/tasks` 200 and `POST /api/tasks` 201 against a
  tenant it was never scoped to.
- **Severity, honestly:** minting the token needs the `JWT_SECRET`, and anyone
  with the secret can already sign a token for any named tenant — so this is
  **not** privilege escalation for an outsider. It is an operator footgun: the
  easy-to-forget flag produces an all-tenants key instead of a one-tenant key,
  and if that key leaks it is a wildcard rather than a single-tenant token.
- **Shipped this session (the CLI half):** `appximo token` now prints a loud
  WARNING when `--tenant` is empty, naming the wildcard behavior and the fix.
- **Deferred (the engine half), with reason:** the empty-`TenantID` skip is on
  the auth HOT PATH and has a legitimate neighbour (platform tokens carry
  `scope=platform` + no tenant and are handled by a separate `/admin` chain).
  Making the data plane reject an empty-tenant token needs a careful pass over
  every token issuer (worker service JWTs, platform tokens, MFA-pending
  tokens) to confirm none legitimately reaches `/api` with an empty tenant —
  a security change that earns its own session with the gate + bench, not a
  rushed one-liner here.
- **Ready when:** the data plane refuses a token whose tenant does not match
  the host (empty included), verified against every issuer, through the
  binary-diff gate + ABBA bench.

### ENG-45 — The implicit-requirement audit inventory (schema validates, breaks at runtime)

- **Origin:** FRESH-AGENT-GAPS-S1 (2026-08-21), Part A class audit — 27
  findings, 22 silent. SILENT-CORRUPTION-S1 (2026-08-21) re-audited the two
  worst FAMILIES with two fresh sweeps (literal-English-name-bound behavior:
  18 findings; map-iteration/boot-order non-determinism: 10 findings) and
  closed them — see its DONE section. The doctrine stands (C-DOCTRINA): each
  family is closed by a LOAD-TIME check (error, or a SCHEMA-5 warning when
  sometimes legal) or documented — **never** by the engine filling values in.
- **Priority criterion (SILENT-CORRUPTION-S1, written):** silent corruption >
  non-determinism > loud failure > friction. The list below is ordered by it.
- **CLOSED by SILENT-CORRUPTION-S1** (detail in its DONE section): the
  name-bound `auto` family (auto:"create"/"update" roles + `auto_requires_time`
  + `invalid_auto` errors + `auto_update_intent` warning + one refresh source
  `schema.AutoRefreshColumns` consumed by REST/batch/Ctx.Update); the relation
  subroute collision (`relation_subroute_collision` load error, single source
  `schema.RelationSubroute`); field/relation named `id`
  (`reserved_field_name`); custom-route writes now invalidate the response
  cache; validation-error order, file-policy 422 order, filter/aggregate/
  reject-list first-error — all deterministic; the GraphQL self-shadowed list
  query warns (`graphql_list_query_shadowed`); OpenAPI marks auto fields
  `readOnly` + `x-appximo-auto` and /app + both backoffice contract.js read it
  from the contract.
- **CLOSED by WRITE-ASYMMETRY-S1 (2026-08-21):** family 1 — create accepted a
  forged `id`/`auto` value with 201 (REST POST, batch create, Ctx.Insert) while
  update answered 422 `read_only` and GraphQL rejected structurally. Closed as
  a class with ONE source (`schema.GovernedFieldViolations` +
  `IsGovernedWriteField`) consumed by every door: PrepareCreate (REST/batch/
  Ctx.Insert), the GraphQL create resolver, CollectUpdate (REST PUT/PATCH,
  GraphQL update, batch update) and PrepareUpdate (Ctx.Update — which used to
  pass `{"id":…}` through as `SET id = …`, a PK rewrite; that id/auto half of
  family 3 is closed too). The import use case became DECLARABLE (doctrine
  C-DOCTRINA-3): a resource-level `"import": {"roles": […], "fields": […]}`
  permits exactly the enumerated roles to supply governed fields ON CREATE
  (never update), load-validated (`import_roles_required` /
  `import_unknown_role` / `import_unknown_field` / `import_fields_empty`),
  published as `x-appximo-import` (fields only — role names stay
  unpublished, the ENG-27 asymmetry), authored in Studio, rendered by
  `explain`, taught by the grammar, and honored hot from the deployed surface.
  The bench-fixture restore path is now the declared one (erp-demo `empleados`
  grants `rrhh-admin`; README documents it; proven live on nimbus). Update-side
  messages stay byte-compatible; anti-divergence tests pin every door
  (governed_divergence_test.go + governed_write_integration_test.go). Gate:
  126 SAME + 3 intentional DIFFs (the fix itself, corpus rows added); ABBA
  no_change on POST and PATCH protocols.
- **CLOSED by MOTOR-AUTORIZACION-S1 (2026-08-27):** family 1 — the row
  give-away on update. Audited first (docs/audits/AUTHZ_WRITE_AUDIT_S1.md: 61
  real requests × 3 binaries, every write door, every RBAC form; exploitable
  in v0.1.8 and v0.1.9), then closed as ONE policy (ADR-027,
  `codegen.EnforceUpdateRBAC` beside the create half in
  `pkg/codegen/rbac_write.go`): an identity-bound condition column is
  server-owned on update — another id / null → the same 403 as create; a
  PUT that omits it keeps the caller; own id re-sent is a no-op; judged on
  the body before the row lookup and before the allowlist; a literal
  condition stays a visibility filter. All-doors test
  `ownership_update_integration_test.go`; 11 gate rows. The audit ALSO closed
  the state-null 500 (a state field set to null / omitted on PUT → named
  422, `codegen.StateFieldNullViolations`) and re-ranked #3 below.
- **The families still OPEN, by damage:**
  1. ~~the row give-away~~ — **closed above.**
  2. **(silent corruption) `files`-resource shadowing:** the validator allows a
     schema resource named `files` but `migration.isEngineManagedTable`
     excludes the table from every diff — never provisioned, never converged —
     and a `file` field's FK assumes the engine's metadata table.
     **Disposition: close at load** (reject the shadow when any `file` field
     exists; provision the declared table otherwise) — or reject the shadow
     outright.
  3. **(loud failure) Ctx.Update remaining parity gap:** a null on a required
     field surfaces a raw 23502 as a 500 (CollectUpdate answers a clean 422
     `required`). The id/auto half of this family was closed by
     WRITE-ASYMMETRY-S1 (PrepareUpdate now runs the governed pass); the
     state-field-null half by MOTOR-AUTORIZACION-S1 (PrepareUpdate now runs
     `StateFieldNullViolations`). What remains is exactly the
     required-null: **Disposition: add the null-required check to
     PrepareUpdate — the CTX_PARITY fix pattern — 5 lines + the row in
     ownership_update_integration_test.go's door table; kept out of the
     security session on purpose (functional, no client can escalate with a
     500).**
  4. **(silent no-op) Declared-relation pieces don't line up:** `fk`, m2m
     `through` and `target_fk` are not checked to exist ✓ — fails at
     `?include=`/GraphQL time. **Disposition: SCHEMA-5 warning** (a column can
     be hot-added; the index-existence check honors the same escape).
  5. **(silent no-op → now warned) GraphQL naming:** the self-singular list
     shadow now WARNS (`graphql_list_query_shadowed`); the real fix (a
     collision-free get-by-id name, e.g. `<name>ById`) is a GraphQL contract
     break — **disposition: own increment, with a migration note**. Also still
     open: a required field WITH a default is `NonNull` in the create input
     where REST allows omission; Spanish plural mis-singularization
     (`clases`→`Clas`) is cosmetic. **Disposition: reconcile or document per
     case.**
  6. **(friction/cosmetic — EXCEPT the first, which the MOTOR-AUTORIZACION-S1
     audit re-reads as security-adjacent) Runtime-config assumptions**
     (documented today): `hmac_secret_env` unset signs with the EMPTY key — a
     receiver verifying `X-Appximo-Signature` accepts a payload anyone can
     sign, so the webhook's authenticity guarantee is silently void; a
     `wasm` hook naming an unloadable module; an `events` list with no
     consumer; `$external_client_id` never populated. **Disposition: the
     empty-key case becomes a boot-time WARNING naming the hook and the
     variable (or a load error when the env var is declared and empty) in
     the next engine increment; the rest stay candidate warnings.**
  7. **(cosmetic) OpenAPI:** auto fields in responses now carry `readOnly`;
     what remains is documenting the refresh semantics in the description
     text. **Disposition: documentation.**
- **Ready when:** every family above is either a load-time check with an
  actionable message or a documented deliberate-restart/limitation note — none
  left as a runtime-only surprise.


### OPS-29 — Releases lag the tags: v0.1.8's release run failed, and the Docker badge shows a SHA

- **Origin:** SHOWHN-MATERIAL-S1 (2026-08-19), pre-launch verification sweep.
- **What:** the tag `v0.1.8` exists (2026-08-17, commit 97480d1) but its
  Release workflow failed at the **"Create GitHub Release"** step (binaries
  built fine) — so the Releases page, the version-less download aliases and
  `appximo upgrade` all serve **v0.1.7**, while `go get @latest` and the tag
  list say **v0.1.8**. A launch-day visitor who checks tags sees the skew.
  Related cosmetic finding: the README's Docker badge renders the image tag
  `a7689d7` (a commit SHA) instead of a version, because Docker Hub carries
  no semver tags — only `latest` + per-commit SHAs.
- **Impact:** low functionally (every path works; they just disagree on
  "latest"), but it is exactly the kind of inconsistency an HN thread finds
  in minutes.
- **PRELAUNCH-TRUTH-S1 (2026-08-19) — cause narrowed, workflow hardened,
  four of five surfaces verified; the re-run remains the owner's:**
  - The failing step died in **~1 s** (18:32:28→29, per the public jobs API),
    BEFORE any asset upload (v0.1.7's same step took 10 s uploading), with
    release.yml unchanged across 7 straight successes. Two causes match that
    exact signature: a pre-existing release/**draft** for v0.1.8 (drafts are
    invisible anonymously but block `gh release create`) or a transient API
    error on the create call. The literal error line needs a logged-in look
    at the run — one click for the owner.
  - **release.yml hardened:** the create step is now idempotent (release
    exists in any state → upload `--clobber` + publish; else create with one
    30 s retry), so a re-run converges instead of failing on its own partial
    success. YAML validated; workflow-only change, zero engine code.
  - Surfaces verified separately: tags `v0.1.8` ✓ · `go get @latest` →
    v0.1.8 ✓ · version-less aliases → v0.1.7, checksum OK ✓ (they follow the
    release) · **`appximo upgrade` tested end to end from a real v0.1.6
    install** (detects v0.1.7, downloads via alias, checksum ok,
    self-replaces, exit 0) · Releases page → v0.1.7 ⚠ pending. Bonus: Docker
    was ALREADY coherent (`neodevtrix/appximo:v0.1.8` published 08-17) and
    the README badge now uses `sort=semver`, so it shows the version, not a
    SHA.
- **Ready when:** a release exists for the newest tag. The owner's exact
  path: (1) check https://github.com/appximo/appximo/releases logged-in for
  a stale v0.1.8 DRAFT and delete it if present (the re-run uses the OLD
  workflow, which dies against a draft); (2) "Re-run failed jobs" on run
  32055036588 (or `gh run rerun 32055036588 --failed`); (3) verify
  `releases/latest` redirects to v0.1.8. Alternative: the next tag
  (`git tag v0.1.9 && git push --tags`) flows through the hardened workflow
  — sessions do not tag.

### OPS-27 — crisblogs demo-account buttons fail, and the box is not ours

- **Origin:** LANDING-COMMERCIAL-S1 field note (2026-08-17); investigated by
  DEMO-SHOWCASE-S1 (2026-08-18).
- **What:** the editor/lector demo buttons on `crisblogs.appximo.com` answer
  «Correo o contraseña incorrectos» — the public third-party showcase does not
  let a visitor in. The technical site links the blog (which works, 200); the
  broken part is inside crisblogs' own login.
- **Why this session could NOT fix it:** `crisblogs.appximo.com` resolves to
  **147.182.163.170** — NOT the 58 (tiendita/petfriendly live there; verified
  against the 58's Caddyfile). It is the third-party evaluator's own droplet;
  no SSH key on the dev box opens it and no admin key is on file. Restoring
  the demo users requires whoever operates that droplet (or Miguel asking
  them) — `POST /admin/tenants/{id}/users` with their ADMIN_KEY, or a seed
  re-run.
- **Impact:** a prospect who clicks the demo buttons on a public showcase hits
  a dead end. **SHOWCASE-TRUTH-S1 (2026-08-18) re-verified from a clean
  browser and hardened the finding:** the buttons fill
  `editor@crisblogs.local` / `lector@…` and the server answers 401 for BOTH;
  crisblogs declares NO `rbac.public` block (its /openapi.json carries zero
  `x-public` operations), so there is no anonymous reading path either — a
  stranger cannot see a single article. Audit verdict: **ROTA** as a showcase
  (alive, valid TLS, mobile-clean — but nothing behind the door). One
  leverage fact: the box is the evaluator's (147.182.163.170), but
  `crisblogs.appximo.com` is OUR DNS zone — we can repoint the subdomain
  without touching their machine.
- **The three options (measured, decision is Miguel's — recorded, not taken):**
  (a) **repair** — ask the crisblogs operator to re-seed the two demo users
  (`POST /admin/tenants/{id}/users` with their ADMIN_KEY, or their seed);
  minutes of THEIR time, zero of ours, but stays hostage to a third-party box
  for the landing's credibility. (b) **replace** — stand a fourth app on the
  58 (it holds ~760 rps with two apps; a blog/portal is static-light) and
  repoint the subdomain we already control; ~a session of work, full control,
  but the "built by a third party" credential dilutes unless the case study
  stays linked as the story. (c) **discount it** — the trust bar publishes
  the number without crisblogs until (a) or (b) lands; zero work, honest
  today.
- **LINKABLE-TRUTH-S1 (2026-08-19):** the DEAD LINKS are gone from every
  public surface — README (×2), the technical site, the case study — while
  the third-party build STORY stays everywhere (that is what crisblogs
  proves, and it survives without a URL). The landing's quantity claim now
  says only what survives a click ("4 construidos, 2 abiertos para probar").
  The app itself is unchanged and still broken for a visitor.
- **Ready when:** the two demo accounts log in from the buttons, or the
  buttons are removed from crisblogs' login, or the subdomain points at a
  replacement we operate. Needs: Miguel's call between (a)/(b)/(c).

- **Update (CENTRO-MANDO-S2, 2026-09-17):** the box is now operated (see
  OPS-51's update — retotr-prod, panel key authorized), so option (a)
  re-seeding the two demo users no longer depends on a third party; the
  choice is now simply fix-or-retire, executable in-house.

### OPS-23 — `install.sh` has no `--static` flag for a frontend directory
- **Origin:** LAUNCHPAD-S1, second fresh-agent run. An agent deploying an app
  with its own SPA (served by the stock binary, not a consumer build) has to
  hand-add `APPXIMO_STATIC_DIR=` to `/etc/<app>/<app>.env` after the install,
  because the installer exposes no `--static`/`--spa` flag even though
  `serve` and `ParseServeArgs` both do. It cost a two-minute detour, not a
  wall (the env equivalent IS documented in `serve --help`), but it is the
  one place where the deploy path is narrower than the run path.
- **Impact:** Low-medium. Self-solvable and documented, yet it breaks the
  "the installer prints every name it created; don't re-derive paths" promise
  for the most common frontend case.
- **Ready:** `install.sh --static=<dir> [--spa]` copies the directory under
  `/opt/<app>/web`, writes `APPXIMO_STATIC_DIR`/`APPXIMO_STATIC_SPA` into the
  env file, and the summary names the served root — verified by installing an
  app with a SPA and getting a 200 at `/` with no manual env edit.

### ENG-44 — `/health` and `/healthz` share the per-tenant rate-limit bucket
- **Origin:** LAUNCH-ASSETS-S1, measured on the production demo box. During the
  deliberate 1,200 rps saturation probe (tenant limiter at the default 1000),
  the health canary probing `https://tiendita…/health` was answered **429**
  twice — the health endpoints count against the same per-tenant token bucket
  as data traffic, so under exactly the load where an operator most wants a
  health verdict, the probe reads as failure. Box-local probes (`127.0.0.1`,
  no tenant Host) and the systemd/Caddy checks are unaffected, which is why
  this never surfaced before.
- **Impact:** Low-medium. External uptime monitors pointed at a tenant domain
  will report an outage during a traffic spike that the engine is actually
  shedding correctly (429 ≠ down; recovery was instant when load stopped).
- **Ready:** `/healthz` and `/health` (constant-time, no tenant work) are
  exempted from the per-tenant limiter — or the docs state plainly that
  external monitors must probe a non-tenant host / the box directly. Either
  closes it; the exemption is the better product.

### ENG-41 — the create path validates `required` before RBAC fills the ownership column
- **Origin:** LAUNCHPAD-S1. `ValidateWrite` (required/rules) runs at
  builder.go:561, `EnforceCreateRBAC` (which forces the row-condition column
  to the caller's identity) at :609 — so a column that is BOTH `required` and
  identity-scoped answers 422 "<field> is required" on every create by that
  role, for a value the client was never meant to send.
- **Impact:** Medium, and now WARNED rather than silent: this session shipped
  the SCHEMA-5 warning `required_field_is_rbac_forced`, which names the
  symptom and the fix (drop `required`). The warning closes the discovery
  problem; this item tracks the deeper fix.
- **Why deferred:** reordering the two phases is a real data-path change with
  a non-obvious side effect — EnforceCreateRBAC also DROPS fields outside the
  role's allowlist, so running it first would remove those fields from
  validation, turning today's 422 on a badly-typed non-allowlisted field into
  a silent 201. That trade needs its own session with the binary-diff gate,
  not a tail-end edit.
- **Ready:** either (a) the injection of condition VALUES moves before
  validation while the allowlist DROP stays after it, with the gate showing
  no other behavioral change, or (b) an ADR records that the warning is the
  permanent answer and why.

### ENG-35 — GraphQL String-typed variables silently coerce any scalar
- **Origin:** NIGHT-SWEEP-S1 audit (GraphQL surface), CONFIRMED adversarially.
  A `query($t: String!)` executed with `variables: {"t": 5}` runs — the library
  coerces the Int to `"5"` instead of raising the spec-required request error —
  while Int-typed variables ARE strict (a string is rejected). Asymmetric
  coercion inside graphql-go, not our resolvers.
- **Impact:** Medium. A client bug (wrong variable type) executes with a
  stringified value instead of failing loudly; the same bug on an Int variable
  fails correctly. Same family as ENG-22 (GraphQL value handling) — fix them in
  one GraphQL-coercion pass.
- **Why deferred:** changing variable coercion is a library-level contract wider
  than a sweep session (the audit's own precedent: "GraphQL argument coercion
  changes a contract wider than one release").
- **Ready:** a wrongly-typed variable for ANY declared type is a request error
  naming the variable and both types, or the tolerance is documented per type.

### OPS-16 — Small named-rejection residue from the NIGHT-SWEEP audit
- **Origin:** NIGHT-SWEEP-S1 audit, all LOW, verified live, none silent-harmful
  enough to fix at 4 a.m.:
  (a) fleet manifest unknown-key error surfaces the raw decoder message (names
  the field, lists no valid keys); (b) control-plane `plan` accepts any value
  and is read by nothing — an enumerated-looking field with no set (needs a
  product decision on what plan means); (c) trailing data after the first JSON
  value is ignored on /auth bodies (`{}garbage` parses — needs a dec.More()
  check per body or a written exception); (d) MFA session endpoints answer
  "authentication required" to a VALID engine token that carries no user
  identity (message could name the real problem); (e) a tx create HONORS a
  caller-supplied `id` in data (echoed — reported tolerance) while update's own
  error calls id read_only — decide and write the create-id contract.
- **Impact:** Low each; they are message-quality/contract-wording items, not
  silences (everything silent got fixed in the session).
- **Ready:** each sub-item either fixed with a test or written down as an
  ADR-024 exception with its reason.

### ENG-1 — Embed cache for `?include=` relations
- **Origin:** ADR-019 §cache invalidation; carried in ESTADO_Y_PLAN_MAESTRO as
  "the only piece of relations not implemented".
- **Impact:** Medium. A nested read runs its LATERAL every time. Correct, and
  measured at +0.01 ms p50 for ~15 children, so this is throughput headroom on
  read-heavy embeds, not a defect.
- **Ready:** an embed result is cached per (tenant, parent-set, role) and
  INVALIDATED by a write to either the parent or the child resource; a stale
  embed is impossible after a write in the same tenant; benchmarked `no_change`
  on the non-embed path and a measured win on the embed path.

### ENG-2 — Zero-downtime binary upgrade
- **Origin:** PROD_PATH_AUDIT §1.2 ("Zero-downtime (binario) — no existe").
- **Impact:** Medium. `deploy-update.sh` does SIGTERM → wait → start, measured at
  **0.47 s** of 502s under live traffic (docs/BENCHMARKS.md). Acceptable for a
  single box; visible for an API with a strict SLO.
- **Consumer numbers (re-measured in CONSUMER-PATH-S1, 2026-07-31):** with the
  reworked deploy-update.sh (250 ms health polling; crash-loop detection via
  NRestarts — `Restart=always` never reaches systemd's "failed" state; the
  rollback branch VERIFIES recovery), a normal deploy costs **0.28 s of 502s
  (15/3001 at 50 rps)** — was 0.58 s — and a BROKEN deploy rolls back verified
  in **17 s end-to-end with 10.75 s user-visible** — was 35 s / 30.7 s. The
  cheap tooling wins are done; what remains is the actual zero-downtime work.
- **Ready:** a deploy costs **zero** failed requests under 500 rps, verified by
  the chaos suite. Candidates: socket hand-off (`SO_REUSEPORT`) between old and
  new process, or a symlink + blue/green pair behind the existing `/readyz` drain.

### OPS-43 — Per-tenant selective restore (ENG-3's last third)
- **Origin:** ENG-3 (PROD_PATH_AUDIT §1.5 / §2.8), narrowed by RESILIENCIA-S1
  (2026-08-30), which made the rest DONE: `scripts/restore.sh` (timed, verified
  against the manifest), the nightly timer written by `install.sh`, the set
  with uploads + secrets + manifest, the off-box copy.
- **Impact:** Low-Medium. `restore.sh` is a FULL replace — every tenant goes
  back to the set's instant. Restoring ONE tenant that deleted its own data
  while the others keep working is a manual path today (`pg_restore
  --schema=tenant_x` into a scratch database, then copy).
- **Ready:** `restore.sh --tenant=X` (or `appximo restore`) that restores one
  tenant schema from a set into the live database under the tenant's advisory
  lock, verified against the manifest's rows for that schema; and the drill in
  `scripts/verify-production/`.

### ENG-58 — The graceful stop always waits the full 5 s drain, so a restore/restart pays 5 s even with zero in-flight requests
- **Origin:** RESILIENCIA-S1 scenario 1 timings: `systemctl stop` = 5.0 s of
  the 13.6 s restore; `app.go` passes a fixed `5*time.Second` to
  `shutdown.Serve` (the LB-drain delay: `/readyz`→503, then sleep, then
  `Shutdown`).
- **Impact:** Low. Every restart (deploy, restore, the editor's self-restart)
  costs 5 s of unavailability behind Caddy by design, because the delay is
  what lets a load balancer notice the 503 before the listener closes — on a
  single box with Caddy retrying the upstream, the useful part of that wait is
  the time in-flight requests need, which is milliseconds.
- **Ready:** `APPXIMO_SHUTDOWN_DRAIN` (default 5 s, the installer may set 1 s
  on a single-box layout) + drain that ends EARLY when in-flight reaches zero,
  measured with `deploy-update.sh` under a probe (B1's 2.8 s crash-restart
  is the floor a graceful restart should approach, not exceed).

### ENG-4 — OTLP / OpenTelemetry export
- **Origin:** README "Known limits"; docs/CAPABILITIES.md.
- **Impact:** Low for the target user (the in-binary observability is the
  differentiator — ADR-020), blocking for anyone standardized on OTel.
- **Ready:** an opt-in exporter behind a build tag or config, with the hot path
  measured `no_change` when it is off (the reason it has not shipped: the SDK's
  weight must not reach the request path by default).


### MIG-1 — A gin index's `opclass` change is a silent no-op
- **Origin:** AUTHORING-GAPS-S1, the ENG-13 class audit
  ([docs/audits/MIGRATION_HONESTY_AUDIT.md](audits/MIGRATION_HONESTY_AUDIT.md)
  finding "Left open"). An index's `opclass` (`jsonb_ops` → `jsonb_path_ops`) is
  DELIBERATELY excluded from the diff key because the introspector cannot read one
  back, so declaring a different opclass on an EXISTING index does nothing — and,
  like ENG-13, says nothing.
- **Impact:** Low and narrow (one optional key on gin indexes only), but it is the
  last known member of the "declared ≠ applied, silently" class the session closed.
  The post-apply verification cannot catch it: the desired model has no opclass to
  compare against.
- **Ready:** either the declared opclass is recorded (a comment on the index, or a
  side table) so a change can be detected and applied as drop+recreate, or the
  validator REFUSES to change the opclass of an index that already exists, naming
  the manual `DROP INDEX` + re-apply. Silence is the one option ruled out.

### MIG-2 — A `schema_history` append failure is log-only
- **Origin:** AUTHORING-GAPS-S1, the ENG-13 class audit. `schemahistory.Append` is
  best-effort at every call site: on failure the deploy proceeds and a WARNING goes
  to the log, so the version trail silently gains a gap. `EnsureSeeded` (this
  session) reduces the blast radius — the schema being REPLACED is now always
  recorded first — but the write itself still has no signal to the caller.
- **Impact:** Low today. The trail is used by ENG-9's ownership classifier and by
  Studio's History/rollback; a gap degrades a dry-run's drop classification and
  removes a rollback target, both quietly.
- **Ready:** the deploy response carries a `history_warning` when the append fails
  (the deploy still succeeds — the DDL is applied and the record is authoritative),
  and Studio's History view shows it. Not an error: failing a correct deploy over a
  bookkeeping row would be worse than the gap.

### OPS-11 — `install.sh --app` has not been run on a real multi-app box
- **Origin:** AUTHORING-GAPS-S1. The two-app isolation, the refuse-to-clobber guard
  and the per-app Caddy site were verified in staged `--dry-run --root` mode. The
  live path — systemd units, the postgres role/database per app, and above all the
  **migration of an existing monolithic `/etc/caddy/Caddyfile` to `import
  sites/*.caddy`** — has not run on a real box.
- **Impact:** Medium. The migration only strips the block for the domain being
  installed and backs the file up first, so the failure mode is "Caddy refuses to
  reload", not data loss — but it would be an outage on a box serving live apps.
- **Ready:** install a third, ephemeral app on the 58 with `--app`, confirm the
  tienda and petfriendly do not blink (a purchase + a CRUD call through each), then
  `--uninstall --app=<that app>` and confirm the two survivors are untouched.
- **Update (MIGRACION-CONFIANZA-S1, 2026-08-28):** the multi-app path WAS run for
  real — two apps (`vecingo`, `retotr`) side by side in an LXD Ubuntu 22.04
  container with native Postgres + Caddy + systemd, clean install / upgrade in
  place / foreign schema / port collision, all verified — so the "never run
  live" part now applies only to the 58 itself (its pre-OPS-10 inline Caddyfile
  migration). The container's Caddyfile was the OPS-10 layout from the start,
  so the inline-block migration remains unexercised.


### OPS-12 — The NestJS comparative benchmark cannot be re-run
- **Origin:** CERTIFY-S1. The harness survives (`benchmark-lab/nestjs-baseline/`
  with `dist/` + `node_modules`, plus `/root/archives-58/nestjs-bench-58-20260731.tgz`),
  but the published measurement's conditions are gone: the 58 has no Node, no pm2 and
  no Docker (so no 0.5-vCPU PostgreSQL cap, which was a declared, symmetric condition
  of the original), and it now serves two production apps.
- **Impact:** **High for publication.** "~4.8× faster than NestJS" is the most
  quotable claim the project has, and it is currently unverifiable. It is marked as
  historical (2026-06-10) in the README and the benchmark doc.
- **Ready:** a dedicated, disposable SUT with both stacks installed and PostgreSQL
  constrained identically for both arms, run through the existing ABBA protocol —
  then the claim is restored with a fresh date, or dropped.

### OPS-13 — Fifteen config values fall back silently when they fail to parse
- **Count corrected 2026-08-01** (was "nineteen"): a dedicated re-count found **15
  numeric env vars across 18 code sites**, and — the number that actually matters —
  **8 of them print nothing at all afterwards**. Those 8 are the fully silent class;
  the other 10 at least log the effective value, so an operator who reads the boot
  log can notice the discrepancy. Split the fix accordingly: the 8 silent ones first.
- **Origin:** SILENT-FAILURE-S1 audit + measured live. Booting with
  `RATE_LIMIT_RPS=abc RATE_LIMIT_BURST=oops` logs `rate limiter: 1000 RPS / 100 burst
  per tenant` and never says the operator's values were rejected. The same shape in
  `APPXIMO_AUTH_MIN_PASSWORD`, `APPXIMO_CONTROL_PORT`, `APPXIMO_FILES_MAX_BYTES`,
  `DB_MAX_CONNS`, `APPXIMO_MAX_TX_OPS`, the fleet's per-app `envInt`, and more.
  `envTruthy` maps ANY unrecognized value to false — including
  `APPXIMO_AUTH_REQUIRE_VERIFIED`, a security toggle. And there is **no inventory
  of the 60+ `APPXIMO_*` variables**, so a misspelled one is never read at all.
- **Impact:** Medium-high for operators: the box runs with a configuration nobody
  chose, and the only evidence is a log line stating the default as if it were the
  request.
- **Ready:** one `envInt`/`envBool`/`envDuration` helper that logs
  `WARNING: RATE_LIMIT_RPS="abc" is not a number — using 1000`, plus a boot-time
  inventory that warns on an unknown `APPXIMO_*` variable (the fleet already has
  the pattern for per-app keys).

### ENG-21 — No write body uses `DisallowUnknownFields`
- **Origin:** SILENT-FAILURE-S1 audit. The engine already uses it in `pkg/userauth`,
  `pkg/platformadmin` and `pkg/fleet` — the discipline exists and was never applied
  to the data plane. Unknown keys in the `/api/transaction` envelope, in an
  operation, and in a `guard` are dropped. Worse, the `422 unknown_field` guarantee
  on CREATE is not a key check at all — it is a side effect of Postgres `42703`, so
  it evaporates for a role with a `fields` allowlist (the key is deleted before the
  DB sees it) and for a drift column the additive migration left behind.
- **Impact:** Medium. SILENT-FAILURE-S1 fixed the operator bodies that carry a
  safety flag; the data-plane bodies are a wider contract change.
- **Narrowed by NIGHT-SWEEP-S1:** the DECLARED-but-irrelevant keys are no longer
  silent — `guard` on a create and `data` on a delete are named 400s, a tx
  create with a non-uuid id is the same 400 as single-op (was a masked 500), and
  the envelope was re-probed live: `{"atomic": false}` and a misspelled
  `"operation"` are still dropped (the remaining hole is exactly UNKNOWN keys).
- **Ready:** strict decode on the transaction envelope/op/guard, and a real key check
  on CREATE that does not depend on the database's error code.

### ENG-22 — GraphQL drops variables and nested jsonb values
- **Origin:** SILENT-FAILURE-S1 audit. `GET /graphql` never reads the `variables`
  query parameter, so a filtered query returns the UNFILTERED result. A variable
  nested inside a `jsonb` inline literal is written as `null`.
- **Impact:** Medium-high for the GET path (a filter that silently does not apply).
- **Re-verified by NIGHT-SWEEP-S1** (live, base binary): the GET `variables`
  drop reproduces exactly as filed. The audit also REFUTED a related claim (an
  unknown POST-body key like a misspelled `variables` — the GraphQL-over-HTTP
  envelope tolerance applies); and split off the String-variable coercion
  asymmetry as **ENG-35** (fix the two together — one GraphQL value-handling pass).
- **Ready:** parse `variables` on GET, or reject a GET carrying it; resolve variables
  inside jsonb literals or reject them.

### OPS-14 — `api.appximo.com` (the gold-path demo) is down: Cloudflare 525
- **Origin:** observed 2026-08-01 while checking server state at the end of
  SILENT-FAILURE-S1. NOT caused by this session — nothing was deployed to the 58.
- **Evidence:** `GET https://api.appximo.com/healthz` → **525** on 3/3 attempts,
  `server: cloudflare`. The host resolves to a **Cloudflare** address
  (`2606:4700:3030::6815:4d8c`), while the two live apps resolve **directly** to the
  origin (`the production VPS`) and both answer `/healthz` → **200**. A 525 is
  "Cloudflare could not complete the TLS handshake to the origin".
- **Impact:** the public demo URL from PROD-PATH-GOLD-S1 — the one that proved the
  official install path end to end with a real Let's Encrypt certificate — is dead
  from the internet, while the newer apps (added later with direct DNS) are fine.
  Anyone following that write-up hits an error page.
- **Likely cause, to confirm before touching anything:** the origin's Caddy no
  longer holds a certificate for `api.appximo.com` (the host was probably dropped
  from the Caddyfile when tiendita/petfriendly were set up with direct DNS), so
  Cloudflare's strict origin check fails. Alternatively the Cloudflare SSL mode was
  changed.
- **Ready:** either point the DNS record directly at the origin like the other two
  apps (matching what already works), or restore the origin certificate for that
  hostname and confirm `https://api.appximo.com/healthz` → 200 from outside.
  **Miguel's call** — it is also fair to retire the hostname if the demo has moved.


### SCHEMA-7 — Schema KEYS are strict; VALUES and key×type combinations are not
- **Origin:** SILENT-FAILURE-S1 audit. The strict-key claim was verified and HOLDS at
  all 17 levels. One level down it does not: `auto: true` silently discards the
  field's declared `type` and creates a TIMESTAMPTZ; `enum` on a non-string field
  loads and makes the field permanently unwritable; role-global `actions` are the
  only action list the meta-schema does not enumerate, so a typo becomes a permission
  that grants nothing; `hooks.<event>.timeout` is accepted at every layer and read by
  no code.
- **Impact:** Medium. Each is a schema that validates and then behaves differently
  from what it says.
- **Ready:** each combination validated at load with an error naming the conflict —
  the mechanism already exists (`validateFilterOp` is the model), it simply has not
  been applied to these pairs.

### SEC-5 — (details delivered to the maintainer directly)
- **Origin:** SILENT-FAILURE-S1 audit, verified live by the session.
- **Handling:** an exploitable finding, so per the session's rule the
  reproduction — and everything that would narrow it down — was reported
  directly to the maintainer and is deliberately NOT written in this file, in
  the audit document, or in any commit message (the class, scope and fix sketch
  live in the maintainer's internal handoff package). Ask Miguel for the detail
  before working on it.
- **Impact:** High.
- **Ready:** fixed per the privately-delivered description, with a regression
  test and a binary-diff-gate corpus row.

### SCHEMA-8 — Omit a declared-heavy field from collections by default (`"list": "on_request"`) — PROPOSED, not built
- **Origin:** MOTOR-FIELDS-S1 (ADR-029). The migration report's alternative to
  `?fields=` was "exclude large fields from collections by default". Rejected
  as a flipped default: it is a contract break (every client reading
  `row.data` from a list gets `undefined` after an upgrade, silently — the
  ADR-024 class) and "heavy" is not a type the engine can decide. `?fields=`
  ships instead (opt-in per request, no schema change).
- **The declared form, if a second app asks:** a per-field key
  `"data": {"type": "json", "list": "on_request"}` — list and subroute reads
  omit the field unless `fields=` names it; the detail keeps it;
  `/openapi.json` publishes `x-appximo-list: "on_request"` on the property so
  a generic client (the `/app`) knows why a column is absent; `validate`
  warns when a `json`/`jsonb`/`text` field without it sits on a resource the
  author marks as large. Opt-in per schema: the author breaks their own
  clients on purpose, at a version of their choosing, and the contract says it.
- **Impact:** low today — `?fields=` covers the migrated case and the `/app`
  uses it; a client that does not send `fields=` keeps paying the document.
- **Ready:** the key parsed/validated at load (strict-key, json/jsonb/text
  only), honoured by list/subroute/GraphQL-list/admin-browse, published in
  the contract, pinned by the gate; measured `no_change` on the plain list.

### SCHEMA-1 — Computed / derived fields
- **Origin:** docs/MODEL_LAB.md G7 ("order totals as a computed field").
- **Impact:** Medium. Totals, counts and balances are recomputed by the client or
  a handler; the aggregation endpoint (G3) covers the reporting case but not "a
  field on the row".
- **Ready:** a declarative computed field materializes as a generated column or a
  view-backed read, is validated at load (an expression grammar, not raw SQL —
  the same rule as SCHEMA-2), and never appears on the write path.

### SCHEMA-2 — Structured index predicates (partial indexes)
- **Origin:** commerce docs/GAPS.md #4; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 2**.
- **Impact:** Low-medium. A partial index today lives in the user's boot DDL
  (`Config.BeforeStart`), which works and is documented.
- **Ready:** a STRUCTURED predicate (`{"where": {"field","op","value"}}` — never
  raw SQL), validated at load, rendered through `pkg/schemadiff`, diffed by
  structure, and surviving an introspect→diff→apply cycle with an empty plan.

### SCHEMA-3 — Per-transition RBAC on state machines
- **Origin:** AGENTS.md §state machines "Out of scope (documented)"; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 3**.
- **Impact:** Medium for commerce/finance (who may refund vs who may ship). The
  documented pattern today — one custom route per privileged transition, granted
  by the `routes` block — covers it with a single authorization surface.
- **Ready:** enforced identically on REST, GraphQL **and** inside
  `POST /api/transaction`, expressible in Studio's state-machine designer and in
  the LLM grammar, benchmarked `no_change` on the update path.
- **Evidence from 1-B (2026-07-30):** the pattern carried the real case
  (empleado despacha / solo dueño reembolsa) but cost three things — `empleado`
  lost `update` on `ordenes` entirely, the transition table is re-stated in Go,
  and two authorization surfaces must stay in sync. See commerce
  `docs/GAPS.md` 1B-4 and **ENG-7** (the cheap fix that keeps this deferred).

### SCHEMA-4 — GraphQL keyset pagination
- **Origin:** docs/SCHEMA_REFERENCE.md §GraphQL ("no keyset cursors — a
  documented future increment").
- **Impact:** Low-medium. GraphQL forwards only `page`/`per_page` (OFFSET) while
  REST has keyset; a deep GraphQL page is slower than the same REST page.
- **Ready:** `after`/`before` on the GraphQL list args, mapped to the same keyset
  builder REST uses, with the offset path kept for compatibility.

### RBAC-1 — Read-scoped row-condition operators (`in`, `neq`, `is_null`)
- **Origin:** docs/SCHEMA_REFERENCE.md §7.7; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 1** — `eq`
  stays the only operator because the create path FORCES the condition value
  (the mass-assignment block), which only equality can mean.
- **Impact:** Low. Per-resource `permissions` and custom routes cover the cases
  seen so far.
- **Ready:** a real case that neither covers; then `in`/`neq`/`is_null` permitted
  **only** on actions listed in `condition_actions` that exclude `create`,
  type-validated at load, with negative tests and an ABBA benchmark.

### OPS-6 — Orphan ufw ALLOW rules on the 105 (2375/2376 = Docker API without TLS)
- **Origin:** LIBRARY-GAPS-S2 Parte 0, the port-exposure review. `ufw status`
  on the 105 shows ALLOW rules for **2375/tcp and 2376/tcp** (the Docker daemon
  API — 2375 is the UNENCRYPTED variant, root-equivalent if anything ever
  listens there), plus 5678 and 8000. **Nothing listens on any of them today**,
  so they are landmines, not active holes. Deliberately NOT touched this
  session: they may belong to a workflow of Miguel's (a remote docker context,
  n8n on 5678?), and firewall edits on a box he works on are his call.
- **Impact:** Low today, high the day something binds those ports. The rest of
  the review landed: commerce now binds 127.0.0.1 (data + control plane) via
  the new `Config.Host`/`ControlHost`, and ufw's default-deny covers
  8099/9099/3099.
- **Ready:** Miguel confirms whether anything needs 2375/2376/5678/8000; the
  unneeded rules are deleted; `ufw status` on the 105 is re-verified and
  recorded in `05_SERVIDORES_Y_OPERATIVA.md`.

### OPS-1 — A measurement box without CPU steal
- **Origin:** ESTADO_Y_PLAN_MAESTRO "DEUDA TÉCNICA CONOCIDA"; reconfirmed by
  LIBRARY-GAPS-S1 (a base-vs-base control measured a **±8 %** noise floor on the
  105) and again by this session.
- **Impact:** Medium for engineering confidence. Fine deltas (a few ns on the
  RBAC path) are unmeasurable here; the ABBA + control protocol works around it
  but costs a full extra arm every time.
- **Ready:** a dedicated box (or a pinned/isolated cgroup) where a base-vs-base
  control lands under ±2 %, wired into the bench protocol.

### OPS-2 — Statistical benchmark gate in CI
- **Origin:** ESTADO_Y_PLAN_MAESTRO FASE 4.
- **Impact:** Medium. Regressions are caught by hand today, per session.
- **Ready:** a push runs the protocol against a stable baseline and fails on a
  verdict of `CHANGED` beyond max(0.5 ms, 3 %). Depends on OPS-1.


### OPS-4 — The deployed binary is not traceable to a commit
- **Origin:** this session, while bootstrapping the admin account on the 58.
  `/health` reports `"version":"dev"` and `appximo version` says
  `commit unknown` — the live binary was built without the ldflags the canonical
  build (`scripts/build-engine.sh`) injects.
- **Impact:** Medium for operations. Nobody can tell from outside WHICH build is
  serving `api.appximo.com`, and the DevHub deploy pipeline's smoke check
  (`/health` version == pushed SHA) cannot pass, so a deploy's own verification
  step is degraded. It also makes a rollback decision guesswork.
- **Ready:** every path that produces a shipped binary (the installer's download,
  `deploy-update.sh`, the Docker image, a manual build documented in
  PRODUCTION.md) goes through `scripts/build-engine.sh`, and `/health` on the 58
  reports a real SHA. Related to the release tag (see "Requires Miguel" below).

### COMMERCE-7 — Partial refunds (nota crédito parcial)
- **Origin:** PHASE4-FIRST-MILE-S1 (closing COMMERCE-1 surfaced it). The refund
  route always reverses the FULL payment and the credit note reverses every
  line; `pagos.monto_reembolsado_centavos` and the payments layer already
  support partial amounts, but no surface asks for one.
- **Impact:** Medium — a merchant returning ONE item of a three-item order has
  to refund everything. Product scope, not an engine gap.
- **Ready:** the refund route accepts an optional line/amount subset, the nota
  crédito carries only the reversed lines, and stock returns only for those
  lines; suite coverage for the partial path.

### COMMERCE-10 — A 100% coupon produces a $0 order, accepted

- **Origin:** LINKABLE-TRUTH-S1, pinned while writing verify.sh section E
  (COMMERCE-8): a coupon with descuento_pct=100 yields base 0, IVA 0,
  total $0 — and the checkout accepts it, creating an order whose payment
  is for zero pesos. Defined behavior (the arithmetic closes), but a real
  store probably wants either a cap (<100) on coupon creation or a
  zero-total short-circuit that skips the gateway.
- **Impact:** Low — coupons are merchant-created (panel/dueño), not
  visitor-supplied; no data corruption, the math is exact.
- **Ready:** a decision — cap the pct at coupon creation, or define the
  $0-order flow (auto-paid? gateway skip?) — implemented with a suite case.

### COMMERCE-4 — A real DIAN Proveedor Tecnológico adapter
- **Origin:** `docs/DIAN.md` (the interface exists; the implementation is a stub).
- **Impact:** High to go live in Colombia, zero for the engine.
- **Ready:** one PT implemented behind the existing `Issuer` interface, with
  sandbox credentials, the CUFE returned and stored, and the failure modes mapped
  to the `facturas` state machine.

### OPS-18 — The `neodevtrix/appximo` Docker image: first publish pending; org namespace deferred
- **Origin:** RENAME-AND-PUBLISH-PREP-S1; re-scoped by CI-GREEN-S1 (2026-08-05):
  the `appximo` Docker Hub org was not created (Docker Hub charges for orgs), so
  the image publishes under the personal namespace **`neodevtrix/appximo`** —
  every doc/compose/badge now says so, and the publish secrets are loaded.
- **Impact:** Medium. The quick-start docker path 404s until the first green CI
  on main pushes the image (the README says so inline). The namespace can
  migrate to an `appximo` org later if the project justifies the fee — a rename
  in docker-publish.yml + docs plus a deprecation tag on the old image.
- **Ready:** `docker pull neodevtrix/appximo` works anonymously (green CI on
  main → docker-publish.yml pushes). Reconsider the org when there is revenue
  or a second maintainer.

---

### OPS-20 — The Windows path is written but UNVERIFIED (refreshed by FIELD-FEEDBACK-S1)
- **Origin:** PHASE4-FIRST-MILE-S1; **refreshed 2026-08-07**: the first field
  evaluation RAN Windows 11 and its findings drove real fixes — C1 (no more
  per-invocation maxprocs stderr; PowerShell `$?` is trustworthy), F1/F1-bis
  (the binary loads `.env`, BOM-tolerant), W1 (data under
  `%LOCALAPPDATA%\Appximo`, never `C:\var`), W2 (`appximo gen-secret`), C5
  (`version --json`). All are cross-compiled and unit-tested but **NOT
  LIVE-VERIFIED on Windows** (this project has no Windows machine); the
  numbered verification script for Miguel is in
  docs/FIELD_FEEDBACK_RESPONSE.md §Windows.
- **Impact:** Medium — Windows developers are a real slice of the first mile.
- **Ready:** the numbered script executed on a real Windows machine,
  discrepancies fixed, and QUICKSTART's NOT-VERIFIED marker replaced with the
  verification date.

---

### ENG-36 — Warn when a tenant's stored schema diverges from the boot schema (B7 residual)
- **Origin:** field report B7 (FEEDBACK.md): the tenant record was stale
  relative to the boot schema (v2 without the `routes` grants added later),
  so a Studio deploy — which correctly loads the RECORD — would have quietly
  reverted live RBAC. B1's fix removed the two-process workaround that
  CAUSED the divergence, and the operator rule is documented in `appximo
  quickstart` §5 ("after changing the schema, run migrate — never only edit
  the boot file"), but nothing DETECTS the state.
- **Impact:** Medium. Two sources of truth by design (boot = served surface;
  record = per-tenant migration state); a silent divergence turns the next
  well-intentioned deploy into a revert.
- **Ready:** at boot (and in Studio's deploy target list), a tenant whose
  stored schema hash differs from the boot schema is flagged — a log line +
  a visible marker in the deploy modal ("record differs from the running
  surface — review the preview closely"), with zero hot-path cost.

---

### OPS-21 — `appximo files gc` (orphaned uploads collector — M8)
- **Origin:** field report M8: `POST /api/files` runs before the referencing
  record exists, so abandoned forms strand uploads forever (7 of 10 in the
  evaluation). Behavior + the safe manual sweep are documented in
  docs/FILES.md; no collector exists.
- **Impact:** Low-Medium (disk growth on long-lived apps; confusing inventory).
- **Ready:** `appximo files gc --tenant X --older-than 24h [--dry-run]`
  deletes uploads not referenced by ANY `file`-typed column, grouping by
  `sha256` (content-addressing means the blob may back other uploads — the
  safe unit is "every id sharing the hash is orphaned"), dry-run first,
  per-tenant, tested against attach/detach races.

---

### ENG-37 — The consumer dependency graph is disproportionate (B2 + D3)
- **Origin:** field report B2/D3: `go mod tidy` on a two-endpoint backend
  downloads >1.2 GB (full AWS SDK, gcloud, gRPC, Redis, OTel, MaxMind …) and
  the binary is ~78 MB — almost all of it OPTIONAL-by-config features (S3,
  Redis) every consumer compiles anyway.
- **Impact:** Medium: first contact (15 cold minutes reads as a hang), CI
  cost, image size. Not correctness.
- **Ready:** the optional backends live behind build tags or submodules so the
  default graph is core+pgx; measured module-download and binary-size deltas
  published; ADR for the split (it changes the consumer contract).

---

### OPS-22 — Verify the D1/D2 release hardening at the next tag
- **Origin:** FIELD-FEEDBACK-S1 implemented, in release.yml, the version-less
  asset aliases (`releases/latest/download/appximo-<os>-<arch>` — D1) and the
  keyless cosign signature over checksums.txt (trust anchor in Sigstore's
  transparency log, not in the release — D2). A release workflow can only be
  proven by a release; none has run since.
- **Impact:** High for D2 (supply-chain trust for a binary that runs with DB
  credentials), Low effort.
- **Ready:** the next tag's release carries both asset name families +
  `checksums.txt.sigstore.json`; the documented `cosign verify-blob` line
  passes from a clean machine; then a one-line fetch installer (install.ps1 /
  get.sh) can be written against the stable URLs (the rest of D1).

---

### ENG-39 — `appximo up --embedded-pg` for machines without Docker
- **Origin:** FEEDBACK.md §13 names it as the third Postgres path
  (`DATABASE_URL` / Docker / embedded). FIRST-TEN-MINUTES-S1 shipped the first
  two and deliberately deferred this one: an embedded Postgres means a new
  runtime-download dependency (e.g. fergusstrange/embedded-postgres fetches a
  ~30 MB PG binary at first run), exactly the dependency-weight class the same
  field report flags in ENG-37 — adding it deserves its own measured session
  and Miguel's sign-off on the download-at-runtime posture.
- **Impact:** A no-Docker machine today gets an actionable error naming three
  ways out (install Docker / local PG / hosted PG) — a mitigation, not the §13
  promise. Mostly affects corporate laptops where Docker is banned.
- **Ready:** `appximo up --embedded-pg` boots on a machine with no Docker and
  no Postgres, the downloaded runtime is checksum-verified, and `appximo down`
  knows how to stop it; dependency cost measured and accepted.

### OPS-48 — The dev box (the 105) ran its root disk to 99 %; the alert existed and nobody was looking
- **Origin:** MANUAL-OPERACION-S1. The first screenshot of the new "Health now"
  strip showed `Disco BAJO · 1.4 % libre` — true: `/` at 99 % (893 MB free).
  Consumers: `/root/.cache` 19 GB (Go build cache), `/tmp/claude-0` 4.4 GB
  (agent scratch), stale `/tmp/go-build*` 3 GB (removed: → 87 %), `/root/go`
  3.6 GB (module cache), `/var/lib/docker` 2.5 GB, `/var/log` 1.4 GB, old
  session scratch (`/tmp/cent`, `/tmp/mf`, `/tmp/mc` ~2.4 GB).
- **Impact:** medium — the box that builds, benches and drives the lab; a full
  disk fails a build or a Postgres write mid-session, silently.
- **Ready:** `go clean -cache` (or a cron that trims `/root/.cache/go-build` to
  a size), the old session scratch dirs deleted by Miguel (they are copies —
  the evidence lives in the internal repo), `journalctl --vacuum-size=200M`,
  and the 105 running its own self-monitor with `APPXIMO_DISK_MIN_FREE_PCT`
  pointed at `/` so the panel says it before it bites.

### OPS-49 — `deploy-app.sh`'s outside probe reports a bare `✗ … → 200` when the proxy answers an unknown Host with an empty body
- **Origin:** MANUAL-OPERACION-S1, first `deploy-app.sh` against the lab with
  `--tenant-host=lab.applab-target-basic.internal` (a host Caddy did not
  serve): Caddy answers `200` with an EMPTY body for an unknown site, the
  version check reads `""`, the read probe sees 200 without `data[]`, and the
  script rolled back a good binary and exited 2 ("rollback did not recover")
  — correct behaviour (it refused to call it verified) with an unhelpful
  message.
- **Impact:** low — a lab/first-setup trap; on a box whose Caddy site matches
  the tenant host it never fires.
- **Ready:** when `/health` through the proxy answers 200 with an empty or
  non-JSON body, the ✗ line says "the proxy answered for an unknown Host —
  does Caddy serve <host>?", and the rollback is skipped when the on-box
  health passed and the outside failure is that shape (the binary is not the
  problem). Pinned by a test with a stub proxy.

### DOC-4 — A Spanish AUTHORING guide for a customer's developer (schema change → migrate → deploy → verify) does not exist; the manual covers operating
- **Origin:** MANUAL-OPERACION-S1's honest reading. `docs/MANUAL_OPERACION.md`
  answers "how do I run it, see it, change a knob, recover, repeat a
  scenario". The next question a customer's developer asks — "how do I add a
  field, a resource, a role, and get it live without breaking anything" — is
  answered only in English (`GUIDE.md`, `AUTHORING_JOURNEY.md`,
  `SCHEMA_SPEC_LLM.md`) and in Studio's own UI.
- **Impact:** medium once the two customers' developers start changing
  schemas: the safe path (dry-run → approve drops → hot vs restart) is the one
  they most need in their language.
- **Ready:** `docs/GUIA_AUTORIA.md` (Spanish): the schema in ten minutes, the
  four kinds of change and what each activates (hot column / restart for a
  resource), `appximo migrate --dry-run` read aloud, Studio's deploy with the
  destructive gate, and the regression flows — every command executed, one
  screen per step.

### ENG-61 — A GraphQL resolver PANIC is recovered by graphql-go into `errors[]` and never captured

- **Origin:** DEPLOY-FLOTA-S1 (2026-08-31), while closing ENG-56. graphql-go's
  executor recovers a panic inside a resolver (`executor.go` `handleFieldError`)
  and formats it as a field error, so the engine's Recoverer never sees it: the
  request is a 200 + `errors[]` with the panic's message and NO capture — the
  REST equivalent is captured on the panicking goroutine with its site.
  ENG-56 closed the database/hook-execution failures (the ones the field
  report measured); this is the remaining resolver failure class.
- **Impact: low.** The engine's own resolvers do not panic on known inputs;
  the message still reaches the client's `errors[]`.
- **Ready:** wrap each generated resolver's `Resolve` in a deferred recover
  that calls `captureResolverError` with the panic value and re-panics (so
  graphql-go still formats the field error); one provocation through the
  tracing integration test (a resolver that panics on a declared trigger
  field) persisted as a 500 with the site.

### ENG-62 — Two engines booting the same second on one FRESH shared database: the control-plane bootstrap is not serialized

- **Origin:** VOZ-SIN-IA-S1 (2026-09-19), side finding. Booting the base and the new binary
  together against the same lab database, one died with `bootstrap control plane: ERROR:
  tuple concurrently updated (SQLSTATE XX000)` — both ran the idempotent control-plane DDL at
  once. One engine per database (production) never sees it; a systemd unit would restart
  the loser.
- **Impact:** a boot that can die through no operator fault whenever two processes share a
  database (a hand blue/green, a misconfigured `fleet run` with one DATABASE_URL).
- **Ready:** wrap the bootstrap in `pg_advisory_xact_lock` (the workflow scheduler's own
  technique) or retry once on XX000; a test that starts two `App.New` in parallel on an
  empty database.

### OPS-46 — The stock `serve` binds every interface; on the 58 vetapp's `:8091` and control `:9098` listen on `*` and the firewall is the only line

- **Origin:** DEPLOY-FLOTA-S1 (2026-08-31), the pre-provocation port review
  of the 58: `ss -ltnp` shows vetapp on `*:8091` and `*:9098` (the tiendita's
  commerce binary binds `127.0.0.1` because its `main()` sets `Config.Host`);
  `ufw` blocks both from the internet (verified from the 105: 000 on all
  four ports), so this is defence-in-depth, not an exposure. `Config.Host` /
  `Config.ControlHost` exist for a library consumer (LIBRARY-GAPS-S2) but the
  stock `appximo serve` has no flag or env to set them, and `install.sh`
  cannot ask for it.
- **Impact:** Low-Medium. A box whose firewall is reset (a cloud console
  "reset firewall", an image without ufw) exposes the control plane
  (`X-Admin-Key`-gated) and the engine directly.
- **Ready:** `APPXIMO_HOST` / `APPXIMO_CONTROL_HOST` env knobs (default
  unchanged; `127.0.0.1` enforces localhost at the socket), the installer
  writes `APPXIMO_CONTROL_HOST=127.0.0.1` and, behind Caddy, `APPXIMO_HOST=
  127.0.0.1`; `fleet-audit.sh` flags a control port that listens on `*`.


### OPS-50 — The backup SET does not carry what the env references OUTSIDE it: a static mount (`APPXIMO_STATIC_DIR`) makes a restored box refuse to boot

- **Origin:** CENTRO-MANDO-S1 (2026-08-31). The migration rehearsal (vetapp,
  the 58 → a fresh box) restored the set and the engine restart-looped: the
  restored `/etc/vetapp/vetapp.env` mounts `APPXIMO_STATIC_DIR=/opt/vetapp/web`
  (the landing page) and that directory is not in the set (dump + uploads +
  `/etc/<app>` + manifest). `restore.sh` reported "engine did not become
  healthy" and left the app stopped — correct, but the set had promised a
  complete restore.
- **Impact: medium.** The 3 a.m. runbook (§4.3 scenario B, a lost box) is
  incomplete for any app whose env points outside `/etc/<app>`: a static
  mount, a theme CSS outside `/etc`, a custom `APPXIMO_FILES_DIR` on another
  disk. The panel's migration chain works around it (ships the static dir
  beside the set), the plain runbook does not.
- **Ready:** `backup.sh` includes every path the env references outside the set
  (`APPXIMO_STATIC_DIR`, `APPXIMO_APP_THEME_CSS` when outside `/etc`) as
  `<set>.extra.tar.gz` + a manifest line, and `restore.sh` restores it; or,
  at minimum, `restore.sh` PRE-CHECKS the restored env against the box and
  names the missing path before stopping the app.

### OPS-51 — An undocumented droplet in Miguel's DigitalOcean account: crisblogs' box (`do-amd-1cpu-7`, 147.182.163.170, $7/mo)

- **Origin:** CENTRO-MANDO-S1 (2026-08-31), building the inventory: `doctl
  compute droplet list` with the lab token shows THREE droplets in the account
  — the 58, the new centro-mando, and `do-amd-1cpu-7` (nyc1, 1 vCPU/1 GB),
  which is the box `crisblogs.appximo.com` resolves to (OPS-27: the external
  evaluator's install). The 105 is NOT in that account (another account/team).
  Nobody had written down that Miguel pays for the evaluator's box; the panel
  cannot ssh into it (no key).
- **Impact: low ($7/mo) but exactly the class the inventory exists for.**
- **Ready:** Miguel decides — ask the evaluator for the key (and mark the
  box `apps` in the inventory) or power it off and stop paying; either way
  the inventory row (marked `ajeno`) says which.


- **Update (CENTRO-MANDO-S2, 2026-09-17, observed from the panel):** the box is
  no longer keyless or unnamed — the live inventory shows `147.182.163.170` as
  **retotr-prod** (activo, uso `apps`, the panel's ssh key authorized) serving
  **Reto Tributario** (`https://retotr.appximo.com`, unit active, public 200)
  alongside the evaluator's old crisblogs install (public 200; its `/health`
  is not readable from inside — a foreign layout). What remains open shifted:
  the box is REAL PRODUCTION now and is still absent from the handoff's
  `05_SERVIDORES` and has no companions (`fleet-audit.sh` cannot run there);
  and the old crisblogs install still needs OPS-27's decision.

### OPS-52 — The command center's own loose ends that only Miguel can close: its domain, MFA, an alert destination

- **Origin:** CENTRO-MANDO-S1 (2026-08-31). The panel serves at
  `https://centro.159-203-57-144.sslip.io` (a public wildcard DNS + a real
  Let's Encrypt certificate) because appximo.com's DNS lives in Cloudflare
  and no token for it exists on the 105 — deliberately. Its login is the
  tenant's password login (no TOTP on the panel's SPA; `/admin` has it). Its
  `fleet-audit.sh` is green on alerts since ALERTAS-TELEGRAM-S1 (Telegram
  verified live); the ✗ that remain are the domain/MFA halves.
- **Ready:** (a) an A record `centro.appximo.com → 159.203.57.144` (DNS only)
  and `install.sh --app=centro --domain=centro.appximo.com …` re-run on the
  box (secrets/data kept); (b) ~~an alert destination~~ DONE (ALERTAS-TELEGRAM-S1: Telegram verified);
  (c) optional: a super-admin with TOTP for `/admin` and the panel's SPA
  learning the `mfa_required` branch. Also: the panel holds ssh keys to the
  boxes it operates (inherent to an orchestrator) — its own box is hardened
  (`--harden`, swap, off-box encrypted backup to the 105, restore rehearsed).

### OPS-53 — The lab does not ship the companion scripts to its boxes (the "customer path" stops halfway)

`tools/lab` provisions with install.sh, but `/root/lab/` carries only
install.sh + schema + seed: the companions (backup.sh, restore.sh,
fleet-audit.sh, deploy-update.sh) never travel, the installer NAMES the gap as
a warning, and every lab box lives without them. Found in AUTOMATIZACION-S1
while provoking fleet-audit's new worker check on the lab box (the script had
to be copied up by hand). The lab promises the EXACT customer path; without
the companions its operational half (backup/audit/deploy) is never exercised
there. **Ready:** provision.go ships `scripts/*.sh` next to install.sh (or a
tarball of scripts/), and the smoke runs fleet-audit once.

### OPS-54 — `pkg/platformadmin` integration tests are not in-process idempotent (`-count=2` fails) and flaked once under parallel load

- **Origin:** ALERTAS-TELEGRAM-S1 (2026-09-18). The full lane, running in
  parallel with the browser suite, failed `pkg/platformadmin` once; a solo
  re-run passed, and `go test -count=2` fails identically on the BASE commit
  (shared DB state between in-process runs) — pre-existing, not that
  session's change.
- **Impact:** low-medium. A lane that flakes on shared state trains everyone
  to read FAIL as noise; the day the FAIL is real it gets ignored.
- **Ready:** isolate the state (schema/DB per run or TRUNCATE in setup) and
  leave `-count=2` green.


### VOZ-6 — Miguel dictates ten real questions to the bot from his phone — the one verification an agent cannot run

- **Origin:** VOZ-PREGUNTAS-S1 (2026-09-19). VOZ-3 is BUILT (archive): any word sent to
  the tiendita's bot that is not `resumen`/`estado`/`ayuda` goes to `POST /api/ask`. A
  bot cannot generate a Telegram user message, so the real ENTRY leg — Spanish
  dictation with its mangled names — is Miguel's. 23 lab questions + 7 live over HTTPS
  passed; the voice itself has not been heard.
- **Impact:** without it the session claims "works by voice" with simulated dictation;
  ADR-033 §Verification names the real dictation as the ready criterion.
- **Ready:** ten dictated questions to `@appximodev_bot` (tiendita) — «cuántas órdenes hay
  hoy», «qué pedidos están sin pagar», «las órdenes de <a client, said badly>», «órdenes
  por estado», «cuánto vendimos esta semana», plus whatever an owner asks — with a note
  of how many were right, how many «no entendí», and how many WRONG (the number that
  decides whether this is offered to a customer). Since VOZ-ESCRITURAS-S1 the same ten
  should include three orders («anotá…», «marcá como hecha…») and their yes.

### VOZ-7 — The questions the v1 grammar cannot express: cross-resource, period comparisons, rankings, percentages, follow-ups

- **Origin:** VOZ-PREGUNTAS-S1 (2026-09-19). The plan is ONE resource, one operator per
  filter, one period, and each question stands alone (ADR-033 §Limits). Measured with
  owner questions: «cuál fue la orden más cara del mes» answers as a max (fine), but «el
  producto más vendido», «¿vendimos más que el mes pasado?», «qué porcentaje está sin
  pagar» and «¿y ayer?» are «No entendí». And «vendimos» means whatever the model maps to
  the schema's states (it read `estado = pagada`) — the schema cannot declare which
  states count as a sale.
- **Impact:** the third or fourth question an owner asks; today it gets an honest «No
  entendí» with what CAN be asked — honest, but short.
- **Ready:** Miguel decides which are worth it: (a) rankings = list + sort on a numeric
  field (small; the engine has sort); (b) period comparison = two plans and a subtraction
  by the engine (medium); (c) ~~a declarable vocabulary block~~ **BUILT in VOZ-AHORRO-S2
  as `aliases` (ADR-038)** — resource and value synonyms, validated unique at load; what
  an alias cannot name (a verb like «vendimos», a comparison) is VOZ-12; (d) follow-ups
  with a one-question memory (medium, and it opens the door to ambiguous answers). Each
  one provoked before it is built.

### VOZ-10 — The redacted history cannot hide a name the engine did not identify

- **Origin:** VOZ-TRAZABILIDAD-S1 (2026-09-19); the limit is written in ADR-036 §2.
  `APPXIMO_ASK_HISTORY_TEXT=redacted` replaces by `[nombre]` the proper names the
  plan carries as `match`; a question with no plan (unclear), or a name the plan did
  not capture, is stored as typed. Today's escape is `none`.
- **Impact:** a tenant with a strict personal-data rule loses the whole text for a few
  phrases, and the word «redacted» carries a written exception an auditor will read.
- **Ready:** decide whether a generic, model-free proper-name detector (capitalized
  words after «de/del/para/con», the parser's own heuristic) applied BEFORE storing
  even without a plan is worth it; measure false positives on the corpus.
### VOZ-11 — Pending confirmations live in process memory: a restart or a multi-PROCESS fleet forgets them

- **Origin:** VOZ-ESCRITURAS-S1 (2026-09-20), ADR-037 §What is not built. A voice write
  waits for its yes in `ask.PendingStore` (in memory, one per tenant|role|user, 5 min).
  A graceful restart or a `fleet run` (one process per app, N replicas behind a proxy)
  loses it: the owner says «sí» and hears «no hay ninguna escritura pendiente».
- **Impact:** safe direction (nothing unconfirmed is ever written) but a confusing
  minute during a deploy; today's fleet is single-process per app so it does not bite.
- **Ready:** a `public.ask_pending` table (tenant, role, user, id, plan JSON, expires)
  read/written through the ledger's pool — only when a multi-replica deploy of one app
  exists. Cheap; not before it is needed.

### VOZ-12 — What an alias cannot name still goes to the model: «vendimos», «vigentes», «la más cara», «¿y ayer?», a question in English on a Spanish app

- **Origin:** VOZ-AHORRO-S2 (2026-09-20), ADR-038 §Limits. With the aliases declared the
  parser settles 75 % of the 58's real questions (50 % before). What remains is verbs and
  comparisons an alias cannot express: «cuánto vendimos esta semana» (a verb that implies
  a state AND a sum), «tenemos cupones vigentes?» (a comparison with the present), «cuál
  fue la orden más cara del mes» (a ranked max), «how many pets are vaccinated» (another
  language), «hoy y ayer» (two periods). Each is a model call (≈ US$ 0.003) that answers
  right or says an honest «no entendí».
- **Impact:** the 25 % that still costs; in habitual use the plan cache absorbs repeats,
  so the cost is the NEW phrases of those shapes — tens of cents a month, not dollars.
- **Ready:** a product decision (VOZ-7): a declarable block of business VERBS («vendimos»
  = sum of `total_centavos` over states [pagada, entregada, cerrada]) would be the natural
  extension of `aliases` (same place, same validator) — medium: grammar + spec + Studio.
  The alternative is to leave it to the model and the cache, which is what is measured
  today. Miguel decides with one more week of `gasto` (waste is now shown apart).

### VOZ-13 — `aliases` are authored only in Studio's Code view: the entity panel preserves them but does not edit them

- **Origin:** VOZ-AHORRO-S2 (2026-09-20), ADR-038 §Limits. Studio round-trips the block
  faithfully (resource-level in the entity's extras, value-level inside the field def),
  like `summary`; there is no visual control, so an owner who wants to declare «pedidos»
  edits JSON. `appximo explain` reads them back and the validator keeps them honest.
- **Impact:** the promise is that the OWNER declares their words; today a developer does
  it in JSON. Small, but it is the surface a non-programmer touches.
- **Ready:** an «también le dicen» chip list in the entity panel and, in the enum editor,
  a list per value; live validation mirroring `validateAliases` (the same uniqueness
  rules). A small Studio session.

### VOZ-5 — Reactive rules declared by voice ("cuando una orden quede pagada, avisame")

- **Origin:** A-70 step 4/5; ordered last in VOZ-VISUAL-S1 (A-73). **Correction
  recorded here:** the session brief called `workflows` v1 "cron only — it does not
  react to data changes". Verified against the code: it DOES — event triggers are
  outbox consumers (`pkg/workflows/consumer.go` `EventConsumer`, tested in
  `workflows_test.go`, ADR-031 §1); a resource that declares `events` fires its
  workflows on create/update/delete. What is missing is the AUTHORING, not the
  engine: the grammar for agents TEACHES `workflows` since CAPACIDADES-VISIBLES-S1
  (AUTO-10 done — the generator declares them by signal of the description), Studio
  has no panel for them (AUTO-11), and nobody can declare one by voice.
- **Impact:** without it every "avisame cuando…" is a JSON edit by the developer.
- **Ready:** AUTO-11 first (the declaration is teachable; it must be visible in
  Studio), then a voice front that produces a `workflows` entry the validator accepts.

### AUTO — The automation front (consolidated 2026-09-17, CENTRO-MANDO-S2)

The worker/outbox/workflows/voice front lived in a separate architecture chat
and nowhere else; CENTRO-MANDO-S2 consolidated it here. The handoff document
(`TRASPASO_AUTOMATIZACION.md`) was NOT present in the internal repo when this
session ran — the items below carry what the session brief transcribed from it,
plus what this session verified live (the 34 pending events, the echo-ack
mechanism, the breaker's silent 8 s, the worker's env reads). Two PRODUCT
decisions are already taken by Miguel and recorded (internal A-67, A-68): the
worker SHIPS with the installer, and the `workflows` executor GETS BUILT.
Structured fields for every item: [backlog/items.json](backlog/items.json).

### OPS-55 — `deploy-app.sh` rolls back the BINARY but not the SCHEMA it was deployed with

- **Origin:** VOZ-DELTA-S1 (2026-09-18), found doing the rollback leg on vetapp: the
  new engine had been deployed together with a schema that declares keys the
  previous binary does not know (`summary.notify`, `quiet_days`), so `deploy-app.sh
  --binary=<previous>` failed the old binary's boot ("unknown key") and its own
  4b safety net rolled forward again. The pre-deploy schema copy exists
  (`/root/<app>-schema.pre-<tag>`) but nothing restores it. The session rolled back
  by hand (schema first, then the binary) and forward again — both verified.
- **Impact:** medium. A rollback after a deploy that shipped a schema change is
  a two-step operation the tool does not know; done blind it "succeeds" by
  staying on the new binary.
- **Ready:** `deploy-app.sh` records the schema's md5 at deploy and, on an
  explicit rollback (`--rollback-to=<tag>`), restores `/root/<app>-schema.pre-<tag>`
  together with the binary (and refuses the automatic 4b rollback when the boot
  error names an unknown schema key, saying which file to restore).

### OPS-57 — The gate's `admission-shed-burst` probe flips by scheduling, not by binary

- **Origin:** VOZ-SIN-IA-S1 (2026-09-19). 64 requests, 32 in parallel, `APPXIMO_MAX_INFLIGHT=1`,
  expecting some 429: whether two requests overlap is the scheduler's business. It read
  base=yes/new=no twice on a quiet box; isolated (six alternating rounds, same DB) BOTH
  binaries shed sometimes and serve all 64 other times
  (`evidencia/VOZ-SIN-IA-S1/gate-admission-probe-isolated.log`). It had flipped under load in
  VOZ-DELTA and VOZ-PREGUNTAS too.
- **Impact:** a DIFF explained away every session erodes the gate; one day a real one is.
- **Ready:** make it deterministic — hold ONE slow request in flight (a sleeping endpoint or
  a heavy `?search=`) and fire the burst meanwhile; at cap 1 the 429 is certain with
  admission control and impossible without.

### AUTO-9 — The voice plan: five steps, with what the research already settled

Operating an app by voice, in five deliberate steps: **(1) close the
worker/outbox traps — DONE (AUTOMATIZACION-S1)** → (2) read-only by voice —
**the digest is DONE (VOZ-ESCALON1-S1) and reads at a glance as a picture with
a declared filter and an honest vocabulary (VOZ-VISUAL-S1, ADR-032); read
QUESTIONS are VOZ-3 (ADR-033, built)** → **(3) writes with confirmation — DONE
(VOZ-ESCRITURAS-S1, ADR-037: create + update, never delete, an exact yes, the
engine's own write cores)** → **(4) declarative rules — DONE: the workflows executor
exists (ADR-031), event AND cron triggers; declaring them by voice is VOZ-5** →
(5) a visual layer. The settled foundations
(`expr-lang/expr`, `pg_try_advisory_lock`, the schema as source of truth) are
now shipped code; Studio-as-the-rules-editor is AUTO-11. **Warnings that must
not be lost:** Spanish dictation mangles proper names — correct server-side
against what exists, never trust the transcript; real latency will be 2–4 s —
design the confirmations for it; the scheduler's DST policy is written (ADR-031
§5). **Ready:** steps 2–3 and 5 are their own sessions with Miguel validating
the experience — the base beneath them is done.

### AUTO-11 — Studio has no visual workflows panel (Code view only)

Workflows are authorable today as JSON (Studio's Code view validates live
through /editor/validate), but there is no graphical panel: no
resource/event dropdowns, no step editor, no run history in Studio. A-70
declared Studio THE editor of the rules. **Ready:** an editor session
(pkg/editorui): a Workflows panel faithful to `validateWorkflows` (the same
pattern as the RBAC and relations panels), plus a runs view reading
`GET /admin/workflows`.

### AUTO-12 — `appximo up` does not start the worker: a schema with workflows leaves the promise PRINTED on the card, not running

`up` compiles and serves the schema in-process, but `workflows` execute in
`appximo-worker`, a separate binary `up` does not start. Since
CAPACIDADES-VISIBLES-S1 the `up` card (and the `--json` result, field
`workflows`) names the declared workflows and the exact worker command, and
MASTER_PROMPT asks for it in step 3 — but it is still a second command a
user may not run, and the generated tasks app comes out with a reminder
declared. The failure is VISIBLE (the engine says so at boot, `/admin/workflows`
shows no runs) but it is a dead promise until then. **Ready:** `up` starts the
worker in the same process or as a supervised child when the schema declares
workflows (`pkg/workflows` is a package; the executor already runs in-process
in tests), and asks for the Telegram credentials in its question block ONLY when
a workflow enqueues `summary.telegram`.

### DEC — Decisions that wait on Miguel (stable IDs since CENTRO-MANDO-S2)

The old "Requires a decision from Miguel" table is now these `DEC-*` items, so
the panel and the structured register can point at them; resolved rows moved to
[BACKLOG_ARCHIVO.md](BACKLOG_ARCHIVO.md). MIG-FRONT (the migration front)
keeps its own ID above; OPS-47 (alert destination) is DONE — ALERTAS-TELEGRAM-S1.

### DEC-1 — An off-box destination for the 58's backups (the one catastrophic single copy)

The ONLY ✗ `fleet-audit.sh` leaves on both apps of the 58: `BACKUP_COPY_TO`
is unset, so every backup set dies with that disk (a lost droplet = the golden
dump, the vetapp data and both apps' secrets, gone). Needs a destination
Miguel owns (DO Space $5/mo via rclone, or scp to a box he controls) +
`BACKUP_PASSPHRASE_FILE` so secrets travel encrypted. One line per env file;
the next backup run proves it (`offbox=yes`). Flagged "this week" since
DEPLOY-FLOTA-S1.

### DEC-2 — Publication is PAUSED on purpose (licensing review); the broken CI is known, not abandoned

Miguel decided not to publish further public releases for now, while he
reviews licensing. Independently, CI has been red since `29f3ec0`
(2026-08-29): `max64` is defined only in `pkg/observability/resources_linux.go`
(GOOS-suffixed) and used from `resources_db.go` (no constraint), so every
non-linux build of `pkg/observability` fails — reproduced with
`GOOS=windows go build ./pkg/observability` → `undefined: max64` ×4; the CI
job that catches it is "Windows gate (OPS-25)". Consequence: the Release
workflow is skipped and tags v0.1.14, v0.1.15 and v0.1.16 have no GitHub
release (latest published: v0.1.13). **The two facts cancel out: there is no
urgency, and red CI must NOT be read as neglect.** The repair is written and
parked: internal repo `nuevo_chat_web/prompts/PROMPT_REPARAR_PUBLICACION.md`,
marked "when publication resumes", not before. Recorded as decision A-69 in
the internal package.

*Addendum (CAPACIDADES-VISIBLES-S1, 2026-09-20):* the pause now has a visible
cost, written where it bites — everything the automation/voice front built
since 2026-09-18 (workflows executor, the worker among the release assets,
`/api/summary`, `/api/ask`, `aliases`, the spend cap, the Telegram bot,
`appximo drill`) exists ONLY on `main`; the docs, `appximo spec`, GUIDE §9,
ESTADO_DEL_MOTOR and the site now say so in one place each instead of
implying a release carries it. Resuming publication is still Miguel's call
(licensing), and nothing else was repaired (A-69 stands).

### DEC-3 — Publish the v0.1.10 security advisory (the text is ready; it never went out)

**The contradiction between two session reports, resolved with evidence
(2026-09-17):** (a) there is NO GitHub Security Advisory on the repo at all —
the public API returns an empty list — and the v0.1.10 release body is only
the auto-generated changelog line: the prepared security text
(internal `RELEASE_NOTE_v0.1.10.md`) was never pasted anywhere public.
(b) The claim "v0.1.13 does not have the authorization fix" is **FALSE**:
commit `6429a00` (ADR-027, `EnforceUpdateRBAC`) is an ancestor of v0.1.10 and
of every later tag — `git grep EnforceUpdateRBAC v0.1.13 -- pkg/codegen/
rbac_write.go` finds the enforcement in the tagged tree. Anyone running
≥v0.1.10 has the fix; the unwarned population is v0.1.8/v0.1.9. For the
customer whose acceptance criterion is "no user sees another user's data":
any ≥v0.1.10 satisfies it — and the vulnerability was about WRITE/attribution
(handing your own row to someone else); reading another user's rows was never
possible (they stay 404). **Ready:** Miguel pastes the prepared text as a
GHSA (medium-high) — currently waiting behind DEC-2's pause on public
activity.

### DEC-4 — Does the migration report count as the FIFTH external evaluation? (A-25)

Meets (2) and (3); missing (1): a WRITTEN confirmation that the migration ran
without our direction. With it: "five independent field evaluations, one a
real 23-table migration".

### DEC-5 — A response-time promise on the landing

The research's strongest lever (5 vs 30 min = 21× qualification). Publish only
a number Miguel can sustain; the WhatsApp Business away-message is the
zero-cost floor.

### DEC-6 — Name + face next to the landing CTA

Research says +34.7 % with a founder photo; standing decision A-32 says "el
equipo", no proper name, and no photo exists (FOTO-PENDIENTE). A-32 wins until
Miguel re-decides; reversing costs one sentence + one ≤40 KB .webp.

### DEC-7 — The VecinGo testimonial returns only with a named person's written confirmation

Removed from the commercial pages (no raw report, no name, independence
unverifiable from here). With a named confirmation it is worth ten times what
it was.

### DEC-8 — "Cientos de sistemas generados" — stays out until there are real deliveries to count

The real evidence is the eval corpus + CI, not deliveries. Restoring it is one
stat div; the written recommendation is not to.

### DEC-9 — Retire the old `api.appitools.com` DNS entry

Dead-ends at the Cloudflare proxy; the bare-engine demo was deliberately
retired (petfriendly IS the engine demo). One deletion in Cloudflare.

### VOZ-14 — Asociar varias personas a un compromiso por voz (many-to-many): the voice writes ONE row per confirmation

«reunión con Fabián y Marta» associates ONE person today, through a
`belongs_to` FK (`persona_id`); a `many_to_many` relation (tarea ↔ persona
through a junction resource) READS fine (`?include=`, verified in
MOTOR-AGENDA-S1) but the voice never writes the junction row — the second name
stays in the title. **Ready:** a write plan that takes a LIST on an m2m relation
field (`{"personas": [{"match":"Fabián"},{"match":"Marta"}]}`), which the
txWriter turns into the main row + N junction rows in ONE transaction
(`/api/transaction` already does), with a confirmation that lists each person.
Origin: MOTOR-AGENDA-S1 Part E.1.

### SCHEMA-10 — Studio has no panel for `ranges` nor for the `time` trigger (the Code view preserves them)

The `ranges` block (start/end, `no_overlap`, `default_duration`,
`timezone_field`) and a workflow's `time` trigger are authored only in Studio's
Code view; the entity panel preserves them losslessly (round-trip pinned) but
does not edit them. Same class as VOZ-13 (`aliases`) and AUTO-11 (workflows):
what the voice and the worker execute has no visual face, so «que no se me
crucen» needs JSON. **Ready:** a Range section in the entity inspector (two
time-field dropdowns, scope, when, duration) and the `time` trigger in the
workflows panel once AUTO-11 exists. Origin: MOTOR-AGENDA-S1 Part B.6.

### AUTO-13 — `/admin/workflows` does not show how many reminders are coming

The panel renders the `time` trigger (`time:eventos.inicio -15m`) and the
24-hour fired/failed gauges, but not «how many rows will fire in the next
hour» — the reminder's pending lives in the tenant's own rows, not in the
ledger. Without it, «the reminder did not arrive» and «there was nothing to
remind» look the same until the hour passes. **Ready:** the sweep publishes,
per workflow, the candidate count of the next window (one more query per tick,
or the last tick's count) in `/admin/workflows` and as a gauge. Origin:
MOTOR-AGENDA-S1 Part C.5.

### OPS-58 — The binary-diff gate cannot declare `ranges` nor `aliases`: its schema lives in the BASE binary's grammar

The gate feeds ONE schema to both binaries; a key the base rejects at load
(`aliases` since VOZ-AHORRO-S2, `ranges` since MOTOR-AGENDA-S1) cannot enter
the corpus, so the new behaviors are pinned by unit/integration tests and the
lab logs, not by the gate — which stays the only differential proof against
the previous binary. **Ready:** a corpus with TWO schemas (base and new) and
cases tagged «new only», fired at the new binary and compared against a
written expectation instead of the base. Origin: MOTOR-AGENDA-S1 gates.

### OPS-59 — The stock binary's control plane listens on EVERY interface (`*:9092`); ufw is the only guard

`appximo serve` opens the control plane (`X-Admin-Key`) on `:<port>` without
binding loopback: on the 58 `agenda` and `vetapp` show `*:9092` / `*:9098`
while the consumer binary of the tiendita binds `127.0.0.1:9099`. The written
rule is "never expose the control plane"; today the firewall keeps it, not the
engine — a box without ufw (or a chaos drill's `ufw disable`) leaves it on the
Internet behind one admin key. **Ready:** `serve` binds `127.0.0.1` for the
control plane by default (an `APPXIMO_CONTROL_BIND` for the multi-host case)
and `fleet-audit` flags ✗ a control plane not on loopback. Origin:
APP-AGENDA-S1 Part A. Decides: agent.

### OPS-61 — Restoring a set from the 58 needs PostgreSQL 18 on the destination: the 105's PostgreSQL 16 cannot read the dump

`pg_restore` 16 → "unsupported version (1.16) in file header" over the dump
the 58's PostgreSQL 18 writes; the off-box set was restored in a throwaway
`postgres:18` container instead (25 tables = manifest). The "lost box" runbook
silently depends on the destination's major version. **Ready:** `backup.sh`
writes the server version into the `.manifest`; `restore.sh` and the command
center's `pg_version` check demand it; one line in docs/PRODUCTION.md §4.
Origin: APP-AGENDA-S1 Part A. Decides: agent.

### VOZ-22 — Relative times («en una hora», «dentro de veinte minutos», «en dos días») are still the model's

The time-token vocabulary is closed on purpose (ADR-037 §6): a day plus a
clock. A time relative to NOW has no token; the parser lets it through and
the model resolves it (or not). An owner with the phone in hand says
«recordame en una hora» more than «a las cuatro»; today it costs US$ 0.003
and sometimes a «no entendí». In the sentence bank (151) it appeared once
(C05, resolved by the model). **Ready:** relative tokens (`in 1h`, `in 20m`,
`in 2d`) resolved by the engine in the app's zone with a spoken form — only
once the real history shows the phrase more than once a month. Origin:
AGENDA-ASISTENTE-S1. Decides: agent.

### VOZ-23 — Two intentions in one sentence execute ONE: the second is said back, not queued

«anotá comprar pintura y agendá reunión con Fabián mañana a las 4» plans the
first and says «Lo segundo («agendá…») decímelo aparte cuando confirmes».
There is no queue of pendings — a stray yes can never execute the wrong one
(ADR-040) — but the owner dictates twice, and with Siri a long sentence is the
normal case. **Ready:** a queue of ONE: after the yes (or the no) of the first,
the engine offers the second as a fresh confirmation without the owner
repeating it; never two live pendings at once. Origin: AGENDA-ASISTENTE-S1
provocation 10. Decides: agent.

### VOZ-24 — «Arrancá con X» / «empezá X» (to start = move to in-progress) go to the model, which sometimes answers that it cannot

The parser settles a transition by the state value said («poné en curso…»)
or by a verb whose stem is a state («cancelá»); a verb of beginning names no
state. The model answered `write_refused` in two runs (U05 of the bank, and
«poné en curso» before its fix): it reads «arrancá» as an action that is not
about data. It is the natural phrase to start a task and the only colloquial
transition of the bank still failing (1 of 151). **Ready:** with evidence
from the real history (the owner saying it more than once): a generic map
verb-of-beginning → the non-initial, non-terminal state whose name contains
«curso»/«progreso»/«proceso», plus a line for the model (W1c) saying that
starting is a transition. Origin: AGENDA-ASISTENTE-S1, bank U05. Decides: agent.

### VOZ-25 — The assistant speaks «tú»; there is no way to ask for «usted» (or «vos») as the reply register

Since A-85 every reply of the voice channel is neutral «tú» and the
recognizer understands both «vos» and «tú». There is no register knob: an
owner who wants to be addressed as «usted» (common in Bogotá) or «vos»
cannot ask for it. The register is how the app treats its owner; today it is
a fixed product decision. **Ready:** a declarable key (`summary`/`ask` block
or `APPXIMO_ASK_TRATO=tu|usted|vos`) choosing the set of forms at
composition time — a conjugation dictionary in the composing layer, never
duplicated strings — built only when a real owner asks. Origin:
AGENDA-ASISTENTE-S1 addendum, decision A-85. Decides: Miguel.

### VOZ-18 — The estimate-vs-real mirror is per task; there is no weekly aggregate («esta semana subestimaste 60 %»)

`espejo_estimacion` sends one message when a task with an estimate closes. A
period summary (sum of estimates vs reals, the bias) does not exist: the
digest does not aggregate two numeric columns against each other. The value
of the mirror is the sustained bias, not one case; after four weeks the
per-task message becomes noise. **Ready:** first four weeks of Miguel's real
estimates (decides whether the bias is constant); then a digest section or an
`aggregate` with a `ratio` of two sums over the period. Origin: APP-AGENDA-S1
Part C. Decides: Miguel.

### ENG-63 — The `/app` lists "Summary" as if it were a resource (0 rows): it takes `/api/summary` from the OpenAPI for a table

The generic panel derives its resources from `/openapi.json`; `/api/summary`
and `/api/ask` are published there and `summary` shows in the sidebar as an
empty resource (seen on the agenda: «SU · Summary · 0»). A phantom resource in
the owner's menu breaks the promise "everything you see comes from the
contract". **Ready:** tag the cross-resource endpoints in the OpenAPI
(`x-appximo-endpoint: summary|ask`, like `x-appximo-virtual-resource` for
files) and have `/app` omit them from the menu (or show them as actions, not
tables). Origin: APP-AGENDA-S1 browser check. Decides: agent.

### AUTO-14 — The installed worker reports `appximo-worker dev (unknown)`: a bare `go build ./cmd/appximo-worker` injects no version

The engine was built with its version (`8bef63c-app`); the worker with a bare
`go build` → `fleet-audit` and the boot line say `dev (unknown)`
(`scripts/build-worker.sh` does pass the ldflags). On a box with three apps
nobody can tell which worker each one runs nor whether a deploy changed it;
`deploy-app --worker-binary` verifies a version and here there is none.
**Ready:** the engine build recipe builds the worker with the same ldflags,
and `install.sh` warns when the worker says `dev`. Origin: APP-AGENDA-S1
Part A. Decides: agent.

### OPS-63 — Nobody tells Telegram when an app is DOWN: the command center sees it (salud:false every 10 min) but does not notify

The engine's alerter reports incidents of a LIVE app (stale/failed backup,
disk, a 500, a stuck outbox, spend); an app that is down cannot report itself.
The command center reads every app's health every 10 minutes and stores the
reading, but has no Telegram destination. Miguel asked that the demos "only
say something if they fall": today a fall of tiendita/petfriendly/agenda shows
in the center's panel, not on the phone. **Ready:** a Telegram destination in
the center (its own `APPXIMO_TELEGRAM_*`) and a rule in the health reader —
on ok→false (and back) send ONE message per app, with two-reading hysteresis.
Origin: APP-AGENDA-S2 Part 2. Decides: agent.
