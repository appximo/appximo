package observability

import (
	"sync"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// TenantHistogram records request latency per tenant using HDR histograms.
type TenantHistogram struct {
	mu   sync.RWMutex
	hist map[string]*hdrhistogram.Histogram
}

func NewTenantHistogram() *TenantHistogram {
	return &TenantHistogram{
		hist: make(map[string]*hdrhistogram.Histogram),
	}
}

// Record adds a latency sample (in milliseconds) for a given tenant.
func (th *TenantHistogram) Record(tenantID string, durationMs int64) {
	th.mu.RLock()
	h, ok := th.hist[tenantID]
	th.mu.RUnlock()
	if !ok {
		th.mu.Lock()
		if _, exists := th.hist[tenantID]; !exists {
			th.hist[tenantID] = hdrhistogram.New(1, 10_000, 3)
		}
		h = th.hist[tenantID]
		th.mu.Unlock()
	}
	h.RecordValue(durationMs) //nolint:errcheck
}

// PercentileSnapshot holds a point-in-time latency summary for one tenant.
type PercentileSnapshot struct {
	TenantID string  `json:"tenant_id"`
	P50      float64 `json:"p50_ms"`
	P95      float64 `json:"p95_ms"`
	P99      float64 `json:"p99_ms"`
	P999     float64 `json:"p999_ms"`
	Count    int64   `json:"count"`
	Mean     float64 `json:"mean_ms"`
}

// Snapshot returns the current percentile summary for a tenant, or nil if no data.
func (th *TenantHistogram) Snapshot(tenantID string) *PercentileSnapshot {
	th.mu.RLock()
	h, ok := th.hist[tenantID]
	th.mu.RUnlock()
	if !ok {
		return nil
	}
	return &PercentileSnapshot{
		TenantID: tenantID,
		P50:      float64(h.ValueAtQuantile(50)),
		P95:      float64(h.ValueAtQuantile(95)),
		P99:      float64(h.ValueAtQuantile(99)),
		P999:     float64(h.ValueAtQuantile(99.9)),
		Count:    h.TotalCount(),
		Mean:     h.Mean(),
	}
}
