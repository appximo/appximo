package migration

import (
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemadiff"
)

// TestBuildDesiredSchema_FileField asserts the `file` field bridge
// (FILES-LINK-S1): the column is a plain UUID, its FK targets the engine's
// per-tenant files table at id (restrict by default, set_null honored,
// on_update NO ACTION), and the column is auto-indexed like every other FK.
func TestBuildDesiredSchema_FileField(t *testing.T) {
	s := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"pacientes": {
				Fields: map[string]schema.FieldDef{
					"nombre":  {Type: "string", Required: true},
					"formula": {Type: "file"},
					"foto":    {Type: "file", OnDelete: "set_null"},
				},
			},
		},
	}
	ds := buildDesiredSchema("tenant_x", s)
	tbl := ds.Tables["pacientes"]
	if tbl == nil {
		t.Fatal("pacientes table missing")
	}

	if c := tbl.Columns["formula"]; c == nil || c.Type.Base != schemadiff.BaseUUID || c.NotNull {
		t.Errorf("file column should be a nullable UUID: %+v", c)
	}

	fk := tbl.FKs["fk_pacientes_formula"]
	if fk == nil {
		t.Fatal("file FK missing")
	}
	if fk.RefTable != "files" || len(fk.RefColumns) != 1 || fk.RefColumns[0] != "id" {
		t.Errorf("file FK must reference files(id): %+v", fk)
	}
	if fk.OnDelete != schemadiff.Restrict {
		t.Errorf("unset on_delete must default to RESTRICT: %v", fk.OnDelete)
	}
	if fk.OnUpdate != schemadiff.NoAction {
		t.Errorf("file FK on_update must be NO ACTION: %v", fk.OnUpdate)
	}
	if fk2 := tbl.FKs["fk_pacientes_foto"]; fk2 == nil || fk2.OnDelete != schemadiff.SetNull {
		t.Errorf("set_null on_delete not honored: %+v", fk2)
	}

	if idx := tbl.Indexes["idx_pacientes_formula"]; idx == nil || len(idx.Columns) != 1 || idx.Columns[0] != "formula" {
		t.Errorf("file FK column must be auto-indexed: %+v", idx)
	}

	// The engine-managed files table itself is never part of the desired model.
	if ds.Tables["files"] != nil {
		t.Error("files table must not be modeled in the desired schema")
	}

	// schemaHasFileFields: the runner's ensure trigger.
	if !schemaHasFileFields(s) {
		t.Error("schemaHasFileFields should be true")
	}
	if schemaHasFileFields(&schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"a": {Fields: map[string]schema.FieldDef{"x": {Type: "uuid"}}},
	}}) {
		t.Error("schemaHasFileFields should be false without file fields")
	}
}
