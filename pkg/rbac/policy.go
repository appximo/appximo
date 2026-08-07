package rbac

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Policy holds all role definitions for a tenant.
type Policy struct {
	Roles map[string]RolePolicy `json:"roles"`
}

// PublicRoleName is the reserved role the schema's `rbac.public` block compiles
// into (ADR-026, PUBLIC-SURFACE-S1). An anonymous request (no Authorization
// header, on an app whose schema declares the block) is evaluated AS this role
// through the one existing evaluator — same deny-by-default, same conditions,
// same field allowlists, on every surface. The name is not declarable in
// rbac.roles (schema.PublicRoleName mirrors it; a cross-pin test keeps the two
// constants identical), so the anonymous surface can only come from the block.
const PublicRoleName = "$public"

// UnmarshalJSON folds the schema's `rbac.public` block into the reserved
// public role, so everything downstream — Evaluate, Allows, RoleCacheable,
// DenyDetail — sees ONE uniform role map and needs no second code path.
func (p *Policy) UnmarshalJSON(data []byte) error {
	type plain struct {
		Roles  map[string]RolePolicy         `json:"roles"`
		Public map[string]ResourcePermission `json:"public"`
	}
	var a plain
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	p.Roles = a.Roles
	if len(a.Public) > 0 {
		if p.Roles == nil {
			p.Roles = make(map[string]RolePolicy, 1)
		}
		p.Roles[PublicRoleName] = RolePolicy{Permissions: a.Public}
	}
	return nil
}

// HasPublicSurface reports whether the policy declares an anonymous surface —
// the switch that lets the auth middleware admit tokenless /api requests as
// the public role instead of answering 401.
func (p *Policy) HasPublicSurface() bool {
	_, ok := p.Roles[PublicRoleName]
	return ok
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

	// Routes grants CUSTOM-ROUTE segments (LIBRARY-GAPS-S1) — the virtual resource
	// the middleware derives from a custom endpoint's first /api/ segment. Mirrors
	// schema.RolePolicy.Routes so the schema→rbac.Policy JSON round-trip is lossless.
	// Orthogonal to Resources/Permissions (different namespace: registered endpoints,
	// not tables) and carries NO condition or field allowlist — a virtual segment has
	// no rows. Empty for every pre-S1 role, so the evaluation path is unchanged.
	Routes map[string]RouteGrant `json:"routes,omitempty"`
}

// RouteGrant is one role's grant on one custom-route segment: the allowed actions,
// nothing else. See schema.RouteGrant / ADR-021.
type RouteGrant struct {
	Actions []string `json:"actions"`
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

// DenyDetail returns the operator-facing explanation of a deny: whether the
// caller's role is not declared by any schema role at all, or is declared but
// lacks the grant. SERVER LOG ONLY — deliberately asymmetric (ENG-27):
//
//   - The RESPONSE stays the byte-identical `403 {"error":"forbidden"}` for both
//     cases. Distinguishing them in the body would turn every endpoint into an
//     enumeration oracle over the schema's role namespace (an attacker minting
//     tokens could probe which role names exist).
//   - The LOG gains the distinction, because that is where the operator looks
//     and the attacker cannot. Before this, a token carrying a typo'd or forged
//     role produced a deny indistinguishable from a legitimate one anywhere —
//     not the engine log, not the access log, not the trace.
//
// Echoing the role name in the log leaks nothing to the caller (they hold the
// JWT; its claims are base64, not encrypted). Same family as SEC-5: a defence
// must not leak through its own error channel.
func (p *Policy) DenyDetail(role, resource, action string) string {
	if _, declared := p.Roles[role]; !declared {
		return fmt.Sprintf("role %q is not declared by any schema role", role)
	}
	return fmt.Sprintf("role %q is declared but not permitted %q on %q", role, action, resource)
}

// Allows reports whether role may perform action on resource.
// Conditions are not evaluated here — use Evaluate for that.
func (p *Policy) Allows(role, resource, action string) bool {
	rp, ok := p.Roles[role]
	if !ok {
		return false
	}
	// Custom-route grants (LIBRARY-GAPS-S1) are checked first and are authoritative
	// for the segments they name — see routeGrantFor.
	if grant, isRoute := routeGrantFor(&rp, resource); isRoute {
		return actionAllowed(grant.Actions, action)
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

// routeGrantFor returns the role's grant on name when name is one of its declared
// CUSTOM-ROUTE segments (LIBRARY-GAPS-S1), and whether it is one at all.
//
// Two properties make this safe to sit on the authorization hot path:
//
//   - It is AUTHORITATIVE but never widening. A segment listed here is decided
//     here (so what you wrote is what applies — the engine's "declared == applied"
//     rule); a segment NOT listed falls through to the role's normal
//     resources/permissions evaluation, so deny-by-default is untouched and no
//     role gains access to anything it did not declare.
//   - It cannot shadow a real resource: schema validation rejects a `routes` key
//     that names a declared resource, so the two namespaces are disjoint.
//
// Cost for the overwhelming majority of roles (no routes at all): one len() on a
// nil map. rp is taken BY POINTER deliberately — RolePolicy is a wide struct (a
// RawMessage, three slices, a pointer and two maps), and copying it per call on
// the authorization path is measurable (it showed up as a ~8 ns regression on the
// microbenchmark before this was a pointer).
func routeGrantFor(rp *RolePolicy, name string) (RouteGrant, bool) {
	if len(rp.Routes) == 0 {
		return RouteGrant{}, false
	}
	g, ok := rp.Routes[name]
	return g, ok
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
