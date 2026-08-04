package schema_test

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// TestValidate_OnUpdate mirrors the on_delete checks for the MIG-F1-S5 on_update
// action: valid values only on a relation, set_null only over a nullable column.
func TestValidate_OnUpdate(t *testing.T) {
	mk := func(f schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appximo.com/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{
				"parents": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
				"kids":    {Fields: map[string]schema.FieldDef{"p": f}},
			},
		}
	}
	for _, action := range []string{"restrict", "cascade", "set_null"} {
		if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", OnUpdate: action})); hasError(errs, "on_update") {
			t.Errorf("on_update %q should be valid, got: %v", action, errs)
		}
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", OnUpdate: "nuke"})); !hasError(errs, "on_update") {
		t.Errorf("unknown on_update must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", OnUpdate: "cascade"})); !hasError(errs, "on_update") {
		t.Errorf("on_update without a relation must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", Required: true, OnUpdate: "set_null"})); !hasError(errs, "on_update") {
		t.Errorf("on_update set_null on a required column must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", Required: true, OnUpdate: "cascade"})); hasError(errs, "on_update") {
		t.Errorf("on_update cascade on a required column should be valid, got: %v", errs)
	}
}

// TestValidate_References checks the field-level FK target column (MIG-F1-S5): it
// must be "id" or a UNIQUE column of the target, type-compatible, and only valid on
// a relation field.
func TestValidate_References(t *testing.T) {
	mk := func(postField schema.FieldDef, userFields map[string]schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appximo.com/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{
				"users": {Fields: userFields},
				"posts": {Fields: map[string]schema.FieldDef{"author": postField}},
			},
		}
	}
	uniqueEmail := map[string]schema.FieldDef{"email": {Type: "string", Unique: true}}

	// Valid: references a unique column, type-compatible.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "string", Relation: "users", References: "email"}, uniqueEmail)); hasError(errs, "references") {
		t.Errorf("references to a unique column should be valid, got: %v", errs)
	}
	// Valid: references "id" explicitly (same as default).
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "users", References: "id"}, uniqueEmail)); hasError(errs, "references") {
		t.Errorf("references id should be valid, got: %v", errs)
	}
	// Invalid: references a NON-unique column.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "string", Relation: "users", References: "email"}, map[string]schema.FieldDef{"email": {Type: "string"}})); !hasError(errs, "references") {
		t.Errorf("references to a non-unique column must be rejected")
	}
	// Invalid: references a column that does not exist.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "string", Relation: "users", References: "ghost"}, uniqueEmail)); !hasError(errs, "references") {
		t.Errorf("references to a missing column must be rejected")
	}
	// Invalid: type mismatch (uuid field references a string unique column).
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "users", References: "email"}, uniqueEmail)); !hasError(errs, "references") {
		t.Errorf("references with incompatible type must be rejected")
	}
	// Invalid: references without a relation.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "string", References: "email"}, uniqueEmail)); !hasError(errs, "references") {
		t.Errorf("references without a relation must be rejected")
	}
}

// TestValidate_CompositeForeignKeys checks the resource-level foreign_keys block
// (MIG-F1-S5): valid composite FK accepted; mismatched counts, non-unique target,
// missing columns, type mismatch and set_null-over-required all rejected.
func TestValidate_CompositeForeignKeys(t *testing.T) {
	mk := func(fks []schema.ForeignKeyDef, orderFields map[string]schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appximo.com/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{
				"branches": {
					Fields: map[string]schema.FieldDef{
						"region_code": {Type: "string", Required: true},
						"branch_code": {Type: "string", Required: true},
					},
					Indexes: []schema.IndexDef{{Fields: []string{"region_code", "branch_code"}, Unique: true}},
				},
				"orders": {Fields: orderFields, ForeignKeys: fks},
			},
		}
	}
	orderCols := map[string]schema.FieldDef{
		"region_code": {Type: "string"},
		"branch_code": {Type: "string"},
	}
	good := schema.ForeignKeyDef{
		Columns: []string{"region_code", "branch_code"}, Target: "branches",
		RefColumns: []string{"region_code", "branch_code"}, OnDelete: "cascade", OnUpdate: "restrict",
	}

	// Valid composite FK against a composite unique index.
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{good}, orderCols)); hasError(errs, "foreign_keys") {
		t.Fatalf("valid composite FK rejected: %v", errs)
	}
	// Mismatched column counts.
	bad := good
	bad.RefColumns = []string{"region_code"}
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{bad}, orderCols)); !hasError(errs, "foreign_keys") {
		t.Errorf("mismatched column/ref_column counts must be rejected")
	}
	// Ref columns not a unique key on the target (drop the unique index).
	noUnique := &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "test",
		Resources: map[string]schema.ResourceSchema{
			"branches": {Fields: map[string]schema.FieldDef{
				"region_code": {Type: "string"}, "branch_code": {Type: "string"},
			}},
			"orders": {Fields: orderCols, ForeignKeys: []schema.ForeignKeyDef{good}},
		},
	}
	if errs := schema.Validate(noUnique); !hasError(errs, "ref_columns") {
		t.Errorf("composite FK to non-unique target columns must be rejected")
	}
	// Source column does not exist.
	badSrc := good
	badSrc.Columns = []string{"region_code", "ghost"}
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{badSrc}, orderCols)); !hasError(errs, "columns") {
		t.Errorf("composite FK with a missing source column must be rejected")
	}
	// Type mismatch: order's region_code is int, branch's is string.
	mismatchCols := map[string]schema.FieldDef{
		"region_code": {Type: "int"},
		"branch_code": {Type: "string"},
	}
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{good}, mismatchCols)); !hasError(errs, "foreign_keys") {
		t.Errorf("composite FK with a type mismatch must be rejected")
	}
	// set_null over a required source column.
	requiredCols := map[string]schema.FieldDef{
		"region_code": {Type: "string", Required: true},
		"branch_code": {Type: "string"},
	}
	setNull := good
	setNull.OnDelete = "set_null"
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{setNull}, requiredCols)); !hasError(errs, "foreign_keys") {
		t.Errorf("set_null over a required source column must be rejected")
	}
	// Unknown target resource.
	badTarget := good
	badTarget.Target = "ghosts"
	if errs := schema.Validate(mk([]schema.ForeignKeyDef{badTarget}, orderCols)); !hasError(errs, "target") {
		t.Errorf("composite FK to an unknown target must be rejected")
	}
}

// TestValidate_ForeignKeysStrictKeys verifies an unknown key inside a foreign_keys
// entry is rejected (no silent dead config), via the raw strict-key checker.
func TestValidate_ForeignKeysStrictKeys(t *testing.T) {
	raw := []byte(`{
      "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "t",
      "resources": {
        "branches": { "fields": { "code": { "type": "string", "unique": true } } },
        "orders": {
          "fields": { "code": { "type": "string" } },
          "foreign_keys": [ { "columns": ["code"], "target": "branches", "refcolumns": ["code"] } ]
        }
      }
    }`)
	errs := schema.CheckUnknownKeys(raw)
	if !hasError(errs, "refcolumns") {
		t.Errorf("unknown key 'refcolumns' inside a foreign_keys entry must be rejected, got: %v", errs)
	}
}
