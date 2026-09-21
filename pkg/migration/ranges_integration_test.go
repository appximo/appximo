package migration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/appximo/appximo/pkg/schema"
)

func rangeSchema(noOverlap *schema.NoOverlapDef) *schema.APISchema {
	return mkSchema(map[string]schema.ResourceSchema{
		"eventos": {
			Fields: map[string]schema.FieldDef{
				"titulo":   {Type: "string", Required: true},
				"inicio":   {Type: "time"},
				"fin":      {Type: "time"},
				"dueno_id": {Type: "uuid"},
				"ocupa":    {Type: "bool", Default: true},
			},
			Ranges: map[string]schema.RangeDef{
				"horario": {Start: "inicio", End: "fin", NoOverlap: noOverlap},
			},
		},
	})
}

// TestIntegration_Ranges_ExclusionEnforcedIdempotentReplaced pins the whole
// migration contract of a declared time range (MOTOR-AGENDA-S1):
//
//	provision with no_overlap      → CHECK + EXCLUDE exist, btree_gist installed
//	re-provision unchanged         → empty diff (symbol match, CHECK text match)
//	4–5 then 5–6                   → both accepted (half-open ranges)
//	4:30–5:30                      → 23P01 exclusion_violation
//	end before start               → 23514 on the CHECK
//	a "no ocupa" row on top        → accepted (partial constraint)
//	rule changed (scope removed)   → old symbol dropped + new one added, atomically
func TestIntegration_Ranges_ExclusionEnforcedIdempotentReplaced(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_rg"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_rg"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	rule := &schema.NoOverlapDef{Scope: []string{"dueno_id"}, When: &schema.WhenDef{Field: "ocupa", Op: "eq", Val: true}}
	s := rangeSchema(rule)
	if errs := schema.Validate(s); len(errs) > 0 {
		t.Fatalf("schema must validate: %v", errs)
	}
	if err := ApplyTenantMigration(ctx, pool, pg, s); err != nil {
		t.Fatalf("provision: %v", err)
	}

	defs := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT con.conname, con.contype::text FROM pg_constraint con
		JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND c.relname='eventos'`, pg)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatal(err)
		}
		defs[name] = typ
	}
	rows.Close()
	if defs["chk_eventos_horario_order"] != "c" {
		t.Fatalf("CHECK missing: %v", defs)
	}
	sym := schema.RangeExclusionName("eventos", "horario", s.Resources["eventos"].Ranges["horario"])
	if defs[sym] != "x" {
		t.Fatalf("EXCLUDE %s missing: %v", sym, defs)
	}
	var ext string
	if err := pool.QueryRow(ctx, `SELECT extname FROM pg_extension WHERE extname='btree_gist'`).Scan(&ext); err != nil {
		t.Fatalf("btree_gist must be installed: %v", err)
	}

	// Idempotent: the re-diff is empty (CHECK text and EXCLUDE symbol both match).
	plan, _, err := diffTenant(ctx, pool, pg, s, false)
	if err != nil {
		t.Fatalf("re-diff: %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("re-provision must be a no-op, got:\n%s", plan)
	}

	owner := "11111111-1111-1111-1111-111111111111"
	ins := func(titulo, from, to string, ocupa bool) error {
		_, err := pool.Exec(ctx, `INSERT INTO "tenant_rg"."eventos" (titulo, inicio, fin, dueno_id, ocupa) VALUES ($1,$2,$3,$4,$5)`, titulo, from, to, owner, ocupa)
		return err
	}
	if err := ins("4-5", "2026-09-22T16:00:00-05:00", "2026-09-22T17:00:00-05:00", true); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if err := ins("5-6", "2026-09-22T17:00:00-05:00", "2026-09-22T18:00:00-05:00", true); err != nil {
		t.Fatalf("adjacent 5-6 must be accepted (half-open): %v", err)
	}
	err = ins("4:30-5:30", "2026-09-22T16:30:00-05:00", "2026-09-22T17:30:00-05:00", true)
	var pgErr *pgconn.PgError
	if err == nil || !asPG(err, &pgErr) || pgErr.Code != "23P01" {
		t.Fatalf("overlap must be a 23P01 exclusion_violation, got %v", err)
	}
	if pgErr.ConstraintName != sym {
		t.Fatalf("violated constraint %q, want %q", pgErr.ConstraintName, sym)
	}
	err = ins("reversed", "2026-09-22T19:00:00-05:00", "2026-09-22T18:30:00-05:00", true)
	if err == nil || !asPG(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("end before start must be a 23514 check_violation, got %v", err)
	}
	if err := ins("tentativo", "2026-09-22T16:30:00-05:00", "2026-09-22T17:30:00-05:00", false); err != nil {
		t.Fatalf("a row that does not block (ocupa=false) must be accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "tenant_rg"."eventos" (titulo, dueno_id) VALUES ('sin fecha', $1)`, owner); err != nil {
		t.Fatalf("an unscheduled row (NULL bounds) must be accepted: %v", err)
	}

	// Changed rule: same range, scope removed → a NEW symbol; the migration
	// must drop the old constraint and add the new one (a replacement, not a
	// lingering pair). With no scope, the existing 4-5 / 5-6 rows still do not
	// overlap and the ocupa=false row is outside the partial rule, so it applies.
	s2 := rangeSchema(&schema.NoOverlapDef{When: &schema.WhenDef{Field: "ocupa", Op: "eq", Val: true}})
	sym2 := schema.RangeExclusionName("eventos", "horario", s2.Resources["eventos"].Ranges["horario"])
	if sym2 == sym {
		t.Fatalf("a changed rule must produce a new symbol")
	}
	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, s2, nil)
	if err != nil {
		t.Fatalf("apply changed rule: %v", err)
	}
	if out.Partial() {
		t.Fatalf("changed rule must apply fully, unapplied: %v", out.Unapplied)
	}
	var have []string
	rows, err = pool.Query(ctx, `SELECT con.conname FROM pg_constraint con
		JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND c.relname='eventos' AND con.contype='x'`, pg)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		have = append(have, name)
	}
	rows.Close()
	if len(have) != 1 || have[0] != sym2 {
		t.Fatalf("after the rule changed exactly the new symbol must exist, got %v (old %s, new %s)", have, sym, sym2)
	}
}

// TestIntegration_Ranges_ExistingOverlapBlocksTheRuleNamingPairs: adding a
// no-overlap rule over rows that ALREADY overlap is refused — the dry-run names
// the pairs, the apply reports the rule as not applied (Partial) with the pairs,
// the rest of the plan lands, nothing is half-applied.
func TestIntegration_Ranges_ExistingOverlapBlocksTheRuleNamingPairs(t *testing.T) {
	pool, done := startPG(t)
	defer done()
	ctx := context.Background()
	pg := "tenant_rg2"
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "tenant_rg2"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	// v1: the range without a rule.
	s1 := rangeSchema(nil)
	if err := ApplyTenantMigration(ctx, pool, pg, s1); err != nil {
		t.Fatalf("provision v1: %v", err)
	}
	owner := "11111111-1111-1111-1111-111111111111"
	var idA, idB string
	if err := pool.QueryRow(ctx, `INSERT INTO "tenant_rg2"."eventos" (titulo, inicio, fin, dueno_id) VALUES ('a','2026-09-22T16:00:00Z','2026-09-22T17:00:00Z',$1) RETURNING id::text`, owner).Scan(&idA); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO "tenant_rg2"."eventos" (titulo, inicio, fin, dueno_id) VALUES ('b','2026-09-22T16:30:00Z','2026-09-22T17:30:00Z',$1) RETURNING id::text`, owner).Scan(&idB); err != nil {
		t.Fatal(err)
	}
	// v2: the rule arrives, plus an unrelated new column that MUST land.
	s2 := rangeSchema(&schema.NoOverlapDef{Scope: []string{"dueno_id"}})
	res := s2.Resources["eventos"]
	res.Fields["notas"] = schema.FieldDef{Type: "text"}
	s2.Resources["eventos"] = res

	pv, err := PreviewTenantMigration(ctx, pool, pg, s2, nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var blocked string
	for _, c := range pv.Concerns {
		if strings.HasPrefix(c, "[blocked] no_overlap") {
			blocked = c
		}
	}
	if blocked == "" {
		t.Fatalf("dry-run must name the overlapping pairs, concerns: %v", pv.Concerns)
	}
	if !strings.Contains(blocked, idA) || !strings.Contains(blocked, idB) {
		t.Fatalf("the concern must name both rows (%s, %s): %s", idA, idB, blocked)
	}

	out, err := ApplyTenantMigrationApproved(ctx, pool, pg, s2, nil)
	if err != nil {
		t.Fatalf("apply must not error out (the rule is reported, not thrown): %v", err)
	}
	if !out.Partial() {
		t.Fatalf("a blocked rule is a PARTIAL apply, got %+v", out)
	}
	if len(out.BlockedExclusions) != 1 || !(strings.Contains(out.BlockedExclusions[0], idA+" × "+idB) || strings.Contains(out.BlockedExclusions[0], idB+" × "+idA)) {
		t.Fatalf("blocked exclusion must name the pair: %v", out.BlockedExclusions)
	}
	// The unrelated column landed (nothing half-applied, nothing withheld).
	assertColumns(t, pool, pg, "eventos", []string{"id", "titulo", "inicio", "fin", "dueno_id", "ocupa", "notas"})
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace ns ON ns.oid=c.relnamespace WHERE ns.nspname=$1 AND c.relname='eventos' AND con.contype='x'`, pg).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the rule must NOT be in place over colliding rows")
	}
	// Fix the data (mark one as not blocking is not possible without `when`;
	// move it) and re-apply: converges, the rule lands.
	if _, err := pool.Exec(ctx, `UPDATE "tenant_rg2"."eventos" SET inicio='2026-09-22T17:00:00Z', fin='2026-09-22T18:00:00Z' WHERE id=$1`, idB); err != nil {
		t.Fatal(err)
	}
	out, err = ApplyTenantMigrationApproved(ctx, pool, pg, s2, nil)
	if err != nil || out.Partial() {
		t.Fatalf("after fixing the data the rule must land: err=%v out=%+v", err, out)
	}
}

func asPG(err error, target **pgconn.PgError) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		*target = pe
		return true
	}
	return false
}
