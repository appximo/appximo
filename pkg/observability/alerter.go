package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/appximo/appximo/pkg/extensions"
)

// Alert levels.
const (
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Alert is a single SLO-breach notification.
type Alert struct {
	TenantID string
	Level    string // LevelWarning | LevelCritical
	Message  string
	BurnRate float64
	P95ms    float64
}

// Alerter delivers SLO alerts somewhere (Slack, a no-op sink, …).
type Alerter interface {
	Send(ctx context.Context, a Alert) error
}

// NoopAlerter discards alerts (used when no SLACK_WEBHOOK_URL is configured).
type NoopAlerter struct{}

// Send logs that the alert was suppressed and returns nil.
func (NoopAlerter) Send(_ context.Context, a Alert) error {
	log.Printf("[INFO] alert suppressed (noop): [tenant %s] %s burn=%.1fx", a.TenantID, a.Level, a.BurnRate)
	return nil
}

// SlackAlerter posts alerts to a Slack incoming-webhook URL.
// The HTTP client is the shared SSRF-safe egress client (blocks loopback/private/
// link-local and refuses redirects), so a misconfigured webhook can't be used to
// reach internal services.
type SlackAlerter struct {
	webhookURL string
	client     *http.Client
}

// NewSlackAlerter builds a SlackAlerter posting to webhookURL with a 5s timeout.
func NewSlackAlerter(webhookURL string) *SlackAlerter {
	return &SlackAlerter{
		webhookURL: webhookURL,
		client:     extensions.NewSSRFSafeClient(5 * time.Second),
	}
}

// NewSlackAlerterFromEnv returns a SlackAlerter when SLACK_WEBHOOK_URL is set, or a
// NoopAlerter otherwise — so the server always starts cleanly without alerting config.
func NewSlackAlerterFromEnv() Alerter {
	url := os.Getenv("SLACK_WEBHOOK_URL")
	if url == "" {
		log.Println("[WARN] SLACK_WEBHOOK_URL not set — SLO alerts disabled (noop)")
		return NoopAlerter{}
	}
	return NewSlackAlerter(url)
}

// Send posts the formatted alert to Slack. A blank webhook URL is treated as a soft
// no-op (log + nil) rather than an error or panic.
func (s *SlackAlerter) Send(ctx context.Context, a Alert) error {
	if s.webhookURL == "" {
		log.Printf("[WARN] SlackAlerter.Send called with empty webhook URL — dropping alert for tenant %s", a.TenantID)
		return nil
	}

	body, err := json.Marshal(map[string]string{"text": slackText(a)})
	if err != nil {
		return fmt.Errorf("slack alert marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack alert request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack alert send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack alert: unexpected status %d", resp.StatusCode)
	}
	return nil
}

// slackText renders an Alert as a single Slack message line, e.g.
// "🔴 [tenant 10] CRITICAL burn rate 18.2x | p95=145ms (SLO: 100ms)".
func slackText(a Alert) string {
	emoji := "🟡"
	if a.Level == LevelCritical {
		emoji = "🔴"
	}
	text := fmt.Sprintf("%s [tenant %s] %s burn rate %.1fx | p95=%.0fms",
		emoji, a.TenantID, strings.ToUpper(a.Level), a.BurnRate, a.P95ms)
	if a.Message != "" {
		text += " (" + a.Message + ")"
	}
	return text
}

// CooldownAlerter wraps another Alerter and drops repeat alerts for the same
// (tenant, level) pair that arrive within the cooldown window.
type CooldownAlerter struct {
	inner    Alerter
	cooldown time.Duration
	last     sync.Map // key "tenantID|level" -> time.Time
}

// NewCooldownAlerter wraps inner so that each (tenant, level) fires at most once per cooldown.
func NewCooldownAlerter(inner Alerter, cooldown time.Duration) *CooldownAlerter {
	return &CooldownAlerter{inner: inner, cooldown: cooldown}
}

// Send forwards to the inner Alerter unless an alert for the same (tenant, level)
// was sent less than cooldown ago, in which case it is suppressed (returns nil).
func (c *CooldownAlerter) Send(ctx context.Context, a Alert) error {
	key := a.TenantID + "|" + a.Level
	now := time.Now()
	if v, ok := c.last.Load(key); ok {
		if now.Sub(v.(time.Time)) < c.cooldown {
			return nil // within cooldown — suppress
		}
	}
	c.last.Store(key, now)
	return c.inner.Send(ctx, a)
}
