package editorui

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
	for _, p := range []string{"/editor", "/editor/"} {
		rec := get(t, h, p)
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
		if !strings.Contains(rec.Body.String(), `id="app"`) {
			t.Fatalf("%s: body is not the SPA shell: %q", p, rec.Body.String())
		}
	}
}

// TestServeAsset asserts hashed assets are served immutable. Skipped when no built
// assets are embedded (a bare `go build` without `make editor-ui`), so CI without a
// Node step still passes.
func TestServeAsset(t *testing.T) {
	h, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if !h.hasAssets {
		t.Skip("no built assets embedded (run `make editor-ui` before `go build`)")
	}
	entries, err := fs.ReadDir(h.sub, "assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("read assets: %v", err)
	}
	asset := entries[0].Name()
	rec := get(t, router(t), "/editor/assets/"+asset)
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
	// A missing real asset (has an extension) is a genuine 404.
	if rec := get(t, h, "/editor/assets/does-not-exist.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown asset: status %d, want 404", rec.Code)
	}
	// A traversal attempt must not escape the build prefix.
	if rec := get(t, h, "/editor/assets/../../../etc/passwd"); rec.Code == http.StatusOK {
		t.Fatalf("path traversal served a file (status %d)", rec.Code)
	}
	// An extension-less path is a client route → serves the SPA shell, not 404.
	if rec := get(t, h, "/editor/some/client/route"); rec.Code != http.StatusOK {
		t.Fatalf("client route: status %d, want 200 (SPA fallback)", rec.Code)
	}
}

func TestHasBuiltAssets(t *testing.T) {
	h, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	if HasBuiltAssets() != h.hasAssets {
		t.Fatalf("HasBuiltAssets disagrees with handler state")
	}
}
