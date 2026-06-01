package codegen

import (
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

func guidesOnlySchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":        {Type: "string", Required: true},
					"status":      {Type: "string"},
					"origin":      {Type: "string", Required: true},
					"destination": {Type: "string", Required: true},
					"weight_kg":   {Type: "float64"},
					"client_id":   {Type: "uuid", Relation: "clients"},
					"created_at":  {Type: "time", Auto: true},
				},
			},
		},
	}
}

func TestGenerateStructs_ContainsExpectedStruct(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	out := string(src)

	if !strings.Contains(out, "type GuidesRow struct") {
		t.Errorf("expected 'type GuidesRow struct', got:\n%s", out)
	}
}

func TestGenerateStructs_PackageDeclaration(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "mypkg")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	if !strings.HasPrefix(string(src), "package mypkg") {
		t.Errorf("expected 'package mypkg' prefix, got: %s", string(src)[:30])
	}
}

func TestGenerateStructs_IDFieldAlwaysPresent(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	if !strings.Contains(normalizeWS(string(src)), `ID string`) {
		t.Errorf("expected 'ID string' field, got:\n%s", string(src))
	}
}

// normalizeWS collapses all runs of whitespace to a single space, making it
// easy to check struct field declarations regardless of gofmt alignment tabs.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func TestGenerateStructs_RequiredFieldIsNonPointer(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	norm := normalizeWS(string(src))
	// "code" is required:true → should be "Code string", not "*string"
	if strings.Contains(norm, "Code *string") {
		t.Errorf("required field 'code' must not be a pointer, got:\n%s", string(src))
	}
	if !strings.Contains(norm, "Code string") {
		t.Errorf("required field 'code' must be 'Code string', got:\n%s", string(src))
	}
}

func TestGenerateStructs_OptionalFieldIsPointer(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	norm := normalizeWS(string(src))
	// "weight_kg" is not required → *float64
	if !strings.Contains(norm, "WeightKg *float64") {
		t.Errorf("optional float field must be '*float64', got:\n%s", string(src))
	}
}

func TestGenerateStructs_AutoFieldIsPointer(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	norm := normalizeWS(string(src))
	// "created_at" is auto:true → *time.Time
	if !strings.Contains(norm, "CreatedAt *time.Time") {
		t.Errorf("auto field must be '*time.Time', got:\n%s", string(src))
	}
}

func TestGenerateStructs_ImportsTime(t *testing.T) {
	src, err := GenerateStructs(guidesOnlySchema(), "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	if !strings.Contains(string(src), `"time"`) {
		t.Errorf("expected time import when schema has time fields, got:\n%s", string(src))
	}
}

func TestGenerateStructs_NoTimeImportWhenNotNeeded(t *testing.T) {
	s := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"name": {Type: "string", Required: true},
				},
			},
		},
	}
	src, err := GenerateStructs(s, "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	if strings.Contains(string(src), `"time"`) {
		t.Errorf("unexpected time import when no time fields present")
	}
}

func TestGenerateStructs_MultipleResources(t *testing.T) {
	s := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"orders":   {Fields: map[string]schema.FieldDef{"total": {Type: "float64", Required: true}}},
			"products": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
		},
	}
	src, err := GenerateStructs(s, "handlers")
	if err != nil {
		t.Fatalf("GenerateStructs: %v", err)
	}
	out := string(src)
	if !strings.Contains(out, "type OrdersRow struct") {
		t.Errorf("expected OrdersRow, got:\n%s", out)
	}
	if !strings.Contains(out, "type ProductsRow struct") {
		t.Errorf("expected ProductsRow, got:\n%s", out)
	}
}
