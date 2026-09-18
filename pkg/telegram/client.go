// Package telegram is the low-level Telegram Bot API client shared by every
// Telegram path in the engine (VOZ-ESCALON1-S1): the alert sink
// (pkg/observability), the inbound command receiver (the engine's background
// listener), and the scheduled-summary consumer (pkg/consumers). One package
// means ONE token-handling path — the token is scrubbed from every error and
// never logged, and the config-shape rules live in one place.
//
// It is deliberately dependency-light (net/http + the shared SSRF-safe egress
// client) so pkg/consumers and the worker can import it without dragging in the
// observability stack.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/appximo/appximo/pkg/extensions"
)

// Config shape rules, enforced where a Client is built (fail-fast, the
// worker-env discipline): a malformed token or chat id never becomes a
// silently dead channel.
var (
	tokenRe = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]{20,}$`)
	chatRe  = regexp.MustCompile(`^(-?[0-9]+|@[A-Za-z0-9_]{5,})$`)
)

// ValidToken reports whether s has the shape @BotFather issues (<digits>:<token>).
func ValidToken(s string) bool { return tokenRe.MatchString(s) }

// ValidChatID reports whether s is an integer id, a -100… group id, or @channel.
func ValidChatID(s string) bool { return chatRe.MatchString(s) }

// RetryAfterError is returned on a 429 so a retry loop can honor the API's own
// pacing instead of guessing. The async alert wrapper matches it structurally
// (an interface with RetryAfter()), so its concrete home here changes nothing.
type RetryAfterError struct {
	After time.Duration
	Msg   string
}

func (e *RetryAfterError) Error() string             { return e.Msg }
func (e *RetryAfterError) RetryAfter() time.Duration { return e.After }

// Client talks to one bot + one chat. The HTTP client is the shared SSRF-safe
// egress client (blocks loopback/private/link-local, refuses redirects), the
// same as every other outbound call the engine makes.
type Client struct {
	token  string
	chatID string
	http   *http.Client
	// poll is a SEPARATE client for getUpdates: a long-poll holds the connection
	// up to the server-side timeout (tens of seconds) before headers arrive, so
	// it needs a far longer ResponseHeaderTimeout/Timeout than a send. Using the
	// 15s send client here made every idle poll time out and retry (VOZ-ESCALON1
	// field fix). Per-call context still bounds it.
	poll    *http.Client
	apiBase string // overridable in tests; default https://api.telegram.org
}

// pollClientTimeout must exceed the largest getUpdates long-poll (the receiver
// uses 45s) with margin; the per-request context is the tighter real bound.
const pollClientTimeout = 70 * time.Second

// New validates the token/chat SHAPE and returns a Client. The values are never
// echoed in the error (the token is a credential).
func New(token, chatID string) (*Client, error) {
	if !ValidToken(token) {
		return nil, fmt.Errorf("APPXIMO_TELEGRAM_BOT_TOKEN is not a Telegram bot token (expected <digits>:<token>, as issued by @BotFather); the value is deliberately not echoed here")
	}
	if !ValidChatID(chatID) {
		return nil, fmt.Errorf("APPXIMO_TELEGRAM_CHAT_ID %q is not a Telegram chat id (an integer like 8851136988 or -100123456, or @channelname)", chatID)
	}
	return &Client{
		token:   token,
		chatID:  chatID,
		http:    extensions.NewSSRFSafeClient(15 * time.Second),
		poll:    extensions.NewSSRFSafeClient(pollClientTimeout),
		apiBase: "https://api.telegram.org",
	}, nil
}

// ChatID is the configured chat (the receiver compares an inbound chat against it).
func (c *Client) ChatID() string { return c.chatID }

// SetHTTPClient / SetAPIBase are test seams (127.0.0.1 fake API is rejected by
// the production SSRF client).
func (c *Client) SetHTTPClient(h *http.Client) { c.http = h; c.poll = h }
func (c *Client) SetAPIBase(b string)          { c.apiBase = b }

// Redact scrubs the bot token from any string that might reach a log or an
// error chain (a Go http error embeds the request URL, which carries it).
func (c *Client) Redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<token>")
}

// ChatIDValue is the configured chat as the API wants it: numeric ids as JSON
// numbers, @channel names as strings (for callers that address the
// configured chat through the explicit-chat methods).
func (c *Client) ChatIDValue() any { return c.chatIDValue() }

// chatIDValue sends numeric ids as JSON numbers and @channel names as strings.
func (c *Client) chatIDValue() any {
	if strings.HasPrefix(c.chatID, "@") {
		return c.chatID
	}
	return json.Number(c.chatID)
}

// SendMessage posts one HTML message to the configured chat.
func (c *Client) SendMessage(ctx context.Context, html string) error {
	return c.SendMessageTo(ctx, c.chatIDValue(), html)
}

// SendMessageTo posts one HTML message to an explicit chat (the receiver replies
// to whoever sent a command, which for an authorized message is the same chat).
func (c *Client) SendMessageTo(ctx context.Context, chatID any, html string) error {
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}, nil)
}

// GetMe / GetChat are the read-only liveness checks (no message sent).
func (c *Client) GetMe(ctx context.Context) (username string, err error) {
	var out struct {
		Result struct {
			Username string `json:"username"`
		} `json:"result"`
	}
	if err := c.call(ctx, "getMe", map[string]any{}, &out); err != nil {
		return "", err
	}
	return out.Result.Username, nil
}

func (c *Client) GetChat(ctx context.Context) error {
	return c.call(ctx, "getChat", map[string]any{"chat_id": c.chatIDValue()}, nil)
}

// Update is one inbound update (only the fields the receiver needs).
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Chat *struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// GetUpdates long-polls for updates after offset. timeout is the server-side
// long-poll seconds (0 = no wait). The context deadline should exceed timeout.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	var out struct {
		OK     bool     `json:"ok"`
		Result []Update `json:"result"`
	}
	payload := map[string]any{"timeout": timeout, "allowed_updates": []string{"message"}}
	if offset > 0 {
		payload["offset"] = offset
	}
	if err := c.callWith(ctx, c.poll, "getUpdates", payload, &out); err != nil {
		return nil, err
	}
	return out.Result, nil
}

// SetWebhook registers url as the update destination, with secret echoed by
// Telegram in X-Telegram-Bot-Api-Secret-Token on every delivery (the receiver's
// access control). drop=true discards any updates queued while polling.
func (c *Client) SetWebhook(ctx context.Context, url, secret string, drop bool) error {
	return c.call(ctx, "setWebhook", map[string]any{
		"url":                  url,
		"secret_token":         secret,
		"allowed_updates":      []string{"message"},
		"drop_pending_updates": drop,
	}, nil)
}

// DeleteWebhook removes any registered webhook (so getUpdates can be used).
func (c *Client) DeleteWebhook(ctx context.Context, drop bool) error {
	return c.call(ctx, "deleteWebhook", map[string]any{"drop_pending_updates": drop}, nil)
}

// call POSTs one Bot API method. A 429 comes back as *RetryAfterError carrying
// the API's own retry_after; every other non-ok is an error with the API
// description and NO token.
func (c *Client) call(ctx context.Context, method string, payload map[string]any, out any) error {
	return c.callWith(ctx, c.http, method, payload, out)
}

// callWith is call over an explicit client (getUpdates needs the long-poll one).
func (c *Client) callWith(ctx context.Context, hc *http.Client, method string, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("telegram %s marshal: %w", method, err)
	}
	return c.callRaw(ctx, hc, method, "application/json", body, out)
}

// callRaw POSTs an already-encoded body (JSON or multipart) and decodes the
// Bot API envelope: 429 → *RetryAfterError, other non-ok → *APIError.
func (c *Client) callRaw(ctx context.Context, hc *http.Client, method, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram %s request: %s", method, c.Redact(err.Error()))
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("telegram %s send: %s", method, c.Redact(err.Error()))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
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
		return &APIError{Method: method, Status: resp.StatusCode, Description: c.Redact(desc)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("telegram %s decode: %w", method, err)
		}
	}
	return nil
}

// Telegram caps a photo caption at 1024 characters (UTF-16 units — we count
// runes, which is never more permissive for the BMP text the digest uses).
const captionMax = 1024

// SendPhoto posts one PNG (multipart/form-data, field "photo") with an HTML
// caption to an explicit chat. The caption must fit captionMax; callers that
// may exceed it use SendPhotoWithText.
func (c *Client) SendPhoto(ctx context.Context, chatID any, png []byte, captionHTML string) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	chat := fmt.Sprint(chatID)
	if n, ok := chatID.(json.Number); ok {
		chat = n.String()
	}
	_ = mw.WriteField("chat_id", chat)
	if captionHTML != "" {
		_ = mw.WriteField("caption", captionHTML)
		_ = mw.WriteField("parse_mode", "HTML")
	}
	part, err := mw.CreateFormFile("photo", "resumen.png")
	if err != nil {
		return fmt.Errorf("telegram sendPhoto multipart: %w", err)
	}
	if _, err := part.Write(png); err != nil {
		return fmt.Errorf("telegram sendPhoto multipart: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("telegram sendPhoto multipart: %w", err)
	}
	return c.callRaw(ctx, c.http, "sendPhoto", mw.FormDataContentType(), body.Bytes(), nil)
}

// SendPhotoWithText delivers a digest as PICTURE + TEXT, never picture alone
// (the text is the accessibility fallback and the record when the image does
// not load). When the HTML fits Telegram's caption limit it rides as the
// caption (one message); otherwise the photo goes with a short caption (the
// first line) and the full text follows as a second message.
//
// Failure semantics for the caller's retry loop: a transport error or a 429
// from the PHOTO comes back unchanged (retry the whole thing — nothing was
// delivered); a non-retryable API rejection of the photo (a 400 — e.g. an
// image Telegram will not accept) falls back to sending the TEXT so the
// content still arrives, and the returned error is nil with the fallback
// logged by the caller through PhotoFallbackError.
func (c *Client) SendPhotoWithText(ctx context.Context, chatID any, png []byte, html string) error {
	caption := html
	var tail string
	if len([]rune(html)) > captionMax {
		first := html
		if i := strings.IndexByte(html, '\n'); i >= 0 {
			first = html[:i]
		}
		if len([]rune(first)) > captionMax-64 {
			first = string([]rune(first)[:captionMax-64])
		}
		caption = first + "\n(el detalle completo va abajo)"
		tail = html
	}
	if err := c.SendPhoto(ctx, chatID, png, caption); err != nil {
		if IsRetryable(err) {
			return err
		}
		// The photo was refused for good; deliver the words.
		if terr := c.SendMessageTo(ctx, chatID, html); terr != nil {
			return terr
		}
		return &PhotoFallbackError{Cause: err}
	}
	if tail != "" {
		return c.SendMessageTo(ctx, chatID, tail)
	}
	return nil
}

// PhotoFallbackError reports that the TEXT was delivered but the photo was
// rejected by the API (non-retryable). Callers treat it as success-with-a-
// warning: the content reached the phone.
type PhotoFallbackError struct{ Cause error }

func (e *PhotoFallbackError) Error() string {
	return "telegram: photo rejected, text delivered instead: " + e.Cause.Error()
}
func (e *PhotoFallbackError) Unwrap() error { return e.Cause }

// IsRetryable classifies a client error: transport failures and 429s are
// retryable (nothing was delivered, the API may accept it next time); an API
// rejection with a 4xx status is not.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ra *RetryAfterError
	if errors.As(err, &ra) {
		return true
	}
	var api *APIError
	if errors.As(err, &api) {
		return api.Status >= 500
	}
	return true // transport / context errors
}

// APIError is a non-ok Bot API answer (token already scrubbed).
type APIError struct {
	Method      string
	Status      int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: status %d — %s", e.Method, e.Status, e.Description)
}
