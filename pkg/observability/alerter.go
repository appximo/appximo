package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	zlog "github.com/rs/zerolog/log"
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

// Alert kinds: an SLO breach (burn rate / p95) or a NEW ERROR GROUP — a
// defect seen for the first time (OBSERVABILIDAD-ERRORES-S1).
const (
	KindSLO      = ""          // historical default
	KindNewError = "new_error" // first occurrence of a fingerprint
	KindStorm    = "storm"     // many new groups at once, summarized
)

// Alert is a single notification.
type Alert struct {
	TenantID string
	Level    string // LevelWarning | LevelCritical
	Message  string
	BurnRate float64
	P95ms    float64
	// New-error fields (Kind == KindNewError / KindStorm).
	Kind    string
	Route   string
	TraceID string
	Count   int
	// Fields carries the alert's structured detail (age, topic, path, free
	// bytes…) so every sink renders its OWN text from data instead of parsing
	// another sink's sentence — Slack keeps its historical English one-liner,
	// Telegram renders Spanish for a phone (ALERTAS-TELEGRAM-S1). Emit sites
	// fill it; a sink must tolerate it being nil.
	Fields map[string]string
}

// Alerter delivers SLO alerts somewhere (Slack, a no-op sink, …).
type Alerter interface {
	Send(ctx context.Context, a Alert) error
}

// NoopAlerter discards alerts (used when no alert destination — Telegram or
// Slack — is configured).
type NoopAlerter struct{}

// Send logs that the alert was suppressed and returns nil.
func (NoopAlerter) Send(_ context.Context, a Alert) error {
	zlog.Info().Str("tenant_id", a.TenantID).Str("kind", a.Kind).Str("alert_level", a.Level).Str("route", a.Route).Str("message", a.Message).Float64("burn_rate", a.BurnRate).Msg("alert (no webhook configured — recorded only)")
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
//
// Deprecated: the engine wires NewAlerterFromEnv (Telegram + Slack, async
// delivery with retry, fail-fast config). Kept for API compatibility.
func NewSlackAlerterFromEnv() Alerter {
	url := os.Getenv("SLACK_WEBHOOK_URL")
	if url == "" {
		log.Println("[WARN] SLACK_WEBHOOK_URL not set — SLO alerts disabled (noop)")
		return NoopAlerter{}
	}
	return NewSlackAlerter(url)
}

// NewAlerterFromEnv builds the engine's alert channel from the environment
// (ALERTAS-TELEGRAM-S1, closes OPS-47):
//
//   - APPXIMO_TELEGRAM_BOT_TOKEN + APPXIMO_TELEGRAM_CHAT_ID → Telegram (the
//     destination that reaches a phone; Spanish, phone-first rendering).
//   - SLACK_WEBHOOK_URL → Slack (unchanged English one-liner).
//   - Both set → BOTH receive every alert.
//   - APPXIMO_ALERT_APP_NAME names the app in every message (defaults to the
//     schema name); APPXIMO_ALERT_PANEL_URL adds a "Ver el panel" deep link.
//
// Fail-fast both ways: a malformed token/chat id — or only ONE of the pair —
// is an ERROR (the caller refuses to boot naming it; the worker-env
// discipline, AUTO-2). NO destination at all boots — dev and `appximo up`
// must keep working — but says so LOUDLY: that silence in a fleet box was
// OPS-47. A syntactically valid but revoked token is verified out-of-band at
// boot (VerifyLive on a goroutine, read-only getMe+getChat) and screams in
// the log; the boot itself never depends on api.telegram.org being up.
//
// Delivery is out-of-band for every sink: the composite journals each alert
// FIRST (the alert is never lost, even with every channel down), then a
// per-sink queue+worker retries with backoff, honoring Telegram's own
// retry_after on a 429.
func NewAlerterFromEnv(defaultAppName string) (Alerter, error) {
	tgToken := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_BOT_TOKEN"))
	tgChat := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_CHAT_ID"))
	slackURL := strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL"))
	appName := os.Getenv("APPXIMO_ALERT_APP_NAME")
	if appName == "" {
		appName = defaultAppName
	}
	panelURL := strings.TrimSpace(os.Getenv("APPXIMO_ALERT_PANEL_URL"))

	if (tgToken == "") != (tgChat == "") {
		missing, present := "APPXIMO_TELEGRAM_BOT_TOKEN", "APPXIMO_TELEGRAM_CHAT_ID"
		if tgChat == "" {
			missing, present = present, missing
		}
		return nil, fmt.Errorf("appximo: %s is set but %s is missing — Telegram alerts need BOTH (token from @BotFather, chat id from getUpdates after messaging the bot)", present, missing)
	}

	var sinks []Alerter
	if tgToken != "" {
		tg, err := NewTelegramAlerter(tgToken, tgChat, appName, panelURL)
		if err != nil {
			return nil, fmt.Errorf("appximo: telegram alert destination rejected: %w", err)
		}
		go func() { // out-of-band liveness: loud, never boot-blocking
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := tg.VerifyLive(ctx); err != nil {
				zlog.Error().Err(err).Msg("TELEGRAM ALERT DESTINATION NOT WORKING — alerts will be journaled and retried, but check the token (@BotFather) and the chat id NOW")
			}
		}()
		sinks = append(sinks, NewAsyncRetryAlerter("telegram", tg))
	}
	if slackURL != "" {
		sinks = append(sinks, NewAsyncRetryAlerter("slack", NewSlackAlerter(slackURL)))
	}

	if len(sinks) == 0 {
		log.Println("[WARN] ════════════════════════════════════════════════════════════════════")
		log.Println("[WARN] NO ALERT DESTINATION CONFIGURED — failed/stale backups, low disk, SLO")
		log.Println("[WARN] burn, first-occurrence errors and a stuck outbox will only be journal")
		log.Println("[WARN] lines nobody reads (this silence was OPS-47). Set")
		log.Println("[WARN]   APPXIMO_TELEGRAM_BOT_TOKEN + APPXIMO_TELEGRAM_CHAT_ID  (reaches a phone)")
		log.Println("[WARN] and/or SLACK_WEBHOOK_URL, then restart. docs/PRODUCTION.md §4.6b.")
		log.Println("[WARN] ════════════════════════════════════════════════════════════════════")
		return NoopAlerter{}, nil
	}
	return &journalAlerter{inner: MultiAlerter(sinks)}, nil
}

// ── delivery composition: journal → fan-out → per-sink async retry ─────────

// journalAlerter records every alert in the structured log BEFORE any delivery
// attempt — the guarantee that an alert is never LOST, only possibly late.
type journalAlerter struct{ inner Alerter }

func (j *journalAlerter) Send(ctx context.Context, a Alert) error {
	zlog.Warn().Str("tenant_id", a.TenantID).Str("kind", a.Kind).Str("alert_level", a.Level).
		Str("route", a.Route).Str("message", a.Message).Float64("burn_rate", a.BurnRate).
		Msg("alert emitted")
	return j.inner.Send(ctx, a)
}

// MultiAlerter fans one alert out to every sink.
type MultiAlerter []Alerter

// Send forwards to every sink; each sink owns its own failure handling (the
// engine's sinks are AsyncRetryAlerters, so this returns immediately).
func (m MultiAlerter) Send(ctx context.Context, a Alert) error {
	var firstErr error
	for _, s := range m {
		if err := s.Send(ctx, a); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// AsyncRetryAlerter decouples the emitter from the network: Send enqueues and
// returns (nothing alert-related ever blocks a caller — the hot-path rule),
// and one worker goroutine delivers with backoff. On a Telegram 429 it honors
// the API's retry_after. After the last attempt fails the alert is NOT gone:
// it was journaled before delivery was tried, and the failure names the sink.
type AsyncRetryAlerter struct {
	name  string
	inner Alerter
	ch    chan Alert
}

// asyncBackoff is the delay BEFORE each retry (attempt 1 is immediate).
var asyncBackoff = []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}

// NewAsyncRetryAlerter starts the delivery worker for one sink.
func NewAsyncRetryAlerter(name string, inner Alerter) *AsyncRetryAlerter {
	q := &AsyncRetryAlerter{name: name, inner: inner, ch: make(chan Alert, 64)}
	go q.run()
	return q
}

// Send enqueues without blocking. A full queue (64 in flight — only reachable
// with the channel down during a storm) drops the NEW alert with a loud log;
// the journal line already recorded it.
func (q *AsyncRetryAlerter) Send(_ context.Context, a Alert) error {
	select {
	case q.ch <- a:
	default:
		zlog.Error().Str("sink", q.name).Str("kind", a.Kind).Msg("alert queue full — this alert is in the journal only")
	}
	return nil
}

func (q *AsyncRetryAlerter) run() {
	for a := range q.ch {
		var err error
		for attempt := 0; ; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err = q.inner.Send(ctx, a)
			cancel()
			if err == nil {
				zlog.Info().Str("sink", q.name).Str("kind", a.Kind).Str("route", a.Route).Int("attempt", attempt+1).Msg("alert delivered")
				break
			}
			if attempt >= len(asyncBackoff) {
				break
			}
			wait := asyncBackoff[attempt]
			var ra interface{ RetryAfter() time.Duration }
			if errors.As(err, &ra) && ra.RetryAfter() > wait {
				wait = ra.RetryAfter()
			}
			time.Sleep(wait)
		}
		if err != nil {
			zlog.Error().Str("sink", q.name).Str("kind", a.Kind).Err(err).
				Msg("alert delivery FAILED after retries — the alert text is preserved in this journal (search: \"alert emitted\")")
		}
	}
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
	switch a.Kind {
	case KindNewError:
		return fmt.Sprintf("🆕 [tenant %s] new error group on %s — %s (trace %s)", a.TenantID, a.Route, a.Message, a.TraceID)
	case KindStorm:
		return fmt.Sprintf("🌩 [tenant %s] %d NEW error groups in the last minute — individual alerts suppressed until it calms (%s)", a.TenantID, a.Count, a.Message)
	case "ask_spend_warning", "ask_spend_capped", "ask_user_capped":
		return fmt.Sprintf("🧾 [tenant %s] %s", a.TenantID, a.Message)
	}
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

// ── first-occurrence alerts with a noise brake ────────────────────────────

// NewErrorNotifier turns "this fingerprint was never seen for this tenant"
// into ONE alert — at the first occurrence, not when the SLO budget burns
// (a systematic, reproducible 500 used to generate nothing until then). The
// brake: at most maxPerMinute new-group alerts per tenant per minute; past
// that, the notifier sends ONE storm summary per stormCooldown naming how
// many groups it suppressed. A thousand new groups in a deploy gone wrong is
// one message, not a thousand.
type NewErrorNotifier struct {
	inner         Alerter
	maxPerMinute  int
	stormCooldown time.Duration
	mu            sync.Mutex
	windows       map[string]*alertWindow // per tenant
	nowFn         func() time.Time
}

type alertWindow struct {
	start      time.Time
	sent       int
	suppressed int
	lastStorm  time.Time
}

// NewNewErrorNotifier wraps inner with the per-tenant brake.
func NewNewErrorNotifier(inner Alerter, maxPerMinute int, stormCooldown time.Duration) *NewErrorNotifier {
	if maxPerMinute <= 0 {
		maxPerMinute = 5
	}
	if stormCooldown <= 0 {
		stormCooldown = 5 * time.Minute
	}
	return &NewErrorNotifier{inner: inner, maxPerMinute: maxPerMinute, stormCooldown: stormCooldown,
		windows: map[string]*alertWindow{}, nowFn: time.Now}
}

// NewGroup reports a first occurrence. Returns whether an individual alert
// went out (false = braked; a storm summary may have gone out instead).
func (n *NewErrorNotifier) NewGroup(ctx context.Context, a Alert) bool {
	a.Kind = KindNewError
	now := n.nowFn()
	n.mu.Lock()
	w := n.windows[a.TenantID]
	if w == nil || now.Sub(w.start) >= time.Minute {
		w = &alertWindow{start: now}
		n.windows[a.TenantID] = w
	}
	if w.sent < n.maxPerMinute {
		w.sent++
		n.mu.Unlock()
		_ = n.inner.Send(ctx, a)
		return true
	}
	w.suppressed++
	storm := now.Sub(w.lastStorm) >= n.stormCooldown
	if storm {
		w.lastStorm = now
	}
	count := w.suppressed + w.sent
	n.mu.Unlock()
	if storm {
		_ = n.inner.Send(ctx, Alert{TenantID: a.TenantID, Kind: KindStorm, Count: count,
			Message: "last: " + a.Route + " — " + a.Message})
	}
	return false
}
