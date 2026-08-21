package codegen

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func updRes() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"title":      {Type: "string", Required: true},
			"amount":     {Type: "int64"},
			"attachment": {Type: "file"},
			"created_at": {Type: "time", Auto: schema.AutoLegacy},
		},
	}
}

func allWritable(string) bool { return true }

// TestCollectUpdate_S44Shape pins ENG-29: an update violation is reported in
// the SAME fields[] shape create uses — every offending field at once, each
// with a rule — instead of the old single flat string. A client parses both
// verbs with one code path, and the documented ValidationErrorResponse is true
// for PATCH/PUT too.
func TestCollectUpdate_S44Shape(t *testing.T) {
	body := map[string]any{
		"id":         "x",          // read_only
		"ghost":      "y",          // unknown_field
		"created_at": "2026-01-01", // read_only (auto)
		"title":      nil,          // required (explicit null)
		"amount":     1.9,          // type (fractional on int64)
		"attachment": float64(7),   // type (ENG-31: file is a uuid column)
	}
	sets, errs := CollectUpdate(updRes(), body, false, allWritable)
	if sets != nil {
		t.Fatalf("sets must be nil on violation, got %v", sets)
	}
	if len(errs) != 6 {
		t.Fatalf("want ALL 6 violations reported at once, got %d: %v", len(errs), errs)
	}
	wantRules := map[string]string{
		"id": "read_only", "ghost": "unknown_field", "created_at": "read_only",
		"title": "required", "amount": "type", "attachment": "type",
	}
	for _, e := range errs {
		if want, ok := wantRules[e.Field]; !ok || e.Rule != want {
			t.Errorf("field %q: rule %q (want %q); msg %q", e.Field, e.Rule, want, e.Message)
		}
		if e.Message == "" {
			t.Errorf("field %q: empty message", e.Field)
		}
	}
	// Deterministic order (sorted by field) — a reshuffling response is
	// untestable and unreadable in a log.
	for i := 1; i < len(errs); i++ {
		if errs[i-1].Field > errs[i].Field {
			t.Fatalf("errors not sorted: %v", errs)
		}
	}

	// A valid body still collects its sets with no errors.
	sets, errs = CollectUpdate(updRes(), map[string]any{"amount": float64(3)}, false, allWritable)
	if len(errs) != 0 || sets["amount"] != float64(3) {
		t.Fatalf("valid body: errs=%v sets=%v", errs, sets)
	}

	// PUT: a missing required field is rule "required" in the same shape.
	_, errs = CollectUpdate(updRes(), map[string]any{"amount": float64(3)}, true, allWritable)
	if len(errs) != 1 || errs[0].Field != "title" || errs[0].Rule != "required" {
		t.Fatalf("PUT missing required: %v", errs)
	}
}

// TestValidateFieldValue_File pins ENG-31: `file` validates like the uuid it
// is, naming the field, instead of falling through the switch as "valid" and
// surfacing later as an unnamed FK/cast error.
func TestValidateFieldValue_File(t *testing.T) {
	fd := schema.FieldDef{Type: "file"}
	if msg, ok := validateFieldValue("attachment", fd, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"); !ok {
		t.Fatalf("valid file id rejected: %s", msg)
	}
	for _, bad := range []any{float64(7), true, "not-a-uuid", map[string]any{}} {
		if _, ok := validateFieldValue("attachment", fd, bad); ok {
			t.Errorf("file value %v must be rejected", bad)
		}
	}
}
