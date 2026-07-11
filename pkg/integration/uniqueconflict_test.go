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

// G6 (FIX-G1-G6): a unique-constraint collision on the schema-derived CRUD path
// must surface as a 409 conflict (REST) / a clean error (GraphQL), NOT the masked
// 500 it used to. Covers BOTH a field-level `unique:true` and a composite unique
// `index`, on create (REST POST + GraphQL createX) and update (REST PATCH).
func ucSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "uniqueconflict",
		Resources: map[string]schema.ResourceSchema{
			"widgets": {
				Fields: map[string]schema.FieldDef{
					"code":  {Type: "string", Required: true, Unique: true}, // field-level unique
					"label": {Type: "string"},
					"slot":  {Type: "int"},
					"bin":   {Type: "int"},
				},
				Indexes: []schema.IndexDef{
					{Fields: []string{"slot", "bin"}, Unique: true}, // composite unique index
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func setupUC(t *testing.T) (*httptest.Server, http.Handler, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("uniqueconflict: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := ucSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "UC Co", Email: "u@u.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))

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
	return rest, gqlWrap, genToken, func() { rest.Close(); cleanPG() }
}

func TestUniqueConflict_REST_Create(t *testing.T) {
	rest, _, tok, done := setupUC(t)
	defer done()
	super := tok("super_admin", superID)

	// field-level unique:true on `code`
	dpDo(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "A", "slot": 1, "bin": 1}, http.StatusCreated)
	code, body := dpStatusBody(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "A", "slot": 2, "bin": 2})
	if code != http.StatusConflict {
		t.Fatalf("field-unique dup: want 409, got %d (%s)", code, body)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("field-unique 409 body not consumable: %s", body)
	}
	if strings.Contains(body, "SQLSTATE") || strings.Contains(strings.ToLower(body), "pq:") {
		t.Errorf("409 body leaks raw DB internals: %s", body)
	}

	// composite unique index on (slot, bin)
	code, body = dpStatusBody(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "B", "slot": 1, "bin": 1})
	if code != http.StatusConflict {
		t.Fatalf("composite-index dup: want 409, got %d (%s)", code, body)
	}

	// a non-conflicting create still succeeds (success path unaffected)
	dpDo(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "C", "slot": 9, "bin": 9}, http.StatusCreated)
}

func TestUniqueConflict_REST_UpdateRegression(t *testing.T) {
	rest, _, tok, done := setupUC(t)
	defer done()
	super := tok("super_admin", superID)

	dpDo(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "X", "slot": 1, "bin": 1}, http.StatusCreated)
	w2 := dpDo(t, rest, "POST", "/api/widgets", super, map[string]any{"code": "Y", "slot": 2, "bin": 2}, http.StatusCreated)
	id2, _ := w2["id"].(string)

	// PATCH w2.code to the value already held by w1 → 409 (update path, regression).
	code, body := dpStatusBody(t, rest, "PATCH", "/api/widgets/"+id2, super, map[string]any{"code": "X"})
	if code != http.StatusConflict {
		t.Fatalf("update to a taken unique value: want 409, got %d (%s)", code, body)
	}
}

func TestUniqueConflict_GraphQL_Create(t *testing.T) {
	_, gql, tok, done := setupUC(t)
	defer done()
	super := tok("super_admin", superID)

	if _, errs := gqlDo(t, gql, super, `mutation { createWidget(input:{code:"G", slot:5, bin:5}) { id code } }`); len(errs) != 0 {
		t.Fatalf("first GraphQL create should succeed, got errors: %v", errs)
	}
	// Same code again → unique violation surfaced as a clean error in errors[].
	_, errs := gqlDo(t, gql, super, `mutation { createWidget(input:{code:"G", slot:6, bin:6}) { id code } }`)
	if len(errs) == 0 {
		t.Fatal("duplicate GraphQL create should return an error")
	}
	msg := errMsg(errs[0])
	if !strings.Contains(msg, "already exists") {
		t.Errorf("GraphQL conflict error not consumable: %s", msg)
	}
	if strings.Contains(msg, "SQLSTATE") || strings.Contains(strings.ToLower(msg), "pq:") {
		t.Errorf("GraphQL conflict error leaks raw DB internals: %s", msg)
	}
}

// errMsg pulls the "message" out of a GraphQL error object.
func errMsg(e any) string {
	if m, ok := e.(map[string]any); ok {
		if s, ok := m["message"].(string); ok {
			return s
		}
	}
	return ""
}
