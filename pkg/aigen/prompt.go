package aigen

// GrammarCore is the COMPACT Appitools schema grammar for an LLM — the closed
// sets (types, ops, actions), the strict-key rule, naming, relations, and one
// canonical worked example. It is deliberately condensed from
// docs/SCHEMA_REFERENCE.md (~2000 lines) to exactly the facts the validator is
// strict about.
//
// It is the SINGLE SOURCE shared by two consumers (JSON-EDITOR-S3):
//   - the internal generation loop (systemPrompt below — the ai-generate command)
//   - `appitools spec` (Spec, spec.go) — the printable pack for an EXTERNAL
//     agent (Claude Code / Cursor / any LLM with the user's own subscription)
//
// Change the grammar here and both stay in sync by construction; prompt_test.go
// additionally validates every embedded example against the real validator.
const GrammarCore = `REQUIRED top-level keys: "$schema", "version", "name", "resources".
- "$schema" must be exactly "https://appitools.dev/schema/v1"
- "version" must be the string "1"
- "name" is a short kebab-case app name, e.g. "optica-crm"
- "resources" is an object mapping resource name -> { "fields": {...}, ... }
- Optional top-level "rbac": { "roles": {...} }

NAMING: resource names AND field names match ^[a-z][a-z0-9_]*$ (lowercase, start
with a letter, underscore for multi-word: order_items). Hyphens are NOT allowed
in names. Do NOT name a resource "transaction" or start one with "auth_" (reserved).
Every resource has an implicit "id" UUID primary key — do NOT declare "id".

FIELD TYPES (exact set — nothing else; "number" is INVALID):
  string, text, int, int64, float64, bool, uuid, time, json, jsonb, file
  - MONEY: use "int64" in the currency's MINOR unit (cents) and name the field so
    the unit is unmissable (price_cents, total_cents). There is NO decimal/money
    type, and float64 money is a rounding bug waiting to happen.
  - DOCUMENTS: prefer "jsonb" (a real jsonb column: containment "@>" and a GIN
    index) over "json" (stored as TEXT — exact bytes, but not queryable).

FIELD KEYS (all optional unless noted):
  "type" (required), "required": true, "unique": true,
  "default": <literal of the field's type; on a time field "now" = insert moment>,
  "enum": ["a","b"]  (string fields only),
  numeric fields: "min", "max"
  string/text fields: "minLength", "maxLength", "pattern" (RE2 regex),
    "format": one of "email" | "uuid" | "url" | "date"
  "auto": true  (engine-managed timestamp, for created_at/updated_at; type must be time)
  "relation": "<other_resource>"  (makes this uuid field a foreign key),
    optional "on_delete": "restrict" | "cascade" | "set_null"
  a "file" field attaches an uploaded file to the record (stores a file_id with
    referential integrity); optional "on_delete": "restrict" | "set_null" only —
    no relation/references/enum/default/auto on it

RELATIONS (optional per-resource "relations" block, sibling of "fields", for nested reads):
  "relations": {
    "lines":    { "type": "has_many",   "target": "order_lines", "fk": "order_id" },
    "customer": { "type": "belongs_to", "target": "customers",   "fk": "customer_id" }
  }
  - has_many: the FK lives on the TARGET (child) table.
  - belongs_to: the FK lives on THIS table.
  - many_to_many: needs "through" (junction resource), "fk", "target_fk".
  - "target" must be a declared resource.

INDEXES (optional per-resource "indexes" array): [ { "fields": ["status"], "unique": true } ]
  Optional "method": "btree" (default) | "gin". A gin index is ONLY valid over
  jsonb columns and never unique; it is what makes containment ("attributes @>
  {...}") an index lookup. Optional "opclass" (gin only): "jsonb_ops" (default)
  or "jsonb_path_ops" (smaller/faster, indexes ONLY containment):
    { "fields": ["attributes"], "method": "gin", "opclass": "jsonb_path_ops" }

RBAC (optional). Actions are exactly: read, create, update, delete, or "*".
A role is EITHER role-global OR per-resource — never both keys.
  Role-global form:
    "admin":  { "resources": "*", "actions": ["*"] }
    "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id","title"] }
  Optional "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" }
    - "op" MUST be "eq" (or omitted). "val" may be "$user_id" or a literal.
    - "conditions.field" and every "fields" entry must be a REAL column of the resource.
    - A role-global condition is injected into EVERY resource the role lists, so
      the column must exist on ALL of them. When resources are scoped by DIFFERENT
      columns, use the per-resource "permissions" form instead.

CANONICAL EXAMPLE (a valid schema — follow this shape exactly):
{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"], "default": "open" },
        "due":    { "type": "time" },
        "owner_id": { "type": "uuid" },
        "created_at": { "type": "time", "auto": true }
      },
      "indexes": [ { "fields": ["status"] } ]
    }
  },
  "rbac": {
    "roles": {
      "admin":  { "resources": "*", "actions": ["*"] },
      "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id", "title", "status"] }
    }
  }
}`

// systemPrompt is the internal generation loop's system prompt: the shared
// grammar wrapped in the loop-specific instructions ("output ONLY JSON", model
// the app faithfully). Built by CONCATENATION from GrammarCore so the internal
// loop and `appitools spec` can never diverge — the assembled text is
// byte-identical to the pre-refactor literal (AI-F2-S4 behavior unchanged;
// asserted by TestSystemPromptComposition).
//
// It deliberately stays COMPACT (the core grammar only, none of Spec's advanced
// sections): the measured ~90% first-try / 100% convergence economics (AI-F2-S4)
// were established with this prompt, and the internal loop has the validator
// oracle to catch what brevity misses.
const systemPrompt = `You generate Appitools schemas. An Appitools schema is ONE JSON object that an
engine compiles into a multi-tenant REST + GraphQL API at boot. Given a
natural-language description of an app, output a SINGLE valid schema JSON.

OUTPUT RULES (critical):
- Output ONLY the JSON object. No prose, no explanation, no markdown code fences.
- The validator is STRICT about keys: any key outside the grammar below rejects
  the schema. Use only the documented keys.

` + GrammarCore + `

Model the user's app faithfully: pick sensible resources, fields with appropriate
types and validations, relations between them, and at least an "admin" role.
Output ONLY the JSON.`

// correctionPreamble prefaces the actionable validation errors fed back to the
// model on a failed attempt. The errors themselves are the machine-readable
// report from schema.ValidateReport (path/rule/message/expected/got/fix), which
// is exactly what the AI-F0-S2 feedback contract was built to be corrected from.
const correctionPreamble = `The schema you produced is INVALID. Below is the validator's machine-readable
report. For each error, edit the schema at "path" following "fix" (use "expected"
to pick a valid value). Then output the COMPLETE corrected schema as a single JSON
object — ONLY the JSON, no prose, no fences.

VALIDATION ERRORS:
`

// irCorrectionNote is appended to the correction preamble in array-IR mode. The
// model generated the IR (arrays of named objects), so the error paths use array
// indices (resources[0].fields[1]…); this reminds it to keep emitting the IR form.
const irCorrectionNote = `(You are generating the ARRAY-IR form: resources, fields, relations and roles are
ARRAYS of objects with an explicit "name". The error paths below use array indices
into that IR — correct at those positions and keep the same array-IR shape.)

`
