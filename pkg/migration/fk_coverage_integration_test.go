package migration

import (
	"context"
	"testing"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/schema"
)

// fkCoverageSchema exercises all three MIG-F1-S5 forms at once:
//   - users(email unique)
//   - posts.author_email → users(email) with ON UPDATE CASCADE   (references + on_update)
//   - branches(region_code, branch_code) composite unique
//   - posts(region_code, branch_code) → branches(...)  composite FK, ON DELETE CASCADE
func fkCoverageSchema() *schema.APISchema {
	return mkSchema(map[string]schema.ResourceSchema{
		"users": {Fields: map[string]schema.FieldDef{
			"email": {Type: "string", Required: true, Unique: true},
		}},
		"branches": {
			Fields: map[string]schema.FieldDef{
				"region_code": {Type: "string", Required: true},
				"branch_code": {Type: "string", Required: true},
				"name":        {Type: "string"},
			},
			Indexes: []schema.IndexDef{{Fields: []string{"region_code", "branch_code"}, Unique: true}},
		},
		"posts": {
			Fields: map[string]schema.FieldDef{
				"title":        {Type: "string", Required: true},
				"author_email": {Type: "string", Relation: "users", References: "email", OnUpdate: schema.OnUpdateCascade},
				"region_code":  {Type: "string"},
				"branch_code":  {Type: "string"},
			},
			ForeignKeys: []schema.ForeignKeyDef{{
				Columns: []string{"region_code", "branch_code"}, Target: "branches",
				RefColumns: []string{"region_code", "branch_code"}, OnDelete: schema.OnDeleteCascade,
			}},
		},
	})
}

// TestIntegration_FKCoverage_AllThreeForms is the MIG-F1-S5 proof: on_update,
// references-to-non-id, and composite FKs are all created faithfully, are idempotent
// (re-provision is a no-op — the key no-churn property), and each behaves at the DB
// level (FK violation → 23503; ON UPDATE CASCADE propagates; composite FK enforced).
func TestIntegration_FKCoverage_AllThreeForms(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_fkcov"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_fkcov"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	s := fkCoverageSchema()
	if err := ApplyTenantMigration(ctx, pool, pg, s); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// references FK points at users(email) with ON UPDATE CASCADE (confupdtype 'c').
	var refTable, refCol, confupd, confdel string
	err := pool.QueryRow(ctx, `
		SELECT cf.relname,
		       (SELECT a.attname FROM pg_attribute a WHERE a.attrelid=con.confrelid AND a.attnum=con.confkey[1]),
		       con.confupdtype::text, con.confdeltype::text
		FROM pg_constraint con
		JOIN pg_class c ON c.oid=con.conrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_class cf ON cf.oid=con.confrelid
		WHERE n.nspname=$1 AND con.conname='fk_posts_author_email'`, pg).Scan(&refTable, &refCol, &confupd, &confdel)
	if err != nil {
		t.Fatalf("references FK not found: %v", err)
	}
	if refTable != "users" || refCol != "email" {
		t.Errorf("references FK should point at users(email), got %s(%s)", refTable, refCol)
	}
	if confupd != "c" {
		t.Errorf("author_email FK confupdtype=%q want 'c' (ON UPDATE CASCADE)", confupd)
	}
	if confdel != "r" {
		t.Errorf("author_email FK confdeltype=%q want 'r' (on_delete default RESTRICT)", confdel)
	}

	// Composite FK exists over (region_code, branch_code), ON DELETE CASCADE, ON UPDATE NO ACTION.
	var ncols int
	var cdel, cupd string
	err = pool.QueryRow(ctx, `
		SELECT cardinality(con.conkey), con.confdeltype::text, con.confupdtype::text
		FROM pg_constraint con
		JOIN pg_class c ON c.oid=con.conrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND con.conname='fk_posts_region_code_branch_code' AND con.contype='f'`, pg).Scan(&ncols, &cdel, &cupd)
	if err != nil {
		t.Fatalf("composite FK not found: %v", err)
	}
	if ncols != 2 {
		t.Errorf("composite FK should span 2 columns, got %d", ncols)
	}
	if cdel != "c" {
		t.Errorf("composite FK confdeltype=%q want 'c' (CASCADE)", cdel)
	}
	if cupd != "a" {
		t.Errorf("composite FK confupdtype=%q want 'a' (NO ACTION default — no churn)", cupd)
	}

	// CRITICAL no-churn property: re-provision is a true no-op.
	plan, _, err := diffTenant(ctx, pool, pg, s, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision with the three FK forms must be a no-op, got:\n%s", plan)
	}

	// ── Behavior: references FK enforced (nonexistent email → 23503). ──
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.users (email) VALUES ('a@x.com')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.posts (title, author_email) VALUES ('p', 'ghost@x.com')`); err == nil {
		t.Error("post with a nonexistent author_email must be rejected by the FK")
	} else if _, ok := db.ForeignKeyViolation(err); !ok {
		t.Errorf("expected FK violation, got: %v", err)
	}

	// ── Behavior: ON UPDATE CASCADE propagates an email change to the child. ──
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.posts (title, author_email) VALUES ('p1', 'a@x.com')`); err != nil {
		t.Fatalf("seed post: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tenant_fkcov.users SET email='b@x.com' WHERE email='a@x.com'`); err != nil {
		t.Fatalf("update parent email: %v", err)
	}
	var cascaded string
	if err := pool.QueryRow(ctx, `SELECT author_email FROM tenant_fkcov.posts WHERE title='p1'`).Scan(&cascaded); err != nil {
		t.Fatalf("read cascaded post: %v", err)
	}
	if cascaded != "b@x.com" {
		t.Errorf("ON UPDATE CASCADE should have propagated the new email, got %q", cascaded)
	}

	// ── Behavior: composite FK enforced (unknown (region,branch) → 23503; known → ok). ──
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.branches (region_code, branch_code, name) VALUES ('NA','01','HQ')`); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.posts (title, region_code, branch_code) VALUES ('bad','NA','99')`); err == nil {
		t.Error("post with an unknown (region,branch) must be rejected by the composite FK")
	} else if _, ok := db.ForeignKeyViolation(err); !ok {
		t.Errorf("expected composite FK violation, got: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_fkcov.posts (title, region_code, branch_code) VALUES ('good','NA','01')`); err != nil {
		t.Errorf("post with a valid (region,branch) should be accepted: %v", err)
	}

	// ── Composite FK ON DELETE CASCADE: deleting the branch deletes its posts. ──
	if _, err := pool.Exec(ctx, `DELETE FROM tenant_fkcov.branches WHERE region_code='NA' AND branch_code='01'`); err != nil {
		t.Fatalf("delete branch (cascade): %v", err)
	}
	if n := countRows(t, pool, `tenant_fkcov.posts WHERE title='good'`); n != 0 {
		t.Errorf("composite ON DELETE CASCADE should have removed the child post, found %d", n)
	}
}

// TestIntegration_FKCoverage_AddOnUpdateNoChurn proves the no-churn default directly:
// a tenant provisioned with a plain field-level relation (no on_update) re-diffs to
// EMPTY after the schema gains the on_update capability — because the unset default
// maps to NO ACTION, matching what the live FK already carries.
func TestIntegration_FKCoverage_AddOnUpdateNoChurn(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_nochurn"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_nochurn"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// A plain FK with no on_update — exactly an existing (pre-S5) tenant's schema.
	s := mkSchema(map[string]schema.ResourceSchema{
		"departments": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"employees": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"dept": {Type: "uuid", Relation: "departments", OnDelete: schema.OnDeleteCascade},
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, s); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Re-diff with the SAME schema (the S5 code path is now live) → must be empty.
	plan, _, err := diffTenant(ctx, pool, pg, s, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision of a pre-S5 FK must stay a no-op (on_update default = NO ACTION), got:\n%s", plan)
	}
}
