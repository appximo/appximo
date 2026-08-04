package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// vetSchemaJSON is the shape the generator produced for the veterinary app
// (docs/AUTHORING_JOURNEY.md 5-1): the vet's row rule compares the LOGGED-IN user's
// id against a foreign key to the `veterinarians` table. Valid, deployable, and it
// returns zero rows forever.
const vetSchemaJSON = `{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "petfriendly",
  "resources": {
    "veterinarians": { "fields": { "name": { "type": "string", "required": true } } },
    "appointments": {
      "fields": {
        "reason": { "type": "string" },
        "veterinarian_id": { "type": "uuid", "relation": "veterinarians" }
      }
    }
  },
  "rbac": { "roles": {
    "admin": { "resources": "*", "actions": ["*"] },
    "veterinario": { "permissions": { "appointments": {
      "actions": ["read", "update"],
      "conditions": { "field": "veterinarian_id", "op": "eq", "val": "$user_id" }
    } } }
  } }
}`

func mustLoad(t *testing.T, raw string) *APISchema {
	t.Helper()
	var s APISchema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if errs := Validate(&s); len(errs) > 0 {
		t.Fatalf("fixture must be VALID (that is the whole point): %v", errs)
	}
	return &s
}

// TestWarnings_IdentityConditionOnRelation: the exact production schema warns, and
// the warning does not make it invalid.
func TestWarnings_IdentityConditionOnRelation(t *testing.T) {
	s := mustLoad(t, vetSchemaJSON)
	warns := Warnings(s)
	if len(warns) != 1 {
		t.Fatalf("want exactly 1 warning, got %d: %v", len(warns), warns)
	}
	w := warns[0]
	if w.Rule != "identity_condition_on_relation" {
		t.Errorf("rule = %q", w.Rule)
	}
	if w.Field != "rbac.roles.veterinario.permissions.appointments.conditions.field" {
		t.Errorf("path = %q", w.Field)
	}
	// The message must name BOTH sides of the mismatch, in the reader's terms.
	for _, want := range []string{"veterinarian_id", "veterinarians", "shows nothing"} {
		if !strings.Contains(w.Message, want) {
			t.Errorf("message does not mention %q: %s", want, w.Message)
		}
	}
	if !strings.Contains(w.Fix, "references") || !strings.Contains(w.Fix, "user_id") {
		t.Errorf("fix does not name the bridge column: %s", w.Fix)
	}
}

// TestWarnings_ReportCarriesThemButStaysValid: `validate --json` — the oracle the AI
// correction loop reads — emits the warning as an actionable finding WITHOUT
// flipping `valid`.
func TestWarnings_ReportCarriesThemButStaysValid(t *testing.T) {
	rep := ValidateReport([]byte(vetSchemaJSON))
	if !rep.Valid {
		t.Fatalf("schema must stay valid; errors: %v", rep.Errors)
	}
	if len(rep.Warnings) != 1 {
		t.Fatalf("want 1 warning in the report, got %d", len(rep.Warnings))
	}
	w := rep.Warnings[0]
	if w.Rule != "identity_condition_on_relation" || w.Path == "" || w.Fix == "" {
		t.Errorf("warning is not actionable: %+v", w)
	}
}

// TestWarnings_BridgeColumnSilencesIt: applying the suggested fix removes the
// warning. A warning you cannot turn off is noise.
func TestWarnings_BridgeColumnSilencesIt(t *testing.T) {
	fixed := strings.Replace(vetSchemaJSON,
		`"veterinarians": { "fields": { "name": { "type": "string", "required": true } } }`,
		`"veterinarians": { "fields": { "name": { "type": "string", "required": true }, "user_id": { "type": "uuid", "unique": true } } }`, 1)
	fixed = strings.Replace(fixed,
		`"veterinarian_id": { "type": "uuid", "relation": "veterinarians" }`,
		`"veterinarian_id": { "type": "uuid", "relation": "veterinarians", "references": "user_id" }`, 1)
	s := mustLoad(t, fixed)
	if warns := Warnings(s); len(warns) != 0 {
		t.Fatalf("the recommended fix must silence the warning, got: %v", warns)
	}
}

// TestWarnings_NoFalsePositives: an owner-scoped condition on a plain column (the
// normal, correct pattern) must not warn — nor must a literal condition on a
// relation column.
func TestWarnings_NoFalsePositives(t *testing.T) {
	raw := `{
	  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "x",
	  "resources": {
	    "customers": { "fields": { "name": { "type": "string" } } },
	    "orders": { "fields": {
	      "user_id": { "type": "uuid" },
	      "status": { "type": "string" },
	      "customer_id": { "type": "uuid", "relation": "customers" }
	    } }
	  },
	  "rbac": { "roles": {
	    "owner":  { "permissions": { "orders": { "actions": ["read"],
	                "conditions": { "field": "user_id", "op": "eq", "val": "$user_id" } } } },
	    "public": { "permissions": { "orders": { "actions": ["read"],
	                "conditions": { "field": "customer_id", "op": "eq", "val": "acme" } } } }
	  } }
	}`
	if warns := Warnings(mustLoad(t, raw)); len(warns) != 0 {
		t.Fatalf("no warning expected, got: %v", warns)
	}
}

// TestWarnings_RoleGlobalFormToo: the role-global form is checked against every
// resource the role lists, exactly like the runtime injects it.
func TestWarnings_RoleGlobalFormToo(t *testing.T) {
	raw := `{
	  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "x",
	  "resources": {
	    "vets": { "fields": { "name": { "type": "string" } } },
	    "visits": { "fields": { "vet_id": { "type": "uuid", "relation": "vets" } } }
	  },
	  "rbac": { "roles": {
	    "vet": { "resources": ["visits"], "actions": ["read"],
	             "conditions": { "field": "vet_id", "op": "eq", "val": "$user_id" } }
	  } }
	}`
	warns := Warnings(mustLoad(t, raw))
	if len(warns) != 1 || warns[0].Rule != "identity_condition_on_relation" {
		t.Fatalf("role-global form must warn too, got: %v", warns)
	}
}

// TestWarnings_BareVariableVal — ENG-20's warning half: a condition val that is a
// known variable's name WITHOUT the `$` ("user_id") is legal (a literal), almost
// always a forgotten dollar sign, and produces the zero-rows-forever failure —
// so it warns, suggesting the variable, on both RBAC forms. A real literal
// ("published") stays silent.
func TestWarnings_BareVariableVal(t *testing.T) {
	raw := `{
      "$schema":"x","version":"1","name":"x",
      "resources": { "posts": { "fields": { "author_id": { "type": "string" }, "status": { "type": "string" } } } },
      "rbac": { "roles": {
        "owner": { "resources": ["posts"], "actions": ["read"],
                   "conditions": { "field":"author_id","op":"eq","val":"user_id" } },
        "pub":   { "permissions": { "posts": { "actions": ["read"],
                   "conditions": { "field":"status","op":"eq","val":"published" } } } }
      } }
    }`
	var s APISchema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if errs := Validate(&s); len(errs) != 0 {
		t.Fatalf("schema must stay VALID (warning, not error), got: %v", errs)
	}
	warns := Warnings(&s)
	var hit bool
	for _, w := range warns {
		if w.Rule == "bare_condition_variable" {
			hit = true
			if !strings.Contains(w.Message, "$user_id") && !strings.Contains(w.Fix, "$user_id") {
				t.Fatalf("warning must suggest $user_id, got: %+v", w)
			}
		}
		if strings.Contains(w.Field, "pub.") {
			t.Fatalf("literal \"published\" must not warn, got: %+v", w)
		}
	}
	if !hit {
		t.Fatalf("expected bare_condition_variable warning, got: %v", warns)
	}
}
