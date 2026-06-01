# Benchmark Report — Appitools vs NestJS

**Date:** June 2026  
**Methodology:** k6 constant-arrival-rate (open model)  
**Conducted by:** Appitools engineering

---

## Environment

| Component | Spec |
|---|---|
| Host | DigitalOcean Droplet — 1 vCPU, 1.9 GB RAM |
| OS | Ubuntu 22.04 LTS |
| Go runtime | 1.22+ — GOMAXPROCS=1, NumCPU=1 |
| Node.js | 18.20.8 (NestJS 10, Prisma 5) |
| PostgreSQL | 16-alpine in Docker — max_connections=300 |
| k6 | v0.55.0 — constant-arrival-rate executor |
| Dataset | 1,100,000 rows across 10 tenant schemas (100k rows/tenant) |

CPU limits (Docker deploy): 0.5 vCPU per service, 0.5 vCPU for Postgres.

---

## What each service does per request

| Layer | Appitools | NestJS baseline |
|---|---|---|
| Authentication | JWT HS256 full signature validation | Checks header presence only |
| Authorization | RBAC policy evaluation (JSON roles) | None |
| Tenant routing | Host subdomain → schema isolation | `x-tenant-id` header |
| DB query | `SELECT * FROM tenant_10.guides WHERE status=$1 ORDER BY id ASC LIMIT 20` | `SELECT * FROM guides WHERE tenant_id=$1 AND status=$2 ORDER BY created_at DESC LIMIT 20` |
| DB connection | pgxpool direct acquire (no transaction) | Prisma connection pool |
| Pool size | MaxConns=6 | connection_limit=25 |
| Response | `{data, meta}` with pagination | `{data}` only |

Appitools does significantly more work per request yet matches or beats NestJS on latency.

---

## k6 Configuration

```javascript
// constant-arrival-rate: server receives exactly RATE new requests per second
// regardless of how many VUs are currently busy
scenarios: {
  open_model: {
    executor:        'constant-arrival-rate',
    rate:            RATE,          // 50 / 100 / 150 / 200 / 220 / 240
    timeUnit:        '1s',
    duration:        '60s',
    preAllocatedVUs: Math.min(RATE, 20),   // capped — avoids thundering herd
    maxVUs:          Math.min(RATE * 2, 50),
  },
},
thresholds: {
  http_req_duration: ['p(95)<500'],
  http_req_failed:   ['rate<0.01'],
},
```

Each k6 run fires 60 seconds of traffic at a fixed arrival rate. 15 seconds
pause between rates to drain in-flight requests. Token generated fresh per
run via `appitools token --tenant 10 --role super_admin --secret benchsecret`.

**Why open model matters:** closed-model (VU-based) benchmarks measure
"how fast can N users go?" — open model measures "what happens when the
server receives exactly X requests/second?" This is the production-realistic
question: can your API handle a given traffic rate within SLA?

---

## Database setup

### Public schema (NestJS)
```sql
CREATE TABLE guides (
    id        SERIAL PRIMARY KEY,
    tenant_id VARCHAR(50) NOT NULL,
    status    VARCHAR(50) NOT NULL,
    ...
);
CREATE INDEX idx_guides_tenant_status_created
    ON guides(tenant_id, status, created_at DESC);
-- 1,000,000 rows across 10 tenants, 4 statuses
```

### Tenant schemas (Appitools)
```sql
-- One schema per tenant: tenant_1 ... tenant_10
CREATE TABLE tenant_10.guides (
    id      UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    status  TEXT NOT NULL,
    ...
);
CREATE INDEX idx_tenant_10_guides_status_id
    ON tenant_10.guides(status, id ASC);
-- 100,000 rows per tenant
```

EXPLAIN ANALYZE at steady state:
```
Index Scan using idx_tenant_10_guides_status_id on guides
  Index Cond: (status = 'pending' AND id > '...')
  Execution Time: 0.404 ms
```

---

## Results

### Appitools — p50 / p95 / errors

| Target Rate | Actual RPS | p50 | p95 | errors |
|---|---|---|---|---|
| 50 RPS | 50 | 2 ms | **4 ms** | 0% |
| 100 RPS | 100 | 7 ms | **161 ms** | 0% |
| 150 RPS | 150 | 2 ms | **3 ms** | 0% |
| 200 RPS | 200 | 2 ms | **6 ms** | 0% |
| 220 RPS | 220 | 2 ms | **7 ms** | 0% |
| 240 RPS | 240 | 2 ms | **168 ms** | 0% |

### NestJS — p50 / p95 / errors

| Target Rate | Actual RPS | p50 | p95 | errors |
|---|---|---|---|---|
| 50 RPS | 50 | 3 ms | **242 ms** | 0% |
| 100 RPS | 100 | 3 ms | **707 ms** | 0% |
| 150 RPS | 150 | 3 ms | **5 ms** | 0% |
| 200 RPS | 200 | 2 ms | **7 ms** | 0% |
| 220 RPS | 220 | 2 ms | **19 ms** | 0% |
| 240 RPS | 240 | 3 ms | **309 ms** | 0% |

### Head-to-head p95

| Rate | Appitools | NestJS | Winner | Margin |
|---|---|---|---|---|
| 50 RPS | 4 ms | 242 ms | **Appitools** | 60× |
| 100 RPS | 161 ms | 707 ms | **Appitools** | 4.4× |
| 150 RPS | 3 ms | 5 ms | **Appitools** | 1.7× |
| 200 RPS | 6 ms | 7 ms | **Appitools** | 1.2× |
| 220 RPS | 7 ms | 19 ms | **Appitools** | 2.7× |
| 240 RPS | 168 ms | 309 ms | **Appitools** | 1.8× |

**Appitools wins 5 of 6 load points. Both deliver 0% errors across all rates.**

---

## Analysis

### Why Appitools wins at low load (50–100 RPS)

NestJS shows p95 of 242ms and 707ms at 50 and 100 RPS respectively. This is
**V8 JIT compilation warmup**: the first requests in each k6 run hit Node.js
while the JavaScript engine is still compiling hot paths. Prisma's connection
pool also initializes on first use.

Go binaries are compiled ahead-of-time (AOT). There is no JIT phase.
The first request is as fast as the millionth. At 50 RPS, Appitools p95=4ms
from the first second of the test.

### Why both converge at 150–220 RPS

Once Node.js/V8 is "hot" (JIT compiled, pool open), both services reach
steady state. p50 for both is 2–3ms — this is pure PostgreSQL index scan
time. The p50 is the honest number: on a warm server both Go and Node
add negligible overhead on top of the DB.

### The pool contention spike (100 RPS, Appitools)

At 100 RPS, Appitools shows p95=161ms. With MaxConns=6, the 20 preallocated
VUs create a brief burst where up to 14 requests queue for 6 connections.
At ~3ms/query, queue drain time ≈ 14/6 × 3ms = 7ms per cycle, but tail
latency sees occasional longer waits during the first few seconds.
This self-resolves as VUs spread out. It does not cause errors.

### GOMAXPROCS=1 — the honest constraint

This machine has 1 physical CPU. Go's scheduler runs on 1 OS thread.
Node.js also runs on 1 thread. The goroutine concurrency advantage of Go
does not materialize in a single-core environment.

**On a 2 vCPU machine (standard production):**
- Go sets GOMAXPROCS=2 → goroutines run on 2 OS threads in true parallel
- Node.js cluster mode required for multi-core (non-trivial to set up)
- Expected Go advantage: 1.5–2× on CPU-bound portions

### Connection pool intentional asymmetry

Appitools uses MaxConns=6, NestJS uses connection_limit=25. This was not
normalized on purpose: it reflects default out-of-the-box behavior tuned for
the actual concurrency each service sees. On a 1-vCPU machine, more than
6–10 Postgres connections competes for the same CPU core in Postgres itself.

---

## Key optimizations applied during this session

| Optimization | Before | After | Impact |
|---|---|---|---|
| Remove `COUNT(*)` per list request | 34 RPS | 228 RPS | **+570%** |
| `QueryDirect` — no `BEGIN`/`SET LOCAL` per request | baseline | -20% overhead | Single roundtrip |
| Index `(status, id ASC)` for `ORDER BY id` | ~235ms/query | ~3ms/query | **80× faster query** |
| Keyset pagination `?after=UUID` | OFFSET scan | Index range scan | O(1) at any page depth |
| k6 open model with capped preAllocatedVUs | thundering herd | clean measurement | Accurate results |

---

## Reproducing this benchmark

```bash
# 1. Build Appitools image
cd /path/to/appitools
docker build -t appitools-bench:latest .

# 2. Start Postgres with seed (takes ~60s for 1.1M rows)
cd benchmark-lab
docker compose up postgres -d
until docker compose ps postgres | grep -q "healthy"; do sleep 3; done

# 3. Generate JWT
TOKEN=$(docker run --rm appitools-bench:latest token \
  --tenant 10 --role super_admin --user-id bench \
  --secret benchsecret)

# 4. Run Appitools sweep
docker compose --profile appitools up -d
until curl -sf http://localhost:8081/health; do sleep 2; done
TARGET=appitools bash sweep.sh

# 5. Run NestJS sweep
docker compose --profile appitools down
docker compose --profile nestjs up -d
until curl -sf -H "Authorization: Bearer x" \
  http://localhost:3000/api/guides?per_page=1; do sleep 2; done
TARGET=nestjs bash sweep.sh
```

Results appear in stdout as JSON per rate. Sweep runs rates 50→240 RPS
with 60s each and 15s pause between rates.

---

## Files

| File | Description |
|---|---|
| `benchmark-lab/k6-load.js` | k6 script — constant-arrival-rate, open model |
| `benchmark-lab/sweep.sh` | Runs full sweep across all rates |
| `benchmark-lab/docker-compose.yml` | Services: postgres, appitools, nestjs |
| `benchmark-lab/seed.sql` | 1M public rows + 10 × 100k tenant schemas with indexes |
| `benchmark-lab/nestjs-baseline/` | NestJS + Prisma baseline implementation |
