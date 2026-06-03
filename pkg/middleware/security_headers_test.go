package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
)

var noop = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestSecurityHeaders_BaseHeaders(t *testing.T) {
	handler := appmiddleware.SecurityHeaders(noop)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	cases := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
		"X-XSS-Protection":       "0",
	}
	for header, want := range cases {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s: got %q, want %q", header, got, want)
		}
	}
}

func TestSecurityHeaders_HSTS(t *testing.T) {
	handler := appmiddleware.SecurityHeaders(noop)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	hsts := w.Header().Get("Strict-Transport-Security")
	if hsts == "" {
		t.Fatal("Strict-Transport-Security header must be present")
	}
	// Must contain max-age=31536000 and includeSubDomains but NOT preload.
	for _, want := range []string{"max-age=31536000", "includeSubDomains"} {
		found := false
		for _, part := range splitDirectives(hsts) {
			if part == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("HSTS must contain %q; got %q", want, hsts)
		}
	}
	for _, banned := range []string{"preload"} {
		for _, part := range splitDirectives(hsts) {
			if part == banned {
				t.Errorf("HSTS must NOT contain %q yet; got %q", banned, hsts)
			}
		}
	}
}

func TestStrictCSP_APIRoutes(t *testing.T) {
	handler := appmiddleware.StrictCSP(noop)
	req := httptest.NewRequest(http.MethodGet, "/api/items", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy must be set by StrictCSP")
	}
	if csp != "default-src 'none'; frame-ancestors 'none'" {
		t.Errorf("strict CSP: got %q", csp)
	}
}

func TestPermissiveCSP_GraphiQL(t *testing.T) {
	handler := appmiddleware.PermissiveCSP(noop)
	req := httptest.NewRequest(http.MethodGet, "/graphiql", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy must be set by PermissiveCSP")
	}
	// Must allow scripts from self + https but not be as strict as 'none'.
	for _, required := range []string{"script-src", "style-src", "img-src"} {
		found := false
		for _, part := range splitDirectives(csp) {
			if len(part) >= len(required) && part[:len(required)] == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("permissive CSP must contain %q directive; got %q", required, csp)
		}
	}
}

// splitDirectives splits a CSP header value on "; " for comparison.
func splitDirectives(csp string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(csp); i++ {
		if csp[i] == ';' {
			parts = append(parts, trim(csp[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, trim(csp[start:]))
	return parts
}

func trim(s string) string {
	for len(s) > 0 && s[0] == ' ' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == ' ' {
		s = s[:len(s)-1]
	}
	return s
}
