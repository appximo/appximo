package observability

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Check describes one synthetic health probe.
type Check struct {
	Name     string
	URL      string
	Method   string
	Headers  map[string]string
	Expected int // expected HTTP status code; 0 = any 2xx
}

// CheckResult is the last known state of a synthetic check.
type CheckResult struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	LatencyMs int64     `json:"latency_ms"`
	LastCheck time.Time `json:"last_check"`
	LastError string    `json:"last_error,omitempty"`
	Uptime    float64   `json:"uptime_pct"`
	total     int64
	failures  int64
}

// DynamicCheck resolves its probe lazily on every tick. Resolve returns the
// Check to run, or nil plus a human-readable reason — reported as status
// "pending" instead of a failing probe. This exists for canaries whose target
// only makes sense once external state exists (e.g. an API canary on a fresh
// install, before any tenant is registered: probing would just spam 4xx for
// a route nobody created).
type DynamicCheck struct {
	Name    string
	Resolve func(ctx context.Context) (*Check, string)
}

// SyntheticMonitor periodically probes a list of HTTP endpoints.
type SyntheticMonitor struct {
	checks   []Check
	dynamics []DynamicCheck
	results  sync.Map
	client   *http.Client
}

func NewSyntheticMonitor(checks []Check) *SyntheticMonitor {
	return &SyntheticMonitor{
		checks: checks,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// AddDynamic registers a lazily-resolved check. Must be called before Start.
func (sm *SyntheticMonitor) AddDynamic(d DynamicCheck) {
	sm.dynamics = append(sm.dynamics, d)
}

// Start launches the probe loop. Runs until ctx is cancelled.
func (sm *SyntheticMonitor) Start(ctx context.Context, interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				for _, c := range sm.checks {
					go sm.run(ctx, c)
				}
				for _, d := range sm.dynamics {
					go sm.runDynamic(ctx, d)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Results returns all current check results as a map keyed by check name.
func (sm *SyntheticMonitor) Results() map[string]*CheckResult {
	out := make(map[string]*CheckResult)
	sm.results.Range(func(k, v any) bool {
		out[k.(string)] = v.(*CheckResult)
		return true
	})
	return out
}

// runDynamic resolves a DynamicCheck and probes it, or records a "pending"
// result. Pending ticks carry the previous uptime stats forward unchanged —
// "nothing to probe yet" is neither an up nor a down.
func (sm *SyntheticMonitor) runDynamic(ctx context.Context, d DynamicCheck) {
	c, reason := d.Resolve(ctx)
	if c == nil {
		r := &CheckResult{Name: d.Name, Status: "pending", LastCheck: time.Now(), LastError: reason}
		if prev, ok := sm.results.Load(d.Name); ok {
			old := prev.(*CheckResult)
			r.total, r.failures, r.Uptime = old.total, old.failures, old.Uptime
		}
		sm.results.Store(d.Name, r)
		return
	}
	c.Name = d.Name
	sm.run(ctx, *c)
}

func (sm *SyntheticMonitor) run(ctx context.Context, c Check) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, c.Method, c.URL, nil)
	if err != nil {
		return
	}
	for k, v := range c.Headers {
		if k == "Host" {
			// Go's http.Client ignores Host in req.Header; must set req.Host directly.
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	r := &CheckResult{Name: c.Name, LastCheck: time.Now()}

	resp, doErr := sm.client.Do(req)
	r.LatencyMs = time.Since(start).Milliseconds()

	if prev, ok := sm.results.Load(c.Name); ok {
		old := prev.(*CheckResult)
		r.total = old.total + 1
		r.failures = old.failures
	} else {
		r.total = 1
	}

	if doErr != nil || (c.Expected > 0 && resp != nil && resp.StatusCode != c.Expected) {
		r.Status = "down"
		r.failures++
		if doErr != nil {
			r.LastError = doErr.Error()
		}
	} else {
		r.Status = "up"
		if resp != nil {
			resp.Body.Close()
		}
	}

	if r.total > 0 {
		r.Uptime = float64(r.total-r.failures) / float64(r.total) * 100
	}
	sm.results.Store(c.Name, r)
}
