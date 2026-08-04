package schema_test

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func findErr(rep schema.ValidationReport, pathContains string) (schema.StructuredError, bool) {
	for _, e := range rep.Errors {
		if strings.Contains(e.Path, pathContains) {
			return e, true
		}
	}
	return schema.StructuredError{}, false
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestValidateReport_Valid: a valid schema reports valid with an empty (non-null)
// errors array.
func TestValidateReport_Valid(t *testing.T) {
	raw := []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1",
	  "resources":{"tasks":{"fields":{"title":{"type":"string","required":true}}}},
	  "rbac":{"roles":{"admin":{"resources":"*","actions":["*"]}}}}`)
	rep := schema.ValidateReport(raw)
	if !rep.Valid || len(rep.Errors) != 0 {
		t.Fatalf("expected valid with no errors, got valid=%v errors=%v", rep.Valid, rep.Errors)
	}
}

// TestValidateReport_Actionable: each class of error produces a located error with a
// machine-readable rule, a non-empty fix, and (for closed sets) the expected values —
// everything an LLM needs to correct it.
func TestValidateReport_Actionable(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		pathHas     string
		wantSource  string
		fixContains string // substring the fix should contain (case-insensitive), "" = any non-empty
		expectHas   string // a value the Expected list should contain, "" = skip
	}{
		{
			name:        "bad field type",
			raw:         `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"varchar"}}}}}`,
			pathHas:     "fields.a.type",
			wantSource:  "metaschema",
			fixContains: "",
			expectHas:   "string",
		},
		{
			name:        "unknown relation target",
			raw:         `{"$schema":"x","version":"1","resources":{"posts":{"fields":{"u":{"type":"uuid","relation":"userz"}}},"users":{"fields":{"n":{"type":"string"}}}}}`,
			pathHas:     "fields.u.relation",
			wantSource:  "semantic",
			fixContains: "existing",
			expectHas:   "users",
		},
		{
			name:        "references not unique",
			raw:         `{"$schema":"x","version":"1","resources":{"posts":{"fields":{"a":{"type":"string","relation":"users","references":"email"}}},"users":{"fields":{"email":{"type":"string"}}}}}`,
			pathHas:     "fields.a.references",
			wantSource:  "semantic",
			fixContains: "unique",
		},
		{
			name:        "condition field unknown",
			raw:         `{"$schema":"x","version":"1","resources":{"posts":{"fields":{"author_id":{"type":"uuid"}}}},"rbac":{"roles":{"m":{"permissions":{"posts":{"actions":["read"],"conditions":{"field":"ownerz","op":"eq","val":"$user_id"}}}}}}}`,
			pathHas:     "conditions.field",
			wantSource:  "semantic",
			fixContains: "available",
			expectHas:   "author_id",
		},
		{
			name:        "condition op not eq",
			raw:         `{"$schema":"x","version":"1","rbac":{"roles":{"m":{"resources":["t"],"actions":["read"],"conditions":{"field":"x","op":"gt","val":"$user_id"}}}}}`,
			pathHas:     "conditions.op",
			wantSource:  "metaschema",
			fixContains: "",
			expectHas:   "eq",
		},
		{
			name:        "js after-hook",
			raw:         `{"$schema":"x","version":"1","resources":{"t":{"hooks":{"after_create":{"type":"js","script":"x"}}}}}`,
			pathHas:     "after_create.type",
			wantSource:  "metaschema",
			fixContains: "",
		},
		{
			name:        "unknown key",
			raw:         `{"$schema":"x","version":"1","resources":{"t":{"fields":{"a":{"type":"string","foo":1}}}}}`,
			pathHas:     "fields.a",
			wantSource:  "metaschema",
			fixContains: "remove",
		},
		{
			name:        "fields allowlist unknown",
			raw:         `{"$schema":"x","version":"1","resources":{"posts":{"fields":{"title":{"type":"string"}}}},"rbac":{"roles":{"m":{"permissions":{"posts":{"actions":["read"],"fields":["title","ghostz"]}}}}}}`,
			pathHas:     "permissions.posts.fields",
			wantSource:  "semantic",
			fixContains: "available",
			expectHas:   "title",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := schema.ValidateReport([]byte(c.raw))
			if rep.Valid {
				t.Fatalf("expected invalid, got valid")
			}
			e, ok := findErr(rep, c.pathHas)
			if !ok {
				t.Fatalf("no error at path containing %q; got %+v", c.pathHas, rep.Errors)
			}
			if e.Rule == "" {
				t.Errorf("error has no rule: %+v", e)
			}
			if e.Fix == "" {
				t.Errorf("error has no fix: %+v", e)
			}
			if c.wantSource != "" && e.Source != c.wantSource {
				t.Errorf("source = %q, want %q (%+v)", e.Source, c.wantSource, e)
			}
			if c.fixContains != "" && !strings.Contains(strings.ToLower(e.Fix), strings.ToLower(c.fixContains)) {
				t.Errorf("fix %q does not contain %q", e.Fix, c.fixContains)
			}
			if c.expectHas != "" && !contains(e.Expected, c.expectHas) {
				t.Errorf("expected list %v does not contain %q", e.Expected, c.expectHas)
			}
		})
	}
}

// TestValidateReport_CorrectionLoop simulates the AI feedback loop WITHOUT the AI:
// an invalid schema → the structured error says exactly what to fix and the valid
// options → apply the fix → the schema is valid. This is the contract that lets an
// LLM self-correct.
func TestValidateReport_CorrectionLoop(t *testing.T) {
	// A schema whose only error is a typo'd relation target ("userz" → "users").
	bad := `{"$schema":"https://appximo.com/schema/v1","version":"1",
	  "resources":{
	    "users":{"fields":{"name":{"type":"string"}}},
	    "posts":{"fields":{"author_id":{"type":"uuid","relation":"userz"}}}
	  }}`
	rep := schema.ValidateReport([]byte(bad))
	if rep.Valid {
		t.Fatal("expected the typo'd schema to be invalid")
	}
	e, ok := findErr(rep, "fields.author_id.relation")
	if !ok {
		t.Fatalf("no relation error found: %+v", rep.Errors)
	}
	// The error tells the corrector the valid options.
	if !contains(e.Expected, "users") {
		t.Fatalf("expected the fix options to include the real target 'users', got %v", e.Expected)
	}
	// "Apply the fix": replace the typo with the suggested valid target.
	fixed := strings.Replace(bad, `"relation":"userz"`, `"relation":"`+e.Expected[0]+`"`, 1)
	// Pick 'users' specifically (Expected is sorted; ensure determinism by using it).
	if e.Expected[0] != "users" {
		fixed = strings.Replace(bad, `"relation":"userz"`, `"relation":"users"`, 1)
	}
	rep2 := schema.ValidateReport([]byte(fixed))
	if !rep2.Valid {
		t.Fatalf("after applying the suggested fix the schema should be valid, got: %+v", rep2.Errors)
	}
}
