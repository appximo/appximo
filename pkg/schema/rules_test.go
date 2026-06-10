package schema

import (
	"strings"
	"testing"
)

func iptr(i int) *int         { return &i }
func fptr(f float64) *float64 { return &f }

// rulesResource builds a resource exercising every declarative rule kind.
func rulesResource() *ResourceSchema {
	return &ResourceSchema{
		Fields: map[string]FieldDef{
			"email":  {Type: "string", Required: true, Format: "email", MaxLength: iptr(254)},
			"status": {Type: "string", Enum: []string{"pending", "active", "done"}},
			"amount": {Type: "float64", Min: fptr(0), Max: fptr(999999999)},
			"nit":    {Type: "string", Pattern: "^[0-9]{9,10}$"},
			"code":   {Type: "string", MinLength: iptr(3), MaxLength: iptr(20)},
			"ref":    {Type: "string", Format: "uuid"},
			"site":   {Type: "string", Format: "url"},
			"day":    {Type: "string", Format: "date"},
			"id_at":  {Type: "time", Auto: true, Required: true}, // auto ⇒ never required at write
		},
	}
}

func findRule(t *testing.T, errs []FieldRuleError, field, rule string) *FieldRuleError {
	t.Helper()
	for i := range errs {
		if errs[i].Field == field && errs[i].Rule == rule {
			return &errs[i]
		}
	}
	return nil
}

func TestValidateWrite_RequiredCreateAndPut(t *testing.T) {
	rv := CompileRules(rulesResource())

	// Absent required → required error (create / PUT).
	errs := rv.ValidateWrite(map[string]any{"status": "active"}, true)
	if findRule(t, errs, "email", "required") == nil {
		t.Fatalf("missing required email not reported: %v", errs)
	}
	// Present but null required → required error.
	errs = rv.ValidateWrite(map[string]any{"email": nil}, true)
	if findRule(t, errs, "email", "required") == nil {
		t.Fatalf("null required email not reported: %v", errs)
	}
	// Auto fields are never required at write time.
	if findRule(t, errs, "id_at", "required") != nil {
		t.Fatalf("auto field must not be required: %v", errs)
	}
	// Empty string is PRESENT (passes required) but fails the format rule —
	// the "empty vs absent" distinction.
	errs = rv.ValidateWrite(map[string]any{"email": ""}, true)
	if findRule(t, errs, "email", "required") != nil {
		t.Fatalf("empty string must not count as missing: %v", errs)
	}
	if findRule(t, errs, "email", "format") == nil {
		t.Fatalf("empty string must fail email format: %v", errs)
	}
}

func TestValidateWrite_PatchSkipsRequired(t *testing.T) {
	rv := CompileRules(rulesResource())
	// PATCH (requireAll=false): missing required is fine; present fields validate.
	errs := rv.ValidateWrite(map[string]any{"status": "nope"}, false)
	if findRule(t, errs, "email", "required") != nil {
		t.Fatalf("PATCH must not require absent fields: %v", errs)
	}
	if findRule(t, errs, "status", "enum") == nil {
		t.Fatalf("PATCH must validate present fields: %v", errs)
	}
}

func TestValidateWrite_MinMaxNumbers(t *testing.T) {
	rv := CompileRules(rulesResource())

	// 0 with min:0 is valid — zero is NOT "missing".
	if errs := rv.ValidateWrite(map[string]any{"email": "a@b.co", "amount": float64(0)}, true); len(errs) != 0 {
		t.Fatalf("amount=0 with min=0 must pass: %v", errs)
	}
	// Null on a non-required field skips the rules entirely.
	if errs := rv.ValidateWrite(map[string]any{"email": "a@b.co", "amount": nil}, true); len(errs) != 0 {
		t.Fatalf("amount=null must skip min/max: %v", errs)
	}
	if errs := rv.ValidateWrite(map[string]any{"email": "a@b.co", "amount": -0.01}, true); findRule(t, errs, "amount", "min") == nil {
		t.Fatalf("negative amount must fail min: %v", errs)
	}
	if errs := rv.ValidateWrite(map[string]any{"email": "a@b.co", "amount": 1e12}, true); findRule(t, errs, "amount", "max") == nil {
		t.Fatalf("1e12 must fail max: %v", errs)
	}
	// Non-number JSON value on a numeric rule field.
	if errs := rv.ValidateWrite(map[string]any{"email": "a@b.co", "amount": "12"}, true); findRule(t, errs, "amount", "min") == nil {
		t.Fatalf("string amount must fail with type message: %v", errs)
	}
}

func TestValidateWrite_LengthsUnicode(t *testing.T) {
	rv := CompileRules(rulesResource())
	// "ñandú" is 5 runes / 7 bytes: must count RUNES (min 3, max 20 → valid).
	if errs := rv.ValidateWrite(map[string]any{"code": "ñandú"}, false); len(errs) != 0 {
		t.Fatalf("rune-count maxLength broken: %v", errs)
	}
	if errs := rv.ValidateWrite(map[string]any{"code": "ño"}, false); findRule(t, errs, "code", "minLength") == nil {
		t.Fatalf("2-rune string must fail minLength 3: %v", errs)
	}
	long := strings.Repeat("ü", 21)
	if errs := rv.ValidateWrite(map[string]any{"code": long}, false); findRule(t, errs, "code", "maxLength") == nil {
		t.Fatalf("21-rune string must fail maxLength 20: %v", errs)
	}
}

func TestValidateWrite_PatternEnumFormats(t *testing.T) {
	rv := CompileRules(rulesResource())

	cases := []struct {
		field string
		ok    []any
		bad   []any
		rule  string
	}{
		{"nit", []any{"123456789", "1234567890"}, []any{"12345678", "12345678901", "ABC123456"}, "pattern"},
		{"status", []any{"pending", "done"}, []any{"cancelled", 42}, "enum"},
		{"email", []any{"a@b.co", "x.y+z@sub.domain.dev"}, []any{"no-at", "a@b", "a @b.co"}, "format"},
		{"ref", []any{"d2719c2e-4dd9-4f3a-9d6c-3a2b1c0d9e8f"}, []any{"not-a-uuid"}, "format"},
		{"site", []any{"https://x.dev/p", "http://a.co"}, []any{"ftp://x.dev", "x.dev", "https://"}, "format"},
		{"day", []any{"2026-06-10", "2026-06-10T12:00:00Z"}, []any{"10/06/2026", "2026-13-40"}, "format"},
	}
	for _, c := range cases {
		for _, v := range c.ok {
			if errs := rv.ValidateWrite(map[string]any{c.field: v}, false); len(errs) != 0 {
				t.Errorf("%s=%v should pass, got %v", c.field, v, errs)
			}
		}
		for _, v := range c.bad {
			errs := rv.ValidateWrite(map[string]any{c.field: v}, false)
			if findRule(t, errs, c.field, c.rule) == nil {
				t.Errorf("%s=%v should fail rule %s, got %v", c.field, v, c.rule, errs)
			}
		}
	}
}

func TestValidateWrite_ReportsAllFieldsAtOnce(t *testing.T) {
	rv := CompileRules(rulesResource())
	errs := rv.ValidateWrite(map[string]any{
		"status": "bogus",
		"nit":    "XX",
		"amount": -5.0,
	}, true) // email also missing → required
	for _, want := range [][2]string{{"email", "required"}, {"status", "enum"}, {"nit", "pattern"}, {"amount", "min"}} {
		if findRule(t, errs, want[0], want[1]) == nil {
			t.Errorf("expected %s/%s among errors: %v", want[0], want[1], errs)
		}
	}
	if len(errs) < 4 {
		t.Fatalf("all violations must be reported in one response, got %d: %v", len(errs), errs)
	}
}

func TestValidate_RejectsBadRuleDefinitions(t *testing.T) {
	mk := func(f FieldDef) *APISchema {
		return &APISchema{
			Schema: "x", Version: "1",
			Resources: map[string]ResourceSchema{"items": {Fields: map[string]FieldDef{"f": f}}},
		}
	}
	cases := []struct {
		name string
		f    FieldDef
		frag string
	}{
		{"invalid regex", FieldDef{Type: "string", Pattern: "([0-9]"}, "invalid pattern"},
		{"pattern too long", FieldDef{Type: "string", Pattern: "^" + strings.Repeat("a", MaxPatternLength+1) + "$"}, "max is"},
		{"pattern on number", FieldDef{Type: "int", Pattern: "^[0-9]+$"}, "only applies to string"},
		{"min on string", FieldDef{Type: "string", Min: fptr(1)}, "only apply to numeric"},
		{"min > max", FieldDef{Type: "float64", Min: fptr(10), Max: fptr(1)}, "min must be <= max"},
		{"minLength on bool", FieldDef{Type: "bool", MinLength: iptr(1)}, "only apply to string"},
		{"negative minLength", FieldDef{Type: "string", MinLength: iptr(-1)}, "must be >= 0"},
		{"minLength > maxLength", FieldDef{Type: "string", MinLength: iptr(5), MaxLength: iptr(2)}, "minLength must be <= maxLength"},
		{"unknown format", FieldDef{Type: "string", Format: "phone"}, "unknown format"},
		{"format on number", FieldDef{Type: "float64", Format: "email"}, "only applies to string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := Validate(mk(c.f))
			if len(errs) == 0 {
				t.Fatalf("schema with %s must be rejected at load", c.name)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Message, c.frag) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected message containing %q, got %v", c.frag, errs)
			}
		})
	}

	// A rule-free schema stays valid (zero breaking changes).
	if errs := Validate(mk(FieldDef{Type: "string"})); len(errs) != 0 {
		t.Fatalf("plain field must stay valid: %v", errs)
	}
}

// TestCompileRules_FailClosedOnBadPattern: if a caller skips Validate and
// compiles a broken pattern anyway, the field must reject every value (fail
// closed) — and must never panic.
func TestCompileRules_FailClosedOnBadPattern(t *testing.T) {
	rv := CompileRules(&ResourceSchema{Fields: map[string]FieldDef{
		"f": {Type: "string", Pattern: "([0-9]"},
	}})
	errs := rv.ValidateWrite(map[string]any{"f": "anything"}, false)
	if findRule(t, errs, "f", "pattern") == nil {
		t.Fatalf("bad pattern must fail closed, got %v", errs)
	}
}
