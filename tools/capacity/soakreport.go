package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// The endurance verdict.
//
// What a soak looks for cannot be seen in a value, only in a SLOPE. A live
// heap that ends the run 40 MB above where it started is a leak whether or not
// any single reading looked alarming; a heap that saws up and down between the
// same two bounds for eight hours is a healthy garbage collector, however
// large its peaks. So every criterion here is a trend over the run:
//
//   - the POST-GC FLOOR of the live heap (the minimum within each hour, which
//     is the closest thing to "what the process could not release"), regressed
//     against time and reported in MB/hour;
//   - the same for RSS and for the goroutine count, because a goroutine leak
//     shows there before it shows in the heap;
//   - the p99 of the FIRST hour against the p99 of the LAST hour, as a
//     percentage — latency degradation that a mean would hide.
//
// The thresholds this session found quoted in the literature (a heap floor
// rising more than ~20 MB/hour, a p99 drifting more than ~15 %) come from a
// vendor blog and are NOT measured, so they are not used as a verdict here.
// The PRINCIPLE — judge a soak by its trend, never by a point — is sound and
// is what this report applies; the numbers are printed so a reader can apply
// whatever threshold they can defend. What this report will say on its own is
// only what the data support: the direction and size of each slope, with the
// spread around it.

type soakTrend struct {
	Name       string  `json:"name"`
	Unit       string  `json:"unit"`
	First      float64 `json:"first"`
	Last       float64 `json:"last"`
	SlopePerHr float64 `json:"slope_per_hour"`
	R2         float64 `json:"r2"`
	Min        float64 `json:"min"`
	Max        float64 `json:"max"`
}

type soakReport struct {
	Slices        int                 `json:"slices"`
	Hours         float64             `json:"hours"`
	Requests      int64               `json:"requests"`
	OfferedRPS    float64             `json:"offered_rps"`
	GoodputMedian float64             `json:"goodput_median_rps"`
	Errors        int64               `json:"errors"`
	Status5xx     int64               `json:"status_5xx"`
	P50FirstHour  float64             `json:"p50_first_hour_ms"`
	P50LastHour   float64             `json:"p50_last_hour_ms"`
	P99FirstHour  float64             `json:"p99_first_hour_ms"`
	P99LastHour   float64             `json:"p99_last_hour_ms"`
	P99DriftPct   float64             `json:"p99_drift_pct"`
	P50DriftPct   float64             `json:"p50_drift_pct"`
	Trends        []soakTrend         `json:"trends"`
	Verdicts      map[string]int      `json:"verdicts"`
	Attributions  map[string]int      `json:"engine_attribution"`
	Notes         []string            `json:"notes"`
	Extra         map[string]struct{} `json:"-"`
}

func cmdSoakReport(args []string) {
	fs := flag.NewFlagSet("soakreport", flag.ExitOnError)
	in := fs.String("in", "soak.jsonl", "soak output JSONL, one row per slice")
	jsonOut := fs.String("json", "", "also write the report as JSON")
	_ = fs.Parse(args)
	rows, err := readRuns(*in, "")
	if err != nil {
		fatal(err)
	}
	if len(rows) < 4 {
		fatal(fmt.Errorf("%d slices — a trend needs more than that", len(rows)))
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StartUnixMs < rows[j].StartUnixMs })
	t0 := rows[0].StartUnixMs
	hours := func(r RunResult) float64 { return float64(r.StartUnixMs-t0) / 3600000 }
	rep := soakReport{
		Slices: len(rows), Hours: hours(rows[len(rows)-1]),
		OfferedRPS: rows[0].OfferedRPS,
		Verdicts:   map[string]int{}, Attributions: map[string]int{},
	}

	var goods []float64
	for _, r := range rows {
		rep.Requests += r.Completed
		rep.Errors += r.Errors
		rep.Status5xx += r.Status5xx
		goods = append(goods, r.GoodputRPS)
		if r.Verdict != nil && r.Verdict.Dominant != "" {
			rep.Verdicts[r.Verdict.Dominant]++
		}
		if r.Engine != nil && r.Engine.Attribution != "" {
			rep.Attributions[r.Engine.Attribution]++
		}
	}
	sort.Float64s(goods)
	rep.GoodputMedian = quantileOf(goods, 0.5)

	firstHour, lastHour := window(rows, hours, 0, 1), window(rows, hours, rep.Hours-1, rep.Hours+1e-9)
	rep.P50FirstHour, rep.P99FirstHour = medianOf(firstHour, p50of), medianOf(firstHour, p99of)
	rep.P50LastHour, rep.P99LastHour = medianOf(lastHour, p50of), medianOf(lastHour, p99of)
	if rep.P99FirstHour > 0 {
		rep.P99DriftPct = (rep.P99LastHour - rep.P99FirstHour) / rep.P99FirstHour * 100
	}
	if rep.P50FirstHour > 0 {
		rep.P50DriftPct = (rep.P50LastHour - rep.P50FirstHour) / rep.P50FirstHour * 100
	}

	add := func(name, unit string, get func(RunResult) (float64, bool), scale float64) {
		var xs, ys []float64
		for _, r := range rows {
			v, ok := get(r)
			if !ok {
				continue
			}
			xs = append(xs, hours(r))
			ys = append(ys, v/scale)
		}
		if len(ys) < 4 {
			return
		}
		slope, r2 := linreg(xs, ys)
		mn, mx := ys[0], ys[0]
		for _, v := range ys {
			mn, mx = math.Min(mn, v), math.Max(mx, v)
		}
		rep.Trends = append(rep.Trends, soakTrend{
			Name: name, Unit: unit, First: ys[0], Last: ys[len(ys)-1],
			SlopePerHr: slope, R2: r2, Min: mn, Max: mx,
		})
	}
	mib := 1024.0 * 1024
	add("live heap after GC", "MiB", func(r RunResult) (float64, bool) {
		if r.Engine == nil {
			return 0, false
		}
		return float64(r.Engine.HeapObjectsBytes), true
	}, mib)
	add("runtime memory total", "MiB", func(r RunResult) (float64, bool) {
		if r.Engine == nil {
			return 0, false
		}
		return float64(r.Engine.RuntimeTotalBytes), true
	}, mib)
	add("RSS", "MiB", func(r RunResult) (float64, bool) {
		if r.Engine == nil {
			return 0, false
		}
		return float64(r.Engine.RSSBytes), true
	}, mib)
	add("goroutines", "count", func(r RunResult) (float64, bool) {
		if r.Engine == nil {
			return 0, false
		}
		return float64(r.Engine.Goroutines), true
	}, 1)
	add("pool connections", "count", func(r RunResult) (float64, bool) {
		if r.Engine == nil {
			return 0, false
		}
		return float64(r.Engine.PoolTotal), true
	}, 1)
	add("p50 response", "ms", func(r RunResult) (float64, bool) { return r.ResponseMs.P50, true }, 1)
	add("p99 response", "ms", func(r RunResult) (float64, bool) { return r.ResponseMs.P99, true }, 1)
	add("engine CPU per request", "ms", func(r RunResult) (float64, bool) { return r.CPU.EngineDemand, true }, 1)

	rep.Notes = []string{
		"a soak is judged by SLOPE, never by a value: a heap that ends where it started is healthy however high its peaks, and one that climbs is a leak however low they are",
		"the heap slope here is over the per-slice reading, which is taken between slices and is therefore close to a post-GC floor, not a peak",
		"the thresholds commonly quoted for this (≈ 20 MB/h of heap, ≈ 15 % of p99 drift) come from a vendor blog and are NOT measured — the principle is used, the numbers are not",
	}

	fmt.Printf("## Endurance — %.1f h at %.0f rps offered, %d slices, %d requests\n\n", rep.Hours, rep.OfferedRPS, rep.Slices, rep.Requests)
	fmt.Printf("goodput median %.1f rps · transport errors %d · 5xx %d\n\n", rep.GoodputMedian, rep.Errors, rep.Status5xx)
	fmt.Printf("| signal | first | last | slope / hour | R² | min | max |\n|:--|--:|--:|--:|--:|--:|--:|\n")
	for _, t := range rep.Trends {
		fmt.Printf("| %s (%s) | %.2f | %.2f | **%+.3f** | %.2f | %.2f | %.2f |\n",
			t.Name, t.Unit, t.First, t.Last, t.SlopePerHr, t.R2, t.Min, t.Max)
	}
	fmt.Printf("\nlatency drift, first hour → last hour: p50 %.2f → %.2f ms (%+.1f %%) · p99 %.2f → %.2f ms (%+.1f %%)\n",
		rep.P50FirstHour, rep.P50LastHour, rep.P50DriftPct, rep.P99FirstHour, rep.P99LastHour, rep.P99DriftPct)
	if len(rep.Verdicts) > 0 {
		fmt.Printf("\nthe engine's own verdict over the slices: %s\n", mixString(rep.Verdicts))
	}
	fmt.Println()
	for _, n := range rep.Notes {
		fmt.Printf("> %s\n", n)
	}
	if *jsonOut != "" {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fatal(fmt.Errorf("encode report: %w", err))
		}
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil { //nolint:gosec
			fatal(err)
		}
	}
	_ = strings.TrimSpace
}

func window(rows []RunResult, hours func(RunResult) float64, lo, hi float64) []RunResult {
	var out []RunResult
	for _, r := range rows {
		if h := hours(r); h >= lo && h <= hi {
			out = append(out, r)
		}
	}
	return out
}

func p50of(r RunResult) float64 { return r.ResponseMs.P50 }
func p99of(r RunResult) float64 { return r.ResponseMs.P99 }

func medianOf(rows []RunResult, get func(RunResult) float64) float64 {
	if len(rows) == 0 {
		return math.NaN()
	}
	v := make([]float64, 0, len(rows))
	for _, r := range rows {
		v = append(v, get(r))
	}
	sort.Float64s(v)
	return quantileOf(v, 0.5)
}

// linreg returns the ordinary-least-squares slope of y on x and its R².
func linreg(x, y []float64) (slope, r2 float64) {
	n := float64(len(x))
	if n < 2 {
		return 0, 0
	}
	var sx, sy, sxx, sxy float64
	for i := range x {
		sx += x[i]
		sy += y[i]
		sxx += x[i] * x[i]
		sxy += x[i] * y[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0
	}
	slope = (n*sxy - sx*sy) / den
	intercept := (sy - slope*sx) / n
	mean := sy / n
	var ssr, sst float64
	for i := range x {
		f := intercept + slope*x[i]
		ssr += (y[i] - f) * (y[i] - f)
		sst += (y[i] - mean) * (y[i] - mean)
	}
	if sst > 0 {
		r2 = 1 - ssr/sst
	}
	return slope, r2
}
