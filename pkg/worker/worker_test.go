package worker

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/outbox"
)

// One throwaway Postgres container is shared by every integration test in this
// package. Drain claims rows GLOBALLY (WHERE state='pending', not per-tenant), so
// two tests draining the same table concurrently would steal each other's rows —
// hence these tests are SEQUENTIAL (no t.Parallel) and each truncates the table
// first via requirePG. The backoff unit test below needs no DB and runs in -short.
var (
	testPool *pgxpool.Pool
	testDSN  string
)

func TestMain(m *testing.M) {
	flag.Parse() // make testing.Short() readable before m.Run()
	if testing.Short() {
		os.Exit(m.Run()) // integration tests self-skip via requirePG; unit tests run
	}

	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker test: start postgres:", err)
		os.Exit(1)
	}

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker test: connection string:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker test: parse config:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	cfg.MaxConns = 10 // headroom for the concurrent-drain test
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "worker test: new pool:", err)
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}
	if err := outbox.EnsureTable(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "worker test: ensure table:", err)
		pool.Close()
		_ = ctr.Terminate(ctx)
		os.Exit(1)
	}

	testPool, testDSN = pool, dsn
	code := m.Run()
	pool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

// requirePG skips in -short, otherwise returns the shared pool with an empty
// outbox so each test sees only its own rows.
func requirePG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("worker integration test: needs Docker (testcontainers); skipped in -short")
	}
	if testPool == nil {
		t.Fatal("worker test: shared pool not initialized (TestMain setup failed)")
	}
	if _, err := testPool.Exec(context.Background(), `TRUNCATE public.outbox RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate outbox: %v", err)
	}
	return testPool
}

// enqueue commits one outbox row via the real engine path (outbox.Enqueue, which
// also emits the NOTIFY) and returns its id.
func enqueue(t *testing.T, pool *pgxpool.Pool, tenantID, topic string, payload any) int64 {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	id, err := outbox.Enqueue(ctx, tx, tenantID, topic, payload)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id
}

// rowState reads the delivery state of one outbox row.
func rowState(t *testing.T, pool *pgxpool.Pool, id int64) (state string, sentAt *time.Time, attempts int) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT state, sent_at, attempts FROM public.outbox WHERE id=$1`, id,
	).Scan(&state, &sentAt, &attempts); err != nil {
		t.Fatalf("read row %d: %v", id, err)
	}
	return state, sentAt, attempts
}

// TestWorker_ProcessesPendingRow: a pending row, drained once, ends sent_at NOT
// NULL / state='sent' / attempts=1, and the Processor saw the full row.
func TestWorker_ProcessesPendingRow(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	id := enqueue(t, pool, "tenant-x", "echo.test", map[string]any{"msg": "hello"})

	var got Row
	proc := ProcessorFunc(func(_ context.Context, row Row) error {
		got = row
		return nil
	})

	res, err := Drain(ctx, pool, proc, 50, 5, nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if res.Processed != 1 || res.Failed != 0 {
		t.Fatalf("drain result = %+v, want 1 processed / 0 failed", res)
	}
	if got.ID != id || got.TenantID != "tenant-x" || got.Topic != "echo.test" {
		t.Errorf("processor saw %+v, want id=%d tenant-x echo.test", got, id)
	}
	if !strings.Contains(string(got.Payload), "hello") {
		t.Errorf("payload = %s, want it to contain hello", got.Payload)
	}

	state, sentAt, attempts := rowState(t, pool, id)
	if state != "sent" {
		t.Errorf("state = %q, want sent", state)
	}
	if sentAt == nil {
		t.Errorf("sent_at is NULL, want set (the delivery barrier)")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

// TestWorker_SkipLockedConcurrent: many rows, several goroutines draining the same
// table concurrently — every row processed EXACTLY once. This is the core
// isolation guarantee of FOR UPDATE SKIP LOCKED, run with -race.
func TestWorker_SkipLockedConcurrent(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	const (
		tenantID  = "concurrent"
		topic     = "skiplocked.test"
		nRows     = 200
		nWorkers  = 4
		batchSize = 10
	)
	for i := 0; i < nRows; i++ {
		enqueue(t, pool, tenantID, topic, map[string]any{"n": i})
	}

	var mu sync.Mutex
	counts := map[int64]int{} // id → times processed

	proc := ProcessorFunc(func(_ context.Context, row Row) error {
		mu.Lock()
		counts[row.ID]++
		mu.Unlock()
		return nil
	})

	var wg sync.WaitGroup
	for w := 0; w < nWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				res, err := Drain(ctx, pool, proc, batchSize, 5, nil)
				if err != nil {
					t.Errorf("concurrent drain: %v", err) // Errorf: safe off-goroutine
					return
				}
				if res.Claimed() == 0 {
					return // queue empty from this goroutine's view
				}
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(counts) != nRows {
		t.Errorf("processed %d distinct rows, want %d", len(counts), nRows)
	}
	for id, c := range counts {
		if c != 1 {
			t.Errorf("row %d processed %d times, want exactly 1 (double processing)", id, c)
		}
	}

	var notSent int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.outbox WHERE state <> 'sent'`,
	).Scan(&notSent); err != nil {
		t.Fatalf("count not-sent: %v", err)
	}
	if notSent != 0 {
		t.Errorf("%d rows not marked sent", notSent)
	}
}

// TestWorker_RollbackKeepsPending: a failing Processor leaves the row pending
// (sent_at NULL) with attempts incremented — it will be retried. Drain itself
// returns nil; the failure is recorded, not propagated.
func TestWorker_RollbackKeepsPending(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	id := enqueue(t, pool, "tenant-fail", "echo.test", map[string]any{"x": 1})

	proc := ProcessorFunc(func(_ context.Context, _ Row) error {
		return errors.New("boom")
	})

	res, err := Drain(ctx, pool, proc, 50, 5, nil)
	if err != nil {
		t.Fatalf("drain should not error on a row failure: %v", err)
	}
	if res.Failed != 1 || res.Processed != 0 {
		t.Fatalf("drain result = %+v, want 1 failed / 0 processed", res)
	}

	state, sentAt, attempts := rowState(t, pool, id)
	if state != "pending" {
		t.Errorf("state = %q, want pending (retryable)", state)
	}
	if sentAt != nil {
		t.Errorf("sent_at is set, want NULL (not delivered)")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (incremented for retry)", attempts)
	}
}

// TestWorker_MaxAttemptsFails: after maxAttempts failures the row is parked in
// state='failed' and never re-claimed.
func TestWorker_MaxAttemptsFails(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	const maxAttempts = 3
	id := enqueue(t, pool, "tenant-poison", "echo.test", map[string]any{"x": 1})

	proc := ProcessorFunc(func(_ context.Context, _ Row) error {
		return errors.New("always fails")
	})

	for i := 1; i <= maxAttempts; i++ {
		res, err := Drain(ctx, pool, proc, 50, maxAttempts, nil)
		if err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
		if res.Failed != 1 {
			t.Fatalf("drain %d: result = %+v, want 1 failed", i, res)
		}
	}

	state, sentAt, attempts := rowState(t, pool, id)
	if state != "failed" {
		t.Errorf("state = %q, want failed after %d attempts", state, maxAttempts)
	}
	if attempts != maxAttempts {
		t.Errorf("attempts = %d, want %d", attempts, maxAttempts)
	}
	if sentAt != nil {
		t.Errorf("sent_at is set, want NULL (never delivered)")
	}

	// A failed row must NOT be re-claimed.
	res, err := Drain(ctx, pool, proc, 50, maxAttempts, nil)
	if err != nil {
		t.Fatalf("drain after park: %v", err)
	}
	if res.Claimed() != 0 {
		t.Errorf("failed row re-claimed: %+v, want 0 claimed", res)
	}
	if _, _, a2 := rowState(t, pool, id); a2 != maxAttempts {
		t.Errorf("attempts changed to %d after park, want %d (no further retries)", a2, maxAttempts)
	}
}

// pidRecorder is a Connector that opens real dedicated connections and records the
// backend PID of each, so a test can terminate the worker's connections server-side.
type pidRecorder struct {
	dsn  string
	mu   sync.Mutex
	pids []uint32
}

func (p *pidRecorder) connect(ctx context.Context) (*pgx.Conn, error) {
	c, err := pgx.Connect(ctx, p.dsn)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.pids = append(p.pids, c.PgConn().PID())
	p.mu.Unlock()
	return c, nil
}

func (p *pidRecorder) snapshot() []uint32 {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]uint32, len(p.pids))
	copy(out, p.pids)
	return out
}

// waitSent polls until row id has sent_at set, or fails after timeout.
func waitSent(t *testing.T, pool *pgxpool.Pool, id int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var sentAt *time.Time
		if err := pool.QueryRow(context.Background(),
			`SELECT sent_at FROM public.outbox WHERE id=$1`, id,
		).Scan(&sentAt); err == nil && sentAt != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("row %d not sent within %s", id, timeout)
}

// TestWorker_Reconnect: kill the running worker's backend connections; it must
// reconnect (capped backoff) and keep draining. Integration — proves the real
// reconnection path, not just the backoff math (see TestNextBackoff for that).
func TestWorker_Reconnect(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, testDSN)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer admin.Close(ctx)

	rec := &pidRecorder{dsn: testDSN}
	proc := ProcessorFunc(func(_ context.Context, _ Row) error { return nil })

	// Snappy timings so the test runs fast.
	w := New(rec.connect, proc, Config{
		PollInterval: 300 * time.Millisecond,
		BackoffMin:   50 * time.Millisecond,
		BackoffMax:   200 * time.Millisecond,
	}, zerolog.Nop())

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = w.Run(runCtx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// 1. Worker is up and draining.
	idA := enqueue(t, pool, "tenant-recon", "echo.test", map[string]any{"a": 1})
	waitSent(t, pool, idA, 10*time.Second)

	// 2. Terminate the worker's listen + drain backends.
	pids := rec.snapshot()
	if len(pids) == 0 {
		t.Fatal("no worker connections were recorded")
	}
	for _, pid := range pids {
		// Ignore errors: the backend may already be gone, and pg_terminate_backend
		// can race with the connection teardown.
		_, _ = admin.Exec(ctx, `SELECT pg_terminate_backend($1)`, pid)
	}

	// 3. After reconnecting, the worker still drains a freshly enqueued row.
	idB := enqueue(t, pool, "tenant-recon", "echo.test", map[string]any{"b": 2})
	waitSent(t, pool, idB, 15*time.Second)
}

// TestNextBackoff guards the capped exponential backoff math. No Docker — runs in
// -short too, as a fast backstop for the reconnect logic.
func TestNextBackoff(t *testing.T) {
	t.Parallel()
	const max = 30 * time.Second
	cases := []struct {
		cur, want time.Duration
	}{
		{1 * time.Second, 2 * time.Second},
		{2 * time.Second, 4 * time.Second},
		{8 * time.Second, 16 * time.Second},
		{16 * time.Second, max}, // 32s capped to 30s
		{max, max},              // stays at the ceiling
	}
	for _, c := range cases {
		if got := nextBackoff(c.cur, max); got != c.want {
			t.Errorf("nextBackoff(%s, %s) = %s, want %s", c.cur, max, got, c.want)
		}
	}
}
