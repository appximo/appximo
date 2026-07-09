package flowtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Persistence: POSTGRES, next to public.schema_history — deliberately not the
// obs SQLite. Flows are TENANT/APP data (they describe the app's behavior, they
// anchor to schema versions, the fleet's per-app processes read them from the
// app's own database, and a tenant CASCADE-deletes them); the obs SQLite is
// per-instance telemetry. Same reasoning STATE-AUDIT-V1 §5 gave for the
// version history itself.

// ErrFlowNotFound marks a lookup of a flow the tenant does not have.
var ErrFlowNotFound = errors.New("flow not found")

// ErrDuplicateName marks a create/rename colliding with an existing flow name.
var ErrDuplicateName = errors.New("a flow with that name already exists")

// StoredFlow is a persisted flow with its identity.
type StoredFlow struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Steps     int       `json:"steps"`
	Flow      *Flow     `json:"flow,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Run is one persisted execution (a single flow or the whole suite), anchored
// to the schema version it ran against — the regression trail.
type Run struct {
	ID            int64           `json:"id"`
	TenantID      string          `json:"tenant_id"`
	SchemaVersion int             `json:"schema_version"`
	Scope         string          `json:"scope"` // "suite" or the flow name
	Pass          bool            `json:"pass"`
	FlowsTotal    int             `json:"flows_total"`
	FlowsFailed   int             `json:"flows_failed"`
	StepsTotal    int             `json:"steps_total"`
	StepsFailed   int             `json:"steps_failed"`
	Results       json.RawMessage `json:"results,omitempty"` // []FlowResult
	CreatedAt     time.Time       `json:"created_at"`
}

// EnsureTables creates the flow tables idempotently (the outbox/schema_history
// pattern — existing databases predate the canonical DDL in migrations/001).
func EnsureTables(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS public.flow_tests (
			id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id  TEXT        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
			name       TEXT        NOT NULL,
			flow       JSONB       NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (tenant_id, name)
		);
		CREATE TABLE IF NOT EXISTS public.flow_runs (
			id             BIGSERIAL   PRIMARY KEY,
			tenant_id      TEXT        NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
			schema_version INT         NOT NULL DEFAULT 0,
			scope          TEXT        NOT NULL,
			pass           BOOLEAN     NOT NULL,
			flows_total    INT         NOT NULL DEFAULT 0,
			flows_failed   INT         NOT NULL DEFAULT 0,
			steps_total    INT         NOT NULL DEFAULT 0,
			steps_failed   INT         NOT NULL DEFAULT 0,
			results        JSONB       NOT NULL,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS idx_flow_runs_tenant
			ON public.flow_runs (tenant_id, id DESC)`)
	if err != nil {
		return fmt.Errorf("flowtest: ensure tables: %w", err)
	}
	return nil
}

// SaveFlow inserts (id=="") or updates a flow. The flow must already be
// Validate()d by the caller.
func SaveFlow(ctx context.Context, pool *pgxpool.Pool, tenantID, id string, f *Flow) (*StoredFlow, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("flowtest: marshal: %w", err)
	}
	var out StoredFlow
	out.TenantID = tenantID
	out.Name = f.Name
	out.Steps = len(f.Steps)
	if id == "" {
		err = pool.QueryRow(ctx, `
			INSERT INTO public.flow_tests (tenant_id, name, flow)
			VALUES ($1, $2, $3) RETURNING id, created_at, updated_at`,
			tenantID, f.Name, b).Scan(&out.ID, &out.CreatedAt, &out.UpdatedAt)
	} else {
		out.ID = id
		err = pool.QueryRow(ctx, `
			UPDATE public.flow_tests SET name = $3, flow = $4, updated_at = now()
			WHERE tenant_id = $1 AND id = $2 RETURNING created_at, updated_at`,
			tenantID, id, f.Name, b).Scan(&out.CreatedAt, &out.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFlowNotFound
		}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil, ErrDuplicateName
	}
	if err != nil {
		return nil, fmt.Errorf("flowtest: save: %w", err)
	}
	return &out, nil
}

// ListFlows returns the tenant's flows, name order (the suite runs in this
// order — deterministic, like the fan-out's tenant enumeration).
func ListFlows(ctx context.Context, pool *pgxpool.Pool, tenantID string) ([]StoredFlow, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, jsonb_array_length(flow->'steps'), created_at, updated_at
		FROM public.flow_tests WHERE tenant_id = $1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("flowtest: list: %w", err)
	}
	defer rows.Close()
	out := []StoredFlow{}
	for rows.Next() {
		f := StoredFlow{TenantID: tenantID}
		if err := rows.Scan(&f.ID, &f.Name, &f.Steps, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFlow returns one flow WITH its steps.
func GetFlow(ctx context.Context, pool *pgxpool.Pool, tenantID, id string) (*StoredFlow, error) {
	var raw []byte
	f := StoredFlow{ID: id, TenantID: tenantID}
	err := pool.QueryRow(ctx, `
		SELECT name, flow, created_at, updated_at
		FROM public.flow_tests WHERE tenant_id = $1 AND id = $2`,
		tenantID, id).Scan(&f.Name, &raw, &f.CreatedAt, &f.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFlowNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("flowtest: get: %w", err)
	}
	var fl Flow
	if err := json.Unmarshal(raw, &fl); err != nil {
		return nil, fmt.Errorf("flowtest: stored flow unreadable: %w", err)
	}
	f.Flow = &fl
	f.Steps = len(fl.Steps)
	return &f, nil
}

// DeleteFlow removes one flow (its runs stay — the regression trail survives).
func DeleteFlow(ctx context.Context, pool *pgxpool.Pool, tenantID, id string) error {
	tag, err := pool.Exec(ctx,
		`DELETE FROM public.flow_tests WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("flowtest: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrFlowNotFound
	}
	return nil
}

// SaveRun persists an execution's verdict + full per-step results, anchored to
// the schema version it ran against.
func SaveRun(ctx context.Context, pool *pgxpool.Pool, run *Run) error {
	err := pool.QueryRow(ctx, `
		INSERT INTO public.flow_runs
		  (tenant_id, schema_version, scope, pass, flows_total, flows_failed, steps_total, steps_failed, results)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, created_at`,
		run.TenantID, run.SchemaVersion, run.Scope, run.Pass,
		run.FlowsTotal, run.FlowsFailed, run.StepsTotal, run.StepsFailed, run.Results).
		Scan(&run.ID, &run.CreatedAt)
	if err != nil {
		return fmt.Errorf("flowtest: save run: %w", err)
	}
	return nil
}

// ListRuns returns the tenant's run history, newest first (without the heavy
// per-step results; fetch one run for the detail).
func ListRuns(ctx context.Context, pool *pgxpool.Pool, tenantID string, limit int) ([]Run, error) {
	if limit < 1 || limit > 200 {
		limit = 30
	}
	rows, err := pool.Query(ctx, `
		SELECT id, schema_version, scope, pass, flows_total, flows_failed, steps_total, steps_failed, created_at
		FROM public.flow_runs WHERE tenant_id = $1 ORDER BY id DESC LIMIT $2`,
		tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("flowtest: list runs: %w", err)
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r := Run{TenantID: tenantID}
		if err := rows.Scan(&r.ID, &r.SchemaVersion, &r.Scope, &r.Pass,
			&r.FlowsTotal, &r.FlowsFailed, &r.StepsTotal, &r.StepsFailed, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns one run WITH its full results.
func GetRun(ctx context.Context, pool *pgxpool.Pool, tenantID string, id int64) (*Run, error) {
	r := Run{ID: id, TenantID: tenantID}
	err := pool.QueryRow(ctx, `
		SELECT schema_version, scope, pass, flows_total, flows_failed, steps_total, steps_failed, results, created_at
		FROM public.flow_runs WHERE tenant_id = $1 AND id = $2`,
		tenantID, id).Scan(&r.SchemaVersion, &r.Scope, &r.Pass,
		&r.FlowsTotal, &r.FlowsFailed, &r.StepsTotal, &r.StepsFailed, &r.Results, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrFlowNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("flowtest: get run: %w", err)
	}
	return &r, nil
}
