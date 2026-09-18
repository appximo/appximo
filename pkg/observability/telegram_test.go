package observability

// ALERTAS-TELEGRAM-S1: the Telegram sink, the multi-destination fan-out, the
// async retry queue and the fail-fast env constructor. The fake Bot API runs
// on 127.0.0.1, which the production SSRF-safe client rightly refuses — tests
// swap in a plain client and point apiBase at the fake.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testTelegram(t *testing.T, handler http.HandlerFunc) *TelegramAlerter {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	tg, err := NewTelegramAlerter("123456:AAExampleExampleExampleExample01", "8851136988", "La Tiendita", "https://tiendita.example.com")
	if err != nil {
		t.Fatalf("NewTelegramAlerter: %v", err)
	}
	tg.client = srv.Client()
	tg.apiBase = srv.URL
	return tg
}

func TestTelegram_ConfigShapeFailFast(t *testing.T) {
	cases := []struct{ token, chat, wantSub string }{
		{"not-a-token", "123", "APPXIMO_TELEGRAM_BOT_TOKEN"},
		{"123456:short", "123", "APPXIMO_TELEGRAM_BOT_TOKEN"},
		{"123456:AAExampleExampleExampleExample01", "twelve", "APPXIMO_TELEGRAM_CHAT_ID"},
		{"123456:AAExampleExampleExampleExample01", "@ab", "APPXIMO_TELEGRAM_CHAT_ID"},
	}
	for _, c := range cases {
		_, err := NewTelegramAlerter(c.token, c.chat, "app", "")
		if err == nil || !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("token=%q chat=%q: want error naming %s, got %v", c.token, c.chat, c.wantSub, err)
		}
		if err != nil && strings.Contains(err.Error(), "AAExampleExampleExampleExample01") {
			t.Errorf("the error echoed the token: %v", err)
		}
	}
	if _, err := NewTelegramAlerter("123456:AAExampleExampleExampleExample01", "-100123456", "app", ""); err != nil {
		t.Errorf("negative (group) chat id must be valid, got %v", err)
	}
	if _, err := NewTelegramAlerter("123456:AAExampleExampleExampleExample01", "@mychannel", "app", ""); err != nil {
		t.Errorf("@channel chat id must be valid, got %v", err)
	}
}

func TestNewAlerterFromEnv_FailFastAndLoudSilence(t *testing.T) {
	// Half a pair refuses to boot naming the missing variable.
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("APPXIMO_TELEGRAM_BOT_TOKEN", "123456:AAExampleExampleExampleExample01")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "")
	if _, err := NewAlerterFromEnv("app"); err == nil || !strings.Contains(err.Error(), "APPXIMO_TELEGRAM_CHAT_ID") {
		t.Fatalf("token without chat id must fail naming the chat var, got %v", err)
	}
	t.Setenv("APPXIMO_TELEGRAM_BOT_TOKEN", "garbage")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "8851136988")
	if _, err := NewAlerterFromEnv("app"); err == nil || !strings.Contains(err.Error(), "APPXIMO_TELEGRAM_BOT_TOKEN") {
		t.Fatalf("malformed token must fail naming the token var, got %v", err)
	}
	// No destination at all: boots (Noop), never an error — dev and `up` keep working.
	t.Setenv("APPXIMO_TELEGRAM_BOT_TOKEN", "")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "")
	a, err := NewAlerterFromEnv("app")
	if err != nil {
		t.Fatalf("no destination must still boot, got %v", err)
	}
	if _, ok := a.(NoopAlerter); !ok {
		t.Fatalf("no destination must yield NoopAlerter, got %T", a)
	}
}

func TestTelegram_SendPayloadIsSpanishPhoneFirst(t *testing.T) {
	var got struct {
		ChatID    json.Number `json:"chat_id"`
		Text      string      `json:"text"`
		ParseMode string      `json:"parse_mode"`
		NoPreview bool        `json:"disable_web_page_preview"`
	}
	tg := testTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	})
	err := tg.Send(context.Background(), Alert{
		Kind: "outbox_stale_pending", Level: LevelWarning,
		Message: "outbox: the oldest pending event is 48d old",
		Fields:  map[string]string{"age_s": "4165200", "topic": "factura.emitir", "pending": "34", "threshold_s": "900"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.ChatID.String() != "8851136988" || got.ParseMode != "HTML" || !got.NoPreview {
		t.Fatalf("payload basics wrong: %+v", got)
	}
	for _, want := range []string{"AVISO", "La Tiendita", "factura.emitir", "34 pendientes", "48.2 días", "Qué hacer", "systemctl status", "https://tiendita.example.com/admin/outbox"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("message must contain %q; got:\n%s", want, got.Text)
		}
	}
}

func TestTelegramText_EveryKindLeadsInSpanish(t *testing.T) {
	cases := []struct {
		name string
		a    Alert
		want []string
	}{
		{"slo", Alert{Level: LevelCritical, BurnRate: 18.2, P95ms: 145, Message: "SLO: 100ms"},
			[]string{"🔴", "CRÍTICA", "18.2×", "p95 145 ms", "Qué hacer"}},
		{"new_error", Alert{Kind: KindNewError, Route: "POST /api/ordenes", Message: "duplicate key", TraceID: "abc123", TenantID: "tiendita"},
			[]string{"🆕", "Error nuevo", "POST /api/ordenes", "abc123", "Problemas"}},
		{"storm", Alert{Kind: KindStorm, Count: 41, Message: "last: /api/x — boom"},
			[]string{"🌩", "41", "frené", "deploy"}},
		{"disk", Alert{Kind: KindHost, Route: "disk", Level: LevelCritical,
			Fields: map[string]string{"path": "/", "free": "900 MiB", "total": "25 GiB", "pct": "3.6"}},
			[]string{"Disco bajo", "900 MiB", "3.6 %", "PostgreSQL", "Qué hacer"}},
		{"backup_failed", Alert{Kind: KindHost, Route: "backup", Level: LevelCritical,
			Fields: map[string]string{"status": "failed", "age_s": "93600"}},
			[]string{"Backup", "FALLÓ", "journalctl -u '*-backup'"}},
		{"backup_stale", Alert{Kind: KindHost, Route: "backup", Level: LevelCritical,
			Fields: map[string]string{"status": "ok", "age_s": "180000", "floor_s": "129600"}},
			[]string{"2.1 días", "36.0 h", "timer"}},
		{"outbox_failed", Alert{Kind: "outbox_failed", Fields: map[string]string{"failed": "3", "oldest_failed_age_s": "7200"}},
			[]string{"3 evento(s)", "last_error", "/admin/outbox"}},
		{"workflow_overdue", Alert{Kind: "workflow_overdue", Fields: map[string]string{"overdue_s": "1260"}},
			[]string{"21 min", "scheduler", "worker"}},
		{"unknown_kind_still_spanish", Alert{Kind: "future_kind", Level: LevelWarning, Message: "some english detail"},
			[]string{"AVISO", "Detalle técnico", "Qué hacer"}},
	}
	for _, c := range cases {
		text := telegramText(c.a, "PetFriendly", "")
		if !strings.Contains(text, "PetFriendly") {
			t.Errorf("%s: message must name the app; got:\n%s", c.name, text)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: want %q in:\n%s", c.name, w, text)
			}
		}
	}
}

func TestTelegram_429ReturnsRetryAfter(t *testing.T) {
	tg := testTelegram(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"ok":false,"description":"Too Many Requests","parameters":{"retry_after":7}}`)
	})
	err := tg.Send(context.Background(), Alert{Message: "x"})
	var ra *RetryAfterError
	if !errors.As(err, &ra) || ra.RetryAfter() != 7*time.Second {
		t.Fatalf("want RetryAfterError 7s, got %v", err)
	}
}

func TestTelegram_ErrorsNeverCarryTheToken(t *testing.T) {
	tg := testTelegram(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"ok":false,"description":"Unauthorized"}`)
	})
	err := tg.Send(context.Background(), Alert{Message: "x"})
	if err == nil || strings.Contains(err.Error(), "AAExampleExampleExampleExample01") {
		t.Fatalf("error must not carry the token: %v", err)
	}
	// A transport-level error embeds the request URL (which has the token) —
	// the redaction must scrub it too.
	tg.apiBase = "http://127.0.0.1:1" // nothing listens
	tg.client = &http.Client{Timeout: 200 * time.Millisecond}
	err = tg.Send(context.Background(), Alert{Message: "x"})
	if err == nil || strings.Contains(err.Error(), "AAExampleExampleExampleExample01") {
		t.Fatalf("transport error must not carry the token: %v", err)
	}
}

// flakySink fails n times, then succeeds.
type flakySink struct {
	mu    sync.Mutex
	fails int
	sent  []Alert
}

func (f *flakySink) Send(_ context.Context, a Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fails > 0 {
		f.fails--
		return errors.New("channel down")
	}
	f.sent = append(f.sent, a)
	return nil
}

func TestAsyncRetry_DeliversThroughFailures(t *testing.T) {
	old := asyncBackoff
	asyncBackoff = []time.Duration{5 * time.Millisecond, 5 * time.Millisecond, 5 * time.Millisecond}
	defer func() { asyncBackoff = old }()

	sink := &flakySink{fails: 2}
	q := NewAsyncRetryAlerter("test", sink)
	if err := q.Send(context.Background(), Alert{Message: "hola"}); err != nil {
		t.Fatalf("Send must never error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		sink.mu.Lock()
		n := len(sink.sent)
		sink.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("alert was not delivered after retries")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMultiAlerter_FansOutToEverySink(t *testing.T) {
	var a, b atomic.Int32
	mk := func(n *atomic.Int32) Alerter {
		return alerterFunc(func(context.Context, Alert) error { n.Add(1); return nil })
	}
	m := MultiAlerter{mk(&a), mk(&b)}
	if err := m.Send(context.Background(), Alert{}); err != nil {
		t.Fatal(err)
	}
	if a.Load() != 1 || b.Load() != 1 {
		t.Fatalf("both sinks must receive the alert: a=%d b=%d", a.Load(), b.Load())
	}
}

type alerterFunc func(context.Context, Alert) error

func (f alerterFunc) Send(ctx context.Context, a Alert) error { return f(ctx, a) }
