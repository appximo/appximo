package appximo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	photos   []fakePhoto // every sendPhoto: caption + png size
	// rejectPhoto makes sendPhoto answer this HTTP status with ok:false
	// (400 = refused for good, 500/429 = retryable).
	rejectPhoto int
	typing      int // sendChatAction calls
}

func (f *fakeTG) typingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typing
}

type fakePhoto struct {
	caption string
	size    int
	chat    int64
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
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Errorf("sendPhoto must be multipart: %v", err)
			}
			file, _, ferr := r.FormFile("photo")
			size := 0
			if ferr == nil {
				b, _ := io.ReadAll(file)
				size = len(b)
				file.Close()
			}
			f.mu.Lock()
			if f.rejectPhoto != 0 {
				f.mu.Unlock()
				w.WriteHeader(f.rejectPhoto)
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Bad Request: IMAGE_PROCESS_FAILED"}) //nolint:errcheck
				return
			}
			chat, _ := strconv.ParseInt(r.FormValue("chat_id"), 10, 64)
			f.photos = append(f.photos, fakePhoto{caption: r.FormValue("caption"), size: size, chat: chat})
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 2}}) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/sendChatAction"):
			f.mu.Lock()
			f.typing++
			f.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true}) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/deleteWebhook"):
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true}) //nolint:errcheck
		default:
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": true}) //nolint:errcheck
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTG) sentPhotos() []fakePhoto {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakePhoto, len(f.photos))
	copy(out, f.photos)
	return out
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
		if r.URL.Query().Get("format") == "png" {
			w.Header().Set("Content-Type", "image/png")
			w.Write(tinyPNG()) //nolint:errcheck
			return
		}
		text := "📋 Resumen — 2 pedidos nuevos"
		if r.URL.Query().Get("view") == "census" {
			text = "📊 Estado — pedidos: 7"
		}
		json.NewEncoder(w).Encode(map[string]any{"text": text}) //nolint:errcheck
	})
}

// tinyPNG is a real 1×1 PNG (the receiver only forwards bytes; Telegram is faked).
func tinyPNG() []byte {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))) //nolint:errcheck
	return buf.Bytes()
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

	// VOZ-VISUAL-S1: resumen is PICTURE + TEXT — one sendPhoto whose caption is
	// the digest, no separate text message when it fits.
	photos := f.sentPhotos()
	if len(photos) != 1 || !strings.Contains(photos[0].caption, "2 pedidos nuevos") || photos[0].size == 0 || photos[0].chat != 8851136988 {
		t.Fatalf("resumen must send the digest as a photo with the text as caption; got %+v", photos)
	}
	if got := f.replies(); len(got) != 0 {
		t.Fatalf("a digest that fits the caption must not also send a text message; got %v", got)
	}
}

// The words are the contract: when Telegram refuses the photo for good (400),
// the text still arrives as a message.
func TestReceiver_ResumenPhotoRejectedFallsBackToText(t *testing.T) {
	f := &fakeTG{rejectPhoto: http.StatusBadRequest}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubSummaryRouter(t, "acme", "owner"))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "resumen")})
	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "2 pedidos nuevos") {
		t.Fatalf("a refused photo must fall back to the text; got %v", got)
	}
}

// An engine that cannot render (older binary, 404/500 on ?format=png) still
// answers: text only, never silence.
func TestReceiver_ResumenWithoutImageSendsText(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	router := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "png" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"text": "📋 Resumen viejo"}) //nolint:errcheck
	})
	rcv := newTestReceiver(t, srv, router)
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "resumen")})
	if got := f.replies(); len(got) != 1 || !strings.Contains(got[0], "Resumen viejo") {
		t.Fatalf("no image → text reply; got %v", got)
	}
	if len(f.sentPhotos()) != 0 {
		t.Fatal("no photo must be sent when the engine has none")
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

// VOZ-PREGUNTAS-S1: a word that is not a fixed command is a QUESTION. When the
// engine has no model key, /api/ask answers 503 ask_disabled and the bot says
// so + the help — the fixed commands are untouched.
func TestReceiver_UnknownWordIsAQuestion_DisabledShowsHelp(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubAskRouter(t, "acme", "owner", askReply{status: 503, body: map[string]any{"kind": "disabled", "text": "Las preguntas libres no están activadas (falta ANTHROPIC_API_KEY)."}}))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "hazme un cafe")})
	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "Comandos") || !strings.Contains(got[0], "ANTHROPIC_API_KEY") {
		t.Fatalf("disabled question path must reply with the reason + help; got %v", got)
	}
}

func TestReceiver_QuestionAnsweredAsTheConfiguredRole(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubAskRouter(t, "acme", "owner", askReply{status: 200, body: map[string]any{"kind": "answer", "text": "<b>7</b> citas hoy"}}))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "cuántas citas tengo hoy")})
	got := f.replies()
	if len(got) != 1 || got[0] != "<b>7</b> citas hoy" {
		t.Fatalf("question must relay the engine's text; got %v", got)
	}
	if f.typingCount() == 0 {
		t.Fatalf("the typing indicator must be sent while the engine thinks")
	}
}

func TestReceiver_QuestionWithImageSendsPhoto(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	png := base64.StdEncoding.EncodeToString(tinyPNG())
	rcv := newTestReceiver(t, srv, stubAskRouter(t, "acme", "owner", askReply{status: 200, body: map[string]any{"kind": "answer", "text": "<b>3</b> citas por estado", "png": png}}))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "citas por estado")})
	if ph := f.sentPhotos(); len(ph) != 1 || ph[0].caption != "<b>3</b> citas por estado" {
		t.Fatalf("a grouped answer must go as photo + caption; got %v", ph)
	}
}

func TestReceiver_QuestionModelDownDegradesToFixedCommands(t *testing.T) {
	f := &fakeTG{}
	srv := f.server(t)
	rcv := newTestReceiver(t, srv, stubAskRouter(t, "acme", "owner", askReply{status: 200, body: map[string]any{"kind": "unavailable", "text": "⚠️ No pude pensar la pregunta ahora. Los comandos fijos siguen: resumen, estado, ayuda."}}))
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "cuántas citas hay")})
	got := f.replies()
	if len(got) != 1 || !strings.Contains(got[0], "resumen") {
		t.Fatalf("model down must degrade, not break; got %v", got)
	}
	// And the fixed commands still work against the same router.
	rcv.handleUpdate(context.Background(), telegram.Update{Message: msg(8851136988, "estado")})
	if got := f.replies(); len(got) != 2 || !strings.Contains(got[1], "Estado") {
		t.Fatalf("fixed commands must survive; got %v", got)
	}
}

type askReply struct {
	status int
	body   map[string]any
}

// stubAskRouter is stubSummaryRouter plus a scripted POST /api/ask that demands
// the same (tenant, role) token and echoes the question it received.
func stubAskRouter(t *testing.T, wantTenant, wantRole string, reply askReply) http.Handler {
	summaryH := stubSummaryRouter(t, wantTenant, wantRole)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ask" {
			summaryH.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		authz := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, err := auth.ValidateToken(authz, testJWTSecret)
		if err != nil || claims.Role != wantRole || claims.TenantID != wantTenant {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var in struct {
			Q string `json:"q"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Q == "" {
			t.Errorf("the question must travel as {q}: %v", err)
		}
		w.WriteHeader(reply.status)
		json.NewEncoder(w).Encode(reply.body) //nolint:errcheck
	})
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
	if len(got) != 2 {
		t.Fatalf("want 2 text replies (the two help requests), got %d: %v", len(got), got)
	}
	// /resumen@bot must be parsed as resumen (the digest → a photo), the others as help.
	if ph := f.sentPhotos(); len(ph) != 1 || !strings.Contains(ph[0].caption, "pedidos nuevos") {
		t.Errorf("/resumen@bot must be the digest photo; got %+v", ph)
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
