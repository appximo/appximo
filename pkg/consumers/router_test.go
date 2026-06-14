package consumers

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/worker"
)

// countingProc records how many rows it received.
type countingProc struct{ n int64 }

func (c *countingProc) Process(_ context.Context, _ worker.Row) error {
	atomic.AddInt64(&c.n, 1)
	return nil
}

func TestRouter_DispatchesByTopic(t *testing.T) {
	email := &countingProc{}
	files := &countingProc{}
	r := NewRouter(zerolog.Nop()).
		Handle("email.send", email).
		HandlePrefix("filejobs.", files)

	rows := []worker.Row{
		{Topic: "email.send"},
		{Topic: "filejobs.created"},
		{Topic: "filejobs.updated"},
		{Topic: "something.else"}, // unmatched → acked, no consumer
	}
	for _, row := range rows {
		if err := r.Process(context.Background(), row); err != nil {
			t.Fatalf("route %s: %v", row.Topic, err)
		}
	}
	if email.n != 1 {
		t.Fatalf("email got %d, want 1", email.n)
	}
	if files.n != 2 {
		t.Fatalf("files got %d, want 2 (both filejobs.* topics)", files.n)
	}
}

func TestRouter_FirstMatchWins(t *testing.T) {
	specific := &countingProc{}
	prefix := &countingProc{}
	// Register the specific exact rule BEFORE the broad prefix rule.
	r := NewRouter(zerolog.Nop()).
		Handle("filejobs.created", specific).
		HandlePrefix("filejobs.", prefix)

	if err := r.Process(context.Background(), worker.Row{Topic: "filejobs.created"}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if specific.n != 1 || prefix.n != 0 {
		t.Fatalf("first match must win: specific=%d prefix=%d", specific.n, prefix.n)
	}
}

// TestRouter_FixesAckDropCoexistence is the point of the Router: an email consumer
// and an xlsx consumer on ONE outbox, where each single consumer would ack-drop the
// other's topic. Behind the Router, each event reaches its real consumer.
func TestRouter_FixesAckDropCoexistence(t *testing.T) {
	emailSender := &mockSender{}
	emailProc := NewEmailProcessor(emailSender, zerolog.Nop())
	xlsxSeen := &countingProc{}

	r := NewRouter(zerolog.Nop()).
		Handle("email.send", emailProc).
		HandlePrefix("filejobs.", xlsxSeen)

	// An email event and a filejob event interleaved on the same outbox.
	emailEv := emailRow(t, 1, "email.send", map[string]any{
		"to": "a@b.com", "template": "welcome", "data": map[string]any{"name": "Z"},
	})
	if err := r.Process(context.Background(), emailEv); err != nil {
		t.Fatalf("route email: %v", err)
	}
	if err := r.Process(context.Background(), worker.Row{Topic: "filejobs.created"}); err != nil {
		t.Fatalf("route filejob: %v", err)
	}
	if emailSender.count() != 1 {
		t.Fatalf("email consumer should have sent 1, got %d", emailSender.count())
	}
	if xlsxSeen.n != 1 {
		t.Fatalf("xlsx consumer should have seen 1 filejob, got %d", xlsxSeen.n)
	}
}
