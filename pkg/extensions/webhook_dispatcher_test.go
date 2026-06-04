package extensions_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/schema"
)

// testDispatcher returns a dispatcher without SSRF/HTTPS enforcement for tests
// that use httptest servers (which are HTTP on loopback by design).
func testDispatcher() *extensions.WebhookDispatcher {
	return extensions.NewWebhookDispatcherOpts(
		extensions.WithInsecureTransport(&http.Client{Timeout: 10e9}),
	)
}

func expectedHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookDispatcher_SignatureAndEvent(t *testing.T) {
	const secret = "test-secret-xyz"
	os.Setenv("TEST_HMAC_SECRET", secret)
	defer os.Unsetenv("TEST_HMAC_SECRET")

	var (
		gotSig   string
		gotEvent string
		gotBody  []byte
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Appitools-Signature")
		gotEvent = r.Header.Get("X-Appitools-Event")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hook := &schema.HookConfig{
		Type:          "webhook",
		URL:           srv.URL,
		HMACSecretEnv: "TEST_HMAC_SECRET",
	}

	payload := map[string]any{"id": "guide-001", "code": "GU-001", "status": "pending"}
	d := testDispatcher()
	d.Dispatch(context.Background(), hook, "after_create", payload, "tenant_test")

	wantBody, _ := json.Marshal(payload)
	wantSig := expectedHMAC(secret, wantBody)

	if gotSig != wantSig {
		t.Errorf("X-Appitools-Signature: got %q, want %q", gotSig, wantSig)
	}
	if gotEvent != "after_create" {
		t.Errorf("X-Appitools-Event: got %q, want %q", gotEvent, "after_create")
	}
	if !bytes.Equal(gotBody, wantBody) {
		t.Errorf("body: got %s, want %s", gotBody, wantBody)
	}
}

func TestWebhookDispatcher_RetriesOnFailure(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	hook := &schema.HookConfig{Type: "webhook", URL: srv.URL, HMACSecretEnv: ""}
	d := testDispatcher()
	d.Dispatch(context.Background(), hook, "after_create", map[string]any{"id": "x"}, "t")

	if attempts != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", attempts)
	}
}

func TestWebhookDispatcher_ContextCancelStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	hook := &schema.HookConfig{Type: "webhook", URL: srv.URL, HMACSecretEnv: ""}
	ctx, cancel := context.WithCancel(context.Background())
	d := testDispatcher()
	go func() { cancel() }()
	d.Dispatch(ctx, hook, "after_create", map[string]any{"id": "x"}, "t")
	// No panic, no block — test passes.
}

// --- SSRF Guard unit tests ---

func TestCheckSSRF_BlocksLoopback(t *testing.T) {
	ips := []string{"127.0.0.1", "127.0.0.2", "::1"}
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if err := extensions.CheckSSRF(ip); err == nil {
			t.Errorf("loopback IP %s must be blocked, got nil", raw)
		}
	}
}

func TestCheckSSRF_BlocksPrivate(t *testing.T) {
	ips := []string{"10.0.0.1", "172.16.0.1", "192.168.1.100", "fc00::1"}
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if err := extensions.CheckSSRF(ip); err == nil {
			t.Errorf("private IP %s must be blocked, got nil", raw)
		}
	}
}

func TestCheckSSRF_BlocksLinkLocal(t *testing.T) {
	// 169.254.169.254 is the AWS/GCP instance metadata IP.
	ips := []string{"169.254.169.254", "169.254.0.1", "fe80::1"}
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if err := extensions.CheckSSRF(ip); err == nil {
			t.Errorf("link-local IP %s must be blocked, got nil", raw)
		}
	}
}

func TestCheckSSRF_AllowsPublicIPs(t *testing.T) {
	ips := []string{"8.8.8.8", "1.1.1.1", "104.21.0.1", "2606:4700::1"}
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if err := extensions.CheckSSRF(ip); err != nil {
			t.Errorf("public IP %s should be allowed, got: %v", raw, err)
		}
	}
}

func TestDispatcher_RejectsHTTPURLsInProduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Should never be called.
		t.Error("handler was called; HTTPS enforcement failed")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Production dispatcher — enforceHTTPS=true by default.
	d := extensions.NewWebhookDispatcher()
	hook := &schema.HookConfig{
		Type: "webhook",
		URL:  srv.URL, // http:// URL
	}
	// Dispatch must return immediately after logging the rejection.
	d.Dispatch(context.Background(), hook, "test", map[string]any{}, "tenant")
}

func TestDispatcher_BlocksLoopbackViaSSRFDialer(t *testing.T) {
	// The production dispatcher must not connect to 127.0.0.1 (loopback).
	d := extensions.NewWebhookDispatcher()
	hook := &schema.HookConfig{
		Type: "webhook",
		URL:  "https://127.0.0.1/evil",
	}
	// Cancel quickly so we don't wait through all retry backoffs.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	// Dispatch should fail at dial time (SSRF guard) and log the error. No panic.
	d.Dispatch(ctx, hook, "test", map[string]any{}, "tenant")
}

func TestCheckSSRF_ErrorContainsBlockReason(t *testing.T) {
	tests := []struct {
		ip     string
		reason string
	}{
		{"127.0.0.1", "loopback"},
		{"10.0.0.1", "private"},
		{"169.254.169.254", "link-local"},
	}
	for _, tt := range tests {
		err := extensions.CheckSSRF(net.ParseIP(tt.ip))
		if err == nil {
			t.Errorf("%s: expected error", tt.ip)
			continue
		}
		if !strings.Contains(err.Error(), tt.reason) {
			t.Errorf("%s: expected %q in error %q", tt.ip, tt.reason, err.Error())
		}
	}
}

// A webhook endpoint returning a large body must be handled without buffering it
// all into memory: the dispatcher drains at most maxWebhookRespBytes and treats a
// 2xx as success, returning promptly. (OWASP API10 bounded consumption.)
func TestWebhookDispatcher_BoundsResponseBody(t *testing.T) {
	const big = 5 << 20 // 5 MB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(bytes.Repeat([]byte("A"), big)) //nolint:errcheck
	}))
	defer srv.Close()

	hook := &schema.HookConfig{Type: "webhook", URL: srv.URL}
	d := testDispatcher()
	done := make(chan struct{})
	go func() {
		d.Dispatch(context.Background(), hook, "after_create", map[string]any{"id": "x"}, "t")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Dispatch did not return promptly for a large response body")
	}
}
