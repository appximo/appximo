package observability

import (
	"math"
	"sync"
)

const (
	ewmaAlpha   = 0.05
	zThreshold  = 3.0
)

type ewmaState struct {
	mean, varc float64
	init       bool
}

// AnomalyDetector flags requests whose latency is statistically anomalous
// using an exponentially-weighted moving average of mean and variance.
type AnomalyDetector struct {
	mu     sync.RWMutex
	states map[string]*ewmaState
}

func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		states: make(map[string]*ewmaState),
	}
}

// Observe updates the EWMA model for tenantID with a new latency sample (ms).
// Returns (true, z-score) when the sample is anomalous, (false, 0) otherwise.
func (d *AnomalyDetector) Observe(tenantID string, ms float64) (bool, float64) {
	d.mu.Lock()
	s, ok := d.states[tenantID]
	if !ok {
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
	d.mu.Unlock()
	if s.varc <= 0 {
		return false, 0
	}
	z := math.Abs(diff) / math.Sqrt(s.varc)
	return z > zThreshold, z
}
