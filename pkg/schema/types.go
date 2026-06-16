package schema

import "encoding/json"

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
	Type     string   `json:"type"` // string, int, int64, float64, bool, uuid, time
	Required bool     `json:"required,omitempty"`
	Unique   bool     `json:"unique,omitempty"`
	Auto     bool     `json:"auto,omitempty"` // for created_at / updated_at
	Enum     []string `json:"enum,omitempty"`
	Relation string   `json:"relation,omitempty"` // name of the related resource
	Default  any      `json:"default,omitempty"`

	// Declarative validation rules (S44). Pointer types distinguish "absent"
	// from a legitimate zero (min: 0, minLength: 0).
	Min       *float64 `json:"min,omitempty"`       // numeric types: value >= Min
	Max       *float64 `json:"max,omitempty"`       // numeric types: value <= Max
	MinLength *int     `json:"minLength,omitempty"` // string/text: rune count >= MinLength
	MaxLength *int     `json:"maxLength,omitempty"` // string/text: rune count <= MaxLength
	Pattern   string   `json:"pattern,omitempty"`   // string/text: RE2 regex, len <= MaxPatternLength
	Format    string   `json:"format,omitempty"`    // string/text: email | uuid | url | date
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
