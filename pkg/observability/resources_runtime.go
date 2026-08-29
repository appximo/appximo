package observability

import (
	"math"
	"runtime/metrics"
)

// Layer 1 — the Go runtime through runtime/metrics. The names below are the
// ones metrics.All() lists in go1.25 (verified in CENTINELA-C-S1; the spec's
// "/sched/threads/total:threads" does not exist — thread counts come from
// /proc/self/status in layer 2). Cumulative histograms (sched latencies, GC
// pauses) are diffed against the previous tick so the p99 is THIS tick's.
const (
	mSchedLat    = "/sched/latencies:seconds"
	mGCPause     = "/sched/pauses/total/gc:seconds"
	mOtherPause  = "/sched/pauses/total/other:seconds"
	mGoroutines  = "/sched/goroutines:goroutines"
	mGOMAXPROCS  = "/sched/gomaxprocs:threads"
	mMutexWait   = "/sync/mutex/wait/total:seconds"
	mCPUTotal    = "/cpu/classes/total:cpu-seconds"
	mCPUUser     = "/cpu/classes/user:cpu-seconds"
	mCPUGC       = "/cpu/classes/gc/total:cpu-seconds"
	mCPUIdle     = "/cpu/classes/idle:cpu-seconds"
	mCPUScavenge = "/cpu/classes/scavenge/total:cpu-seconds"
	mMemTotal    = "/memory/classes/total:bytes"
	mHeapObjects = "/memory/classes/heap/objects:bytes"
	mHeapGoal    = "/gc/heap/goal:bytes"
	mGOGC        = "/gc/gogc:percent"
	mGOMEMLIMIT  = "/gc/gomemlimit:bytes"
	mGCCycles    = "/gc/cycles/total:gc-cycles"
)

var runtimeMetricNames = []string{
	mSchedLat, mGCPause, mOtherPause, mGoroutines, mGOMAXPROCS, mMutexWait,
	mCPUTotal, mCPUUser, mCPUGC, mCPUIdle, mCPUScavenge,
	mMemTotal, mHeapObjects, mHeapGoal, mGOGC, mGOMEMLIMIT, mGCCycles,
}

// runtimeReader owns ONE []metrics.Sample, re-read in place every tick.
// metrics.Read re-uses a sample's *Float64Histogram (its Buckets/Counts
// slices) when the Value already holds one, so after the first read the
// histogram samples allocate nothing either; prevCounts keeps the previous
// tick's counts for the delta-p99.
type runtimeReader struct {
	samples   []metrics.Sample
	idx       map[string]int
	prev      map[string][]uint64 // previous cumulative counts per histogram metric
	prevMutex float64
	prevCPU   [4]float64 // total, gc, idle, user
	prevGC    uint64
	primed    bool
}

func newRuntimeReader() *runtimeReader {
	r := &runtimeReader{
		samples: make([]metrics.Sample, len(runtimeMetricNames)),
		idx:     make(map[string]int, len(runtimeMetricNames)),
		prev:    make(map[string][]uint64, 3),
	}
	for i, n := range runtimeMetricNames {
		r.samples[i].Name = n
		r.idx[n] = i
	}
	return r
}

func (r *runtimeReader) read() { metrics.Read(r.samples) }

func (r *runtimeReader) u64(name string) uint64 {
	s := r.samples[r.idx[name]]
	if s.Value.Kind() == metrics.KindUint64 {
		return s.Value.Uint64()
	}
	return 0
}

func (r *runtimeReader) f64(name string) float64 {
	s := r.samples[r.idx[name]]
	if s.Value.Kind() == metrics.KindFloat64 {
		return s.Value.Float64()
	}
	return 0
}

// histDelta computes the p50/p99/max of the events that landed between the
// previous read and this one for a cumulative Float64Histogram. Percentiles
// are the upper edge of the bucket where the cumulative delta count crosses
// the quantile (+Inf edges fall back to the lower edge). Zero events → zeros.
func (r *runtimeReader) histDelta(name string) (p50, p99, maxv float64) {
	s := r.samples[r.idx[name]]
	if s.Value.Kind() != metrics.KindFloat64Histogram {
		return 0, 0, 0
	}
	h := s.Value.Float64Histogram()
	prev := r.prev[name]
	if len(prev) != len(h.Counts) {
		prev = make([]uint64, len(h.Counts)) // once per metric (first read / shape change)
		r.prev[name] = prev
	}
	var total uint64
	for i, c := range h.Counts {
		if c >= prev[i] {
			total += c - prev[i]
		}
	}
	if total > 0 {
		var cum uint64
		got50, got99 := false, false
		for i, c := range h.Counts {
			d := uint64(0)
			if c >= prev[i] {
				d = c - prev[i]
			}
			if d == 0 {
				continue
			}
			cum += d
			edge := bucketEdge(h.Buckets, i)
			if !got50 && float64(cum) >= 0.50*float64(total) {
				p50, got50 = edge, true
			}
			if !got99 && float64(cum) >= 0.99*float64(total) {
				p99, got99 = edge, true
			}
			maxv = edge
		}
	}
	copy(prev, h.Counts)
	return p50, p99, maxv
}

// bucketEdge is the upper edge of bucket i, or the lower edge when the upper
// one is +Inf (the last bucket), or 0 when the lower one is -Inf.
func bucketEdge(buckets []float64, i int) float64 {
	if i+1 < len(buckets) && !math.IsInf(buckets[i+1], 0) {
		return buckets[i+1]
	}
	if i < len(buckets) && !math.IsInf(buckets[i], 0) {
		return buckets[i]
	}
	return 0
}

// fill reads the runtime and writes layer 1 into st for a tick of secs seconds.
func (r *runtimeReader) fill(st *RuntimeStats, secs float64) {
	r.read()
	st.SchedLatencyP50S, st.SchedLatencyP99S, _ = r.histDelta(mSchedLat)
	_, st.GCPauseTotalP99S, st.GCPauseTotalMaxS = r.histDelta(mGCPause)
	_, st.SchedOtherPauseS, _ = r.histDelta(mOtherPause)
	st.Goroutines = r.u64(mGoroutines)
	st.GOMAXPROCS = r.u64(mGOMAXPROCS)
	st.HeapObjectsBytes = r.u64(mHeapObjects)
	st.MemoryTotalBytes = r.u64(mMemTotal)
	st.HeapGoalBytes = r.u64(mHeapGoal)
	st.GOGCPercent = r.u64(mGOGC)
	st.GOMEMLIMITBytes = r.u64(mGOMEMLIMIT)
	st.GCCyclesTotal = r.u64(mGCCycles)
	st.MutexWaitTotalS = r.f64(mMutexWait)
	st.CPUTotalS = r.f64(mCPUTotal)
	st.CPUUserS = r.f64(mCPUUser)
	st.CPUGCS = r.f64(mCPUGC)
	st.CPUIdleS = r.f64(mCPUIdle)
	st.CPUScavengeS = r.f64(mCPUScavenge)
	if r.primed {
		st.MutexWaitDeltaS = math.Max(0, st.MutexWaitTotalS-r.prevMutex)
		st.CPUTotalDeltaS = math.Max(0, st.CPUTotalS-r.prevCPU[0])
		st.CPUGCDeltaS = math.Max(0, st.CPUGCS-r.prevCPU[1])
		idleDelta := math.Max(0, st.CPUIdleS-r.prevCPU[2])
		busy := st.CPUTotalDeltaS - idleDelta
		if busy > 0 {
			st.GCCPUFraction = st.CPUGCDeltaS / busy
			if st.GCCPUFraction > 1 {
				st.GCCPUFraction = 1
			}
		}
		if secs > 0 && st.GOMAXPROCS > 0 {
			st.CPUBusyFraction = busy / (secs * float64(st.GOMAXPROCS))
		}
		if st.GCCyclesTotal >= r.prevGC {
			st.GCCyclesDelta = st.GCCyclesTotal - r.prevGC
		}
	}
	r.prevMutex = st.MutexWaitTotalS
	r.prevCPU = [4]float64{st.CPUTotalS, st.CPUGCS, st.CPUIdleS, st.CPUUserS}
	r.prevGC = st.GCCyclesTotal
	r.primed = true
}
