package migration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ── container helpers ─────────────────────────────────────────────────────────

func startPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("connection string: %v", err)
	}
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("new pool: %v", err)
	}
	return pool, func() {
		pool.Close()
		ctr.Terminate(ctx)
	}
}

func startRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	rc, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:7-alpine",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections"),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start redis: %v", err)
	}
	host, _ := rc.Host(ctx)
	port, _ := rc.MappedPort(ctx, "6379/tcp")
	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%s", host, port.Port()),
	})
	return rdb, func() {
		rdb.Close()
		rc.Terminate(ctx)
	}
}

func applyControlPlane(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		t.Fatalf("read control plane migration: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply control plane migration: %v", err)
	}
}

// newWorker returns a worker with fast timings for tests.
func newWorker(rdb *redis.Client, pool *pgxpool.Pool) *migration.MigrationWorker {
	w := migration.NewMigrationWorker(rdb, pool, tenant.NewSchemaCache())
	w.BlockTime = 100 * time.Millisecond
	w.BackoffBase = 10 * time.Millisecond    // error-retry delays: 10ms / 20ms / 40ms
	w.LockRetryDelay = 50 * time.Millisecond // lock-contention delay
	return w
}

// seedTenant inserts a row in public.tenants and creates the pg schema.
func seedTenant(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	ctx := context.Background()
	pgSchema := "tenant_" + tenantID
	_, err := pool.Exec(ctx, `
		INSERT INTO public.tenants
		  (id, pg_schema, display_name, email, plan, json_schema, created_at, updated_at)
		VALUES ($1, $2, $1, $1||'@test.com', 'free', '{}', now(), now())`,
		tenantID, pgSchema)
	if err != nil {
		t.Fatalf("seed tenant %q: %v", tenantID, err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", pgSchema)); err != nil {
		t.Fatalf("create schema %q: %v", pgSchema, err)
	}
}

// itemsSchema returns a minimal valid schema JSON string with an "items" resource.
func itemsSchema() string {
	s := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test",
		Resources: map[string]schema.ResourceSchema{
			"items": {Fields: map[string]schema.FieldDef{
				"name":   {Type: "string", Required: true},
				"status": {Type: "string"},
			}},
		},
	}
	b, _ := json.Marshal(s)
	return string(b)
}

// waitFor polls cond until it returns true or the deadline is exceeded.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestMigrationWorker_ValidJob verifies that a well-formed job creates the
// expected table and is acknowledged from the consumer group.
func TestMigrationWorker_ValidJob(t *testing.T) {
	pool, cleanPG := startPostgres(t)
	defer cleanPG()
	rdb, cleanRedis := startRedis(t)
	defer cleanRedis()

	applyControlPlane(t, pool)
	seedTenant(t, pool, "validtenant")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: migration.StreamName,
		Values: map[string]any{
			"tenant_id":   "validtenant",
			"schema":      itemsSchema(),
			"retry_count": "0",
		},
	})

	w := newWorker(rdb, pool)
	go w.Run(ctx)

	// Wait until migration_log shows "ok".
	ok := waitFor(t, 10*time.Second, func() bool {
		var status string
		pool.QueryRow(ctx,
			"SELECT status FROM public.migration_log WHERE tenant_id=$1 ORDER BY id DESC LIMIT 1",
			"validtenant",
		).Scan(&status)
		return status == "ok"
	})
	if !ok {
		t.Fatal("migration_log never reached status 'ok'")
	}

	// Table must exist in the tenant schema.
	var exists bool
	pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2)",
		"tenant_validtenant", "items",
	).Scan(&exists)
	if !exists {
		t.Error("table tenant_validtenant.items not found after migration")
	}

	// Consumer group PEL must be empty (message was XAcked).
	pending, err := rdb.XPending(ctx, migration.StreamName, migration.ConsumerGroup).Result()
	if err != nil {
		t.Fatalf("xpending: %v", err)
	}
	if pending.Count != 0 {
		t.Errorf("expected 0 pending messages, got %d", pending.Count)
	}
}

// TestMigrationWorker_InvalidTenant verifies that a job referencing a
// non-existent tenant is gracefully discarded and the loop continues to
// process subsequent messages.
func TestMigrationWorker_InvalidTenant(t *testing.T) {
	pool, cleanPG := startPostgres(t)
	defer cleanPG()
	rdb, cleanRedis := startRedis(t)
	defer cleanRedis()

	applyControlPlane(t, pool)
	seedTenant(t, pool, "goodtenant") // this tenant has a valid pg schema

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Message 1: ghost tenant at max retries → immediate discard without re-enqueue.
	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: migration.StreamName,
		Values: map[string]any{
			"tenant_id":   "ghosttenant",
			"schema":      itemsSchema(),
			"retry_count": "3", // already at maxRetries → discard immediately
		},
	})
	// Message 2: valid tenant → should be processed after message 1.
	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: migration.StreamName,
		Values: map[string]any{
			"tenant_id":   "goodtenant",
			"schema":      itemsSchema(),
			"retry_count": "0",
		},
	})

	w := newWorker(rdb, pool)
	go w.Run(ctx)

	// Loop continued if message 2 was processed successfully.
	ok := waitFor(t, 10*time.Second, func() bool {
		var status string
		pool.QueryRow(ctx,
			"SELECT status FROM public.migration_log WHERE tenant_id=$1 ORDER BY id DESC LIMIT 1",
			"goodtenant",
		).Scan(&status)
		return status == "ok"
	})
	if !ok {
		t.Fatal("loop did not continue to process the second message after the error")
	}

	// PEL must be clear (both messages XAcked).
	pending, _ := rdb.XPending(ctx, migration.StreamName, migration.ConsumerGroup).Result()
	if pending.Count != 0 {
		t.Errorf("expected 0 pending messages, got %d", pending.Count)
	}
}

// TestMigrationWorker_MaxRetriesDiscarded verifies that a job that fails
// maxRetries+1 times is discarded with status "failed" in migration_log.
//
// Setup: the tenant row exists (FK valid for migration_log) but the pg schema
// does NOT — so every ApplyTenantMigration call fails deterministically.
func TestMigrationWorker_MaxRetriesDiscarded(t *testing.T) {
	pool, cleanPG := startPostgres(t)
	defer cleanPG()
	rdb, cleanRedis := startRedis(t)
	defer cleanRedis()

	applyControlPlane(t, pool)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Insert tenant row only — no CREATE SCHEMA, so every migration call fails.
	pool.Exec(ctx, `
		INSERT INTO public.tenants
		  (id, pg_schema, display_name, email, plan, json_schema, created_at, updated_at)
		VALUES ('retrytenant','tenant_retrytenant','Retry','r@t.com','free','{}',now(),now())`)

	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: migration.StreamName,
		Values: map[string]any{
			"tenant_id":   "retrytenant",
			"schema":      itemsSchema(),
			"retry_count": "0",
		},
	})

	w := newWorker(rdb, pool)
	go w.Run(ctx)

	// With BackoffBase=10ms: retries add 10+20+40 = 70ms + processing time.
	ok := waitFor(t, 15*time.Second, func() bool {
		var status string
		pool.QueryRow(ctx,
			"SELECT status FROM public.migration_log WHERE tenant_id=$1 ORDER BY id DESC LIMIT 1",
			"retrytenant",
		).Scan(&status)
		return status == "failed"
	})
	if !ok {
		t.Fatal("migration_log never reached status 'failed' after max retries")
	}

	// PEL must be empty — all re-enqueued messages eventually XAcked too.
	ok = waitFor(t, 5*time.Second, func() bool {
		p, _ := rdb.XPending(ctx, migration.StreamName, migration.ConsumerGroup).Result()
		return p.Count == 0
	})
	if !ok {
		t.Error("expected empty PEL after all retries exhausted")
	}
}

// TestMigrationWorker_TwoWorkersSameTenant verifies that two simultaneous workers
// processing jobs for the same tenant:
//   - Never run migrations concurrently (advisory lock prevents it)
//   - Do not lose any job (all 5 eventually appear in migration_log)
//   - Leave the database in a consistent state
func TestMigrationWorker_TwoWorkersSameTenant(t *testing.T) {
	pool, cleanPG := startPostgres(t)
	defer cleanPG()
	rdb, cleanRedis := startRedis(t)
	defer cleanRedis()

	applyControlPlane(t, pool)
	seedTenant(t, pool, "locktenant")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Enqueue 5 identical jobs for the SAME tenant.
	for i := 0; i < 5; i++ {
		rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: migration.StreamName,
			Values: map[string]any{
				"tenant_id":   "locktenant",
				"schema":      itemsSchema(),
				"retry_count": "0",
			},
		})
	}

	// Start 2 workers with UNIQUE consumer names so Redis distributes messages
	// between them, and fast timings so the test completes quickly.
	w1 := newWorker(rdb, pool)
	w1.ConsumerName = "test-worker-1"

	w2 := newWorker(rdb, pool)
	w2.ConsumerName = "test-worker-2"

	go w1.Run(ctx)
	go w2.Run(ctx)

	// Wait until all 5 jobs have been processed successfully.
	// Each job (including any re-enqueues from lock contention) eventually
	// produces exactly one "ok" entry in migration_log.
	ok := waitFor(t, 30*time.Second, func() bool {
		var count int
		pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.migration_log
			 WHERE tenant_id = 'locktenant' AND status = 'ok'`,
		).Scan(&count)
		return count >= 5
	})
	if !ok {
		// Report how many were processed to help diagnose.
		var count int
		pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM public.migration_log WHERE tenant_id='locktenant' AND status='ok'",
		).Scan(&count)
		t.Fatalf("two workers did not process all 5 jobs within timeout (got %d/5 ok entries)", count)
	}

	// PEL must be empty — no messages stuck awaiting acknowledgment.
	pelEmpty := waitFor(t, 10*time.Second, func() bool {
		p, _ := rdb.XPending(ctx, migration.StreamName, migration.ConsumerGroup).Result()
		return p.Count == 0
	})
	if !pelEmpty {
		p, _ := rdb.XPending(ctx, migration.StreamName, migration.ConsumerGroup).Result()
		t.Errorf("expected empty PEL, got %d pending messages", p.Count)
	}

	// Table must exist and accept writes — proves the schema is consistent.
	var exists bool
	pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM information_schema.tables
		 WHERE table_schema='tenant_locktenant' AND table_name='items')`,
	).Scan(&exists)
	if !exists {
		t.Error("items table not found after two-worker migration")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenant_locktenant.items (name) VALUES ('final-check')`,
	); err != nil {
		t.Errorf("final INSERT failed — schema inconsistent: %v", err)
	}
}
