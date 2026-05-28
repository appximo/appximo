# RBAC Guide

Role-Based Access Control in Appitools uses JSON policies defined in your `schema.json`.  
Every API request is evaluated against the policy before reaching the handler.

---

## How RBAC works

1. The client sends `X-User-Role: operario` in the request header.
2. `RBACMiddleware` looks up the `operario` policy in the schema.
3. It checks: does this role have access to this resource (`/api/guides`) with this action (`DELETE`)?
4. If **no** → `403 {"error":"forbidden"}`, the handler is never called.
5. If **yes** → the `EvalResult` (including conditions and field allowlists) is injected into the request context and the handler runs.

---

## HTTP headers

| Header | Required | Example | Resolves |
|---|---|---|---|
| `X-User-Role` | yes (if RBAC defined) | `operario` | Selects the policy to evaluate |
| `X-User-ID` | when role uses `$user_id` | `550e8400-...` | Replaces `$user_id` in conditions |
| `X-External-Client-ID` | when role uses `$external_client_id` | `cli-acme-001` | Replaces `$external_client_id` in conditions |

**No `X-User-Role` → 403.** If your schema has no `rbac` section, all requests pass through.

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
  -H "X-User-Role: super_admin"
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
# Create → OK
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: gerente" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-001","origin":"Bogotá","destination":"Medellín"}'
# → 201 ✓

# Delete → 403
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: acme.localhost" \
  -H "X-User-Role: gerente"
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
- A row-level condition is injected: `operator_id = <value of X-User-ID>`.

```bash
# Create a guide as operario
curl -X POST http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: operario" \
  -H "X-User-ID: 550e8400-e29b-41d4-a716-446655440001" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-002","origin":"Cali","destination":"Bogotá"}'
# → 201 ✓

# Access clients → 403
curl http://localhost:8080/api/clients \
  -H "Host: acme.localhost" \
  -H "X-User-Role: operario"
# → 403 {"error":"forbidden"} ✓

# Delete → 403
curl -X DELETE http://localhost:8080/api/guides/some-id \
  -H "Host: acme.localhost" \
  -H "X-User-Role: operario"
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
curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: tercero" \
  -H "X-External-Client-ID: client-abc-123"
# → 200 (list, filtered to client_id = "client-abc-123" in context)
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
curl http://localhost:8080/api/guides \
  -H "Host: acme.localhost" \
  -H "X-User-Role: public"
# → 200 ✓
# (field filtering applied via AllowedFields in context — full filter in v0.2)
```

---

## Row-level conditions with `$user_id`

The `conditions` field describes a predicate that should narrow the query to records belonging to the current user.

```json
"conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
```

At request time, `$user_id` is replaced by the value of the `X-User-ID` header:

```
EvalResult.Condition = &WhereCondition{
    Field: "operator_id",
    Op:    "eq",
    Value: "550e8400-e29b-41d4-a716-446655440001",  // from X-User-ID header
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

| Variable | Header | Use case |
|---|---|---|
| `$user_id` | `X-User-ID` | Filter to the authenticated user's own records |
| `$external_client_id` | `X-External-Client-ID` | Filter to a specific external client's records |

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
| `{"error":"forbidden"}` | 403 | Role not allowed for this resource+action | Check the role's `resources` and `actions` in schema.json |
| `{"error":"forbidden"}` | 403 | Missing `X-User-Role` header | Add `-H "X-User-Role: your_role"` to your request |
| `{"error":"forbidden"}` | 403 | Role name typo | Role names are case-sensitive: `"operario"` ≠ `"Operario"` |
| `{"error":"invalid tenant"}` | 400 | Subdomain too short or invalid chars | Use `acme.localhost`, min 2 chars, `[a-z0-9-]` only |
| Condition not applied | — | `X-User-ID` header missing | Add `-H "X-User-ID: your-user-uuid"` |

---

## Debugging RBAC

**Print the parsed policy** — add a temporary log to `pkg/rbac/middleware.go` while debugging.

**Check the EvalResult** — in your handler, call `rbac.EvalResultFromCtx(r.Context())` and log the result.

**Verify the policy JSON** — after `json.Marshal(s.RBAC)`, print the bytes to confirm the policy reached the middleware correctly:

```bash
# Quick test: does super_admin pass on GET /api/guides?
curl -v http://localhost:8080/api/guides \
  -H "Host: test.localhost" \
  -H "X-User-Role: super_admin" 2>&1 | grep "< HTTP"
# < HTTP/1.1 200 OK  ← good
# < HTTP/1.1 403 Forbidden  ← check your schema's rbac section
```
