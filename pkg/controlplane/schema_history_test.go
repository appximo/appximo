package controlplane_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemahistory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VERSION-S1 integration: the full history + rollback path against a real
// Postgres — register (v1) → deploy v2 (new column, data written) → deploy v3
// → history is the append-only timeline → rolling back to v1 runs the REAL
// migration machinery (the v2/v3 additions become gated destructive drops with
// measured rows_lost; nothing is dropped without enumeration) → the approved
// rollback drops them and appends v4 whose content equals v1.
func TestIntegration_SchemaHistoryAndRollback(t *testing.T) {
	pool, done := startPostgres(t)
	defer done()
	applyControlPlane(t, pool)
	ctx := context.Background()
	svc := controlplane.NewService(pool, nil)

	// v1 — register.
	v1 := minimalSchema()
	if _, err := svc.Register(ctx, controlplane.RegisterRequest{
		TenantID: "hist", DisplayName: "Hist", Email: "h@x.com", Plan: "free", Schema: v1,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// v2 — add a nullable column, then put DATA in it (the rows a rollback loses).
	v2 := minimalSchema()
	v2.Resources["items"] = withField(v2.Resources["items"], "notes", schema.FieldDef{Type: "string"})
	if err := svc.UpdateSchema(ctx, "hist", v2); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenant_hist.items (name, notes) VALUES ('a', 'keep-me'), ('b', 'me-too')`); err != nil {
		t.Fatalf("seed v2 data: %v", err)
	}

	// v3 — another column.
	v3 := minimalSchema()
	v3.Resources["items"] = withField(v3.Resources["items"], "notes", schema.FieldDef{Type: "string"})
	v3.Resources["items"] = withField(v3.Resources["items"], "priority", schema.FieldDef{Type: "int"})
	if err := svc.UpdateSchema(ctx, "hist", v3); err != nil {
		t.Fatalf("deploy v3: %v", err)
	}

	// Re-deploying v3 unchanged must NOT create a new version (hash dedup).
	if err := svc.UpdateSchema(ctx, "hist", v3); err != nil {
		t.Fatalf("re-deploy v3: %v", err)
	}

	// The timeline: exactly 3 versions, newest first, right sources.
	page, err := svc.ListSchemaHistory(ctx, "hist", 1, 50)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if page.Total != 3 || len(page.Versions) != 3 {
		t.Fatalf("history total = %d (%d rows), want 3 — a rollback trail must not spam duplicates", page.Total, len(page.Versions))
	}
	if page.Versions[0].Version != 3 || page.Versions[0].Source != schemahistory.SourceDeploy ||
		page.Versions[2].Version != 1 || page.Versions[2].Source != schemahistory.SourceRegister {
		t.Fatalf("timeline wrong: %+v", page.Versions)
	}

	// Get v1: the stored schema round-trips.
	got1, err := svc.GetSchemaVersion(ctx, "hist", 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	var sc1 schema.APISchema
	if err := json.Unmarshal(got1.SchemaJSON, &sc1); err != nil {
		t.Fatalf("v1 schema unreadable: %v", err)
	}
	if _, has := sc1.Resources["items"].Fields["notes"]; has {
		t.Fatal("v1 must not contain the v2 column")
	}
	if _, err := svc.GetSchemaVersion(ctx, "hist", 99); !errors.Is(err, schemahistory.ErrVersionNotFound) {
		t.Fatalf("get v99: err = %v, want ErrVersionNotFound", err)
	}

	// Preview of the rollback (the honesty surface): reverting to v1 must
	// classify the v2/v3 columns as GATED destructive drops with the measured
	// impact — notes has 2 rows of data to lose.
	pv, err := svc.PreviewSchema(ctx, "hist", &sc1, nil)
	if err != nil {
		t.Fatalf("rollback preview: %v", err)
	}
	drops := map[string]int64{}
	for _, d := range pv.Destructive {
		if d.Approved {
			t.Fatalf("nothing approved yet, but %q is marked approved", d.Key)
		}
		drops[d.Key] = d.RowsLost
	}
	if drops["items.notes"] != 2 || drops["items.priority"] != 0 {
		t.Fatalf("impact wrong: %v (want items.notes→2 rows lost, items.priority→0)", drops)
	}

	// Rollback WITHOUT approval: nothing dropped (fail-safe), but the schema
	// record + history move to v1's content (v4 appended).
	res, err := svc.RollbackSchema(ctx, "hist", 1, nil)
	if err != nil {
		t.Fatalf("rollback (no approvals): %v", err)
	}
	if res.NewVersion != 4 || res.TargetVersion != 1 {
		t.Fatalf("rollback versions: new=%d target=%d, want 4/1", res.NewVersion, res.TargetVersion)
	}
	if len(res.Outcome.AppliedDrops) != 0 || len(res.Outcome.GatedDrops) != 2 {
		t.Fatalf("unapproved rollback must gate both drops: %+v", res.Outcome)
	}
	if !columnExists(t, pool, "tenant_hist", "items", "notes") {
		t.Fatal("unapproved rollback dropped items.notes — the gate failed")
	}

	// Approved rollback: enumerate both keys → the columns actually drop, the
	// pre-existing v1 data survives, and the history appended AGAIN? No — the
	// schema is unchanged vs v4 (same hash), so no v5 spam.
	res, err = svc.RollbackSchema(ctx, "hist", 1, []string{"items.notes", "items.priority"})
	if err != nil {
		t.Fatalf("rollback (approved): %v", err)
	}
	if len(res.Outcome.AppliedDrops) != 2 {
		t.Fatalf("approved rollback applied %v, want both drops", res.Outcome.AppliedDrops)
	}
	if columnExists(t, pool, "tenant_hist", "items", "notes") || columnExists(t, pool, "tenant_hist", "items", "priority") {
		t.Fatal("approved drops did not execute")
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM tenant_hist.items`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("v1-era rows must survive the rollback: rows=%d err=%v", rows, err)
	}
	page, _ = svc.ListSchemaHistory(ctx, "hist", 1, 50)
	if page.Total != 4 {
		t.Fatalf("history total = %d, want 4 (append-only: v4 = v1's content; the second, same-schema rollback dedups)", page.Total)
	}
	if page.Versions[0].Source != schemahistory.SourceRollback || page.Versions[0].Note != "rollback to v1" {
		t.Fatalf("v4 must be tagged as the rollback: %+v", page.Versions[0])
	}
	if page.Versions[0].Hash != page.Versions[3].Hash {
		t.Fatal("v4's content hash must equal v1's (rollback = old content as a new version)")
	}
}

func withField(r schema.ResourceSchema, name string, def schema.FieldDef) schema.ResourceSchema {
	fields := map[string]schema.FieldDef{}
	for k, v := range r.Fields {
		fields[k] = v
	}
	fields[name] = def
	r.Fields = fields
	return r
}

func columnExists(t *testing.T, pool *pgxpool.Pool, pgSchema, table, col string) bool {
	t.Helper()
	var exists bool
	pool.QueryRow(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3)`,
		pgSchema, table, col,
	).Scan(&exists) //nolint:errcheck
	return exists
}
