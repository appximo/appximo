package schema

import (
	"strings"
	"testing"
)

// ENG-40: `explain` must narrate the rbac.public block — the owner reviewing
// an AI-written schema most needs to hear what the whole internet can read.
func TestExplainNarratesPublicBlock(t *testing.T) {
	s := &APISchema{
		Name:    "blog",
		Version: "1",
		Resources: map[string]ResourceSchema{
			"articles": {Fields: map[string]FieldDef{
				"title":  {Type: "string", Required: true},
				"body":   {Type: "text"},
				"status": {Type: "string", Enum: []string{"draft", "published"}},
			}},
		},
		RBAC: RBACPolicy{
			Roles: map[string]RolePolicy{
				"admin": {Resources: []byte(`"*"`), Actions: []string{"*"}},
			},
			Public: map[string]ResourcePermission{
				"articles": {
					Actions:    []string{"read"},
					Conditions: &Condition{Field: "status", Op: "eq", Val: "published"},
					Fields:     []string{"id", "title", "body"},
				},
			},
		},
	}

	en := Explain(s, "en")
	for _, want := range []string{
		"ANYONE on the internet, without logging in, can read articles",
		`but only rows whose "status" equals "published"`,
		"sees only the fields: id, title, body",
	} {
		if !strings.Contains(en, want) {
			t.Errorf("EN explain missing %q\n---\n%s", want, en)
		}
	}

	es := Explain(s, "es")
	for _, want := range []string{
		"CUALQUIERA en internet, sin iniciar sesión, puede leer articles",
		`pero solo las filas cuyo "status" vale "published"`,
	} {
		if !strings.Contains(es, want) {
			t.Errorf("ES explain missing %q\n---\n%s", want, es)
		}
	}
}

// A public-only schema (zero declared roles) is legal and deployable — explain
// must narrate its public surface instead of claiming every request is denied.
func TestExplainPublicOnlySchema(t *testing.T) {
	s := &APISchema{
		Name:    "catalog",
		Version: "1",
		Resources: map[string]ResourceSchema{
			"products": {Fields: map[string]FieldDef{"name": {Type: "string"}}},
		},
		RBAC: RBACPolicy{
			Public: map[string]ResourcePermission{
				"products": {Actions: []string{"read"}},
			},
		},
	}
	out := Explain(s, "en")
	if strings.Contains(out, "No roles are declared") {
		t.Errorf("public-only schema must not print the no-RBAC warning:\n%s", out)
	}
	if !strings.Contains(out, "ANYONE on the internet, without logging in, can read products") {
		t.Errorf("public-only schema must narrate the public surface:\n%s", out)
	}
	if !strings.Contains(out, "denied by default") {
		t.Errorf("deny-by-default line must still close the section:\n%s", out)
	}
}
