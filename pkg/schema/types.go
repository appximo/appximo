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

// IndexDef specifies a composite index on one or more fields.
type IndexDef struct {
	Fields []string `json:"fields"`
}

// RBACPolicy holds all role definitions for a resource set.
type RBACPolicy struct {
	Roles map[string]RolePolicy `json:"roles"`
}

// RolePolicy defines what a role can do.
// Resources is json.RawMessage because it can be the string "*" or an array of resource names.
type RolePolicy struct {
	Resources  json.RawMessage `json:"resources"`
	Actions    []string        `json:"actions"`
	Conditions *Condition      `json:"conditions,omitempty"`
	Fields     []string        `json:"fields,omitempty"` // field-level allowlist (read-only roles)
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
