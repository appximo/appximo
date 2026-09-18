package appximo

// Telegram INPUT channel (VOZ-ESCALON1-S1, step 1 of the voice plan A-70):
// the same bot that DELIVERS alerts also RECEIVES a small set of commands —
// `resumen`, `estado`, `ayuda` — and replies with the owner-language digest
// composed by GET /api/summary. Read-only: no command in step 1 writes
// anything (the escalation to writes is step 2, with its own confirmation
// discipline).
//
// Transport is getUpdates long-poll (the DECISION, with its argument, in
// docs/PRODUCTION.md §4.6d): one idle HTTPS long-poll returns sub-second on a
// new message, adds ZERO inbound attack surface, needs no public URL or
// setWebhook moving part, and works behind any NAT — the webhook's only edge
// (no held connection) matters at high volume / many bots, not one small box.
//
// Access control (the whole channel's security): ONLY the configured chat id
// may command. A message from any other chat is ignored and logged — never
// answered. Everything runs off the request hot path in its own goroutine.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	zlog "github.com/rs/zerolog/log"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/telegram"
)

// telegramReceiver polls for commands and answers with the summary. It is built
// at boot (fail-fast on half-configuration) and run in startBackground.
type telegramReceiver struct {
	client      *telegram.Client
	authChatID  int64  // the ONLY chat allowed to command
	tenant      string // which tenant the digest covers
	role        string // the RBAC role the digest is computed as
	jwtSecret   string
	hostSuffix  string
	getRouter   func() http.Handler // the live data-plane router (survives hot-swap)
	pollTimeout int
	offset      int64
}

// newTelegramReceiver builds the input channel from the environment, or returns
// (nil, nil) when it is not configured. The channel is ENABLED by setting
// APPXIMO_TELEGRAM_SUMMARY_TENANT; once enabled, a missing/invalid token, chat
// id or role is a BOOT ERROR (the worker-env fail-fast discipline) — a
// half-configured command channel never boots silently.
func newTelegramReceiver(cfg Config, declaredRoles map[string]bool, getRouter func() http.Handler) (*telegramReceiver, error) {
	tenantID := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_SUMMARY_TENANT"))
	if tenantID == "" {
		return nil, nil // input channel not requested
	}
	token := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_CHAT_ID"))
	role := strings.TrimSpace(os.Getenv("APPXIMO_TELEGRAM_SUMMARY_ROLE"))

	if token == "" || chatID == "" {
		return nil, fmt.Errorf("appximo: APPXIMO_TELEGRAM_SUMMARY_TENANT is set (the Telegram command channel), so APPXIMO_TELEGRAM_BOT_TOKEN and APPXIMO_TELEGRAM_CHAT_ID must be set too — the bot that answers is the same one that alerts")
	}
	client, err := telegram.New(token, chatID)
	if err != nil {
		return nil, fmt.Errorf("appximo: Telegram command channel rejected: %w", err)
	}
	// The command SOURCE must be a numeric chat (a private chat / group id) so an
	// inbound message can be matched against it; a @channel is a broadcast
	// target, not a place commands come from.
	authChatID, convErr := strconv.ParseInt(chatID, 10, 64)
	if convErr != nil {
		return nil, fmt.Errorf("appximo: APPXIMO_TELEGRAM_CHAT_ID must be a NUMERIC chat id to receive commands (a @channel can receive alerts but not send commands); got %q", chatID)
	}
	if role == "" {
		return nil, fmt.Errorf("appximo: APPXIMO_TELEGRAM_SUMMARY_ROLE is required with APPXIMO_TELEGRAM_SUMMARY_TENANT — the digest is computed AS this role, so it shows exactly what that role may read")
	}
	if declaredRoles != nil && !declaredRoles[role] {
		return nil, fmt.Errorf("appximo: APPXIMO_TELEGRAM_SUMMARY_ROLE=%q is not a role this schema declares — the digest could never be authorized; declare it in rbac.roles or pick an existing role", role)
	}

	return &telegramReceiver{
		client:      client,
		authChatID:  authChatID,
		tenant:      tenantID,
		role:        role,
		jwtSecret:   cfg.JWTSecret,
		hostSuffix:  ".svc.internal",
		getRouter:   getRouter,
		pollTimeout: 45,
	}, nil
}

// run is the long-poll loop. It stops when ctx is done. getUpdates is called
// with a context deadline slightly beyond the server-side long-poll so a lost
// connection is retried, never wedged.
func (rcv *telegramReceiver) run(ctx context.Context) {
	// Clear any webhook so getUpdates is allowed (they are mutually exclusive on
	// Telegram's side); harmless if none was set.
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_ = rcv.client.DeleteWebhook(dctx, false)
	cancel()

	zlog.Info().Str("tenant", rcv.tenant).Str("role", rcv.role).Int64("chat_id", rcv.authChatID).
		Msg("telegram command channel listening (getUpdates) — resumen/estado/ayuda")

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		pctx, pcancel := context.WithTimeout(ctx, time.Duration(rcv.pollTimeout+15)*time.Second)
		updates, err := rcv.client.GetUpdates(pctx, rcv.offset, rcv.pollTimeout)
		pcancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			zlog.Warn().Err(err).Msg("telegram getUpdates failed — retrying")
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
		for _, u := range updates {
			rcv.offset = u.UpdateID + 1 // ack past this update
			rcv.handleUpdate(ctx, u)
		}
	}
}

// handleUpdate is the access control + command dispatch for one update.
func (rcv *telegramReceiver) handleUpdate(ctx context.Context, u telegram.Update) {
	if u.Message == nil || u.Message.Chat == nil {
		return
	}
	chatID := u.Message.Chat.ID
	// ACCESS CONTROL — the security of the whole channel. Only the configured
	// chat may command; anything else is logged and dropped, never answered
	// (no reply = no oracle that the bot exists to a stranger who guessed it).
	if chatID != rcv.authChatID {
		zlog.Warn().Int64("from_chat", chatID).Str("text", firstWord(u.Message.Text)).
			Msg("telegram: ignored a command from an UNAUTHORIZED chat")
		return
	}

	cmd := parseCommand(u.Message.Text)
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	switch cmd {
	case "resumen":
		rcv.reply(sendCtx, rcv.summary(ctx, "today"))
	case "estado":
		rcv.reply(sendCtx, rcv.summary(ctx, "census"))
	case "ayuda", "start", "help":
		rcv.reply(sendCtx, helpText)
	default:
		rcv.reply(sendCtx, fmt.Sprintf("No entendí <code>%s</code>.\n\n%s", htmlEscapeMsg(firstWord(u.Message.Text)), helpText))
	}
}

const helpText = "🤖 <b>Comandos</b>\n" +
	"• <b>resumen</b> — qué pasó hoy (nuevos, actualizados, pendientes)\n" +
	"• <b>estado</b> — cuántos hay de cada cosa ahora mismo\n" +
	"• <b>ayuda</b> — esta lista\n\n" +
	"Solo lectura: escribir datos por acá llega en una próxima etapa."

// summary calls GET /api/summary in-process through the LIVE router (the real
// tenant→JWT→RBAC chain, always the current surface even after a hot-swap),
// authenticating as a freshly minted, short-lived token for the configured
// (tenant, role). view is "today" or "census".
func (rcv *telegramReceiver) summary(ctx context.Context, view string) string {
	h := rcv.getRouter()
	if h == nil {
		return "⚠️ El motor todavía no está listo; probá en unos segundos."
	}
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{
		UserID:   "telegram:summary",
		Role:     rcv.role,
		TenantID: rcv.tenant,
	}, rcv.jwtSecret, 60*time.Second)
	if err != nil {
		zlog.Error().Err(err).Msg("telegram summary: mint token")
		return "⚠️ No pude generar el resumen (token)."
	}
	path := "/api/summary"
	if view == "census" {
		path += "?view=census"
	}
	// A fresh chi RouteContext isolates this re-entrant request (the flow-test
	// runner learned this the hard way — a reused RouteContext mis-routes).
	rctx := context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req := httptest.NewRequest(http.MethodGet, "http://placeholder"+path, nil).WithContext(rctx)
	req.Host = rcv.tenant + rcv.hostSuffix
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		zlog.Warn().Int("status", rec.Code).Str("view", view).Msg("telegram summary: engine did not answer 200")
		return fmt.Sprintf("⚠️ No pude generar el resumen (el motor respondió %d).", rec.Code)
	}
	var rep struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rep.Text == "" {
		return "⚠️ El resumen llegó vacío."
	}
	return rep.Text
}

// reply sends text to the authorized chat, tolerating a transient Telegram
// failure with a short retry so an answer is not lost to one blip.
func (rcv *telegramReceiver) reply(ctx context.Context, text string) {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = rcv.client.SendMessageTo(ctx, rcv.authChatID, text); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	zlog.Error().Err(err).Msg("telegram: failed to send a reply after retries")
}

// parseCommand normalizes the first token: strips a leading '/', lowercases,
// drops a @botname suffix (Telegram appends it in groups).
func parseCommand(text string) string {
	w := firstWord(text)
	w = strings.TrimPrefix(w, "/")
	if i := strings.IndexByte(w, '@'); i >= 0 {
		w = w[:i]
	}
	return strings.ToLower(w)
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func htmlEscapeMsg(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
