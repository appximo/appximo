package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/worker"
)

// DefaultEmailTopic is the outbox topic the EmailProcessor consumes. A producer
// (a Class-1 handler, ctx.Enqueue) writes an "email.send" event in its business
// transaction; the user gets their HTTP response immediately and the email goes
// out async — never blocking the request (the whole point of the outbox).
const DefaultEmailTopic = "email.send"

// emailEvent is the outbox payload for an email send. `data` carries the template
// variables (name, code, link, …); `subject` overrides the template default.
//
//	{"to":"a@b.com","template":"verification","subject":"...","data":{"name":"Ana","code":"123456"}}
type emailEvent struct {
	To       string         `json:"to"`
	Template string         `json:"template"`
	Subject  string         `json:"subject,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// EmailProcessor is the EMAIL-CONSUMER-V1 consumer: on an "email.send" outbox
// event it renders the named template with the payload data and sends it through
// an external SMTP provider (EmailSender). It writes nothing back to the engine —
// an email needs no write-back; the outbox row IS the record (sent ⇒ delivered to
// the provider; parked 'failed' ⇒ gave up after retries).
//
// Idempotency / at-least-once: delivery is at-least-once, so a worker that crashes
// AFTER the provider accepts the message but BEFORE the outbox COMMIT will resend
// on redelivery. For TRANSACTIONAL email a rare duplicate is tolerable (a second
// "verify your email" is annoying, not harmful) and is the accepted trade-off here
// — far better than dropping the mail. As a free, standards-based mitigation every
// send carries a DETERMINISTIC Message-ID derived from the outbox Row.ID, so a
// well-behaved provider/MTA can dedupe the redelivery. For HARD idempotency, a
// consumer can record Row.ID in its own table and no-op the second delivery (the
// XLSX consumer's terminal-status check is the analogue); not done here by choice.
type EmailProcessor struct {
	sender EmailSender
	tmpls  *templateSet
	topic  string
	domain string // Message-ID domain, e.g. "appitools.local"
	log    zerolog.Logger
}

// NewEmailProcessor builds the consumer. topic defaults to DefaultEmailTopic; the
// templates are the built-in demo set (verification, welcome) — swap them with
// WithTemplates for an app's own.
func NewEmailProcessor(sender EmailSender, log zerolog.Logger) *EmailProcessor {
	return &EmailProcessor{
		sender: sender,
		tmpls:  newBuiltinTemplates(),
		topic:  DefaultEmailTopic,
		domain: "appitools.local",
		log:    log,
	}
}

// WithTopic overrides the consumed topic. Returns p for chaining.
func (p *EmailProcessor) WithTopic(topic string) *EmailProcessor {
	if topic != "" {
		p.topic = topic
	}
	return p
}

// Process implements worker.Processor. A foreign topic is acked (this is safe ONLY
// when the email worker is the sole consumer of those rows; for a shared outbox
// with multiple event types, compose consumers behind a Router instead — see
// Router). A malformed payload, unknown template, or empty recipient is a
// permanent error: it is returned (the worker retries to maxAttempts then parks
// the row 'failed', durably recording the bad event) and the SMTP send is never
// attempted. A send error (transient or 5xx) is returned so the row is retried.
func (p *EmailProcessor) Process(ctx context.Context, row worker.Row) error {
	if row.Topic != p.topic {
		return nil // not our event — ack (see the Router caveat above)
	}
	var ev emailEvent
	if err := json.Unmarshal(row.Payload, &ev); err != nil {
		p.log.Error().Int64("id", row.ID).Err(err).Msg("consumers: email event payload is not valid JSON (permanent)")
		return fmt.Errorf("consumers: decode email event id=%d: %w", row.ID, err)
	}
	if strings.TrimSpace(ev.To) == "" {
		return fmt.Errorf("consumers: email event id=%d has no recipient (permanent)", row.ID)
	}
	if ev.Template == "" {
		return fmt.Errorf("consumers: email event id=%d has no template (permanent)", row.ID)
	}

	html, err := p.tmpls.render(ev.Template, ev.Data)
	if err != nil {
		// Unknown template / bad data → permanent; won't succeed on a retry.
		p.log.Error().Int64("id", row.ID).Str("template", ev.Template).Err(err).
			Msg("consumers: email template render failed (permanent)")
		return err
	}

	msg := Email{
		To:        ev.To,
		Subject:   p.tmpls.subjectFor(ev.Template, ev.Subject),
		HTML:      html,
		MessageID: p.messageID(row.TenantID, row.ID),
	}
	if err := p.sender.Send(ctx, msg); err != nil {
		// Transient (connection/timeout) or permanent (5xx) — return it so the row
		// stays pending and is retried; the worker parks it 'failed' at maxAttempts.
		p.log.Warn().Int64("id", row.ID).Str("to", ev.To).Err(err).Msg("consumers: email send failed; will retry")
		return err
	}
	p.log.Info().Int64("id", row.ID).Str("tenant_id", row.TenantID).Str("to", ev.To).
		Str("template", ev.Template).Msg("consumers: email sent")
	return nil
}

// messageID is the deterministic Message-ID for an outbox row: identical on every
// redelivery of the same event, so a provider can dedupe (best-effort idempotency).
func (p *EmailProcessor) messageID(tenant string, id int64) string {
	return fmt.Sprintf("<outbox-%s-%d@%s>", nonce(tenant), id, p.domain)
}
