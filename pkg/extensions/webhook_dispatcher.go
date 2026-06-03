package extensions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/miguelangel/appitools/pkg/schema"
)

// WebhookDispatcher sends signed HTTP POST notifications to webhook endpoints.
type WebhookDispatcher struct {
	client       *http.Client
	enforceHTTPS bool
}

// NewWebhookDispatcher creates a production dispatcher with:
//   - SSRF egress guard (blocks loopback, private, and link-local IPs)
//   - HTTPS-only enforcement
//   - No redirect following (prevents open-redirect SSRF bypasses)
func NewWebhookDispatcher() *WebhookDispatcher {
	return &WebhookDispatcher{
		client:       newSSRFSafeClient(),
		enforceHTTPS: true,
	}
}

// WithInsecureTransport returns a dispatcher that bypasses SSRF and HTTPS
// checks. Use ONLY in tests where the target is a local httptest server.
func WithInsecureTransport(c *http.Client) func(*WebhookDispatcher) {
	return func(d *WebhookDispatcher) {
		d.client = c
		d.enforceHTTPS = false
	}
}

// NewWebhookDispatcherOpts creates a dispatcher with optional overrides.
func NewWebhookDispatcherOpts(opts ...func(*WebhookDispatcher)) *WebhookDispatcher {
	d := &WebhookDispatcher{
		client:       newSSRFSafeClient(),
		enforceHTTPS: true,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// newSSRFSafeClient builds an http.Client whose dialer rejects private/loopback/
// link-local addresses and whose redirect policy refuses all redirects.
func newSSRFSafeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   ssrfDialerControl,
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
		},
		// Never follow redirects — a 3xx to an internal IP would bypass the SSRF guard.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return fmt.Errorf("ssrf guard: redirect not allowed")
		},
	}
}

// Dispatch sends a signed POST to hook.URL with up to 3 retries (4 total attempts)
// and exponential backoff (1s, 2s, 4s). Failures are logged but never returned.
func (d *WebhookDispatcher) Dispatch(ctx context.Context, hook *schema.HookConfig, event string, payload map[string]any, tenantID string) {
	if d.enforceHTTPS && !strings.HasPrefix(hook.URL, "https://") {
		log.Printf("WEBHOOK [%s] rejected: only HTTPS endpoints are allowed (got %q)", tenantID, hook.URL)
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("WEBHOOK [%s] marshal error: %v", tenantID, err)
		return
	}

	secret := os.Getenv(hook.HMACSecretEnv)
	sig := "sha256=" + signHMAC(secret, body)

	const maxAttempts = 4
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.Duration(1<<uint(attempt-1)) * time.Second // 1s, 2s, 4s
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("WEBHOOK [%s] build request error: %v", tenantID, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Appitools-Event", event)
		req.Header.Set("X-Appitools-Signature", sig)

		resp, err := d.client.Do(req)
		if err != nil {
			log.Printf("WEBHOOK [%s] attempt %d/%d error: %v", tenantID, attempt+1, maxAttempts, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		log.Printf("WEBHOOK [%s] attempt %d/%d status %d", tenantID, attempt+1, maxAttempts, resp.StatusCode)
	}
	log.Printf("WEBHOOK [%s] all attempts failed for %s", tenantID, hook.URL)
}

func signHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
