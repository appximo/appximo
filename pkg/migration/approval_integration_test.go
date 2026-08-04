package migration

import (
	"context"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These integration tests exercise the DESTRUCTIVE-OPERATION APPROVAL GATE end to
// end against a real Postgres: a data-losing drop is reported by the dry-run preview
// (with its impact), gated by default, and applied ONLY when its key is explicitly
// enumerated — never by accident, never "yes to everything".

// helper: total + non-null counts for a column.
func colNonNull(t *testing.T, pool *pgxpool.Pool, qualified, col string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM "+qualified+" WHERE "+col+" IS NOT NULL").Scan(&n); err != nil {
		t.Fatalf("count non-null %s.%s: %v", qualified, col, err)
	}
	return n
}

func tableExists(t *testing.T, pool *pgxpool.Pool, pg, table string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2)`,
		pg, table).Scan(&ok); err != nil {
		t.Fatalf("table exists %s.%s: %v", pg, table, err)
	}
	return ok
}

// findDestructive returns the preview's destructive op for key, or fails.
func findDestructive(t *testing.T, pv *Preview, key string) DestructiveOp {
	t.Helper()
	for _, d := range pv.Destructive {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("preview has no destructive op for key %q (have: %+v)", key, pv.Destructive)
	return DestructiveOp{}
}

// TestApproval_ColumnDrop_GateAndApprove covers a FIELD removal:
//   - dry-run reports DropColumn with the right impact (rows with a non-null value)
//   - applying WITHOUT approval gates it (column + data stay as drift)
//   - applying with a DIFFERENT approval still gates it (approval is specific)
//   - applying with the EXACT key drops it (data-losing drop executed by consent)
func TestApproval_ColumnDrop_GateAndApprove(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_appcol"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_appcol"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"empleados": {Fields: map[string]schema.FieldDef{
			"nombre":   {Type: "string", Required: true},
			"telefono": {Type: "string"},
			"fax":      {Type: "string"},
		}},
	})
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, base, nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// 3 rows: 2 with telefono non-null, 1 with telefono NULL.
	pool.Exec(ctx, `INSERT INTO tenant_appcol.empleados (nombre, telefono) VALUES ('a','111'),('b','222')`) //nolint:errcheck
	pool.Exec(ctx, `INSERT INTO tenant_appcol.empleados (nombre) VALUES ('c')`)                             //nolint:errcheck

	// Desired schema removes telefono AND fax.
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"empleados": {Fields: map[string]schema.FieldDef{
			"nombre": {Type: "string", Required: true},
		}},
	})

	// ── dry-run: reports both drops with impact, applies nothing ──
	pv, err := PreviewTenantMigration(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !pv.HasDestructive() || len(pv.Destructive) != 2 {
		t.Fatalf("preview must report 2 destructive drops, got: %+v", pv.Destructive)
	}
	tel := findDestructive(t, pv, "empleados.telefono")
	if tel.RowsLost != 2 || tel.TableRows != 3 || tel.Approved {
		t.Errorf("telefono impact wrong: got RowsLost=%d TableRows=%d approved=%v, want 2/3/false (%s)",
			tel.RowsLost, tel.TableRows, tel.Approved, tel.Summary)
	}
	// Preview is a pure dry-run: the columns must STILL exist afterwards.
	if !columnSet(t, pool, pg, "empleados")["telefono"] {
		t.Fatalf("dry-run must NOT drop anything — telefono is gone")
	}

	// ── apply WITHOUT approval → gated (data preserved) ──
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("apply (no approval): %v", err)
	}
	if len(out.AppliedDrops) != 0 {
		t.Errorf("no approval ⇒ no drop applied, got %v", out.AppliedDrops)
	}
	cols := columnSet(t, pool, pg, "empleados")
	if !cols["telefono"] || !cols["fax"] {
		t.Fatalf("gated drops lost columns: %v", cols)
	}
	if n := colNonNull(t, pool, "tenant_appcol.empleados", "telefono"); n != 2 {
		t.Fatalf("gated drop lost data: telefono non-null=%d, want 2", n)
	}

	// ── apply approving ONLY fax → telefono STILL gated (approval is specific) ──
	out, err = ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, []string{"empleados.fax"})
	if err != nil {
		t.Fatalf("apply (approve fax): %v", err)
	}
	if len(out.AppliedDrops) != 1 || out.AppliedDrops[0] != "empleados.fax" {
		t.Errorf("expected only fax applied, got %v", out.AppliedDrops)
	}
	cols = columnSet(t, pool, pg, "empleados")
	if cols["fax"] {
		t.Errorf("approved drop fax must be gone")
	}
	if !cols["telefono"] {
		t.Errorf("telefono was NOT approved — it must survive (approval is specific, not 'yes to all')")
	}
	if n := colNonNull(t, pool, "tenant_appcol.empleados", "telefono"); n != 2 {
		t.Errorf("telefono data must be intact, non-null=%d want 2", n)
	}

	// ── apply approving telefono → finally dropped ──
	out, err = ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, []string{"empleados.telefono"})
	if err != nil {
		t.Fatalf("apply (approve telefono): %v", err)
	}
	if len(out.AppliedDrops) != 1 || out.AppliedDrops[0] != "empleados.telefono" {
		t.Errorf("expected telefono applied, got %v", out.AppliedDrops)
	}
	if columnSet(t, pool, pg, "empleados")["telefono"] {
		t.Errorf("approved telefono must be dropped")
	}
	// nombre + its 3 rows intact.
	if n := countRows(t, pool, "tenant_appcol.empleados"); n != 3 {
		t.Errorf("rows must be intact, got %d want 3", n)
	}
}

// TestApproval_TableDrop covers a RESOURCE (table) removal: dry-run impact, gated by
// default, dropped only on explicit approval, then idempotent.
func TestApproval_TableDrop(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_apptbl"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_apptbl"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"widgets":   {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		"proyectos": {Fields: map[string]schema.FieldDef{"code": {Type: "string"}}},
	})
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, base, nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO tenant_apptbl.proyectos (code) VALUES ('p1'),('p2'),('p3'),('p4'),('p5')`) //nolint:errcheck
	pool.Exec(ctx, `INSERT INTO tenant_apptbl.widgets (name) VALUES ('w1')`)                               //nolint:errcheck

	// Desired removes the proyectos resource entirely.
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"widgets": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})

	// ── dry-run: reports DropTable proyectos with the row count ──
	pv, err := PreviewTenantMigration(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	d := findDestructive(t, pv, "proyectos")
	if d.Kind != "table" || d.RowsLost != 5 {
		t.Errorf("proyectos table-drop impact wrong: kind=%s rows=%d, want table/5 (%s)", d.Kind, d.RowsLost, d.Summary)
	}
	if !tableExists(t, pool, pg, "proyectos") {
		t.Fatalf("dry-run must not drop the table")
	}

	// ── apply WITHOUT approval → gated (table + rows stay) ──
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, nil); err != nil {
		t.Fatalf("apply (no approval): %v", err)
	}
	if !tableExists(t, pool, pg, "proyectos") {
		t.Fatalf("table drop must be GATED without approval")
	}
	if n := countRows(t, pool, "tenant_apptbl.proyectos"); n != 5 {
		t.Fatalf("gated table drop lost rows: %d", n)
	}

	// ── apply approving proyectos → table dropped ──
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, []string{"proyectos"})
	if err != nil {
		t.Fatalf("apply (approve proyectos): %v", err)
	}
	if len(out.AppliedDrops) != 1 || out.AppliedDrops[0] != "proyectos" {
		t.Errorf("expected proyectos applied, got %v", out.AppliedDrops)
	}
	if tableExists(t, pool, pg, "proyectos") {
		t.Fatalf("approved table drop must remove the table")
	}
	if !tableExists(t, pool, pg, "widgets") {
		t.Fatalf("widgets must be untouched")
	}

	// ── idempotent: re-apply the shrunk schema → no destructives, no-op ──
	pv2, err := PreviewTenantMigration(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("re-preview: %v", err)
	}
	if !pv2.Empty {
		t.Errorf("re-provision after the drop must be a no-op, got: %+v", pv2)
	}
}

// TestApproval_TableDrop_WithIncomingFK proves an approved table drop stays coherent
// even when another (kept) table still has a foreign key referencing it: the FK is
// dropped as a necessary, data-safe consequence so the DROP TABLE succeeds (rather
// than failing on the dangling reference).
func TestApproval_TableDrop_WithIncomingFK(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_appfk"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_appfk"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		"orders": {Fields: map[string]schema.FieldDef{
			"customer_id": {Type: "uuid", Relation: "customers"}, // real FK → customers.id
		}},
	})
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, base, nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO tenant_appfk.customers (name) VALUES ('acme'),('globex')`) //nolint:errcheck

	// Remove the customers resource; keep orders.customer_id as a plain uuid (drop the
	// relation). The FK orders→customers now has no place in the desired schema.
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"orders": {Fields: map[string]schema.FieldDef{
			"customer_id": {Type: "uuid"}, // FK relation removed; column kept
		}},
	})

	// Approve dropping the customers TABLE. The incoming FK from orders must be
	// dropped as a consequence so DROP TABLE customers succeeds.
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, shrunk, []string{"customers"})
	if err != nil {
		t.Fatalf("approved table drop with incoming FK must succeed (FK dropped as a consequence): %v", err)
	}
	if len(out.AppliedDrops) != 1 || out.AppliedDrops[0] != "customers" {
		t.Errorf("expected customers applied, got %v", out.AppliedDrops)
	}
	if tableExists(t, pool, pg, "customers") {
		t.Fatalf("customers table must be dropped")
	}
	// orders + its customer_id column survive (the column was kept, only the FK went).
	if !columnSet(t, pool, pg, "orders")["customer_id"] {
		t.Fatalf("orders.customer_id must survive (only the FK was removed)")
	}
	// The dangling FK must be gone (verified structurally: a new insert is unconstrained).
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_appfk.orders (customer_id) VALUES (gen_random_uuid())`); err != nil {
		t.Fatalf("after the FK was dropped, an unconstrained insert should work: %v", err)
	}
}

// TestApproval_HappyPath_NoDestructive proves an additive schema needs no approval:
// the preview reports no destructives, and a plain apply works as always.
func TestApproval_HappyPath_NoDestructive(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_apphappy"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_apphappy"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"tasks": {Fields: map[string]schema.FieldDef{"title": {Type: "string", Required: true}}},
	})
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, base, nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO tenant_apphappy.tasks (title) VALUES ('t1')`) //nolint:errcheck

	// Additive change: add a nullable column.
	evolved := mkSchema(map[string]schema.ResourceSchema{
		"tasks": {Fields: map[string]schema.FieldDef{
			"title": {Type: "string", Required: true},
			"note":  {Type: "text"},
		}},
	})

	pv, err := PreviewTenantMigration(ctx, pool, pg, evolved, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv.HasDestructive() {
		t.Errorf("additive schema must report NO destructives, got: %+v", pv.Destructive)
	}
	if len(pv.Apply) == 0 {
		t.Errorf("preview should list the ADD COLUMN as an apply op")
	}

	// Plain apply (no approval) does the additive change.
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, evolved, nil); err != nil {
		t.Fatalf("apply additive: %v", err)
	}
	if !columnSet(t, pool, pg, "tasks")["note"] {
		t.Fatalf("additive column 'note' must be added")
	}
	if n := countRows(t, pool, "tenant_apphappy.tasks"); n != 1 {
		t.Fatalf("additive change must preserve rows, got %d", n)
	}
}

// TestApproval_AdditivePath_NoOrphanDrops proves the PURELY ADDITIVE entry point
// (ApplyTenantMigration — the worker/register path) never even proposes dropping a
// removed-resource table: a removed resource is invisible drift there, byte-identical
// to the historical behavior (only the approval-aware path surfaces it).
func TestApproval_AdditivePath_NoOrphanDrops(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_appadditive"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_appadditive"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := mkSchema(map[string]schema.ResourceSchema{
		"a": {Fields: map[string]schema.FieldDef{"x": {Type: "string"}}},
		"b": {Fields: map[string]schema.FieldDef{"y": {Type: "string"}}},
	})
	if err := ApplyTenantMigration(ctx, pool, pg, base); err != nil {
		t.Fatalf("provision: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO tenant_appadditive.b (y) VALUES ('keep')`) //nolint:errcheck

	// Remove resource b. The additive path computes an EMPTY diff (b is invisible).
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"a": {Fields: map[string]schema.FieldDef{"x": {Type: "string"}}},
	})
	plan, _, err := diffTenant(ctx, pool, pg, shrunk, false) // includeOrphans=false (additive)
	if err != nil {
		t.Fatalf("additive diff: %v", err)
	}
	if !plan.Empty() {
		t.Errorf("additive diff of a removed resource must be EMPTY (invisible drift), got:\n%s", plan)
	}
	// The additive apply is a no-op; table b + its row stay.
	if err := ApplyTenantMigration(ctx, pool, pg, shrunk); err != nil {
		t.Fatalf("additive apply: %v", err)
	}
	if !tableExists(t, pool, pg, "b") {
		t.Fatalf("additive path must NEVER drop a table — b is gone")
	}

	// The approval-aware path DOES surface b as an approvable DropTable.
	pv, err := PreviewTenantMigration(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("approval-aware preview: %v", err)
	}
	if !pv.HasDestructive() {
		t.Fatalf("approval-aware preview must surface the removed resource b as a DropTable")
	}
	bd := findDestructive(t, pv, "b") // asserts b is present as a destructive drop
	if bd.Kind != "table" || bd.RowsLost != 1 {
		t.Errorf("b drop impact wrong: kind=%s rows=%d, want table/1", bd.Kind, bd.RowsLost)
	}
}
