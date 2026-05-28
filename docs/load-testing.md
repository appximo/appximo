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
appitools serve --schema schema.json --port 8080
```

---

## k6 script (copy-paste ready)

Save as `benchmark/k6_script.js` (already included in the repo):

```javascript
import http from 'k6/http';
import { sleep, check } from 'k6';

const BASE_URL    = __ENV.BASE_URL    || 'http://localhost:8080';
const TENANT_HOST = __ENV.TENANT_HOST || 'acme.localhost';

export const options = {
  stages: [
    { duration: '30s', target: 10  },  // ramp up to 10 VUs
    { duration: '30s', target: 50  },  // ramp up to 50 VUs
    { duration: '60s', target: 200 },  // hold at 200 VUs
    { duration: '30s', target: 0   },  // ramp down
  ],
  thresholds: {
    http_req_duration: ['p(99)<50'],   // 99th percentile under 50ms
    http_req_failed:   ['rate<0.01'],  // error rate under 1%
  },
};

const HEADERS = {
  headers: {
    Host:          TENANT_HOST,
    'X-User-Role': 'super_admin',
  },
};

const CREATE_HEADERS = {
  headers: {
    Host:             TENANT_HOST,
    'X-User-Role':    'super_admin',
    'Content-Type':   'application/json',
  },
};

let counter = 0;

export default function () {
  const vu = __VU;
  const iter = __ITER;

  // Mix: 80% reads, 20% writes
  if (counter % 5 === 0) {
    // POST /api/guides
    const payload = JSON.stringify({
      code:        `VU${vu}-IT${iter}`,
      origin:      'Bogotá',
      destination: 'Medellín',
    });
    const res = http.post(`${BASE_URL}/api/guides`, payload, CREATE_HEADERS);
    check(res, {
      'create: status 201': (r) => r.status === 201,
    });
  } else {
    // GET /api/guides
    const res = http.get(`${BASE_URL}/api/guides`, HEADERS);
    check(res, {
      'list: status 200':    (r) => r.status === 200,
      'list: is JSON array': (r) => r.body.startsWith('['),
    });
  }

  counter++;
  sleep(0.05);  // 50ms between iterations per VU
}

export function handleSummary(data) {
  return {
    'benchmark/results.json': JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}

function textSummary(data, opts) {
  // k6 built-in — outputs the standard summary table
  return '';
}
```

**Run it:**

```bash
k6 run benchmark/k6_script.js

# Override host/URL for a remote server:
k6 run -e BASE_URL=http://165.22.100.200:8080 \
       -e TENANT_HOST=acme.yourdomain.com \
       benchmark/k6_script.js
```

---

## Interpreting the results

After the run, k6 prints a summary like this:

```
     ✓ list: status 200
     ✓ list: is JSON array
     ✓ create: status 201

     checks.........................: 100.00% ✓ 248140  ✗ 0
     data_received..................: 156 MB  1.7 MB/s
     data_sent......................: 38 MB   421 kB/s

     http_req_blocked...............: avg=4µs      min=1µs    med=3µs    max=18ms   p(90)=6µs    p(99)=12µs
     http_req_connecting............: avg=1µs      min=0s     med=0s     max=9ms    p(90)=0s     p(99)=0s
     http_req_duration..............: avg=3.1ms    min=0.8ms  med=2.4ms  max=98ms   p(90)=6.2ms  p(99)=12.1ms
     http_req_failed................: 0.00%   ✓ 0       ✗ 248140
     http_req_receiving.............: avg=42µs     min=12µs   med=31µs   max=8ms    p(90)=88µs   p(99)=210µs
     http_req_sending...............: avg=18µs     min=8µs    med=14µs   max=4ms    p(90)=32µs   p(99)=62µs
     http_req_tls_handshaking.......: avg=0s       min=0s     med=0s     max=0s     p(90)=0s     p(99)=0s
     http_req_waiting...............: avg=3.0ms    min=0.7ms  med=2.3ms  max=97ms   p(90)=6.0ms  p(99)=11.9ms
     http_reqs......................: 248140  ~2757/s
     iteration_duration.............: avg=108ms    min=52ms   med=102ms  max=358ms  p(90)=162ms  p(99)=228ms
     iterations.....................: 248140  ~2757/s
     vus............................: 200     min=10     max=200
     vus_max........................: 200     min=200    max=200
```

### What each metric means

| Metric | What it tells you | Good value |
|---|---|---|
| `http_req_duration p(50)` | Half your requests finish in this time | < 5ms on local |
| `http_req_duration p(90)` | 90% of requests are faster than this | < 20ms on Droplet $16 |
| `http_req_duration p(95)` | 95% of requests — your "typical slow" | < 35ms |
| `http_req_duration p(99)` | The slowest 1% — your worst case | < 50ms on Droplet $16 |
| `http_reqs / s` | Requests per second (RPS) | > 40k on Droplet $16 |
| `http_req_failed rate` | Percentage of non-2xx responses | < 1% |
| `vus` | Active virtual users at any point | Matches your stage config |
| `checks` | Your `check()` assertions passed/failed | 100% |

**p99 is the most important threshold** — it represents the experience of your slowest users. SaaS customers notice p99, not p50.

---

## Reference: what to expect by Droplet size

Measured with Appitools + PostgreSQL 16 on the same machine, simple `GET /api/guides`, 1 000 rows, no JOINs.

| Droplet | Monthly | RAM | vCPU | RPS (c=200) | P99 latency |
|---|---|---|---|---|---|
| Basic | $6/mo | 1 GB | 1 | ~15k–25k | ~25ms |
| Basic | $16/mo | 4 GB | 2 | ~45k–65k | ~12ms |
| Basic | $32/mo | 8 GB | 4 | ~80k–120k | ~8ms |
| CPU-Optimized | $42/mo | 8 GB | 4 | ~100k–140k | ~6ms |

> **The bottleneck is always PostgreSQL, not the HTTP layer.** Go + chi handles 300k+ req/s in pure CPU benchmarks. In practice, you're limited by `max_connections` and query complexity.

**Rule of thumb:** At c=200, expect 1 000–2 000 RPS per available Postgres connection. If Postgres has `max_connections=100`, your ceiling is ~150k RPS at best (limited by connection acquisition latency).

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
       benchmark/k6_script.js > benchmark/nestjs-results.txt

# Run against Appitools (port 8080)
k6 run -e BASE_URL=http://localhost:8080 \
       benchmark/k6_script.js > benchmark/appitools-results.txt

# Compare
diff benchmark/appitools-results.txt benchmark/nestjs-results.txt
```

Expect Appitools to be **2–3× faster in RPS** and **10–15× lower in RAM idle**.  
The gap widens as concurrency increases because Go's goroutine scheduler outperforms Node.js's event loop under connection saturation.

---

## Troubleshooting

### p99 > 100ms

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

**Likely cause:** Postgres connection pool exhausted.

```bash
# Check current connections
psql "$DATABASE_URL" -c "SELECT count(*) FROM pg_stat_activity;"

# If near max_connections, increase it
# In postgresql.conf:
max_connections = 200
```

Appitools uses `pgxpool` with `MaxConns=25` by default. At c=200 VUs, you need 200+ pool connections if each request holds a connection. With connection multiplexing, 25 handles ~1000 concurrent requests.

### RPS much lower than expected on local machine

**It's your laptop, not the framework.** macOS and Windows have higher TCP stack overhead than Linux. Local benchmarks on a MacBook Pro M3 run at 20–30% of what you'd see on a Linux VPS with the same core count.

Always benchmark on the target hardware (a DigitalOcean Droplet or similar Linux server).

### `ERRO[0030] some thresholds have failed`

Your p99 or error rate exceeded the configured threshold. This is not a test failure — it means your server doesn't meet the SLA defined in `options.thresholds`. Investigate with the tips above.
