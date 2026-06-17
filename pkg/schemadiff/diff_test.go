package schemadiff_test

import (
	"context"
	"testing"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

// assertConverges materializes `current` in a fresh ephemeral schema, diffs it to
// `desired`, applies the plan, and verifies the result is structurally equivalent
// to `desired` (convergence) and that re-diffing is empty (idempotence / fixed
// point). It also checks materialization fidelity (current round-trips). It
// returns the migration plan for further op-level assertions. For rename-free
// schemas only (a desired with RenamedFrom needs its source present in current).
func assertConverges(t *testing.T, current, desired *sd.Schema) *sd.Plan {
	t.Helper()
	pool := requireDB(t)
	schema := setupSchema(t, pool, "")

	mp, err := sd.Diff(current, sd.NewSchema(""))
	if err != nil {
		t.Fatalf("diff(current, ∅): %v", err)
	}
	apply(t, pool, schema, mp)
	rc := introspectSchema(t, pool, schema)
	if !schemaEquivalent(rc, current) {
		t.Fatalf("materialization fidelity failed: introspected != current\n-- introspected --\n%s\n-- current --\n%s", render(rc), render(current))
	}

	plan, err := sd.Diff(desired, rc)
	if err != nil {
		t.Fatalf("diff(desired, current): %v", err)
	}
	apply(t, pool, schema, plan)
	final := introspectSchema(t, pool, schema)
	if !schemaEquivalent(final, desired) {
		t.Fatalf("convergence failed: final != desired\n-- plan --\n%s\n-- final --\n%s\n-- desired --\n%s", plan, render(final), render(desired))
	}

	if p2, err := sd.Diff(desired, final); err != nil {
		t.Fatalf("re-diff: %v", err)
	} else if !p2.Empty() {
		t.Errorf("idempotence failed: re-diff is not empty:\n%s", p2)
	}
	return plan
}

// ── self-diff (the diff(A,A)=∅ guardrail, pure / no DB) ─────────────────────────

func TestDiff_SelfIsEmpty(t *testing.T) {
	rich := sch(
		tbl("parent", []string{"id"},
			col("id", sd.BaseUUID, true),
			col("code", sd.BaseText, true),
		),
		tbl("child", []string{"id"},
			col("id", sd.BaseUUID, true),
			colT("name", sd.Type{Base: sd.BaseVarchar, Size: 120}, true),
			col("qty", sd.BaseInteger, false),
		),
	)
	rich.Tables["parent"].Uniques["parent_code_key"] = &sd.UniqueConstraint{Symbol: "parent_code_key", Columns: []string{"code"}}
	rich.Tables["child"].Indexes["idx_child_name"] = &sd.Index{Name: "idx_child_name", Columns: []string{"name"}, Method: "btree"}
	rich.Tables["child"].FKs["child_parent_fkey"] = &sd.ForeignKey{
		Symbol: "child_parent_fkey", Columns: []string{"id"}, RefTable: "parent", RefColumns: []string{"id"}, OnDelete: sd.Cascade,
	}

	plan, err := sd.Diff(rich, rich)
	if err != nil {
		t.Fatalf("diff(A,A): %v", err)
	}
	if !plan.Empty() {
		t.Errorf("diff(A,A) must be empty, got:\n%s", plan)
	}
}

// ── tables ──────────────────────────────────────────────────────────────────────

func TestDiff_CreateTable(t *testing.T) {
	current := sch()
	desired := sch(tbl("widget", []string{"id"},
		col("id", sd.BaseUUID, true),
		col("name", sd.BaseText, true),
	))
	plan := assertConverges(t, current, desired)
	if !hasOp(plan, func(o sd.Operation) bool { ct, ok := o.(sd.CreateTable); return ok && ct.Table.Name == "widget" }) {
		t.Errorf("expected CreateTable(widget):\n%s", plan)
	}
}

func TestDiff_DropTable(t *testing.T) {
	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true)))
	desired := sch()
	plan := assertConverges(t, current, desired)
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropTable); return ok }) {
		t.Errorf("expected DropTable:\n%s", plan)
	}
}

// ── columns ─────────────────────────────────────────────────────────────────────

func TestDiff_AddColumn(t *testing.T) {
	base := func() *sd.Table {
		return tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true))
	}
	current := sch(base())
	d := base()
	d.AddColumn(col("age", sd.BaseInteger, false)) // nullable add → safe
	desired := sch(d)
	plan := assertConverges(t, current, desired)
	add := func(o sd.Operation) bool { ac, ok := o.(sd.AddColumn); return ok && ac.Column.Name == "age" }
	if !hasOp(plan, add) {
		t.Fatalf("expected AddColumn(age):\n%s", plan)
	}
	for _, o := range plan.Ops {
		if ac, ok := o.(sd.AddColumn); ok && ac.Column.Name == "age" && ac.Risk() != sd.RiskSafe {
			t.Errorf("nullable AddColumn should be RiskSafe, got %s", ac.Risk())
		}
	}
}

func TestDiff_AddColumn_NotNullNoDefault_IsBackfillRisk(t *testing.T) {
	// Pure plan check (the diagnostic's case B): a NOT NULL add with no default is
	// flagged RiskBackfill / RequiresBackfill — the engine's converger silently
	// dropped the NOT NULL instead.
	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true)))
	d := tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("sku", sd.BaseText, true))
	plan, err := sd.Diff(sch(d), current)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	found := false
	for _, o := range plan.Ops {
		if ac, ok := o.(sd.AddColumn); ok && ac.Column.Name == "sku" {
			found = true
			if ac.Risk() != sd.RiskBackfill || !ac.RequiresBackfill() {
				t.Errorf("NOT NULL no-default add: risk=%s backfill=%t, want backfill/true", ac.Risk(), ac.RequiresBackfill())
			}
		}
	}
	if !found {
		t.Fatalf("expected AddColumn(sku):\n%s", plan)
	}
}

func TestDiff_DropColumn(t *testing.T) {
	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("note", sd.BaseText, false)))
	desired := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true)))
	plan := assertConverges(t, current, desired)
	drop := func(o sd.Operation) bool { dc, ok := o.(sd.DropColumn); return ok && dc.Column.Name == "note" }
	if !hasOp(plan, drop) {
		t.Fatalf("expected DropColumn(note):\n%s", plan)
	}
	for _, o := range plan.Ops {
		if dc, ok := o.(sd.DropColumn); ok && dc.Risk() != sd.RiskDestructive {
			t.Errorf("DropColumn should be RiskDestructive, got %s", dc.Risk())
		}
	}
}

func TestDiff_AlterColumn_Type(t *testing.T) {
	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("qty", sd.BaseInteger, false)))
	desired := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("qty", sd.BaseBigint, false)))
	plan := assertConverges(t, current, desired)
	found := false
	for _, o := range plan.Ops {
		if ac, ok := o.(sd.AlterColumn); ok && ac.To.Name == "qty" {
			found = true
			if !ac.TypeChanged() || ac.Risk() != sd.RiskTransformational {
				t.Errorf("type change: typeChanged=%t risk=%s, want true/transformational", ac.TypeChanged(), ac.Risk())
			}
		}
	}
	if !found {
		t.Fatalf("expected AlterColumn(qty):\n%s", plan)
	}
}

func TestDiff_AlterColumn_Nullability(t *testing.T) {
	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, false)))
	desired := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))
	plan := assertConverges(t, current, desired)
	found := false
	for _, o := range plan.Ops {
		if ac, ok := o.(sd.AlterColumn); ok && ac.To.Name == "name" {
			found = true
			if !ac.NullabilityAdded() || ac.Risk() != sd.RiskBackfill {
				t.Errorf("set not null: nullabilityAdded=%t risk=%s", ac.NullabilityAdded(), ac.Risk())
			}
		}
	}
	if !found {
		t.Fatalf("expected AlterColumn(name):\n%s", plan)
	}
}

// ── renames preserve data (the diagnostic's worst bug, case F) ──────────────────

func TestDiff_RenameColumn_PreservesData(t *testing.T) {
	pool := requireDB(t)
	c := context.Background()
	schema := setupSchema(t, pool, "")

	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))
	mp, _ := sd.Diff(current, sd.NewSchema(""))
	apply(t, pool, schema, mp)
	if _, err := pool.Exec(c, `INSERT INTO `+q(schema)+`.`+q("widget")+` (id, name) VALUES (gen_random_uuid(), 'hello')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	rc := introspectSchema(t, pool, schema)

	title := col("title", sd.BaseText, true)
	title.RenamedFrom = "name"
	desired := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), title))

	plan, err := sd.Diff(desired, rc)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	// The fix: a RENAME, never drop+add (which would strand 'hello' in the dead column).
	if !hasOp(plan, func(o sd.Operation) bool {
		rc, ok := o.(sd.RenameColumn)
		return ok && rc.Table == "widget" && rc.From == "name" && rc.To == "title"
	}) {
		t.Fatalf("expected RenameColumn(name->title), got:\n%s", plan)
	}
	if n := countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropColumn); return ok }); n != 0 {
		t.Errorf("rename must NOT drop a column (got %d DropColumn):\n%s", n, plan)
	}
	if n := countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddColumn); return ok }); n != 0 {
		t.Errorf("rename must NOT add a column (got %d AddColumn):\n%s", n, plan)
	}

	apply(t, pool, schema, plan)
	var got string
	if err := pool.QueryRow(c, `SELECT title FROM `+q(schema)+`.`+q("widget")).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != "hello" {
		t.Errorf("data lost across rename: title=%q, want \"hello\"", got)
	}
}

func TestDiff_RenameTable_PreservesData(t *testing.T) {
	pool := requireDB(t)
	c := context.Background()
	schema := setupSchema(t, pool, "")

	current := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))
	mp, _ := sd.Diff(current, sd.NewSchema(""))
	apply(t, pool, schema, mp)
	if _, err := pool.Exec(c, `INSERT INTO `+q(schema)+`.`+q("widget")+` (id, name) VALUES (gen_random_uuid(), 'world')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	rc := introspectSchema(t, pool, schema)

	renamed := tbl("gadget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true))
	renamed.RenamedFrom = "widget"
	desired := sch(renamed)

	plan, err := sd.Diff(desired, rc)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !hasOp(plan, func(o sd.Operation) bool {
		rt, ok := o.(sd.RenameTable)
		return ok && rt.From == "widget" && rt.To == "gadget"
	}) {
		t.Fatalf("expected RenameTable(widget->gadget), got:\n%s", plan)
	}
	if n := countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropTable); return ok }); n != 0 {
		t.Errorf("rename must NOT drop a table (got %d):\n%s", n, plan)
	}
	if n := countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.CreateTable); return ok }); n != 0 {
		t.Errorf("rename must NOT create a table (got %d):\n%s", n, plan)
	}

	apply(t, pool, schema, plan)
	var got string
	if err := pool.QueryRow(c, `SELECT name FROM `+q(schema)+`.`+q("gadget")).Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != "world" {
		t.Errorf("data lost across table rename: name=%q, want \"world\"", got)
	}

	// Malformed rename intent (source absent) is an error, not a silent drop+add.
	bad := tbl("ghost", []string{"id"}, col("id", sd.BaseUUID, true))
	bad.RenamedFrom = "does_not_exist"
	if _, err := sd.Diff(sch(bad), rc); err == nil {
		t.Error("expected error for RenamedFrom naming a non-existent table")
	}
}

// ── foreign keys (with referential actions — none of which the converger emits) ──

func TestDiff_ForeignKey_AddChangeDrop(t *testing.T) {
	parent := func() *sd.Table { return tbl("parent", []string{"id"}, col("id", sd.BaseUUID, true)) }
	childNoFK := func() *sd.Table {
		return tbl("child", []string{"id"}, col("id", sd.BaseUUID, true), col("parent_id", sd.BaseUUID, false))
	}
	withFK := func(action sd.RefAction) *sd.Table {
		ch := childNoFK()
		ch.FKs["child_parent_fkey"] = &sd.ForeignKey{
			Symbol: "child_parent_fkey", Columns: []string{"parent_id"},
			RefTable: "parent", RefColumns: []string{"id"}, OnDelete: action,
		}
		return ch
	}

	// add
	plan := assertConverges(t, sch(parent(), childNoFK()), sch(parent(), withFK(sd.Cascade)))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddForeignKey); return ok }) {
		t.Errorf("expected AddForeignKey:\n%s", plan)
	}

	// change action CASCADE -> RESTRICT (drop + re-add)
	plan = assertConverges(t, sch(parent(), withFK(sd.Cascade)), sch(parent(), withFK(sd.Restrict)))
	if countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropForeignKey); return ok }) != 1 ||
		countOps(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddForeignKey); return ok }) != 1 {
		t.Errorf("FK action change should drop+add:\n%s", plan)
	}

	// drop
	plan = assertConverges(t, sch(parent(), withFK(sd.Cascade)), sch(parent(), childNoFK()))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropForeignKey); return ok }) {
		t.Errorf("expected DropForeignKey:\n%s", plan)
	}
}

// ── indexes & unique constraints ────────────────────────────────────────────────

func TestDiff_Index_AddDrop(t *testing.T) {
	withIdx := func() *sd.Table {
		w := tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("status", sd.BaseText, false))
		w.Indexes["idx_widget_status"] = &sd.Index{Name: "idx_widget_status", Columns: []string{"status"}, Method: "btree"}
		return w
	}
	noIdx := func() *sd.Table {
		return tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("status", sd.BaseText, false))
	}
	plan := assertConverges(t, sch(noIdx()), sch(withIdx()))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddIndex); return ok }) {
		t.Errorf("expected AddIndex:\n%s", plan)
	}
	plan = assertConverges(t, sch(withIdx()), sch(noIdx()))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropIndex); return ok }) {
		t.Errorf("expected DropIndex:\n%s", plan)
	}
}

func TestDiff_Unique_AddDrop(t *testing.T) {
	withU := func() *sd.Table {
		w := tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("code", sd.BaseText, true))
		w.Uniques["uq_widget_code"] = &sd.UniqueConstraint{Symbol: "uq_widget_code", Columns: []string{"code"}}
		return w
	}
	noU := func() *sd.Table {
		return tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("code", sd.BaseText, true))
	}
	plan := assertConverges(t, sch(noU()), sch(withU()))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddUnique); return ok }) {
		t.Errorf("expected AddUnique:\n%s", plan)
	}
	plan = assertConverges(t, sch(withU()), sch(noU()))
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropUnique); return ok }) {
		t.Errorf("expected DropUnique:\n%s", plan)
	}
}

// ── check constraints (expression text is normalized by Postgres, so assert the
//    op + presence/absence after apply rather than full structural equivalence) ──

func TestDiff_Check_AddDrop(t *testing.T) {
	pool := requireDB(t)
	schema := setupSchema(t, pool, "")

	// Materialize a table WITHOUT a check, then introspect the real current.
	base := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("qty", sd.BaseInteger, false)))
	mp, _ := sd.Diff(base, sd.NewSchema(""))
	apply(t, pool, schema, mp)
	rc := introspectSchema(t, pool, schema)

	// Desired adds a check; author the predicate as Postgres stores it.
	wChk := tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("qty", sd.BaseInteger, false))
	wChk.Checks["widget_qty_chk"] = &sd.Check{Symbol: "widget_qty_chk", Expression: "(qty >= 0)"}
	plan, err := sd.Diff(sch(wChk), rc)
	if err != nil {
		t.Fatalf("diff add check: %v", err)
	}
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddCheck); return ok }) {
		t.Fatalf("expected AddCheck:\n%s", plan)
	}
	apply(t, pool, schema, plan)
	afterAdd := introspectSchema(t, pool, schema)
	if len(afterAdd.Tables["widget"].Checks) != 1 {
		t.Fatalf("expected 1 check after add, got %d", len(afterAdd.Tables["widget"].Checks))
	}

	// Now drop it: desired has no check.
	dropPlan, err := sd.Diff(base, afterAdd)
	if err != nil {
		t.Fatalf("diff drop check: %v", err)
	}
	if !hasOp(dropPlan, func(o sd.Operation) bool { _, ok := o.(sd.DropCheck); return ok }) {
		t.Fatalf("expected DropCheck:\n%s", dropPlan)
	}
	apply(t, pool, schema, dropPlan)
	afterDrop := introspectSchema(t, pool, schema)
	if len(afterDrop.Tables["widget"].Checks) != 0 {
		t.Errorf("expected 0 checks after drop, got %d", len(afterDrop.Tables["widget"].Checks))
	}
}

// ── primary key add/drop ────────────────────────────────────────────────────────

func TestDiff_PrimaryKey_AddDrop(t *testing.T) {
	withPK := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true)))
	noPK := sch(tbl("widget", nil, col("id", sd.BaseUUID, true)))

	plan := assertConverges(t, noPK, withPK)
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.AddPrimaryKey); return ok }) {
		t.Errorf("expected AddPrimaryKey:\n%s", plan)
	}
	plan = assertConverges(t, withPK, noPK)
	if !hasOp(plan, func(o sd.Operation) bool { _, ok := o.(sd.DropPrimaryKey); return ok }) {
		t.Errorf("expected DropPrimaryKey:\n%s", plan)
	}
}
