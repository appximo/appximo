package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Report is the whole answer to "how many users does it hold?" — the measured
// ladder, the fitted law with its uncertainty, the translation into users
// under DECLARED load profiles, the saturation sequence the engine itself
// reported, and the conditions under which all of it is true.
type Report struct {
	Workload   string     `json:"workload"`
	Metric     string     `json:"throughput_metric"`
	Levels     []Level    `json:"levels"`
	USL        USL        `json:"usl"`
	Boot       Bootstrap  `json:"bootstrap"`
	NMax       jsonFloat  `json:"n_max"`
	XMax       jsonFloat  `json:"x_max_rps"`
	Retrograde bool       `json:"retrograde"`
	Trust      Trust      `json:"trust"`
	Profiles   []Profile  `json:"user_profiles"`
	Queue      QueueView  `json:"queueing"`
	Demand     DemandView `json:"service_demand"`
	Sequence   []SeqEntry `json:"saturation_sequence"`
	Conditions Conditions `json:"conditions"`
}

// jsonFloat encodes +Inf (an Amdahl model has no peak) and NaN as null rather
// than failing the whole document: encoding/json refuses both, and a dropped
// error there writes a zero-byte file that looks like a successful export.
type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(v, 'g', -1, 64)), nil
}

// Level is one offered rate, aggregated over its repeats.
type Level struct {
	OfferedRPS  float64 `json:"offered_rps"`
	Repeats     int     `json:"repeats"`
	N           float64 `json:"concurrency"`
	X           float64 `json:"throughput_rps"`
	XCV         float64 `json:"throughput_cv"`
	P50Resp     float64 `json:"p50_response_ms"`
	P99Resp     float64 `json:"p99_response_ms"`
	P50Serv     float64 `json:"p50_service_ms"`
	P99Serv     float64 `json:"p99_service_ms"`
	COGapP99    float64 `json:"coordinated_omission_gap_p99_ms"`
	Shed429     int64   `json:"status_429"`
	Err5xx      int64   `json:"status_5xx"`
	Abandoned   int64   `json:"abandoned"`
	EngineCPUms float64 `json:"engine_cpu_ms_per_request"`
	PGCPUms     float64 `json:"postgres_cpu_ms_per_request"`
	GenShare    float64 `json:"generator_cpu_share"`
	IdleShare   float64 `json:"idle_share"`
	StealShare  float64 `json:"steal_share"`
	Verdict     string  `json:"dominant_verdict"`
	VerdictMix  string  `json:"verdict_distribution"`
}

// Trust is the published confidence in the fit — the session's own gate, not a
// decoration: below it the number is not published as a ceiling.
type Trust struct {
	R2          float64  `json:"r2"`
	WorstCV     float64  `json:"worst_level_cv"`
	Points      int      `json:"levels"`
	PastPeak    bool     `json:"has_points_past_the_peak"`
	Trustworthy bool     `json:"trustworthy"`
	Reasons     []string `json:"reasons"`
}

// Profile is one declared way of using the app. A throughput number without
// one is not a user count, and this is where the assumption is written down.
type Profile struct {
	Name       string  `json:"name"`
	ThinkTime  string  `json:"think_time"`
	RespTimeMs float64 `json:"response_time_ms"`
	Users      float64 `json:"concurrent_users"`
	UsersLo    float64 `json:"concurrent_users_lo"`
	UsersHi    float64 `json:"concurrent_users_hi"`
}

// QueueView is the M/M/1 overlay: where the latency knee sits, independent of
// the USL, as a cross-check that the ceiling is a queueing ceiling.
type QueueView struct {
	ServiceMs   float64 `json:"service_time_ms"`
	MuRPS       float64 `json:"mu_rps"`
	Knee70RPS   float64 `json:"rps_at_70pct_utilisation"`
	Knee70Ms    float64 `json:"response_ms_at_70pct"`
	Knee80Ms    float64 `json:"response_ms_at_80pct"`
	PlanningRPS float64 `json:"planning_rps"`
}

// DemandView is the generator-free bound: the service demand law.
type DemandView struct {
	EngineMs  float64 `json:"engine_cpu_ms_per_request"`
	PGMs      float64 `json:"postgres_cpu_ms_per_request"`
	TotalMs   float64 `json:"total_cpu_ms_per_request"`
	CeilingX  float64 `json:"ceiling_rps_one_cpu"`
	Ceiling2X float64 `json:"ceiling_rps_two_cpu"`
	Note      string  `json:"note"`
}

// SeqEntry is one rung of the saturation sequence.
type SeqEntry struct {
	OfferedRPS float64 `json:"offered_rps"`
	Verdict    string  `json:"verdict"`
	Owner      string  `json:"owner"`
	Mix        string  `json:"mix"`
}

// Conditions is what makes the numbers reproducible — and falsifiable.
type Conditions struct {
	CPUs        int     `json:"cpus"`
	GeneratorOn string  `json:"generator_location"`
	WorstGenCPU float64 `json:"worst_generator_cpu_share"`
	WorstSteal  float64 `json:"worst_steal_share"`
	Started     string  `json:"started"`
	Ended       string  `json:"ended"`
}

// BuildReport aggregates, fits and translates.
func BuildReport(rows []RunResult, metric, thinks string, resamples int) Report {
	rep := Report{Metric: metric}
	if len(rows) == 0 {
		return rep
	}
	rep.Workload = rows[0].Workload
	byRate := map[float64][]RunResult{}
	for _, r := range rows {
		byRate[r.OfferedRPS] = append(byRate[r.OfferedRPS], r)
	}
	var rates []float64
	for k := range byRate {
		rates = append(rates, k)
	}
	sort.Float64s(rates)

	var levels [][]Point
	var flat []Point
	tMin, tMax := int64(math.MaxInt64), int64(0)
	for _, rate := range rates {
		rs := byRate[rate]
		l := Level{OfferedRPS: rate, Repeats: len(rs)}
		var xs, ns []float64
		for _, r := range rs {
			x := r.GoodputRPS
			if metric == "achieved" {
				x = r.AchievedRPS
			}
			xs = append(xs, x)
			ns = append(ns, r.Concurrency)
			l.P50Resp += r.ResponseMs.P50
			l.P99Resp += r.ResponseMs.P99
			l.P50Serv += r.ServiceMs.P50
			l.P99Serv += r.ServiceMs.P99
			l.Shed429 += r.Status429
			l.Err5xx += r.Status5xx
			l.Abandoned += r.Abandoned
			l.EngineCPUms += r.CPU.EngineDemand
			l.PGCPUms += r.CPU.PGDemand
			l.GenShare = math.Max(l.GenShare, r.CPU.GenShare)
			l.IdleShare += r.CPU.IdleShare
			l.StealShare = math.Max(l.StealShare, r.CPU.StealShare)
			if r.StartUnixMs < tMin {
				tMin = r.StartUnixMs
			}
			if r.EndUnixMs > tMax {
				tMax = r.EndUnixMs
			}
			if r.Verdict != nil && r.Verdict.Dominant != "" {
				l.Verdict = r.Verdict.Dominant
				l.VerdictMix = mixString(r.Verdict.Distribution)
			}
		}
		n := float64(len(rs))
		l.X, l.XCV = meanCV(xs)
		l.N = mean(ns)
		l.P50Resp /= n
		l.P99Resp /= n
		l.P50Serv /= n
		l.P99Serv /= n
		l.EngineCPUms /= n
		l.PGCPUms /= n
		l.IdleShare /= n
		l.COGapP99 = l.P99Resp - l.P99Serv
		rep.Levels = append(rep.Levels, l)

		var reps []Point
		for i := range rs {
			reps = append(reps, Point{N: ns[i], X: xs[i]})
			flat = append(flat, Point{N: ns[i], X: xs[i]})
		}
		levels = append(levels, reps)
	}

	u, err := Fit(flat)
	fitErr := ""
	if err != nil {
		fitErr = "the USL was NOT fitted: " + err.Error() +
			" — with no usable (concurrency, throughput) pair there is no curve to fit, and printing zeros for γ, α and β would be a fabricated model, not a result"
	}
	if err == nil {
		rep.USL = u
		rep.NMax = jsonFloat(u.NMax())
		rep.XMax = jsonFloat(u.XMax())
		rep.Retrograde = u.Retrograde()
		rep.Boot = BootstrapFit(levels, resamples, 0.95, 20260829)
	}

	// Trust gate.
	tr := Trust{R2: rep.USL.R2, Points: len(rep.Levels)}
	if fitErr != "" {
		tr.Reasons = append(tr.Reasons, fitErr)
	}
	for _, l := range rep.Levels {
		if l.XCV > tr.WorstCV {
			tr.WorstCV = l.XCV
		}
	}
	if !math.IsInf(float64(rep.NMax), 0) {
		for _, l := range rep.Levels {
			if l.N > float64(rep.NMax) {
				tr.PastPeak = true
			}
		}
	}
	tr.Trustworthy = true
	if tr.Points < 6 {
		tr.Trustworthy = false
		tr.Reasons = append(tr.Reasons, fmt.Sprintf("%d load levels — the practice is ≥ 6", tr.Points))
	}
	if tr.R2 < 0.9 {
		tr.Trustworthy = false
		tr.Reasons = append(tr.Reasons, fmt.Sprintf("R² = %.3f < 0.90 — the law does not describe these data", tr.R2))
	}
	if tr.WorstCV > 0.05 {
		tr.Trustworthy = false
		tr.Reasons = append(tr.Reasons, fmt.Sprintf("worst between-repeat CV = %.1f%% > 5%% — the host is too noisy at some level", tr.WorstCV*100))
	}
	if !tr.PastPeak {
		tr.Reasons = append(tr.Reasons, "no measured level is past the fitted peak — N_max is an EXTRAPOLATION, not an observation")
	}
	rep.Trust = tr

	// Service demand, from the busiest level that still answered 2xx.
	var best Level
	for _, l := range rep.Levels {
		if l.EngineCPUms > 0 && l.X > best.X {
			best = l
		}
	}
	rep.Demand = DemandView{
		EngineMs: best.EngineCPUms, PGMs: best.PGCPUms,
		TotalMs: best.EngineCPUms + best.PGCPUms,
		Note:    "service demand law X_max = C/D — an UPPER bound: it assumes the whole CPU is free for the app and that nothing else (the load generator, the box's other processes) competes for it.",
	}
	if d := rep.Demand.TotalMs; d > 0 {
		rep.Demand.CeilingX = 1000 / d
		rep.Demand.Ceiling2X = 2000 / d
	}

	// M/M/1 overlay from the lowest-load service time.
	if len(rep.Levels) > 0 {
		s := rep.Levels[0].P50Serv
		rep.Queue = QueueView{ServiceMs: s}
		if s > 0 {
			mu := 1000 / s
			rep.Queue.MuRPS = mu
			rep.Queue.Knee70RPS = 0.7 * mu
			rep.Queue.Knee70Ms = s / 0.3
			rep.Queue.Knee80Ms = s / 0.2
			rep.Queue.PlanningRPS = 0.7 * math.Min(mu, nonInf(float64(rep.XMax), mu))
		}
	}

	// The user translation. Response time at the planning point is the p50 of
	// the level closest to it — measured, never modelled.
	for _, t := range strings.Split(thinks, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		d, err := time.ParseDuration(t)
		if err != nil {
			continue
		}
		lam := rep.Queue.PlanningRPS
		if lam <= 0 {
			lam = float64(rep.XMax)
		}
		r := closestLevel(rep.Levels, lam).P50Resp
		p := Profile{Name: profileName(d), ThinkTime: t, RespTimeMs: r}
		p.Users = lam * (d.Seconds() + r/1000)
		if !math.IsNaN(rep.Boot.XMax.Lo) {
			p.UsersLo = 0.7 * rep.Boot.XMax.Lo * (d.Seconds() + r/1000)
			p.UsersHi = 0.7 * rep.Boot.XMax.Hi * (d.Seconds() + r/1000)
		}
		rep.Profiles = append(rep.Profiles, p)
	}

	for _, l := range rep.Levels {
		if l.Verdict != "" {
			rep.Sequence = append(rep.Sequence, SeqEntry{OfferedRPS: l.OfferedRPS, Verdict: l.Verdict, Mix: l.VerdictMix})
		}
	}

	// Where the generator ran is read off the data, not asserted: engine
	// CPU-seconds are measurable only when the engine shares the box (they
	// come from local /proc). Zero engine CPU across every row means the
	// generator had its own host — the isolated-lab mode (LAB-CAPACIDAD-S2).
	genOn := "its own host (isolated lab); the CPU columns describe the GENERATOR box"
	for _, r := range rows {
		if r.CPU.EngineS > 0 {
			genOn = "same host as the engine (declared confound)"
			break
		}
	}
	rep.Conditions = Conditions{
		CPUs: rows[0].CPU.CPUs, GeneratorOn: genOn,
		Started: time.UnixMilli(tMin).UTC().Format(time.RFC3339),
		Ended:   time.UnixMilli(tMax).UTC().Format(time.RFC3339),
	}
	for _, l := range rep.Levels {
		if l.GenShare > rep.Conditions.WorstGenCPU {
			rep.Conditions.WorstGenCPU = l.GenShare
		}
		if l.StealShare > rep.Conditions.WorstSteal {
			rep.Conditions.WorstSteal = l.StealShare
		}
	}
	return rep
}

func profileName(d time.Duration) string {
	switch {
	case d == 0:
		return "burst (no pause between requests)"
	case d <= 5*time.Second:
		return "active use (a request every " + d.String() + ")"
	default:
		return "browsing (a request every " + d.String() + ")"
	}
}

func closestLevel(ls []Level, x float64) Level {
	if len(ls) == 0 {
		return Level{}
	}
	best, bd := ls[0], math.Inf(1)
	for _, l := range ls {
		if d := math.Abs(l.X - x); d < bd {
			best, bd = l, d
		}
	}
	return best
}

func nonInf(v, fallback float64) float64 {
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return fallback
	}
	return v
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func meanCV(v []float64) (float64, float64) {
	m := mean(v)
	if len(v) < 2 || m == 0 {
		return m, 0
	}
	var s float64
	for _, x := range v {
		s += (x - m) * (x - m)
	}
	return m, math.Sqrt(s/float64(len(v)-1)) / m
}

func mixString(d map[string]int) string {
	if len(d) == 0 {
		return ""
	}
	type kv struct {
		k string
		n int
	}
	var out []kv
	for k, n := range d {
		out = append(out, kv{k, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].n > out[j].n })
	var b []string
	for _, e := range out {
		b = append(b, fmt.Sprintf("%s×%d", e.k, e.n))
	}
	return strings.Join(b, " ")
}

// Markdown renders the report exactly as it goes into the session evidence.
func (r Report) Markdown() string {
	var b strings.Builder
	f := func(s string, a ...any) { fmt.Fprintf(&b, s, a...) }
	f("## Capacity — %s (%s as throughput)\n\n", r.Workload, r.Metric)
	f("| offered | N (Little) | X rps | CV | p50 resp | p99 resp | p99 serv | CO gap p99 | 429 | 5xx | aband | engine ms CPU/req | pg ms CPU/req | gen CPU | steal | verdict |\n")
	f("|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|:--|\n")
	for _, l := range r.Levels {
		f("| %.0f | %.2f | %.1f | %.1f%% | %.2f | %.2f | %.2f | %.2f | %d | %d | %d | %.3f | %.3f | %.0f%% | %.0f%% | %s |\n",
			l.OfferedRPS, l.N, l.X, l.XCV*100, l.P50Resp, l.P99Resp, l.P99Serv, l.COGapP99,
			l.Shed429, l.Err5xx, l.Abandoned, l.EngineCPUms, l.PGCPUms, l.GenShare*100, l.StealShare*100, l.Verdict)
	}
	if r.USL.N == 0 {
		f("\n**USL fit** — not fitted.\n")
		for _, s := range r.Trust.Reasons {
			f("  - %s\n", s)
		}
		f("\n**Conditions** — %d CPU · generator %s · worst generator CPU share %.0f %% · worst steal %.0f %% · %s → %s\n",
			r.Conditions.CPUs, r.Conditions.GeneratorOn, r.Conditions.WorstGenCPU*100, r.Conditions.WorstSteal*100,
			r.Conditions.Started, r.Conditions.Ended)
		return b.String()
	}
	f("\n**USL fit** — X(N) = γN / (1 + α(N−1) + βN(N−1))\n\n")
	f("- γ = %.2f rps  (CI %.2f – %.2f)\n", r.USL.Gamma, r.Boot.Gamma.Lo, r.Boot.Gamma.Hi)
	f("- α = %.4f  contention  (CI %.4f – %.4f)\n", r.USL.Alpha, r.Boot.Alpha.Lo, r.Boot.Alpha.Hi)
	f("- β = %.6f  coherency  (CI %.6f – %.6f)\n", r.USL.Beta, r.Boot.Beta.Lo, r.Boot.Beta.Hi)
	f("- R² = %.4f · RMSE = %.2f rps · %d points\n", r.USL.R2, r.USL.RMSE, r.USL.N)
	if math.IsInf(float64(r.NMax), 0) {
		f("- β = 0 → **no peak**: the law degenerates to Amdahl, a ceiling at γ/α = %.0f rps, never retrograde.\n", float64(r.XMax))
	} else {
		f("- **N_max = %.1f concurrent requests** (CI %.1f – %.1f) → **X_max = %.0f rps** (CI %.0f – %.0f)\n",
			float64(r.NMax), r.Boot.NMax.Lo, r.Boot.NMax.Hi, float64(r.XMax), r.Boot.XMax.Lo, r.Boot.XMax.Hi)
		f("- retrograde past the peak: **%v** (β > 0 means throughput goes DOWN under more load, not merely flat)\n", r.Retrograde)
	}
	f("\n**Trust** — %s\n", trustLine(r.Trust))
	for _, s := range r.Trust.Reasons {
		f("  - %s\n", s)
	}
	f("\n**Queueing cross-check (M/M/1)** — service time %.2f ms → µ = %.0f rps; at 70 %% utilisation (%.0f rps) the response time is already %.2f ms, at 80 %% it is %.2f ms. Planning point: **%.0f rps**.\n",
		r.Queue.ServiceMs, r.Queue.MuRPS, r.Queue.Knee70RPS, r.Queue.Knee70Ms, r.Queue.Knee80Ms, r.Queue.PlanningRPS)
	f("\n**Service demand (generator-free upper bound)** — engine %.3f ms CPU/request + PostgreSQL %.3f ms = %.3f ms → ceiling %.0f rps on one CPU, %.0f rps on two. %s\n",
		r.Demand.EngineMs, r.Demand.PGMs, r.Demand.TotalMs, r.Demand.CeilingX, r.Demand.Ceiling2X, r.Demand.Note)
	f("\n**Users** — at the planning rate of %.0f rps, under each DECLARED profile (Little: users = λ × (think time + response time)):\n\n", r.Queue.PlanningRPS)
	f("| profile | think time | response time | concurrent users | range |\n|:--|--:|--:|--:|:--|\n")
	for _, p := range r.Profiles {
		rng := "—"
		if p.UsersHi > 0 {
			rng = fmt.Sprintf("%.0f – %.0f", p.UsersLo, p.UsersHi)
		}
		f("| %s | %s | %.1f ms | **%.0f** | %s |\n", p.Name, p.ThinkTime, p.RespTimeMs, p.Users, rng)
	}
	if len(r.Sequence) > 0 {
		f("\n**Saturation sequence** — what the engine said about itself at each rung:\n\n")
		f("| offered rps | dominant verdict | tick mix |\n|--:|:--|:--|\n")
		for _, s := range r.Sequence {
			f("| %.0f | %s | %s |\n", s.OfferedRPS, s.Verdict, s.Mix)
		}
	}
	f("\n**Conditions** — %d CPU · generator %s · worst generator CPU share %.0f %% · worst steal %.0f %% · %s → %s\n",
		r.Conditions.CPUs, r.Conditions.GeneratorOn, r.Conditions.WorstGenCPU*100, r.Conditions.WorstSteal*100,
		r.Conditions.Started, r.Conditions.Ended)
	f("\n> The USL is a model, not a crystal ball (Gunther): it cannot predict an intrinsic pathology or a broken measurement, and where the data diverge from it that is a fact about the system, said and not smoothed. Every number above is an estimate until it is checked against real traffic.\n")
	return b.String()
}

func trustLine(t Trust) string {
	if t.Trustworthy {
		return fmt.Sprintf("**publishable** (R² = %.3f ≥ 0.90, worst CV %.1f %% ≤ 5 %%, %d levels ≥ 6)", t.R2, t.WorstCV*100, t.Points)
	}
	return fmt.Sprintf("**NOT publishable as a ceiling** (R² = %.3f, worst CV %.1f %%, %d levels)", t.R2, t.WorstCV*100, t.Points)
}
