package adminui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func router(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	if err := Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServeIndex(t *testing.T) {
	h := router(t)
	for _, p := range []string{"/admin", "/admin/"} {
		rec := get(t, h, p)
		if !HasBuiltAssets() {
			// ADR-025: with no embedded assets the shell must be an honest 503,
			// never a blank 200 (field report B1).
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s without assets: status %d, want 503", p, rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: content-type %q", p, ct)
		}
		// index.html must NOT be cached (it names the current hashed assets).
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("%s: index Cache-Control must be no-cache, got %q", p, cc)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Fatalf("%s: missing/short CSP: %q", p, csp)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("%s: body is not the SPA shell: %q", p, rec.Body.String())
		}
	}
}

// TestServeAsset asserts hashed assets are served with an immutable cache. It is
// skipped when no built assets are embedded (a bare `go build` without `make
// admin-ui`), so CI without a Node step still passes.
func TestServeAsset(t *testing.T) {
	h, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if !h.hasAssets {
		t.Skip("no built assets embedded (run `make admin-ui` before `go build`)")
	}
	entries, err := fs.ReadDir(h.sub, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("read assets: %v", err)
	}
	asset := entries[0].Name()
	rec := get(t, router(t), "/admin/assets/"+asset)
	if rec.Code != http.StatusOK {
		t.Fatalf("asset %s: status %d", asset, rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("asset must be immutable-cached, got %q", cc)
	}
	if strings.HasSuffix(asset, ".js") {
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf(".js content-type wrong: %q", ct)
		}
	}
}

func TestUnknownAssetAndTraversal(t *testing.T) {
	h := router(t)
	if rec := get(t, h, "/admin/assets/does-not-exist.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown asset: status %d, want 404", rec.Code)
	}
	// A traversal attempt must not escape the assets prefix.
	if rec := get(t, h, "/admin/assets/../../../etc/passwd"); rec.Code == http.StatusOK {
		t.Fatalf("path traversal served a file (status %d)", rec.Code)
	}
}

// TestServeIndexNoAssets pins the ADR-025 contract regardless of the embed's
// state: a handler with no built assets answers the shell with a 503 page that
// names the missing piece and the fix — never the blank 200 shell of B1.
func TestServeIndexNoAssets(t *testing.T) {
	h := &handler{indexHTML: []byte("<html>shell</html>"), hasAssets: false}
	rec := httptest.NewRecorder()
	h.serveIndex(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no-assets shell: status %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"make admin-ui", "ADR-025", "503"} {
		if !strings.Contains(body, want) {
			t.Fatalf("no-assets page must mention %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "<html>shell</html>") {
		t.Fatalf("no-assets page must not serve the broken shell")
	}
}

func TestHasBuiltAssets(t *testing.T) {
	// Must not panic and must agree with a fresh handler.
	h, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if HasBuiltAssets() != h.hasAssets {
		t.Fatalf("HasBuiltAssets disagrees with handler state")
	}
}
