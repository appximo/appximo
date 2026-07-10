package controlplane

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/miguelangel/appitools/pkg/schema"
)

// SetApplyMigrationForTest swaps the registration's migration step so tests can
// force a deterministic post-commit failure and assert the all-or-nothing
// rollback (a real ApplyTenantMigration failure is hard to provoke on a fresh,
// empty schema). Returns the restore func.
func SetApplyMigrationForTest(f func(ctx context.Context, pool *pgxpool.Pool, pgSchema string, s *schema.APISchema) error) (restore func()) {
	old := applyTenantMigration
	applyTenantMigration = f
	return func() { applyTenantMigration = old }
}
