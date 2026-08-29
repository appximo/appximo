// Package stats provides the statistical primitives behind the DevHub benchmark
// engine (S42): robust summaries plus a two-sample significance test so a
// benchmark delta can be called improvement / regression / no_change with a
// p-value instead of eyeballing a single run.
//
// Everything here is pure and dependency-free (stdlib only — no gonum): the
// algorithms are implemented by hand so the tooling stays CGO-free and trivially
// vendorable.
package stats

import (
	"math"
	"math/rand"
	"sort"
)

// mean is the arithmetic mean; 0 for an empty slice.
func mean(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

// sortedCopy returns a sorted copy without mutating the input.
func sortedCopy(x []float64) []float64 {
	s := append([]float64(nil), x...)
	sort.Float64s(s)
	return s
}

// Median returns the median of x (mean of the two central values for even n).
// It copies and sorts, so the input is never mutated.
func Median(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := sortedCopy(x)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Percentile returns the p-th percentile (p in [0,100]) using linear
// interpolation between closest ranks — the R-7 / NumPy-default / Excel
// PERCENTILE.INC method. Percentile([1..100], 95) == 95.05.
func Percentile(x []float64, p float64) float64 {
	if len(x) == 0 {
		return 0
	}
	s := sortedCopy(x)
	if len(s) == 1 {
		return s[0]
	}
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[len(s)-1]
	}
	h := (p / 100) * float64(len(s)-1)
	lo := int(math.Floor(h))
	frac := h - float64(lo)
	if lo+1 >= len(s) {
		return s[lo]
	}
	return s[lo] + frac*(s[lo+1]-s[lo])
}

// CV is the coefficient of variation (sample stddev / mean). Returns 0 when the
// mean is 0 or there are fewer than 2 samples. Uses the sample standard
// deviation (n-1 denominator).
func CV(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := mean(x)
	if m == 0 {
		return 0
	}
	var ss float64
	for _, v := range x {
		d := v - m
		ss += d * d
	}
	variance := ss / float64(len(x)-1)
	return math.Sqrt(variance) / m
}

// IQROutliers returns the indices (into the original, unsorted x) of values that
// fall outside the Tukey fences: below Q1-1.5*IQR or above Q3+1.5*IQR.
func IQROutliers(x []float64) []int {
	if len(x) < 4 {
		return nil
	}
	q1 := Percentile(x, 25)
	q3 := Percentile(x, 75)
	iqr := q3 - q1
	lo := q1 - 1.5*iqr
	hi := q3 + 1.5*iqr
	var out []int
	for i, v := range x {
		if v < lo || v > hi {
			out = append(out, i)
		}
	}
	return out
}

// normalCDF is the CDF of the standard normal distribution, via the
// complementary error function: Φ(z) = 0.5 * erfc(-z/√2).
func normalCDF(z float64) float64 {
	return 0.5 * math.Erfc(-z/math.Sqrt2)
}

// MannWhitneyU runs a two-sided Mann-Whitney U test (a.k.a. Wilcoxon rank-sum)
// on two independent samples, using the normal approximation with tie and
// continuity corrections. It returns the smaller U statistic and the two-sided
// p-value.
//
// Algorithm:
//  1. Rank the union of both samples (average ranks within tie groups).
//  2. R1 = sum of ranks of sample a.
//  3. U1 = R1 - n1*(n1+1)/2 ; U2 = n1*n2 - U1 ; U = min(U1, U2).
//  4. mu = n1*n2/2.
//  5. sigma = sqrt( (n1*n2/12) * ((n+1) - sum(t^3-t)/(n*(n-1))) ), t = tie sizes.
//  6. z = (U - mu + 0.5) / sigma   (continuity correction toward the mean).
//  7. p = 2 * (1 - Φ(|z|)).
func MannWhitneyU(a, b []float64) (u float64, pValue float64) {
	n1, n2 := len(a), len(b)
	if n1 == 0 || n2 == 0 {
		return 0, 1
	}

	type obs struct {
		v   float64
		grp int
	}
	all := make([]obs, 0, n1+n2)
	for _, v := range a {
		all = append(all, obs{v, 1})
	}
	for _, v := range b {
		all = append(all, obs{v, 2})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })

	// Average ranks (1-based) with tie correction term sum(t^3 - t).
	ranks := make([]float64, len(all))
	var tieTerm float64
	for i := 0; i < len(all); {
		j := i
		for j < len(all) && all[j].v == all[i].v {
			j++
		}
		avg := float64(i+j+1) / 2.0 // mean of 1-based ranks i+1..j
		for k := i; k < j; k++ {
			ranks[k] = avg
		}
		t := float64(j - i)
		tieTerm += t*t*t - t
		i = j
	}

	var r1 float64
	for k := range all {
		if all[k].grp == 1 {
			r1 += ranks[k]
		}
	}

	n1f, n2f := float64(n1), float64(n2)
	u1 := r1 - n1f*(n1f+1)/2
	u2 := n1f*n2f - u1
	u = math.Min(u1, u2)

	mu := n1f * n2f / 2
	n := n1f + n2f
	sigma := math.Sqrt((n1f * n2f / 12.0) * ((n + 1) - tieTerm/(n*(n-1))))
	if sigma == 0 {
		// No variance to test against (e.g. all values identical) → no evidence
		// of a difference.
		return u, 1.0
	}
	z := (u - mu + 0.5) / sigma
	pValue = 2 * (1 - normalCDF(math.Abs(z)))
	if pValue > 1 {
		pValue = 1
	}
	if pValue < 0 {
		pValue = 0
	}
	return u, pValue
}

// BootstrapMedianDiffCI returns the 95% confidence interval of the difference in
// medians (median(b) - median(a)) via the percentile bootstrap with 2000
// resamples. The RNG is seeded deterministically (42) so results are
// reproducible across runs.
func BootstrapMedianDiffCI(a, b []float64) (lower, upper float64) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	const resamples = 2000
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // determinism wanted, not crypto
	diffs := make([]float64, resamples)
	ra := make([]float64, len(a))
	rb := make([]float64, len(b))
	for i := 0; i < resamples; i++ {
		for j := range ra {
			ra[j] = a[rng.Intn(len(a))]
		}
		for j := range rb {
			rb[j] = b[rng.Intn(len(b))]
		}
		diffs[i] = Median(rb) - Median(ra)
	}
	return Percentile(diffs, 2.5), Percentile(diffs, 97.5)
}

// PermutationQuantileDiff answers the question Mann-Whitney U does NOT: "did
// the p-th percentile move?" (CENTINELA-C-S1, OPS-35). Mann-Whitney compares
// stochastic dominance — in practice the medians — so a change that shifts only
// the tail (p99) can pass it with p ≈ 1. This is a two-sided permutation test on
// the statistic of interest itself: observed = P_p(b) − P_p(a); under the null
// (the two samples come from one distribution) the labels are exchangeable, so
// the samples are pooled, re-labelled `resamples` times with a deterministic
// RNG, and the fraction of |permuted diff| ≥ |observed| is the p-value (with the
// +1 correction so it never reports exactly 0). It is exact in spirit (no
// normality, no variance formula) and its resolution is bounded by the sample
// sizes: a percentile of the tail estimated from a few hundred points is wide by
// construction — that is the caveat A-54 writes down, not a defect of the test.
//
// p is the percentile (99 for p99); resamples ≥ 1000 is the practical floor.
func PermutationQuantileDiff(a, b []float64, p float64, resamples int) (observed float64, pValue float64) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 1
	}
	if resamples < 1 {
		resamples = 1
	}
	observed = Percentile(b, p) - Percentile(a, p)
	pooled := make([]float64, 0, len(a)+len(b))
	pooled = append(pooled, a...)
	pooled = append(pooled, b...)
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // determinism wanted, not crypto
	pa := make([]float64, len(a))
	pb := make([]float64, len(b))
	extreme := 0
	absObs := math.Abs(observed)
	for i := 0; i < resamples; i++ {
		rng.Shuffle(len(pooled), func(x, y int) { pooled[x], pooled[y] = pooled[y], pooled[x] })
		copy(pa, pooled[:len(a)])
		copy(pb, pooled[len(a):])
		if math.Abs(Percentile(pb, p)-Percentile(pa, p)) >= absObs {
			extreme++
		}
	}
	pValue = float64(extreme+1) / float64(resamples+1)
	if pValue > 1 {
		pValue = 1
	}
	return observed, pValue
}
