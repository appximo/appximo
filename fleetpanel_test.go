package appitools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/shutdown"
)

// The fleet console is gated by the fleet-operator key with a UNIFORM 404 on
// any failure — no key configured, missing, or wrong — so its existence is not
// fingerprintable without the credential (MT-STRUCT-S5).
func TestFleetConsoleAuthUniform404(t *testing.T) {
	get := func(panel *fleetPanel, path, key string) *httptest.ResponseRecorder {
		reg := NewRegistry(unmatchedApp(shutdown.New(), "test", 0, panel), nil)
		req := httptest.NewRequest("GET", "http://x"+path, nil)
		req.Host = "127.0.0.1:8080" // bare host → process-level handler
		if key != "" {
			req.Header.Set("X-Fleet-Key", key)
		}
		rec := httptest.NewRecorder()
		reg.ServeHTTP(rec, req)
		return rec
	}

	enabled := &fleetPanel{operatorKey: "fleet-op-key-0123456789", version: "test"}
	disabled := &fleetPanel{operatorKey: "", version: "test"}

	// No key / wrong key / disabled console → the SAME 404 body as unknown paths.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"missing key":      get(enabled, "/fleet", ""),
		"wrong key":        get(enabled, "/fleet", "nope"),
		"disabled console": get(disabled, "/fleet", "anything"),
		"api wrong key":    get(enabled, "/fleet/api/apps", "nope"),
	} {
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown app domain") {
			t.Fatalf("%s: want uniform 404, got %d %q", name, rec.Code, rec.Body.String())
		}
	}

	// The right key opens the console page and the inventory API.
	if rec := get(enabled, "/fleet", "fleet-op-key-0123456789"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Appitools — Fleet") {
		t.Fatalf("console with key: got %d", rec.Code)
	}
	if rec := get(enabled, "/fleet/api/apps", "fleet-op-key-0123456789"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"fleet_apps":0`) {
		t.Fatalf("inventory with key: got %d %q", rec.Code, rec.Body.String())
	}

	// ?key= works too (browser bookmark form).
	if rec := get(enabled, "/fleet?key=fleet-op-key-0123456789", ""); rec.Code != 200 {
		t.Fatalf("query-key form: got %d", rec.Code)
	}
}

// The console lives ONLY at the process level: on an app's claimed domain the
// request goes to the app's router (which has no /fleet route), so an app
// surface never exposes the fleet console.
func TestFleetConsoleNotServedOnAppDomains(t *testing.T) {
	panel := &fleetPanel{operatorKey: "fleet-op-key-0123456789", version: "test"}
	appHandler := namedApp("crm")
	reg := NewRegistry(unmatchedApp(shutdown.New(), "test", 1, panel), map[string]*compiledApp{
		"crm.local": appHandler,
	})
	req := httptest.NewRequest("GET", "http://x/fleet", nil)
	req.Host = "crm.local"
	req.Header.Set("X-Fleet-Key", "fleet-op-key-0123456789")
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)
	if rec.Body.String() != "crm" {
		t.Fatalf("/fleet on an app domain must reach the APP (got %q) — the console is process-level only", rec.Body.String())
	}
}

// Registry.Snapshot is the read-only inventory view: it reflects swaps and
// never exposes the live map.
func TestRegistrySnapshot(t *testing.T) {
	reg := NewRegistry(namedApp("def"), map[string]*compiledApp{
		"crm.local": namedApp("crm"), "shop.local": namedApp("shop"),
	})
	snap := reg.Snapshot()
	if len(snap) != 2 || snap["crm.local"] != "crm" || snap["shop.local"] != "shop" {
		t.Fatalf("snapshot mismatch: %v", snap)
	}
	reg.SwapApp([]string{"crm.local"}, namedApp("crm-v2"))
	if snap["crm.local"] != "crm" {
		t.Fatal("snapshot must be a copy, not the live map")
	}
	if got := reg.Snapshot()["crm.local"]; got != "crm-v2" {
		t.Fatalf("fresh snapshot must reflect the swap, got %q", got)
	}
}
