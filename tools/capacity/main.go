package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "sweep":
		cmdSweep(os.Args[2:])
	case "fit":
		cmdFit(os.Args[2:])
	case "soak":
		cmdSoak(os.Args[2:])
	case "abba":
		cmdABBA(os.Args[2:])
	case "soakreport":
		cmdSoakReport(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `capacity — the Appximo capacity laboratory

  capacity run    -url … -path … [-rate N -duration 60s]      one load level
  capacity sweep  -url … -path … -rates 10,25,50,…  -repeats 3  the ladder, to JSONL
  capacity fit    -in sweep.jsonl [-think 30s,0s]              fit the USL, print the report
  capacity soak   -url … -path … -rate N -duration 4h          the endurance run
  capacity abba   -in arms.jsonl                                the frozen A B B A verdict
  capacity soakreport -in soak.jsonl                            the endurance verdict, by slope

Every latency is reported twice: from the actual send (service) and from the
SCHEDULED send (response). The second is the coordinated-omission-corrected
number and the one to quote.
`)
	os.Exit(2)
}

// ── shared target flags ────────────────────────────────────────────────────

type targetFlags struct {
	url, host, token, path, method, body, adminKey string
	laddrs                                         string
	values                                         string
	patience                                       time.Duration
	pgPID                                          int
	span                                           int
	timeout                                        time.Duration
	enginePort                                     int
}

func (t *targetFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&t.url, "url", "http://127.0.0.1:8181", "base URL of the engine under test")
	fs.StringVar(&t.host, "host", "", "Host header (the tenant subdomain)")
	fs.StringVar(&t.token, "token", "", "Bearer token")
	fs.StringVar(&t.path, "path", "/api/productos?per_page=20", "request path; {n} is replaced by a random integer in [0,span) — use it to defeat the 5 s response cache")
	fs.StringVar(&t.method, "method", "GET", "HTTP method")
	fs.StringVar(&t.body, "body", "", "request body; {n} is replaced like the path")
	fs.StringVar(&t.adminKey, "admin-key", "", "X-Admin-Key, to read /admin/resources for the self-monitor verdict of each run")
	fs.IntVar(&t.span, "span", 1000, "range of the {n} placeholder")
	fs.StringVar(&t.values, "values", "", "file of strings, one per line; {v} in the path or body is replaced by a random one — how a soak PATCHes real rows instead of inserting new ones")
	fs.DurationVar(&t.timeout, "timeout", 30*time.Second, "per-request timeout")
	fs.StringVar(&t.laddrs, "laddrs", "", "comma-separated source IPs to spread the load over (e.g. 127.0.0.2,127.0.0.3) — models N distinct clients")
	fs.IntVar(&t.pgPID, "pg-pid", 0, "PID of the PostgreSQL postmaster (or its container) — its cgroup cpu.stat is the authority for the database's CPU-seconds; 0 falls back to summing processes named postgres, which undercounts")
	fs.IntVar(&t.enginePort, "engine-port", 0, "port the engine listens on (0 = derive from -url) — used to find its pid for CPU accounting")
}

func (t *targetFlags) target() Target {
	h := map[string]string{"Accept": "application/json"}
	if t.token != "" {
		h["Authorization"] = "Bearer " + t.token
	}
	if t.body != "" {
		h["Content-Type"] = "application/json"
	}
	rnd := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // load shaping, not cryptography
	var vals []string
	if t.values != "" {
		b, err := os.ReadFile(t.values) //nolint:gosec
		if err != nil {
			fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				vals = append(vals, line)
			}
		}
		if len(vals) == 0 {
			fatal(fmt.Errorf("values file %s is empty", t.values))
		}
	}
	var mu = make(chan struct{}, 1)
	mu <- struct{}{}
	next := func() int {
		<-mu
		v := rnd.Intn(t.span)
		mu <- struct{}{}
		return v
	}
	nextVal := func() string {
		if len(vals) == 0 {
			return ""
		}
		<-mu
		v := vals[rnd.Intn(len(vals))]
		mu <- struct{}{}
		return v
	}
	fill := func(tmpl string) string {
		out := strings.ReplaceAll(tmpl, "{n}", strconv.Itoa(next()))
		if strings.Contains(out, "{v}") {
			out = strings.ReplaceAll(out, "{v}", nextVal())
		}
		return out
	}
	var la []string
	for _, a := range strings.Split(t.laddrs, ",") {
		if a = strings.TrimSpace(a); a != "" {
			la = append(la, a)
		}
	}
	tg := Target{
		LocalAddrs: la,
		Method:     t.method,
		Headers:    h,
		Host:       t.host,
		URLFor:     func(int64) string { return t.url + fill(t.path) },
	}
	if t.body != "" {
		tg.Body = func(int64) []byte { return []byte(fill(t.body)) }
	}
	return tg
}

func (t *targetFlags) port() int {
	if t.enginePort > 0 {
		return t.enginePort
	}
	i := strings.LastIndex(t.url, ":")
	if i < 0 {
		return 0
	}
	p, _ := strconv.Atoi(strings.TrimSuffix(t.url[i+1:], "/"))
	return p
}

// ── run ────────────────────────────────────────────────────────────────────

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var tf targetFlags
	tf.bind(fs)
	rate := fs.Float64("rate", 50, "offered requests per second")
	dur := fs.Duration("duration", 60*time.Second, "run length")
	warm := fs.Duration("warmup", 10*time.Second, "discarded from the statistics (still sent)")
	label := fs.String("label", "run", "label")
	inflight := fs.Int("max-inflight", 0, "cap on concurrent requests (0 = 2 × rate)")
	fs.DurationVar(&tf.patience, "patience", 5*time.Second, "how long a request waits for its turn before the client gives up (an abandoned request is counted, never omitted)")
	_ = fs.Parse(args)
	r := doOne(context.Background(), &tf, *rate, *dur, *warm, *inflight, *label)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(r)
}

func doOne(ctx context.Context, tf *targetFlags, rate float64, dur, warm time.Duration, inflight int, label string) RunResult {
	smp := cpuSampler{enginePID: findPIDByListenPort(tf.port()), pgMatch: "postgres", selfPID: os.Getpid()}
	if tf.pgPID > 0 {
		smp.pgCgroup = pgCgroupOf(tf.pgPID)
	}
	a := smp.snap(float64(time.Now().UnixNano()) / 1e9)
	res, _ := Run(ctx, tf.target(), RunConfig{
		Rate: rate, Duration: dur, Warmup: warm, MaxInFlight: inflight,
		Timeout: tf.timeout, Patience: tf.patience,
	}, label)
	b := smp.snap(float64(time.Now().UnixNano()) / 1e9)
	res.CPU = smp.report(a, b, res.Completed, 1)
	res.CPU.CPUs = numCPU()
	if tf.adminKey != "" {
		// ?since= the instant this run began: the window verdict must be THIS
		// run's, never the history behind it.
		res.Verdict = selfmonWindow(tf.url, tf.adminKey, res.StartUnixMs)
	}
	return res
}

func numCPU() int { return runtimeNumCPU() }

// ── sweep ──────────────────────────────────────────────────────────────────

func cmdSweep(args []string) {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	var tf targetFlags
	tf.bind(fs)
	rates := fs.String("rates", "10,25,50,100,150,200,300,400", "comma-separated offered rates")
	repeats := fs.Int("repeats", 3, "measurements per level")
	dur := fs.Duration("duration", 45*time.Second, "per measurement")
	warm := fs.Duration("warmup", 10*time.Second, "discarded per measurement")
	rest := fs.Duration("rest", 15*time.Second, "idle between measurements, so the pool and the GC settle")
	out := fs.String("out", "sweep.jsonl", "output JSONL")
	name := fs.String("name", "workload", "workload name recorded on every row")
	inflight := fs.Int("max-inflight", 0, "cap on concurrent requests (0 = 2 × rate)")
	fs.DurationVar(&tf.patience, "patience", 5*time.Second, "client patience before abandoning a queued request")
	_ = fs.Parse(args)

	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	defer f.Close() //nolint:errcheck
	w := bufio.NewWriter(f)
	defer w.Flush() //nolint:errcheck

	var levels []float64
	for _, s := range strings.Split(*rates, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil || v <= 0 {
			fatal(fmt.Errorf("bad rate %q", s))
		}
		levels = append(levels, v)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Printf("# sweep %s — %d levels × %d repeats × %s (+%s warm-up, %s rest)\n",
		*name, len(levels), *repeats, *dur, *warm, *rest)
	// Repeat-major order: every level is visited once before any level is
	// visited twice, so a slow drift of the host (a neighbour waking up) is
	// spread across all levels instead of landing on one.
	for rep := 0; rep < *repeats; rep++ {
		for _, rate := range levels {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(*rest)
			label := fmt.Sprintf("%s@%.0f#%d", *name, rate, rep+1)
			r := doOne(ctx, &tf, rate, *dur, *warm, *inflight, label)
			r.Workload = *name
			r.Repeat = rep + 1
			b, _ := json.Marshal(r)
			_, _ = w.Write(append(b, '\n'))
			_ = w.Flush()
			fmt.Println(r.String())
		}
	}
}

// ── fit ────────────────────────────────────────────────────────────────────

func cmdFit(args []string) {
	fs := flag.NewFlagSet("fit", flag.ExitOnError)
	in := fs.String("in", "sweep.jsonl", "sweep output")
	jsonOut := fs.String("json", "", "also write the report as JSON here")
	think := fs.String("think", "30s,5s,0s", "think times for the user translation")
	boot := fs.Int("bootstrap", 2000, "bootstrap resamples")
	workload := fs.String("workload", "", "only rows with this workload name")
	metric := fs.String("metric", "goodput", "throughput metric: goodput (2xx/s) or achieved (completions/s)")
	_ = fs.Parse(args)

	rows, err := readRuns(*in, *workload)
	if err != nil {
		fatal(err)
	}
	rep := BuildReport(rows, *metric, *think, *boot)
	fmt.Print(rep.Markdown())
	if *jsonOut != "" {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fatal(fmt.Errorf("encode report: %w", err))
		}
		if err := os.WriteFile(*jsonOut, b, 0o644); err != nil { //nolint:gosec
			fatal(err)
		}
	}
}

func readRuns(path, workload string) ([]RunResult, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck
	var out []RunResult
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r RunResult
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		if workload != "" && r.Workload != workload {
			continue
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// ── soak ───────────────────────────────────────────────────────────────────

func cmdSoak(args []string) {
	fs := flag.NewFlagSet("soak", flag.ExitOnError)
	var tf targetFlags
	tf.bind(fs)
	rate := fs.Float64("rate", 50, "sustained offered rate")
	dur := fs.Duration("duration", 4*time.Hour, "total length")
	slice := fs.Duration("slice", 5*time.Minute, "one measured slice; the run is a sequence of slices so the trend is visible")
	out := fs.String("out", "soak.jsonl", "output JSONL, one row per slice")
	fs.DurationVar(&tf.patience, "patience", 5*time.Second, "client patience before abandoning a queued request")
	_ = fs.Parse(args)

	f, err := os.Create(*out)
	if err != nil {
		fatal(err)
	}
	defer f.Close() //nolint:errcheck
	w := bufio.NewWriter(f)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.Now().Add(*dur)
	i := 0
	for time.Now().Before(deadline) && ctx.Err() == nil {
		i++
		left := time.Until(deadline)
		d := *slice
		if left < d {
			d = left
		}
		r := doOne(ctx, &tf, *rate, d, 5*time.Second, 0, fmt.Sprintf("soak#%d", i))
		r.Workload = "soak"
		r.Repeat = i
		if tf.adminKey != "" {
			r.Engine = engineSnapshot(tf.url, tf.adminKey)
		}
		b, _ := json.Marshal(r)
		_, _ = w.Write(append(b, '\n'))
		_ = w.Flush()
		fmt.Println(r.String())
	}
	_ = w.Flush()
}

// ── the self-monitor, read as an instrument ────────────────────────────────

func selfmonWindow(base, key string, sinceMs int64) *WindowVerdict {
	var env struct {
		Window struct {
			Dominant     string         `json:"dominant"`
			Owner        string         `json:"owner"`
			Reason       string         `json:"reason"`
			Distribution map[string]int `json:"distribution"`
			TrafficTicks int            `json:"traffic_ticks"`
			PeakRPS      float64        `json:"peak_rps"`
			PeakP99Ms    float64        `json:"peak_p99_ms"`
			Requests     int64          `json:"requests"`
			Shed         int64          `json:"shed_429_503"`
			Errors5xx    int64          `json:"errors_5xx"`
		} `json:"window"`
	}
	if err := getJSON(fmt.Sprintf("%s/admin/resources?series=900&since=%d", base, sinceMs), key, &env); err != nil {
		return nil
	}
	w := env.Window
	return &WindowVerdict{
		Dominant: w.Dominant, Owner: w.Owner, Reason: w.Reason,
		Distribution: w.Distribution, TrafficTicks: w.TrafficTicks,
		PeakRPS: w.PeakRPS, PeakP99Ms: w.PeakP99Ms,
		Requests: w.Requests, Shed: w.Shed, Errors5xx: w.Errors5xx,
	}
}

func engineSnapshot(base, key string) *EngineSample {
	var env struct {
		Latest struct {
			Runtime struct {
				HeapObjectsBytes  uint64  `json:"heap_objects_bytes"`
				RuntimeTotalBytes uint64  `json:"memory_total_bytes"`
				Goroutines        int     `json:"goroutines"`
				GCCPUFraction     float64 `json:"gc_cpu_fraction"`
				GCCyclesTotal     uint64  `json:"gc_cycles_total"`
			} `json:"runtime"`
			// The process card is published as "process_cgroup", not
			// "process" — reading the wrong key is silent (a zero), which is
			// how this session's first soak recorded four hours of RSS = 0.
			Process struct {
				RSSBytes    uint64 `json:"rss_bytes"`
				MemCurrentB uint64 `json:"mem_current_bytes"`
			} `json:"process_cgroup"`
			DBClient struct {
				TotalConns    int32 `json:"total_conns"`
				AcquiredConns int32 `json:"acquired_conns"`
				MaxConns      int32 `json:"max_conns"`
				Warming       bool  `json:"warming"`
			} `json:"db_client"`
			Attribution string `json:"attribution"`
		} `json:"latest"`
	}
	if err := getJSON(base+"/admin/resources", key, &env); err != nil {
		return nil
	}
	l := env.Latest
	return &EngineSample{
		HeapObjectsBytes:  l.Runtime.HeapObjectsBytes,
		RuntimeTotalBytes: l.Runtime.RuntimeTotalBytes,
		RSSBytes:          l.Process.RSSBytes,
		Goroutines:        l.Runtime.Goroutines,
		GCCyclesTotal:     l.Runtime.GCCyclesTotal,
		PoolTotal:         l.DBClient.TotalConns,
		PoolMax:           l.DBClient.MaxConns,
		Attribution:       l.Attribution,
	}
}

func getJSON(url, adminKey string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if adminKey != "" {
		req.Header.Set("X-Admin-Key", adminKey)
	}
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "capacity:", err)
	os.Exit(1)
}
