# RBAC Guide

Role-Based Access Control in Appitools uses JSON policies defined in your `schema.json`.  
Every API request is evaluated against the policy before reaching the handler.

---

## How RBAC works

1. The client sends a JWT in `Authorization: Bearer <token>`.
2. `JWTMiddleware` validates the token and injects `role`, `user_id`, and `tenant_id` claims into the request context.
3. `RBACMiddleware` reads the role from the JWT claims and looks up the matching policy.
4. It checks: does this role have access to this resource (`/api/guides`) with this action (`DELETE`)?
5. If **no** → `403 {"error":"forbidden"}`, the handler is never called.
6. If **yes** → the `EvalResult` (including conditions and field allowlists) is injected into the request context and the handler runs.

> **Note:** The middleware falls back to `X-User-Role` / `X-User-ID` / `X-External-Client-ID` headers if no JWT is present, but `JWTMiddleware` runs first and returns `401` for any `/api/*` request without a valid token. In practice, every API call needs a JWT.

---

## Set up a test token

Generate a token with `appitools token` and store it in `TOKEN`:

```bash
# Use the same JWT_SECRET your server was started with
export TOKEN=$(appitools token \
  --role super_admin \
  --tenant acme \
  --secret "$JWT_SECRET")
```

For roles that use `$user_id` conditions (like `operario`), pass `--user-id`:

```bash
export OPERARIO_TOKEN=$(appitools token \
  --role operario \
  --tenant acme \
  --user-id 550e8400-e29b-41d4-a716-446655440001 \
  --secret "$JWT_SECRET")
```

---

## HTTP headers

| Header | Required | Example | Resolves |
|---|---|---|---|
| `Authorization` | yes | `Bearer eyJ...` | JWT with embedded role, user_id, tenant_id |
| `Host` | yes | `acme.localhost` | Selects the tenant schema |

The `X-User-ID` and `X-External-Client-ID` headers are only needed if you are not using JWTs and have disabled `JWTMiddleware` in a custom server build.

**No `Authorization` header → 401 `{"error":"missing token"}`.** If your schema has no `rbac` section, RBAC is skipped, but the JWT is still required.

---

## The 5 roles — logistics example

Based on `examples/logistics-api/schema.json`:

### `super_admin`

```json
"super_admin": {
  "resources": "*",
  "actions": ["*"]
}
```

Full access to everything. No conditions, no field restrictions.

```bash
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $TOKEN"
# → 204 No Content ✓
```

---

### `gerente`

```json
"gerente": {
  "resources": "*",
  "actions": ["read", "create", "update"]
}
```

Can read and write to any resource, but **cannot delete**.

```bash
# Generate a gerente token
GERENTE_TOKEN=$(appitools token --role gerente --tenant acme --secret "$JWT_SECRET")

# Create → OK
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $GERENTE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-001","origin":"Bogotá","destination":"Medellín"}'
# → 201 ✓

# Delete → 403
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $GERENTE_TOKEN"
# → 403 {"error":"forbidden"} ✓
```

---

### `operario`

```json
"operario": {
  "resources": ["guides", "dispatches", "incidents"],
  "actions": ["read", "create", "update"],
  "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
}
```

- Only access to guides, dispatches, and incidents (not users or clients).
- Can read, create, update — **cannot delete**.
- A row-level condition is injected: `operator_id = <value of JWT user_id claim>`.

```bash
# Generate a token with the operator's user ID
OPERARIO_TOKEN=$(appitools token \
  --role operario \
  --tenant acme \
  --user-id 550e8400-e29b-41d4-a716-446655440001 \
  --secret "$JWT_SECRET")

# Create a guide as operario
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $OPERARIO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-002","origin":"Cali","destination":"Bogotá"}'
# → 201 ✓

# Access clients → 403
curl http://localhost:8080/api/clients \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $OPERARIO_TOKEN"
# → 403 {"error":"forbidden"} ✓

# Delete → 403
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $OPERARIO_TOKEN"
# → 403 {"error":"forbidden"} ✓
```

---

### `tercero`

```json
"tercero": {
  "resources": ["guides"],
  "actions": ["read"],
  "conditions": { "field": "client_id", "op": "eq", "val": "$external_client_id" }
}
```

An external client (customer). Read-only on guides, with a condition that filters to their own records.

```bash
TERCERO_TOKEN=$(appitools token --role tercero --tenant acme --secret "$JWT_SECRET")

curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $TERCERO_TOKEN"
# → 200 (list, filtered to client_id = external_client_id in context)
```

> **Note:** The condition is stored in the request context — full WHERE clause injection into the SQL query is planned for v0.2. Today, the condition is available to handlers via `rbac.EvalResultFromCtx()`.

---

### `public`

```json
"public": {
  "resources": ["guides"],
  "actions": ["read"],
  "fields": ["code", "status", "updated_at"]
}
```

Anonymous access. Read-only, restricted to three fields.

```bash
PUBLIC_TOKEN=$(appitools token --role public --tenant acme --secret "$JWT_SECRET")

curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "Authorization: Bearer $PUBLIC_TOKEN"
# → 200 ✓
# (field filtering applied via AllowedFields in context — full filter in v0.2)
```

---

## Row-level conditions with `$user_id`

The `conditions` field describes a predicate that should narrow the query to records belonging to the current user.

```json
"conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
```

At request time, `$user_id` is replaced by the `user_id` claim in the JWT (set via `--user-id` when generating the token):

```
EvalResult.Condition = &WhereCondition{
    Field: "operator_id",
    Op:    "eq",
    Value: "550e8400-e29b-41d4-a716-446655440001",  // from JWT user_id claim
}
```

Handlers retrieve this from context:
```go
result := rbac.EvalResultFromCtx(r.Context())
if result != nil && result.Condition != nil {
    // append WHERE operator_id = $1 to your query
}
```

Supported dynamic variables:

| Variable | JWT claim / Header | Use case |
|---|---|---|
| `$user_id` | `user_id` claim | Filter to the authenticated user's own records |
| `$external_client_id` | `external_client_id` claim | Filter to a specific external client's records |

Literal values also work:
```json
"conditions": { "field": "status", "op": "eq", "val": "active" }
```

---

## Field allowlists with `fields`

Use `fields` to restrict which columns a role can see. This is useful for public-facing roles that should only see non-sensitive data.

```json
"public": {
  "resources": ["guides"],
  "actions": ["read"],
  "fields": ["code", "status", "updated_at"]
}
```

`EvalResult.AllowedFields` contains `["code", "status", "updated_at"]`.  
Full response filtering (stripping unlisted fields from JSON output) is planned for v0.2.

---

## Common errors and fixes

| Error | Status | Cause | Fix |
|---|---|---|---|
| `{"error":"missing token"}` | 401 | No `Authorization` header | Add `-H "Authorization: Bearer $TOKEN"` |
| `{"error":"invalid token"}` | 401 | Expired, malformed, or wrong-secret token | Re-run `appitools token` with the correct `--secret` |
| `{"error":"token tenant mismatch"}` | 401 | Token `tenant_id` doesn't match Host subdomain | Generate token with `--tenant <subdomain>` matching `Host` header |
| `{"error":"forbidden"}` | 403 | Role not allowed for this resource+action | Check the role's `resources` and `actions` in schema.json |
| `{"error":"forbidden"}` | 403 | Role name typo | Role names are case-sensitive: `"operario"` ≠ `"Operario"` |
| `{"error":"invalid tenant"}` | 400 | Subdomain too short or invalid chars | Use `acme.localhost`, min 2 chars, `[a-z0-9-]` only |

---

## Debugging RBAC

**Quick token check:**
```bash
# Decode the token payload (no verification)
echo "$TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | python3 -m json.tool
```

**Verify the policy reaches the server:**
```bash
curl -v http://localhost:8080/api/guides \
  -H "Host: test.localhost" \
  -H "Authorization: Bearer $TOKEN" 2>&1 | grep "< HTTP"
# < HTTP/1.1 200 OK  ← good
# < HTTP/1.1 403 Forbidden  ← check your schema's rbac section
# < HTTP/1.1 401 Unauthorized  ← token issue, check JWT_SECRET
```

**Check the EvalResult** — in your handler, call `rbac.EvalResultFromCtx(r.Context())` and log the result.
