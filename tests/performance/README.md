# Performance tests (k6)

`sustained_2krps.js` is the SLO gate for Escenario 5 of `context-docs/TESTING_PLAN.md`.
It drives `constant-arrival-rate` traffic through the full data plane (JWT + RBAC +
multi-tenancy) and enforces the project SLOs as k6 thresholds with `abortOnFail`:

| SLO | k6 threshold |
|---|---|
| p95 < 15ms under sustained load | `http_req_duration: p(95)<15` |
| error rate < 1% | `http_req_failed: rate<0.01` |

A breach makes k6 **exit 99**, failing `make test-perf` / CI.

## Run it

The script needs a running server, a registered tenant, and a JWT for that tenant.
Mint the token with the built-in CLI (no secret is hardcoded in the script):

```bash
TOKEN=$(./appitools token --tenant acme --role super_admin --secret "$JWT_SECRET")

BENCH_TOKEN="$TOKEN" TENANT_ID=acme TARGET_URL=http://localhost:8080 \
  k6 run tests/performance/sustained_2krps.js
```

Or via the Makefile (passes through the same env): `make test-perf`.

## Tunables (env)

| Var | Default | Notes |
|---|---|---|
| `TARGET_URL` | `http://localhost:8080` | Data-plane base URL |
| `TENANT_ID` | `acme` | Sent as `Host: <id>.localhost` |
| `BENCH_TOKEN` | — (**required**) | HS256 JWT whose `tenant_id` matches `TENANT_ID` |
| `RATE` | `2000` | Requests/sec |
| `DURATION` | `60s` | Hold time |
| `ENDPOINT` | `/api/guides?filter[status][eq]=pending&per_page=20` | Path + query |

## Hardware note (important)

The `2000 RPS / p95<15ms` target is calibrated to **prod hardware** (DigitalOcean
2 vCPU; measured p95 ≈ 10.6ms at 2000 RPS — see PRIMER). A shared GitHub-hosted
runner with Postgres co-located is *not* prod hardware, so CI drives a lower,
runner-safe `RATE` while still enforcing the same thresholds (the gate proves the
pipeline and the SLO logic; the full-rate validation is run against prod or a
dedicated box). Don't raise CI `RATE` to 2000 expecting the prod p95 — that would
flake. Run the full 2000 RPS validation with `make test-perf` against prod-class
hardware.

`last-run.json` is written next to this README on each run (git-ignored).
