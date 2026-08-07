# Appximo — Schema Reference

> **The complete grammar of the Appximo schema JSON, extracted from the engine
> source.** This is the exhaustive reference that the feature docs (AGENTS.md,
> README) summarize: every key the schema accepts, at every level, with its exact
> syntax, valid values, defaults, and the precise condition under which the
> validator rejects it.

Appximo compiles one JSON file into a multi-tenant REST + GraphQL + OpenAPI
server at boot. There are no handlers, models, or migration files — the schema
*is* the contract. This document specifies that contract precisely enough that a
human (or an AI generation layer) can author a valid schema from it alone.

> Source references use **file + symbol** (a function/type/var/const name), not
> line numbers — line numbers drift as files grow, symbol names don't. Each
> citation names the file and the symbol its rule lives in; find it with your
> editor's go-to-symbol or `grep`. (Same convention as
> [MENTAL_MODEL.md](MENTAL_MODEL.md).)

## Provenance — this is extracted from the code

The **source of truth is the engine code**, not memory or prose. Every rule below
was read out of the validator and parser (`pkg/schema`), the query builder
(`pkg/query`), the router/codegen (`pkg/codegen`), the RBAC evaluator
(`pkg/rbac`), the migration bridge (`pkg/migration`, `pkg/schemadiff`), the hook
runtime (`pkg/extensions`), and the boot wiring (`app.go`). The authority for
"what is a valid schema" is `schema.Validate` + `schema.LoadFromFile`
(`pkg/schema`). Citations are inline as **file + symbol**.

This is documentation only — no engine behavior was changed. Where the code does
something that diverges from what a reader would reasonably expect (or from how a
feature is documented elsewhere), it is recorded — not fixed — in
[Appendix A](#appendix-a--code-vs-expectation-findings).

## How to read this

- **file + symbol citations** point at the rule's implementation (grep the symbol).
- **`§N` cross-references** point at another section of this document.
- A claim of the form *"rejected at load"* means `schema.Validate` (or
  `LoadFromFile`'s strict-key / required-field pass) returns an error and the
  engine refuses to boot / `appximo validate` fails. *"422 at request time"*
  means the schema loads but a write violating the rule is rejected per-request.

## Sections

1. [Root schema structure](#1-root-schema-structure)
2. [Resources](#2-resources)
3. [Fields and types](#3-fields-and-types)
4. [Declarative field validation](#4-declarative-field-validation)
5. [State machines](#5-state-machines)
6. [Declarative relations (nested `?include=` reads)](#6-declarative-relations-nested-include-reads)
7. [RBAC — roles, permissions, conditions](#7-rbac--roles-permissions-conditions)
8. [Hooks (lifecycle extensions)](#8-hooks-lifecycle-extensions)
9. [Indexes, events, the file store, and reserved blocks](#9-indexes-events-the-file-store-and-reserved-blocks)
10. [What a schema generates (the API surface)](#10-what-a-schema-generates-the-api-surface)
11. [Complete annotated example](#11-complete-annotated-example)
12. [Machine validation — the formal JSON Schema meta-schema](#12-machine-validation--the-formal-json-schema-meta-schema)
- [Appendix A — code-vs-expectation findings](#appendix-a--code-vs-expectation-findings)

---

## 1. Root schema structure

An Appximo project is **one JSON file**. It is loaded in two stages and the second only runs if the first succeeds (`app.go`): `schema.LoadFromFile` (parse + strict-key + required-field check) then `schema.Validate` (semantic validation). Both must pass clean for the engine to boot.

### 1.1 Top-level keys

The root object is the `APISchema` struct (`pkg/schema/types.go`). Its complete, exhaustive key set — and the only keys accepted at the top level — is (`pkg/schema/keys.go`):

| JSON key | Type | Required? | Meaning |
|---|---|---|---|
| `$schema` | string | **Yes** — load fails if empty/absent | Schema dialect URL (any non-empty string passes; conventionally `"https://appximo.com/schema/v1"`). |
| `version` | string | **Yes** — load fails if empty/absent | Schema version string (any non-empty string passes; conventionally `"1"`). |
| `name` | string | No (structurally optional) | Project name. Parsed but not required by either stage. |
| `resources` | object (`map[string]ResourceSchema`) | No (structurally optional) | The entity/table definitions, keyed by resource name. See §2. A schema with no resources serves no `/api/*` routes but is not rejected. |
| `rbac` | object (`{ "roles": { … } }`) | No (structurally optional) | Role policies. See §7. |
| `workflows` | object (`map[string]WorkflowSchema`) | No — **reserved, non-functional** (see §1.4) | Forward-compatibility only; no executor runs it. |

**Exact required-field rule** (`pkg/schema/loader.go`): only `$schema` and `version` are checked, and only for emptiness (the zero value of a Go `string`, which is also what an absent key unmarshals to). The exact failure messages are:

- missing/empty `$schema` → `missing required field "$schema"`
- missing/empty `version` → `missing required field "version"`

`$schema` is checked first; if it is empty the loader returns that error before even looking at `version`. Neither value is format-checked — `$schema: "x"` and `version: "banana"` both pass; only emptiness is rejected. (`version` is a Go `string` field, so a JSON number such as `"version": 1` fails to unmarshal at the parse stage — `parse schema JSON: …` — rather than at the required-field stage.)

`name`, `resources`, and `rbac` are **structurally optional** — neither `LoadFromFile` nor `Validate` requires their presence. `Validate` (`pkg/schema/validator.go`) simply iterates `s.Resources` and `s.RBAC.Roles`; absent/empty maps yield zero iterations and zero errors. (Whether a useful API results is another matter — a schema with no resources exposes no CRUD routes, and a request whose role is not in `rbac.roles` is denied by RBAC's deny-by-default.)

### 1.2 Two-stage load contract & strict-key rejection

**Stage 1 — `LoadFromFile(path)` (`pkg/schema/loader.go`):**
1. Read the file (`read schema file: …` on I/O error).
2. `json.Unmarshal` into `APISchema` (`parse schema JSON: …` on malformed JSON).
3. `CheckUnknownKeys(rawBytes)` — strict-key validation against the **raw** bytes (run after unmarshalling, on the original bytes, because `json.Unmarshal` has already silently dropped unknown keys by the time it returns). Any unknown key aborts the load with:
   ```
   schema has unknown keys:
     <field path>: unknown key "<key>" (valid keys: <comma-separated valid set>)
     …
   ```
4. Required-field check (`$schema`, `version`) as above.

Note the order: strict-key (step 3) runs **before** the required-field check (step 4), so an unknown key is reported even when `$schema`/`version` are also missing.

**Strict keys at EVERY level.** `CheckUnknownKeys` (`pkg/schema/keys.go`) walks the entire document and rejects any key outside the documented set *for that level*, listing the valid keys in the error. This is deliberate: an unknown key is a typo, not an extension (e.g. `webhooks` instead of `hooks`, `refcolumns` instead of `ref_columns`), and a silently-dropped key would become quietly dead config. The levels it strict-checks (each "valid keys" list below is emitted in the exact argument order shown):

- root: `$schema, version, name, resources, rbac, workflows` (keys.go)
- each resource: `fields, hooks, indexes, events, relations, renamed_from, foreign_keys` (keys.go)
- each `foreign_keys[]` entry: `columns, target, ref_columns, on_delete, on_update` (keys.go)
- each relation: `type, target, fk, through, target_fk, limit` (keys.go)
- each `indexes[]` entry: `fields, unique` (keys.go)
- each field: `type, required, unique, auto, enum, relation, on_delete, on_update, references, renamed_from, default, min, max, minLength, maxLength, pattern, format, state_machine` (keys.go)
- each field's `state_machine`: `initial, transitions` (the `transitions` map's *keys* are user state names, so only these two top-level keys are checked) (keys.go)
- each hook event name must be one of `before_create, after_create, before_update, after_update` (keys.go) and each hook object: `type, script, url, hmac_secret_env, wasm_module, wasm_fn, timeout` (keys.go)
- `rbac`: `roles` (keys.go); each role: `resources, actions, conditions, fields, permissions` (keys.go); each role `conditions`: `field, op, val` (keys.go); each `permissions.<resource>`: `actions, conditions, condition_actions, fields` (keys.go), and that permission's nested `conditions`: `field, op, val` (keys.go)
- each workflow: `trigger, steps`; the trigger: `type, event, resource, cron, path`; each step: `name, type, ref, config, next` (keys.go) — note `step.config` is the **one deliberately free-form map** (its inner keys are not strict-checked).

A non-object value where an object is expected is reported (`must be a JSON object`), not silently ignored (keys.go). The root itself, if not a JSON object, is reported as `schema is not a JSON object: …` (keys.go). Errors are sorted by field path (keys.go).

**Stage 2 — `schema.Validate(s)` (`pkg/schema/validator.go`):** semantic validation over the already-parsed, strict-key-clean struct: identifier regexes, reserved names, type/relation/RBAC/state-machine/default coherence, etc. (covered in the later sections). On the boot path `app.New` joins all returned errors into one message prefixed `appximo: invalid schema:` (app.go). The `appximo validate <schema>` subcommand runs the same two stages.

Identifier rule relevant at this level: resource and field names both match the regex `^[a-z][a-z0-9_]*$` — lowercase, start with a letter, `_` for multi-word names; `-` is rejected (`pkg/schema/validator.go`). Reserved resource names: any name with the `auth_` prefix and the exact name `transaction` are rejected (validator.go).

### 1.3 Minimal complete example

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"], "default": "open" },
        "due":    { "type": "time" }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin":  { "resources": "*", "actions": ["*"] },
      "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id", "title", "status"] }
    }
  }
}
```

(The `id` UUID primary key is implicit — never declare it. Field/resource details are in §2–§7.)

### 1.4 The `workflows` block — reserved, non-functional

`workflows` is **parsed for forward-compatibility only; no executor runs it** (`pkg/schema/types.go`). It exists so a schema that declares workflows loads today and stays valid when the orchestration engine eventually ships. Its keys ARE strict-checked (keys.go), so typos are still caught, but nothing in the engine consults the block at runtime.

Reserved shape (`pkg/schema/types.go`):

```json
"workflows": {
  "<workflow_name>": {
    "trigger": {
      "type": "event",            // "event" | "cron" | "http"
      "event": "after_create",    // with "resource" for type=event
      "resource": "tasks",
      "cron": "0 * * * *",        // for type=cron
      "path": "/run/something"    // for type=http
    },
    "steps": [
      {
        "name": "notify",
        "type": "webhook",        // "hook" | "webhook" | "wasm" | "branch"
        "ref": "some-ref",
        "config": { "any": "free-form map" },
        "next": "next_step_name"
      }
    ]
  }
}
```

Do not rely on any of this executing — it is inert in the current engine. Note that the trigger `type`/`event`/`resource`/`cron`/`path` and step `type`/`name`/`ref`/`next` values shown in the comments are NOT validated for membership (no semantic check runs over the block); only the *key names* are strict-checked.

---

## 2. Resources

A schema's `resources` object maps each resource (entity) **name** to a resource definition. Each resource becomes one Postgres table (per tenant) plus its derived REST + GraphQL surface. This section covers resource **naming**, the resource-level **key set**, and the two resource-level structural blocks `renamed_from` and `foreign_keys`. Field-level keys, relations, indexes, events, hooks and RBAC are documented in their own sections (see §3 Fields, §4 Relations, §5 Indexes, §6 Events/Hooks, §7 RBAC).

### 2.1 Resource naming

Every resource name MUST match the regex:

```
^[a-z][a-z0-9_]*$
```

i.e. start with a lowercase letter, then any of lowercase letters, digits, and `_`. Multi-word names use underscores. (Validated in `pkg/schema/validator.go` via `resourceNameRe`, applied per resource in `Validate`.)

| Valid | Invalid | Why invalid |
|---|---|---|
| `order_items` | `Orders` | uppercase letter |
| `tasks` | `order-items` | `-` not allowed |
| `ledger_entries` | `2things` | must start with a letter |
| `customer` | `order items` | space not allowed |

**Why `-` is rejected (not just style):** a resource name becomes a **GraphQL type/field identifier**, and GraphQL identifiers permit `_` but not `-`. Allowing `-` historically let a schema *pass* `validate` and then **panic the engine at boot** while building the GraphQL schema. The regex closes that gap end to end.

A name that fails the regex is rejected with:

> `invalid resource name "<name>": must match ^[a-z][a-z0-9_]*$ — start with a lowercase letter and use '_' for multi-word names (e.g. order_items); '-' is not allowed (a resource name must be a valid GraphQL identifier)`

#### Reserved prefix `auth_`

A resource name may not begin with `auth_` (`reservedResourcePrefix`). This prefix guards the engine's per-tenant **authentication tables**, which live in the same tenant schema: `auth_users`, `auth_tokens`, `auth_identities`, `auth_mfa`, `auth_backup_codes`. Because resource names may now carry `_`, a resource named e.g. `auth_users` would collide with these engine-managed tables, so the whole prefix is reserved. Rejection message:

> `invalid resource name "<name>": the "auth_" prefix is reserved for the engine's per-tenant authentication tables (auth_users, auth_tokens, …)`

#### Reserved name `transaction`

The exact name `transaction` (`reservedTransactionResource`) is rejected. The engine claims it for the atomic multi-resource batch endpoint `POST /api/transaction`; a resource literally named `transaction` would have its collection route shadowed by the batch handler. (The plural `transactions` is **not** reserved and is allowed.) Rejection message:

> `invalid resource name "transaction": reserved for the atomic multi-resource transaction endpoint (POST /api/transaction)`

> Note: the implicit `id` UUID primary key is created by the engine for every resource — do not declare it as a field (see §3).

### 2.2 Resource-level key set

A resource object accepts exactly these keys (any other key rejects the schema, from `pkg/schema/keys.go` `CheckUnknownKeys`):

| Key | Type | Required | Covered in |
|---|---|---|---|
| `fields` | object (field name → field def) | structurally central | §3 Fields |
| `hooks` | object (event → hook config) | optional | §6 |
| `indexes` | array of index defs | optional | §5 |
| `events` | array of `"create"`/`"update"`/`"delete"` | optional | §6 |
| `relations` | object (embed name → relation def) | optional | §4 |
| `renamed_from` | string | optional | §2.3 below |
| `foreign_keys` | array of composite-FK defs | optional | §2.4 below |

Only `fields` carries the entity's data shape; the rest are optional and additive — a resource that omits them serves exactly as before with zero added overhead. An unknown key produces:

> `unknown key "<key>" (valid keys: fields, hooks, indexes, events, relations, renamed_from, foreign_keys)`

### 2.3 `renamed_from` (resource / table rename)

Declares the resource's **previous table name** so the migration engine performs a data-preserving rename rather than a drop+add (which would strand the data). It emits:

```sql
ALTER TABLE <old> RENAME TO <new>
```

This is metadata-only: the table's rows, indexes and constraints are preserved under the new name. Once the rename has been applied the key is **inert** — re-provisioning with it still present is a no-op (the old table no longer exists), so it is safe to leave in the schema.

Syntax:

```json
"clients": {
  "renamed_from": "customers",
  "fields": { "name": { "type": "string" } }
}
```

Three validation rules (from `Validate`, all rejecting the schema at load):

1. **Valid identifier** — `renamed_from` must match `^[a-z][a-z0-9_]*$`, else:
   > `invalid renamed_from "<old>": must match ^[a-z][a-z0-9_]*$`
2. **Must differ from the resource's own name** — `renamed_from` cannot equal the new name:
   > `renamed_from must differ from the resource's own name`
3. **Old name must NOT still be a declared resource** — you cannot rename from a name that still exists in the same schema:
   > `renamed_from "<old>" is still a declared resource — you cannot rename from a name that still exists (remove the old resource)`

Note that these checks are **purely structural and load-time** — they cannot and do not verify that the old table actually exists in any tenant DB. A `renamed_from` naming a table that was never created is simply a no-op rename intent (load validation still passes).

(The same `renamed_from` key exists at the field level for a column rename; see §3.)

### 2.4 `foreign_keys` (resource-level COMPOSITE foreign keys)

`foreign_keys` is an **array** of composite (multi-column) foreign-key definitions. Use it for a genuinely multi-column FK whose `(col_a, col_b, …)` references a multi-column PK/unique on the target. A **single-column** FK is normally the field-level `relation` (with optional `references`, `on_delete`, `on_update`; see §4) — that is the idiomatic form. (The validator only requires `len(columns) >= 1`, so a single-column entry here is not *rejected*, but the field-level `relation` is the intended single-column path.)

Each entry (`ForeignKeyDef`) has exactly these keys (strict-checked; a typo like `refcolumns` is rejected):

| Key | Type | Default | Meaning |
|---|---|---|---|
| `columns` | array of strings | (required) | local source columns on this resource |
| `target` | string | (required) | referenced resource name |
| `ref_columns` | array of strings | (required) | referenced columns on `target` |
| `on_delete` | `restrict` \| `cascade` \| `set_null` | `restrict` | action when the referenced row is deleted |
| `on_update` | `restrict` \| `cascade` \| `set_null` | NO ACTION (unset) | action when the referenced key changes |

**Defaults applied by the engine** (in `pkg/migration/desired.go`, `refActionForOnDelete` / `refActionForOnUpdate`): unset `on_delete` ⇒ `RESTRICT` (the safe choice — a delete of a still-referenced parent is rejected rather than orphaning children); unset `on_update` ⇒ Postgres `NO ACTION` (NOT `restrict`) — deliberately, so adding `on_update` to an existing schema generates no migration churn on tenants whose FKs already carry NO ACTION.

**Auto-index:** the source `columns` are automatically given a composite btree index named `idx_<resource>_<col1>_<col2>_…` (the columns joined by `_`) at provisioning, so the referential check is an index lookup (you do not declare it by hand). This index is created in the migration layer (`addCompositeForeignKeys`), not by the load-time validator.

#### Validation rules (`validateForeignKeys`)

Every rule below rejects the schema at load:

1. **At least one source column** — `len(columns) >= 1`, else
   > `foreign key must list at least one column`
2. **Source columns must exist on this resource** — each entry in `columns` must be a declared field; the implicit primary key `id` is explicitly allowed (treated as always-valid without being a declared field). Otherwise:
   > `column "<c>" does not exist on this resource`
3. **Target must be a known resource** — `target` must name another declared resource, else:
   > `foreign key target "<target>" references unknown resource`
4. **At least one ref_column** — `len(ref_columns) >= 1`, else:
   > `foreign key must list at least one ref_column`
5. **Equal counts** — `len(columns) == len(ref_columns)`, else:
   > `columns (<n>) and ref_columns (<m>) must have the same length`
6. **Ref columns must exist on target** — each `ref_columns` entry must be a declared field on `target` (or the literal `id`), else:
   > `ref_column "<rc>" does not exist on target "<target>"`
7. **Ref columns must TOGETHER be unique on the target** (`columnsAreUniqueOnTarget`). Postgres requires an FK destination to be a primary key or unique key. The set qualifies when it is one of:
   - exactly `["id"]` (the implicit primary key), OR
   - a **single** column whose field declares `unique: true`, OR
   - the **exact column set of a declared `unique` index** on the target — matched **order-independently** (`sameStringSet`), so the FK may list the columns in any order.

   Otherwise:
   > `ref_columns must together form the target's primary key or a UNIQUE constraint/index on "<target>" (a foreign key may only point at a unique key)`
8. **Per-position type compatibility** — for each position `j` (only checked when `len(columns) == len(ref_columns)`), the coarse Postgres class of `columns[j]` must equal that of `ref_columns[j]` (`refColumnKind` / `pgKindForAPIType`). The classes are: `string`/`text`/`json` → `text`; `int` → `integer`; `int64` → `bigint`; `float64` → `double`; `bool` → `bool`; `uuid` → `uuid`; `time` → `timestamptz`; the implicit `id` is `uuid`. A mismatch yields:
   > `type mismatch: <col> (<type>) references <target>.<refcol> (<reftype>)`
9. **`set_null` requires nullable source columns** — if either `on_delete` or `on_update` is `set_null` and **any** source column is `required: true` (NOT NULL), the schema is rejected:
   > `set_null requires all source columns to be nullable, but at least one is required (NOT NULL)`
10. **Action vocabulary** — an `on_delete`/`on_update` value outside `restrict|cascade|set_null` yields `unknown on_delete "<v>": must be one of restrict, cascade, set_null` (and the analogous `on_update` message).

#### Complete composite-FK example

The target `branches` exposes a composite `unique` index over `(region_code, branch_code)` (the destination the FK points at), and `orders` references it with a 2-column FK:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "logistics",
  "resources": {
    "branches": {
      "fields": {
        "region_code": { "type": "string", "required": true },
        "branch_code": { "type": "string", "required": true },
        "name":        { "type": "string" }
      },
      "indexes": [
        { "fields": ["region_code", "branch_code"], "unique": true }
      ]
    },
    "orders": {
      "fields": {
        "region_code": { "type": "string" },
        "branch_code": { "type": "string" },
        "total":       { "type": "float64" }
      },
      "foreign_keys": [
        {
          "columns":     ["region_code", "branch_code"],
          "target":      "branches",
          "ref_columns": ["region_code", "branch_code"],
          "on_delete":   "cascade",
          "on_update":   "restrict"
        }
      ]
    }
  },
  "rbac": {
    "roles": { "admin": { "resources": "*", "actions": ["*"] } }
  }
}
```

Here both `region_code` and `branch_code` on `orders` are nullable (not `required`), so `set_null` would also be permissible; `cascade` deletes a branch's orders when the branch is deleted.

---

## 3. Fields and types

A resource's data shape is declared under its `fields` map: each key is a field
name and each value a `FieldDef`. Field names are validated against
`^[a-z][a-z0-9_]*$` (`pkg/schema/validator.go`, `fieldNameRe`) — lowercase, start
with a letter, `_` for multi-word names; anything else rejects the schema with
`invalid field name "<name>": must match ^[a-z][a-z0-9_]*$` (`validator.go`).

### 3.1 The implicit `id` primary key

Every resource gets an implicit `id` column of type UUID, `NOT NULL`, primary key,
`DEFAULT gen_random_uuid()` — added by the migration builder, not declared in JSON
(`pkg/migration/desired.go`). **Do NOT declare `id` in `fields`**: the builder
explicitly skips a field literally named `id` (`desired.go`), and RBAC/FK code
treats `id` as always-present (`rbacFieldExists` returns true for `id`,
`validator.go`; `refColumnKind`/`targetFieldType` treat `id` as `uuid`,
`validator.go`). `id` is also the implicit FK destination (see §3.4) and the
default sort/keyset key (see the query section).

### 3.2 The field type set

`type` is required and must be one of exactly eleven values from `validFieldTypes`
(`pkg/schema/validator.go`). Any other value — notably `number` — rejects the
schema with `unknown field type "<type>"` (`validator.go`). The Postgres
column type is fixed by `TypeForAPIType` (`pkg/schemadiff/parsetype.go`), and
the filter operators a type accepts are fixed by `operatorsForType`
(`pkg/query/builder.go`).

| `type` | Meaning | Postgres column | Filter ops available |
|---|---|---|---|
| `string` | short UTF-8 text | `TEXT` | `eq`, `partial` (`ILIKE %v%`), `start` (`ILIKE v%`), `is_null` |
| `text` | long UTF-8 text (identical column to `string`) | `TEXT` | `eq`, `partial`, `start`, `is_null` |
| `int` | 32-bit integer | `INTEGER` | `eq`, `gt`, `gte`, `lt`, `lte`, `is_null` |
| `int64` | 64-bit integer | `BIGINT` | `eq`, `gt`, `gte`, `lt`, `lte`, `is_null` |
| `float64` | floating-point number | `DOUBLE PRECISION` | `eq`, `gt`, `gte`, `lt`, `lte`, `is_null` |
| `bool` | boolean | `BOOLEAN` | `eq`, `is_null` |
| `uuid` | UUID | `UUID` | `eq`, `is_null` |
| `time` | timestamp with time zone | `TIMESTAMPTZ` | `eq`, `gt`, `gte`, `lt`, `lte`, `after` (→ `>`), `before` (→ `<`), `is_null` |
| `json` | JSON stored as text | `TEXT` | `eq`, `is_null` |
| `jsonb` | a real JSON document (LIBRARY-GAPS-S1) | `JSONB` | `eq`, `is_null` |
| `file` | reference to an uploaded file (FILES-LINK-S1) | `UUID` + a real FK to the tenant's `files(id)` | `eq`, `is_null` |

`is_null` (SCHEMA-6, HOUSEKEEPING-S1): `?filter[field][is_null]=true` → `IS
NULL`, `=false` → `IS NOT NULL`; accepted values are exactly `true`/`1`/
`false`/`0` (the `?count` vocabulary, ENG-23 — anything else is a named 400).
It is valid on every declared type but only on a NULLABLE column: on the
implicit `id` or a `required` field it is a 400 saying the column can never be
null. The clause is structural (no bound parameter). GraphQL parity: `is_null:
Boolean` on `StringFilter`/`DateFilter`/`RangeFilter`, and `uuid`/`bool`/
`file`/`json`/`jsonb` fields — previously unfilterable in GraphQL — take a
`NullFilter { is_null }`. The aggregate endpoint inherits it like any filter.

Notes verified in code:
- `string` and `text` are byte-identical at the column level (both `BaseText` →
  `TEXT`, `parsetype.go`) and share the same filter ops; `text` exists only
  as a documentation hint. The declarative validation keys `minLength`/`maxLength`/
  `pattern`/`format` apply to both (`stringTypes = {string, text}`,
  `validator.go`), and `min`/`max` apply to the three numeric types
  (`numericTypes = {int, int64, float64}`, `validator.go`).
- `after`/`before` on `time` are aliases: `filterToSQL` maps `after`→`>`,
  `before`→`<` (`builder.go`).
- **`json` vs `jsonb`** — two different storage decisions, both kept:
  - `json` maps to **TEXT** (`TypeForAPIType`): the bytes you sent come back
    unchanged, but Postgres sees an opaque string. Not indexable as a document, no
    containment. Every pre-LIBRARY-GAPS-S1 column stays exactly as it was.
  - `jsonb` maps to **JSONB**: a parsed, binary document. It is the only type a
    `gin` index may cover (`{"fields":["attrs"],"method":"gin"}` — §9.1), which is
    what makes `attrs @> '{"brand":"Acme"}'` an index lookup instead of a
    sequential scan. pgx decodes it into a Go `map[string]any` on read and encodes
    a Go map (or a pre-encoded JSON string) on write.
  - **Prefer `jsonb`** for anything you may query. Reach for `json` only when the
    exact byte representation matters (a stored signature payload).
  - Neither can be a `group_by` key (`groupByTypeOK`, `pkg/query/aggregate.go`),
    and both filter with `eq` only. In GraphQL a `jsonb` field is the `JSON`
    scalar (the document, passed through); `json` stays `String`.
- **Money has no type of its own.** There is no `decimal`/`money`; `float64` money
  is a rounding bug. Use `int64` in the currency's MINOR unit and name the field
  for it (`price_cents`, `total_cents`) — the industry-standard representation, and
  the one most payment APIs already speak.
- A `file` field stores the `file_id` that `POST /api/files` returns and carries a
  REAL foreign key to the tenant's own `files` table (`buildDesiredSchema`,
  `pkg/migration/desired.go`): a write whose value references no file of the tenant
  is a `422` `file_not_found` on that field (REST and GraphQL —
  `db.FileReferenceViolation`); deleting a still-attached file is a `409` unless the
  field declares `on_delete: "set_null"` (`cascade` is rejected at load); deleting
  the record never deletes the file. `relation`/`references`/`on_update`/`enum`/
  `default`/`auto` are rejected on a file field (`Validate`, the file-field block).
- A filter operator outside a KNOWN type's set returns `400`
  `operator "<op>" not allowed for type "<type>" (allowed: …)` (`builder.go`).
  A `filter[<field>]` for an unknown field returns `400`
  `unknown filter field: <field>` (`builder.go`).

Example resource exercising the type table:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "catalog-api",
  "resources": {
    "products": {
      "fields": {
        "name":        { "type": "string", "required": true, "maxLength": 200 },
        "description": { "type": "text" },
        "sku":         { "type": "string", "unique": true },
        "quantity":    { "type": "int" },
        "views":       { "type": "int64" },
        "price":       { "type": "float64" },
        "active":      { "type": "bool", "default": true },
        "owner_id":    { "type": "uuid" },
        "released_at": { "type": "time" },
        "attributes":  { "type": "json" },
        "created_at":  { "type": "time", "auto": true },
        "updated_at":  { "type": "time", "auto": true }
      }
    }
  }
}
```

### 3.3 Structural field keys

These keys shape the column and its constraints. (The declarative *validation* keys
`min`/`max`/`minLength`/`maxLength`/`pattern`/`format` and the typing rules for
`default` are covered in §4 — `default` is noted here only as existing.)

- **`type`** (string, required) — see §3.2.
- **`required`** (bool, default false) — emits a `NOT NULL` column
  (`desired.go`, `NotNull: f.Required`). A POST/PUT (full write) that omits a
  required field is a `422` validation failure; a PATCH validates only the fields
  actually sent (partial semantics). A required field that also declares a `default`
  is satisfied by the default when omitted (see §4).
- **`unique`** (bool, default false) — emits a single-column `UNIQUE` constraint
  named `<resource>_<field>_key` (`desired.go`). A write that collides returns
  `409 Conflict` (the raw Postgres error is masked). A composite unique constraint
  is declared via `indexes` (`{ "fields": [...], "unique": true }`), not here.
- **`auto`** (bool, default false) — engine-managed timestamp column: `TIMESTAMPTZ
  DEFAULT now()`, nullable (`desired.go`). Intended for `created_at` /
  `updated_at`. An `auto` field is exempt from the `required` check, and **cannot
  declare a `default`** — `validateDefault` rejects it with
  `default cannot be set on an auto field` (`validator.go`).
- **`enum`** (array of strings) — restricts the value to the listed set. String
  values only; a write outside the set is a `422`. The key must not be present and
  empty: an empty array rejects the schema with `enum must not be empty`
  (`validator.go`).
- **`default`** — applied on create when the field is omitted; type-checked at
  load. Full rules (per-type literal forms, the dynamic `"now"` on `time`, enum
  membership, interaction with `required` and `auto`) are in §4.

### 3.4 Single-column foreign keys: `relation`, `on_delete`, `on_update`, `references`

Declaring `relation` on a field turns that column into a real single-column foreign
key (the FK lives on the column, exactly like `unique`/`required`). It also
generates one read-only subroute (see below).

```json
"customer_id": {
  "type": "uuid",
  "relation": "customers",
  "on_delete": "restrict",
  "on_update": "cascade",
  "references": "id"
}
```

**`relation`** (string) — the name of the referenced resource. It must be a known
resource in the same schema, else the schema is rejected with
`relation "<name>" references unknown resource` (`validator.go`). The FK
column references the target's `id` by default (or the `references` column).

**`on_delete`** — the referential action when the parent row is deleted. Valid
values are exactly `restrict`, `cascade`, `set_null`
(`validOnDeleteActions`, `types.go`). **Unset defaults to `restrict`**
(`refActionForOnDelete`, `desired.go` — empty and `"restrict"` both map to
`Restrict`). `restrict` rejects a delete of a still-referenced row with `409`;
`cascade` deletes the children; `set_null` nulls the FK column. Rejection
conditions (`validator.go`):
- `on_delete` without `relation` → `on_delete is only valid on a field that declares a relation`
- an unknown value → `unknown on_delete "<v>": must be one of restrict, cascade, set_null`
- `set_null` on a `required` field → `on_delete set_null requires a nullable column, but this field is required (NOT NULL)`

**`on_update`** — the action when the parent row's referenced key changes. Same
value set `restrict`/`cascade`/`set_null` (`validOnUpdateActions`,
`types.go`). **Unset defaults to NO ACTION** — deliberately NOT `restrict`
(`refActionForOnUpdate`, `desired.go`; the empty/default case returns
`NoAction`). This asymmetry with `on_delete` exists so adding `on_update` to an
existing schema produces no churn on FKs already created without an `ON UPDATE`
clause (Postgres stores such FKs with confupdtype `'a'` = NO ACTION). Rejection
conditions mirror `on_delete` (`validator.go`): only valid with `relation`;
unknown value rejected; `set_null` on a `required` field rejected.

**`references`** (string) — the target COLUMN the FK points at. **Unset (or `"id"`)
defaults to `"id"`** (`refColumnOrID`, `desired.go`; the load-time validator
also skips the unique/type checks when `references` is empty or `"id"`,
`validator.go`). A non-`id` value must name a column that is UNIQUE on the target
and be type-compatible with this field (`validator.go`). Rejection
conditions:
- `references` without `relation` → `references is only valid on a field that declares a relation`
- a non-unique target column → `references "<col>" must be the target's id or a UNIQUE column of "<target>" (a foreign key may only point at a primary key or unique column)` (uniqueness is satisfied by the target's `id`, a field with `unique: true`, or a single-column unique `indexes` entry — `columnsAreUniqueOnTarget`, `validator.go`)
- a type mismatch → `references "<col>" has an incompatible type: this field is "<type>" but <target>.<col> is "<targetType>"` (FK type classes from `pgKindForAPIType`: `string`/`text`/`json`→`text`, `int`→`integer`, `int64`→`bigint`, `float64`→`double`, `bool`→`bool`, `uuid`→`uuid`, `time`→`timestamptz`; `id` is `uuid`, `validator.go`)

**Generated read subroute.** For a relation field, the engine registers
`GET /api/{resource}/{id}/{relRoute}` returning the referenced record, where
`relRoute = strings.TrimSuffix(fieldName, "_id")` (`pkg/codegen/builder.go`).
So `customer_id` → `GET /api/orders/{id}/customer`. The JOIN follows the FK to the
`references` column (default `id`) (`builder.go`). A relation field whose
name does NOT end in `_id` keeps its full name in the route (e.g. a field
`customer_email` → `GET /api/orders/{id}/customer_email`; `manager_user_id` →
`…/manager_user`). The FK column is also auto-indexed (`idx_<table>_<field>`,
`desired.go`).

The subroute enforces the role's RBAC on the **referenced** resource (SEC-AUDIT-V1):
it requires `read` on the target (→ `403` otherwise), injects the target's row
condition (→ a hidden row is `404`), and applies the target's field allowlist — the
same scoping `GET /api/{target}` and the `?include=` embeds apply, so the subroute
can never expose a row or field the role could not otherwise read.

(Multi-column composite FKs use the resource-level `foreign_keys` block, not a
field key — covered in a later section. Declarative nested embeds use the
resource-level `relations` block — also a later section.)

Example relation field in context:

```json
"resources": {
  "customers": {
    "fields": {
      "email": { "type": "string", "unique": true, "format": "email" },
      "name":  { "type": "string", "required": true }
    }
  },
  "orders": {
    "fields": {
      "customer_id":    { "type": "uuid",   "relation": "customers", "on_delete": "restrict" },
      "customer_email": { "type": "string", "relation": "customers", "references": "email", "on_delete": "set_null" },
      "total":          { "type": "float64" }
    }
  }
}
```

### 3.5 Field-level `renamed_from` (data-preserving column rename)

`renamed_from` declares this column's PREVIOUS name so the migration engine emits
`ALTER TABLE … RENAME COLUMN <old> TO <new>` (metadata-only, data preserved, with
the column's index/FK/unique following it) instead of a drop+add that would strand
the data (`desired.go`, `RenamedFrom` wired onto the column).

```json
"full_name": { "type": "string", "renamed_from": "nombre_completo" }
```

Validation (`validator.go`):
- must be a valid identifier — else `invalid renamed_from "<old>": must match ^[a-z][a-z0-9_]*$`
- must differ from the field's own name — else `renamed_from must differ from the field's own name`
- cannot be `"id"` — else `cannot rename from "id" (the implicit primary key)`
- the old name must NOT still be a declared field of this resource — else `renamed_from "<old>" is still a declared field — you cannot rename from a name that still exists (remove the old field)`

Once the rename is applied the key is inert (the old column no longer exists, so a
re-provision is a no-op) and safe to leave in place. (A resource/table-level
`renamed_from` exists analogously at the resource level, `validator.go`; see
the resources section.)

### 3.6 What does not exist as a field type

`number` is NOT a type (use `int`, `int64`, or `float64`) — and there is no
`date`/`datetime`/`decimal`/`array`/`enum`-as-type/`relation`-as-type. The full set
is the ten values in §3.2; anything else is rejected at load with
`unknown field type "<type>"`.

### 3.7 Divergences recorded for findings

The findings below are documented behaviors that diverge from how they read
elsewhere; they are recorded, not fixed.

- **`json` fields accept the `eq` filter operator, not "none".** (Historical
  finding; since HOUSEKEEPING-S1 `json`/`jsonb` carry explicit
  `operatorsForType` entries — `eq` + `is_null` — so the unknown-type fallback
  no longer decides them and their rejection messages list the allowed set like
  every other type.) So `?filter[attributes][eq]=...` on a `json` field is
  accepted (compared as TEXT equality, since `json`→`TEXT`); any other op
  (`gt`, `partial`, …) is rejected naming the allowed set.

- **`unique` is single-column only; composite uniqueness is a separate code path.**
  `unique: true` produces exactly one single-column UNIQUE constraint named
  `<resource>_<field>_key` (`desired.go`). There is no field-level way to
  express composite uniqueness; that requires a resource-level `indexes` entry with
  `"unique": true`. The two keys are entirely separate.

- **The relation read-subroute strips only a trailing `_id`.** `relRoute =
  strings.TrimSuffix(fieldName, "_id")` (`builder.go`). For `customer_id` this
  yields `…/customer` as documented, but a relation field without an `_id` suffix
  keeps its full name verbatim (`customer` → `…/customer`; `manager_user_id` →
  `…/manager_user`). The doc phrasing "field name minus _id" is only correct when
  the field actually ends in `_id`.

- **A regular field's `default` is intentionally NOT emitted as a Postgres column
  DEFAULT.** `buildDesiredSchema` deliberately ignores a regular field's `default`
  when building the migration model (`desired.go`: "The field's `default` is
  DELIBERATELY IGNORED"). Defaults are an app-layer concern applied on create by the
  engine, not a DB-level `DEFAULT`. Consequence: the database column has no DEFAULT;
  the value only materialises through the engine's create path. A direct SQL insert
  bypassing the engine would not get the default. Only `auto` fields and the implicit
  `id` carry real DB defaults — `now()` and `gen_random_uuid()` respectively
  (`desired.go`).

---

## 4. Declarative field validation

Every field may carry declarative validation keys. They are checked **at two times**:

1. **Load time** (`schema.Validate` → `validateFieldRules`, `validateDefault`): the *definition* of each rule is checked. A malformed rule (wrong field type, bad regex, `min > max`, etc.) **rejects the whole schema** with a `ValidationError` (`<field-path>: <message>`); the engine refuses to boot / `appximo validate` fails. Nothing is silently ignored.
2. **Request time** (`ResourceValidator`, built once at load by `CompileRules`; executed by `ApplyDefaults` then `ValidateWrite`): an incoming write body is checked against the precompiled closures and a `422` is returned listing **every** failing field.

The rules below are all **optional**. A field that declares none produces no load errors and adds no runtime cost beyond the create-gate length check.

### 4.1 `required`

- **Syntax:** `"required": true` (any field type).
- **Semantics (`ValidateWrite` with `requireAll`):**
  - **POST (create)** and **PUT (full replace)** pass `requireAll = true`: every required, **non-`auto`** field must be **present AND non-null**. A missing key or an explicit `null` produces `{"field":"<f>","rule":"required","message":"is required"}`.
  - **PATCH** passes `requireAll = false`: required-ness is **not** enforced — only the fields actually present in the body are run through their per-field rules.
- `auto` fields are exempt (excluded from the required list in `CompileRules`: `if fd.Required && !fd.Auto`).
- **Interaction with `default`:** `ApplyDefaults` runs **before** the required check on create, so a required field that also declares a `default` is satisfied by the default when omitted; a required field **without** a default still 422s. (See §4.8.)
- Note: a `null` value is skipped by the per-field rules (`if v == nil { continue }` in `ValidateWrite`); null semantics belong only to `required` and the update path.

### 4.2 `enum`

- **Applies to:** intended for string values; the runtime check requires the value be a `string` (a non-string value fails with the enum message).
- **Syntax:** `"enum": ["open", "done"]`. The `enum` field is typed `[]string`, so the members are always JSON strings (a JSON number/boolean as a member fails to decode into the schema, not the validator).
- **Load rejection:** an enum key **present but empty** (`[]`) → `enum must not be empty`. NOTE: the load validator does **not** check that the declaring field is itself `string`/`text`-typed (see findings) — `enum` on an `int` field passes `validate` but every runtime value (a `float64`) fails the enum closure.
- **Runtime:** a value not in the set → `{"rule":"enum","message":"must be one of: <v1>, <v2>, …"}` (members joined with `, `). A non-string value yields the same `enum` error.

### 4.3 `min` / `max` (numeric bounds)

- **Applies to ONLY:** `int`, `int64`, `float64` (`numericTypes`). On any other type → load error `min/max only apply to numeric fields (int, int64, float64), not "<type>"`.
- **Syntax:** `"min": 0`, `"max": 100` (JSON numbers).
- **Load rejection:** if both are set and `min > max` → `min must be <= max`.
- **Runtime:** JSON decodes every number to `float64`, so a non-number value → `{"rule":"min"|"max","message":"must be a number"}`. Out of range → `must be >= <min>` / `must be <= <max>` (formatted with `%v`).

### 4.4 `minLength` / `maxLength`

- **Applies to ONLY:** `string`, `text` (`stringTypes`). Else load error `minLength/maxLength only apply to string/text fields, not "<type>"`.
- **Syntax:** `"minLength": 1`, `"maxLength": 200` (integers).
- **Counting:** measured in **runes**, not bytes — `utf8.RuneCountInString`. So `"é"` counts as 1.
- **Load rejection:** `minLength < 0` → `minLength must be >= 0`; `maxLength < 0` → `maxLength must be >= 0`; both set with `minLength > maxLength` → `minLength must be <= maxLength`.
- **Runtime:** a non-string value → `must be a string`; out of range → `must be at least <n> characters` / `must be at most <n> characters`.

### 4.5 `pattern`

- **Applies to ONLY:** `string`, `text`. Else load error `pattern only applies to string/text fields, not "<type>"`.
- **Engine:** Go `regexp` — **RE2** (linear-time, no catastrophic backtracking). The pattern is **not implicitly anchored**; `re.MatchString` matches a substring unless you anchor with `^…$`.
- **Length cap:** `MaxPatternLength = 200` source characters. Over the cap → load error `pattern is <n> chars; max is 200`.
- **Load rejection:** a pattern that fails `regexp.Compile` → `invalid pattern: <err>`.
- **Fail-closed safety net:** `CompileRules` re-compiles the pattern; if it is invalid OR over 200 chars (only reachable if `Validate` was skipped), the field gets a rule that **rejects every value** with `{"rule":"pattern","message":"schema declares an invalid pattern for this field"}` — never a silently dropped rule.
- **Runtime:** no match → `must match pattern <pattern>`; non-string → `must be a string`.

### 4.6 `format`

- **Applies to ONLY:** `string`, `text`. Else load error `format only applies to string/text fields, not "<type>"`.
- **Closed set (exactly these four — `validFormats`):** `email`, `uuid`, `url`, `date`. Any other value → load error `unknown format "<v>": must be one of email, uuid, url, date`.
- **Syntax:** `"format": "email"`.
- **Predicates (`formatChecker`) and exact runtime messages:**
  - **`email`** — a **pragmatic anchored regex** `local@domain.tld`, NOT a full RFC 5322 parser. Pattern: `^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$` — requires at least one dot in the domain (a TLD). Message: `must be a valid email`.
  - **`uuid`** — passes iff `uuid.Parse(s)` succeeds. Message: `must be a valid UUID`.
  - **`url`** — passes iff `url.Parse` succeeds AND the scheme is exactly `http` or `https` AND the host is non-empty. Message: `must be a valid http(s) URL`.
  - **`date`** — passes iff the value parses as **RFC 3339** (`2006-01-02T15:04:05Z07:00`) **OR** as `YYYY-MM-DD` (`2006-01-02`). Message: `must be an ISO-8601 date (YYYY-MM-DD or RFC 3339)`.
  - Any format value: a non-string runtime value → `must be a string`.

### 4.7 `state_machine`

Covered in detail in §3 (field types / state machines). For validation completeness, at the declarative-rule layer (`checkKnownStates` / `ValidateInitialStates`) a present state field must be a **string** member of the declared lifecycle on every write (`unknown state "X"`), and on **create** must be one of the `initial` states (`cannot be created in state "X" (must be an initial state)`); these arrive in the same `422` `fields[]` shape with `"rule":"state"`. A non-string value on a state field is reported by `checkKnownStates` as `{"rule":"state","message":"must be a string"}`. The load validator (`validateStateMachine`) additionally requires the field be `string`/`text`, at least one `initial` state, each `initial` non-empty, enum coherence when an `enum` is declared, and a string `default` that is an initial state (`default "<v>" must be one of the state_machine initial states`).

### 4.8 `default`

- **Syntax:** `"default": <value>` — a literal of the field's type (see per-type rules below).
- **Applies on CREATE only**, and only when the key is **ABSENT** from the body (`ApplyDefaults`): a key that is present — **even an explicit `null`** — is left exactly as the caller sent it (matching SQL `DEFAULT`, which fills only an omitted column). `PUT` (full replace) writes an omitted optional field as `NULL`; defaults are **not** applied on PUT/PATCH (`ApplyDefaults` is invoked only on the create path).
- **Ordering on create:** `ApplyDefaults` runs **before** `ValidateWrite`, so a default can satisfy a `required` field (see §4.1).
- **Dynamic value:** on a `time` field the value `"now"` (case-insensitive, `isNowDefault` via `strings.EqualFold`) is the one dynamic default — resolved at insert to `time.Now().UTC()`. Any other string on a `time` field is treated as a literal.
- **Load rejection — `auto` fields:** a `default` on an `auto` field → `default cannot be set on an auto field`. (At compile time `CompileRules` also skips a default on an auto field — `if fd.Default != nil && !fd.Auto`.)
- **Load rejection — `enum` fields:** the default must be a **string** that is a declared enum member, else `default must be a string matching one of the enum values` or `default "<v>" is not one of the enum values`. (This enum check takes precedence over the per-type checks.)
- **Load rejection — per type (`validateDefault`):**

  | Field type | Accepted default | Reject message |
  |---|---|---|
  | `string`, `text` | a JSON string | `default must be a string` |
  | `int`, `int64` | an **integral** `float64` (JSON number with no fraction; `f == float64(int64(f))`) | `default must be an integer` |
  | `float64` | any JSON number | `default must be a number` |
  | `bool` | a JSON boolean | `default must be a boolean` |
  | `uuid` | a string that `uuid.Parse` accepts | `default must be a uuid string` / `default must be a valid uuid string` |
  | `time` | a string (RFC3339 literal, or `"now"`) | `default must be a string (an RFC3339 timestamp, or "now" for the insert moment)` |
  | `json` | **any** JSON value (no check) | — |

  NOTE: `validateDefault` only type-checks the default; it is **not** cross-checked against `min`/`max`/`minLength`/`maxLength`/`pattern`/`format`. A contradictory default (e.g. `default: ""` with `minLength: 1`) loads cleanly but, being applied before `ValidateWrite`, makes every omitted-field create 422 (see findings).

### 4.9 The 422 response shape

A failing write returns **HTTP 422** (`http.StatusUnprocessableEntity`) with this exact body (`writeValidationErrs`), where `fields` is the JSON array of `FieldRuleError` `{field, rule, message}` and **every** failing field is listed at once (`ValidateWrite` accumulates all violations, deterministically ordered — required fields first in sorted order, then per-field rules over sorted body keys, then state checks):

```json
{
  "error": "validation_failed",
  "fields": [
    { "field": "title",  "rule": "required",  "message": "is required" },
    { "field": "email",  "rule": "format",    "message": "must be a valid email" },
    { "field": "status", "rule": "enum",      "message": "must be one of: open, done" }
  ]
}
```

GraphQL surfaces the identical engine and field list under `errors[].extensions.fields` (HTTP 200). The atomic `POST /api/transaction` path runs the same `ValidateWrite` per op and reports `validation_failed` with the failing op index. Writing a field the resource does not declare is a related but distinct error surfaced by the DB-error mapper as `422` with `"rule":"unknown_field"` (the Postgres `42703` is mapped, not the declarative validator).

### 4.10 Worked example — several rules on several fields

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "contacts-api",
  "resources": {
    "contacts": {
      "fields": {
        "name":     { "type": "string", "required": true, "minLength": 1, "maxLength": 120 },
        "email":    { "type": "string", "required": true, "format": "email" },
        "website":  { "type": "string", "format": "url" },
        "handle":   { "type": "string", "pattern": "^@[a-z0-9_]{1,15}$" },
        "age":      { "type": "int", "min": 0, "max": 130 },
        "score":    { "type": "float64", "min": 0, "default": 0 },
        "status":   { "type": "string", "enum": ["lead", "active", "archived"], "default": "lead" },
        "ref":      { "type": "uuid", "default": "00000000-0000-0000-0000-000000000000" },
        "added_at": { "type": "time", "default": "now" },
        "verified": { "type": "bool", "default": false },
        "created_at": { "type": "time", "auto": true }
      }
    }
  }
}
```

On `POST /api/contacts` with body `{"name":"","email":"nope","age":200,"handle":"BadHandle"}` the response is `422 validation_failed` listing: `age` (`max` → "must be <= 130"), `email` (`format` → "must be a valid email"), `handle` (`pattern` → "must match pattern ^@[a-z0-9_]{1,15}$"), `name` (`minLength` → "must be at least 1 characters") — per-field rules are emitted over the body's **sorted** keys. The omitted `score`/`status`/`ref`/`added_at`/`verified` are filled from their defaults; `created_at` is engine-managed.

---

## 5. State machines

A `string`/`text` field can declare a `state_machine` to turn a status column into an enforced lifecycle: the engine controls which states a row may be **created** in and which moves are permitted between states. Without `state_machine` a string field is a free label (unchanged); with it, the rules below are forced on every write path (REST, GraphQL, and `POST /api/transaction`).

> **The database itself carries no CHECK constraint** (field report B3, by
> design): enums and state machines live in the engine, at the API layer — a
> CHECK would make every lifecycle evolution a constraint migration. The
> consequence to know: any write that bypasses the API (a manual SQL
> migration, an import script, a seeding job) can store values the API would
> never accept, and nothing detects them afterwards. Route bulk writes
> through the API (`POST /api/transaction`, or a custom route using
> `Ctx.Insert`/`Ctx.Update`, which enforce the machine) — or re-validate by
> hand after direct SQL.

The declaration is a field-level key (`pkg/schema/types.go`, `FieldDef.StateMachine *StateMachine`):

```json
"status": {
  "type": "string",
  "enum": ["pending", "paid", "shipped", "delivered", "cancelled"],
  "default": "pending",
  "state_machine": {
    "initial": "pending",
    "transitions": {
      "pending":   ["paid", "cancelled"],
      "paid":      ["shipped"],
      "shipped":   ["delivered"],
      "delivered": [],
      "cancelled": []
    }
  }
}
```

### Syntax

The `state_machine` object has exactly two keys — `initial` and `transitions` — and they are **strict-checked** at load (`pkg/schema/keys.go`: `addUnknown(... ".state_machine", sm, "initial", "transitions")`). Any other key inside `state_machine` rejects the schema. The *user state names* under `transitions` are free-form (they are not strict-checked).

- **`initial`** — the state(s) a row may be **created** in. It accepts **either a single string OR an array of strings**; `StateMachine.UnmarshalJSON` (`pkg/schema/types.go`) normalizes both to a slice, so `"initial": "pending"` and `"initial": ["pending"]` are equivalent. Internally it is always `[]string`.
- **`transitions`** — a map from each state to the list of states it may move **to**. A state whose outgoing list is `[]` — **or that is absent from the map entirely** — has no outgoing transition and is therefore **terminal / immutable** (it can never change to another state).

Three derived helpers drive enforcement (all in `pkg/schema/types.go`):

- `KnownStates()` — the union of every state the machine references: all `initial` states, every transition **source** key, and every transition **target**. This is the universe of valid values for the field.
- `IsInitial(s)` — true if `s` appears in `initial`.
- `OriginsOf(target)` — the reverse of `transitions`: the (sorted) set of states from which a transition INTO `target` is declared. Used to build the race-safe UPDATE guard (below). Returns empty if no state transitions into `target`.

### Load-time validation (`validateStateMachine`, `pkg/schema/validator.go`)

A `state_machine` is checked at schema load; a bad lifecycle is rejected cleanly (never a runtime surprise). The exact rules and messages:

1. **String/text fields only.** If the field's `type` is not `string` or `text`, the schema is rejected with `state_machine only applies to string/text fields, not "<type>"` (and no further state checks run). The valid types are exactly those in `stringTypes = {"string","text"}`.
2. **At least one initial state.** If `initial` is empty (`len(sm.Initial) == 0`), rejected with `state_machine requires at least one initial state` (field path `…state_machine.initial`).
3. **Non-empty initial strings.** Any `initial` entry that is the empty string `""` is rejected with `initial state must be a non-empty string`.
4. **Enum coherence (only when `enum` is declared).** If the field also declares `enum`, **every** state named anywhere in the machine (`KnownStates()`) must be a member of `enum`. A state outside the enum is rejected with `state "<state>" is not one of the field's enum values` (checked in sorted order, so all offenders are reportable). If the field declares no `enum`, this check is skipped — any state names are allowed.
5. **Default must be initial.** If the field declares a `string` `default`, it must be one of the `initial` states (`IsInitial`), else rejected with `default "<value>" must be one of the state_machine initial states` (field path `…default`). (A non-string default never reaches this check; a string default that is not initial is the failure.)

### Runtime enforcement

The compiled form (`compiledSM{initial, known}` in `pkg/schema/rules.go`) holds two sets per state-machine field: the `initial` set and the `known` set. A resource with no state machines has `rv.states == nil` and pays zero cost on every check below.

**Known-state check — runs on BOTH create and update** (`checkKnownStates`, called from `ValidateWrite` in `pkg/schema/rules.go`). For every state-machine field *present* in the body:
- a non-string value → `FieldRuleError{Rule:"state", Message:"must be a string"}`;
- a string that is not in `known` → `FieldRuleError{Rule:"state", Message:'unknown state "<s>"'}`.

These surface as the standard **422** declarative-validation response (`writeValidationErrs` → `{"error":"validation_failed","fields":[{"field":"status","rule":"state","message":"…"}]}`). An absent or `null` field is skipped (a PATCH that doesn't touch the status is unaffected).

**Create — must be an INITIAL state** (`ValidateInitialStates`, `pkg/schema/rules.go`). Called on the create path only, after `ValidateWrite`. For every state-machine field present with a string value that is `known` but **not** `initial`, it returns `FieldRuleError{Rule:"state", Message:'cannot be created in state "<s>" (must be an initial state)'}` → **422**. So you cannot create a row already advanced in its lifecycle (e.g. create an order directly as `shipped`). This is wired on all three create paths: REST POST (`pkg/codegen/builder.go`), GraphQL `create<Singular>` (`pkg/graphql/handler.go`), and a `create` op inside a transaction (`pkg/codegen/transaction.go`).

**Update — only along a DECLARED transition, RACE-SAFE.** The transition is **not** enforced with a read-then-write; it is compiled directly into the UPDATE's `WHERE` clause by `appendStateTransitionGuard` (`pkg/codegen/builder.go`). For each state-machine field being SET to `newState`, it appends:

```sql
AND (<col> = $n OR <col> = ANY($n+1))
```

binding `$n = newState` and `$n+1 = OriginsOf(newState)` (the allowed origin states; bound as `'{}'` empty `text[]`, never NULL, when there are none). Both the new value and the origin set are always bound parameters — never interpolated.

Consequences of this single WHERE guard:
- The row matches (and updates) **only if its current state already equals the new value** (a no-op self-set — so a full-object PUT/PATCH that re-sends the unchanged status still succeeds) **OR is a state from which `newState` is a declared transition**.
- A **terminal** state (`[]` / absent) is its own special case: `OriginsOf` of any *other* target won't include it, so no move out of it is ever permitted — it is immutable (append-only; e.g. a fintech `posted` entry).
- It is **race-safe**: two concurrent updates can't both advance the same row — the move is permitted by the row's CURRENT state inside the atomic UPDATE, so one wins and the other matches **zero rows**. No read-modify-write window.
- The guard appends **no clause** for an update touching no state-machine field, so a resource without one is byte-identical (no overhead).

When the guarded UPDATE matches **zero rows**, `ExplainTransitionFailure` (`pkg/codegen/builder.go`) runs on the error path only (it is the sole place a current-state read happens): it reads the row's current state(s) and returns **422** `invalid transition for "<field>": from "<current>" to "<wanted>" is not allowed`, or **404** `not found` if the row vanished (a race), or **409** `the resource changed during the update; retry` if every state field already equalled its target (a concurrent change after the existence check). This is enforced on REST PUT/PATCH (`builder.go`), GraphQL `update<Singular>` (`pkg/graphql/handler.go`), and an `update` op inside a transaction (`pkg/codegen/transaction.go`). A transition violation inside `POST /api/transaction` fails the WHOLE batch (all-or-nothing).

### Out of scope (honest limits)

- **No per-transition RBAC.** The engine validates transitions *structurally* (which moves are legal); it does **not** support "only role X may move to `shipped`". WHO may update is governed by the normal `update` RBAC (the role's per-resource grant / row condition — see §7 (RBAC)); the state machine only constrains WHICH moves are legal once the caller is authorized to update.
- A `state_machine` is a lifecycle, not arbitrary computation — there is no in-place value math; a transition is a value rewrite from one declared state to another.

### Complete example — order lifecycle

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "orders-api",
  "resources": {
    "orders": {
      "fields": {
        "customer_id": { "type": "uuid", "required": true },
        "total":       { "type": "float64", "required": true, "min": 0 },
        "status": {
          "type": "string",
          "enum": ["pending", "paid", "shipped", "delivered", "cancelled"],
          "default": "pending",
          "state_machine": {
            "initial": ["pending"],
            "transitions": {
              "pending":   ["paid", "cancelled"],
              "paid":      ["shipped", "cancelled"],
              "shipped":   ["delivered"],
              "delivered": [],
              "cancelled": []
            }
          }
        }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] }
    }
  }
}
```

With this schema: a `POST /api/orders` may only create with `status` `pending` (or omit it, taking the `pending` default); creating directly as `shipped` → 422 `cannot be created in state "shipped" (must be an initial state)`. A `PATCH` from `pending`→`shipped` → 422 `invalid transition for "status": from "pending" to "shipped" is not allowed` (only `paid` or `cancelled` are reachable from `pending`). A `PATCH` of a `delivered` or `cancelled` order to any other state → 422 (both are terminal). Re-sending `"status":"pending"` on a still-pending order is an accepted no-op. Any value outside the enum/known set → 422 `unknown state "…"`.

---

## 6. Declarative relations (nested ?include= reads)

This block enables nested reads — embedding related records inside a parent record in a single round-trip when the caller opts in with `?include=`. It is declared per resource as a `relations` map (sibling of `fields`), and is compiled into SQL once at boot (never inferred from a FK catalog, never per request).

### 6.1 Critical distinction: field-level `relation` vs resource-level `relations`

These are two **independent** features that are easy to confuse — you can declare either one without the other.

| | Field-level `"relation"` key (see §3 Relations) | Resource-level `"relations"` block (this section) |
|---|---|---|
| Where it lives | inside a single field's definition | a `relations` map at the resource level |
| What it produces | a **real Postgres FOREIGN KEY** on the column + a read **subroute** (`GET /api/{res}/{id}/{field-minus-_id}`) | **nested read embeds** served on opt-in `?include=` (`json_agg` + `LEFT JOIN LATERAL`) |
| Physical schema effect | creates/validates an actual FK constraint | none — purely a read overlay (it only declares how to JOIN at read time) |
| Triggered by | always present (the FK is materialized at tenant registration) | only when the client sends `?include=` |

A `relations` entry does **not** create any FK and does **not** require a field-level `relation` to exist; it only says how to join two tables for a nested read. Conversely, a field-level `relation` does not by itself enable `?include=`. To both enforce referential integrity AND offer nested embeds, declare both.

### 6.2 The three relation types and where the FK lives

`type` is a closed set (`validRelationTypes`): exactly `has_many`, `belongs_to`, or `many_to_many`. The FK location differs by type (verified in `pkg/query/relations.go` `buildEmbed` and `pkg/migration/runner.go` `relationIndexTargets`):

- **`has_many`** — parent → many children. The FK lives on the **TARGET (child)** table. Join: `child.<fk> = parent.id`. The embed returns a JSON **array** of up to `limit` children, ordered by the child's `id`.
- **`belongs_to`** — child → its one parent. The FK lives on **THIS (source)** table. Join: `target.id = source.<fk>`. The embed returns a single JSON **object** (or `null` when the FK is null / no row matches).
- **`many_to_many`** — both sides via a junction (`through`) table. The junction holds `fk` (THIS side's id) and `target_fk` (the TARGET's id). Joins: `through.<fk> = parent.id` and `target.id = through.<target_fk>`. The embed returns a JSON **array** of up to `limit` targets, ordered by target `id`.

### 6.3 `RelationDef` keys (strict-validated)

Keys are strict — only these are accepted (`pkg/schema/keys.go`: `"type", "target", "fk", "through", "target_fk", "limit"`); any other key rejects the schema.

| Key | Required | Applies to | Meaning |
|---|---|---|---|
| `type` | yes | all | `has_many` \| `belongs_to` \| `many_to_many` |
| `target` | yes | all | the related resource name (must exist in the schema) |
| `fk` | yes | all | the FK column name (which table per §6.2) |
| `through` | **m2m only** | `many_to_many` | the junction table name |
| `target_fk` | **m2m only** | `many_to_many` | the target's FK column inside the junction table |
| `limit` | no | all | top-N children per parent; `0`/omitted → **`DefaultEmbedLimit = 50`**; must be `>= 0` |

`through` and `target_fk` on a non-m2m type are **rejected** (see §6.6). The relation's map key (its name) is what the caller types in `?include=` and what GraphQL exposes as a nested field.

### 6.4 Using `?include=`

- Request embeds with a comma list: `?include=lines,customer`.
- Nest with a dot: `?include=lines.product` (embed `product` inside each `lines` child).
- **Max nesting depth is `DefaultMaxIncludeDepth = 2`** (e.g. `lines.product` is depth 2). A deeper path → **400** `"include nesting exceeds max depth 2"`. (`pkg/codegen/builder.go` and `pkg/graphql/handler.go` both pass `schema.DefaultMaxIncludeDepth` at the call site.)
- An additional fan-out guard: at most **25** total embeds across one request (`maxIncludeNodes`) → 400 `"too many includes"`.
- An unknown embed name on the resource → **400** `"unknown relation <name> on <resource>"`.
- **Opt-in and zero-cost when unused.** WITHOUT `?include=` the request never reaches the include compiler — the plain list/get SQL is byte-identical to before.
- **GraphQL** exposes the same relations as nested fields on the generated types, backed by the **same single LATERAL query** (no dataloader).

### 6.5 RBAC and auto-indexing of embeds

- **RBAC is compiled into the embed SQL.** A relation fragment is emitted only if the role may `read` the target. Asking to `include` a target the role cannot read → **403** `"forbidden: role may not read <target> via include"`.
- The target's **field allowlist** scopes the embedded `json_build_object` (only permitted columns are emitted; `id` is always included).
- The target's **row-level condition** is injected into the embed's `WHERE` (`AND child.<cond_field> = $N`), so an embed can never return a child the role may not see.
- **Auto FK index.** Every declared relation's FK column gets a btree index at tenant registration, on the side where the FK lives: `has_many` → index on `target.<fk>`; `belongs_to` → index on `this_resource.<fk>`; `many_to_many` → indexes on `through.<fk>` AND `through.<target_fk>` (`relationIndexTargets`). The embed is therefore an index lookup, not a per-parent seq scan.

### 6.6 Validation rules (`validateRelations`, `pkg/schema/validator.go`)

Each rule and its exact rejection message:

- **Relation name** must match `^[a-z][a-z0-9_]*$` (the field-name regex `fieldNameRe`), else: `invalid relation name "<n>": must match ^[a-z][a-z0-9_]*$`.
- **No clash with a field** of the same resource: `relation name "<n>" collides with a field of the same name`.
- **`type`** must be in the closed set, else: `unknown relation type "<t>": must be one of has_many, belongs_to, many_to_many`.
- **`target`** must be an existing resource, else: `relation target "<t>" references unknown resource`.
- **`fk`** must be a valid field name (`^[a-z][a-z0-9_]*$`, non-empty), else: `relation fk "<fk>" must be a valid field name (^[a-z][a-z0-9_]*$)`.
- **`limit`** must be `>= 0`, else: `relation limit must be >= 0`.
- For **`many_to_many`**: `through` must be non-empty and match `throughNameRe` = `^[a-z][a-z0-9_\-]*$` (else `many_to_many requires a valid through (junction table) name`), and `target_fk` must be a valid field name (else `many_to_many requires a valid target_fk column name`).
- For **non-m2m types**: `through` set → `through only applies to many_to_many, not "<type>"`; `target_fk` set → `target_fk only applies to many_to_many, not "<type>"`.

> Note the asymmetry: `through` accepts a **hyphen** (`throughNameRe` = `^[a-z][a-z0-9_\-]*$`) whereas every other identifier (resource names, field names, `fk`, `target_fk`, the relation name) forbids it. This is deliberate — a junction may be a bare join table whose name carries a hyphen; the SQL layer double-quotes it (`quoteIdent`).

Note also: `validateRelations` is **structural only** — it does NOT verify that the `fk` / `target_fk` columns actually exist (it only checks that `target` names a declared resource). Column existence is checked against `information_schema` at tenant migration time (a logged warning, never a hard failure), since columns can be added to the live table at runtime.

### 6.7 Examples

#### `belongs_to` (FK on this/source table)

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "shop",
  "resources": {
    "customers": {
      "fields": { "name": { "type": "string", "required": true } }
    },
    "orders": {
      "fields": {
        "status": { "type": "string" },
        "customer_id": { "type": "uuid", "relation": "customers" }
      },
      "relations": {
        "customer": { "type": "belongs_to", "target": "customers", "fk": "customer_id" }
      }
    }
  }
}
```

`GET /api/orders?include=customer` → each order carries a nested `customer` object (joined on `customers.id = orders.customer_id`). The field-level `"relation": "customers"` also creates the real FK; the `relations` block adds the embed.

#### `has_many` (FK on the target/child table)

```json
"orders": {
  "fields": { "status": { "type": "string" } },
  "relations": {
    "lines": { "type": "has_many", "target": "order_lines", "fk": "order_id", "limit": 100 }
  }
},
"order_lines": {
  "fields": {
    "order_id": { "type": "uuid", "relation": "orders" },
    "qty": { "type": "int" }
  }
}
```

`GET /api/orders/{id}?include=lines` → the order carries a `lines` array (joined on `order_lines.order_id = orders.id`), capped at `limit` (100 here; default would be 50). Nested: `?include=lines.product` (depth 2).

#### `many_to_many` (through a junction table)

```json
"products": {
  "fields": { "name": { "type": "string" } },
  "relations": {
    "orders": {
      "type": "many_to_many",
      "target": "orders",
      "through": "order_products",
      "fk": "product_id",
      "target_fk": "order_id"
    }
  }
}
```

`GET /api/products?include=orders` → each product carries an `orders` array, joined as `order_products.product_id = products.id` then `orders.id = order_products.order_id`. The junction `order_products` gets auto-indexes on both `product_id` and `order_id`.

---

## 7. RBAC — roles, permissions, conditions

The `rbac` block declares the authorization policy. It is the schema's only access-control surface — there is no separate code path. Every request (REST and GraphQL, read or write, including aggregates and relation embeds) funnels through one call, `rbac.Policy.Evaluate(evalCtx, resource, action)` (`pkg/rbac/evaluator.go`), so a rule stated here applies uniformly to all of them.

### 7.1 Shape and key sets

`rbac` is an object with exactly one valid key: `roles` (`pkg/schema/keys.go`). `roles` is a map from role name → role policy. The JWT `role` claim selects which policy applies; an unknown role is denied (`Evaluate` returns `Allowed:false` when the role is absent — `pkg/rbac/evaluator.go`).

A role is expressed in **one of two mutually-exclusive forms**, plus an
independent `routes` block (below) that either form may carry.

**Role-global (legacy) form** — keys (strict-checked, `pkg/schema/keys.go`): `resources`, `actions`, `conditions`, `fields`. The single `conditions` and `fields` apply to **every** resource the role lists.

**Per-resource form** — one key: `permissions`, a map from resource name → grant. Each grant has the strict key set `actions`, `conditions`, `condition_actions`, `fields` (`pkg/schema/keys.go`). Each resource is scoped by its **own** condition and field allowlist.

**`routes` (custom-route grants, LIBRARY-GAPS-S1)** — a map from custom-route
SEGMENT → `{ "actions": [...] }`, orthogonal to both forms (see §7.9).

A nested `conditions` object (in either form) has exactly the keys `field`, `op`, `val` (`pkg/schema/keys.go`).

Any key outside these sets rejects the schema with `unknown key "<k>" (valid keys: …)`.

### 7.2 Actions

The valid action set is closed (`validRBACActions`, `pkg/schema/validator.go`):

```
read | create | update | delete | *
```

`*` grants all actions. Matching is by `actionAllowed` (`pkg/rbac/policy.go`): an action is allowed if the list contains `"*"` or the exact action string.

### 7.3 `resources` (role-global form only)

`resources` is a `json.RawMessage` that may be **either** the string `"*"` **or** an array of resource names (`pkg/rbac/policy.go`):

- `"*"` → wildcard, grants every resource.
- `["tasks","projects"]` → only the listed resources.

A resource not listed (and not under a `"*"`) is denied. The parse is memoized per distinct raw value.

### 7.4 Conditions (row-level filtering) — shape and the equality-only rule

A `conditions` object is `{ "field", "op", "val" }` (`pkg/schema/types.go`).

- `field` — the column the row is filtered on (must be a real column; see §7.6).
- `val` — resolved by `resolveVar` (`pkg/rbac/evaluator.go`) to one of:
  - `$user_id` → the JWT subject (`EvalContext.UserID`),
  - `$external_client_id` → `EvalContext.ExternalClientID`,
  - anything else → used **as a literal** (e.g. `"published"` for a public-read role).
  - **Any OTHER `$…` val is rejected at load** (ENG-20, `validateConditionVal`):
    `$userid`, `$tenant_id` etc. would be compared as the literal dollar-prefixed
    text — matching zero rows forever with no error at any layer — so a `$`
    announces intent and must resolve. A bare `user_id` (the `$` forgotten) stays
    legal as a literal but raises the `bare_condition_variable` WARNING.
- `op` — **must be `"eq"` (or omitted).** Row conditions are enforced as equality; a non-eq operator is **rejected at load** (SEC-AUDIT-V1) so the schema can only declare what the engine applies. See §7.7.

When a condition applies, a row excluded by it reads as **404, not 403** (the row simply isn't in the result set / matches no row).

### 7.5 `condition_actions` (per-resource form only — "read all, write own")

`condition_actions` scopes the condition to a **subset** of the granted actions (`conditionAppliesToAction`, `pkg/rbac/evaluator.go`):

- **empty / omitted** → the condition applies to **all** granted actions (the safe default, most restrictive).
- a non-empty list → the condition applies only to the listed actions; actions not listed are **unconditional**.

This enables "read all, write own": grant `["read","create","update","delete"]` with a condition gated by `condition_actions: ["create","update","delete"]` — reads are unrestricted, writes are owner-scoped.

### 7.6 Validation (`validateRBAC`, `pkg/schema/validator.go`)

**Both forms are validated** (SEC-AUDIT-V1). Every condition — role-global or
per-resource — must use the eq-only operator (`validateConditionOp`): a non-`eq`,
non-empty `op` is rejected with
`unsupported RBAC condition operator "<op>": only equality ("eq") is enforced for row conditions — a non-eq operator would be silently ignored, so it may not be declared`.

For the **role-global form** (`validateRoleGlobal`), a role that carries a condition
or a `fields` allowlist is checked against the resources it applies to (a wildcard
admin with neither is unaffected):

- **Condition field exists on EVERY applicable resource** (tightened in
  LIBRARY-GAPS-S1), else
  `condition field "<f>" does not exist on '<res1>', '<res2>' — a role-global condition is applied to EVERY resource the role lists, so a resource without the column fails at request time`
  (empty → `condition field is required`). This is the fail-closed fix for a real
  latent bug: the condition IS injected into the WHERE of every listed resource, so
  a column present on only some of them produced a schema that validated and then
  broke the first time another resource was queried. The `Fix` points at the shape
  that expresses the intent — per-resource `permissions`, where each resource is
  scoped by its own column. Names in the role's `resources` list that are not
  declared resources are skipped: a role-global list may legitimately carry a
  VIRTUAL custom-route segment (the pre-`routes` way of granting one).
- **Allowlist fields exist** on at least one applicable resource, else
  `field "<f>" does not exist on any of the role's resources`. The allowlist keeps
  the **union** rule deliberately: `fields` is a projection filter, so a field
  missing on one resource simply projects nothing there (fail-closed already), and
  requiring every field on every resource would break the legitimate "one allowlist
  across several shapes" use.

For a role that declares `permissions` (per-resource form), the following are
enforced (exact messages):

- **Mutual exclusivity.** If the role ALSO has any of `resources` / `actions` / `conditions` / `fields` (`validator.go`):
  `"a role uses EITHER the role-global form (resources/actions/conditions/fields) OR per-resource permissions, not both — move the role-global keys into permissions entries"`
- **Resource must exist.** A permission over an unknown resource:
  `permission references unknown resource "<name>"`
- **Actions non-empty:** `at least one action is required (read, create, update, delete, or *)`
- **Actions known:** `unknown action "<a>": must be one of read, create, update, delete, *`
- **Condition field present + exists on that resource** (`rbacFieldExists` accepts the implicit `id` plus any declared field — `validator.go`):
  - empty: `condition field is required`
  - not a column: `condition field "<f>" does not exist on resource "<res>"`
- **`condition_actions` rules:**
  - requires a condition: `condition_actions requires conditions to be set`
  - must be a concrete action (`"*"` rejected): `invalid condition action "<a>": must be a concrete action (read, create, update, delete)`
  - must be a subset of `actions` (unless `actions` contains `"*"`): `condition_actions lists "<a>" which is not in actions`
- **`fields` must name real columns** (per-resource form): `field "<f>" does not exist on resource "<res>"`
- **Condition `op` must be eq** (both forms): see `validateConditionOp` above.

### 7.7 Row conditions are equality-only (`op` must be `eq`)

A row condition is always enforced as a bare equality `field = $n`:

- single-row get/delete path — `AppendRowCondition` appends `" AND %s = $%d"` (`pkg/query/builder.go`).
- list / aggregate path — `appendConditions` appends `"%s = $%d"`.
- the relation-embed and subroute paths likewise emit `<alias>.<field> = $n`.

Because the operator is never anything but equality, the schema may **only declare
`op: "eq"`** (or omit it): a non-eq operator is **rejected at load** (SEC-AUDIT-V1,
§7.6), so "declared == applied" — a condition can never silently behave differently
from what it states. (Before SEC-AUDIT-V1 a non-eq `op` loaded clean and was then
ignored at runtime; that is now impossible.)

This is distinct from the transaction `guard` mechanism (§10), a different feature
that genuinely supports `eq | ne | gt | gte | lt | lte` (with type-aware binding).
Richer RBAC row operators would require the same binding and are a future increment.

### 7.8 Enforcement across operations (read, create, update, delete, aggregate, embeds)

`Evaluate` returns the condition + field allowlist **of the requested resource** (legacy: the role-global ones; per-resource: the matched entry's own — `pkg/rbac/evaluator.go`). All operations on both REST and GraphQL go through it, so the scope is consistent.

- **`fields`** is a response allowlist: only the listed columns are returned (`AllowedFields`).
- **Conditions on reads/updates/deletes/aggregates** filter rows (equality, §7.7); an aggregate is scoped to the same row set, so totals never leak across principals.
- **Conditions and `fields` are enforced on CREATE too** (mass-assignment block, `EnforceCreateRBAC`, `pkg/codegen/builder.go`, shared by REST POST and GraphQL `create…`):
  - any body field outside `AllowedFields` is **dropped** (only when an allowlist is set — `len(AllowedFields) > 0`), except the condition field, which is preserved for forcing — `builder.go`.
  - the condition field is **forced** to the caller's resolved value; if the body supplies a *different* non-null value for it, the create is **rejected with 403** `field "<f>" must match the authenticated principal` (`builder.go`). So an owner-scoped role can only create rows attributed to itself.
- **Relation embeds** (`?include=`) are governed by the target's `read` permission, field allowlist, and condition — the same `Evaluate` result.

### 7.9 `routes` — granting CUSTOM routes (LIBRARY-GAPS-S1)

A **custom route** is an endpoint a Go backend registers with `appximo.Route`
(the library model, ADR-016) — it has no table. The middleware authorizes it by its
**first `/api/` segment**, treated as a VIRTUAL resource, with the action derived
from the HTTP method (`GET`→read, `POST`→create, `PUT`/`PATCH`→update,
`DELETE`→delete): `POST /api/checkout` is "create on `checkout`".

`routes` is how a role grants one:

```json
"customer": {
  "permissions": {
    "orders": { "actions": ["read"],
                "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
  },
  "routes": { "checkout": { "actions": ["create"] } }
}
```

**It is orthogonal to `resources`/`permissions`** — a different namespace
(registered endpoints, not tables) — so a role may declare it alongside either
form. That is the point: before this key, a role using per-resource `permissions`
could not reach ANY custom route (every `permissions` key is validated against a
real resource), which made "owner-scoped end users + a custom action endpoint"
inexpressible. See [ADR-021](adr/ADR-021-custom-route-authorization.md).

Key set: a grant has **exactly one** key, `actions` (`pkg/schema/keys.go`).

**No `conditions`, no `fields`** — a virtual segment has no rows to filter and no
columns to project. Declaring either is a load error that explains why, rather than
a silently ignored key. The DATA a handler touches is authorized separately:
`Ctx.Query`/`Insert`/`Update` re-evaluate the role against the **real** resources,
condition and allowlist included.

**Two validation layers:**

| Layer | Where | Checks |
|---|---|---|
| schema | `validateRouteGrants`, `pkg/schema/validator.go` | segment matches `^[a-z][a-z0-9_\-]*$` (hyphens allowed — a route path is a URL, not a GraphQL identifier); at least one known action; the segment is **not** a declared resource (`"<x>" is a declared resource, not a custom route`) |
| boot | `validateRouteGrants`, `route.go` (in `Start`, and on `POST /admin/engine/schema`) | the segment is **registered** by this binary, and every concrete action has a registered method — else the boot fails, listing the registered segments |

The boot layer is what a standalone schema cannot check: `appximo validate`,
Studio and the AI loop see no Go program. Consequence: the pure `appximo serve`
binary registers no custom routes, so it **refuses to boot** a schema with a
`routes` grant — deliberately, since such a policy could never match.

**Evaluation** (`routeGrantFor`, `pkg/rbac/policy.go`): a segment listed in
`routes` is decided by that entry (it can only NARROW a wildcard role, never widen
one); a segment not listed falls through to the role's normal
`resources`/`permissions` evaluation, so deny-by-default is untouched. A role
without `routes` pays one `len()` on a nil map — the hot path measured `no_change`.

### 7.10 Deny-by-default

No matching policy → **403**:

- unknown role → denied (`evaluator.go`);
- per-resource form: a resource absent from `permissions`, or present but not granting the action → denied (`evaluator.go`);
- legacy form: resource not in `resources` (and no `"*"`), or action not granted → denied (`evaluator.go`).

A row excluded by an applicable condition reads as **404** (it matches no row), not 403.

### 7.10b `public` — the declarative anonymous surface (ADR-026)

`rbac.public` (sibling of `roles`) declares what an UNAUTHENTICATED request may
**read** — the no-Go path to a blog/catalogue/landing:

```json
"rbac": {
  "roles": { "admin": { "resources": "*", "actions": ["*"] } },
  "public": {
    "articulos": {
      "actions": ["read"],
      "conditions": { "field": "estado", "op": "eq", "val": "publicado" },
      "fields": ["id", "titulo", "cuerpo", "portada"]
    },
    "files": { "actions": ["read"] }
  }
}
```

Each entry has the per-resource permission shape minus `condition_actions`.
Compiled into the ONE evaluator as the reserved role `$public` (declaring that
name in `roles` is a load error), so every surface — REST, GraphQL, aggregate,
`?include=` embeds, subroutes, SSE, files — enforces it identically.

Validated at load: `actions` must be exactly `["read"]`
(`public_read_only`); a condition `val` must be a **literal** —
`$user_id`/`$external_client_id` are errors (`public_condition_identity`: an
anonymous request has no identity, the rule would match zero rows forever);
`conditions.field` and every `fields` entry must exist; `files` is grantable
actions-only. Runtime: anonymous requests ride the `Route.Public` rate
limiter (per tenant+IP), never read or populate the response cache, and a
present-but-invalid Bearer stays **401** (no silent downgrade). The `fields`
allowlist also bounds what may be **filtered and sorted** (`403`,
`ErrForbiddenField`) and which text columns `?search=` sweeps. With a block
declared, a tokenless request on an undeclared resource is **403** (it
reached RBAC); with no block, tokenless stays **401** — byte-identical to
before the key existed.

### 7.11 Examples

**Role-global, owner-scoped** — `operator` may read and update tasks, sees only `id`/`title`/`status`, and is restricted to rows it owns (`operator_id = $user_id`). The condition applies to **every** listed resource and **both** granted actions:

```json
{
  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] },
      "operator": {
        "resources": ["tasks"],
        "actions": ["read", "update"],
        "fields": ["id", "title", "status"],
        "conditions": { "field": "operator_id", "op": "eq", "val": "$user_id" }
      }
    }
  }
}
```

**Per-resource, read-all / write-own** — `member` scopes each resource by its own column, reads all `posts` but writes only its own, and reads `tags` unconditionally:

```json
{
  "rbac": {
    "roles": {
      "member": {
        "permissions": {
          "projects": {
            "actions": ["read", "create", "update", "delete"],
            "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" }
          },
          "documents": {
            "actions": ["read", "create", "update", "delete"],
            "conditions": { "field": "created_by", "op": "eq", "val": "$user_id" }
          },
          "tags": { "actions": ["read"] },
          "posts": {
            "actions": ["read", "create", "update", "delete"],
            "fields": ["id", "title", "status"],
            "conditions": { "field": "author_id", "op": "eq", "val": "$user_id" },
            "condition_actions": ["create", "update", "delete"]
          }
        }
      }
    }
  }
}
```

(The `op:"eq"` values above are the only operator that has any effect; any other `op` would behave identically — see §7.7.)

---

## 8. Hooks (lifecycle extensions)

Hooks let a resource run custom logic around the engine's generated CRUD writes. They are declared per resource under a `hooks` map, keyed by **lifecycle event**, with each value being a single hook configuration object.

```json
"tasks": {
  "fields": { "title": { "type": "string", "required": true } },
  "hooks": {
    "before_create": { "type": "js", "script": "…" },
    "after_create":  { "type": "webhook", "url": "https://…" }
  }
}
```

### 8.1 Hook events

The valid event keys are exactly four (`pkg/schema/keys.go`, `ValidHookEvents`):

- `before_create`
- `after_create`
- `before_update`
- `after_update`

There are **no delete hooks** (`before_delete` / `after_delete` do not exist) and no other event names. Any other key under `hooks` is rejected at load with the message:

```
unknown hook event "<name>" (valid events: after_create, after_update, before_create, before_update)
```

(The valid list is rendered sorted alphabetically — `strings.Join(sortedKeys(ValidHookEvents), ", ")`.)

`hooks` is a strict-key map only at the event level — an unknown event key is the error above. Within each hook object, the allowed keys are validated separately (see §8.3).

### 8.2 The three hook types and their required keys

The hook `type` selects the runtime. The validator (`pkg/schema/validator.go`, the hook switch) enforces, per type, that the type's mandatory key is present and non-empty:

| `type` | Required key | Rejection message if the key is empty |
|---|---|---|
| `js` | `script` (non-empty) | `js hook requires a non-empty script` |
| `webhook` | `url` (non-empty) | `webhook hook requires a non-empty url` |
| `wasm` | `wasm_module` (non-empty) | `wasm hook requires a non-empty wasm_module` |

Any other (or missing) `type` value is rejected with:

```
unknown hook type "<type>": must be "js", "webhook", or "wasm"
```

Note: the validator only enforces the **presence** of the type-appropriate key. It does **not** reject a hook that *also* carries keys irrelevant to its type (e.g. a `js` hook that also sets `url`) — those keys are simply ignored at runtime, as long as every key is in the allowed set below.

**After-hooks must be `webhook` (SEC-AUDIT-V2).** A `js` or `wasm` hook on `after_create` / `after_update` is **rejected at load** — a sandboxed hook runs *after* the commit with no way to change the row and no I/O, so it could only ever be a silent no-op:

```
after_create hooks of type "js" are not supported — a sandboxed hook running after the commit cannot change the row or reach an external system; use a "webhook" after-hook to notify externally, or a before_create/before_update hook to transform the write
```

**Before-hooks must be `js` or `wasm` (ENG-19)** — the exact mirror. A `webhook` hook on `before_create` / `before_update` is **rejected at load**: it used to validate (the URL was even required) while the runner never dispatched it — a declared guard rail that silently never ran. A before-hook must decide the write synchronously, which an async signed POST cannot:

```
before_create hooks of type "webhook" are not supported — the engine never dispatched a before-webhook (it was accepted and silently did nothing): a before-hook must decide the write synchronously, so use a "js" or "wasm" before-hook to validate/transform the write, and a "webhook" AFTER-hook (or the events outbox) to notify an external system
```

So: `js`/`wasm` belong on `before_create` / `before_update` (where they transform/validate the write); `webhook` is the only after-hook type (external notification) — each event family accepts exactly the types that can do real work there.

### 8.3 The full `HookConfig` key set

The strict-key allowlist for a hook object (`pkg/schema/keys.go`, the `addUnknown` call for `…hooks.<event>`) is exactly:

```
type, script, url, hmac_secret_env, wasm_module, wasm_fn, timeout
```

From `pkg/schema/types.go` (`HookConfig`):

| Key | JSON | Used by | Meaning |
|---|---|---|---|
| `type` | `type` | all | `"js" \| "webhook" \| "wasm"` |
| `script` | `script` | js | JS source code |
| `url` | `url` | webhook | endpoint (HTTPS only — see §8.5) |
| `hmac_secret_env` | `hmac_secret_env` | webhook | **name of an env var** holding the HMAC secret (not the secret itself) |
| `wasm_module` | `wasm_module` | wasm | name of a pre-loaded module |
| `wasm_fn` | `wasm_fn` | wasm | exported function to call (default `transform`) |
| `timeout` | `timeout` | (declared only) | execution budget string, e.g. `"500ms"` — see the finding "`timeout` key is inert" |

Any key outside this set rejects the schema (typos never become silently dead config).

### 8.4 `js` hooks — the Goja sandbox

A `js` hook runs `script` in a fresh Goja VM per invocation (`pkg/extensions/js_sandbox.go`, `JSSandbox.RunHook`). The sandbox is intentionally tiny: **no `require`, no `fetch`, no `os`, no `fs`** — only the bindings listed below.

**Bindings injected into the VM:**

- `data` — the mutable request record (a JS object of the incoming write body). The script may read and modify it.
- `user` — the caller/actor context (SEC-AUDIT-V2): `user.user_id`, `user.role`, `user.tenant_id` from the JWT claims (and `user.external_client_id` when present), so a before-hook knows WHO performed the operation. It is `nil` only when there are no claims (an internal call without a JWT). Built by `auth.HookUserContext`.
- `result` — an object pre-initialised to `{ proceed: true, data: <the record>, error: "" }`.

**The contract** — to reject a write, set `result.proceed = false` and `result.error = "<message>"`. The script's final `result` object decides the outcome:

- `result.proceed === false` ⇒ the write is **rejected with HTTP 422** (`Unprocessable Entity`), body exactly `{"error": "<result.error>"}` (REST: `pkg/codegen/builder.go` writes `map[string]string{"error": hookRes.Error}`; GraphQL: surfaced as a plain message in `errors[]` — not `errors[].extensions.fields`). No stack is captured (it is treated as a client error, not a bug).
- `result.proceed === true` (or any truthy/default) ⇒ the write proceeds, using `result.data` (if it is a `map[string]any`) as the body that is inserted/updated — so a `js` `before_create`/`before_update` hook can **transform** the payload. (If `result.data` is not an object, the original `payload` is used as the body.)
- If the script **throws** (a JS runtime error), the hook returns `{proceed:false, error:<the JS error>}`, which is likewise a **422** rejection (the script never executing successfully is treated as a failed validation, not a 500). A 500 is reserved for an *infrastructure* failure of the hook runner itself — i.e. when `RunBeforeHook` returns a non-nil Go error (a watchdog hard-timeout or context cancellation), `builder.go` captures a stack and returns HTTP 500.

**Watchdog timeouts** (hard-coded in `js_sandbox.go`):

- **Soft watchdog: 80 ms.** A `time.AfterFunc(80ms)` calls `vm.Interrupt("hook timeout")`. This is the effective per-script CPU budget; a script over ~80 ms is interrupted.
- **Hard fallback: 500 ms** (`s.timeout`, set in `NewJSSandbox`). If the interrupt is somehow delayed, a `select` on `time.After(500ms)` interrupts again and the call returns `errors.New("hook timeout: exceeded 500ms")` (a Go error → HTTP 500).
- Concurrency is bounded to `GOMAXPROCS × 2` simultaneous VM executions (a semaphore guards against a hook storm saturating all cores).

**Built-in helper functions** available to every `js` script (exact set, from `vm.Set(...)` in `js_sandbox.go`):

- `parseFloat(s)` → Go `strconv.ParseFloat`
- `parseInt(s)` → Go `strconv.Atoi`
- `now()` → current Unix time in **whole seconds** as `int64` (`time.Now().Unix()`)
- `formatMoney(v)` → `string`, formats a float to 2 decimals (`"%.2f"`)
- `isValidEmail(addr)` → `bool`, regex `^[^@\s]+@[^@\s]+\.[^@\s]+$`
- `isValidNIT(nit)` → `bool`, regex `^\d{9,10}$` (shape check only)
- `validateNIT(nit)` → `bool`, **real** Colombian DIAN NIT mod-11 check-digit verification (`pkg/extensions/dian.go`); separators (`.`, `-`) are stripped and the check digit must equal the computed one.
- `calculateCUFE(fields)` → `string`, hex-encoded **SHA-384** (`sha512.Sum384`) over the fixed `cufeFields` order (`pkg/extensions/dian.go`); missing fields are treated as empty strings.

(Note: the engine ships `isValidNIT` *and* `validateNIT` as distinct helpers — `isValidNIT` is a regex shape check; `validateNIT` is the algorithmic check-digit validator. AGENTS.md lists only `validateNIT`.)

**`js` and `after_*` events:** a `js` (or `wasm`) hook is **only valid on `before_create` / `before_update`** — declaring one on `after_create` / `after_update` is **rejected at schema load** (SEC-AUDIT-V2; it would be a post-commit no-op, see §8.2). Put validation/transformation logic in a `before_*` `js` hook; use a `webhook` for after-the-fact notification.

### 8.5 `webhook` hooks — async signed POST

A `webhook` hook (`pkg/extensions/webhook_dispatcher.go`, `WebhookDispatcher.Dispatch`) sends an HTTP `POST` to `url`.

- **`after_create` / `after_update`**: dispatched **asynchronously**, fire-and-forget, on a bounded background goroutine (`FireAfterHook`, cap 256 in-flight via `defaultAfterHookConcurrency`; excess are dropped and logged, returning `false`). The request never blocks on webhook latency.
- **`before_create` / `before_update`**: a `webhook` `before_*` hook **never blocks and never rejects** — `RunBeforeHook` returns `{Proceed:true, Data:payload}` immediately for type `webhook` (it does not actually fire a POST in the before path). Use `js` or `wasm` for blocking pre-write logic.

**Request shape** — POST with JSON body (the record / payload), headers:

- `Content-Type: application/json`
- `X-Appximo-Event: <event>` — the **real lifecycle event** (`after_create` or `after_update`): the runner threads it through `FireAfterHook`→`RunAfterHook`→`Dispatch` (SEC-AUDIT-V2), so an `after_update` webhook sends `X-Appximo-Event: after_update`.
- `X-Appximo-Signature: sha256=<hmac>` where `<hmac>` is hex `HMAC-SHA256(secret, body)`. The secret is read from the environment variable **named** by `hmac_secret_env` (`os.Getenv(hook.HMACSecretEnv)` — an unset/empty env var simply yields an empty-key HMAC).

**HTTPS-only + SSRF guard** (production dispatcher, `NewWebhookDispatcher`):

- `enforceHTTPS = true`: any `url` not prefixed `https://` is **refused** (logged: `only HTTPS endpoints are allowed`, never delivered). Plain `http://` never fires.
- The HTTP client uses an SSRF-safe dialer (`pkg/extensions/ssrf.go`, `CheckSSRF` via `net.Dialer.Control`) that rejects, at dial time, IPs that are: **loopback** (`ip.IsLoopback()` — `127.0.0.0/8`, `::1`), **private** (`ip.IsPrivate()` — RFC 1918 + RFC 4193 ULA), or **link-local** (`ip.IsLinkLocalUnicast()` — `169.254.0.0/16`, `fe80::/10`, which covers the AWS/GCP metadata endpoint). A LAN/localhost receiver will therefore never be called — test against a public HTTPS endpoint.
- **Redirects are refused** (`CheckRedirect` returns `ssrf guard: redirect not allowed`) so a `3xx` to an internal IP cannot bypass the guard.
- Client timeout is 10 s (bounds dial, TLS handshake, response-header wait, and the whole request).
- The response body is drained up to 64 KiB (`maxWebhookRespBytes = 64 << 10`) into `io.Discard` (only the status code matters; never buffers an unbounded response).

**Retries / backoff:** up to **4 total attempts** (`maxAttempts = 4`: 1 initial + 3 retries) with exponential backoff **1 s, 2 s, 4 s**. A `2xx` is success; anything else is logged and retried. Failures are logged but **never returned** to the client (the write already committed; the after-hook is best-effort).

### 8.6 `wasm` hooks — Wazero sandbox

A `wasm` hook (`pkg/extensions/wasm_runner.go`, `WasmRunner`) runs a pre-loaded WASM module via Wazero (no CGO). On the CRUD path it is invoked only on `before_create`/`before_update` (via `runWasmBeforeHook`); the wasm runtime must be wired in (`NewHookRunnerWithWasm`), else a wasm-typed before-hook fails closed with `{Proceed:false, Error:"wasm runtime not configured"}`.

- `wasm_module` — the name the module was registered under (`RegisterModule`). At load this must be non-empty; at runtime an unregistered name fails the hook closed (`wasm module "<name>" not registered`).
- `wasm_fn` — the exported function to call; **defaults to `"transform"`** when empty.
- **Per-module memory cap: 16 MiB** (`wasmMemoryPages = 256` × 64 KiB) — a guest cannot exhaust host memory.
- **Timeout:** if the caller's context has no deadline, a default of **500 ms** (`wasmDefaultTimeout`) is applied; a guest that runs past the deadline is aborted (`WithCloseOnContextDone(true)`).
- **Guest sandbox:** no filesystem, no network, no wall-clock syscalls — only two host functions: `appximo_log(ptr,len)` (bounded to 4096 bytes per message, `wasmMaxLogBytes`) and `appximo_now()` (Unix **milliseconds**). Concurrency bounded to `GOMAXPROCS × 2`.
- **ABI:** the module must export `memory`, `alloc(size i32)->ptr i32`, and `wasm_fn` as `(ptr i32, len i32)->(outPtr i32, outLen i32)`; `free(ptr,len)` is called if exported.
- **`before_*` contract:** the payload is passed to the module as JSON; the module returns the (possibly transformed) payload as JSON, which becomes the new record (`HookResult.Data`). **Any error fails closed** (`proceed:false`) so a broken module never silently lets a write through. (Unlike `js`, a `wasm` `before_*` hook cannot signal a *clean* rejection message — it either returns transformed JSON or errors closed.) The tenant id passed to the module is derived from `userCtx["tenant_id"]`, which is empty on the live CRUD path (the user context is `nil`).

### 8.7 The `timeout` key

`timeout` is an accepted, strict-key-validated `HookConfig` field intended as an execution budget string (e.g. `"500ms"`). See the finding below — in the current engine this field is **parsed but not consumed**; the effective budgets are the hard-coded 80 ms / 500 ms (js) and 500 ms (wasm).

### 8.8 Hooks are compiled at BOOT

Hooks are wired into the live router from the `--schema` file at boot (`pkg/codegen.BuildRouter` reads `res.Hooks["before_create"]`, `["after_create"]`, `["before_update"]`, `["after_update"]` when constructing each handler; GraphQL does the same in its resolvers). Declaring or changing hooks through the control-plane (`PUT /tenants/{id}/schema` + reload) does **not** re-wire them — a process **restart** is required for hook changes to take effect. (See the finding "the reload `warnings` field no longer mentions hooks" — `schemaWarnings` unconditionally returns `nil`, so no such warning is emitted.)

### 8.9 Examples

A `js` `before_create` hook that validates and normalises the body, rejecting with a 422:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "billing-api",
  "resources": {
    "invoices": {
      "fields": {
        "nit":    { "type": "string", "required": true },
        "email":  { "type": "string" },
        "amount": { "type": "float64", "required": true },
        "label":  { "type": "string" }
      },
      "hooks": {
        "before_create": {
          "type": "js",
          "script": "if (!validateNIT(data.nit)) { result.proceed = false; result.error = 'invalid NIT check digit'; } else if (data.email && !isValidEmail(data.email)) { result.proceed = false; result.error = 'invalid email'; } else { data.label = formatMoney(data.amount); }"
        }
      }
    }
  }
}
```

A `webhook` `after_create` hook (HMAC secret read from env var `WEBHOOK_SECRET_INVOICES`):

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "billing-api",
  "resources": {
    "invoices": {
      "fields": {
        "nit":    { "type": "string", "required": true },
        "amount": { "type": "float64", "required": true }
      },
      "hooks": {
        "after_create": {
          "type": "webhook",
          "url": "https://erp.example.com/webhooks/invoice-created",
          "hmac_secret_env": "WEBHOOK_SECRET_INVOICES"
        }
      }
    }
  }
}
```

The receiver verifies the delivery by recomputing `HMAC-SHA256(os.Getenv("WEBHOOK_SECRET_INVOICES"), rawBody)` and comparing it (hex) against the value after `sha256=` in the `X-Appximo-Signature` header. The `X-Appximo-Event` header carries the real event (`after_create` or `after_update`; see §8.5).

---

## 9. Indexes, events, the file store, and reserved blocks

This section covers the remaining resource-level blocks (`indexes`, `events`), the implicit file-store routes, and the reserved `workflows` block. Field types, relations, RBAC, hooks, and foreign keys are covered in earlier sections.

### 9.1 `indexes` — declarative secondary indexes

`indexes` is an optional array on a resource (sibling of `fields`/`hooks`). Each entry is one index over one or more columns (a **composite** index when more than one). The shape (`IndexDef`, `pkg/schema/types.go`) is exactly:

| Key | Type | Required | Meaning |
|---|---|---|---|
| `fields` | array of strings | yes | the column(s) the index covers, in order |
| `unique` | bool | no (default `false`) | `true` ⇒ a `UNIQUE` index |
| `method` | string | no (default `btree`) | access method: `btree` or `gin` (LIBRARY-GAPS-S1) |
| `opclass` | string | no | operator class applied to every column; `gin` only: `jsonb_ops` (default) or `jsonb_path_ops` |

Those four are the **only** accepted keys; any other key (e.g. `field`, `uniqe`, `using`) rejects the schema with `unknown key "<key>" (valid keys: fields, unique, method, opclass)` — every index entry is strict-key-checked (`pkg/schema/keys.go`).

**`method`: `gin` — the jsonb index.** A `gin` index is what makes containment
(`attrs @> '{"brand":"Acme"}'`) an index lookup. It is accepted **only over `jsonb`
columns** and **never with `unique`** (GIN cannot enforce uniqueness); both are
load errors, never a Postgres failure mid-migration. `opclass` is rendered verbatim
into the DDL, so it is restricted to a closed allowlist — `jsonb_path_ops` is
smaller and faster but indexes ONLY containment (no key-existence `?` queries);
`jsonb_ops` (the default) covers more operators at a larger size.

```json
"indexes": [
  { "fields": ["status"] },
  { "fields": ["attributes"], "method": "gin", "opclass": "jsonb_path_ops" }
]
```

A non-btree index gets a **method suffix** in its derived name
(`idx_products_attributes_gin`), so a gin and a btree index over the same column
coexist. btree names are unsuffixed, so every pre-existing index keeps its exact
name — adding `method` to a schema causes zero migration churn. The opclass is
deliberately excluded from the diff key: the introspector reads an index's key
columns from `pg_index.indkey`, which carries no opclass, so comparing it would
drop and recreate the index on every migration.

**What is NOT supported: a partial index (`WHERE` predicate).** Postgres normalizes
predicate text (`estado = 'activa'` is stored as `(estado = 'activa'::text)`), so
round-tripping one through the diff would churn the index on every migration.
Create a partial index in your own boot DDL (`Config.BeforeStart`) instead; the
additive migration leaves it alone.

#### Load-time validation (`validateIndexes`, `pkg/schema/validator.go`)

For each entry:

- **At least one field.** An empty (or absent) `fields` array → `index must list at least one field` at `<resource>.indexes[<i>].fields`.
- **Each field a valid identifier.** Every entry of `fields` must match the field-name regex `^[a-z][a-z0-9_]*$`; otherwise `invalid index field "<f>": must match ^[a-z][a-z0-9_]*$` at `<resource>.indexes[<i>].fields`.

Note that load validation checks only the *shape* of the names, **not** that the named columns actually exist as declared fields. Column *existence* is checked later, at tenant migration, against the introspected table model — a missing column is logged and skipped, never a load error (see below and finding F-1).

#### Materialization at tenant registration (`addDeclaredIndexes`, `pkg/migration/desired.go`)

When a tenant provisions/evolves, each declared index becomes a btree index created idempotently (`CREATE [UNIQUE] INDEX IF NOT EXISTS`) over the listed columns. The index name is **derived from the table and columns**, not chosen by the user:

- non-unique: `idx_<table>_<col1>_<col2>_…`
- unique: `uniq_<table>_<col1>_<col2>_…`

(e.g. an index on `["owner_id","status"]` of resource `tasks` becomes `idx_tasks_owner_id_status`; the same fields with `"unique": true` become `uniq_tasks_owner_id_status`.)

Two behaviors worth knowing:

- **A column that does not (yet) exist in the live table is skipped, not a failure.** `addDeclaredIndexes` checks each listed column against the desired-schema table model (built from introspecting the live DB plus the desired columns); if any column in an index entry is absent, the **whole** index entry is dropped from the desired schema for that run (so it is never created). This is parity with the legacy converger's `information_schema` existence check. Columns can be added to a live table at runtime, so the DB is treated as the source of truth — the same contract as relation FK indexes. The index is materialized on a later provision once the column exists.
- **Name collisions collapse harmlessly.** If a declared index has the same derived name/columns/method/uniqueness as a relation FK index (below), they merge into one entry in the desired-schema map — equivalent to the `IF NOT EXISTS` no-op.

#### Relation FK columns are auto-indexed — do NOT declare those by hand

You never need an `indexes` entry for a foreign-key column. At tenant registration the migration layer auto-creates btree indexes for FK columns (`addForeignKeys` + `addRelationIndexes`, `pkg/migration/desired.go`; `relationIndexTargets`, `pkg/migration/runner.go`):

- every **field-level relation** FK column (the `relation` source column) → `idx_<table>_<column>`, and
- every **declarative relation** FK column, named `idx_<table>_<fkcolumn>`:
  - `has_many` — the FK lives on the **target** (child) table, so the child's FK column is indexed: `idx_<target>_<fk>`.
  - `belongs_to` — the FK lives on **this** (source) table: `idx_<source>_<fk>`.
  - `many_to_many` — **both** FK columns on the `through` junction table: `idx_<through>_<fk>` and `idx_<through>_<target_fk>`.
- every **composite `foreign_keys`** block's source columns (a single composite btree: `idx_<table>_<col1>_<col2>_…`).

These auto-indexes exist so the referential `RESTRICT` check and the `?include=` embeds are index lookups, never per-parent sequential scans.

#### Example — including a composite unique index

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "orders-api",
  "resources": {
    "order_items": {
      "fields": {
        "order_id":   { "type": "uuid" },
        "product_id": { "type": "uuid" },
        "sku":        { "type": "string" },
        "qty":        { "type": "int" }
      },
      "indexes": [
        { "fields": ["sku"] },
        { "fields": ["order_id", "product_id"], "unique": true }
      ]
    }
  }
}
```

This provisions `idx_order_items_sku` (a plain btree on `sku`) and `uniq_order_items_order_id_product_id` (a composite UNIQUE index that prevents the same product appearing twice in one order). The `order_id` / `product_id` columns also get their own auto-FK index if they declare a `relation`.

### 9.2 `events` — transactional outbox emission

`events` is an optional array on a resource opting that resource's generated CRUD writes into emitting a **transactional outbox event**. The accepted values are an exact, closed set (`validEmitActions`, `pkg/schema/types.go`):

```
"create"   "update"   "delete"
```

These are the **present-tense** RBAC action names. A resource that omits `events` emits nothing and pays zero overhead on the write path.

#### Load-time validation (`pkg/schema/validator.go`)

- **Unknown value rejected.** Any value not in `{create, update, delete}` → `unknown event action "<v>": must be one of create, update, delete` at `<resource>.events`.
- **Duplicate value rejected.** A value listed twice → `duplicate event action "<v>"` at `<resource>.events`.

#### Runtime behavior

- **Topic** is `{resource}.{created|updated|deleted}` — the emitted suffix is the **past-tense** mapping (`emitTopicSuffix`, `pkg/schema/types.go`): `create → created`, `update → updated`, `delete → deleted`. So a POST to `tasks` emits `tasks.created`, PUT/PATCH emit `tasks.updated`, DELETE emits `tasks.deleted`. (PUT and PATCH both map to `updated`.) `EmitTopic("tasks","create") == "tasks.created"` (empty for an unknown action); `ResourceSchema.EmitsOn(action)` reports whether the resource opted into a given action.
- **Same transaction.** The event row is written to `public.outbox` in the **same Postgres transaction** as the CRUD write. If the write rolls back (e.g. a unique violation), the event never exists; if the write commits, the event is guaranteed present. On commit the engine fires `pg_notify(outbox_notify, <id>)` (`outbox.NotifyChannel == "outbox_notify"`).
- **Lean payload.** The event carries only `{id, tenant_id, resource, action}` — the affected row's id plus identity, never the full row. Note the payload's `action` value is the **past-tense suffix** (`created`/`updated`/`deleted`), the same suffix as the topic (`enqueueCRUDEvent`, `pkg/codegen/builder.go`). A consumer that needs more does its own `SELECT` (for a delete the row is already gone, so the id is all that survives).
- **A delete matching no row emits nothing** (a 404 delete produces no event — `RunDelete` only enqueues when `affected > 0`).
- **Consumed by the separate worker** (`cmd/appximo-worker`), at-least-once with `SELECT … FOR UPDATE SKIP LOCKED` — processors must be idempotent.
- **REST, GraphQL, and `POST /api/transaction` all emit identically** — the GraphQL `create`/`update`/`delete` mutations and batch operations enqueue the same topic + lean payload in the same transaction as their REST counterparts.

#### Example

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "tasks-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true },
        "status": { "type": "string", "enum": ["open", "done"] }
      },
      "events": ["create", "update", "delete"]
    }
  }
}
```

Here a POST/PUT/PATCH/DELETE on `tasks` enqueues `tasks.created` / `tasks.updated` / `tasks.deleted` respectively, atomically with the write.

### 9.3 The file store — implicit routes, not a schema block

The content-addressable file store is **not declared in the schema**. The two routes exist automatically on the data plane (`app.go`):

- `POST /api/files` — multipart upload (form field `file`), returns `201 {"file_id","sha256","size"}`.
- `GET /api/files/{id}` — streams the blob back.

Both flow through the identical request chain as generated CRUD (tenant Host → JWT → RBAC), so RBAC is enforced against a resource named **`files`**: a role must list `files` in its policy (`"resources": ["files", …]` or `"*"`) with `create` (upload) / `read` (download). The download bypasses the response cache (a blob is never buffered in RAM).

**Collision rule:** if (and only if) the schema declares a resource literally named `files`, the engine **disables** the file-store routes — the generated CRUD routes for that resource win — and logs at boot:

```
WARNING: schema declares a "files" resource — engine file-store routes (/api/files) are disabled for this schema
```

So name a resource `files` only if you intend to replace the built-in store with your own CRUD resource. Full file-store detail (size cap, dedup, `APPXIMO_FILES_DIR`, the VFS) is in §10.

### 9.4 Reserved `workflows` block — inert, parse-only

`workflows` is a top-level key (sibling of `resources`/`rbac`) reserved for the Phase 2 multi-step orchestration engine (ADR-012). It is **parsed for forward compatibility but has no executor** — declaring workflows loads and validates today and does nothing at runtime. Its shape (`WorkflowSchema` and below, `pkg/schema/types.go`) is strict-key-checked (`pkg/schema/keys.go`) like every other level:

- `workflows.<name>` accepts `trigger`, `steps`.
- `workflows.<name>.trigger` accepts `type`, `event`, `resource`, `cron`, `path`.
- `workflows.<name>.steps[<i>]` accepts `name`, `type`, `ref`, `config`, `next` — where `config` is the one deliberately free-form map (its inner keys are not strict-checked).

Because there is no executor, do not rely on any workflow behavior; treat the block as a no-op placeholder.

---

## 10. What a schema generates (the API surface)

Loading a schema produces a complete server at boot — no code generation, no files written. `pkg/codegen.BuildRouter` walks every resource (sorted by name) and registers a fixed set of REST routes per resource; `pkg/graphql.BuildHandler` builds the GraphQL schema from the same resources; `app.go` mounts the always-present engine surface (auth, files, OpenAPI, health). This section enumerates exactly what you get.

All data-plane routes require the tenant `Host` header (subdomain → Postgres schema) and a JWT (except `/auth/*`, `/openapi*`, `/docs`, and the health probes). RBAC is enforced before every handler.

### 10.1 REST — per resource `{res}`

For each resource `{res}` in `resources`, `BuildRouter` registers (`pkg/codegen/builder.go`):

| Method | Path | Purpose | RBAC action |
|---|---|---|---|
| `GET` | `/api/{res}` | List (paginated, filterable) | `read` |
| `POST` | `/api/{res}` | Create one record | `create` |
| `GET` | `/api/{res}/{id}` | Get one by id (UUID) | `read` |
| `PUT` | `/api/{res}/{id}` | Full replace | `update` |
| `PATCH` | `/api/{res}/{id}` | Partial update | `update` |
| `DELETE` | `/api/{res}/{id}` | Delete by id | `delete` |
| `GET` | `/api/{res}/events` | SSE change stream | `read` |
| `GET` | `/api/{res}/aggregate` | Aggregation (§10.2) | `read` |
| `GET` | `/api/{res}/{id}/{relField}` | Relation read subroute (per field with `relation`) | `read` |

Notes grounded in code:

- **`{id}` must be a UUID.** `GET/PUT/PATCH/DELETE /api/{res}/{id}` parse the path id with `uuid.Parse`; a non-UUID returns `400 {"error":"invalid id format"}` before any DB access.
- **`events` and `aggregate` win over `{id}`.** The static `events` and `aggregate` segments are registered as their own routes (`GET /api/{res}/events`, `GET /api/{res}/aggregate`) and are matched by chi before the `{id}` wildcard — so a literal record id of `events`/`aggregate` is unreachable (it never reaches the UUID check). The `events` stream is NOT response-cached (a stream is never cacheable). With no SSE hub wired it returns `503 {"error":"events not enabled"}`; on a per-tenant subscriber cap it returns `429` (with a `Retry-After: 10` header). Stream frames are `event: create|update|delete` with `data: {"resource","id","record"}` (`record` is null on delete). Per-subscriber RBAC field allowlist + row condition (an `eq` condition) are applied at delivery.
- **Relation subroute name = field name minus `_id`.** For a field `customer_id` with `"relation":"customers"`, the route is `GET /api/{res}/{id}/customer` (`strings.TrimSuffix(field, "_id")` — a relation field that does not end in `_id` keeps its full name) and it JOINs to the referenced column (`references`, default `id`). One read-only route per relation field. **Note:** this subroute applies only the parent-id check — it does NOT inject the role's row condition or field allowlist (the handler runs a bare `SELECT r.* FROM target r JOIN res src ON src.<fk> = r.<refCol> WHERE src.id = $1`), unlike the list/get/update/delete/aggregate/embed paths.
- **Response shape** for list is `{"data":[...],"meta":{...}}`. `meta` always carries `page`, `per_page`, `has_next` (true when the page is full: `len(data) == per_page`), `has_prev` (`page > 1`).
- **Create** caps the body at 1 MiB (`maxRequestBodyBytes`, → `413` "request body too large"); an empty body (`{}`) → `400 "empty body"`, malformed JSON → `400 "invalid JSON body"`. Returns `201` with the created row. A unique-constraint collision → `409 {"error":"field \"<f>\": value already exists"}`. An unknown column is surfaced by Postgres and mapped to `422` `unknown_field` (the DB is the source of truth for the writable column set; columns are NOT whitelisted against `res.Fields`, so a runtime-added column still works).
- **PUT vs PATCH** (`CollectUpdate`): PUT requires every non-auto required field (absent optional fields are written NULL — full replacement); PATCH validates and writes only the fields present. Both reject `id`, unknown fields, and `auto` fields in the body — as the SAME `422 {"error":"validation_failed","fields":[…]}` shape create uses, every violation at once, with rules `type` / `unknown_field` / `read_only` / `required` (ENG-29; it was a single flat string before 2026-08-01, so a client needed two parsers for one error class). REST, the GraphQL `update…` mutation (`errors[].extensions.fields`) and a batch transaction op emit it identically. A missing/RBAC-excluded row → `404` (never `403`, to avoid leaking existence). A state-machine transition that is not allowed → `422` "invalid transition for ...".

#### List query parameters (`pkg/query/builder.go`)

- **Filters:** `?filter[field]=v` (implies `eq`) or `?filter[field][op]=v`. The bracket pattern (`filterParamRe`) is deliberately PERMISSIVE — `^filter\[([^\[\]]+)\](?:\[([^\[\]]+)\])?$` — because a pattern decides what a filter IS, never what is VALID (ADR-024/ENG-14: the old strict pattern silently dropped anything it did not match); validation happens in code that names the problem. Valid ops per field type: `string`/`text` → `eq`, `partial` (`ILIKE %v%`), `start` (`ILIKE v%`); `int`/`int64`/`float64` → `eq`, `gt`, `gte`, `lt`, `lte`; `time` → those five plus `after`/`before`; `uuid`/`bool`/`file`/`json`/`jsonb` → `eq` only; every NULLABLE column additionally takes `is_null` (`true`→`IS NULL`, `false`→`IS NOT NULL`, values `true|1|false|0`, no bound parameter; on `id` or a `required` field it is a named 400 — SCHEMA-6). **`id` (the implicit PK) is filterable as a uuid, `eq` only** (ENG-26). Unknown field → `400` "unknown filter field" listing the available set (incl. `id`); incompatible op → `400` listing the allowed ops; a value the field's type cannot take → `400` naming the parameter, value and type (`validateFilterValue`, ENG-25 — the acceptors reproduce Postgres's input grammar, pinned by a live conformance test; `time` values are the documented exception, validated only by Postgres). With curl, brackets need `-g`: `curl -g '...?filter[status][eq]=open'`.
- **Search:** `?search=term` → case-insensitive `ILIKE %term%` (with `%`/`_` escaped, `ESCAPE '\'`) over only the resource's `string`/`text` fields, OR-ed together and AND-ed with filters. No-op on a resource with no string/text fields.
- **Sort:** `?sort=field&order=asc|desc` — ONE field only; default direction is `ASC`, `desc` (case-insensitive) flips it. Alternative `?order[field]=desc` also works and, when both are present, the `order[...]` form overrides `sort`. An unknown sort/order field, or a direction that is not `asc`/`desc`, is a **400** naming it (ADR-024; both were silently ignored before 2026-08-01). An EMPTY `?sort=`/`?order=`, and `?order=desc` with no `?sort=`, are also named 400s (ENG-30 — presence is the gate, so an empty form field no longer defaults silently). Multi-field sort and `sort=field:desc` remain unsupported.
- **Pagination — keyset (preferred):** `?after=<uuid>` (`id > cursor`, ORDER BY id ASC) or `?before=<uuid>` (`id < cursor`, ORDER BY id DESC; mutually exclusive, `after` wins). The cursor must be a strict lowercase-hyphenated UUID matching `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`, else `400` "invalid after/before cursor: must be a lowercase UUID".
- **Pagination — offset (discouraged):** `?page=` (1-based, default `1`, capped at `10000`) with `?per_page=` (default `20`, max `100`). A non-integer, non-positive **or EMPTY** `page`/`per_page` → `400` naming the value ("invalid page parameter \"0\": must be a positive integer"). Before 2026-08-01 a value ≤ 0 — and `?page=`, what an empty form field produces — was silently served as page 1, even though that same message already said "must be a positive integer" (ADR-024, ENG-30). A value ABOVE the cap is still clamped, not rejected — the cap is documented and `meta` reports the effective value.
- **Total count (opt-in):** `?count=true` (presence of the key, any value) adds `meta.total` and `meta.total_pages` from a `COUNT(*)` over the same filtered, RBAC-scoped set. Off by default — the plain list runs no COUNT.
- **Nested embeds:** `?include=rel,rel.sub` (max depth 2) embeds declared `relations` in one `json_agg`+`LATERAL` query; the list/get response is RBAC-compiled into the SQL. Without `?include=` the SQL is byte-identical to the plain path.

All filter values, the RBAC row condition value, and cursors are bound parameters; identifiers are validated/quoted. RBAC row conditions and field allowlists are applied to list, get-by-id, update, delete, aggregate, and embeds identically (but NOT to the relation read subroute — see above).

### 10.2 Aggregation — `GET /api/{res}/aggregate`

A separate read path (`pkg/query/aggregate.go`), scoped EXACTLY like a list read: same RBAC row condition injected into the `WHERE`, same `filter[...]`, same tenant, same field allowlist.

- **Functions (fixed allowlist, never arbitrary SQL):**
  - `count` — presence flag → `COUNT(*)` (needs no field; always allowed).
  - `sum`, `avg` — numeric fields only (`int`/`int64`/`float64`); comma-separated field list.
  - `min`, `max` — numeric OR `time` fields; comma-separated field list.
  - `group_by` — comma-separated field list; any type except `json`.
- At least one of `count`/`sum`/`avg`/`min`/`max` is required, else `400` "aggregate requires at least one of: count, sum, avg, min, max".
- An unknown field → `400`; a function on an incompatible type → `400`; a field the role may not read → `403` (`ErrAggForbiddenField` — no leak via aggregates).

Example:

```
GET /api/orders/aggregate?count&sum=total,tax&avg=total&min=created&max=created&group_by=status&filter[status][eq]=paid
```

Response WITHOUT `group_by` (one overall object, only the requested keys):

```json
{ "count": 17, "sum": { "total": 4210.5, "tax": 318.2 }, "avg": { "total": 247.6 },
  "min": { "created": "2026-01-02T10:00:00Z" }, "max": { "created": "2026-06-01T09:00:00Z" } }
```

Response WITH `group_by` (a `groups` array, each carrying its group fields plus the aggregates):

```json
{ "groups": [ { "status": "paid", "count": 12, "sum": { "total": 3900 } } ] }
```

### 10.3 Atomic multi-resource transactions — `POST /api/transaction`

One Postgres transaction, all-or-nothing (`pkg/codegen/transaction.go`). The route name `transaction` is reserved — a resource may not be named it (it would shadow the route).

Request body:

```json
{ "operations": [
  { "op": "create", "resource": "ledger_entries", "data": { "account_id": "…", "amount": -100 } },
  { "op": "update", "resource": "products", "id": "…", "data": { "stock": 7 },
    "guard": [ { "field": "stock", "op": "eq", "value": 10 } ] },
  { "op": "delete", "resource": "carts", "id": "…" }
] }
```

- **Op shapes:** `create` = `{op,resource,data}`; `update` = `{op,resource,id,data}` (PATCH/partial semantics); `delete` = `{op,resource,id}`. `id` must be a UUID. Unknown `op` → `400`; unknown `resource` → `400`. An empty `data` on create/update → `400`.
- **Each op is authorized and validated EXACTLY like its single-op counterpart**: per-resource RBAC, declarative validators, `EnforceCreateRBAC` mass-assignment block, `CollectUpdate`, and `before_create`/`before_update` hooks all run (in Phase 1, outside the tx). Outbox events emit in the SAME tx per op.
- **`guard` (compare-and-set, update/delete only):** extra `AND <field> <op> $n` predicates the row must satisfy or the op matches zero rows and the whole batch fails. `op` ∈ `eq | ne | gt | gte | lt | lte` (mapped from a fixed allowlist `{eq:"=", ne:"<>", gt:">", gte:">=", lt:"<", lte:"<="}`; never interpolated). The field must be a declared column and the value is type-checked + bound. **This guard is a DIFFERENT mechanism from the RBAC `conditions.op` of §7** (which supports only `eq`); the guard's six operators apply only inside a transaction op and are about optimistic-locking, not authorization.
- **Limit:** at most `DefaultMaxTxOps` = 100 operations (override `APPXIMO_MAX_TX_OPS`); over the cap → `400`. The 1 MiB body cap also applies. An empty `operations` array → `400`.
- **Failure naming:** any op failure rolls the whole batch back and returns the failing op's status with `{ "error", "failed_operation": <index>, "op", "resource"[, "fields"] }`. Unique collision → `409`; unknown column → `422`; forbidden → `403`; bad op/resource → `400`; an update/delete matching no row → `404` (or `422` when a state-machine transition was the cause). Success returns `200 {"results":[...]}` (each entry is the created/updated row, or `{"deleted":true,"id":…}` for a delete) and invalidates the tenant's response cache.

### 10.4 GraphQL — `POST /graphql`

One endpoint (`pkg/graphql/handler.go`). Resource names are singularized + PascalCased for type/mutation names (`tasks` → `Task`, `createTask`).

- **Queries:**
  - `{res}(page, per_page, filter, order)` — list, returns a `{res}Connection` with `data`, `meta` (`page`, `per_page`, `total`, `total_pages`, `has_next`, `has_prev`), and `links` (`self`/`first`/`last`/`next`/`prev`). Nested relation fields embed via the same `json_agg`+`LATERAL` query as REST `?include=`. The **`COUNT(*)` is lazy** (SEC-AUDIT-V2): it runs **only when** `meta.total` / `meta.total_pages` / `links.last` is selected — a list that doesn't ask for the total pays no COUNT (consistent with REST's opt-in `?count=true`). `has_next` is page-fullness (`len(data) == per_page`), matching REST. GraphQL still exposes **no keyset cursors** (`after`/`before`); only `page`/`per_page` offset pagination is forwarded to the query builder (a documented future increment).
  - `{singular}(id: ID!)` — get by id.
  - `{res}Aggregate(count, sum, avg, min, max, group_by, filter)` — returns `AggregateResult { count, values{fn,field,value}, groups{key{field,value},count,values} }`. Aggregate `value`s are Strings (one shape carries ints/floats/timestamps); `count` is null when `group_by` is used.
- **Mutations** (each exists only when applicable):
  - `create{Singular}(input)` — returns the row; `input` has every non-auto field, required ones non-null. Omitted entirely when the resource has no writable (non-auto) fields.
  - `update{Singular}(id, input)` — PATCH semantics; `input` has every non-auto field, all OPTIONAL. Shares the REST update core (`codegen.RunUpdate`). Omitted when the resource has no writable (non-auto) fields.
  - `delete{Singular}(id)` — returns `Boolean` (true if a row was deleted). Always present (needs only an id).
  - All three write mutations emit outbox events identically to their REST counterparts (`RunInsert`/`RunUpdate`/`RunDelete`).
- **Always HTTP 200.** Errors (validation, RBAC, DB) arrive in the `errors[]` array, never as a non-200 status — check `errors`, not the HTTP code. Validation failures are `errors[].extensions.fields` (same rule engine as REST 422). DB errors are masked to generic messages (`safeDBErr`); an FK violation surfaces the clear referential message, not "internal error".
- **Limits:** body capped at 1 MiB (→ `413`); at most **50 root selections** per operation and **2000 total selections** across the document (alias-amplification guard), else the query is rejected with "query too complex". A fragment's cost is charged at EVERY spread site (ENG-28 — the cap used to be bypassable ~46× by spreading one fragment across many root aliases; counted ≥ resolved now). There is no separate depth counter.
- **Introspection** (`__schema`/`__type`, detected anywhere including fragment definitions — `__typename` is allowed) is rejected with "introspection disabled in production" unless `APPXIMO_ENV=development`. Mutations over `GET` are rejected ("mutations must use POST"). GraphiQL is served at `/graphiql` only in development.

### 10.5 Auth — `/auth/*` (always present, unauthenticated, tenant-aware)

Mounted from `pkg/userauth` (`handlers.go`, under `/auth`). Tenant comes from the Host (except OAuth, where it rides the signed state).

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/auth/signup` | Create a user (enabled only when `APPXIMO_AUTH_SIGNUP_ROLE` is set, else `403`) |
| `POST` | `/auth/login` | Password login → `{user, token}` or `{mfa_required, mfa_token}` |
| `POST` | `/auth/refresh` | Re-mint a fresh token from a still-valid one |
| `POST` | `/auth/reset/request` | Request a password-reset email (uniform anti-enum response) |
| `POST` | `/auth/reset/confirm` | Consume reset token + set new password |
| `POST` | `/auth/verify/request` | Request an email-verification email |
| `GET` / `POST` | `/auth/verify` | Confirm email verification (clickable link or body token) |
| `GET` | `/auth/oauth` | List configured OAuth providers |
| `GET` | `/auth/oauth/{provider}` | Start the OAuth authorization-code flow (302) |
| `GET` | `/auth/oauth/{provider}/callback` | OAuth callback → `{user, token}` |
| `POST` | `/auth/mfa/enable` | Begin TOTP enrollment (session JWT) |
| `POST` | `/auth/mfa/confirm` | Confirm TOTP + receive backup codes (session JWT) |
| `POST` | `/auth/mfa/verify` | Complete the login MFA challenge → final JWT |
| `POST` | `/auth/mfa/disable` | Disable MFA (requires a second factor) |

The JWT a login issues is byte-identical in shape to the one `/api/*` validates (HS256, same `JWT_SECRET`).

### 10.6 File store — `/api/files`

Present whenever no resource is literally named `files` (`app.go`; a `files` resource disables these routes and logs a warning):

| Method | Path | Purpose | RBAC |
|---|---|---|---|
| `POST` | `/api/files` | Multipart upload (form field `file`), streamed + content-deduplicated → `201 {file_id,sha256,size}`; over `APPXIMO_FILES_MAX_BYTES` (default 256 MiB) → `413` | `create` on `files` |
| `GET` | `/api/files/{id}` | Stream the blob back (`404` if unknown to the tenant) | `read` on `files` |

A role needs `files` in its policy (`"resources":["files", …]` or `"*"`).

### 10.7 OpenAPI + Swagger UI (`openapi_serve.go`)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/openapi.json` | OpenAPI 3.0.3 spec (JSON), engine-global, unauthenticated |
| `GET` | `/openapi.yaml` | Same spec (YAML) |
| `GET` | `/docs` | Swagger UI (pinned CDN) pointed at `/openapi.json` |

The server URL in the spec is `/` so the browser's "Try it out" calls the same origin.

### 10.8 Health + operator surfaces (`app.go`)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | none | Liveness — never touches Postgres |
| `GET` | `/readyz` | none | Readiness — flips to `503` while draining on SIGTERM |
| `GET` | `/health` | none | `{"status":"ok","version":…}` |
| `GET` | `/metrics` | `X-Admin-Key` | Prometheus metrics (admin-gated even on the public listener) |
| `*` | `/debug/*` | `X-Admin-Key` | Trace/profile surfaces (admin-gated) |

The control plane (tenant registration, `X-Admin-Key`) lives on a separate listener (`:9090`) and is not part of the data-plane surface above.

---

## 11. Complete annotated example

This section gives ONE complete schema that exercises essentially the entire grammar, and proves it validates. The JSON below was written to a file and run through the engine's own validator:

```
$ ./appximo validate schema.json
Schema válido ✓
```

That exact success line — `Schema válido ✓` — is what `cmd/appximo/cmd_validate.go` prints when `schema.Validate` returns zero errors (`cmd_validate.go`); any rule violation instead prints one line per `schema.ValidationError` to stderr (formatted as `<field>: <message>`) and exits non-zero. Everything claimed below was confirmed against the running validator, not from memory.

### 11.1 The validated schema

The following is the exact, comment-free JSON that passed `appximo validate` (JSON forbids comments, so the explanation is in §11.2):

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "shop-api",
  "resources": {
    "customers": {
      "fields": {
        "email":      { "type": "string", "required": true, "unique": true, "format": "email", "maxLength": 254 },
        "full_name":  { "type": "string", "required": true, "minLength": 1, "maxLength": 120, "renamed_from": "name" },
        "tier":       { "type": "string", "enum": ["free", "pro", "enterprise"], "default": "free" },
        "credit":     { "type": "float64", "default": 0, "min": 0 },
        "metadata":   { "type": "json" },
        "created_at": { "type": "time", "auto": true }
      },
      "relations": {
        "orders": { "type": "has_many", "target": "orders", "fk": "customer_id", "limit": 100 }
      },
      "indexes": [
        { "fields": ["tier"] }
      ],
      "events": ["create"]
    },

    "branches": {
      "fields": {
        "region_code": { "type": "string", "required": true, "maxLength": 8 },
        "branch_code": { "type": "string", "required": true, "maxLength": 8 },
        "label":       { "type": "string", "required": true }
      },
      "indexes": [
        { "fields": ["region_code", "branch_code"], "unique": true }
      ]
    },

    "products": {
      "fields": {
        "sku":         { "type": "string", "required": true, "unique": true, "pattern": "^[A-Z0-9-]{3,32}$" },
        "title":       { "type": "string", "required": true, "maxLength": 200 },
        "description": { "type": "text" },
        "price_cents": { "type": "int", "required": true, "min": 0 },
        "stock":       { "type": "int64", "default": 0, "min": 0 },
        "active":      { "type": "bool", "default": true }
      },
      "relations": {
        "orders": {
          "type": "many_to_many",
          "target": "orders",
          "through": "order_items",
          "fk": "product_id",
          "target_fk": "order_id"
        }
      },
      "indexes": [
        { "fields": ["active"] }
      ]
    },

    "orders": {
      "fields": {
        "customer_id": {
          "type": "uuid",
          "required": true,
          "relation": "customers",
          "on_delete": "restrict",
          "on_update": "cascade"
        },
        "product_sku": {
          "type": "string",
          "relation": "products",
          "references": "sku",
          "on_delete": "set_null",
          "on_update": "cascade"
        },
        "region_code": { "type": "string" },
        "branch_code": { "type": "string" },
        "status": {
          "type": "string",
          "enum": ["pending", "paid", "shipped", "delivered", "cancelled"],
          "default": "pending",
          "state_machine": {
            "initial": "pending",
            "transitions": {
              "pending":   ["paid", "cancelled"],
              "paid":      ["shipped", "cancelled"],
              "shipped":   ["delivered"],
              "delivered": [],
              "cancelled": []
            }
          }
        },
        "total_cents": { "type": "int", "required": true, "min": 0 },
        "placed_at":   { "type": "time", "default": "now" },
        "owner_id":    { "type": "uuid" }
      },
      "foreign_keys": [
        {
          "columns": ["region_code", "branch_code"],
          "target": "branches",
          "ref_columns": ["region_code", "branch_code"],
          "on_delete": "cascade",
          "on_update": "restrict"
        }
      ],
      "relations": {
        "items":    { "type": "has_many",   "target": "order_items", "fk": "order_id" },
        "customer": { "type": "belongs_to", "target": "customers",   "fk": "customer_id" }
      },
      "indexes": [
        { "fields": ["status"] },
        { "fields": ["owner_id", "status"] }
      ],
      "events": ["create", "update", "delete"],
      "hooks": {
        "before_create": {
          "type": "js",
          "script": "if (!data.total_cents || data.total_cents <= 0) { result.proceed = false; result.error = 'total_cents must be positive'; }"
        },
        "after_create": {
          "type": "webhook",
          "url": "https://hooks.example.com/orders/created",
          "hmac_secret_env": "WEBHOOK_SECRET_ORDERS"
        }
      }
    },

    "order_items": {
      "fields": {
        "order_id":   { "type": "uuid", "required": true, "relation": "orders",   "on_delete": "cascade" },
        "product_id": { "type": "uuid", "required": true, "relation": "products", "on_delete": "restrict" },
        "quantity":   { "type": "int", "required": true, "min": 1 },
        "unit_cents": { "type": "int", "required": true, "min": 0 }
      },
      "indexes": [
        { "fields": ["order_id", "product_id"], "unique": true }
      ]
    }
  },

  "rbac": {
    "roles": {
      "admin": { "resources": "*", "actions": ["*"] },

      "support": {
        "resources": ["customers", "orders"],
        "actions": ["read"],
        "fields": ["id", "email", "full_name", "status"]
      },

      "member": {
        "permissions": {
          "orders": {
            "actions": ["read", "create", "update", "delete"],
            "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" },
            "condition_actions": ["update", "delete"]
          },
          "products": {
            "actions": ["read"],
            "fields": ["id", "sku", "title", "price_cents", "active"]
          },
          "customers": {
            "actions": ["read", "update"],
            "conditions": { "field": "id", "op": "eq", "val": "$user_id" }
          }
        }
      }
    }
  }
}
```

### 11.2 Annotated walkthrough

#### Top level

`$schema` and `version` are both mandatory — `LoadFromFile` (`pkg/schema/loader.go`) returns `missing required field "$schema"` / `"version"` if either is empty, before validation even runs. The six allowed top-level keys are exactly `$schema`, `version`, `name`, `resources`, `rbac`, and the forward-compat `workflows`; any other top-level key is rejected by the strict-key checker (`CheckUnknownKeys`, `pkg/schema/keys.go`). `name` here is `"shop-api"` — note that resource *names* are constrained to `^[a-z][a-z0-9_]*$` but the schema `name` is free text.

#### Resource `customers` — field types, validation rules, defaults, a rename

- `email` exercises `required` (NOT NULL + present on POST/PUT), `unique` (a UNIQUE constraint; a collision is `409`), `format: "email"` (one of the four `validFormats` — `email | uuid | url | date`, string/text only), and `maxLength` (rune count). A 422 results from any rule violation.
- `full_name` shows `minLength`/`maxLength` together (the validator requires `minLength <= maxLength`) and a **`renamed_from: "name"`**. The rename intent is accepted only because: `"name"` matches `^[a-z][a-z0-9_]*$`, differs from `full_name`, is not `"id"`, and `name` is **not still a declared field** of this resource (all four checks in `validator.go`). On migration the engine emits `ALTER TABLE … RENAME COLUMN name TO full_name`, preserving data.
- `tier` shows an `enum` with a `default: "free"` — an enum default is validated to be a declared member (`validateDefault`). Writing a value outside the set is a 422.
- `credit` is a `float64` with `default: 0` and `min: 0`. `min`/`max` apply only to numeric types (`int`, `int64`, `float64`). A JSON `0` decodes to `float64(0)`, which the float branch accepts.
- `metadata` is `json` — stored as TEXT, not filterable, and accepts any JSON value as a default (it declares none here).
- `created_at` is `time` with `auto: true` — an engine-managed `TIMESTAMPTZ DEFAULT now()`. An `auto` field may **not** carry a `default` (`validateDefault` rejects that combination first), so none is declared.
- A `relations` block declares `orders` as a **`has_many`** (FK `customer_id` lives on the child `orders` table) with an explicit `limit: 100` (the per-parent embed cap; default would be 50; the validator only forbids a negative `limit`). Requested with `?include=orders`.
- One non-unique `index` on `tier`, and `events: ["create"]` so a POST emits `customers.created` to the transactional outbox.

#### Resource `branches` — the composite-unique target

`branches` exists to be the destination of a composite foreign key. It declares a **composite UNIQUE index** on `["region_code", "branch_code"]`. This is the precondition `columnsAreUniqueOnTarget` checks: a multi-column FK may only point at the target's primary key or a declared `unique` index whose column set matches exactly (order-independent — `sameStringSet`).

#### Resource `products` — pattern, more types, a many_to_many side

- `sku` is `unique` with a `pattern` (RE2 regex, ≤ 200 chars — `MaxPatternLength` in `rules.go`). It doubles as the **non-id unique target column** referenced by `orders.product_sku` (see below).
- `description` is `text`, `price_cents` is `int`, `stock` is `int64` (with an integer `default: 0` — `int`/`int64` defaults must be an integral number, checked as `f == float64(int64(f))`), `active` is `bool` with `default: true`.
- `products.orders` is the **`many_to_many`** relation: it requires `through` (the junction table `order_items`), `fk` (this side's column in the junction, `product_id`), and `target_fk` (the target's column, `order_id`). `through`/`target_fk` are valid ONLY on `many_to_many`; placing them on any other type is rejected.

#### Resource `orders` — relations with on_delete/on_update/references, a composite FK, a state machine, events, both hook types

- `customer_id` is a field-level **`relation`** with **both `on_delete: "restrict"` and `on_update: "cascade"`**. `on_delete` defaults to `restrict` when unset; `on_update` defaults to NO ACTION when unset (a deliberate asymmetry — the two sibling keys do NOT share a default; see findings). Either action is one of `restrict | cascade | set_null`, and `set_null` would be rejected here because the field is `required` (NOT NULL).
- `product_sku` demonstrates **`references` to a unique non-id column**: it is a `string` FK into `products.sku`. The validator confirms `sku` is `unique` on the target and that the FK column's type class matches (`string`/`text`/`json` all map to the `text` class via `pgKindForAPIType`, so `string → string` is compatible). It is nullable, so `on_delete: "set_null"` is permitted (a `set_null` FK requires a nullable column).
- `region_code` + `branch_code` are plain columns feeding the resource-level **composite `foreign_keys`** block: `columns` and `ref_columns` are equal length, `ref_columns` form the target's composite unique index, per-position types match, and the `set_null` rule does not apply here (the actions are `cascade`/`restrict`). The source columns are nullable, which is why `on_delete: "cascade"` (not `set_null`) is fine.
- `status` carries a **`state_machine`**. `initial` accepts a string or array (here the string `"pending"`); the field is string-typed (required for a state machine — `state_machine` only applies to `string`/`text`); every state named in `initial`/`transitions` is a member of the `enum` (coherence check); and the field's `default: "pending"` is one of the initial states. `delivered` and `cancelled` have `[]` outgoing transitions — they are terminal/immutable.
- `total_cents` is a required `int` with `min: 0`; `placed_at` is a `time` field with the special **`default: "now"`** (the one dynamic time default, resolved at insert — any other string is treated as a literal RFC3339 timestamp); `owner_id` is a plain `uuid` used as the RBAC ownership column below.
- `events: ["create", "update", "delete"]` — all three CRUD topics emit (`orders.created`/`.updated`/`.deleted`). Values are checked against `create | update | delete` with no duplicates allowed.
- `hooks` shows **both** a `js` `before_create` (Goja sandbox; `result.proceed = false` rejects the write) and a `webhook` `after_create` with **`hmac_secret_env`** (the NAME of an env var holding the HMAC secret — not the secret itself). The hook event key must be one of `before_create | after_create | before_update | after_update` (`ValidHookEvents`); a `js` hook requires a non-empty `script`, a `webhook` a non-empty `url`.

#### Resource `order_items` — the junction, cascade integrity

`order_items` is both the m2m junction table and a regular resource. Its two FK columns (`order_id` → `orders` with `on_delete: "cascade"`, `product_id` → `products` with `on_delete: "restrict"`) give the junction real referential integrity. The composite **unique** index on `["order_id", "product_id"]` prevents duplicate line items.

#### RBAC — both role forms

- `admin` is the simplest **role-global** form: `resources: "*"` (a string, valid because `resources` is `json.RawMessage`) and `actions: ["*"]`.
- `support` is another role-global role with a response `fields` allowlist. Note this allowlist lists `status` even though `customers` has no `status` field — the role-global form is **intentionally not field-checked** by the validator (`validateRBAC` returns early for any role with no `permissions`, `validator.go`), so this passes (see findings).
- `member` is the **per-resource `permissions`** form, mutually exclusive with the role-global keys. Its `orders` grant shows the **read-all / write-own** pattern: a `conditions` on `owner_id = $user_id` scoped by `condition_actions: ["update", "delete"]`, so reads and creates are unconditional while updates/deletes are restricted to the caller's own rows. Each `condition_actions` entry must be a concrete action present in `actions` (`"*"` is not allowed). The `products` grant is read-only with a per-resource field allowlist. The `customers` grant scopes by the implicit `id` column (allowed because `rbacFieldExists` treats `id` as a real column) so a member can only read/update their own customer record. Every per-resource `conditions.field` is validated to exist on that exact resource.

Together this single schema exercises: all nine field types; `required`/`unique`/`enum`/`min`/`max`/`minLength`/`maxLength`/`pattern`/`format`; literal, enum, and `"now"` defaults; a state machine; field-level relations with `on_delete` + `on_update` + a `references`-to-unique-non-id; a composite `foreign_keys` block against a composite unique index; `has_many`, `belongs_to`, and `many_to_many` relations; plain and composite-unique indexes; outbox `events`; `js` + `webhook` hooks; a `renamed_from`; and both RBAC role forms. It validates clean with `appximo validate`.

---

## 12. Machine validation — the formal JSON Schema meta-schema

This whole grammar is also published as a **formal JSON Schema (Draft 2020-12)
meta-schema** — `pkg/schema/appximo.schema.json`, embedded in the binary
(AI-F0-S1). It is the **deterministic structural net**: any candidate schema can be
checked against it instantly, with no engine and no database, which is exactly what
an AI generation layer (or an editor / IDE) needs — generate JSON, validate it
against the meta-schema, get precise located errors, iterate.

### 12.1 The two layers — structural vs. semantic

Validation is split in two, and the split is deliberate:

- **Structural (the meta-schema, `appximo.schema.json`).** Everything JSON Schema
  *can* express on its own: the field-type enum, the `on_delete`/`on_update`/
  `format`/`relation type`/RBAC-action/condition-`op` enums, the identifier
  patterns (`^[a-z][a-z0-9_]*$`; the `auth_`/`transaction` resource-name
  exclusions), the strict key sets at every level (`additionalProperties: false`),
  which keys are required, the value types, and the two mutually-exclusive RBAC
  forms (`oneOf`). It also encodes the audit outcomes: a condition `op` may only be
  `eq` (SEC-AUDIT-V1), and an `after_create`/`after_update` hook must be `webhook`
  (SEC-AUDIT-V2).
- **Semantic (the Go validator, `pkg/schema` — the AUTHORITY).** Everything that
  needs to look *across* the document, which JSON Schema cannot express:
  - a `relation` / FK `target`, a `references` column, and the FK `ref_columns`
    must **exist** on the target — and `references`/`ref_columns` must be a PK or a
    **unique** column/index of the target, and type-compatible;
  - a condition `field` and a `fields` allowlist entry must **exist** on the
    role's resource(s) (the meta-schema only checks they are valid identifiers);
  - a `state_machine`'s states must be coherent with the field's `enum`, and a
    string `default` must be one of the `initial` states;
  - a `default` must be type-compatible with its field; an `enum` `default` a
    member; `set_null` requires a nullable column;
  - `renamed_from` must not still be a declared name; `condition_actions` must be a
    subset of `actions`; etc.

A document that passes **both** is accepted by the engine. The meta-schema never
rejects a schema the Go validator accepts (verified by a parity test over every
valid schema in the repo); where they differ, it is always the Go validator being
*stricter* (the semantic checks above), never the meta-schema.

### 12.2 Using it

```bash
appximo validate-schema schema.json   # structural (meta-schema), engine-free
appximo validate schema.json          # semantic (Go) — the authority, human output
appximo validate --json schema.json   # UNIFIED LLM-friendly report (structural + semantic)
appximo meta-schema > appximo.schema.json   # print it (for an IDE's $schema, tooling, or an AI)
```

`validate --json` emits one machine-readable report — `{ "valid", "errors":[ {path,
rule, message, expected, got, fix, source} ] }` — merging both validators, the
feedback an AI uses to self-correct. The format and the correction loop are
documented in [AI_SCHEMA_GENERATION.md](AI_SCHEMA_GENERATION.md) (AI-F0-S2).

`validate-schema` reports each structural error with its JSON path and a precise
reason, e.g.:

```
resources.t.fields.a.type: value must be one of 'string', 'text', 'int', 'int64', 'float64', 'bool', 'uuid', 'time', 'json'
resources.t.hooks.after_create.type: value must be 'webhook'
rbac.roles.r.conditions.op: value must be one of 'eq', ''
```

The Go API mirrors this: `schema.ValidateAgainstMetaSchema(raw []byte)
[]ValidationError` (structural) alongside `schema.Validate(*APISchema)
[]ValidationError` (semantic). The existing `validate` command and the engine boot
path are **unchanged** — the meta-schema is a new, additive layer.

---

## Appendix A — code-vs-expectation findings

These are places where the **code's actual behavior diverges from what a reader
would expect**, or from how a feature is described in AGENTS.md / the README.
They are documented here as findings, **not fixed** (this was a documentation
task). Each was read from the code; the ones marked **(verified)** were
additionally re-checked by hand against the running validator/engine during this
audit. Knowing these is essential for generating schemas that behave as intended.

### A.1 High-impact (behavior contradicts documentation or expectation)

1. **`json` fields ARE filterable by `eq`** — the README/AGENTS.md field-type
   table said `json` has *no* filter operators. In fact `json` is simply absent
   from `operatorsForType`, and `validateFilterOp` falls into its "unknown type →
   allow `eq` only" branch, so `filter[<jsonfield>]=v` is accepted and emits
   `<field> = $1` over the TEXT column. Only `eq` works; `gt`/`partial`/etc. are
   still rejected. (`pkg/query/builder.go`) **(verified)**
   **✅ DOC-ALIGNED (SEC-AUDIT-V2):** the code is correct (eq is a sensible exact-
   match on the stored text); §3's type table and AGENTS.md now document `json` as
   `eq`-only rather than "none". No code change.

2. **RBAC row conditions ignore `op` — filtering is ALWAYS equality.**
   **✅ RESOLVED (SEC-AUDIT-V1):** the row condition is enforced as equality
   everywhere it is built, so the schema may only DECLARE what is enforced — the
   validator now rejects any condition whose `op` is not `eq` (or omitted), on
   BOTH the role-global and per-resource forms, with a clear load-time error. A
   non-eq operator can no longer be silently ignored ("declared == applied").
   Richer operators (with type-aware value binding, like the §10 transaction
   `guard`) remain a future increment. (`pkg/schema/validator.go`
   `validateConditionOp`)

   *Original finding:* the `conditions` object carried an `op` key (documented
   "eq", "neq", "in", …) but `AppendRowCondition` hard-coded `<field> = $n`; `op`
   was neither honored nor validated, so `"op":"gt"` silently behaved as `eq`. (Do
   not confuse this with the transaction `guard` in §10, a *different* mechanism
   that genuinely supports `eq|ne|gt|gte|lt|lte`.)

3. **The relation read-subroute is NOT RBAC row/field scoped.**
   **✅ RESOLVED (SEC-AUDIT-V1):** `GET /api/{res}/{id}/{relField}` now enforces
   the role's RBAC on the **related** resource — it evaluates `read` on the target
   (→ `403` if the role may not read it), injects the target's row condition into
   the JOIN (qualified by the `r` alias via `query.AppendAliasedRowCondition`, so
   a hidden row reads as `404`), and applies the target's field allowlist to the
   returned record. It reuses the SAME evaluator the `?include=` embeds use, so the
   subroute, the embeds, and `GET /api/{related}` all scope identically. (Embeds /
   GraphQL nested reads were already scoped — only the subroute was bare.)
   (`pkg/codegen/builder.go` subresource handler)

   *Original finding:* the subroute issued a bare `SELECT r.* FROM <target> r JOIN
   <parent> … WHERE src.id = $1`, gated only by the route-level `read` on the
   PARENT, so a role could read related rows/fields it could not see via
   `GET /api/{related}`.

4. **JS/WASM after-hooks were silent no-ops.**
   **✅ RESOLVED (SEC-AUDIT-V2):** a `js`/`wasm` `after_create`/`after_update` hook
   is now **rejected at schema load** (a sandboxed hook running after the commit
   can neither change the row nor reach an external system, so it could only ever
   be a no-op). Use a `webhook` after-hook to notify externally, or a
   `before_create`/`before_update` js/wasm hook to transform the write. (There are
   still no delete hooks — by design.) (`pkg/schema/validator.go`, the hook loop)

   *Original finding:* `RunAfterHook` switched only on `"webhook"`; a js/wasm
   after-hook passed validation and did nothing.

5. **The webhook `X-Appximo-Event` header was hard-coded to `after_create`.**
   **✅ RESOLVED (SEC-AUDIT-V2):** the runner now threads the real event through
   `FireAfterHook`→`RunAfterHook`→`Dispatch`, so an `after_update` webhook carries
   `X-Appximo-Event: after_update`. (`pkg/extensions/hook_runner.go`)

6. **GraphQL↔REST list asymmetry.**
   **✅ RESOLVED (SEC-AUDIT-V2) — COUNT + has_next:** GraphQL no longer runs
   `COUNT(*)` by default — `meta.total` / `meta.total_pages` / `links.last` are
   **lazy field resolvers** that run the COUNT only when selected (consistent with
   REST's opt-in `?count=true`, and measurably cheaper: a default list dropped
   from p50 ≈ 3.3 ms to ≈ 2.7 ms in an A/B). A client that selects `total` still
   gets it (no breakage). `has_next` now matches REST (`len(data) == per_page`).
   **Still open:** GraphQL has no keyset (`after`/`before`) — a documented future
   increment; GraphQL lists remain OFFSET-paginated. (`pkg/graphql/handler.go`
   `listResolver` + `resolveTotal`/`resolveTotalPages`/`resolveLastLink`)

7. **The legacy role-global RBAC form is not field-validated.**
   **✅ RESOLVED (SEC-AUDIT-V1):** the role-global form now validates, at load,
   that `conditions.field` and each `fields` allowlist entry reference a column
   that exists on at least one of the resources the role applies to (reusing
   `rbacFieldExists`), so a typo is caught at load instead of failing at runtime. A
   wildcard admin with no condition/allowlist is unaffected. (Union semantics: a
   field present on some-but-not-all of a role's resources is accepted — that is a
   fail-closed correctness concern, not a leak, and validating it strictly would
   reject several shipped schemas; it remains a documented limitation.)
   (`pkg/schema/validator.go` `validateRoleGlobal`)

   *Original finding:* `validateRBAC` returned early for any role without a
   `permissions` map, so a role-global `conditions.field`/`fields` naming a
   nonexistent column loaded clean (only the per-resource form was checked).

8. **Control-plane schema reload no longer warns about hook drift.**
   `controlplane.schemaWarnings` is a stub returning `nil`, so a
   `PUT /tenants/{id}/schema` reload no longer surfaces the "hooks changed but
   are boot-compiled — restart required" warning AGENTS.md describes. (The
   separate admin endpoint `POST /admin/tenants/{id}/reload` in `app.go` *does*
   still compute hook-drift warnings via `hooksDiffering`.)
   (`pkg/controlplane/server.go`; `app.go`) **(verified)**

9. **Naming a resource `files` silently disables the built-in file store** with
   only a boot `log.Println` warning — no validation error. The schema loads
   normally and `POST/GET /api/files` simply stop existing. (`app.go`)

10. **The outbox event payload `action` is past-tense.** A resource declares
    `events: ["create", …]` (present tense), but the emitted payload's `action`
    field is the topic suffix `created`/`updated`/`deleted`. A consumer matching
    on `action == "create"` will never match. (`pkg/codegen/builder.go`)

11. **The `workflows` block is fully parsed and strict-key validated but
    completely inert.** Typos in `trigger`/`steps` are rejected (giving the
    impression of a live feature), yet no executor runs it and `trigger.type` /
    `step.type` *values* are never semantically validated. A syntactically
    perfect workflow does nothing. (`pkg/schema/types.go`;
    `pkg/schema/keys.go`)

### A.2 Sharp edges & silent behaviors (generation-relevant gotchas)

12. **Only `$schema` and `version` are required** (and only checked for
    emptiness, accepting any non-empty string). `name`, `resources`, and `rbac`
    are structurally optional — a schema with none of them boots. A JSON-number
    `version` fails at the parse stage, not the required-field stage.
    (`pkg/schema/loader.go`; `pkg/schema/validator.go`)

13. **A field `default` is NOT a Postgres column `DEFAULT`** — it is an app-layer,
    **create-only** fill (`ApplyDefaults`). Existing rows are never backfilled,
    and a `PUT` (full replace) writes an omitted optional field as `NULL`.
    (`pkg/migration/desired.go`; `pkg/schema/rules.go`)

14. **An explicit JSON `null` on create bypasses `default`** and stores `null` —
    defaults fill only *absent* keys (matching SQL `DEFAULT`).
    (`pkg/schema/rules.go`)

15. **`pattern` is not implicitly anchored** — it is a substring match
    (`re.MatchString`). Authors must write `^…$` to validate the whole value.
    (`pkg/schema/rules.go`)

16. **`format:"date"` also accepts a full RFC3339 timestamp** (it tries RFC3339
    before `YYYY-MM-DD`), and **`format:"email"` requires a dotted domain**
    (rejecting `user@localhost`). Both are intentional pragmatic shape checks.
    (`pkg/schema/rules.go`)

17. **A `default` is not cross-checked against the field's own `min`/`max`/
    `minLength`/`maxLength`/`pattern`/`format` at load** (only its type / enum
    membership / state-machine-initial). A rule-violating default still 422s at
    create time, but the schema loads. (`pkg/schema/validator.go`)

18. **`enum` is only meaningful on string fields, but `enum` on a non-string
    field is not rejected at load** — at runtime its enum closure requires a
    string, so every value would fail. (`enum` is `[]string`, so non-string
    *members* fail at JSON parse instead.) (`pkg/schema/validator.go`;
    `pkg/schema/rules.go`)

19. **`relations.fk` / `target_fk` column existence is NOT validated at load** —
    `validateRelations` is structural (it only checks `target` is a declared
    resource). A typo'd FK passes `appximo validate` and surfaces only as a
    logged warning at tenant migration. The same is true of declared `indexes`
    field names (regex-checked only) and FK source columns of the composite
    `foreign_keys` block. (`pkg/schema/validator.go`)

20. **`limit: 0` on a relation does NOT mean "no children"** — it is silently
    treated as the default 50 at SQL-build time (`if limit <= 0 { limit = 50 }`).
    Only `limit < 0` is rejected at load. (`pkg/query/relations.go`)

21. **Two independent `?include=` guards:** nesting depth > 2 → `400 include
    nesting exceeds max depth 2`; selection node count > 25 → `400 too many
    includes`. Depth alone does not bound sibling fan-out.

22. **The `timeout` HookConfig key is parsed and accepted but never consumed** —
    the JS watchdog uses fixed budgets (≈80 ms soft / 500 ms hard) regardless.
    (`pkg/schema/types.go` `HookConfig`)

23. **A hook's `user` context was always `nil` on the live CRUD path.**
    **✅ RESOLVED (SEC-AUDIT-V2):** REST and GraphQL now pass
    `auth.HookUserContext(ctx)` to `RunBeforeHook`, so a before-hook's `user`
    binding carries the actor (`user.user_id`, `user.role`, `user.tenant_id`) from
    the JWT claims. (`pkg/auth/middleware.go` `HookUserContext`; the
    `RunBeforeHook` call sites in `pkg/codegen/builder.go` + `pkg/graphql/handler.go`)

24. **A throwing/failing JS *before*-hook is a 422** (`{"error": <js error>}`),
    not a 500. (`pkg/extensions` + `pkg/codegen/builder.go`)

25. **The validator does not reject type-irrelevant hook keys** — a `type:"js"`
    hook may also carry `url` / `wasm_module`; only the type's own required key is
    checked for presence. (`pkg/schema/validator.go`)

26. **The `many_to_many` `through` (junction) name allows `-`** (`^[a-z][a-z0-9_\-]*$`)
    while every other identifier — resource, field, relation name, `fk`,
    `target_fk` — forbids it (`^[a-z][a-z0-9_]*$`), because a junction may be a
    bare join table that is never a GraphQL type. (`pkg/schema/validator.go`)
    **(verified)**

27. **`renamed_from` is validated only structurally** (valid identifier, differs
    from the current name, not still a declared name) — never cross-checked
    against a live DB. Naming a never-existed old name is accepted and is simply a
    no-op rename. (`pkg/schema/validator.go`)

28. **The resource-level `foreign_keys` block does not enforce multi-column-only**
    — a single-column entry validates and works; the "composite-only" framing is
    a convention. The implicit `id` is also usable as a FK source or `ref_column`
    though it is never a declared field. (`pkg/schema/validator.go`)

29. **Derived index / FK constraint names have no 63-byte guard** — a long table
    plus a long composite column set can exceed Postgres' identifier limit and is
    silently truncated. (`pkg/migration/desired.go`)

30. **Two Colombian DIAN JS helpers ship** — `isValidNIT` (regex shape) and
    `validateNIT` (mod-11 check digit) — though AGENTS.md lists only the latter
    among the built-ins. (`pkg/extensions/js_sandbox.go`, `dian.go`)
