# Appitools — production verification report

| | |
|---|---|
| started utc | `2026-07-28T15:14:23Z` |
| target | `https://api.appitools.com` |
| origin ip | `PROD-VPS` |
| cdn detected | `cloudflare` |
| tenant | `api` |
| server | `2 vCPU, 1964 MiB, Ubuntu 22.04.5 LTS` |
| loader | `1 vCPU loader` |
| dataset | `orders=1000000` |

## 1. Footprint — what the stack costs at rest and under load

PSS (proportional set size) is used, not RSS: RSS double-counts the shared
pages every PostgreSQL backend maps, which is how PostgreSQL gets reported
as several times larger than it is. `anon` is memory the process really
owns; the remainder is mapped binary/library page cache the kernel reclaims
under pressure — the number that matters for "does this fit in 1 GB".

| state | service | PSS MiB | anon MiB | procs | fds | CPU %core |
|---|---|---:|---:|---:|---:|---:|
| idle | postgresql | 175.1 | 19.4 | 16 | 452 | 0.1 |
| idle | appitools | 85.3 | 67.6 | 1 | 50 | 0.0 |
| idle | caddy | 47.1 | 22.1 | 1 | 46 | 0.0 |
| idle | **TOTAL STACK** | **307.5** | **109.0** | | | |
| idle | _system_ | total 1964 | available 1263 | | | used 35.7% |
| under-load | postgresql | 211.0 | 24.0 | 19 | 331 | 11.6 |
| under-load | appitools | 138.3 | 107.0 | 1 | 55 | 27.2 |
| under-load | caddy | 58.7 | 22.5 | 1 | 102 | 23.0 |
| under-load | **TOTAL STACK** | **408.1** | **153.5** | | | |
| under-load | _system_ | total 1964 | available 1290 | | | used 34.3% |

## 2. Load — what the production surface sustains

Percentiles come from the POOLED raw sample of every measurement run, not
from averaging each run's percentile (the mean of per-run p99s is not the
p99). `cache` says whether the engine's 5 s response cache was allowed to
serve — the bypassed arm is the floor, where every request reaches Postgres.

| scenario | rate | cache | n | p50 ms | p90 | p95 | p99 | max | err | achieved rps | run-to-run CV |
|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| aggregate | 10 | bypassed | 498 | 4459.16 | 5004.96 | 5005.40 | 5008.22 | 5011.24 | 37.58% | 7 | 2.7% |
| graphql_nested | 100 | bypassed | 6000 | 11.01 | 20.22 | 27.04 | 65.68 | 202.23 | 0.00% | 100 | 6.2% |
| mix | 200 | on | 12003 | 3.19 | 5.38 | 6.75 | 51.77 | 178.44 | 0.00% | 200 | 0.9% |
| read | 200 | bypassed | 12001 | 4.05 | 5.46 | 7.77 | 42.59 | 123.05 | 0.00% | 200 | 1.9% |
| read | 200 | on | 11994 | 2.94 | 23.22 | 52.74 | 116.03 | 225.19 | 0.00% | 200 | 44.9% |
| rest_include | 100 | bypassed | 6003 | 6.89 | 8.25 | 9.84 | 19.45 | 122.04 | 0.00% | 100 | 2.5% |
| write | 200 | bypassed | 12003 | 4.89 | 5.97 | 7.47 | 29.00 | 80.68 | 0.00% | 200 | 3.4% |
| write | 200 | on | 11787 | 4.97 | 15.00 | 32.25 | 141.40 | 533.11 | 0.00% | 196 | 27.5% |

> A CDN (`cloudflare`) fronts this domain; the numbers above were measured against the ORIGIN (`PROD-VPS`), i.e. the product itself.

### Capacity — where the knee is

| scenario | levels driven | highest clean | knee |
|---|---|---:|---:|
| read | 100 250 500 750 1000 | 500 | 750 |

Saturation = median p95 > 100 ms, or error rate > 1%, or the offered rate
could not be delivered. "not reached" means the ladder ran out of levels
before the server ran out of capacity.

## 3. What the production layers cost

| path | p50 ms | p95 ms | n |
|---|---:|---:|---:|
| direct-engine-http | 2.50 | 8.36 | 6003 |
| public-https-via-caddy | 3.77 | 26.26 | 6003 |

**Overhead of TLS + the reverse proxy: +1.27 ms p50 (+50.6%)**, Mann-Whitney p=0.0.

## 4. Scale — behaviour as the table grows

| dataset | scenario | rate | p50 ms | p95 | p99 | err |
|---|---|---:|---:|---:|---:|---:|
| 100K orders | aggregate | 100 | 3698.83 | 5008.41 | 5018.63 | 35.42% |
| 100K orders | heavy | 100 | 7.03 | 19.84 | 45.62 | 0.00% |
| 100K orders | rest_include | 100 | 7.81 | 12.72 | 20.11 | 0.00% |
| 1M orders | aggregate | 10 | 4097.33 | 5005.67 | 5007.69 | 22.75% |
| 1M orders | heavy | 100 | 4.44 | 5.55 | 9.55 | 0.00% |
| 1M orders | rest_include | 100 | 7.09 | 13.84 | 28.48 | 0.00% |
| 500K orders | aggregate | 10 | 173.95 | 957.45 | 1784.99 | 0.00% |
| 500K orders | heavy | 100 | 4.54 | 5.94 | 9.06 | 0.00% |
| 500K orders | rest_include | 100 | 7.13 | 9.03 | 13.31 | 0.00% |

## 5. REST vs GraphQL — the same data, both ways

| shape | round trips per view | p50 ms | p95 ms | bytes/request |
|---|---:|---:|---:|---:|
| REST — `?include=customer,items` (1 request) | 1 | 6.89 | 9.84 | 18628 |
| GraphQL — one nested query (1 request) | 1 | 11.01 | 27.04 | 11652 |

**REST is 4.12 ms faster at the median (6.89 vs 11.01 ms); GraphQL sends 37% fewer bytes (11,652 vs 18,628 per request).**

Both fetch the nested data in ONE request and ONE database round trip — `?include=` and the GraphQL resolver share the same `LATERAL` query — so the usual argument for GraphQL, *fewer round trips*, does not apply here: REST already collapses them. What GraphQL genuinely buys is **field selection**: REST's `?include=` returns every column of the parent and its relations, while the GraphQL query names exactly the fields it wants. That is the 37% payload difference, and it is paid for with ~60% more server latency (parse, validate and resolve the document).

**Recommendation for a frontend:** default to REST with `?include=` — it is faster, simpler to cache (it is a GET, so the engine's response cache serves it; GraphQL POSTs are never cached), and easier to debug. Reach for GraphQL when payload size actually matters (mobile, poor networks) or when different views need very different field sets from a wide resource.

## 6. Resilience — what breaks, and what the user loses

Each row: a fault injected while a probe kept requesting at a fixed rate.
"Requests lost" is measured, not inferred — the probe records every outcome,
including connection refused and timeouts.

| fault | requests lost | codes the user saw | time to 1st error | outage | recovered by itself |
|---|---:|---|---:|---:|---|
| engine-kill | 90 / 896 | 502×90 | 0.00s | 4.53s | yes |
| caddy-kill | 6 / 636 | refused×6 | 0.48s | 0.98s | yes |
| postgres-stop | 227 / 1169 | 500×227 | 0.00s | 11.91s | yes |
| pool-exhaust | 0 / 891 | none | — | — | yes |
| memory-pressure | 0 / 729 | none | — | — | yes |
| deploy-update | 7 / 725 | 502×7 | 3.95s | 0.47s | yes |
| reboot | 468 / 2913 | refused×369, timeout×94, 502×5 | 0.06s | 27.80s | yes |

## 7. Tuning — what was changed and whether it actually helped

A change is kept only if it is BOTH statistically significant (Mann-Whitney
p < 0.05) AND practically material (median moved more than max(0.5 ms, 3%)).
"no_change" means the tuning did nothing measurable and was NOT adopted.

| change | before p50 | after p50 | delta | p | verdict |
|---|---:|---:|---:|---:|---|
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| — | — | — | — (—) | — | — |
| composite index (status, created_at) on orders | 21.42 | 4.39 | -17.03 ms (-79.5%) | 0.0 | improvement |
| PostgreSQL sizing for the box — aggregate workload | 369.39 | 229.32 | -140.07 ms (-37.9%) | 0.0 | improvement |
| PostgreSQL sizing for the box — read workload | 4.62 | 4.66 | 0.04 ms (0.9%) | 0.0 | no_change |

---

Generated by `scripts/verify-production/report.py`. Raw JSON for every number
in this report is in the same directory — nothing here is hand-written.
