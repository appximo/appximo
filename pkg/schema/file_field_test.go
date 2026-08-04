package schema_test

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// TestValidate_FileField covers the `file` field type (FILES-LINK-S1): a
// first-class reference to the engine file store. Valid shapes are accepted;
// every key that cannot apply to a file reference (relation, references,
// on_update, auto, enum, default, cascade) is rejected at load.
func TestValidate_FileField(t *testing.T) {
	mk := func(f schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appximo.com/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{
				"pacientes": {Fields: map[string]schema.FieldDef{
					"nombre":  {Type: "string", Required: true},
					"formula": f,
				}},
			},
		}
	}

	// Valid: plain, required, unique, restrict (explicit or default), set_null.
	for name, f := range map[string]schema.FieldDef{
		"plain":             {Type: "file"},
		"required":          {Type: "file", Required: true},
		"unique":            {Type: "file", Unique: true},
		"restrict explicit": {Type: "file", OnDelete: "restrict"},
		"set_null":          {Type: "file", OnDelete: "set_null"},
	} {
		if errs := schema.Validate(mk(f)); len(errs) > 0 {
			t.Errorf("%s file field should be valid, got: %v", name, errs)
		}
	}

	// required + set_null is contradictory (nullable column needed) — the shared
	// on_delete check must fire for file fields too.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", Required: true, OnDelete: "set_null"})); !hasError(errs, "on_delete") {
		t.Errorf("set_null on a required file field must be rejected")
	}
	// cascade would delete the RECORD when its file is deleted — rejected.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", OnDelete: "cascade"})); !hasError(errs, "on_delete") {
		t.Errorf("on_delete cascade on a file field must be rejected")
	}
	// The target is fixed (the file store): relation/references cannot apply.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", Relation: "pacientes"})); !hasError(errs, "relation") {
		t.Errorf("relation on a file field must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", References: "sha256"})); !hasError(errs, "references") {
		t.Errorf("references on a file field must be rejected")
	}
	// A file id never changes; on_update is dead config.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", OnUpdate: "cascade"})); !hasError(errs, "on_update") {
		t.Errorf("on_update on a file field must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", Auto: true})); !hasError(errs, "auto") {
		t.Errorf("auto on a file field must be rejected")
	}
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", Enum: []string{"a"}})); !hasError(errs, "enum") {
		t.Errorf("enum on a file field must be rejected")
	}
	// A hardcoded file id would dangle in every tenant.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", Default: "3b241101-e2bb-4255-8caf-4136c566a962"})); !hasError(errs, "default") {
		t.Errorf("default on a file field must be rejected")
	}
	// String rules don't apply to a file reference (shared type gating).
	two := 2
	if errs := schema.Validate(mk(schema.FieldDef{Type: "file", MinLength: &two})); len(errs) == 0 {
		t.Errorf("minLength on a file field must be rejected")
	}
}
