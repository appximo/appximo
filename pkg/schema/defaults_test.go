package schema

import (
	"testing"
	"time"
)

// SCHEMA-CLOSE-V1: defaults are filled on create for omitted fields, type-checked
// at load, and never override a value the caller provided.
func TestApplyDefaults(t *testing.T) {
	res := &ResourceSchema{
		Fields: map[string]FieldDef{
			"title":  {Type: "string", Required: true, Default: "untitled"},
			"qty":    {Type: "int", Default: float64(7)},
			"active": {Type: "bool", Default: true},
			"due":    {Type: "time", Default: "now"},
			"note":   {Type: "string"}, // no default
		},
	}
	rv := CompileRules(res)

	t.Run("fills omitted fields", func(t *testing.T) {
		body := map[string]any{}
		rv.ApplyDefaults(body)
		if body["title"] != "untitled" {
			t.Errorf("title default not applied: %v", body["title"])
		}
		if body["qty"] != float64(7) {
			t.Errorf("qty default not applied: %v", body["qty"])
		}
		if body["active"] != true {
			t.Errorf("active default not applied: %v", body["active"])
		}
		if _, ok := body["due"].(time.Time); !ok {
			t.Errorf("time \"now\" default should resolve to time.Time, got %T", body["due"])
		}
		if _, ok := body["note"]; ok {
			t.Errorf("field without a default must not be filled")
		}
	})

	t.Run("does not override a provided value", func(t *testing.T) {
		body := map[string]any{"title": "given", "qty": float64(99)}
		rv.ApplyDefaults(body)
		if body["title"] != "given" || body["qty"] != float64(99) {
			t.Errorf("defaults overrode provided values: %v", body)
		}
	})

	t.Run("present null is left as-is (not defaulted)", func(t *testing.T) {
		body := map[string]any{"title": nil}
		rv.ApplyDefaults(body)
		if body["title"] != nil {
			t.Errorf("explicit null must not be replaced by the default, got %v", body["title"])
		}
	})

	t.Run("required+default satisfies the required check", func(t *testing.T) {
		body := map[string]any{}
		rv.ApplyDefaults(body)
		if verrs := rv.ValidateWrite(body, true); len(verrs) > 0 {
			t.Errorf("required field with a default should pass after ApplyDefaults, got %v", verrs)
		}
	})

	t.Run("no defaults → ApplyDefaults is a no-op", func(t *testing.T) {
		plain := CompileRules(&ResourceSchema{Fields: map[string]FieldDef{"x": {Type: "string"}}})
		body := map[string]any{}
		plain.ApplyDefaults(body)
		if len(body) != 0 {
			t.Errorf("expected no-op, got %v", body)
		}
	})
}

func TestValidateDefault_LoadErrors(t *testing.T) {
	mk := func(fd FieldDef) *APISchema {
		return &APISchema{
			Schema:    "https://appitools.dev/schema/v1",
			Version:   "1",
			Name:      "t",
			Resources: map[string]ResourceSchema{"items": {Fields: map[string]FieldDef{"f": fd}}},
		}
	}
	cases := []struct {
		name string
		fd   FieldDef
		ok   bool
	}{
		{"string ok", FieldDef{Type: "string", Default: "x"}, true},
		{"string wrong type", FieldDef{Type: "string", Default: float64(1)}, false},
		{"int ok", FieldDef{Type: "int", Default: float64(3)}, true},
		{"int non-integral", FieldDef{Type: "int", Default: float64(3.5)}, false},
		{"int wrong type", FieldDef{Type: "int", Default: "3"}, false},
		{"bool ok", FieldDef{Type: "bool", Default: true}, true},
		{"bool wrong type", FieldDef{Type: "bool", Default: "true"}, false},
		{"time now ok", FieldDef{Type: "time", Default: "now"}, true},
		{"time literal ok", FieldDef{Type: "time", Default: "2020-01-01T00:00:00Z"}, true},
		{"time wrong type", FieldDef{Type: "time", Default: float64(1)}, false},
		{"uuid ok", FieldDef{Type: "uuid", Default: "11111111-1111-1111-1111-111111111111"}, true},
		{"uuid bad", FieldDef{Type: "uuid", Default: "not-a-uuid"}, false},
		{"enum member ok", FieldDef{Type: "string", Enum: []string{"a", "b"}, Default: "a"}, true},
		{"enum non-member", FieldDef{Type: "string", Enum: []string{"a", "b"}, Default: "c"}, false},
		{"auto+default rejected", FieldDef{Type: "time", Auto: true, Default: "now"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := Validate(mk(c.fd))
			if c.ok && len(errs) != 0 {
				t.Errorf("expected valid, got %v", errs)
			}
			if !c.ok && len(errs) == 0 {
				t.Errorf("expected a load error, got none")
			}
		})
	}
}
