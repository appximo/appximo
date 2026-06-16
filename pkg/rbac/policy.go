package rbac

import (
	"encoding/json"
	"sync"
)

// Policy holds all role definitions for a tenant.
type Policy struct {
	Roles map[string]RolePolicy `json:"roles"`
}

// RolePolicy defines what a role can do.
// Resources is json.RawMessage because it can be the string "*" or a []string.
//
// A role is expressed in ONE of two mutually-exclusive forms (G2):
//
//   - Role-global (legacy): Resources + Actions + (optional) Conditions/FieldsAllow.
//     The single Conditions/FieldsAllow apply to EVERY listed resource. Behaviour is
//     unchanged from before per-resource permissions existed.
//   - Per-resource (Permissions): each resource carries its OWN actions, condition and
//     field allowlist — so one role can scope `workspace_id` on one resource and
//     `conversation_id` on another (workspace/participation/owner scoping). When
//     Permissions is non-empty it is the SOLE source of truth: a resource absent from
//     the map is denied (deny-by-default). The two forms never mix (schema validation
//     rejects a role that declares both).
type RolePolicy struct {
	Resources   json.RawMessage `json:"resources,omitempty"`
	Actions     []string        `json:"actions,omitempty"`
	Conditions  *Condition      `json:"conditions,omitempty"`
	FieldsAllow []string        `json:"fields,omitempty"`

	// Permissions is the per-resource form: resource name → its grant. Empty for a
	// legacy role (omitempty keeps the marshalled legacy policy byte-identical).
	Permissions map[string]ResourcePermission `json:"permissions,omitempty"`
}

// ResourcePermission is one role's grant on ONE resource (G2): the actions allowed,
// an optional row-level Condition carrying that resource's OWN ownership column, an
// optional field allowlist, and ConditionActions to scope the condition to a subset
// of the actions (the "read all, write own" pattern — read unconditional, writes
// owner-scoped). An empty ConditionActions means the condition applies to ALL granted
// actions (the safe default — most restrictive).
type ResourcePermission struct {
	Actions          []string   `json:"actions"`
	Conditions       *Condition `json:"conditions,omitempty"`
	ConditionActions []string   `json:"condition_actions,omitempty"`
	Fields           []string   `json:"fields,omitempty"`
}

// Condition is a predicate evaluated at request time for row-level filtering.
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`  // "eq", "neq", "in", etc.
	Val   string `json:"val"` // may be "$user_id", "$external_client_id", or a literal
}

// Allows reports whether role may perform action on resource.
// Conditions are not evaluated here — use Evaluate for that.
func (p *Policy) Allows(role, resource, action string) bool {
	rp, ok := p.Roles[role]
	if !ok {
		return false
	}
	// Per-resource form: the resource must be listed AND grant the action.
	if len(rp.Permissions) > 0 {
		perm, ok := rp.Permissions[resource]
		return ok && actionAllowed(perm.Actions, action)
	}
	// Legacy role-global form.
	if !resourceAllowed(rp.Resources, resource) {
		return false
	}
	return actionAllowed(rp.Actions, action)
}

// actionAllowed reports whether the action list grants action ("*" grants all).
func actionAllowed(actions []string, action string) bool {
	for _, a := range actions {
		if a == "*" || a == action {
			return true
		}
	}
	return false
}

// RoleCacheable reports whether the response cache may SHARE a role's responses
// across users. A role is cacheable ONLY if it injects no per-user row condition
// and no field allowlist anywhere — otherwise caching by role would let one user
// receive another's rows/fields. Covers BOTH forms: a legacy role with a global
// condition/allowlist is not cacheable, and a per-resource role is not cacheable if
// ANY of its resources carries a condition or a field allowlist (fail-safe).
func (p *Policy) RoleCacheable(role string) bool {
	rp, ok := p.Roles[role]
	if !ok {
		return false
	}
	if len(rp.Permissions) > 0 {
		for _, perm := range rp.Permissions {
			if perm.Conditions != nil || len(perm.Fields) > 0 {
				return false
			}
		}
		return true
	}
	return rp.Conditions == nil && len(rp.FieldsAllow) == 0
}

// parsedResources is the compiled form of a role's Resources field: either a
// wildcard or an O(1)-lookup set. Computed once per distinct raw value.
type parsedResources struct {
	wildcard bool
	set      map[string]struct{}
}

// resourceParseCache memoizes the parse of each role's Resources json.RawMessage.
// Keyed by the raw bytes (server-supplied policy config, a small fixed set), it
// removes a json.Unmarshal — and a linear scan — from every /api request, which
// the profile showed on the RBAC hot path. The key space is bounded by the number
// of roles, so the cache cannot grow unbounded.
var resourceParseCache sync.Map // string(raw) → *parsedResources

func parseResources(raw json.RawMessage) *parsedResources {
	pr := &parsedResources{}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		pr.wildcard = s == "*"
		return pr
	}
	pr.set = make(map[string]struct{})
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		for _, r := range arr {
			pr.set[r] = struct{}{}
		}
	}
	return pr
}

// resourceAllowed reports whether the (compiled) Resources value grants resource.
// The raw value may be "*" or a []string; parsing is memoized in resourceParseCache.
func resourceAllowed(raw json.RawMessage, resource string) bool {
	v, ok := resourceParseCache.Load(string(raw))
	if !ok {
		v, _ = resourceParseCache.LoadOrStore(string(raw), parseResources(raw))
	}
	pr := v.(*parsedResources)
	if pr.wildcard {
		return true
	}
	_, found := pr.set[resource]
	return found
}
