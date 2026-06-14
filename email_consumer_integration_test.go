//go:build integration

// EMAIL-CONSUMER-V1 integration test: the full async email path over a REAL
// Postgres outbox. An "email.send" event is enqueued, the worker's Drain loop
// claims it, the real EmailProcessor renders the template and hands the message to
// a capturing sender (NO real SMTP — the wire protocol is unit-tested separately),
// and the outbox row is marked sent. A failing sender drives the failure path:
// after maxAttempts the row is parked 'failed'. Reuses the shared container from
// TestMain (library_integration_test.go).
package appitools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/consumers"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/worker"
)

// captureSender implements consumers.EmailSender: it records messages and returns
// its configured error (nil = success). No network, no real email.
type captureSender struct {
	mu   sync.Mutex
	msgs []consumers.Email
	err  error
}

func (c *captureSender) Send(_ context.Context, m consumers.Email) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.msgs = append(c.msgs, m)
	}
	return c.err
}

func (c *captureSender) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.msgs) }

func enqueueEmail(t *testing.T, tenant string, payload any) {
	t.Helper()
	if err := outbox.EnsureTable(context.Background(), itPool); err != nil {
		t.Fatalf("ensure outbox: %v", err)
	}
	b, _ := json.Marshal(payload)
	if _, err := itPool.Exec(context.Background(),
		`INSERT INTO public.outbox (tenant_id, topic, payload) VALUES ($1,$2,$3)`,
		tenant, "email.send", string(b)); err != nil {
		t.Fatalf("enqueue email: %v", err)
	}
}

func outboxState(t *testing.T, tenant string) (state string, sent bool) {
	t.Helper()
	if err := itPool.QueryRow(context.Background(),
		`SELECT state, sent_at IS NOT NULL FROM public.outbox WHERE tenant_id=$1 AND topic='email.send' ORDER BY id DESC LIMIT 1`,
		tenant).Scan(&state, &sent); err != nil {
		t.Fatalf("query outbox state: %v", err)
	}
	return state, sent
}

func TestEmailConsumer_E2E_Sent(t *testing.T) {
	enqueueEmail(t, "emailok", map[string]any{
		"to": "ana@example.com", "template": "verification",
		"data": map[string]any{"name": "Ana", "code": "654321"},
	})
	sender := &captureSender{}
	proc := consumers.NewEmailProcessor(sender, zerolog.Nop())

	nop := zerolog.Nop()
	res, err := worker.Drain(context.Background(), itPool, proc, 50, 5, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Processed < 1 {
		t.Fatalf("processed %d, want >=1", res.Processed)
	}
	if sender.count() != 1 {
		t.Fatalf("sender got %d messages, want 1", sender.count())
	}
	msg := sender.msgs[0]
	if msg.To != "ana@example.com" || !strings.Contains(msg.HTML, "654321") {
		t.Fatalf("unexpected email: to=%q html=%q", msg.To, msg.HTML)
	}
	if st, sent := outboxState(t, "emailok"); st != "sent" || !sent {
		t.Fatalf("outbox row state=%q sent=%v, want sent/true", st, sent)
	}
}

func TestEmailConsumer_E2E_FailedAfterMaxAttempts(t *testing.T) {
	enqueueEmail(t, "emailbad", map[string]any{
		"to": "x@y.com", "template": "welcome", "data": map[string]any{"name": "Z"},
	})
	// A sender that always errors → the EmailProcessor keeps returning the error.
	sender := &captureSender{err: context.DeadlineExceeded}
	proc := consumers.NewEmailProcessor(sender, zerolog.Nop())

	nop := zerolog.Nop()
	// maxAttempts=1 → the first failure parks the row 'failed' immediately.
	res, err := worker.Drain(context.Background(), itPool, proc, 50, 1, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Failed < 1 {
		t.Fatalf("failed %d, want >=1", res.Failed)
	}
	if st, sent := outboxState(t, "emailbad"); st != "failed" || sent {
		t.Fatalf("outbox row state=%q sent=%v, want failed/false", st, sent)
	}
}
