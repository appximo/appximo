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

// bugsSchema exercises BUGS-V1: a HYPHENATED resource name ("order-items" — only
// possible via the relaxed table quoting) and user-declared `indexes` (single,
// composite, unique, and one naming a missing column that must be skipped).
func bugsSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "bugs",
		Resources: map[string]schema.ResourceSchema{
			"order-items": {
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

// BUG1: a hyphenated resource name now works end-to-end through every write/get
// path (create handler previously emitted unquoted SQL → 500). Before this fix a
// junction had to be named without a separator.
func TestBugHyphenResourceCRUD(t *testing.T) {
	srv, super, _, done := setupBugs(t)
	defer done()

	created := dpDo(t, srv, "POST", "/api/order-items", super, map[string]any{"sku": "ABC", "qty": 3}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("create order-item: no id")
	}

	if items := dpList(t, srv, "/api/order-items", super); len(items) != 1 {
		t.Fatalf("list order-items: want 1, got %d", len(items))
	}

	got := dpDo(t, srv, "GET", "/api/order-items/"+id, super, nil, http.StatusOK)
	if got["sku"] != "ABC" {
		t.Fatalf("get order-item: %v", got)
	}

	dpDo(t, srv, "PUT", "/api/order-items/"+id, super, map[string]any{"sku": "XYZ", "qty": 5}, http.StatusOK)
	patched := dpDo(t, srv, "PATCH", "/api/order-items/"+id, super, map[string]any{"qty": 9}, http.StatusOK)
	if patched["sku"] != "XYZ" || patched["qty"].(float64) != 9 {
		t.Fatalf("patch order-item: %v", patched)
	}

	if code := dpStatus(t, srv, "DELETE", "/api/order-items/"+id, super, nil); code != http.StatusNoContent {
		t.Fatalf("delete order-item: want 204, got %d", code)
	}
	if code := dpStatus(t, srv, "GET", "/api/order-items/"+id, super, nil); code != http.StatusNotFound {
		t.Fatalf("get deleted order-item: want 404, got %d", code)
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

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
