package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// sec2Schema: a notes resource whose before_create JS hook stamps the row with the
// ACTOR from the hook's `user` binding — proving SEC-AUDIT-V2 Hallazgo B (the before
// hook now sees who performed the operation; it used to be nil).
func sec2Schema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "sec2",
		Resources: map[string]schema.ResourceSchema{
			"notes": {
				Fields: map[string]schema.FieldDef{
					"text":  {Type: "string", Required: true},
					"actor": {Type: "string"},
				},
				Hooks: map[string]schema.HookConfig{
					"before_create": {
						Type:   "js",
						Script: "if (user && user.user_id) { data.actor = user.user_id; } else { result.proceed = false; result.error = 'no actor in hook context'; }",
					},
				},
			},
		},
		RBAC: schema.RBACPolicy{
			Roles: map[string]schema.RolePolicy{
				"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			},
		},
	}
}

// TestBeforeHookActorContext (Hallazgo B): a before_create hook reads user.user_id
// (the JWT subject) and stamps it onto the row. Before the fix the hook's `user` was
// nil and the create would have been rejected by the hook.
func TestBeforeHookActorContext(t *testing.T) {
	if testing.Short() {
		t.Skip("sec2: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := sec2Schema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Sec2", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	defer srv.Close()
	tok := genToken("super_admin", superID)

	got := dpDo(t, srv, "POST", "/api/notes", tok, map[string]any{"text": "hello"}, http.StatusCreated)
	if got["actor"] != superID {
		t.Fatalf("before-hook should have stamped actor=%s from user.user_id, got %v", superID, got["actor"])
	}
}

// buildGQLServer stands up the /graphql endpoint with the same chain as the engine
// (tenant → jwt → rbac), mirroring TestRelationsGraphQL.
func buildGQLServer(t *testing.T, s *schema.APISchema, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	gqlH := gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, events.NewHub(0))
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(policyJSON))
	mux.Handle("/graphql", gqlH)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	}))
}

// TestGraphQLLazyCount (Hallazgo C): a list selecting only data succeeds without a
// COUNT; selecting meta.total returns the real total. (The "no COUNT by default" is
// guaranteed structurally — total/total_pages are lazy field resolvers — and unit-
// tested in pkg/graphql; here we prove the end-to-end behavior is correct.)
func TestGraphQLLazyCount(t *testing.T) {
	if testing.Short() {
		t.Skip("sec2 gql: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := sec2Schema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Sec2", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	defer rest.Close()
	tok := genToken("super_admin", superID)
	for _, txt := range []string{"a", "b", "c"} {
		dpDo(t, rest, "POST", "/api/notes", tok, map[string]any{"text": txt}, http.StatusCreated)
	}

	gsrv := buildGQLServer(t, s, pool)
	defer gsrv.Close()

	// 1. Default list — data only, no total selected → must succeed (no COUNT).
	def := gqlQuery(t, gsrv, tok, `{"query":"{ notes { data { id text } } }"}`)
	notes := def["data"].(map[string]any)["notes"].(map[string]any)
	if len(notes["data"].([]any)) != 3 {
		t.Fatalf("expected 3 notes, got %v", notes["data"])
	}

	// 2. Selecting meta.total → COUNT runs, returns the real total.
	withTotal := gqlQuery(t, gsrv, tok, `{"query":"{ notes { data { id } meta { total total_pages has_next } } }"}`)
	meta := withTotal["data"].(map[string]any)["notes"].(map[string]any)["meta"].(map[string]any)
	if int(meta["total"].(float64)) != 3 {
		t.Fatalf("expected meta.total=3, got %v", meta["total"])
	}
	if int(meta["total_pages"].(float64)) != 1 {
		t.Fatalf("expected meta.total_pages=1, got %v", meta["total_pages"])
	}
}

func gqlQuery(t *testing.T, srv *httptest.Server, token, body string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/graphql", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("graphql: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errs, ok := out["errors"]; ok && errs != nil {
		t.Fatalf("graphql errors: %v", errs)
	}
	return out
}
