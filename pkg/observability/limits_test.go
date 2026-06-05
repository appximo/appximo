package observability

import (
	"fmt"
	"testing"
)

// TestRings_CapBoundsTenants verifies that the per-tenant ring registry stops
// allocating new rings once the tenant cap is reached, so an attacker rotating
// the Host subdomain cannot grow it without bound.
func TestRings_CapBoundsTenants(t *testing.T) {
	old := maxTrackedTenants
	maxTrackedTenants = 3
	defer func() { maxTrackedTenants = old }()

	rs := NewRings()
	for i := 0; i < 200; i++ {
		rs.Record(fmt.Sprintf("tenant-%d", i), Sample{})
	}
	if got := rs.Count(); got > 3 {
		t.Fatalf("ring registry grew past cap: got %d tenants, cap 3", got)
	}
}

// TestErrorStore_CapBoundsTenants verifies the same bound for the error store.
func TestErrorStore_CapBoundsTenants(t *testing.T) {
	old := maxTrackedTenants
	maxTrackedTenants = 3
	defer func() { maxTrackedTenants = old }()

	es := NewErrorStore()
	for i := 0; i < 200; i++ {
		es.Record(fmt.Sprintf("tenant-%d", i), fmt.Errorf("boom"))
	}
	es.mu.RLock()
	n := len(es.groups)
	es.mu.RUnlock()
	if n > 3 {
		t.Fatalf("error store grew past cap: got %d tenants, cap 3", n)
	}
}

// TestMetrics_TenantCardinalityBounded verifies that Prometheus tenant_id label
// cardinality is capped, collapsing overflow tenants into one shared series.
func TestMetrics_TenantCardinalityBounded(t *testing.T) {
	old := maxTrackedTenants
	maxTrackedTenants = 2
	defer func() { maxTrackedTenants = old }()

	m := NewMetrics()
	for i := 0; i < 50; i++ {
		m.ObserveRequest(fmt.Sprintf("tenant-%d", i), "GET", "/api/{x}", "200", 0.001)
	}
	m.seenMu.Lock()
	n := len(m.seen)
	m.seenMu.Unlock()
	if n > 2 {
		t.Fatalf("metrics tracked %d distinct real tenant labels, cap 2", n)
	}
	// A further unseen tenant must be relabeled onto the shared overflow series.
	if got := m.boundTenant("yet-another-tenant"); got != metricsOverflowTenant {
		t.Fatalf("expected overflow relabel %q, got %q", metricsOverflowTenant, got)
	}
}

// TestHistogram_CapBoundsTenants verifies the latency histogram store is bounded
// and that Record does not panic when a tenant is dropped at the cap.
func TestHistogram_CapBoundsTenants(t *testing.T) {
	old := maxTrackedTenants
	maxTrackedTenants = 3
	defer func() { maxTrackedTenants = old }()

	th := NewTenantHistogram()
	for i := 0; i < 200; i++ {
		th.Record(fmt.Sprintf("tenant-%d", i), 1234, false) // must not panic past cap
	}
	th.mu.RLock()
	n := len(th.uncached)
	th.mu.RUnlock()
	if n > 3 {
		t.Fatalf("histogram store grew past cap: got %d tenants, cap 3", n)
	}
}
