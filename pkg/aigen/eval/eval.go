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

// BaselineConditions are the three arms: plain generation, the AI-F1-S1
// structured-ENVELOPE decoding, and the AI-F2-S2 array-IR (structured DEEP)
// decoding. With three arms Cochran's Q + Holm engage automatically; the canonical
// per-stratum first-vs-last pair becomes plain vs array-IR (the full effect).
func BaselineConditions() []Condition {
	return []Condition{
		{Name: "plain", Options: aigen.Options{NoStructured: true}},
		{Name: "structured", Options: aigen.Options{NoStructured: false}},
		{Name: "array-IR", Options: aigen.Options{ArrayIR: true}},
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
// the documented mechanism with a FAITHFUL structural-depth split so the three arms
// genuinely differ (the point of measuring array-IR):
//
//   - `plain` can emit BOTH a shallow ENVELOPE structural fault (unknown top-level
//     key, bad const) AND a DEEP structural fault (a field's type outside the set).
//   - `structured` (AI-F1-S1 envelope) removes the ENVELOPE class but can STILL emit
//     a DEEP structural fault — the strict subset cannot reach the map-keyed depth.
//   - `array-IR` (AI-F2-S2) removes BOTH classes by construction (an array of fixed
//     items constrains the depth too), leaving only the SEMANTIC, cross-reference
//     class — the hypothesis the harness measures: deep p_struct→1.
//
// The semantic-fault probability is shared by all arms (no decoder constrains
// cross-reference semantics). The report labels this mode SIMULATED, loudly.
type SimulatedClient struct {
	gold      json.RawMessage
	caseID    string
	condition string
	stratum   string
	arrayIR   bool // emit the array-IR form (the loop transforms IR→map)
	envelope  bool // this arm can emit a shallow envelope structural fault (plain only)
	deep      bool // this arm can emit a deep structural fault (plain + structured)
	attempt   int
}

// NewSimulatedClient builds the demonstration driver for one (case, condition),
// deriving each arm's structural coverage from its Options: plain emits both fault
// depths, structured removes only the envelope, array-IR removes both.
func NewSimulatedClient(c Case, cond Condition) *SimulatedClient {
	plain := cond.Options.NoStructured
	arrayIR := cond.Options.ArrayIR
	return &SimulatedClient{
		gold: c.Gold, caseID: c.ID, condition: cond.Name, stratum: c.Stratum,
		arrayIR:  arrayIR,
		envelope: plain,    // only plain can fail the envelope
		deep:     !arrayIR, // plain + structured can fail the deep structure; IR cannot
	}
}

// The documented difficulty model: each fault class has a per-stratum base rate that
// decays by half per attempt (the validator-guided correction shrinks the remaining
// failure each round). semFailProb is the cross-reference (semantic) class shared by
// every arm; envelopeFailProb is the shallow structural class (top-level); deepFailProb
// is the deep structural class (field types / strict keys) — the class the array-IR
// removes that the envelope cannot.
func semFailProb(stratum string, attempt int) float64 {
	base := map[string]float64{"simple": 0.12, "media": 0.32, "compleja": 0.50}[stratum]
	return base * pow2(attempt-1)
}
func deepFailProb(stratum string, attempt int) float64 {
	base := map[string]float64{"simple": 0.08, "media": 0.18, "compleja": 0.28}[stratum]
	return base * pow2(attempt-1)
}
func envelopeFailProb(stratum string, attempt int) float64 {
	base := map[string]float64{"simple": 0.05, "media": 0.10, "compleja": 0.15}[stratum]
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

// Complete returns the (faithfully fault-injected) schema for this attempt. In the
// array-IR arm it emits the IR FORM (the loop transforms IR→map); the other arms
// emit the map form. Each fault class fires independently per the arm's coverage,
// so the validator + the real loop react to exactly what a model of that capability
// would produce — the harness measures the real loop over the simulated model.
func (s *SimulatedClient) Complete(_ context.Context, req aigen.Request) (aigen.Completion, error) {
	s.attempt++
	usage := aigen.Usage{InputTokens: 1200, OutputTokens: 400}
	if s.attempt > 1 {
		// later rounds re-read the cached system prompt
		usage = aigen.Usage{InputTokens: 300, OutputTokens: 380, CacheReadTokens: 1100}
	}

	semFail := unit("sem", s.caseID, s.condition, itoa(s.attempt)) < semFailProb(s.stratum, s.attempt)
	deepFail := s.deep && unit("deep", s.caseID, s.condition, itoa(s.attempt)) < deepFailProb(s.stratum, s.attempt)
	envFail := s.envelope && unit("env", s.caseID, s.condition, itoa(s.attempt)) < envelopeFailProb(s.stratum, s.attempt)

	// Array-IR arm: emit the IR form. Only semantic faults can occur (the IR removes
	// both structural classes by construction); the loop transforms IR→map.
	if s.arrayIR {
		ir := aigen.MapToIR(decode(s.gold))
		if semFail {
			ir = injectSemanticFaultIR(ir)
		}
		return aigen.Completion{Text: string(encode(ir)), Usage: usage}, nil
	}

	// Map-form arms: apply every fault that fired (they touch disjoint parts), so the
	// validator reports each — plain accumulates envelope+deep+sem, structured deep+sem.
	doc := decode(s.gold)
	if envFail {
		injectEnvelopeFault(doc)
	}
	if deepFail {
		injectDeepFault(doc)
	}
	if semFail {
		injectSemanticFault(doc)
	}
	return aigen.Completion{Text: string(encode(doc)), Usage: usage}, nil
}

// injectEnvelopeFault adds an unknown TOP-LEVEL key — a shallow (envelope) metaschema
// (structural) error: the class the structured-envelope decoding (AI-F1-S1) removes.
func injectEnvelopeFault(doc map[string]any) {
	doc["extra_top_key"] = 1
}

// injectDeepFault sets the (deterministically) first field's type to the invalid
// "number" — a DEEP metaschema (structural) error inside the map-keyed structure:
// the class the envelope CANNOT reach but the array-IR (AI-F2-S2) removes.
func injectDeepFault(doc map[string]any) {
	if res := firstResource(doc); res != nil {
		if fields, ok := res["fields"].(map[string]any); ok {
			if k := firstKey(fields); k != "" {
				if f, ok := fields[k].(map[string]any); ok {
					f["type"] = "number"
				}
			}
		}
	}
}

// injectStructuralFault is the legacy single-fault entry point kept for the
// faithfulness test (it must still produce a metaschema error). It is the deep fault.
func injectStructuralFault(gold json.RawMessage) json.RawMessage {
	doc := decode(gold)
	injectDeepFault(doc)
	return encode(doc)
}

// injectSemanticFault adds a validly-named relation field whose target resource does
// not exist — a PURELY semantic (cross-reference) error (the field name is valid, so
// it adds no structural error). Deterministic (targets the SORTED-first resource).
func injectSemanticFault(doc map[string]any) {
	if res := firstResource(doc); res != nil {
		if fields, ok := res["fields"].(map[string]any); ok {
			fields["brokenref_id"] = map[string]any{"type": "uuid", "relation": "nope"}
		}
	}
}

// injectSemanticFaultIR injects the SAME semantic fault into the IR form: it appends
// a broken-relation field object to the sorted-first resource's fields ARRAY. After
// the loop's IR→map transform this is the identical unknown_relation_target error.
func injectSemanticFaultIR(ir map[string]any) map[string]any {
	resources, ok := ir["resources"].([]any)
	if !ok || len(resources) == 0 {
		return ir
	}
	res, ok := resources[0].(map[string]any)
	if !ok {
		return ir
	}
	fields, _ := res["fields"].([]any)
	res["fields"] = append(fields, map[string]any{
		"name": "brokenref_id", "type": "uuid", "relation": "nope",
	})
	return ir
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
