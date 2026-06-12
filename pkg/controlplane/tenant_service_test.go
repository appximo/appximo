package controlplane_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func startPostgres(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
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

// applyControlPlane runs 001_control_plane.sql so the control plane tables exist.
func applyControlPlane(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	sql, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(sql)); err != nil {
		t.Fatalf("apply control plane migration: %v", err)
	}
}

// minimalSchema returns a RegisterRequest with the minimal logistics-like schema.
func minimalSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "test-api",
		Resources: map[string]schema.ResourceSchema{
			"items": {
				Fields: map[string]schema.FieldDef{
					"name":       {Type: "string", Required: true},
					"status":     {Type: "string"},
					"created_at": {Type: "time", Auto: true},
				},
			},
		},
		RBAC: schema.RBACPolicy{},
	}
}

// tableExists checks whether a table is present in the given schema.
func tableExists(t *testing.T, pool *pgxpool.Pool, pgSchema, table string) bool {
	t.Helper()
	var exists bool
	pool.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2)",
		pgSchema, table,
	).Scan(&exists)
	return exists
}

// schemaExists checks whether a PostgreSQL schema (namespace) exists.
func schemaExists(t *testing.T, pool *pgxpool.Pool, pgSchema string) bool {
	t.Helper()
	var exists bool
	pool.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name=$1)",
		pgSchema,
	).Scan(&exists)
	return exists
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRegisterTenant_Success(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	req := controlplane.RegisterRequest{
		TenantID:    "acme",
		DisplayName: "Acme Corp",
		Email:       "admin@acme.com",
		Plan:        "free",
		Schema:      minimalSchema(),
	}

	tenant, err := controlplane.RegisterTenant(context.Background(), pool, req)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}

	// Verify returned struct.
	if tenant.ID != "acme" {
		t.Errorf("ID: got %q, want %q", tenant.ID, "acme")
	}
	if tenant.PGSchema != "tenant_acme" {
		t.Errorf("PGSchema: got %q, want %q", tenant.PGSchema, "tenant_acme")
	}
	if tenant.Plan != "free" {
		t.Errorf("Plan: got %q, want %q", tenant.Plan, "free")
	}

	// Verify row exists in public.tenants.
	var id string
	if err := pool.QueryRow(context.Background(),
		"SELECT id FROM public.tenants WHERE id=$1", "acme",
	).Scan(&id); err != nil {
		t.Errorf("tenant row not found in public.tenants: %v", err)
	}

	// Verify PostgreSQL schema was created.
	if !schemaExists(t, pool, "tenant_acme") {
		t.Error("postgres schema tenant_acme not found")
	}

	// Verify resource table was created inside tenant schema.
	if !tableExists(t, pool, "tenant_acme", "items") {
		t.Error("table tenant_acme.items not found")
	}
}

func TestRegisterTenant_DuplicateID(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	req := controlplane.RegisterRequest{
		TenantID:    "beta",
		DisplayName: "Beta Inc",
		Email:       "admin@beta.com",
		Plan:        "free",
		Schema:      minimalSchema(),
	}

	if _, err := controlplane.RegisterTenant(context.Background(), pool, req); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	_, err := controlplane.RegisterTenant(context.Background(), pool, req)
	if err == nil {
		t.Fatal("expected error for duplicate tenant, got nil")
	}
	if !containsAny(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
}

func TestRegisterTenant_InvalidID(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	cases := []string{
		"A",         // uppercase
		"x",         // single char (regex requires ≥2)
		"-start",    // starts with hyphen
		"has space", // contains space
		"UPPERCASE", // all uppercase
	}

	for _, id := range cases {
		_, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
			TenantID: id,
			Schema:   minimalSchema(),
		})
		if err == nil {
			t.Errorf("expected error for tenantID %q, got nil", id)
		}
		// Must fail before touching the DB — no tenant row should exist.
		var exists bool
		pool.QueryRow(context.Background(),
			"SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id=$1)", id,
		).Scan(&exists)
		if exists {
			t.Errorf("tenant row should not have been created for invalid id %q", id)
		}
	}
}

func TestRegisterTenant_MultipleResources(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	multiSchema := &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "logistics",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":       {Type: "string", Required: true, Unique: true},
					"status":     {Type: "string"},
					"created_at": {Type: "time", Auto: true},
					"updated_at": {Type: "time", Auto: true},
				},
			},
			"clients": {
				Fields: map[string]schema.FieldDef{
					"name":  {Type: "string", Required: true},
					"email": {Type: "string", Unique: true},
				},
			},
			"incidents": {
				Fields: map[string]schema.FieldDef{
					"description": {Type: "text"},
					"created_at":  {Type: "time", Auto: true},
				},
			},
		},
		RBAC: schema.RBACPolicy{},
	}

	req := controlplane.RegisterRequest{
		TenantID:    "logistics",
		DisplayName: "Logistics SA",
		Email:       "admin@logistics.com",
		Plan:        "pro",
		Schema:      multiSchema,
	}

	if _, err := controlplane.RegisterTenant(context.Background(), pool, req); err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}

	for _, table := range []string{"guides", "clients", "incidents"} {
		if !tableExists(t, pool, "tenant_logistics", table) {
			t.Errorf("table tenant_logistics.%s not found after migration", table)
		}
	}

	// Verify guides table actually accepts inserts (correct column types).
	_, err := pool.Exec(context.Background(),
		`INSERT INTO tenant_logistics.guides (code, status) VALUES ('GU-001', 'pending')`,
	)
	if err != nil {
		t.Errorf("insert into tenant_logistics.guides failed: %v", err)
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

// TestRegisterTenant_OrphanSchema reproduces the clean-droplet finding (FIX 10):
// a Postgres schema tenant_<id> left behind by a previous install (no row in
// public.tenants) made registration blow up with a 500. The contract now is an
// explicit conflict (ErrAlreadyExists → HTTP 409) with both remedies in the
// message — never adoption: a new tenant must not be born holding old data.
func TestRegisterTenant_OrphanSchema(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	// The orphan: physical schema with leftover data, no control-plane row.
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE SCHEMA tenant_ghost`); err != nil {
		t.Fatalf("create orphan schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE tenant_ghost.tasks (id int); INSERT INTO tenant_ghost.tasks VALUES (1)`); err != nil {
		t.Fatalf("seed orphan data: %v", err)
	}

	_, err := controlplane.RegisterTenant(ctx, pool, controlplane.RegisterRequest{
		TenantID:    "ghost",
		DisplayName: "Ghost",
		Email:       "g@ghost.com",
		Plan:        "free",
		Schema:      minimalSchema(),
	})
	if err == nil {
		t.Fatal("expected orphan-schema conflict, got nil")
	}
	if !errors.Is(err, controlplane.ErrAlreadyExists) {
		t.Errorf("must wrap ErrAlreadyExists (HTTP 409), got: %v", err)
	}
	for _, want := range []string{"orphan", "DROP SCHEMA", "tenant_ghost"} {
		if !containsAny(err.Error(), want) {
			t.Errorf("error must mention %q, got: %v", want, err)
		}
	}

	// Refusal means: no tenant row created, and the orphan data UNTOUCHED.
	var rows int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tenant_ghost.tasks").Scan(&rows); err != nil || rows != 1 {
		t.Errorf("orphan data must be untouched, rows=%d err=%v", rows, err)
	}
	var exists bool
	pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id='ghost')").Scan(&exists)
	if exists {
		t.Error("no tenant row should exist after the refusal")
	}
}
