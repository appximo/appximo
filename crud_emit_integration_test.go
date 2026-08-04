//go:build integration

// CRUD-EMIT-V1 integration tests: the generated CRUD write path emits a
// transactional outbox event for resources that opt in via the schema "events"
// key — in the SAME transaction as the write — and emits nothing (zero overhead)
// for resources that do not. Reuses the shared Postgres container + control
// plane from TestMain in library_integration_test.go.
package appximo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/worker"
	"github.com/appximo/appximo/tests/helpers"
)

func emitFixturePath() string {
	return filepath.Join(helpers.RepoRoot(), "tests", "fixtures", "schemas", "emit.json")
}

func loadEmitSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(emitFixturePath())
	if err != nil {
		t.Fatalf("load emit fixture: %v", err)
	}
	return s
}

// newEmitApp builds an App over the emit fixture (tasks opts into create/update/
// delete events; notes opts into none) and returns an httptest server fronting
// the real generated CRUD router.
func newEmitApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: emitFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New(emit): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func outboxCount(t *testing.T, tenant, topic string) int {
	t.Helper()
	var n int
	if err := itPool.QueryRow(context.Background(),
		"SELECT count(*) FROM public.outbox WHERE tenant_id=$1 AND topic=$2", tenant, topic).Scan(&n); err != nil {
		t.Fatalf("count outbox %s/%s: %v", tenant, topic, err)
	}
	return n
}

// outboxHasPayloadID reports whether an outbox row exists for (tenant, topic)
// whose payload id matches id — proving the lean {id,...} payload shape.
func outboxHasPayloadID(t *testing.T, tenant, topic, id string) bool {
	t.Helper()
	var n int
	if err := itPool.QueryRow(context.Background(),
		"SELECT count(*) FROM public.outbox WHERE tenant_id=$1 AND topic=$2 AND payload->>'id'=$3",
		tenant, topic, id).Scan(&n); err != nil {
		t.Fatalf("payload-id query: %v", err)
	}
	return n > 0
}

func TestCRUDEmit_OptInLifecycle(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "emit", loadEmitSchema(t))
	srv := newEmitApp(t)
	tok := helpers.GenToken(t, "admin", "u", "emit")
	const host = "emit.localhost"

	// CREATE → tasks.created with the new row's id in the payload.
	resp := do(t, srv, "POST", "/api/tasks", host, tok, `{"title":"evt-create","status":"open"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", resp.StatusCode)
	}
	created := decode(t, resp)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create response missing id: %v", created)
	}
	if !outboxHasPayloadID(t, "emit", "tasks.created", id) {
		t.Fatalf("no tasks.created outbox row carrying id %s", id)
	}

	// UPDATE (PATCH) → tasks.updated.
	resp = do(t, srv, "PATCH", "/api/tasks/"+id, host, tok, `{"status":"done"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if !outboxHasPayloadID(t, "emit", "tasks.updated", id) {
		t.Fatalf("no tasks.updated outbox row for id %s", id)
	}

	// DELETE → tasks.deleted.
	resp = do(t, srv, "DELETE", "/api/tasks/"+id, host, tok, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if !outboxHasPayloadID(t, "emit", "tasks.deleted", id) {
		t.Fatalf("no tasks.deleted outbox row for id %s", id)
	}
}

func TestCRUDEmit_NoOptInIsSilent(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "emitn", loadEmitSchema(t))
	srv := newEmitApp(t)
	tok := helpers.GenToken(t, "admin", "u", "emitn")

	// notes declares no events → POST must NOT write any outbox row.
	resp := do(t, srv, "POST", "/api/notes", "emitn.localhost", tok, `{"body":"hello"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create note: want 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	for _, topic := range []string{"notes.created", "notes.updated", "notes.deleted"} {
		if n := outboxCount(t, "emitn", topic); n != 0 {
			t.Fatalf("resource without opt-in emitted %d %s rows (want 0)", n, topic)
		}
	}
}

func TestCRUDEmit_WriteFailureRollsBackEvent(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "emitr", loadEmitSchema(t))
	srv := newEmitApp(t)
	tok := helpers.GenToken(t, "admin", "u", "emitr")
	const host = "emitr.localhost"

	// First create succeeds (1 event). tasks.title is UNIQUE.
	r1 := do(t, srv, "POST", "/api/tasks", host, tok, `{"title":"dup","status":"open"}`)
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d", r1.StatusCode)
	}
	r1.Body.Close()

	// Second create with the same title hits the UNIQUE constraint → the INSERT
	// errors and the transaction rolls back, so NO second event may exist (the
	// write and its event are atomic). The engine masks the DB error as 500 on
	// the create path (only the update path translates 23505→409); either way the
	// write did NOT succeed.
	r2 := do(t, srv, "POST", "/api/tasks", host, tok, `{"title":"dup","status":"done"}`)
	if r2.StatusCode == http.StatusCreated {
		t.Fatalf("dup create unexpectedly succeeded (status 201) — unique constraint not enforced")
	}
	r2.Body.Close()

	if n := outboxCount(t, "emitr", "tasks.created"); n != 1 {
		t.Fatalf("failed write must not emit: want exactly 1 tasks.created, got %d", n)
	}
}

func TestCRUDEmit_TenantIsolation(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "emitia", loadEmitSchema(t))
	helpers.RegisterTenant(t, itPool, "emitib", loadEmitSchema(t))
	srv := newEmitApp(t)

	ra := do(t, srv, "POST", "/api/tasks", "emitia.localhost",
		helpers.GenToken(t, "admin", "u", "emitia"), `{"title":"a","status":"open"}`)
	ra.Body.Close()

	// Tenant A's event is tagged tenant_id=emitia, never emitib.
	if outboxCount(t, "emitia", "tasks.created") != 1 {
		t.Fatal("tenant emitia should have exactly 1 tasks.created event")
	}
	if outboxCount(t, "emitib", "tasks.created") != 0 {
		t.Fatal("tenant emitib must not see emitia's event (isolation breach)")
	}
}

// TestCRUDEmit_WorkerConsumes proves the real worker (pkg/worker) drains an event
// the generated CRUD path emitted: POST → outbox row → worker.Drain → sent_at set.
func TestCRUDEmit_WorkerConsumes(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "emitw", loadEmitSchema(t))
	srv := newEmitApp(t)
	tok := helpers.GenToken(t, "admin", "u", "emitw")

	resp := do(t, srv, "POST", "/api/tasks", "emitw.localhost", tok, `{"title":"worker-evt","status":"open"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", resp.StatusCode)
	}
	id := decode(t, resp)["id"].(string)

	// The freshly emitted row is pending.
	var sentAt *string
	q := "SELECT sent_at::text FROM public.outbox WHERE tenant_id='emitw' AND topic='tasks.created' AND payload->>'id'=$1"
	if err := itPool.QueryRow(context.Background(), q, id).Scan(&sentAt); err != nil {
		t.Fatalf("pre-drain query: %v", err)
	}
	if sentAt != nil {
		t.Fatalf("row should be pending before drain, sent_at=%v", *sentAt)
	}

	// Drain with a no-op processor (the engine wrote it; the worker consumes it).
	nop := zerolog.Nop()
	res, err := worker.Drain(context.Background(), itPool,
		worker.ProcessorFunc(func(context.Context, worker.Row) error { return nil }),
		50, 5, &nop)
	if err != nil {
		t.Fatalf("worker.Drain: %v", err)
	}
	if res.Processed < 1 {
		t.Fatalf("worker processed %d rows, want >=1", res.Processed)
	}

	// After drain the row is marked sent.
	if err := itPool.QueryRow(context.Background(), q, id).Scan(&sentAt); err != nil {
		t.Fatalf("post-drain query: %v", err)
	}
	if sentAt == nil {
		t.Fatalf("worker did not mark the CRUD-emitted event sent (sent_at still NULL)")
	}
	_ = fmt.Sprint(res.Claimed())
}
