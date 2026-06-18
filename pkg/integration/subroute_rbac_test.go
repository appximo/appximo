package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
)

// SEC-AUDIT-V1 Hallazgo 2: the relation subresource route GET /api/{res}/{id}/{rel}
// must enforce the role's RBAC on the RELATED resource — its row condition AND its
// field allowlist — not just the parent's `read`. Before the fix it was a bare JOIN
// gated only by the parent, leaking related rows/fields a row-scoped role could not
// see via GET /api/{related}.
//
// Schema: tickets.assignee_id -> agents (a field-level relation, which generates the
// subroute /api/tickets/{id}/assignee). The `scoped` role reads tickets
// unconditionally but is row-scoped on agents by owner_id = $user_id, with a field
// allowlist that excludes `secret`.
func subrouteSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "subroute-sec",
		Resources: map[string]schema.ResourceSchema{
			"agents": {
				Fields: map[string]schema.FieldDef{
					"name":     {Type: "string", Required: true},
					"owner_id": {Type: "uuid"},
					"secret":   {Type: "string"},
				},
			},
			"tickets": {
				Fields: map[string]schema.FieldDef{
					"title":       {Type: "string"},
					"assignee_id": {Type: "uuid", Relation: "agents"},
				},
			},
		},
		RBAC: schema.RBACPolicy{
			Roles: map[string]schema.RolePolicy{
				"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
				// reads ALL tickets, but only its OWN agents (owner_id), and never the
				// agents.secret field.
				"scoped": {Permissions: map[string]schema.ResourcePermission{
					"tickets": {Actions: []string{"read"}},
					"agents": {
						Actions:    []string{"read"},
						Fields:     []string{"id", "name", "owner_id"},
						Conditions: &schema.Condition{Field: "owner_id", Op: "eq", Val: "$user_id"},
					},
				}},
				// can read tickets but NOT agents — the subroute must 403.
				"noagents": {Permissions: map[string]schema.ResourcePermission{
					"tickets": {Actions: []string{"read"}},
				}},
			},
		},
	}
}

func TestSubrouteRelationRBAC(t *testing.T) {
	if testing.Short() {
		t.Skip("subroute rbac: skipping in -short mode")
	}
	const ownerA = "11111111-1111-1111-1111-111111111111"
	const ownerB = "22222222-2222-2222-2222-222222222222"

	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := subrouteSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Sub Co", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	defer srv.Close()
	super := genToken("super_admin", superID)

	// Seed two agents (owned by A and B) and a ticket assigned to each.
	agentA := dpDo(t, srv, "POST", "/api/agents", super, map[string]any{"name": "Ada", "owner_id": ownerA, "secret": "sA"}, http.StatusCreated)
	agentB := dpDo(t, srv, "POST", "/api/agents", super, map[string]any{"name": "Bob", "owner_id": ownerB, "secret": "sB"}, http.StatusCreated)
	ticketA := dpDo(t, srv, "POST", "/api/tickets", super, map[string]any{"title": "tA", "assignee_id": agentA["id"]}, http.StatusCreated)
	ticketB := dpDo(t, srv, "POST", "/api/tickets", super, map[string]any{"title": "tB", "assignee_id": agentB["id"]}, http.StatusCreated)

	scopedA := genToken("scoped", ownerA)

	// 1. scoped(A) → own ticket's assignee (agent A) → 200, allowlist applied.
	got := dpDo(t, srv, "GET", "/api/tickets/"+ticketA["id"].(string)+"/assignee", scopedA, nil, http.StatusOK)
	if got["name"] != "Ada" {
		t.Fatalf("expected agent Ada, got %v", got["name"])
	}
	if _, leaked := got["secret"]; leaked {
		t.Fatalf("FIELD LEAK: agents.secret returned via subroute despite allowlist: %v", got)
	}
	if _, ok := got["owner_id"]; !ok {
		t.Fatalf("allowlisted field owner_id missing: %v", got)
	}

	// 2. scoped(A) → ticket whose assignee (agent B) is owned by B → 404 (hidden by
	//    the related resource's row condition). THE LEAK, CLOSED: before the fix this
	//    returned agent B (with its secret).
	dpDo(t, srv, "GET", "/api/tickets/"+ticketB["id"].(string)+"/assignee", scopedA, nil, http.StatusNotFound)

	// 3. a role without read on agents → 403 (deny-by-default), even though it can
	//    read the parent ticket.
	noagents := genToken("noagents", ownerA)
	dpDo(t, srv, "GET", "/api/tickets/"+ticketA["id"].(string)+"/assignee", noagents, nil, http.StatusForbidden)

	// 4. sanity: an unrestricted role still sees any assignee, with all fields.
	full := dpDo(t, srv, "GET", "/api/tickets/"+ticketB["id"].(string)+"/assignee", super, nil, http.StatusOK)
	if full["secret"] != "sB" {
		t.Fatalf("unrestricted role should see agent B fully, got %v", full)
	}
}
