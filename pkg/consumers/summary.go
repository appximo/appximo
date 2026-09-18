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
	status, body, err := p.client.Do(ctx, row.TenantID, http.MethodGet, "/api/summary", nil)
	if err != nil {
		return fmt.Errorf("summary: fetch digest for tenant %s: %w", row.TenantID, err) // transient → retry
	}
	if status != http.StatusOK {
		return fmt.Errorf("summary: engine answered %d fetching the digest for tenant %s (retrying)", status, row.TenantID)
	}
	var rep struct {
		Text string `json:"text"`
	}
	if jerr := json.Unmarshal(body, &rep); jerr != nil || rep.Text == "" {
		return fmt.Errorf("summary: empty or unparseable digest for tenant %s", row.TenantID)
	}
	if serr := p.tg.SendMessage(ctx, rep.Text); serr != nil {
		return fmt.Errorf("summary: send to Telegram for tenant %s: %w", row.TenantID, serr) // transient → retry
	}
	p.log.Info().Str("tenant", row.TenantID).Str("topic", p.topic).Msg("summary: scheduled digest delivered to Telegram")
	return nil
}
