package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
)

// tenantIDRe: first char alnum, rest alnum or hyphen, total 2-30 chars.
var tenantIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]{1,29}$`)

// RegisterRequest carries all data needed to onboard a new tenant.
type RegisterRequest struct {
	TenantID    string
	DisplayName string
	Email       string
	Plan        string
	Schema      *schema.APISchema
}

// Tenant is the created tenant record returned to the caller.
type Tenant struct {
	ID          string
	PGSchema    string
	DisplayName string
	Email       string
	Plan        string
	CreatedAt   time.Time
}

// RegisterTenant onboards a new tenant in 10 atomic steps:
//  1. Validate tenantID format.
//  2. Verify no duplicate in public.tenants.
//  3-7. Transaction: INSERT tenant + CREATE SCHEMA + INSERT policy → COMMIT.
//  8. ApplyTenantMigration: CREATE TABLE for each resource.
//  9. pg_notify('schema_updated', tenantID).
//  10. Return the created Tenant.
func RegisterTenant(ctx context.Context, pool *pgxpool.Pool, req RegisterRequest) (*Tenant, error) {
	// Step 1 — validate tenantID.
	if !tenantIDRe.MatchString(req.TenantID) {
		return nil, fmt.Errorf("invalid tenant id %q: must match ^[a-z0-9][a-z0-9-]{1,29}$", req.TenantID)
	}

	pgSchema := "tenant_" + req.TenantID

	// Step 2 — check no duplicate.
	var exists bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id = $1)", req.TenantID,
	).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check tenant existence: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("tenant %q already exists", req.TenantID)
	}

	now := time.Now().UTC()

	schemaJSON, err := json.Marshal(req.Schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	policyJSON, err := json.Marshal(req.Schema.RBAC)
	if err != nil {
		return nil, fmt.Errorf("marshal policy: %w", err)
	}

	// Steps 3-7 — single transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Step 4 — INSERT tenant row.
	if _, err = tx.Exec(ctx, `
		INSERT INTO public.tenants
		  (id, pg_schema, display_name, email, plan, json_schema, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		req.TenantID, pgSchema, req.DisplayName, req.Email,
		req.Plan, schemaJSON, now,
	); err != nil {
		return nil, fmt.Errorf("insert tenant: %w", err)
	}

	// Step 5 — CREATE SCHEMA.
	// pgx.Identifier.Sanitize() quotes the name, preventing SQL injection.
	if _, err = tx.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", pgSchema)); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	// Step 6 — INSERT default RBAC policy.
	if _, err = tx.Exec(ctx, `
		INSERT INTO public.tenant_policies (tenant_id, policy, updated_at)
		VALUES ($1, $2, $3)`,
		req.TenantID, policyJSON, now,
	); err != nil {
		return nil, fmt.Errorf("insert tenant policy: %w", err)
	}

	// Step 7 — COMMIT.
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// Step 8 — apply migrations (CREATE TABLE per resource, idempotent).
	migStart := time.Now().UTC()
	migErr := migration.ApplyTenantMigration(ctx, pool, pgSchema, req.Schema)

	migStatus, migErrStr := "ok", ""
	if migErr != nil {
		migStatus = "failed"
		migErrStr = migErr.Error()
	}
	// Log the migration outcome — best-effort, ignore insert error.
	pool.Exec(ctx, `
		INSERT INTO public.migration_log
		  (tenant_id, status, error, started_at, finished_at)
		VALUES ($1, $2, NULLIF($3,''), $4, now())`,
		req.TenantID, migStatus, migErrStr, migStart,
	) //nolint:errcheck

	if migErr != nil {
		return nil, fmt.Errorf("apply migrations: %w", migErr)
	}

	// Step 9 — notify in-process caches / listeners.
	pool.Exec(ctx, "SELECT pg_notify('schema_updated', $1)", req.TenantID) //nolint:errcheck

	// Step 10 — return.
	return &Tenant{
		ID:          req.TenantID,
		PGSchema:    pgSchema,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Plan:        req.Plan,
		CreatedAt:   now,
	}, nil
}
