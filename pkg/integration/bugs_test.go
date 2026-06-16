package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
)

// bugsSchema exercises a MULTI-WORD resource name ("order_items" — the supported
// underscore form; hyphenated names are rejected at validation since G1, see
// pkg/schema/validator_test.go) and user-declared `indexes` (single, composite,
// unique, and one naming a missing column that must be skipped). The unique
// index on tasks.title also drives the G6 create-conflict test below.
func bugsSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "bugs",
		Resources: map[string]schema.ResourceSchema{
			"order_items": {
				Fields: map[string]schema.FieldDef{
					"sku": {Type: "string", Required: true},
					"qty": {Type: "int"},
				},
			},
			"tasks": {
				Fields: map[string]schema.FieldDef{
					"title":    {Type: "string", Required: true},
					"status":   {Type: "string"},
					"owner_id": {Type: "uuid"},
				},
				Indexes: []schema.IndexDef{
					{Fields: []string{"status"}},
					{Fields: []string{"owner_id", "status"}},
					{Fields: []string{"title"}, Unique: true},
					{Fields: []string{"ghost"}}, // missing column → warn + skip, not fail
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func setupBugs(t *testing.T) (*httptest.Server, string, *pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("bugs: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	s := bugsSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Bugs Co", Email: "b@b.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return srv, genToken("super_admin", superID), pool, func() { srv.Close(); cleanPG() }
}

// A multi-word (underscore) resource name works end-to-end through every
// write/get path — the identifiers are quoted/sanitized identically. Hyphenated
// names are now rejected at validation (G1), so this is the supported form.
func TestBugMultiwordResourceCRUD(t *testing.T) {
	srv, super, _, done := setupBugs(t)
	defer done()

	created := dpDo(t, srv, "POST", "/api/order_items", super, map[string]any{"sku": "ABC", "qty": 3}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create order_item: no id")
	}

	if items := dpList(t, srv, "/api/order_items", super); len(items) != 1 {
		t.Fatalf("list order_items: want 1, got %d", len(items))
	}

	got := dpDo(t, srv, "GET", "/api/order_items/"+id, super, nil, http.StatusOK)
	if got["sku"] != "ABC" {
		t.Fatalf("get order_item: %v", got)
	}

	dpDo(t, srv, "PUT", "/api/order_items/"+id, super, map[string]any{"sku": "XYZ", "qty": 5}, http.StatusOK)
	patched := dpDo(t, srv, "PATCH", "/api/order_items/"+id, super, map[string]any{"qty": 9}, http.StatusOK)
	if patched["sku"] != "XYZ" || patched["qty"].(float64) != 9 {
		t.Fatalf("patch order_item: %v", patched)
	}

	if code := dpStatus(t, srv, "DELETE", "/api/order_items/"+id, super, nil); code != http.StatusNoContent {
		t.Fatalf("delete order_item: want 204, got %d", code)
	}
	if code := dpStatus(t, srv, "GET", "/api/order_items/"+id, super, nil); code != http.StatusNotFound {
		t.Fatalf("get deleted order_item: want 404, got %d", code)
	}
}

// FEATURE: user-declared indexes are materialized at tenant registration —
// single, composite, and unique — while an index naming a non-existent column is
// skipped (warning) without failing the registration.
func TestUserDeclaredIndexes(t *testing.T) {
	_, _, pool, done := setupBugs(t)
	defer done()

	rows, err := pool.Query(context.Background(),
		`SELECT indexname, indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename='tasks'`,
		"tenant_"+tenantID)
	if err != nil {
		t.Fatalf("query pg_indexes: %v", err)
	}
	defer rows.Close()

	defs := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan: %v", err)
		}
		defs[name] = def
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	if _, ok := defs["idx_tasks_status"]; !ok {
		t.Errorf("missing single-column index idx_tasks_status; have %v", keysOf(defs))
	}
	if _, ok := defs["idx_tasks_owner_id_status"]; !ok {
		t.Errorf("missing composite index idx_tasks_owner_id_status; have %v", keysOf(defs))
	}
	if def, ok := defs["uniq_tasks_title"]; !ok {
		t.Errorf("missing unique index uniq_tasks_title; have %v", keysOf(defs))
	} else if !strings.Contains(def, "UNIQUE") {
		t.Errorf("uniq_tasks_title is not UNIQUE: %s", def)
	}
	if _, ok := defs["idx_tasks_ghost"]; ok {
		t.Error("index on non-existent column 'ghost' must be skipped, but it exists")
	}
}

// G6: a unique-constraint collision on the schema-derived CRUD path is an
// EXPECTED conflict — it must return 409 (with a consumable message), not the
// masked 500 it used to. tasks.title has a UNIQUE index (idx uniq_tasks_title).
func TestUniqueViolationReturns409OnCreate(t *testing.T) {
	srv, super, _, done := setupBugs(t)
	defer done()

	dpDo(t, srv, "POST", "/api/tasks", super, map[string]any{"title": "dup"}, http.StatusCreated)

	// Second create with the same title violates the unique index.
	code, body := dpStatusBody(t, srv, "POST", "/api/tasks", super, map[string]any{"title": "dup"})
	if code != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d (body: %s)", code, body)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("409 body should be a consumable conflict message, got: %s", body)
	}
	// Must NOT leak raw Postgres internals (SQLSTATE/constraint SQL).
	if strings.Contains(body, "SQLSTATE") || strings.Contains(strings.ToLower(body), "pq:") {
		t.Errorf("409 body leaks raw DB error: %s", body)
	}

	// A non-conflicting create still succeeds (the success path is unaffected).
	dpDo(t, srv, "POST", "/api/tasks", super, map[string]any{"title": "fresh"}, http.StatusCreated)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
