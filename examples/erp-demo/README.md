# Nimbus ERP — Appximo full-app example (auth + RBAC + relations)

The first **complete application** example for Appximo: an HR + operations ERP
modeled entirely as a schema, exercising the engine end to end — typed fields,
declarative validation, all three relation kinds, hooks, outbox events, indexes,
and a realistic RBAC policy with row-level isolation, wired to the in-engine
**password identity** (`/auth/*`).

It is designed as a **single-tenant** validation case (one company = one tenant),
which is the product's sweet spot. Multi-tenant is the same schema registered for
more tenants; nothing here changes for that.

> The canonical *minimal* example is still
> [`examples/quickstart`](../quickstart/schema.json) (`todo-api`, one resource).
> This one is the opposite end: a full app, to prove the engine on a real domain.

## What it models

A company ("Nimbus S.A.") running its own HR + projects backend.

| Resource | What it is | Notable engine features it exercises |
|---|---|---|
| `departamentos` | Departments | `has_many` empleados; unique `codigo` with RE2 `pattern`; `bool` default |
| `empleados` | Employee **profile** | linked to identity by `user_id`; `belongs_to` departamento; `has_many` solicitudes/evaluaciones; `many_to_many` proyectos; `format:email`; numeric `min`/`max`; enum + `default` |
| `solicitudes` | Leave/permission requests | row-level RBAC owner (`user_id`); JS `before_create` hook; outbox `events`; `belongs_to` empleado |
| `proyectos` | Projects | `many_to_many` empleados (inverse); enum/default; composite types |
| `asignaciones` | Junction empleado↔proyecto | the m2m `through` table; **composite unique index**; enum role |
| `evaluaciones` | Performance reviews | `belongs_to` empleado; `float64` score with `min`/`max`; `pattern` period |

Field types used across the schema: `string`, `text`, `int`, `float64`, `time`,
`uuid`, `bool`. Validations used: `required`, `unique`, `min`/`max`,
`minLength`/`maxLength`, `pattern` (RE2), `format: email`, `enum`, `default`,
`auto`.

## Identity ↔ profile: how `auth_users` connects to `empleados`

Appximo's identity table (`tenant_<id>.auth_users`) is **fixed** — it holds only
`email` / `password_hash` / `role` / MFA. The **person's profile** (name, document,
position, salary, department…) is a normal schema resource, `empleados`, joined to
the identity by a single column:

```
auth_users.id  ──(JWT "sub" claim = $user_id)──▶  empleados.user_id  (uuid, unique)
```

- A user signs up at `POST /auth/signup` (role forced to the configured signup
  role — here `empleado`). That creates the `auth_users` row and returns its `id`.
- An admin then creates the matching `empleados` row with `user_id` = that id.
- From then on, every request that user makes carries `sub = user_id` in the JWT,
  so the engine can resolve "this caller ↔ this employee" with no extra lookup.

Not every employee needs a login: 12 of the seeded `empleados` have no `auth_users`
account (`user_id` null) — exactly how a real HR roster looks.

## RBAC (4 roles, with one row-level)

| Role | Resources | Actions | Row condition |
|---|---|---|---|
| `rrhh-admin` | `*` | `*` | — (full HR admin) |
| `supervisor` | `*` | read/create/update | — (manager: sees everyone, can't delete) |
| `empleado` | `empleados`, `solicitudes` | read/create/update | **`user_id = $user_id`** |
| `auditor` | `*` | read | — (read-only audit) |

The `empleado` role is row-scoped: an employee sees **only their own** profile and
**only their own** requests. Verified live: logging in as an employee and listing
`/api/empleados` returns exactly 1 row (theirs); `/api/proyectos` returns `403`
(deny by default); `DELETE` returns `403` (no delete action).

> **Why `empleado` only spans `empleados` + `solicitudes`:** a role's `conditions`
> are **role-global** — the one condition is applied to *every* resource the role
> can read. So a row-conditioned role can only include resources that all carry the
> condition column (`user_id` here). This is a real engine constraint, not an
> oversight — see *Engine findings* below.

## Run it

```bash
# 1. boot the engine WITH this schema (it becomes the served schema)
set -a; source <your-secrets>; set +a      # DATABASE_URL, JWT_SECRET, ADMIN_KEY
export APPXIMO_AUTH_SIGNUP_ROLE=empleado  # enables POST /auth/signup as 'empleado'
./appximo serve --schema examples/erp-demo/schema.json --port 8080

# 2. register the tenant with the SAME schema (boot ↔ stored must match)
PAYLOAD=$(jq -n --slurpfile s examples/erp-demo/schema.json \
  '{tenant_id:"nimbus",display_name:"Nimbus S.A.",email:"rrhh@nimbus.example",plan:"pro",schema:$s[0]}')
curl -X POST http://localhost:9090/tenants -H "X-Admin-Key: $ADMIN_KEY" \
  -H "Content-Type: application/json" -d "$PAYLOAD"

# 3. mint an admin token, write data (Host = tenant subdomain)
TOKEN=$(./appximo token --secret "$JWT_SECRET" --tenant nimbus --role rrhh-admin | tail -1)
curl -X POST http://localhost:8080/api/departamentos \
  -H "Authorization: Bearer $TOKEN" -H "Host: nimbus.localhost" \
  -H "Content-Type: application/json" \
  -d '{"codigo":"TEC","nombre":"Tecnología","presupuesto":850000000}'
```

A populated dataset (5 departments, 17 employees, 6 projects, 24 assignments, 20
requests, 14 reviews, 5 of them with linked logins) can be recreated with the
seeding approach used in the Phase-2 validation session.

### Restoring the bench fixture (the `import` grant)

The k6 write benches (`tests/performance/erp_writes.js`, `erp_patch.js`,
`erp_tx.js`) target one well-known employee row,
`id = 11111111-1111-1111-1111-111111111111`. If that row is ever lost, restore
it by POSTing it back **with its id** — `empleados` declares
`"import": { "roles": ["rrhh-admin"] }` (WRITE-ASYMMETRY-S1), which is what
lets an rrhh-admin supply the engine-governed `id` on create. Without that
declaration the engine rejects a caller-supplied id at every door (422
`read_only`):

```bash
curl -X POST http://localhost:8080/api/empleados \
  -H "Authorization: Bearer $TOKEN" -H "Host: nimbus.localhost" \
  -H "Content-Type: application/json" \
  -d '{"id":"11111111-1111-1111-1111-111111111111","documento":"11111111",
       "nombre":"Bench","apellido":"Fixture","email":"bench.fixture@nimbus.example",
       "cargo":"Benchmark","salario":1000000,"tipo_contrato":"indefinido",
       "fecha_ingreso":"2024-01-01T00:00:00Z"}'
```

(If the running tenant's stored schema predates the `import` key, deploy this
schema first: `appximo migrate --tenant nimbus --schema examples/erp-demo/schema.json`.)

### Relations in one round-trip (`?include=`, no N+1)

```bash
# department + its employees (has_many)
curl -g "http://localhost:8080/api/departamentos/<id>?include=empleados" -H ...
# employee + department + requests (belongs_to + has_many, ONE query)
curl -g "http://localhost:8080/api/empleados/<id>?include=departamento,solicitudes" -H ...
# employee ↔ projects (many_to_many through asignaciones), both directions
curl -g "http://localhost:8080/api/empleados/<id>?include=proyectos" -H ...
curl -g "http://localhost:8080/api/proyectos/<id>?include=empleados" -H ...
```

GraphQL serves the same nested shape from the same single LATERAL query:

```graphql
{ departamentos { data { codigo nombre empleados { nombre cargo } } } }
```

## Engine findings surfaced by this example (Phase 2)

Building a *real* app surfaced three things worth the engine's attention. **Two of
the three (the security ones) are now fixed** (FASE3-SEC); the first remains a
documented design limitation:

1. **Row `conditions` are role-global.** One condition per role, applied to all of
   the role's resources — so a row-scoped role can only span resources that share
   the condition column. Modeling "an employee also reads the department catalog"
   under the same row-scoped role isn't expressible today. Documentation gap +
   possible future per-resource conditions. *(Open — design limitation, not a bug.)*

2. **~~Create bypasses the row condition and the field allowlist.~~ FIXED.** The
   create path now applies the SAME row-level `conditions` and field allowlist that
   read/update/delete do (`codegen.EnforceCreateRBAC`, shared by REST `POST` and the
   GraphQL `create` mutation): an owner-scoped role's record is **forced** to its own
   id, and a body claiming another principal's id is **rejected with `403`** — the
   `empleado` mass-assignment is closed. An unrestricted role (`rrhh-admin`) still
   creates freely (measured `no_change` on the create hot path).

3. **~~Prometheus path label is the concrete URL for middleware-rejected by-id
   requests.~~ FIXED.** A request denied *before* the router resolves its pattern
   (e.g. an RBAC `403` on `DELETE /api/empleados/{uuid}`) now has its id-bearing path
   segments templated to `{id}` for the metric label, so by-id probing can no longer
   explode `/metrics` cardinality. Handler-reached requests are templated by chi
   exactly as before.

Everything else worked on the first try: schema load, all relation kinds, the JS
hook, multi-field `422` validation, declarative defaults, indexes, the admin API
data browse, GraphQL nested embeds, auth signup/login, and per-tenant
observability.

## API certification (Newman)

`api-cert.postman_collection.json` is an end-to-end certification of the
production API surface, run as a real external consumer would. With the engine
serving this schema for tenant `nimbus`:

```bash
bash scripts/api-cert.sh        # mints the tokens, runs `newman run`
```

It exercises, as 19 requests / 36 assertions: the served OpenAPI contract
(`/openapi.json`), the full auth cycle (refresh + invalid/missing-token `401`),
CRUD with **pagination meta**, **filters** and **sort**, consumable error codes
(`400`/`403`/`404`/`422`-with-`fields[]`), RBAC deny-by-default (a read-only role
gets `403` on write), and create-time **mass-assignment protection** (an
owner-scoped role cannot attribute a row to another principal → `403`). `newman
run` exits non-zero on any failed assertion, so it is a reproducible PASS/FAIL gate.

Interactive docs: open **`/docs`** (Swagger UI) against the running engine.
