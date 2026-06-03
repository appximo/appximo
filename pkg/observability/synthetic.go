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

// SyntheticMonitor periodically probes a list of HTTP endpoints.
type SyntheticMonitor struct {
	checks  []Check
	results sync.Map
	client  *http.Client
}

func NewSyntheticMonitor(checks []Check) *SyntheticMonitor {
	return &SyntheticMonitor{
		checks: checks,
		client: &http.Client{Timeout: 5 * time.Second},
	}
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
