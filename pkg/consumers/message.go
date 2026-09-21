package consumers

// MessageProcessor (MOTOR-AGENDA-S1) sends ONE Telegram text message per
// outbox row on the `message.telegram` topic — the "what to do" of a per-row
// reminder workflow: an `enqueue` step whose data carries `text` (an expr over
// the row: "=\"En 15 min: \" + record.titulo") reaches the same bot chat the
// digest and the alerts use. Idempotency is the outbox's at-least-once
// contract (a retry after a Telegram outage re-sends the same text — a
// reminder twice is better than never, and the ledger behind the time
// trigger already guarantees one row per due instant). A payload with no
// text is a decision, not a delivery: discarded with the reason.

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/telegram"
	"github.com/appximo/appximo/pkg/worker"
)

// MessageTopic is the topic a workflow enqueues to send a text.
const MessageTopic = "message.telegram"

// MessageProcessor drains message.telegram.
type MessageProcessor struct {
	tg    *telegram.Client
	topic string
	log   zerolog.Logger
}

// NewMessageProcessor builds the consumer over the shared bot client.
func NewMessageProcessor(tg *telegram.Client, topic string, log zerolog.Logger) *MessageProcessor {
	if topic == "" {
		topic = MessageTopic
	}
	return &MessageProcessor{tg: tg, topic: topic, log: log}
}

// Topics implements worker.TopicOwner.
func (p *MessageProcessor) Topics() worker.TopicSet {
	return worker.TopicSet{Exact: []string{p.topic}}
}

// Process sends payload.text (Telegram HTML; `<`, `>` and `&` in a plain
// text are escaped by the caller's expression or arrive already safe — a
// message Telegram refuses is retried as plain text once).
func (p *MessageProcessor) Process(ctx context.Context, row worker.Row) error {
	var payload struct {
		Text     string `json:"text"`
		Workflow string `json:"workflow"`
	}
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return worker.Discard("message.telegram: payload is not JSON: " + err.Error())
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		return worker.Discard("message.telegram: payload has no text (the enqueue step's data needs a \"text\")")
	}
	if err := p.tg.SendMessage(ctx, text); err != nil {
		// A formatting rejection (bad HTML) would loop forever; send it escaped.
		if strings.Contains(strings.ToLower(err.Error()), "parse") {
			if err2 := p.tg.SendMessage(ctx, html.EscapeString(text)); err2 == nil {
				p.log.Warn().Str("tenant", row.TenantID).Str("workflow", payload.Workflow).Msg("message: text sent escaped — Telegram rejected its HTML")
				return nil
			}
		}
		return fmt.Errorf("message: send to Telegram for tenant %s: %w", row.TenantID, err) // transient → retry
	}
	p.log.Info().Str("tenant", row.TenantID).Str("workflow", payload.Workflow).Int("chars", len(text)).Msg("message: delivered to Telegram")
	return nil
}
