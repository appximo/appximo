package eval

import (
	"context"
	"encoding/json"
	"hash/fnv"

	"github.com/miguelangel/appitools/pkg/aigen"
)

// Condition is one arm of the paired ablation — a named treatment plus the
// aigen.Options that realize it. New techniques (array-IR, constraint-aware, RAG)
// plug in as NEW conditions; the harness, the outcomes, and the statistics do not
// change. That genericity is the point: every future technique is measured against
// the baseline with McNemar on THIS domain, or discarded.
type Condition struct {
	Name    string
	Options aigen.Options
}

// BaselineConditions are the two arms available today: plain generation vs the
// AI-F1-S1 structured-envelope decoding. (Cochran's Q + Holm engage automatically
// once a third arm exists.)
func BaselineConditions() []Condition {
	return []Condition{
		{Name: "plain", Options: aigen.Options{NoStructured: true}},
		{Name: "structured", Options: aigen.Options{NoStructured: false}},
	}
}

// Outcome is one (case × condition) measurement — the paired observational unit.
type Outcome struct {
	CaseID      string  `json:"case_id"`
	Stratum     string  `json:"stratum"`
	Condition   string  `json:"condition"`
	FirstTry    bool    `json:"first_try"`    // valid at attempt 1 (the primary paired binary outcome)
	Converged   bool    `json:"converged"`    // valid within the iteration budget
	Iterations  int     `json:"iterations"`   // EMPIRICAL iterations to valid (not 1/p_sem)
	StructErrs0 int     `json:"struct_errs0"` // structural errors at attempt 1
	SemErrs0    int     `json:"sem_errs0"`    // semantic errors at attempt 1
	Refused     bool    `json:"refused"`
	InputTok    int     `json:"input_tokens"`
	OutputTok   int     `json:"output_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

// ClientFactory builds the ModelClient for a (case, condition). Live mode returns
// the real Anthropic client (temperature 0 for determinism); the demonstration
// mode returns a deterministic SimulatedClient. This is the seam that lets the
// harness run with NO API key (reproducible) and against a real model identically.
type ClientFactory func(c Case, cond Condition) aigen.ModelClient

// RunAblation evaluates every case under every condition, in deterministic order,
// and returns the paired outcomes. The same cases run under each condition, so the
// outcomes are PAIRED by case id — the structure McNemar/Cochran require.
func RunAblation(ctx context.Context, cases []Case, conds []Condition, factory ClientFactory, model string) ([]Outcome, error) {
	var out []Outcome
	for _, c := range cases {
		for _, cond := range conds {
			opts := cond.Options
			opts.Model = model
			client := factory(c, cond)
			res, err := aigen.Generate(ctx, client, c.Description, opts)
			if err != nil {
				return nil, err
			}
			o := Outcome{
				CaseID: c.ID, Stratum: c.Stratum, Condition: cond.Name,
				FirstTry: res.FirstTry, Converged: res.Converged, Iterations: res.Iterations,
				Refused:  res.Refused,
				InputTok: res.Usage.InputTokens, OutputTok: res.Usage.OutputTokens, CostUSD: res.CostUSD,
			}
			if len(res.Attempts) > 0 {
				o.StructErrs0 = res.Attempts[0].StructuralCount
				o.SemErrs0 = res.Attempts[0].SemanticCount
			}
			out = append(out, o)
		}
	}
	return out, nil
}

// ── deterministic demonstration driver ─────────────────────────────────────
//
// SimulatedClient is a deterministic ModelClient that lets the instrument run
// end-to-end WITHOUT an API key, for demonstrating + testing the harness. It is
// NOT a real measurement — real p_sem requires a real model (temperature 0). Its
// outcomes are a deterministic function of (case id, condition, attempt), modeling
// the documented mechanism: harder strata fail more often, correction makes later
// attempts more likely to succeed, and the `structured` condition CANNOT emit a
// structural fault (the AI-F1-S1 envelope), so it strictly dominates `plain` on
// the structural-error class. The report labels this mode SIMULATED, loudly.
type SimulatedClient struct {
	gold      json.RawMessage
	caseID    string
	condition string
	stratum   string
	plain     bool // condition emits structural faults (the envelope is off)
	attempt   int
}

// NewSimulatedClient builds the demonstration driver for one (case, condition).
func NewSimulatedClient(c Case, cond Condition) *SimulatedClient {
	return &SimulatedClient{
		gold: c.Gold, caseID: c.ID, condition: cond.Name, stratum: c.Stratum,
		plain: cond.Options.NoStructured,
	}
}

// semFailProb / structFailProb are the documented difficulty model. Semantic-fault
// probability is shared by both conditions; structural-fault probability applies
// only to `plain` (structured decoding removes the structural class). Both decay by
// half per attempt (the validator-guided correction shrinks the remaining failure).
func semFailProb(stratum string, attempt int) float64 {
	base := map[string]float64{"simple": 0.12, "media": 0.32, "compleja": 0.50}[stratum]
	return base * pow2(attempt-1)
}
func structFailProb(stratum string, attempt int) float64 {
	base := map[string]float64{"simple": 0.08, "media": 0.18, "compleja": 0.28}[stratum]
	return base * pow2(attempt-1)
}
func pow2(n int) float64 {
	v := 1.0
	for i := 0; i < n; i++ {
		v *= 0.5
	}
	return v
}

// unit hashes the tuple to a deterministic value in [0,1) — the source of
// reproducible "randomness" (no Math.random; the same tuple always maps the same).
func unit(parts ...string) float64 {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return float64(h.Sum64()) / float64(^uint64(0))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Complete returns the gold on a simulated success, or a fault-injected schema on
// a simulated failure (structural fault only when `plain`, else semantic). The
// real validator + loop then react to it — so the harness measures the real loop
// over the simulated model.
func (s *SimulatedClient) Complete(_ context.Context, req aigen.Request) (aigen.Completion, error) {
	s.attempt++
	usage := aigen.Usage{InputTokens: 1200, OutputTokens: 400}
	if s.attempt > 1 {
		// later rounds re-read the cached system prompt
		usage = aigen.Usage{InputTokens: 300, OutputTokens: 380, CacheReadTokens: 1100}
	}

	semFail := unit("sem", s.caseID, s.condition, itoa(s.attempt)) < semFailProb(s.stratum, s.attempt)
	structFail := s.plain && unit("struct", s.caseID, s.condition, itoa(s.attempt)) < structFailProb(s.stratum, s.attempt)

	switch {
	case structFail:
		return aigen.Completion{Text: string(injectStructuralFault(s.gold)), Usage: usage}, nil
	case semFail:
		return aigen.Completion{Text: string(injectSemanticFault(s.gold)), Usage: usage}, nil
	default:
		return aigen.Completion{Text: string(s.gold), Usage: usage}, nil
	}
}

// injectStructuralFault sets the (deterministically) first field's type to the
// invalid "number" — a metaschema (structural) error. injectSemanticFault adds a
// validly-named relation field whose target resource does not exist — a PURELY
// semantic (cross-reference) error (the field name is valid, so it adds no
// structural error). Both pick targets by SORTED key, so they are deterministic
// (reproducibility — map iteration order must never leak in). Tests assert each
// produces exactly the intended error source via schema.ValidateReport.
func injectStructuralFault(gold json.RawMessage) json.RawMessage {
	doc := decode(gold)
	if res := firstResource(doc); res != nil {
		if fields, ok := res["fields"].(map[string]any); ok {
			if k := firstKey(fields); k != "" {
				if f, ok := fields[k].(map[string]any); ok {
					f["type"] = "number"
				}
			}
		}
	}
	return encode(doc)
}

func injectSemanticFault(gold json.RawMessage) json.RawMessage {
	doc := decode(gold)
	if res := firstResource(doc); res != nil {
		if fields, ok := res["fields"].(map[string]any); ok {
			// valid name + a relation to a non-existent (but validly-named) target:
			// structurally fine, semantically broken (unknown_relation_target).
			fields["brokenref_id"] = map[string]any{"type": "uuid", "relation": "nope"}
		}
	}
	return encode(doc)
}

func decode(b json.RawMessage) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
func encode(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}

// firstResource returns the resource with the lexicographically smallest name
// (deterministic — never the random map order).
func firstResource(doc map[string]any) map[string]any {
	resources, ok := doc["resources"].(map[string]any)
	if !ok {
		return nil
	}
	k := firstKey(resources)
	if k == "" {
		return nil
	}
	r, _ := resources[k].(map[string]any)
	return r
}

func firstKey(m map[string]any) string {
	first := ""
	for k := range m {
		if first == "" || k < first {
			first = k
		}
	}
	return first
}
