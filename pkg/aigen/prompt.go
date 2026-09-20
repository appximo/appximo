package aigen

// GrammarCore is the COMPACT Appximo schema grammar for an LLM — the closed
// sets (types, ops, actions), the strict-key rule, naming, relations, and one
// canonical worked example. It is deliberately condensed from
// docs/SCHEMA_REFERENCE.md (~2000 lines) to exactly the facts the validator is
// strict about.
//
// It is the SINGLE SOURCE shared by two consumers (JSON-EDITOR-S3):
//   - the internal generation loop (systemPrompt below — the ai-generate command)
//   - `appximo spec` (Spec, spec.go) — the printable pack for an EXTERNAL
//     agent (Claude Code / Cursor / any LLM with the user's own subscription)
//
// Change the grammar here and both stay in sync by construction; prompt_test.go
// additionally validates every embedded example against the real validator.
const GrammarCore = `REQUIRED top-level keys: "$schema", "version", "name", "resources".
- "$schema" must be exactly "https://appximo.com/schema/v1"
- "version" must be the string "1"
- "name" is a short kebab-case app name, e.g. "optica-crm"
- "resources" is an object mapping resource name -> { "fields": {...}, ... }
- Optional top-level "rbac": { "roles": {...} }
- Optional top-level "summary": { "resources": ["orders", "payments"],
  "notify": "changes", "quiet_days": 7 } — "resources": which resources the
  owner's DAILY DIGEST (GET /api/summary, the Telegram "resumen" command) reports
  and in what ORDER (every name must be a declared resource; declare it when the
  app has many resources; omit it and every readable resource is reported,
  attention first). "notify": the SCHEDULED send policy — "changes" (default:
  the morning digest goes out only when something changed since the last one)
  or "always" (the daily report regardless). "quiet_days": after that many
  consecutive silent mornings one short "all quiet, still alive" message goes
  out (default 7; 0 = never). All three keys optional.

NAMING: resource names AND field names match ^[a-z][a-z0-9_]*$ (lowercase, start
with a letter, underscore for multi-word: order_items). Hyphens are NOT allowed
in names. Do NOT name a resource "transaction", "summary" or "ask" (reserved cross-resource
endpoints) or start one with "auth_" (reserved).
Every resource has an implicit "id" UUID primary key — do NOT declare "id".

FIELD TYPES (exact set — nothing else; "number" is INVALID):
  string, text, int, int64, float64, bool, uuid, time, json, jsonb, file
  - MONEY: use "int64" in the currency's MINOR unit (cents) and name the field so
    the unit is unmissable (price_cents, total_cents). There is NO decimal/money
    type, and float64 money is a rounding bug waiting to happen.
  - DOCUMENTS: prefer "jsonb" (a real jsonb column: containment "@>" and a GIN
    index) over "json" (stored as canonical JSON text — not queryable). BOTH
    take and return a JSON VALUE on every door (an object/array/number/boolean;
    a string is read as JSON text); a string that is not JSON is a 422.

FIELD KEYS (all optional unless noted):
  "type" (required), "required": true, "unique": true,
  "default": <literal of the field's type; on a time field "now" = insert moment>,
  "enum": ["a","b"]  (string fields only),
  numeric fields: "min", "max"
  string/text fields: "minLength", "maxLength", "pattern" (RE2 regex),
    "format": one of "email" | "uuid" | "url" | "date"
    On a REQUIRED string/text field ALWAYS declare "minLength": 1 (or more):
    "required" rejects only an absent key or null — the empty string "" is a
    present value, so an empty form field would create blank records with 201.
    The validator warns on this (rule required_text_without_min_length).
  "auto": "create" | "update" | true  (engine-managed timestamp; type must be time,
    field is read-only for clients). "create" = set once when the record is
    inserted; "update" = ALSO refreshed by the engine on every update. The roles
    work with ANY field name — a Spanish "modificado_en" or "fecha_registro" is
    fine. Legacy "auto": true = create semantics, EXCEPT the literal name
    updated_at which also refreshes; on any other update-intent name it would
    freeze at creation (the validator warns, rule auto_update_intent) — prefer
    the explicit "create"/"update" strings.
    The engine OWNS auto fields and the implicit "id" on EVERY write door: a
    create or update body carrying them is a 422 (rule "read_only"). The ONE
    exception is a resource-level "import" declaration (sibling of "fields"):
      "import": { "roles": ["admin"], "fields": ["id", "created_at"] }
    which lets exactly the listed RBAC roles supply those fields ON CREATE
    (data migration / restores; "fields" optional = all governed fields).
    Declare it ONLY when the description asks for importing/migrating existing
    records — never by default (it lets the granted role write its own
    timestamps). Updates never accept engine-managed fields, import or not.
  "relation": "<other_resource>"  (makes this uuid field a foreign key),
    optional "on_delete": "restrict" | "cascade" | "set_null"
  a "file" field attaches an uploaded file to the record (stores a file_id with
    referential integrity); optional "on_delete": "restrict" | "set_null" only —
    no relation/references/enum/default/auto on it. Optional per-field attach
    policy: "accept" (a content-type family "image"|"audio"|"video"|"text", the
    alias "pdf", or an exact type like "application/zip"; string or array) and
    "max_bytes" (> 0) — a write attaching a file that violates them is a 422
    naming the field (rule "file_policy"). Both keys are file-field-only.
    NOTE: the "image" family INCLUDES image/svg+xml, and SVG is XML that may
    carry scripts — the engine serves files with hardened headers (attachment,
    nosniff, CSP), but if YOUR app re-serves user images publicly, exclude it
    with exact types: "accept": ["image/png", "image/jpeg", "image/webp"].

RELATIONS (optional per-resource "relations" block, sibling of "fields", for nested reads):
  "relations": {
    "lines":    { "type": "has_many",   "target": "order_lines", "fk": "order_id" },
    "customer": { "type": "belongs_to", "target": "customers",   "fk": "customer_id" }
  }
  - has_many: the FK lives on the TARGET (child) table.
  - belongs_to: the FK lives on THIS table.
  - many_to_many: needs "through" (junction resource), "fk", "target_fk".
  - "target" must be a declared resource.
  - An FK declared with "references" (a non-id target column, e.g. the
    $user_id pattern's "references": "user_id") works everywhere unchanged:
    the generated read subroute (/api/x/{id}/y) and any relation over that FK
    join against the REFERENCED column, not id — you never handle the
    difference, so don't under-declare relations to avoid it.

INDEXES (optional per-resource "indexes" array): [ { "fields": ["status"], "unique": true } ]
  Optional "method": "btree" (default) | "gin". A gin index is ONLY valid over
  jsonb columns and never unique; it is what makes containment ("attributes @>
  {...}") an index lookup. Optional "opclass" (gin only): "jsonb_ops" (default)
  or "jsonb_path_ops" (smaller/faster, indexes ONLY containment):
    { "fields": ["attributes"], "method": "gin", "opclass": "jsonb_path_ops" }

STATE MACHINES — whenever the description says things move through STEPS ("first
requested, then confirmed, then attended or cancelled", "draft → sent → paid"),
model it as a state machine, NOT as a bare enum. A bare enum lets any value become
any other value, so "the system must not let steps be skipped" would not hold:
  "status": {
    "type": "string", "enum": ["requested","confirmed","attended","cancelled"],
    "default": "requested",
    "state_machine": {
      "initial": "requested",
      "transitions": { "requested": ["confirmed","cancelled"], "confirmed": ["attended","cancelled"],
                       "attended": [], "cancelled": [] }
    }
  }
  - "initial" (string or array) = the state(s) a row may be CREATED in; a "default"
    must be an initial state.
  - "transitions" maps each state to the states it may move to; [] = terminal
    (that row can never change state again). Only on string/text fields; every
    state must also be an enum member when an enum is declared.
  - Optional "pending": ["confirmed"] — the states in which a row WAITS for
    someone to act (the daily digest puts them on top as "esperan acción").
    Each must be a known NON-terminal state (a terminal state is finished, not
    waiting → load error). "pending": [] declares that nothing in this lifecycle
    waits for anyone. Omitted → the digest infers: initial non-terminal states
    are reported as "sin avanzar" (created, not moved), the rest as neutral
    counts. Declare it whenever the description says who must act on what.

OPERATIONAL BLOCKS — WHAT THE ENGINE RUNS WITHOUT ANY CODE, AND WHEN TO DECLARE IT
(CAPACIDADES-VISIBLES-S1). Five optional blocks turn a data model into an app
that REMINDS, REACTS, is SPOKEN TO and REPORTS. Declare each one when the
description implies it — and NOT otherwise: every block is real machinery
(rows in an outbox, a scheduler, a vocabulary), and a catalogue does not need
a reminder. Read the description for these signals:

  signal in the description                      → declare
  "recuérdame / cada mañana / todos los días /
   resumen diario / a las 7"                     → a CRON workflow that enqueues "summary.telegram"
  "avisame cuando / notificar cuando / al crear
   una urgente / cuando quede pagada"            → "events" on that resource + an EVENT workflow
  "por Telegram / por voz / le pregunto al bot /
   Siri / hablo en español", OR the schema names
   are in English and the owner speaks Spanish   → "aliases" (resources AND states)
  several resources and one or two the owner
   reads every morning                           → "summary": { "resources": [...] } in reading order
  any lifecycle with steps someone must act on   → "pending" on its state machine (see STATE MACHINES)
  none of the above (a plain catalogue, a
   read-mostly inventory, a lookup table)        → NONE of these blocks. Do not add a workflow
                                                   "just in case"; an omitted block costs nothing.

1. "events" (per resource, sibling of "fields") — the resource emits a
   transactional outbox event on each listed write: "events": ["create","update"].
   Values are EXACTLY create | update | delete. Declare it ONLY on a resource a
   workflow (or an external consumer) reacts to — an event nobody consumes is a
   row that waits forever, and the engine alerts on it. An EVENT workflow on a
   resource that does not declare that event is a LOAD ERROR.

2. "workflows" (top-level, sibling of "resources") — pipelines executed by the
   worker, never on the request path. Shape (all keys strict):
   "workflows": {
     "recordatorio_matinal": {
       "trigger": { "type": "cron", "cron": "0 7 * * *", "timezone": "America/Bogota" },
       "steps": [ { "name": "recordar", "type": "enqueue", "config": { "topic": "summary.telegram", "data": {} } } ],
       "overlap": "skip", "role": "<a declared role>" },
     "avisar_urgente": {
       "trigger": { "type": "event", "event": "create", "resource": "tareas" },
       "steps": [ { "name": "solo_urgentes", "type": "condition", "config": { "expr": "record.prioridad == 'urgente'" } },
                  { "name": "avisar", "type": "enqueue", "config": { "topic": "summary.telegram", "data": {} } } ],
       "role": "<a declared role>" } }
   - trigger "cron": 5-field spec (minute hour dom month dow) or "@daily" /
     "@every 1h"; "timezone" is an IANA zone (default UTC — declare the owner's
     zone for a morning reminder). trigger "event": "event" is create|update|
     delete and "resource" MUST declare it in its "events".
   - steps run in order; types and their ONLY config keys: "condition" {expr}
     (expr-lang over event/record/tenant/now; false STOPS the run), "update"
     {resource, id, data}, "create" {resource, data}, "webhook" {url,
     hmac_secret_env, data} (HTTPS only), "enqueue" {topic, data}. In data/id a
     string starting with "=" is an expression (e.g. "=record.id").
   - "summary.telegram" is the ONE topic the shipped worker consumes out of the
     box: it sends the owner's daily digest (picture + text) to the app's
     Telegram chat. So "recuérdame cada mañana lo que vence hoy" is a cron
     workflow that enqueues it — no code. "avisame cuando anote algo urgente"
     is an event workflow with a condition step that enqueues the same topic.
     Any other topic needs a consumer the app's backend registers.
   - ALWAYS set "role" to a declared role that may READ the resources the
     steps touch (and write, for update/create steps) — without it the worker's
     default service role gets 403 and the run fails. Use the owner/admin role.
   - "overlap": "skip" (default) | "allow". Name workflows in snake_case, in the
     owner's language. An enqueue of a topic that triggers a workflow is a load
     error (a declared infinite loop).

3. "aliases" — how PEOPLE say a resource or a state, so the voice channel
   (POST /api/ask, the Telegram bot, a Siri shortcut) answers at ZERO cost in
   the owner's words. Two places:
   - resource level (sibling of "fields"): "aliases": ["pedidos", "ventas"]
   - enum field level: "aliases": { "pendiente_pago": ["sin pagar", "impaga"], "pagada": ["cobrada"] }
   Rules: write the SINGULAR form (plural and gender derive: "cobrada" also
   matches «cobradas»/«cobrados»); 1–3 real everyday words per thing, the
   ones a Spanish speaker would actually SAY ("mascotas" for pets, "citas" or
   "turnos" for appointments, "dueños" for owners, "cobrada" for paid) — never
   the schema name itself or its plural, never a word that is already another
   resource or another state (each alias must mean exactly ONE thing in the
   whole schema; the validator rejects collisions naming both sides). A value
   alias MAY repeat across resources ("sin pagar" on orders and on invoices).
   The engine wires NO domain word: if the schema does not declare «pedidos»,
   the bot does not know «pedidos». Declare them whenever the app will be
   spoken to, and ALWAYS when the resource names are in a language other than
   the owner's.

4. "summary" (top-level) — see the top-level keys above: the resources the
   daily digest reports, in reading order. Declare it when the app has more
   than three resources and the description says what the owner tracks;
   omit it for a one-resource app (the default reports everything, attention
   first). "notify"/"quiet_days" only when the description asks.

5. "pending" (inside state_machine) — see STATE MACHINES: declare the states
   in which a row WAITS for someone whenever there is a lifecycle. It is what
   the digest puts on top and what the voice channel counts as "esperan
   acción".

OPERATIONAL EXAMPLE (valid — a personal task app that reminds and reacts; copy
the SHAPE, not the words):
{
  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "agenda",
  "resources": {
    "personas": { "aliases": ["contactos", "gente"],
      "fields": { "nombre": { "type": "string", "required": true, "minLength": 1 }, "telefono": { "type": "string" } } },
    "tareas": { "aliases": ["cosas", "recordatorios"], "events": ["create", "update"],
      "fields": {
        "titulo":     { "type": "string", "required": true, "minLength": 1, "maxLength": 200 },
        "persona_id": { "type": "uuid", "relation": "personas", "on_delete": "set_null" },
        "prioridad":  { "type": "string", "enum": ["normal", "urgente"], "default": "normal" },
        "vence_en":   { "type": "time" },
        "estado":     { "type": "string", "enum": ["pendiente", "hecha", "cancelada"], "default": "pendiente",
                        "aliases": { "pendiente": ["por hacer"], "hecha": ["lista", "terminada"], "cancelada": ["anulada"] },
                        "state_machine": { "initial": "pendiente", "pending": ["pendiente"],
                                           "transitions": { "pendiente": ["hecha", "cancelada"], "hecha": [], "cancelada": [] } } },
        "creado_en":  { "type": "time", "auto": "create" }
      } }
  },
  "workflows": {
    "recordatorio_matinal": { "trigger": { "type": "cron", "cron": "0 7 * * *", "timezone": "America/Bogota" },
      "steps": [ { "name": "recordar", "type": "enqueue", "config": { "topic": "summary.telegram", "data": {} } } ],
      "overlap": "skip", "role": "dueno" },
    "avisar_urgente": { "trigger": { "type": "event", "event": "create", "resource": "tareas" },
      "steps": [ { "name": "solo_urgentes", "type": "condition", "config": { "expr": "record.prioridad == 'urgente'" } },
                 { "name": "avisar", "type": "enqueue", "config": { "topic": "summary.telegram", "data": {} } } ],
      "role": "dueno" }
  },
  "summary": { "resources": ["tareas", "personas"] },
  "rbac": { "roles": { "dueno": { "resources": "*", "actions": ["*"] } } }
}

RBAC (optional). Actions are exactly: read, create, update, delete, or "*".
A role is EITHER role-global OR per-resource — never both keys.
  Role-global form:
    "admin":  { "resources": "*", "actions": ["*"] }
    "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id","title"] }
  Optional "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" }
    - "op" MUST be "eq" (or omitted). "val" may be "$user_id",
      "$external_client_id", or a literal. Those are the ONLY two variables: any
      other "$..." (e.g. "$userid", "$tenant_id") REJECTS the schema.
    - "conditions.field" and every "fields" entry must be a REAL column of the resource.
    - A role-global condition is injected into EVERY resource the role lists, so
      the column must exist on ALL of them. When resources are scoped by DIFFERENT
      columns, use the per-resource "permissions" form instead.
  Per-resource form (each resource scoped by its OWN condition/actions/fields):
    "member": { "permissions": {
      "projects": { "actions": ["read","create","update","delete"],
                    "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" } },
      "tags":     { "actions": ["read"] },
      "posts":    { "actions": ["read","update"],
                    "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
                    "condition_actions": ["update"] } } }
    - A resource absent from "permissions" is DENIED (deny by default).
    - "condition_actions" limits the condition to those actions ("read all, write
      own"); every entry must also be in "actions".

  "$user_id" IS THE ID OF THE LOGIN, NOT A ROW IN ANOTHER TABLE. This is the most
  damaging mistake possible here, because it is VALID and silently returns zero
  rows forever. If "each vet sees only their own appointments" and appointments
  carry "veterinarian_id" (a relation to "veterinarians"), comparing that column
  to "$user_id" matches NOTHING — a veterinarian row's id is not a login id. Give
  the catalogue resource a "user_id" column (uuid, unique) that holds the login id
  and point the relation at it:
    "veterinarians": { "fields": { "name": {"type":"string"},
                                   "user_id": {"type":"uuid","unique":true} } },
    "appointments":  { "fields": { "veterinarian_id":
                         {"type":"uuid","relation":"veterinarians","references":"user_id"} } }
  Rule of thumb: a "$user_id" condition may only name a column that stores a LOGIN
  id — either a plain uuid column, or a relation whose "references" is such a column.

  PUBLIC (anonymous) READS — "rbac.public", sibling of "roles" (ADR-026). When
  the app has pages anyone may see without logging in (a blog's published
  articles, a catalogue, a landing), declare them:
    "rbac": { "roles": { … },
      "public": { "articulos": { "actions": ["read"],
                    "conditions": { "field": "estado", "op": "eq", "val": "publicado" },
                    "fields": ["id","titulo","cuerpo"] },
                  "files": { "actions": ["read"] } } }
    - actions MUST be exactly ["read"] (anonymous writes are rejected at load).
    - conditions.val MUST be a literal — "$user_id" is a load error here (an
      anonymous request has no identity).
    - "fields" bounds what anonymous callers see AND what they may filter/sort
      by; a resource absent from the block stays denied (deny by default).
    - grant "files": {"actions":["read"]} to serve attached images publicly.
  Do NOT create a role named "public" for this — an authenticated role cannot
  be reached without a token; only the rbac.public block is.

  A ROLE THAT WRITES A RESOURCE WITH A "file" FIELD ALSO NEEDS THE "files" GRANT.
  Uploads flow through the built-in store (POST /api/files → attach the returned
  id), and that endpoint is authorized as the virtual resource "files". A role
  granted only the resource can set ids but never upload — every POST /api/files
  answers 403. Grant it explicitly: per-resource form add
  "files": { "actions": ["create", "read"] } (actions only — no conditions/fields
  on the built-in store); role-global form add "files" to the resources list.
  The validator warns on this (rule file_field_without_files_grant).

CANONICAL EXAMPLE (a valid schema — follow this shape exactly):
{
  "$schema": "https://appximo.com/schema/v1",
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
// loop and `appximo spec` can never diverge — the assembled text is
// byte-identical to the pre-refactor literal (AI-F2-S4 behavior unchanged;
// asserted by TestSystemPromptComposition).
//
// It deliberately stays COMPACT (the core grammar only, none of Spec's advanced
// sections): the measured ~90% first-try / 100% convergence economics (AI-F2-S4)
// were established with this prompt, and the internal loop has the validator
// oracle to catch what brevity misses.
const systemPrompt = `You generate Appximo schemas. An Appximo schema is ONE JSON object that an
engine compiles into a multi-tenant REST + GraphQL API at boot. Given a
natural-language description of an app, output a SINGLE valid schema JSON.

OUTPUT RULES (critical):
- Output ONLY the JSON object. No prose, no explanation, no markdown code fences.
- The validator is STRICT about keys: any key outside the grammar below rejects
  the schema. Use only the documented keys.

` + GrammarCore + `

Model the user's app faithfully: pick sensible resources, fields with appropriate
types and validations, relations between them, and at least an "admin" role.
Then read the description once more for the OPERATIONAL BLOCKS signals — a
reminder means a cron workflow, "avisame cuando" means events + an event
workflow, an app that is spoken to (or named in English for a Spanish owner)
means aliases, a lifecycle means "pending" — and declare exactly those, none
by default. Output ONLY the JSON.`

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
