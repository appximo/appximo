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

Real numbers from k6 v0.55.0 on a **$6/mo DigitalOcean Droplet (1 vCPU / 2 GB RAM)**,
Postgres 16 in Docker, 1 004 pre-loaded rows, 0% error rate across 55 649 requests.

| Scenario | VUs | RPS | P90 | P95 | RAM (appitools) |
|---|---|---|---|---|---|
| **GET list — max throughput** | 20 | **~530 req/s** | 115 ms | 144 ms | 19 MB |
| **GET list — sustained 50 VUs** | 50 | **~303 req/s** | 216 ms | 442 ms | 27 MB |
| **80% read / 20% write + JS hook** | 10 | **~618 req/s** | 78 ms | 91 ms | 27 MB |

Bottleneck is Postgres in Docker sharing 1 vCPU with other processes — not the HTTP layer.
On a dedicated $16/mo Droplet (2 vCPU, unix socket Postgres) expect **2–3× higher RPS**.

> [Full methodology, raw k6 output, and bottleneck analysis →](docs/benchmarks.md)

---

## Real Performance — Scientific Benchmark

Methodology: k6 constant-arrival-rate (open model),
1 shared vCPU, 1.9 GB RAM, PostgreSQL 16.
Appitools: JWT HS256 + RBAC + multi-tenant schema isolation.
NestJS baseline: header check only, no RBAC, no multi-tenancy.

| Load | Appitools p95 | NestJS p95 |
|------|--------------|------------|
| 50 RPS  | **4ms**   | 242ms |
| 100 RPS | **161ms** | 707ms |
| 150 RPS | **3ms**   | 5ms   |
| 200 RPS | **6ms**   | 7ms   |
| 220 RPS | **7ms**   | 19ms  |
| 240 RPS | **168ms** | 309ms |

**p50 steady state: 2–3ms on both.**
**Error rate: 0% on both up to 240 RPS.**

Go wins 5 of 6 load points — with real JWT validation, RBAC enforcement,
and per-tenant schema isolation that NestJS didn't implement.

Key insight: NestJS pays 242–707ms JIT warmup at low load. Go binary is
AOT-compiled — first request is as fast as the millionth.

RAM idle: ~1 MB (Go) vs ~42 MB (Node).
Expected on 2 vCPU: Go separates further as goroutines use both cores;
Node stays single-threaded.

> [See full methodology →](benchmark-lab/BENCHMARK_REPORT.md)

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
