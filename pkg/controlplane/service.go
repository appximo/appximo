package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
)

// Service is the control plane dependency injected into the HTTP handlers.
// All methods are safe to mock in unit tests.
type Service interface {
	Register(ctx context.Context, req RegisterRequest) (*Tenant, error)
	GetByID(ctx context.Context, id string) (*Tenant, error)
	UpdateSchema(ctx context.Context, id string, s *schema.APISchema) error
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
	b, err := json.Marshal(apiSchema)
	if err != nil {
		return fmt.Errorf("marshal schema: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE public.tenants SET json_schema = $2, updated_at = now() WHERE id = $1`,
		id, b,
	)
	if err != nil {
		return fmt.Errorf("update schema: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("tenant %q: %w", id, ErrNotFound)
	}

	// Apply the DDL synchronously (CREATE TABLE / ADD COLUMN IF NOT EXISTS — all
	// idempotent) so a schema change actually takes effect even when the async Redis
	// migration worker is not running. When Redis IS configured the worker also picks
	// it up; re-running the same idempotent DDL is harmless. This is what makes
	// "edit a field → deploy → the API reflects it" work without a worker.
	if err := migration.ApplyTenantMigration(ctx, s.pool, "tenant_"+id, apiSchema); err != nil {
		return fmt.Errorf("apply migration: %w", err)
	}

	// Enqueue async migration to Redis Stream (best-effort — don't fail the request).
	if s.redis != nil {
		s.redis.XAdd(ctx, &redis.XAddArgs{ //nolint:errcheck
			Stream: "migrations",
			Values: map[string]any{"tenant_id": id, "schema": string(b)},
		})
	}

	// Notify in-process caches to reload this tenant's schema.
	s.pool.Exec(ctx, "SELECT pg_notify('schema_updated', $1)", id) //nolint:errcheck
	return nil
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
