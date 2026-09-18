package extensions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	zlog "github.com/rs/zerolog/log"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/appximo/appximo/pkg/schema"
)

// maxWebhookRespBytes bounds how much of a webhook endpoint's response body the
// dispatcher will read. Only the status code matters, so the body is drained (up
// to this limit) into io.Discard purely to allow keep-alive connection reuse —
// never buffering an unbounded or malicious response into memory. This is the
// MaxBytesReader-equivalent for an outbound HTTP consumer (OWASP API10: unsafe
// consumption of upstream APIs).
const maxWebhookRespBytes = 64 << 10 // 64 KB

// WebhookDispatcher sends signed HTTP POST notifications to webhook endpoints.
type WebhookDispatcher struct {
	client       *http.Client
	enforceHTTPS bool

	// deadLetter, when set, receives every dispatch that EXHAUSTED its retries
	// (AUTO-6). Before it existed, an exhausted webhook vanished with one log
	// line: for the receiving system (an ERP, an accountant) the event simply
	// never happened, and nobody on this side knew. The engine wires this to an
	// outbox enqueue with topic "webhook.dead", so the loss becomes a durable,
	// visible row: /admin/outbox lists it, the pending-age metric and alert name
	// it, and the operator can re-fire it by hand (the payload carries the URL,
	// the event and the original body). See docs/PRODUCTION.md §webhooks.
	deadLetter func(ctx context.Context, tenantID string, deadEvent map[string]any)
}

// WebhookDeadTotal counts dispatches that exhausted their retries (exported for
// /metrics via a CounterFunc). Incremented whether or not a dead-letter sink is
// configured.
var WebhookDeadTotal atomic.Int64

// SetDeadLetter installs the exhausted-dispatch sink. Call before serving.
func (d *WebhookDispatcher) SetDeadLetter(fn func(ctx context.Context, tenantID string, deadEvent map[string]any)) {
	d.deadLetter = fn
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
	return NewSSRFSafeClient(10 * time.Second)
}

// NewSSRFSafeClient returns an http.Client whose dialer rejects private, loopback,
// and link-local addresses and whose redirect policy refuses all redirects. The
// given timeout bounds the dial, TLS handshake, response-header wait, and the whole
// request. Exported so other outbound senders (e.g. the SLO Slack alerter) reuse the
// exact same egress guard instead of duplicating it.
func NewSSRFSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   ssrfDialerControl,
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
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
		zlog.Warn().Str("tenant_id", tenantID).Str("url", hook.URL).Msg("webhook rejected: only HTTPS endpoints are allowed")
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		zlog.Error().Str("tenant_id", tenantID).Err(err).Msg("webhook: marshal error")
		return
	}

	secret := os.Getenv(hook.HMACSecretEnv)
	sig := "sha256=" + signHMAC(secret, body)

	const maxAttempts = 4
	var lastErr string
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
			zlog.Error().Str("tenant_id", tenantID).Err(err).Msg("webhook: build request error")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Appximo-Event", event)
		req.Header.Set("X-Appximo-Signature", sig)

		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = err.Error()
			zlog.Warn().Str("tenant_id", tenantID).Int("attempt", attempt+1).Int("max_attempts", maxAttempts).Err(err).Msg("webhook: attempt failed")
			continue
		}
		// Bounded drain: read at most maxWebhookRespBytes so the connection can be
		// reused, without ever buffering an unbounded/malicious response body.
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookRespBytes)) //nolint:errcheck
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return
		}
		lastErr = fmt.Sprintf("endpoint answered %d", resp.StatusCode)
		zlog.Warn().Str("tenant_id", tenantID).Int("attempt", attempt+1).Int("max_attempts", maxAttempts).Int("status", resp.StatusCode).Msg("webhook: non-2xx")
	}

	// Exhausted (AUTO-6): count it, log it, and — when a sink is wired — park a
	// durable dead-letter row instead of letting the delivery vanish.
	WebhookDeadTotal.Add(1)
	zlog.Error().Str("tenant_id", tenantID).Str("url", hook.URL).Str("last_error", lastErr).
		Msg("webhook: all attempts exhausted — dead-lettered to the outbox as topic webhook.dead (or lost if no dead-letter sink is wired)")
	if d.deadLetter != nil {
		// Fresh context: the request ctx may already be cancelled, and the
		// dead-letter write must not die with it.
		dlCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		d.deadLetter(dlCtx, tenantID, map[string]any{
			"url":        hook.URL,
			"event":      event,
			"payload":    payload,
			"error":      lastErr,
			"attempts":   maxAttempts,
			"exhausted":  time.Now().UTC().Format(time.RFC3339),
			"redeliver":  "POST the payload to the url with headers X-Appximo-Event and X-Appximo-Signature (sha256 HMAC of the body with the hook's secret), or fix the endpoint and re-enqueue",
			"secret_env": hook.HMACSecretEnv,
		})
	}
}

// DispatchOnce sends ONE signed POST and returns the outcome — the synchronous
// primitive the workflow executor's `webhook` step uses (workflow retries happen
// at the RUN level through the outbox, so the fire-and-forget retry loop of
// Dispatch would double-retry). Same client, same SSRF guard, same HTTPS-only
// rule, same signature scheme as Dispatch.
func (d *WebhookDispatcher) DispatchOnce(ctx context.Context, url, secretEnv, event string, payload any) error {
	if d.enforceHTTPS && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("webhook: only HTTPS endpoints are allowed, got %q", url)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}
	secret := os.Getenv(secretEnv)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Appximo-Event", event)
	req.Header.Set("X-Appximo-Signature", "sha256="+signHMAC(secret, body))
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, maxWebhookRespBytes)) //nolint:errcheck
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: endpoint answered %d", resp.StatusCode)
	}
	return nil
}

func signHMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
