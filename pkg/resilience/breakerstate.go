package resilience

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	zlog "github.com/rs/zerolog/log"
	"github.com/sony/gobreaker"
)

// The breaker used to change state in silence: it opened, every write answered
// 503 for 8 s, and there was no log line, no metric and no way to tell "the
// breaker did this" from "the engine broke" (AUTO-4, AUTOMATIZACION-S1). This
// file is the missing chain: gobreaker's OnStateChange → a structured log line +
// a per-breaker state registry that /metrics (BreakerCollector) and
// /admin/outbox-style surfaces read.

// BreakerStatus is one breaker's current, observable state.
type BreakerStatus struct {
	Name        string    `json:"name"`
	State       string    `json:"state"` // closed | half-open | open
	Opens       int64     `json:"opens_total"`
	LastChange  time.Time `json:"last_change"`
	LastOpened  time.Time `json:"last_opened,omitempty"`
	Transitions int64     `json:"transitions_total"`
}

var (
	breakerMu  sync.RWMutex
	breakerReg = map[string]*BreakerStatus{}
)

// stateNum maps a state to the gauge value: 0 closed, 1 half-open, 2 open.
func stateNum(s string) float64 {
	switch s {
	case "open":
		return 2
	case "half-open":
		return 1
	default:
		return 0
	}
}

// recordStateChange is wired as gobreaker.Settings.OnStateChange for every
// breaker built by NewQueryBreakerWith.
func recordStateChange(name string, from, to gobreaker.State) {
	now := time.Now()
	breakerMu.Lock()
	st, ok := breakerReg[name]
	if !ok {
		st = &BreakerStatus{Name: name}
		breakerReg[name] = st
	}
	st.State = to.String()
	st.LastChange = now
	st.Transitions++
	if to == gobreaker.StateOpen {
		st.Opens++
		st.LastOpened = now
	}
	breakerMu.Unlock()

	evt := zlog.Warn()
	if to == gobreaker.StateClosed {
		evt = zlog.Info()
	}
	evt.Str("breaker", name).
		Str("from", from.String()).
		Str("to", to.String()).
		Msg("circuit breaker state change — while OPEN, writes answer 503 naming the breaker; it re-probes after the 8s window")
}

// BreakerSnapshot returns the current state of every breaker that has ever
// transitioned. A breaker that never left closed does not appear (it has
// nothing to report).
func BreakerSnapshot() []BreakerStatus {
	breakerMu.RLock()
	defer breakerMu.RUnlock()
	out := make([]BreakerStatus, 0, len(breakerReg))
	for _, st := range breakerReg {
		out = append(out, *st)
	}
	return out
}

// BreakerCollector exposes the registry on /metrics:
//
//	appximo_breaker_state{name}        0 closed · 1 half-open · 2 open
//	appximo_breaker_opens_total{name}  times the breaker has opened
type BreakerCollector struct {
	state *prometheus.Desc
	opens *prometheus.Desc
}

// NewBreakerCollector builds the collector for registration.
func NewBreakerCollector() *BreakerCollector {
	return &BreakerCollector{
		state: prometheus.NewDesc("appximo_breaker_state",
			"Circuit breaker state (0 closed, 1 half-open, 2 open)", []string{"name"}, nil),
		opens: prometheus.NewDesc("appximo_breaker_opens_total",
			"Times the circuit breaker has opened", []string{"name"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *BreakerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.state
	ch <- c.opens
}

// Collect implements prometheus.Collector.
func (c *BreakerCollector) Collect(ch chan<- prometheus.Metric) {
	for _, st := range BreakerSnapshot() {
		ch <- prometheus.MustNewConstMetric(c.state, prometheus.GaugeValue, stateNum(st.State), st.Name)
		ch <- prometheus.MustNewConstMetric(c.opens, prometheus.CounterValue, float64(st.Opens), st.Name)
	}
}
