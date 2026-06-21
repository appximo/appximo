package eval

import (
	"math"
	"testing"
)

func approx(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.6f, want %.6f (±%.0e)", what, got, want, tol)
	}
}

func TestWilsonInterval(t *testing.T) {
	// 0/10 successes: the classic Wilson 95% upper bound is ~0.2775 (Wald gives 0).
	_, lo, hi := WilsonInterval(0, 10, Z95)
	approx(t, lo, 0, 1e-9, "wilson lo (0/10)")
	approx(t, hi, 0.2775, 1e-3, "wilson hi (0/10)")
	// 10/10: Wilson lower bound ~0.7226 (NOT the Clopper-Pearson 0.6915), upper
	// clamped to 1.
	_, lo, hi = WilsonInterval(10, 10, Z95)
	approx(t, hi, 1, 1e-9, "wilson hi (10/10)")
	approx(t, lo, 0.7226, 1e-3, "wilson lo (10/10)")
	// n==0 → whole interval.
	p, lo, hi := WilsonInterval(0, 0, Z95)
	if p != 0 || lo != 0 || hi != 1 {
		t.Errorf("empty n should give [0,1], got p=%v lo=%v hi=%v", p, lo, hi)
	}
}

func TestChiSquareSurvival(t *testing.T) {
	// Known df=1 critical values.
	approx(t, chiSquareSurvival(3.841459, 1), 0.05, 1e-3, "χ²(1) survival @3.841")
	approx(t, chiSquareSurvival(6.634897, 1), 0.01, 1e-3, "χ²(1) survival @6.635")
	approx(t, chiSquareSurvival(2.705543, 1), 0.10, 1e-3, "χ²(1) survival @2.706")
	// df=2.
	approx(t, chiSquareSurvival(5.991465, 2), 0.05, 1e-3, "χ²(2) survival @5.991")
	approx(t, chiSquareSurvival(0, 1), 1, 1e-9, "χ² survival @0")
}

func TestMcNemarExact(t *testing.T) {
	// b=1, c=9 → two-sided exact = 2*(C(10,0)+C(10,1))*0.5^10 = 2*11/1024 = 0.021484.
	r := McNemar(1, 9)
	if r.Method != "exact-binomial" {
		t.Errorf("expected exact-binomial, got %s", r.Method)
	}
	approx(t, r.P, 0.0214844, 1e-5, "mcnemar exact p (1,9)")
	if !r.Underpowered() {
		t.Errorf("10 discordants should be underpowered (<25)")
	}
}

func TestMcNemarNoDiscordants(t *testing.T) {
	r := McNemar(0, 0)
	if r.Method != "no-discordants" || r.P != 1 {
		t.Errorf("no discordants → p=1, method no-discordants; got %+v", r)
	}
}

func TestMcNemarChiSquare(t *testing.T) {
	// b=20, c=5 → 25 discordants → Edwards χ²: (|15|-1)²/25 = 196/25 = 7.84.
	r := McNemar(20, 5)
	if r.Method != "chi2-edwards" {
		t.Errorf("expected chi2-edwards at 25 discordants, got %s", r.Method)
	}
	approx(t, r.Stat, 7.84, 1e-9, "edwards stat")
	if r.Underpowered() {
		t.Errorf("25 discordants is not underpowered")
	}
	if r.P >= 0.05 {
		t.Errorf("expected p<0.05 for stat 7.84, got %.4f", r.P)
	}
}

func TestCochranQ(t *testing.T) {
	rows := [][]bool{
		{true, false, false},
		{true, true, false},
		{true, true, true},
		{false, false, true},
	}
	q := CochranQ(rows)
	approx(t, q.Q, 0.6666667, 1e-6, "cochran Q")
	if q.DF != 2 || q.K != 3 || q.N != 4 {
		t.Errorf("expected df=2 k=3 n=4, got %+v", q)
	}
	// All-concordant rows → Q=0, p=1.
	allSame := CochranQ([][]bool{{true, true}, {false, false}})
	if allSame.P != 1 {
		t.Errorf("all-concordant should give p=1, got %v", allSame.P)
	}
}

func TestHolmBonferroni(t *testing.T) {
	got := HolmBonferroni([]float64{0.01, 0.04, 0.03})
	want := []float64{0.03, 0.06, 0.06}
	for i := range want {
		approx(t, got[i], want[i], 1e-9, "holm["+itoa(i)+"]")
	}
	// Monotonicity + cap at 1.
	got = HolmBonferroni([]float64{0.5, 0.6})
	if got[0] > got[1]+1e-12 || got[1] > 1 {
		t.Errorf("holm not monotone/capped: %v", got)
	}
}
