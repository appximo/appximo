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
	// The structural fault must produce a metaschema (structural) error.
	rep := schema.ValidateReport(injectStructuralFault(gold))
	if rep.Valid {
		t.Fatal("structural fault did not break the schema")
	}
	if !hasSource(rep.Errors, "metaschema") {
		t.Errorf("structural fault should yield a metaschema error, got %+v", rep.Errors)
	}
	// The semantic fault must produce a semantic error.
	rep = schema.ValidateReport(injectSemanticFault(gold))
	if rep.Valid {
		t.Fatal("semantic fault did not break the schema")
	}
	if !hasSource(rep.Errors, "semantic") {
		t.Errorf("semantic fault should yield a semantic error, got %+v", rep.Errors)
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

func TestStructuredRemovesStructuralClass(t *testing.T) {
	cases, _ := LoadCorpus()
	conds := BaselineConditions()
	outcomes, err := RunAblation(context.Background(), cases, conds, simFactory, "claude-haiku-4-5")
	if err != nil {
		t.Fatalf("RunAblation: %v", err)
	}
	a := Analyze(outcomes, conds, "SIMULATED", "claude-haiku-4-5", cases)
	// The structured arm must show ZERO mean structural errors at attempt 1 overall
	// (the simulator never injects a structural fault under structured — the
	// AI-F1-S1 envelope) while plain shows some.
	var structuredOverall, plainOverall CondStats
	for _, cs := range a.PerCondition {
		if cs.Stratum != "" {
			continue
		}
		switch cs.Condition {
		case "structured":
			structuredOverall = cs
		case "plain":
			plainOverall = cs
		}
	}
	if structuredOverall.MeanStructErr0 != 0 {
		t.Errorf("structured arm should have 0 structural errors at attempt 1, got %.3f", structuredOverall.MeanStructErr0)
	}
	if plainOverall.MeanStructErr0 <= 0 {
		t.Errorf("plain arm should show some structural errors at attempt 1, got %.3f", plainOverall.MeanStructErr0)
	}
	// Empirical E[iter] must be populated (and the report must not be using 1/p_sem
	// as the iteration estimate — they differ).
	if structuredOverall.MeanIter <= 0 {
		t.Errorf("expected empirical mean iterations > 0")
	}
}
