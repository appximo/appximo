package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
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

	// ── schema version history + rollback (VERSION-S1) ──────────────────────

	// ListSchemaHistory returns one page of the tenant's deployed-schema history,
	// newest first (append-only; the latest version is the current schema).
	ListSchemaHistory(ctx context.Context, id string, page, perPage int) (*schemahistory.Page, error)
	// GetSchemaVersion returns one recorded version WITH its full schema.
	GetSchemaVersion(ctx context.Context, id string, version int) (*schemahistory.Version, error)
	// RollbackSchema re-applies the stored schema of history version v — the SAME
	// diff→gate→apply migration path as UpdateSchemaApproved (NOT a second engine),
	// so what later versions added is reverted as gated destructive drops, and data
	// already lost by an approved forward drop is NOT recovered. Append-only: the
	// rollback records a NEW version whose content is v's.
	RollbackSchema(ctx context.Context, id string, version int, approvedDrops []string) (*RollbackResult, error)
}

// RollbackResult reports an applied rollback: the migration outcome (what was
// applied/gated, same shape as a deploy), the version rolled back TO, the NEW
// history version the rollback appended (0 if the history write failed —
// logged, never blocks the applied migration), and the schema now live.
type RollbackResult struct {
	Outcome       *migration.ApplyOutcome
	TargetVersion int
	NewVersion    int
	Schema        *schema.APISchema
}

// pgService is the production implementation backed by pgxpool + optional Redis.
type pgService struct {
	pool          *pgxpool.Pool
	redis         *redis.Client // nil when REDIS_URL is not set
	provisionHook TenantProvisionHook
}

// ServiceOption configures NewService (variadic so existing call sites are
// untouched).
type ServiceOption func(*pgService)

// WithProvisionHook wires the consumer's per-tenant provisioning seam (ENG-8)
// into every registration this Service performs. See TenantProvisionHook.
func WithProvisionHook(h TenantProvisionHook) ServiceOption {
	return func(s *pgService) { s.provisionHook = h }
}

// NewService creates a production Service. redisClient may be nil — in that case
// schema updates are written to the DB only and pg_notify handles cache invalidation.
func NewService(pool *pgxpool.Pool, redisClient *redis.Client, opts ...ServiceOption) Service {
	s := &pgService{pool: pool, redis: redisClient}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *pgService) Register(ctx context.Context, req RegisterRequest) (*Tenant, error) {
	return RegisterTenantWithHook(ctx, s.pool, req, s.provisionHook)
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
	outcome, _, err := s.updateSchemaSourced(ctx, id, apiSchema, approvedDrops, schemahistory.SourceDeploy, "")
	return outcome, err
}

// updateSchemaSourced is the single json_schema write path: persist + migrate +
// notify, and APPEND the schema to the version history tagged with its source
// (deploy vs rollback). Returns the migration outcome and the history version
// recorded (0 when the append failed or was a no-op re-deploy).
func (s *pgService) updateSchemaSourced(ctx context.Context, id string, apiSchema *schema.APISchema, approvedDrops []string, source, note string) (*migration.ApplyOutcome, int, error) {
	b, err := json.Marshal(apiSchema)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal schema: %w", err)
	}
	// Keep the schema the tenant is on RIGHT NOW: if the migration does not fully
	// land, the record is restored to it. The engine's contract is that a tenant's
	// recorded schema describes its real database — a deploy that half-applies must
	// not leave the record claiming a shape the tables do not have (ENG-13).
	var previous []byte
	_ = s.pool.QueryRow(ctx,
		`SELECT json_schema FROM public.tenants WHERE id = $1`, id).Scan(&previous)
	// Before overwriting, make sure the schema being replaced is IN the history —
	// it is what tells a later dry-run that a now-removed column was once declared
	// (and is therefore an approvable drop, not a consumer's own column). See
	// schemahistory.EnsureSeeded.
	schemahistory.EnsureSeeded(ctx, s.pool, id)

	tag, err := s.pool.Exec(ctx, `
		UPDATE public.tenants SET json_schema = $2, updated_at = now() WHERE id = $1`,
		id, b,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("update schema: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, 0, fmt.Errorf("tenant %q: %w", id, ErrNotFound)
	}

	// Apply the DDL synchronously so a schema change takes effect even when the async
	// Redis migration worker is not running. This is the ONLY place destructive drops
	// are applied, and only the ones explicitly approved (approvedDrops); every other
	// drop stays gated as drift.
	outcome, err := migration.ApplyTenantMigrationApproved(ctx, s.pool, "tenant_"+id, apiSchema, approvedDrops)
	if err != nil {
		s.restoreSchema(ctx, id, previous, "the migration failed")
		return nil, 0, fmt.Errorf("apply migration: %w", err)
	}
	// A PARTIAL apply is a failed deploy: the database does not have everything the
	// new schema declares, so the record goes back to the schema the database DOES
	// have. Reporting it as applied is the exact failure ENG-13 named.
	if outcome.Partial() {
		s.restoreSchema(ctx, id, previous, "the migration did not fully apply")
		return nil, 0, fmt.Errorf("apply migration: PARTIAL — the database does not have everything this schema declares, "+
			"so it was NOT saved (the tenant keeps the previous one). Not applied: %s",
			strings.Join(outcome.Unapplied, "; "))
	}

	// Version history (VERSION-S1): the history mirrors json_schema, and is appended
	// only once the migration ACTUALLY applied — a version in the trail is a version
	// the database really took. Best-effort: a history failure is logged loudly,
	// never blocks the deploy that already persisted and applied.
	version, appended, histErr := schemahistory.Append(ctx, s.pool, id, b, source, note)
	if histErr != nil {
		log.Printf("WARNING: schema history append for tenant %q failed (the deploy itself succeeded): %v", id, histErr)
		version = 0
	} else if !appended {
		version = 0 // unchanged schema — no new version recorded
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
	return outcome, version, nil
}

// restoreSchema puts the tenant's recorded schema back to what it was before a
// deploy that did not fully apply, and re-notifies the in-process caches — so the
// running engine goes on serving the schema the DATABASE actually has. Best-effort
// and loudly logged: if even the restore fails, the operator is told exactly what
// diverged and how to put it back.
func (s *pgService) restoreSchema(ctx context.Context, id string, previous []byte, why string) {
	if previous == nil {
		return // the tenant had no schema recorded — nothing to go back to
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE public.tenants SET json_schema = $2, updated_at = now() WHERE id = $1`, id, previous); err != nil {
		log.Printf("CRITICAL: tenant %q — %s, and restoring its previous schema ALSO failed (%v). "+
			"The tenant record now claims a schema its database does not have; re-deploy the previous schema to realign.", id, why, err)
		return
	}
	log.Printf("tenant %q: %s — the recorded schema was rolled back to the previous one, so it keeps matching the database", id, why)
	s.pool.Exec(ctx, "SELECT pg_notify('schema_updated', $1)", id) //nolint:errcheck
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

// ── schema version history + rollback (VERSION-S1) ──────────────────────────

func (s *pgService) ListSchemaHistory(ctx context.Context, id string, page, perPage int) (*schemahistory.Page, error) {
	// Distinguish "tenant unknown" (404) from "tenant known, empty history".
	if _, err := s.GetByID(ctx, id); err != nil {
		return nil, err
	}
	return schemahistory.List(ctx, s.pool, id, page, perPage)
}

func (s *pgService) GetSchemaVersion(ctx context.Context, id string, version int) (*schemahistory.Version, error) {
	if _, err := s.GetByID(ctx, id); err != nil {
		return nil, err
	}
	return schemahistory.Get(ctx, s.pool, id, version)
}

func (s *pgService) RollbackSchema(ctx context.Context, id string, version int, approvedDrops []string) (*RollbackResult, error) {
	v, err := s.GetSchemaVersion(ctx, id, version)
	if err != nil {
		return nil, err
	}
	// Re-validate under the CURRENT engine rules: the schema was valid when it
	// was deployed, but the validator may have tightened since — the migration
	// must never run on a schema today's engine would reject (fail actionable).
	if errs := schema.CheckUnknownKeys(v.SchemaJSON); len(errs) > 0 {
		return nil, fmt.Errorf("stored v%d is no longer valid under the current engine: %s", version, errs[0].Error())
	}
	var target schema.APISchema
	if err := json.Unmarshal(v.SchemaJSON, &target); err != nil {
		return nil, fmt.Errorf("unmarshal stored v%d: %w", version, err)
	}
	if errs := schema.Validate(&target); len(errs) > 0 {
		return nil, fmt.Errorf("stored v%d is no longer valid under the current engine: %s", version, errs[0].Error())
	}
	// The rollback IS a deploy of the old schema: the same diff → destructive
	// gate → production-safe apply. Only the history tag differs.
	outcome, newVersion, err := s.updateSchemaSourced(ctx, id, &target, approvedDrops,
		schemahistory.SourceRollback, fmt.Sprintf("rollback to v%d", version))
	if err != nil {
		return nil, err
	}
	return &RollbackResult{Outcome: outcome, TargetVersion: version, NewVersion: newVersion, Schema: &target}, nil
}

// BackfillSchemaHistory captures the CURRENT schema of every pre-versioning
// tenant (json_schema set, history empty) as its v1 — run once at boot, so the
// history is immediately useful on an existing install. Each schema is
// re-marshaled through schema.APISchema so its hash is canonical (raw jsonb
// text would hash differently than the engine's own marshaling and break the
// unchanged-schema dedup). Best-effort per tenant; returns the first error
// after attempting all.
func BackfillSchemaHistory(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	svc := &pgService{pool: pool}
	ids, err := schemahistory.TenantsNeedingBackfill(ctx, pool)
	if err != nil {
		return 0, err
	}
	var firstErr error
	n := 0
	for _, id := range ids {
		sc, err := svc.GetSchema(ctx, id)
		if err != nil || sc == nil {
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("backfill %q: %w", id, err)
			}
			continue
		}
		b, err := json.Marshal(sc)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("backfill %q: %w", id, err)
			}
			continue
		}
		if _, _, err := schemahistory.Append(ctx, pool, id, b, schemahistory.SourceBackfill,
			"pre-versioning schema captured at upgrade"); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("backfill %q: %w", id, err)
			}
			continue
		}
		n++
	}
	return n, firstErr
}
