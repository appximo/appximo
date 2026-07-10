package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// tenantIDRe is THE tenant id rule, aligned with what the rest of the engine
// actually enforces: the id becomes the Postgres schema `tenant_<id>` — and the
// data path (pkg/db ValidateSchemaName) refuses any schema outside
// ^[a-z][a-z0-9_]*$ on EVERY query. The old rule here accepted hyphens, so a
// hyphenated tenant registered fine and then failed every data access
// ("invalid schema name") — a zombie by construction. One rule, three layers:
// this backend check (the authority), the Studio/admin UIs (live UX mirror),
// and the docs.
var tenantIDRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)

// SuggestTenantID converts a rejected id into the closest VALID one (lowercase;
// hyphens/spaces → '_'; other characters dropped; leading non-letters trimmed;
// capped at 30). Returns "" when nothing salvageable remains — callers append
// it to the validation error so the user gets an actionable fix, not just a rule.
func SuggestTenantID(raw string) string {
	var b []rune
	for _, r := range raw {
		switch {
		case r >= 'A' && r <= 'Z':
			r += 'a' - 'A'
		case r == '-' || r == ' ' || r == '.':
			r = '_'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			// A schema name must start with a letter: trim leading digits/underscores.
			if len(b) == 0 && !(r >= 'a' && r <= 'z') {
				continue
			}
			b = append(b, r)
		}
	}
	if len(b) > 30 {
		b = b[:30]
	}
	s := string(b)
	if !tenantIDRe.MatchString(s) {
		return ""
	}
	return s
}

// RegisterRequest carries all data needed to onboard a new tenant.
type RegisterRequest struct {
	TenantID    string            `json:"tenant_id"`
	DisplayName string            `json:"display_name"`
	Email       string            `json:"email"`
	Plan        string            `json:"plan"`
	Schema      *schema.APISchema `json:"schema"`
}

// Tenant is the created tenant record returned to the caller.
type Tenant struct {
	ID          string    `json:"id"`
	PGSchema    string    `json:"pg_schema"`
	DisplayName string    `json:"display_name"`
	Email       string    `json:"email"`
	Plan        string    `json:"plan"`
	CreatedAt   time.Time `json:"created_at"`
}

// orphanSchemaErr is the conflict returned when the tenant's Postgres schema
// already exists without a registered tenant. Wraps ErrAlreadyExists so the
// HTTP handler answers 409 with this actionable message.
func orphanSchemaErr(pgSchema, tenantID string) error {
	return fmt.Errorf(
		"postgres schema %q already exists but no tenant %q is registered (orphan from a previous install?): "+
			"drop it to discard its data (DROP SCHEMA %q CASCADE) or choose another tenant_id: %w",
		pgSchema, tenantID, pgSchema, ErrAlreadyExists)
}

// applyTenantMigration is a seam over migration.ApplyTenantMigration so tests
// can force a post-commit failure and assert the all-or-nothing rollback.
var applyTenantMigration = migration.ApplyTenantMigration

// rollbackFailedRegistration undoes everything a failed registration created:
// the tenant's Postgres schema (empty or partially provisioned — CASCADE), its
// public.tenants row, its default policy, its v1 history entry and its
// migration-log rows. Mirrors platformadmin.DeleteTenant's table list for the
// subset that can exist at registration time, so a failed create is
// indistinguishable from one that never happened (zero orphans). The failure
// itself stays in the engine log.
func rollbackFailedRegistration(ctx context.Context, pool *pgxpool.Pool, tenantID, pgSchema string) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pgx.Identifier{pgSchema}.Sanitize())); err != nil {
		return fmt.Errorf("drop schema: %w", err)
	}
	for _, q := range []string{
		`DELETE FROM public.tenant_policies WHERE tenant_id = $1`,
		`DELETE FROM public.schema_history WHERE tenant_id = $1`,
		`DELETE FROM public.migration_log WHERE tenant_id = $1`,
		`DELETE FROM public.tenants WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, q, tenantID); err != nil {
			return fmt.Errorf("cleanup %q: %w", q, err)
		}
	}
	return nil
}

// RegisterTenant onboards a new tenant in 10 atomic steps:
//  1. Validate tenantID format.
//  2. Verify no duplicate in public.tenants.
//     3-7. Transaction: INSERT tenant + CREATE SCHEMA + INSERT policy → COMMIT.
//  8. ApplyTenantMigration: CREATE TABLE for each resource.
//  9. pg_notify('schema_updated', tenantID).
//  10. Return the created Tenant.
func RegisterTenant(ctx context.Context, pool *pgxpool.Pool, req RegisterRequest) (*Tenant, error) {
	// Step 1 — validate tenantID BEFORE touching the DB. The id becomes the
	// Postgres schema (tenant_<id>) and the Host subdomain, so anything the
	// data path would refuse must be rejected here, with the fix in the error.
	if !tenantIDRe.MatchString(req.TenantID) {
		msg := fmt.Sprintf("invalid tenant id %q: must match ^[a-z][a-z0-9_]{1,29}$ — start with a lowercase letter, then lowercase letters, digits or '_' (2-30 chars); hyphens, uppercase and spaces are not allowed (the id becomes the tenant's Postgres schema and subdomain)", req.TenantID)
		if s := SuggestTenantID(req.TenantID); s != "" && s != req.TenantID {
			msg += fmt.Sprintf("; try %q", s)
		}
		return nil, fmt.Errorf("%s: %w", msg, ErrInvalidInput)
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
		return nil, fmt.Errorf("tenant %q already exists: %w", req.TenantID, ErrAlreadyExists)
	}

	// Step 2b — orphan physical schema: tenant_<id> exists in Postgres but no
	// public.tenants row points at it (leftover of a previous install or a
	// half-failed registration). REFUSE rather than adopt: silently reusing
	// the schema would resurrect tables and rows the operator believes
	// deleted — a brand-new tenant must never be born holding old data. The
	// operator gets both remedies in the error. (Without this check the
	// CREATE SCHEMA below failed with 42P06 and surfaced as a 500.)
	var orphan bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)", pgSchema,
	).Scan(&orphan); err != nil {
		return nil, fmt.Errorf("check orphan schema: %w", err)
	}
	if orphan {
		return nil, orphanSchemaErr(pgSchema, req.TenantID)
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
	// 42P06 (duplicate_schema) is classified to the same orphan conflict as
	// Step 2b: it can still happen in the race window between that check and
	// this statement — a 409 in every variant, never a 500.
	if _, err = tx.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", pgSchema)); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P06" {
			return nil, orphanSchemaErr(pgSchema, req.TenantID)
		}
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

	// Version history (VERSION-S1): the registration schema is the tenant's v1.
	// Right after the commit that set json_schema (the history mirrors it), and
	// best-effort — a history failure never fails the registration.
	if _, _, histErr := schemahistory.Append(ctx, pool, req.TenantID, schemaJSON, schemahistory.SourceRegister, ""); histErr != nil {
		log.Printf("WARNING: schema history append for new tenant %q failed: %v", req.TenantID, histErr)
	}

	// Step 8 — apply migrations (CREATE TABLE per resource, idempotent).
	migStart := time.Now().UTC()
	migErr := applyTenantMigration(ctx, pool, pgSchema, req.Schema)

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
		// ALL-OR-NOTHING: the registration tx committed before the migration
		// could run (it manages its own transactions/locks and cannot join
		// ours), so a migration failure used to strand a ZOMBIE — a registered
		// tenant whose schema has no tables, unusable and un-self-healing. Undo
		// everything this registration created; a failed create leaves the
		// system exactly as it was.
		if cleanupErr := rollbackFailedRegistration(ctx, pool, req.TenantID, pgSchema); cleanupErr != nil {
			log.Printf("CRITICAL: tenant %q registration failed AND cleanup failed — remove it manually with `appitools tenant delete %s --yes`: cleanup error: %v", req.TenantID, req.TenantID, cleanupErr)
			return nil, fmt.Errorf("apply migrations: %w (automatic rollback also failed — remove the tenant with `appitools tenant delete %s --yes`)", migErr, req.TenantID)
		}
		return nil, fmt.Errorf("apply migrations: %w (the registration was rolled back — no tenant was left behind)", migErr)
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
