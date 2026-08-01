package migration

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
)

// ENG-13 — the migration that reported success and did not apply.
//
// The field report (docs/AUTHORING_JOURNEY.md 5-8) measured this on a live app: a
// deploy repointed appointments.veterinarian_id from veterinarians(id) to
// veterinarians(user_id); the dry-run listed the ADD FOREIGN KEY, the apply printed
// ✓ for every table, and the database kept the OLD foreign key — which then BLOCKED
// the data change the new schema required. Declared and applied had diverged, with a
// success report in between.
//
// The tests below pin both halves of the fix:
//   - the change actually lands (asserted against pg_constraint, never the log), and
//   - the guarantee that catches the whole CLASS: after any apply, re-introspecting
//     the database must show nothing declared still pending.

// fkRefColumn returns the column a foreign key on (table, col) currently references,
// read straight from the catalog. It is deliberately independent of the migration
// engine's own model — the point of the test is to trust the DATABASE.
func fkRefColumn(t *testing.T, pool *pgxpool.Pool, pgSchema, table, col string) (refTable, refCol string) {
	t.Helper()
	const q = `
		SELECT rel_t.relname, att_r.attname
		  FROM pg_constraint c
		  JOIN pg_class     src_t ON src_t.oid = c.conrelid
		  JOIN pg_namespace ns    ON ns.oid    = src_t.relnamespace
		  JOIN pg_class     rel_t ON rel_t.oid = c.confrelid
		  JOIN pg_attribute att_s ON att_s.attrelid = c.conrelid  AND att_s.attnum = c.conkey[1]
		  JOIN pg_attribute att_r ON att_r.attrelid = c.confrelid AND att_r.attnum = c.confkey[1]
		 WHERE c.contype = 'f' AND ns.nspname = $1 AND src_t.relname = $2 AND att_s.attname = $3`
	rows, err := pool.Query(context.Background(), q, pgSchema, table, col)
	if err != nil {
		t.Fatalf("read pg_constraint: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		if err := rows.Scan(&refTable, &refCol); err != nil {
			t.Fatalf("scan pg_constraint: %v", err)
		}
		n++
	}
	if n > 1 {
		t.Fatalf("%s.%s has %d foreign keys — a replacement left a duplicate behind", table, col, n)
	}
	return refTable, refCol
}

// vetSchemaRefID is the shape the generator produced: appointments.veterinarian_id
// is a plain FK to veterinarians(id).
func vetSchemaRefID() *schema.APISchema {
	return mkSchema(map[string]schema.ResourceSchema{
		"veterinarians": {Fields: map[string]schema.FieldDef{
			"name":    {Type: "string", Required: true},
			"user_id": {Type: "uuid", Unique: true},
		}},
		"appointments": {Fields: map[string]schema.FieldDef{
			"reason":          {Type: "string"},
			"veterinarian_id": {Type: "uuid", Relation: "veterinarians"},
		}},
	})
}

// vetSchemaRefUserID is the owner's fix: the SAME relation, repointed at the bridge
// column veterinarians.user_id.
func vetSchemaRefUserID() *schema.APISchema {
	s := vetSchemaRefID()
	f := s.Resources["appointments"].Fields["veterinarian_id"]
	f.References = "user_id"
	s.Resources["appointments"].Fields["veterinarian_id"] = f
	return s
}

// TestIntegration_ENG13_ReferencesChangeActuallyApplies is the regression for the
// exact production finding: repointing an EXISTING relation with `references` must
// change the live constraint, and the outcome must not claim success if it did not.
func TestIntegration_ENG13_ReferencesChangeActuallyApplies(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_eng13ref"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_eng13ref"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	if err := ApplyTenantMigration(ctx, pool, pg, vetSchemaRefID()); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if rt, rc := fkRefColumn(t, pool, pg, "appointments", "veterinarian_id"); rt != "veterinarians" || rc != "id" {
		t.Fatalf("initial FK = %s(%s), want veterinarians(id)", rt, rc)
	}

	// Populate: the finding only appeared on a table with data, and a REPLACE must
	// preserve every row (dropping and re-adding a constraint touches no row data).
	var vetID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO "tenant_eng13ref".veterinarians (name, user_id) VALUES ('Ana', gen_random_uuid()) RETURNING id`,
	).Scan(&vetID); err != nil {
		t.Fatalf("seed vet: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO "tenant_eng13ref".appointments (reason, veterinarian_id) VALUES ('vacuna', $1)`, vetID,
	); err != nil {
		t.Fatalf("seed appointment: %v", err)
	}

	// The repoint. It carries a data concern (existing rows hold veterinarian ids,
	// not user ids) — the FK is added NOT VALID, which is the documented policy and
	// is exactly what lets the owner backfill afterwards.
	outcome, err := ApplyTenantMigrationApproved(ctx, pool, pg, vetSchemaRefUserID(), nil)
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}
	if outcome.Partial() {
		t.Fatalf("apply reported PARTIAL, not applied: %v", outcome.Unapplied)
	}

	// The verdict comes from the catalog, not from the migration's own report.
	rt, rc := fkRefColumn(t, pool, pg, "appointments", "veterinarian_id")
	if rt != "veterinarians" || rc != "user_id" {
		t.Fatalf("FK after repoint = %s(%s), want veterinarians(user_id) — the change was reported and not applied (ENG-13)", rt, rc)
	}

	// Rows survive a constraint replacement.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "tenant_eng13ref".appointments`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("appointments = %d, want 1 — a foreign-key replacement must not touch row data", n)
	}

	// The stale FK used to BLOCK the backfill the new schema requires. With the
	// relation repointed, the owner's data change goes through.
	if _, err := pool.Exec(ctx, `
		UPDATE "tenant_eng13ref".appointments a
		   SET veterinarian_id = v.user_id
		  FROM "tenant_eng13ref".veterinarians v
		 WHERE a.veterinarian_id = v.id`); err != nil {
		t.Fatalf("backfill blocked by a stale foreign key: %v", err)
	}

	// Re-applying the same schema is a true no-op: a replacement must not churn.
	again, err := ApplyTenantMigrationApproved(ctx, pool, pg, vetSchemaRefUserID(), nil)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if !again.NoChange {
		t.Fatalf("re-applying an unchanged schema was not a no-op — the FK replacement churns")
	}
}

// TestIntegration_ENG13_OnDeleteChangeActuallyApplies covers the other definition
// change that used to collide on the constraint name: the referential action.
func TestIntegration_ENG13_OnDeleteChangeActuallyApplies(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_eng13act"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_eng13act"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	base := vetSchemaRefID()
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}

	cascade := vetSchemaRefID()
	f := cascade.Resources["appointments"].Fields["veterinarian_id"]
	f.OnDelete = schema.OnDeleteCascade
	cascade.Resources["appointments"].Fields["veterinarian_id"] = f

	outcome, err := ApplyTenantMigrationApproved(ctx, pool, pg, cascade, nil)
	if err != nil {
		t.Fatalf("change on_delete: %v", err)
	}
	if outcome.Partial() {
		t.Fatalf("apply reported PARTIAL, not applied: %v", outcome.Unapplied)
	}

	var action string
	if err := pool.QueryRow(ctx, `
		SELECT c.confdeltype::text
		  FROM pg_constraint c
		  JOIN pg_class src ON src.oid = c.conrelid
		  JOIN pg_namespace ns ON ns.oid = src.relnamespace
		 WHERE c.contype = 'f' AND ns.nspname = $1 AND src.relname = 'appointments'`, pg).Scan(&action); err != nil {
		t.Fatalf("read confdeltype: %v", err)
	}
	if action != "c" { // 'c' = CASCADE, 'r' = RESTRICT
		t.Fatalf("ON DELETE is %q, want %q (CASCADE) — the change was reported and not applied", action, "c")
	}
}

// TestIntegration_BlockedRenameIsReported is the second case the ENG-13 audit found
// in the same class: a `renamed_from` whose target column ALREADY exists cannot run,
// and used to be dropped in silence — leaving the values in the old column while the
// schema claimed they had moved. It must now come back as a divergence.
func TestIntegration_BlockedRenameIsReported(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_blockedrename"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_blockedrename"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	base := mkSchema(map[string]schema.ResourceSchema{
		"pets": {Fields: map[string]schema.FieldDef{
			"name":      {Type: "string", Required: true},
			"weight":    {Type: "float64"},
			"weight_kg": {Type: "float64"}, // the target name is ALREADY taken
		}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}

	renamed := mkSchema(map[string]schema.ResourceSchema{
		"pets": {Fields: map[string]schema.FieldDef{
			"name":      {Type: "string", Required: true},
			"weight_kg": {Type: "float64", RenamedFrom: "weight"},
		}},
	})
	outcome, err := ApplyTenantMigrationApproved(ctx, pool, pg, renamed, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !outcome.Partial() {
		t.Fatalf("a rename that cannot run was reported as success — the values are still in the old column")
	}
	found := false
	for _, u := range outcome.Unapplied {
		if strings.Contains(u, "weight") && strings.Contains(u, "renamed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the report does not name the blocked rename: %v", outcome.Unapplied)
	}

	// And the dry-run says it BEFORE applying.
	pv, err := PreviewTenantMigration(ctx, pool, pg, renamed, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	blocked := false
	for _, c := range pv.Concerns {
		if strings.HasPrefix(c, "[blocked]") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("the dry-run does not warn about the blocked rename: %v", pv.Concerns)
	}
}

// TestIntegration_DeclaredEqualsApplied is the CLASS-level guarantee, independent of
// any particular operation: after an apply that reports success, re-introspecting the
// live database must find NOTHING the schema declares still pending.
//
// It is deliberately broad — it walks a schema through several kinds of change at
// once (new resource, new column, NOT NULL, unique, index, relation, repointed
// relation, renamed column) and then asks the database, not the migrator, whether the
// declaration is true.
func TestIntegration_DeclaredEqualsApplied(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_declapplied"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_declapplied"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	steps := []struct {
		name string
		s    *schema.APISchema
	}{
		{"provision", vetSchemaRefID()},
		{"repoint the relation", vetSchemaRefUserID()},
		{"add a resource, a column, an index and a second relation", func() *schema.APISchema {
			s := vetSchemaRefUserID()
			s.Resources["pets"] = schema.ResourceSchema{
				Fields: map[string]schema.FieldDef{
					"name":   {Type: "string", Required: true},
					"weight": {Type: "float64"},
				},
				Indexes: []schema.IndexDef{{Fields: []string{"name"}}},
			}
			ap := s.Resources["appointments"]
			ap.Fields["pet_id"] = schema.FieldDef{Type: "uuid", Relation: "pets", OnDelete: schema.OnDeleteCascade}
			ap.Fields["note"] = schema.FieldDef{Type: "string"}
			s.Resources["appointments"] = ap
			return s
		}()},
		{"rename a column and tighten it", func() *schema.APISchema {
			s := vetSchemaRefUserID()
			s.Resources["pets"] = schema.ResourceSchema{
				Fields: map[string]schema.FieldDef{
					"name":      {Type: "string", Required: true},
					"weight_kg": {Type: "float64", RenamedFrom: "weight"},
				},
				Indexes: []schema.IndexDef{{Fields: []string{"name"}}},
			}
			ap := s.Resources["appointments"]
			ap.Fields["pet_id"] = schema.FieldDef{Type: "uuid", Relation: "pets", OnDelete: schema.OnDeleteCascade}
			ap.Fields["note"] = schema.FieldDef{Type: "string"}
			s.Resources["appointments"] = ap
			return s
		}()},
	}

	for _, st := range steps {
		outcome, err := ApplyTenantMigrationApproved(ctx, pool, pg, st.s, nil)
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if outcome.Partial() {
			t.Fatalf("%s: apply reported success but the database is missing: %v", st.name, outcome.Unapplied)
		}
		// The independent check: re-introspect and diff. Anything non-drop left is a
		// declaration the database does not honor.
		if pending := verifyApplied(ctx, pool, pg, st.s, true); len(pending) > 0 {
			t.Fatalf("%s: declared ≠ applied — still pending after the apply: %v", st.name, pending)
		}
	}
}
