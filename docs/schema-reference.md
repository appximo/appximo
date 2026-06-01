# Schema Reference

Complete reference for `schema.json`. Every field, every option, every rule.

---

## Top-level structure

```json
{
  "$schema": "https://appitools.dev/schema/v1",
  "version":  "1",
  "name":     "my-api",
  "resources": { ... },
  "rbac":      { ... }
}
```

| Field | Required | Type | Description |
|---|---|---|---|
| `$schema` | yes | string | Schema URL — must be `https://appitools.dev/schema/v1` |
| `version` | yes | string | Schema version — currently `"1"` |
| `name` | no | string | Human name for the API, used in generated code comments |
| `resources` | yes | object | Map of resource name → resource definition |
| `rbac` | no | object | Role-based access control policy |

---

## `resources`

### Resource name rules

- Must start with a lowercase letter (`a-z`)
- Followed by lowercase letters, digits, or hyphens (`a-z0-9-`)
- Regex: `^[a-z][a-z0-9\-]*$`
- Maps to a PostgreSQL table in the tenant schema

```
✅  guides        → tenant_acme.guides
✅  line-items    → tenant_acme.line-items
❌  Guides        → fails (uppercase)
❌  _guides       → fails (underscore start)
❌  1guides       → fails (digit start)
```

---

### `fields`

Each field maps to a database column. Field names follow similar rules but use underscores instead of hyphens:

- Starts with a lowercase letter
- Followed by lowercase letters, digits, or underscores
- Regex: `^[a-z][a-z0-9_]*$`

```
✅  created_at
✅  client_id
❌  ClientId     → fails (uppercase)
❌  _internal    → fails (underscore start)
```

---

#### Field types reference

| Type | Go type | PostgreSQL | Notes |
|---|---|---|---|
| `string` | `string` | `TEXT` | Short to medium text, indexed efficiently |
| `text` | `string` | `TEXT` | Long content: descriptions, notes, HTML |
| `int` | `int` | `INTEGER` | Whole numbers, ±2.1 billion |
| `int64` | `int64` | `BIGINT` | Large integers, ±9.2 × 10¹⁸ |
| `float64` | `float64` | `FLOAT8` | Double precision: prices, weights, coords |
| `bool` | `bool` | `BOOLEAN` | `true` / `false` |
| `uuid` | `[16]byte` | `UUID` | Foreign keys; returned as `"xxxxxxxx-xxxx-..."` in JSON |
| `time` | `time.Time` | `TIMESTAMPTZ` | Timestamps with timezone (UTC stored) |
| `json` | `map[string]any` | `JSONB` | Nested objects, flexible schema |

---

#### Field options

All options are optional unless noted.

##### `required` (bool)

The field must be present in create requests. Appitools validates non-empty at the DB constraint level.

```json
"code": { "type": "string", "required": true }
```

**Fails if:** `POST /api/guides` body omits `code` (Postgres NOT NULL fires).

---

##### `unique` (bool)

Creates a `UNIQUE` constraint on this column.

```json
"email": { "type": "string", "unique": true }
```

**Fails if:** Two records have the same email (Postgres UNIQUE fires, returns 500 today — per-field validation coming in v0.2).

---

##### `auto` (bool)

Marks the field as automatically set by the database. Use with `type: "time"` for `created_at`/`updated_at`.

```json
"created_at": { "type": "time", "auto": true }
```

Clients must NOT send `auto` fields in request bodies. If sent, they are ignored.

---

##### `enum` (array of strings)

Restricts values to this list. Must be non-empty. Checked at schema validation time.

```json
"status": {
  "type": "string",
  "enum": ["pending", "in_transit", "delivered", "returned"]
}
```

**Fails validation if:**
```json
"status": { "type": "string", "enum": [] }
// → resources.guides.fields.status.enum: enum must not be empty
```

---

##### `relation` (string)

Declares a foreign key to another resource. The field type must be `uuid`.

```json
"client_id": {
  "type": "uuid",
  "relation": "clients"
}
```

**Fails validation if:**
```json
"client_id": { "type": "uuid", "relation": "companies" }
// → resources.guides.fields.client_id.relation: relation "companies" references unknown resource
```

---

##### `default` (any)

A default value used when the field is absent from the request body. Type must match the field type.

```json
"role":   { "type": "string",  "default": "operario" },
"active": { "type": "bool",    "default": true },
"count":  { "type": "int",     "default": 0 }
```

---

### `hooks`

Hooks attach code to lifecycle events. Defined per resource.

#### Supported lifecycle events

| Event | Timing | Can abort? |
|---|---|---|
| `before_create` | Before `INSERT` | Yes — set `result.proceed = false` |
| `after_create` | After `INSERT`, async | No — fire-and-forget |

---

#### JS hook

```json
"hooks": {
  "before_create": {
    "type": "js",
    "script": "if (!data.code) { result.proceed = false; result.error = 'code required'; }"
  }
}
```

| Field | Required | Description |
|---|---|---|
| `type` | yes | Must be `"js"` |
| `script` | yes | JavaScript source string. Must be non-empty. |

**Fails validation if `script` is empty:**
```json
"before_create": { "type": "js", "script": "" }
// → resources.guides.hooks.before_create.script: js hook requires a non-empty script
```

---

#### Webhook hook

```json
"hooks": {
  "after_create": {
    "type": "webhook",
    "url": "https://erp.company.com/hooks/guide-created",
    "hmac_secret_env": "WEBHOOK_SECRET"
  }
}
```

| Field | Required | Description |
|---|---|---|
| `type` | yes | Must be `"webhook"` |
| `url` | yes | HTTPS endpoint to POST to |
| `hmac_secret_env` | no | Env var name holding the HMAC-SHA256 signing secret |

**Fails validation if `url` is empty:**
```json
"after_create": { "type": "webhook", "url": "" }
// → resources.guides.hooks.after_create.url: webhook hook requires a non-empty url
```

---

### `indexes`

Composite indexes to create on the table. Not yet used by the generate command — reserved for the Atlas migration runner.

```json
"indexes": [
  { "fields": ["status", "created_at"] },
  { "fields": ["client_id"] }
]
```

---

## `rbac`

### Structure

```json
"rbac": {
  "roles": {
    "role_name": {
      "resources":  "*",
      "actions":    ["*"],
      "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" },
      "fields":     ["code", "status"]
    }
  }
}
```

### `roles`

A map of role name → role policy. Role names can be any string (e.g. `"super_admin"`, `"operario"`).

---

#### `resources` (required)

Which resources this role can access.

| Value | Effect |
|---|---|
| `"*"` | All resources |
| `["guides", "clients"]` | Only listed resources |

```json
"resources": "*"
"resources": ["guides", "dispatches"]
```

---

#### `actions` (required)

Which CRUD operations are permitted.

| Value | HTTP methods |
|---|---|
| `"*"` | All |
| `"read"` | `GET` |
| `"create"` | `POST` |
| `"update"` | `PUT`, `PATCH` |
| `"delete"` | `DELETE` |

```json
"actions": ["*"]
"actions": ["read", "create"]
```

---

#### `conditions` (optional)

A single predicate that is evaluated at request time and injected as a WHERE clause context. The handler receives it via `rbac.EvalResultFromCtx()`.

```json
"conditions": {
  "field": "operator_id",
  "op":    "eq",
  "val":   "$user_id"
}
```

| Sub-field | Description |
|---|---|
| `field` | Column name in the resource table |
| `op` | Operator: `"eq"` (more operators in v0.2) |
| `val` | Static value or session variable (`$user_id`, `$external_client_id`) |

Session variables are resolved from request headers:
- `$user_id` → `X-User-ID` header
- `$external_client_id` → `X-External-Client-ID` header

---

#### `fields` (optional)

A field allowlist for read-restricted roles. When set, the evaluation result includes `AllowedFields` — handlers use it to strip non-listed fields from responses (full field filtering in v0.2).

```json
"fields": ["code", "status", "updated_at"]
```

---

## Validation rules summary

| Rule | Error message |
|---|---|
| Resource name invalid | `invalid resource name "X": must match ^[a-z][a-z0-9-]*$` |
| Field name invalid | `invalid field name "X": must match ^[a-z][a-z0-9_]*$` |
| Unknown field type | `unknown field type "X"` |
| Relation references missing resource | `relation "X" references unknown resource` |
| Empty enum array | `enum must not be empty` |
| JS hook with empty script | `js hook requires a non-empty script` |
| Webhook hook with empty URL | `webhook hook requires a non-empty url` |
| Unknown hook type | `unknown hook type "X": must be "js" or "webhook"` |

---

## Complete example

```json
{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "logistics-api",
  "resources": {
    "clients": {
      "fields": {
        "name":  { "type": "string", "required": true },
        "nit":   { "type": "string", "unique": true },
        "email": { "type": "string" },
        "type":  { "type": "string", "enum": ["empresa", "persona"] }
      }
    },
    "guides": {
      "fields": {
        "code":           { "type": "string",  "unique": true, "required": true },
        "status":         { "type": "string",  "enum": ["pending", "in_transit", "delivered"] },
        "origin":         { "type": "string",  "required": true },
        "destination":    { "type": "string",  "required": true },
        "weight_kg":      { "type": "float64" },
        "declared_value": { "type": "float64" },
        "client_id":      { "type": "uuid",    "relation": "clients" },
        "created_at":     { "type": "time",    "auto": true }
      },
      "hooks": {
        "before_create": {
          "type": "js",
          "script": "if (!data.code) { result.proceed = false; result.error = 'code required'; }"
        },
        "after_create": {
          "type": "webhook",
          "url": "https://erp.example.com/hooks/guide",
          "hmac_secret_env": "WEBHOOK_SECRET"
        }
      },
      "indexes": [
        { "fields": ["status", "created_at"] }
      ]
    }
  },
  "rbac": {
    "roles": {
      "super_admin": {
        "resources": "*",
        "actions": ["*"]
      },
      "operator": {
        "resources": ["guides"],
        "actions": ["read", "create"],
        "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
      },
      "public": {
        "resources": ["guides"],
        "actions": ["read"],
        "fields": ["code", "status"]
      }
    }
  }
}

---

## Paginación de la API REST

### Offset (default)

```
GET /api/guides?page=2&per_page=20
```

| Parámetro | Default | Máximo | Descripción |
|---|---|---|---|
| `page` | 1 | 10000 | Página (1-based) |
| `per_page` | 20 | 100 | Registros por página |

### Cursor / Keyset (recomendado para producción)

Evita la degradación de `OFFSET` en tablas grandes. Usa un index range scan — rendimiento constante sin importar la profundidad de paginación.

```
GET /api/guides?after=01234567-89ab-cdef-0123-456789abcdef&per_page=20
GET /api/guides?before=01234567-89ab-cdef-0123-456789abcdef&per_page=20
```

| Parámetro | SQL generado | Orden |
|---|---|---|
| `after=UUID` | `WHERE id > UUID LIMIT N` | `id ASC` |
| `before=UUID` | `WHERE id < UUID LIMIT N` | `id DESC` (cliente invierte) |

`after` tiene precedencia sobre `before` si ambos se envían.

**Flujo típico:**
```
1ª página: GET /api/guides?per_page=20
           → guarda data[last].id como cursor

Sig. página: GET /api/guides?after={cursor}&per_page=20
Pág. anterior: GET /api/guides?before={first_id}&per_page=20
```

### Filtros

```
GET /api/guides?filter[status][eq]=pending
GET /api/guides?filter[code][partial]=GD-
GET /api/guides?filter[weight_kg][gte]=50
```

| Tipo de campo | Operadores |
|---|---|
| `string`, `text` | `eq`, `partial`, `start` |
| `int`, `int64`, `float64` | `eq`, `gte`, `lte`, `gt`, `lt` |
| `time` | `eq`, `gte`, `lte`, `gt`, `lt`, `after`, `before` |
| `uuid`, `bool` | `eq` |

Sin operador explícito → `eq`: `filter[status]=pending`

### Ordenamiento

```
GET /api/guides?order[created_at]=desc
```

### Búsqueda full-text

```
GET /api/guides?search=bogota
```

Busca en todos los campos `string` y `text` (ILIKE).
```
