package observability

import (
	"fmt"
	"math"
)

// The attribution verdict — the §4 table of the Module C spec, as code.
//
// It is DETERMINISTIC: a fixed set of rules over this tick's numbers and a
// healthy baseline, evaluated in a fixed priority order, each with a written
// threshold. No language model is on this path and none will be: a model may
// later NARRATE a verdict already computed here (numbers and causality
// supplied by this code, never introduced by the model) — noted, not built
// (docs/adr/ADR-030). The rows are ranked so the verdict names the cause
// FURTHEST from the operator's own code first: a plan's CPU quota explains a
// scheduler queue; GC explains CPU; a slow database explains a full pool.
//
// The row that matters most is the one that says "it is not you": p99 high
// while CPU, GC and the pool are healthy and the query stage is most of the
// request — the DATABASE (or the network to it) is the bottleneck, not
// Appximo. That distinction is the product.

// Attribution is the verdict vocabulary, exactly as the spec defines it.
type Attribution string

const (
	AttrCPUSaturated   Attribution = "cpu_saturated"
	AttrGCPressure     Attribution = "gc_pressure"
	AttrCPUThrottled   Attribution = "cpu_throttled"
	AttrPoolExhausted  Attribution = "pool_exhausted"
	AttrDBBound        Attribution = "db_bound"
	AttrMemoryPressure Attribution = "memory_pressure"
	AttrLockContention Attribution = "lock_contention"
	AttrHealthy        Attribution = "healthy"
)

// Attributions lists the eight values in the priority order the rules use.
var Attributions = []Attribution{
	AttrCPUThrottled, AttrMemoryPressure, AttrGCPressure, AttrCPUSaturated,
	AttrPoolExhausted, AttrDBBound, AttrLockContention, AttrHealthy,
}

// Signal is one measured value the rule read, with the threshold it was
// compared against — the evidence line under the verdict.
type Signal struct {
	Name      string  `json:"name"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit"`
	Fired     bool    `json:"fired"`
}

// Verdict is the human-readable side of an Attribution.
type Verdict struct {
	Attribution Attribution `json:"attribution"`
	// Owner says whose problem it is: "appximo" (code / memory), "host"
	// (the plan's quota, the box's RAM), "database" (pool config / the DB /
	// the network to it), "none".
	Owner string `json:"owner"`
	// Reason is one sentence an operator can act on.
	Reason string `json:"reason"`
	// Signals are every rule input that was evaluated (fired or not), so the
	// operator sees the numbers behind the sentence.
	Signals []Signal `json:"signals"`
	// Also lists lower-priority attributions whose rule ALSO fired this tick.
	Also []Attribution `json:"also,omitempty"`
	// LatencyHigh is the gate most code-side rules share: p99 over the
	// absolute floor, or a multiple of the healthy baseline.
	LatencyHigh bool    `json:"latency_high"`
	BaselineP99 float64 `json:"baseline_p99_ms"`
}

// AttributionThresholds are the rule constants. Every one is documented in
// ADR-030 with why; the engine maps APPXIMO_SELFMON_P99_MS onto HighP99Ms
// (the one an operator most plausibly tunes: "what is slow for MY app").
type AttributionThresholds struct {
	HighP99Ms         float64 // absolute "slow" floor (default 50 ms)
	BaselineRise      float64 // p99 ≥ BaselineRise × healthy baseline is "slow" too (default 3×), if ≥ MinRiseMs
	MinRiseMs         float64 // (default 10 ms)
	MinRPS            float64 // below this the window is idle → healthy (default 1)
	ThrottledFraction float64 // throttled_usec / interval ≥ this → cpu_throttled (default 0.02: 20 ms of every second stopped by the quota is a p99 the operator feels)
	MemUseFraction    float64 // memory.current / memory.max ≥ this → memory_pressure (default 0.90)
	MemPSISome10      float64 // memory some avg10 ≥ this % → memory_pressure (default 10)
	GCCPUFraction     float64 // gc cpu / busy cpu ≥ this → gc_pressure (default 0.25)
	GCPauseP99Ms      float64 // gc pause p99 ≥ this with ≥ GCCyclesPerS → gc_pressure (default 5)
	GCCyclesPerS      float64 // (default 2)
	SchedLatP99Ms     float64 // sched latency p99 ≥ max(this, 5 % of the request p99) → cpu_saturated (default 2 ms: a 1-vCPU box shared with anything shows ~1 ms wakeup latency that explains nothing)
	CPUPSISome10      float64 // cpu some avg10 ≥ this % corroborates (default 10)
	CPUBusyFraction   float64 // busy cpu / (interval × GOMAXPROCS) ≥ this corroborates when PSI is unavailable (default 0.85)
	PoolWaitFraction  float64 // empty_acquire_wait / interval ≥ this → pool_exhausted even without latency_high (default 0.10)
	QueryShare        float64 // query p99 / request p99 ≥ this → db_bound (default 0.5)
	MutexWaitFraction float64 // mutex wait / interval ≥ this → lock_contention (default 0.10)
}

func (t AttributionThresholds) withDefaults() AttributionThresholds {
	def := func(v *float64, d float64) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.HighP99Ms, 50)
	def(&t.BaselineRise, 3)
	def(&t.MinRiseMs, 10)
	def(&t.MinRPS, 1)
	def(&t.ThrottledFraction, 0.02)
	def(&t.MemUseFraction, 0.90)
	def(&t.MemPSISome10, 10)
	def(&t.GCCPUFraction, 0.25)
	def(&t.GCPauseP99Ms, 5)
	def(&t.GCCyclesPerS, 2)
	def(&t.SchedLatP99Ms, 2)
	def(&t.CPUPSISome10, 10)
	def(&t.CPUBusyFraction, 0.85)
	def(&t.PoolWaitFraction, 0.10)
	def(&t.QueryShare, 0.5)
	def(&t.MutexWaitFraction, 0.10)
	return t
}

// attribute evaluates the rules for one tick. baselineP99 is the EWMA of the
// p99 over previous HEALTHY ticks (0 = none yet). With evidence=false (the
// tick) it allocates nothing; with evidence=true (the readers) it also fills
// Signals and Also — a pure function of the same numbers, so the two calls
// agree by construction.
func attribute(s *ResourceSnapshot, t AttributionThresholds, baselineP99 float64, evidence bool) (Attribution, Verdict) {
	secs := float64(s.IntervalMs) / 1000
	if secs <= 0 {
		secs = 1
	}
	v := Verdict{BaselineP99: baselineP99}
	if evidence {
		v.Signals = make([]Signal, 0, 24)
	}
	sig := func(name string, val, thr float64, unit string, fired bool) {
		if evidence {
			v.Signals = append(v.Signals, Signal{Name: name, Value: val, Threshold: thr, Unit: unit, Fired: fired})
		}
	}
	rq := s.Request
	rt := s.Runtime
	ps := s.Process
	pr := s.Pressure
	db := s.DBClient

	// ── the shared gate: is the app slow right now? ────────────────────────
	idle := rq.RPS < t.MinRPS
	p99 := rq.LatencyP99Ms
	riseGate := baselineP99 > 0 && p99 >= t.BaselineRise*baselineP99 && p99 >= t.MinRiseMs
	latencyHigh := !idle && (p99 >= t.HighP99Ms || riseGate)
	v.LatencyHigh = latencyHigh
	sig("request_p99_ms", p99, t.HighP99Ms, "ms", p99 >= t.HighP99Ms)
	if baselineP99 > 0 {
		sig("request_p99_over_baseline", safeDiv(p99, baselineP99), t.BaselineRise, "×", riseGate)
	}

	// ── each rule, evaluated always (the signals are the evidence), then ranked ──
	throttledFrac := float64(ps.CPUThrottledDelta) / (secs * 1e6)
	cpuThrottled := ps.Source == "cgroup" && ps.CPUNrThrottledDlt > 0 && throttledFrac >= t.ThrottledFraction
	sig("cgroup_cpu_throttled_fraction", throttledFrac, t.ThrottledFraction, "fraction of interval", cpuThrottled)

	memFrac := 0.0
	if ps.MemMaxBytes > 0 {
		memFrac = float64(ps.MemCurrentBytes) / float64(ps.MemMaxBytes)
	}
	limitFrac := 0.0
	if rt.GOMEMLIMITBytes > 0 && rt.GOMEMLIMITBytes < math.MaxInt64 {
		limitFrac = float64(rt.HeapObjectsBytes) / float64(rt.GOMEMLIMITBytes)
	}
	memPressure := memFrac >= t.MemUseFraction || (pr.Source != "unavailable" && pr.Memory.SomeAvg10 >= t.MemPSISome10) || limitFrac >= t.MemUseFraction
	sig("cgroup_memory_use_fraction", memFrac, t.MemUseFraction, "fraction of memory.max", memFrac >= t.MemUseFraction)
	sig("psi_memory_some_avg10", pr.Memory.SomeAvg10, t.MemPSISome10, "%", pr.Source != "unavailable" && pr.Memory.SomeAvg10 >= t.MemPSISome10)
	if limitFrac > 0 {
		sig("heap_over_gomemlimit_fraction", limitFrac, t.MemUseFraction, "fraction of GOMEMLIMIT", limitFrac >= t.MemUseFraction)
	}

	gcCycles := float64(rt.GCCyclesDelta) / secs
	gcPauseMs := rt.GCPauseTotalP99S * 1000
	gcHot := rt.GCCPUFraction >= t.GCCPUFraction || (gcPauseMs >= t.GCPauseP99Ms && gcCycles >= t.GCCyclesPerS)
	gcPressure := gcHot && (latencyHigh || rt.GCCPUFraction >= 1.6*t.GCCPUFraction)
	sig("gc_cpu_fraction", rt.GCCPUFraction, t.GCCPUFraction, "fraction of busy CPU", rt.GCCPUFraction >= t.GCCPUFraction)
	sig("gc_pause_p99_ms", gcPauseMs, t.GCPauseP99Ms, "ms", gcPauseMs >= t.GCPauseP99Ms && gcCycles >= t.GCCyclesPerS)
	sig("gc_cycles_per_s", gcCycles, t.GCCyclesPerS, "/s", gcCycles >= t.GCCyclesPerS)

	// The CPU wait must be MATERIAL: at least the floor AND 5 % of the request
	// p99. The lock provocation on this box read sched p99 1.0–1.5 ms with PSI
	// 50 % — the wakeup latency of a vCPU shared with the load generator — next
	// to a 690 ms p99 caused by a mutex; naming the CPU there would have hidden
	// the lock. A wait that explains < 5 % of the latency is not the verdict.
	schedMs := rt.SchedLatencyP99S * 1000
	schedFloor := math.Max(t.SchedLatP99Ms, 0.05*p99)
	cpuCorroborated := (pr.Source != "unavailable" && pr.CPU.SomeAvg10 >= t.CPUPSISome10) || (pr.Source == "unavailable" && rt.CPUBusyFraction >= t.CPUBusyFraction)
	cpuHot := schedMs >= schedFloor && cpuCorroborated
	cpuSaturated := cpuHot && (latencyHigh || schedMs >= 5*t.SchedLatP99Ms)
	sig("sched_latency_p99_ms", schedMs, schedFloor, "ms", schedMs >= schedFloor)
	sig("psi_cpu_some_avg10", pr.CPU.SomeAvg10, t.CPUPSISome10, "%", pr.Source != "unavailable" && pr.CPU.SomeAvg10 >= t.CPUPSISome10)
	sig("cpu_busy_fraction", rt.CPUBusyFraction, t.CPUBusyFraction, "fraction of GOMAXPROCS", rt.CPUBusyFraction >= t.CPUBusyFraction)

	// The pool is judged by the TIME goroutines spent waiting for a connection
	// during the tick (EmptyAcquireWaitTime delta, summed over waiters — it can
	// exceed the interval) and by how many asked and found none — never by the
	// instantaneous acquired/max alone: the first provocation (2 connections,
	// a 40 ms database) showed the pool oscillating between 1/2 and 2/2 while
	// 3 empty acquires per tick queued behind it, and the snapshot at the tick
	// boundary read 1/2 "healthy". Saturated at the instant is corroboration.
	poolWaitFrac := db.EmptyAcquireWaitDelta / (secs * 1000)
	poolExhausted := db.EmptyAcquireDelta > 0 &&
		(poolWaitFrac >= t.PoolWaitFraction || (latencyHigh && (db.Saturated || poolWaitFrac >= t.PoolWaitFraction/5)))
	sig("pool_acquired_of_max", float64(db.AcquiredConns), float64(db.MaxConns), "connections", db.Saturated)
	sig("pool_empty_acquire_delta", float64(db.EmptyAcquireDelta), 1, "count", db.EmptyAcquireDelta > 0)
	sig("pool_empty_acquire_wait_fraction", poolWaitFrac, t.PoolWaitFraction, "fraction of interval", poolWaitFrac >= t.PoolWaitFraction)

	// query p99 over request p99, capped at 1: the query histogram covers only
	// the requests that ran a query (a cache hit runs none), so its p99 can
	// exceed the request p99 — "100 %" is the honest reading, not "130 %".
	queryShare := math.Min(1, safeDiv(db.QueryLatencyP99Ms, p99))
	dbBound := latencyHigh && db.QueryCount > 0 && queryShare >= t.QueryShare && !cpuHot && !gcHot
	sig("query_p99_share_of_request_p99", queryShare, t.QueryShare, "fraction", queryShare >= t.QueryShare && db.QueryCount > 0)
	sig("query_p99_ms", db.QueryLatencyP99Ms, 0, "ms", false)

	mutexFrac := rt.MutexWaitDeltaS / secs
	lockContention := mutexFrac >= t.MutexWaitFraction
	sig("mutex_wait_fraction", mutexFrac, t.MutexWaitFraction, "blocked-seconds per second", lockContention)

	// ── ranking: the cause furthest from the operator's code wins ──────────
	fired := []struct {
		a  Attribution
		ok bool
	}{
		{AttrCPUThrottled, cpuThrottled},
		{AttrMemoryPressure, memPressure},
		{AttrGCPressure, gcPressure},
		{AttrCPUSaturated, cpuSaturated},
		{AttrPoolExhausted, poolExhausted},
		{AttrDBBound, dbBound},
		{AttrLockContention, lockContention},
	}
	first := AttrHealthy
	for _, f := range fired {
		if !f.ok {
			continue
		}
		if first == AttrHealthy {
			first = f.a
		} else if evidence {
			v.Also = append(v.Also, f.a)
		}
	}
	v.Attribution = first
	v.Owner = ownerOf(first, memFrac >= t.MemUseFraction, limitFrac >= t.MemUseFraction)
	// The sentence is NOT written here: fmt allocates, and the tick allocates
	// nothing it does not have to. Describe() writes it on the reader's
	// goroutine (Latest / Series), from the same numbers.
	return first, v
}

// ownerOf maps a verdict to whose problem it is (memory_pressure depends on
// which of its three signals fired: the cgroup limit / GOMEMLIMIT are the
// process's; host reclaim is the box's).
func ownerOf(a Attribution, cgroupMem, limitMem bool) string {
	switch a {
	case AttrCPUThrottled:
		return "host"
	case AttrMemoryPressure:
		if cgroupMem || limitMem {
			return "appximo"
		}
		return "host"
	case AttrGCPressure, AttrCPUSaturated, AttrLockContention:
		return "appximo"
	case AttrPoolExhausted, AttrDBBound:
		return "database"
	}
	return "none"
}

// Describe fills the read-side half of the verdict — the evidence (Signals,
// Also) and the sentence — from the snapshot's numbers, with the default
// thresholds. Idempotent; call it on a COPY of a ring slot (Latest and Series
// do, with the collector's own thresholds), never on the slot itself.
func (s *ResourceSnapshot) Describe() { s.describeWith(AttributionThresholds{}.withDefaults()) }

func (s *ResourceSnapshot) describeWith(t AttributionThresholds) {
	t = t.withDefaults()
	// Re-run the rules with evidence on: same numbers, same thresholds, same
	// baseline → the same attribution, now with its signals and its "also".
	a, v := attribute(s, t, s.Verdict.BaselineP99, true)
	if a != s.Attribution {
		// Cannot happen unless the thresholds changed between the tick and the
		// read (a different collector config): keep the tick's verdict — it is
		// the recorded one — and the evidence of the re-evaluation.
		v.Attribution = s.Attribution
	}
	s.Verdict = v
	secs := float64(s.IntervalMs) / 1000
	if secs <= 0 {
		secs = 1
	}
	rq, rt, ps, db := s.Request, s.Runtime, s.Process, s.DBClient
	idle := rq.RPS < t.MinRPS
	memFrac, limitFrac := 0.0, 0.0
	if ps.MemMaxBytes > 0 {
		memFrac = float64(ps.MemCurrentBytes) / float64(ps.MemMaxBytes)
	}
	if rt.GOMEMLIMITBytes > 0 && rt.GOMEMLIMITBytes < math.MaxInt64 {
		limitFrac = float64(rt.HeapObjectsBytes) / float64(rt.GOMEMLIMITBytes)
	}
	s.Verdict.Owner, s.Verdict.Reason = explain(s.Attribution, s, t, s.Verdict.LatencyHigh, idle,
		float64(ps.CPUThrottledDelta)/(secs*1e6), memFrac, limitFrac,
		rt.SchedLatencyP99S*1000, rt.GCPauseTotalP99S*1000, float64(rt.GCCyclesDelta)/secs,
		db.EmptyAcquireWaitDelta/(secs*1000), math.Min(1, safeDiv(db.QueryLatencyP99Ms, rq.LatencyP99Ms)),
		rt.MutexWaitDeltaS/secs, s.Verdict.BaselineP99)
}

func safeDiv(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b
}

// explain writes the sentence and names the owner. English, like every
// engine message (C0-bis); the /admin view localizes nothing — the numbers
// are the language.
func explain(a Attribution, s *ResourceSnapshot, t AttributionThresholds, latencyHigh, idle bool,
	throttledFrac, memFrac, limitFrac, schedMs, gcPauseMs, gcCycles, poolWaitFrac, queryShare, mutexFrac, baseline float64) (owner, reason string) {
	rq, rt, ps, pr, db := s.Request, s.Runtime, s.Process, s.Pressure, s.DBClient
	switch a {
	case AttrCPUThrottled:
		q := "no quota"
		if ps.CPUQuotaUsec > 0 && ps.CPUPeriodUsec > 0 {
			q = fmt.Sprintf("cpu.max %.2f CPU", float64(ps.CPUQuotaUsec)/float64(ps.CPUPeriodUsec))
		}
		return "host", fmt.Sprintf("the cgroup's CPU quota is throttling this process — %.0f%% of the interval throttled (%d periods, %s); the plan's limit, not the code. Raise the quota or the plan, or lower GOMAXPROCS (%d) to the quota.",
			throttledFrac*100, ps.CPUNrThrottledDlt, q, rt.GOMAXPROCS)
	case AttrMemoryPressure:
		if limitFrac >= t.MemUseFraction && memFrac < t.MemUseFraction {
			return "appximo", fmt.Sprintf("the live heap is at %.0f%% of GOMEMLIMIT (%.0f of %.0f MiB) — the GC is running to stay under it; raise GOMEMLIMIT or the box's RAM.",
				limitFrac*100, mib(rt.HeapObjectsBytes), mib(rt.GOMEMLIMITBytes))
		}
		if memFrac >= t.MemUseFraction {
			return "appximo", fmt.Sprintf("memory.current is at %.0f%% of memory.max (%.0f of %.0f MiB) — an OOM-kill is imminent; memory pressure some=%.1f%%.",
				memFrac*100, mib(uint64(ps.MemCurrentBytes)), mib(uint64(ps.MemMaxBytes)), pr.Memory.SomeAvg10)
		}
		return "host", fmt.Sprintf("the host is reclaiming memory (memory PSI some avg10 = %.1f%%, RSS %.0f MiB) — the box is short of RAM (Postgres shares it), not this process alone.",
			pr.Memory.SomeAvg10, mib(uint64(ps.RSSBytes)))
	case AttrGCPressure:
		return "appximo", fmt.Sprintf("the garbage collector is taking %.0f%% of the busy CPU (%.1f cycles/s, STW p99 %.2f ms, live heap %.0f MiB) — allocation pressure in the code path; p99 %.1f ms.",
			rt.GCCPUFraction*100, gcCycles, gcPauseMs, mib(rt.HeapObjectsBytes), rq.LatencyP99Ms)
	case AttrCPUSaturated:
		corr := fmt.Sprintf("CPU PSI some avg10 %.1f%%", pr.CPU.SomeAvg10)
		if pr.Source == "unavailable" {
			corr = fmt.Sprintf("%.0f%% of GOMAXPROCS busy", rt.CPUBusyFraction*100)
		}
		return "appximo", fmt.Sprintf("runnable goroutines wait %.2f ms (p99) for a CPU — more work than CPU (%s, GOMAXPROCS %d, %.0f rps); this process, or the box's sizing. Check GOMAXPROCS against cpu.max.",
			schedMs, corr, rt.GOMAXPROCS, rq.RPS)
	case AttrPoolExhausted:
		why := "the pool is undersized for this load"
		if db.QueryLatencyP99Ms >= 0.5*rq.LatencyP99Ms && db.QueryCount > 0 {
			why = fmt.Sprintf("queries hold connections too long (query p99 %.1f ms) — the database is slow and the pool fills behind it", db.QueryLatencyP99Ms)
		}
		return "database", fmt.Sprintf("the connection pool is exhausted — %d of %d acquired at the tick, %d requests found no free connection, %.0f%% of the interval (summed over waiters) spent waiting for one; %s. Raise DB_MAX_CONNS only if Postgres has headroom.",
			db.AcquiredConns, db.MaxConns, db.EmptyAcquireDelta, poolWaitFrac*100, why)
	case AttrDBBound:
		return "database", fmt.Sprintf("the database is the bottleneck, not Appximo — the query stage is %.0f%% of the request p99 (query p99 %.1f ms of %.1f ms) while CPU (sched p99 %.2f ms), GC (%.0f%%) and the pool (%d/%d) are healthy. Look at the statement, its indexes, the Postgres box or the network to it.",
			queryShare*100, db.QueryLatencyP99Ms, rq.LatencyP99Ms, schedMs, rt.GCCPUFraction*100, db.AcquiredConns, db.MaxConns)
	case AttrLockContention:
		return "appximo", fmt.Sprintf("goroutines spend %.0f%% of wall time blocked on mutexes (%.3f s per second) — lock contention inside the process.",
			mutexFrac*100, mutexFrac)
	default:
		if idle {
			return "none", fmt.Sprintf("no traffic in this window (%.1f rps) — nothing to attribute.", rq.RPS)
		}
		if latencyHigh {
			return "none", fmt.Sprintf("p99 %.1f ms is high but no resource rule fired (CPU sched p99 %.2f ms, GC %.0f%%, pool %d/%d, query p99 %.1f ms, mutex %.0f%%) — the time is in the handler itself, or in an external call it makes.",
				rq.LatencyP99Ms, schedMs, rt.GCCPUFraction*100, db.AcquiredConns, db.MaxConns, db.QueryLatencyP99Ms, mutexFrac*100)
		}
		bl := ""
		if baseline > 0 {
			bl = fmt.Sprintf(", baseline %.1f ms", baseline)
		}
		return "none", fmt.Sprintf("healthy — %.0f rps, p99 %.1f ms%s, pool %d/%d, GC %.0f%%.", rq.RPS, rq.LatencyP99Ms, bl, db.AcquiredConns, db.MaxConns, rt.GCCPUFraction*100)
	}
}

func mib(b uint64) float64 { return float64(b) / (1024 * 1024) }

// WindowSummary aggregates a series into the load-test verdict: the dominant
// non-healthy attribution over the window (if non-healthy ticks are at least
// MinShare of the traffic-bearing ticks), the distribution, and the peaks —
// what the operator attaches to a report after a k6.
type WindowSummary struct {
	From         int64               `json:"from"`
	To           int64               `json:"to"`
	Ticks        int                 `json:"ticks"`
	TrafficTicks int                 `json:"traffic_ticks"`
	Dominant     Attribution         `json:"dominant"`
	Owner        string              `json:"owner"`
	Reason       string              `json:"reason"`
	Distribution map[Attribution]int `json:"distribution"`
	PeakRPS      float64             `json:"peak_rps"`
	PeakP99Ms    float64             `json:"peak_p99_ms"`
	PeakTick     *ResourceSnapshot   `json:"peak_tick,omitempty"`
	Requests     int64               `json:"requests"`
	Shed         int64               `json:"shed_429_503"`
	Errors5xx    int64               `json:"errors_5xx"`
}

// Summarize computes the window verdict over a series (oldest first).
func Summarize(series []ResourceSnapshot) WindowSummary {
	out := WindowSummary{Distribution: make(map[Attribution]int, 8), Dominant: AttrHealthy, Owner: "none"}
	if len(series) == 0 {
		out.Reason = "no ticks in the window"
		return out
	}
	out.From, out.To, out.Ticks = series[0].TS, series[len(series)-1].TS, len(series)
	var peak *ResourceSnapshot
	for i := range series {
		s := &series[i]
		out.Distribution[s.Attribution]++
		out.Requests += s.Request.Count
		out.Shed += s.Request.Status429 + s.Request.Status503
		out.Errors5xx += s.Request.Errors5xx
		if s.Request.RPS > out.PeakRPS {
			out.PeakRPS = s.Request.RPS
		}
		if s.Request.Count > 0 {
			out.TrafficTicks++
			if s.Request.LatencyP99Ms > out.PeakP99Ms {
				out.PeakP99Ms = s.Request.LatencyP99Ms
			}
		}
		if s.Attribution != AttrHealthy && (peak == nil || s.Request.LatencyP99Ms > peak.Request.LatencyP99Ms) {
			peak = s
		}
	}
	// Dominant = most frequent non-healthy verdict, if it covers ≥ 10 % of
	// the traffic-bearing ticks (a single stray tick is noise, not a verdict).
	best, bestN := AttrHealthy, 0
	for _, a := range Attributions {
		if a == AttrHealthy {
			continue
		}
		if n := out.Distribution[a]; n > bestN {
			best, bestN = a, n
		}
	}
	if bestN > 0 && out.TrafficTicks > 0 && float64(bestN) >= 0.10*float64(out.TrafficTicks) && peak != nil {
		out.Dominant = best
		// The reason of the worst tick that carries the dominant verdict.
		for i := len(series) - 1; i >= 0; i-- {
			if series[i].Attribution == best {
				if peak.Attribution != best {
					peak = &series[i]
				}
				break
			}
		}
		for i := range series {
			if series[i].Attribution == best && series[i].Request.LatencyP99Ms >= peak.Request.LatencyP99Ms {
				peak = &series[i]
			}
		}
		cp := *peak
		out.PeakTick = &cp
		out.Owner, out.Reason = peak.Verdict.Owner, peak.Verdict.Reason
		return out
	}
	if out.TrafficTicks == 0 {
		out.Reason = "no traffic in the window"
	} else {
		out.Reason = fmt.Sprintf("healthy across %d traffic ticks — peak %.0f rps, peak p99 %.1f ms, %d requests, %d shed, %d 5xx.",
			out.TrafficTicks, out.PeakRPS, out.PeakP99Ms, out.Requests, out.Shed, out.Errors5xx)
	}
	return out
}
