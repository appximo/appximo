//go:build integration || e2e

// Package helpers contains shared test infrastructure for the integration and
// e2e suites: a real Postgres (testcontainers), control-plane migration + tenant
// registration, JWT minting, and a data-plane server wired with the SAME
// observability tap as cmd_serve.go so metric assertions exercise the real path.
//
// It is compiled only under the `integration` or `e2e` build tags, so it never
// touches `go test ./... -short` (the unit lane).
package helpers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	chi "github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/logging"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// Shared test credentials. These never reach production — they exist only to sign
// and verify JWTs inside the in-process test server.
const (
	JWTSecret = "s37-integration-jwt-secret"
	AdminKey  = "s37-integration-admin-key"
)

// RepoRoot returns the absolute path to the repository root, resolved from this
// source file's location so it is independent of the test's working directory.
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0) // .../tests/helpers/server.go
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// FixtureSchema loads and validates a schema fixture from tests/fixtures/schemas.
func FixtureSchema(t *testing.T, name string) *schema.APISchema {
	t.Helper()
	path := filepath.Join(RepoRoot(), "tests", "fixtures", "schemas", name)
	s, err := schema.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	if errs := schema.Validate(s); len(errs) > 0 {
		t.Fatalf("fixture %s failed validation: %v", name, errs)
	}
	return s
}

// StartPostgres boots a postgres:16-alpine container and returns a ready pool plus
// a terminate function. It takes a context (not *testing.T) so it is usable from
// TestMain, where one container is shared across the whole suite.
func StartPostgres(ctx context.Context) (*pgxpool.Pool, func(), error) {
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("appitools_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("start postgres: %w", err)
	}
	connStr, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, nil, fmt.Errorf("connection string: %w", err)
	}
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		_ = ctr.Terminate(ctx)
		return nil, nil, fmt.Errorf("new pool: %w", err)
	}
	cleanup := func() { pool.Close(); _ = ctr.Terminate(ctx) }
	return pool, cleanup, nil
}

// ApplyControlPlane applies migrations/001_control_plane.sql (creates public.tenants,
// public.migration_log, etc.). RegisterTenant depends on it.
func ApplyControlPlane(ctx context.Context, pool *pgxpool.Pool) error {
	sqlBytes, err := os.ReadFile(filepath.Join(RepoRoot(), "migrations", "001_control_plane.sql"))
	if err != nil {
		return fmt.Errorf("read control-plane migration: %w", err)
	}
	if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
		return fmt.Errorf("apply control-plane migration: %w", err)
	}
	return nil
}

// RegisterTenant creates the tenant row + its dedicated pg schema and tables from s.
func RegisterTenant(t *testing.T, pool *pgxpool.Pool, tenantID string, s *schema.APISchema) {
	t.Helper()
	_, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID:    tenantID,
		DisplayName: tenantID + " Test Co",
		Email:       "admin@" + tenantID + ".test",
		Plan:        "free",
		Schema:      s,
	})
	if err != nil {
		t.Fatalf("register tenant %q: %v", tenantID, err)
	}
}

// GenToken mints an HS256 JWT signed with JWTSecret for the given role/user/tenant.
func GenToken(t *testing.T, role, userID, tenantID string) string {
	t.Helper()
	tok, err := auth.GenerateToken(auth.Claims{
		UserID:   userID,
		Role:     role,
		TenantID: tenantID,
	}, JWTSecret)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return tok
}

// BuildObservableServer builds the data-plane router with the SAME middleware chain
// and observability tap as cmd_serve.go (TenantMiddleware → RequestLogger(tap) →
// JWT → RBAC → generated routes), returning the test server and the live *Metrics
// instance so callers can scrape/assert real Prometheus series.
//
// Unlike pkg/integration's buildDP, this includes the RequestLogger tap that feeds
// appitools_requests_total / _request_duration_seconds / _active_tenants — the wiring
// under test. The server reads the per-request Host header (set Req.Host to
// "<tenant>.localhost"), so a single server can serve multiple tenants.
func BuildObservableServer(t *testing.T, s *schema.APISchema, pool *pgxpool.Pool) (*httptest.Server, *observability.Metrics) {
	t.Helper()
	// Silence request logs so test output stays readable; the tap still fires.
	logging.Log = zerolog.Nop()

	policyJSON, err := json.Marshal(s.RBAC)
	if err != nil {
		t.Fatalf("marshal rbac: %v", err)
	}
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())

	metrics := observability.NewMetrics()
	rings := observability.NewRings()

	// The observability tap: identical metric calls to cmd_serve.go's tap, trimmed
	// to the pieces that populate the Prometheus collectors and the active-tenant gauge.
	tap := func(rt logging.RequestTap) {
		if rt.TenantID == "" {
			return
		}
		metrics.ObserveRequest(rt.TenantID, rt.Method, rt.Route,
			strconv.Itoa(rt.Status), float64(rt.DurationUS)/1e6)
		rings.Record(rt.TenantID, observability.Sample{
			Start:  rt.StartUS,
			DurUS:  int32(rt.DurationUS),
			Route:  rings.RouteID(rt.Route),
			Status: uint16(rt.Status),
		})
		metrics.SetActiveTenants(rings.Count())
	}

	r := chi.NewMux()
	r.Use(chimiddleware.RequestID)
	r.Use(tenant.TenantMiddleware)
	r.Use(logging.RequestLogger(nil, nil, tap))
	r.Use(auth.JWTMiddleware(JWTSecret))
	r.Use(rbac.RBACMiddleware(policyJSON))
	r.Use(chimiddleware.Recoverer)
	r.Mount("/", codegen.BuildRouter(s, tdb, hr, nil))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, metrics
}

// TenantRequest builds an *http.Request against srv with the tenant Host subdomain
// and bearer token set, ready for srv.Client().Do.
func TenantRequest(t *testing.T, srv *httptest.Server, method, path, tenantID, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = tenantID + ".localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}
