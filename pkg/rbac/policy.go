package rbac

import "encoding/json"

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

// resourceAllowed parses the json.RawMessage which may be "*" or []string.
func resourceAllowed(raw json.RawMessage, resource string) bool {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s == "*"
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		for _, r := range arr {
			if r == resource {
				return true
			}
		}
	}
	return false
}
