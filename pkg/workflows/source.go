package workflows

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/worker"
)

// Source keeps the compiled workflows of every tenant, refreshed from
// public.tenants.json_schema — the schema IS the source of truth (A-70), so a
// deploy through Studio / the control plane / migrate reaches the worker within
// one refresh interval with no second store and no restart.
type Source struct {
	pool     *pgxpool.Pool
	interval time.Duration
	log      zerolog.Logger

	mu       sync.RWMutex
	byTenant map[string][]*Workflow
	topics   worker.TopicSet
	entries  []CronEntry
}

// CronEntry is one (tenant, cron workflow) pair the scheduler drives.
type CronEntry struct {
	Tenant   string
	Workflow *Workflow
}

// NewSource builds a Source polling every interval (default 60s).
func NewSource(pool *pgxpool.Pool, interval time.Duration, log zerolog.Logger) *Source {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &Source{pool: pool, interval: interval, log: log, byTenant: map[string][]*Workflow{}}
}

// Run refreshes immediately and then on every tick until ctx is cancelled.
func (s *Source) Run(ctx context.Context) {
	if err := s.Refresh(ctx); err != nil {
		s.log.Warn().Err(err).Msg("workflows: initial schema refresh failed; retrying on the next tick")
	}
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Refresh(ctx); err != nil {
				s.log.Warn().Err(err).Msg("workflows: schema refresh failed; keeping the previous set")
			}
		}
	}
}

// Refresh reloads every tenant's workflows. A tenant whose schema no longer
// compiles is SKIPPED WITH A LOUD LOG (the deploy gate validates, so this is a
// version-skew corner) — the other tenants keep working.
func (s *Source) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx,
		`SELECT id, json_schema FROM public.tenants WHERE json_schema IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()

	byTenant := map[string][]*Workflow{}
	var topics worker.TopicSet
	var entries []CronEntry
	seenTopic := map[string]bool{}

	for rows.Next() {
		var tenant string
		var raw []byte
		if err := rows.Scan(&tenant, &raw); err != nil {
			return err
		}
		sch, err := schema.LoadFromBytes(raw)
		if err != nil {
			s.log.Warn().Str("tenant", tenant).Err(err).Msg("workflows: tenant schema does not parse — its workflows are NOT running")
			continue
		}
		if len(sch.Workflows) == 0 {
			continue
		}
		wfs, err := Compile(sch)
		if err != nil {
			s.log.Warn().Str("tenant", tenant).Err(err).Msg("workflows: tenant workflows do not compile — they are NOT running (redeploy a valid schema)")
			continue
		}
		byTenant[tenant] = wfs
		for _, wf := range wfs {
			switch wf.TriggerType {
			case "event":
				if !seenTopic[wf.Topic] {
					seenTopic[wf.Topic] = true
					topics.Exact = append(topics.Exact, wf.Topic)
				}
			case "cron":
				entries = append(entries, CronEntry{Tenant: tenant, Workflow: wf})
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	s.byTenant = byTenant
	s.topics = topics
	s.entries = entries
	s.mu.Unlock()
	return nil
}

// ForTopic returns the tenant's workflows triggered by topic.
func (s *Source) ForTopic(tenant, topic string) []*Workflow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Workflow
	for _, wf := range s.byTenant[tenant] {
		if wf.TriggerType == "event" && wf.Topic == topic {
			out = append(out, wf)
		}
	}
	return out
}

// Topics is the union of every tenant's event-trigger topics — what the worker
// may CLAIM for the workflow consumer.
func (s *Source) Topics() worker.TopicSet {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.topics
}

// CronEntries returns the current (tenant, cron workflow) pairs.
func (s *Source) CronEntries() []CronEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]CronEntry(nil), s.entries...)
}
