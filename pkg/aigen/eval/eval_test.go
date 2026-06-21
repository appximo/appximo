package eval

import (
	"context"
	"reflect"
	"testing"

	"github.com/miguelangel/appitools/pkg/aigen"
	"github.com/miguelangel/appitools/pkg/schema"
)

func TestAllGoldsValidate(t *testing.T) {
	cases, err := LoadCorpus()
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("empty corpus")
	}
	for _, c := range cases {
		rep := schema.ValidateReport(c.Gold)
		if !rep.Valid {
			t.Errorf("gold %s/%s does NOT validate: %+v", c.Stratum, c.ID, rep.Errors)
		}
		if c.Description == "" {
			t.Errorf("case %s has empty description", c.ID)
		}
	}
}

func TestCorpusStratified(t *testing.T) {
	cases, _ := LoadCorpus()
	counts := CorpusCounts(cases)
	for _, st := range Strata {
		if counts[st] == 0 {
			t.Errorf("stratum %s is empty", st)
		}
	}
	t.Logf("corpus: %v (target %d/stratum for full power)", counts, TargetPerStratum)
}

func TestSimulatedFaultsAreFaithful(t *testing.T) {
	cases, _ := LoadCorpus()
	gold := cases[0].Gold

	// The DEEP structural fault must produce a metaschema (structural) error.
	rep := schema.ValidateReport(injectStructuralFault(gold))
	if rep.Valid || !hasSource(rep.Errors, "metaschema") {
		t.Errorf("deep structural fault should yield a metaschema error, got %+v", rep.Errors)
	}

	// The ENVELOPE fault (unknown top-level key) must also be a metaschema error.
	envDoc := decode(gold)
	injectEnvelopeFault(envDoc)
	rep = schema.ValidateReport(encode(envDoc))
	if rep.Valid || !hasSource(rep.Errors, "metaschema") {
		t.Errorf("envelope fault should yield a metaschema error, got %+v", rep.Errors)
	}

	// The semantic fault (map form) must produce a semantic error and NO structural one.
	semDoc := decode(gold)
	injectSemanticFault(semDoc)
	rep = schema.ValidateReport(encode(semDoc))
	if rep.Valid || !hasSource(rep.Errors, "semantic") {
		t.Errorf("semantic fault should yield a semantic error, got %+v", rep.Errors)
	}
	if hasSource(rep.Errors, "metaschema") {
		t.Errorf("semantic fault must be PURELY semantic (no structural), got %+v", rep.Errors)
	}

	// The IR semantic fault, once transformed back to the map form, is the SAME
	// semantic error (proving the IR arm exercises the real loop end-to-end).
	ir := aigen.MapToIR(decode(gold))
	ir = injectSemanticFaultIR(ir)
	mapBack := aigen.IRToMap(ir)
	rep = schema.ValidateReport(encode(mapBack))
	if rep.Valid || !hasSource(rep.Errors, "semantic") {
		t.Errorf("IR semantic fault should yield a semantic error after IR→map, got %+v", rep.Errors)
	}
}

func hasSource(errs []schema.StructuredError, src string) bool {
	for _, e := range errs {
		if e.Source == src {
			return true
		}
	}
	return false
}

func simFactory(c Case, cond Condition) aigen.ModelClient { return NewSimulatedClient(c, cond) }

func TestRunAblationDeterministic(t *testing.T) {
	cases, _ := LoadCorpus()
	conds := BaselineConditions()
	run := func() []Outcome {
		o, err := RunAblation(context.Background(), cases, conds, simFactory, "claude-haiku-4-5")
		if err != nil {
			t.Fatalf("RunAblation: %v", err)
		}
		return o
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatal("ablation is not deterministic (same corpus + simulator twice differ)")
	}
	if len(a) != len(cases)*len(conds) {
		t.Errorf("expected %d outcomes, got %d", len(cases)*len(conds), len(a))
	}
}

// TestArmsStructuralCoverage is the AI-F2-S2 hypothesis encoded as a deterministic
// assertion over the simulated ablation: the array-IR arm shows ZERO structural
// errors at attempt 1 (deep p_struct→1 — the deep structure guaranteed by
// construction), the structured-envelope arm still shows SOME (the deep class the
// envelope cannot reach), and plain shows the MOST (envelope + deep).
func TestArmsStructuralCoverage(t *testing.T) {
	cases, _ := LoadCorpus()
	conds := BaselineConditions()
	outcomes, err := RunAblation(context.Background(), cases, conds, simFactory, "claude-haiku-4-5")
	if err != nil {
		t.Fatalf("RunAblation: %v", err)
	}
	a := Analyze(outcomes, conds, "SIMULATED", "claude-haiku-4-5", cases)
	overall := map[string]CondStats{}
	for _, cs := range a.PerCondition {
		if cs.Stratum == "" {
			overall[cs.Condition] = cs
		}
	}
	ir, structured, plain := overall["array-IR"], overall["structured"], overall["plain"]

	// The core claim — array-IR removes the WHOLE structural class in depth.
	if ir.MeanStructErr0 != 0 {
		t.Errorf("array-IR must have 0 structural errors at attempt 1 (deep p_struct→1), got %.3f", ir.MeanStructErr0)
	}
	// The envelope still leaks the deep structural class.
	if structured.MeanStructErr0 <= 0 {
		t.Errorf("structured (envelope) should still show deep structural errors, got %.3f", structured.MeanStructErr0)
	}
	// Plain shows the most (envelope + deep stacked).
	if plain.MeanStructErr0 < structured.MeanStructErr0 {
		t.Errorf("plain should show >= structured structural errors (envelope+deep), got plain=%.3f structured=%.3f",
			plain.MeanStructErr0, structured.MeanStructErr0)
	}
	// Empirical E[iter] populated (the report does not substitute 1/p_sem).
	if ir.MeanIter <= 0 {
		t.Errorf("expected empirical mean iterations > 0")
	}
	// Three arms ⇒ Cochran's Q omnibus must be computed.
	if a.Cochran == nil {
		t.Errorf("expected Cochran's Q with 3 arms")
	}
	t.Logf("struct@1 means: plain=%.3f structured=%.3f array-IR=%.3f", plain.MeanStructErr0, structured.MeanStructErr0, ir.MeanStructErr0)
	t.Logf("p_sem (first-try): plain=%.3f structured=%.3f array-IR=%.3f", plain.Phat, structured.Phat, ir.Phat)
}
