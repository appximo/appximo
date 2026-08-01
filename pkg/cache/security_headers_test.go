package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SEC-1 — a cached response must carry the same security posture as a fresh one.
//
// Measured in CERTIFY-S1: `GET /api/notes` answered with no
// Content-Security-Policy, while the same URL with `Cache-Control: no-cache`
// (which bypasses the cache entirely) carried it. The engine sets security
// headers in two places — SecurityHeaders runs OUTSIDE the cache, so its headers
// are re-applied per request and survived; StrictCSP runs INSIDE the cached
// group, so its header was captured by the buffer and then dropped, because the
// cache only stored Content-Type and Etag.
//
// The consequence is not an exploit (the bodies are JSON with nosniff, XFO and
// HSTS still present) but a posture that DEPENDS ON CACHE STATE, which is
// exactly the kind of thing nobody notices until it matters. The guarantee below
// is the general one: whatever security headers the chain sets, a HIT and a MISS
// answer identically.

// securedHandler mimics the engine's chain: a handler wrapped by a middleware
// that sets a CSP inside the cached group.
func securedHandler(body string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		inner.ServeHTTP(w, r)
	})
}

// TestSecurityHeadersSurviveTheCache is the SEC-1 regression: MISS, HIT and a
// cache-bypassing request must all answer with the same security headers.
func TestSecurityHeadersSurviveTheCache(t *testing.T) {
	rc := New(5 * time.Second)
	h := rc.Middleware(securedHandler(`{"data":[]}`))

	get := func(extra map[string]string) http.Header {
		t.Helper()
		req := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/notes", nil), "acme"))
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		return rec.Header()
	}

	miss := get(nil)
	hit := get(nil)
	bypass := get(map[string]string{"Cache-Control": "no-cache"})

	if hit.Get("X-Cache") != "HIT" {
		t.Fatalf("the second request was not a cache HIT (X-Cache=%q) — the test is not exercising the bug",
			hit.Get("X-Cache"))
	}

	for _, hdr := range []string{"Content-Security-Policy", "X-Frame-Options", "Referrer-Policy"} {
		m, hi, by := miss.Get(hdr), hit.Get(hdr), bypass.Get(hdr)
		if m == "" {
			t.Fatalf("%s absent even on a MISS — the fixture is wrong", hdr)
		}
		if hi != m {
			t.Errorf("%s differs between MISS and HIT:\n  miss = %q\n  hit  = %q\n"+
				"a cached response must not have a weaker security posture than a fresh one (SEC-1)", hdr, m, hi)
		}
		if by != m {
			t.Errorf("%s differs between MISS and cache-bypass:\n  miss   = %q\n  bypass = %q", hdr, m, by)
		}
	}
}

// TestPerRequestHeadersAreNotReplayed is the other half of the contract, and the
// reason the cache replays an ALLOWLIST rather than every captured header: a
// per-request value must NOT be served from cache. Replaying one request's trace
// id to every later caller would corrupt the trace ring and make an incident
// unreadable — a worse bug than the one SEC-1 fixed.
func TestPerRequestHeadersAreNotReplayed(t *testing.T) {
	rc := New(5 * time.Second)
	var n int
	h := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("X-Trace-Id", "trace-of-request-1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":[]}`)) //nolint:errcheck
	}))

	get := func() *httptest.ResponseRecorder {
		req := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/traced", nil), "acme"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	get()
	hit := get()

	if hit.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second request was not a HIT — test not exercising the path")
	}
	if got := hit.Header().Get("Content-Security-Policy"); got == "" {
		t.Errorf("the security header was not replayed on the HIT (SEC-1 regression)")
	}
	if got := hit.Header().Get("X-Trace-Id"); got != "" {
		t.Errorf("X-Trace-Id = %q on a cache HIT — a per-request header must NEVER be replayed from cache; "+
			"only the allowlisted security headers are", got)
	}
}

// TestNoSecurityHeadersCostsNothing: a chain that sets none stores none, so the
// fix is free for callers that do not use it.
func TestNoSecurityHeadersCostsNothing(t *testing.T) {
	rc := New(5 * time.Second)
	h := rc.Middleware(echoJSON(`{"data":[]}`, http.StatusOK))
	req := withAuth(withTenant(httptest.NewRequest(http.MethodGet, "/api/plain", nil), "acme"))
	h.ServeHTTP(httptest.NewRecorder(), req)

	rc.mu.RLock()
	defer rc.mu.RUnlock()
	for _, items := range rc.tenants {
		for _, it := range items {
			if it.secHeaders != nil {
				t.Errorf("secHeaders = %v, want nil when the chain sets no security headers", it.secHeaders)
			}
		}
	}
}
