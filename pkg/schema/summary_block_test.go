package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

// VOZ-VISUAL-S1: the top-level `summary` block (which resources the daily digest
// reports, in order) and `state_machine.pending` (which states WAIT for
// someone) are validated at LOAD — a typo is an error naming the fix, never a
// digest that silently reports the wrong thing.

func summarySchemaJSON(summaryBlock, pending string) string {
	return `{
  "$schema": "https://appximo.com/schema/v1", "version": "1", "name": "t",
  "resources": {
    "ordenes": { "fields": {
      "estado": { "type": "string", "enum": ["creada","pagada","enviada","cerrada"], "default": "creada",
        "state_machine": { "initial": "creada",
          "transitions": { "creada": ["pagada"], "pagada": ["enviada"], "enviada": ["cerrada"], "cerrada": [] }` + pending + ` } }
    } },
    "clientes": { "fields": { "nombre": { "type": "string" } } }
  },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }` + summaryBlock + `
}`
}

func TestSummaryBlock_Validation(t *testing.T) {
	cases := []struct {
		name     string
		block    string
		wantRule string
	}{
		{"absent = default", ``, ""},
		{"valid ordered list", `, "summary": { "resources": ["ordenes", "clientes"] }`, ""},
		{"valid subset", `, "summary": { "resources": ["clientes"] }`, ""},
		{"unknown resource is a load error", `, "summary": { "resources": ["ordenes", "ventas"] }`, "summary_unknown_resource"},
		{"duplicate", `, "summary": { "resources": ["ordenes", "ordenes"] }`, "summary_duplicate_resource"},
		{"empty list is dead config", `, "summary": { "resources": [] }`, "summary_resources_empty"},
		{"empty name", `, "summary": { "resources": [""] }`, "summary_resource_empty"},
		// VOZ-DELTA-S1: the send policy
		{"policy only, no resources", `, "summary": { "notify": "always" }`, ""},
		{"notify changes + quiet_days", `, "summary": { "resources": ["ordenes"], "notify": "changes", "quiet_days": 3 }`, ""},
		{"quiet_days zero = never", `, "summary": { "quiet_days": 0 }`, ""},
		{"bad notify", `, "summary": { "notify": "daily" }`, "summary_notify_invalid"},
		{"negative quiet_days", `, "summary": { "quiet_days": -1 }`, "summary_quiet_days_negative"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseFor(t, summarySchemaJSON(tc.block, ""))
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

func TestSummaryBlock_StrictKeys(t *testing.T) {
	raw := summarySchemaJSON(`, "summary": { "resource": ["ordenes"] }`, "")
	errs := CheckUnknownKeys([]byte(raw))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "resources") {
		t.Fatalf("a typo'd summary key must be rejected listing the valid keys, got %v", errs)
	}
	raw = summarySchemaJSON(`, "summary": { "notify": "changes", "quietdays": 2 }`, "")
	errs = CheckUnknownKeys([]byte(raw))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "quiet_days") {
		t.Fatalf("a typo'd quiet_days key must be rejected listing the valid keys, got %v", errs)
	}
	raw = summarySchemaJSON(``, `, "pendin": ["creada"]`)
	errs = CheckUnknownKeys([]byte(raw))
	if len(errs) == 0 || !strings.Contains(errs[0].Message, "pending") {
		t.Fatalf("a typo'd pending key must be rejected listing the valid keys, got %v", errs)
	}
}

func TestStateMachinePending_Validation(t *testing.T) {
	cases := []struct {
		name     string
		pending  string
		wantRule string
	}{
		{"absent = infer", ``, ""},
		{"declared non-terminal", `, "pending": ["pagada"]`, ""},
		{"declared several", `, "pending": ["creada", "pagada"]`, ""},
		{"explicit none", `, "pending": []`, ""},
		{"terminal cannot wait", `, "pending": ["cerrada"]`, "state_machine_pending_terminal"},
		{"unknown state", `, "pending": ["entregada"]`, "state_machine_pending_unknown"},
		{"duplicate", `, "pending": ["pagada", "pagada"]`, "state_machine_pending_duplicate"},
		{"empty string", `, "pending": [""]`, "state_machine_pending_empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseFor(t, summarySchemaJSON("", tc.pending))
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

// nil vs [] must survive a JSON round trip: an explicit "pending": [] is a
// declaration ("nothing here waits"), absence means "infer".
func TestStateMachinePending_NilVsEmptyRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		in           string
		wantDeclared bool
		wantOut      string
	}{
		{`{"initial":"a","transitions":{"a":["b"],"b":[]}}`, false, `"pending"`},
		{`{"initial":"a","transitions":{"a":["b"],"b":[]},"pending":[]}`, true, `"pending":[]`},
		{`{"initial":"a","transitions":{"a":["b"],"b":[]},"pending":["a"]}`, true, `"pending":["a"]`},
	} {
		var sm StateMachine
		if err := json.Unmarshal([]byte(tc.in), &sm); err != nil {
			t.Fatal(err)
		}
		if sm.PendingDeclared() != tc.wantDeclared {
			t.Errorf("%s: declared=%v want %v", tc.in, sm.PendingDeclared(), tc.wantDeclared)
		}
		out, _ := json.Marshal(sm)
		if tc.wantDeclared && !strings.Contains(string(out), tc.wantOut) {
			t.Errorf("%s: marshal lost the declaration: %s", tc.in, out)
		}
		if !tc.wantDeclared && strings.Contains(string(out), tc.wantOut) {
			t.Errorf("%s: absent pending must stay absent: %s", tc.in, out)
		}
	}
}

func TestExplain_ReadsBackSummaryAndPending(t *testing.T) {
	s := parseOK(t, summarySchemaJSON(`, "summary": { "resources": ["ordenes", "clientes"] }`, `, "pending": ["pagada"]`))
	es := Explain(s, "es")
	for _, want := range []string{"en este orden", `"ordenes"`, "a la espera de que alguien actúe", `"pagada"`} {
		if !strings.Contains(es, want) {
			t.Errorf("explain es must contain %q:\n%s", want, es)
		}
	}
	en := Explain(s, "en")
	if !strings.Contains(en, "waiting for someone to act") || !strings.Contains(en, "in this order") {
		t.Errorf("explain en must read back pending + summary:\n%s", en)
	}
	s2 := parseOK(t, summarySchemaJSON(``, `, "pending": []`))
	if !strings.Contains(Explain(s2, "es"), "nada en este ciclo espera a nadie") {
		t.Errorf("explicit [] must be read back")
	}
}

func parseOK(t *testing.T, doc string) *APISchema {
	t.Helper()
	s, err := LoadFromBytes([]byte(doc))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}
