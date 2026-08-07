package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/extensions"
	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	"github.com/appximo/appximo/pkg/migration"
	"github.com/appximo/appximo/pkg/query"
	rbacpkg "github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/jackc/pgx/v5/pgxpool"
)

const authTestSecret = "auth-security-test-secret"

// gqlPost sends a POST to /graphql with an optional Bearer token.
func gqlPost(t *testing.T, srv *httptest.Server, body []byte, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

// buildGQLSrv creates a full JWT+Tenant+GraphQL test server for the given pool and tenantID.
func buildGQLSrv(t *testing.T, pool *pgxpool.Pool, tenantID, pgSchema string, s *schema.APISchema) *httptest.Server {
	t.Helper()
	ctx := context.Background()

	if pgSchema != "" {
		pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgSchema)
		if err := migration.ApplyTenantMigration(ctx, pool, pgSchema, s); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}

	policyJSON, _ := json.Marshal(s.RBAC)
	var policy rbacpkg.Policy
	json.Unmarshal(policyJSON, &policy)

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	h := gqlhandler.BuildHandler(s, tdb, hr, &policy, nil, false)
	h = auth.JWTMiddleware(authTestSecret)(h)
	h = tenant.TenantMiddleware(h)

	host := tenantID + ".localhost"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── 1. /graphql sin JWT → 401 ─────────────────────────────────────────────────

func TestSecurity_GraphQL_NoJWT_Returns401(t *testing.T) {
	bare := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := auth.JWTMiddleware(authTestSecret)(bare)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "myco.localhost"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	body, _ := json.Marshal(map[string]any{"query": `{ items { data { id } } }`})
	resp := gqlPost(t, srv, body, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without JWT, got %d", resp.StatusCode)
	}
}

// ── 2. /graphql con JWT de otro tenant → 401 ─────────────────────────────────

func TestSecurity_GraphQL_WrongTenant_Returns401(t *testing.T) {
	bare := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := auth.JWTMiddleware(authTestSecret)(bare)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "beta.localhost" // tenant is "beta"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	// Token TenantID = "alpha" ≠ "beta" → must be rejected.
	tok, _ := auth.GenerateToken(auth.Claims{UserID: "u1", Role: "admin", TenantID: "alpha"}, authTestSecret)
	body, _ := json.Marshal(map[string]any{"query": `{ items { data { id } } }`})
	resp := gqlPost(t, srv, body, tok)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong-tenant JWT, got %d", resp.StatusCode)
	}
}

// ── 3. /graphql con JWT correcto → 200 ───────────────────────────────────────

func TestSecurity_GraphQL_CorrectJWT_Returns200(t *testing.T) {
	bare := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":{}}`))
	})
	h := auth.JWTMiddleware(authTestSecret)(bare)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "myco.localhost"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	tok, _ := auth.GenerateToken(auth.Claims{UserID: "u1", Role: "admin", TenantID: "myco"}, authTestSecret)
	body, _ := json.Marshal(map[string]any{"query": `{ items { data { id } } }`})
	resp := gqlPost(t, srv, body, tok)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with valid JWT, got %d", resp.StatusCode)
	}
}

// ── 4. getByID con rol restringido → solo campos permitidos ──────────────────

func TestSecurity_GetByID_FieldStripping(t *testing.T) {
	if testing.Short() {
		t.Skip("field stripping: skipping in -short mode (requires Docker)")
	}
	pool, cleanup := startPG(t)
	defer cleanup()
	applyControlPlane(t, pool)

	s := &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "sec",
		Resources: map[string]schema.ResourceSchema{
			"items": {Fields: map[string]schema.FieldDef{
				"name":   {Type: "string", Required: true},
				"secret": {Type: "string"},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"admin":    {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			"readonly": {Resources: json.RawMessage(`["items"]`), Actions: []string{"read"}, Fields: []string{"name"}},
		}},
	}

	srv := buildGQLSrv(t, pool, "fieldstrip", "tenant_fieldstrip", s)

	adminTok, _ := auth.GenerateToken(auth.Claims{UserID: "adm", Role: "admin", TenantID: "fieldstrip"}, authTestSecret)
	roTok, _ := auth.GenerateToken(auth.Claims{UserID: "ro", Role: "readonly", TenantID: "fieldstrip"}, authTestSecret)

	// Create an item as admin.
	createQ := `mutation { createItem(input: { name: "visible", secret: "hidden" }) { id } }`
	body, _ := json.Marshal(map[string]any{"query": createQ})
	resp := gqlPost(t, srv, body, adminTok)
	var cr map[string]any
	json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()
	crData, _ := cr["data"].(map[string]any)
	created, _ := crData["createItem"].(map[string]any)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create item failed: %+v", cr)
	}

	// Query by ID as readonly — "secret" must be absent.
	byIDQ := `{ item(id: "` + id + `") { name secret } }`
	body2, _ := json.Marshal(map[string]any{"query": byIDQ})
	resp2 := gqlPost(t, srv, body2, roTok)
	var gr map[string]any
	json.NewDecoder(resp2.Body).Decode(&gr)
	resp2.Body.Close()
	gdata, _ := gr["data"].(map[string]any)
	item, _ := gdata["item"].(map[string]any)
	if _, hasSecret := item["secret"]; hasSecret {
		t.Errorf("field stripping failed: readonly role received 'secret' field via getByID")
	}
	if item["name"] != "visible" {
		t.Errorf("expected name=visible, got %v", item["name"])
	}
}

// ── 5. page=999999999 → clamped a MaxPage ────────────────────────────────────

func TestSecurity_PageClamped(t *testing.T) {
	res := &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{"name": {Type: "string"}},
	}
	params := url.Values{}
	params.Set("page", "999999999")

	qb, err := query.BuildQuery("items", res, params, nil, nil)
	if err != nil {
		t.Fatalf("BuildQuery: %v", err)
	}
	if qb.Page() != query.MaxPage {
		t.Errorf("expected page clamped to %d, got %d", query.MaxPage, qb.Page())
	}
}

// ── 6. __schema query en producción → error ───────────────────────────────────

func TestSecurity_Introspection_DisabledInProd(t *testing.T) {
	s := minimalSchema()
	policyJSON, _ := json.Marshal(s.RBAC)
	var policy rbacpkg.Policy
	json.Unmarshal(policyJSON, &policy)

	// nil tdb/hr are safe here: handler exits before any resolver fires.
	// allowIntrospection=false — the caller's resolved gate (app.go: dev or the
	// APPXIMO_GRAPHQL_PLAYGROUND opt-in), exercised directly rather than via
	// env vars now that BuildHandler takes it as an explicit parameter.
	h := gqlhandler.BuildHandler(s, nil, nil, &policy, nil, false)
	h = auth.JWTMiddleware(authTestSecret)(h)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "sec.localhost"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	tok, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "sec"}, authTestSecret)
	body, _ := json.Marshal(map[string]any{"query": `{ __schema { types { name } } }`})
	resp := gqlPost(t, srv, body, tok)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 envelope, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	errs, _ := result["errors"].([]any)
	if len(errs) == 0 {
		t.Fatal("expected introspection error in production, got none")
	}
	msg, _ := errs[0].(map[string]any)["message"].(string)
	if !strings.Contains(msg, "introspection disabled") {
		t.Errorf("expected 'introspection disabled' message, got %q", msg)
	}
}

// ── 6b. the explicit opt-in allows introspection outside dev ────────────────
// (GRAPHQL-EXPLORER-S1) — app.go resolves allowIntrospection as
// Env=="development" || cfg.GraphQLPlayground || APPXIMO_GRAPHQL_PLAYGROUND
// (per-app in the in-process fleet, see multiapp.go buildFleetApp), then
// passes the resolved bool into BuildHandler. This exercises the "opted in"
// value directly — off by default, see the prod test above.
func TestSecurity_Introspection_EnabledWhenExplicitlyAllowed(t *testing.T) {
	s := minimalSchema()
	policyJSON, _ := json.Marshal(s.RBAC)
	var policy rbacpkg.Policy
	json.Unmarshal(policyJSON, &policy)

	h := gqlhandler.BuildHandler(s, nil, nil, &policy, nil, true)
	h = auth.JWTMiddleware(authTestSecret)(h)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "sec.localhost"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	tok, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "sec"}, authTestSecret)
	body, _ := json.Marshal(map[string]any{"query": `{ __schema { types { name } } }`})
	resp := gqlPost(t, srv, body, tok)
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if errs, _ := result["errors"].([]any); len(errs) > 0 {
		t.Fatalf("introspection should be allowed with APPXIMO_GRAPHQL_PLAYGROUND=on, got errors: %v", errs)
	}
	data, _ := result["data"].(map[string]any)
	if data["__schema"] == nil {
		t.Fatal("expected __schema data in the response")
	}
}

// ── 7. body de 2 MB → 413 ────────────────────────────────────────────────────

func TestSecurity_LargeBody_Returns413(t *testing.T) {
	s := minimalSchema()
	policyJSON, _ := json.Marshal(s.RBAC)
	var policy rbacpkg.Policy
	json.Unmarshal(policyJSON, &policy)

	h := gqlhandler.BuildHandler(s, nil, nil, &policy, nil, false)
	h = auth.JWTMiddleware(authTestSecret)(h)
	h = tenant.TenantMiddleware(h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = "sec.localhost"
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	tok, _ := auth.GenerateToken(auth.Claims{UserID: "u", Role: "admin", TenantID: "sec"}, authTestSecret)

	// 2 MB payload embedded inside a JSON string field.
	filler := bytes.Repeat([]byte("a"), 2<<20)
	bigBody := append([]byte(`{"query":"`), filler...)
	bigBody = append(bigBody, '"', '}')

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for 2MB body, got %d", resp.StatusCode)
	}
}

// ── 8. search con % → escapado correctamente ─────────────────────────────────

func TestSecurity_SearchEscapesWildcards(t *testing.T) {
	res := &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{"name": {Type: "string"}},
	}
	params := url.Values{}
	params.Set("search", "50%_off")

	qb, err := query.BuildQuery("items", res, params, nil, nil)
	if err != nil {
		t.Fatalf("BuildQuery: %v", err)
	}
	_, _, selectArgs, _ := qb.SQL()

	// Verify the search arg contains escaped wildcards.
	found := false
	for _, arg := range selectArgs {
		s, ok := arg.(string)
		if ok && strings.Contains(s, `\%`) && strings.Contains(s, `\_`) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected search arg to contain \\%% and \\_, got args: %v", selectArgs)
	}
}
