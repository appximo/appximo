package observability

// Telegram alert sink (ALERTAS-TELEGRAM-S1, OPS-47).
//
// The alerter grew every signal that matters — SLO burn, first-occurrence
// errors, failed/stale backups, low disk, a stuck outbox — and all of it died
// in a journal nobody reads, because the only sink was Slack and no box had a
// webhook configured. This sink posts to the Telegram Bot API (one HTTPS POST,
// no new dependency — the same pattern rt-centinela runs in production) so an
// alert reaches a phone.
//
// The MESSAGE is rendered for a phone, in Spanish: what happened, in which
// app, and WHAT TO DO — severity visible at a glance, panel link when one is
// configured. Slack keeps its historical English one-liner untouched; each
// sink renders from the Alert's structured fields, so the two can never hold
// each other back.
//
// The bot token is a credential: it is never echoed in errors or logs (a Go
// http error normally embeds the request URL, which CONTAINS the token — every
// error path here is scrubbed through redactToken).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	zlog "github.com/rs/zerolog/log"

	"github.com/appximo/appximo/pkg/extensions"
)

// Telegram config shape rules, enforced at BOOT (fail-fast, the worker-env
// discipline): a malformed token or chat id refuses to boot naming the
// variable and the rule — never a silently dead alert channel.
var (
	telegramTokenRe = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)
	telegramChatRe  = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{5,})$`)
)

// RetryAfterError is returned on a Telegram 429 so the retry loop can honor
// the API's own pacing instead of guessing.
type RetryAfterError struct {
	After time.Duration
	Msg   string
}

func (e *RetryAfterError) Error() string { return e.Msg }

// RetryAfter reports how long Telegram asked us to wait.
func (e *RetryAfterError) RetryAfter() time.Duration { return e.After }

// TelegramAlerter posts alerts to one Telegram chat via the Bot API.
// The HTTP client is the shared SSRF-safe egress client, same as Slack.
type TelegramAlerter struct {
	token    string
	chatID   string
	appName  string // which app this engine serves — every message names it
	panelURL string // optional public origin (https://app.example.com) for panel links
	client   *http.Client
	apiBase  string // overridable in tests; default https://api.telegram.org
}

// NewTelegramAlerter validates the token/chat SHAPE and returns the sink.
// Values are never echoed back in the error (the token is a credential).
func NewTelegramAlerter(token, chatID, appName, panelURL string) (*TelegramAlerter, error) {
	if !telegramTokenRe.MatchString(token) {
		return nil, fmt.Errorf("APPXIMO_TELEGRAM_BOT_TOKEN is not a Telegram bot token (expected <digits>:<token>, as issued by @BotFather); the value is deliberately not echoed here")
	}
	if !telegramChatRe.MatchString(chatID) {
		return nil, fmt.Errorf("APPXIMO_TELEGRAM_CHAT_ID %q is not a Telegram chat id (an integer like 8851136988 or -100123456, or @channelname); get yours by messaging the bot and reading getUpdates", chatID)
	}
	return &TelegramAlerter{
		token: token, chatID: chatID, appName: appName, panelURL: strings.TrimRight(panelURL, "/"),
		client:  extensions.NewSSRFSafeClient(10 * time.Second),
		apiBase: "https://api.telegram.org",
	}, nil
}

// redactToken scrubs the bot token from any string that might reach a log or
// an error chain (http errors embed the request URL, which carries it).
func (t *TelegramAlerter) redactToken(s string) string {
	if t.token == "" {
		return s
	}
	return strings.ReplaceAll(s, t.token, "<token>")
}

// VerifyLive checks the token and chat against the live API — read-only, no
// message is sent (getMe + getChat). Called on a BACKGROUND goroutine at boot:
// a revoked token screams in the log without making the boot depend on
// api.telegram.org being reachable (a Telegram outage must never keep the app
// down after a restart). fleet-audit.sh runs the same two calls from outside.
func (t *TelegramAlerter) VerifyLive(ctx context.Context) error {
	var me struct {
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := t.call(ctx, "getMe", map[string]any{}, &me); err != nil {
		return fmt.Errorf("telegram getMe: %s", t.redactToken(err.Error()))
	}
	if err := t.call(ctx, "getChat", map[string]any{"chat_id": t.chatIDValue()}, nil); err != nil {
		return fmt.Errorf("telegram getChat (bot @%s is fine, the CHAT is not — did the chat start the bot?): %s", me.Result.Username, t.redactToken(err.Error()))
	}
	zlog.Info().Str("bot", "@"+me.Result.Username).Str("chat_id", t.chatID).Msg("telegram alert destination verified (getMe+getChat)")
	return nil
}

// chatIDValue sends numeric ids as numbers (the API accepts strings too, but
// being exact costs nothing) and @channel names as strings.
func (t *TelegramAlerter) chatIDValue() any {
	if strings.HasPrefix(t.chatID, "@") {
		return t.chatID
	}
	return json.Number(t.chatID)
}

// Send renders the Spanish phone-first message and posts it.
func (t *TelegramAlerter) Send(ctx context.Context, a Alert) error {
	payload := map[string]any{
		"chat_id":                  t.chatIDValue(),
		"text":                     telegramText(a, t.appName, t.panelURL),
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	return t.call(ctx, "sendMessage", payload, nil)
}

// call POSTs one Bot API method. A 429 comes back as *RetryAfterError carrying
// the API's own retry_after; every other non-ok is an error with the API
// description and NO token.
func (t *TelegramAlerter) call(ctx context.Context, method string, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram %s marshal: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.apiBase+"/bot"+t.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s request: %s", method, t.redactToken(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s send: %s", method, t.redactToken(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var apiResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(raw, &apiResp)
	if resp.StatusCode == http.StatusTooManyRequests {
		after := time.Duration(apiResp.Parameters.RetryAfter) * time.Second
		if after <= 0 {
			after = 5 * time.Second
		}
		return &RetryAfterError{After: after, Msg: fmt.Sprintf("telegram %s: 429 rate limited, retry_after=%s", method, after)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !apiResp.OK {
		desc := apiResp.Description
		if desc == "" {
			desc = strings.TrimSpace(string(raw))
		}
		return fmt.Errorf("telegram %s: status %d — %s", method, resp.StatusCode, t.redactToken(desc))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("telegram %s decode: %w", method, err)
		}
	}
	return nil
}

// ── the Spanish phone-first rendering ───────────────────────────────────────

// telegramText renders an Alert for a phone screen, in Spanish: a bold header
// with the severity and the app, WHAT happened in the owner's words, WHAT TO
// DO next, and the panel link when one is configured. Structured detail comes
// from a.Fields (filled by each emit site); when a kind arrives without them
// the fallback still leads in Spanish and quotes the technical message as
// detail — never a bare English line.
func telegramText(a Alert, appName, panelURL string) string {
	esc := html.EscapeString
	app := esc(appName)
	if app == "" {
		app = "appximo"
	}
	f := func(k string) string { return esc(a.Fields[k]) }
	dur := func(k string) string { return esc(humanDurationES(a.Fields[k])) }

	var b strings.Builder
	panelPath := "/admin#/observability"

	switch {
	case a.Kind == KindNewError:
		fmt.Fprintf(&b, "🆕 <b>Error nuevo · %s</b>\n", app)
		fmt.Fprintf(&b, "Apareció un error que nunca se había visto en <code>%s</code>: %s.\n", esc(a.Route), esc(a.Message))
		b.WriteString("<b>Qué hacer:</b> abrí el panel → Problemas; la traza tiene la sentencia, el usuario y el rol.\n")
		if a.TraceID != "" {
			fmt.Fprintf(&b, "Traza: <code>%s</code>\n", esc(a.TraceID))
		}
	case a.Kind == KindStorm:
		fmt.Fprintf(&b, "🌩 <b>Tormenta de errores · %s</b>\n", app)
		fmt.Fprintf(&b, "%d tipos de error NUEVOS en el último minuto — frené las alertas individuales para no inundarte (%s).\n", a.Count, esc(a.Message))
		b.WriteString("<b>Qué hacer:</b> esto suele ser un deploy roto o la base caída; mirá el panel → Problemas, y si acabás de desplegar, revertí.\n")
	case a.Kind == KindHost && a.Route == "disk":
		fmt.Fprintf(&b, "%s <b>%s · Disco bajo · %s</b>\n", levelEmoji(a.Level), levelWordES(a.Level), app)
		if a.Fields["free"] != "" {
			fmt.Fprintf(&b, "Queda poco disco en <code>%s</code>: %s libres de %s (%s %%).", f("path"), f("free"), f("total"), f("pct"))
			if a.Fields["more"] != "" {
				fmt.Fprintf(&b, " Y %s disco(s) más bajo el piso.", f("more"))
			}
			b.WriteString("\n")
		} else {
			fmt.Fprintf(&b, "Queda poco disco (%s).\n", esc(a.Message))
		}
		b.WriteString("Si llega a 0, PostgreSQL deja de escribir y la app responde 503.\n")
		b.WriteString("<b>Qué hacer:</b> liberá espacio ya — sets viejos de backup, <code>journalctl --vacuum-size=200M</code>, caché de apt.\n")
		panelPath = "/admin#/resources"
	case a.Kind == KindHost && a.Route == "backup":
		fmt.Fprintf(&b, "🔴 <b>CRÍTICA · Backup · %s</b>\n", app)
		switch a.Fields["status"] {
		case "failed":
			b.WriteString("El último backup FALLÓ — no hay copia nueva. Si la base se rompe hoy, se restaura la de anoche.\n")
			b.WriteString("<b>Qué hacer:</b> en el server: <code>journalctl -u '*-backup' -n 40</code> dice por qué; corré uno a mano cuando lo arregles.\n")
		case "none":
			fmt.Fprintf(&b, "NUNCA corrió un backup y la app lleva %s prendida.\n", dur("uptime_s"))
			b.WriteString("<b>Qué hacer:</b> ¿está el timer instalado? <code>systemctl list-timers '*backup*'</code>\n")
		case "unknown":
			b.WriteString("El último backup dejó un estado VACÍO o ilegible — típico de disco lleno: ni el resultado pudo escribir.\n")
			b.WriteString("<b>Qué hacer:</b> <code>df -h</code> y <code>journalctl -u '*-backup' -n 40</code>.\n")
		default: // stale
			fmt.Fprintf(&b, "El último backup bueno tiene %s (el piso es %s) — el timer no está corriendo.\n", dur("age_s"), dur("floor_s"))
			b.WriteString("<b>Qué hacer:</b> <code>systemctl list-timers '*backup*'</code>; corré uno ya: <code>/opt/&lt;app&gt;/scripts/backup.sh</code>\n")
		}
		panelPath = "/admin#/resources"
	case a.Kind == "outbox_stale_pending":
		fmt.Fprintf(&b, "🟡 <b>AVISO · Cola varada · %s</b>\n", app)
		fmt.Fprintf(&b, "Hay trabajo encolado que NADIE está drenando: el evento más viejo lleva %s esperando (tema <code>%s</code>, %s pendientes).\n", dur("age_s"), f("topic"), f("pending"))
		b.WriteString("<b>Qué hacer:</b> ¿corre el worker? <code>systemctl status &lt;app&gt;-worker</code>; el detalle por tema está en /admin/outbox.\n")
		panelPath = "/admin/outbox"
	case a.Kind == "outbox_failed":
		fmt.Fprintf(&b, "🟡 <b>AVISO · Eventos fallados · %s</b>\n", app)
		fmt.Fprintf(&b, "%s evento(s) agotaron sus reintentos y quedaron parados en <code>failed</code> — el porqué exacto está guardado en cada fila.\n", f("failed"))
		b.WriteString("<b>Qué hacer:</b> /admin/outbox lista cada uno con su <code>last_error</code>; arreglá la causa y re-armalo (la receta está en el manual, §cola).\n")
		panelPath = "/admin/outbox"
	case a.Kind == "workflow_overdue":
		fmt.Fprintf(&b, "🟡 <b>AVISO · Workflow vencido · %s</b>\n", app)
		fmt.Fprintf(&b, "Un workflow programado lleva %s vencido — ningún scheduler está disparando.\n", dur("overdue_s"))
		b.WriteString("<b>Qué hacer:</b> ¿corre el worker? <code>systemctl status &lt;app&gt;-worker</code>; las corridas están en /admin/workflows.\n")
		panelPath = "/admin/workflows"
	case a.Kind == "workflow_failed":
		fmt.Fprintf(&b, "🟡 <b>AVISO · Workflows fallando · %s</b>\n", app)
		fmt.Fprintf(&b, "%s corrida(s) de workflow fallaron en las últimas 24 h.\n", f("failed_24h"))
		b.WriteString("<b>Qué hacer:</b> /admin/workflows tiene el error y el detalle por paso de cada corrida.\n")
		panelPath = "/admin/workflows"
	case a.Kind == KindSLO || a.BurnRate > 0:
		fmt.Fprintf(&b, "%s <b>%s · La app está sufriendo · %s</b>\n", levelEmoji(a.Level), levelWordES(a.Level), app)
		fmt.Fprintf(&b, "Errores o lentitud por encima de lo prometido: quemando el presupuesto a %.1f× (p95 %.0f ms).\n", a.BurnRate, a.P95ms)
		b.WriteString("<b>Qué hacer:</b> panel → Observabilidad dice si es la base, el disco o un error nuevo; si acabás de desplegar, revertí.\n")
	default:
		// A kind this renderer doesn't know yet: still lead in Spanish.
		fmt.Fprintf(&b, "%s <b>%s · %s</b>\n", levelEmoji(a.Level), levelWordES(a.Level), app)
		fmt.Fprintf(&b, "Detalle técnico: %s\n", esc(a.Message))
		b.WriteString("<b>Qué hacer:</b> mirá el journal del server y el panel /admin.\n")
	}

	if a.TenantID != "" && !strings.EqualFold(a.TenantID, appName) {
		fmt.Fprintf(&b, "Tenant: <code>%s</code>\n", esc(a.TenantID))
	}
	if panelURL != "" {
		fmt.Fprintf(&b, "<a href=\"%s%s\">Ver el panel</a>", panelURL, panelPath)
	}
	return strings.TrimRight(b.String(), "\n")
}

// humanDurationES renders a seconds field for a phone, in Spanish. An
// unparseable value comes back verbatim (never a panic over a message).
func humanDurationES(secsField string) string {
	secs, err := strconv.ParseFloat(secsField, 64)
	if err != nil || secs < 0 {
		return secsField
	}
	d := time.Duration(secs * float64(time.Second))
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f días", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0f min", d.Minutes())
	default:
		return fmt.Sprintf("%.0f s", d.Seconds())
	}
}

func levelEmoji(level string) string {
	if level == LevelCritical {
		return "🔴"
	}
	return "🟡"
}

func levelWordES(level string) string {
	if level == LevelCritical {
		return "CRÍTICA"
	}
	return "AVISO"
}
