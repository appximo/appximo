package migration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These integration tests exercise the RESUMABLE MULTI-TENANT ORCHESTRATOR end to
// end against a real Postgres: an additive change fans out to N tenants, a re-run is
// idempotent, a PARTIAL FAILURE does not abort the healthy tenants (and is resumable),
// and destructive drops stay gated unless mass-approved.

// setupControlPlane creates public.tenants + migration_log + the schema_updated
// trigger (the tables the orchestrator enumerates / persists / logs to).
func setupControlPlane(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	b, err := os.ReadFile("../../migrations/001_control_plane.sql")
	if err != nil {
		t.Fatalf("read control-plane migration: %v", err)
	}
	if _, err := pool.Exec(context.Background(), string(b)); err != nil {
		t.Fatalf("apply control-plane schema: %v", err)
	}
}

// seedFanoutTenant registers a tenant (public.tenants row + CREATE SCHEMA) and
// provisions it from baseSchema, exactly as RegisterTenant would.
func seedFanoutTenant(t *testing.T, pool *pgxpool.Pool, id string, baseSchema *schema.APISchema) {
	t.Helper()
	ctx := context.Background()
	js, err := json.Marshal(baseSchema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.tenants (id, pg_schema, display_name, email, plan, json_schema)
		VALUES ($1, $2, $3, 'x@y.z', 'free', $4)`,
		id, "tenant_"+id, id, js); err != nil {
		t.Fatalf("insert tenant %s: %v", id, err)
	}
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_`+id+`"`); err != nil {
		t.Fatalf("create schema for %s: %v", id, err)
	}
	if err := ApplyTenantMigration(ctx, pool, "tenant_"+id, baseSchema); err != nil {
		t.Fatalf("provision %s: %v", id, err)
	}
}

// storedSchema reads back a tenant's persisted json_schema field names for a resource.
func storedHasField(t *testing.T, pool *pgxpool.Pool, id, resource, field string) bool {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT json_schema FROM public.tenants WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatalf("read stored schema %s: %v", id, err)
	}
	var s schema.APISchema
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal stored schema %s: %v", id, err)
	}
	res, ok := s.Resources[resource]
	if !ok {
		return false
	}
	_, ok = res.Fields[field]
	return ok
}

func resultFor(res *FanoutResult, id string) TenantFanoutResult {
	for _, r := range res.Results {
		if r.TenantID == id {
			return r
		}
	}
	return TenantFanoutResult{TenantID: id, Status: "MISSING"}
}

// TestFanout_AdditiveAndIdempotent: an additive change reaches every tenant; a
// re-run is a pure no-op (the resume/idempotency guarantee).
func TestFanout_AdditiveAndIdempotent(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	setupControlPlane(t, pool)

	base := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	for _, id := range []string{"fa", "fb", "fc"} {
		seedFanoutTenant(t, pool, id, base)
	}

	// Additive: add a NULLABLE column to the base.
	evolved := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"note": {Type: "text"},
		}},
	})

	res, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved})
	if err != nil {
		t.Fatalf("fan-out: %v", err)
	}
	if res.Total != 3 || res.Applied != 3 || res.Failed != 0 {
		t.Fatalf("expected 3 applied, got total=%d applied=%d noop=%d failed=%d", res.Total, res.Applied, res.Noop, res.Failed)
	}
	for _, id := range []string{"fa", "fb", "fc"} {
		if !columnSet(t, pool, "tenant_"+id, "items")["note"] {
			t.Errorf("tenant %s did not get the note column", id)
		}
		if !storedHasField(t, pool, id, "items", "note") {
			t.Errorf("tenant %s json_schema was not persisted with note", id)
		}
	}

	// Idempotent re-run → every tenant is a no-op.
	res2, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved})
	if err != nil {
		t.Fatalf("re-run fan-out: %v", err)
	}
	if res2.Noop != 3 || res2.Applied != 0 {
		t.Fatalf("re-run must be all no-op, got applied=%d noop=%d failed=%d", res2.Applied, res2.Noop, res2.Failed)
	}
}

// TestFanout_PartialFailureAndResume is the heart of the resilience contract: a
// change that fails on ONE tenant (NOT NULL over existing rows) must apply to the
// healthy ones, record the failure, NOT abort — and a re-run after fixing the broken
// tenant resumes (healthy ones are no-ops, the fixed one applies).
func TestFanout_PartialFailureAndResume(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	setupControlPlane(t, pool)

	base := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	for _, id := range []string{"p1", "p2", "p3"} {
		seedFanoutTenant(t, pool, id, base)
	}
	// p2 has a row → a NEW required (NOT NULL) column will FAIL there (faithful NOT
	// NULL over populated data), while p1/p3 are empty and accept it.
	if _, err := pool.Exec(ctx, `INSERT INTO tenant_p2.items (name) VALUES ('keep')`); err != nil {
		t.Fatalf("seed p2 row: %v", err)
	}

	evolved := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"code": {Type: "string", Required: true}, // NOT NULL — fails over p2's row
		}},
	})

	res, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved})
	if err != nil {
		t.Fatalf("fan-out returned a top-level error (must be nil; failures are per-tenant): %v", err)
	}
	if res.Applied != 2 || res.Failed != 1 {
		t.Fatalf("expected 2 applied + 1 failed, got applied=%d noop=%d failed=%d", res.Applied, res.Noop, res.Failed)
	}
	if resultFor(res, "p2").Status != FanoutFailed {
		t.Errorf("p2 (populated) must FAIL, got %s", resultFor(res, "p2").Status)
	}
	// Healthy tenants got the column; the failed one did NOT (atomic rollback).
	if !columnSet(t, pool, "tenant_p1", "items")["code"] || !columnSet(t, pool, "tenant_p3", "items")["code"] {
		t.Errorf("healthy tenants must have the code column")
	}
	if columnSet(t, pool, "tenant_p2", "items")["code"] {
		t.Errorf("failed tenant p2 must NOT have the code column (atomic rollback, left in previous state)")
	}
	// p2's stored schema must NOT have been persisted (apply failed before persist).
	if storedHasField(t, pool, "p2", "items", "code") {
		t.Errorf("p2 json_schema must not be persisted on a failed apply")
	}

	// ── RESUME: fix p2 (empty its table), re-run → p1/p3 no-op, p2 now applies ──
	if _, err := pool.Exec(ctx, `DELETE FROM tenant_p2.items`); err != nil {
		t.Fatalf("empty p2: %v", err)
	}
	res2, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved})
	if err != nil {
		t.Fatalf("resume fan-out: %v", err)
	}
	if res2.Failed != 0 {
		t.Fatalf("resume must have no failures, got failed=%d", res2.Failed)
	}
	if resultFor(res2, "p2").Status != FanoutApplied {
		t.Errorf("p2 must now APPLY on resume, got %s", resultFor(res2, "p2").Status)
	}
	if resultFor(res2, "p1").Status != FanoutNoop || resultFor(res2, "p3").Status != FanoutNoop {
		t.Errorf("already-migrated tenants must be no-ops on resume (p1=%s p3=%s)",
			resultFor(res2, "p1").Status, resultFor(res2, "p3").Status)
	}
	if !columnSet(t, pool, "tenant_p2", "items")["code"] {
		t.Errorf("p2 must have the code column after resume")
	}
}

// TestFanout_DestructiveGatedThenMassApproved: a change that removes a column is
// GATED in every tenant by default (additive, no data loss); the dry-run reports the
// AGGREGATE impact; and a mass --approve-drops applies it to all.
func TestFanout_DestructiveGatedThenMassApproved(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	setupControlPlane(t, pool)

	base := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"old":  {Type: "string"},
		}},
	})
	for _, id := range []string{"d1", "d2"} {
		seedFanoutTenant(t, pool, id, base)
		if _, err := pool.Exec(ctx, `INSERT INTO tenant_`+id+`.items (name, old) VALUES ('a','x'),('b','y')`); err != nil {
			t.Fatalf("seed rows %s: %v", id, err)
		}
	}
	// Desired removes the `old` column.
	shrunk := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})

	// Dry-run: reports the aggregate destructive impact, applies nothing.
	dry, err := RunFanout(ctx, pool, FanoutOptions{Schema: shrunk, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	imp := dry.AggregateDestructive()
	if len(imp) != 1 || imp[0].Key != "items.old" || imp[0].Tenants != 2 || imp[0].RowsLost != 4 {
		t.Fatalf("aggregate impact wrong: %+v", imp)
	}

	// Apply WITHOUT approval → gated in every tenant (column stays).
	res, err := RunFanout(ctx, pool, FanoutOptions{Schema: shrunk})
	if err != nil {
		t.Fatalf("apply (no approval): %v", err)
	}
	for _, id := range []string{"d1", "d2"} {
		if !columnSet(t, pool, "tenant_"+id, "items")["old"] {
			t.Errorf("tenant %s: drop must be GATED without approval (old column gone)", id)
		}
	}
	// noop because nothing was applied (only gated drops); gated drops recorded.
	if res.Noop != 2 {
		t.Errorf("expected 2 noop (all gated), got applied=%d noop=%d", res.Applied, res.Noop)
	}
	if len(resultFor(res, "d1").GatedDrops) != 1 {
		t.Errorf("d1 should report the gated drop, got %v", resultFor(res, "d1").GatedDrops)
	}

	// Mass-approve → applied to every tenant.
	res2, err := RunFanout(ctx, pool, FanoutOptions{Schema: shrunk, ApprovedDrops: []string{"items.old"}})
	if err != nil {
		t.Fatalf("apply (mass approval): %v", err)
	}
	if res2.Applied != 2 {
		t.Fatalf("mass approval should apply to both, got applied=%d", res2.Applied)
	}
	for _, id := range []string{"d1", "d2"} {
		if columnSet(t, pool, "tenant_"+id, "items")["old"] {
			t.Errorf("tenant %s: approved drop must remove the old column", id)
		}
	}
}

// TestFanout_SubsetAndMissing: --tenants targets only the listed tenants and reports
// requested-but-absent ids without failing the run.
func TestFanout_SubsetAndMissing(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	setupControlPlane(t, pool)

	base := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	for _, id := range []string{"s1", "s2", "s3"} {
		seedFanoutTenant(t, pool, id, base)
	}
	evolved := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"note": {Type: "text"},
		}},
	})

	res, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved, TenantIDs: []string{"s1", "s3", "ghost"}})
	if err != nil {
		t.Fatalf("subset fan-out: %v", err)
	}
	if res.Total != 2 || res.Applied != 2 {
		t.Fatalf("subset should target exactly s1+s3, got total=%d applied=%d", res.Total, res.Applied)
	}
	if len(res.MissingTenants) != 1 || res.MissingTenants[0] != "ghost" {
		t.Errorf("missing tenant 'ghost' should be reported, got %v", res.MissingTenants)
	}
	// s2 was NOT targeted → unchanged (no note column).
	if columnSet(t, pool, "tenant_s2", "items")["note"] {
		t.Errorf("s2 was not in the subset — it must be untouched")
	}
}

// TestFanout_MigrationLogRecorded: per-tenant outcomes are persisted to
// public.migration_log (consultable run state, grouped by the fan-out run id).
func TestFanout_MigrationLogRecorded(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	setupControlPlane(t, pool)

	base := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
	})
	seedFanoutTenant(t, pool, "lg1", base)
	evolved := mkSchema(map[string]schema.ResourceSchema{
		"items": {Fields: map[string]schema.FieldDef{
			"name": {Type: "string", Required: true},
			"note": {Type: "text"},
		}},
	})

	res, err := RunFanout(ctx, pool, FanoutOptions{Schema: evolved})
	if err != nil {
		t.Fatalf("fan-out: %v", err)
	}
	var status, changes string
	if err := pool.QueryRow(ctx,
		`SELECT status, changes FROM public.migration_log WHERE tenant_id='lg1' ORDER BY id DESC LIMIT 1`).
		Scan(&status, &changes); err != nil {
		t.Fatalf("read migration_log: %v", err)
	}
	if status != "ok" {
		t.Errorf("migration_log status = %q, want ok", status)
	}
	// The fan-out run id is stamped in `changes` so a run's rows are groupable.
	if want := "fanout:" + res.RunID; !strings.Contains(changes, want) {
		t.Errorf("migration_log changes %q should contain %q", changes, want)
	}
}
