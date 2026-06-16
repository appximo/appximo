package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// nextSpy is a sentinel downstream handler that records whether it ran and writes
// a 200 — so a test can assert a preflight short-circuited (next NOT called).
func nextSpy(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

func do(t *testing.T, cfg CORSConfig, method, path, origin string, preflightMethod string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	h := CORS(cfg)(nextSpy(&called))
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if preflightMethod != "" {
		req.Header.Set("Access-Control-Request-Method", preflightMethod)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, called
}

func TestCORS_Disabled_NoOrigins(t *testing.T) {
	if (CORSConfig{}).Enabled() {
		t.Fatal("empty CORSConfig must report Enabled()=false")
	}
	if (CORSConfig{AllowedOrigins: []string{"https://a.com"}}).Enabled() != true {
		t.Fatal("a configured origin must report Enabled()=true")
	}
}

func TestCORS_NoOriginHeader_Passthrough(t *testing.T) {
	// A server-to-server request (no Origin) must be untouched: no CORS headers,
	// downstream runs. This is the bench/hot path — it must be a no-op.
	rec, called := do(t, CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}, http.MethodGet, "/api/tasks", "", "")
	if !called {
		t.Fatal("downstream handler must run for a non-CORS request")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("no Origin header → no ACAO, got %q", got)
	}
}

func TestCORS_AllowedExactOrigin(t *testing.T) {
	rec, called := do(t, CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}, http.MethodGet, "/api/tasks", "https://app.example.com", "")
	if !called {
		t.Fatal("allowed simple request must reach downstream")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("ACAO = %q, want the exact origin", got)
	}
	if got := rec.Header().Get("Vary"); got == "" {
		t.Fatal("Vary must include Origin for a non-* response")
	}
}

func TestCORS_DisallowedOrigin_SimpleRequest(t *testing.T) {
	// A disallowed origin's simple request still reaches the handler (the server
	// processes it), but NO Allow-Origin is emitted so the browser withholds it.
	rec, called := do(t, CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}, http.MethodGet, "/api/tasks", "https://evil.example.com", "")
	if !called {
		t.Fatal("simple request should pass through even for a disallowed origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed origin must get no ACAO, got %q", got)
	}
}

func TestCORS_Preflight_Allowed(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}, MaxAge: 300}
	rec, called := do(t, cfg, http.MethodOptions, "/api/tasks", "https://app.example.com", "POST")
	if called {
		t.Fatal("preflight must be short-circuited — downstream must NOT run")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", rec.Code)
	}
	h := rec.Header()
	if h.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("preflight ACAO = %q", h.Get("Access-Control-Allow-Origin"))
	}
	if h.Get("Access-Control-Allow-Methods") == "" {
		t.Fatal("preflight must echo Allow-Methods")
	}
	if h.Get("Access-Control-Allow-Headers") == "" {
		t.Fatal("preflight must echo Allow-Headers")
	}
	if h.Get("Access-Control-Max-Age") != "300" {
		t.Fatalf("preflight Max-Age = %q, want 300", h.Get("Access-Control-Max-Age"))
	}
}

func TestCORS_Preflight_Disallowed_NoHeaders(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	rec, called := do(t, cfg, http.MethodOptions, "/api/tasks", "https://evil.example.com", "POST")
	if called {
		t.Fatal("a disallowed preflight must still short-circuit, not reach the handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("disallowed preflight must emit no ACAO, got %q", got)
	}
}

func TestCORS_Wildcard_NoCredentials(t *testing.T) {
	rec, _ := do(t, CORSConfig{AllowedOrigins: []string{"*"}}, http.MethodGet, "/api/tasks", "https://anything.example.com", "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard without credentials → ACAO '*', got %q", got)
	}
}

func TestCORS_Wildcard_WithCredentials_ReflectsOrigin(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}
	rec, _ := do(t, cfg, http.MethodGet, "/api/tasks", "https://anything.example.com", "")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Fatalf("wildcard+credentials must REFLECT origin (Fetch spec forbids '*'), got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Allow-Credentials = %q, want true", got)
	}
}

func TestCORS_OutOfScopePath_NoHeaders(t *testing.T) {
	// /admin, /metrics, the control plane etc. are not browser-consumer routes —
	// CORS must not touch them even with an allowed Origin.
	// Includes prefix-confusion guards: a bare /openapi or /openapi-* must NOT be
	// scoped (only /openapi.json / /openapi.yaml are), same separator discipline as
	// /api/ vs /apiX.
	for _, path := range []string{"/admin", "/admin/tenants", "/metrics", "/debug/traces", "/healthz", "/openapi", "/openapiEvil", "/openapi-internal", "/authsomething"} {
		rec, called := do(t, CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}, http.MethodGet, path, "https://app.example.com", "")
		if !called {
			t.Fatalf("%s: out-of-scope request must pass through", path)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s: out-of-scope path must get no ACAO, got %q", path, got)
		}
	}
}

func TestCORS_ScopedPaths(t *testing.T) {
	cfg := CORSConfig{AllowedOrigins: []string{"https://app.example.com"}}
	for _, path := range []string{"/api/tasks", "/auth/login", "/graphql", "/openapi.json"} {
		rec, _ := do(t, cfg, http.MethodGet, path, "https://app.example.com", "")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
			t.Fatalf("%s: expected CORS headers on a scoped path, ACAO=%q", path, got)
		}
	}
}

func TestCORS_CustomMethodsAndHeaders(t *testing.T) {
	cfg := CORSConfig{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{"GET", "POST"},
		AllowedHeaders: []string{"Authorization", "X-Custom"},
	}
	rec, _ := do(t, cfg, http.MethodOptions, "/api/tasks", "https://app.example.com", "POST")
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("Allow-Methods = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, X-Custom" {
		t.Fatalf("Allow-Headers = %q", got)
	}
}
