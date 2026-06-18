package migration

import (
	"context"
	"testing"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/schema"
)

// fkSchema: a parent and three child FK columns, one per on_delete action.
func fkSchema() *schema.APISchema {
	return mkSchema(map[string]schema.ResourceSchema{
		"departments": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
		}},
		"employees": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			// belongs to a department; deleting the department nulls this (nullable).
			"dept_setnull": {Type: "uuid", Relation: "departments", OnDelete: schema.OnDeleteSetNull},
			// cascade: deleting the department deletes this employee row.
			"dept_cascade": {Type: "uuid", Relation: "departments", OnDelete: schema.OnDeleteCascade},
			// default restrict: deleting the department is blocked while referenced.
			"dept_restrict": {Type: "uuid", Relation: "departments"},
		}},
	})
}

// TestIntegration_ForeignKeys_CreatedIdempotentAndEnforced is the core MIG-F1-S1
// proof: FKs are created with the right on_delete actions, re-provision is a
// no-op (idempotent), and each referential action behaves at the DB level
// (restrict blocks → 23503, cascade deletes children, set_null nulls the FK).
func TestIntegration_ForeignKeys_CreatedIdempotentAndEnforced(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_fk"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_fk"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	s := fkSchema()

	if err := ApplyTenantMigration(ctx, pool, pg, s); err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Three real FK constraints with the expected confdeltype (a=NO ACTION,
	// r=RESTRICT, c=CASCADE, n=SET NULL).
	wantAction := map[string]string{
		"fk_employees_dept_setnull":  "n",
		"fk_employees_dept_cascade":  "c",
		"fk_employees_dept_restrict": "r",
	}
	for sym, want := range wantAction {
		var deltype string
		err := pool.QueryRow(ctx, `
			SELECT con.confdeltype::text FROM pg_constraint con
			JOIN pg_class c ON c.oid=con.conrelid
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname=$1 AND con.conname=$2 AND con.contype='f'`, pg, sym).Scan(&deltype)
		if err != nil {
			t.Fatalf("FK %s not found: %v", sym, err)
		}
		if deltype != want {
			t.Errorf("FK %s confdeltype=%q want %q", sym, deltype, want)
		}
	}

	// Idempotent: re-diff is empty (FKs match by structure + action).
	plan, err := diffTenant(ctx, pool, pg, s, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision must be a no-op once FKs exist, got:\n%s", plan)
	}

	// Seed: one department, three employees (one per action) pointing at it.
	var deptID string
	if err := pool.QueryRow(ctx, `INSERT INTO tenant_fk.departments (name) VALUES ('eng') RETURNING id`).Scan(&deptID); err != nil {
		t.Fatalf("seed dept: %v", err)
	}
	mkEmp := func(col string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO tenant_fk.employees (name, `+col+`) VALUES ('e', $1) RETURNING id`, deptID).Scan(&id); err != nil {
			t.Fatalf("seed employee (%s): %v", col, err)
		}
		return id
	}
	setnullEmp := mkEmp("dept_setnull")
	cascadeEmp := mkEmp("dept_cascade")
	_ = mkEmp("dept_restrict")

	// RESTRICT: deleting the referenced department is blocked → 23503, classified.
	_, err = pool.Exec(ctx, `DELETE FROM tenant_fk.departments WHERE id=$1`, deptID)
	if err == nil {
		t.Fatal("delete of referenced department should be blocked by RESTRICT FK")
	}
	if msg, ok := db.ForeignKeyViolation(err); !ok {
		t.Errorf("expected a foreign-key violation, got: %v", err)
	} else {
		t.Logf("RESTRICT delete → classified 409 message: %q", msg)
	}

	// Remove the restrict reference, then delete the department: cascade deletes the
	// cascade employee, set_null nulls the set_null employee.
	if _, err := pool.Exec(ctx, `UPDATE tenant_fk.employees SET dept_restrict=NULL WHERE dept_restrict=$1`, deptID); err != nil {
		t.Fatalf("clear restrict ref: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM tenant_fk.departments WHERE id=$1`, deptID); err != nil {
		t.Fatalf("delete department after clearing restrict ref: %v", err)
	}
	// cascade employee gone.
	if n := countRows(t, pool, `tenant_fk.employees WHERE id='`+cascadeEmp+`'`); n != 0 {
		t.Errorf("CASCADE: cascade employee should be deleted, found %d", n)
	}
	// set_null employee survives with a NULL FK.
	var isNull bool
	if err := pool.QueryRow(ctx, `SELECT dept_setnull IS NULL FROM tenant_fk.employees WHERE id=$1`, setnullEmp).Scan(&isNull); err != nil {
		t.Fatalf("read setnull employee: %v", err)
	}
	if !isNull {
		t.Errorf("SET NULL: dept_setnull should be NULL after parent delete")
	}
}

// TestIntegration_ForeignKeys_TolerantValidateKeepsNotValid proves the transition
// policy: when pre-existing rows violate a new FK, the ADD … NOT VALID still
// commits (forward protection) and provisioning is NOT broken — the FK is left
// NOT VALID, not rolled back.
func TestIntegration_ForeignKeys_TolerantValidateKeepsNotValid(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_fkdirty"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_fkdirty"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// Provision WITHOUT the relation first (no FK), then plant an orphan row.
	noRel := mkSchema(map[string]schema.ResourceSchema{
		"departments": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"employees": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"dept": {Type: "uuid"}, // plain column, no relation yet
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, noRel); err != nil {
		t.Fatalf("provision (no rel): %v", err)
	}
	// Orphan: dept points to a non-existent department.
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenant_fkdirty.employees (name, dept) VALUES ('orphan', gen_random_uuid())`); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	// Now evolve to add the relation (restrict FK). VALIDATE will fail on the orphan,
	// but provisioning must NOT error — the FK is added NOT VALID.
	withRel := mkSchema(map[string]schema.ResourceSchema{
		"departments": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"employees": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"dept": {Type: "uuid", Relation: "departments"},
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, withRel); err != nil {
		t.Fatalf("evolve with dirty data must NOT break provisioning: %v", err)
	}

	// FK exists but is NOT VALID (convalidated=false).
	var convalidated bool
	err := pool.QueryRow(ctx, `
		SELECT con.convalidated FROM pg_constraint con
		JOIN pg_class c ON c.oid=con.conrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND con.conname='fk_employees_dept'`, pg).Scan(&convalidated)
	if err != nil {
		t.Fatalf("FK not added at all (should be NOT VALID): %v", err)
	}
	if convalidated {
		t.Errorf("FK should be left NOT VALID over orphan data, but is validated")
	}

	// Forward protection: a NEW bad insert is REJECTED even though the FK is NOT VALID.
	_, err = pool.Exec(ctx,
		`INSERT INTO tenant_fkdirty.employees (name, dept) VALUES ('new', gen_random_uuid())`)
	if err == nil {
		t.Error("NOT VALID FK must still reject NEW writes with a bad reference")
	} else if _, ok := db.ForeignKeyViolation(err); !ok {
		t.Errorf("expected FK violation on new bad insert, got: %v", err)
	}
}
