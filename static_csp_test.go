package appximo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	appmiddleware "github.com/appximo/appximo/pkg/middleware"
)

// LIBRARY-GAPS-S2 (ENG-5) — the static handler OWNS its Content-Security-Policy.
//
// The bug these tests pin: the ROOT mount is chi's NotFound handler, which chi
// copies into the API subrouter mounted INSIDE the StrictCSP group — so the SPA
// shell shipped `default-src 'none'` and every browser blocked the app's own
// scripts. curl never saw it (curl does not enforce CSP), which is why every
// assertion here is on the HEADER of the served document, not on the status.
// The router below mirrors production EXACTLY: static registered first, then
// the API mounted at "/" inside a StrictCSP group.

func cspRouter(hs []*staticHandler) *chi.Mux {
	r := chi.NewMux()
	registerStatic(r, hs)
	r.Group(func(sub chi.Router) {
		sub.Use(appmiddleware.StrictCSP)
		api := chi.NewMux()
		api.Get("/api/tasks", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"data":[]}`))
		})
		sub.Mount("/", api)
	})
	return r
}

// expectedStaticCSP is the policy a DEFAULT mount actually resolves to for a given
// shell. Since SEC-2 the default is HARDENED per mount — inline scripts pinned by
// sha256, or `script-src 'self'` when there are none — so these tests compare
// against what the mount resolved, not against the DefaultStaticCSP constant. What
// they are actually pinning is unchanged and is the ENG-5 contract: the STATIC
// mount's policy is what ships, never the API's `default-src 'none'`.
func expectedStaticCSP(index []byte) string {
	policy, _ := hardenedStaticCSP(DefaultStaticCSP, index)
	return policy
}

// spaIndex is the fixture shell's index.html (see spaFS in static_test.go).
var spaIndex = []byte("<!doctype html><div id=app>")

func cspOf(t *testing.T, h http.Handler, path string) (string, int) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Header().Get("Content-Security-Policy"), rec.Code
}

func TestStaticCSP_RootMountOverridesTheAPIPolicy(t *testing.T) {
	h := cspRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS(), SPA: true}))

	for _, p := range []string{"/", "/orders/42" /* SPA fallback */, "/nested/page.html"} {
		csp, code := cspOf(t, h, p)
		if code != 200 {
			t.Fatalf("GET %s = %d, want 200", p, code)
		}
		if csp != expectedStaticCSP(spaIndex) {
			t.Errorf("GET %s CSP = %q, want the static mount's own policy (the API's default-src 'none' must NOT leak onto the document)", p, csp)
		}
	}

	// The API keeps ITS policy — the static override must never bleed back.
	csp, code := cspOf(t, h, "/api/tasks")
	if code != 200 || csp != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("GET /api/tasks = %d CSP %q, want 200 with the strict API policy", code, csp)
	}
	// And an unknown /api path (a real 404, served through the API's NotFound…
	// which IS the static handler on a root mount) — a JSON caller is not a
	// document, but the header must still be the static one only when HTML is
	// actually served; /api stays engine-owned and never reaches the SPA.
	if _, code := cspOf(t, h, "/api/nope"); code != 404 {
		t.Errorf("GET /api/nope = %d, want 404 (never the shell)", code)
	}
}

func TestStaticCSP_SubPathMountGetsTheSamePolicy(t *testing.T) {
	h := cspRouter(mustCompile(t, StaticMount{Path: "/ui", FS: spaFS(), SPA: true}))

	for _, p := range []string{"/ui", "/ui/", "/ui/orders/42"} {
		csp, code := cspOf(t, h, p)
		if code != 200 {
			t.Fatalf("GET %s = %d, want 200", p, code)
		}
		if csp != expectedStaticCSP(spaIndex) {
			t.Errorf("GET %s CSP = %q, want the static mount's own policy (a sub-path mount used to ship NO policy at all)", p, csp)
		}
	}
}

func TestStaticCSP_PerMountOverride(t *testing.T) {
	custom := "default-src 'self'; img-src 'self' https://cdn.example.com"
	h := cspRouter(mustCompile(t,
		StaticMount{Path: "/", FS: spaFS(), SPA: true, CSP: custom},
		StaticMount{Path: "/plain", FS: spaFS(), CSP: CSPOff},
	))

	if csp, _ := cspOf(t, h, "/"); csp != custom {
		t.Errorf("root override: CSP = %q, want %q verbatim", csp, custom)
	}
	// CSPOff must mean NO header — including on responses where the chain
	// already set one (that inherited header is the original bug).
	if csp, code := cspOf(t, h, "/plain"); code != 200 || csp != "" {
		t.Errorf("CSPOff mount: code %d CSP %q, want 200 with the header ABSENT", code, csp)
	}
}

func TestStaticCSP_AssetsAreStampedToo(t *testing.T) {
	h := cspRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS(), SPA: true}))
	csp, code := cspOf(t, h, "/assets/app-abc123.js")
	if code != 200 || csp != expectedStaticCSP(spaIndex) {
		t.Errorf("asset: code %d CSP %q — the mount's policy applies uniformly (never the API's)", code, csp)
	}
}

// 1B-2: an assets-only mount (no SPA) needs no index.html; an SPA mount still
// does (the index IS the fallback document).
func TestStaticMount_AssetsOnlyNeedsNoIndex(t *testing.T) {
	assets := fstest.MapFS{
		"immutable/chunk-abc.js": {Data: []byte("export{}")},
	}

	hs, err := validateStaticMounts([]StaticMount{{Path: "/_app", FS: assets}})
	if err != nil {
		t.Fatalf("assets-only mount must boot without an index: %v", err)
	}
	h := cspRouter(hs)

	if _, code := cspOf(t, h, "/_app/immutable/chunk-abc.js"); code != 200 {
		t.Errorf("asset file = %d, want 200", code)
	}
	// The mount root has nothing to serve — an honest 404, not a panic.
	if _, code := cspOf(t, h, "/_app"); code != 404 {
		t.Errorf("assets-only mount root = %d, want 404", code)
	}

	// SPA:true without an index stays a BOOT error.
	if _, err := validateStaticMounts([]StaticMount{{Path: "/x", FS: assets, SPA: true}}); err == nil {
		t.Error("an SPA mount with no index must fail the boot (the index is the fallback)")
	}
}
