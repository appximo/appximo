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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	sendCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	switch cmd {
	case "resumen":
		// PICTURE + TEXT (VOZ-VISUAL-S1): the digest is read at a glance as an
		// image; the same text rides as the caption (or a second message when
		// it does not fit) so nothing is ever image-only.
		text := rcv.summary(ctx, "today")
		rcv.replyWithImage(sendCtx, rcv.summaryPNG(ctx), text)
	case "estado":
		rcv.reply(sendCtx, rcv.summary(ctx, "census"))
	case "gasto":
		// What the questions cost (VOZ-TRAZABILIDAD-S1): today, the month, the
		// cap, who answered how many, the phrases that cost the most — as the
		// same census card the digest uses, text beneath. Authorized by the
		// engine for admin-grade roles only (GET /api/ask/spend).
		text, png := rcv.spend(ctx)
		rcv.replyWithImage(sendCtx, png, text)
	case "ayuda", "start", "help":
		rcv.reply(sendCtx, helpText)
	default:
		// Anything that is not a fixed command is a QUESTION (VOZ-PREGUNTAS-S1,
		// ADR-033): the engine's /api/ask translates it into a validated read
		// plan and answers with its own numbers. When questions are not
		// enabled on this app (no model key) the endpoint says so and the
		// help follows — the three fixed commands are never broken by this.
		rcv.question(ctx, sendCtx, u.Message.Text)
	}
}

const helpText = "🤖 <b>Comandos</b>\n" +
	"• <b>resumen</b> — qué pasó hoy (nuevos, actualizados, pendientes)\n" +
	"• <b>estado</b> — cuántos hay de cada cosa ahora mismo\n" +
	"• <b>gasto</b> — cuánto van costando las preguntas (hoy, el mes, el techo, quién las resolvió)\n" +
	"• <b>ayuda</b> — esta lista\n" +
	"• o <b>preguntá</b> con tus palabras: «cuántas órdenes hay hoy», «qué pedidos están sin pagar», «cuánto vendimos esta semana»\n\n" +
	"Solo lectura: escribir datos por acá llega en una próxima etapa."

// question sends the free text to POST /api/ask as the configured role and
// relays the engine's reply. While the engine thinks (a model call, 2–4 s
// expected, bounded at ~20 s) the chat shows Telegram's "typing…" indicator,
// re-sent every 4 s, so the owner is never looking at nothing. A model
// failure degrades to the fixed commands (the endpoint's own text says so);
// an engine not yet up, or a disabled question path, is said in words.
func (rcv *telegramReceiver) question(ctx, sendCtx context.Context, text string) {
	// First "typing…" synchronously (the owner sees it before the model is
	// even called), then kept alive every 4 s until the answer is in.
	actx, acancel := context.WithTimeout(ctx, 5*time.Second)
	_ = rcv.client.SendChatAction(actx, rcv.authChatID, "typing")
	acancel()
	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go rcv.typing(typingCtx)

	body, _ := json.Marshal(map[string]string{"q": text})
	rec := rcv.selfPost(ctx, "/api/ask", body)
	stopTyping()
	if rec == nil {
		rcv.reply(sendCtx, "⚠️ El motor todavía no está listo; probá en unos segundos.")
		return
	}
	var rep struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
		PNG  string `json:"png"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	switch {
	case rec.Code == http.StatusServiceUnavailable && rep.Kind == "disabled":
		rcv.reply(sendCtx, "ℹ️ "+htmlEscapeMsg(rep.Text)+"\n\n"+helpText)
	case rec.Code == http.StatusTooManyRequests:
		rcv.reply(sendCtx, "⏳ Demasiadas preguntas en este minuto. Esperá un momento y volvé a preguntar.")
	case rec.Code != http.StatusOK || rep.Text == "":
		zlog.Warn().Int("status", rec.Code).Msg("telegram question: engine did not answer 200")
		rcv.reply(sendCtx, fmt.Sprintf("⚠️ No pude responder la pregunta (el motor respondió %d). Los comandos fijos siguen: resumen, estado, ayuda.", rec.Code))
	default:
		if rep.PNG != "" {
			if png, err := base64.StdEncoding.DecodeString(rep.PNG); err == nil {
				rcv.replyWithImage(sendCtx, png, rep.Text)
				return
			}
		}
		rcv.reply(sendCtx, rep.Text)
	}
}

// typing keeps the "typing…" indicator alive until ctx is cancelled.
func (rcv *telegramReceiver) typing(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(4 * time.Second):
		}
		actx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = rcv.client.SendChatAction(actx, rcv.authChatID, "typing")
		cancel()
	}
}

// selfPost is selfCall for a JSON POST (the question door).
func (rcv *telegramReceiver) selfPost(ctx context.Context, path string, body []byte) *httptest.ResponseRecorder {
	h := rcv.getRouter()
	if h == nil {
		return nil
	}
	rec := httptest.NewRecorder()
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{
		UserID:   "telegram:summary",
		Role:     rcv.role,
		TenantID: rcv.tenant,
	}, rcv.jwtSecret, 60*time.Second)
	if err != nil {
		zlog.Error().Err(err).Msg("telegram question: mint token")
		rec.WriteHeader(http.StatusInternalServerError)
		return rec
	}
	rctx := context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req := httptest.NewRequest(http.MethodPost, "http://placeholder"+path, bytes.NewReader(body)).WithContext(rctx)
	req.Host = rcv.tenant + rcv.hostSuffix
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

// spend fetches GET /api/ask/spend (text) and its ?format=png (picture). A
// role the engine does not consider admin-grade gets a plain refusal; an
// engine not yet up its own sentence.
func (rcv *telegramReceiver) spend(ctx context.Context) (string, []byte) {
	rec := rcv.selfCall(ctx, "/api/ask/spend")
	if rec == nil {
		return "⚠️ El motor todavía no está listo; probá en unos segundos.", nil
	}
	var rep struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	switch {
	case rec.Code == http.StatusForbidden:
		return "🔒 Tu rol no puede ver el gasto de la plataforma.", nil
	case rec.Code != http.StatusOK || rep.Text == "":
		zlog.Warn().Int("status", rec.Code).Msg("telegram gasto: engine did not answer 200")
		return fmt.Sprintf("⚠️ No pude leer el gasto (el motor respondió %d).", rec.Code), nil
	}
	var png []byte
	if prec := rcv.selfCall(ctx, "/api/ask/spend?format=png"); prec != nil && prec.Code == http.StatusOK && strings.HasPrefix(prec.Header().Get("Content-Type"), "image/png") {
		png = prec.Body.Bytes()
	}
	return rep.Text, png
}

// summaryPNG fetches GET /api/summary?format=png the same way summary fetches
// the text. nil on any failure — the caller then sends text only (the image
// is an enhancement; the words are the contract).
func (rcv *telegramReceiver) summaryPNG(ctx context.Context) []byte {
	rec := rcv.selfCall(ctx, "/api/summary?format=png")
	if rec == nil || rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/png") {
		if rec != nil {
			zlog.Warn().Int("status", rec.Code).Msg("telegram summary: image not rendered — sending text only")
		}
		return nil
	}
	return rec.Body.Bytes()
}

// summary calls GET /api/summary in-process through the LIVE router (the real
// tenant→JWT→RBAC chain, always the current surface even after a hot-swap),
// authenticating as a freshly minted, short-lived token for the configured
// (tenant, role). view is "today" or "census".
func (rcv *telegramReceiver) summary(ctx context.Context, view string) string {
	path := "/api/summary"
	if view == "census" {
		path += "?view=census"
	}
	rec := rcv.selfCall(ctx, path)
	if rec == nil {
		return "⚠️ El motor todavía no está listo; probá en unos segundos."
	}
	if rec.Code == http.StatusInternalServerError && rec.Header().Get("X-Self-Call") == "token" {
		return "⚠️ No pude generar el resumen (token)."
	}

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

// selfCall performs one GET against the LIVE router as the configured
// (tenant, role) with a freshly minted short-lived token. nil when the router
// is not up yet; a token-mint failure is reported as a 500 tagged X-Self-Call.
func (rcv *telegramReceiver) selfCall(ctx context.Context, path string) *httptest.ResponseRecorder {
	h := rcv.getRouter()
	if h == nil {
		return nil
	}
	rec := httptest.NewRecorder()
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{
		UserID:   "telegram:summary",
		Role:     rcv.role,
		TenantID: rcv.tenant,
	}, rcv.jwtSecret, 60*time.Second)
	if err != nil {
		zlog.Error().Err(err).Msg("telegram summary: mint token")
		rec.Header().Set("X-Self-Call", "token")
		rec.WriteHeader(http.StatusInternalServerError)
		return rec
	}
	// A fresh chi RouteContext isolates this re-entrant request (the flow-test
	// runner learned this the hard way — a reused RouteContext mis-routes).
	rctx := context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req := httptest.NewRequest(http.MethodGet, "http://placeholder"+path, nil).WithContext(rctx)
	req.Host = rcv.tenant + rcv.hostSuffix
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Cache-Control", "no-cache") // the digest is never served stale to the owner
	h.ServeHTTP(rec, req)
	return rec
}

// replyWithImage sends picture + text (text alone when png is nil), with the
// same short retry as reply. A photo the API refuses for good falls back to
// the text inside the client (PhotoFallbackError) — logged, never lost.
func (rcv *telegramReceiver) replyWithImage(ctx context.Context, png []byte, text string) {
	if len(png) == 0 {
		rcv.reply(ctx, text)
		return
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = rcv.client.SendPhotoWithText(ctx, rcv.authChatID, png, text)
		var fb *telegram.PhotoFallbackError
		if errors.As(err, &fb) {
			zlog.Warn().Err(fb.Cause).Msg("telegram: photo rejected by the API — text delivered instead")
			return
		}
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	zlog.Error().Err(err).Msg("telegram: failed to send the digest image after retries — sending text")
	rcv.reply(ctx, text)
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
