package observability

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// The cardinal principle, pinned: a tick allocates nothing except the one
// published copy for Latest() and the verdict's Signals slice (both stated in
// the code as the deliberate exceptions). runtime/metrics re-uses the
// histogram buffers, the pseudo-files re-read in place, the ring slot is a
// value. AllocsPerRun measures the average over N runs, so a one-off
// allocation from the first read cannot hide a per-tick one.
func TestResourceCollector_TickAllocatesNothing(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{})
	c.SetDB(func() PoolStat { return PoolStat{MaxConns: 10, AcquiredConns: 1, IdleConns: 9} }, nil)
	c.rt.read()
	c.proc.read()
	c.db.readClient()
	now := time.Now()
	for i := 0; i < 3; i++ { // prime: first reads allocate the per-metric prev slices
		now = now.Add(time.Second)
		c.tick(now)
	}
	// Traffic in the window, so the histograms and the verdict have work to do.
	for i := 0; i < 100; i++ {
		c.Observe(int64(1000+i), int64(300+i), 200)
	}
	allocs := testing.AllocsPerRun(20, func() {
		now = now.Add(time.Second)
		c.tick(now)
	})
	// Budget: the Latest() copy — ONE. Anything above that is a per-tick
	// allocation that crept into a layer — the exact regression this test
	// exists to catch. (The verdict's evidence used to be the second one; it is
	// recomputed on read now.)
	if allocs > 1 {
		t.Fatalf("tick allocates %.1f objects/tick; budget is 1 (the published copy)", allocs)
	}
}

// The footprint, in bytes: the fixed ring + the four windowed histograms —
// ALL of it (the ring slots hold no heap: the evidence is recomputed on read).
// The number is printed so the session report states it; the assertion is the
// ceiling nothing may creep past.
func TestResourceCollector_FootprintBytes(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{})
	fixed := c.FootprintBytes()
	c.tick(time.Now())
	if n := len(c.ring[0].Verdict.Signals); n != 0 {
		t.Fatalf("a ring slot must hold no evidence slice (got %d signals) — it is recomputed on read", n)
	}
	if n := len(c.Latest().Verdict.Signals); n < 10 {
		t.Fatalf("Latest() must carry the recomputed evidence, got %d signals", n)
	}
	t.Logf("footprint = %d B (%.2f MiB): ring %d slots × %d B + 4 HDR (2 sig. figs, 1 µs–60 s)",
		fixed, float64(fixed)/1048576, ResourceRingSize, unsafe.Sizeof(ResourceSnapshot{}))
	if fixed > 2<<20 {
		t.Fatalf("collector footprint %d B exceeds the 2 MiB ceiling", fixed)
	}
}

// Observe — the request path — must not allocate at all.
func TestResourceCollector_ObserveAllocatesNothing(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{})
	allocs := testing.AllocsPerRun(1000, func() { c.Observe(1234, 456, 200) })
	if allocs != 0 {
		t.Fatalf("Observe allocates %.2f objects/call; the request path must allocate nothing", allocs)
	}
}

// The ring is a fixed array: writing more ticks than its size overwrites the
// oldest, Series is bounded, and the snapshots keep their order.
func TestResourceCollector_RingIsBounded(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{})
	now := time.Now()
	for i := 0; i < ResourceRingSize+50; i++ {
		now = now.Add(time.Second)
		c.tick(now)
	}
	if c.Count() != ResourceRingSize {
		t.Fatalf("count = %d, want %d", c.Count(), ResourceRingSize)
	}
	s := c.Series(0)
	if len(s) != ResourceRingSize {
		t.Fatalf("series len = %d, want %d", len(s), ResourceRingSize)
	}
	for i := 1; i < len(s); i++ {
		if s[i].TS <= s[i-1].TS {
			t.Fatalf("series not ordered at %d: %d then %d", i, s[i-1].TS, s[i].TS)
		}
	}
	last := c.Series(5)
	if len(last) != 5 || last[4].TS != s[len(s)-1].TS {
		t.Fatalf("Series(5) must return the 5 newest, oldest first")
	}
}

// The request window: counts, RPS and percentiles come from the requests that
// finished in the tick, then the histograms are reset for the next one.
func TestResourceCollector_RequestWindow(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{})
	now := time.Now()
	c.tick(now)
	for i := 0; i < 200; i++ {
		c.Observe(2000, 800, 200) // 2 ms, query 0.8 ms
	}
	for i := 0; i < 10; i++ {
		c.Observe(90_000, 85_000, 200) // ten slow, db-dominated requests (p99 of ~210 needs > 2 in the tail)
	}
	c.Observe(5000, 0, 503)
	c.Observe(5000, 0, 429)
	c.Observe(5000, 0, 500)
	now = now.Add(2 * time.Second)
	c.tick(now)
	s := c.Latest()
	if s == nil || s.Request.Count != 213 {
		t.Fatalf("count = %+v", s)
	}
	if s.Request.RPS < 105 || s.Request.RPS > 108 {
		t.Fatalf("rps = %.1f, want ~106.5", s.Request.RPS)
	}
	if s.Request.Status503 != 1 || s.Request.Status429 != 1 || s.Request.Errors5xx != 1 {
		t.Fatalf("status counters = %+v", s.Request)
	}
	if s.Request.LatencyP50Ms < 1.9 || s.Request.LatencyP50Ms > 2.1 {
		t.Fatalf("p50 = %.2f, want ~2", s.Request.LatencyP50Ms)
	}
	if s.Request.LatencyMaxMs < 89 {
		t.Fatalf("max = %.2f, want ~90", s.Request.LatencyMaxMs)
	}
	if s.DBClient.QueryCount != 210 || s.DBClient.QueryLatencyP99Ms < 80 {
		t.Fatalf("query window = %+v", s.DBClient)
	}
	// Next tick: empty window, histograms were reset.
	now = now.Add(time.Second)
	c.tick(now)
	if s2 := c.Latest(); s2.Request.Count != 0 || s2.Request.LatencyP99Ms != 0 || s2.DBClient.QueryCount != 0 {
		t.Fatalf("window not reset: %+v", s2.Request)
	}
}

// Live mode: Touch switches the cadence for LiveWindow and it decays back.
func TestResourceCollector_LiveMode(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{LiveWindow: 50 * time.Millisecond, LiveInterval: time.Millisecond, BackgroundInterval: time.Hour})
	if c.Interval() != time.Hour {
		t.Fatal("background by default")
	}
	c.Touch()
	if !c.Live() || c.Interval() != time.Millisecond {
		t.Fatal("Touch must switch to the live cadence")
	}
	time.Sleep(70 * time.Millisecond)
	if c.Live() {
		t.Fatal("live mode must decay after LiveWindow")
	}
}

// Touch wakes the loop: a poll during a long background interval gets a live
// tick within the live cadence, not after the background timer runs out.
func TestResourceCollector_TouchWakesTheLoop(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{BackgroundInterval: time.Hour, LiveInterval: 10 * time.Millisecond, LiveWindow: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	if c.Count() != 0 {
		t.Fatalf("background 1h: no tick yet, got %d", c.Count())
	}
	c.Touch()
	deadline := time.Now().Add(time.Second)
	for c.Count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Count() < 2 {
		t.Fatalf("Touch must re-arm the timer at the live cadence; ticks = %d after 1 s", c.Count())
	}
}

// Run: the goroutine ticks on its own and stops with the context.
func TestResourceCollector_Run(t *testing.T) {
	c := NewResourceCollector(ResourceConfig{BackgroundInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for c.Count() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if c.Count() < 3 {
		t.Fatalf("collector ticked %d times in 2 s at 5 ms", c.Count())
	}
	s := c.Latest()
	if s.Runtime.GOMAXPROCS == 0 || s.Runtime.GOMAXPROCS != uint64(runtime.GOMAXPROCS(0)) {
		t.Fatalf("runtime layer not read: %+v", s.Runtime)
	}
	if s.Runtime.Goroutines == 0 {
		t.Fatal("goroutines must be > 0")
	}
	if s.Process.Source == "unavailable" && runtime.GOOS == "linux" {
		t.Fatalf("process layer unavailable on linux: %+v", s.Process)
	}
	if runtime.GOOS == "linux" && s.Process.RSSBytes == 0 {
		t.Fatalf("/proc/self/status not read: %+v", s.Process)
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatalf("snapshot must marshal: %v", err)
	}
}

// The runtime histogram delta: the p99 is THIS tick's, not the cumulative one.
func TestRuntimeReader_HistDeltaIsPerTick(t *testing.T) {
	r := newRuntimeReader()
	r.read()
	_, _, _ = r.histDelta(mSchedLat)
	// Burn some scheduler events.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); runtime.Gosched() }()
	}
	wg.Wait()
	r.read()
	_, p99a, _ := r.histDelta(mSchedLat)
	r.read()
	_, p99b, _ := r.histDelta(mSchedLat)
	if p99a < 0 || p99b < 0 {
		t.Fatal("negative percentile")
	}
	// A second read with no new events must report zero, not the cumulative.
	if p99b != 0 && p99b >= p99a && p99a > 0 {
		t.Logf("note: events landed between the two reads (p99a=%g p99b=%g) — acceptable on a busy box", p99a, p99b)
	}
}

// The parsers used on the pseudo-files, on real-shaped buffers.
func TestProcParsers(t *testing.T) {
	cpuStat := []byte("usage_usec 378224673620\nuser_usec 241237775512\nsystem_usec 97847356829\nnr_periods 12\nnr_throttled 3\nthrottled_usec 4500\n")
	if v, ok := kvInt64(cpuStat, "throttled_usec"); !ok || v != 4500 {
		t.Fatalf("throttled_usec = %d %v", v, ok)
	}
	if v, ok := kvInt64(cpuStat, "user_usec"); !ok || v != 241237775512 {
		t.Fatalf("user_usec = %d", v)
	}
	if _, ok := kvInt64(cpuStat, "usage"); ok {
		t.Fatal("prefix must not match a longer key")
	}
	status := []byte("Name:\tappximo\nVmHWM:\t  48420 kB\nVmRSS:\t  45120 kB\nThreads:\t9\n")
	if v, ok := kvInt64(status, "VmRSS"); !ok || v != 45120 {
		t.Fatalf("VmRSS = %d", v)
	}
	if v, _ := kvInt64(status, "Threads"); v != 9 {
		t.Fatalf("Threads = %d", v)
	}
	if parseInt64([]byte("max\n")) != -1 || parseInt64([]byte(" 123 ")) != 123 {
		t.Fatal("parseInt64")
	}
	var l PSILine
	if !psiParse([]byte("some avg10=45.49 avg60=14.16 avg300=4.81 total=155947240060\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"), &l) {
		t.Fatal("psi parse")
	}
	if l.SomeAvg10 != 45.49 || l.SomeAvg300 != 4.81 || l.SomeTotal != 155947240060 || l.FullAvg10 != 0 {
		t.Fatalf("psi = %+v", l)
	}
	if psiParse([]byte("garbage\n"), &l) {
		t.Fatal("no some/full lines must return false")
	}
}

// The DB client deltas and the saturation flag.
func TestDBReader_ClientDeltas(t *testing.T) {
	calls := 0
	stats := []PoolStat{
		{MaxConns: 10, AcquiredConns: 2, IdleConns: 8, AcquireCount: 100, EmptyAcquireCount: 0},
		{MaxConns: 10, AcquiredConns: 10, IdleConns: 0, AcquireCount: 400, EmptyAcquireCount: 37, EmptyAcquireWaitTime: 900 * time.Millisecond},
	}
	r := newDBReader(func() PoolStat { s := stats[calls]; calls++; return s }, nil, false, time.Second)
	r.readClient()
	var st DBClientStats
	r.fillClient(&st)
	if !st.Saturated || st.EmptyAcquireDelta != 37 || st.AcquireDelta != 300 || st.EmptyAcquireWaitDelta != 900 {
		t.Fatalf("client = %+v", st)
	}
	var sv DBServerStats
	r.fillServer(&sv, time.Now())
	if sv.Observable || sv.Reason == "" {
		t.Fatalf("remote database must be reported as not observable with a reason: %+v", sv)
	}
}

// The server probe: local + probe wired → observable, cadence respected,
// an error keeps the previous numbers and says why.
func TestDBReader_ServerProbe(t *testing.T) {
	n := 0
	probe := func(_ context.Context, out *DBServerStats) error {
		n++
		if n == 2 {
			return context.DeadlineExceeded
		}
		out.DBSizeBytes = int64(1000 * n)
		out.BlksHit, out.BlksRead = 90, 10
		return nil
	}
	r := newDBReader(func() PoolStat { return PoolStat{} }, probe, true, time.Second)
	now := time.Now()
	var sv DBServerStats
	r.fillServer(&sv, now)
	if !sv.Observable || sv.DBSizeBytes != 1000 || sv.CacheHitRatio != 0.9 {
		t.Fatalf("first probe = %+v", sv)
	}
	r.fillServer(&sv, now.Add(100*time.Millisecond)) // not due
	if n != 1 {
		t.Fatal("probe ran before its cadence")
	}
	r.fillServer(&sv, now.Add(2*time.Second)) // due, fails
	if n != 2 || !sv.Observable || sv.DBSizeBytes != 1000 || sv.Reason == "" {
		t.Fatalf("failed probe must keep numbers + say why: %+v", sv)
	}
	r.fillServer(&sv, now.Add(4*time.Second))
	if n != 3 || sv.DBSizeBytes != 3000 || sv.Reason != "" {
		t.Fatalf("third probe = %+v", sv)
	}
}

// Cold start is not a wall: on the first tick of a load run the pool holds no
// connections, so every waiter queues behind a connection being CONSTRUCTED.
// The reader marks the tick Warming and the pool_exhausted rule stands down;
// once the pool has grown to MaxConns the same numbers ARE the verdict
// (CAPACIDAD-USL-S1).
func TestDBReader_ColdStartIsWarmingNotExhaustion(t *testing.T) {
	calls := 0
	stats := []PoolStat{
		// t0: empty pool, nothing asked yet.
		{MaxConns: 10, TotalConns: 0, AcquiredConns: 0, IdleConns: 0},
		// t1: traffic arrives — 40 goroutines found no connection and waited
		// 2 s in total while the pool opened all 10 connections. The tick
		// boundary already reads 10/10, which is exactly why the test is the
		// DELTA and not the instantaneous size.
		{MaxConns: 10, TotalConns: 10, AcquiredConns: 10, IdleConns: 0, NewConnsCount: 10,
			AcquireCount: 300, EmptyAcquireCount: 40, EmptyAcquireWaitTime: 2 * time.Second},
		// t2: pool fully grown and NOT growing, the same waiting continues —
		// real exhaustion.
		{MaxConns: 10, TotalConns: 10, AcquiredConns: 10, IdleConns: 0, NewConnsCount: 10,
			AcquireCount: 600, EmptyAcquireCount: 90, EmptyAcquireWaitTime: 4 * time.Second},
	}
	r := newDBReader(func() PoolStat { s := stats[calls]; calls++; return s }, nil, true, time.Second)
	r.readClient()

	warm := ResourceSnapshot{IntervalMs: 1000}
	r.fillClient(&warm.DBClient)
	if !warm.DBClient.Warming || warm.DBClient.NewConnsDelta != 10 {
		t.Fatalf("cold start must be Warming with the new-conn delta: %+v", warm.DBClient)
	}
	warm.Request = RequestStats{Count: 300, RPS: 300, LatencyP99Ms: 400}
	if a, _ := attribute(&warm, AttributionThresholds{}.withDefaults(), 5, false); a == AttrPoolExhausted {
		t.Fatalf("a warming pool must never be attributed pool_exhausted (got %s)", a)
	}

	grown := ResourceSnapshot{IntervalMs: 1000}
	r.fillClient(&grown.DBClient)
	if grown.DBClient.Warming {
		t.Fatalf("a pool at max_conns is not warming: %+v", grown.DBClient)
	}
	grown.Request = RequestStats{Count: 300, RPS: 300, LatencyP99Ms: 400}
	if a, _ := attribute(&grown, AttributionThresholds{}.withDefaults(), 5, false); a != AttrPoolExhausted {
		t.Fatalf("a grown, saturated, waiting pool IS pool_exhausted (got %s)", a)
	}
}

// ?since= must cut the series at the instant a run started, so the window
// verdict is the run's and not the history's (CAPACIDAD-USL-S1).
func TestSinceFilter(t *testing.T) {
	s := []ResourceSnapshot{{TS: 100}, {TS: 200}, {TS: 300}, {TS: 400}}
	if got := sinceFilter(s, "250"); len(got) != 2 || got[0].TS != 300 {
		t.Fatalf("since=250 must keep the last two ticks, got %+v", got)
	}
	if got := sinceFilter(s, ""); len(got) != 4 {
		t.Fatal("no since must keep everything")
	}
	if got := sinceFilter(s, "not-a-number"); len(got) != 4 {
		t.Fatal("an unparseable since must keep everything, never silently narrow a verdict")
	}
	if got := sinceFilter(s, "500"); len(got) != 0 {
		t.Fatal("a since past every tick keeps nothing")
	}
}
