package aigen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// scriptedClient is a deterministic ModelClient: it returns the next canned
// response per call, recording the messages it was given. This proves the loop
// end-to-end (generate → validate → correct → valid) with NO network and NO API
// key — exactly the property that lets `make test` stay green.
type scriptedClient struct {
	responses []string
	usages    []Usage
	call      int
	lastMsgs  [][]Message
}

func (c *scriptedClient) Complete(_ context.Context, _ string, messages []Message) (Completion, error) {
	c.lastMsgs = append(c.lastMsgs, messages)
	i := c.call
	c.call++
	if i >= len(c.responses) {
		return Completion{}, nil
	}
	u := Usage{InputTokens: 1000, OutputTokens: 500}
	if i < len(c.usages) {
		u = c.usages[i]
	}
	return Completion{Text: c.responses[i], Usage: u}, nil
}

const validSchema = `{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title": { "type": "string", "required": true },
        "status": { "type": "string", "enum": ["open", "done"] }
      }
    }
  },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
}`

// invalidSchema uses type "number" — rejected by the validator (the type set is
// string/text/int/int64/float64/bool/uuid/time/json). A classic AI mistake the
// loop must correct.
const invalidSchema = `{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title": { "type": "string", "required": true },
        "count": { "type": "number" }
      }
    }
  }
}`

func TestGenerate_ValidFirstTry(t *testing.T) {
	client := &scriptedClient{responses: []string{validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Valid || !res.Converged {
		t.Fatalf("expected valid+converged, got valid=%v converged=%v", res.Valid, res.Converged)
	}
	if !res.FirstTry {
		t.Errorf("expected FirstTry true")
	}
	if res.Iterations != 1 {
		t.Errorf("expected 1 iteration, got %d", res.Iterations)
	}
	// The result schema must itself revalidate (round-trip).
	if rep := schema.ValidateReport(res.Schema); !rep.Valid {
		t.Errorf("returned schema does not revalidate: %+v", rep.Errors)
	}
}

func TestGenerate_CorrectsAfterError(t *testing.T) {
	client := &scriptedClient{responses: []string{invalidSchema, validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Converged {
		t.Fatalf("expected convergence after correction, remaining: %+v", res.RemainingErrors)
	}
	if res.FirstTry {
		t.Errorf("expected FirstTry false (it took a correction)")
	}
	if res.Iterations != 2 {
		t.Errorf("expected 2 iterations, got %d", res.Iterations)
	}
	// The correction round must have been fed the actionable errors: the second
	// call's messages include a user turn quoting the validator report.
	if len(client.lastMsgs) < 2 {
		t.Fatalf("expected at least 2 model calls")
	}
	second := client.lastMsgs[1]
	last := second[len(second)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "VALIDATION ERRORS") {
		t.Errorf("correction turn did not feed back the error report: %q", last.Content)
	}
	// And it should name the offending rule for the bad type.
	if !strings.Contains(last.Content, "invalid_type") && !strings.Contains(last.Content, "invalid_enum_value") {
		t.Errorf("expected the type error to be surfaced, got: %s", last.Content)
	}
}

func TestGenerate_NeverConvergesReportsErrors(t *testing.T) {
	// The model keeps returning the same invalid schema — the loop must stop at
	// the budget and report the remaining errors (no infinite loop).
	client := &scriptedClient{responses: []string{invalidSchema, invalidSchema, invalidSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku, MaxIterations: 3})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Converged || res.Valid {
		t.Fatalf("expected non-convergence")
	}
	if res.Iterations != 3 {
		t.Errorf("expected 3 iterations (the budget), got %d", res.Iterations)
	}
	if len(res.RemainingErrors) == 0 {
		t.Errorf("expected remaining errors to be reported")
	}
}

func TestGenerate_CostInstrumentation(t *testing.T) {
	client := &scriptedClient{
		responses: []string{invalidSchema, validSchema},
		usages:    []Usage{{InputTokens: 2000, OutputTokens: 800}, {InputTokens: 3000, OutputTokens: 600}},
	}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Usage.InputTokens != 5000 || res.Usage.OutputTokens != 1400 {
		t.Fatalf("cumulative usage wrong: %+v", res.Usage)
	}
	// Haiku: $1/MTok in, $5/MTok out → 5000/1e6*1 + 1400/1e6*5 = 0.005 + 0.007 = 0.012
	want := 0.012
	if res.CostUSD < want-1e-9 || res.CostUSD > want+1e-9 {
		t.Errorf("cost = %.6f, want %.6f", res.CostUSD, want)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // a substring the output must start with / contain
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced_bare", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"prose_prefix", "Here is the schema:\n{\"a\":1}", `{"a":1}`},
		{"brace_in_string", `{"pattern":"^[a-z]{2,5}$"}`, `{"pattern":"^[a-z]{2,5}$"}`},
		{"trailing_prose", "{\"a\":1}\nHope this helps!", `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.in)
			if got != tc.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Extracted JSON should parse (for the well-formed cases).
			var v any
			if err := json.Unmarshal([]byte(got), &v); err != nil {
				t.Errorf("extracted output is not valid JSON: %v", err)
			}
		})
	}
}

func TestPricingFor(t *testing.T) {
	for _, m := range []string{ModelHaiku, ModelSonnet, ModelOpus} {
		if _, ok := PricingFor(m); !ok {
			t.Errorf("expected pricing for %s", m)
		}
	}
	if _, ok := PricingFor("nonexistent-model"); ok {
		t.Errorf("expected no pricing for unknown model")
	}
}
