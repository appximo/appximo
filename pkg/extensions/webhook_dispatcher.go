package extensions

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/miguelangel/appitools/pkg/schema"
)

// WebhookDispatcher sends signed HTTP POST notifications to webhook endpoints.
type WebhookDispatcher struct {
	client *http.Client
}

// NewWebhookDispatcher creates a dispatcher with a 10s per-request HTTP timeout.
func NewWebhookDispatcher() *WebhookDispatcher {
	return &WebhookDispatcher{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// Dispatch sends a signed POST to hook.URL with up to 3 retries (4 total attempts)
// and exponential backoff (1s, 2s, 4s). Failures are logged but never returned.
func (d *WebhookDispatcher) Dispatch(ctx context.Context, hook *schema.HookConfig, event string, payload map[string]any, tenantID string) {
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
