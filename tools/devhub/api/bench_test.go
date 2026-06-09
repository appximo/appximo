package api

import "testing"

// A median difference far below the practical-effect floor must read as
// no_change even when Mann-Whitney calls it statistically significant. This is
// the S42 1-vCPU artifact in miniature: n=3000 tightly-clustered samples shifted
// by only 0.01ms → p<0.05, but the 0.01ms shift is well under the 0.5ms floor.
func TestClassify_PracticalSignificanceFloor(t *testing.T) {
	const n = 3000
	a := make([]float64, n)
	b := make([]float64, n)
	for i := 0; i < n; i++ {
		v := 1.0 + float64(i)/float64(n)*0.1 // tight spread 1.0..1.1ms
		a[i] = v
		b[i] = v + 0.01 // exact median shift of 0.01ms
	}

	res := classify(a, b)

	if res.PValue >= 0.05 {
		t.Fatalf("setup invalid: expected statistical significance (p<0.05), got p=%v", res.PValue)
	}
	if res.Direction != "no_change" {
		t.Errorf("0.01ms median shift (< minEffect %.3fms) must be no_change, got %q (p=%v)",
			res.MinEffectMs, res.Direction, res.PValue)
	}
	if res.Significant {
		t.Error("Significant must be false: statistically but not practically significant")
	}
}
