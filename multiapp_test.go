package appitools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/shutdown"
)

// In the multi-app runtime an unmatched Host must NEVER fall into an
// arbitrary app (the S2 single-app default changes semantics here): it gets
// the process-level handler — health probes only, everything else a clean 404.
func TestUnmatchedHostGets404NotAnApp(t *testing.T) {
	crm := namedApp("crm")
	reg := NewRegistry(unmatchedApp(shutdown.New(), "test", 2), map[string]*compiledApp{
		"crm.local": crm,
	})

	get := func(host, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		req.Host = host
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, req)
		return rec
	}

	// Claimed domain (and tenant subdomain) → the app.
	if rec := get("acme.crm.local", "/api/tasks"); rec.Body.String() != "crm" {
		t.Fatalf("claimed host must reach its app, got %q", rec.Body.String())
	}
	// Unclaimed Hosts → 404 with the fleet-proxy error, NEVER an app.
	for _, host := range []string{"other.example.com", "localhost", "127.0.0.1:8080", "crm.local.evil.com"} {
		rec := get(host, "/api/tasks")
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown app domain") {
			t.Fatalf("unmatched host %q must 404 cleanly, got %d %q", host, rec.Code, rec.Body.String())
		}
	}
	// Process health probes keep working on a bare Host (LB/supervisor contract).
	if rec := get("127.0.0.1:8080", "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("/healthz on bare host must be 200, got %d", rec.Code)
	}
	if rec := get("localhost", "/health"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "fleet_apps") {
		t.Fatalf("/health on bare host must report fleet status, got %d %q", rec.Code, rec.Body.String())
	}
}

// crm.local.evil.com must NOT match crm.local (suffix walk matches label
// SUFFIXES, not substrings) — pinned above; this makes the intent explicit.
func TestSuffixWalkNeverMatchesEmbeddedDomains(t *testing.T) {
	crm := namedApp("crm")
	reg := NewRegistry(unmatchedApp(shutdown.New(), "test", 1), map[string]*compiledApp{"crm.local": crm})
	if got := reg.Resolve("crm.local.evil.com"); got.name != "__unmatched__" {
		t.Fatalf("embedded-domain host resolved to %q — must be unmatched", got.name)
	}
}
