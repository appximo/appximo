package aigen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
)

// scriptedClient is a deterministic ModelClient: it returns the next canned
// response per call, recording the requests it was given. This proves the loop
// end-to-end (generate → validate → correct → valid) with NO network and NO API
// key — the property that lets `make test` stay green.
type scriptedClient struct {
	responses []string
	usages    []Usage
	refuseAt  int // 1-based call index to refuse at (0 = never)
	call      int
	reqs      []Request
}

func (c *scriptedClient) Complete(_ context.Context, req Request) (Completion, error) {
	c.reqs = append(c.reqs, req)
	i := c.call
	c.call++
	u := Usage{InputTokens: 1000, OutputTokens: 500}
	if i < len(c.usages) {
		u = c.usages[i]
	}
	if c.refuseAt == i+1 {
		return Completion{Usage: u, Refused: true, Refusal: "declined for safety"}, nil
	}
	if i >= len(c.responses) {
		return Completion{Usage: u}, nil
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

// semanticInvalidSchema is STRUCTURALLY valid (every key/type is fine) but has a
// SEMANTIC error: a relation to a non-existent target. This is the class the
// re-architected loop is meant to correct (structure is decode-guaranteed).
const semanticInvalidSchema = `{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title": { "type": "string", "required": true },
        "owner_id": { "type": "uuid", "relation": "people" }
      }
    }
  }
}`

// emptyResourcesSchema is structurally valid and the engine accepts it, but it is
// useless — the guard must reject it and fall back, not "converge".
const emptyResourcesSchema = `{
  "$schema": "https://appitools.dev/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {}
}`

func TestGenerate_ValidFirstTry(t *testing.T) {
	client := &scriptedClient{responses: []string{validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Valid || !res.Converged || !res.FirstTry || res.Iterations != 1 {
		t.Fatalf("expected valid first-try in 1 iter, got %+v", res)
	}
	if !res.Structured {
		t.Errorf("expected structured=true by default")
	}
	if rep := schema.ValidateReport(res.Schema); !rep.Valid {
		t.Errorf("returned schema does not revalidate: %+v", rep.Errors)
	}
}

func TestGenerate_PassesOutputSchema(t *testing.T) {
	client := &scriptedClient{responses: []string{validSchema}}
	if _, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("expected 1 request")
	}
	if client.reqs[0].OutputSchema == nil {
		t.Errorf("expected structured outputs (OutputSchema) to be passed by default")
	}
	if client.reqs[0].System == "" {
		t.Errorf("expected the system prompt to be passed (for caching)")
	}
}

func TestGenerate_NoStructured(t *testing.T) {
	client := &scriptedClient{responses: []string{validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku, NoStructured: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if client.reqs[0].OutputSchema != nil {
		t.Errorf("expected NO OutputSchema with NoStructured")
	}
	if res.Structured {
		t.Errorf("expected Structured=false")
	}
}

func TestGenerate_CorrectsSemanticError(t *testing.T) {
	client := &scriptedClient{responses: []string{semanticInvalidSchema, validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Converged || res.FirstTry || res.Iterations != 2 {
		t.Fatalf("expected convergence after 1 correction, got %+v", res.RemainingErrors)
	}
	// The first attempt's errors must be SEMANTIC (the relation target), proving the
	// loop now handles the semantic class.
	a0 := res.Attempts[0]
	if a0.SemanticCount == 0 {
		t.Errorf("expected a semantic error on attempt 1, got struct=%d sem=%d", a0.StructuralCount, a0.SemanticCount)
	}
	// And the correction turn fed back the actionable report.
	last := client.reqs[1].Messages[len(client.reqs[1].Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "VALIDATION ERRORS") {
		t.Errorf("correction turn did not feed back the report: %q", last.Content)
	}
}

func TestGenerate_Refusal(t *testing.T) {
	client := &scriptedClient{responses: []string{validSchema}, refuseAt: 1}
	res, err := Generate(context.Background(), client, "something disallowed", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Refused {
		t.Fatalf("expected Refused")
	}
	if res.Valid || res.Converged {
		t.Errorf("a refusal is not a valid/converged schema")
	}
	if res.RefusalText == "" {
		t.Errorf("expected refusal text surfaced")
	}
	if res.Iterations != 1 {
		t.Errorf("refusal should stop immediately, got %d iters", res.Iterations)
	}
}

func TestGenerate_EmptyResourcesFallsBack(t *testing.T) {
	// Structured output yields empty resources (the silent-worsening case) → guard
	// drops structured and the next plain attempt produces a real schema.
	client := &scriptedClient{responses: []string{emptyResourcesSchema, validSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Converged {
		t.Fatalf("expected convergence after fallback")
	}
	if res.Structured {
		t.Errorf("expected structured disabled after empty-resources fallback")
	}
	// Second request must be plain (no OutputSchema).
	if client.reqs[1].OutputSchema != nil {
		t.Errorf("expected the post-fallback request to be plain")
	}
}

func TestGenerate_StructuredErrorFallsBack(t *testing.T) {
	// First call (structured) errors; the loop must retry the SAME iteration plain.
	client := &erroringStructuredClient{plain: validSchema}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !res.Converged {
		t.Fatalf("expected convergence via fallback")
	}
	if res.Structured {
		t.Errorf("expected structured disabled after the structured request errored")
	}
}

// erroringStructuredClient errors when given an OutputSchema, succeeds plain.
type erroringStructuredClient struct{ plain string }

func (c *erroringStructuredClient) Complete(_ context.Context, req Request) (Completion, error) {
	if req.OutputSchema != nil {
		return Completion{}, errStructuredRejected
	}
	return Completion{Text: c.plain, Usage: Usage{InputTokens: 100, OutputTokens: 50}}, nil
}

var errStructuredRejected = &structuredRejectedError{}

type structuredRejectedError struct{}

func (*structuredRejectedError) Error() string { return "structured output schema rejected" }

func TestGenerate_NeverConvergesReportsErrors(t *testing.T) {
	client := &scriptedClient{responses: []string{semanticInvalidSchema, semanticInvalidSchema, semanticInvalidSchema}}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku, MaxIterations: 3})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Converged || res.Valid {
		t.Fatalf("expected non-convergence")
	}
	if res.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", res.Iterations)
	}
	if len(res.RemainingErrors) == 0 {
		t.Errorf("expected remaining errors reported")
	}
}

func TestGenerate_DefaultIterationsIsThree(t *testing.T) {
	if DefaultMaxIterations != 3 {
		t.Errorf("AI-F1-S1 lowered the default to 3, got %d", DefaultMaxIterations)
	}
}

func TestGenerate_CostWithCacheTokens(t *testing.T) {
	client := &scriptedClient{
		responses: []string{semanticInvalidSchema, validSchema},
		usages: []Usage{
			{InputTokens: 2000, OutputTokens: 800, CacheCreationTokens: 1000},
			{InputTokens: 500, OutputTokens: 600, CacheReadTokens: 1000},
		},
	}
	res, err := Generate(context.Background(), client, "a todo app", Options{Model: ModelHaiku})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Haiku in=$1/MTok out=$5/MTok; cache write 1.25x in, cache read 0.1x in.
	// input: (2000+500)=2500 *1 = 2500
	// cache write: 1000*1.25 = 1250 ; cache read: 1000*0.1 = 100
	// output: (800+600)=1400 *5 = 7000
	// total/1e6 = (2500+1250+100+7000)/1e6 = 10850/1e6 = 0.01085
	want := 0.01085
	if res.CostUSD < want-1e-9 || res.CostUSD > want+1e-9 {
		t.Errorf("cost = %.6f, want %.6f", res.CostUSD, want)
	}
}

func TestOutputSchema_WithinStrictSubset(t *testing.T) {
	b, err := json.Marshal(OutputSchema())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	for _, bad := range disallowedInStrictSubset {
		if strings.Contains(s, "\""+bad+"\"") {
			t.Errorf("OutputSchema uses %q, which the strict-outputs subset does not support", bad)
		}
	}
	// Must close the top-level object (no unknown top-level keys) and pin the consts.
	if !strings.Contains(s, "\"additionalProperties\":false") {
		t.Errorf("OutputSchema must set additionalProperties:false at the root")
	}
	if !strings.Contains(s, SchemaURL) || !strings.Contains(s, "\"const\"") {
		t.Errorf("OutputSchema must pin $schema/version via const")
	}
}

func TestHasResources(t *testing.T) {
	if !HasResources([]byte(validSchema)) {
		t.Errorf("validSchema has resources")
	}
	if HasResources([]byte(emptyResourcesSchema)) {
		t.Errorf("emptyResourcesSchema has no resources")
	}
	if HasResources([]byte("not json")) {
		t.Errorf("garbage has no resources")
	}
}

func TestExtractJSON(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"prose_prefix", "Here is the schema:\n{\"a\":1}", `{"a":1}`},
		{"brace_in_string", `{"pattern":"^[a-z]{2,5}$"}`, `{"pattern":"^[a-z]{2,5}$"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSON(tc.in); got != tc.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPricingFor(t *testing.T) {
	for _, m := range []string{ModelHaiku, ModelSonnet, ModelOpus} {
		if _, ok := PricingFor(m); !ok {
			t.Errorf("expected pricing for %s", m)
		}
		if !SupportsStructuredOutput(m) {
			t.Errorf("expected %s to support structured outputs", m)
		}
	}
	if _, ok := PricingFor("nope"); ok {
		t.Errorf("unknown model should have no pricing")
	}
}
