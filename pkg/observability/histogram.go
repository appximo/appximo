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

// Record adds a latency sample (in microseconds) for a given tenant.
func (th *TenantHistogram) Record(tenantID string, durationUs int64) {
	th.mu.RLock()
	h, ok := th.hist[tenantID]
	th.mu.RUnlock()
	if !ok {
		th.mu.Lock()
		if _, exists := th.hist[tenantID]; !exists {
			// Range: 1µs – 10s (10_000_000 µs), 3 significant figures.
			th.hist[tenantID] = hdrhistogram.New(1, 10_000_000, 3)
		}
		h = th.hist[tenantID]
		th.mu.Unlock()
	}
	h.RecordValue(durationUs) //nolint:errcheck
}

// PercentileSnapshot holds a point-in-time latency summary for one tenant.
type PercentileSnapshot struct {
	TenantID string  `json:"tenant_id"`
	P50Us    float64 `json:"p50_us"`
	P95Us    float64 `json:"p95_us"`
	P99Us    float64 `json:"p99_us"`
	P999Us   float64 `json:"p999_us"`
	Count    int64   `json:"count"`
	Mean     float64 `json:"mean_us"`
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
		P50Us:    float64(h.ValueAtQuantile(50)),
		P95Us:    float64(h.ValueAtQuantile(95)),
		P99Us:    float64(h.ValueAtQuantile(99)),
		P999Us:   float64(h.ValueAtQuantile(99.9)),
		Count:    h.TotalCount(),
		Mean:     h.Mean(),
	}
}
