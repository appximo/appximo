package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
