package schema_test

import (
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// LIBRARY-GAPS-S1 / ADR-021 — the SCHEMA half of custom-route authorization.
// (The boot half — "is this segment actually registered?" — lives in the root
// package, where the route set exists: TestValidateRouteGrants_* in
// route_grants_boot_test.go.)

const routeGrantBase = `{
  "$schema":"https://appitools.dev/schema/v1","version":"1","name":"x",
  "resources": {
    "orders": { "fields": { "user_id": { "type": "uuid" }, "total": { "type": "int64" } } }
  },
  "rbac": { "roles": { "customer": {
      "permissions": { "orders": { "actions": ["read"],
        "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } } },
      %s
  } } }
}`

func routeGrantSchema(t *testing.T, routesJSON string) *schema.APISchema {
	t.Helper()
	return parseSchema(t, strings.Replace(routeGrantBase, "%s", routesJSON, 1))
}

// TestRouteGrants_PermissionsRoleCanDeclareARoute is THE gap this closes: before
// it, a role using per-resource `permissions` could not be granted any custom
// route (every permissions key is checked against a real resource).
func TestRouteGrants_PermissionsRoleCanDeclareARoute(t *testing.T) {
	s := routeGrantSchema(t, `"routes": { "checkout": { "actions": ["create"] } }`)
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Fatalf("permissions + routes must be valid together, got: %v", errs)
	}
	if got := s.RBAC.Roles["customer"].Routes["checkout"].Actions; len(got) != 1 || got[0] != "create" {
		t.Fatalf("routes grant did not round-trip: %v", got)
	}
}

func TestRouteGrants_RejectedShapes(t *testing.T) {
	cases := []struct {
		name, routes, want string
	}{
		{
			name:   "a segment that names a real resource",
			routes: `"routes": { "orders": { "actions": ["read"] } }`,
			want:   "is a declared resource, not a custom route",
		},
		{
			name:   "a full path instead of the segment",
			routes: `"routes": { "/api/checkout": { "actions": ["create"] } }`,
			want:   "invalid custom-route segment",
		},
		{
			name:   "no actions",
			routes: `"routes": { "checkout": { "actions": [] } }`,
			want:   "at least one action is required",
		},
		{
			name:   "an unknown action",
			routes: `"routes": { "checkout": { "actions": ["submit"] } }`,
			want:   `unknown action "submit"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := schema.Validate(routeGrantSchema(t, c.routes))
			if !hasError(errs, c.want) {
				t.Fatalf("expected an error containing %q, got: %v", c.want, errs)
			}
		})
	}
}

// TestRouteGrants_ConditionsAndFieldsRejected pins the deliberate semantics: a
// virtual segment has no rows to filter and no columns to project, so declaring
// either is a LOAD error explaining why — never a silently ignored key.
func TestRouteGrants_ConditionsAndFieldsRejected(t *testing.T) {
	for _, key := range []string{
		`"conditions": { "field": "user_id", "op": "eq", "val": "$user_id" }`,
		`"fields": ["id"]`,
	} {
		raw := strings.Replace(routeGrantBase, "%s",
			`"routes": { "checkout": { "actions": ["create"], `+key+` } }`, 1)
		_, err := schema.LoadFromBytes([]byte(raw))
		if err == nil {
			t.Fatalf("expected a load error for a route grant carrying %s", key)
		}
		if !strings.Contains(err.Error(), "not valid on a custom-route grant") {
			t.Fatalf("expected the explanatory message, got: %v", err)
		}
	}
}

func TestRouteGrants_UnknownKeyRejected(t *testing.T) {
	raw := strings.Replace(routeGrantBase, "%s",
		`"routes": { "checkout": { "action": ["create"] } }`, 1) // "action", not "actions"
	if _, err := schema.LoadFromBytes([]byte(raw)); err == nil ||
		!strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("a typo inside a route grant must be rejected, got: %v", err)
	}
}

// TestRoleGlobal_ConditionMustExistOnEveryResource is the fail-closed fix
// (LIBRARY-GAPS-S1): a role-global condition is injected into EVERY resource the
// role lists, so a column present on only some of them used to validate and then
// break at request time. It now names exactly which resources lack it.
func TestRoleGlobal_ConditionMustExistOnEveryResource(t *testing.T) {
	const raw = `{
      "$schema":"x","version":"1","name":"x",
      "resources": {
        "orders":  { "fields": { "user_id": { "type": "uuid" } } },
        "reports": { "fields": { "title":   { "type": "string" } } }
      },
      "rbac": { "roles": { "customer": {
        "resources": ["orders","reports"], "actions": ["read"],
        "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" }
      } } }
    }`
	errs := schema.Validate(parseSchema(t, raw))
	if !hasError(errs, `condition field "user_id" does not exist on 'reports'`) {
		t.Fatalf("expected the missing-column rejection naming reports, got: %v", errs)
	}
	// The machine-readable fix (what the AI correction loop and Studio surface)
	// must point at the shape that expresses the intent.
	var fix string
	for _, e := range errs {
		if e.Rule == "condition_field_missing_on_resource" {
			fix = e.Fix
		}
	}
	if !strings.Contains(fix, `per-resource "permissions"`) {
		t.Fatalf("expected the fix hint to point at per-resource permissions, got %q", fix)
	}
}

// A role-global list may still carry a VIRTUAL custom-route segment (the pre-
// `routes` way of granting one). It is not a table, so the condition check must
// skip it rather than reject the schema.
func TestRoleGlobal_ConditionSkipsVirtualSegments(t *testing.T) {
	const raw = `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "orders": { "fields": { "user_id": { "type": "uuid" } } } },
      "rbac": { "roles": { "customer": {
        "resources": ["orders","checkout"], "actions": ["read","create"],
        "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" }
      } } }
    }`
	if errs := schema.Validate(parseSchema(t, raw)); len(errs) != 0 {
		t.Fatalf("a virtual segment in a role-global list must not trip the column check, got: %v", errs)
	}
}
