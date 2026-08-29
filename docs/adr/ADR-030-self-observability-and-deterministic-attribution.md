# ADR-030 — The engine observes its own resources out of band and attributes a bottleneck deterministically; its overhead budget is stated on the proxies that resolve it

**Status:** accepted (CENTINELA-C-S1, 2026-08-29)
**Drivers:** Module C of the Centinela package (internal: `centinela/modulo_C_appximo_auto_observabilidad.md`) — the operator's question *"how much do I, the engine, weigh on the box, and when a k6 / a DoS / a burst arrives, am I the bottleneck, or is it the database, the network, or the plan's CPU quota?"* — and the research report (`investigacion_capacidad_y_huecos.md`) that found two of the spec's promises unverifiable or illegal (the overhead budget; Module B's IP default — the latter is a spec correction, decision A-53, not engine work).

## Context

The engine already had the observability that shows WHAT is slow: per-tenant
HDR latency histograms, a request ring with span breakdowns, SLO burn rate,
the z-score anomaly detector, Prometheus `/metrics` with the Go and process
collectors, persisted slow traces. What it did not have is the layer that
says WHY, from the resources' side: nothing read `runtime/metrics`' scheduler
latencies or GC pauses, nothing read the process's cgroup (`cpu.stat
throttled_usec` — on a cheap VPS the most common "the server feels slow"
cause, and invisible from inside Go), nothing read PSI, and `pgxpool.Stat()`
— the pool's own account of "I asked for a connection and there was none" —
was never sampled. An operator running a load test saw a p99 and guessed.

Anyone can expose `runtime/metrics`. The product is the **verdict**: "the
bottleneck is the exhausted pool, not your code"; "the database is the
bottleneck, not Appximo". The row that matters most is the one that says
*it is not you*.

## Decision

**1. Four layers, read by ONE goroutine on a timer — never on the request path.**
The request path pays exactly two atomic adds and one HDR `RecordValue` under a
mutex (`ResourceCollector.Observe`, called from the logging tap that already
runs per request), and allocates nothing (`TestResourceCollector_ObserveAllocatesNothing`).
Every read of `runtime/metrics`, every cgroup / `/proc` / PSI file, every
`pgxpool.Stat()` and the one SQL statement of the server-side probe happen in
`ResourceCollector.Run` (`pkg/observability/resources*.go`):

| Layer | Source | The fields the verdict reads |
|---|---|---|
| 1 · Go runtime | `runtime/metrics` — a pre-allocated `[]metrics.Sample` re-read in place; histogram buffers re-used by the runtime; cumulative histograms diffed per tick | `/sched/latencies:seconds` (p99 of the tick — runnable goroutines waiting for a CPU, the CPU-saturation signal), `/sched/pauses/total/gc:seconds`, `/cpu/classes/{total,gc/total,idle,user}` (→ GC share of busy CPU, busy fraction of GOMAXPROCS), `/sync/mutex/wait/total:seconds` (which accumulates ONLY while `runtime.SetMutexProfileFraction` > 0 — `runtime/sema.go` stamps `acquiretime` on a contended acquire iff the rate is set, verified in this session when the first lock provocation read 0 under real contention; the engine sets 1e6 when no rate is set: every contended acquire is timed, ~none is profiled), `/gc/cycles/total`, heap live / goal / GOMEMLIMIT / GOGC, goroutines, GOMAXPROCS |
| 2 · process / cgroup v2 | the cgroup named by `/proc/self/cgroup` (re-resolved EVERY tick with an allocation-free byte compare — a process can be moved after boot, and the first provocation found a 30-tick recheck still reading the old cgroup), files opened once and re-read with `pread(2)` into fixed buffers; `/proc/self/status` always (RSS, HWM, threads — `runtime/metrics` has no thread count) | `memory.current`, `memory.max`, `memory.peak` (5.19+; VmHWM otherwise), `memory.swap.current`, `cpu.stat` (`usage_usec`, **`nr_throttled`, `throttled_usec`**), `cpu.max`, `pids.current/max`; `cgroup_shared` when the cgroup holds more than this process (a login session scope) so memory.current is read as the cgroup's, not the process's |
| 3 · host pressure | PSI — the cgroup's own `cpu/memory/io.pressure` first, `/proc/pressure/*` second, `unavailable` third (each answer says which) | `some avg10/60/300`, `full`, totals |
| 4a · database, client side | `pgxpool.Stat()` through a func (no pgx import in the package) | `AcquiredConns == MaxConns && IdleConns == 0` (saturated), **`EmptyAcquireCount`**, **`EmptyAcquireWaitTime`** (deltas per tick), `CanceledAcquireCount`; plus the client-side **query stage p50/p99** from the request's `query`/`count` spans, in its own windowed HDR |
| 4b · database, server side | `pg_stat_database` + `pg_database_size` + `pg_stat_activity` counts, ONLY when the DSN host is loopback / a unix socket; every 10 s; one pool connection acquired with a 250 ms timeout — under exhaustion the probe reports *skipped: pool busy* instead of competing with requests | size, cache hit ratio, backends active / waiting / idle-in-tx, deadlocks, temp bytes, `pg_stat_statements` presence |

A remote database is reported as **`observable: false` with the reason** —
correct by design, not a gap: the app cannot see another box's RAM.

**2. Two cadences and a fixed ring.** Background 10 s (the cost a box pays
24/7); **live 1 s while someone is looking** — the `/admin` Resources view
sends `?live=1` on every poll and the collector decays back 60 s after the
last one. The correlation series is a fixed array of 900 ticks (15 min live,
2.5 h background), never a slice that grows. The tick allocates two objects
by declaration (the published copy for the lock-free readers and the
verdict's evidence slice) and nothing else
(`TestResourceCollector_TickAllocatesNothing` pins the budget at 2).

**3. The verdict is deterministic, ranked, and carries its evidence.** A fixed
set of rules over the tick's numbers plus a healthy p99 baseline (EWMA over
previous healthy ticks), evaluated in a fixed order, each with a written
threshold — `pkg/observability/attribution.go`. The vocabulary is exactly the
spec's: `cpu_saturated | gc_pressure | cpu_throttled | pool_exhausted |
db_bound | memory_pressure | lock_contention | healthy`.

The shared gate, *latency_high*: p99 ≥ 50 ms (`APPXIMO_SELFMON_P99_MS`, the
one threshold an operator plausibly tunes — "what is slow for MY app"), or
p99 ≥ 3× the healthy baseline and ≥ 10 ms. A window under 1 rps is idle →
healthy ("no traffic").

| Rank | Verdict | Fires when | Owner |
|---|---|---|---|
| 1 | `cpu_throttled` | cgroup source and `nr_throttled` delta > 0 and `throttled_usec` delta ≥ 2 % of the interval (20 ms of every second stopped) | host — the plan's quota |
| 2 | `memory_pressure` | `memory.current` ≥ 90 % of `memory.max`, or live heap ≥ 90 % of GOMEMLIMIT, or memory PSI some avg10 ≥ 10 % | appximo (limit) / host (reclaim) |
| 3 | `gc_pressure` | GC ≥ 25 % of busy CPU, or STW p99 ≥ 5 ms with ≥ 2 cycles/s — with latency_high (or GC ≥ 40 % on its own) | appximo |
| 4 | `cpu_saturated` | sched latency p99 ≥ max(2 ms, 5 % of the request p99) — the wait must be MATERIAL (a shared vCPU shows ~1 ms wakeup latency next to a 690 ms lock wait) — corroborated by CPU PSI some avg10 ≥ 10 % (or ≥ 85 % of GOMAXPROCS busy when PSI is unavailable) — with latency_high (or sched p99 ≥ 5 ms on its own) | appximo, or the box's sizing |
| 5 | `pool_exhausted` | requests found no free connection (EmptyAcquireCount delta > 0) AND the time spent waiting for one (EmptyAcquireWaitTime delta, summed over waiters) ≥ 10 % of the interval — or, with latency_high, the pool saturated at the tick or ≥ 2 % waited. Judged by the wait TIME, never by the instantaneous acquired/max alone (a 2-connection pool behind a 40 ms database oscillated 1/2 ↔ 2/2 and the tick boundary read "healthy") | database / pool config — the reason says whether the pool is undersized or the queries hold it (query p99 ≥ ½ request p99) |
| 6 | `db_bound` | latency_high AND queries ran AND query p99 ≥ 50 % of request p99 AND CPU not hot AND GC not hot | **the database or the network — not Appximo** |
| 7 | `lock_contention` | `/sync/mutex/wait/total` delta ≥ 10 % of the interval | appximo |
| 8 | `healthy` | none of the above; a slow window with no rule → "the time is in the handler itself, or in an external call it makes" | none |

Ranking rule: **the cause furthest from the operator's code wins** — a quota
explains a scheduler queue; GC explains CPU; a slow database explains a full
pool. Lower-ranked rules that also fired are listed under `also`, and every
rule input is listed as a signal with its value, its threshold and whether it
fired — the evidence under the sentence. Over a window (the load-test view)
the dominant verdict is the most frequent non-healthy one if it covers ≥ 10 %
of the traffic-bearing ticks (`Summarize`); a single stray tick is noise.

**No language model is on this path, and none will be.** Text-to-SQL reaches
73 % execution accuracy on BIRD against 92.96 % for a human expert and ~21 %
on Spider 2.0 (the research report §Bloque 6); a model interpreting metrics
hallucinates correlations and invents causality. If a narrative is ever
wanted, it will be written OVER numbers and causal claims produced here, with
the rule that the model may introduce neither — noted, not built.

**4. The surfaces reuse what exists.** `GET /admin/resources` (+ `/snapshot`,
the exportable JSON of a run — `appximo.selfmon.snapshot/v1`, with the build,
the host and every tick) on the platform admin API — platform token or admin
key, **never a tenant admin** (the box's RAM, cgroup and pool are not a
tenant's to see); `GET /debug/resources` on the admin-key debug router; 21
`appximo_selfmon_*` gauges on the existing Prometheus registry (`/metrics`),
projected from the latest tick with no extra read. The `/admin` panel gains a
Resources view — live board, load-test window with the verdict and the
correlation charts, snapshot export + before/after comparison — generic by
construction, and the console shell gains its first mobile layout (a drawer
under 720 px).

**5. The overhead budget is stated on what the instrument resolves (A-54).**
Not "< 1 % of p99": the 1 % of a 1.6 ms p50 is ~0.016 ms against a protocol
whose smallest detectable effect is 0.5 ms — ~30× below the instrument's
resolution, and the standard error of a tail percentile plus a shared vCPU's
noise bury it. The budget is **≤ 1 % in allocations per operation and CPU
seconds under identical load (`testing.B -benchmem` + `benchstat`, N ≥ 10, p
< 0.05) and ≤ 1 % of steady-state RSS after GC; the p99 effect is declared as
an upper bound (≤ the 0.5 ms the ABBA protocol resolves), never as "no
effect"**. Measured values: docs/BENCHMARKS.md §4c. Corollary for the whole
house (OPS-35): Mann-Whitney compares stochastic dominance, not a percentile —
a claim about the tail needs a permutation test on the Δp99 or a bootstrap of
the difference (`tools/devhub/stats.PermutationQuantileDiff`).

## Knobs

`APPXIMO_SELFMON=off` disables the collector (Config.SelfMonDisabled);
`APPXIMO_SELFMON_INTERVAL` (background, default 10s, floor 250ms),
`APPXIMO_SELFMON_LIVE_INTERVAL` (default 1s), `APPXIMO_SELFMON_P99_MS`
(default 50). A value that does not parse refuses to boot naming the variable
(OPS-13 discipline).

## Alternatives considered

- **eBPF / a sidecar (Parca, OTel collector).** Dead weight on a 1–2 GB box
  shared with Postgres; the engine already has in-process visibility of its
  own stacks. Reconsider only on ≥ 8 GB boxes (research §Bloque 4).
- **Continuous block/mutex profiling.** 5–20 % overhead at rate 1 (vendor
  numbers, unverified); `/sync/mutex/wait/total` gives the contention signal
  for free. pprof stays on demand.
- **A per-request span for the pool acquire.** Would put a timer on the hot
  path; `EmptyAcquireWaitTime` from the pool's own counter is the same
  information at zero request cost.
- **Reading pg_stat_* on a remote database too.** The views are readable
  remotely, but the verdict cares about the box's RAM/CPU/IO, which are not;
  publishing half a picture as "observable" would mislead. Explicitly
  not observable is the honest answer.
- **A model to narrate the verdict.** See Decision 3.

## Consequences

- One new goroutine, one timer, ~520 KB of ring (900 × ~580 B) — the RSS proxy
  of A-54 in bytes.
- `logging.RequestTap` consumers now run for tenant-less requests too (the
  self-monitor counts every non-infra request); the per-tenant metrics still
  skip them.
- The admin panel's built assets grow by 30,049 B (a 25 KB Resources chunk,
  4 KB of CSS, 1 KB in the index); ECharts moved to a chunk shared by the two
  charting views without growing.
- What the verdict CANNOT see, said: a slow external call inside a handler
  (reported as "the handler itself"), a network hop to a remote database
  (reported as db_bound with the database, which is the right owner), disk
  I/O of a remote Postgres, and anything on a box without cgroup v2 / PSI
  (each layer reports `unavailable`; the rules treat that as no signal, never
  as healthy).
