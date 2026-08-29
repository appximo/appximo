// Package main — the capacity laboratory: an open-model load generator, a
// Universal Scalability Law fit, and the translation of a throughput ceiling
// into concurrent users under a declared load profile.
//
// It is LABORATORY tooling, not engine surface: nothing here runs in a served
// binary and nothing here is imported by pkg/. It exists so the question a
// customer actually asks — "how many users does this hold?" — is answered with
// a method that can be re-run on a new box in one command, instead of with an
// anecdote.
//
// The model (Gunther, Guerrilla Capacity Planning, 2007; Holtman & Gunther,
// CMG 2008, arXiv:0809.2541):
//
//	X(N) = γN / (1 + α(N−1) + βN(N−1))
//
// γ is the single-client throughput (the scale factor), α the CONTENTION
// (serialisation: the fraction of the work that cannot proceed in parallel),
// β the COHERENCY penalty (the cost of keeping shared state consistent, which
// grows as N², and is what makes throughput go DOWN past the peak instead of
// merely flattening). With β = 0 the law degenerates to Amdahl (a ceiling at
// γ/α, never retrograde). The peak is at
//
//	N_max = sqrt((1 − α)/β)
//
// Gunther's own warning is part of the contract and is printed with every
// report: the law is not a crystal ball. It cannot predict an intrinsic
// pathology or a broken measurement, and when the data diverge from the model
// that is a fact about the system, to be said, not smoothed.
package main

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"strconv"
)

// Point is one measured load level: N concurrent requests in the system
// (Little's law over the measured window, never the generator's configured
// worker count) achieving X completions per second.
type Point struct {
	N float64 `json:"n"`
	X float64 `json:"x"`
}

// USL is a fitted model.
type USL struct {
	Gamma float64 `json:"gamma"` // X(1), completions/s at one concurrent request
	Alpha float64 `json:"alpha"` // contention
	Beta  float64 `json:"beta"`  // coherency
	R2    float64 `json:"r2"`
	RMSE  float64 `json:"rmse"`
	N     int     `json:"points"`
}

// Throughput evaluates the fitted law at n.
func (u USL) Throughput(n float64) float64 {
	den := 1 + u.Alpha*(n-1) + u.Beta*n*(n-1)
	if den <= 0 {
		return 0
	}
	return u.Gamma * n / den
}

// NMax is the concurrency at which throughput peaks. Zero β means the law is
// Amdahl: no peak, an asymptote at γ/α — reported as +Inf so the caller must
// say so rather than print a number that does not exist.
func (u USL) NMax() float64 {
	if u.Beta <= 0 {
		return math.Inf(1)
	}
	return math.Sqrt((1 - u.Alpha) / u.Beta)
}

// XMax is the throughput at the peak (or the Amdahl asymptote γ/α).
func (u USL) XMax() float64 {
	n := u.NMax()
	if math.IsInf(n, 1) {
		if u.Alpha <= 0 {
			return math.Inf(1)
		}
		return u.Gamma / u.Alpha
	}
	return u.Throughput(n)
}

// Retrograde reports whether the fit says throughput DECREASES past the peak
// (β > 0) — the difference between "it flattens" and "it gets worse", which is
// the difference between over-provisioning and an outage.
func (u USL) Retrograde() bool { return u.Beta > 0 }

// Fit fits the USL to pts by nonlinear least squares on the throughput
// residuals. The seed comes from Gunther's own linearisation — dividing the
// law through by N and rearranging gives
//
//	(γN/X − 1)/(N − 1) = α + βN
//
// an ordinary line in N whose intercept is α and whose slope is β — and the
// seed is then refined by Levenberg–Marquardt on the original (non-linearised)
// form, which is what the R `usl` package does and what keeps the residuals in
// the units the operator reads (requests per second), not in the units of a
// transformed variable that weights the low-load points far more heavily.
func Fit(pts []Point) (USL, error) {
	clean := make([]Point, 0, len(pts))
	for _, p := range pts {
		if p.N > 0 && p.X > 0 && !math.IsNaN(p.N) && !math.IsNaN(p.X) {
			clean = append(clean, p)
		}
	}
	if len(clean) < 4 {
		return USL{}, errors.New("usl: fewer than 4 usable load points — the fit is not trustworthy below 4 and the practice is ≥ 6")
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].N < clean[j].N })

	gamma0 := 0.0
	for _, p := range clean {
		if r := p.X / p.N; r > gamma0 {
			gamma0 = r
		}
	}
	alpha0, beta0 := seedLinear(clean, gamma0)
	best := levenberg(clean, [3]float64{gamma0, alpha0, beta0})

	// A second seed from the lowest-load point guards against the linear seed
	// landing in a bad basin when the low-N end is noisy.
	alt := levenberg(clean, [3]float64{clean[0].X / clean[0].N, 0.1, 0.001})
	if sse(clean, alt) < sse(clean, best) {
		best = alt
	}

	u := USL{Gamma: best[0], Alpha: best[1], Beta: best[2], N: len(clean)}
	res := sse(clean, best)
	mean := 0.0
	for _, p := range clean {
		mean += p.X
	}
	mean /= float64(len(clean))
	tot := 0.0
	for _, p := range clean {
		tot += (p.X - mean) * (p.X - mean)
	}
	if tot > 0 {
		u.R2 = 1 - res/tot
	}
	u.RMSE = math.Sqrt(res / float64(len(clean)))
	return u, nil
}

// seedLinear is the α + βN regression of Gunther's linearisation.
func seedLinear(pts []Point, gamma float64) (alpha, beta float64) {
	var sx, sy, sxx, sxy float64
	n := 0.0
	for _, p := range pts {
		if p.N <= 1 {
			continue
		}
		y := (gamma*p.N/p.X - 1) / (p.N - 1)
		sx += p.N
		sy += y
		sxx += p.N * p.N
		sxy += p.N * y
		n++
	}
	if n < 2 {
		return 0.1, 0.001
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0.1, 0.001
	}
	beta = (n*sxy - sx*sy) / den
	alpha = (sy - beta*sx) / n
	return clamp(alpha, 0, 0.999999), clamp(beta, 0, 1)
}

func modelAt(p [3]float64, n float64) float64 {
	den := 1 + p[1]*(n-1) + p[2]*n*(n-1)
	if den <= 0 {
		return 0
	}
	return p[0] * n / den
}

func sse(pts []Point, p [3]float64) float64 {
	s := 0.0
	for _, q := range pts {
		r := q.X - modelAt(p, q.N)
		s += r * r
	}
	return s
}

// levenberg runs Levenberg–Marquardt with an analytic Jacobian and the
// parameters clamped to their physical range (γ > 0, 0 ≤ α < 1, 0 ≤ β ≤ 1).
func levenberg(pts []Point, seed [3]float64) [3]float64 {
	p := [3]float64{math.Max(seed[0], 1e-9), clamp(seed[1], 0, 0.999999), clamp(seed[2], 0, 1)}
	lambda := 1e-3
	cur := sse(pts, p)
	for iter := 0; iter < 500; iter++ {
		var jtj [3][3]float64
		var jtr [3]float64
		for _, q := range pts {
			n := q.N
			den := 1 + p[1]*(n-1) + p[2]*n*(n-1)
			if den <= 0 {
				den = 1e-9
			}
			f := p[0] * n / den
			// ∂f/∂γ, ∂f/∂α, ∂f/∂β
			j := [3]float64{
				n / den,
				-p[0] * n * (n - 1) / (den * den),
				-p[0] * n * n * (n - 1) / (den * den),
			}
			r := q.X - f
			for a := 0; a < 3; a++ {
				jtr[a] += j[a] * r
				for b := 0; b < 3; b++ {
					jtj[a][b] += j[a] * j[b]
				}
			}
		}
		improved := false
		for try := 0; try < 12; try++ {
			m := jtj
			for a := 0; a < 3; a++ {
				m[a][a] *= 1 + lambda
				if m[a][a] == 0 {
					m[a][a] = lambda
				}
			}
			d, ok := solve3(m, jtr)
			if !ok {
				lambda *= 10
				continue
			}
			cand := [3]float64{
				math.Max(p[0]+d[0], 1e-9),
				clamp(p[1]+d[1], 0, 0.999999),
				clamp(p[2]+d[2], 0, 1),
			}
			if e := sse(pts, cand); e < cur {
				p, cur = cand, e
				lambda = math.Max(lambda/10, 1e-12)
				improved = true
				break
			}
			lambda *= 10
			if lambda > 1e12 {
				break
			}
		}
		if !improved {
			break
		}
	}
	return p
}

func solve3(a [3][3]float64, b [3]float64) ([3]float64, bool) {
	m := [3][4]float64{}
	for i := 0; i < 3; i++ {
		copy(m[i][:3], a[i][:])
		m[i][3] = b[i]
	}
	for c := 0; c < 3; c++ {
		piv, pv := c, math.Abs(m[c][c])
		for r := c + 1; r < 3; r++ {
			if math.Abs(m[r][c]) > pv {
				piv, pv = r, math.Abs(m[r][c])
			}
		}
		if pv < 1e-15 {
			return [3]float64{}, false
		}
		m[c], m[piv] = m[piv], m[c]
		for r := 0; r < 3; r++ {
			if r == c {
				continue
			}
			f := m[r][c] / m[c][c]
			for k := c; k < 4; k++ {
				m[r][k] -= f * m[c][k]
			}
		}
	}
	return [3]float64{m[0][3] / m[0][0], m[1][3] / m[1][1], m[2][3] / m[2][2]}, true
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }

// CI is a percentile confidence interval from the bootstrap.
//
// The bounds marshal as null when they are NaN — a bootstrap that could not
// produce an interval (every replicate refused to fit) has NO interval, and
// that is the honest encoding. Marshalling NaN is an ERROR in encoding/json,
// so the alternative was an empty file: this session wrote three of them
// before noticing, because the error return was dropped.
type CI struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// MarshalJSON encodes NaN/±Inf bounds as null instead of failing the document.
func (c CI) MarshalJSON() ([]byte, error) {
	f := func(v float64) string {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return "null"
		}
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	return []byte("{\"lo\":" + f(c.Lo) + ",\"hi\":" + f(c.Hi) + "}"), nil
}

// Bootstrap is the uncertainty of a fit, resampled at the level of the
// REPEATED MEASUREMENT: each load level contributes several independent runs,
// and each bootstrap replicate draws one run per level with replacement and
// refits. That is the honest resampling unit here — the runs are what varies,
// the levels are chosen by the experimenter and are not a random sample of
// anything.
type Bootstrap struct {
	Resamples int `json:"resamples"`
	Converged int `json:"converged"`
	Alpha     CI  `json:"alpha_ci"`
	Beta      CI  `json:"beta_ci"`
	Gamma     CI  `json:"gamma_ci"`
	NMax      CI  `json:"n_max_ci"`
	XMax      CI  `json:"x_max_ci"`
}

// BootstrapFit resamples levels (each a slice of repeat measurements) and
// refits, returning percentile CIs at the given level (e.g. 0.95).
func BootstrapFit(levels [][]Point, resamples int, level float64, seed int64) Bootstrap {
	rng := rand.New(rand.NewSource(seed))
	out := Bootstrap{Resamples: resamples}
	var as, bs, gs, ns, xs []float64
	draw := make([]Point, len(levels))
	for i := 0; i < resamples; i++ {
		for j, reps := range levels {
			if len(reps) == 0 {
				continue
			}
			draw[j] = reps[rng.Intn(len(reps))]
		}
		u, err := Fit(draw)
		if err != nil {
			continue
		}
		out.Converged++
		as = append(as, u.Alpha)
		bs = append(bs, u.Beta)
		gs = append(gs, u.Gamma)
		if nm := u.NMax(); !math.IsInf(nm, 0) {
			ns = append(ns, nm)
		}
		if xm := u.XMax(); !math.IsInf(xm, 0) {
			xs = append(xs, xm)
		}
	}
	out.Alpha, out.Beta, out.Gamma = pctCI(as, level), pctCI(bs, level), pctCI(gs, level)
	out.NMax, out.XMax = pctCI(ns, level), pctCI(xs, level)
	return out
}

func pctCI(v []float64, level float64) CI {
	if len(v) < 2 {
		return CI{Lo: math.NaN(), Hi: math.NaN()}
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	lo := (1 - level) / 2
	return CI{Lo: quantileOf(s, lo), Hi: quantileOf(s, 1-lo)}
}

// quantileOf is the linear-interpolation quantile of an already-sorted slice.
func quantileOf(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	i := int(math.Floor(pos))
	f := pos - float64(i)
	if i+1 >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[i]*(1-f) + sorted[i+1]*f
}
