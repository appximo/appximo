# Benchmark Methodology

This document describes the exact setup used to produce the numbers in the README.

---

## Hardware

- **Platform:** DigitalOcean Droplet — Basic, $16/month
- **CPU:** 2 vCPU (AMD EPYC 7543)
- **RAM:** 4 GB
- **Disk:** 80 GB NVMe SSD
- **OS:** Ubuntu 22.04 LTS
- **PostgreSQL:** 16 (local, same machine, unix socket)
- **Appitools version:** v0.1.0-alpha

---

## Test conditions

| Parameter | Value |
|---|---|
| Tool | [k6](https://k6.io) v0.50 |
| Endpoint | `GET /api/guides` |
| Concurrency | 200 virtual users |
| Duration | 30 s ramp-up + 60 s sustained |
| Pre-loaded rows | 1 000 rows in `tenant_acme.guides` |
| Response | JSON array, ~50 fields |
| Auth header | `X-User-Role: super_admin` |
| TLS | Disabled (HTTP only, local loopback) |

**Why 200 VUs?** A $16 Droplet runs out of Postgres connections before hitting 200. This is the realistic ceiling for this hardware tier.

---

## PostgreSQL tuning applied

```ini
shared_buffers = 1GB
work_mem = 8MB
max_connections = 100
effective_cache_size = 2GB
random_page_cost = 1.1     # NVMe SSD
```

---

## k6 script

See [`benchmark/k6_script.js`](../benchmark/k6_script.js).

Run it with:
```bash
k6 run benchmark/k6_script.js
```

---

## Results (raw)

### Appitools (Go 1.25 + chi v5 + pgx v5)

```
scenarios: (100.00%) 1 scenario, 200 max VUs
default: 200 looping VUs for 1m30s (gracefulStop: 30s)

✓ status is 200

checks.........................: 100.00%
http_req_duration..............: avg=3.1ms  min=0.8ms  med=2.4ms  max=98ms   p(90)=6.2ms  p(99)=12.1ms
http_reqs......................: 3 901 482  ~65k/s
```

### NestJS + Prisma (Node.js 20, PM2 cluster, 4 workers)

```
http_req_duration..............: avg=7.9ms  min=1.2ms  med=5.8ms  max=480ms  p(90)=18ms   p(99)=38ms
http_reqs......................: 1 499 200  ~25k/s
```

### API Platform (PHP 8.3, FPM, 8 workers, Nginx)

```
http_req_duration..............: avg=14ms   min=2.1ms  med=10ms   max=900ms  p(90)=32ms   p(99)=65ms
http_reqs......................: 840 600    ~14k/s
```

---

## Important caveats

1. **The bottleneck is always Postgres**, not the HTTP layer. At c=200 with a local DB, all stacks saturate the same Postgres queue.
2. **Business logic changes everything.** A single complex JOIN or a missing index will flatten all stacks equally.
3. **RAM numbers are idle RSS** — no traffic, just the process alive. Prisma ORM alone adds ~250 MB of Node.js V8 heap.
4. Appitools has an unfair advantage at idle: no JIT warmup, no GC tuning needed.
5. These numbers are **not reproducible without the exact same hardware.** Use them for order-of-magnitude comparison only.

---

## Reproduce it yourself

```bash
# 1. Install k6
# https://k6.io/docs/get-started/installation/

# 2. Start the server (logistics example)
cd examples/logistics-api
docker compose up -d

# 3. Load 1000 rows
psql "$DATABASE_URL" -c "
INSERT INTO tenant_acme.guides (code, status, origin, destination)
SELECT 'GU-' || i, 'pending', 'CityA', 'CityB'
FROM generate_series(1, 1000) AS i;
"

# 4. Run the benchmark
k6 run ../../benchmark/k6_script.js
```
