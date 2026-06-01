# Getting Started with Appitools

This guide takes you from zero to a running multi-tenant REST API in under 10 minutes.  
No prior Go experience is required to run the server. You do need Go to generate handlers.

---

## Prerequisites

| Tool | Version | Why |
|---|---|---|
| Go | 1.22+ | Build the CLI and generated handlers |
| Docker | any recent | Run PostgreSQL without installing it |
| curl | any | Test your endpoints |

Check your versions:
```bash
go version   # go version go1.22.0 linux/amd64
docker --version  # Docker version 24.0.0
```

---

## Install the CLI

### Option A: `go install` (recommended)

```bash
go install github.com/miguelangel/appitools/cmd/appitools@latest
appitools version   # Appitools v0.1.0
```

Make sure `$(go env GOPATH)/bin` is in your `PATH`. Add this to your `~/.bashrc` or `~/.zshrc`:
```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Option B: Build from source

```bash
git clone https://github.com/miguelangel/appitools
cd appitools
go build -o appitools ./cmd/appitools
./appitools version
```

---

## Create your first project

```bash
appitools init my-api
# Proyecto "my-api" inicializado.
#   → Edita schema.json y corre:
#   → appitools validate schema.json
#   → appitools generate schema.json
cd my-api
ls
# go.mod  schema.json
```

`appitools init` creates a directory with a starter `schema.json` and a minimal `go.mod`. Open `schema.json` — you'll see a simple example with one resource. We'll expand it below.

---

## Your schema.json — field by field

A schema has four top-level sections: `$schema`, `version`, `name`, `resources`, and optionally `rbac`.

```json
{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "my-api",
  "resources": { ... },
  "rbac": { ... }
}
```

### Resource names

Resources map to database tables. Names must be **lowercase**, start with a letter, and use only letters, numbers, and hyphens (`-`).

```json
"resources": {
  "guides":     { ... },
  "clients":    { ... },
  "line-items": { ... }
}
```

Each resource becomes four endpoints: `GET /api/guides`, `POST /api/guides`, `GET /api/guides/{id}`, `DELETE /api/guides/{id}`.

---

### Field types

Every field needs a `type`. Appitools supports nine types:

| Type | Go type | PostgreSQL | Use for |
|---|---|---|---|
| `string` | `string` | `TEXT` | Short text: names, codes, emails |
| `text` | `string` | `TEXT` | Long text: descriptions, notes |
| `int` | `int` | `INTEGER` | Whole numbers: counts, ages |
| `int64` | `int64` | `BIGINT` | Large integers: file sizes, IDs |
| `float64` | `float64` | `FLOAT8` | Decimal numbers: prices, weights |
| `bool` | `bool` | `BOOLEAN` | True/false flags |
| `uuid` | `[16]byte` | `UUID` | Foreign keys, generated IDs |
| `time` | `time.Time` | `TIMESTAMPTZ` | Timestamps with timezone |
| `json` | `map[string]any` | `JSONB` | Flexible nested data |

Examples:

```json
"fields": {
  "name":        { "type": "string" },
  "description": { "type": "text" },
  "quantity":    { "type": "int" },
  "file_size":   { "type": "int64" },
  "price":       { "type": "float64" },
  "active":      { "type": "bool" },
  "client_id":   { "type": "uuid" },
  "created_at":  { "type": "time" },
  "metadata":    { "type": "json" }
}
```

---

### Field options

Add these to any field definition:

#### `required: true`
The field must be present in every `POST` request body. The validator enforces this at schema validation time; the generated handler returns a 500 if the DB constraint fires at runtime.

```json
"code": { "type": "string", "required": true }
```

#### `unique: true`
Tells your migration tool to create a `UNIQUE` constraint on this column.

```json
"email": { "type": "string", "unique": true }
```

#### `auto: true`
Used with `type: "time"` — marks a field as automatically managed (`DEFAULT now()`). Don't send it in POST bodies.

```json
"created_at": { "type": "time", "auto": true },
"updated_at": { "type": "time", "auto": true }
```

#### `enum: [...]`
Restricts the field to a fixed set of values. The generated SQL uses a `CHECK` constraint.

```json
"status": {
  "type": "string",
  "enum": ["pending", "in_transit", "delivered", "returned"]
}
```

#### `relation: "resource-name"`
Creates a foreign key to another resource. The field type must be `uuid`.

```json
"client_id": {
  "type": "uuid",
  "relation": "clients"
}
```

The referenced resource (`clients`) must exist in the same schema. The `validate` command checks this.

#### `default: value`
A default value for the field when not provided.

```json
"role": { "type": "string", "default": "operario", "enum": ["admin", "operario"] }
```

---

### Hooks

Hooks run code at lifecycle events. Two lifecycle points are supported:

- `before_create` — runs before the `INSERT`. Can modify data or abort the request.
- `after_create` — runs after the `INSERT`. Fire-and-forget (does not block the response).

Two hook types are supported:

#### JS hooks (`"type": "js"`)

The `script` field contains JavaScript that runs in a sandboxed Goja VM.
Available variables: `data` (the request body), `user` (caller context), `result` (the outcome object).

JS hooks are supported for `before_create` only. A JS `after_create` hook is accepted by the validator but is currently a no-op.

```json
"hooks": {
  "before_create": {
    "type": "js",
    "script": "if (!data.code) { result.proceed = false; result.error = 'code is required'; }"
  }
}
```

See [docs/hooks-guide.md](hooks-guide.md) for the full list of available functions and examples.

#### Webhook hooks (`"type": "webhook"`)

Sends an HTTP POST to `url` after the record is created. Uses the `hmac_secret_env` env var for request signing.

```json
"hooks": {
  "after_create": {
    "type": "webhook",
    "url": "https://erp.company.com/webhooks/guide-created",
    "hmac_secret_env": "WEBHOOK_SECRET_GUIDES"
  }
}
```

---

### RBAC

The `rbac` section defines who can do what. See [docs/rbac-guide.md](rbac-guide.md) for a full guide. Quick overview:

```json
"rbac": {
  "roles": {
    "admin": {
      "resources": "*",
      "actions": ["*"]
    },
    "viewer": {
      "resources": ["guides", "clients"],
      "actions": ["read"]
    },
    "operator": {
      "resources": ["guides"],
      "actions": ["read", "create", "update"],
      "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
    }
  }
}
```

| Field | Value | Effect |
|---|---|---|
| `resources` | `"*"` or `["guides", "clients"]` | Which tables this role can touch |
| `actions` | `["*"]` or `["read", "create", "update", "delete"]` | Which operations are allowed |
| `conditions` | `{ "field": "...", "op": "eq", "val": "$user_id" }` | Row-level filter injected into context |
| `fields` | `["code", "status"]` | Field allowlist (read-only roles) |

---

## The 5 CLI commands

### `appitools init <name>`

Scaffolds a new project directory with a starter `schema.json` and `go.mod`.

```bash
appitools init logistics-api
# Creates: logistics-api/schema.json  logistics-api/go.mod
```

---

### `appitools validate [file]`

Validates a `schema.json` against all rules (resource names, field types, enum sizes, relation targets, hook configs).

```bash
appitools validate schema.json
# Schema válido ✓

appitools validate schema.json
# resources.guides.fields.status.type: unknown field type "Status"
# resources.guides.fields.client_id.relation: relation "company" references unknown resource
```

Always run `validate` before `generate`. The generator does not re-validate.

---

### `appitools generate [file]`

Reads the schema and writes Go source files to `internal/handlers/`:

```bash
appitools generate schema.json
#   ✓ generated internal/handlers/guides_handler.go
#   ✓ generated internal/handlers/clients_handler.go
#   ✓ generated internal/handlers/router.go
# Generate completo. 2 handlers generados.
```

Generated files are formatted with `gofmt`. **Do not edit them** — re-run `generate` after schema changes.

---

### `appitools serve --schema [file] --port [n]`

Starts the multi-tenant HTTP server with an in-memory router (no generated files required).

**Required environment variables:**

| Variable | Example | Purpose |
|---|---|---|
| `DATABASE_URL` | `postgres://user:pass@localhost:5432/mydb?sslmode=disable` | PostgreSQL connection string |
| `JWT_SECRET` | `change-in-production` | HS256 signing key for JWT tokens |
| `ADMIN_KEY` | `change-in-production` | Bearer key for the control-plane API (port 9090) |

```bash
export DATABASE_URL="postgres://appuser:secret@localhost:5432/mydb?sslmode=disable"
export JWT_SECRET="dev-secret-change-in-prod"
export ADMIN_KEY="dev-admin-key-change-in-prod"
appitools serve --schema schema.json --port 8080
# Appitools serving on :8080 — Ctrl+C to stop
```

**Optional:**

| Variable | Effect |
|---|---|
| `REDIS_URL` | Enables the migration worker (Redis Streams queue) |
| `APPITOOLS_ENV=development` | Enables pprof on `:6060` and GraphiQL at `/graphiql` |

Middleware chain (in order):
1. `SecurityHeaders` — HSTS, X-Content-Type-Options, etc.
2. `Compress` — gzip for `application/json` responses
3. `RealIP` — trust X-Forwarded-For from proxies
4. `RequestID` — add X-Request-ID to every request
5. `TenantMiddleware` — extract tenant from subdomain
6. `ResponseCache` — 5-second in-memory cache, invalidated by Postgres NOTIFY
7. `JWTMiddleware` — validate Bearer token; 401 on missing or invalid token
8. `RBACMiddleware` — enforce role policy; 403 if denied
9. `Logger` — structured request log
10. `Recoverer` — catch panics

---

### `appitools token`

Generates a signed JWT for local testing. Use this token in `Authorization: Bearer` headers.

```bash
export TOKEN=$(appitools token \
  --role super_admin \
  --tenant test \
  --secret "$JWT_SECRET")
```

| Flag | Default | Purpose |
|---|---|---|
| `--role` | `super_admin` | Role embedded in the JWT claims |
| `--tenant` | _(empty)_ | Tenant ID — must match the request subdomain |
| `--secret` | _(required)_ | Must match your server's `JWT_SECRET` |
| `--user-id` | _(empty)_ | Resolves `$user_id` in RBAC conditions |

---

### `appitools version`

```bash
appitools version
# Appitools v0.1.0
```

---

## Start PostgreSQL with Docker

```bash
docker run -d \
  --name appitools-pg \
  -e POSTGRES_DB=mydb \
  -e POSTGRES_USER=appuser \
  -e POSTGRES_PASSWORD=secret \
  -p 5432:5432 \
  postgres:16-alpine

export DATABASE_URL="postgres://appuser:secret@localhost:5432/mydb?sslmode=disable"
```

Wait a few seconds for Postgres to start, then verify:
```bash
docker exec appitools-pg pg_isready -U appuser
# localhost:5432 - accepting connections
```

---

## Create your tenant schema

Appitools uses a schema-per-tenant model. You must create the tenant schema and tables before making API calls. For local testing, create schema `tenant_test` (maps to subdomain `test.localhost`):

```bash
psql "$DATABASE_URL" <<'SQL'
CREATE SCHEMA IF NOT EXISTS tenant_test;

CREATE TABLE tenant_test.guides (
    id          UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    code        TEXT UNIQUE NOT NULL,
    status      TEXT DEFAULT 'pending',
    origin      TEXT NOT NULL,
    destination TEXT NOT NULL,
    created_at  TIMESTAMPTZ DEFAULT now()
);
SQL
```

---

## Test your API with curl

Set up your server and a test token:
```bash
export JWT_SECRET="dev-secret-change-in-prod"
export ADMIN_KEY="dev-admin-key-change-in-prod"
appitools serve --schema schema.json --port 8080 &

export TOKEN=$(appitools token --role super_admin --tenant test --secret "$JWT_SECRET")
```

Every request needs a `Host` header that includes the tenant subdomain and an `Authorization: Bearer` header:
- `Host: test.localhost` → tenant `test` → schema `tenant_test`

### List records

```bash
curl http://localhost:8080/api/guides \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $TOKEN"
# → {"data":[],"links":{"first":"...","last":"...","self":"..."},"meta":{"has_next":false,"has_prev":false,"page":1,"per_page":20,"total":0,"total_pages":1}}
```

### Create a record

```bash
curl -X POST http://localhost:8080/api/guides \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "code": "GU-001",
    "status": "pending",
    "origin": "Bogotá",
    "destination": "Medellín"
  }'
# → 201 Created
# {"id":"550e8400-...","code":"GU-001","status":"pending",...}
```

### Get by ID

```bash
curl http://localhost:8080/api/guides/550e8400-e29b-41d4-a716-446655440000 \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $TOKEN"
# → 200 OK  or  404 {"error":"not found"}
```

### Delete

```bash
curl -X DELETE http://localhost:8080/api/guides/550e8400-e29b-41d4-a716-446655440000 \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $TOKEN"
# → 204 No Content
```

### Health check (no auth required)

```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"0.1.0"}
```

---

## RBAC: controlling access with headers

JWT claims carry the role. The `appitools token` command embeds them:

| Flag | Effect | JWT claim |
|---|---|---|
| `--role operario` | Role evaluated by RBAC | `role` |
| `--user-id <uuid>` | Resolves `$user_id` in conditions | `user_id` |

**No `Authorization` header → 401 `{"error":"missing token"}`** (JWT middleware fires before RBAC).

**Wrong role → 403 `{"error":"forbidden"}`** (RBAC middleware).

```bash
# Generate a token for operario role (cannot DELETE)
OPERARIO_TOKEN=$(appitools token --role operario --tenant test --secret "$JWT_SECRET")

# This will 403 because operario cannot DELETE
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $OPERARIO_TOKEN"
# → 403 {"error":"forbidden"}
```

---

## Writing JS hooks

Hooks let you run lightweight JavaScript logic before a record is created.

### Validate a required field

```json
"before_create": {
  "type": "js",
  "script": "if (!data.code || data.code.trim() === '') { result.proceed = false; result.error = 'code cannot be empty'; }"
}
```

### Calculate IVA automatically

```json
"before_create": {
  "type": "js",
  "script": "data.iva = data.subtotal * 0.19; data.total = data.subtotal + data.iva; result.data = data;"
}
```

Send `{"subtotal": 100}` — get back `{"subtotal": 100, "iva": 19, "total": 119, ...}`.

### Reject an operation with a clear message

```json
"before_create": {
  "type": "js",
  "script": "if (data.weight_kg > 500) { result.proceed = false; result.error = 'maximum weight is 500 kg, got ' + data.weight_kg + ' kg'; }"
}
```

If the hook rejects, the response is:
```
422 Unprocessable Entity
{"error": "maximum weight is 500 kg, got 750 kg"}
```

See [docs/hooks-guide.md](hooks-guide.md) for the complete function reference and more examples.

---

## Next steps

- [Schema Reference →](schema-reference.md) — every field and option documented
- [RBAC Guide →](rbac-guide.md) — roles, conditions, field allowlists
- [Hooks Guide →](hooks-guide.md) — JS sandbox with 5 real examples
- [Load Testing →](load-testing.md) — benchmark your API with k6
