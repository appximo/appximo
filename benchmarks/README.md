# Reproducing the Appitools benchmarks

These are the instructions to reproduce the numbers in the root
[README](../README.md#benchmarks). The k6 scripts live in
[`../benchmark-lab/`](../benchmark-lab/).

> **Note on the two benchmark documents.** The root README table is from a **2 vCPU /
> 4 GB** droplet at 500–3000 RPS. [`../benchmark-lab/BENCHMARK_REPORT.md`] is a
> **separate, earlier 1 vCPU baseline** (≈50–240 RPS, different methodology) — it is
> *not* the raw output for the 2 vCPU numbers and the two should not be conflated.
> Re-run the steps below on your own 2 vCPU host to reproduce the headline table.

[`../benchmark-lab/BENCHMARK_REPORT.md`]: ../benchmark-lab/BENCHMARK_REPORT.md

## Hardware

- **Server:** DigitalOcean droplet, **2 vCPU / 4 GB RAM / $16-mo class**, Ubuntu.
- **Database:** PostgreSQL 16 in Docker on the same droplet.
- **Data:** ~10,000 pre-loaded `guides` rows in the test tenant.
- **Load generator:** k6 (`constant-arrival-rate`, open model).

> **Run k6 from a separate machine.** On a 2-vCPU box the load generator competes
> with the server for cores and *understates* performance (a 1-vCPU generator was a
> measured artifact in earlier runs). For an apples-to-apples result, run k6 off-box,
> or — for a quick local sanity check only — over loopback on a machine with spare cores.

## What is active on the server during the test

Unlike most "look how fast my framework is" benchmarks, every production feature is
**on** while these numbers are measured:

- JWT HS256 authentication (validated per request)
- RBAC policy evaluation (role → resource → row condition → field allowlist)
- Multi-tenant isolation (`SET LOCAL search_path` per request, schema-per-tenant)
- Per-tenant rate limiting (token bucket) and the circuit breaker
- 5 s query timeout, body-size limits, the RBAC-gated response cache

## Running the stress test

The script is [`../benchmark-lab/k6-stress.js`](../benchmark-lab/k6-stress.js). It hits
`GET /api/guides?filter[status]=...&page=1&per_page=20` with a Bearer token and the
tenant `Host` header.

```bash
# 1. Mint a token on the server (never print secrets into shared logs):
TOKEN=$(ssh root@SERVER 'source /root/.appitools-secrets; \
  /root/appitools/appitools token --secret "$JWT_SECRET" --tenant 10 --role super_admin')

# 2. Drive load from your machine (sweep the rate):
for RATE in 500 1000 2000 3000; do
  RATE=$RATE \
  TARGET_URL=http://SERVER:8080 \
  TENANT_ID=10 \
  BENCH_TOKEN="$TOKEN" \
  k6 run ../benchmark-lab/k6-stress.js
done
```

There is also [`../benchmark-lab/k6-load.js`](../benchmark-lab/k6-load.js) for a
ramping-load profile.

## Interpreting the output

`handleSummary` prints a compact JSON object per run:

```json
{ "rate": 2000, "rps_actual": 1997, "p50": 0, "p95": 11, "p99": 33, "errors_pct": "0.00%", "dropped": 0 }
```

- `rate` is the **requested** arrival rate; `rps_actual` is what was actually achieved.
  If `rps_actual` lags `rate`, the system (or the generator) could not keep up.
- `p50/p95/p99` are request durations in **milliseconds** (rounded).
- `errors_pct` is the HTTP failure rate. At 3000 RPS a small non-zero value is
  **expected and by design** — the per-tenant rate limiter sheds load (HTTP 429),
  which k6 counts as a failure. `dropped` counts iterations k6 itself skipped.
- Sub-millisecond p50s round to `0` in the integer summary; use
  `summaryTrendStats` (already enabled) or `--summary-export` for sub-ms detail.

## The NestJS comparison — read this

The README contrasts Appitools with a NestJS baseline. **Be clear about what that
baseline was**, because the honesty is what makes the comparison credible:

- The NestJS service ran with **no authentication, no RBAC, and no multi-tenancy**,
  against a **single shared table** via Prisma.
- Appitools ran with **all** of those features active.

So the comparison is **deliberately unfavorable to Appitools** (we carry security and
isolation work that NestJS skipped entirely) and it *still* wins decisively: NestJS
saturated and collapsed at ~**1092 RPS** real throughput, while Appitools served the
full requested rate to 2000 RPS with single-digit-ms p95 and 0 errors. We do **not**
claim NestJS is "10× slower at the same workload" — it was doing *less* work and still
fell over first. Reproduce the NestJS side from
[`../benchmark-lab/nestjs-baseline/`](../benchmark-lab/nestjs-baseline/).
