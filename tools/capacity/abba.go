package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/appximo/appximo/tools/devhub/stats"
)

// The frozen ABBA verdict, on the laboratory's own runs.
//
// The house rule (docs/BENCHMARKS.md §7) is that a change is a change only if
// it is BOTH statistically significant (Mann-Whitney U, p < 0.05) and
// practically material (the median moved more than max(0.5 ms, 3 %)) —
// everything else is `no_change`. Two additions this session inherits:
//
//   - OPS-34: base and new must be built the SAME WAY, in the same session and
//     the same kind of tree. Two builds of identical source measured ~9 % apart
//     on this box once, which is bigger than the gate. Both binaries here come
//     from `git worktree` checkouts built minutes apart.
//   - OPS-35: Mann-Whitney tests dominance, not the tail. A median verdict and
//     a tail verdict are two verdicts, so the p99 gets its own permutation test
//     on Δp99 — reported next to the median one, never instead of it.
//
// The arms are run A B B A: a monotone drift of the host lands on the two
// middle arms and the two outer ones alike, so it cannot be read as an effect.

type armRuns struct {
	label string
	p50   []float64
	p99   []float64
	rps   []float64
}

func cmdABBA(args []string) {
	fs := flag.NewFlagSet("abba", flag.ExitOnError)
	in := fs.String("in", "", "JSONL of runs; the label prefix before '#' names the arm")
	gateMs := fs.Float64("gate-ms", 0.5, "absolute half of the materiality gate")
	gatePct := fs.Float64("gate-pct", 3, "relative half of the materiality gate, in percent")
	resamples := fs.Int("resamples", 4000, "permutation resamples for the Δp99 test")
	_ = fs.Parse(args)

	rows, err := readRuns(*in, "")
	if err != nil {
		fatal(err)
	}
	order := []string{}
	arms := map[string]*armRuns{}
	for _, r := range rows {
		name := r.Label
		if i := strings.Index(name, "#"); i >= 0 {
			name = name[:i]
		}
		a, ok := arms[name]
		if !ok {
			a = &armRuns{label: name}
			arms[name] = a
			order = append(order, name)
		}
		a.p50 = append(a.p50, r.ResponseMs.P50)
		a.p99 = append(a.p99, r.ResponseMs.P99)
		a.rps = append(a.rps, r.GoodputRPS)
	}

	fmt.Printf("| arm | runs | p50 median (ms) | p50 CV | p99 median (ms) | goodput median |\n|:--|--:|--:|--:|--:|--:|\n")
	for _, k := range order {
		a := arms[k]
		fmt.Printf("| %s | %d | %.3f | %.1f %% | %.2f | %.1f |\n",
			a.label, len(a.p50), stats.Median(a.p50), stats.CV(a.p50)*100, stats.Median(a.p99), stats.Median(a.rps))
	}
	fmt.Println()

	type pair struct{ a, b string }
	var pairs []pair
	for i := 0; i+1 < len(order); i++ {
		pairs = append(pairs, pair{order[i], order[i+1]})
	}
	if len(order) == 4 {
		pairs = append(pairs, pair{order[0], order[3]}, pair{order[1], order[2]})
	}
	for _, p := range pairs {
		a, b := arms[p.a], arms[p.b]
		_, pv := stats.MannWhitneyU(a.p50, b.p50)
		ma, mb := stats.Median(a.p50), stats.Median(b.p50)
		delta := mb - ma
		pct := 0.0
		if ma != 0 {
			pct = delta / ma * 100
		}
		gate := math.Max(*gateMs, math.Abs(ma)**gatePct/100)
		verdict := "no_change"
		if pv < 0.05 && math.Abs(delta) > gate {
			verdict = "regression"
			if delta < 0 {
				verdict = "improvement"
			}
		}
		lo, hi := stats.BootstrapMedianDiffCI(a.p50, b.p50)
		d99, p99p := stats.PermutationQuantileDiff(a.p99, b.p99, 0.99, *resamples)
		fmt.Printf("%-16s → %-16s  Δp50 %+7.3f ms (%+5.1f %%)  MWU p=%.3f  gate %.3f ms  CI [%+.3f, %+.3f]  **%s**\n",
			p.a, p.b, delta, pct, pv, gate, lo, hi, verdict)
		fmt.Printf("%-16s   %-16s  Δp99 %+7.2f ms  permutation p=%.3f  (the tail is its own verdict — OPS-35)\n", "", "", d99, p99p)
	}
	if *in != "" {
		out := struct {
			Arms  []string `json:"arms"`
			Gate  string   `json:"gate"`
			Notes string   `json:"notes"`
		}{order, fmt.Sprintf("max(%.1f ms, %.0f %%)", *gateMs, *gatePct),
			"A B B A; base and new built from git worktrees minutes apart (OPS-34); the tail carries its own permutation test (OPS-35)"}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fatal(err)
		}
		_ = os.WriteFile(strings.TrimSuffix(*in, ".jsonl")+"-abba.json", b, 0o644) //nolint:gosec
	}
}
