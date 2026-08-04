package consumers

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/worker"
)

// mockSender records every Send and returns its configured error (nil = success).
type mockSender struct {
	mu       sync.Mutex
	attempts []Email
	err      error
}

func (m *mockSender) Send(_ context.Context, e Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts = append(m.attempts, e)
	return m.err
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.attempts)
}

func (m *mockSender) last() Email {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[len(m.attempts)-1]
}

func emailRow(t *testing.T, id int64, topic string, payload any) worker.Row {
	t.Helper()
	var raw json.RawMessage
	switch p := payload.(type) {
	case string:
		raw = json.RawMessage(p)
	default:
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		raw = b
	}
	return worker.Row{ID: id, TenantID: "acme", Topic: topic, Payload: raw}
}

func newEmailProc(s EmailSender) *EmailProcessor {
	return NewEmailProcessor(s, zerolog.Nop())
}

func TestEmail_Success(t *testing.T) {
	s := &mockSender{}
	p := newEmailProc(s)
	row := emailRow(t, 7, "email.send", map[string]any{
		"to": "ana@example.com", "template": "verification",
		"data": map[string]any{"name": "Ana", "code": "123456"},
	})
	if err := p.Process(context.Background(), row); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if s.count() != 1 {
		t.Fatalf("sends = %d, want 1", s.count())
	}
	got := s.last()
	if got.To != "ana@example.com" {
		t.Fatalf("To = %q", got.To)
	}
	if got.Subject != "Confirm your email" {
		t.Fatalf("Subject = %q, want template default", got.Subject)
	}
	if !strings.Contains(got.HTML, "123456") || !strings.Contains(got.HTML, "Ana") {
		t.Fatalf("HTML missing rendered data: %q", got.HTML)
	}
	if got.MessageID == "" || !strings.Contains(got.MessageID, "-7@") {
		t.Fatalf("MessageID = %q, want deterministic with row id", got.MessageID)
	}
}

func TestEmail_SubjectOverride(t *testing.T) {
	s := &mockSender{}
	p := newEmailProc(s)
	row := emailRow(t, 1, "email.send", map[string]any{
		"to": "x@y.com", "template": "welcome", "subject": "Custom subject",
		"data": map[string]any{"name": "Bo"},
	})
	if err := p.Process(context.Background(), row); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if s.last().Subject != "Custom subject" {
		t.Fatalf("Subject = %q, want override", s.last().Subject)
	}
}

func TestEmail_ForeignTopicAcked(t *testing.T) {
	s := &mockSender{}
	p := newEmailProc(s)
	row := emailRow(t, 1, "filejobs.created", map[string]any{"id": "x"})
	if err := p.Process(context.Background(), row); err != nil {
		t.Fatalf("foreign topic should ack with nil, got %v", err)
	}
	if s.count() != 0 {
		t.Fatalf("foreign topic must not send; sends = %d", s.count())
	}
}

func TestEmail_PermanentFailures_NoSend(t *testing.T) {
	cases := map[string]any{
		"bad json":         `{not json`,
		"empty recipient":  map[string]any{"to": "", "template": "welcome"},
		"missing template": map[string]any{"to": "a@b.com"},
		"unknown template": map[string]any{"to": "a@b.com", "template": "does-not-exist"},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			s := &mockSender{}
			p := newEmailProc(s)
			err := p.Process(context.Background(), emailRow(t, 1, "email.send", payload))
			if err == nil {
				t.Fatalf("%s: expected a (permanent) error", name)
			}
			if s.count() != 0 {
				t.Fatalf("%s: SMTP must NOT be attempted on a permanent pre-send error; sends = %d", name, s.count())
			}
		})
	}
}

func TestEmail_TransientSendError_Retryable(t *testing.T) {
	s := &mockSender{err: context.DeadlineExceeded} // stand-in for a transient SMTP error
	p := newEmailProc(s)
	row := emailRow(t, 1, "email.send", map[string]any{
		"to": "a@b.com", "template": "welcome", "data": map[string]any{"name": "Z"},
	})
	if err := p.Process(context.Background(), row); err == nil {
		t.Fatal("a send error must propagate so the row stays pending (retry)")
	}
	if s.count() != 1 {
		t.Fatalf("send should have been attempted once; got %d", s.count())
	}
}

func TestEmail_IdempotentMessageID_DoubleSendTolerated(t *testing.T) {
	s := &mockSender{}
	p := newEmailProc(s)
	row := emailRow(t, 42, "email.send", map[string]any{
		"to": "a@b.com", "template": "verification", "data": map[string]any{"code": "9"},
	})
	// At-least-once: the SAME row may be delivered twice (worker crash before COMMIT).
	for i := 0; i < 2; i++ {
		if err := p.Process(context.Background(), row); err != nil {
			t.Fatalf("Process #%d: %v", i, err)
		}
	}
	if s.count() != 2 {
		t.Fatalf("double-send is tolerated; sends = %d, want 2", s.count())
	}
	// …but both carry the SAME deterministic Message-ID, the dedup hint a provider
	// can use to drop the duplicate.
	if s.attempts[0].MessageID != s.attempts[1].MessageID {
		t.Fatalf("Message-ID must be stable across redelivery: %q vs %q",
			s.attempts[0].MessageID, s.attempts[1].MessageID)
	}
}

func TestEmail_TemplateAutoEscapes(t *testing.T) {
	s := &mockSender{}
	p := newEmailProc(s)
	row := emailRow(t, 1, "email.send", map[string]any{
		"to": "a@b.com", "template": "welcome",
		"data": map[string]any{"name": "<script>alert(1)</script>"},
	})
	if err := p.Process(context.Background(), row); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if strings.Contains(s.last().HTML, "<script>alert(1)</script>") {
		t.Fatal("html/template must escape hostile data — found raw <script> in body")
	}
}
