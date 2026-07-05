package fleet

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/miguelangel/appitools/migrations"
)

// BootstrapControlPlane applies the canonical control-plane DDL (embedded from
// migrations/001_control_plane.sql — public.tenants, tenant_policies,
// migration_log, the schema_updated trigger) to one app's database.
//
// Rationale: in the single-app deployment docker-compose's initdb applies this
// file; a fleet gives EACH app its own fresh database, so provisioning it is
// the orchestrator's job (both the multi-process supervisor and the in-process
// ServeFleet runtime call this). Idempotent (IF NOT EXISTS / OR REPLACE) — a no-op on
// an already-initialized database. The engine itself stays untouched.
func BootstrapControlPlane(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(context.Background()) //nolint:errcheck
	// No bind args ⇒ pgx uses the simple protocol, so the multi-statement DDL
	// (including the plpgsql trigger function) applies in one call.
	if _, err := conn.Exec(ctx, migrations.ControlPlane); err != nil {
		return fmt.Errorf("apply control-plane DDL: %w", err)
	}
	return nil
}
