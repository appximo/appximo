package schema_test

import (
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// G5 unit tests for the state-machine compile + body checks (no DB). The end-to-end
// transition enforcement (the UPDATE WHERE guard) is in pkg/integration.

func orderStatusField() schema.FieldDef {
	return schema.FieldDef{
		Type: "string",
		Enum: []string{"pending", "paid", "shipped", "delivered", "cancelled"},
		StateMachine: &schema.StateMachine{
			Initial: []string{"pending"},
			Transitions: map[string][]string{
				"pending":   {"paid", "cancelled"},
				"paid":      {"shipped"},
				"shipped":   {"delivered"},
				"delivered": {},
				"cancelled": {},
			},
		},
	}
}

func TestStateMachine_Methods(t *testing.T) {
	sm := orderStatusField().StateMachine

	known := sm.KnownStates()
	for _, s := range []string{"pending", "paid", "shipped", "delivered", "cancelled"} {
		if !known[s] {
			t.Errorf("KnownStates missing %q", s)
		}
	}
	if known["nope"] {
		t.Error("KnownStates should not contain an undeclared state")
	}

	if !sm.IsInitial("pending") || sm.IsInitial("paid") {
		t.Error("IsInitial wrong")
	}

	// OriginsOf("paid") = {pending}; OriginsOf("delivered") = {shipped}; an
	// unreachable target = {} (only origins that DECLARE a move to it).
	if got := sm.OriginsOf("paid"); len(got) != 1 || got[0] != "pending" {
		t.Errorf("OriginsOf(paid) = %v; want [pending]", got)
	}
	if got := sm.OriginsOf("delivered"); len(got) != 1 || got[0] != "shipped" {
		t.Errorf("OriginsOf(delivered) = %v; want [shipped]", got)
	}
	if got := sm.OriginsOf("pending"); len(got) != 0 {
		t.Errorf("OriginsOf(pending) = %v; want [] (no state transitions INTO pending)", got)
	}
}

func TestStateMachine_InitialAsString(t *testing.T) {
	// `initial` accepts a bare string (the common case) and normalizes to a slice.
	var sm schema.StateMachine
	if err := sm.UnmarshalJSON([]byte(`{"initial":"draft","transitions":{"draft":["sent"]}}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sm.Initial) != 1 || sm.Initial[0] != "draft" {
		t.Fatalf("initial = %v; want [draft]", sm.Initial)
	}
}

func TestValidate_InitialAndKnownStates(t *testing.T) {
	res := schema.ResourceSchema{Fields: map[string]schema.FieldDef{"status": orderStatusField()}}
	rv := schema.CompileRules(&res)

	// Create: an initial state passes; an advanced state is rejected.
	if errs := rv.ValidateInitialStates(map[string]any{"status": "pending"}); len(errs) != 0 {
		t.Errorf("pending should be a valid initial state: %v", errs)
	}
	if errs := rv.ValidateInitialStates(map[string]any{"status": "shipped"}); len(errs) == 0 {
		t.Error("creating in 'shipped' must be rejected (not an initial state)")
	}

	// Known-state check (ValidateWrite, both create + update): an unknown value is
	// rejected; a known value passes.
	if errs := rv.ValidateWrite(map[string]any{"status": "frobnicate"}, false); len(errs) == 0 {
		t.Error("an unknown state must be rejected by ValidateWrite")
	}
	if errs := rv.ValidateWrite(map[string]any{"status": "shipped"}, false); len(errs) != 0 {
		t.Errorf("a known state must pass ValidateWrite on update: %v", errs)
	}
}

func TestValidateStateMachine_LoadRejections(t *testing.T) {
	base := func(field string) string {
		return `{"$schema":"x","version":"1","name":"x","resources":{"o":{"fields":{` + field +
			`}}},"rbac":{"roles":{"a":{"resources":"*","actions":["*"]}}}}`
	}
	cases := []struct {
		name, field, wantSubstr string
		valid                   bool
	}{
		{"valid", `"s":{"type":"string","enum":["a","b"],"default":"a","state_machine":{"initial":"a","transitions":{"a":["b"],"b":[]}}}`, "", true},
		{"non-string field", `"n":{"type":"int","state_machine":{"initial":"a","transitions":{"a":["b"]}}}`, "string/text", false},
		{"state not in enum", `"s":{"type":"string","enum":["a"],"state_machine":{"initial":"a","transitions":{"a":["zzz"]}}}`, "enum", false},
		{"default not initial", `"s":{"type":"string","default":"b","state_machine":{"initial":"a","transitions":{"a":["b"]}}}`, "initial", false},
		{"empty initial", `"s":{"type":"string","state_machine":{"initial":[],"transitions":{"a":["b"]}}}`, "at least one initial", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := schema.Validate(parseSchema(t, base(c.field)))
			if c.valid && len(errs) != 0 {
				t.Fatalf("expected valid, got %v", errs)
			}
			if !c.valid && !hasError(errs, c.wantSubstr) {
				t.Fatalf("expected an error containing %q, got %v", c.wantSubstr, errs)
			}
		})
	}
}

// A resource WITHOUT a state machine is unaffected — no state checks fire.
func TestValidate_NoStateMachine_Unaffected(t *testing.T) {
	res := schema.ResourceSchema{Fields: map[string]schema.FieldDef{"status": {Type: "string"}}}
	rv := schema.CompileRules(&res)
	if errs := rv.ValidateInitialStates(map[string]any{"status": "anything"}); len(errs) != 0 {
		t.Errorf("free string field should accept any value: %v", errs)
	}
	if errs := rv.ValidateWrite(map[string]any{"status": "anything"}, false); len(errs) != 0 {
		t.Errorf("free string field should accept any value on update: %v", errs)
	}
}
