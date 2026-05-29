# Benchmark Methodology

This document describes the **exact setup** used to produce the numbers in the README.
All numbers here are from a real k6 run — not estimates.

---

## Hardware

- **Platform:** DigitalOcean Droplet — Basic, $6/month
- **CPU:** 1 vCPU (DO-Premium-Intel)
- **RAM:** 1.9 GB total, ~650 MB available during the test
- **OS:** Ubuntu 22.04 LTS
- **PostgreSQL:** 16-alpine in Docker (same machine, loopback TCP — *not* unix socket)
- **Appitools version:** v0.1.0 (commit `4afb37b`)
- **Other processes running:** 3 additional Docker containers (`backend-app`, `backend-db`, `backend-worker`) sharing the same vCPU

> **Honest note:** This is a smaller server than typical production. A $16/mo Droplet (2 vCPU / 4 GB) would produce roughly 2–3× higher RPS. The numbers here are what a developer gets on the cheapest reasonable hardware.

---

## Test conditions

| Parameter | Value |
|---|---|
| Tool | k6 v0.55.0 |
| Endpoints tested | `GET /api/guides` (list), `POST /api/guides` (create with JS hook) |
| Pre-loaded rows | 1 004 rows in `tenant_acme.guides` |
| Auth | `X-User-Role: super_admin` |
| TLS | Disabled (HTTP, loopback) |
| Error rate | **0.00%** (all 55 649 requests succeeded) |

---

## Scenarios

### Scenario A — Max throughput (20 VUs, 30 s, no sleep)

Goal: find the ceiling on this hardware.

| Metric | Value |
|---|---|
| VUs | 20 |
| Duration | 30 s |
| Total requests | ~15 900 |
| **RPS** | **~530 req/s** |
| Avg | 44.7 ms |
| Median | 27.2 ms |
| P90 | 114.9 ms |
| P95 | 144.1 ms |
| Max | 428 ms |

Threshold `p(99) < 500ms`: ✅ **passed**

---

### Scenario B — Sustained load (0 → 50 → 0 VUs, 70 s)

Goal: realistic ramp, hold at 50 VUs for 45 s.

| Metric | Value |
|---|---|
| Peak VUs | 50 |
| Duration | 70 s (15 ramp + 45 hold + 10 down) |
| Total requests | ~21 200 |
| **RPS** | **~303 req/s** |
| Avg | 102.8 ms |
| Median | 55.0 ms |
| P90 | 216.0 ms |
| P95 | 442.0 ms |
| Max | 2 519 ms |

Threshold `p(99) < 300ms`: ❌ **failed** — at 50 VUs, Postgres (Docker, 1 vCPU shared) becomes the bottleneck. The 2.5s spike is a Postgres queue flush at peak.

> **Why it failed:** `pgxpool` is configured with `MaxConns=25`. At 50 concurrent VUs each waiting on a query, requests queue at the pool level. The p99 would improve significantly with a larger connection pool or a dedicated Postgres instance.

---

### Scenario C — Read/write mix with JS hook (10 VUs, 30 s)

80% `GET /api/guides`, 20% `POST /api/guides` (JS `before_create` hook active).

| Metric | Value |
|---|---|
| VUs | 10 |
| Duration | 30 s |
| Total requests | ~18 500 |
| **RPS** | **~618 req/s** |
| Avg | 20.7 ms |
| Median | 2.2 ms |
| P90 | 77.8 ms |
| P95 | 91.4 ms |
| Max | 251 ms |

Threshold `p(99) < 500ms`: ✅ **passed**

The median of 2.2 ms reflects fast GET-by-ID cache hits and the 80/20 mix. JS hook overhead is negligible (< 1 ms in the sandbox for a simple `if (!data.code)` check).

---

## RAM usage

| Point | RSS |
|---|---|
| Idle (server just started) | **14.5 MB** |
| After warmup (connection pool established) | **19.3 MB** |
| Under full load (50 VUs, scenario B peak) | **26.7 MB** |

Go binary with no JVM warmup, no V8 heap. RAM stays flat under sustained load.

---

## Bottleneck analysis

At 50 VUs on this hardware, the bottleneck is **Postgres in Docker** sharing a single vCPU:

1. `MaxConns=25` in `pgxpool` → queue forms at 26+ concurrent requests
2. Docker TCP loopback adds ~0.5–1 ms vs unix socket
3. `SELECT * FROM guides LIMIT 100` returns ~1 004 rows × 7 columns → large response per request
4. The 1 vCPU is shared with 3 other containers

Expected improvements with dedicated hardware:

| Change | Expected RPS gain |
|---|---|
| Unix socket (no Docker) | +10–20% |
| Raise `MaxConns` to 50 | +30–50% at high VU counts |
| Add index on `guides.created_at` | +5–15% |
| 2 vCPU / $16 Droplet | +80–120% |
| Add `LIMIT` parameter to requests | Biggest single improvement |

---

## k6 script

See [`benchmark/k6_script.js`](../benchmark/k6_script.js).

Run it with:
```bash
# Pre-load 1000 rows first
docker exec -i appitools-pg psql -U appuser -d testdb <<'SQL'
INSERT INTO tenant_acme.guides (code, status, origin, destination)
SELECT 'BENCH-' || i, 'pending', 'CityA', 'CityB'
FROM generate_series(1, 1000) AS i ON CONFLICT DO NOTHING;
SQL

# Run
export DATABASE_URL="postgres://..."
./appitools serve --schema schema.json --port 8080 &
k6 run benchmark/k6_script.js
```

---

## Raw k6 output

```
scenarios: (100.00%) 3 scenarios, 70 max VUs, 2m50s max duration

✓ status 200
✓ list 200
✓ create 201

checks.........................: 100.00% ✓ 55649  ✗ 0
http_req_duration..............: avg=67.71ms  min=1.29ms  med=33.68ms  max=2.51s
  ✓ { scenario:max_throughput }: avg=44.74ms  med=27.16ms  p(90)=114.9ms  p(95)=144.06ms
  ✗ { scenario:sustained_load }: avg=102.8ms  med=54.96ms  p(90)=216.04ms p(95)=442.01ms
  ✓ { scenario:read_write_mix }: avg=20.67ms  med=2.15ms   p(90)=77.79ms  p(95)=91.4ms
http_req_failed................: 0.00%  ✓ 0  ✗ 55649
http_reqs......................: 55649  397.47/s
```
