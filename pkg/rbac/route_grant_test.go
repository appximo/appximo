package rbac_test

import (
	"encoding/json"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
)

// LIBRARY-GAPS-S1 / ADR-021 — custom-route grants (`routes`).
//
// These are the SECURITY tests of the feature. The positive case is one line; the
// weight is on the negatives, because the whole risk of adding a new grant source
// to the authorization path is that it widens something it should not.

// policyWithRoutes: a customer that owns its orders (per-resource permissions, the
// form that COULD NOT reach a custom route before this feature) plus a grant on the
// virtual "checkout" segment; a viewer with no routes at all; an admin wildcard.
func policyWithRoutes(t *testing.T) *rbac.Policy {
	t.Helper()
	const raw = `{
      "roles": {
        "admin":  { "resources": "*", "actions": ["*"] },
        "viewer": { "permissions": { "orders": { "actions": ["read"] } } },
        "customer": {
          "permissions": {
            "orders": { "actions": ["read","create"],
                        "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } }
          },
          "routes": { "checkout": { "actions": ["create"] } }
        }
      }
    }`
	var p rbac.Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	return &p
}

func TestRouteGrant_GrantsTheDeclaredSegmentAndAction(t *testing.T) {
	p := policyWithRoutes(t)

	// The gap this closes: a `permissions` role reaching a custom route.
	if !p.Allows("customer", "checkout", "create") {
		t.Fatal("customer should reach POST /api/checkout via its routes grant")
	}
	res := p.Evaluate(rbac.EvalContext{Role: "customer", UserID: "u1"}, "checkout", "create")
	if !res.Allowed {
		t.Fatal("Evaluate should allow the declared route action")
	}
	// A virtual segment has no rows and no columns: a condition here would be
	// injected into SQL for a table that does not exist.
	if res.Condition != nil {
		t.Fatalf("a route grant must never carry a row condition, got %+v", res.Condition)
	}
	if len(res.AllowedFields) != 0 {
		t.Fatalf("a route grant must never carry a field allowlist, got %v", res.AllowedFields)
	}
}

func TestRouteGrant_NegativeDenyByDefault(t *testing.T) {
	p := policyWithRoutes(t)

	t.Run("an action the grant does not list is denied", func(t *testing.T) {
		// checkout is granted create only — GET must not come along for the ride.
		if p.Allows("customer", "checkout", "read") {
			t.Fatal("read on checkout was never granted")
		}
		if p.Evaluate(rbac.EvalContext{Role: "customer"}, "checkout", "delete").Allowed {
			t.Fatal("delete on checkout was never granted")
		}
	})

	t.Run("a segment the role does not declare is denied", func(t *testing.T) {
		if p.Allows("customer", "reconciliation", "read") {
			t.Fatal("customer reached an undeclared custom route")
		}
	})

	t.Run("a role with NO routes block reaches no custom route", func(t *testing.T) {
		for _, action := range []string{"read", "create", "update", "delete"} {
			if p.Allows("viewer", "checkout", action) {
				t.Fatalf("viewer has no routes grant but reached checkout/%s", action)
			}
		}
	})

	t.Run("an unknown role is denied", func(t *testing.T) {
		if p.Allows("nobody", "checkout", "create") {
			t.Fatal("unknown role must be denied")
		}
	})

	t.Run("the route grant does not widen the role's RESOURCE access", func(t *testing.T) {
		// Granting /api/checkout must not hand the customer the orders it could not
		// otherwise write, nor any other table.
		if p.Allows("customer", "orders", "delete") {
			t.Fatal("routes grant leaked a resource action")
		}
		if p.Allows("customer", "payments", "read") {
			t.Fatal("routes grant leaked an entirely undeclared resource")
		}
		// …and the resource condition it DOES have is untouched.
		res := p.Evaluate(rbac.EvalContext{Role: "customer", UserID: "u1"}, "orders", "read")
		if !res.Allowed || res.Condition == nil || res.Condition.Value != "u1" {
			t.Fatalf("the resource's own row condition must still apply, got %+v", res)
		}
	})
}

func TestRouteGrant_WildcardRoleUnaffected(t *testing.T) {
	p := policyWithRoutes(t)
	// A wildcard role declares no routes, so it keeps reaching every custom route
	// exactly as before this feature existed (the pre-S1 behavior).
	for _, seg := range []string{"checkout", "reports", "anything"} {
		if !p.Allows("admin", seg, "create") {
			t.Fatalf("wildcard admin lost access to custom route %q", seg)
		}
	}
}

// TestRouteGrant_AuthoritativeForDeclaredSegment pins the documented rule: the
// entry decides for the segment it names. It can only NARROW a wildcard (never
// widen), which is what makes "declared == applied" safe here.
func TestRouteGrant_AuthoritativeForDeclaredSegment(t *testing.T) {
	const raw = `{
      "roles": {
        "ops": { "resources": "*", "actions": ["*"],
                 "routes": { "reports": { "actions": ["read"] } } }
      }
    }`
	var p rbac.Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Allows("ops", "reports", "read") {
		t.Fatal("the declared action must be allowed")
	}
	if p.Allows("ops", "reports", "delete") {
		t.Fatal("the routes entry is authoritative: an undeclared action on a declared segment is denied even for a wildcard role")
	}
	// An UNdeclared segment still falls through to the wildcard — routes never
	// removes access it did not speak about.
	if !p.Allows("ops", "other-endpoint", "delete") {
		t.Fatal("an undeclared segment must fall through to the role's normal evaluation")
	}
}

// TestRouteGrant_UnchangedForPolicyWithoutRoutes is the regression guard for every
// schema that predates this key: identical decisions, both forms.
func TestRouteGrant_UnchangedForPolicyWithoutRoutes(t *testing.T) {
	const raw = `{
      "roles": {
        "admin":    { "resources": "*", "actions": ["*"] },
        "operator": { "resources": ["tasks"], "actions": ["read","update"],
                      "conditions": { "field": "owner_id", "op": "eq", "val": "$user_id" } },
        "member":   { "permissions": { "posts": { "actions": ["read"] } } }
      }
    }`
	var p rbac.Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		role, resource, action string
		want                   bool
	}{
		{"admin", "tasks", "delete", true},
		{"operator", "tasks", "read", true},
		{"operator", "tasks", "delete", false},
		{"operator", "posts", "read", false},
		{"member", "posts", "read", true},
		{"member", "posts", "create", false},
		{"member", "tasks", "read", false},
	}
	for _, c := range cases {
		if got := p.Allows(c.role, c.resource, c.action); got != c.want {
			t.Errorf("Allows(%s,%s,%s) = %v, want %v", c.role, c.resource, c.action, got, c.want)
		}
	}
	// The row condition still resolves as before.
	res := p.Evaluate(rbac.EvalContext{Role: "operator", UserID: "u7"}, "tasks", "read")
	if res.Condition == nil || res.Condition.Field != "owner_id" || res.Condition.Value != "u7" {
		t.Fatalf("role-global condition changed: %+v", res.Condition)
	}
}

// BenchmarkEvaluate_* guards the hot path: every /api request evaluates the policy,
// so the routes lookup must be free for roles that declare none.
func BenchmarkEvaluateNoRoutes(b *testing.B) {
	const raw = `{"roles":{"operator":{"resources":["tasks"],"actions":["read"],
	  "conditions":{"field":"owner_id","op":"eq","val":"$user_id"}}}}`
	var p rbac.Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		b.Fatal(err)
	}
	ctx := rbac.EvalContext{Role: "operator", UserID: "u1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Evaluate(ctx, "tasks", "read").Allowed {
			b.Fatal("denied")
		}
	}
}

func BenchmarkEvaluateWithRoutes(b *testing.B) {
	const raw = `{"roles":{"operator":{"resources":["tasks"],"actions":["read"],
	  "conditions":{"field":"owner_id","op":"eq","val":"$user_id"},
	  "routes":{"checkout":{"actions":["create"]}}}}}`
	var p rbac.Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		b.Fatal(err)
	}
	ctx := rbac.EvalContext{Role: "operator", UserID: "u1"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.Evaluate(ctx, "tasks", "read").Allowed {
			b.Fatal("denied")
		}
	}
}
