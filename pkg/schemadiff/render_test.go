package schemadiff_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

func mustDiff(t *testing.T, desired, current *sd.Schema) *sd.Plan {
	t.Helper()
	p, err := sd.Diff(desired, current)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	return p
}

func renderStmts(t *testing.T, plan *sd.Plan) []sd.Statement {
	t.Helper()
	ordered, err := sd.OrderPlan(plan)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	stmts, err := sd.Render(ordered)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return stmts
}

func anyStmt(stmts []sd.Statement, pred func(sd.Statement) bool) bool {
	for _, st := range stmts {
		if pred(st) {
			return true
		}
	}
	return false
}

// ── pure render assertions: every op renders to its production-SAFE form ────────

func TestRender_SafePatterns(t *testing.T) {
	// SET NOT NULL → validated-CHECK pattern (no long ACCESS EXCLUSIVE scan)
	cur := sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, false)))
	des := sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))
	stmts := renderStmts(t, mustDiff(t, des, cur))
	for _, want := range []string{"IS NOT NULL) NOT VALID", "VALIDATE CONSTRAINT", "SET NOT NULL"} {
		if !anyStmt(stmts, func(s sd.Statement) bool { return strings.Contains(s.SQL, want) }) {
			t.Errorf("SET NOT NULL must use the validated-CHECK pattern; missing %q in:\n%s", want, sqlDump(stmts))
		}
	}

	// FK → NOT VALID + VALIDATE
	p := tbl("p", []string{"id"}, col("id", sd.BaseUUID, true))
	c := tbl("c", []string{"id"}, col("id", sd.BaseUUID, true), col("p_id", sd.BaseUUID, false))
	linkFK(c, "fk_c_p", "p_id", "p", sd.NoAction)
	stmts = renderStmts(t, mustDiff(t, sch(p, c), sch()))
	if !anyStmt(stmts, func(s sd.Statement) bool {
		return strings.Contains(s.SQL, "FOREIGN KEY") && strings.Contains(s.SQL, "NOT VALID")
	}) || !anyStmt(stmts, func(s sd.Statement) bool { return strings.Contains(s.SQL, "VALIDATE CONSTRAINT") }) {
		t.Errorf("FK must render as NOT VALID + VALIDATE:\n%s", sqlDump(stmts))
	}

	// index → CONCURRENTLY, and marked Concurrent (runs outside a transaction)
	wi := tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("status", sd.BaseText, false))
	wi.Indexes["idx_w_status"] = &sd.Index{Name: "idx_w_status", Columns: []string{"status"}, Method: "btree"}
	stmts = renderStmts(t, mustDiff(t, sch(wi), sch()))
	if !anyStmt(stmts, func(s sd.Statement) bool {
		return s.Concurrent && strings.Contains(s.SQL, "CREATE INDEX CONCURRENTLY")
	}) {
		t.Errorf("index must render as CONCURRENTLY + marked Concurrent:\n%s", sqlDump(stmts))
	}

	// rename → ALTER … RENAME (NEVER drop+add)
	renamed := col("title", sd.BaseText, true)
	renamed.RenamedFrom = "name"
	stmts = renderStmts(t, mustDiff(t,
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), renamed)),
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))))
	if !anyStmt(stmts, func(s sd.Statement) bool { return strings.Contains(s.SQL, "RENAME COLUMN") }) {
		t.Errorf("rename must render as RENAME COLUMN:\n%s", sqlDump(stmts))
	}
	if anyStmt(stmts, func(s sd.Statement) bool {
		return strings.Contains(s.SQL, "DROP COLUMN") || strings.Contains(s.SQL, "ADD COLUMN")
	}) {
		t.Errorf("rename must NOT drop+add:\n%s", sqlDump(stmts))
	}
}

func sqlDump(stmts []sd.Statement) string {
	var b strings.Builder
	for _, s := range stmts {
		if s.Concurrent {
			b.WriteString("[concurrent] ")
		}
		b.WriteString(s.SQL)
		b.WriteString("\n")
	}
	return b.String()
}

// ── Validate(): the planning gate's concern data ────────────────────────────────

func TestValidate_Concerns(t *testing.T) {
	// NOT NULL without a default → a backfill concern with the actionable message
	notnull := mustDiff(t,
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("sku", sd.BaseText, true))),
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true))))
	cs := sd.Validate(notnull)
	if len(cs) != 1 || cs[0].Risk != sd.RiskBackfill || !strings.Contains(cs[0].Message, "NOT NULL without a default") {
		t.Errorf("expected one NOT-NULL-without-default backfill concern, got %+v", cs)
	}

	// drop table / drop column → destructive concerns
	drop := mustDiff(t, sch(), sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true))))
	if cs := sd.Validate(drop); len(cs) == 0 || cs[0].Risk != sd.RiskDestructive {
		t.Errorf("expected a destructive concern for DropTable, got %+v", cs)
	}

	// type change → transformational concern
	typ := mustDiff(t,
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("n", sd.BaseBigint, false))),
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("n", sd.BaseInteger, false))))
	if !concernWithRisk(sd.Validate(typ), sd.RiskTransformational) {
		t.Errorf("expected a transformational concern for a type change, got %+v", sd.Validate(typ))
	}

	// a purely additive (nullable) change → no concerns
	safe := mustDiff(t,
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true), col("note", sd.BaseText, false))),
		sch(tbl("w", []string{"id"}, col("id", sd.BaseUUID, true))))
	if cs := sd.Validate(safe); len(cs) != 0 {
		t.Errorf("a nullable add should raise no concerns, got %+v", cs)
	}
}

func concernWithRisk(cs []sd.Concern, r sd.RiskClass) bool {
	for _, c := range cs {
		if c.Risk == r {
			return true
		}
	}
	return false
}

// ── the diagnostic's worst 🔴: NOT NULL without default on a POPULATED table must
//    fail LOUDLY (never silently drop the NOT NULL), and roll back cleanly ────────

func TestExecutor_NotNullNoDefaultOnData_FailsLoudAndRollsBack(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	schema := setupSchema(t, pool, "")

	base := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("name", sd.BaseText, true)))
	apply(t, pool, schema, mustDiff(t, base, sch()))
	if _, err := pool.Exec(ctx, `INSERT INTO `+q(schema)+`.`+q("widget")+` (id, name) VALUES (gen_random_uuid(), 'keep-me')`); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	rc := introspectSchema(t, pool, schema)

	// desired adds a NOT NULL column WITHOUT a default — impossible over existing rows
	des := sch(tbl("widget", []string{"id"},
		col("id", sd.BaseUUID, true), col("name", sd.BaseText, true), col("sku", sd.BaseText, true)))
	plan := mustDiff(t, des, rc)

	ex := &sd.Executor{Pool: pool, Schema: schema}
	if err := ex.Apply(ctx, plan); err == nil {
		t.Fatal("expected a loud failure adding NOT NULL (no default) over existing rows, got nil")
	}

	after := introspectSchema(t, pool, schema)
	// the NOT NULL must NOT have been silently added as a nullable column (the bug)
	if _, exists := after.Tables["widget"].Columns["sku"]; exists {
		t.Error("sku must NOT exist after the failed/rolled-back migration (the converger's bug was to add it nullable)")
	}
	// and the existing row must be intact
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+q(schema)+`.`+q("widget")).Scan(&n); err != nil || n != 1 {
		t.Errorf("row must be intact: count=%d err=%v", n, err)
	}
}

// ── FK NOT VALID + VALIDATE applies over a populated table with valid data ──────

func TestExecutor_ForeignKeyValidateOverData(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	schema := setupSchema(t, pool, "")

	parent := tbl("p", []string{"id"}, col("id", sd.BaseUUID, true))
	child := tbl("c", []string{"id"}, col("id", sd.BaseUUID, true), col("p_id", sd.BaseUUID, false))
	apply(t, pool, schema, mustDiff(t, sch(parent, child), sch())) // no FK yet

	var pid string
	if err := pool.QueryRow(ctx, `INSERT INTO `+q(schema)+`.`+q("p")+` (id) VALUES (gen_random_uuid()) RETURNING id`).Scan(&pid); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO `+q(schema)+`.`+q("c")+` (id, p_id) VALUES (gen_random_uuid(), $1)`, pid); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	rc := introspectSchema(t, pool, schema)

	child2 := tbl("c", []string{"id"}, col("id", sd.BaseUUID, true), col("p_id", sd.BaseUUID, false))
	linkFK(child2, "fk_c_p", "p_id", "p", sd.Cascade)
	apply(t, pool, schema, mustDiff(t, sch(parent, child2), rc)) // ADD ... NOT VALID + VALIDATE over data

	after := introspectSchema(t, pool, schema)
	if len(after.Tables["c"].FKs) != 1 {
		t.Fatalf("FK should be present and validated, got %d", len(after.Tables["c"].FKs))
	}
}

// ── a failed statement inside a transactional batch rolls the WHOLE batch back ───

func TestExecutor_AtomicBatchRollback(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	schema := setupSchema(t, pool, "")

	// CreateTable + an AddCheck referencing a non-existent column → the ADD fails;
	// both are in one transactional batch, so the CREATE must roll back too.
	bad := tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("qty", sd.BaseInteger, false))
	bad.Checks["widget_bad_chk"] = &sd.Check{Symbol: "widget_bad_chk", Expression: "(nonexistent_col > 0)"}
	plan := mustDiff(t, sch(bad), sch())

	ex := &sd.Executor{Pool: pool, Schema: schema}
	if err := ex.Apply(ctx, plan); err == nil {
		t.Fatal("expected failure from the invalid check expression, got nil")
	}
	after := introspectSchema(t, pool, schema)
	if _, ok := after.Tables["widget"]; ok {
		t.Error("widget must NOT exist — the whole transactional batch must roll back on a mid-batch failure")
	}
}

// ── lock_timeout + retry: DDL fails fast under contention and retries with backoff ─

func TestExecutor_LockTimeoutAndRetry(t *testing.T) {
	pool := requireDB(t)
	ctx := context.Background()
	schema := setupSchema(t, pool, "")

	apply(t, pool, schema, mustDiff(t, sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true))), sch()))
	rc := introspectSchema(t, pool, schema)

	// hold ACCESS EXCLUSIVE on widget in an open transaction, blocking any DDL
	holdTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder tx: %v", err)
	}
	defer holdTx.Rollback(ctx) //nolint:errcheck
	if _, err := holdTx.Exec(ctx, `LOCK TABLE `+q(schema)+`.`+q("widget")+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("acquire blocking lock: %v", err)
	}

	// a DDL on the locked table, with a short lock_timeout and a couple of retries
	des := sch(tbl("widget", []string{"id"}, col("id", sd.BaseUUID, true), col("extra", sd.BaseText, false)))
	var retries int
	ex := &sd.Executor{
		Pool: pool, Schema: schema,
		LockTimeout: 200 * time.Millisecond,
		MaxRetries:  2,
		BackoffBase: 10 * time.Millisecond,
		OnRetry:     func(attempt int, err error) { retries++ },
	}
	start := time.Now()
	err = ex.Apply(ctx, mustDiff(t, des, rc))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a lock-timeout failure while the table is exclusively locked")
	}
	if retries != 2 {
		t.Errorf("expected 2 retries (MaxRetries), got %d", retries)
	}
	// 3 attempts × ~200ms lock wait proves it failed FAST each time, not hung forever
	if elapsed < 400*time.Millisecond {
		t.Errorf("expected at least ~3 lock_timeout waits, elapsed only %v", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("took %v — lock_timeout does not appear to be set (near-hang)", elapsed)
	}

	// releasing the lock lets the same migration succeed
	holdTx.Rollback(ctx) //nolint:errcheck
	apply(t, pool, schema, mustDiff(t, des, introspectSchema(t, pool, schema)))
	if _, ok := introspectSchema(t, pool, schema).Tables["widget"].Columns["extra"]; !ok {
		t.Error("after releasing the lock the column should have been added")
	}
}
