package workflows

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
)

var (
	testPool *pgxpool.Pool
	testDSN  string
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run()) // DB tests self-skip via requirePG
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("wf"), tcpostgres.WithUsername("wf"), tcpostgres.WithPassword("wf"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "workflows test: start postgres:", err)
		os.Exit(1)
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err == nil {
		testDSN = dsn
		testPool, err = pgxpool.New(ctx, dsn)
	}
	if err == nil {
		err = EnsureTables(ctx, testPool)
		if err == nil {
			err = outbox.EnsureTable(ctx, testPool) // the reminder tests enqueue on it
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "workflows test setup:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	code := m.Run()
	testPool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

func requirePG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("workflows integration test: needs Docker; skipped in -short")
	}
	if testPool == nil {
		t.Fatal("shared pool not initialized")
	}
	for _, tbl := range []string{"workflow_runs", "workflow_cron", "workflow_reminders", "outbox"} {
		if _, err := testPool.Exec(context.Background(), "TRUNCATE public."+tbl); err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	return testPool
}

type fixedLister struct{ entries []CronEntry }

func (f fixedLister) CronEntries() []CronEntry { return f.entries }

func testCronWorkflow(t *testing.T, spec, overlap string) *Workflow {
	t.Helper()
	sched, err := schema.WorkflowCronParser.Parse(spec)
	if err != nil {
		t.Fatalf("parse cron: %v", err)
	}
	return &Workflow{
		Name: "wf", TriggerType: "cron", CronSpec: spec,
		Schedule: sched, Location: time.UTC, OverlapAllow: overlap == "allow",
	}
}

// TestScheduler_LeaderElection pins provocation E8: with TWO scheduler instances
// on one database, ONLY the advisory-lock holder fires; when it dies, the other
// takes over.
func TestScheduler_LeaderElection(t *testing.T) {
	pool := requirePG(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connect := func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, testDSN) }
	wf := testCronWorkflow(t, "@every 1s", "allow")
	// A schedule already due, so the leader fires on its first tick.
	store := NewStore(pool)
	if err := store.UpsertSchedule(ctx, "wf", "acme", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("seed schedule: %v", err)
	}

	var ran1, ran2 atomic.Int64
	mk := func(counter *atomic.Int64, ctx context.Context) *Scheduler {
		s := &Scheduler{
			Connect:  connect,
			Src:      fixedLister{[]CronEntry{{Tenant: "acme", Workflow: wf}}},
			Store:    store,
			Log:      zerolog.Nop(),
			Interval: 200 * time.Millisecond,
			Retry:    200 * time.Millisecond,
			RunFn: func(context.Context, string, *Workflow, string, map[string]any) error {
				counter.Add(1)
				return nil
			},
		}
		go s.Run(ctx)
		return s
	}

	ctx1, cancel1 := context.WithCancel(ctx)
	mk(&ran1, ctx1)
	// Give the first instance time to take the lock, then start the rival.
	time.Sleep(400 * time.Millisecond)
	mk(&ran2, ctx)

	deadline := time.Now().Add(5 * time.Second)
	for ran1.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if ran1.Load() == 0 {
		t.Fatal("the first scheduler (leader) never fired")
	}
	if ran2.Load() != 0 {
		t.Fatalf("the NON-leader fired %d times — two leaders at once", ran2.Load())
	}

	// Kill the leader (its ctx closes its lock connection → the lock releases);
	// the rival must take over and fire.
	cancel1()
	deadline = time.Now().Add(10 * time.Second)
	for ran2.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if ran2.Load() == 0 {
		t.Fatal("after the leader died, the second scheduler never took over")
	}
}

// TestScheduler_OverlapSkip pins provocation E9: with overlap "skip" (default),
// an occurrence due while the previous run still executes is recorded as
// skipped_overlap and NOT started; with "allow" it runs concurrently.
func TestScheduler_OverlapSkip(t *testing.T) {
	pool := requirePG(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := NewStore(pool)
	wf := testCronWorkflow(t, "@every 1s", "skip")
	if err := store.UpsertSchedule(ctx, "wf", "acme", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("seed: %v", err)
	}

	release := make(chan struct{})
	var started atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	s := &Scheduler{
		Connect:  func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, testDSN) },
		Src:      fixedLister{[]CronEntry{{Tenant: "acme", Workflow: wf}}},
		Store:    store,
		Log:      zerolog.Nop(),
		Interval: 300 * time.Millisecond,
		Retry:    200 * time.Millisecond,
		RunFn: func(ctx context.Context, _ string, _ *Workflow, _ string, _ map[string]any) error {
			if started.Add(1) == 1 {
				defer wg.Done()
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil
		},
	}
	go s.Run(ctx)

	// The first run starts and BLOCKS; the @every 1s schedule keeps coming due.
	// With "skip", no second run may start while it blocks — the occurrences
	// must land as skipped_overlap rows.
	deadline := time.Now().Add(5 * time.Second)
	for started.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if started.Load() == 0 {
		t.Fatal("first run never started")
	}

	var skips int64
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.workflow_runs WHERE status = 'skipped_overlap'`).Scan(&skips); err != nil {
			t.Fatalf("count skips: %v", err)
		}
		if skips >= 1 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if skips == 0 {
		t.Fatal("no skipped_overlap run was recorded while the previous run blocked")
	}
	if got := started.Load(); got != 1 {
		t.Fatalf("a second run STARTED while the first was executing (started=%d) — overlap skip broken", got)
	}
	close(release)
	wg.Wait()
}

// VOZ-DELTA-S1 (found on the 58): a deployed cron whose SPEC changes must be
// re-armed at the next tick. The old ON CONFLICT DO NOTHING kept the stale
// next_run — a daily "0 7" moved to "38 14" would only take effect after the
// old 07:00 had passed.
func TestScheduler_ChangedSpecReArms(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()
	if err := EnsureTables(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool)
	_, _ = pool.Exec(ctx, `DELETE FROM public.workflow_cron WHERE workflow='rearm'`)
	tomorrow := time.Now().Add(24 * time.Hour)
	if err := store.UpsertScheduleSpec(ctx, "rearm", "acme", "0 7 * * * UTC", tomorrow); err != nil {
		t.Fatal(err)
	}
	// Same spec, a restart: next_run must NOT move.
	if err := store.UpsertScheduleSpec(ctx, "rearm", "acme", "0 7 * * * UTC", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var next time.Time
	if err := pool.QueryRow(ctx, `SELECT next_run FROM public.workflow_cron WHERE workflow='rearm'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next.Sub(tomorrow).Abs() > time.Second {
		t.Fatalf("unchanged spec must keep next_run: %v vs %v", next, tomorrow)
	}
	// Changed spec: re-armed to the new next.
	soon := time.Now().Add(2 * time.Minute)
	if err := store.UpsertScheduleSpec(ctx, "rearm", "acme", "38 14 * * * UTC", soon); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT next_run FROM public.workflow_cron WHERE workflow='rearm'`).Scan(&next); err != nil {
		t.Fatal(err)
	}
	if next.Sub(soon).Abs() > time.Second {
		t.Fatalf("changed spec must re-arm next_run: %v vs %v", next, soon)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM public.workflow_cron WHERE workflow='rearm'`)
}
