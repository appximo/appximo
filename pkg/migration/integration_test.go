package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// startPG spins an ephemeral Postgres for the internal-package integration tests
// (the external worker_test has its own copy; this one lives in package migration
// so it can assert diffTenant().Empty() directly).
func startPG(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
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
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("new pool: %v", err)
	}
	return pool, func() { pool.Close(); ctr.Terminate(ctx) }
}

func mkSchema(resources map[string]schema.ResourceSchema) *schema.APISchema {
	return &schema.APISchema{Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "t", Resources: resources}
}

// TestIntegration_ProvisionEvolveNoop walks the full safety net:
//
//	provision a NEW tenant      → tables + columns + indexes created (like before)
//	re-provision unchanged      → diff is EMPTY, no-op, data preserved
//	add a NULLABLE column       → applied additively, existing row preserved (NULL)
//	remove a field              → DROP is GATED (column + data stay as drift)
func TestIntegration_ProvisionEvolveNoop(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_lc"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_lc"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"orders": {
			Fields: map[string]schema.FieldDef{
				"code":       {Type: "string", Required: true, Unique: true},
				"customer":   {Type: "string"},
				"amount":     {Type: "float64"},
				"created_at": {Type: "time", Auto: true},
			},
			Indexes: []schema.IndexDef{{Fields: []string{"customer"}}},
		},
	})

	// ── provision new ──
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}
	assertColumns(t, pool, pg, "orders", []string{"id", "code", "customer", "amount", "created_at"})
	// the row inserts: id default works, NOT NULL code present.
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_lc.orders (code, customer, amount) VALUES ('A1','acme',10)`); err != nil {
		t.Fatalf("insert after provision: %v", err)
	}

	// ── re-provision unchanged → EMPTY diff ──
	plan, err := diffTenant(ctx, pool, pg, base)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision must be a no-op, got:\n%s", plan)
	}
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("re-provision apply: %v", err)
	}
	if n := countRows(t, pool, "tenant_lc.orders"); n != 1 {
		t.Fatalf("row lost across re-provision: %d", n)
	}

	// ── add a NULLABLE column → additive ──
	evolved := mkSchema(map[string]schema.ResourceSchema{
		"orders": {
			Fields: map[string]schema.FieldDef{
				"code":       {Type: "string", Required: true, Unique: true},
				"customer":   {Type: "string"},
				"amount":     {Type: "float64"},
				"note":       {Type: "text"}, // NEW nullable
				"created_at": {Type: "time", Auto: true},
			},
			Indexes: []schema.IndexDef{{Fields: []string{"customer"}}},
		},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, evolved); err != nil {
		t.Fatalf("evolve add column: %v", err)
	}
	assertColumns(t, pool, pg, "orders", []string{"id", "code", "customer", "amount", "note", "created_at"})
	if n := countRows(t, pool, "tenant_lc.orders"); n != 1 {
		t.Fatalf("row lost across add-column: %d", n)
	}

	// ── remove a field → DROP gated (drift kept) ──
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"orders": {
			Fields: map[string]schema.FieldDef{
				"code":       {Type: "string", Required: true, Unique: true},
				"created_at": {Type: "time", Auto: true},
			},
		},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, shrunk); err != nil {
		t.Fatalf("shrink: %v", err)
	}
	// customer/amount/note columns must STILL exist (drop is gated, data preserved).
	assertColumns(t, pool, pg, "orders", []string{"id", "code", "customer", "amount", "note", "created_at"})
	if got := getString(t, pool, "SELECT customer FROM tenant_lc.orders WHERE code='A1'"); got != "acme" {
		t.Fatalf("gated drop lost data: customer=%q", got)
	}
}

// TestIntegration_AddRequiredColumn_FaithfulNotNull proves the converger's silent
// NOT-NULL-discard is fixed: adding a required column to a POPULATED table now
// fails LOUDLY and rolls back atomically (no silent NULL-accepting column), while
// the same op on an EMPTY table applies as a real NOT NULL.
func TestIntegration_AddRequiredColumn_FaithfulNotNull(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_nn"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	pg := "tenant_nn"

	base := mkSchema(map[string]schema.ResourceSchema{
		"widgets": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// EMPTY table: a new required column applies as a real NOT NULL.
	withReq := mkSchema(map[string]schema.ResourceSchema{
		"widgets": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"sku":  {Type: "string", Required: true},
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, withReq); err != nil {
		t.Fatalf("add required column to EMPTY table should succeed: %v", err)
	}
	if nn := isNotNull(t, pool, pg, "widgets", "sku"); !nn {
		t.Errorf("sku must be a REAL NOT NULL (converger silently dropped it)")
	}

	// Now POPULATE, then add ANOTHER required column → must FAIL loudly + roll back.
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_nn.widgets (name, sku) VALUES ('w','s1')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	withReq2 := mkSchema(map[string]schema.ResourceSchema{
		"widgets": {Fields: map[string]schema.FieldDef{
			"name":   {Type: "string", Required: true},
			"sku":    {Type: "string", Required: true},
			"region": {Type: "string", Required: true}, // NOT NULL over existing rows
		}},
	})
	err := ApplyTenantMigration(ctx, pool, pg, withReq2)
	if err == nil {
		t.Fatalf("adding NOT NULL column over populated data must fail loudly (never silently nullable)")
	}
	// Atomic rollback: the region column must NOT exist.
	cols := columnSet(t, pool, pg, "widgets")
	if cols["region"] {
		t.Errorf("failed NOT NULL add must roll back — region column should not exist: %v", cols)
	}
}

// ── small DB helpers ──

func assertColumns(t *testing.T, pool *pgxpool.Pool, pg, table string, want []string) {
	t.Helper()
	got := columnSet(t, pool, pg, table)
	for _, c := range want {
		if !got[c] {
			t.Errorf("table %s.%s missing column %q (have: %v)", pg, table, c, got)
		}
	}
}

func columnSet(t *testing.T, pool *pgxpool.Pool, pg, table string) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT column_name FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2`, pg, table)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		out[c] = true
	}
	return out
}

func isNotNull(t *testing.T, pool *pgxpool.Pool, pg, table, col string) bool {
	t.Helper()
	var nullable string
	if err := pool.QueryRow(context.Background(),
		`SELECT is_nullable FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3`,
		pg, table, col).Scan(&nullable); err != nil {
		t.Fatalf("is_nullable: %v", err)
	}
	return strings.EqualFold(nullable, "NO")
}

func countRows(t *testing.T, pool *pgxpool.Pool, qualified string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+qualified).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", qualified, err)
	}
	return n
}

func getString(t *testing.T, pool *pgxpool.Pool, q string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), q).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s
}
