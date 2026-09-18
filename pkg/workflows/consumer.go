package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/worker"
)

// EventConsumer is the worker.Processor that runs event-triggered workflows. It
// implements worker.TopicOwner with the DYNAMIC union of every tenant's trigger
// topics, so the scoped Drain claims exactly what some tenant's workflow
// consumes — and nothing else.
type EventConsumer struct {
	Src  *Source
	Exec *Executor
	Log  zerolog.Logger
}

// Topics implements worker.TopicOwner.
func (c *EventConsumer) Topics() worker.TopicSet { return c.Src.Topics() }

// crudEvent is the engine's lean CRUD event payload.
type crudEvent struct {
	ID       string `json:"id"`
	Resource string `json:"resource"`
	Action   string `json:"action"` // created | updated | deleted
}

// Process implements worker.Processor: run every workflow of the ROW'S tenant
// triggered by this topic. The topic set is the union across tenants, so a
// tenant that declares no workflow for a claimed topic is a legitimate no-op
// (acked with a debug log — another tenant's workflow owns the claim). A failed
// run returns its error: the row retries with the outbox's backoff and finally
// parks 'failed' carrying the run's error — visible end to end. At-least-once
// therefore applies to WORKFLOW RUNS: steps must be idempotent (the same
// doctrine as every consumer; an `update` setting a field is naturally so).
func (c *EventConsumer) Process(ctx context.Context, row worker.Row) error {
	wfs := c.Src.ForTopic(row.TenantID, row.Topic)
	if len(wfs) == 0 {
		c.Log.Debug().Int64("id", row.ID).Str("tenant", row.TenantID).Str("topic", row.Topic).
			Msg("workflows: no workflow of this tenant consumes the topic (another tenant's does) — nothing to run")
		return nil
	}
	var ev crudEvent
	if err := json.Unmarshal(row.Payload, &ev); err != nil {
		return fmt.Errorf("workflows: decode event id=%d: %w", row.ID, err)
	}

	for _, wf := range wfs {
		env := map[string]any{
			"event": map[string]any{
				"id":       ev.ID,
				"resource": ev.Resource,
				"action":   ev.Action,
				"topic":    row.Topic,
			},
			"tenant": row.TenantID,
			"now":    time.Now().UTC(),
			"record": nil,
		}
		// The lean payload carries only the id: fetch the row through the engine
		// API as the workflow's role (so it sees only what the role may read).
		// For a delete the row is gone by definition — record stays nil.
		if ev.Action != "deleted" && ev.ID != "" {
			rec, err := c.Exec.FetchRecord(ctx, row.TenantID, wf.Role, ev.Resource, ev.ID)
			if err != nil {
				return fmt.Errorf("workflows: %s: %w", wf.Name, err)
			}
			if rec == nil {
				// The row vanished between the event and the run (deleted, or the
				// workflow's role may not read it). Conditions over `record` will
				// evaluate against nil — say so instead of a silent skip.
				c.Log.Warn().Int64("id", row.ID).Str("workflow", wf.Name).Str("record", ev.ID).
					Msg("workflows: triggering record not readable (gone, or outside the role's scope) — running with record = nil")
			}
			env["record"] = rec
		}
		if err := c.Exec.Run(ctx, row.TenantID, wf, "event:"+row.Topic, env); err != nil {
			return fmt.Errorf("workflows: %s: %w", wf.Name, err)
		}
	}
	return nil
}
