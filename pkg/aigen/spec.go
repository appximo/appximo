package aigen

// JSON-EDITOR-S3: the EXTERNAL-agent pack. Spec() assembles the printable
// grammar for `appximo spec` — paste it into any agent's context (Claude
// Code, Cursor, a system prompt) and that agent becomes an Appximo schema
// generator, running the SAME validator-guided loop the internal ai-generate
// uses, but on the user's own subscription (zero product API cost).
//
// Composition: the shared GrammarCore (prompt.go — the exact grammar the
// internal loop generates against, single source) + the ADVANCED sections the
// compact internal prompt deliberately omits (state machines, hooks, events,
// per-resource RBAC, full FK coverage, renames) + a second worked example + the
// correction-loop instructions. Every embedded example is validated against the
// real engine validator by spec_test.go, so the printed grammar can never teach
// a shape the engine rejects.

// specHeader frames the document for the agent that receives it.
const specHeader = `# Appximo schema grammar — for an LLM/agent

You are generating an Appximo schema: ONE JSON object that an engine compiles
into a multi-tenant REST + GraphQL + OpenAPI server at boot. There are no
handlers, models, or migrations — the schema is the whole application
definition. This document is the complete distilled grammar; the engine's
validator is STRICT (any key outside this grammar rejects the schema, nothing
is silently ignored).

This is one of THREE companion documents the CLI prints — together they cover a
complete app: this one (` + "`appximo spec`" + `) teaches the SCHEMA;
` + "`appximo backend-spec`" + ` teaches custom Go handlers, hooks, auth and
background jobs; ` + "`appximo frontend-spec`" + ` teaches the frontend (the
API contract a UI consumes, error→screen-state mapping, files/images).
` + "`appximo specs`" + ` prints all three at once.

## Core grammar
`

// specAdvanced documents the grammar the compact internal prompt omits. Facts
// audited against pkg/schema/validator.go + docs/SCHEMA_REFERENCE.md — do not
// invent surface beyond what is listed.
const specAdvanced = `
## Advanced grammar (optional blocks)

(State machines and the per-resource RBAC form are in the core grammar above —
they are the two constructs a business description most often implies.)

RBAC CUSTOM-ROUTE grants (only when a Go backend registers custom endpoints with
appximo.Route — the pure binary has none, and a grant for a route that is not
registered FAILS THE BOOT):
  "customer": {
    "permissions": { "orders": { "actions": ["read"],
                     "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } } },
    "routes": { "checkout": { "actions": ["create"] } }
  }
  - The key is the FIRST path segment after /api/ (POST /api/checkout → "checkout";
    POST /api/webhooks/stripe → "webhooks"). Actions map from the HTTP method:
    GET→read, POST→create, PUT/PATCH→update, DELETE→delete.
  - "routes" is ORTHOGONAL to resources/permissions and may be combined with
    either — it grants ENDPOINTS, not tables. This is the ONLY way a role using
    per-resource "permissions" can reach a custom route.
  - NO "conditions" and NO "fields": a route segment has no rows and no columns
    (declaring either is rejected at load). The data a handler touches is
    authorized separately, against the real resources.
  - A key that names a declared RESOURCE is rejected — use "permissions" for that.

FOREIGN-KEY EXTRAS (on a relation field):
  - "references": "<column>" — point the FK at a target column other than "id";
    it must be "id" or a "unique" column of the target, type-compatible.
  - "on_update": "restrict" | "cascade" | "set_null" — action when the
    referenced key changes (unset = NO ACTION).
  - "set_null" (either action) requires the field NOT be "required".
  Composite multi-column FKs are a resource-level "foreign_keys" array:
    "foreign_keys": [ { "columns": ["region","branch"], "target": "branches",
                        "ref_columns": ["region","branch"], "on_delete": "cascade" } ]
  - "columns"/"ref_columns" same length; "ref_columns" must form the target's
    primary key or a UNIQUE index (declare it in the target's "indexes").

HOOKS (per-resource "hooks" object; events are EXACTLY before_create,
after_create, before_update, after_update — there are NO delete hooks):
  "hooks": {
    "before_create": { "type": "js",
      "script": "if (!data.total) { result.proceed = false; result.error = 'total requerido'; }" },
    "after_create":  { "type": "webhook", "url": "https://erp.example.com/hook",
      "hmac_secret_env": "WEBHOOK_SECRET" }
  }
  - after_* hooks MUST be type "webhook" (js/wasm after-hooks are rejected).
  - before_* hooks MUST be type "js" or "wasm" (a webhook before-hook is
    rejected: a before-hook decides the write synchronously, which an async
    POST cannot).
  - webhook: "hmac_secret_env" is the NAME of an env var, never the secret;
    HTTPS-only. js: sandboxed, "data" is the record, set result.proceed=false
    + result.error to reject. wasm: "wasm_module" (+ optional "wasm_fn").

EVENTS (per-resource opt-in transactional outbox emission):
  "events": ["create", "update", "delete"]   // exactly these values

RENAMES (safe evolution — the migration renames instead of drop+create):
  - field:    "telefono": { "type": "string", "renamed_from": "tel" }
  - resource: "clientes": { "renamed_from": "customers", "fields": {...} }
  - The old name must NOT still be declared. Only meaningful when evolving an
    EXISTING deployed schema; never emit it on a fresh design.

RELATIONS extra key: "limit": <n> bounds embedded children per parent (default 50).

## Common mistakes (each of these REJECTS the schema)
- "type": "number" — use int, int64 or float64. The type set is closed.
- Declaring "id" — it is implicit on every resource.
- A hyphen in a resource/field name (kebab-case is only for the top-level "name").
- A resource named "transaction" or starting with "auth_".
- Any key not in this grammar, at any level (e.g. "webhooks", "secret",
  "description" on a field) — the validator lists the valid keys in its error.
- "conditions" with "op" other than "eq".
- many_to_many without "through" + "target_fk"; a relation "target"/"through"
  that is not a declared resource.
- "enum" / "default" / "relation" / "auto" on a "file" field; "cascade" as a
  file field's "on_delete" (only restrict | set_null).
- A js/wasm hook on after_create/after_update (webhook only), or a webhook
  hook on before_create/before_update (js/wasm only).
- "events" values other than create | update | delete.
- A "default" that is not a literal of the field's type, not an enum member, or
  not an initial state of its state machine.
`

// specExampleHeader introduces the second worked example.
const specExampleHeader = `
## Worked example — relations, file field, state machine, per-resource RBAC

(An optical-store CRM: patients, appointments with a lifecycle, prescriptions
carrying an uploaded file, products with an m2m to suppliers, and three roles —
including an owner-scoped "read all, write own" optometrist.)

`

// SpecExampleAdvanced is the advanced worked example printed by `appximo
// spec` — kept as a standalone const so spec_test.go validates it against the
// real validator (the spec can never teach an invalid shape).
const SpecExampleAdvanced = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "optica-crm",
  "resources": {
    "pacientes": {
      "fields": {
        "user_id":    { "type": "uuid", "unique": true },
        "documento":  { "type": "string", "required": true, "unique": true, "pattern": "^[0-9]{6,12}$" },
        "nombre":     { "type": "string", "required": true, "maxLength": 120 },
        "email":      { "type": "string", "format": "email" },
        "created_at": { "type": "time", "auto": true }
      },
      "relations": {
        "citas":    { "type": "has_many", "target": "citas",    "fk": "paciente_id", "limit": 50 },
        "formulas": { "type": "has_many", "target": "formulas", "fk": "paciente_id" }
      }
    },
    "citas": {
      "fields": {
        "paciente_id":  { "type": "uuid", "required": true, "relation": "pacientes", "on_delete": "cascade" },
        "optometra_id": { "type": "uuid" },
        "fecha":        { "type": "time", "required": true },
        "estado": {
          "type": "string", "enum": ["agendada", "atendida", "cancelada"], "default": "agendada",
          "state_machine": {
            "initial": "agendada",
            "transitions": { "agendada": ["atendida", "cancelada"], "atendida": [], "cancelada": [] }
          }
        }
      },
      "relations": {
        "paciente": { "type": "belongs_to", "target": "pacientes", "fk": "paciente_id" }
      },
      "events": ["create", "update"],
      "indexes": [ { "fields": ["estado", "fecha"] } ]
    },
    "formulas": {
      "fields": {
        "paciente_id":   { "type": "uuid", "required": true, "relation": "pacientes", "on_delete": "restrict" },
        "archivo":       { "type": "file", "on_delete": "restrict" },
        "observaciones": { "type": "text" },
        "created_at":    { "type": "time", "auto": true }
      }
    },
    "productos": {
      "fields": {
        "sku":    { "type": "string", "required": true, "unique": true },
        "nombre": { "type": "string", "required": true, "maxLength": 160 },
        "precio": { "type": "float64", "min": 0 },
        "stock":  { "type": "int", "min": 0, "default": 0 }
      },
      "relations": {
        "proveedores": { "type": "many_to_many", "target": "proveedores",
                         "through": "producto_proveedores", "fk": "producto_id", "target_fk": "proveedor_id" }
      }
    },
    "proveedores": {
      "fields": { "nombre": { "type": "string", "required": true, "unique": true } }
    },
    "producto_proveedores": {
      "fields": {
        "producto_id":  { "type": "uuid", "required": true, "relation": "productos",   "on_delete": "cascade" },
        "proveedor_id": { "type": "uuid", "required": true, "relation": "proveedores", "on_delete": "cascade" }
      },
      "indexes": [ { "fields": ["producto_id", "proveedor_id"], "unique": true } ]
    }
  },
  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] },
      "optometra": {
        "permissions": {
          "pacientes": { "actions": ["read", "update"] },
          "citas": {
            "actions": ["read", "update"],
            "conditions": { "field": "optometra_id", "op": "eq", "val": "$user_id" },
            "condition_actions": ["update"]
          },
          "formulas": { "actions": ["read", "create"] }
        }
      },
      "vendedor": { "resources": ["productos", "proveedores"], "actions": ["read"] }
    }
  }
}`

// specFooter documents the correction loop — the same validator-guided loop the
// internal ai-generate runs, driven by the external agent instead.
const specFooter = `
## The correction loop (how to work)

1. Generate the schema JSON from the app description, following this grammar.
2. Validate it with the engine's oracle:
       appximo validate --json schema.json
   The output is { "valid", "errors": [ { "path", "rule", "message",
   "expected", "got", "fix" } ] } — machine-readable, one entry per problem.
3. If invalid: for each error, edit the schema at "path" following "fix"
   (pick from "expected" when given). Re-validate. Repeat until "valid": true.
4. Deliver ONLY the JSON object (no prose, no markdown fences).

The finished schema can be pasted into Appximo Studio's Code view (/editor →
Code) — the same validator runs live there, errors land on their lines, and a
gated Apply + Deploy takes it to a running API. Full human-grade reference:
docs/SCHEMA_REFERENCE.md.
`

// Spec returns the complete printable pack for an external agent.
func Spec() string {
	return specHeader + "\n" + GrammarCore + "\n" + specAdvanced + specExampleHeader +
		SpecExampleAdvanced + "\n" + specFooter
}
