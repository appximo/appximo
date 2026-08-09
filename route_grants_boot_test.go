package appximo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// LIBRARY-GAPS-S1 / ADR-021 — the BOOT half of custom-route authorization: the
// schema's `routes` grants cross-checked against the routes this binary actually
// registered. This is the layer that turns dead authorization config into a boot
// failure instead of an inexplicable 403 at request time.

// schemaGranting builds the tasks schema with a `customer` role granting the given
// route segments.
func schemaGranting(routes map[string]schema.RouteGrant) *schema.APISchema {
	s := tasksSchema()
	s.RBAC.Roles["customer"] = schema.RolePolicy{
		Permissions: map[string]schema.ResourcePermission{
			"tasks": {Actions: []string{"read"}},
		},
		Routes: routes,
	}
	return s
}

func TestValidateRouteGrants_AcceptsARegisteredSegment(t *testing.T) {
	s := schemaGranting(map[string]schema.RouteGrant{
		"checkout": {Actions: []string{"create"}},
	})
	routes := []Route{{Method: "POST", Path: "/api/checkout", Handler: noopHandler}}
	if _, err := validateRouteGrants(s, routes); err != nil {
		t.Fatalf("a grant matching a registered route must pass, got: %v", err)
	}
}

func TestValidateRouteGrants_AcceptsANestedPathSegment(t *testing.T) {
	// The middleware authorizes by the FIRST /api/ segment, so a grant on
	// "webhooks" covers POST /api/webhooks/wompi.
	s := schemaGranting(map[string]schema.RouteGrant{
		"webhooks": {Actions: []string{"create"}},
	})
	routes := []Route{{Method: "POST", Path: "/api/webhooks/wompi", Handler: noopHandler}}
	if _, err := validateRouteGrants(s, routes); err != nil {
		t.Fatalf("a nested path's first segment must match, got: %v", err)
	}
}

func TestValidateRouteGrants_RejectsAnUnregisteredSegment(t *testing.T) {
	s := schemaGranting(map[string]schema.RouteGrant{
		"chekout": {Actions: []string{"create"}}, // typo
	})
	routes := []Route{{Method: "POST", Path: "/api/checkout", Handler: noopHandler}}
	_, err := validateRouteGrants(s, routes)
	if err == nil {
		t.Fatal("a grant for a segment nothing serves must fail the boot")
	}
	if !strings.Contains(err.Error(), "no custom route is registered under /api/chekout") ||
		!strings.Contains(err.Error(), "registered segments: checkout") {
		t.Fatalf("the error must name the typo and list what IS registered, got: %v", err)
	}
}

func TestValidateRouteGrants_RejectsAnActionNoMethodProvides(t *testing.T) {
	s := schemaGranting(map[string]schema.RouteGrant{
		"checkout": {Actions: []string{"create", "read"}}, // no GET is registered
	})
	routes := []Route{{Method: "POST", Path: "/api/checkout", Handler: noopHandler}}
	_, err := validateRouteGrants(s, routes)
	if err == nil {
		t.Fatal("granting an action no registered method provides is dead config and must fail")
	}
	if !strings.Contains(err.Error(), `action "read" is granted but no registered route`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRouteGrants_WildcardAcceptsWhateverTheSegmentServes(t *testing.T) {
	s := schemaGranting(map[string]schema.RouteGrant{
		"checkout": {Actions: []string{"*"}},
	})
	routes := []Route{{Method: "POST", Path: "/api/checkout", Handler: noopHandler}}
	if _, err := validateRouteGrants(s, routes); err != nil {
		t.Fatalf("a wildcard grant must not require every method to exist, got: %v", err)
	}
}

// A schema with no `routes` at all — every schema before this feature — must skip
// the check entirely, including in the pure binary (zero registered routes).
func TestValidateRouteGrants_NoGrantsIsANoOp(t *testing.T) {
	warnings, err := validateRouteGrants(tasksSchema(), nil)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("a schema without routes must never fail nor warn, got: %v / %v", warnings, err)
	}
}

// OPS-26 — THE ASYMMETRY. The stock binary (no custom routes registered) BOOTS a
// schema that grants one, warning that the grant is inert: a grant for a segment
// nothing serves authorizes nothing, and refusing to boot meant one schema file
// could not both be `up`-bootable and carry its consumer binary's grants (the
// user ended up maintaining two schemas). The consumer binary keeps the
// fail-closed rejection (the tests above), where the original dead-config
// argument holds with full force.
func TestValidateRouteGrants_StockBinaryWarnsAndBoots(t *testing.T) {
	s := schemaGranting(map[string]schema.RouteGrant{"checkout": {Actions: []string{"create"}}})
	warnings, err := validateRouteGrants(s, nil)
	if err != nil {
		t.Fatalf("the stock binary must tolerate an inert grant (OPS-26), got: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly one warning naming the grant, got %d: %v", len(warnings), warnings)
	}
	// Actionable: name the role, the segment, say it is INERT, and point at the
	// consumer binary that activates it.
	w := warnings[0]
	for _, must := range []string{"rbac.roles.customer.routes.checkout", "INERT", "/api/checkout", "appximo.Route", "Booting anyway"} {
		if !strings.Contains(w, must) {
			t.Errorf("warning must contain %q, got: %s", must, w)
		}
	}
}

func TestFirstAPISegment(t *testing.T) {
	cases := map[string]string{
		"/api/checkout":         "checkout",
		"/api/webhooks/wompi":   "webhooks",
		"/api/reports/x/y":      "reports",
		"/api/product-attrs":    "product-attrs",
		"/health":               "",
		"/api/":                 "",
		"/apifoo/bar":           "",
		"/api/a/{id}/something": "a",
	}
	for path, want := range cases {
		if got := firstAPISegment(path); got != want {
			t.Errorf("firstAPISegment(%q) = %q, want %q", path, got, want)
		}
	}
}

// actionForMethod must stay in lockstep with rbac.actionFromMethod — the boot
// check would otherwise validate a mapping the middleware does not apply.
func TestActionForMethodMatchesMiddlewareMapping(t *testing.T) {
	want := map[string]string{
		"GET": "read", "POST": "create", "PUT": "update",
		"PATCH": "update", "DELETE": "delete", "HEAD": "",
	}
	for method, action := range want {
		if got := actionForMethod(method); got != action {
			t.Errorf("actionForMethod(%s) = %q, want %q", method, got, action)
		}
	}
}

// The deploy path (POST /admin/engine/schema) applies the SAME cross-check, so a
// bad grant is a clean 422 instead of a persist → restart → rollback cycle.
func TestPersistBootSchema_RejectsUnregisteredRouteGrant(t *testing.T) {
	app := &App{schema: tasksSchema()}
	if err := app.Register(Route{Method: "POST", Path: "/api/checkout", Handler: noopHandler}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"$schema": "https://appximo.com/schema/v1", "version": "1", "name": "x",
		"resources": map[string]any{"tasks": map[string]any{
			"fields": map[string]any{"title": map[string]any{"type": "string"}}}},
		"rbac": map[string]any{"roles": map[string]any{
			"customer": map[string]any{
				"permissions": map[string]any{"tasks": map[string]any{"actions": []string{"read"}}},
				"routes":      map[string]any{"nonexistent": map[string]any{"actions": []string{"create"}}},
			}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// SchemaPath is empty: the check must reject BEFORE any file is touched.
	if err := app.persistBootSchema(raw); err == nil ||
		!strings.Contains(err.Error(), "no custom route is registered under /api/nonexistent") {
		t.Fatalf("expected the deploy to be rejected on the route grant, got: %v", err)
	}
}
