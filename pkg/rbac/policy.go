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
type RolePolicy struct {
	Resources   json.RawMessage `json:"resources"`
	Actions     []string        `json:"actions"`
	Conditions  *Condition      `json:"conditions,omitempty"`
	FieldsAllow []string        `json:"fields,omitempty"`
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
	if !resourceAllowed(rp.Resources, resource) {
		return false
	}
	for _, a := range rp.Actions {
		if a == "*" || a == action {
			return true
		}
	}
	return false
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
