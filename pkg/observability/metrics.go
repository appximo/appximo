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
			Name: "appitools_requests_total",
			Help: "Total de requests por tenant y endpoint",
		}, []string{"tenant_id", "method", "path", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "appitools_request_duration_seconds",
			Help:    "Duración de requests",
			Buckets: prometheus.DefBuckets,
		}, []string{"tenant_id", "method", "path"}),
		activeTenants: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "appitools_active_tenants",
			Help: "Número de tenants cargados en cache",
		}),
		migrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "appitools_migration_duration_seconds",
			Help:    "Duración de migraciones Atlas por tenant",
			Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120},
		}, []string{"tenant_id", "status"}),
	}
	m.reg.MustRegister(
		m.requestsTotal,
		m.requestDuration,
		m.activeTenants,
		m.migrationDuration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler returns the Prometheus exposition handler scoped to this registry.
// It carries no auth of its own — mount it behind the admin-key middleware.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
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
