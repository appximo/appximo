// Package eval is the scientific measurement instrument for the AI schema-
// generation layer (AI-F2-S1): a stratified NL→schema gold test set + a paired
// ablation harness + statistics with the rigor the research demands. It measures
// the loop's real behavior on THIS domain (typed JSON schemas with FKs/RBAC/state
// machines) — a number published nowhere; every figure in the literature is an
// extrapolation from text-to-SQL/code-gen. It builds NO new generation technique;
// it is the instrument those techniques are measured with.
//
// stats.go is the statistics core (pure Go, no deps):
//   - Wilson score interval for a proportion (NOT Wald — Wald under-covers near 0/1).
//   - McNemar's paired test (Dietterich 1998: the only test with acceptable Type I
//     error for run-once algorithms; never a paired t-test or a difference of
//     proportions). EXACT binomial when discordants b+c < 25, Edwards continuity-
//     corrected χ² otherwise.
//   - Cochran's Q omnibus for >2 paired conditions, with Holm-Bonferroni FWER
//     control over the pairwise McNemar family.
//
// E[iterations] is measured EMPIRICALLY by the harness (the loop is validator-
// guided, not i.i.d., so the geometric 1/p_sem is only a theoretical bound, marked
// as such — see report.go).
package eval

import (
	"math"
	"sort"
)

// Z95 is the standard normal quantile for a two-sided 95% interval.
const Z95 = 1.959963984540054

// WilsonInterval returns the point estimate and the Wilson score interval [lo, hi]
// for `successes` out of `n` at confidence implied by z (use Z95). For n == 0 it
// returns the whole [0,1]. The interval is clamped to [0,1] and never degenerate
// at the boundaries the way Wald is.
func WilsonInterval(successes, n int, z float64) (phat, lo, hi float64) {
	if n == 0 {
		return 0, 0, 1
	}
	p := float64(successes) / float64(n)
	nf := float64(n)
	z2 := z * z
	denom := 1 + z2/nf
	center := (p + z2/(2*nf)) / denom
	half := (z / denom) * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))
	lo, hi = center-half, center+half
	if lo < 0 {
		lo = 0
	}
	if hi > 1 {
		hi = 1
	}
	return p, lo, hi
}

// McNemarResult is the outcome of a paired McNemar test on two conditions.
// B = cases where A succeeded and B failed; C = A failed and B succeeded. The
// power lives in the discordants (B+C); Concordant pairs carry no information.
type McNemarResult struct {
	B          int     `json:"b"`
	C          int     `json:"c"`
	Discordant int     `json:"discordant"`
	Stat       float64 `json:"stat"`    // χ² statistic (0 for the exact path)
	P          float64 `json:"p_value"` // two-sided
	Method     string  `json:"method"`  // "exact-binomial" | "chi2-edwards" | "no-discordants"
}

// Underpowered reports whether the discordant count is too small for the χ²
// approximation to be trustworthy — the instrument must say so rather than
// present a fragile p-value as conclusive.
func (m McNemarResult) Underpowered() bool { return m.B+m.C < 25 }

// McNemar runs the paired test. With fewer than 25 discordants it uses the EXACT
// two-sided binomial (p = 0.5); otherwise the Edwards continuity-corrected χ²
// with one degree of freedom.
func McNemar(b, c int) McNemarResult {
	n := b + c
	if n == 0 {
		return McNemarResult{B: b, C: c, Discordant: 0, P: 1, Method: "no-discordants"}
	}
	if n < 25 {
		// Exact two-sided binomial: 2 * P(X <= min(b,c)) with X ~ Binom(n, 0.5),
		// capped at 1. Computed in log space for numerical safety.
		k := b
		if c < k {
			k = c
		}
		var cdf float64
		for i := 0; i <= k; i++ {
			cdf += math.Exp(logBinomCoef(n, i) - float64(n)*math.Ln2)
		}
		p := 2 * cdf
		if p > 1 {
			p = 1
		}
		return McNemarResult{B: b, C: c, Discordant: n, P: p, Method: "exact-binomial"}
	}
	diff := math.Abs(float64(b - c))
	stat := 0.0
	if diff >= 1 {
		stat = (diff - 1) * (diff - 1) / float64(n)
	}
	return McNemarResult{B: b, C: c, Discordant: n, Stat: stat, P: chiSquareSurvival(stat, 1), Method: "chi2-edwards"}
}

// CochranQResult is the omnibus test across k>=2 paired conditions.
type CochranQResult struct {
	Q  float64 `json:"q"`
	DF int     `json:"df"`
	P  float64 `json:"p_value"`
	K  int     `json:"k"` // number of conditions
	N  int     `json:"n"` // number of cases (rows)
}

// CochranQ runs Cochran's Q over `rows`, where each row is one case's binary
// outcome under each of the k conditions (same column order every row). Concordant
// rows (all-success or all-failure) contribute nothing, exactly as in McNemar.
func CochranQ(rows [][]bool) CochranQResult {
	if len(rows) == 0 || len(rows[0]) < 2 {
		return CochranQResult{}
	}
	k := len(rows[0])
	colSum := make([]int, k)
	var sumRow, sumRowSq int
	for _, r := range rows {
		ti := 0
		for j := 0; j < k; j++ {
			if r[j] {
				colSum[j]++
				ti++
			}
		}
		sumRow += ti
		sumRowSq += ti * ti
	}
	var sumColSq, totalCol int
	for _, c := range colSum {
		sumColSq += c * c
		totalCol += c
	}
	denom := float64(k*sumRow - sumRowSq)
	if denom == 0 {
		// Every row concordant — no information; Q undefined → report 0, p=1.
		return CochranQResult{Q: 0, DF: k - 1, P: 1, K: k, N: len(rows)}
	}
	q := float64(k-1) * (float64(k*sumColSq) - float64(totalCol*totalCol)) / denom
	return CochranQResult{Q: q, DF: k - 1, P: chiSquareSurvival(q, k-1), K: k, N: len(rows)}
}

// HolmBonferroni returns the Holm-adjusted p-values aligned to the input order,
// controlling the family-wise error rate (step-down). Adjusted values are
// monotone non-decreasing in the sorted order and capped at 1.
func HolmBonferroni(p []float64) []float64 {
	m := len(p)
	idx := make([]int, m)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return p[idx[a]] < p[idx[b]] })
	adj := make([]float64, m)
	running := 0.0
	for rank, original := range idx {
		v := float64(m-rank) * p[original]
		if v > 1 {
			v = 1
		}
		if v < running {
			v = running // enforce monotonicity
		}
		running = v
		adj[original] = v
	}
	return adj
}

// ── numerics: log binomial coefficient + χ² survival via the incomplete gamma ──

func logBinomCoef(n, k int) float64 {
	lg := func(x float64) float64 { v, _ := math.Lgamma(x); return v }
	return lg(float64(n+1)) - lg(float64(k+1)) - lg(float64(n-k+1))
}

// chiSquareSurvival returns P(X > x) for X ~ χ²(df), i.e. the regularized upper
// incomplete gamma Q(df/2, x/2).
func chiSquareSurvival(x float64, df int) float64 {
	if x <= 0 {
		return 1
	}
	return gammaQ(float64(df)/2, x/2)
}

// gammaQ is the regularized upper incomplete gamma function Q(a,x) = 1 - P(a,x),
// via the series (x < a+1) or the continued fraction (x >= a+1) — Numerical
// Recipes, with math.Lgamma for the normalizer.
func gammaQ(a, x float64) float64 {
	if x < 0 || a <= 0 {
		return math.NaN()
	}
	if x == 0 {
		return 1
	}
	if x < a+1 {
		return 1 - gammaSeries(a, x)
	}
	return gammaCF(a, x)
}

func gammaSeries(a, x float64) float64 {
	gln, _ := math.Lgamma(a)
	ap := a
	sum := 1 / a
	del := sum
	for i := 0; i < 1000; i++ {
		ap++
		del *= x / ap
		sum += del
		if math.Abs(del) < math.Abs(sum)*1e-15 {
			break
		}
	}
	return sum * math.Exp(-x+a*math.Log(x)-gln)
}

func gammaCF(a, x float64) float64 {
	const tiny = 1e-300
	gln, _ := math.Lgamma(a)
	b := x + 1 - a
	c := 1 / tiny
	d := 1 / b
	h := d
	for i := 1; i < 1000; i++ {
		an := -float64(i) * (float64(i) - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Exp(-x+a*math.Log(x)-gln) * h
}
