package main

import (
	"math"
	"math/rand"
	"testing"
)

// The fitter must recover parameters it generated itself — the only way to
// know a nonlinear fit is not just drawing a plausible curve through noise.
func TestFit_RecoversKnownParameters(t *testing.T) {
	cases := []USL{
		{Gamma: 1000, Alpha: 0.02, Beta: 0.0001}, // a scalable service
		{Gamma: 300, Alpha: 0.5, Beta: 0.005},    // heavily serialised
		{Gamma: 120, Alpha: 0.9, Beta: 0.001},    // a single-core box
	}
	ns := []float64{1, 2, 4, 8, 16, 32, 64, 128}
	for _, want := range cases {
		var pts []Point
		for _, n := range ns {
			pts = append(pts, Point{N: n, X: want.Throughput(n)})
		}
		got, err := Fit(pts)
		if err != nil {
			t.Fatalf("fit: %v", err)
		}
		if math.Abs(got.Alpha-want.Alpha) > 0.02 || math.Abs(got.Beta-want.Beta) > 0.002 ||
			math.Abs(got.Gamma-want.Gamma)/want.Gamma > 0.05 {
			t.Fatalf("want α=%.4f β=%.5f γ=%.1f, got α=%.4f β=%.5f γ=%.1f (R²=%.4f)",
				want.Alpha, want.Beta, want.Gamma, got.Alpha, got.Beta, got.Gamma, got.R2)
		}
		if got.R2 < 0.999 {
			t.Fatalf("noiseless data must fit almost exactly, R²=%.4f", got.R2)
		}
		if rel := math.Abs(got.NMax()-want.NMax()) / want.NMax(); rel > 0.10 {
			t.Fatalf("N_max %.1f vs %.1f (%.1f%%)", got.NMax(), want.NMax(), rel*100)
		}
	}
}

// With 5 % multiplicative noise the point estimate must still land close and
// R² must stay high — the criterion the session applies to its own data.
func TestFit_ToleratesNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	want := USL{Gamma: 800, Alpha: 0.08, Beta: 0.0008}
	var pts []Point
	for _, n := range []float64{1, 2, 4, 8, 16, 24, 32, 48, 64, 96} {
		pts = append(pts, Point{N: n, X: want.Throughput(n) * (1 + rng.NormFloat64()*0.05)})
	}
	got, err := Fit(pts)
	if err != nil {
		t.Fatal(err)
	}
	if got.R2 < 0.9 {
		t.Fatalf("R² = %.3f under 5%% noise", got.R2)
	}
	if rel := math.Abs(got.NMax()-want.NMax()) / want.NMax(); rel > 0.30 {
		t.Fatalf("N_max %.1f vs %.1f", got.NMax(), want.NMax())
	}
}

// β = 0 is Amdahl: a ceiling, never a peak. The model must SAY that instead of
// printing an N_max that does not exist.
func TestFit_AmdahlHasNoPeak(t *testing.T) {
	u := USL{Gamma: 500, Alpha: 0.25, Beta: 0}
	if !math.IsInf(u.NMax(), 1) {
		t.Fatal("β = 0 must report N_max = +Inf")
	}
	if u.Retrograde() {
		t.Fatal("β = 0 is not retrograde")
	}
	if math.Abs(u.XMax()-2000) > 1e-6 {
		t.Fatalf("Amdahl asymptote γ/α = 2000, got %.3f", u.XMax())
	}
}

// Fewer than four points is refused, not fitted.
func TestFit_RefusesTooFewPoints(t *testing.T) {
	if _, err := Fit([]Point{{N: 1, X: 10}, {N: 2, X: 18}, {N: 4, X: 30}}); err == nil {
		t.Fatal("3 points must be refused")
	}
}

// The bootstrap must produce an interval that brackets the point estimate.
func TestBootstrap_BracketsTheEstimate(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	want := USL{Gamma: 600, Alpha: 0.1, Beta: 0.0006}
	var levels [][]Point
	var flat []Point
	for _, n := range []float64{1, 2, 4, 8, 16, 32, 48, 64} {
		var reps []Point
		for r := 0; r < 3; r++ {
			p := Point{N: n, X: want.Throughput(n) * (1 + rng.NormFloat64()*0.04)}
			reps = append(reps, p)
			flat = append(flat, p)
		}
		levels = append(levels, reps)
	}
	point, err := Fit(flat)
	if err != nil {
		t.Fatal(err)
	}
	b := BootstrapFit(levels, 400, 0.95, 3)
	if b.Converged < 300 {
		t.Fatalf("only %d/%d replicates converged", b.Converged, b.Resamples)
	}
	if point.Alpha < b.Alpha.Lo-1e-9 || point.Alpha > b.Alpha.Hi+1e-9 {
		t.Fatalf("α %.4f outside CI [%.4f, %.4f]", point.Alpha, b.Alpha.Lo, b.Alpha.Hi)
	}
	if b.NMax.Hi <= b.NMax.Lo {
		t.Fatalf("degenerate N_max CI [%.1f, %.1f]", b.NMax.Lo, b.NMax.Hi)
	}
}
