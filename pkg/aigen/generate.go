package aigen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/miguelangel/appitools/pkg/schema"
)

// DefaultMaxIterations bounds the generate→validate→correct loop. If the model
// has not produced a valid schema within this many rounds, the loop stops and
// returns the last attempt + its remaining errors (never an infinite loop).
const DefaultMaxIterations = 5

// Options configures a Generate run.
type Options struct {
	// MaxIterations caps the correction rounds (default DefaultMaxIterations).
	MaxIterations int
	// Model is informational only — used to price the run. The actual model is
	// whatever the ModelClient targets; set this to the same id for an accurate
	// cost figure (the AnthropicClient.Model() is the right value).
	Model string
}

// Attempt records one round of the loop: the raw model output, whether it
// validated, the errors if not, and the tokens that round consumed.
type Attempt struct {
	Iteration int                      `json:"iteration"`
	Raw       string                   `json:"-"` // the raw model text (kept out of JSON to stay lean)
	Valid     bool                     `json:"valid"`
	Errors    []schema.StructuredError `json:"errors,omitempty"`
	Usage     Usage                    `json:"usage"`
}

// Result is the outcome of a Generate run — the artifact plus the FULL economic
// instrumentation (iterations, cumulative tokens, approximate cost) that is the
// data point validating the democratization thesis.
type Result struct {
	// Schema is the final schema bytes (valid when Valid is true; otherwise the
	// best/last attempt). Pretty-printed JSON.
	Schema json.RawMessage `json:"schema"`
	// Valid reports whether the final schema passed BOTH validators.
	Valid bool `json:"valid"`
	// Converged is true when the loop reached a valid schema within the budget.
	Converged bool `json:"converged"`
	// FirstTry is true when the very first generation was already valid (no
	// correction needed) — the headline metric for "the cheap model is enough".
	FirstTry bool `json:"first_try"`
	// Iterations is how many model calls it took (1 = valid first try).
	Iterations int `json:"iterations"`
	// Attempts is the per-round detail.
	Attempts []Attempt `json:"attempts"`
	// Usage is the cumulative token usage across all iterations.
	Usage Usage `json:"usage"`
	// Model is the model id used (for the cost line).
	Model string `json:"model"`
	// CostUSD is the approximate dollar cost of the whole run.
	CostUSD float64 `json:"cost_usd"`
	// RemainingErrors are the validation errors of the final schema when the loop
	// did NOT converge (empty on success).
	RemainingErrors []schema.StructuredError `json:"remaining_errors,omitempty"`
}

// Generate runs the full loop: describe → generate → validate → correct → repeat
// until valid or the iteration budget is exhausted. It is pure orchestration over
// the injected ModelClient and the engine's own schema.ValidateReport — so the
// SAME loop is exercised by the live CLI and by the deterministic unit tests.
func Generate(ctx context.Context, client ModelClient, description string, opts Options) (*Result, error) {
	if strings.TrimSpace(description) == "" {
		return nil, fmt.Errorf("aigen: description is empty")
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}

	res := &Result{Model: opts.Model}

	// The conversation: a fixed system prompt + a growing user/assistant thread.
	// Keeping the prior attempt and its errors in-context is what lets the model
	// CORRECT rather than regenerate blind.
	messages := []Message{
		{Role: "user", Content: "Generate an Appitools schema for this app:\n\n" + description},
	}

	for i := 1; i <= maxIter; i++ {
		comp, err := client.Complete(ctx, systemPrompt, messages)
		if err != nil {
			return nil, fmt.Errorf("aigen: iteration %d: %w", i, err)
		}
		res.Usage.Add(comp.Usage)

		raw := extractJSON(comp.Text)
		report := schema.ValidateReport([]byte(raw))

		attempt := Attempt{
			Iteration: i,
			Raw:       raw,
			Valid:     report.Valid,
			Errors:    report.Errors,
			Usage:     comp.Usage,
		}
		res.Attempts = append(res.Attempts, attempt)
		res.Iterations = i

		if report.Valid {
			res.Schema = prettyJSON(raw)
			res.Valid = true
			res.Converged = true
			res.FirstTry = (i == 1)
			res.CostUSD = res.Usage.CostUSD(res.Model)
			return res, nil
		}

		// Record the assistant's (invalid) attempt and feed back the actionable
		// errors so the next round corrects in-context.
		messages = append(messages, Message{Role: "assistant", Content: comp.Text})
		messages = append(messages, Message{Role: "user", Content: correctionPreamble + formatErrors(report.Errors)})

		// On the final iteration we keep the last attempt for the report.
		if i == maxIter {
			res.Schema = prettyJSON(raw)
			res.RemainingErrors = report.Errors
		}
	}

	res.CostUSD = res.Usage.CostUSD(res.Model)
	return res, nil
}

// formatErrors renders the structured errors as the JSON the model is told to
// correct from (the AI-F0-S2 report shape). Compact but complete.
func formatErrors(errs []schema.StructuredError) string {
	b, err := json.MarshalIndent(errs, "", "  ")
	if err != nil {
		// Fallback: never block the loop on a marshal error.
		var sb strings.Builder
		for _, e := range errs {
			fmt.Fprintf(&sb, "- %s: %s (fix: %s)\n", e.Path, e.Message, e.Fix)
		}
		return sb.String()
	}
	return string(b)
}

// extractJSON pulls the schema JSON out of a model response. Models sometimes
// wrap the object in ```json fences or add a stray sentence despite instructions;
// this strips fences and, failing that, extracts the first balanced top-level
// object. If nothing object-like is found it returns the trimmed input (so the
// validator reports a clean "invalid JSON" the loop can still act on).
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	// Strip a leading ```json / ``` fence and its closing fence, if present.
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	// Fast path: already a clean object.
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return s
	}

	// Otherwise extract the first balanced {...}, respecting strings/escapes so a
	// brace inside a string value (e.g. a regex pattern) doesn't end it early.
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return s
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
			// inside a string — ignore braces
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:] // unbalanced — let the validator surface it
}

// prettyJSON re-indents valid JSON for a readable artifact; on any parse issue it
// returns the input unchanged (the loop only calls this on validated bytes).
func prettyJSON(s string) json.RawMessage {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return json.RawMessage(s)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return json.RawMessage(s)
	}
	return json.RawMessage(b)
}
