package controlplane_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/controlplane"
)

// ENG-8 (CONSUMER-PATH-S1): the consumer's per-tenant provisioning seam. The
// hook must run inside registration — AFTER the engine's tables exist — so
// consumer DDL reaches tenants created post-boot (the normal SaaS flow), and a
// hook failure must roll the whole registration back (all-or-nothing).
func TestRegisterTenantWithHook_RunsConsumerDDL(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	var gotTenant, gotSchema string
	hook := func(ctx context.Context, p *pgxpool.Pool, tenantID, pgSchema string) error {
		gotTenant, gotSchema = tenantID, pgSchema
		// The engine's tables must ALREADY exist when the hook runs — the hook's
		// whole purpose is DDL over them (a generated column, a CHECK).
		if !tableExists(t, p, pgSchema, "items") {
			return errors.New("hook ran before the engine provisioned the tenant's tables")
		}
		// Real consumer-style DDL: a generated column on an engine table.
		_, err := p.Exec(ctx, fmt.Sprintf(
			`ALTER TABLE %s.items ADD COLUMN IF NOT EXISTS name_upper text GENERATED ALWAYS AS (upper(name)) STORED`,
			pgSchema))
		return err
	}

	tn, err := controlplane.RegisterTenantWithHook(context.Background(), pool,
		controlplane.RegisterRequest{
			TenantID: "hooked", DisplayName: "Hooked", Email: "h@x.co", Plan: "free",
			Schema: minimalSchema(),
		}, hook)
	if err != nil {
		t.Fatalf("register with hook: %v", err)
	}
	if gotTenant != "hooked" || gotSchema != "tenant_hooked" {
		t.Fatalf("hook args = (%q, %q), want (hooked, tenant_hooked)", gotTenant, gotSchema)
	}
	if tn.PGSchema != "tenant_hooked" {
		t.Fatalf("tenant pg_schema = %q", tn.PGSchema)
	}
	// The consumer DDL is live on the fresh tenant — no restart involved.
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_schema='tenant_hooked' AND table_name='items' AND column_name='name_upper'`,
	).Scan(&n); err != nil || n != 1 {
		t.Fatalf("consumer column missing after registration (n=%d err=%v)", n, err)
	}
}

func TestRegisterTenantWithHook_FailureRollsBackRegistration(t *testing.T) {
	pool, cleanup := startPostgres(t)
	defer cleanup()
	applyControlPlane(t, pool)

	boom := errors.New("consumer DDL failed")
	_, err := controlplane.RegisterTenantWithHook(context.Background(), pool,
		controlplane.RegisterRequest{
			TenantID: "doomed", DisplayName: "Doomed", Email: "d@x.co", Plan: "free",
			Schema: minimalSchema(),
		},
		func(ctx context.Context, p *pgxpool.Pool, tenantID, pgSchema string) error { return boom })
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("want the hook error surfaced, got %v", err)
	}
	// All-or-nothing: no tenant row, no physical schema — never half-provisioned.
	var exists bool
	pool.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id='doomed')").Scan(&exists)
	if exists {
		t.Fatal("tenant row survived a hook failure — registration must be all-or-nothing")
	}
	if schemaExists(t, pool, "tenant_doomed") {
		t.Fatal("physical schema survived a hook failure")
	}
	// The id is reusable after the rollback (the real-world retry).
	if _, err := controlplane.RegisterTenant(context.Background(), pool,
		controlplane.RegisterRequest{
			TenantID: "doomed", DisplayName: "Doomed", Email: "d@x.co", Plan: "free",
			Schema: minimalSchema(),
		}); err != nil {
		t.Fatalf("re-register after rolled-back hook failure: %v", err)
	}
}
