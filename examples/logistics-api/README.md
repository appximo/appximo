# Logistics API — Appitools Example

A real-world multi-tenant logistics API built with Appitools.  
Models: users, clients, shipment guides, dispatches, incidents.  
RBAC: super_admin / gerente / operario / tercero / public.

---

## Run locally with Docker

```bash
# From the repo root
docker compose -f examples/logistics-api/docker-compose.yml up --build

# API is now available at http://localhost:8080
# Tenant subdomain: use Host header to simulate multi-tenancy (see below)
```

You must create the tenant schema and tables manually before making requests:

```bash
psql "postgres://logistics:logistics@localhost:5432/logistics?sslmode=disable" <<'SQL'
CREATE SCHEMA IF NOT EXISTS tenant_acme;

CREATE TABLE tenant_acme.users (
    id         UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    name       TEXT NOT NULL,
    email      TEXT UNIQUE NOT NULL,
    role       TEXT DEFAULT 'operario',
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE tenant_acme.clients (
    id    UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    name  TEXT NOT NULL,
    nit   TEXT UNIQUE,
    email TEXT,
    type  TEXT
);

CREATE TABLE tenant_acme.guides (
    id             UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    code           TEXT UNIQUE NOT NULL,
    status         TEXT DEFAULT 'pending',
    origin         TEXT NOT NULL,
    destination    TEXT NOT NULL,
    weight_kg      FLOAT,
    declared_value FLOAT,
    client_id      UUID REFERENCES tenant_acme.clients(id),
    operator_id    UUID REFERENCES tenant_acme.users(id),
    created_at     TIMESTAMPTZ DEFAULT now(),
    updated_at     TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE tenant_acme.dispatches (
    id           UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    guide_id     UUID NOT NULL REFERENCES tenant_acme.guides(id),
    operator_id  UUID REFERENCES tenant_acme.users(id),
    dispatched_at TIMESTAMPTZ DEFAULT now(),
    notes        TEXT
);

CREATE TABLE tenant_acme.incidents (
    id          UUID DEFAULT gen_random_uuid() PRIMARY KEY,
    guide_id    UUID NOT NULL REFERENCES tenant_acme.guides(id),
    reported_by UUID REFERENCES tenant_acme.users(id),
    type        TEXT NOT NULL,
    description TEXT,
    created_at  TIMESTAMPTZ DEFAULT now()
);
SQL
```

---

## API endpoints

Every request must include a `Host` header with the tenant subdomain.  
`acme.localhost` → tenant `acme` → schema `tenant_acme`.

### List guides (no auth required by public role)

```bash
curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: public"
```

### Create a guide (super_admin)

```bash
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: super_admin" \
  -H "Content-Type: application/json" \
  -d '{
    "code": "GU-2024-001",
    "status": "pending",
    "origin": "Bogotá",
    "destination": "Medellín",
    "weight_kg": 5.2
  }'
```

Response `201 Created`:
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "code": "GU-2024-001",
  "status": "pending",
  "origin": "Bogotá",
  "destination": "Medellín",
  "weight_kg": 5.2,
  "created_at": "2024-01-15T10:30:00Z"
}
```

### Get guide by ID

```bash
curl http://localhost:8080/api/guides/550e8400-e29b-41d4-a716-446655440000 \
  -H "Host: acme.localhost" \
  -H "X-User-Role: super_admin"
```

### Delete a guide

```bash
curl -X DELETE http://localhost:8080/api/guides/550e8400-e29b-41d4-a716-446655440000 \
  -H "Host: acme.localhost" \
  -H "X-User-Role: super_admin"
# → 204 No Content
```

---

## RBAC in action

### Operario: can create but row-level condition applies

The `operario` role has a condition `operator_id = $user_id`. The current implementation
injects the condition into context — the handler will honor it in a future update.

```bash
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: operario" \
  -H "X-User-ID: 550e8400-e29b-41d4-a716-446655440001" \
  -H "Content-Type: application/json" \
  -d '{ "code": "GU-2024-002", "origin": "Cali", "destination": "Bogotá" }'
```

### Operario: DELETE is forbidden (403)

```bash
curl -X DELETE http://localhost:8080/api/guides/550e8400-e29b-41d4-a716-446655440000 \
  -H "Host: acme.localhost" \
  -H "X-User-Role: operario"
# → 403 {"error":"forbidden"}
```

### Public role: field-restricted reads

The `public` role can only see `code`, `status`, `updated_at` (enforced in context —
full field filtering comes in a future release).

```bash
curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: public"
```

---

## JS hook example

The schema includes a `before_create` hook on guides:

```js
if (!data.code) {
  result.proceed = false;
  result.error = 'code required';
}
```

Try triggering it:

```bash
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: super_admin" \
  -H "Content-Type: application/json" \
  -d '{ "origin": "Cali", "destination": "Bogotá" }'
# → 422 {"error":"code required"}
```

### Custom hook: calculate total weight

You can replace the hook script in `schema.json` with any JS logic:

```json
"before_create": {
  "type": "js",
  "script": "if (data.weight_kg > 500) { result.proceed = false; result.error = 'max weight 500 kg'; } data.declared_value = data.weight_kg * 2.5; result.data = data;"
}
```

Regenerate after schema changes:
```bash
appitools generate schema.json
```

---

## Health check

```bash
curl http://localhost:8080/health
# → {"status":"ok","version":"0.1.0"}
```
