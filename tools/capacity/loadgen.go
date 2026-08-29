package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// The generator is OPEN-MODEL by construction: requests are issued on a fixed
// schedule that does not wait for the previous one to finish. A closed model
// (a fixed number of virtual users, each sending the next request only after
// its answer arrives) throttles ITSELF when the server slows down, so it never
// measures degradation — the load "waits" for the system instead of arriving
// at it. Real internet traffic does not wait.
//
// Coordinated omission is corrected by construction too, not by a
// post-processing trick. Every request has a SCHEDULED time t_i = start + i/λ
// fixed before the run begins. Two latencies are recorded:
//
//	service  = done − actually_sent   (what the server took once it was asked)
//	response = done − scheduled       (what a user who wanted an answer at t_i waited)
//
// When the system keeps up the two are the same. When it stalls, the requests
// that "should" have been sent during the stall are still counted, and their
// response latency includes the wait — exactly the samples a closed-loop
// generator silently never takes. Both are reported: their DIVERGENCE is the
// evidence that the correction is real, and their agreement at low load is the
// evidence the instrument is not inventing it.

// Sample is one request.
type Sample struct {
	SchedNs   int64
	SendNs    int64
	DoneNs    int64
	Status    int
	Err       bool
	Abandoned bool
	BytesRead int64
}

// Target describes what to send. URLFor lets a workload make every request URI
// unique (the only way to measure past the engine's 5 s response cache).
type Target struct {
	Method  string
	URLFor  func(i int64) string
	Body    func(i int64) []byte
	Headers map[string]string
	Host    string
	// LocalAddrs are source IPs to spread the requests over — the only way to
	// tell a per-IP throttle from a per-tenant one WITHOUT changing the app:
	// the same offered rate from one address and from N addresses either
	// sheds the same (the key is the tenant) or sheds N times less (the key
	// is the address). Every 127.x address is local on Linux, so eight of
	// them model eight phones without a second machine.
	LocalAddrs []string
}

// RunConfig is one load level.
type RunConfig struct {
	Rate        float64 // offered requests per second (the open-model arrival rate)
	Duration    time.Duration
	Warmup      time.Duration // discarded from the statistics, still sent
	MaxInFlight int           // hard cap on concurrent requests; 0 = max(64, 2 × rate)
	Timeout     time.Duration
	// Patience is how long a request waits for its turn before the client
	// GIVES UP and never sends it. Past the ceiling an open-model generator
	// accumulates an unbounded backlog — the honest model of a real client is
	// not "wait forever", it is "abandon", and abandoning is also what keeps
	// the generator from OOM-ing the box it is measuring. An abandoned request
	// is COUNTED (status 0, error) with its full scheduled-to-abandon latency:
	// it is the opposite of coordinated omission, not a version of it.
	Patience time.Duration
}

// RunResult is one load level's measurement.
type RunResult struct {
	Label       string    `json:"label"`
	OfferedRPS  float64   `json:"offered_rps"`
	DurationS   float64   `json:"duration_s"`
	Sent        int64     `json:"sent"`
	Completed   int64     `json:"completed"`
	Errors      int64     `json:"transport_errors"`
	Abandoned   int64     `json:"abandoned"`
	Status2xx   int64     `json:"status_2xx"`
	Status4xx   int64     `json:"status_4xx"`
	Status429   int64     `json:"status_429"`
	Status5xx   int64     `json:"status_5xx"`
	Status503   int64     `json:"status_503"`
	AchievedRPS float64   `json:"achieved_rps"` // completions per second over the measured window
	GoodputRPS  float64   `json:"goodput_rps"`  // 2xx per second — the only throughput a user would call throughput
	ServiceMs   Percent   `json:"service_ms"`   // measured from the actual send
	ResponseMs  Percent   `json:"response_ms"`  // measured from the scheduled send (coordinated omission corrected)
	MeanRespMs  float64   `json:"mean_response_ms"`
	MeanServMs  float64   `json:"mean_service_ms"`
	Concurrency float64   `json:"concurrency"` // Little: achieved_rps × mean_response_s
	MaxInFlight int64     `json:"max_in_flight_observed"`
	BytesPerReq float64   `json:"bytes_per_request"`
	StartUnixMs int64     `json:"start_unix_ms"`
	EndUnixMs   int64     `json:"end_unix_ms"`
	CPU         CPUReport `json:"cpu"`
	Workload    string    `json:"workload,omitempty"`
	Repeat      int       `json:"repeat,omitempty"`
	// Verdict is the engine's own attribution over this window (Module C).
	Verdict *WindowVerdict `json:"verdict,omitempty"`
	// Engine is the endurance run's per-slice sample of the engine's memory.
	Engine *EngineSample `json:"engine,omitempty"`
}

// Percent is the latency distribution of one run.
type Percent struct {
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

func newClient(maxConns int, timeout time.Duration, laddr string) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if laddr != "" {
		d.LocalAddr = &net.TCPAddr{IP: net.ParseIP(laddr)}
	}
	tr := &http.Transport{
		DialContext:         d.DialContext,
		MaxIdleConns:        maxConns,
		MaxIdleConnsPerHost: maxConns,
		MaxConnsPerHost:     maxConns,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // lab tool, may target a self-signed local box
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

// Run issues cfg.Rate requests per second for cfg.Duration and returns the
// measured result. It never blocks its own scheduler: a request that cannot
// start because the in-flight cap is reached still carries its scheduled time,
// so the wait is measured rather than omitted.
func Run(ctx context.Context, t Target, cfg RunConfig, label string) (RunResult, []Sample) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	inflightCap := cfg.MaxInFlight
	if inflightCap <= 0 {
		inflightCap = int(math.Max(64, cfg.Rate*2))
	}
	addrs := t.LocalAddrs
	if len(addrs) == 0 {
		addrs = []string{""}
	}
	clients := make([]*http.Client, len(addrs))
	for i, a := range addrs {
		clients[i] = newClient(inflightCap, cfg.Timeout, a)
	}
	defer func() {
		for _, c := range clients {
			c.CloseIdleConnections()
		}
	}()

	total := int64(cfg.Rate * cfg.Duration.Seconds())
	if total <= 0 {
		total = 1
	}
	samples := make([]Sample, total)
	var sent, inflight, maxInflight int64

	sem := make(chan struct{}, inflightCap)
	var wg sync.WaitGroup
	start := time.Now()
	interval := time.Duration(float64(time.Second) / cfg.Rate)

	for i := int64(0); i < total; i++ {
		sched := start.Add(time.Duration(i) * interval)
		if d := time.Until(sched); d > 0 {
			timer := time.NewTimer(d)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				total = i
				goto done
			}
		}
		samples[i].SchedNs = sched.UnixNano()
		wg.Add(1)
		atomic.AddInt64(&sent, 1)
		// The semaphore is taken by the WORKER, not by the scheduler: the
		// scheduler must never be blocked by a slow server, or the arrival
		// process stops being open and coordinated omission is back.
		go func(idx int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cur := atomic.AddInt64(&inflight, 1)
			for {
				m := atomic.LoadInt64(&maxInflight)
				if cur <= m || atomic.CompareAndSwapInt64(&maxInflight, m, cur) {
					break
				}
			}
			defer atomic.AddInt64(&inflight, -1)
			s := &samples[idx]
			now := time.Now()
			if cfg.Patience > 0 && now.Sub(time.Unix(0, s.SchedNs)) > cfg.Patience {
				s.SendNs, s.DoneNs, s.Err, s.Abandoned = now.UnixNano(), now.UnixNano(), true, true
				return
			}
			s.SendNs = now.UnixNano()
			st, n, err := do(ctx, clients[int(idx)%len(clients)], t, idx, cfg.Timeout)
			s.DoneNs = time.Now().UnixNano()
			s.Status, s.BytesRead, s.Err = st, n, err
		}(i)
	}
done:
	wg.Wait()
	end := time.Now()
	return summarize(label, cfg, samples[:total], start, end), samples[:total]
}

func do(ctx context.Context, c *http.Client, t Target, i int64, timeout time.Duration) (int, int64, bool) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var body io.Reader
	if t.Body != nil {
		if b := t.Body(i); b != nil {
			body = bytesReader(b)
		}
	}
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(rctx, method, t.URLFor(i), body)
	if err != nil {
		return 0, 0, true
	}
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}
	if t.Host != "" {
		req.Host = t.Host
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, 0, true
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close() //nolint:errcheck
	return resp.StatusCode, n, false
}

func summarize(label string, cfg RunConfig, s []Sample, start, end time.Time) RunResult {
	r := RunResult{
		Label: label, OfferedRPS: cfg.Rate, Sent: int64(len(s)),
		StartUnixMs: start.UnixMilli(), EndUnixMs: end.UnixMilli(),
	}
	warmUntil := start.Add(cfg.Warmup).UnixNano()
	var serv, resp []float64
	var bytes, sumResp, sumServ float64
	var firstNs, lastNs int64
	for i := range s {
		x := &s[i]
		if x.DoneNs == 0 || x.SchedNs < warmUntil {
			continue
		}
		if firstNs == 0 || x.SchedNs < firstNs {
			firstNs = x.SchedNs
		}
		if x.DoneNs > lastNs {
			lastNs = x.DoneNs
		}
		r.Completed++
		if x.Err {
			r.Errors++
		}
		if x.Abandoned {
			r.Abandoned++
		}
		switch {
		case x.Status >= 200 && x.Status < 300:
			r.Status2xx++
		case x.Status == 429:
			r.Status429++
			r.Status4xx++
		case x.Status >= 400 && x.Status < 500:
			r.Status4xx++
		case x.Status == 503:
			r.Status503++
			r.Status5xx++
		case x.Status >= 500:
			r.Status5xx++
		}
		sv := float64(x.DoneNs-x.SendNs) / 1e6
		rs := float64(x.DoneNs-x.SchedNs) / 1e6
		serv = append(serv, sv)
		resp = append(resp, rs)
		sumServ += sv
		sumResp += rs
		bytes += float64(x.BytesRead)
	}
	window := float64(lastNs-firstNs) / 1e9
	if window <= 0 {
		window = math.Max(cfg.Duration.Seconds()-cfg.Warmup.Seconds(), 1e-9)
	}
	r.DurationS = window
	if r.Completed > 0 {
		r.AchievedRPS = float64(r.Completed) / window
		r.GoodputRPS = float64(r.Status2xx) / window
		r.MeanRespMs = sumResp / float64(r.Completed)
		r.MeanServMs = sumServ / float64(r.Completed)
		r.BytesPerReq = bytes / float64(r.Completed)
		r.Concurrency = r.AchievedRPS * (r.MeanRespMs / 1000)
	}
	r.ServiceMs, r.ResponseMs = pct(serv), pct(resp)
	return r
}

func pct(v []float64) Percent {
	if len(v) == 0 {
		return Percent{}
	}
	sort.Float64s(v)
	return Percent{
		P50: quantileOf(v, 0.50), P90: quantileOf(v, 0.90),
		P95: quantileOf(v, 0.95), P99: quantileOf(v, 0.99),
		Max: v[len(v)-1],
	}
}

type sliceReader struct {
	b []byte
	i int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.i >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.i:])
	s.i += n
	return n, nil
}

func bytesReader(b []byte) io.Reader { return &sliceReader{b: b} }

func (r RunResult) String() string {
	return fmt.Sprintf("%-22s offered %7.0f  achieved %7.1f  goodput %7.1f  N %6.2f  p50 %7.2f  p99 %8.2f (resp)  |  p50 %7.2f p99 %8.2f (serv)  2xx %d 429 %d 503 %d 5xx %d err %d",
		r.Label, r.OfferedRPS, r.AchievedRPS, r.GoodputRPS, r.Concurrency,
		r.ResponseMs.P50, r.ResponseMs.P99, r.ServiceMs.P50, r.ServiceMs.P99,
		r.Status2xx, r.Status429, r.Status503, r.Status5xx, r.Errors)
}
