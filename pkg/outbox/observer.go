package outbox

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Observer makes the outbox VISIBLE (AUTO-3, AUTOMATIZACION-S1). The outbox was
// the best-built and worst-delivered piece of the engine: at-least-once delivery,
// transactional enqueue — and not one metric, route or alert. 34 real
// factura.emitir rows sat pending for six weeks and nothing said so.
//
// The Observer is a small poller on the ENGINE side (the table is the durable
// truth, so the engine can observe the queue without coupling to any worker):
// every Interval it collects counts by state, the age of the OLDEST pending row —
// the metric that matters: a thousand draining rows are healthy, one row parked
// for a month is not — the oldest failed age, the sent-last-hour drain rate proxy,
// and the pending backlog per topic (bounded cardinality). The snapshot feeds
// /metrics (see Collector), GET /admin/outbox, and the alerter.
type Observer struct {
	pool *pgxpool.Pool
	cfg  ObserverConfig

	latest atomic.Pointer[Stats]

	// lastAlert rate-limits each alert kind (touched only by the Run goroutine).
	lastAlert map[string]time.Time
}

// ObserverConfig tunes the Observer. Zero values fall back to defaults.
type ObserverConfig struct {
	// Interval between collections. Default 15s.
	Interval time.Duration
	// MaxPendingAge is the alert threshold for the oldest pending row's age
	// (APPXIMO_OUTBOX_MAX_PENDING_AGE). Default 15m; negative disables the alert
	// (collection continues — the gauge stays truthful either way).
	MaxPendingAge time.Duration
	// OnAlert receives alerts (kind, message). Nil = no alerting. Each kind is
	// rate-limited to one alert per hour.
	OnAlert func(kind, message string)
}

// TopicCount is one topic's pending backlog.
type TopicCount struct {
	Topic     string    `json:"topic"`
	Count     int64     `json:"count"`
	OldestAge float64   `json:"oldest_age_seconds"`
	Oldest    time.Time `json:"oldest"`
}

// Stats is one collected snapshot of the queue's health.
type Stats struct {
	CollectedAt        time.Time    `json:"collected_at"`
	Pending            int64        `json:"pending"`
	Failed             int64        `json:"failed"`
	SentLastHour       int64        `json:"sent_last_hour"`
	OldestPendingAge   float64      `json:"oldest_pending_age_seconds"` // 0 when no pending rows
	OldestFailedAge    float64      `json:"oldest_failed_age_seconds"`  // 0 when no failed rows
	OldestPendingTopic string       `json:"oldest_pending_topic,omitempty"`
	PendingByTopic     []TopicCount `json:"pending_by_topic,omitempty"`

	// Workflow health (ADR-031) — collected from public.workflow_runs /
	// public.workflow_cron when they exist (the engine ensures them at boot).
	// WorkflowOverdueSeconds is the headline: how far past due the most-overdue
	// cron schedule is. A growing value means NO LEADER IS FIRING — the worker is
	// down or none runs the scheduler — which is exactly the outbox's old silent
	// failure, transplanted; here it alerts instead.
	WorkflowRuns24h        int64   `json:"workflow_runs_24h"`
	WorkflowFailed24h      int64   `json:"workflow_failed_24h"`
	WorkflowOverdueSeconds float64 `json:"workflow_overdue_seconds"`
}

// NewObserver builds an Observer over the engine's pool.
func NewObserver(pool *pgxpool.Pool, cfg ObserverConfig) *Observer {
	if cfg.Interval <= 0 {
		cfg.Interval = 15 * time.Second
	}
	if cfg.MaxPendingAge == 0 {
		cfg.MaxPendingAge = 15 * time.Minute
	}
	return &Observer{pool: pool, cfg: cfg, lastAlert: make(map[string]time.Time)}
}

// Latest returns the most recent snapshot, or nil before the first collection.
func (o *Observer) Latest() *Stats { return o.latest.Load() }

// Run collects every Interval until ctx is cancelled. Collection errors are
// swallowed (the DB being down has its own, better signals — the breaker, the
// 503s); the previous snapshot stays available.
func (o *Observer) Run(ctx context.Context) {
	// First collection immediately, so /metrics is truthful right after boot.
	if s, err := o.Collect(ctx); err == nil {
		o.latest.Store(s)
		o.alert(s)
	}
	t := time.NewTicker(o.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s, err := o.Collect(ctx)
			if err != nil {
				continue
			}
			o.latest.Store(s)
			o.alert(s)
		}
	}
}

// Collect runs one collection pass. Each query rides a partial index
// (idx_outbox_pending / idx_outbox_failed / idx_outbox_sent_at), so the pass
// stays cheap even over a table with a long sent history.
func (o *Observer) Collect(ctx context.Context) (*Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	s := &Stats{CollectedAt: time.Now()}

	var oldestPending, oldestFailed *time.Time
	if err := o.pool.QueryRow(ctx,
		`SELECT count(*), min(created_at) FROM public.outbox WHERE state = 'pending'`,
	).Scan(&s.Pending, &oldestPending); err != nil {
		return nil, fmt.Errorf("outbox: observe pending: %w", err)
	}
	if err := o.pool.QueryRow(ctx,
		`SELECT count(*), min(created_at) FROM public.outbox WHERE state = 'failed'`,
	).Scan(&s.Failed, &oldestFailed); err != nil {
		return nil, fmt.Errorf("outbox: observe failed: %w", err)
	}
	if err := o.pool.QueryRow(ctx,
		`SELECT count(*) FROM public.outbox WHERE state = 'sent' AND sent_at > now() - interval '1 hour'`,
	).Scan(&s.SentLastHour); err != nil {
		return nil, fmt.Errorf("outbox: observe sent: %w", err)
	}
	now := time.Now()
	if oldestPending != nil {
		s.OldestPendingAge = now.Sub(*oldestPending).Seconds()
	}
	if oldestFailed != nil {
		s.OldestFailedAge = now.Sub(*oldestFailed).Seconds()
	}

	if s.Pending > 0 {
		// Per-topic backlog, cardinality bounded: at most 20 topics per snapshot,
		// oldest-first so the stuck ones always make the cut. The topic value is
		// producer-controlled, so the bound is what keeps /metrics from exploding.
		rows, err := o.pool.Query(ctx, `
			SELECT topic, count(*), min(created_at)
			FROM public.outbox
			WHERE state = 'pending'
			GROUP BY topic
			ORDER BY min(created_at)
			LIMIT 20`)
		if err != nil {
			return nil, fmt.Errorf("outbox: observe topics: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tc TopicCount
			var oldest time.Time
			if err := rows.Scan(&tc.Topic, &tc.Count, &oldest); err != nil {
				return nil, fmt.Errorf("outbox: scan topic: %w", err)
			}
			tc.Oldest = oldest
			tc.OldestAge = now.Sub(oldest).Seconds()
			s.PendingByTopic = append(s.PendingByTopic, tc)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("outbox: iterate topics: %w", err)
		}
		if len(s.PendingByTopic) > 0 {
			s.OldestPendingTopic = s.PendingByTopic[0].Topic
		}
	}

	// Workflow health — only when the tables exist (a database driven by an older
	// engine simply reports zeros).
	var hasWF bool
	if err := o.pool.QueryRow(ctx,
		`SELECT to_regclass('public.workflow_runs') IS NOT NULL AND to_regclass('public.workflow_cron') IS NOT NULL`,
	).Scan(&hasWF); err == nil && hasWF {
		if err := o.pool.QueryRow(ctx, `
			SELECT count(*), count(*) FILTER (WHERE status = 'failed')
			FROM public.workflow_runs
			WHERE started_at > now() - interval '24 hours'`,
		).Scan(&s.WorkflowRuns24h, &s.WorkflowFailed24h); err != nil {
			return nil, fmt.Errorf("outbox: observe workflow runs: %w", err)
		}
		var overdue *float64
		if err := o.pool.QueryRow(ctx, `
			SELECT extract(epoch FROM now() - min(next_run))
			FROM public.workflow_cron
			WHERE next_run IS NOT NULL AND next_run <= now()`,
		).Scan(&overdue); err != nil {
			return nil, fmt.Errorf("outbox: observe workflow schedules: %w", err)
		}
		if overdue != nil && *overdue > 0 {
			s.WorkflowOverdueSeconds = *overdue
		}
	}
	return s, nil
}

// alert fires the two conditions that mean "a human must look":
//   - the oldest pending row is older than MaxPendingAge — enqueued and nothing
//     drains it (no worker, or no consumer owns its topic);
//   - any row is parked 'failed' — retries exhausted; the row carries last_error.
//
// Each kind fires at most once per hour so a stuck queue does not become a
// stuck-alert storm.
func (o *Observer) alert(s *Stats) {
	if o.cfg.OnAlert == nil {
		return
	}
	const cooldown = time.Hour
	fire := func(kind, msg string) {
		if time.Since(o.lastAlert[kind]) < cooldown {
			return
		}
		o.lastAlert[kind] = time.Now()
		o.cfg.OnAlert(kind, msg)
	}
	if o.cfg.MaxPendingAge > 0 && s.OldestPendingAge > o.cfg.MaxPendingAge.Seconds() {
		fire("outbox_stale_pending", fmt.Sprintf(
			"outbox: the oldest pending event is %s old (threshold %s; topic %q, %d pending). Nothing is draining it — check that appximo-worker is running and has a consumer for that topic. Details: GET /admin/outbox",
			(time.Duration(s.OldestPendingAge)*time.Second).Round(time.Second), o.cfg.MaxPendingAge, s.OldestPendingTopic, s.Pending))
	}
	if s.Failed > 0 {
		fire("outbox_failed", fmt.Sprintf(
			"outbox: %d event(s) parked state='failed' (retries exhausted; oldest %s old). Each row carries its error in last_error — GET /admin/outbox lists them",
			s.Failed, (time.Duration(s.OldestFailedAge)*time.Second).Round(time.Second)))
	}
	// A cron schedule more than 10 minutes past due means NO leader is firing —
	// the worker is down, or none of the running workers has the scheduler.
	if s.WorkflowOverdueSeconds > (10 * time.Minute).Seconds() {
		fire("workflow_overdue", fmt.Sprintf(
			"workflows: a cron schedule is %s past due — no scheduler is firing (is appximo-worker running?). Details: GET /admin/workflows",
			(time.Duration(s.WorkflowOverdueSeconds)*time.Second).Round(time.Second)))
	}
	if s.WorkflowFailed24h > 0 {
		fire("workflow_failed", fmt.Sprintf(
			"workflows: %d failed run(s) in the last 24h — each run's error and per-step detail is in GET /admin/workflows (public.workflow_runs)",
			s.WorkflowFailed24h))
	}
}
