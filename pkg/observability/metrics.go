package observability

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the four Prometheus collectors described in DEPLOYMENT_INFRA.md,
// backed by a dedicated registry rather than the global default. The dedicated
// registry keeps construction side-effect-free (safe to build more than once,
// e.g. in tests) and scopes /metrics output to exactly these series plus the
// standard Go/process collectors.
type Metrics struct {
	reg *prometheus.Registry

	requestsTotal     *prometheus.CounterVec
	requestDuration   *prometheus.HistogramVec
	activeTenants     prometheus.Gauge
	migrationDuration *prometheus.HistogramVec

	// Phase-0 safety counters (LIBRARY-HARDEN-S1). requestPanics counts panics
	// recovered by the request-chain Recoverer (a handler paniced → clean 500,
	// process alive); goroutinePanics counts panics recovered inside Ctx.SafeGo
	// (the ONLY sanctioned way to launch a goroutine from a handler — a raw
	// `go func(){panic()}()` would take the whole multi-tenant process down).
	// A nonzero rate is an alertable defect in extension code, never expected.
	requestPanics   prometheus.Counter
	goroutinePanics prometheus.Counter

	// seenMu/seen bound tenant_id label cardinality: the id is client-controlled
	// (Host subdomain), so without a cap an attacker could mint unbounded series.
	seenMu sync.Mutex
	seen   map[string]struct{}
}

// NewMetrics constructs and registers all collectors.
func NewMetrics() *Metrics {
	m := &Metrics{
		reg:  prometheus.NewRegistry(),
		seen: make(map[string]struct{}),
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "appximo_requests_total",
			Help: "Total requests by tenant and endpoint",
		}, []string{"tenant_id", "method", "path", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "appximo_request_duration_seconds",
			Help:    "Request duration",
			Buckets: prometheus.DefBuckets,
		}, []string{"tenant_id", "method", "path"}),
		activeTenants: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "appximo_active_tenants",
			Help: "Number of tenants loaded in the cache",
		}),
		migrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "appximo_migration_duration_seconds",
			Help:    "Tenant migration duration",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120},
		}, []string{"tenant_id", "status"}),
		requestPanics: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "appximo_request_panics_total",
			Help: "Panics recovered by the request-chain Recoverer (handler panic → clean 500, process survives)",
		}),
		goroutinePanics: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "appximo_goroutine_panics_total",
			Help: "Panics recovered inside Ctx.SafeGo (a raw goroutine panic would crash the whole process)",
		}),
	}
	m.reg.MustRegister(
		m.requestsTotal,
		m.requestDuration,
		m.activeTenants,
		m.migrationDuration,
		m.requestPanics,
		m.goroutinePanics,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// IncRequestPanic records one panic recovered by the request-chain Recoverer.
func (m *Metrics) IncRequestPanic() { m.requestPanics.Inc() }

// IncGoroutinePanic records one panic recovered inside a Ctx.SafeGo goroutine.
func (m *Metrics) IncGoroutinePanic() { m.goroutinePanics.Inc() }

// Handler returns the Prometheus exposition handler scoped to this registry.
// It carries no auth of its own — mount it behind the admin-key middleware.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Gatherer exposes the dedicated registry as a prometheus.Gatherer. It enables
// end-to-end metric assertions (prometheus/testutil.GatherAndCompare) against
// exactly this Metrics' series, without reaching for the global default registry.
// Like Handler(), it carries no auth of its own.
func (m *Metrics) Gatherer() prometheus.Gatherer {
	return m.reg
}

// ObserveRequest records one served request: increments the counter and observes
// the duration histogram. path should be the chi route pattern (e.g. "/api/{entity}")
// rather than the raw URL path, to keep label cardinality bounded.
func (m *Metrics) ObserveRequest(tenantID, method, path, status string, durationSeconds float64) {
	tenantID = m.boundTenant(tenantID)
	m.requestsTotal.WithLabelValues(tenantID, method, path, status).Inc()
	m.requestDuration.WithLabelValues(tenantID, method, path).Observe(durationSeconds)
}

// boundTenant caps tenant_id label cardinality. Known tenants keep their own
// series; once maxTrackedTenants distinct ids have been seen, further unseen
// tenants collapse into metricsOverflowTenant so series count stays bounded.
func (m *Metrics) boundTenant(tenantID string) string {
	m.seenMu.Lock()
	defer m.seenMu.Unlock()
	if _, ok := m.seen[tenantID]; ok {
		return tenantID
	}
	if len(m.seen) >= maxTrackedTenants {
		return metricsOverflowTenant
	}
	m.seen[tenantID] = struct{}{}
	return tenantID
}

// SetActiveTenants updates the gauge of tenants currently loaded in cache.
func (m *Metrics) SetActiveTenants(n int) {
	m.activeTenants.Set(float64(n))
}

// ObserveMigration records the duration and outcome of a tenant migration.
func (m *Metrics) ObserveMigration(tenantID, status string, durationSeconds float64) {
	m.migrationDuration.WithLabelValues(tenantID, status).Observe(durationSeconds)
}
