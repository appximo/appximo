# Load Testing Your Appitools API

This guide shows you how to benchmark your generated API with [k6](https://k6.io) and interpret the results honestly.

---

## Install k6

### macOS

```bash
brew install k6
k6 version  # k6 v0.50.0
```

### Windows

```powershell
choco install k6
# or: winget install k6 --source winget
```

### Linux (Debian/Ubuntu)

```bash
sudo gpg -k
sudo gpg --no-default-keyring \
  --keyring /usr/share/keyrings/k6-archive-keyring.gpg \
  --keyserver hkp://keyserver.ubuntu.com:80 \
  --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69

echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] \
  https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list

sudo apt-get update && sudo apt-get install k6
```

### Any platform (binary)

Download from [k6.io/docs/get-started/installation](https://k6.io/docs/get-started/installation/).

---

## Set up the full stack

You need Postgres running and the tenant schema created before benchmarking.

```bash
# 1. Start Postgres
docker run -d \
  --name appitools-pg \
  -e POSTGRES_DB=mydb \
  -e POSTGRES_USER=appuser \
  -e POSTGRES_PASSWORD=secret \
  -p 5432:5432 \
  postgres:16-alpine

# 2. Create tenant schema and pre-load 1000 rows
export DATABASE_URL="postgres://appuser:secret@localhost:5432/mydb?sslmode=disable"

psql "$DATABASE_URL" <<'SQL'
CREATE SCHEMA IF NOT EXISTS tenant_acme;
CREATE TABLE IF NOT EXISTS tenant_acme.guides (
    id          UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    code        TEXT UNIQUE NOT NULL,
    status      TEXT DEFAULT 'pending',
    origin      TEXT,
    destination TEXT,
    created_at  TIMESTAMPTZ DEFAULT now()
);
INSERT INTO tenant_acme.guides (code, status, origin, destination)
SELECT 'GU-' || i, 'pending', 'CityA', 'CityB'
FROM generate_series(1, 1000) AS i;
SQL

# 3. Start the API server
export JWT_SECRET="dev-secret"
export ADMIN_KEY="dev-admin"
appitools serve --schema schema.json --port 8080

# 4. Generate a benchmark token
export BENCH_TOKEN=$(appitools token \
  --role super_admin --tenant acme --secret "$JWT_SECRET")
```

---

## k6 script (already in the repo)

The benchmark script lives at `benchmark/k6_script.js`. It runs three sequential scenarios:

```javascript
/**
 * Appitools benchmark — 3 escenarios secuenciales
 *
 * Uso:
 *   k6 run benchmark/k6_script.js
 *
 * Variables de entorno:
 *   BASE_URL    (default: http://localhost:8080)
 *   TENANT_HOST (default: acme.localhost)
 *   BENCH_TOKEN (required — generate with: appitools token ...)
 */
import http from 'k6/http';
import { check } from 'k6';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

const BASE = __ENV.BASE_URL    || 'http://localhost:8080';
const HOST = __ENV.TENANT_HOST || 'acme.localhost';
const BENCH_TOKEN = __ENV.BENCH_TOKEN || '';

export const options = {
  scenarios: {
    // Scenario A: maximum throughput — 20 VUs, no sleep
    max_throughput: {
      executor: 'constant-vus',
      vus: 20,
      duration: '30s',
      startTime: '0s',
      exec: 'listGuides',
    },
    // Scenario B: sustained realistic load — ramp 0→50→0
    sustained_load: {
      executor: 'ramping-vus',
      stages: [
        { duration: '15s', target: 50 },
        { duration: '45s', target: 50 },
        { duration: '10s', target: 0 },
      ],
      startTime: '35s',
      exec: 'listGuides',
    },
    // Scenario C: 80% read / 20% write with active JS hook
    read_write_mix: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      startTime: '110s',
      exec: 'readWriteMix',
    },
  },
  thresholds: {
    'http_req_duration{scenario:max_throughput}': ['p(99)<500'],
    'http_req_duration{scenario:sustained_load}': ['p(99)<300'],
    'http_req_duration{scenario:read_write_mix}': ['p(99)<500'],
    http_req_failed: ['rate<0.01'],
  },
};

const AUTH = BENCH_TOKEN ? { 'Authorization': `Bearer ${BENCH_TOKEN}` } : {};

const HEADERS = {
  headers: { Host: HOST, ...AUTH },
};
const POST_HEADERS = {
  headers: { Host: HOST, 'Content-Type': 'application/json', ...AUTH },
};

export function listGuides() {
  const res = http.get(`${BASE}/api/guides`, HEADERS);
  check(res, { 'status 200': r => r.status === 200 });
}

let counter = 0;
export function readWriteMix() {
  if (++counter % 5 === 0) {
    const res = http.post(
      `${BASE}/api/guides`,
      JSON.stringify({ code: `LIVE-${__VU}-${__ITER}`, origin: 'Alpha', destination: 'Beta' }),
      POST_HEADERS,
    );
    check(res, { 'create 201': r => r.status === 201 });
  } else {
    const res = http.get(`${BASE}/api/guides`, HEADERS);
    check(res, {
      'list 200': r => r.status === 200,
      'has data array': r => {
        try { return Array.isArray(JSON.parse(r.body).data); }
        catch { return false; }
      },
    });
  }
}

export function handleSummary(data) {
  return {
    'benchmark/results.json': JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}
```

**Run it:**

```bash
k6 run -e BENCH_TOKEN="$BENCH_TOKEN" benchmark/k6_script.js

# Override host/URL for a remote server:
k6 run -e BASE_URL=http://165.22.100.200:8080 \
       -e TENANT_HOST=acme.yourdomain.com \
       -e BENCH_TOKEN="$BENCH_TOKEN" \
       benchmark/k6_script.js
```

Results are written to `benchmark/results.json` and printed to stdout.

---

## Interpreting the results

After the run, k6 prints a summary like this:

```
     ✓ status 200
     ✓ list 200
     ✓ create 201

     checks.........................: 100.00% ✓ 55649   ✗ 0
     data_received..................: 71 MB   427 kB/s
     data_sent......................: 12 MB   71 kB/s

     ✓ { scenario:max_throughput }...: avg=44ms  min=1ms  med=27ms  max=427ms  p(90)=115ms  p(95)=144ms
     ✗ { scenario:sustained_load }...: avg=103ms min=1ms  med=55ms  max=2.51s  p(90)=216ms  p(95)=442ms
     ✓ { scenario:read_write_mix }...: avg=21ms  min=1ms  med=2ms   max=251ms  p(90)=78ms   p(95)=91ms

     http_req_failed................: 0.00%   ✓ 0       ✗ 55649
     http_reqs......................: 55649   ~397/s
```

### What each metric means

| Metric | What it tells you | Good value |
|---|---|---|
| `http_req_duration p(50)` | Half your requests finish in this time | < 5ms on local |
| `http_req_duration p(90)` | 90% of requests are faster than this | < 150ms on $6 Droplet |
| `http_req_duration p(95)` | 95% of requests — your "typical slow" | < 250ms on $6 Droplet |
| `http_req_duration p(99)` | The slowest 1% — your worst case | < 500ms on $6 Droplet |
| `http_reqs / s` | Requests per second (RPS) | > 400 on $6 Droplet |
| `http_req_failed rate` | Percentage of non-2xx responses | < 1% |
| `vus` | Active virtual users at any point | Matches your stage config |
| `checks` | Your `check()` assertions passed/failed | 100% |

**p99 is the most important threshold** — it represents the experience of your slowest users. SaaS customers notice p99, not p50.

---

## Reference: what to expect by Droplet size

Measured with Appitools + PostgreSQL 16 co-located on the same machine, `GET /api/guides`, 1 000 rows pre-loaded, simple list query. The bottleneck is Postgres sharing vCPU with the API process.

| Droplet | Monthly | RAM | vCPU | RPS (20 VUs) | P90 latency |
|---|---|---|---|---|---|
| Basic | $6/mo | 1 GB | 1 | ~400–600 | ~115ms |
| Basic | $16/mo | 4 GB | 2 | ~900–1 400 | ~50ms |
| Basic | $32/mo | 8 GB | 4 | ~2 000–3 500 | ~25ms |
| CPU-Optimized | $42/mo | 8 GB | 4 | ~2 500–4 000 | ~20ms |

> **Numbers are with Postgres in Docker sharing the same vCPU** — on dedicated hardware (Postgres on a separate machine via unix socket) expect 2–3× more RPS.

> **The bottleneck is always PostgreSQL, not the HTTP layer.** Go + chi handles 300k+ req/s in pure CPU benchmarks. In practice, you're limited by `max_connections` and query complexity.

**Rule of thumb:** Appitools uses `MaxConns=6` in its default pool. At low VU counts, 6 connections saturate the pool before the vCPU. Increase `MaxConns` in `pkg/db/pool.go` if you need more throughput and have spare CPU for Postgres.

---

## Compare Appitools vs NestJS on the same server

Run the same k6 script against both servers and compare. Here's how to set up NestJS for a fair comparison:

```bash
# Clone a standard NestJS + Prisma CRUD example
git clone https://github.com/prisma/prisma-examples
cd prisma-examples/typescript/rest-nestjs
npm install
DATABASE_URL="$DATABASE_URL" npx prisma migrate dev
npm run start:prod &

# Run the benchmark against NestJS (port 3000)
k6 run -e BASE_URL=http://localhost:3000 \
       -e TENANT_HOST=localhost \
       -e BENCH_TOKEN="" \
       benchmark/k6_script.js > benchmark/nestjs-results.txt

# Run against Appitools (port 8080)
k6 run -e BASE_URL=http://localhost:8080 \
       -e BENCH_TOKEN="$BENCH_TOKEN" \
       benchmark/k6_script.js > benchmark/appitools-results.txt

# Compare
diff benchmark/appitools-results.txt benchmark/nestjs-results.txt
```

Expect Appitools to be **2–3× faster in RPS** and **10–15× lower in RAM idle**.  
The gap widens as concurrency increases because Go's goroutine scheduler outperforms Node.js's event loop under connection saturation.

---

## Troubleshooting

### p99 > 500ms

**Likely cause:** Missing index on a filtered column.

```sql
-- Check slow queries
SELECT query, calls, mean_exec_time
FROM pg_stat_statements
ORDER BY mean_exec_time DESC LIMIT 10;

-- Add an index on your most-queried column
CREATE INDEX CONCURRENTLY ON tenant_acme.guides (status);
```

### Error rate > 1%

**Likely cause 1:** Missing or invalid `BENCH_TOKEN`. Check that the token was generated with the same `JWT_SECRET` the server uses and with the correct `--tenant` flag.

**Likely cause 2:** Postgres connection pool exhausted.

```bash
# Check current connections
psql "$DATABASE_URL" -c "SELECT count(*) FROM pg_stat_activity;"
```

Appitools uses `MaxConns=6` by default. At high VU counts you may need to raise it in `pkg/db/pool.go`.

### RPS much lower than expected on local machine

**It's your laptop, not the framework.** macOS and Windows have higher TCP stack overhead than Linux. Local benchmarks on a MacBook Pro M3 run at 20–30% of what you'd see on a Linux VPS with the same core count.

Always benchmark on the target hardware (a DigitalOcean Droplet or similar Linux server).

### `ERRO[0030] some thresholds have failed`

Your p99 or error rate exceeded the configured threshold. This is not a test failure — it means your server doesn't meet the SLA defined in `options.thresholds`. Investigate with the tips above.
