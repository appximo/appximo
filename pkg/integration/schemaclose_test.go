package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/outbox"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

const scOwnerID = "cccc0000-0000-0000-0000-000000000003"

// scSchema exercises SCHEMA-CLOSE-V1: defaults on insert (status/qty/due) and the
// GraphQL update mutation (RBAC allowlist + row condition + events:["update"]).
func scSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "schemaclose",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"name":     {Type: "string", Required: true},
					"status":   {Type: "string", Default: "open"},
					"qty":      {Type: "int", Default: float64(5)},
					"due":      {Type: "time", Default: "now"},
					"owner_id": {Type: "uuid"},
				},
				Events: []string{"update"},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			"fieldlimited": {
				Resources: json.RawMessage(`["items"]`),
				Actions:   []string{"read", "create", "update"},
				Fields:    []string{"id", "name", "status"}, // no qty
			},
			"owner": {
				Resources:  json.RawMessage(`["items"]`),
				Actions:    []string{"read", "create", "update"},
				Conditions: &schema.Condition{Field: "owner_id", Op: "eq", Val: "$user_id"},
			},
		}},
	}
}

func setupSC(t *testing.T) (*httptest.Server, http.Handler, *db.TenantDB, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("schemaclose: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := scSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "SC Co", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))

	// GraphQL server (same chain as cmd_serve): tenant → jwt → rbac → /graphql.
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	gqlH := gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, events.NewHub(0), false)
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(policyJSON))
	mux.Handle("/graphql", gqlH)
	gqlWrap := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	})

	return rest, gqlWrap, tdb, genToken, func() { rest.Close(); cleanPG() }
}

// gqlDo posts a GraphQL query and returns the decoded {data, errors}.
func gqlDo(t *testing.T, h http.Handler, token, query string) (map[string]any, []any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Data   map[string]any `json:"data"`
		Errors []any          `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("graphql body not JSON: %s", rec.Body.String())
	}
	return out.Data, out.Errors
}

func TestSchemaDefaultsOnInsert(t *testing.T) {
	rest, _, _, tok, done := setupSC(t)
	defer done()
	super := tok("super_admin", superID)

	t.Run("omitted fields get their defaults", func(t *testing.T) {
		got := dpDo(t, rest, "POST", "/api/items", super, map[string]any{"name": "a"}, http.StatusCreated)
		if got["status"] != "open" {
			t.Errorf("status default: want open, got %v", got["status"])
		}
		if got["qty"].(float64) != 5 {
			t.Errorf("qty default: want 5, got %v", got["qty"])
		}
		if got["due"] == nil {
			t.Errorf("time \"now\" default should be set, got nil")
		}
	})

	t.Run("explicit value wins over the default", func(t *testing.T) {
		got := dpDo(t, rest, "POST", "/api/items", super, map[string]any{"name": "b", "status": "done", "qty": 9}, http.StatusCreated)
		if got["status"] != "done" || got["qty"].(float64) != 9 {
			t.Errorf("explicit values overridden by defaults: %v", got)
		}
	})

	t.Run("required field without a default still 422s", func(t *testing.T) {
		if code := dpStatus(t, rest, "POST", "/api/items", super, map[string]any{"status": "open"}); code != 422 {
			t.Fatalf("missing required name: want 422, got %d", code)
		}
	})
}

func TestGraphQLUpdateMutation(t *testing.T) {
	rest, gql, tdb, tok, done := setupSC(t)
	defer done()
	super := tok("super_admin", superID)

	newItem := func(body map[string]any) string {
		t.Helper()
		got := dpDo(t, rest, "POST", "/api/items", super, body, http.StatusCreated)
		return got["id"].(string)
	}

	t.Run("updateItem updates provided fields (partial)", func(t *testing.T) {
		id := newItem(map[string]any{"name": "x", "status": "open", "qty": 1})
		data, errs := gqlDo(t, gql, super,
			`mutation { updateItem(id: "`+id+`", input: { status: "done" }) { id status qty } }`)
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		upd := data["updateItem"].(map[string]any)
		if upd["status"] != "done" {
			t.Errorf("status not updated: %v", upd["status"])
		}
		if upd["qty"].(float64) != 1 {
			t.Errorf("partial update changed an unset field: %v", upd["qty"])
		}
	})

	t.Run("GraphQL update == REST PATCH", func(t *testing.T) {
		idA := newItem(map[string]any{"name": "A", "status": "open", "qty": 2})
		idB := newItem(map[string]any{"name": "B", "status": "open", "qty": 2})
		patched := dpDo(t, rest, "PATCH", "/api/items/"+idA, super, map[string]any{"status": "done", "qty": 8}, http.StatusOK)
		data, errs := gqlDo(t, gql, super,
			`mutation { updateItem(id: "`+idB+`", input: { status: "done", qty: 8 }) { status qty } }`)
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		upd := data["updateItem"].(map[string]any)
		if patched["status"] != upd["status"] || patched["qty"].(float64) != upd["qty"].(float64) {
			t.Errorf("GraphQL update diverged from REST PATCH: rest=%v gql=%v", patched, upd)
		}
	})

	t.Run("RBAC field allowlist: field outside allowlist is not updated", func(t *testing.T) {
		id := newItem(map[string]any{"name": "fl", "status": "open", "qty": 3})
		fl := tok("fieldlimited", superID)
		// qty is not in fieldlimited's allowlist → silently dropped; status updates.
		data, errs := gqlDo(t, gql, fl,
			`mutation { updateItem(id: "`+id+`", input: { status: "done", qty: 99 }) { id status } }`)
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		// Read back qty as super_admin to confirm it did NOT change.
		got := dpDo(t, rest, "GET", "/api/items/"+id, super, nil, http.StatusOK)
		if got["qty"].(float64) != 3 {
			t.Errorf("qty outside allowlist must not change: got %v", got["qty"])
		}
		if got["status"] != "done" {
			t.Errorf("allowed field status should have updated: %v", got["status"])
		}
		_ = data
	})

	t.Run("RBAC row condition: cannot update a row outside the role's scope", func(t *testing.T) {
		// Row owned by scOwnerID; a different owner principal must not update it.
		id := newItem(map[string]any{"name": "owned", "status": "open", "owner_id": scOwnerID})
		other := tok("owner", "dddd0000-0000-0000-0000-000000000004")
		data, errs := gqlDo(t, gql, other,
			`mutation { updateItem(id: "`+id+`", input: { status: "hacked" }) { id } }`)
		if len(errs) == 0 {
			t.Fatalf("expected an error updating a row outside scope, got data=%v", data)
		}
		got := dpDo(t, rest, "GET", "/api/items/"+id, super, nil, http.StatusOK)
		if got["status"] != "open" {
			t.Errorf("row outside scope must remain unchanged: %v", got["status"])
		}
	})

	t.Run("events:[update] → GraphQL update emits to the outbox", func(t *testing.T) {
		id := newItem(map[string]any{"name": "ev", "status": "open"})
		before := countOutbox(t, tdb, "items.updated")
		_, errs := gqlDo(t, gql, super,
			`mutation { updateItem(id: "`+id+`", input: { status: "done" }) { id } }`)
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		after := countOutbox(t, tdb, "items.updated")
		if after != before+1 {
			t.Errorf("GraphQL update must emit items.updated like REST: before=%d after=%d", before, after)
		}
	})
}

func countOutbox(t *testing.T, tdb *db.TenantDB, topic string) int {
	t.Helper()
	// public.outbox is not tenant-scoped; query it directly via a tenant tx on the
	// public schema (QueryTenant sets search_path to <tenant>,public).
	n, err := tdb.QueryScalarTenant(context.Background(), "tenant_"+tenantID,
		"SELECT count(*) FROM public.outbox WHERE topic = $1 AND tenant_id = $2", topic, tenantID)
	if err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return int(n)
}
