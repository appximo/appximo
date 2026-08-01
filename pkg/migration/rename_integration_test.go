package migration

import (
	"context"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// TestIntegration_RenameColumn_PreservesData is the central MIG-F1-S2 proof: a
// field with renamed_from is migrated with ALTER RENAME COLUMN — the data stays in
// the (renamed) column, accessible under the new name, NOT stranded in an old column
// with a new empty one. Re-provision is then a clean no-op.
func TestIntegration_RenameColumn_PreservesData(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_ren"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_ren"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"people": {Fields: map[string]schema.FieldDef{
			"nombre_completo": {Type: "string", Required: true},
			"email":           {Type: "string", Unique: true},
		}, Indexes: []schema.IndexDef{{Fields: []string{"email"}}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenant_ren.people (nombre_completo, email) VALUES ('Ada Lovelace','ada@x.com')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Rename nombre_completo → full_name.
	renamed := mkSchema(map[string]schema.ResourceSchema{
		"people": {Fields: map[string]schema.FieldDef{
			"full_name": {Type: "string", Required: true, RenamedFrom: "nombre_completo"},
			"email":     {Type: "string", Unique: true},
		}, Indexes: []schema.IndexDef{{Fields: []string{"email"}}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, renamed); err != nil {
		t.Fatalf("rename apply: %v", err)
	}

	// The DATA is under the NEW column, not stranded.
	cols := columnSet(t, pool, pg, "people")
	if !cols["full_name"] {
		t.Fatalf("renamed column full_name missing: %v", cols)
	}
	if cols["nombre_completo"] {
		t.Errorf("old column nombre_completo should be GONE (renamed, not drop+add): %v", cols)
	}
	if got := getString(t, pool, `SELECT full_name FROM tenant_ren.people WHERE email='ada@x.com'`); got != "Ada Lovelace" {
		t.Errorf("data not preserved under new name: full_name=%q", got)
	}
	if n := countRows(t, pool, "tenant_ren.people"); n != 1 {
		t.Errorf("row count changed: %d", n)
	}

	// Re-provision with renamed_from STILL present → no-op (intent is inert once
	// applied; the old column no longer exists).
	plan, _, err := diffTenant(ctx, pool, pg, renamed, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision after rename must be a no-op, got:\n%s", plan)
	}
}

// TestIntegration_RenameColumn_PreservesFKAndUnique verifies a renamed column keeps
// its foreign key and unique constraint working (Postgres preserves them through the
// rename; the diff does not churn them).
func TestIntegration_RenameColumn_PreservesFKAndUnique(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_renfk"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_renfk"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	base := mkSchema(map[string]schema.ResourceSchema{
		"orgs": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"members": {Fields: map[string]schema.FieldDef{
			"org_id": {Type: "uuid", Relation: "orgs"}, // restrict FK + index
			"badge":  {Type: "string", Unique: true},
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}
	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO tenant_renfk.orgs (name) VALUES ('acme') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_renfk.members (org_id, badge) VALUES ($1,'B1')`, orgID); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// Rename the FK column org_id → organization_id and the unique column badge → tag.
	renamed := mkSchema(map[string]schema.ResourceSchema{
		"orgs": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"members": {Fields: map[string]schema.FieldDef{
			"organization_id": {Type: "uuid", Relation: "orgs", RenamedFrom: "org_id"},
			"tag":             {Type: "string", Unique: true, RenamedFrom: "badge"},
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, renamed); err != nil {
		t.Fatalf("rename apply: %v", err)
	}

	// Re-provision is a no-op — proves the FK/unique/index did not churn (no spurious
	// duplicate constraint on the renamed columns).
	plan, _, err := diffTenant(ctx, pool, pg, renamed, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("rename of FK/unique columns must converge to a no-op, got:\n%s", plan)
	}

	// FK still enforced on the renamed column: deleting the referenced org is blocked.
	if _, err := pool.Exec(ctx, `DELETE FROM tenant_renfk.orgs WHERE id=$1`, orgID); err == nil {
		t.Error("FK should still block deleting a referenced org after the column rename")
	}
	// Unique still enforced on the renamed column.
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_renfk.members (organization_id, tag) VALUES ($1,'B1')`, orgID); err == nil {
		t.Error("unique should still block a duplicate tag after the column rename")
	}
	// Exactly one FK and one unique constraint on members (no duplicate from churn).
	var nFK, nUniq int
	pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='members' AND con.contype='f'`, pg).Scan(&nFK)
	pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='members' AND con.contype='u'`, pg).Scan(&nUniq)
	if nFK != 1 {
		t.Errorf("expected exactly 1 FK on members after rename, got %d (churn duplicated it)", nFK)
	}
	if nUniq != 1 {
		t.Errorf("expected exactly 1 unique on members after rename, got %d (churn duplicated it)", nUniq)
	}
}

// TestIntegration_RenameTable_PreservesData verifies a resource (table) rename keeps
// the table's data and constraints, and converges to a no-op.
func TestIntegration_RenameTable_PreservesData(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_rentbl"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_rentbl"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	base := mkSchema(map[string]schema.ResourceSchema{
		"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_rentbl.customers (name) VALUES ('globex')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	renamed := mkSchema(map[string]schema.ResourceSchema{
		"clients": {RenamedFrom: "customers", Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, renamed); err != nil {
		t.Fatalf("rename apply: %v", err)
	}

	// New table exists with the data; old table gone.
	if n := countRows(t, pool, "tenant_rentbl.clients"); n != 1 {
		t.Errorf("renamed table should hold the data, got %d rows", n)
	}
	if got := getString(t, pool, `SELECT name FROM tenant_rentbl.clients LIMIT 1`); got != "globex" {
		t.Errorf("data not preserved through table rename: %q", got)
	}
	var oldExists bool
	pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name='customers')`, pg).Scan(&oldExists)
	if oldExists {
		t.Errorf("old table customers should be gone after rename")
	}

	plan, _, err := diffTenant(ctx, pool, pg, renamed, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision after table rename must be a no-op, got:\n%s", plan)
	}
}

// TestIntegration_NoRenamedFrom_IdenticalBehavior is the safety net: a schema with no
// renamed_from converges exactly as before (no rename ops ever appear).
func TestIntegration_NoRenamedFrom_IdenticalBehavior(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_norename"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_norename"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	s := mkSchema(map[string]schema.ResourceSchema{
		"widgets": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, s); err != nil {
		t.Fatalf("provision: %v", err)
	}
	plan, _, err := diffTenant(ctx, pool, pg, s, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("schema without renamed_from must be a clean no-op, got:\n%s", plan)
	}
}
