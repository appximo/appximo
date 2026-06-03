package observability

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 1) Empty SLACK_WEBHOOK_URL → NewSlackAlerterFromEnv returns a NoopAlerter that
//    sends without panicking or erroring.
func TestAlerter_EmptyEnvIsNoop(t *testing.T) {
	t.Setenv("SLACK_WEBHOOK_URL", "")

	a := NewSlackAlerterFromEnv()
	if _, ok := a.(NoopAlerter); !ok {
		t.Fatalf("want NoopAlerter when env empty, got %T", a)
	}
	if err := a.Send(context.Background(), Alert{TenantID: "10", Level: LevelCritical}); err != nil {
		t.Errorf("noop Send must return nil, got %v", err)
	}
}

// 2) CooldownAlerter suppresses a repeat (tenant, level) inside the window and lets it
//    through once the window elapses. A short window keeps the test deterministic and fast.
func TestAlerter_CooldownWindow(t *testing.T) {
	mock := &mockAlerter{}
	c := NewCooldownAlerter(mock, 50*time.Millisecond)
	ctx := context.Background()
	crit := Alert{TenantID: "10", Level: LevelCritical}

	_ = c.Send(ctx, crit)
	_ = c.Send(ctx, crit) // within window → suppressed
	if mock.count() != 1 {
		t.Fatalf("within cooldown: want 1 delivered, got %d", mock.count())
	}

	// A different level is a different key → not suppressed.
	_ = c.Send(ctx, Alert{TenantID: "10", Level: LevelWarning})
	if mock.count() != 2 {
		t.Fatalf("different level should pass: want 2, got %d", mock.count())
	}

	time.Sleep(60 * time.Millisecond) // window elapses
	_ = c.Send(ctx, crit)
	if mock.count() != 3 {
		t.Fatalf("after cooldown: want 3 delivered, got %d", mock.count())
	}
}

// 3) SlackAlerter posts a correctly formatted {"text": ...} payload.
func TestAlerter_SlackPayloadFormat(t *testing.T) {
	var gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p map[string]string
		_ = json.Unmarshal(body, &p)
		gotText = p["text"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sa := NewSlackAlerter(srv.URL)
	sa.client = srv.Client() // bypass the SSRF guard (loopback test server)

	err := sa.Send(context.Background(), Alert{
		TenantID: "10",
		Level:    LevelCritical,
		Message:  "SLO: 100ms",
		BurnRate: 18.2,
		P95ms:    145,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	want := "🔴 [tenant 10] CRITICAL burn rate 18.2x | p95=145ms (SLO: 100ms)"
	if gotText != want {
		t.Errorf("slack text mismatch:\n want %q\n got  %q", want, gotText)
	}
}
