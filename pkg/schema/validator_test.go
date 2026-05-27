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
