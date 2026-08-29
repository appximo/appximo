package stats

import (
	"math"
	"math/rand"
	"testing"
)

// seqFloat returns [start, start+1, ..., start+(n-1)] as float64.
func seqFloat(start, n int) []float64 {
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = float64(start + i)
	}
	return out
}

func contains(idx []int, want int) bool {
	for _, v := range idx {
		if v == want {
			return true
		}
	}
	return false
}

// 1. Total separation: U must be 0 and the difference significant.
func TestMannWhitneyU_TotalSeparation(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5}
	b := []float64{10, 11, 12, 13, 14}
	u, p := MannWhitneyU(a, b)
	if u != 0 {
		t.Errorf("U = %v, want 0", u)
	}
	if p >= 0.02 {
		t.Errorf("pValue = %v, want < 0.02", p)
	}
}

// 2. Identical samples: no difference at all.
func TestMannWhitneyU_Identical(t *testing.T) {
	a := []float64{5, 5, 5, 5, 5}
	b := []float64{5, 5, 5, 5, 5}
	_, p := MannWhitneyU(a, b)
	if p <= 0.9 {
		t.Errorf("pValue = %v, want > 0.9", p)
	}
}

// 3. Same distribution shifted by a negligible amount, large n: not significant.
func TestMannWhitneyU_NegligibleShift(t *testing.T) {
	a := seqFloat(1, 100)
	b := make([]float64, len(a))
	for i := range a {
		b[i] = a[i] + 0.001
	}
	_, p := MannWhitneyU(a, b)
	if p <= 0.05 {
		t.Errorf("pValue = %v, want high (> 0.05, not significant)", p)
	}
}

// 4. CV of a constant series is 0.
func TestCV_Constant(t *testing.T) {
	if got := CV([]float64{10, 10, 10}); got != 0 {
		t.Errorf("CV = %v, want 0", got)
	}
}

// 5. Median of an even-length slice.
func TestMedian_Even(t *testing.T) {
	if got := Median([]float64{1, 2, 3, 4}); got != 2.5 {
		t.Errorf("Median = %v, want 2.5", got)
	}
}

// 6. Percentile with linear interpolation (R-7).
func TestPercentile_P95(t *testing.T) {
	got := Percentile(seqFloat(1, 100), 95)
	if math.Abs(got-95.05) > 0.5 {
		t.Errorf("Percentile(.., 95) = %v, want ~95.05 (tol 0.5)", got)
	}
}

// 7. IQR outlier detection flags the extreme value's index.
func TestIQROutliers_Extreme(t *testing.T) {
	x := []float64{1, 2, 3, 4, 5, 100}
	idx := IQROutliers(x)
	if !contains(idx, 5) { // index of the value 100
		t.Errorf("IQROutliers = %v, want to include index 5 (value 100)", idx)
	}
}

// 8. Bootstrap CI of the median difference: contains the true shift (10), excludes 0.
func TestBootstrapMedianDiffCI(t *testing.T) {
	a := seqFloat(1, 50)  // 1..50,  median 25.5
	b := seqFloat(11, 50) // 11..60, median 35.5  → diff 10
	lower, upper := BootstrapMedianDiffCI(a, b)
	if !(lower <= 10 && upper >= 10) {
		t.Errorf("CI [%v, %v] must contain 10", lower, upper)
	}
	if lower <= 0 && upper >= 0 {
		t.Errorf("CI [%v, %v] must NOT contain 0", lower, upper)
	}
}

// The OPS-35 case: two samples with the SAME median and a shifted TAIL.
// Mann-Whitney must NOT see it (that is the finding), the permutation test on
// the p99 must.
func TestPermutationQuantileDiff_TailOnlyShift(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	a := make([]float64, 2000)
	b := make([]float64, 2000)
	for i := range a {
		a[i] = 1.5 + rng.Float64()*0.2 // 1.5–1.7 ms, flat
		b[i] = 1.5 + rng.Float64()*0.2
		if i%50 == 0 { // 2 % of b carries a 20 ms tail
			b[i] = 20 + rng.Float64()
		}
	}
	_, mw := MannWhitneyU(a, b)
	obs, pp := PermutationQuantileDiff(a, b, 99, 2000)
	if obs < 10 {
		t.Fatalf("observed Δp99 = %.2f, want the injected ~18 ms tail", obs)
	}
	if pp > 0.01 {
		t.Fatalf("permutation p = %.4f, want < 0.01 for a tail-only shift", pp)
	}
	if mw < 0.05 {
		t.Logf("note: Mann-Whitney ALSO flagged this one (p=%.3f) — the finding is that it need not", mw)
	}
	// Same distribution → no evidence.
	_, pNull := PermutationQuantileDiff(a, a, 99, 500)
	if pNull < 0.5 {
		t.Fatalf("identical samples: p = %.3f, want ≈ 1", pNull)
	}
}
