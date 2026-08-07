//go:build integration

// PUBLIC-SURFACE-S1 (ADR-026): the declarative anonymous surface. A schema's
// rbac.public block exposes declared resources READ-ONLY to unauthenticated
// requests — the evaluator's blog scenario: list published articles with no
// token, no Go — while everything NOT declared stays exactly as denied as
// before, on every surface (REST, GraphQL, aggregate, filters, sort, search).
// Reuses the shared Postgres container from TestMain in
// library_integration_test.go.
package appximo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

func publicSurfaceFixturePath() string {
	return filepath.Join(helpers.RepoRoot(), "tests", "fixtures", "schemas", "publicsurface.json")
}

func newPublicSurfaceApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: publicSurfaceFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New(publicsurface): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func loadPublicSurfaceSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(publicSurfaceFixturePath())
	if err != nil {
		t.Fatalf("load publicsurface fixture: %v", err)
	}
	return s
}

// seedBlog creates one published and one draft article as admin, returning
// their ids.
func seedBlog(t *testing.T, srv *httptest.Server, host, admin string) (publishedID, draftID string) {
	t.Helper()
	resp := do(t, srv, "POST", "/api/articulos", host, admin,
		`{"titulo":"hola mundo","cuerpo":"visible","estado":"publicado","notas_internas":"secreto editorial"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed published: want 201, got %d", resp.StatusCode)
	}
	publishedID = decode(t, resp)["id"].(string)
	resp = do(t, srv, "POST", "/api/articulos", host, admin,
		`{"titulo":"borrador oculto","cuerpo":"no publicado","estado":"borrador","notas_internas":"wip"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed draft: want 201, got %d", resp.StatusCode)
	}
	draftID = decode(t, resp)["id"].(string)
	return publishedID, draftID
}

func TestPublicSurface_AnonymousReads(t *testing.T) {
	const tn = "pubblog"
	helpers.RegisterTenant(t, itPool, tn, loadPublicSurfaceSchema(t))
	srv := newPublicSurfaceApp(t)
	const host = tn + ".localhost"
	admin := helpers.GenToken(t, "admin", csecAdmin, tn)
	pubID, draftID := seedBlog(t, srv, host, admin)

	// 1. Anonymous list: 200, ONLY the published row, ONLY the allowlisted fields.
	resp := do(t, srv, "GET", "/api/articulos", host, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anon list: want 200, got %d", resp.StatusCode)
	}
	body := decode(t, resp)
	rows, _ := body["data"].([]any)
	if len(rows) != 1 {
		t.Fatalf("anon list must contain ONLY the published article, got %d rows", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["id"] != pubID || row["titulo"] != "hola mundo" {
		t.Fatalf("anon list row = %v", row)
	}
	if _, leaked := row["notas_internas"]; leaked {
		t.Fatal("anon list leaked a field outside the public allowlist")
	}

	// 2. Anonymous get-by-id: the published row is served; the draft is a 404
	// (excluded by the row condition — never a 403 that confirms existence).
	if resp := do(t, srv, "GET", "/api/articulos/"+pubID, host, "", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("anon get published: want 200, got %d", resp.StatusCode)
	}
	if resp := do(t, srv, "GET", "/api/articulos/"+draftID, host, "", ""); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anon get draft: want 404, got %d", resp.StatusCode)
	}

	// 3. The aggregate is scoped by the same condition: count = 1, not 2.
	resp = do(t, srv, "GET", "/api/articulos/aggregate?count", host, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anon aggregate: want 200, got %d", resp.StatusCode)
	}
	if n, _ := decode(t, resp)["count"].(float64); n != 1 {
		t.Fatalf("anon aggregate count = %v, want 1 (only published rows)", n)
	}

	// 4. Filtering/sorting by an allowlisted field works.
	resp = do(t, srv, "GET", "/api/articulos?filter[titulo][partial]=hola&sort=titulo", host, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anon filter+sort on allowed field: want 200, got %d", resp.StatusCode)
	}
}

func TestPublicSurface_DenyByDefaultHolds(t *testing.T) {
	const tn = "pubdeny"
	helpers.RegisterTenant(t, itPool, tn, loadPublicSurfaceSchema(t))
	srv := newPublicSurfaceApp(t)
	const host = tn + ".localhost"
	admin := helpers.GenToken(t, "admin", csecAdmin, tn)
	pubID, _ := seedBlog(t, srv, host, admin)

	// 1. A resource NOT in the public block is denied to anonymous callers.
	if resp := do(t, srv, "GET", "/api/suscriptores", host, "", ""); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("anon read of undeclared resource: want 403, got %d", resp.StatusCode)
	}
	// 2. Anonymous writes are denied everywhere (read-only surface).
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/articulos", `{"titulo":"spam"}`},
		{"PATCH", "/api/articulos/" + pubID, `{"titulo":"defaced"}`},
		{"DELETE", "/api/articulos/" + pubID, ""},
		{"POST", "/api/transaction", `{"operations":[{"op":"create","resource":"articulos","data":{"titulo":"x"}}]}`},
	} {
		if resp := do(t, srv, c.method, c.path, host, "", c.body); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("anon %s %s: want 403, got %d", c.method, c.path, resp.StatusCode)
		}
	}
	// 3. SEC-5: a field OUTSIDE the public allowlist cannot be named — not as a
	// filter (a value oracle), not as sort/order (an ordering oracle), not as
	// an aggregate metric.
	for _, path := range []string{
		"/api/articulos?filter[notas_internas][partial]=secreto",
		"/api/articulos?sort=notas_internas",
		"/api/articulos?order[notas_internas]=desc",
		"/api/articulos/aggregate?count&group_by=notas_internas",
	} {
		if resp := do(t, srv, "GET", path, host, "", ""); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("anon %s: want 403 (hidden field named), got %d", path, resp.StatusCode)
		}
	}
	// 4. Search must not probe the hidden text column: the term only present in
	// notas_internas matches nothing.
	resp := do(t, srv, "GET", "/api/articulos?search=secreto", host, "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("anon search: want 200, got %d", resp.StatusCode)
	}
	if rows, _ := decode(t, resp)["data"].([]any); len(rows) != 0 {
		t.Fatalf("anon search swept a hidden column: got %d rows", len(rows))
	}
	// 5. An INVALID bearer is a 401, never a silent downgrade to anonymous.
	if resp := do(t, srv, "GET", "/api/articulos", host, "garbage.token.here", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token: want 401, got %d", resp.StatusCode)
	}
	// 6. Authenticated surfaces are untouched: admin still sees both rows and
	// every field.
	resp = do(t, srv, "GET", "/api/articulos?sort=titulo", host, admin, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list: want 200, got %d", resp.StatusCode)
	}
	if rows, _ := decode(t, resp)["data"].([]any); len(rows) != 2 {
		t.Fatalf("admin list rows = %d, want 2", len(rows))
	}
}

// anonGqlReq is gqlReq WITHOUT the Authorization header (gqlReq always sends
// "Bearer <token>", and an empty Bearer is an unsupported-scheme 401 by design
// — anonymity means NO header, never an empty one).
func anonGqlReq(t *testing.T, srv *httptest.Server, host, query string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("graphql request: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m) //nolint:errcheck
	return m
}

func TestPublicSurface_GraphQLParity(t *testing.T) {
	const tn = "pubgql"
	helpers.RegisterTenant(t, itPool, tn, loadPublicSurfaceSchema(t))
	srv := newPublicSurfaceApp(t)
	const host = tn + ".localhost"
	admin := helpers.GenToken(t, "admin", csecAdmin, tn)
	seedBlog(t, srv, host, admin)

	// Anonymous GraphQL read: only the published article, hidden field scrubbed.
	res := anonGqlReq(t, srv, host, `{ articulos { data { id titulo estado } } }`)
	if gqlHasError(res) {
		t.Fatalf("anon graphql read errored: %v", res["errors"])
	}
	data := res["data"].(map[string]any)["articulos"].(map[string]any)["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("anon graphql rows = %d, want 1 (published only)", len(data))
	}
	// Naming the hidden field in the selection is an error (unfetchable), and
	// mutations are denied.
	if res := anonGqlReq(t, srv, host, `{ articulos { data { id notas_internas } } }`); !gqlHasError(res) {
		got := res["data"].(map[string]any)["articulos"].(map[string]any)["data"].([]any)
		for _, r := range got {
			if _, leaked := r.(map[string]any)["notas_internas"]; leaked && r.(map[string]any)["notas_internas"] != nil {
				t.Fatalf("anon graphql leaked notas_internas: %v", r)
			}
		}
	}
	if res := anonGqlReq(t, srv, host, `mutation { createArticulo(input:{titulo:"spam"}) { id } }`); !gqlHasError(res) {
		t.Fatal("anon graphql mutation must be denied")
	}
	// The undeclared resource is denied.
	if res := anonGqlReq(t, srv, host, `{ suscriptores { data { id } } }`); !gqlHasError(res) {
		t.Fatal("anon graphql read of undeclared resource must be denied")
	}
}

// TestPublicSurface_NoBlockMeansNoChange pins the opt-in: a schema WITHOUT
// rbac.public keeps the exact tokenless behavior (401) — the anonymous path
// can only exist because the block does.
func TestPublicSurface_NoBlockMeansNoChange(t *testing.T) {
	const tn = "pubnone"
	helpers.RegisterTenant(t, itPool, tn, loadCreateSecSchema(t))
	srv := newCreateSecApp(t)
	resp := do(t, srv, "GET", "/api/records", tn+".localhost", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no public block: tokenless must stay 401, got %d", resp.StatusCode)
	}
}

// TestPublicRoleNamePinned keeps the two PublicRoleName constants — schema
// (validation) and rbac (evaluation) — identical; they live in packages that
// must not import each other.
func TestPublicRoleNamePinned(t *testing.T) {
	if rbac.PublicRoleName != schema.PublicRoleName {
		t.Fatalf("rbac.PublicRoleName %q != schema.PublicRoleName %q", rbac.PublicRoleName, schema.PublicRoleName)
	}
	if !strings.HasPrefix(rbac.PublicRoleName, "$") {
		t.Fatal("PublicRoleName must stay outside the schema role-name alphabet")
	}
}
