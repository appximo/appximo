package backofficeui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func testRouter(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	if err := Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The bare /app redirects to /app/ so the SPA's relative module references
// ("./app.js" → /app/app.js) resolve regardless of how the URL was typed.
func TestAppRedirectsToSlash(t *testing.T) {
	rec := get(t, testRouter(t), "/app")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /app = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/app/" {
		t.Fatalf("Location = %q, want /app/", loc)
	}
}

func TestShellServesWithCSP(t *testing.T) {
	rec := get(t, testRouter(t), "/app/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/ = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("missing same-origin CSP, got %q", csp)
	}
	if body := rec.Body.String(); !strings.Contains(body, "./app.js") {
		t.Fatalf("shell does not reference ./app.js:\n%s", body)
	}
}

// Every file the shell references must be embedded and typed — a missing one
// here is the blank-page class the browser sees and curl does not (B1/ENG-5).
func TestModuleFilesServeWithTypes(t *testing.T) {
	r := testRouter(t)
	cases := []struct{ path, ct, marker string }{
		{"/app/app.js", "text/javascript", "loadContract"},
		{"/app/contract.js", "text/javascript", "x-appximo-references"},
		{"/app/style.css", "text/css", ".shell"},
	}
	for _, c := range cases {
		rec := get(t, r, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.ct) {
			t.Fatalf("%s Content-Type = %q, want %s", c.path, ct, c.ct)
		}
		if !strings.Contains(rec.Body.String(), c.marker) {
			t.Fatalf("%s does not contain %q", c.path, c.marker)
		}
	}
}

// The generic form's five rules and the contract-extension readers are the
// load-bearing behavior of the SPA — pin their presence so an edit that drops
// one fails here, not in a browser three sessions later.
func TestGenericBehaviorsPinned(t *testing.T) {
	r := testRouter(t)
	app := get(t, r, "/app/app.js").Body.String()
	for _, marker := range []string{
		"OMIT empty on create", // rule 1
		"PATCH",                // rule 2
		"explicit null clears", // rule 3
		"paint them ALL",       // rule 4
		"only legal moves",     // rule 5
		"x-appximo",            // extension-driven (via contract.js import)
	} {
		if !strings.Contains(app, marker) && marker != "x-appximo" {
			t.Errorf("app.js lost the %q behavior marker", marker)
		}
	}
	contract := get(t, r, "/app/contract.js").Body.String()
	for _, ext := range []string{
		"x-appximo-relation", "x-appximo-references", "x-appximo-file",
		"x-appximo-transitions", "x-appximo-initial", "x-appximo-virtual-resources",
	} {
		if !strings.Contains(contract, ext) {
			t.Errorf("contract.js lost the %s reader", ext)
		}
	}
}

func TestUnknownFileIs404(t *testing.T) {
	rec := get(t, testRouter(t), "/app/nope.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /app/nope.js = %d, want 404", rec.Code)
	}
}
