package workflows

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
)

// LeaderLockKey is the pg_advisory_lock key that elects the ONE cron scheduler
// among N workers ("apxmwkfl" as int64). Leadership is a session-level advisory
// lock on a dedicated connection: no Redis, no etcd — the Postgres every install
// already has (A-70). Losing the connection loses the lock, and another worker
// takes over within a retry interval.
const LeaderLockKey int64 = 0x6170786d776b666c

// Scheduler drives cron-triggered workflows. Only the LEADER runs the loop;
// every worker competes for the advisory lock and non-leaders retry quietly.
//
// The firing model (vocabulary borrowed from K8s CronJob and Temporal, per the
// research): schedules persist in public.workflow_cron (workflow, tenant,
// next_run, last_run). Every tick the leader reconciles the rows against the
// declared workflows and fires the DUE ones. A due schedule runs ONCE however
// late it is (missed occurrences COLLAPSE into one catch-up run — the K8s
// "catch up one" shape: a worker down for three days does not replay 72 hourly
// runs). next_run is advanced BEFORE the run starts, so a crash mid-run never
// double-fires an occurrence. Overlap: with "skip" (default) an occurrence due
// while the previous run of the same (tenant, workflow) is still executing is
// recorded as skipped_overlap — a visible row, not a silent nothing; "allow"
// lets them overlap. DST is handled by Workflow.NextAfter (see its comment and
// ADR-031 §DST).
type Scheduler struct {
	Connect  func(ctx context.Context) (*pgx.Conn, error) // dedicated conn factory (the worker's Connector)
	Src      CronLister
	Exec     *Executor
	Store    *Store
	Log      zerolog.Logger
	Interval time.Duration // tick; default 30s
	Retry    time.Duration // non-leader retry; default 15s

	// RunFn executes one cron run; nil = Exec.Run. Injectable so the leadership
	// and overlap semantics are pinned by tests without a full executor.
	RunFn func(ctx context.Context, tenant string, wf *Workflow, trigger string, env map[string]any) error

	mu       sync.Mutex
	inflight map[string]bool // "tenant\x00workflow" → a run is executing
	wg       sync.WaitGroup
}

// CronLister yields the current (tenant, cron workflow) pairs — implemented by
// *Source; an interface so tests can inject a fixed set.
type CronLister interface {
	CronEntries() []CronEntry
}

// Run competes for leadership and drives the loop until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	if s.Interval <= 0 {
		s.Interval = 30 * time.Second
	}
	if s.Retry <= 0 {
		s.Retry = 15 * time.Second
	}
	s.inflight = map[string]bool{}

	for ctx.Err() == nil {
		conn, err := s.Connect(ctx)
		if err != nil {
			s.sleep(ctx, s.Retry)
			continue
		}
		var got bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, LeaderLockKey).Scan(&got); err != nil || !got {
			conn.Close(context.Background())
			s.sleep(ctx, s.Retry)
			continue
		}
		s.Log.Info().Int64("lock", LeaderLockKey).Msg("workflows: cron leadership acquired (pg_try_advisory_lock)")
		s.lead(ctx, conn)
		conn.Close(context.Background()) // releases the advisory lock with the session
		if ctx.Err() == nil {
			s.Log.Warn().Msg("workflows: cron leadership lost; re-competing")
		}
	}
	s.wg.Wait()
}

// lead runs ticks while the lock connection stays alive.
func (s *Scheduler) lead(ctx context.Context, lockConn *pgx.Conn) {
	t := time.NewTicker(s.Interval)
	defer t.Stop()
	s.tick(ctx) // first tick immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Leadership is the lock CONNECTION: ping it; a dead conn = lost lock.
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := lockConn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
			s.tick(ctx)
		}
	}
}

// tick reconciles schedules and fires due ones.
func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now()
	entries := s.Src.CronEntries()

	// Reconcile: every declared (workflow, tenant) has a row; stale rows go.
	keep := make(map[string]bool, len(entries))
	byKey := make(map[string]CronEntry, len(entries))
	for _, e := range entries {
		k := e.Workflow.Name + "\x00" + e.Tenant
		keep[k] = true
		byKey[k] = e
		if err := s.Store.UpsertScheduleSpec(ctx, e.Workflow.Name, e.Tenant, e.Workflow.CronSpec+" "+e.Workflow.Location.String(), e.Workflow.NextAfter(now)); err != nil {
			s.Log.Warn().Err(err).Str("workflow", e.Workflow.Name).Str("tenant", e.Tenant).Msg("workflows: schedule upsert failed")
			return
		}
	}
	if err := s.Store.PruneSchedules(ctx, keep); err != nil {
		s.Log.Warn().Err(err).Msg("workflows: schedule prune failed")
	}

	due, err := s.Store.DueSchedules(ctx, now)
	if err != nil {
		s.Log.Warn().Err(err).Msg("workflows: due query failed")
		return
	}
	for _, row := range due {
		k := row.Workflow + "\x00" + row.Tenant
		entry, ok := byKey[k]
		if !ok {
			continue // pruned this tick
		}
		next := entry.Workflow.NextAfter(now)
		// Advance FIRST: however this firing ends, the occurrence is consumed.
		if err := s.Store.Advance(ctx, row.Workflow, row.Tenant, now, next); err != nil {
			s.Log.Warn().Err(err).Str("workflow", row.Workflow).Msg("workflows: advance failed; skipping this firing")
			continue
		}
		if !entry.Workflow.OverlapAllow && s.isInflight(k) {
			s.Store.RecordSkip(ctx, row.Tenant, row.Workflow, "cron",
				"previous run still executing (overlap: skip)")
			s.Log.Warn().Str("workflow", row.Workflow).Str("tenant", row.Tenant).
				Msg("workflows: occurrence skipped — previous run still executing (overlap: skip)")
			continue
		}
		s.setInflight(k, true)
		wf := entry.Workflow
		tenant := entry.Tenant
		runFn := s.RunFn
		if runFn == nil {
			runFn = s.Exec.Run
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.setInflight(k, false)
			env := map[string]any{
				"tenant": tenant,
				"now":    time.Now().UTC(),
				"event":  nil,
				"record": nil,
			}
			// A cron run's failure is recorded in workflow_runs (and the metrics
			// alert on failed runs); there is no outbox row to retry, so the next
			// occurrence is the retry — Temporal's cron semantics.
			_ = runFn(ctx, tenant, wf, "cron", env)
		}()
	}
}

func (s *Scheduler) isInflight(k string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inflight[k]
}

func (s *Scheduler) setInflight(k string, v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v {
		s.inflight[k] = true
	} else {
		delete(s.inflight, k)
	}
}

func (s *Scheduler) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
