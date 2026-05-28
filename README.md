# Appitools

[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://golang.org/dl/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Build Status](https://github.com/miguelangel/appitools/workflows/CI/badge.svg)](https://github.com/miguelangel/appitools/actions)

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

| Stack | Req/s (c=200) | P99 latency | RAM idle |
|---|---|---|---|
| **Appitools** (Go + chi + pgx) | **~65k** | **~12 ms** | **~35 MB** |
| NestJS + Prisma + PM2 (4 workers) | ~25k | ~38 ms | ~480 MB |
| API Platform (PHP 8.3 + FPM) | ~14k | ~65 ms | ~180 MB |

> Droplet $16 (2 vCPU / 4 GB RAM), c=200, PostgreSQL 16 local, simple CRUD endpoint with 1 000 rows pre-loaded.  
> Bottleneck is always Postgres, not the HTTP layer. Numbers vary by query complexity.  
> [See full methodology →](docs/benchmarks.md)

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
