//go:build integration

// Phase-0 safety integration tests (LIBRARY-HARDEN-S1): the in-process model is
// made ROBUST, not only powerful. Exercised against a real Postgres through the
// REAL middleware chain:
//   - Ctx.SafeGo recovers a background-goroutine panic → the process survives
//     and keeps serving (a raw `go func(){panic()}()` would crash every tenant);
//   - the request-chain Recoverer turns a handler panic into a clean 500 → the
//     process survives;
//   - Route.Timeout cancels a slow query at the deadline (propagated to pgx);
//   - the Ctx is tenant-scoped BY CONSTRUCTION — even a raw UnsafeTx query reads
//     only the request tenant's rows.
package appximo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/appximo/appximo/tests/helpers"
)

// safeGoRan signals that a SafeGo background goroutine started executing, so the
// test can wait for it before asserting the process survived its panic.
var safeGoRan = make(chan struct{}, 1)

// newHardenApp builds an App with the Phase-0 probe routes and returns both the
// app (for metric assertions) and an httptest server fronting the real chain.
func newHardenApp(t *testing.T) (*App, *httptest.Server) {
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

	// SafeGo(panic): the background goroutine signals, then panics. runSafe must
	// recover it; if it did not, this test binary would crash.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_safego_panic", Handler: func(ctx Ctx) error {
		ctx.SafeGo(func(context.Context) {
			safeGoRan <- struct{}{}
			panic("boom in a SafeGo background goroutine")
		})
		return ctx.JSON(http.StatusAccepted, map[string]any{"accepted": true})
	}})

	// A handler that panics directly (the request goroutine) → the Recoverer
	// middleware must turn it into a clean 500.
	mustRegister(t, app, Route{Method: "POST", Path: "/api/_panic", Handler: func(ctx Ctx) error {
		panic("boom in the request handler")
	}})

	// Route.Timeout: a query that would take 2s is cancelled at the 300ms deadline.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_slow", Timeout: 300 * time.Millisecond,
		Handler: func(ctx Ctx) error {
			if _, err := ctx.Tx().Exec(ctx.Context(), "SELECT pg_sleep(2)"); err != nil {
				return ctx.Error(http.StatusGatewayTimeout, "deadline exceeded", err)
			}
			return ctx.JSON(200, map[string]any{"slept": true})
		}})

	// Raw UnsafeTx count — bypasses the RBAC helpers; proves the tenant scoping is
	// structural (search_path), not just policy.
	mustRegister(t, app, Route{Method: "GET", Path: "/api/_rawcount", Handler: func(ctx Ctx) error {
		var n int
		if err := ctx.UnsafeTx().QueryRow(ctx.Context(), "SELECT count(*) FROM tasks").Scan(&n); err != nil {
			return ctx.Error(500, "count", err)
		}
		return ctx.JSON(200, map[string]any{"count": n})
	}})

	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return app, srv
}

// TestHarden_SafeGo_PanicSurvives is the headline Phase-0 proof: a panic in a
// SafeGo goroutine is recovered (metric + log), the process stays up, and a
// subsequent request is served. A raw goroutine panic would crash the binary.
func TestHarden_SafeGo_PanicSurvives(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "safego", s)
	app, srv := newHardenApp(t)
	token := helpers.GenToken(t, "admin", "u", "safego")

	before := counterValue(t, app.metrics.Gatherer(), "appximo_goroutine_panics_total")

	resp := do(t, srv, "POST", "/api/_safego_panic", "safego.localhost", token, `{}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The background goroutine ran (then panicked, recovered by runSafe).
	select {
	case <-safeGoRan:
	case <-time.After(3 * time.Second):
		t.Fatal("SafeGo goroutine never executed")
	}

	// The metric increments AFTER the panic is recovered — poll for it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if counterValue(t, app.metrics.Gatherer(), "appximo_goroutine_panics_total") > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("goroutine_panics_total did not increment — recover path did not run")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The process survived the background panic: a follow-up request is served.
	resp2 := do(t, srv, "GET", "/api/_rawcount", "safego.localhost", token, "")
	if resp2.StatusCode != 200 {
		t.Fatalf("process should keep serving after a recovered SafeGo panic; got %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// TestHarden_HandlerPanic_Becomes500 proves the request-chain Recoverer: a
// handler panic → clean 500 + request_panics_total, process stays up.
func TestHarden_HandlerPanic_Becomes500(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "panicky", s)
	app, srv := newHardenApp(t)
	token := helpers.GenToken(t, "admin", "u", "panicky")

	before := counterValue(t, app.metrics.Gatherer(), "appximo_request_panics_total")

	resp := do(t, srv, "POST", "/api/_panic", "panicky.localhost", token, `{}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 from a recovered handler panic, got %d", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["error"] != "internal error" {
		t.Fatalf("expected masked error body, got %v", body)
	}
	if got := counterValue(t, app.metrics.Gatherer(), "appximo_request_panics_total"); got <= before {
		t.Fatalf("request_panics_total did not increment (%v -> %v)", before, got)
	}

	// Process survived: a follow-up request is served on the same server.
	resp2 := do(t, srv, "GET", "/api/_rawcount", "panicky.localhost", token, "")
	if resp2.StatusCode != 200 {
		t.Fatalf("process should keep serving after a recovered handler panic; got %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

// TestHarden_RouteTimeout_CancelsSlowQuery: the 300ms Route.Timeout cancels a
// 2s query (deadline propagated to pgx), so the request returns fast, not in 2s.
func TestHarden_RouteTimeout_CancelsSlowQuery(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "slowt", s)
	_, srv := newHardenApp(t)
	token := helpers.GenToken(t, "admin", "u", "slowt")

	start := time.Now()
	resp := do(t, srv, "GET", "/api/_slow", "slowt.localhost", token, "")
	elapsed := time.Since(start)
	resp.Body.Close()

	if resp.StatusCode == 200 {
		t.Fatalf("expected the slow query to be cancelled, but it completed 200")
	}
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("Route.Timeout did not cancel the query: took %s (query was pg_sleep(2))", elapsed)
	}
}

// TestHarden_Ctx_ScopedByConstruction: two tenants with different row counts;
// a raw UnsafeTx `SELECT count(*) FROM tasks` sees only the request tenant's
// rows (search_path scoping is structural, independent of the RBAC helpers).
func TestHarden_Ctx_ScopedByConstruction(t *testing.T) {
	s := loadQuickstart(t)
	helpers.RegisterTenant(t, itPool, "scopea", s)
	helpers.RegisterTenant(t, itPool, "scopeb", s)
	_, srv := newHardenApp(t)
	tokA := helpers.GenToken(t, "admin", "u", "scopea")
	tokB := helpers.GenToken(t, "admin", "u", "scopeb")

	// Seed: 3 tasks for A, 1 for B via the generated POST route.
	for i := 0; i < 3; i++ {
		r := do(t, srv, "POST", "/api/tasks", "scopea.localhost", tokA, `{"title":"a","status":"open"}`)
		if r.StatusCode != 201 {
			t.Fatalf("seed scopea: %d", r.StatusCode)
		}
		r.Body.Close()
	}
	r := do(t, srv, "POST", "/api/tasks", "scopeb.localhost", tokB, `{"title":"b","status":"open"}`)
	if r.StatusCode != 201 {
		t.Fatalf("seed scopeb: %d", r.StatusCode)
	}
	r.Body.Close()

	if c := decode(t, do(t, srv, "GET", "/api/_rawcount", "scopea.localhost", tokA, ""))["count"]; c != float64(3) {
		t.Fatalf("scopea raw count = %v, want 3", c)
	}
	if c := decode(t, do(t, srv, "GET", "/api/_rawcount", "scopeb.localhost", tokB, ""))["count"]; c != float64(1) {
		t.Fatalf("scopeb raw count = %v, want 1 (cross-tenant leak via UnsafeTx!)", c)
	}
}

// counterValue reads a named counter's current value from a Prometheus gatherer.
func counterValue(t *testing.T, g prometheus.Gatherer, name string) float64 {
	t.Helper()
	mfs, err := g.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		if len(mf.GetMetric()) == 0 {
			return 0
		}
		return mf.GetMetric()[0].GetCounter().GetValue()
	}
	return 0
}
