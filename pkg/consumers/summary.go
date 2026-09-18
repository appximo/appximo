package consumers

// SummaryProcessor is the SCHEDULED half of the voice plan's step 1
// (VOZ-ESCALON1-S1): a cron `workflow` enqueues a topic each morning, and this
// consumer computes the SAME digest the interactive `resumen` command returns
// (GET /api/summary, composed deterministically in the engine) and sends it to
// the configured Telegram chat. Cron → enqueue → consumer is the canonical
// ADR-031 shape: the workflow is pure schema, the sending is a consumer.
//
// Read-only and idempotent-enough: at-least-once delivery means a rare double
// morning summary is possible on a retry — harmless for a read-only digest
// (unlike a write), and documented. A transient engine/Telegram failure keeps
// the row pending for retry; it is never marked sent on failure.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/telegram"
	"github.com/appximo/appximo/pkg/worker"
)

// SummaryProcessor drains one topic and sends the tenant's digest to Telegram.
type SummaryProcessor struct {
	client *worker.EngineClient // GET /api/summary AS the configured summary role
	tg     *telegram.Client     // the same bot that receives commands and alerts
	topic  string
	log    zerolog.Logger
}

// NewSummaryProcessor builds the consumer. client must be scoped to the digest
// role (so RBAC shows exactly what that role may read); tg is the shared bot
// client; topic is the outbox topic the cron workflow enqueues.
func NewSummaryProcessor(client *worker.EngineClient, tg *telegram.Client, topic string, log zerolog.Logger) *SummaryProcessor {
	if topic == "" {
		topic = "summary.telegram"
	}
	return &SummaryProcessor{client: client, tg: tg, topic: topic, log: log}
}

// Topics implements worker.TopicOwner: it claims ONLY its configured topic, so
// it coexists with every other consumer on one outbox (AUTO-1).
func (p *SummaryProcessor) Topics() worker.TopicSet {
	return worker.TopicSet{Exact: []string{p.topic}}
}

// Process computes the digest for the event's tenant and sends it.
func (p *SummaryProcessor) Process(ctx context.Context, row worker.Row) error {
	// ?mode=scheduled (VOZ-DELTA-S1): the engine compares against yesterday,
	// applies the schema's send policy and records the decision. This consumer
	// OBEYS should_send: a silent morning is acked as processed (the outbox row
	// is done — the decision is on record in the digest's snapshot and visible
	// through the `estado` command), never retried into noise.
	status, body, err := p.client.Do(ctx, row.TenantID, http.MethodGet, "/api/summary?mode=scheduled", nil)
	if err != nil {
		return fmt.Errorf("summary: fetch digest for tenant %s: %w", row.TenantID, err) // transient → retry
	}
	if status != http.StatusOK {
		return fmt.Errorf("summary: engine answered %d fetching the digest for tenant %s (retrying)", status, row.TenantID)
	}
	var rep struct {
		Text       string   `json:"text"`
		ShouldSend *bool    `json:"should_send"`
		SendReason string   `json:"send_reason"`
		Reasons    []string `json:"change_reasons"`
		Level      string   `json:"level"`
	}
	if jerr := json.Unmarshal(body, &rep); jerr != nil || rep.Text == "" {
		return fmt.Errorf("summary: empty or unparseable digest for tenant %s", row.TenantID)
	}
	// An engine that predates the policy (no should_send in the JSON) is
	// treated as "always" — the historical behavior, never a silent drop.
	if rep.ShouldSend != nil && !*rep.ShouldSend {
		p.log.Info().Str("tenant", row.TenantID).Str("topic", p.topic).Str("level", rep.Level).Str("reason", rep.SendReason).
			Msg("summary: scheduled digest evaluated — nothing changed since the last one, staying SILENT by policy (summary.notify=changes)")
		return nil
	}
	p.log.Info().Str("tenant", row.TenantID).Str("reason", rep.SendReason).Strs("changes", rep.Reasons).Msg("summary: scheduled digest will be sent")

	// The IMAGE (VOZ-VISUAL-S1): same endpoint, ?format=png, rendered by the
	// engine from the same counts. Any failure here degrades to text-only —
	// an engine that predates the image (a mixed deploy) still delivers the
	// words; the picture is never a reason to hold the digest back.
	var png []byte
	if st, img, perr := p.client.Do(ctx, row.TenantID, http.MethodGet, "/api/summary?format=png", nil); perr == nil && st == http.StatusOK && len(img) > 8 && string(img[1:4]) == "PNG" {
		png = img
	} else {
		p.log.Warn().Str("tenant", row.TenantID).Int("status", st).Err(perr).Msg("summary: image not available — sending text only")
	}

	var serr error
	if png != nil {
		serr = p.tg.SendPhotoWithText(ctx, p.tg.ChatIDValue(), png, rep.Text)
		var fb *telegram.PhotoFallbackError
		if errors.As(serr, &fb) {
			p.log.Warn().Str("tenant", row.TenantID).Err(fb.Cause).Msg("summary: photo rejected by Telegram — text delivered instead")
			serr = nil
		}
	} else {
		serr = p.tg.SendMessage(ctx, rep.Text)
	}
	if serr != nil {
		return fmt.Errorf("summary: send to Telegram for tenant %s: %w", row.TenantID, serr) // transient → retry
	}
	p.log.Info().Str("tenant", row.TenantID).Str("topic", p.topic).Bool("image", png != nil).Msg("summary: scheduled digest delivered to Telegram")
	return nil
}
