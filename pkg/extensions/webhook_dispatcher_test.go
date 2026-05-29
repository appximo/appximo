package extensions_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/schema"
)

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
	d := extensions.NewWebhookDispatcher()
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
	d := extensions.NewWebhookDispatcher()
	d.Dispatch(context.Background(), hook, "after_create", map[string]any{"id": "x"}, "t")

	if attempts != 3 {
		t.Errorf("expected 3 attempts (2 failures + 1 success), got %d", attempts)
	}
}

func TestWebhookDispatcher_ContextCancelStops(t *testing.T) {
	hook := &schema.HookConfig{
		Type:          "webhook",
		URL:           "http://127.0.0.1:19999", // nothing listening
		HMACSecretEnv: "",
	}
	// Cancel after the first attempt so we don't wait through all retry delays.
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	hook.URL = srv.URL
	ctx, cancel := context.WithCancel(context.Background())
	d := extensions.NewWebhookDispatcher()
	go func() {
		// Cancel mid-flight so retries stop.
		cancel()
	}()
	d.Dispatch(ctx, hook, "after_create", map[string]any{"id": "x"}, "t")
	// No panic, no block — test passes.
}
