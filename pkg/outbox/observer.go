package outbox

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isNoRows reports the empty-queue case of a LIMIT 1 probe.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

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
	// OnAlert receives alerts (kind, message, structured fields — age/topic/
	// pending counts, so a sink can render its own language without parsing
	// the sentence). Nil = no alerting. Each kind is rate-limited to one
	// alert per hour.
	OnAlert func(kind, message string, fields map[string]string)
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
	CollectedAt  time.Time `json:"collected_at"`
	Pending      int64     `json:"pending"`
	Failed       int64     `json:"failed"`
	Discarded    int64     `json:"discarded"` // parked by a processor's DECISION (reason in last_error); visible, never alerting
	SentLastHour int64     `json:"sent_last_hour"`
	// Capped reports that Pending/Failed hit the counting bound (countCap): the
	// real number is AT LEAST the reported one. Counting is bounded on purpose —
	// measured on a real backlog (868 962 pending rows, 1-vCPU box) an unbounded
	// count(*) cost ~1.1 s PER AGGREGATE every collection tick, so the observer
	// would have added serious background load exactly during the incident it
	// exists to surface. A queue at the cap is already every alarm firing.
	Capped             bool         `json:"capped,omitempty"`
	OldestPendingAge   float64      `json:"oldest_pending_age_seconds"` // 0 when no pending rows
	OldestFailedAge    float64      `json:"oldest_failed_age_seconds"`  // 0 when no failed rows
	OldestPendingTopic string       `json:"oldest_pending_topic,omitempty"`
	PendingByTopic     []TopicCount `json:"pending_by_topic,omitempty"` // omitted when Pending > topicBreakdownMax

	// Workflow health (ADR-031) — collected from public.workflow_runs /
	// public.workflow_cron when they exist (the engine ensures them at boot).
	// WorkflowOverdueSeconds is the headline: how far past due the most-overdue
	// cron schedule is. A growing value means NO LEADER IS FIRING — the worker is
	// down or none runs the scheduler — which is exactly the outbox's old silent
	// failure, transplanted; here it alerts instead.
	WorkflowRuns24h   int64 `json:"workflow_runs_24h"`
	WorkflowFailed24h int64 `json:"workflow_failed_24h"`
	// Per-row reminders (MOTOR-AGENDA-S1): claims fired in the last 24h and
	// the reminder runs that failed in the same window (a failed run releases
	// its claim and retries on the next sweep while inside grace).
	RemindersFired24h      int64   `json:"reminders_fired_24h"`
	RemindersFailed24h     int64   `json:"reminders_failed_24h"`
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

// countCap bounds every count the observer runs: the cost of counting must not
// grow with the size of the incident being counted. 100k is far past every
// alerting threshold; the Capped flag says "at least this many".
const countCap = 100000

// topicBreakdownMax bounds the per-topic GROUP BY, the one aggregate whose cost
// grows with backlog size (measured 1.1s over 869k rows): past this many
// pending rows the breakdown is skipped and only the O(1) oldest row is named.
const topicBreakdownMax = 20000

// Collect runs one collection pass with BOUNDED cost whatever the backlog size:
// the oldest-pending age (the headline) is an O(1) index probe, counts stop at
// countCap, and the per-topic breakdown only runs on backlogs small enough that
// a GROUP BY over the partial index is cheap.
func (o *Observer) Collect(ctx context.Context) (*Stats, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	s := &Stats{CollectedAt: time.Now()}
	now := time.Now()

	// Oldest pending: one index probe, never a scan.
	var oldestPending time.Time
	var oldestTopic string
	err := o.pool.QueryRow(ctx,
		`SELECT created_at, topic FROM public.outbox WHERE state = 'pending' ORDER BY created_at LIMIT 1`,
	).Scan(&oldestPending, &oldestTopic)
	switch {
	case err == nil:
		s.OldestPendingAge = now.Sub(oldestPending).Seconds()
		s.OldestPendingTopic = oldestTopic
	case isNoRows(err):
	default:
		return nil, fmt.Errorf("outbox: observe oldest pending: %w", err)
	}
	var oldestFailed time.Time
	err = o.pool.QueryRow(ctx,
		`SELECT created_at FROM public.outbox WHERE state = 'failed' ORDER BY created_at LIMIT 1`,
	).Scan(&oldestFailed)
	switch {
	case err == nil:
		s.OldestFailedAge = now.Sub(oldestFailed).Seconds()
	case isNoRows(err):
	default:
		return nil, fmt.Errorf("outbox: observe oldest failed: %w", err)
	}

	// Bounded counts: the subquery stops at countCap rows.
	boundedCount := func(state string) (int64, error) {
		var n int64
		err := o.pool.QueryRow(ctx, fmt.Sprintf(
			`SELECT count(*) FROM (SELECT 1 FROM public.outbox WHERE state = '%s' LIMIT %d) t`,
			state, countCap)).Scan(&n)
		return n, err
	}
	if s.Pending, err = boundedCount("pending"); err != nil {
		return nil, fmt.Errorf("outbox: observe pending: %w", err)
	}
	if s.Failed, err = boundedCount("failed"); err != nil {
		return nil, fmt.Errorf("outbox: observe failed: %w", err)
	}
	if s.Discarded, err = boundedCount("discarded"); err != nil {
		return nil, fmt.Errorf("outbox: observe discarded: %w", err)
	}
	s.Capped = s.Pending >= countCap || s.Failed >= countCap
	if err := o.pool.QueryRow(ctx,
		`SELECT count(*) FROM (SELECT 1 FROM public.outbox WHERE state = 'sent' AND sent_at > now() - interval '1 hour' LIMIT 100000) t`,
	).Scan(&s.SentLastHour); err != nil {
		return nil, fmt.Errorf("outbox: observe sent: %w", err)
	}

	if s.Pending > 0 && s.Pending <= topicBreakdownMax {
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
		var hasRem bool
		if err := o.pool.QueryRow(ctx, `SELECT to_regclass('public.workflow_reminders') IS NOT NULL`).Scan(&hasRem); err == nil && hasRem {
			if err := o.pool.QueryRow(ctx, `
				SELECT (SELECT count(*) FROM public.workflow_reminders WHERE fired_at > now() - interval '24 hours'),
				       (SELECT count(*) FROM public.workflow_runs WHERE trigger LIKE 'time:%' AND status = 'failed' AND started_at > now() - interval '24 hours')`,
			).Scan(&s.RemindersFired24h, &s.RemindersFailed24h); err != nil {
				return nil, fmt.Errorf("outbox: observe reminders: %w", err)
			}
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
	fire := func(kind, msg string, fields map[string]string) {
		if time.Since(o.lastAlert[kind]) < cooldown {
			return
		}
		o.lastAlert[kind] = time.Now()
		o.cfg.OnAlert(kind, msg, fields)
	}
	if o.cfg.MaxPendingAge > 0 && s.OldestPendingAge > o.cfg.MaxPendingAge.Seconds() {
		age := (time.Duration(s.OldestPendingAge) * time.Second).Round(time.Second)
		fire("outbox_stale_pending", fmt.Sprintf(
			"outbox: the oldest pending event is %s old (threshold %s; topic %q, %d pending). Nothing is draining it — check that appximo-worker is running and has a consumer for that topic. Details: GET /admin/outbox",
			age, o.cfg.MaxPendingAge, s.OldestPendingTopic, s.Pending),
			map[string]string{
				"age_s":       fmt.Sprintf("%.0f", s.OldestPendingAge),
				"topic":       s.OldestPendingTopic,
				"pending":     fmt.Sprintf("%d", s.Pending),
				"threshold_s": fmt.Sprintf("%.0f", o.cfg.MaxPendingAge.Seconds()),
			})
	}
	if s.Failed > 0 {
		oldest := (time.Duration(s.OldestFailedAge) * time.Second).Round(time.Second)
		fire("outbox_failed", fmt.Sprintf(
			"outbox: %d event(s) parked state='failed' (retries exhausted; oldest %s old). Each row carries its error in last_error — GET /admin/outbox lists them",
			s.Failed, oldest),
			map[string]string{"failed": fmt.Sprintf("%d", s.Failed), "oldest_failed_age_s": fmt.Sprintf("%.0f", s.OldestFailedAge)})
	}
	// A cron schedule more than 10 minutes past due means NO leader is firing —
	// the worker is down, or none of the running workers has the scheduler.
	if s.WorkflowOverdueSeconds > (10 * time.Minute).Seconds() {
		overdue := (time.Duration(s.WorkflowOverdueSeconds) * time.Second).Round(time.Second)
		fire("workflow_overdue", fmt.Sprintf(
			"workflows: a cron schedule is %s past due — no scheduler is firing (is appximo-worker running?). Details: GET /admin/workflows",
			overdue),
			map[string]string{"overdue_s": fmt.Sprintf("%.0f", s.WorkflowOverdueSeconds)})
	}
	if s.WorkflowFailed24h > 0 {
		fire("workflow_failed", fmt.Sprintf(
			"workflows: %d failed run(s) in the last 24h — each run's error and per-step detail is in GET /admin/workflows (public.workflow_runs)",
			s.WorkflowFailed24h),
			map[string]string{"failed_24h": fmt.Sprintf("%d", s.WorkflowFailed24h)})
	}
}
