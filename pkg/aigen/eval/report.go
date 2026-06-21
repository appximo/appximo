package eval

import (
	"fmt"
	"sort"
	"strings"
)

// CondStats are the per-condition (optionally per-stratum) figures: p_sem with a
// Wilson interval, and the EMPIRICAL iteration distribution (mean/median) — never
// the geometric 1/p_sem, which assumes i.i.d. retries the validator-guided loop is
// not. TheoIIDBound carries 1/p_sem ONLY as the labeled "independent retries"
// reference, for contrast.
type CondStats struct {
	Condition       string  `json:"condition"`
	Stratum         string  `json:"stratum,omitempty"` // "" = overall
	N               int     `json:"n"`
	FirstTrySuccess int     `json:"first_try_success"`
	Phat            float64 `json:"p_sem"`
	WilsonLo        float64 `json:"wilson_lo"`
	WilsonHi        float64 `json:"wilson_hi"`
	MeanIter        float64 `json:"mean_iter"`
	MedianIter      float64 `json:"median_iter"`
	NonConverged    int     `json:"non_converged"`
	TheoIIDBound    float64 `json:"theoretical_iid_bound"`
	MeanStructErr0  float64 `json:"mean_struct_err0"`
	MeanSemErr0     float64 `json:"mean_sem_err0"`
	MeanCostUSD     float64 `json:"mean_cost_usd"`
}

// PairTest is a paired McNemar comparison between two conditions (optionally within
// one stratum), with its Holm-adjusted p when part of a family.
type PairTest struct {
	A       string        `json:"a"`
	B       string        `json:"b"`
	Stratum string        `json:"stratum,omitempty"`
	Result  McNemarResult `json:"result"`
	HolmP   float64       `json:"holm_p,omitempty"`
}

// Analysis is the full statistical readout.
type Analysis struct {
	Mode         string          `json:"mode"` // "SIMULATED" | "LIVE"
	Model        string          `json:"model"`
	Counts       map[string]int  `json:"counts"`
	TotalCases   int             `json:"total_cases"`
	Conditions   []string        `json:"conditions"`
	PerCondition []CondStats     `json:"per_condition"`
	Pairwise     []PairTest      `json:"pairwise"`
	Cochran      *CochranQResult `json:"cochran_q,omitempty"`
	Note         string          `json:"note"`
}

// Analyze turns paired outcomes into the full statistical readout.
func Analyze(outcomes []Outcome, conds []Condition, mode, model string, cases []Case) Analysis {
	names := make([]string, len(conds))
	for i, c := range conds {
		names[i] = c.Name
	}
	a := Analysis{
		Mode: mode, Model: model, Conditions: names,
		Counts: CorpusCounts(cases), TotalCases: len(cases), Note: CorpusNote,
	}

	// Per condition: overall, then per stratum.
	for _, cn := range names {
		a.PerCondition = append(a.PerCondition, condStats(outcomes, cn, ""))
	}
	for _, st := range Strata {
		for _, cn := range names {
			cs := condStats(outcomes, cn, st)
			if cs.N > 0 {
				a.PerCondition = append(a.PerCondition, cs)
			}
		}
	}

	// Pairwise McNemar overall, Holm-adjusted across the family.
	byCondCase := index(outcomes)
	var overall []PairTest
	var ps []float64
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			b, c := discordants(byCondCase, names[i], names[j], "")
			r := McNemar(b, c)
			overall = append(overall, PairTest{A: names[i], B: names[j], Result: r})
			ps = append(ps, r.P)
		}
	}
	holm := HolmBonferroni(ps)
	for i := range overall {
		overall[i].HolmP = holm[i]
	}
	a.Pairwise = append(a.Pairwise, overall...)

	// Per-stratum McNemar for the canonical first-vs-last pair (where the effect is
	// expected to vary by complexity). Not Holm-bundled with the overall family.
	if len(names) >= 2 {
		first, last := names[0], names[len(names)-1]
		for _, st := range Strata {
			b, c := discordants(byCondCase, first, last, st)
			if b+c == 0 && countStratum(outcomes, st) == 0 {
				continue
			}
			a.Pairwise = append(a.Pairwise, PairTest{A: first, B: last, Stratum: st, Result: McNemar(b, c)})
		}
	}

	// Cochran's Q omnibus only with >=3 conditions (with 2 it reduces to McNemar).
	if len(names) >= 3 {
		rows := cochranRows(byCondCase, names, cases)
		q := CochranQ(rows)
		a.Cochran = &q
	}
	return a
}

func condStats(outcomes []Outcome, cond, stratum string) CondStats {
	cs := CondStats{Condition: cond, Stratum: stratum}
	var iters []int
	var sumStruct, sumSem, sumCost float64
	for _, o := range outcomes {
		if o.Condition != cond || (stratum != "" && o.Stratum != stratum) {
			continue
		}
		cs.N++
		if o.FirstTry {
			cs.FirstTrySuccess++
		}
		if o.Converged {
			iters = append(iters, o.Iterations)
		} else {
			cs.NonConverged++
		}
		sumStruct += float64(o.StructErrs0)
		sumSem += float64(o.SemErrs0)
		sumCost += o.CostUSD
	}
	if cs.N == 0 {
		return cs
	}
	cs.Phat, cs.WilsonLo, cs.WilsonHi = WilsonInterval(cs.FirstTrySuccess, cs.N, Z95)
	cs.MeanStructErr0 = sumStruct / float64(cs.N)
	cs.MeanSemErr0 = sumSem / float64(cs.N)
	cs.MeanCostUSD = sumCost / float64(cs.N)
	if len(iters) > 0 {
		cs.MeanIter, cs.MedianIter = meanMedian(iters)
	}
	if cs.Phat > 0 {
		cs.TheoIIDBound = 1 / cs.Phat
	}
	return cs
}

func meanMedian(xs []int) (mean, median float64) {
	sort.Ints(xs)
	var sum int
	for _, x := range xs {
		sum += x
	}
	mean = float64(sum) / float64(len(xs))
	n := len(xs)
	if n%2 == 1 {
		median = float64(xs[n/2])
	} else {
		median = float64(xs[n/2-1]+xs[n/2]) / 2
	}
	return
}

// index maps condition → case id → outcome (for paired lookups).
func index(outcomes []Outcome) map[string]map[string]Outcome {
	m := map[string]map[string]Outcome{}
	for _, o := range outcomes {
		if m[o.Condition] == nil {
			m[o.Condition] = map[string]Outcome{}
		}
		m[o.Condition][o.CaseID] = o
	}
	return m
}

// discordants returns (b, c): b = A first-try success & B failure; c = the reverse.
// Restricted to one stratum when stratum != "".
func discordants(idx map[string]map[string]Outcome, a, b, stratum string) (int, int) {
	var nb, nc int
	for id, oa := range idx[a] {
		ob, ok := idx[b][id]
		if !ok {
			continue
		}
		if stratum != "" && oa.Stratum != stratum {
			continue
		}
		switch {
		case oa.FirstTry && !ob.FirstTry:
			nb++
		case !oa.FirstTry && ob.FirstTry:
			nc++
		}
	}
	return nb, nc
}

func countStratum(outcomes []Outcome, st string) int {
	n := 0
	seen := map[string]bool{}
	for _, o := range outcomes {
		if o.Stratum == st && !seen[o.CaseID] {
			seen[o.CaseID] = true
			n++
		}
	}
	return n
}

// cochranRows builds the case × condition binary matrix (first-try outcome).
func cochranRows(idx map[string]map[string]Outcome, names []string, cases []Case) [][]bool {
	var rows [][]bool
	for _, c := range cases {
		row := make([]bool, len(names))
		complete := true
		for j, cn := range names {
			o, ok := idx[cn][c.ID]
			if !ok {
				complete = false
				break
			}
			row[j] = o.FirstTry
		}
		if complete {
			rows = append(rows, row)
		}
	}
	return rows
}

// Format renders the human report. It is explicit about statistical power: an
// underpowered McNemar (discordants < 25) is flagged INCONCLUSIVE, never sold as
// significant — the instrument does not lie about its own power.
func Format(a Analysis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "═══ AI schema-gen ablation — %s%s ═══\n", a.Mode, modelSuffix(a))
	fmt.Fprintf(&b, "corpus: %d cases  (simple=%d media=%d compleja=%d)\n",
		a.TotalCases, a.Counts["simple"], a.Counts["media"], a.Counts["compleja"])
	fmt.Fprintf(&b, "NOTE: %s\n\n", a.Note)

	fmt.Fprintln(&b, "── p_sem (first-try semantic success) + Wilson 95% CI + empirical E[iter] ──")
	fmt.Fprintf(&b, "%-12s %-9s %5s %7s %-18s %8s %8s %6s %s\n",
		"condition", "stratum", "n", "p_sem", "wilson95", "E[it]μ", "E[it]~", "nonconv", "struct/sem@1")
	for _, cs := range a.PerCondition {
		st := cs.Stratum
		if st == "" {
			st = "ALL"
		}
		fmt.Fprintf(&b, "%-12s %-9s %5d %7.3f [%.3f,%.3f] %8.2f %8.1f %6d   %.2f/%.2f\n",
			cs.Condition, st, cs.N, cs.Phat, cs.WilsonLo, cs.WilsonHi,
			cs.MeanIter, cs.MedianIter, cs.NonConverged, cs.MeanStructErr0, cs.MeanSemErr0)
	}
	fmt.Fprintf(&b, "\n(E[it]μ = empirical mean iterations to valid; E[it]~ = median. The geometric\n"+
		" 1/p_sem is NOT used — the validator-guided loop is not i.i.d.; 1/p_sem is only a\n"+
		" theoretical 'independent retries' bound: ")
	for _, cs := range a.PerCondition {
		if cs.Stratum == "" && cs.TheoIIDBound > 0 {
			fmt.Fprintf(&b, "%s=%.2f ", cs.Condition, cs.TheoIIDBound)
		}
	}
	fmt.Fprintf(&b, ")\n\n")

	fmt.Fprintln(&b, "── paired significance — McNemar (exact <25 discordants, else Edwards χ²) ──")
	for _, pt := range a.Pairwise {
		scope := "overall"
		if pt.Stratum != "" {
			scope = "stratum=" + pt.Stratum
		}
		verdict := significanceVerdict(pt)
		holm := ""
		if pt.Stratum == "" {
			holm = fmt.Sprintf(" holm_p=%.4f", pt.HolmP)
		}
		fmt.Fprintf(&b, "  %s vs %s [%s]: b=%d c=%d (discordant=%d) %s p=%.4f%s → %s\n",
			pt.A, pt.B, scope, pt.Result.B, pt.Result.C, pt.Result.Discordant,
			pt.Result.Method, pt.Result.P, holm, verdict)
	}

	if a.Cochran != nil {
		fmt.Fprintf(&b, "\n── Cochran's Q omnibus (k=%d conditions, n=%d) ──\n", a.Cochran.K, a.Cochran.N)
		fmt.Fprintf(&b, "  Q=%.4f df=%d p=%.4f\n", a.Cochran.Q, a.Cochran.DF, a.Cochran.P)
	}
	return b.String()
}

func modelSuffix(a Analysis) string {
	if a.Model == "" {
		return ""
	}
	return " (" + a.Model + ")"
}

// significanceVerdict never declares significance on an underpowered comparison.
func significanceVerdict(pt PairTest) string {
	if pt.Result.Method == "no-discordants" {
		return "INCONCLUSIVE (no discordant pairs — zero power)"
	}
	if pt.Result.Underpowered() {
		p := pt.Result.P
		if pt.Stratum == "" {
			p = pt.HolmP
		}
		if p < 0.05 {
			return fmt.Sprintf("nominally p<0.05 but UNDERPOWERED (only %d discordants <25; not conclusive — need more n)", pt.Result.Discordant)
		}
		return fmt.Sprintf("not significant + UNDERPOWERED (%d discordants <25 — need more n)", pt.Result.Discordant)
	}
	p := pt.Result.P
	if pt.Stratum == "" {
		p = pt.HolmP
	}
	if p < 0.05 {
		return "SIGNIFICANT (Holm-adjusted p<0.05)"
	}
	return "not significant"
}
