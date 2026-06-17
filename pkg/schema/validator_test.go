package schema_test

import (
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

func TestValidate_ValidLogisticsSchema(t *testing.T) {
	s, err := schema.LoadFromFile("../../testdata/logistics/schema.json")
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	errs := schema.Validate(s)
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d:", len(errs))
		for _, e := range errs {
			t.Errorf("  %s", e.Error())
		}
	}
}

func TestValidate_InvalidFieldType(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"data": {Type: "BYTEA"},
				},
			},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "type") {
		t.Errorf("expected error about unknown field type, got: %v", errs)
	}
}

func TestValidate_RelationToMissingResource(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"orders": {
				Fields: map[string]schema.FieldDef{
					"user_id": {Type: "uuid", Relation: "nonexistent"},
				},
			},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "relation") {
		t.Errorf("expected error about unknown relation, got: %v", errs)
	}
}

func TestValidate_OnDelete(t *testing.T) {
	mk := func(f schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{
				"parents": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
				"kids":    {Fields: map[string]schema.FieldDef{"p": f}},
			},
		}
	}
	// Valid: restrict / cascade / set_null (on a nullable column).
	for _, action := range []string{"restrict", "cascade", "set_null"} {
		if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", OnDelete: action})); hasError(errs, "on_delete") {
			t.Errorf("on_delete %q should be valid, got: %v", action, errs)
		}
	}
	// Invalid value.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", OnDelete: "nuke"})); !hasError(errs, "on_delete") {
		t.Errorf("unknown on_delete must be rejected")
	}
	// on_delete without a relation.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", OnDelete: "cascade"})); !hasError(errs, "on_delete") {
		t.Errorf("on_delete without a relation must be rejected")
	}
	// set_null on a REQUIRED (NOT NULL) column → invalid.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", Required: true, OnDelete: "set_null"})); !hasError(errs, "on_delete") {
		t.Errorf("set_null on a required column must be rejected")
	}
	// cascade on a required column is fine.
	if errs := schema.Validate(mk(schema.FieldDef{Type: "uuid", Relation: "parents", Required: true, OnDelete: "cascade"})); hasError(errs, "on_delete") {
		t.Errorf("cascade on a required column should be valid, got: %v", errs)
	}
}

func TestValidate_RenamedFrom(t *testing.T) {
	// Field-level renamed_from.
	mkField := func(fields map[string]schema.FieldDef) *schema.APISchema {
		return &schema.APISchema{
			Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
			Resources: map[string]schema.ResourceSchema{"r": {Fields: fields}},
		}
	}
	// Valid: rename from an absent name.
	if errs := schema.Validate(mkField(map[string]schema.FieldDef{
		"full_name": {Type: "string", RenamedFrom: "nombre"},
	})); hasError(errs, "renamed_from") {
		t.Errorf("valid field renamed_from rejected: %v", errs)
	}
	// Invalid: old name still a declared field.
	if errs := schema.Validate(mkField(map[string]schema.FieldDef{
		"full_name": {Type: "string", RenamedFrom: "nombre"},
		"nombre":    {Type: "string"},
	})); !hasError(errs, "renamed_from") {
		t.Errorf("renamed_from of a still-present field must be rejected")
	}
	// Invalid: rename from itself.
	if errs := schema.Validate(mkField(map[string]schema.FieldDef{
		"full_name": {Type: "string", RenamedFrom: "full_name"},
	})); !hasError(errs, "renamed_from") {
		t.Errorf("renamed_from equal to the field name must be rejected")
	}
	// Invalid: bad identifier.
	if errs := schema.Validate(mkField(map[string]schema.FieldDef{
		"full_name": {Type: "string", RenamedFrom: "Bad-Name"},
	})); !hasError(errs, "renamed_from") {
		t.Errorf("renamed_from with an invalid identifier must be rejected")
	}

	// Resource-level renamed_from.
	mkRes := func(resources map[string]schema.ResourceSchema) *schema.APISchema {
		return &schema.APISchema{Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test", Resources: resources}
	}
	// Valid: table rename from an absent name.
	if errs := schema.Validate(mkRes(map[string]schema.ResourceSchema{
		"clients": {RenamedFrom: "customers", Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
	})); hasError(errs, "renamed_from") {
		t.Errorf("valid resource renamed_from rejected: %v", errs)
	}
	// Invalid: old resource still declared.
	if errs := schema.Validate(mkRes(map[string]schema.ResourceSchema{
		"clients":   {RenamedFrom: "customers", Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
	})); !hasError(errs, "renamed_from") {
		t.Errorf("renamed_from of a still-present resource must be rejected")
	}
}

func TestValidate_WebhookWithoutURL(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"orders": {
				Fields: map[string]schema.FieldDef{
					"name": {Type: "string"},
				},
				Hooks: map[string]schema.HookConfig{
					"after_create": {Type: "webhook"},
				},
			},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "url") {
		t.Errorf("expected error about missing webhook url, got: %v", errs)
	}
}

func TestValidate_ResourceNameWithUppercase(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"Guides": {
				Fields: map[string]schema.FieldDef{
					"code": {Type: "string"},
				},
			},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "resources.Guides") {
		t.Errorf("expected error about invalid resource name, got: %v", errs)
	}
}

// G1 (FIX-G1-G6): a hyphenated resource name passed `validate` but PANICKED the
// engine at boot building the GraphQL schema ('-' is not a valid GraphQL
// identifier char). It must now be REJECTED at validation, so validate↔boot agree.
func TestValidate_HyphenatedResourceNameRejected(t *testing.T) {
	s := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
		Resources: map[string]schema.ResourceSchema{
			"order-items": {Fields: map[string]schema.FieldDef{"sku": {Type: "string"}}},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "resources.order-items") {
		t.Errorf("expected hyphenated resource name to be rejected, got: %v", errs)
	}
	if !hasError(errs, "order_items") {
		t.Errorf("expected the error to suggest the underscore form, got: %v", errs)
	}
}

// The supported multi-word form (underscore) must VALIDATE — it is a valid
// GraphQL identifier and boots end-to-end.
func TestValidate_UnderscoreResourceNameAccepted(t *testing.T) {
	s := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
		Resources: map[string]schema.ResourceSchema{
			"order_items": {Fields: map[string]schema.FieldDef{"sku": {Type: "string"}}},
		},
	}
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Errorf("expected underscore resource name to validate, got: %v", errs)
	}
}

// Allowing '_' means a resource could otherwise be named "auth_users" and collide
// with the per-tenant authentication tables; the "auth_" prefix is reserved.
func TestValidate_ReservedAuthPrefixRejected(t *testing.T) {
	s := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
		Resources: map[string]schema.ResourceSchema{
			"auth_users": {Fields: map[string]schema.FieldDef{"email": {Type: "string"}}},
		},
	}
	if errs := schema.Validate(s); !hasError(errs, "reserved") {
		t.Errorf("expected auth_ prefix to be rejected as reserved, got: %v", errs)
	}
	// A name that merely starts with "auth" (no underscore) is fine.
	ok := &schema.APISchema{
		Schema: "https://appitools.dev/schema/v1", Version: "1", Name: "test",
		Resources: map[string]schema.ResourceSchema{
			"authors": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		},
	}
	if errs := schema.Validate(ok); len(errs) != 0 {
		t.Errorf("expected 'authors' to validate, got: %v", errs)
	}
}

func TestValidate_EmptyEnum(t *testing.T) {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"status": {Type: "string", Enum: []string{}},
				},
			},
		},
	}
	errs := schema.Validate(s)
	if !hasError(errs, "enum") {
		t.Errorf("expected error about empty enum, got: %v", errs)
	}
}

func hasError(errs []schema.ValidationError, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Field, substr) || strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}
