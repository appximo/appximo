package aigen

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/miguelangel/appitools/pkg/schema"
)

// DefaultMaxIterations bounds the generate→validate→correct loop. AI-F1-S1 lowered
// it from 5 to 3: with the structural envelope guaranteed by constrained decoding,
// the loop only spends rounds on SEMANTIC corrections, which converge faster — so
// the budget no longer needs to absorb structural-mistake rounds.
const DefaultMaxIterations = 3

// Options configures a Generate run.
type Options struct {
	// MaxIterations caps the correction rounds (default DefaultMaxIterations).
	MaxIterations int
	// Model prices the run (set to the ModelClient's model id for an accurate cost).
	Model string
	// NoStructured disables structured outputs (forces plain generation). Default
	// (false) uses structured outputs when the client/model supports it. The
	// generator also falls back to plain generation automatically on a structured
	// error or an empty-resources result — this flag forces it off from the start.
	NoStructured bool
}

// Attempt records one round of the loop: whether it validated, the errors split
// by layer (structural vs semantic — the metric that shows the envelope removed
// the structural class), and the tokens that round consumed.
type Attempt struct {
	Iteration       int                      `json:"iteration"`
	Raw             string                   `json:"-"`
	Valid           bool                     `json:"valid"`
	Structured      bool                     `json:"structured"`
	StructuralCount int                      `json:"structural_errors"`
	SemanticCount   int                      `json:"semantic_errors"`
	Errors          []schema.StructuredError `json:"errors,omitempty"`
	Usage           Usage                    `json:"usage"`
}

// Result is the outcome of a Generate run — the artifact plus the economic
// instrumentation that validates the democratization thesis.
type Result struct {
	Schema     json.RawMessage `json:"schema"`
	Valid      bool            `json:"valid"`
	Converged  bool            `json:"converged"`
	FirstTry   bool            `json:"first_try"`
	Iterations int             `json:"iterations"`
	// Structured reports whether the final/converged generation used structured
	// outputs (false when it fell back to plain generation).
	Structured bool      `json:"structured"`
	Attempts   []Attempt `json:"attempts"`
	Usage      Usage     `json:"usage"`
	Model      string    `json:"model"`
	CostUSD    float64   `json:"cost_usd"`
	// Refused is set when the model declined for safety — not a schema failure.
	Refused     bool   `json:"refused,omitempty"`
	RefusalText string `json:"refusal_text,omitempty"`
	// RemainingErrors are the final schema's validation errors when it did not
	// converge (empty on success).
	RemainingErrors []schema.StructuredError `json:"remaining_errors,omitempty"`
}

// Generate runs the re-architected loop: structured generation (envelope
// guaranteed by decoding) → schema.ValidateReport (the trusted oracle, full
// structural+semantic as defense-in-depth) → feed back the remaining errors →
// repeat until valid or the budget is exhausted. It is pure orchestration over
// the injected ModelClient and the engine's own validator.
func Generate(ctx context.Context, client ModelClient, description string, opts Options) (*Result, error) {
	if strings.TrimSpace(description) == "" {
		return nil, fmt.Errorf("aigen: description is empty")
	}
	maxIter := opts.MaxIterations
	if maxIter <= 0 {
		maxIter = DefaultMaxIterations
	}

	res := &Result{Model: opts.Model}

	// Structured outputs on by default; the per-model capability is checked by the
	// caller (it passes a client for a supporting model). NoStructured forces off.
	useStructured := !opts.NoStructured
	var outSchema map[string]any
	if useStructured {
		outSchema = OutputSchema()
	}

	messages := []Message{
		{Role: "user", Content: "Generate an Appitools schema for this app:\n\n" + description},
	}

	for i := 1; i <= maxIter; i++ {
		req := Request{System: systemPrompt, Messages: messages}
		if useStructured {
			req.OutputSchema = outSchema
		}

		comp, err := client.Complete(ctx, req)
		if err != nil {
			// A structured request can be rejected (e.g. the live subset rejects the
			// schema). Degrade gracefully to plain generation rather than aborting —
			// never worse than the unstructured loop.
			if useStructured {
				useStructured, outSchema = false, nil
				req.OutputSchema = nil
				comp, err = client.Complete(ctx, req)
			}
			if err != nil {
				return nil, fmt.Errorf("aigen: iteration %d: %w", i, err)
			}
		}
		res.Usage.Add(comp.Usage)

		// Safety refusal: stop cleanly — not a schema error, not correctable.
		if comp.Refused {
			res.Refused = true
			res.RefusalText = comp.Refusal
			res.Iterations = i
			res.CostUSD = res.Usage.CostUSD(res.Model)
			return res, nil
		}

		raw := extractJSON(comp.Text)

		// Empty-resources guard: structured outputs forcing an empty resources map
		// would "validate" (the engine accepts zero resources) and the loop would
		// wrongly converge. If that happens, drop structured and regenerate plainly.
		if useStructured && !HasResources([]byte(raw)) {
			useStructured, outSchema = false, nil
			res.Iterations = i
			res.Attempts = append(res.Attempts, Attempt{
				Iteration: i, Structured: true, Valid: false, Usage: comp.Usage,
				Errors: []schema.StructuredError{{Path: "resources", Rule: "empty_resources",
					Message: "structured output produced no resources; falling back to plain generation"}},
				StructuralCount: 1,
			})
			continue
		}

		report := schema.ValidateReport([]byte(raw))
		nStruct, nSem := countBySource(report.Errors)
		res.Attempts = append(res.Attempts, Attempt{
			Iteration:       i,
			Raw:             raw,
			Valid:           report.Valid,
			Structured:      useStructured,
			StructuralCount: nStruct,
			SemanticCount:   nSem,
			Errors:          report.Errors,
			Usage:           comp.Usage,
		})
		res.Iterations = i

		if report.Valid {
			res.Schema = prettyJSON(raw)
			res.Valid = true
			res.Converged = true
			res.FirstTry = (i == 1)
			res.Structured = useStructured
			res.CostUSD = res.Usage.CostUSD(res.Model)
			return res, nil
		}

		// Feed back the actionable errors so the next round corrects in-context.
		messages = append(messages, Message{Role: "assistant", Content: comp.Text})
		messages = append(messages, Message{Role: "user", Content: correctionPreamble + formatErrors(report.Errors)})

		if i == maxIter {
			res.Schema = prettyJSON(raw)
			res.RemainingErrors = report.Errors
			res.Structured = useStructured
		}
	}

	res.CostUSD = res.Usage.CostUSD(res.Model)
	return res, nil
}

// countBySource splits a report's errors into structural (metaschema) vs semantic
// counts — the measurement that shows constrained decoding removed the structural
// class (structural errors should trend to zero once the envelope is guaranteed).
func countBySource(errs []schema.StructuredError) (structural, semantic int) {
	for _, e := range errs {
		if e.Source == "semantic" {
			semantic++
		} else {
			structural++
		}
	}
	return
}

// formatErrors renders the structured errors as the JSON the model is told to
// correct from (the AI-F0-S2 report shape). Compact but complete.
func formatErrors(errs []schema.StructuredError) string {
	b, err := json.MarshalIndent(errs, "", "  ")
	if err != nil {
		var sb strings.Builder
		for _, e := range errs {
			fmt.Fprintf(&sb, "- %s: %s (fix: %s)\n", e.Path, e.Message, e.Fix)
		}
		return sb.String()
	}
	return string(b)
}

// extractJSON pulls the schema JSON out of a model response. With structured
// outputs the response is already a clean object, but this stays as defense for
// the plain-generation fallback (fences / stray prose). It strips fences and, if
// needed, extracts the first balanced top-level object.
func extractJSON(s string) string {
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return s
	}

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
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
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
