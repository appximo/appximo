package observability

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ewmaAlpha  = 0.05
	zThreshold = 3.0
)

// anomalyRingSize bounds the per-tenant recent-anomaly ring. Anomalies are rare and
// only the most recent matter for the admin panel, so a small fixed ring is plenty
// (and the read-only exposure never grows without bound).
const anomalyRingSize = 32

// AnomalyEvent is one detected latency anomaly, retained so the admin observability
// panel can show WHEN it happened, the request latency, and the z-score — data the
// detector already computes on detection but previously only logged + counted.
type AnomalyEvent struct {
	TS        int64   `json:"ts"`         // unix microseconds (matches trace TS)
	LatencyUS float64 `json:"latency_us"` // request latency in microseconds
	ZScore    float64 `json:"z_score"`
}

// anomalyRing is a fixed circular buffer of recent AnomalyEvents for one tenant.
type anomalyRing struct {
	buf  [anomalyRingSize]AnomalyEvent
	head int // total events ever recorded
	n    int // number of populated slots (<= anomalyRingSize)
}

type ewmaState struct {
	mean, varc float64
	init       bool
}

// AnomalyDetector flags requests whose latency is statistically anomalous
// using an exponentially-weighted moving average of mean and variance.
type AnomalyDetector struct {
	mu       sync.RWMutex
	states   map[string]*ewmaState
	counters sync.Map // tenantID → *atomic.Int64

	// events holds a small per-tenant ring of recent anomalies, guarded by its own
	// mutex so recording one never contends with Observe's hot-path lock (d.mu). It
	// is touched ONLY when Observe reports an anomaly (rare), so the common request
	// path is unaffected.
	evMu   sync.Mutex
	events map[string]*anomalyRing
}

func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		states: make(map[string]*ewmaState),
		events: make(map[string]*anomalyRing),
	}
}

// Observe updates the EWMA model for tenantID with a new latency sample (µs).
// Returns (true, z-score) when the sample is anomalous, (false, 0) otherwise.
func (d *AnomalyDetector) Observe(tenantID string, ms float64) (bool, float64) {
	d.mu.Lock()
	s, ok := d.states[tenantID]
	if !ok {
		if len(d.states) >= maxTrackedTenants {
			// Cap reached: do not model another (possibly attacker-rotated)
			// tenant. See limits.go.
			d.mu.Unlock()
			return false, 0
		}
		s = &ewmaState{}
		d.states[tenantID] = s
	}
	if !s.init {
		s.mean, s.init = ms, true
		d.mu.Unlock()
		return false, 0
	}
	diff := ms - s.mean
	s.mean = ewmaAlpha*ms + (1-ewmaAlpha)*s.mean
	s.varc = ewmaAlpha*diff*diff + (1-ewmaAlpha)*s.varc
	// Snapshot varc under the lock: another goroutine may update this same
	// *ewmaState concurrently, so the post-unlock math must read the local copy,
	// not s.varc (a data race the -race detector flags under concurrent load).
	varc := s.varc
	d.mu.Unlock()
	if varc <= 0 {
		return false, 0
	}
	z := math.Abs(diff) / math.Sqrt(varc)
	return z > zThreshold, z
}

// IncrCounter increments the anomaly count for tenantID by one.
func (d *AnomalyDetector) IncrCounter(tenantID string) {
	v, _ := d.counters.LoadOrStore(tenantID, &atomic.Int64{})
	v.(*atomic.Int64).Add(1)
}

// RecordAnomaly increments the tenant's anomaly counter AND appends the event to its
// recent-anomaly ring. Call it (instead of IncrCounter) when Observe reports an
// anomaly: latencyUS is the offending request's latency and z the detected z-score.
// Off the common request path — runs only on a detection (z > zThreshold).
func (d *AnomalyDetector) RecordAnomaly(tenantID string, latencyUS, z float64) {
	d.IncrCounter(tenantID)
	d.evMu.Lock()
	r := d.events[tenantID]
	if r == nil {
		if len(d.events) >= maxTrackedTenants {
			// Cap reached: do not retain events for another (possibly
			// attacker-rotated) tenant. Mirrors the states/rings caps. See limits.go.
			d.evMu.Unlock()
			return
		}
		r = &anomalyRing{}
		d.events[tenantID] = r
	}
	r.buf[r.head%anomalyRingSize] = AnomalyEvent{TS: time.Now().UnixMicro(), LatencyUS: latencyUS, ZScore: z}
	r.head++
	if r.n < anomalyRingSize {
		r.n++
	}
	d.evMu.Unlock()
}

// RecentAnomalies returns up to n of tenantID's most recent anomalies, newest first.
// Returns a non-nil empty slice when none are recorded, so it marshals to [] not null.
func (d *AnomalyDetector) RecentAnomalies(tenantID string, n int) []AnomalyEvent {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	r := d.events[tenantID]
	if r == nil {
		return []AnomalyEvent{}
	}
	if n > r.n {
		n = r.n
	}
	out := make([]AnomalyEvent, 0, n)
	for i := 0; i < n; i++ {
		idx := (r.head - 1 - i + anomalyRingSize) % anomalyRingSize
		out = append(out, r.buf[idx])
	}
	return out
}

// GetCount returns the total anomaly count for tenantID since startup.
func (d *AnomalyDetector) GetCount(tenantID string) int64 {
	v, ok := d.counters.Load(tenantID)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}
