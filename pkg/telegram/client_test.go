package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The getUpdates long-poll holds the connection for tens of seconds before
// headers arrive; the send client's 15s ResponseHeaderTimeout would abort it,
// so getUpdates MUST use a separate, longer-lived client. This pins that the
// poll client outlasts the send client (the VOZ-ESCALON1 field fix — before it,
// every idle poll timed out and retried, adding ~30s command latency).
func TestPollClientOutlastsSendClient(t *testing.T) {
	c, err := New("123456:AAExampleExampleExampleExample01", "8851136988")
	if err != nil {
		t.Fatal(err)
	}
	if c.poll.Timeout <= c.http.Timeout {
		t.Fatalf("poll client timeout (%s) must exceed the send client (%s)", c.poll.Timeout, c.http.Timeout)
	}
	if c.poll.Timeout < 60*time.Second {
		t.Fatalf("poll client timeout (%s) must comfortably exceed a 45s long-poll", c.poll.Timeout)
	}
}

// A getUpdates whose response is delayed beyond the SEND client's 15s window
// still succeeds, because it flows through the long-poll client. Uses a short
// fake delay (well under the poll timeout) to stay fast.
func TestGetUpdates_SurvivesDelayedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)                                                       // stand-in for a long-poll hold
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []Update{{UpdateID: 5}}}) //nolint:errcheck
	}))
	defer srv.Close()
	c, _ := New("123456:AAExampleExampleExampleExample01", "8851136988")
	c.SetHTTPClient(srv.Client()) // sets both http and poll to the test client
	c.SetAPIBase(srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ups, err := c.GetUpdates(ctx, 0, 1)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(ups) != 1 || ups[0].UpdateID != 5 {
		t.Fatalf("want one update id=5, got %v", ups)
	}
}

// VOZ-VISUAL-S1: picture + text, never picture alone.
func TestSendPhotoWithText_CaptionOrSplit(t *testing.T) {
	var photos, messages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendPhoto"):
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("sendPhoto must be multipart: %v", err)
			}
			if r.FormValue("parse_mode") != "HTML" || r.FormValue("chat_id") != "8851136988" {
				t.Errorf("caption must be HTML to the configured chat; got mode=%q chat=%q", r.FormValue("parse_mode"), r.FormValue("chat_id"))
			}
			f, _, err := r.FormFile("photo")
			if err != nil {
				t.Errorf("photo part: %v", err)
			} else {
				b, _ := io.ReadAll(f)
				if string(b) != "PNGBYTES" {
					t.Errorf("photo bytes not forwarded: %q", b)
				}
			}
			photos = append(photos, r.FormValue("caption"))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			var body struct {
				Text string `json:"text"`
			}
			json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
			messages = append(messages, body.Text)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}}) //nolint:errcheck
	}))
	defer srv.Close()
	c, err := New("123456:AAExampleExampleExampleExample01", "8851136988")
	if err != nil {
		t.Fatal(err)
	}
	c.SetHTTPClient(srv.Client())
	c.SetAPIBase(srv.URL)

	short := "📋 <b>Resumen</b>\n🔴 3 esperan acción"
	if err := c.SendPhotoWithText(context.Background(), c.ChatIDValue(), []byte("PNGBYTES"), short); err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || photos[0] != short || len(messages) != 0 {
		t.Fatalf("short text rides as the caption alone; photos=%v messages=%v", photos, messages)
	}

	long := "📋 <b>Resumen largo</b>\n" + strings.Repeat("línea de detalle que no cabe en el pie\n", 60)
	photos, messages = nil, nil
	if err := c.SendPhotoWithText(context.Background(), c.ChatIDValue(), []byte("PNGBYTES"), long); err != nil {
		t.Fatal(err)
	}
	if len(photos) != 1 || !strings.HasPrefix(photos[0], "📋 <b>Resumen largo</b>") || !strings.Contains(photos[0], "detalle completo va abajo") || len([]rune(photos[0])) > captionMax {
		t.Fatalf("long text: photo gets the first line as caption; got %q", photos[0])
	}
	if len(messages) != 1 || messages[0] != long {
		t.Fatalf("long text must follow in full as a second message; got %d messages", len(messages))
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(&RetryAfterError{After: time.Second, Msg: "429"}) {
		t.Error("429 is retryable")
	}
	if !IsRetryable(&APIError{Status: 502}) {
		t.Error("5xx is retryable")
	}
	if IsRetryable(&APIError{Status: 400}) {
		t.Error("400 is final")
	}
	if !IsRetryable(context.DeadlineExceeded) {
		t.Error("transport/context errors are retryable")
	}
}
