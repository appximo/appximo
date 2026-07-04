package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/redis/go-redis/v9"
)

// Service is the control plane dependency injected into the HTTP handlers.
// All methods are safe to mock in unit tests.
type Service interface {
	Register(ctx context.Context, req RegisterRequest) (*Tenant, error)
	GetByID(ctx context.Context, id string) (*Tenant, error)
	// UpdateSchema applies a schema change with NO destructive approval (additive:
	// every drop is gated). Equivalent to UpdateSchemaApproved with no approved drops.
	UpdateSchema(ctx context.Context, id string, s *schema.APISchema) error
	// UpdateSchemaApproved applies a schema change, executing ONLY the destructive
	// drops whose approval key is in approvedDrops (DropTable "<table>" / DropColumn
	// "<table>.<column>"). With an empty slice it is identical to UpdateSchema (fail-
	// safe: nothing is dropped). It returns what was applied/gated for the response.
	UpdateSchemaApproved(ctx context.Context, id string, s *schema.APISchema, approvedDrops []string) (*migration.ApplyOutcome, error)
	// PreviewSchema computes a dry-run of the schema change WITHOUT applying it: the
	// classified plan plus the impact (rows lost) of each data-losing drop, evaluated
	// against the given approval set. It is the informed-consent surface.
	PreviewSchema(ctx context.Context, id string, s *schema.APISchema, approvedDrops []string) (*migration.Preview, error)
	GetSchema(ctx context.Context, id string) (*schema.APISchema, error)
}

// pgService is the production implementation backed by pgxpool + optional Redis.
type pgService struct {
	pool  *pgxpool.Pool
	redis *redis.Client // nil when REDIS_URL is not set
}

// NewService creates a production Service. redisClient may be nil — in that case
// schema updates are written to the DB only and pg_notify handles cache invalidation.
func NewService(pool *pgxpool.Pool, redisClient *redis.Client) Service {
	return &pgService{pool: pool, redis: redisClient}
}

func (s *pgService) Register(ctx context.Context, req RegisterRequest) (*Tenant, error) {
	return RegisterTenant(ctx, s.pool, req)
}

func (s *pgService) GetByID(ctx context.Context, id string) (*Tenant, error) {
	var t Tenant
	err := s.pool.QueryRow(ctx, `
		SELECT id, pg_schema, display_name, email, plan, created_at
		FROM public.tenants WHERE id = $1`,
		id,
	).Scan(&t.ID, &t.PGSchema, &t.DisplayName, &t.Email, &t.Plan, &t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}
	return &t, nil
}

func (s *pgService) UpdateSchema(ctx context.Context, id string, apiSchema *schema.APISchema) error {
	_, err := s.UpdateSchemaApproved(ctx, id, apiSchema, nil)
	return err
}

func (s *pgService) UpdateSchemaApproved(ctx context.Context, id string, apiSchema *schema.APISchema, approvedDrops []string) (*migration.ApplyOutcome, error) {
	b, err := json.Marshal(apiSchema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE public.tenants SET json_schema = $2, updated_at = now() WHERE id = $1`,
		id, b,
	)
	if err != nil {
		return nil, fmt.Errorf("update schema: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("tenant %q: %w", id, ErrNotFound)
	}

	// Apply the DDL synchronously so a schema change takes effect even when the async
	// Redis migration worker is not running. This is the ONLY place destructive drops
	// are applied, and only the ones explicitly approved (approvedDrops); every other
	// drop stays gated as drift.
	outcome, err := migration.ApplyTenantMigrationApproved(ctx, s.pool, "tenant_"+id, apiSchema, approvedDrops)
	if err != nil {
		return nil, fmt.Errorf("apply migration: %w", err)
	}

	// Enqueue async migration to Redis Stream (best-effort — don't fail the request).
	// The async worker re-applies ADDITIVELY (migration.ApplyTenantMigration, NO
	// approval): it can NEVER auto-approve a drop. The approved drops are already done
	// synchronously above, so a re-diff finds nothing left to drop — the worker job is
	// a harmless, idempotent additive convergence + cache-invalidation signal.
	if s.redis != nil {
		s.redis.XAdd(ctx, &redis.XAddArgs{ //nolint:errcheck
			Stream: "migrations",
			Values: map[string]any{"tenant_id": id, "schema": string(b)},
		})
	}

	// Notify in-process caches to reload this tenant's schema.
	s.pool.Exec(ctx, "SELECT pg_notify('schema_updated', $1)", id) //nolint:errcheck
	return outcome, nil
}

func (s *pgService) PreviewSchema(ctx context.Context, id string, apiSchema *schema.APISchema, approvedDrops []string) (*migration.Preview, error) {
	// Verify the tenant exists first — a dry-run against an unknown tenant would
	// introspect an empty schema and misleadingly report "create everything".
	if _, err := s.GetByID(ctx, id); err != nil {
		return nil, err
	}
	// A pure dry run: compute + classify the plan with impact, write NOTHING (no
	// json_schema update, no DDL, no notify).
	return migration.PreviewTenantMigration(ctx, s.pool, "tenant_"+id, apiSchema, approvedDrops)
}

func (s *pgService) GetSchema(ctx context.Context, id string) (*schema.APISchema, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT json_schema FROM public.tenants WHERE id = $1`,
		id,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get schema: %w", err)
	}
	if raw == nil {
		return nil, nil // tenant exists but schema not yet set
	}
	var out schema.APISchema
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return &out, nil
}
