package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// WRITE-ASYMMETRY-S1 — the single source for the governed-field write rule.

func governedRes(imp *ImportConfig) *ResourceSchema {
	return &ResourceSchema{
		Fields: map[string]FieldDef{
			"title":         {Type: "string", Required: true},
			"created_at":    {Type: "time", Auto: AutoCreate},
			"modificado_en": {Type: "time", Auto: AutoUpdate},
			"updated_at":    {Type: "time", Auto: AutoLegacy},
			"owner_id":      {Type: "uuid"},
		},
		Import: imp,
	}
}

func TestGovernedWriteFields(t *testing.T) {
	got := governedRes(nil).GovernedWriteFields()
	want := []string{"created_at", "id", "modificado_en", "updated_at"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GovernedWriteFields = %v, want %v", got, want)
	}
}

func TestIsGovernedWriteField(t *testing.T) {
	r := governedRes(nil)
	for _, f := range []string{"id", "created_at", "modificado_en", "updated_at"} {
		if !r.IsGovernedWriteField(f) {
			t.Errorf("%q must be governed", f)
		}
	}
	for _, f := range []string{"title", "owner_id", "ghost"} {
		if r.IsGovernedWriteField(f) {
			t.Errorf("%q must NOT be governed", f)
		}
	}
}

// TestGovernedViolations_CreateRejectsByDefault pins the family fix: with no
// import declaration, a create body carrying governed fields is rejected with
// rule read_only for EVERY one of them, sorted, each message naming the field
// and the way out (ADR-024).
func TestGovernedViolations_CreateRejectsByDefault(t *testing.T) {
	r := governedRes(nil)
	body := map[string]any{
		"title":      "x",
		"id":         "99999999-9999-4999-8999-999999999999",
		"created_at": "1999-01-01T00:00:00Z",
		"updated_at": "1999-01-01T00:00:00Z",
	}
	errs := GovernedFieldViolations(r, body, GovernedCreate, "admin")
	if len(errs) != 3 {
		t.Fatalf("want 3 violations, got %d: %v", len(errs), errs)
	}
	wantFields := []string{"created_at", "id", "updated_at"} // sorted
	for i, e := range errs {
		if e.Field != wantFields[i] {
			t.Errorf("errs[%d].Field = %q, want %q (sorted)", i, e.Field, wantFields[i])
		}
		if e.Rule != "read_only" {
			t.Errorf("field %q: rule %q, want read_only", e.Field, e.Rule)
		}
		if !strings.Contains(e.Message, `"import"`) {
			t.Errorf("field %q: create message must name the declarable way out, got %q", e.Field, e.Message)
		}
	}
}

// TestGovernedViolations_UpdateMessagesByteCompatible pins the historical
// CollectUpdate messages: the update-side rejection is byte-identical to what
// PATCH answered before the single source existed (a public contract clients
// may match on).
func TestGovernedViolations_UpdateMessagesByteCompatible(t *testing.T) {
	r := governedRes(&ImportConfig{Roles: []string{"admin"}}) // import must NOT matter on update
	errs := GovernedFieldViolations(r, map[string]any{
		"id":         "x",
		"created_at": "1999-01-01T00:00:00Z",
	}, GovernedUpdate, "admin")
	if len(errs) != 2 {
		t.Fatalf("want 2 violations (import never applies on update), got %d: %v", len(errs), errs)
	}
	want := map[string]string{
		"id":         `field "id" cannot be set`,
		"created_at": `field "created_at" is set automatically and cannot be written`,
	}
	for _, e := range errs {
		if e.Message != want[e.Field] {
			t.Errorf("field %q: message %q, want byte-compatible %q", e.Field, e.Message, want[e.Field])
		}
	}
}

// TestGovernedViolations_ImportGrant pins the declared door: the granted role
// passes for the declared fields, everyone and everything else stays rejected.
func TestGovernedViolations_ImportGrant(t *testing.T) {
	r := governedRes(&ImportConfig{Roles: []string{"importer"}})
	body := map[string]any{"id": "u", "created_at": "1999-01-01T00:00:00Z"}

	if errs := GovernedFieldViolations(r, body, GovernedCreate, "importer"); len(errs) != 0 {
		t.Fatalf("granted role must import freely, got %v", errs)
	}
	if errs := GovernedFieldViolations(r, body, GovernedCreate, "admin"); len(errs) != 2 {
		t.Fatalf("non-granted role must be rejected, got %v", errs)
	}
	if errs := GovernedFieldViolations(r, body, GovernedCreate, ""); len(errs) != 2 {
		t.Fatalf("empty role must be rejected, got %v", errs)
	}
}

// TestGovernedViolations_ImportFieldsSubset pins the fields subset: ["id"]
// unlocks client-generated ids WITHOUT opening timestamp forgery.
func TestGovernedViolations_ImportFieldsSubset(t *testing.T) {
	r := governedRes(&ImportConfig{Roles: []string{"importer"}, Fields: []string{"id"}})
	errs := GovernedFieldViolations(r, map[string]any{
		"id":         "u",
		"created_at": "1999-01-01T00:00:00Z",
	}, GovernedCreate, "importer")
	if len(errs) != 1 || errs[0].Field != "created_at" {
		t.Fatalf("subset [id] must reject only created_at, got %v", errs)
	}
	if !strings.Contains(errs[0].Message, "outside this resource's declared") {
		t.Errorf("subset rejection must say the field is outside the declared set, got %q", errs[0].Message)
	}
}

func TestImportDeclaredFields(t *testing.T) {
	if got := governedRes(nil).ImportDeclaredFields(); got != nil {
		t.Fatalf("no declaration → nil, got %v", got)
	}
	full := governedRes(&ImportConfig{Roles: []string{"a"}}).ImportDeclaredFields()
	if !reflect.DeepEqual(full, []string{"created_at", "id", "modificado_en", "updated_at"}) {
		t.Fatalf("no subset → full governed set, got %v", full)
	}
	sub := governedRes(&ImportConfig{Roles: []string{"a"}, Fields: []string{"id", "created_at"}}).ImportDeclaredFields()
	if !reflect.DeepEqual(sub, []string{"created_at", "id"}) {
		t.Fatalf("subset must round-trip sorted, got %v", sub)
	}
}

// ── load-time validation of the declaration ─────────────────────────────────

func importSchemaJSON(imp string) string {
	return `{
	  "$schema": "https://appximo.com/schema/v1",
	  "version": "1",
	  "name": "imp",
	  "resources": {
	    "notes": {
	      "fields": {
	        "title":      { "type": "string", "required": true },
	        "created_at": { "type": "time", "auto": "create" }
	      }` + imp + `
	    }
	  },
	  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
	}`
}

func parseFor(t *testing.T, doc string) []ValidationError {
	t.Helper()
	var s APISchema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return Validate(&s)
}

func hasRule(errs []ValidationError, rule string) bool {
	for _, e := range errs {
		if e.Rule == rule {
			return true
		}
	}
	return false
}

func TestImportValidation(t *testing.T) {
	cases := []struct {
		name     string
		imp      string
		wantRule string // "" = valid
	}{
		{"valid full", `, "import": { "roles": ["admin"] }`, ""},
		{"valid subset", `, "import": { "roles": ["admin"], "fields": ["id", "created_at"] }`, ""},
		{"no roles key", `, "import": { }`, "import_roles_required"},
		{"empty roles", `, "import": { "roles": [] }`, "import_roles_required"},
		{"undeclared role", `, "import": { "roles": ["ghost"] }`, "import_unknown_role"},
		{"empty fields", `, "import": { "roles": ["admin"], "fields": [] }`, "import_fields_empty"},
		{"non-governed field", `, "import": { "roles": ["admin"], "fields": ["title"] }`, "import_unknown_field"},
		{"unknown field name", `, "import": { "roles": ["admin"], "fields": ["nope"] }`, "import_unknown_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseFor(t, importSchemaJSON(tc.imp))
			if tc.wantRule == "" {
				if len(errs) != 0 {
					t.Fatalf("want valid, got %v", errs)
				}
				return
			}
			if !hasRule(errs, tc.wantRule) {
				t.Fatalf("want rule %q, got %v", tc.wantRule, errs)
			}
		})
	}
}

// TestImportStrictKeys pins the strict-key contract on the new level: a typo
// inside "import" is an error listing the valid keys, never a silently dead
// grant.
func TestImportStrictKeys(t *testing.T) {
	doc := importSchemaJSON(`, "import": { "role": ["admin"] }`)
	errs := CheckUnknownKeys(json.RawMessage(doc))
	found := false
	for _, e := range errs {
		if strings.Contains(e.Field, "import.role") && strings.Contains(e.Message, "roles") {
			found = true
		}
	}
	if !found {
		t.Fatalf(`want unknown-key error for "role" naming valid keys, got %v`, errs)
	}
}

// TestImportRoundTrip pins the editor/export contract: a schema with an import
// declaration marshals back with it intact.
func TestImportRoundTrip(t *testing.T) {
	doc := importSchemaJSON(`, "import": { "roles": ["admin"], "fields": ["id"] }`)
	var s APISchema
	if err := json.Unmarshal([]byte(doc), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 APISchema
	if err := json.Unmarshal(out, &s2); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	imp := s2.Resources["notes"].Import
	if imp == nil || !reflect.DeepEqual(imp.Roles, []string{"admin"}) || !reflect.DeepEqual(imp.Fields, []string{"id"}) {
		t.Fatalf("import did not round-trip: %+v", imp)
	}
}
