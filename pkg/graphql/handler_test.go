package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/extensions"
	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const gqlTestSecret = "gql-test-secret-1234"

func startPostgres(t *testing.T) (string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}
	return connStr, func() { ctr.Terminate(ctx) }
}

// withFullStack wraps handler with TenantMiddleware → JWTMiddleware and injects the host.
func withFullStack(handler http.Handler, host, jwtSecret string) http.Handler {
	h := auth.JWTMiddleware(jwtSecret)(handler)
	h = tenant.TenantMiddleware(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		h.ServeHTTP(w, r)
	})
}

// gqlDo sends a GraphQL POST request with a Bearer token and returns the decoded response map.
// The caller is responsible for ensuring the HTTP status is 200; gqlDo fatals if it is not.
func gqlDo(t *testing.T, srv *httptest.Server, query, token string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("gqlDo request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result
}

// firstError extracts the first error message from a GraphQL response.
func firstError(result map[string]any) string {
	errs, _ := result["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	msg, _ := errs[0].(map[string]any)["message"].(string)
	return msg
}

func TestGraphQL(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE SCHEMA tenant_gql`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE tenant_gql.guides (
			id          UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code        TEXT NOT NULL,
			status      TEXT DEFAULT 'pending',
			origin      TEXT NOT NULL,
			destination TEXT NOT NULL,
			created_at  TIMESTAMPTZ DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"guides": {Fields: map[string]schema.FieldDef{
				"code":        {Type: "string", Required: true},
				"status":      {Type: "string"},
				"origin":      {Type: "string", Required: true},
				"destination": {Type: "string", Required: true},
				"created_at":  {Type: "time", Auto: schema.AutoLegacy},
			}},
		},
	}

	var policy rbac.Policy
	json.Unmarshal([]byte(`{
		"roles": {
			"super_admin": {"resources": "*", "actions": ["*"]},
			"public":      {"resources": ["guides"], "actions": ["read"]}
		}
	}`), &policy)

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	handler := gqlhandler.BuildHandler(testSchema, tdb, hr, &policy, nil, false)
	// host subdomain "gql" must match TenantID in tokens below
	srv := httptest.NewServer(withFullStack(handler, "gql.localhost", gqlTestSecret))
	defer srv.Close()

	// Pre-generate tokens; TenantID must match "gql" (subdomain of gql.localhost)
	adminToken, _ := auth.GenerateToken(auth.Claims{UserID: "u1", Role: "super_admin", TenantID: "gql"}, gqlTestSecret)
	publicToken, _ := auth.GenerateToken(auth.Claims{UserID: "u2", Role: "public", TenantID: "gql"}, gqlTestSecret)

	// track the ID created in CreateMutation for subsequent subtests
	var createdID string

	// ── 1. Query empty list ───────────────────────────────────────────────────
	t.Run("QueryList", func(t *testing.T) {
		result := gqlDo(t, srv, `{ guides { data { id code } meta { total } } }`, adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		guides := data["guides"].(map[string]any)
		dataArr := guides["data"].([]any)
		meta := guides["meta"].(map[string]any)
		if len(dataArr) != 0 {
			t.Errorf("expected 0 items, got %d", len(dataArr))
		}
		if int(meta["total"].(float64)) != 0 {
			t.Errorf("expected total=0, got %v", meta["total"])
		}
	})

	// ── 2. Mutation createGuide ───────────────────────────────────────────────
	t.Run("CreateMutation", func(t *testing.T) {
		result := gqlDo(t, srv, `mutation {
			createGuide(input: {code: "G-001", origin: "Bogota", destination: "Medellin"}) {
				id code status
			}
		}`, adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		created := data["createGuide"].(map[string]any)
		if created["code"] != "G-001" {
			t.Errorf("expected code G-001, got %v", created["code"])
		}
		if created["id"] == nil || created["id"] == "" {
			t.Error("expected non-empty id in response")
		}
		createdID, _ = created["id"].(string)
	})

	// ── 3. Query by ID ────────────────────────────────────────────────────────
	t.Run("QueryByID", func(t *testing.T) {
		if createdID == "" {
			t.Skip("skipped: no guide ID from CreateMutation")
		}
		result := gqlDo(t, srv, fmt.Sprintf(`{ guide(id: "%s") { id code } }`, createdID), adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		guide := data["guide"].(map[string]any)
		if guide["id"] != createdID {
			t.Errorf("expected id %s, got %v", createdID, guide["id"])
		}
		if guide["code"] != "G-001" {
			t.Errorf("expected code G-001, got %v", guide["code"])
		}
	})

	// ── 4. Query with filter ──────────────────────────────────────────────────
	t.Run("Filter", func(t *testing.T) {
		pool.Exec(ctx, `INSERT INTO tenant_gql.guides (code, origin, destination) VALUES ('G-002', 'Cali', 'Bogota')`)

		result := gqlDo(t, srv,
			`{ guides(filter: { code: { exact: "G-001" } }) { data { code } meta { total } } }`,
			adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		guides := data["guides"].(map[string]any)
		dataArr := guides["data"].([]any)
		if len(dataArr) != 1 {
			t.Errorf("expected 1 guide after filter, got %d", len(dataArr))
		}
		if len(dataArr) > 0 {
			row := dataArr[0].(map[string]any)
			if row["code"] != "G-001" {
				t.Errorf("expected code G-001, got %v", row["code"])
			}
		}
	})

	// ── 5. Mutation deleteGuide ───────────────────────────────────────────────
	t.Run("DeleteMutation", func(t *testing.T) {
		if createdID == "" {
			t.Skip("skipped: no guide ID from CreateMutation")
		}
		result := gqlDo(t, srv, fmt.Sprintf(`mutation { deleteGuide(id: "%s") }`, createdID), adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		deleted, _ := data["deleteGuide"].(bool)
		if !deleted {
			t.Error("expected deleteGuide to return true")
		}

		result2 := gqlDo(t, srv, fmt.Sprintf(`{ guide(id: "%s") { id } }`, createdID), adminToken)
		if msg := firstError(result2); msg != "" {
			t.Fatalf("unexpected error after delete: %s", msg)
		}
		data2 := result2["data"].(map[string]any)
		if data2["guide"] != nil {
			t.Errorf("expected null after delete, got %v", data2["guide"])
		}
	})

	// ── 6. Missing token → 401 at HTTP level ─────────────────────────────────
	t.Run("MissingToken", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"query": `{ guides { data { id } } }`})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401 without token, got %d", resp.StatusCode)
		}
	})

	// ── 7. Role without permission → error "forbidden" ────────────────────────
	t.Run("Forbidden", func(t *testing.T) {
		result := gqlDo(t, srv, `mutation {
			createGuide(input: {code: "G-FBD", origin: "X", destination: "Y"}) { id }
		}`, publicToken)
		msg := firstError(result)
		if msg != "forbidden" {
			t.Errorf("expected 'forbidden' error, got %q", msg)
		}
	})
}

// TestGraphQLValidation proves the S44 declarative validator runs on GraphQL
// create mutations with the SAME compiled rules as REST, surfacing violations
// in GraphQL form: errors[0].message == "validation_failed" and
// extensions.fields == [{field,rule,message}...] (all violations, one response).
// NOTE: the engine has no update mutation (REST PUT/PATCH only), so create is
// the only GraphQL write path the validator applies to.
func TestGraphQLValidation(t *testing.T) {
	ctx := context.Background()
	connStr, cleanup := startPostgres(t)
	defer cleanup()

	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `CREATE SCHEMA tenant_gqlval`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE tenant_gqlval.items (
			id     UUID DEFAULT gen_random_uuid() PRIMARY KEY,
			code   TEXT NOT NULL,
			status TEXT,
			amount DOUBLE PRECISION,
			qty    INTEGER
		)
	`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	fmin := func(f float64) *float64 { return &f }
	testSchema := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"items": {Fields: map[string]schema.FieldDef{
				"code":   {Type: "string", Required: true, Pattern: "^[A-Z]{3}-[0-9]+$"},
				"status": {Type: "string", Enum: []string{"pending", "active"}},
				"amount": {Type: "float64", Min: fmin(0)},
				"qty":    {Type: "int", Min: fmin(1)}, // exercises the Int→float64 arg normalization
			}},
		},
	}

	var policy rbac.Policy
	json.Unmarshal([]byte(`{"roles":{"super_admin":{"resources":"*","actions":["*"]}}}`), &policy)

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	handler := gqlhandler.BuildHandler(testSchema, tdb, hr, &policy, nil, false)
	srv := httptest.NewServer(withFullStack(handler, "gqlval.localhost", gqlTestSecret))
	defer srv.Close()

	adminToken, _ := auth.GenerateToken(auth.Claims{UserID: "u1", Role: "super_admin", TenantID: "gqlval"}, gqlTestSecret)

	t.Run("InvalidMutation", func(t *testing.T) {
		result := gqlDo(t, srv, `mutation {
			createItem(input: {code: "bad code", status: "archived", amount: -5.5, qty: 0}) { id }
		}`, adminToken)
		errs, _ := result["errors"].([]any)
		if len(errs) == 0 {
			t.Fatalf("expected a validation error, got %v", result)
		}
		first, _ := errs[0].(map[string]any)
		if first["message"] != "validation_failed" {
			t.Fatalf("expected message validation_failed, got %v", first["message"])
		}
		ext, _ := first["extensions"].(map[string]any)
		fields, _ := ext["fields"].([]any)
		got := map[string]string{} // field → rule
		for _, f := range fields {
			fo, _ := f.(map[string]any)
			field, _ := fo["field"].(string)
			rule, _ := fo["rule"].(string)
			got[field] = rule
			if msg, _ := fo["message"].(string); msg == "" {
				t.Errorf("field %s: empty message", field)
			}
		}
		want := map[string]string{"code": "pattern", "status": "enum", "amount": "min", "qty": "min"}
		for f, r := range want {
			if got[f] != r {
				t.Errorf("expected %s/%s among extensions.fields, got %v", f, r, got)
			}
		}
		if len(fields) != len(want) {
			t.Errorf("all violations must be reported at once: want %d, got %d (%v)", len(want), len(fields), got)
		}
		if result["data"] != nil {
			if d, _ := result["data"].(map[string]any); d["createItem"] != nil {
				t.Errorf("no row must be created on validation failure, got %v", d["createItem"])
			}
		}
	})

	t.Run("ValidMutation", func(t *testing.T) {
		result := gqlDo(t, srv, `mutation {
			createItem(input: {code: "ABC-123", status: "active", amount: 99.5, qty: 3}) { id code qty }
		}`, adminToken)
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		created := data["createItem"].(map[string]any)
		if created["code"] != "ABC-123" {
			t.Errorf("expected code ABC-123, got %v", created["code"])
		}
		if created["id"] == nil || created["id"] == "" {
			t.Error("expected non-empty id")
		}
		if qty, _ := created["qty"].(float64); qty != 3 {
			t.Errorf("expected qty 3 round-tripped, got %v", created["qty"])
		}
	})
}
