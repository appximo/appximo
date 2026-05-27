package schema

import "encoding/json"

// APISchema is the top-level contract for an Appitools project.
type APISchema struct {
	Schema    string                     `json:"$schema"`
	Version   string                     `json:"version"`
	Name      string                     `json:"name"`
	Resources map[string]ResourceSchema  `json:"resources"`
	RBAC      RBACPolicy                 `json:"rbac"`
}

// ResourceSchema defines a single entity (table) with its fields, hooks, and indexes.
type ResourceSchema struct {
	Fields  map[string]FieldDef    `json:"fields"`
	Hooks   map[string]HookConfig  `json:"hooks,omitempty"`
	Indexes []IndexDef             `json:"indexes,omitempty"`
}

// FieldDef describes one field within a resource.
type FieldDef struct {
	Type     string   `json:"type"`               // string, int, int64, float64, bool, uuid, time
	Required bool     `json:"required,omitempty"`
	Unique   bool     `json:"unique,omitempty"`
	Auto     bool     `json:"auto,omitempty"`     // for created_at / updated_at
	Enum     []string `json:"enum,omitempty"`
	Relation string   `json:"relation,omitempty"` // name of the related resource
	Default  any      `json:"default,omitempty"`
}

// HookConfig defines a lifecycle hook on a resource (before_create, after_create, etc.).
type HookConfig struct {
	Type          string `json:"type"`                     // "js" or "webhook"
	Script        string `json:"script,omitempty"`         // JS source for type=js
	URL           string `json:"url,omitempty"`            // endpoint for type=webhook
	HMACSecretEnv string `json:"hmac_secret_env,omitempty"` // env var holding the HMAC secret
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
