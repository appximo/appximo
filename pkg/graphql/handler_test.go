package graphql_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) (string, func()) {
	t.Helper()
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

func withTenant(handler http.Handler, host string) http.Handler {
	wrapped := tenant.TenantMiddleware(handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Host = host
		wrapped.ServeHTTP(w, r)
	})
}

// gqlDo sends a GraphQL POST request and returns the decoded response map.
func gqlDo(t *testing.T, srv *httptest.Server, query, role, userID string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("X-User-Role", role)
	}
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
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
				"created_at":  {Type: "time", Auto: true},
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
	handler := gqlhandler.BuildHandler(testSchema, tdb, hr, &policy)
	srv := httptest.NewServer(withTenant(handler, "gql.localhost"))
	defer srv.Close()

	// track the ID created in CreateMutation for subsequent subtests
	var createdID string

	// ── 1. Query empty list ───────────────────────────────────────────────────
	t.Run("QueryList", func(t *testing.T) {
		result := gqlDo(t, srv, `{ guides { data { id code } meta { total } } }`, "super_admin", "")
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
		}`, "super_admin", "")
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
		result := gqlDo(t, srv, fmt.Sprintf(`{ guide(id: "%s") { id code } }`, createdID), "super_admin", "")
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
		// Add a second guide directly so we can verify filter narrows results.
		pool.Exec(ctx, `INSERT INTO tenant_gql.guides (code, origin, destination) VALUES ('G-002', 'Cali', 'Bogota')`)

		result := gqlDo(t, srv,
			`{ guides(filter: { code: { exact: "G-001" } }) { data { code } meta { total } } }`,
			"super_admin", "")
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
		result := gqlDo(t, srv, fmt.Sprintf(`mutation { deleteGuide(id: "%s") }`, createdID), "super_admin", "")
		if msg := firstError(result); msg != "" {
			t.Fatalf("unexpected error: %s", msg)
		}
		data := result["data"].(map[string]any)
		deleted, _ := data["deleteGuide"].(bool)
		if !deleted {
			t.Error("expected deleteGuide to return true")
		}

		// Verify the record is gone
		result2 := gqlDo(t, srv, fmt.Sprintf(`{ guide(id: "%s") { id } }`, createdID), "super_admin", "")
		if msg := firstError(result2); msg != "" {
			t.Fatalf("unexpected error after delete: %s", msg)
		}
		data2 := result2["data"].(map[string]any)
		if data2["guide"] != nil {
			t.Errorf("expected null after delete, got %v", data2["guide"])
		}
	})

	// ── 6. Missing token → error "missing token" ──────────────────────────────
	t.Run("MissingToken", func(t *testing.T) {
		result := gqlDo(t, srv, `{ guides { data { id } } }`, "", "")
		msg := firstError(result)
		if msg != "missing token" {
			t.Errorf("expected 'missing token' error, got %q", msg)
		}
	})

	// ── 7. Role without permission → error "forbidden" ────────────────────────
	t.Run("Forbidden", func(t *testing.T) {
		// public role can read but not create
		result := gqlDo(t, srv, `mutation {
			createGuide(input: {code: "G-FBD", origin: "X", destination: "Y"}) { id }
		}`, "public", "")
		msg := firstError(result)
		if msg != "forbidden" {
			t.Errorf("expected 'forbidden' error, got %q", msg)
		}
	})
}
