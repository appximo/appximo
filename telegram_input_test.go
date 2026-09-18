package appximo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/telegram"
)

const testJWTSecret = "a-test-secret-of-at-least-32-characters!!"

// fakeTG is a stand-in Telegram Bot API: it serves one getUpdates batch then
// empties, and captures every sendMessage. The receiver can't be driven by a
// real inbound user message (a bot cannot generate one), so the API is faked
// while the engine self-call it makes is the REAL router.
type fakeTG struct {
	mu       sync.Mutex
	updates  []telegram.Update
	sent     []string
	served   bool
	sendChat []int64
}

func (f *fakeTG) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			f.mu.Lock()
			defer f.mu.Unlock()
			var res []telegram.Update
			if !f.served {
				res = f.updates
				f.served = true
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": res}) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				ChatID json.Number `json:"chat_id"`
				Text   string      `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			f.mu.Lock()
			f.sent = append(f.sent, body.Text)
			n, _ := body.ChatID.Int64()
			f.sendChat = append(f.sendChat, n)
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 1}}) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/deleteWebhook"):
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true}) //nolint:errcheck
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true}) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTG) replies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.sent))
	copy(out, f.sent)
	return out
}

// stubSummaryRouter returns a handler that behaves like the real chain enough to
// verify the receiver: it demands a valid Bearer for (tenant, role), reads the
// Host, and answers /api/summary with a role-dependent text.
func stubSummaryRouter(t *testing.T, wantTenant, wantRole string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/summary" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		authz := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, err := auth.ValidateToken(authz, testJWTSecret)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if claims.Role != wantRole || claims.TenantID != wantTenant {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Host, wantTenant+".") {
			t.Errorf("self-call Host must carry the tenant subdomain, got %q", r.Host)
		}
		text := "📋 Resumen — 2 pedidos nuevos"
		if r.URL.Query().Get("view") == "census" {
			text = "📊 Estado — pedidos: 7"
		}
		json.NewEncoder(w).Encode(map[string]any{"text": text}) //nolint:errcheck
	})
}

func newTestReceiver(t *testing.T, tgSrv *httptest.Server, router http.Handler) *telegramReceiver {
	client, err := telegram.New("123456:AAExampleExampleExampleExample01", "8851136988")
	if err != nil {
		t.Fatal(err)
	}
	client.SetHTTPClient(tgSrv.Client())
	client.SetAPIBase(tgSrv.URL)
	return &telegramReceiver{
		client:      client,
		authChatID:  8851136988,
		tenant:      "acme",
		role:        "owner",
		jwtSecret:   testJWTSecret,
		hostSuffix:  ".svc.internal",
		getRouter:   func() http.Handler { return router },
		pollTimeout: 0,
	}
}

func TestReceiver_AuthorizedResumen(t *testing.T) {
	f := &fakeTG{updates: []telegram.Update{{UpdateID: 1, Message: msg(8851136988, "resumen")}}}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	rcv.handleUpdate(context.Background(), f.updates[0])

	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "2 pedidos nuevos") {
		t.Fatalf("resumen must reply with the digest; got %v", got)
	}
}

func TestReceiver_EstadoCensus(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "estado")})
	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "Estado") {
		t.Fatalf("estado must reply with the census; got %v", got)
	}
}

func TestReceiver_UnknownCommandShowsHelp(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "hazme un cafe")})
	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "Comandos") {
		t.Fatalf("unknown command must reply with help; got %v", got)
	}
}

func TestReceiver_UnauthorizedChatIgnored(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	// A different chat id — the access-control boundary of the whole channel.
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(99999999, "resumen")})
	if got := f.replies(); len(got) != 0 {
		t.Fatalf("an unauthorized chat must get NO reply; got %v", got)
	}
}

func TestReceiver_HelpAndSlashAndBotSuffix(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	for _, cmd := range []string{"/ayuda", "/resumen@appximodev_bot", "AYUDA"} {
		rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, cmd)})
	}
	got := f.replies()
	if len(got) != 3 {
		t.Fatalf("want 3 replies, got %d", len(got))
	}
	// /resumen@bot must be parsed as resumen (the digest), the others as help.
	if !strings.Contains(got[1], "pedidos nuevos") {
		t.Errorf("/resumen@bot must be the digest; got %q", got[1])
	}
}

func TestReceiver_FailFastConfig(t *testing.T) {
	t.Setenv("APPXIMO_TELEGRAM_SUMMARY_TENANT", "acme")
	t.Setenv("APPXIMO_TELEGRAM_BOT_TOKEN", "")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "")
	t.Setenv("APPXIMO_TELEGRAM_SUMMARY_ROLE", "owner")
	if _, err := newTelegramReceiver(Config{JWTSecret: testJWTSecret}, map[string]bool{"owner": true}, nil); err == nil {
		t.Fatal("summary tenant without token/chat must fail-fast")
	}
	t.Setenv("APPXIMO_TELEGRAM_BOT_TOKEN", "123456:AAExampleExampleExampleExample01")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "8851136988")
	t.Setenv("APPXIMO_TELEGRAM_SUMMARY_ROLE", "ghost")
	if _, err := newTelegramReceiver(Config{JWTSecret: testJWTSecret}, map[string]bool{"owner": true}, nil); err == nil {
		t.Fatal("an undeclared summary role must fail-fast")
	}
	t.Setenv("APPXIMO_TELEGRAM_SUMMARY_ROLE", "owner")
	t.Setenv("APPXIMO_TELEGRAM_CHAT_ID", "@achannel")
	if _, err := newTelegramReceiver(Config{JWTSecret: testJWTSecret}, map[string]bool{"owner": true}, nil); err == nil {
		t.Fatal("a @channel chat id cannot be a command source — must fail-fast")
	}
	// Not requested at all → (nil, nil), boots clean.
	t.Setenv("APPXIMO_TELEGRAM_SUMMARY_TENANT", "")
	rcv, err := newTelegramReceiver(Config{JWTSecret: testJWTSecret}, nil, nil)
	if err != nil || rcv != nil {
		t.Fatalf("no summary tenant → (nil,nil); got rcv=%v err=%v", rcv, err)
	}
}

func msg(chatID int64, text string) *struct {
	MessageID int64 `json:"message_id"`
	From      *struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	} `json:"from"`
	Chat *struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text string `json:"text"`
} {
	m := &struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	}{Text: text}
	m.Chat = &struct {
		ID int64 `json:"id"`
	}{ID: chatID}
	return m
}

var _ = time.Second
