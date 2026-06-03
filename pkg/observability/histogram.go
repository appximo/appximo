package observability

import (
	"sync"

	hdrhistogram "github.com/HdrHistogram/hdrhistogram-go"
)

// TenantHistogram records request latency per tenant using HDR histograms,
// split by whether the response was served from cache.
type TenantHistogram struct {
	mu       sync.RWMutex
	cached   map[string]*hdrhistogram.Histogram
	uncached map[string]*hdrhistogram.Histogram
}

func NewTenantHistogram() *TenantHistogram {
	return &TenantHistogram{
		cached:   make(map[string]*hdrhistogram.Histogram),
		uncached: make(map[string]*hdrhistogram.Histogram),
	}
}

// Record adds a latency sample (in microseconds) for a given tenant.
// fromCache distinguishes cache hits (sub-ms) from full DB-backed responses.
func (th *TenantHistogram) Record(tenantID string, durationUs int64, fromCache bool) {
	th.mu.Lock()
	if fromCache {
		th.ensureHist(th.cached, tenantID)
		th.cached[tenantID].RecordValue(durationUs) //nolint:errcheck
	} else {
		th.ensureHist(th.uncached, tenantID)
		th.uncached[tenantID].RecordValue(durationUs) //nolint:errcheck
	}
	th.mu.Unlock()
}

func (th *TenantHistogram) ensureHist(m map[string]*hdrhistogram.Histogram, id string) {
	if _, ok := m[id]; !ok {
		// Range: 1µs – 10s (10_000_000µs), 3 significant figures.
		m[id] = hdrhistogram.New(1, 10_000_000, 3)
	}
}

// PercentileSnapshot holds a point-in-time latency summary for one bucket.
// TenantID is intentionally absent — the response envelope provides it.
type PercentileSnapshot struct {
	P50Us  float64 `json:"p50_us"`
	P95Us  float64 `json:"p95_us"`
	P99Us  float64 `json:"p99_us"`
	P999Us float64 `json:"p999_us"`
	Count  int64   `json:"count"`
	Mean   float64 `json:"mean_us"`
}

// FullSnapshot splits latency into cache-hit vs cache-miss buckets.
type FullSnapshot struct {
	Cached   *PercentileSnapshot `json:"cached,omitempty"`
	Uncached *PercentileSnapshot `json:"uncached,omitempty"`
}

// FullSnapshot returns the cached/uncached latency split for a tenant, or nil
// buckets when no data has been recorded for that category.
func (th *TenantHistogram) FullSnapshot(tenantID string) *FullSnapshot {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return &FullSnapshot{
		Cached:   snapshotOf(th.cached[tenantID]),
		Uncached: snapshotOf(th.uncached[tenantID]),
	}
}

// Snapshot returns the uncached latency snapshot.
// Kept for test backward-compatibility; prefer FullSnapshot in production paths.
func (th *TenantHistogram) Snapshot(tenantID string) *PercentileSnapshot {
	th.mu.RLock()
	defer th.mu.RUnlock()
	return snapshotOf(th.uncached[tenantID])
}

func snapshotOf(h *hdrhistogram.Histogram) *PercentileSnapshot {
	if h == nil || h.TotalCount() == 0 {
		return nil
	}
	return &PercentileSnapshot{
		P50Us:  float64(h.ValueAtQuantile(50)),
		P95Us:  float64(h.ValueAtQuantile(95)),
		P99Us:  float64(h.ValueAtQuantile(99)),
		P999Us: float64(h.ValueAtQuantile(99.9)),
		Count:  h.TotalCount(),
		Mean:   h.Mean(),
	}
}
