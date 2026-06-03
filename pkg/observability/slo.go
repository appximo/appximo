package observability

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// SLO status values.
const (
	statusOK       = "ok"
	statusWarning  = "warning"
	statusCritical = "critical"
)

// Multi-window burn-rate thresholds (Google SRE Workbook, "Alerting on SLOs").
// A burn rate of 14.4x exhausts a 30-day budget in ~2 days; 6x in ~5 days.
const (
	criticalBurn = 14.4 // 5m AND 1h windows
	warningBurn  = 6.0  // 30m AND 6h windows
)

// sloWindowMinutes is the size of the per-tenant minute-rollup ring: 360 minutes = 6h,
// the longest window the burn-rate alerts need.
const sloWindowMinutes = 360

// usPerMinute is one minute expressed in microseconds (Sample.Start unit).
const usPerMinute = 60 * 1_000_000

// SLOConfig holds the per-tenant objectives and alert cadence.
type SLOConfig struct {
	TargetUptime  float64       // e.g. 0.999 (99.9%)
	LatencySLOms  float64       // e.g. 100.0 ms — requests slower than this count as errors
	AlertCooldown time.Duration // e.g. 15min between repeat alerts of the same level
}

// DefaultSLOConfig is the objective applied to every tenant unless overridden.
func DefaultSLOConfig() SLOConfig {
	return SLOConfig{
		TargetUptime:  0.999,
		LatencySLOms:  100.0,
		AlertCooldown: 15 * time.Minute,
	}
}

// errorBudget returns 1 - TargetUptime (the fraction of bad requests allowed).
func (c SLOConfig) errorBudget() float64 { return 1 - c.TargetUptime }

// MinuteBucket is one minute of aggregated traffic. Three uint32 fields, stored in a
// fixed array — no per-minute allocations.
type MinuteBucket struct {
	Count  uint32
	Errors uint32
	SumUS  uint32
}

// tenantBuckets is a fixed circular buffer of sloWindowMinutes MinuteBuckets.
type tenantBuckets struct {
	buf  [sloWindowMinutes]MinuteBucket
	head int // index of the next write
	n    int // number of populated buckets (<= sloWindowMinutes)
}

// windowRatio returns the error ratio (errors/count) over the most recent `minutes`
// buckets, plus the total request count in that window. Ratio is 0 when the window
// has no requests.
func (tb *tenantBuckets) windowRatio(minutes int) (ratio float64, total uint64) {
	if minutes > tb.n {
		minutes = tb.n
	}
	var count, errs uint64
	for i := 0; i < minutes; i++ {
		idx := (tb.head - 1 - i + sloWindowMinutes) % sloWindowMinutes
		count += uint64(tb.buf[idx].Count)
		errs += uint64(tb.buf[idx].Errors)
	}
	if count == 0 {
		return 0, 0
	}
	return float64(errs) / float64(count), count
}

// SLOSnapshot is the JSON projection exposed under /debug/tenant/{id} → "slo".
type SLOSnapshot struct {
	ErrorRatio5m float64 `json:"error_ratio_5m"`
	BurnRate5m   float64 `json:"burn_rate_5m"`
	ErrorRatio1h float64 `json:"error_ratio_1h"`
	BurnRate1h   float64 `json:"burn_rate_1h"`
	Status       string  `json:"status"`
}

// SLOEngine rolls the per-tenant request ring up into minute buckets every 60s,
// evaluates multi-window burn rates, and fires alerts through alerter. It is fully
// independent of the AnomalyDetector.
type SLOEngine struct {
	rings   *Rings
	hist    *TenantHistogram
	alerter Alerter
	cfg     SLOConfig

	mu      sync.RWMutex
	buckets map[string]*tenantBuckets
}

// NewSLOEngine builds an engine with the default SLO config. The provided alerter is
// wrapped in a CooldownAlerter so each (tenant, level) fires at most once per cooldown.
func NewSLOEngine(rings *Rings, hist *TenantHistogram, alerter Alerter) *SLOEngine {
	return NewSLOEngineWithConfig(rings, hist, alerter, DefaultSLOConfig())
}

// NewSLOEngineWithConfig is NewSLOEngine with an explicit config (used in tests to
// shrink the cooldown window).
func NewSLOEngineWithConfig(rings *Rings, hist *TenantHistogram, alerter Alerter, cfg SLOConfig) *SLOEngine {
	return &SLOEngine{
		rings:   rings,
		hist:    hist,
		alerter: NewCooldownAlerter(alerter, cfg.AlertCooldown),
		cfg:     cfg,
		buckets: make(map[string]*tenantBuckets),
	}
}

// Run evaluates all tenants immediately, then every 60s until ctx is cancelled.
func (e *SLOEngine) Run(ctx context.Context) {
	e.tick(ctx, time.Now().UnixMicro())
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			e.tick(ctx, now.UnixMicro())
		}
	}
}

// tick rolls each active tenant's last-minute traffic into a bucket and evaluates it.
func (e *SLOEngine) tick(ctx context.Context, nowUS int64) {
	for _, tid := range e.rings.TenantIDs() {
		b := computeBucket(e.rings.Snapshot(tid), e.cfg, nowUS)
		e.appendBucket(tid, b)
		e.evaluateAndAlert(ctx, tid)
	}
}

// computeBucket aggregates the samples whose Start falls within the last minute.
// A request is an "error" if it returned >= 500 or was slower than the latency SLO.
func computeBucket(samples []Sample, cfg SLOConfig, nowUS int64) MinuteBucket {
	var b MinuteBucket
	cutoff := nowUS - usPerMinute
	latThreshUS := int32(cfg.LatencySLOms * 1000)
	for _, s := range samples {
		if s.Start < cutoff {
			continue
		}
		b.Count++
		if s.DurUS > 0 {
			b.SumUS += uint32(s.DurUS)
		}
		if s.Status >= 500 || s.DurUS > latThreshUS {
			b.Errors++
		}
	}
	return b
}

// appendBucket stores b as tenant tid's newest minute bucket.
func (e *SLOEngine) appendBucket(tid string, b MinuteBucket) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tb := e.buckets[tid]
	if tb == nil {
		tb = &tenantBuckets{}
		e.buckets[tid] = tb
	}
	tb.buf[tb.head] = b
	tb.head = (tb.head + 1) % sloWindowMinutes
	if tb.n < sloWindowMinutes {
		tb.n++
	}
}

// Snapshot returns the current SLO state for a tenant (used by /debug and tests).
func (e *SLOEngine) Snapshot(tid string) SLOSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	tb := e.buckets[tid]
	if tb == nil {
		return SLOSnapshot{Status: statusOK}
	}
	budget := e.cfg.errorBudget()
	r5, _ := tb.windowRatio(5)
	r30, _ := tb.windowRatio(30)
	r1h, _ := tb.windowRatio(60)
	r6h, _ := tb.windowRatio(360)

	status := statusOK
	switch {
	case r5 > criticalBurn*budget && r1h > criticalBurn*budget:
		status = statusCritical
	case r30 > warningBurn*budget && r6h > warningBurn*budget:
		status = statusWarning
	}

	return SLOSnapshot{
		ErrorRatio5m: r5,
		BurnRate5m:   r5 / budget,
		ErrorRatio1h: r1h,
		BurnRate1h:   r1h / budget,
		Status:       status,
	}
}

// evaluateAndAlert sends an alert when the tenant is in warning/critical state.
// Cooldown suppression is handled by the wrapped alerter.
func (e *SLOEngine) evaluateAndAlert(ctx context.Context, tid string) {
	snap := e.Snapshot(tid)
	if snap.Status == statusOK {
		return
	}
	a := Alert{
		TenantID: tid,
		Level:    snap.Status,
		Message:  fmt.Sprintf("SLO: %.0fms", e.cfg.LatencySLOms),
		BurnRate: snap.BurnRate5m,
		P95ms:    e.p95ms(tid),
	}
	if err := e.alerter.Send(ctx, a); err != nil {
		log.Printf("[WARN] slo: alert send failed for tenant %s: %v", tid, err)
	}
}

// p95ms returns the worse of the cached/uncached p95 latency for a tenant, in ms.
func (e *SLOEngine) p95ms(tid string) float64 {
	if e.hist == nil {
		return 0
	}
	fs := e.hist.FullSnapshot(tid)
	var p95us float64
	if fs.Uncached != nil && fs.Uncached.P95Us > p95us {
		p95us = fs.Uncached.P95Us
	}
	if fs.Cached != nil && fs.Cached.P95Us > p95us {
		p95us = fs.Cached.P95Us
	}
	return p95us / 1000.0
}
