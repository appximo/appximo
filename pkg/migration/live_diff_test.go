package migration

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
)

// TestLiveDiff_NimbusIsNoop is a guarded live check (not part of the normal lanes):
// against a REAL database (MIG_LIVE_DSN) it introspects an existing converger-built
// tenant and asserts that diffing the erp-demo schema against it is a no-op — the
// red-de-seguridad invariant that a re-provision of an unchanged schema changes
// nothing. Run with:
//
//	MIG_LIVE_DSN=postgres://… MIG_LIVE_TENANT=tenant_nimbus \
//	  go test -run TestLiveDiff_NimbusIsNoop -count=1 ./pkg/migration/
func TestLiveDiff_NimbusIsNoop(t *testing.T) {
	dsn := os.Getenv("MIG_LIVE_DSN")
	if dsn == "" {
		t.Skip("MIG_LIVE_DSN not set — skipping live diff check")
	}
	pgSchema := os.Getenv("MIG_LIVE_TENANT")
	if pgSchema == "" {
		pgSchema = "tenant_nimbus"
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	s, err := schema.LoadFromFile("../../examples/erp-demo/schema.json")
	if err != nil {
		t.Fatalf("load erp schema: %v", err)
	}

	plan, _, err := diffTenant(ctx, pool, pgSchema, s, false)
	if err != nil {
		t.Fatalf("diffTenant: %v", err)
	}
	if !plan.Empty() {
		t.Errorf("expected EMPTY diff against converger-built %s — got:\n%s", pgSchema, plan)
	} else {
		t.Logf("OK: diff against %s is empty (re-provision is a no-op)", pgSchema)
	}
}
