package migration

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemadiff"
)

// erpResource subset used to assert the JSON→canonical bridge structurally,
// without a database. Mirrors examples/erp-demo (departamentos + empleados).
func bridgeSchema() *schema.APISchema {
	return &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"departamentos": {
				Fields: map[string]schema.FieldDef{
					"codigo":     {Type: "string", Required: true, Unique: true},
					"nombre":     {Type: "string", Required: true},
					"activo":     {Type: "bool", Default: true},
					"created_at": {Type: "time", Auto: true},
				},
				Indexes: []schema.IndexDef{{Fields: []string{"activo"}}},
			},
			"empleados": {
				Fields: map[string]schema.FieldDef{
					"email":           {Type: "string", Required: true, Unique: true, Format: "email"},
					"departamento_id": {Type: "uuid", Relation: "departamentos"},
					"created_at":      {Type: "time", Auto: true},
				},
				Relations: map[string]schema.RelationDef{
					"departamento": {Type: schema.RelationBelongsTo, Target: "departamentos", FK: "departamento_id"},
				},
				Indexes: []schema.IndexDef{{Fields: []string{"email"}}},
			},
		},
	}
}

func TestBuildDesiredSchema_MirrorsConverger(t *testing.T) {
	ds := buildDesiredSchema("tenant_x", bridgeSchema())

	dep := ds.Tables["departamentos"]
	if dep == nil {
		t.Fatal("departamentos table missing")
	}

	// Implicit id PK: uuid, NOT NULL, DEFAULT gen_random_uuid().
	id := dep.Columns["id"]
	if id == nil || id.Type.Base != schemadiff.BaseUUID || !id.NotNull || id.Default == nil || id.Default.Raw != "gen_random_uuid()" {
		t.Errorf("id column not faithful: %+v", id)
	}
	if dep.PK == nil || len(dep.PK.Columns) != 1 || dep.PK.Columns[0] != "id" {
		t.Errorf("PK not on [id]: %+v", dep.PK)
	}

	// required → NOT NULL.
	if c := dep.Columns["nombre"]; c == nil || !c.NotNull {
		t.Errorf("required field nombre should be NOT NULL: %+v", c)
	}

	// field `default` is IGNORED (the converger emits no DB default for a regular
	// field) — critical for the re-provision no-op.
	if c := dep.Columns["activo"]; c == nil || c.Default != nil {
		t.Errorf("regular field default must NOT become a DB default: %+v", c)
	}

	// auto → TIMESTAMPTZ DEFAULT now(), nullable.
	if c := dep.Columns["created_at"]; c == nil || c.Type.Base != schemadiff.BaseTimestamptz || c.NotNull || c.Default == nil || c.Default.Raw != "now()" {
		t.Errorf("auto field not faithful: %+v", c)
	}

	// unique → UNIQUE constraint (not a standalone index).
	if _, ok := dep.Uniques["departamentos_codigo_key"]; !ok {
		t.Errorf("unique constraint missing: %+v", dep.Uniques)
	}

	// declared index → standalone btree index.
	if idx, ok := dep.Indexes["idx_departamentos_activo"]; !ok || idx.Unique || idx.Method != "btree" {
		t.Errorf("declared index idx_departamentos_activo missing/wrong: %+v", dep.Indexes)
	}

	// relation FK index on the belongs_to source column.
	emp := ds.Tables["empleados"]
	if idx, ok := emp.Indexes["idx_empleados_departamento_id"]; !ok || idx.Columns[0] != "departamento_id" {
		t.Errorf("relation FK index missing: %+v", emp.Indexes)
	}
	// email is BOTH a unique constraint AND a declared standalone index — distinct.
	if _, ok := emp.Uniques["empleados_email_key"]; !ok {
		t.Errorf("email unique constraint missing")
	}
	if _, ok := emp.Indexes["idx_empleados_email"]; !ok {
		t.Errorf("email declared index missing")
	}

	// MIG-F1-S1: a field-level relation now models a REAL foreign key (default
	// on_delete = RESTRICT) on the FK column, referencing the target's id.
	fk, ok := emp.FKs["fk_empleados_departamento_id"]
	if !ok {
		t.Fatalf("expected FK fk_empleados_departamento_id, got: %v", emp.FKs)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "departamento_id" || fk.RefTable != "departamentos" ||
		len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" || fk.OnDelete != schemadiff.Restrict {
		t.Errorf("FK not faithful: %+v", fk)
	}
	// CHECK constraints are still never modeled (the converger creates none).
	if len(emp.Checks) != 0 {
		t.Errorf("desired must model no CHECK constraints: Checks=%v", emp.Checks)
	}
}

// TestBuildDesiredSchema_OnDeleteActions verifies the on_delete → RefAction mapping
// (default restrict, plus cascade and set_null) and that set_null lands on a
// nullable column.
func TestBuildDesiredSchema_OnDeleteActions(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"parents": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		"kids": {Fields: map[string]schema.FieldDef{
			"p_restrict": {Type: "uuid", Relation: "parents"},                                   // default → restrict
			"p_cascade":  {Type: "uuid", Relation: "parents", OnDelete: schema.OnDeleteCascade}, // cascade
			"p_setnull":  {Type: "uuid", Relation: "parents", OnDelete: schema.OnDeleteSetNull}, // set_null (nullable)
		}},
	}}
	ds := buildDesiredSchema("t", s)
	kids := ds.Tables["kids"]
	cases := map[string]schemadiff.RefAction{
		"fk_kids_p_restrict": schemadiff.Restrict,
		"fk_kids_p_cascade":  schemadiff.Cascade,
		"fk_kids_p_setnull":  schemadiff.SetNull,
	}
	for sym, want := range cases {
		fk, ok := kids.FKs[sym]
		if !ok {
			t.Errorf("missing FK %s", sym)
			continue
		}
		if fk.OnDelete != want {
			t.Errorf("FK %s on_delete = %v, want %v", sym, fk.OnDelete, want)
		}
	}
}

// TestBuildDesiredSchema_OnUpdateActions verifies the on_update → RefAction mapping
// (MIG-F1-S5): the UNSET default is NO ACTION (the no-churn choice — an FK created
// without ON UPDATE already carries NO ACTION), while cascade/restrict/set_null map
// faithfully.
func TestBuildDesiredSchema_OnUpdateActions(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"parents": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		"kids": {Fields: map[string]schema.FieldDef{
			"p_default":  {Type: "uuid", Relation: "parents"},                                   // on_update unset → NO ACTION
			"p_cascade":  {Type: "uuid", Relation: "parents", OnUpdate: schema.OnUpdateCascade}, // cascade
			"p_setnull":  {Type: "uuid", Relation: "parents", OnUpdate: schema.OnUpdateSetNull}, // set_null (nullable)
			"p_restrict": {Type: "uuid", Relation: "parents", OnUpdate: schema.OnUpdateRestrict},
		}},
	}}
	ds := buildDesiredSchema("t", s)
	kids := ds.Tables["kids"]
	cases := map[string]schemadiff.RefAction{
		"fk_kids_p_default":  schemadiff.NoAction, // CRITICAL: no-churn default
		"fk_kids_p_cascade":  schemadiff.Cascade,
		"fk_kids_p_setnull":  schemadiff.SetNull,
		"fk_kids_p_restrict": schemadiff.Restrict,
	}
	for sym, want := range cases {
		fk, ok := kids.FKs[sym]
		if !ok {
			t.Errorf("missing FK %s", sym)
			continue
		}
		if fk.OnUpdate != want {
			t.Errorf("FK %s on_update = %v, want %v", sym, fk.OnUpdate, want)
		}
		// on_delete is unaffected — still RESTRICT (its own default).
		if fk.OnDelete != schemadiff.Restrict {
			t.Errorf("FK %s on_delete should stay RESTRICT, got %v", sym, fk.OnDelete)
		}
	}
}

// TestBuildDesiredSchema_ReferencesNonIDColumn verifies a field-level relation with
// `references` points the FK at the named unique column instead of id (MIG-F1-S5).
func TestBuildDesiredSchema_ReferencesNonIDColumn(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"users": {Fields: map[string]schema.FieldDef{
			"email": {Type: "string", Required: true, Unique: true, Format: "email"},
		}},
		"posts": {Fields: map[string]schema.FieldDef{
			"author_email": {Type: "string", Relation: "users", References: "email"},
		}},
	}}
	ds := buildDesiredSchema("t", s)
	fk, ok := ds.Tables["posts"].FKs["fk_posts_author_email"]
	if !ok {
		t.Fatalf("missing FK fk_posts_author_email: %v", ds.Tables["posts"].FKs)
	}
	if fk.RefTable != "users" || len(fk.RefColumns) != 1 || fk.RefColumns[0] != "email" {
		t.Errorf("FK should reference users(email), got reftable=%q refcols=%v", fk.RefTable, fk.RefColumns)
	}
	if len(fk.Columns) != 1 || fk.Columns[0] != "author_email" {
		t.Errorf("FK source column wrong: %v", fk.Columns)
	}
}

// TestBuildDesiredSchema_CompositeForeignKey verifies the resource-level foreign_keys
// block produces a real multi-column FK + a composite source index (MIG-F1-S5).
func TestBuildDesiredSchema_CompositeForeignKey(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"branches": {
			Fields: map[string]schema.FieldDef{
				"region_code": {Type: "string", Required: true},
				"branch_code": {Type: "string", Required: true},
			},
			Indexes: []schema.IndexDef{{Fields: []string{"region_code", "branch_code"}, Unique: true}},
		},
		"orders": {
			Fields: map[string]schema.FieldDef{
				"region_code": {Type: "string"},
				"branch_code": {Type: "string"},
			},
			ForeignKeys: []schema.ForeignKeyDef{{
				Columns:    []string{"region_code", "branch_code"},
				Target:     "branches",
				RefColumns: []string{"region_code", "branch_code"},
				OnDelete:   schema.OnDeleteCascade,
				OnUpdate:   schema.OnUpdateRestrict,
			}},
		},
	}}
	ds := buildDesiredSchema("t", s)
	orders := ds.Tables["orders"]
	fk, ok := orders.FKs["fk_orders_region_code_branch_code"]
	if !ok {
		t.Fatalf("missing composite FK: %v", orders.FKs)
	}
	if !equalSlice(fk.Columns, []string{"region_code", "branch_code"}) ||
		fk.RefTable != "branches" || !equalSlice(fk.RefColumns, []string{"region_code", "branch_code"}) {
		t.Errorf("composite FK not faithful: %+v", fk)
	}
	if fk.OnDelete != schemadiff.Cascade || fk.OnUpdate != schemadiff.Restrict {
		t.Errorf("composite FK actions wrong: del=%v upd=%v", fk.OnDelete, fk.OnUpdate)
	}
	if idx, ok := orders.Indexes["idx_orders_region_code_branch_code"]; !ok || !equalSlice(idx.Columns, []string{"region_code", "branch_code"}) {
		t.Errorf("composite FK source index missing/wrong: %v", orders.Indexes)
	}
}

// TestBuildDesiredSchema_AllFKFormsSelfDiffEmpty is the no-churn guardrail for the
// three new forms: a schema using on_update, references and a composite FK diffs to
// nothing against itself (the model is internally consistent — idempotent).
func TestBuildDesiredSchema_AllFKFormsSelfDiffEmpty(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"users": {Fields: map[string]schema.FieldDef{
			"email": {Type: "string", Required: true, Unique: true},
		}},
		"branches": {
			Fields: map[string]schema.FieldDef{
				"region_code": {Type: "string", Required: true},
				"branch_code": {Type: "string", Required: true},
			},
			Indexes: []schema.IndexDef{{Fields: []string{"region_code", "branch_code"}, Unique: true}},
		},
		"posts": {
			Fields: map[string]schema.FieldDef{
				"author_email": {Type: "string", Relation: "users", References: "email", OnUpdate: schema.OnUpdateCascade},
				"region_code":  {Type: "string"},
				"branch_code":  {Type: "string"},
			},
			ForeignKeys: []schema.ForeignKeyDef{{
				Columns: []string{"region_code", "branch_code"}, Target: "branches",
				RefColumns: []string{"region_code", "branch_code"}, OnDelete: schema.OnDeleteCascade,
			}},
		},
	}}
	ds := buildDesiredSchema("t", s)
	plan, err := schemadiff.Diff(ds, ds)
	if err != nil {
		t.Fatalf("self-diff: %v", err)
	}
	if !plan.Empty() {
		t.Errorf("desired self-diff must be empty:\n%s", plan)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildDesiredSchema_SelfDiffEmpty is the pure (no-DB) guardrail: the desired
// schema diffs to nothing against itself — the model is internally consistent.
func TestBuildDesiredSchema_SelfDiffEmpty(t *testing.T) {
	ds := buildDesiredSchema("tenant_x", bridgeSchema())
	plan, err := schemadiff.Diff(ds, ds)
	if err != nil {
		t.Fatalf("self-diff: %v", err)
	}
	if !plan.Empty() {
		t.Errorf("desired self-diff must be empty:\n%s", plan)
	}
}

// TestPartitionByPolicy_GatesDrops verifies the fail-safe default: with NO approval,
// every DROP op is gated, every other op is applied (the additive v1 policy).
func TestPartitionByPolicy_GatesDrops(t *testing.T) {
	plan := &schemadiff.Plan{Ops: []schemadiff.Operation{
		schemadiff.CreateTable{Table: schemadiff.NewTable("t")},
		schemadiff.AddColumn{Table: "t", Column: &schemadiff.Column{Name: "c", Type: schemadiff.Type{Base: schemadiff.BaseText}}},
		schemadiff.DropColumn{Table: "t", Column: &schemadiff.Column{Name: "old"}},
		schemadiff.DropTable{Table: schemadiff.NewTable("gone")},
		schemadiff.RenameColumn{Table: "t", From: "a", To: "b"},
		schemadiff.DropIndex{Table: "t", Index: &schemadiff.Index{Name: "idx_old"}},
	}}
	apply, gated, applied := partitionByPolicyApproved(plan, nil)
	if len(apply.Ops) != 3 {
		t.Errorf("expected 3 applied ops (create/add/rename), got %d: %v", len(apply.Ops), apply)
	}
	if len(gated) != 3 {
		t.Errorf("expected 3 gated drops, got %d: %v", len(gated), gated)
	}
	if len(applied) != 0 {
		t.Errorf("no destructive drop may be applied without approval, got %v", applied)
	}
	for _, op := range apply.Ops {
		if isDropOp(op.Kind()) {
			t.Errorf("a drop op leaked into the apply set: %s", op)
		}
	}
}

// TestPartitionByPolicy_ApprovedDropApplies verifies the gate opens ONLY for the
// enumerated destructive keys: an approved drop is applied, an un-approved one stays
// gated, and a safe drop is never applied (additive drift), even alongside approvals.
func TestPartitionByPolicy_ApprovedDropApplies(t *testing.T) {
	plan := &schemadiff.Plan{Ops: []schemadiff.Operation{
		schemadiff.DropColumn{Table: "empleados", Column: &schemadiff.Column{Name: "telefono"}},
		schemadiff.DropColumn{Table: "empleados", Column: &schemadiff.Column{Name: "fax"}},
		schemadiff.DropTable{Table: schemadiff.NewTable("proyectos")},
		schemadiff.DropIndex{Table: "empleados", Index: &schemadiff.Index{Name: "idx_old"}},
	}}
	// Approve ONLY empleados.telefono and proyectos — fax and the index must stay gated.
	apply, gated, applied := partitionByPolicyApproved(plan, map[string]bool{
		"empleados.telefono": true,
		"proyectos":          true,
	})
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied drops, got %d: %v", len(applied), applied)
	}
	gotApplied := map[string]bool{}
	for _, k := range applied {
		gotApplied[k] = true
	}
	if !gotApplied["empleados.telefono"] || !gotApplied["proyectos"] {
		t.Errorf("approved keys not applied: %v", applied)
	}
	// fax (unapproved destructive) + idx_old (safe drop) must be gated.
	if len(gated) != 2 {
		t.Errorf("expected 2 gated ops (unapproved fax + safe index), got %d: %v", len(gated), gated)
	}
	if len(apply.Ops) != 2 {
		t.Errorf("expected 2 ops in apply plan (the 2 approved drops), got %d: %v", len(apply.Ops), apply)
	}
}
