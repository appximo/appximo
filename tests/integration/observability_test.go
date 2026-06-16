//go:build integration

// Package integration_test holds DB-backed integration tests (build tag
// `integration`). observability_test.go asserts that the real middleware chain
// (TenantMiddleware → RequestLogger tap → JWT → RBAC → generated routes) populates
// the actual Prometheus collectors, using the official prometheus/testutil
// comparator and the REAL metric names grepped from pkg/observability/metrics.go:
//
//	appitools_requests_total            (counter)
//	appitools_request_duration_seconds  (histogram)
//	appitools_active_tenants            (gauge)
//
// One postgres container is shared across the suite via TestMain; each test
// registers its own tenant and gets a fresh *Metrics so counts are isolated.
package integration_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/miguelangel/appitools/tests/helpers"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	ctx := context.Background()
	pool, cleanup, err := helpers.StartPostgres(ctx)
	if err != nil {
		log.Fatalf("integration: %v", err)
	}
	if err := helpers.ApplyControlPlane(ctx, pool); err != nil {
		cleanup()
		log.Fatalf("integration: %v", err)
	}
	testPool = pool
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// fireGET issues n GET /api/guides requests for tenantID, asserting each is 200.
func fireGET(t *testing.T, srv *httptest.Server, tenantID, token string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		req := helpers.TenantRequest(t, srv, http.MethodGet, "/api/guides", tenantID, token)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("GET /api/guides #%d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/guides #%d: want 200, got %d", i, resp.StatusCode)
		}
	}
}

// TestObservability_RequestMetrics fires N successful GETs as one tenant and asserts
// both the request counter (exact value, via testutil) and the duration histogram's
// observation count reflect exactly N.
func TestObservability_RequestMetrics(t *testing.T) {
	s := helpers.FixtureSchema(t, "logistics_schema.json")
	const tenantID = "obsmetrics"
	helpers.RegisterTenant(t, testPool, tenantID, s)
	srv, metrics := helpers.BuildObservableServer(t, s, testPool)
	token := helpers.GenToken(t, "super_admin", "00000000-0000-0000-0000-000000000001", tenantID)

	const n = 5
	fireGET(t, srv, tenantID, token, n)

	// appitools_requests_total: exact series + value via the official comparator.
	expected := `
# HELP appitools_requests_total Total de requests por tenant y endpoint
# TYPE appitools_requests_total counter
appitools_requests_total{method="GET",path="/api/guides",status="200",tenant_id="obsmetrics"} 5
`
	if err := testutil.GatherAndCompare(metrics.Gatherer(),
		strings.NewReader(expected), "appitools_requests_total"); err != nil {
		t.Fatalf("appitools_requests_total mismatch:\n%v", err)
	}

	// appitools_request_duration_seconds: histogram observation count == n.
	if got := histogramSampleCount(t, metrics.Gatherer(), "appitools_request_duration_seconds",
		map[string]string{"tenant_id": tenantID, "method": "GET", "path": "/api/guides"}); got != n {
		t.Fatalf("appitools_request_duration_seconds count: got %d want %d", got, n)
	}
}

// TestObservability_DeniedByIDPathIsTemplated proves the metric-cardinality fix
// (FASE3-SEC Hallazgo 3): three DELETEs by DISTINCT random UUIDs, each REJECTED at
// RBAC before the router resolves a pattern, must collapse into ONE bounded series
// with path "/api/guides/{id}" — not three series carrying the concrete UUIDs. The
// official comparator asserts the WHOLE requests_total family is exactly that single
// series; a raw-UUID label would make it fail.
func TestObservability_DeniedByIDPathIsTemplated(t *testing.T) {
	s := helpers.FixtureSchema(t, "logistics_schema.json")
	const tenantID = "obscard"
	helpers.RegisterTenant(t, testPool, tenantID, s)
	srv, metrics := helpers.BuildObservableServer(t, s, testPool)
	// An unknown role → RBAC denies (deny-by-default) at the middleware, before the
	// router runs, so RoutePattern is empty — the pre-router denial path under test.
	token := helpers.GenToken(t, "nobody", "00000000-0000-0000-0000-000000000009", tenantID)

	uuids := []string{
		"3fa85f64-5717-4562-b3fc-2c963f66afa6",
		"7c9e6679-7425-40de-944b-e07fc1f90ae7",
		"01234567-89ab-cdef-0123-456789abcdef",
	}
	for _, u := range uuids {
		req := helpers.TenantRequest(t, srv, http.MethodDelete, "/api/guides/"+u, tenantID, token)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("DELETE /api/guides/%s: %v", u, err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("DELETE by-id with unknown role: want 403, got %d", resp.StatusCode)
		}
	}

	// The whole requests_total family must be exactly ONE templated 403 series with
	// value 3 — proving the three random UUIDs did not create three series.
	expected := `
# HELP appitools_requests_total Total de requests por tenant y endpoint
# TYPE appitools_requests_total counter
appitools_requests_total{method="DELETE",path="/api/guides/{id}",status="403",tenant_id="obscard"} 3
`
	if err := testutil.GatherAndCompare(metrics.Gatherer(),
		strings.NewReader(expected), "appitools_requests_total"); err != nil {
		t.Fatalf("denied by-id path must be templated to {id} (bounded cardinality):\n%v", err)
	}
}

// TestObservability_ActiveTenantsGauge drives traffic for two distinct tenants and
// asserts the active-tenants gauge equals 2.
func TestObservability_ActiveTenantsGauge(t *testing.T) {
	s := helpers.FixtureSchema(t, "logistics_schema.json")
	const ta, tb = "obsalpha", "obsbeta"
	helpers.RegisterTenant(t, testPool, ta, s)
	helpers.RegisterTenant(t, testPool, tb, s)
	srv, metrics := helpers.BuildObservableServer(t, s, testPool)

	fireGET(t, srv, ta, helpers.GenToken(t, "super_admin", "00000000-0000-0000-0000-000000000001", ta), 2)
	fireGET(t, srv, tb, helpers.GenToken(t, "super_admin", "00000000-0000-0000-0000-000000000002", tb), 3)

	expected := `
# HELP appitools_active_tenants Número de tenants cargados en cache
# TYPE appitools_active_tenants gauge
appitools_active_tenants 2
`
	if err := testutil.GatherAndCompare(metrics.Gatherer(),
		strings.NewReader(expected), "appitools_active_tenants"); err != nil {
		t.Fatalf("appitools_active_tenants mismatch:\n%v", err)
	}
}

// TestObservability_MetricsEndpointExposesNames scrapes the real exposition handler
// (Metrics.Handler, the /metrics body served in prod) and confirms the exercised
// metric families are present under their real names.
func TestObservability_MetricsEndpointExposesNames(t *testing.T) {
	s := helpers.FixtureSchema(t, "logistics_schema.json")
	const tenantID = "obsexpo"
	helpers.RegisterTenant(t, testPool, tenantID, s)
	srv, metrics := helpers.BuildObservableServer(t, s, testPool)
	fireGET(t, srv, tenantID, helpers.GenToken(t, "super_admin", "00000000-0000-0000-0000-000000000001", tenantID), 1)

	ms := httptest.NewServer(metrics.Handler())
	t.Cleanup(ms.Close)
	resp, err := http.Get(ms.URL)
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	for _, name := range []string{
		"appitools_requests_total",
		"appitools_request_duration_seconds",
		"appitools_active_tenants",
	} {
		if !strings.Contains(text, name) {
			t.Errorf("/metrics exposition missing %q", name)
		}
	}
}

// histogramSampleCount gathers from g and returns the summed observation count of
// every series of the named histogram family whose labels are a superset of `labels`.
func histogramSampleCount(t *testing.T, g prometheus.Gatherer, name string, labels map[string]string) uint64 {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var total uint64
	found := false
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			match := true
			for k, v := range labels {
				has := false
				for _, lp := range m.GetLabel() {
					if lp.GetName() == k && lp.GetValue() == v {
						has = true
						break
					}
				}
				if !has {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			total += m.GetHistogram().GetSampleCount()
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s series matched labels %v", name, labels)
	}
	return total
}
