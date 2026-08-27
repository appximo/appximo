package codegen

import (
	"net/http"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// The identity-column update rule, table-driven (MOTOR-AUTORIZACION-S1). The
// integration file ownership_update_integration_test.go proves it through every
// door of a real engine; this pins the decision table itself.
func TestEnforceUpdateRBAC(t *testing.T) {
	const me, other = "u-me", "u-other"
	ident := &rbac.EvalResult{Allowed: true, Condition: &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: me, Identity: true}}
	literal := &rbac.EvalResult{Allowed: true, Condition: &rbac.WhereCondition{Field: "status", Op: "eq", Value: "pending"}}
	cases := []struct {
		name       string
		ev         *rbac.EvalResult
		body, sets map[string]any
		wantStatus int
		wantSets   map[string]any
	}{
		{"nil eval (no policy) is a no-op", nil, map[string]any{"owner_id": other}, map[string]any{"owner_id": other}, 0, map[string]any{"owner_id": other}},
		{"no condition is a no-op", &rbac.EvalResult{Allowed: true}, map[string]any{"owner_id": other}, map[string]any{"owner_id": other}, 0, map[string]any{"owner_id": other}},
		{"literal condition is a visibility filter, not ownership", literal, map[string]any{"status": "approved"}, map[string]any{"status": "approved"}, 0, map[string]any{"status": "approved"}},
		{"another principal → 403", ident, map[string]any{"owner_id": other}, map[string]any{"owner_id": other}, http.StatusForbidden, nil},
		{"null → 403 (a detach is a give-away to nobody)", ident, map[string]any{"owner_id": nil}, map[string]any{"owner_id": nil}, http.StatusForbidden, nil},
		{"allowlist dropped it from sets, body still judged → 403", ident, map[string]any{"owner_id": other, "title": "x"}, map[string]any{"title": "x"}, http.StatusForbidden, nil},
		{"own id re-sent is a no-op", ident, map[string]any{"owner_id": me, "title": "x"}, map[string]any{"owner_id": me, "title": "x"}, 0, map[string]any{"owner_id": me, "title": "x"}},
		{"PATCH without the column touches nothing", ident, map[string]any{"title": "x"}, map[string]any{"title": "x"}, 0, map[string]any{"title": "x"}},
		{"PUT omitting the column keeps the caller (never NULL)", ident, map[string]any{"title": "x"}, map[string]any{"title": "x", "owner_id": nil}, 0, map[string]any{"title": "x", "owner_id": me}},
		{"PUT omitting a column the allowlist excludes leaves it alone", ident, map[string]any{"title": "x"}, map[string]any{"title": "x"}, 0, map[string]any{"title": "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, msg := EnforceUpdateRBAC(c.body, c.sets, c.ev)
			if st != c.wantStatus {
				t.Fatalf("status %d (%q), want %d", st, msg, c.wantStatus)
			}
			if st != 0 {
				if msg != `field "owner_id" must match the authenticated principal` {
					t.Errorf("message %q — must be the create side's, byte for byte", msg)
				}
				return
			}
			for k, v := range c.wantSets {
				if c.sets[k] != v {
					t.Errorf("sets[%s] = %v, want %v", k, c.sets[k], v)
				}
			}
			if len(c.sets) != len(c.wantSets) {
				t.Errorf("sets %v, want %v", c.sets, c.wantSets)
			}
		})
	}
}

// The two halves must answer the identical message: a client parses one string.
func TestEnforceCreateAndUpdateRBAC_SameMessage(t *testing.T) {
	ev := &rbac.EvalResult{Allowed: true, Condition: &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: "me", Identity: true}}
	_, cm := EnforceCreateRBAC(map[string]any{"owner_id": "other"}, ev)
	_, um := EnforceUpdateRBAC(map[string]any{"owner_id": "other"}, map[string]any{"owner_id": "other"}, ev)
	if cm == "" || cm != um {
		t.Fatalf("create %q vs update %q", cm, um)
	}
}

// A state-machine field is never null: PATCH null and PUT-omitted are named
// 422s; a required state field's null is left to the `required` rule.
func TestStateFieldNullViolations(t *testing.T) {
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"status": {Type: "string", StateMachine: &schema.StateMachine{Initial: []string{"a"}, Transitions: map[string][]string{"a": {"b"}}}},
		"phase":  {Type: "string", Required: true, StateMachine: &schema.StateMachine{Initial: []string{"x"}}},
		"title":  {Type: "string"},
	}}
	rule := func(errs []schema.FieldRuleError) map[string]string {
		m := map[string]string{}
		for _, e := range errs {
			m[e.Field] = e.Rule
		}
		return m
	}
	if got := rule(StateFieldNullViolations(res, map[string]any{"status": nil, "phase": nil, "title": nil}, false)); got["status"] != "state" || got["phase"] != "" || got["title"] != "" {
		t.Errorf("PATCH nulls: %v", got)
	}
	if got := rule(StateFieldNullViolations(res, map[string]any{"title": "x"}, true)); got["status"] != "state" || got["phase"] != "state" {
		t.Errorf("PUT omission: %v (both state fields must be named)", got)
	}
	if got := StateFieldNullViolations(res, map[string]any{"title": "x"}, false); len(got) != 0 {
		t.Errorf("PATCH without the field must pass: %v", got)
	}
	if got := StateFieldNullViolations(res, map[string]any{"status": "b", "phase": "x"}, true); len(got) != 0 {
		t.Errorf("PUT with both present must pass: %v", got)
	}
}
