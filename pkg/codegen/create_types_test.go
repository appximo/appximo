package codegen

import (
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

func typeTestResource() *schema.ResourceSchema {
	return &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"title":  {Type: "string"},
		"amount": {Type: "int64"},
		"ratio":  {Type: "float64"},
		"done":   {Type: "bool"},
		"due":    {Type: "time"},
		"meta":   {Type: "jsonb"},
		"status": {Type: "string", Enum: []string{"open", "done"}},
	}}
}

// The headline regression. Measured on the live engine before the fix:
//
//	POST  {"amount": 1.9} → 201 Created, and the stored value is 1
//	PATCH {"amount": 1.9} → 422 field "amount" must be an integer
//
// The create path had no type check at all, so a value that could be coerced was
// silently coerced and reported as success. That is data corruption, not a bad
// message, and it is the reason this file exists.
func TestValidateCreateTypes_CatchesTheSilentCoercion(t *testing.T) {
	errs := validateCreateTypes(typeTestResource(), map[string]any{"amount": 1.9})
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %+v", len(errs), errs)
	}
	if errs[0].Field != "amount" {
		t.Errorf("error does not name the offending field: %+v", errs[0])
	}
	if errs[0].Rule != "type" {
		t.Errorf("rule = %q, want \"type\"", errs[0].Rule)
	}
}

// Every failing field at once — the S44 contract the rest of create validation
// already honours — and in a STABLE order, since map iteration is random.
func TestValidateCreateTypes_ReportsEveryFieldStably(t *testing.T) {
	body := map[string]any{"amount": "nope", "done": 1.0, "ratio": true}
	first := validateCreateTypes(typeTestResource(), body)
	if len(first) != 3 {
		t.Fatalf("got %d errors, want 3: %+v", len(first), first)
	}
	for i := 0; i < 20; i++ {
		again := validateCreateTypes(typeTestResource(), body)
		for j := range again {
			if again[j].Field != first[j].Field {
				t.Fatalf("field order is not stable across calls: %v vs %v", first, again)
			}
		}
	}
	if first[0].Field != "amount" || first[1].Field != "done" || first[2].Field != "ratio" {
		t.Errorf("errors not sorted by field: %+v", first)
	}
}

// Constraint 1 (ENG-12): a key that is not a declared field must PASS THROUGH, not
// be rejected here. A migration can add a column without rebuilding the router, so
// the database is the source of truth for what exists — the unknown column has to
// reach Postgres and come back as the 422 unknown_field shape. Rejecting it here
// would break writing to a column the tenant genuinely has.
func TestValidateCreateTypes_UnknownFieldPassesThrough(t *testing.T) {
	if errs := validateCreateTypes(typeTestResource(), map[string]any{"column_added_by_a_migration": "x"}); len(errs) != 0 {
		t.Errorf("an undeclared key must pass through to Postgres (ENG-12), got %+v", errs)
	}
}

// Constraint 2: an explicit null is a legitimate write to a nullable column;
// `required` governs whether it is allowed, not the type checker.
func TestValidateCreateTypes_ExplicitNullIsNotATypeError(t *testing.T) {
	if errs := validateCreateTypes(typeTestResource(), map[string]any{"amount": nil, "due": nil}); len(errs) != 0 {
		t.Errorf("explicit null must not be a type error, got %+v", errs)
	}
}

// Valid bodies stay valid — including the types the checker is deliberately
// lenient about (time is validated by Postgres; jsonb takes any JSON value).
func TestValidateCreateTypes_AcceptsValidBodies(t *testing.T) {
	ok := []map[string]any{
		{"title": "t", "amount": 7.0, "ratio": 1.5, "done": true},
		{"due": "2026-08-01T00:00:00Z"},
		{"meta": map[string]any{"a": 1}},
		{"meta": []any{1, 2}},
		{"status": "open"},
		{"amount": 0.0},
		{"amount": -3.0},
	}
	for _, b := range ok {
		if errs := validateCreateTypes(typeTestResource(), b); len(errs) != 0 {
			t.Errorf("valid body %v rejected: %+v", b, errs)
		}
	}
}

func TestValidateCreateTypes_NilSafe(t *testing.T) {
	if errs := validateCreateTypes(nil, map[string]any{"a": 1}); errs != nil {
		t.Errorf("nil resource must be a no-op, got %+v", errs)
	}
	if errs := validateCreateTypes(typeTestResource(), nil); errs != nil {
		t.Errorf("nil body must be a no-op, got %+v", errs)
	}
}
