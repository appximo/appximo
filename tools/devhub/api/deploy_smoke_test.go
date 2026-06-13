package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newSmokeServer returns a test HTTP server that serves /health and /readyz.
// versionFn is called on each /health request and controls the returned version
// field; it receives the 1-based call count so tests can vary the response over
// time without shared state.
func newSmokeServer(t *testing.T, versionFn func(call int) string) *httptest.Server {
	t.Helper()
	calls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			calls++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"version": versionFn(calls),
			})
		case "/readyz":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestSmokeCheckAddr_VersionMatch(t *testing.T) {
	srv := newSmokeServer(t, func(_ int) string { return "abc1234" })
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := smokeCheckAddr(addr, 3, "abc1234", 0); err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestSmokeCheckAddr_VersionMismatch(t *testing.T) {
	srv := newSmokeServer(t, func(_ int) string { return "oldsha00" })
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	err := smokeCheckAddr(addr, 3, "newsha00", 0)
	if err == nil {
		t.Fatal("expected failure on version mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("expected 'version mismatch' in error, got: %v", err)
	}
}

func TestSmokeCheckAddr_RetriableStartup(t *testing.T) {
	// Engine not ready for first 2 polls (503), then starts responding with
	// the correct version — smoke must succeed after retrying.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			calls++
			if calls < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "newsha00"})
		case "/readyz":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if err := smokeCheckAddr(addr, 5, "newsha00", 0); err != nil {
		t.Fatalf("expected success after startup retries, got: %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected at least 3 /health polls, got %d", calls)
	}
}

func TestSmokeCheckAddr_HealthUnreachable(t *testing.T) {
	// Start a server to grab a port, then close it so that port is unreachable.
	srv := newSmokeServer(t, func(_ int) string { return "anysha1" })
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()
	err := smokeCheckAddr(addr, 2, "anysha1", 0)
	if err == nil {
		t.Fatal("expected failure when engine is unreachable, got nil")
	}
}
