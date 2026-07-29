package schema

import (
	"encoding/json"
	"sort"
)

// APISchema is the top-level contract for an Appitools project.
type APISchema struct {
	Schema    string                    `json:"$schema"`
	Version   string                    `json:"version"`
	Name      string                    `json:"name"`
	Resources map[string]ResourceSchema `json:"resources"`
	RBAC      RBACPolicy                `json:"rbac"`
	// Workflows is reserved for the Phase 2 multi-step orchestration engine
	// (ADR-012). The struct is parsed for forward compatibility, but no executor
	// runs it yet — present so existing schemas remain valid once it ships.
	Workflows map[string]WorkflowSchema `json:"workflows,omitempty"`
}

// ResourceSchema defines a single entity (table) with its fields, hooks, and indexes.
type ResourceSchema struct {
	Fields  map[string]FieldDef   `json:"fields"`
	Hooks   map[string]HookConfig `json:"hooks,omitempty"`
	Indexes []IndexDef            `json:"indexes,omitempty"`

	// ForeignKeys declares COMPOSITE (multi-column) foreign keys at the resource
	// level (MIG-F1-S5). A single-column FK is the field-level `relation` (which is
	// inherently 1 column = 1 column → target.id, optionally → a unique non-id column
	// via `references`); a composite FK references MULTIPLE columns of the target's
	// composite PK/unique, which does not fit the field-level model, so it gets an
	// explicit block. Each entry is one constraint over (columns) → target(ref_columns)
	// with its own on_delete/on_update. A resource that omits this key behaves exactly
	// as before. See ForeignKeyDef.
	ForeignKeys []ForeignKeyDef `json:"foreign_keys,omitempty"`

	// RenamedFrom declares the PREVIOUS name of this resource's table (MIG-F1-S2):
	// the migration engine emits `ALTER TABLE <old> RENAME TO <new>` (metadata-only,
	// data + indexes + constraints preserved) instead of the converger's drop+add
	// that stranded the data. The old name must NOT still be a current resource
	// (you cannot rename from a name that still exists); validated at load. Once the
	// rename is applied the intent is INERT — re-provisioning with it still present
	// is a no-op (the old table no longer exists), so it is safe to leave in place.
	RenamedFrom string `json:"renamed_from,omitempty"`

	// Relations is the opt-in set of declarative relations (RELATIONS-V1,
	// ADR-019) keyed by the embed name exposed to clients (the key used in
	// ?include=<name> and the GraphQL nested field). A resource that omits this
	// key serves exactly as before and pays zero overhead on the read path —
	// relations are served ONLY when explicitly requested via ?include=.
	Relations map[string]RelationDef `json:"relations,omitempty"`

	// Events is the opt-in list of write actions that emit a transactional
	// outbox event (CRUD-EMIT-V1). Valid values: "create", "update", "delete"
	// (present-tense, matching the RBAC action vocabulary). For a declared
	// action the engine writes a row to public.outbox IN THE SAME TRANSACTION as
	// the CRUD write, with topic "{resource}.{created|updated|deleted}". A
	// resource that omits this key emits nothing and pays zero overhead. See
	// EmitActions / EmitTopic.
	Events []string `json:"events,omitempty"`
}

// validEmitActions is the closed set of write actions a resource may opt into
// for outbox emission. They mirror the RBAC action names (present tense); the
// emitted topic uses the past-tense form (see emitTopicSuffix).
var validEmitActions = map[string]bool{"create": true, "update": true, "delete": true}

// emitTopicSuffix maps an opt-in action to its topic suffix (past tense), e.g.
// "create" → "created" so the topic reads "tasks.created".
var emitTopicSuffix = map[string]string{"create": "created", "update": "updated", "delete": "deleted"}

// EmitsOn reports whether the resource opted into emitting an event for action
// ("create" | "update" | "delete"). Computed from ResourceSchema.Events.
func (r ResourceSchema) EmitsOn(action string) bool {
	for _, a := range r.Events {
		if a == action {
			return true
		}
	}
	return false
}

// EmitTopic returns the outbox topic for (resource, action), e.g.
// EmitTopic("tasks","create") == "tasks.created". Empty if action is unknown.
func EmitTopic(resource, action string) string {
	suffix, ok := emitTopicSuffix[action]
	if !ok {
		return ""
	}
	return resource + "." + suffix
}

// FieldDef describes one field within a resource.
//
// The declarative validation keys (min/max, minLength/maxLength, pattern,
// format) are ALL optional — a schema that declares none of them behaves
// exactly as before. They are compiled into a ResourceValidator at schema load
// (see rules.go); the request path never compiles anything.
type FieldDef struct {
	// Type is one of: string, text, int, int64, float64, bool, uuid, time, json,
	// file. A `file` field (FILES-LINK-S1) stores the id of an uploaded file
	// (the file_id `POST /api/files` returns) as a UUID column carrying a REAL
	// foreign key to the tenant's own files table — the first-class file↔record
	// link. Its on_delete (restrict default | set_null; cascade rejected) governs
	// what happens to the record when the referenced FILE is deleted; deleting
	// the record never deletes the file.
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Unique   bool     `json:"unique,omitempty"`
	Auto     bool     `json:"auto,omitempty"` // for created_at / updated_at
	Enum     []string `json:"enum,omitempty"`
	Relation string   `json:"relation,omitempty"` // name of the related resource
	// OnDelete declares the referential action of THIS field's foreign key when the
	// referenced (parent) row is deleted (MIG-F1-S1): "restrict" | "cascade" |
	// "set_null". Only meaningful on a field that also declares `relation` (the FK
	// lives on the column, like `unique`/`required`). EMPTY DEFAULTS TO RESTRICT —
	// the safe choice: a delete of a still-referenced row is rejected (409) rather
	// than silently orphaning its children (the integrity bug this closes). set_null
	// requires the column to be nullable (not `required`); validated at load.
	OnDelete string `json:"on_delete,omitempty"`
	// OnUpdate declares the referential action of THIS field's foreign key when the
	// referenced (parent) row's KEY changes (MIG-F1-S5): "restrict" | "cascade" |
	// "set_null". Only meaningful with `relation`. EMPTY DEFAULTS TO NO ACTION — which
	// is what Postgres already records for an FK created without ON UPDATE, so adding
	// this key to an existing schema generates NO churn (a re-provision stays a no-op;
	// see refActionForOnUpdate). This is the deliberate asymmetry with on_delete (whose
	// default is RESTRICT): on_delete shipped RESTRICT from the start, so the live FKs
	// already carry it; on_update is introduced over FKs that already exist with NO
	// ACTION, so its default must match them. set_null requires a nullable column.
	OnUpdate string `json:"on_update,omitempty"`
	// References declares the target COLUMN this field's foreign key points at
	// (MIG-F1-S5). EMPTY DEFAULTS TO "id" (the target's implicit primary key — the only
	// behavior before this key, so retrocompat is total). A non-id value must name a
	// column that is UNIQUE on the target (Postgres requires an FK destination to be a
	// PK or unique column/index); validated at load. Only meaningful with `relation`.
	References string `json:"references,omitempty"`
	// RenamedFrom declares the PREVIOUS name of this field's column (MIG-F1-S2): the
	// migration engine emits `ALTER TABLE … RENAME COLUMN <old> TO <new>`
	// (metadata-only, data preserved, indexes/FK/unique follow the column) instead
	// of the converger's drop+add that stranded the data in the old column. The old
	// name must NOT still be a current field (you cannot rename from a name that
	// still exists); validated at load. Once applied the intent is INERT — a
	// re-provision with it present is a no-op (the old column no longer exists).
	RenamedFrom string `json:"renamed_from,omitempty"`
	Default     any    `json:"default,omitempty"`

	// Declarative validation rules (S44). Pointer types distinguish "absent"
	// from a legitimate zero (min: 0, minLength: 0).
	Min       *float64 `json:"min,omitempty"`       // numeric types: value >= Min
	Max       *float64 `json:"max,omitempty"`       // numeric types: value <= Max
	MinLength *int     `json:"minLength,omitempty"` // string/text: rune count >= MinLength
	MaxLength *int     `json:"maxLength,omitempty"` // string/text: rune count <= MaxLength
	Pattern   string   `json:"pattern,omitempty"`   // string/text: RE2 regex, len <= MaxPatternLength
	Format    string   `json:"format,omitempty"`    // string/text: email | uuid | url | date

	// StateMachine (G5) declares the allowed lifecycle transitions of a string
	// status field: which states a row may be CREATED in (Initial) and which moves
	// are permitted between states (Transitions). The engine forces it on create
	// (the initial state must be valid) and on update (a state may only move along a
	// declared transition; a state with no outgoing transitions is terminal /
	// immutable). A field without StateMachine is a free string (unchanged). Only
	// string/text fields; coherent with `enum` if both are declared.
	StateMachine *StateMachine `json:"state_machine,omitempty"`
}

// StateMachine is the declarative lifecycle of a status field (G5). Initial is the
// set of states a row may be created in; Transitions maps each state to the states
// it may move to (an absent key or empty list ⇒ terminal). `initial` accepts a
// single string or an array in JSON; it is always a slice after parsing.
type StateMachine struct {
	Initial     []string            `json:"initial"`
	Transitions map[string][]string `json:"transitions"`
}

// UnmarshalJSON accepts `initial` as either a string ("pending") or an array
// (["pending","draft"]), normalizing to a slice — the rest is plain.
func (sm *StateMachine) UnmarshalJSON(data []byte) error {
	var aux struct {
		Initial     json.RawMessage     `json:"initial"`
		Transitions map[string][]string `json:"transitions"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	sm.Transitions = aux.Transitions
	if len(aux.Initial) == 0 {
		return nil
	}
	var single string
	if err := json.Unmarshal(aux.Initial, &single); err == nil {
		sm.Initial = []string{single}
		return nil
	}
	return json.Unmarshal(aux.Initial, &sm.Initial)
}

// KnownStates returns the set of every state the machine references (initial states,
// transition sources, and transition targets) — the universe of valid state values.
func (sm *StateMachine) KnownStates() map[string]bool {
	k := make(map[string]bool, len(sm.Transitions)+len(sm.Initial))
	for _, s := range sm.Initial {
		k[s] = true
	}
	for from, tos := range sm.Transitions {
		k[from] = true
		for _, to := range tos {
			k[to] = true
		}
	}
	return k
}

// IsInitial reports whether s is a state a row may be created in.
func (sm *StateMachine) IsInitial(s string) bool {
	for _, x := range sm.Initial {
		if x == s {
			return true
		}
	}
	return false
}

// OriginsOf returns the states from which a transition to target is declared (the
// reverse of Transitions), sorted for deterministic SQL. Empty ⇒ no state may
// transition INTO target (it is only reachable as an initial state, if at all).
func (sm *StateMachine) OriginsOf(target string) []string {
	froms := make([]string, 0, len(sm.Transitions))
	for f := range sm.Transitions {
		froms = append(froms, f)
	}
	sort.Strings(froms)
	var origins []string
	for _, f := range froms {
		for _, to := range sm.Transitions[f] {
			if to == target {
				origins = append(origins, f)
				break
			}
		}
	}
	return origins
}

// RelationDef declares one relation between resources (RELATIONS-V1, ADR-019),
// served nested in a single round-trip via json_agg + LATERAL when a client
// opts in with ?include=. Declaration is EXPLICIT (no FK-catalog inference): the
// relation compiles once at boot from the shared schema, never per request.
//
//	has_many     — parent → many children: FK lives on the TARGET (child) table,
//	               matched against the parent's id (child.<FK> = parent.id).
//	belongs_to   — child → its parent: FK lives on THIS (source) table, matched
//	               against the target's id (target.id = source.<FK>).
//	many_to_many — both sides via a junction table: Through holds FK (this side's
//	               id) and TargetFK (the target's id).
type RelationDef struct {
	Type     string `json:"type"`                // has_many | belongs_to | many_to_many
	Target   string `json:"target"`              // related resource name
	FK       string `json:"fk"`                  // foreign-key column (see Type for which table)
	Through  string `json:"through,omitempty"`   // junction table (many_to_many only)
	TargetFK string `json:"target_fk,omitempty"` // target's FK column in Through (many_to_many only)
	Limit    int    `json:"limit,omitempty"`     // top-N children per parent (0 → DefaultEmbedLimit)
}

// Relation type constants and embed defaults (ADR-019 §4).
const (
	RelationHasMany    = "has_many"
	RelationBelongsTo  = "belongs_to"
	RelationManyToMany = "many_to_many"

	// DefaultEmbedLimit bounds children per parent in an embed when a relation
	// declares no Limit — a paginated embed that caps fan-out (DoS guard).
	DefaultEmbedLimit = 50
	// DefaultMaxIncludeDepth is the maximum nesting depth of ?include= (e.g.
	// "lines.product" is depth 2). Requests beyond it are rejected with 400.
	DefaultMaxIncludeDepth = 2
)

// validRelationTypes is the closed set accepted for RelationDef.Type.
var validRelationTypes = map[string]bool{
	RelationHasMany:    true,
	RelationBelongsTo:  true,
	RelationManyToMany: true,
}

// On-delete referential actions for a field-level foreign key (MIG-F1-S1). An
// unset action defaults to RESTRICT — the safe choice (reject a delete that would
// orphan children, rather than silently orphaning them, the integrity bug closed
// here). set_null requires the FK column to be nullable.
const (
	OnDeleteRestrict = "restrict"
	OnDeleteCascade  = "cascade"
	OnDeleteSetNull  = "set_null"
)

// validOnDeleteActions is the closed set accepted for FieldDef.OnDelete.
var validOnDeleteActions = map[string]bool{
	OnDeleteRestrict: true,
	OnDeleteCascade:  true,
	OnDeleteSetNull:  true,
}

// On-update referential actions (MIG-F1-S5). The action vocabulary mirrors
// on_delete, but the UNSET default is NO ACTION (Postgres' own default, which the
// pre-S5 FKs already carry) — not RESTRICT — so introducing on_update generates no
// drift on existing tenants (see FieldDef.OnUpdate). set_null requires the FK column
// to be nullable.
const (
	OnUpdateRestrict = "restrict"
	OnUpdateCascade  = "cascade"
	OnUpdateSetNull  = "set_null"
)

// validOnUpdateActions is the closed set accepted for FieldDef.OnUpdate and
// ForeignKeyDef.OnUpdate.
var validOnUpdateActions = map[string]bool{
	OnUpdateRestrict: true,
	OnUpdateCascade:  true,
	OnUpdateSetNull:  true,
}

// ForeignKeyDef is one resource-level COMPOSITE foreign key (MIG-F1-S5): a set of
// local Columns referencing the same number of RefColumns on the Target resource,
// which must together form the target's PRIMARY KEY or a UNIQUE constraint/index
// (Postgres requires it; validated at load). OnDelete defaults to RESTRICT (safe —
// like the field-level relation) and OnUpdate defaults to NO ACTION (no-churn). A
// single-column FK is the field-level `relation`, not this block.
type ForeignKeyDef struct {
	Columns    []string `json:"columns"`             // local columns (this resource)
	Target     string   `json:"target"`              // referenced resource
	RefColumns []string `json:"ref_columns"`         // referenced columns on Target (same count as Columns)
	OnDelete   string   `json:"on_delete,omitempty"` // restrict (default) | cascade | set_null
	OnUpdate   string   `json:"on_update,omitempty"` // restrict | cascade | set_null; unset → NO ACTION
}

// HookConfig defines a lifecycle hook on a resource (before_create, after_create, etc.).
type HookConfig struct {
	Type          string `json:"type"`                      // "js" | "webhook" | "wasm"
	Script        string `json:"script,omitempty"`          // JS source for type=js
	URL           string `json:"url,omitempty"`             // endpoint for type=webhook
	HMACSecretEnv string `json:"hmac_secret_env,omitempty"` // env var holding the HMAC secret
	WasmModule    string `json:"wasm_module,omitempty"`     // for type=wasm: name of the pre-loaded module
	WasmFn        string `json:"wasm_fn,omitempty"`         // for type=wasm: function to call (default "transform")
	Timeout       string `json:"timeout,omitempty"`         // execution budget, e.g. "500ms" (default "500ms")
}

// IndexDef specifies an index on one or more fields (a composite index when more
// than one). Applied at tenant migration as CREATE [UNIQUE] INDEX IF NOT EXISTS
// over the listed columns (BUGS-V1 — previously parsed but not applied).
type IndexDef struct {
	Fields []string `json:"fields"`
	Unique bool     `json:"unique,omitempty"`

	// Method is the Postgres access method (LIBRARY-GAPS-S1): "btree" (the
	// default, and what every pre-S1 index used) or "gin". EMPTY MEANS BTREE, so
	// adding this key to an existing schema generates zero churn — the introspector
	// reads pg_am.amname back, and an unchanged index diffs identical.
	//
	// "gin" is the index that makes jsonb containment (`@>`) an index lookup
	// instead of a sequential scan; it is accepted ONLY over `jsonb` columns
	// (validated at load, never a runtime surprise) and never with unique (GIN
	// cannot enforce uniqueness).
	Method string `json:"method,omitempty"`

	// Opclass is the operator class applied to EVERY listed column, e.g.
	// "jsonb_path_ops" for a GIN index that only ever answers `@>` (smaller and
	// faster than the default jsonb_ops, at the cost of key-existence `?`
	// queries). Only valid with an explicit method, and only a value from the
	// method's closed allowlist (validIndexOpclasses) — it is rendered into DDL,
	// so it is never free-form text. Invisible to the diff (the introspector
	// cannot read an opclass back from the index key list), which is exactly why
	// declaring one causes no migration churn.
	Opclass string `json:"opclass,omitempty"`
}

// Index access methods (LIBRARY-GAPS-S1). Empty defaults to btree — the historical
// behavior — so an existing schema is byte-identical.
const (
	IndexMethodBtree = "btree"
	IndexMethodGIN   = "gin"
)

// validIndexMethods is the closed set accepted for IndexDef.Method.
var validIndexMethods = map[string]bool{
	IndexMethodBtree: true,
	IndexMethodGIN:   true,
}

// validIndexOpclasses is the closed per-method allowlist for IndexDef.Opclass. The
// value goes verbatim into DDL, so it may only ever be one of these literals.
var validIndexOpclasses = map[string]map[string]bool{
	IndexMethodGIN: {"jsonb_ops": true, "jsonb_path_ops": true},
}

// RBACPolicy holds all role definitions for a resource set.
type RBACPolicy struct {
	Roles map[string]RolePolicy `json:"roles"`
}

// RolePolicy defines what a role can do.
// Resources is json.RawMessage because it can be the string "*" or an array of resource names.
//
// A role is expressed in ONE of two mutually-exclusive forms (G2):
//   - Role-global (legacy): Resources + Actions + (optional) Conditions/Fields — the
//     single condition/allowlist applies to EVERY listed resource (unchanged).
//   - Per-resource: a Permissions map where each resource carries its OWN actions,
//     condition and field allowlist (workspace/participation/owner scoping). When
//     present it is the sole source of truth (deny-by-default for absent resources).
//
// Declaring both forms on one role is rejected at validation.
type RolePolicy struct {
	Resources  json.RawMessage `json:"resources,omitempty"`
	Actions    []string        `json:"actions,omitempty"`
	Conditions *Condition      `json:"conditions,omitempty"`
	Fields     []string        `json:"fields,omitempty"` // field-level allowlist (read-only roles)

	// Permissions is the per-resource form (G2): resource name → its grant. Empty for
	// a legacy role; omitempty keeps the marshalled legacy policy byte-identical so the
	// schema→rbac.Policy round-trip is unchanged for every existing schema.
	Permissions map[string]ResourcePermission `json:"permissions,omitempty"`

	// Routes grants access to CUSTOM ROUTES — endpoints a Go backend registers with
	// (*App).Register, whose first /api/ segment the engine authorizes as a VIRTUAL
	// resource (LIBRARY-GAPS-S1). It is keyed by that segment, with its own actions:
	//
	//	"routes": { "checkout": { "actions": ["create"] } }   → POST /api/checkout
	//
	// It is ORTHOGONAL to resources/permissions — a role may declare both, because
	// they govern different namespaces (real tables vs registered endpoints). That
	// is the whole point: before this key, a role using per-resource `permissions`
	// could not reach ANY custom route (every permissions key is checked against a
	// real resource), so "owner-scoped end users + a custom action endpoint" — a
	// customer with their own orders AND a checkout — was inexpressible.
	//
	// SEMANTICS, deliberately narrow:
	//   - AUTHORITATIVE: for a segment listed here, this entry decides. It can only
	//     narrow a wildcard role, never widen one — a segment NOT listed falls
	//     through to the role's normal resources/permissions evaluation, so
	//     deny-by-default is untouched.
	//   - NO conditions, NO field allowlist. A virtual segment has no rows and no
	//     columns; a row filter would be injected into SQL for a table that does not
	//     exist. Declaring either is a LOAD error, never a silent no-op.
	//   - Validated at BOOT against the REGISTERED routes: a grant for a segment no
	//     route serves (or an action no registered method provides) fails the boot
	//     with a clear message, instead of a confusing 403 at request time.
	// See docs/adr/ADR-021-custom-route-authorization.md.
	Routes map[string]RouteGrant `json:"routes,omitempty"`
}

// RouteGrant is one role's grant on ONE custom-route segment (LIBRARY-GAPS-S1).
// Actions are the same vocabulary as everywhere else (read/create/update/delete/*),
// mapped from the HTTP method the middleware already derives: GET→read, POST→create,
// PUT|PATCH→update, DELETE→delete. There is deliberately no conditions/fields key —
// see RolePolicy.Routes.
type RouteGrant struct {
	Actions []string `json:"actions"`
}

// ResourcePermission is one role's grant on one resource (G2). Mirrors
// rbac.ResourcePermission so the schema→rbac.Policy JSON round-trip is lossless.
type ResourcePermission struct {
	Actions          []string   `json:"actions"`
	Conditions       *Condition `json:"conditions,omitempty"`
	ConditionActions []string   `json:"condition_actions,omitempty"` // actions the condition gates (empty = all)
	Fields           []string   `json:"fields,omitempty"`            // per-resource field allowlist
}

// Condition is a simple predicate evaluated at request time for row-level filtering.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`  // "eq", "neq", "in", etc.
	Val   string `json:"val"` // may reference session vars like "$user_id"
}

// ── Workflows (Phase 2, ADR-012) ────────────────────────────────────────────
// These structs only RESERVE the schema shape for the multi-step orchestration
// engine. There is no executor yet; the validator ignores unknown fields, so a
// schema that declares workflows loads today and stays valid when the engine
// ships. Do not build the executor here.

// WorkflowSchema is one named workflow: a trigger plus an ordered list of steps.
type WorkflowSchema struct {
	Trigger WorkflowTrigger `json:"trigger"`
	Steps   []WorkflowStep  `json:"steps,omitempty"`
}

// WorkflowTrigger describes what starts a workflow.
type WorkflowTrigger struct {
	Type     string `json:"type"`               // "event" | "cron" | "http"
	Event    string `json:"event,omitempty"`    // e.g. "after_create" (with Resource)
	Resource string `json:"resource,omitempty"` // resource the event applies to
	Cron     string `json:"cron,omitempty"`     // cron expression for type=cron
	Path     string `json:"path,omitempty"`     // route for type=http
}

// WorkflowStep is a single node in a workflow pipeline.
type WorkflowStep struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`             // "hook" | "webhook" | "wasm" | "branch"
	Ref    string         `json:"ref,omitempty"`    // hook/module/url reference
	Config map[string]any `json:"config,omitempty"` // step-specific configuration
	Next   string         `json:"next,omitempty"`   // name of the next step (or branch target)
}
