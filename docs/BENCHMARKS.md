# Production benchmarks — the whole stack, on a real server

> **Re-certification status (CERTIFY-S1, 2026-08-01).** Partially re-measured. The
> layer cost and the 1 M-row read path were re-run and are consistent (§3, §4); the
> **whole-stack footprint** and the **750 rps knee** could NOT be reproduced, because
> the reference box now serves two production apps and the API path would need a new
> Caddy site on it. Those two tables are historical, valid as of the date above them.
> Details: [CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md) §2.4.

> **Don't take these numbers. [Run the suite](../scripts/verify-production/) against
> your own server and get your own.**

Everything here measures the **production stack as a user actually runs it** —
Caddy terminating real Let's Encrypt TLS → the engine under systemd → native
PostgreSQL, on one $16/mo droplet — not the engine in isolation. That distinction
is the whole point: the [existing engine benchmark](../context-docs/BENCHMARK_PUBLIC.md)
measured a bare binary over plain HTTP, which is not what anyone deploys.

Every number below was produced by [`scripts/verify-production/`](../scripts/verify-production/),
which writes the raw JSON alongside the report. Reproduce it with one command:

```bash
bash scripts/verify-production/run-all.sh \
  --target=https://api.example.com --server-ssh=root@YOUR.IP
```

---

## The reference box

| Role | Machine |
|---|---|
| **Server under test** | DigitalOcean droplet, **2 vCPU / 1963 MiB RAM**, Ubuntu 22.04, $16/mo |
| Stack | Caddy 2.8.4 (static binary) · Appximo engine (systemd, `GOMAXPROCS=2`) · **PostgreSQL 18.4** (native apt) |
| TLS | real Let's Encrypt certificate for a public domain |
| **Load generator** | a separate 1 vCPU droplet, same region · RTT floor **~1.3 ms** |
| Dataset | 1,000,000 orders · 3,200,000 order_items · 50,000 customers · **673 MiB** on disk |

Installed by `scripts/install.sh` with no manual steps.

---

## 1. Before any number: two things that make benchmarks lie

Both were hit while producing this document, and both would have published wrong
numbers if left undetected.

### A CDN in front of the domain

`api.appximo.com` is proxied by Cloudflare. The public name resolves to
Cloudflare's edge, so every "HTTPS" measurement crossed the internet twice and was
terminated by Cloudflare's TLS, not ours. Measured that way the stack looks
**3.5× slower than it is**:

| Path | p50 |
|---|---:|
| the origin — Caddy TLS → engine → PostgreSQL (**the product**) | **2.78 ms** |
| through the Cloudflare edge (what an end user hits) | 9.75 ms |

Both are true; they answer different questions. The suite detects a CDN and, with
`--origin-ip`, measures the origin — otherwise it warns loudly that the number
includes someone else's network.

### The response cache

The engine caches GET responses for 5 s, keyed by tenant + request URI. Point a
load generator at one URL and you measure the cache. So every read result below is
reported in **both** arms — `cache on` (the production default, modelling many
clients loading the same page) and `cache bypassed` (every request URI unique, so
every request reaches PostgreSQL). **The bypassed number is the floor** and the one
to quote when someone asks what the stack really sustains.

---

## 2. Footprint — it fits on a small box

PSS, not RSS: RSS double-counts the shared pages every PostgreSQL backend maps,
which is how PostgreSQL gets reported as several times its real size. `anon` is
memory the process genuinely owns; the rest is mapped-binary page cache the kernel
reclaims under pressure — so **`anon` is the number that decides "does this fit in
1 GB"**.

| State | Service | PSS MiB | **anon MiB** |
|---|---|---:|---:|
| idle, empty DB | PostgreSQL | 62.6 | 15.8 |
| idle, empty DB | Caddy | 47.4 | 10.6 |
| idle, empty DB | engine | 42.0 | 11.1 |
| idle, empty DB | **whole stack** | **152.0** | **37.5** |
| 1M rows, idle | PostgreSQL | 175.1 | 19.4 |
| 1M rows, idle | engine | 85.3 | 67.6 |
| 1M rows, idle | Caddy | 47.1 | 22.1 |
| 1M rows, idle | **whole stack** | **307.5** | **109.0** |
| 1M rows, **under load** | PostgreSQL | 211.0 | 24.0 |
| 1M rows, **under load** | engine | 138.3 | 107.0 |
| 1M rows, **under load** | Caddy | 58.7 | 22.5 |
| 1M rows, **under load** | **whole stack** | **408.1** | **153.5** |

**The whole stack owns 37.5 MiB at rest on an empty database, 109 MiB with a
million rows, and 153.5 MiB under sustained load — leaving ~1.29 GB of the 2 GB
box available at peak.** There is no OOM risk at this scale, and `dmesg` recorded
zero OOM kills across the entire session including the deliberate memory-pressure
test. **The binding constraint on this hardware is CPU, not memory.**

CPU under that same load (200 rps mixed read/write, cache bypassed) splits as
**engine 27%**, **Caddy 23%**, **PostgreSQL 12%** of one core — note that Caddy
costs almost as much CPU as the engine it fronts, which is the honest price of
TLS termination.

On a **1 GB** box the arithmetic still works — the stack's own anonymous memory
is the same ~150 MiB, `install.sh` sets `GOMEMLIMIT` to 30% of RAM (~300 MiB) and
`shared_buffers` to 25% (~250 MiB) — but the margin for page cache shrinks, so a
dataset much larger than RAM will do more disk I/O.

---

## 3. What the production layers cost

Measured externally, decomposed one layer at a time against the same endpoint
(tiny payload, so the numbers are the *layers*, not the query):

| Path | p50 | added by that layer |
|---|---:|---:|
| engine directly (plain HTTP) | 1.81 ms | — (includes the ~1.3 ms RTT floor) |
| \+ Caddy reverse proxy (still plain HTTP) | 2.52 ms | **+0.71 ms** |
| \+ TLS — **the full production stack** | 2.78 ms | **+0.26 ms** |
| \+ Cloudflare edge | 9.75 ms | +6.97 ms *(not ours)* |

**Running the production stack instead of the bare engine costs ~0.97 ms at the
median** (2026-06-10). That is the price of automatic HTTPS, and it confirms the
~0.5 ms estimate `docs/DEPLOY.md` carried without measurement.

> **Re-measured 2026-08-01 (CERTIFY-S1): +1.22 ms.** Engine direct 1.829 ms →
> through Caddy + TLS 3.052 ms, on the same box now serving two apps. The two live
> apps agreed to 0.01 ms with each other, so the figure is stable; it is simply a
> slightly higher layer cost than the original run measured. Both numbers stand,
> each with its date and condition — see
> [CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md) §2.4.

Two hypotheses were tested and **rejected** — neither made a measurable difference,
so neither is recommended:

- sizing Caddy's upstream keep-alive pool (`keepalive_idle_conns_per_host`):
  `no_change`; Caddy already reuses connections.
- disabling Caddy's access log: `no_change`. Caddy answering a static route costs
  **0.95 ms**, so Caddy itself is not a bottleneck at any rate this box can reach.

---

## 4. Scale — indexed reads do not care how big the table is

Single-query cost measured in PostgreSQL at **1,000,000 orders**:

| Query | Execution time |
|---|---:|
| filtered + sorted page (`status=paid ORDER BY created_at DESC LIMIT 20`) | **0.127 ms** |
| two-predicate filtered page | **0.295 ms** |
| unfiltered newest-20 | **0.109 ms** |
| `COUNT(*)` of a filtered set (`?count=true`) | 44.8 ms |
| unfiltered `GROUP BY` aggregate | 214 ms |

End-to-end through the full stack, cache bypassed, as the table grows:

| Dataset | filtered page (p50) | `?include=` nested (p50) |
|---|---:|---:|
| 100K orders | 7.03 ms | 7.81 ms |
| 500K orders | 4.54 ms | 7.13 ms |
| **1M orders / 3.2M items** | **4.44 ms** | **7.09 ms** |

And the capacity ladder at 1M rows, cache bypassed (every request reaching
PostgreSQL), over real TLS from an external loader:

| Offered rate | p50 | p95 | Errors | Verdict |
|---:|---:|---:|---:|---|
| 100 rps | 4.63 ms | 6.26 ms | 0% | clean |
| 250 rps | 4.61 ms | 14.95 ms | 0% | clean |
| **500 rps** | **6.61 ms** | 35.17 ms | 0% | **clean — highest sustained** |
| 750 rps | 240.75 ms | 684.97 ms | 0% | **knee** — only 658 of 750 rps delivered |

**On 2 vCPU / 2 GB, with a million rows and every request hitting PostgreSQL, the
stack sustains 500 rps and its knee is at 750 rps.** With the response cache
doing its normal job (a page many clients load), the same read is **2.94 ms**.

**A paginated, filtered, sorted query over a million rows answers in ~4.4 ms
end-to-end over real HTTPS, and does not degrade as the table grows** — the index
does the work, so cost tracks the page size, not the table size.

### The honest limit: aggregates are O(table)

An unfiltered `GROUP BY` must read every row. At 1M rows that is ~214 ms of CPU per
query, so **two cores sustain single-digit aggregates per second, not hundreds** —
at 100 rps the box queues and sheds. This is arithmetic, not a bug, and no tuning
changes it. If you need constant-time dashboard totals, filter the aggregate,
cache it, or maintain a rollup table. `?count=true` has the same shape (44.8 ms at
1M rows), which is exactly why it is **off by default**.

---

## 4b. Field selection — the disk, not the wire (MOTOR-FIELDS-S1)

The case a real migration reported (Symfony → v0.1.10): `GET
/api/declarations` returned the whole `data` document of every row although
it paginated — ~940 KB per page of 20, p99 3.8 s — for a list showing a
NIT, a year and a status. Rebuilt on the dev box (1 vCPU, local Postgres 16
in Docker): **46,119 rows, a ~52 KB `json` document each, TOAST 1.8 GB, heap
7.5 MB**. Measured 2026-08-28.

**Why `?fields=` has to reach the SELECT.** The document lives in TOAST; a
`SELECT *` sent to a client detoasts it for every row (the server's output
function must). `EXPLAIN (ANALYZE, BUFFERS)` cannot show this — it discards
the rows, so both plans of page 1000 report the same 20,094 buffers — but the
cumulative statistics can: with the rows consumed, **10 pages of 20 read 1,300
TOAST blocks without `fields=` and 0 with it** (`pg_statio_user_tables`,
engine requests, forced flush). A projection applied in Go would keep every
one of those blocks.

**Single pages, `Server-Timing` of the engine (same binary):**

| Page | without `fields=` | `fields=nit,anio,estado,contador_id` |
|---|---|---|
| 1 | 961,702 B · query **53 ms** | 3,059 B · query **1.2 ms** |
| 1000 | 961,721 B · query **294 ms** | 3,065 B · query **15 ms** |
| 2305 (last) | 961,757 B · query **139 ms** | 3,068 B · query **59 ms** |

The last row is the honest part: the deep-page cost that remains with
`fields=` is the OFFSET (the index scan walks 46k entries: ~20k buffers,
10–13 ms in the plan, more under load) — not the document. Keyset cursors
avoid it; the `/app` pages by number on purpose and shows the cost.

**Under load — k6, 10 rps × 60 s, a random page of the 2,305, same binary:**

| Arm | bytes received | p50 | p95 | p99 | max | dropped |
|---|---|---|---|---|---|---|
| without `fields=` | 575 MB | 174 ms | 2.25 s | **2.8 s** | 3.33 s | 4 |
| `fields=nit,anio,estado,contador_id` | 2.2 MB | 20 ms | 81 ms | **175 ms** | 356 ms | 0 |

Same 10 rps: the plain list saturates a 1-vCPU box (each response is ~1 MB
of JSON to detoast, encode and ship — the queue builds and the tail goes to
seconds, the report's regime); the projected list is a small indexed read.
The `/app` sends `fields=` on every list, board, CSV and label request.

**The pre-feature binary on the same URL** (built from the base commit in a
worktree, `build-engine.sh`): ignores `fields=` — page 1 is 961,702 B with
and without it, query 56 / 175 ms — so a client that adopts `fields=` before
its engine is upgraded gets the old behaviour, never an error. Its plain-list
arm in the 10 rps series measured p50 2.76 s / p99 6.06 s with 30 dropped —
NOT the code: it is the same statement as the new binary's plain arm (the
binary-diff gate says so), and 10 rps of 1 MB responses sits at the
saturation edge of a 1-vCPU box, where the arm that runs last (hot box,
queue already built) loses. Attributed with an alternating series at 6 rps
(30 s each, A B A B, then fields): base-plain med **116 / 93 ms**, new-plain
med **102 / 77 ms** (p99 165 / 150 vs 420 / 124 ms — noise of the box, not a
direction), new-fields med **19 ms** (p99 75 ms). The controlled base-vs-new
comparison of the PLAIN list is the frozen ABBA protocol on benchblank
(`no_change`, see the session report); this section answers "with vs without
`fields=`", which is what the report asked.

**GraphQL** pushes its selection set into the same projection (it used to
select in Go over `SELECT *` and read every document): `{ declarations { data
{ id nit } } }` runs `SELECT "id", "nit" FROM …`.

---

## 4c. The engine watching itself — what the self-monitor costs (CENTINELA-C-S1)

The resource collector ([ADR-030](adr/ADR-030-self-observability-and-deterministic-attribution.md))
reads `runtime/metrics`, the process cgroup, PSI and `pgxpool.Stat` on ONE
goroutine every 10 s (1 s while `/admin` → Resources is open) and attributes
a bottleneck deterministically. Its cost is stated on the proxies decision
A-54 names — not on "< 1 % of p99", which this box cannot resolve (§7 above).

**1. Allocations and bytes per request — the deterministic proxy.** A full
generated read (`GET /api/tasks?per_page=20`, cache bypassed, the real
middleware chain, Postgres in a container) through `App.buildRouter`, collector
ON (ticking in the background) vs OFF, `go test -bench Request_SelfMon
-benchmem -count 10 -benchtime 2000x` + `benchstat`:

| | sec/op | B/op | allocs/op |
|---|---:|---:|---:|
| collector ON | 1.114 ms ± 23 % | **86.99 KiB ± 0 %** | **800.0 ± 0 %** |
| collector OFF | 1.969 ms ± 25 % | **86.99 KiB ± 0 %** | **800.0 ± 0 %** |

The request path adds **zero allocations and zero bytes** — the two atomic
adds and the HDR record are the whole cost, and `TestResourceCollector_ObserveAllocatesNothing`
pins it. (The sec/op column is what a shared 1-vCPU box does to a wall-clock
microbenchmark: ±25 % and the ON arm "faster" — noise, reported as such.)

**2. CPU-seconds under identical load** — `cpu.stat usage_usec` of the
engine's own cgroup over 60 s of 100 rps (k6, `constant-arrival-rate`,
cache bypassed), 10 runs per arm alternated ABBA-style; the ON arm is the
WORST case (1 s ticks AND a client polling `/debug/resources` every second):

| arm | n | CPU-seconds median | mean | min–max |
|---|---:|---:|---:|---|
| ON, 1 s + polled every second | 10 | 6.29 | 6.65 | 5.59–8.96 |
| OFF | 10 | 6.62 | 6.78 | 5.89–8.49 |
| ON, background 10 s, nobody polling | 3 | 7.17 | 7.40 | 7.03–7.98 |

Δmedian ON−OFF = **−4.9 %**, Mann-Whitney p = 0.68, bootstrap 95 % CI of the
median difference **[−14.7 %, +16.1 %]**; between-run CV 13–15 % (two host
stalls of 8–9 s in ten runs, one per arm). **This box does not resolve a 1 %
effect on CPU-seconds either**: the research's premise ("integrating over 60 s
averages the noise") holds on a quiet host, not on a $16 shared vCPU where the
neighbours stall for whole seconds. What the numbers DO bound: the collector's
CPU cost is inside the ±15 % run-to-run noise — an upper bound of a few
percent, not a measurement of 0.x %. The analytic cost is smaller: one tick
reads ~17 `runtime/metrics` samples, 12 pseudo-files and one `pool.Stat()`
(microseconds), and the 10 s cadence makes that 0.01 % of a core.

**3. Steady-state RSS after GC** (20 s idle after the load, `VmRSS`):

| arm | n | RSS median (MiB) | spread |
|---|---:|---:|---|
| ON, 1 s + polled | 10 | 55.4 | 54.5–58.3 |
| OFF | 10 | 53.1 | 50.8–54.4 |
| ON, background, no polling | 3 | 54.3 | 54.1–54.4 |

Δ = **+2.3 MiB (+4.4 %) polled, +1.15 MiB (+2.2 %) in background**
(MWU p = 0.0002; CI [+1.7, +4.5] MiB). This is the one proxy that resolves
the collector, and it is **over the 1 % budget** the spec set (≈ 0.55 MiB of a
55 MiB process). Declared in bytes, not measured through the noise
(`TestResourceCollector_FootprintBytes`): the fixed footprint is
**1,118,992 B (1.07 MiB)** — the 900-slot ring at 1,152 B per snapshot
(1,036,800 B) plus four windowed HDR histograms at 2 significant figures
(~82 KB). The session already cut it from 2.2 MiB (the verdict's evidence
slices no longer live in the ring — they are recomputed on read — and the
HDRs went from 3 to 2 significant figures). What remains IS the ring: 15
minutes of 1 s correlation or 2.5 hours of background history for 1 MiB. The
polled arm's extra ~1.2 MiB is heap the JSON serving of a 60-tick series
leaves behind — a client's cost while it looks, not the box's 24/7 cost. The
budget is missed at 2.2 %, deliberately, and said.

**4. The p99, as an upper bound.** 60,010 per-request latencies per arm
(the ten runs pooled): p50 2.02 → 1.98 ms, p99 15.43 → 12.89 ms (ON faster,
Δp99 −2.5 ms, permutation p = 0.015, bootstrap CI [−4.2, −0.3] ms). A
"significant" improvement from a collector that adds no allocation is the
lesson of A-54 in one line: at 60 k samples the tests resolve the HOST's
drift between minutes, not the code. Reported as: **the collector's effect on
the p99 is not distinguishable from ±2.5 ms of host drift; upper bound
≤ 2.5 ms, direction unknown**. The frozen ABBA read protocol (§the verdict
below) gates the change on the median as every change is.

## 5. REST vs GraphQL — the same data, both ways

Same logical query (orders with their customer and their line items), 100 rps,
**response cache bypassed on both arms** — a GET would otherwise be served from
cache while a GraphQL POST never is, which would compare a cache to a database.

| Shape | Round trips | p50 | p95 | Bytes/request |
|---|---:|---:|---:|---:|
| REST `?include=customer,items` | 1 | **6.89 ms** | 9.84 ms | 18,628 |
| GraphQL, one nested query | 1 | 11.01 ms | 27.04 ms | **11,652** |

**REST is 4.1 ms faster at the median; GraphQL sends 37% fewer bytes.**

Both fetch everything in ONE request and ONE database round trip — `?include=`
and the GraphQL resolver share the same `LATERAL` query — so the usual argument
for GraphQL, *fewer round trips*, does not apply: REST already collapses them.
What GraphQL genuinely buys is **field selection** (REST's `?include=` returns
every column; the GraphQL query names what it wants), and that is paid for with
~60% more server latency to parse, validate and resolve the document.

**For a frontend:** default to REST with `?include=` — faster, cacheable (it is a
GET, so the engine's response cache serves it), easier to debug. Reach for
GraphQL when payload size genuinely matters (mobile, poor networks) or when
different views need very different field sets from a wide resource.

---

## 6. Resilience — measured, not asserted

Each fault was injected while a probe kept requesting at 20 rps, so "requests
lost" is **counted, not inferred** — the probe records every outcome, including
connection-refused and timeouts.

| Fault | Requests lost | What the user saw | Outage | Recovered alone |
|---|---:|---|---:|---|
| **SIGKILL the engine** | 90 / 896 | clean `502` from Caddy | **4.5 s** | yes (systemd) |
| **SIGKILL Caddy** | 6 / 636 | connection refused | **1.0 s** | yes (systemd)† |
| **Stop PostgreSQL 15 s** | 227 / 1169 | clean `500` | **11.9 s** | yes — engine survived |
| **Exhaust the connection pool** (1500 rps burst vs a 10-connection pool) | **0** / 891 | nothing | none | yes |
| **Memory pressure** (400 rps of large nested pages) | **0** / 729 | nothing | none | yes, no OOM |
| **`deploy-update.sh` under live traffic** | 7 / 725 | clean `502` | **0.47 s** | yes |
| **Reboot the whole box** | 468 / 2913 | refused / timeout | **27.8 s** | yes, cert intact |

† **This session found and fixed a real bug.** Caddy's unit had **no `Restart=`
directive at all**, so `SIGKILL`ing it took the site down *permanently* — the
engine was healthy the whole time, but the front door never came back and every
request was refused until a human intervened. `install.sh` now writes a systemd
drop-in (`Restart=always`, `RestartSec=2s`, `StartLimitIntervalSec=0`) for Caddy
however it was installed. With the fix, the same kill costs **1.0 s**.

Notable, and to the stack's credit:

- **The engine survives losing its database.** Through a 15 s PostgreSQL outage
  it never crashed and systemd never restarted it (`NRestarts=0` across the
  window); it returned clean `500`s, logged latency anomalies, and reconnected by
  itself when PostgreSQL came back.
- **Overload degrades, it does not collapse.** A 1500 rps burst against a
  10-connection pool did not cost a *single* request for the concurrent traffic:
  each query is bounded by a 5 s timeout (`pkg/db/tenant.go`) and the circuit
  breaker sheds with `503` rather than piling onto PostgreSQL.
- **Zero OOM kills, ever** (`dmesg`), including under deliberate memory pressure.

One honest wart: an overloaded query is masked as **`500`**, when `503` would
tell a client "retry later" rather than "this is broken". Worth changing.

---

## 7. Tuning — what actually helped

The rule: a change is kept only if it is **both** statistically significant
(Mann-Whitney p < 0.05) **and** practically material (median moved more than
max(0.5 ms, 3%)). Everything else is reported as `no_change` and dropped.

| Change | Before p50 | After p50 | Delta | Verdict |
|---|---:|---:|---:|---|
| **Composite index `(status, created_at)`** | 21.42 ms | **4.39 ms** | −17.0 ms (−79.5%) | **improvement** (p≈0) |
| PostgreSQL sizing — aggregate workload | 369.4 ms | **229.3 ms** | −140.1 ms (−37.9%) | **improvement** (p≈0) |
| PostgreSQL sizing — indexed read workload | 4.62 ms | 4.66 ms | +0.04 ms (+0.9%) | `no_change` |
| Caddy upstream keep-alive pool sizing | — | — | — | `no_change` — rejected |
| Disabling Caddy's access log | — | — | — | `no_change` — rejected |

### What Mann-Whitney does NOT tell you — the tail needs its own test (CENTINELA-C-S1, OPS-35)

The verdict above is the right one for what it gates: the **median**. Mann-Whitney
U compares stochastic dominance between two samples — in practice, whether one
tends to be larger than the other — **not a specific percentile**. A change that
moves only the tail (the p99) while the bulk stays put passes it with p ≈ 1. So a
claim about the p99 needs a test on the p99: a **permutation test on the
Δp99** (pool both samples, re-label them thousands of times, recompute the
difference of p99s; the fraction of permuted differences at least as large as
the observed one is the p-value) or a **bootstrap of the difference of p99s**.
The DevHub statistics package ships `stats.PermutationQuantileDiff` for exactly
this (pinned by a test where the medians are identical and 2 % of one sample
carries a 20 ms tail). Two honest limits come with it: (1) the p99 of a
30-second run at 100 rps is estimated from ~30 tail points, so its confidence
interval is wide by construction; (2) on this box the smallest effect the
protocol resolves is **0.5 ms** — a "< 1 % of p99" promise (≈ 0.016 ms on a
1.6 ms p50) is **not measurable here at all**, and the project stops making it.
A cost that small is stated on the proxies that DO resolve it — allocations per
operation and bytes per operation (`go test -bench -benchmem -count=10` +
`benchstat`), CPU-seconds accumulated under identical load (`cpu.stat
usage_usec` of the process cgroup, ON vs OFF, alternated), and steady-state RSS
after GC — and the p99 is reported as an **upper bound** («Δp99 within the
instrument's resolution, ≤ 0.5 ms»), never as "no effect". §4c below applies
this to the engine's own resource collector.

### The composite index — the biggest finding in this document

This is a **schema-design lesson every Appximo user will hit**, and it is
invisible on a fresh dataset.

The read is `WHERE status = 'paid' ORDER BY created_at DESC LIMIT 20`, with
separate indexes on `status` and on `created_at`. PostgreSQL picks the **sort**
index and filters as it scans. That is fine until rows that *don't* match the
filter start accumulating at the top of the sort order — which is exactly what a
write workload does. After the benchmark inserted ~47,000 `pending` orders:

```
Index Scan Backward using idx_orders_created_at
  Filter: (status = 'paid')
  Rows Removed by Filter: 47463        ← the scan walks all of them, every query
  Buffers: shared hit=8316             ← 0.127 ms became 13.02 ms
```

Declaring one composite index in the schema turns it into a range scan:

```json
"indexes": [ { "fields": ["status", "created_at"] } ]
```

```
Index Scan Backward using idx_orders_status_created_at
  Index Cond: (status = 'paid')        ← no filtering at all
  Buffers: shared hit=13               ← 0.041 ms
```

End to end, at 100 rps with the cache bypassed: **p50 21.4 → 4.4 ms, p95 296.9 →
6.0 ms.** And the realistic 80/20 read+write mix, which had *collapsed* to a p50
of **2,173 ms** at 200 rps, went to **4.07 ms with zero errors** — the same
workload, one index.

Two things worth noting: the index was applied through **the product's own
migration path** (`PUT /tenants/{id}/schema`) on a live 1M-row table in **2.2 s**;
and the rule is general — **for a filtered + sorted list, index (filter column,
sort column) together**, equality column first.

### PostgreSQL sizing

PostgreSQL ships `shared_buffers = 128 MB` and `effective_cache_size = 4 GB`
regardless of the machine. On a 2 GB box the first is small for a 673 MiB
dataset and the second describes cache that cannot exist. `install.sh` now sizes
both from the box's own RAM (25% and 55%), plus `work_mem`,
`maintenance_work_mem` and `max_connections`.

It bought **−37.9% on the scan-heavy aggregate** and **nothing on indexed reads**
(they were already served from cache) — an honest, unexciting result, and the
reason both are reported rather than only the good one.

`GOMEMLIMIT` also changed, for correctness rather than speed: the installer used
to set **1536 MiB on a 2 GB box**, a soft ceiling *above* what the machine can
actually give once PostgreSQL takes its share — so the Go GC would never tighten
before the box was already dead. It is now **30% of RAM** (588 MiB here), which
measurement shows is generous: the engine's own anonymous memory peaked at
**67.6 MiB** with a million rows and live traffic.

---

## 8. The verdict

**Is this stack recommendable for production? Yes, with a clearly-drawn boundary.**

What it does well, measured:

- **It is genuinely small.** 37.5 MiB of real memory at rest, 153.5 MiB under
  sustained load, zero OOM kills ever. A small 1–2 GB VPS is not a compromise here.
- **It is fast where it matters.** A filtered, sorted, paginated page over a
  million rows: **4.4 ms end to end** over real TLS. The production layers
  (reverse proxy + HTTPS) cost about **1 ms** — the convenience of automatic
  certificates is nearly free.
- **It degrades instead of collapsing.** A burst 150× wider than its connection
  pool cost the concurrent traffic **zero requests**: queries are timeout-bounded
  and the circuit breaker sheds load rather than piling onto PostgreSQL.
- **It heals itself.** Kill the engine → back in 4.5 s. Kill Caddy → 1.0 s. Take
  PostgreSQL away for 15 s → the engine never even restarts, it just reconnects.
  Reboot the machine → everything returns in 27.8 s with the certificate intact,
  no human involved.
- **Deploys are effectively invisible.** A real binary swap under live traffic
  cost **0.47 s** and 7 requests.

Where the limit is, honestly:

- **~500 req/s sustained on 2 vCPU** with every request reaching PostgreSQL
  (knee at 750). With the response cache doing its normal job, considerably more.
  That is comfortably enough for a SaaS with thousands of daily users, and not
  enough for a consumer app at national scale — for which the answer is a bigger
  box, since **there is no clustering story**: scale here is vertical.
- **Aggregates are O(table)** and no tuning fixes that. Budget single-digit
  unfiltered `GROUP BY`s per second per million rows, or maintain rollups.
- **CPU is the constraint, not memory** — which is the good problem to have,
  because vCPU is the cheaper axis to buy.
- **Schema design matters more than any knob.** The single largest performance
  change in this entire document was **one composite index** (a 79% latency drop,
  and a 530× drop on the realistic mixed workload). Everything the operator can
  tune — Postgres sizing, keep-alives, logging — was worth between 0% and 38%.

Compared to the numbers already published for the bare engine (2,000 req/s at
p50 1.58 ms over plain HTTP, `context-docs/BENCHMARK_PUBLIC.md`), the production
stack is **the same engine plus ~1 ms**, measured against a million rows instead
of a small table. Nothing in the earlier claim is contradicted; this document
adds the part that was missing — what happens once TLS, a proxy, systemd, real
data and deliberate failure are in the picture.

---

## 9. Reproduce it

```bash
# on a machine that is NOT your server
bash scripts/verify-production/run-all.sh \
  --target=https://api.example.com \
  --origin-ip=YOUR.SERVER.IP \        # if a CDN fronts your domain
  --server-ssh=root@YOUR.SERVER.IP \
  --seed=1000000                      # optional: fill the tenant first
```

Add `--with-chaos` to include the destructive resilience cases (it kills services
and reboots the box — only where you can afford that). Full documentation of every
script, and of the methodology, is in
[`scripts/verify-production/README.md`](../scripts/verify-production/README.md).
