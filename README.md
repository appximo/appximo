# Appitools

[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://golang.org/dl/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Build Status](https://github.com/miguelangel/appitools/workflows/CI/badge.svg)](https://github.com/miguelangel/appitools/actions)
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen)](docs/testing.md)

**Generate production-ready Go APIs from a JSON schema.  
No boilerplate. No magic. No 400 MB runtime.**

---

## Quick Start

```bash
# Install
go install github.com/miguelangel/appitools/cmd/appitools@latest

# Or build from source
git clone https://github.com/miguelangel/appitools
cd appitools && go build -o appitools ./cmd/appitools

# Bootstrap a new project
appitools init my-api && cd my-api

# Validate your schema
appitools validate schema.json
# → Schema válido ✓

# Generate REST handlers
appitools generate schema.json
# → internal/handlers/router.go
# → internal/handlers/*_handler.go

# Run the multi-tenant server
export DATABASE_URL="postgres://user:pass@localhost:5432/mydb?sslmode=disable"
appitools serve --schema schema.json --port 8080
```

One JSON schema → a fully functional, multi-tenant REST API with RBAC, JS hooks, and PostgreSQL isolation. No code to write.

---

## Benchmark

### With the full production stack on

JWT + RBAC + per-tenant isolation **plus** per-tenant rate limiting, circuit breaker,
5 s query timeout and an RBAC-gated response cache — *all active* — on a single
**DigitalOcean 2 vCPU / $16 mo** droplet:

| Load | p50 | p95 | p99 | Errors | CPU (engine) |
|------|:---:|:---:|:---:|:---:|:---:|
| 500 RPS  | 0.5ms | **0.7ms** | 1.6ms | **0%** | 18% of 1 core |
| 2000 RPS | 0.5ms | **11ms**  | **33ms** | **0%** | **40% of 1 core** |

**2000 req/s at p95 = 11 ms, 0 errors, ~40 % of a single core** — with every hardening
feature switched on, and headroom to spare.

**NestJS on the same hardware** (no auth, no RBAC, no multi-tenancy):
500 RPS → p95 = 21 ms · 1000 RPS → p95 = 114 ms, then saturated and collapsed.

<sub>Measured server-side so the load generator isn't the bottleneck; per-tenant rate
limit set to 3000 RPS. The detailed external (separate-machine) run below shows the
original fair head-to-head vs NestJS.</sub>

---

### Detailed comparison vs NestJS (external load)

Methodology: k6 v0.55.0 `constant-arrival-rate` (open model), **k6 on a separate machine**
(fair benchmark — no shared CPU). Hardware: DigitalOcean 2 vCPU / 2 GB / $16 mo,
Postgres 16 in Docker, 10 000 pre-loaded rows. Both servers on the same Droplet.

**Appitools:** JWT HS256 + RBAC + per-tenant schema isolation (`SET LOCAL search_path`).  
**NestJS baseline:** header-only auth, single shared table, Prisma ORM.

| Load | Appitools p50 | Appitools p95 | Appitools p99 | NestJS p50 | NestJS p95 | NestJS p99 |
|------|:---:|:---:|:---:|:---:|:---:|:---:|
| 500 RPS  | **2ms** | **2ms**  | **4ms**  | 4ms   | 21ms   | 64ms   |
| 1000 RPS | **2ms** | **7ms**  | **69ms** | 29ms  | 114ms  | 159ms  |
| 2000 RPS | **2ms** | **48ms** | **84ms** | 808ms | 1236ms | 1509ms |

**Error rate: 0% on both at all load points.**

At 2 000 RPS, Appitools p50 stays at 2 ms. NestJS p50 hits 808 ms — Node.js event loop
saturates while Go goroutines keep serving. NestJS cannot sustain 2 000 RPS
(achieved only 1 092 actual RPS); Appitools delivers the full 1 997.

| Resource | Appitools @ 2000 RPS | NestJS @ saturation |
|---|---|---|
| RAM | **36 MB** | 49 MB (idle, saturated before 2k) |
| CPU (app process) | **37%** of 1 core | 100%+ |
| Errors | **0%** | 0% |

> [Full methodology and raw k6 output →](benchmark-lab/BENCHMARK_REPORT.md)

---

## Battle tested

The test suite validates the full production stack on every commit:

```
go test ./... -timeout 180s    # 22 packages, all passing
```

| Layer | Tests | Coverage |
|---|---|---|
| Unit (auth, RBAC, schema, hooks, query, cache, codegen, middleware) | 91 tests | JWT, sandbox escapes, policy eval, pagination, keyset cursors, field filtering |
| Integration (Postgres real) | 28 tests | CRUD, tenant isolation, migration worker, control plane, GraphQL |
| **E2E — full lifecycle** | **1 test** | Register → CRUD → RBAC → hooks → schema change → migration → new field |
| Security | 14 tests | SQL injection × 2, cross-tenant, JWT tamper, Goja escape, concurrent DDL, large body |
| Benchmark | 3 scenarios | req/s, P50/P95/P99, heap |

Real throughput on CI (2 vCPU, Docker networking):

| Scenario | req/s | p99 | Heap |
|---|---|---|---|
| GET list, 50 goroutines, 1000 rows | **529** | 205 ms | **9 MB** |
| POST with JS hook, 20 goroutines | **794** | 56 ms | — |
| 10 tenants concurrent | **516** | 47 ms | — |

On bare metal with local Postgres: **5–15× higher throughput**.

> [Performance methodology and comparison vs NestJS →](docs/performance.md)  
> [How to run and add tests →](docs/testing.md)

---

## What Appitools generates

Given a `schema.json` with your resources and roles, the engine produces:

- ✅ REST endpoints — `List`, `Create`, `GetByID`, `Delete` per resource
- ✅ Chi router with a production middleware chain
- ✅ Multi-tenant isolation via `SET LOCAL search_path` (never RLS, never a DB per tenant)
- ✅ RBAC with JSON policies — roles, resource lists, row-level conditions, field allowlists
- ✅ JS hooks — `before_create` for validation/transformation, `after_create` for side effects
- ✅ Schema-per-tenant PostgreSQL layout, Atlas-ready for zero-downtime migrations
- 🔜 OpenAPI 3.0 spec generation
- 🔜 GraphQL (gqlgen-based)

---

## What it does NOT do

- ❌ Complex business logic — you write that, on top of the generated code
- ❌ Frontend — use whatever you want
- ❌ Replace an ERP, a payment processor, or a workflow engine
- ❌ Manage your infrastructure — it generates the API layer, you own the rest

---

## Schema example

```json
{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "logistics-api",
  "resources": {
    "guides": {
      "fields": {
        "code":        { "type": "string", "unique": true, "required": true },
        "status":      { "type": "string", "enum": ["pending", "in_transit", "delivered"] },
        "origin":      { "type": "string", "required": true },
        "destination": { "type": "string", "required": true },
        "client_id":   { "type": "uuid",   "relation": "clients" },
        "operator_id": { "type": "uuid",   "relation": "users" },
        "created_at":  { "type": "time",   "auto": true }
      },
      "hooks": {
        "before_create": {
          "type": "js",
          "script": "if (!data.code) { result.proceed = false; result.error = 'code required'; }"
        }
      }
    }
  },
  "rbac": {
    "roles": {
      "super_admin": { "resources": "*", "actions": ["*"] },
      "operario": {
        "resources": ["guides"],
        "actions":   ["read", "create", "update"],
        "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
      }
    }
  }
}
```

See the [full logistics example →](examples/logistics-api/)

---

## Architecture

```
HTTP Request
    │
    ▼
Caddy  (wildcard TLS — acme.yourdomain.com)
    │   tenant extracted from subdomain
    ▼
chi Router
    │
    ├── TenantMiddleware ────── sets tenant_id + pg_schema in context
    ├── RBACMiddleware ──────── checks X-User-Role against JSON policy
    │                          injects field allowlist + WHERE condition
    │
    ▼
Generated Handler
    │
    ├── RunBeforeHook ──────── JS sandbox (500 ms timeout, no I/O)
    │
    ▼
pgxpool
    │  BEGIN
    │  SET LOCAL search_path TO "tenant_acme", public
    │  SELECT / INSERT / DELETE  ($1, $2 — never string concat)
    │  COMMIT
    ▼
PostgreSQL 16
    └── tenant_acme.guides
    └── tenant_acme.clients
    └── tenant_acme.users
```

**Security guarantees:**
- Schema names validated with `^[a-z][a-z0-9_]*$` before any DB interaction
- `pgx.Identifier{}.Sanitize()` on every SQL identifier
- `SET LOCAL search_path` inside every transaction — no cross-tenant leakage
- JS sandbox: no `require`, no `fetch`, no filesystem access

---

## Documentation

| Guide | What it covers |
|---|---|
| [Getting Started](docs/getting-started.md) | Install CLI, create first project, all CLI commands, test with curl |
| [Schema Reference](docs/schema-reference.md) | Every field, type, option, and validation rule |
| [RBAC Guide](docs/rbac-guide.md) | Roles, conditions, field allowlists, debugging |
| [Hooks Guide](docs/hooks-guide.md) | JS sandbox, 5 copy-paste examples, timeout, restrictions |
| [Load Testing](docs/load-testing.md) | k6 setup, interpreting results, Droplet reference table |
| [Benchmark Methodology](docs/benchmarks.md) | How the README numbers were produced |

---

## Project status

**Alpha** — core engine works, integration tests pass, used in internal logistics pilots.  
Production use at your own risk. API may change before v1.0.

**Looking for early adopters.** If you're building a SaaS with multi-tenant data isolation needs, [open an issue](https://github.com/miguelangel/appitools/issues) — let's talk.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
