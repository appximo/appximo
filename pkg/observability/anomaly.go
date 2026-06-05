package observability

import (
	"math"
	"sync"
	"sync/atomic"
)

const (
	ewmaAlpha  = 0.05
	zThreshold = 3.0
)

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
}

func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		states: make(map[string]*ewmaState),
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

// GetCount returns the total anomaly count for tenantID since startup.
func (d *AnomalyDetector) GetCount(tenantID string) int64 {
	v, ok := d.counters.Load(tenantID)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}
