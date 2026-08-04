//go:build integration

// Package appximo integration test (ADR-016 library surface). Exercises the
// REAL request path: a custom Class-1 route registered via Register, mounted in
// the same middleware chain the generated routes use, against a real Postgres
// (testcontainers). Run with: go test -tags integration -race -run TestLibrary .
package appximo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/tests/helpers"
)

var (
	itConnStr string
	itPool    *pgxpool.Pool
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("appximo_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres:", err)
		os.Exit(1)
	}
	itConnStr, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "conn string:", err)
		os.Exit(1)
	}
	itPool, err = db.NewPool(ctx, itConnStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pool:", err)
		os.Exit(1)
	}
	if err := helpers.ApplyControlPlane(ctx, itPool); err != nil {
		fmt.Fprintln(os.Stderr, "control plane:", err)
		os.Exit(1)
	}
	code := m.Run()
	itPool.Close()
	_ = ctr.Terminate(ctx)
	os.Exit(code)
}

// newTestApp builds an App against the shared container, registers a set of
// custom routes that exercise every Ctx capability, and returns an httptest
// server fronting the real middleware chain (no listener — buildRouter directly).
func newTestApp(t *testing.T) *httptest.Server {
	t.Helper()
	quickstart := filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json")
	app, err := New(Config{
		SchemaPath: quickstart,
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })

	mustRegister(t, app, Route{Method: "POST", Path: "/api/_echo", Handler: func(ctx Ctx) error {
		var body struct {
			Msg string `json:"msg"`
		}
		if err := ctx.Bind(&body); err != nil {
			return ctx.Error(400, "invalid body", err)
		}
		id, err := ctx.Enqueue("echo.test", map[string]any{"msg": body.Msg})
		if err != nil {
			return ctx.Error(500, "enqueue", err)
		}
		return ctx.JSON(200, map[string]any{"event_id": id})
	}})

	// Enqueues then returns a raw error → tx (and the enqueue) must roll back.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_echo_rollback", Handler: func(ctx Ctx) error {
		if _, err := ctx.Enqueue("echo.rollback", map[string]any{"x": 1}); err != nil {
			return err
		}
		return errors.New("intentional failure after enqueue")
	}})

	// Returns the active search_path schema — proves tenant isolation on the tx.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_whoami", Handler: func(ctx Ctx) error {
		var sch string
		if err := ctx.Tx().QueryRow(ctx.Context(), "SELECT current_schema()").Scan(&sch); err != nil {
			return ctx.Error(500, "query", err)
		}
		return ctx.JSON(200, map[string]any{"schema": sch, "tenant": ctx.Tenant()})
	}})

	// RBAC-aware Query of tasks → count visible to the request's tenant.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_taskcount", Handler: func(ctx Ctx) error {
		rows, err := ctx.Query("tasks", QueryOpts{Limit: 100})
		if err != nil {
			return ctx.Error(500, "query", err)
		}
		return ctx.JSON(200, map[string]any{"count": len(rows)})
	}})

	// BindResource through the full chain → 422 on invalid body.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_validate", Handler: func(ctx Ctx) error {
		var dst map[string]any
		if err := ctx.BindResource("tasks", &dst); err != nil {
			return err // *ValidationError → 422 in the same shape as REST
		}
		return ctx.JSON(200, map[string]any{"ok": true})
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func mustRegister(t *testing.T, app *App, rt Route) {
	t.Helper()
	if err := app.Register(rt); err != nil {
		t.Fatalf("Register %s %s: %v", rt.Method, rt.Path, err)
	}
}

func loadQuickstart(t *testing.T) *schema.APISchema {
	t.Helper()
	s, err := schema.LoadFromFile(filepath.Join(helpers.RepoRoot(), "examples", "quickstart", "schema.json"))
	if err != nil {
		t.Fatalf("load quickstart: %v", err)
	}
	return s
}

func do(t *testing.T, srv *httptest.Server, method, path, host, token, body string) *http.Response {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func TestLibrary_CustomRoute_Echo_Enqueue(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "acme", s)
	srv := newTestApp(t)
	token := helpers.GenToken(t, "admin", "u1", "acme")

	resp := do(t, srv, "POST", "/api/_echo", "acme.localhost", token, `{"msg":"hello"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := decode(t, resp)
	if _, ok := body["event_id"]; !ok {
		t.Fatalf("expected event_id, got %v", body)
	}

	// The enqueue committed: a public.outbox row exists for tenant acme/topic echo.test.
	var n int
	if err := itPool.QueryRow(context.Background(),
		"SELECT count(*) FROM public.outbox WHERE tenant_id=$1 AND topic=$2", "acme", "echo.test").Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 committed outbox row, got %d", n)
	}
}

func TestLibrary_CustomRoute_NoJWT_401(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "noauth", s)
	srv := newTestApp(t)

	resp := do(t, srv, "POST", "/api/_echo", "noauth.localhost", "", `{"msg":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 (inherited JWT middleware), got %d", resp.StatusCode)
	}
}

func TestLibrary_Tx_SearchPath_And_TenantIsolation(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "isoa", s)
	helpers.RegisterTenant(t, itPool, "isob", s)
	srv := newTestApp(t)
	tokA := helpers.GenToken(t, "admin", "u", "isoa")
	tokB := helpers.GenToken(t, "admin", "u", "isob")

	// search_path proof: the tx hands tenant_isoa to the handler.
	resp := do(t, srv, "GET", "/api/_whoami", "isoa.localhost", tokA, "")
	body := decode(t, resp)
	if body["schema"] != "tenant_isoa" {
		t.Fatalf("expected current_schema tenant_isoa, got %v", body["schema"])
	}

	// Seed: 2 tasks for isoa, 1 for isob — via the generated POST route.
	for i := 0; i < 2; i++ {
		r := do(t, srv, "POST", "/api/tasks", "isoa.localhost", tokA,
			fmt.Sprintf(`{"title":"a-%d","status":"open"}`, i))
		if r.StatusCode != 201 {
			t.Fatalf("seed isoa task: %d", r.StatusCode)
		}
		r.Body.Close()
	}
	r := do(t, srv, "POST", "/api/tasks", "isob.localhost", tokB, `{"title":"b-0","status":"open"}`)
	if r.StatusCode != 201 {
		t.Fatalf("seed isob task: %d", r.StatusCode)
	}
	r.Body.Close()

	// ctx.Query honours the request tenant's search_path: A sees 2, B sees 1.
	if c := decode(t, do(t, srv, "GET", "/api/_taskcount", "isoa.localhost", tokA, ""))["count"]; c != float64(2) {
		t.Fatalf("isoa task count = %v, want 2", c)
	}
	if c := decode(t, do(t, srv, "GET", "/api/_taskcount", "isob.localhost", tokB, ""))["count"]; c != float64(1) {
		t.Fatalf("isob task count = %v, want 1 (cross-tenant leak!)", c)
	}
}

func TestLibrary_Enqueue_RollsBackWithHandlerError(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "rollback", s)
	srv := newTestApp(t)
	token := helpers.GenToken(t, "admin", "u", "rollback")

	resp := do(t, srv, "POST", "/api/_echo_rollback", "rollback.localhost", token, "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 from handler error, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	var n int
	if err := itPool.QueryRow(context.Background(),
		"SELECT count(*) FROM public.outbox WHERE tenant_id=$1 AND topic=$2", "rollback", "echo.rollback").Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if n != 0 {
		t.Fatalf("handler error must roll back the enqueue; found %d outbox rows", n)
	}
}

func TestLibrary_BindResource_422ThroughChain(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "validate", s)
	srv := newTestApp(t)
	token := helpers.GenToken(t, "admin", "u", "validate")

	resp := do(t, srv, "POST", "/api/_validate", "validate.localhost", token, `{"status":"open"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing required title, got %d", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["error"] != "validation_failed" {
		t.Fatalf("expected validation_failed, got %v", body)
	}
}
