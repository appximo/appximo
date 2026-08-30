package resilience

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Admission — degrade instead of tipping (ENG-52, MOTOR-PRODUCCION-S2).
//
// THE FAILURE IT REMOVES, measured in the isolated laboratory
// (docs/BENCHMARKS.md §4e): the engine accepted UNBOUNDED concurrency. In an
// open-arrival overload — offered load past the box's ceiling (~1 100 rps on a
// customer s-2vcpu-2gb, ~1 600 on a dedicated c-2) — in-flight requests
// accumulate without bound: each one is a goroutine, buffers, and a place in
// the pgxpool acquire queue (unbounded, FIFO). Every queued request — including
// the ones that will die at the 5 s query timeout or the client's own patience
// — pays the WHOLE pre-pool pipeline (parse, route, tenant, JWT, RBAC, query
// build), so wasted work grows with the backlog and eats exactly the CPU the
// admitted work needed. That is the positive feedback that makes the collapse
// METASTABLE: backlog → less useful capacity → more backlog; measured, a run
// at 1 100 rps tips to a seconds-scale p50 with LESS goodput and never comes
// back inside the run (6/8 runs; service p50 jumps to ~450–650 ms, all of it
// queue). The old backstop (queryTimeout = 5 s) fires after five seconds of
// held resources — far too late, and the work is thrown away after being paid
// for.
//
// THE MECHANISM: a hard cap on in-flight admission-scoped requests, enforced
// at the FRONT of the chain (before the tenant limiter, the logger, the cache,
// JWT — before any per-request work beyond one atomic add and a path check).
// Over the cap → immediate 429 with Retry-After — the cheapest possible
// refusal, milliseconds-early instead of seconds-late.
//
// WHY A CONCURRENCY CAP AND NOT THE ALTERNATIVES (argued, not asserted):
//
//   - Little's law makes concurrency the SELF-ADAPTIVE quantity: X = N/R. At
//     full capacity with healthy latency the measured N is tiny (≈ 1–5 at
//     1 000–1 400 rps × 1.6–3 ms); tipped runs measure N in the THOUSANDS.
//     Three orders of magnitude separate the regimes, so a crude cap cleanly
//     splits them with margin on both sides — no estimator needed. The same N
//     cap yields each box's own rps ceiling (a 40 % faster plan just serves
//     more per slot) and each workload's own (a heavy screen with 10× the R
//     admits 10× fewer rps — which is exactly its real capacity). A RATE
//     admission would need the ceiling in rps, which varies 20× per endpoint
//     and 40 % per plan — the ENG-53 trap.
//   - Latency-gradient adaptive limits (the Netflix concurrency-limits
//     family) buy precision this system does not need (see the 1000× regime
//     gap) and pay for it with an estimator that whipsaws on benign latency
//     spikes — GC, autovacuum, a shared-vCPU neighbour (measured on the $18
//     box: p99 wandering 10–314 ms between healthy repeats). A false
//     rejection under normal load is the one non-negotiable failure mode.
//   - A bounded queue with deadline (CoDel-style) still buffers (memory +
//     added latency) and adds a tuning surface; clients already retry, and an
//     immediate 429 + Retry-After is both cheaper and more honest.
//   - Pool-pressure admission (reject when acquire wait is high) fires LATE —
//     after JWT/RBAC/parse are paid — and misses CPU-bound overload that
//     never queues on the pool (aggregates, hooks).
//
// SCOPE: everything except infra (probes, /metrics, /debug, /admin, /editor),
// OPTIONS preflight (CORS answers pre-auth), SSE streams (held open for
// minutes BY DESIGN — the `/events` suffix, the same rule the response cache
// uses), and byte-serving downloads (client-paced sendfile, not CPU). A
// long-lived connection inside the cap would consume a slot doing no work.
//
// KNOB: APPXIMO_MAX_INFLIGHT. Unset/"auto" → max(32, 4×(GOMAXPROCS + pool
// max conns)) — on the reference 2-vCPU/10-conn box that is 48, bounding the
// admitted queue at ≈ 48 slots ≈ tens of ms of admitted latency at capacity
// while leaving ≥ 10× headroom over the healthy N of the fastest measured
// workload. "0" disables. A non-integer refuses to boot (the ENG-47 rule: a
// safety knob never falls back silently).
type Admission struct {
	limit    int64
	inflight atomic.Int64
	rejected atomic.Int64
	// exempt reports requests admission never counts (byte-serving downloads;
	// the rest of the scope rules are inline). Nil ⇒ only the inline rules.
	exempt func(r *http.Request) bool
}

// AdmissionEnvVar is the operator knob (in-flight cap; 0 disables; unset = auto).
const AdmissionEnvVar = "APPXIMO_MAX_INFLIGHT"

// NewAdmissionFromEnv builds the controller. cores is runtime.GOMAXPROCS(0),
// poolConns the database pool's MaxConns — the two quantities the auto formula
// derives the cap from (both are per-box facts, not guesses). exempt may be
// nil. Returns (nil, nil) when disabled.
func NewAdmissionFromEnv(cores, poolConns int, exempt func(r *http.Request) bool) (*Admission, error) {
	raw := strings.TrimSpace(os.Getenv(AdmissionEnvVar))
	var limit int64
	switch raw {
	case "", "auto":
		auto := int64(4 * (cores + poolConns))
		if auto < 32 {
			auto = 32
		}
		limit = auto
	case "0":
		return nil, nil
	default:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%s=%q: must be a non-negative integer (0 disables, unset = auto) — refusing to boot rather than guess", AdmissionEnvVar, raw)
		}
		if n == 0 {
			return nil, nil
		}
		limit = n
	}
	return &Admission{limit: limit, exempt: exempt}, nil
}

// Limit is the effective in-flight cap (for the boot log).
func (a *Admission) Limit() int64 { return a.limit }

// InFlight is the current admitted count (metrics).
func (a *Admission) InFlight() int64 { return a.inflight.Load() }

// Rejected is the total shed so far (metrics).
func (a *Admission) Rejected() int64 { return a.rejected.Load() }

// covered reports whether a request occupies an admission slot.
func (a *Admission) covered(r *http.Request) bool {
	if r.Method == http.MethodOptions { // CORS preflight: answered pre-auth, near-free
		return false
	}
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/health"), p == "/readyz", p == "/livez",
		strings.HasPrefix(p, "/metrics"), strings.HasPrefix(p, "/debug"),
		strings.HasPrefix(p, "/admin"), strings.HasPrefix(p, "/editor"),
		strings.HasPrefix(p, "/app"), strings.HasPrefix(p, "/docs"),
		strings.HasPrefix(p, "/openapi"):
		return false
	case strings.HasSuffix(p, "/events"):
		// SSE (S45). Suffix rule shared with the response-cache bypass — the
		// documented tolerance for a resource literally named "events".
		return false
	}
	if a.exempt != nil && a.exempt(r) { // byte-serving downloads (sendfile, client-paced)
		return false
	}
	return true
}

// Middleware enforces the cap. Cost on the admitted path: one atomic add on
// entry, one on exit, a handful of prefix checks. The refusal path allocates
// one small JSON body and touches nothing else — no tenant token, no log
// record, no cache, no JWT, no pool.
func (a *Admission) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.covered(r) {
			next.ServeHTTP(w, r)
			return
		}
		if n := a.inflight.Add(1); n > a.limit {
			a.inflight.Add(-1)
			a.rejected.Add(1)
			w.Header().Set("Content-Type", "application/json")
			// One second: by design the admitted backlog is bounded at
			// `limit` slots (tens of ms at capacity), so a second from now
			// the door is statistically open again.
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"error":       "server at capacity: too many requests in flight",
				"retry_after": 1,
				"knob":        AdmissionEnvVar,
			})
			return
		}
		defer a.inflight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

// PromCollector projects the two counters onto /metrics.
func (a *Admission) PromCollector() prometheus.Collector {
	return &admissionCollector{
		a: a,
		inflight: prometheus.NewDesc("appximo_admission_inflight",
			"admission-scoped requests currently in flight", nil, nil),
		limit: prometheus.NewDesc("appximo_admission_limit",
			"the effective in-flight cap (APPXIMO_MAX_INFLIGHT)", nil, nil),
		rejected: prometheus.NewDesc("appximo_admission_rejected_total",
			"requests shed with 429 by the admission controller", nil, nil),
	}
}

type admissionCollector struct {
	a                         *Admission
	inflight, limit, rejected *prometheus.Desc
}

func (c *admissionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.inflight
	ch <- c.limit
	ch <- c.rejected
}

func (c *admissionCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(c.inflight, prometheus.GaugeValue, float64(c.a.InFlight()))
	ch <- prometheus.MustNewConstMetric(c.limit, prometheus.GaugeValue, float64(c.a.Limit()))
	ch <- prometheus.MustNewConstMetric(c.rejected, prometheus.CounterValue, float64(c.a.Rejected()))
}
