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
	body := map[string]any{"amount": true, "done": 1.0, "ratio": []any{1}}
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

// The regression an adversarial review caught, and the reason constraint 3
// exists. A JSON STRING must reach Postgres untouched, because Postgres parses
// it and the engine must not be stricter than the layer it protects (ADR-024).
//
// Measured against the pre-fix binary: all three of these returned 201 with the
// value stored CORRECTLY, and the first version of validateCreateTypes turned
// them into 422s — an un-versioned break of the create contract, shipped inside
// the commit whose whole point was to stop silent corruption. Form-encoded
// clients, spreadsheet importers and several HTTP libraries send every scalar
// this way.
func TestValidateCreateTypes_StringEncodedScalarsDeferToPostgres(t *testing.T) {
	for _, body := range []map[string]any{
		{"amount": "7"},       // int64 as a string  → Postgres parses it
		{"ratio": "1.5"},      // float64 as a string
		{"done": "true"},      // bool as a string
		{"done": "yes"},       // Postgres accepts this; strconv.ParseBool does not
		{"amount": "  42  "},  // Postgres tolerates surrounding whitespace
		{"due": "2026-08-01"}, // date-only, one of many spellings Postgres takes
		{"amount": "nope"},    // does NOT parse — Postgres rejects it as a 400, as before
	} {
		if errs := validateCreateTypes(typeTestResource(), body); len(errs) != 0 {
			t.Errorf("body %v was rejected in Go; a string value must defer to Postgres: %+v", body, errs)
		}
	}
}

// …while the corruption case stays closed. These are JSON values Postgres would
// either coerce silently or fail on with a masked 500, and none of them can be
// mistaken for a caller encoding a scalar as text.
func TestValidateCreateTypes_StillCatchesNonStringTypeErrors(t *testing.T) {
	for _, tc := range []struct {
		body  map[string]any
		field string
	}{
		{map[string]any{"amount": 1.9}, "amount"},      // THE corruption: stored as 1
		{map[string]any{"amount": true}, "amount"},     // was a masked 500
		{map[string]any{"done": 1.0}, "done"},          // was a masked 500
		{map[string]any{"ratio": true}, "ratio"},       // bool into a numeric column
		{map[string]any{"title": 12345.0}, "title"},    // number into a text column
		{map[string]any{"amount": []any{1}}, "amount"}, // array into a scalar column
		{map[string]any{"amount": map[string]any{}}, "amount"},
	} {
		errs := validateCreateTypes(typeTestResource(), tc.body)
		if len(errs) == 0 {
			t.Errorf("body %v was accepted; it must be a named 422", tc.body)
			continue
		}
		if errs[0].Field != tc.field {
			t.Errorf("body %v: error names %q, want %q", tc.body, errs[0].Field, tc.field)
		}
	}
}

// Constraint 4, and the most damaging bug this checker ever had.
//
// validateCreateTypes runs AFTER ApplyDefaults. A `{"type":"time","default":
// "now"}` field — documented in AGENTS.md as "the one dynamic default" — is
// filled with a Go time.Time, and validateFieldValue's time case requires a
// string. The first version therefore answered EVERY create that omitted such a
// field with 422 `field "fecha" must be a timestamp string`, naming a field the
// caller never sent: a total outage of POST for any schema using the feature.
// The repo's own working schema.json uses it in two resources.
//
// It passed `make test` because the integration test that covers it is skipped by
// the -short unit lane. The guard is therefore written against the TYPE SYSTEM
// rather than against the one symptom: encoding/json can only produce string,
// float64, bool, map, slice or nil, so anything else was put in the map by the
// engine and is not the caller's to be blamed for.
func TestValidateCreateTypes_EngineInjectedDefaultsAreNotCallerInput(t *testing.T) {
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"fecha":  {Type: "time", Default: "now"},
		"amount": {Type: "int64"},
	}}
	body := map[string]any{}
	schema.CompileRules(res).ApplyDefaults(body)

	if _, ok := body["fecha"]; !ok {
		t.Fatal("ApplyDefaults did not inject the default — the test no longer exercises the bug")
	}
	if _, isString := body["fecha"].(string); isString {
		t.Fatal("ApplyDefaults now injects a string; re-check whether this guard is still needed")
	}
	if errs := validateCreateTypes(res, body); len(errs) != 0 {
		t.Errorf("an engine-injected default was reported as a caller type error: %+v", errs)
	}
	// A caller-supplied value on the same field is still checked.
	if errs := validateCreateTypes(res, map[string]any{"amount": 1.9}); len(errs) != 1 {
		t.Errorf("the guard must not disable checking of real caller input: %+v", errs)
	}
}
