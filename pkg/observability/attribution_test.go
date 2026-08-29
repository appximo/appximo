package observability

import (
	"strings"
	"testing"
)

// The eight verdicts, each from the numbers its spec row names — and the
// ranking when several fire. These are the rules as DATA; the provocation of
// each cause against a live engine is the session's Part C (documented in
// the ADR), not something a unit test can do.
func healthyTick() *ResourceSnapshot {
	return &ResourceSnapshot{
		IntervalMs: 1000,
		Request:    RequestStats{Count: 100, RPS: 100, LatencyP50Ms: 1.5, LatencyP95Ms: 4, LatencyP99Ms: 8},
		Runtime:    RuntimeStats{GOMAXPROCS: 2, Goroutines: 40, SchedLatencyP99S: 0.00005, GCCPUFraction: 0.02, GCCyclesDelta: 1, CPUBusyFraction: 0.2, GOMEMLIMITBytes: 1 << 62},
		Process:    ProcessStats{Source: "cgroup", MemCurrentBytes: 60 << 20, MemMaxBytes: -1, CPUQuotaUsec: -1},
		Pressure:   PressureStats{Source: "cgroup", CPU: PSILine{SomeAvg10: 1}, Memory: PSILine{SomeAvg10: 0}},
		DBClient:   DBClientStats{MaxConns: 10, AcquiredConns: 2, IdleConns: 8, QueryCount: 100, QueryLatencyP99Ms: 2},
	}
}

func TestAttribution_EightVerdicts(t *testing.T) {
	th := AttributionThresholds{}.withDefaults()
	cases := []struct {
		name  string
		mut   func(s *ResourceSnapshot)
		want  Attribution
		owner string
		words []string
	}{
		{"healthy", func(s *ResourceSnapshot) {}, AttrHealthy, "none", []string{"healthy"}},
		{"idle window is healthy", func(s *ResourceSnapshot) { s.Request = RequestStats{} }, AttrHealthy, "none", []string{"no traffic"}},
		{"cpu_throttled: cgroup quota, throttled 30% of the tick", func(s *ResourceSnapshot) {
			s.Process.CPUNrThrottledDlt = 8
			s.Process.CPUThrottledDelta = 300_000
			s.Process.CPUQuotaUsec, s.Process.CPUPeriodUsec = 20_000, 100_000
			s.Request.LatencyP99Ms = 120
			s.Runtime.SchedLatencyP99S = 0.02 // the queue the quota causes — must NOT win
			s.Pressure.CPU.SomeAvg10 = 40
		}, AttrCPUThrottled, "host", []string{"quota", "30%", "0.20 CPU"}},
		{"memory_pressure: memory.current at 95% of memory.max", func(s *ResourceSnapshot) {
			s.Process.MemMaxBytes = 100 << 20
			s.Process.MemCurrentBytes = 95 << 20
			s.Pressure.Memory.SomeAvg10 = 22
		}, AttrMemoryPressure, "appximo", []string{"OOM", "95%"}},
		{"memory_pressure: host reclaiming (PSI) with no cgroup limit", func(s *ResourceSnapshot) {
			s.Pressure.Memory.SomeAvg10 = 15
		}, AttrMemoryPressure, "host", []string{"reclaiming", "15.0%"}},
		{"gc_pressure: GC takes 45% of busy CPU under slow requests", func(s *ResourceSnapshot) {
			s.Runtime.GCCPUFraction = 0.45
			s.Runtime.GCCyclesDelta = 30
			s.Runtime.GCPauseTotalP99S = 0.003
			s.Request.LatencyP99Ms = 70
			s.Runtime.SchedLatencyP99S = 0.004 // GC also queues goroutines — gc must rank above cpu
			s.Pressure.CPU.SomeAvg10 = 30
		}, AttrGCPressure, "appximo", []string{"garbage collector", "45%"}},
		{"cpu_saturated: sched latency p99 6 ms + CPU PSI 35%", func(s *ResourceSnapshot) {
			s.Runtime.SchedLatencyP99S = 0.006
			s.Pressure.CPU.SomeAvg10 = 35
			s.Request.LatencyP99Ms = 65
		}, AttrCPUSaturated, "appximo", []string{"runnable goroutines", "6.00 ms"}},
		{"cpu_saturated without PSI: busy fraction corroborates", func(s *ResourceSnapshot) {
			s.Pressure.Source = "unavailable"
			s.Runtime.SchedLatencyP99S = 0.006 // ≥ max(2 ms, 5 % of 65 ms) — material
			s.Runtime.CPUBusyFraction = 0.95
			s.Request.LatencyP99Ms = 65
		}, AttrCPUSaturated, "appximo", []string{"95% of GOMAXPROCS"}},
		{"pool_exhausted: 10/10, empty acquires, 60% of the tick waiting", func(s *ResourceSnapshot) {
			s.DBClient.AcquiredConns, s.DBClient.IdleConns = 10, 0
			s.DBClient.Saturated = true
			s.DBClient.EmptyAcquireDelta = 40
			s.DBClient.EmptyAcquireWaitDelta = 600
			s.Request.LatencyP99Ms = 400
			s.DBClient.QueryLatencyP99Ms = 350 // a slow DB fills the pool — pool ranks first and SAYS it
		}, AttrPoolExhausted, "database", []string{"exhausted", "10 of 10", "queries hold connections"}},
		{"db_bound: p99 high, query is 90% of it, CPU/GC/pool healthy", func(s *ResourceSnapshot) {
			s.Request.LatencyP99Ms = 80
			s.DBClient.QueryLatencyP99Ms = 72
		}, AttrDBBound, "database", []string{"not Appximo", "90%"}},
		{"db_bound by relative rise: 3× the healthy baseline", func(s *ResourceSnapshot) {
			s.Request.LatencyP99Ms = 30 // under the 50 ms floor, but 3.75× the 8 ms baseline
			s.DBClient.QueryLatencyP99Ms = 27
		}, AttrDBBound, "database", []string{"not Appximo"}},
		{"NOT db_bound when CPU is hot: cpu wins", func(s *ResourceSnapshot) {
			s.Request.LatencyP99Ms = 80
			s.DBClient.QueryLatencyP99Ms = 72
			s.Runtime.SchedLatencyP99S = 0.006
			s.Pressure.CPU.SomeAvg10 = 35
		}, AttrCPUSaturated, "appximo", nil},
		{"lock_contention: 300 ms blocked per second", func(s *ResourceSnapshot) {
			s.Runtime.MutexWaitDeltaS = 0.3
		}, AttrLockContention, "appximo", []string{"mutex", "30%"}},
		{"lock_contention beats an IMMATERIAL cpu wait (the lock provocation's shape)", func(s *ResourceSnapshot) {
			s.Request.LatencyP99Ms = 690
			s.Runtime.MutexWaitDeltaS = 50
			s.Runtime.SchedLatencyP99S = 0.0013 // 1.3 ms of wakeup latency on a shared vCPU
			s.Pressure.CPU.SomeAvg10 = 50
		}, AttrLockContention, "appximo", []string{"mutex"}},
		{"slow but no rule: the handler itself", func(s *ResourceSnapshot) {
			s.Request.LatencyP99Ms = 90
			s.DBClient.QueryLatencyP99Ms = 3
		}, AttrHealthy, "none", []string{"handler itself"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := healthyTick()
			tc.mut(s)
			got, v := attribute(s, th, 8, false)
			s.Attribution, s.Verdict = got, v
			s.Describe()
			v = s.Verdict
			if got != tc.want {
				t.Fatalf("attribution = %s, want %s\nreason: %s\nalso: %v", got, tc.want, v.Reason, v.Also)
			}
			if v.Owner != tc.owner {
				t.Fatalf("owner = %s, want %s (%s)", v.Owner, tc.owner, v.Reason)
			}
			for _, w := range tc.words {
				if !strings.Contains(v.Reason, w) {
					t.Fatalf("reason %q must contain %q", v.Reason, w)
				}
			}
			if len(v.Signals) < 10 {
				t.Fatalf("every rule input must be listed as a signal, got %d", len(v.Signals))
			}
		})
	}
}

// When several rules fire the verdict is the one furthest from the code, and
// the rest are listed under Also — the operator sees the whole picture.
func TestAttribution_RankingAndAlso(t *testing.T) {
	th := AttributionThresholds{}.withDefaults()
	s := healthyTick()
	s.Request.LatencyP99Ms = 500
	s.Process.CPUNrThrottledDlt, s.Process.CPUThrottledDelta = 5, 200_000
	s.Runtime.GCCPUFraction = 0.5
	s.DBClient.AcquiredConns, s.DBClient.IdleConns, s.DBClient.Saturated = 10, 0, true
	s.DBClient.EmptyAcquireDelta, s.DBClient.EmptyAcquireWaitDelta = 3, 100
	got, v := attribute(s, th, 8, true)
	if got != AttrCPUThrottled {
		t.Fatalf("got %s", got)
	}
	if len(v.Also) != 2 || v.Also[0] != AttrGCPressure || v.Also[1] != AttrPoolExhausted {
		t.Fatalf("also = %v", v.Also)
	}
}

// The window summary: dominant non-healthy verdict over ≥ 10 % of the
// traffic ticks, with the worst tick's reason; healthy otherwise.
func TestSummarize(t *testing.T) {
	th := AttributionThresholds{}.withDefaults()
	var series []ResourceSnapshot
	for i := 0; i < 20; i++ {
		s := healthyTick()
		s.TS = int64(1000 + i)
		if i >= 10 && i < 15 {
			s.Request.LatencyP99Ms = 80 + float64(i)
			s.DBClient.QueryLatencyP99Ms = 70 + float64(i)
		}
		s.Attribution, s.Verdict = attribute(s, th, 8, false)
		s.Describe()
		series = append(series, *s)
	}
	sum := Summarize(series)
	if sum.Dominant != AttrDBBound || sum.Owner != "database" || sum.PeakTick == nil || sum.PeakTick.TS != 1014 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.Distribution[AttrDBBound] != 5 || sum.Distribution[AttrHealthy] != 15 || sum.Requests != 2000 {
		t.Fatalf("distribution = %v requests=%d", sum.Distribution, sum.Requests)
	}
	// One stray tick out of 20 is noise.
	for i := range series {
		if i != 3 {
			series[i].Attribution = AttrHealthy
		}
	}
	series[3].Attribution = AttrGCPressure
	if sum := Summarize(series); sum.Dominant != AttrHealthy {
		t.Fatalf("a single tick must not become the verdict: %+v", sum.Dominant)
	}
	if sum := Summarize(nil); sum.Dominant != AttrHealthy || sum.Reason == "" {
		t.Fatal("empty series")
	}
}
