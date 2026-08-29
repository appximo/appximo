package observability

// Self-observability of the engine's OWN resources (CENTINELA-C-S1, Module C
// of the Centinela package; ADR-030). The question it answers for the operator
// of an app built on Appximo: "how much do I, the engine, weigh on the box I
// run on — and when load arrives, am I the bottleneck, or is it the database,
// the network, or the plan's CPU quota?"
//
// THE CARDINAL PRINCIPLE (non-negotiable): the request path does ONLY cheap,
// lock-free-ish work — an atomic increment and a Record() into a pre-allocated
// HDR histogram. No I/O, no parsing, no global lock held across anything but a
// ~100 ns histogram write. Every READ of runtime/metrics, every cgroup /
// /proc / PSI file, every pgxpool.Stat() and every SQL statement happens in
// ONE collector goroutine on a timer, out of band. That goroutine allocates
// nothing per tick: the []metrics.Sample, the Float64Histograms, the file
// buffers and the ring slots are allocated once and re-used in place
// (pinned by TestResourceCollector_TickAllocatesNothing).
//
// Four layers, the exact fields the spec verified against the Go and pgx docs:
//  1. the Go runtime via runtime/metrics (resources_runtime.go)
//  2. the process / cgroup v2 with a /proc/self fallback (resources_linux.go)
//  3. host pressure via PSI, the cgroup's own preferred (resources_linux.go)
//  4. the database: the client side ALWAYS (pgxpool.Stat), the server side
//     ONLY when Postgres is local (resources_db.go)
//
// The product is not the numbers — anyone can expose runtime/metrics. It is
// the deterministic ATTRIBUTION verdict (attribution.go): "the bottleneck is
// the exhausted pool, not your code", "the database is the bottleneck, not
// Appximo". No language model is anywhere near that decision.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// ResourceRingSize bounds the correlation series: 900 ticks = 15 minutes in
// live mode (1 s) or 2.5 hours in background mode (10 s). A fixed array —
// never a slice that grows — so the collector's memory is known before the
// first request (the A-54 RSS proxy is stated in bytes for this reason).
const ResourceRingSize = 900

// Default cadences. Background is what a box pays 24/7; live is what the
// /admin correlation view asks for while an operator (or a k6) is looking,
// and it decays back to background LiveWindow after the last poll.
const (
	DefaultBackgroundInterval = 10 * time.Second
	DefaultLiveInterval       = 1 * time.Second
	DefaultLiveWindow         = 60 * time.Second
)

// ResourceConfig configures a ResourceCollector. Zero values take the defaults
// above; the engine maps APPXIMO_SELFMON_INTERVAL / _LIVE_INTERVAL onto it.
type ResourceConfig struct {
	BackgroundInterval time.Duration
	LiveInterval       time.Duration
	LiveWindow         time.Duration
	// DBServerLocal declares that Postgres runs on THIS host (loopback / unix
	// socket DSN): only then does the collector read pg_stat_* — a remote
	// database's internals are "not observable from the app", by design.
	DBServerLocal bool
	// DBServerEvery bounds the server-side probe cadence (default 10 s): it
	// borrows ONE pool connection with a 250 ms acquire timeout and gives up
	// (reported as skipped) rather than compete with requests for the pool.
	DBServerEvery time.Duration
	// Thresholds for the attribution rules; zero values take the documented
	// defaults (see attribution.go).
	Thresholds AttributionThresholds
}

// RuntimeStats is layer 1 — the Go runtime, per tick. Cumulative fields are
// the runtime's counters; *_delta / p99 fields are computed over THIS tick.
type RuntimeStats struct {
	SchedLatencyP99S float64 `json:"sched_latency_p99_s"`  // /sched/latencies:seconds, p99 of the tick's delta — the CPU-saturation signal
	GCPauseTotalP99S float64 `json:"gc_pause_total_p99_s"` // /sched/pauses/total/gc:seconds, p99 of the tick's delta
	Goroutines       uint64  `json:"goroutines"`           // /sched/goroutines:goroutines
	GOMAXPROCS       uint64  `json:"gomaxprocs"`           // /sched/gomaxprocs:threads
	MutexWaitTotalS  float64 `json:"mutex_wait_total_s"`   // /sync/mutex/wait/total:seconds (cumulative)
	MutexWaitDeltaS  float64 `json:"mutex_wait_delta_s"`   // this tick
	HeapObjectsBytes uint64  `json:"heap_objects_bytes"`   // /memory/classes/heap/objects:bytes (live heap)
	MemoryTotalBytes uint64  `json:"memory_total_bytes"`   // /memory/classes/total:bytes (everything the runtime mapped)
	HeapGoalBytes    uint64  `json:"heap_goal_bytes"`      // /gc/heap/goal:bytes
	GOGCPercent      uint64  `json:"gogc_percent"`         // /gc/gogc:percent
	GOMEMLIMITBytes  uint64  `json:"gomemlimit_bytes"`     // /gc/gomemlimit:bytes (math.MaxInt64 = unset)
	GCCyclesTotal    uint64  `json:"gc_cycles_total"`      // /gc/cycles/total:gc-cycles (cumulative)
	GCCyclesDelta    uint64  `json:"gc_cycles_delta"`      // this tick
	CPUUserS         float64 `json:"cpu_user_s"`           // /cpu/classes/user:cpu-seconds (cumulative)
	CPUGCS           float64 `json:"cpu_gc_s"`             // /cpu/classes/gc/total:cpu-seconds (cumulative)
	CPUTotalS        float64 `json:"cpu_total_s"`          // /cpu/classes/total:cpu-seconds (cumulative)
	CPUIdleS         float64 `json:"cpu_idle_s"`           // /cpu/classes/idle:cpu-seconds (cumulative)
	CPUScavengeS     float64 `json:"cpu_scavenge_s"`       // /cpu/classes/scavenge/total:cpu-seconds (cumulative)
	CPUTotalDeltaS   float64 `json:"cpu_total_delta_s"`    // this tick
	CPUGCDeltaS      float64 `json:"cpu_gc_delta_s"`       // this tick
	GCCPUFraction    float64 `json:"gc_cpu_fraction"`      // cpu_gc_delta / (cpu_total_delta − cpu_idle_delta), this tick
	CPUBusyFraction  float64 `json:"cpu_busy_fraction"`    // (cpu_total_delta − cpu_idle_delta) / (interval × GOMAXPROCS)
	SchedLatencyP50S float64 `json:"sched_latency_p50_s"`  // for the chart
	GCPauseTotalMaxS float64 `json:"gc_pause_total_max_s"` // the tick's longest STW
	SchedOtherPauseS float64 `json:"sched_pause_other_p99_s"`
}

// ProcessStats is layer 2 — the process seen through its cgroup v2 (or
// /proc/self when there is none). Cumulative counters carry their tick delta.
type ProcessStats struct {
	Source            string `json:"source"`      // "cgroup" | "proc" | "unavailable"
	CgroupPath        string `json:"cgroup_path"` // relative to /sys/fs/cgroup
	MemCurrentBytes   int64  `json:"mem_current_bytes"`
	MemMaxBytes       int64  `json:"mem_max_bytes"`  // -1 = "max" (unlimited)
	MemPeakBytes      int64  `json:"mem_peak_bytes"` // -1 = unavailable (memory.peak is Linux 5.19+); /proc fallback uses VmHWM
	MemSwapBytes      int64  `json:"mem_swap_bytes"`
	CPUUsageUsec      int64  `json:"cpu_usage_usec"`
	CPUUserUsec       int64  `json:"cpu_user_usec"`
	CPUSystemUsec     int64  `json:"cpu_system_usec"`
	CPUNrPeriods      int64  `json:"cpu_nr_periods"`
	CPUNrThrottled    int64  `json:"cpu_nr_throttled"`
	CPUThrottledUsec  int64  `json:"cpu_throttled_usec"`
	CPUUsageDeltaUsec int64  `json:"cpu_usage_delta_usec"`
	CPUThrottledDelta int64  `json:"cpu_throttled_delta_usec"`
	CPUNrThrottledDlt int64  `json:"cpu_nr_throttled_delta"`
	CPUQuotaUsec      int64  `json:"cpu_quota_usec"`  // cpu.max quota; -1 = "max" (no quota)
	CPUPeriodUsec     int64  `json:"cpu_period_usec"` // cpu.max period
	PidsCurrent       int64  `json:"pids_current"`
	PidsMax           int64  `json:"pids_max"`  // -1 = max
	Threads           int64  `json:"threads"`   // /proc/self/status Threads (runtime/metrics has no thread count)
	RSSBytes          int64  `json:"rss_bytes"` // /proc/self/status VmRSS — always read (cheap), the classic number
	// CgroupShared says the cgroup holds MORE than this process (pids.current
	// > this process's thread count): a login session scope, a container
	// running a supervisor + the app. Then memory.current and cpu.stat are the
	// cgroup's, not the process's — the cards say so and lean on RSS. A
	// systemd service unit (the production layout) is never shared.
	CgroupShared bool `json:"cgroup_shared"`
}

// PSILine is one PSI resource (cpu | memory | io): the "some" and "full"
// stall percentages over 10 / 60 / 300 s windows plus the cumulative totals.
type PSILine struct {
	SomeAvg10  float64 `json:"some_avg10"`
	SomeAvg60  float64 `json:"some_avg60"`
	SomeAvg300 float64 `json:"some_avg300"`
	SomeTotal  int64   `json:"some_total_usec"`
	FullAvg10  float64 `json:"full_avg10"`
	FullAvg60  float64 `json:"full_avg60"`
	FullAvg300 float64 `json:"full_avg300"`
	FullTotal  int64   `json:"full_total_usec"`
}

// PressureStats is layer 3 — PSI. Source says WHOSE pressure it is: the
// process's own cgroup (preferred — inside a container the host view may be
// the whole host or nothing) or the host's /proc/pressure/*.
type PressureStats struct {
	Source string  `json:"source"` // "cgroup" | "host" | "unavailable"
	CPU    PSILine `json:"cpu"`
	Memory PSILine `json:"memory"`
	IO     PSILine `json:"io"`
	// The spec's §7 shorthand, duplicated for the correlation chart.
	CPUSomeAvg10 float64 `json:"cpu_some_avg10"`
	MemSomeAvg10 float64 `json:"mem_some_avg10"`
	IOSomeAvg10  float64 `json:"io_some_avg10"`
}

// DBClientStats is layer 4a — pgxpool.Stat(), always observable, remote DB
// included. EmptyAcquireCount is THE pool signal: a goroutine asked for a
// connection and the pool had none idle.
type DBClientStats struct {
	MaxConns              int32   `json:"max_conns"`
	TotalConns            int32   `json:"total_conns"`
	AcquiredConns         int32   `json:"acquired_conns"`
	IdleConns             int32   `json:"idle_conns"`
	ConstructingConns     int32   `json:"constructing_conns"`
	AcquireCount          int64   `json:"acquire_count"`
	AcquireDurationMs     float64 `json:"acquire_duration_ms"` // cumulative, per pgxpool
	EmptyAcquireCount     int64   `json:"empty_acquire_count"`
	EmptyAcquireWaitMs    float64 `json:"empty_acquire_wait_ms"` // cumulative
	CanceledAcquireCount  int64   `json:"canceled_acquire_count"`
	NewConnsCount         int64   `json:"new_conns_count"`
	MaxLifetimeDestroy    int64   `json:"max_lifetime_destroy_count"`
	MaxIdleDestroy        int64   `json:"max_idle_destroy_count"`
	AcquireDelta          int64   `json:"acquire_delta"`
	EmptyAcquireDelta     int64   `json:"empty_acquire_delta"`
	EmptyAcquireWaitDelta float64 `json:"empty_acquire_wait_delta_ms"`
	AcquireWaitDeltaMs    float64 `json:"acquire_duration_delta_ms"`
	CanceledAcquireDelta  int64   `json:"canceled_acquire_delta"`
	QueryLatencyP50Ms     float64 `json:"query_latency_p50_ms"` // client-side query stage, this tick (the "query" span)
	QueryLatencyP99Ms     float64 `json:"query_latency_p99_ms"`
	QueryCount            int64   `json:"query_count"`
	Saturated             bool    `json:"saturated"` // acquired == max && idle == 0
}

// DBServerStats is layer 4b — pg_stat_* views, ONLY when the database is
// local. Observable=false with a Reason is a correct answer, not a gap.
type DBServerStats struct {
	Observable    bool    `json:"observable"`
	Reason        string  `json:"reason,omitempty"` // why not observable / why skipped this tick
	ProbedAt      int64   `json:"probed_at,omitempty"`
	DBSizeBytes   int64   `json:"db_size_bytes"`
	CacheHitRatio float64 `json:"cache_hit_ratio"` // blks_hit / (blks_hit + blks_read), cumulative
	BlksHit       int64   `json:"blks_hit"`
	BlksRead      int64   `json:"blks_read"`
	XactCommit    int64   `json:"xact_commit"`
	XactRollback  int64   `json:"xact_rollback"`
	Deadlocks     int64   `json:"deadlocks"`
	TempBytes     int64   `json:"temp_bytes"`
	ActiveConns   int64   `json:"active_conns"`
	IdleInTx      int64   `json:"idle_in_transaction"`
	Waiting       int64   `json:"waiting"` // active backends with a wait_event
	TotalBackends int64   `json:"total_backends"`
	StatementsExt bool    `json:"pg_stat_statements"` // extension present
}

// RequestStats is the request path's own view of the tick: throughput and
// the latency histogram of the requests that FINISHED in the window.
type RequestStats struct {
	Count        int64   `json:"count"`
	RPS          float64 `json:"rps"`
	LatencyP50Ms float64 `json:"latency_p50_ms"`
	LatencyP95Ms float64 `json:"latency_p95_ms"`
	LatencyP99Ms float64 `json:"latency_p99_ms"`
	LatencyMaxMs float64 `json:"latency_max_ms"`
	Errors5xx    int64   `json:"errors_5xx"`
	Status429    int64   `json:"status_429"` // shed by the tenant limiter — load the box refused, on purpose
	Status503    int64   `json:"status_503"` // shed by the breaker / memory guard
}

// ResourceSnapshot is one tick: the four layers + the request view + the
// verdict (the §7 data model of the spec, with the deltas the rules use).
type ResourceSnapshot struct {
	TS         int64         `json:"ts"` // unix milliseconds
	IntervalMs int64         `json:"interval_ms"`
	Mode       string        `json:"mode"` // "live" | "background"
	Runtime    RuntimeStats  `json:"runtime"`
	Process    ProcessStats  `json:"process_cgroup"`
	Pressure   PressureStats `json:"pressure"`
	DBClient   DBClientStats `json:"db_client"`
	DBServer   DBServerStats `json:"db_server_local_only"`
	Request    RequestStats  `json:"request"`
	// Attribution is the verdict of the §4 table for THIS tick; Verdict
	// carries its reason and the signals that fired.
	Attribution Attribution `json:"attribution"`
	Verdict     Verdict     `json:"verdict"`
}

// ResourceCollector owns the four layers, the windowed request histograms,
// the ring and the one goroutine that reads everything.
type ResourceCollector struct {
	cfg ResourceConfig

	// ── request path (hot) ─────────────────────────────────────────────────
	reqCount atomic.Int64
	err5xx   atomic.Int64
	st429    atomic.Int64
	st503    atomic.Int64
	// Double-buffered windowed histograms: the request path Records into
	// cur[]; the collector swaps the pair under the same mutex and reads the
	// retired one without contention, then Resets it for the next swap.
	// Range 1 µs – 60 s, TWO significant figures: a ±1 % bucket is plenty for
	// the p50/p99 of a one-second window, and it is a quarter of the memory of
	// the tenant histograms' three (the footprint is stated in bytes —
	// TestResourceCollector_FootprintBytes — because A-54 says RSS is declared,
	// not measured through the noise).
	hmu     sync.Mutex
	latCur  *hdrhistogram.Histogram
	latPrev *hdrhistogram.Histogram
	qCur    *hdrhistogram.Histogram
	qPrev   *hdrhistogram.Histogram
	qCount  int64 // queries seen in the current window (under hmu)

	// ── collector goroutine state (cold) ───────────────────────────────────
	rt   *runtimeReader
	proc *processReader
	db   *dbReader

	ring      [ResourceRingSize]ResourceSnapshot
	ringMu    sync.RWMutex
	ringHead  uint64 // total snapshots ever written
	latest    atomic.Pointer[ResourceSnapshot]
	liveUntil atomic.Int64  // unix nanos; 0 = background
	wake      chan struct{} // Touch → Run re-arms the timer at the live cadence NOW
	lastTick  time.Time
	baseline  ewma // healthy p99 baseline for the relative-rise rule
	started   atomic.Bool
}

// NewResourceCollector builds a collector. It does NOT start reading: call Run
// in a goroutine (the engine does it from startBackground). Nothing is read at
// construction either, so an app that disables self-monitoring pays only the
// struct.
func NewResourceCollector(cfg ResourceConfig) *ResourceCollector {
	if cfg.BackgroundInterval <= 0 {
		cfg.BackgroundInterval = DefaultBackgroundInterval
	}
	if cfg.LiveInterval <= 0 {
		cfg.LiveInterval = DefaultLiveInterval
	}
	if cfg.LiveWindow <= 0 {
		cfg.LiveWindow = DefaultLiveWindow
	}
	if cfg.DBServerEvery <= 0 {
		cfg.DBServerEvery = 10 * time.Second
	}
	cfg.Thresholds = cfg.Thresholds.withDefaults()
	c := &ResourceCollector{cfg: cfg, wake: make(chan struct{}, 1)}
	c.latCur = hdrhistogram.New(1, 60_000_000, 2)
	c.latPrev = hdrhistogram.New(1, 60_000_000, 2)
	c.qCur = hdrhistogram.New(1, 60_000_000, 2)
	c.qPrev = hdrhistogram.New(1, 60_000_000, 2)
	c.rt = newRuntimeReader()
	c.proc = newProcessReader()
	return c
}

// SetDB wires layer 4. stat is pgxpool's Stat (a func so this package does
// not import pgx); probe runs the server-side statement when the database is
// declared local (nil ⇒ never). Call before Run.
func (c *ResourceCollector) SetDB(stat func() PoolStat, probe DBServerProbe) {
	c.db = newDBReader(stat, probe, c.cfg.DBServerLocal, c.cfg.DBServerEvery)
}

// Observe is THE request-path entry point. It is called from the logging
// tap once per finished request with the total duration, the "query" stage
// duration (0 when the request ran no query) and the status. Cost: two atomic
// adds and one or two HDR RecordValue under a mutex. No allocation.
func (c *ResourceCollector) Observe(durUS, queryUS int64, status int) {
	c.reqCount.Add(1)
	switch {
	case status == 429:
		c.st429.Add(1)
	case status == 503:
		c.st503.Add(1)
	case status >= 500:
		c.err5xx.Add(1)
	}
	if durUS < 1 {
		durUS = 1
	}
	c.hmu.Lock()
	c.latCur.RecordValue(durUS) //nolint:errcheck // out-of-range is clamped by the histogram; never an error worth the hot path
	if queryUS > 0 {
		c.qCur.RecordValue(queryUS) //nolint:errcheck
		c.qCount++
	}
	c.hmu.Unlock()
}

// Touch switches the collector to live cadence (1 s by default) for LiveWindow
// after the call. The /admin correlation view calls it on every poll, so the
// 1 s series exists exactly while someone is looking at it.
func (c *ResourceCollector) Touch() {
	was := c.liveUntil.Load()
	c.liveUntil.Store(time.Now().Add(c.cfg.LiveWindow).UnixNano())
	if time.Now().UnixNano() >= was { // background → live: do not wait out the 10 s timer
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// Live reports whether the collector is currently on the live cadence.
func (c *ResourceCollector) Live() bool {
	return time.Now().UnixNano() < c.liveUntil.Load()
}

// Interval returns the cadence in force for the next tick.
func (c *ResourceCollector) Interval() time.Duration {
	if c.Live() {
		return c.cfg.LiveInterval
	}
	return c.cfg.BackgroundInterval
}

// Config returns the effective configuration (defaults applied).
func (c *ResourceCollector) Config() ResourceConfig { return c.cfg }

// Run is the collector goroutine: ONE timer, one tick at a time, until ctx
// ends. It is the only place the four layers are read.
func (c *ResourceCollector) Run(ctx context.Context) {
	c.started.Store(true)
	c.lastTick = time.Now()
	// Prime the cumulative readers so the first published tick has real deltas.
	c.rt.read()
	c.proc.read()
	if c.db != nil {
		c.db.readClient()
	}
	timer := time.NewTimer(c.Interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			// The first live poll re-arms the timer at the live cadence right
			// away (measured before this: the first 1 s tick arrived up to 10 s
			// after the poll — the whole background timer).
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.Interval())
		case now := <-timer.C:
			c.tick(now)
			timer.Reset(c.Interval())
		}
	}
}

// Started reports whether Run has been entered (the admin surface answers 503
// with a reason until then, never an empty snapshot dressed as data).
func (c *ResourceCollector) Started() bool { return c.started.Load() }

// tick reads every layer once, computes the deltas and the verdict, and
// writes ONE ring slot in place. Allocation-free after the first call (the
// readers own their buffers; the snapshot is a value written into the array).
func (c *ResourceCollector) tick(now time.Time) {
	interval := now.Sub(c.lastTick)
	if interval <= 0 {
		interval = c.Interval()
	}
	c.lastTick = now

	// ── request window: swap the histograms, read the retired pair ─────────
	count := c.reqCount.Swap(0)
	e5 := c.err5xx.Swap(0)
	s429 := c.st429.Swap(0)
	s503 := c.st503.Swap(0)
	c.hmu.Lock()
	c.latCur, c.latPrev = c.latPrev, c.latCur
	c.qCur, c.qPrev = c.qPrev, c.qCur
	qn := c.qCount
	c.qCount = 0
	c.hmu.Unlock()
	lat, q := c.latPrev, c.qPrev // retired: only this goroutine touches them now

	c.ringMu.Lock()
	slot := &c.ring[c.ringHead%ResourceRingSize]
	*slot = ResourceSnapshot{} // zero in place — no allocation
	slot.TS = now.UnixMilli()
	slot.IntervalMs = interval.Milliseconds()
	if c.Live() {
		slot.Mode = "live"
	} else {
		slot.Mode = "background"
	}
	secs := interval.Seconds()
	slot.Request = RequestStats{
		Count:     count,
		RPS:       float64(count) / secs,
		Errors5xx: e5,
		Status429: s429,
		Status503: s503,
	}
	if lat.TotalCount() > 0 {
		slot.Request.LatencyP50Ms = float64(lat.ValueAtQuantile(50)) / 1000
		slot.Request.LatencyP95Ms = float64(lat.ValueAtQuantile(95)) / 1000
		slot.Request.LatencyP99Ms = float64(lat.ValueAtQuantile(99)) / 1000
		slot.Request.LatencyMaxMs = float64(lat.Max()) / 1000
	}
	// Layers 1–3.
	c.rt.fill(&slot.Runtime, secs)
	c.proc.fill(&slot.Process, &slot.Pressure, secs)
	// Layer 4.
	if c.db != nil {
		c.db.fillClient(&slot.DBClient)
		c.db.fillServer(&slot.DBServer, now)
	} else {
		slot.DBServer = DBServerStats{Observable: false, Reason: "no database wired"}
	}
	slot.DBClient.QueryCount = qn
	if q.TotalCount() > 0 {
		slot.DBClient.QueryLatencyP50Ms = float64(q.ValueAtQuantile(50)) / 1000
		slot.DBClient.QueryLatencyP99Ms = float64(q.ValueAtQuantile(99)) / 1000
	}
	lat.Reset()
	q.Reset()

	// The verdict — deterministic, from THIS tick's numbers and the healthy
	// baseline of the previous ones. The evidence (Signals, Also) is NOT kept
	// in the ring: it is a pure function of the slot's numbers and the
	// thresholds, so the readers recompute it (Describe) — the tick allocates
	// nothing for it and the ring is 900 × one snapshot, ~1 MiB, not 2.2.
	slot.Attribution, slot.Verdict = attribute(slot, c.cfg.Thresholds, c.baseline.value(), false)
	if slot.Attribution == AttrHealthy && slot.Request.Count > 0 {
		c.baseline.observe(slot.Request.LatencyP99Ms)
	}
	c.ringHead++
	c.ringMu.Unlock()
	// Publish a COPY for the lock-free readers (Prometheus, the cards). THE one
	// allocation per tick, deliberately: it is what lets Latest() never take
	// the ring lock and never hand out a pointer into the array.
	snap := *slot
	c.latest.Store(&snap)
}

// FootprintBytes is the collector's fixed memory: the ring array plus the four
// windowed histograms — the A-54 RSS proxy, declared in bytes (the ring slots'
// evidence slices are allocated per tick on the heap and counted separately by
// the test that reports this).
func (c *ResourceCollector) FootprintBytes() int64 {
	c.hmu.Lock()
	defer c.hmu.Unlock()
	h := c.latCur.ByteSize() + c.latPrev.ByteSize() + c.qCur.ByteSize() + c.qPrev.ByteSize()
	return int64(unsafe.Sizeof(c.ring)) + int64(h)
}

// Latest returns a copy of the most recent snapshot with its verdict
// described, or nil before the first tick. The copy (and the sentence) are
// the READER's cost, never the tick's.
func (c *ResourceCollector) Latest() *ResourceSnapshot {
	p := c.latest.Load()
	if p == nil {
		return nil
	}
	s := *p
	s.describeWith(c.cfg.Thresholds)
	return &s
}

// latestRaw is the lock-free, allocation-free read Prometheus uses.
func (c *ResourceCollector) latestRaw() *ResourceSnapshot { return c.latest.Load() }

// Series copies out the last n snapshots, oldest first (n ≤ ResourceRingSize).
func (c *ResourceCollector) Series(n int) []ResourceSnapshot {
	if n <= 0 || n > ResourceRingSize {
		n = ResourceRingSize
	}
	c.ringMu.RLock()
	defer c.ringMu.RUnlock()
	have := c.ringHead
	if have > ResourceRingSize {
		have = ResourceRingSize
	}
	if uint64(n) > have {
		n = int(have)
	}
	out := make([]ResourceSnapshot, n)
	for i := 0; i < n; i++ {
		idx := (c.ringHead - uint64(n) + uint64(i)) % ResourceRingSize
		out[i] = c.ring[idx]
		out[i].describeWith(c.cfg.Thresholds)
	}
	return out
}

// Count reports how many ticks the ring holds (≤ ResourceRingSize).
func (c *ResourceCollector) Count() int {
	c.ringMu.RLock()
	defer c.ringMu.RUnlock()
	if c.ringHead > ResourceRingSize {
		return ResourceRingSize
	}
	return int(c.ringHead)
}

// ewma is the healthy-p99 baseline the relative-rise rule compares against.
// α = 0.1: a 10-tick memory, so a load test that lasts a minute in live mode
// does not drag its own spike into the baseline before the verdict fires.
type ewma struct {
	v   float64
	set bool
}

func (e *ewma) observe(x float64) {
	if !e.set {
		e.v, e.set = x, true
		return
	}
	e.v = 0.9*e.v + 0.1*x
}

func (e *ewma) value() float64 {
	if !e.set {
		return 0
	}
	return e.v
}
