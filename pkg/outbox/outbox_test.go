package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/outbox"
)

// startPostgres spins up a throwaway Postgres container for each test run.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("outbox integration test: needs Docker (testcontainers); skipped in -short")
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
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { ctr.Terminate(ctx) }) //nolint:errcheck

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := outbox.EnsureTable(ctx, pool); err != nil {
		t.Fatalf("EnsureTable: %v", err)
	}
	return pool
}

// countRows returns the number of rows in public.outbox matching tenant_id and topic.
func countRows(t *testing.T, pool *pgxpool.Pool, tenantID, topic string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM public.outbox WHERE tenant_id=$1 AND topic=$2`,
		tenantID, topic,
	).Scan(&n); err != nil {
		t.Fatalf("countRows: %v", err)
	}
	return n
}

// TestEnqueue_CommitPersistsRow verifies that a committed Enqueue creates a row
// with the correct tenant_id, topic, state=pending, and payload.
func TestEnqueue_CommitPersistsRow(t *testing.T) {
	t.Parallel()
	pool := startPostgres(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	id, err := outbox.Enqueue(ctx, tx, "tenant-a", "order.created", map[string]any{"order_id": "42"})
	if err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatalf("Enqueue: %v", err)
	}
	if id <= 0 {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatalf("expected positive event id, got %d", id)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := countRows(t, pool, "tenant-a", "order.created"); n != 1 {
		t.Errorf("after commit: want 1 row, got %d", n)
	}

	var state, payload string
	if err := pool.QueryRow(ctx,
		`SELECT state, payload::text FROM public.outbox WHERE id=$1`, id,
	).Scan(&state, &payload); err != nil {
		t.Fatalf("verify row: %v", err)
	}
	if state != "pending" {
		t.Errorf("want state=pending, got %q", state)
	}
	if !strings.Contains(payload, "42") {
		t.Errorf("payload missing order_id: %s", payload)
	}
}

// TestEnqueue_RollbackNoRow verifies that a rolled-back transaction leaves NO row
// in the outbox — the core atomicity guarantee of the transactional outbox pattern.
func TestEnqueue_RollbackNoRow(t *testing.T) {
	t.Parallel()
	pool := startPostgres(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err = outbox.Enqueue(ctx, tx, "tenant-b", "rollback.test", map[string]any{"x": 1}); err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if n := countRows(t, pool, "tenant-b", "rollback.test"); n != 0 {
		t.Errorf("after rollback: want 0 rows (atomicity violated), got %d", n)
	}
}

// TestEnqueue_TenantIsolation verifies that enqueueing from tenant A produces a
// row with tenant_id=A, never another tenant's ID, even under interleaved writes.
func TestEnqueue_TenantIsolation(t *testing.T) {
	t.Parallel()
	pool := startPostgres(t)
	ctx := context.Background()

	const topic = "isolation.check"
	for _, tid := range []string{"alpha", "beta", "gamma"} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("[%s] begin tx: %v", tid, err)
		}
		if _, err = outbox.Enqueue(ctx, tx, tid, topic, map[string]any{"tenant": tid}); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			t.Fatalf("[%s] Enqueue: %v", tid, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("[%s] commit: %v", tid, err)
		}
	}

	rows, err := pool.Query(ctx,
		`SELECT tenant_id, payload::text FROM public.outbox WHERE topic=$1 ORDER BY id`, topic,
	)
	if err != nil {
		t.Fatalf("query rows: %v", err)
	}
	defer rows.Close()

	got := map[string]string{}
	for rows.Next() {
		var tid, payload string
		if err := rows.Scan(&tid, &payload); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[tid] = payload
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	for _, tid := range []string{"alpha", "beta", "gamma"} {
		payload, ok := got[tid]
		if !ok {
			t.Errorf("tenant %q: row missing", tid)
			continue
		}
		// The payload was marshalled from map[string]any{"tenant": tid}
		// so it contains the tenant id as a string value.
		if !strings.Contains(payload, fmt.Sprintf("%q", tid)) {
			t.Errorf("tenant %q: payload does not contain own id: %s", tid, payload)
		}
	}
}

// TestEnqueue_EmitsNotify verifies the engine-side half of the async loop: a
// committed Enqueue delivers a NOTIFY on outbox.NotifyChannel carrying the new
// row id (NOT the full payload — that would risk the 8000-byte cap). NOTIFY is
// transactional, so the signal only fires on commit.
func TestEnqueue_EmitsNotify(t *testing.T) {
	t.Parallel()
	pool := startPostgres(t)
	ctx := context.Background()

	// A dedicated connection LISTENs on the channel.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire listen conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+outbox.NotifyChannel); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	id, err := outbox.Enqueue(ctx, tx, "tenant-n", "notify.test", map[string]any{"k": "v"})
	if err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, err := conn.Conn().WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("WaitForNotification: %v (no NOTIFY emitted on commit?)", err)
	}
	if n.Channel != outbox.NotifyChannel {
		t.Errorf("notify channel = %q, want %q", n.Channel, outbox.NotifyChannel)
	}
	if n.Payload != strconv.FormatInt(id, 10) {
		t.Errorf("notify payload = %q, want the row id %d (id only, not the payload)", n.Payload, id)
	}
}

// TestEnqueue_RollbackNoNotify verifies NOTIFY is transactional: a rolled-back
// Enqueue delivers NO notification, so a worker is never woken for a row that
// doesn't exist — the same atomicity guarantee as the row INSERT.
func TestEnqueue_RollbackNoNotify(t *testing.T) {
	t.Parallel()
	pool := startPostgres(t)
	ctx := context.Background()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire listen conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+outbox.NotifyChannel); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := outbox.Enqueue(ctx, tx, "tenant-r", "notify.rollback", map[string]any{"k": "v"}); err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		t.Fatalf("Enqueue: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// No notification should arrive within the window.
	waitCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	if _, err := conn.Conn().WaitForNotification(waitCtx); err == nil {
		t.Fatal("received a NOTIFY after rollback; want none (transactional NOTIFY violated)")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded (no notify), got %v", err)
	}
}

// TestEchoEndpoint_NoJWT verifies that POST /api/_echo returns 401 without a JWT.
// Unit test only (no Docker) — the JWT middleware gates the endpoint before any
// outbox or DB logic runs.
func TestEchoEndpoint_NoJWT(t *testing.T) {
	t.Parallel()
	const secret = "a-test-secret-of-at-least-32-chars!!"
	mw := auth.JWTMiddleware(secret)
	stub := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(stub)

	req := httptest.NewRequest(http.MethodPost, "/api/_echo", strings.NewReader(`{"msg":"hi"}`))
	req.Host = "acme.localhost"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d (body: %s)", w.Code, w.Body.String())
	}
}
