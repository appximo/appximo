package appximo

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// SwapApp replaces one app's domains atomically, leaving every OTHER app's
// entry byte-identical — the per-app hot-swap (MT-STRUCT-S4).
func TestRegistrySwapApp(t *testing.T) {
	crmV1, shop, def := namedApp("crm-v1"), namedApp("shop"), namedApp("def")
	reg := NewRegistry(def, map[string]*compiledApp{
		"crm.local":  crmV1,
		"shop.local": shop,
	})
	// The other app's pointer is captured to prove it is untouched by the swap.
	shopBefore := reg.Resolve("shop.local")

	crmV2 := namedApp("crm-v2")
	reg.SwapApp([]string{"crm.local"}, crmV2)

	if got := reg.Resolve("acme.crm.local"); got != crmV2 {
		t.Fatalf("after swap crm resolves to %q, want crm-v2", got.name)
	}
	if got := reg.Resolve("shop.local"); got != shopBefore {
		t.Fatalf("swap of crm disturbed shop (now %q) — other apps must be untouched", got.name)
	}
	if got := reg.Resolve("nobody.example.org"); got != def {
		t.Fatalf("default disturbed: %q", got.name)
	}
}

// AddApp registers a new domain hot; RemoveApp unregisters it (falls back to default).
func TestRegistryAddRemoveApp(t *testing.T) {
	def := namedApp("def")
	reg := NewRegistry(def, nil)

	if got := reg.Resolve("new.local"); got != def {
		t.Fatalf("pre-add must be default, got %q", got.name)
	}
	added := namedApp("added")
	reg.AddApp([]string{"new.local"}, added)
	if got := reg.Resolve("t.new.local"); got != added {
		t.Fatalf("after add must resolve to added, got %q", got.name)
	}
	reg.RemoveApp([]string{"new.local"})
	if got := reg.Resolve("t.new.local"); got != def {
		t.Fatalf("after remove must fall back to default, got %q", got.name)
	}
}

// The published map is never mutated in place: a reader holding the old map
// keeps seeing the old app while a concurrent swap installs a new one.
// Run under -race: any in-place mutation or torn read is a failure.
func TestRegistrySwapIsRaceFreeUnderLoad(t *testing.T) {
	def := namedApp("def")
	reg := NewRegistry(def, map[string]*compiledApp{"crm.local": namedApp("crm-0")})

	var stop atomic.Bool
	var readers sync.WaitGroup

	// Many readers hammer Resolve+ServeHTTP while swaps happen underneath. Each
	// reader records which version answered; every answer must be a WHOLE app
	// (never a torn/nil pointer) whose body matches its own name.
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				req := httptest.NewRequest("GET", "http://x/api", nil)
				req.Host = "acme.crm.local"
				rec := httptest.NewRecorder()
				reg.ServeHTTP(rec, req)
				body := rec.Body.String()
				if body == "" || body == "def" {
					t.Errorf("torn/default read during swap: %q", body)
					return
				}
			}
		}()
	}
	for v := 1; v <= 200; v++ {
		reg.SwapApp([]string{"crm.local"}, namedApp(fmt.Sprintf("crm-%d", v)))
	}
	stop.Store(true)
	readers.Wait()

	// The final version wins.
	if got := reg.Resolve("acme.crm.local"); got.name != "crm-200" {
		t.Fatalf("final swap not visible: %q", got.name)
	}
}

// An in-flight request that resolved the OLD app finishes on the OLD app even
// though a swap installs a new one mid-request — no straddle, no torn surface.
func TestRegistryInFlightRequestKeepsResolvedApp(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	oldApp := &compiledApp{name: "old", handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release                     // hold the request in-flight across the swap
		w.Write([]byte("old-served")) //nolint:errcheck
	})}
	reg := NewRegistry(namedApp("def"), map[string]*compiledApp{"crm.local": oldApp})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("GET", "http://x/api", nil)
		req.Host = "acme.crm.local"
		reg.ServeHTTP(rec, req)
		close(done)
	}()

	<-entered // request is inside the OLD handler
	reg.SwapApp([]string{"crm.local"}, namedApp("new"))
	if got := reg.Resolve("acme.crm.local"); got.name != "new" {
		t.Fatalf("new requests should see the new app, got %q", got.name)
	}
	close(release) // let the in-flight request finish
	<-done
	if rec.Body.String() != "old-served" {
		t.Fatalf("in-flight request should complete on the OLD app, got %q", rec.Body.String())
	}
}
