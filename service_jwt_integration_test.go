//go:build integration

// SERVICE-JWT-V1 integration tests: the worker writes BACK to the engine through
// its HTTP API using a short-lived, scoped service JWT, and the engine enforces
// tenant + RBAC on it. Reuses the shared Postgres container + control plane from
// TestMain in library_integration_test.go.
package appximo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/worker"
	"github.com/appximo/appximo/tests/helpers"
)

const validUUID = "00000000-0000-0000-0000-000000000001"

func serviceFixturePath() string {
	return filepath.Join(helpers.RepoRoot(), "tests", "fixtures", "schemas", "service.json")
}

// newServiceApp builds an App over the service fixture (tasks emits create events;
// roles admin + scoped service_worker[update,status]) and fronts it with httptest.
func newServiceApp(t *testing.T) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: serviceFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New(service): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

// createTask POSTs a task as admin and returns its id.
func createTask(t *testing.T, srv *httptest.Server, tenant, title string) string {
	t.Helper()
	adminTok := helpers.GenToken(t, "admin", "admin-user", tenant)
	resp := do(t, srv, "POST", "/api/tasks", tenant+".localhost", adminTok,
		`{"title":"`+title+`","status":"open"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create task: want 201, got %d", resp.StatusCode)
	}
	id, _ := decode(t, resp)["id"].(string)
	if id == "" {
		t.Fatal("create task: no id")
	}
	return id
}

func TestServiceJWT_ScopedRole(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "svcscope", loadServiceSchema(t))
	srv := newServiceApp(t)
	id := createTask(t, srv, "svcscope", "scope-task")

	client := worker.NewEngineClient(srv.URL, "localhost", helpers.JWTSecret, "service_worker", worker.DefaultServiceTokenTTL)
	ctx := context.Background()

	// IN scope: update the status field → 200.
	if st, body, err := client.Do(ctx, "svcscope", "PATCH", "/api/tasks/"+id, map[string]any{"status": "done"}); err != nil || st != 200 {
		t.Fatalf("in-scope PATCH status: want 200, got %d err=%v body=%s", st, err, body)
	}

	// OUT of scope: delete → 403 (service_worker may only update). This is the
	// hole the scoped role closes — a worker is NOT an admin.
	if st, _, err := client.Do(ctx, "svcscope", "DELETE", "/api/tasks/"+id, nil); err != nil || st != http.StatusForbidden {
		t.Fatalf("out-of-scope DELETE: want 403, got %d err=%v", st, err)
	}
	// OUT of scope: create → 403.
	if st, _, err := client.Do(ctx, "svcscope", "POST", "/api/tasks", map[string]any{"title": "x"}); err != nil || st != http.StatusForbidden {
		t.Fatalf("out-of-scope POST: want 403, got %d err=%v", st, err)
	}
}

func TestServiceJWT_CrossTenantRejected(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "svcta", loadServiceSchema(t))
	helpers.RegisterTenant(t, itPool, "svctb", loadServiceSchema(t))
	srv := newServiceApp(t)

	// Token minted for tenant svcta, sent with Host of svctb → engine rejects 401
	// (token tenant mismatch). The worker mints per-tenant; this proves a stolen/
	// misrouted token cannot cross tenants.
	tok, err := auth.GenerateTokenWithTTL(auth.Claims{Role: "service_worker", TenantID: "svcta"}, helpers.JWTSecret, time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	resp := do(t, srv, "PATCH", "/api/tasks/"+validUUID, "svctb.localhost", tok, `{"status":"done"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-tenant: want 401, got %d", resp.StatusCode)
	}
}

func TestServiceJWT_ExpiredRejected(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "svcexp", loadServiceSchema(t))
	srv := newServiceApp(t)

	tok, err := auth.GenerateTokenWithTTL(auth.Claims{Role: "service_worker", TenantID: "svcexp"}, helpers.JWTSecret, -1*time.Second)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	resp := do(t, srv, "PATCH", "/api/tasks/"+validUUID, "svcexp.localhost", tok, `{"status":"done"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired token: want 401, got %d", resp.StatusCode)
	}
}

// TestServiceJWT_WritebackE2E proves the full authenticated chain: POST emits
// tasks.created → worker.Drain runs the WritebackProcessor → it mints a scoped
// service JWT and PATCHes the row's status via the engine API → RBAC accepts →
// the row is marked sent AND the task status actually changed.
func TestServiceJWT_WritebackE2E(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "svcwb", loadServiceSchema(t))
	srv := newServiceApp(t)
	id := createTask(t, srv, "svcwb", "writeback-task")

	client := worker.NewEngineClient(srv.URL, "localhost", helpers.JWTSecret, "service_worker", worker.DefaultServiceTokenTTL)
	nop := zerolog.Nop()
	proc := worker.NewWritebackProcessor(client, "done", nop)

	res, err := worker.Drain(context.Background(), itPool, proc, 50, 5, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Processed < 1 {
		t.Fatalf("worker processed %d rows, want >=1", res.Processed)
	}

	// The tasks.created event is marked sent.
	var sentAt *string
	q := "SELECT sent_at::text FROM public.outbox WHERE tenant_id='svcwb' AND topic='tasks.created' AND payload->>'id'=$1"
	if err := itPool.QueryRow(context.Background(), q, id).Scan(&sentAt); err != nil {
		t.Fatalf("outbox query: %v", err)
	}
	if sentAt == nil {
		t.Fatal("writeback succeeded but outbox row not marked sent")
	}

	// The write-back actually changed the task (status open → done) via the API.
	adminTok := helpers.GenToken(t, "admin", "admin-user", "svcwb")
	resp := do(t, srv, "GET", "/api/tasks/"+id, "svcwb.localhost", adminTok, "")
	got := decode(t, resp)
	if got["status"] != "done" {
		t.Fatalf("write-back did not change status: got %v, want done", got["status"])
	}
}

func loadServiceSchema(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(serviceFixturePath())
	if err != nil {
		t.Fatalf("load service fixture: %v", err)
	}
	return s
}
