package migration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// ENG-9 (CONSUMER-PATH-S1): objects NO deployed schema version ever declared —
// a consumer's generated column, a consumer side table — are EXTERNAL: reported
// as consumer-owned drift, never proposed as destructive drops, never
// approvable. Objects a PAST version declared stay fully approvable (the gate's
// purpose is intact). Without control-plane records the behavior is the legacy
// one, which every other test in this package already covers.

// setupOwnedTenant provisions a tenant WITH control-plane records (tenants row +
// history), the way real registration does.
func setupOwnedTenant(t *testing.T, pool *pgxpool.Pool, tenantID, pg string, s *schema.APISchema) {
	t.Helper()
	ctx := context.Background()
	ddl, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		t.Fatalf("read control plane DDL: %v", err)
	}
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatalf("apply control plane DDL: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA `+pg); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := ApplyTenantMigrationApproved(ctx, pool, pg, s, nil); err != nil {
		t.Fatalf("provision: %v", err)
	}
	raw, _ := json.Marshal(s)
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.tenants (id, pg_schema, display_name, email, plan, json_schema)
		VALUES ($1, $2, 'T', 't@x.co', 'free', $3)`, tenantID, pg, raw); err != nil {
		t.Fatalf("insert tenant row: %v", err)
	}
	if _, _, err := schemahistory.Append(ctx, pool, tenantID, raw, schemahistory.SourceRegister, ""); err != nil {
		t.Fatalf("append history: %v", err)
	}
}

func TestExternal_ConsumerObjectsNeverProposed(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_ext"

	base := mkSchema(map[string]schema.ResourceSchema{
		"productos": {Fields: map[string]schema.FieldDef{
			"nombre":   {Type: "string", Required: true},
			"telefono": {Type: "string"},
		}},
	})
	setupOwnedTenant(t, pool, "ext", pg, base)

	// The consumer's own DDL — exactly what commerce's BeforeStart applies: a
	// generated column on an engine table, and a side table of its own.
	if _, err := pool.Exec(ctx, `ALTER TABLE tenant_ext.productos
		ADD COLUMN attr_marca text GENERATED ALWAYS AS (upper(nombre)) STORED`); err != nil {
		t.Fatalf("consumer column: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE tenant_ext.consumer_side (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), note text)`); err != nil {
		t.Fatalf("consumer table: %v", err)
	}
	pool.Exec(ctx, `INSERT INTO tenant_ext.productos (nombre, telefono) VALUES ('acme','1')`) //nolint:errcheck

	// Dry-run with the UNCHANGED schema: nothing destructive may be proposed —
	// before ENG-9 this reported "DROP COLUMN productos.attr_marca — data lost"
	// on every single dry-run, forever.
	pv, err := PreviewTenantMigration(ctx, pool, pg, base, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if pv.HasDestructive() {
		t.Fatalf("consumer-owned objects proposed as destructive drops: %+v", pv.Destructive)
	}
	joined := strings.Join(pv.External, " | ")
	if !strings.Contains(joined, "attr_marca") || !strings.Contains(joined, "consumer_side") {
		t.Fatalf("external must list the consumer column AND table, got: %q", joined)
	}

	// Approving an external key must be REFUSED (unmatched), and the objects must
	// survive the apply untouched.
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, base,
		[]string{"productos.attr_marca", "consumer_side"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(out.AppliedDrops) != 0 {
		t.Fatalf("an external object was DROPPED via approval: %v", out.AppliedDrops)
	}
	if len(out.UnmatchedApprovals) != 2 {
		t.Fatalf("external approvals must be unmatched, got: %+v", out)
	}
	if !columnSet(t, pool, pg, "productos")["attr_marca"] {
		t.Fatal("consumer column was dropped")
	}
	if !tableExists(t, pool, pg, "consumer_side") {
		t.Fatal("consumer table was dropped")
	}

	// A field a schema version DID declare stays a real, approvable drop — the
	// gate is intact where it belongs.
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"productos": {Fields: map[string]schema.FieldDef{
			"nombre": {Type: "string", Required: true},
		}},
	})
	pv2, err := PreviewTenantMigration(ctx, pool, pg, shrunk, nil)
	if err != nil {
		t.Fatalf("preview shrunk: %v", err)
	}
	tel := findDestructive(t, pv2, "productos.telefono")
	if tel.RowsLost != 1 {
		t.Fatalf("telefono (schema-owned) must stay approvable with impact, got %+v", tel)
	}
	if strings.Contains(strings.Join(pv2.External, " "), "telefono") {
		t.Fatal("a schema-owned field leaked into External")
	}
}

// A field declared by a PAST version and later removed stays approvable across
// LATER deploys — ownership comes from the whole history, not just the current
// schema, so "decline the drop today, approve it next quarter" keeps working.
func TestExternal_HistoricalFieldStaysApprovable(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_hist"

	v1 := mkSchema(map[string]schema.ResourceSchema{
		"cosas": {Fields: map[string]schema.FieldDef{
			"nombre": {Type: "string", Required: true},
			"legacy": {Type: "string"},
		}},
	})
	setupOwnedTenant(t, pool, "hist", pg, v1)
	pool.Exec(ctx, `INSERT INTO tenant_hist.cosas (nombre, legacy) VALUES ('x','old')`) //nolint:errcheck

	// Deploy v2 (legacy removed): the drop is gated, and v2 is PERSISTED — the
	// current schema no longer mentions `legacy` at all.
	v2 := mkSchema(map[string]schema.ResourceSchema{
		"cosas": {Fields: map[string]schema.FieldDef{
			"nombre": {Type: "string", Required: true},
		}},
	})
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, v2, nil)
	if err != nil {
		t.Fatalf("apply v2: %v", err)
	}
	if len(out.GatedDrops) != 1 || out.GatedDrops[0] != "cosas.legacy" {
		t.Fatalf("v2 must gate cosas.legacy, got %+v", out)
	}
	if err := PersistTenantSchema(ctx, pool, "hist", v2); err != nil {
		t.Fatalf("persist v2: %v", err)
	}

	// A LATER dry-run (current schema = v2, without the field): the column must
	// STILL be an approvable destructive drop — v1 declared it.
	pv, err := PreviewTenantMigration(ctx, pool, pg, v2, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	d := findDestructive(t, pv, "cosas.legacy")
	if d.RowsLost != 1 {
		t.Fatalf("historical field lost its approvability: %+v", pv)
	}

	// And approving it NOW actually drops it (the gate still works end to end).
	out2, err := ApplyTenantMigrationApproved(ctx, pool, pg, v2, []string{"cosas.legacy"})
	if err != nil {
		t.Fatalf("apply approved: %v", err)
	}
	if len(out2.AppliedDrops) != 1 {
		t.Fatalf("approved historical drop not applied: %+v", out2)
	}
	if columnSet(t, pool, pg, "cosas")["legacy"] {
		t.Fatal("legacy column still present after approved drop")
	}
}
