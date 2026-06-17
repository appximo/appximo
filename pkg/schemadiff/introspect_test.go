package schemadiff_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	sd "github.com/miguelangel/appitools/pkg/schemadiff"
)

// testPool is the shared Postgres handle for the introspector integration tests.
// It comes from APPITOOLS_TEST_DSN / DATABASE_URL (the dev box's appitools-pg) when
// set, otherwise from a throwaway testcontainer (so CI's `make test-all` works).
// nil in -short mode, or if setup failed (setupErr then says why and requireDB
// fails the test loudly rather than crashing the whole binary).
var (
	testPool *pgxpool.Pool
	setupErr error
)

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run()) // integration tests self-skip; no DB needed
	}
	teardown := setupDB()
	code := m.Run()
	if teardown != nil {
		teardown()
	}
	os.Exit(code)
}

// setupDB connects testPool from a DSN (APPITOOLS_TEST_DSN / DATABASE_URL) or, when
// none is set, from a throwaway testcontainer. On failure it records setupErr and
// returns nil — requireDB surfaces that to the individual tests.
func setupDB() func() {
	ctx := context.Background()
	dsn := os.Getenv("APPITOOLS_TEST_DSN")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn != "" {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			setupErr = fmt.Errorf("connect %s: %w", dsn, err)
			return nil
		}
		testPool = pool
		return pool.Close
	}

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		setupErr = fmt.Errorf("start postgres container: %w", err)
		return nil
	}
	cs, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = ctr.Terminate(ctx)
		setupErr = fmt.Errorf("container connection string: %w", err)
		return nil
	}
	pool, err := pgxpool.New(ctx, cs)
	if err != nil {
		_ = ctr.Terminate(ctx)
		setupErr = fmt.Errorf("container pool: %w", err)
		return nil
	}
	testPool = pool
	return func() { pool.Close(); _ = ctr.Terminate(ctx) }
}

func requireDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Postgres; skipped in -short")
	}
	if testPool == nil {
		t.Fatalf("integration test database setup failed: %v", setupErr)
	}
	return testPool
}

var schemaSeq atomic.Int64

// setupSchema creates a fresh ephemeral schema, runs ddl inside it (search_path
// set on a dedicated connection so unqualified CREATEs land in the schema), and
// registers a CASCADE drop for cleanup. Returns the schema name.
func setupSchema(t *testing.T, pool *pgxpool.Pool, ddl string) string {
	t.Helper()
	ctx := context.Background()
	name := "sd_test_" + sanitize(t.Name()) + "_" + strconv.FormatInt(schemaSeq.Add(1), 10)
	ident := pgx.Identifier{name}.Sanitize()

	if _, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+ident+" CASCADE"); err != nil {
		t.Fatalf("drop pre-existing schema: %v", err)
	}
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+ident+" CASCADE")
	})

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET search_path TO "+ident); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	for _, stmt := range splitStatements(ddl) {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("ddl failed:\n%s\nerror: %v", stmt, err)
		}
	}
	return name
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// splitStatements splits a DDL block on ';' (safe for the simple, literal-free DDL
// these tests use) into trimmed, non-empty statements.
func splitStatements(ddl string) []string {
	var out []string
	for _, s := range strings.Split(ddl, ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// diverseDDL exercises a broad slice of the catalog: varied column types, NOT
// NULL, several default kinds, a serial, a composite PK, a composite FK with
// ON DELETE, an FK with ON DELETE/UPDATE actions, a UNIQUE constraint, a CHECK,
// and standalone (unique + non-unique, composite) indexes.
const diverseDDL = `
CREATE TABLE parent (
  id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code text NOT NULL,
  CONSTRAINT parent_code_key UNIQUE (code)
);
CREATE TABLE child (
  seq        serial PRIMARY KEY,
  parent_id  uuid NOT NULL REFERENCES parent(id) ON DELETE CASCADE ON UPDATE RESTRICT,
  name       varchar(120) NOT NULL,
  price      numeric(10,2) DEFAULT 0,
  qty        integer DEFAULT 0,
  active     boolean DEFAULT true,
  created_at timestamptz DEFAULT now(),
  note       text,
  CONSTRAINT child_qty_check CHECK (qty >= 0)
);
CREATE INDEX idx_child_name ON child (name);
CREATE UNIQUE INDEX uq_child_parent_name ON child (parent_id, name);
CREATE TABLE coordinate (
  x integer NOT NULL,
  y integer NOT NULL,
  PRIMARY KEY (x, y)
);
CREATE TABLE point_ref (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  cx integer NOT NULL,
  cy integer NOT NULL,
  CONSTRAINT point_ref_coord_fkey FOREIGN KEY (cx, cy) REFERENCES coordinate(x, y) ON DELETE SET NULL
);`

func TestIntrospect_DiverseTable(t *testing.T) {
	pool := requireDB(t)
	schema := setupSchema(t, pool, diverseDDL)

	s, err := sd.Introspect(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	for _, want := range []string{"parent", "child", "coordinate", "point_ref"} {
		if s.Tables[want] == nil {
			t.Fatalf("table %q missing; got tables %v", want, sortedKeys(s.Tables))
		}
	}

	// ── parent ────────────────────────────────────────────────────────────────
	parent := s.Tables["parent"]
	if parent.PK == nil || !eq(parent.PK.Columns, []string{"id"}) {
		t.Errorf("parent PK = %+v, want [id]", parent.PK)
	}
	id := mustCol(t, parent, "id")
	if id.Type != (sd.Type{Base: sd.BaseUUID}) {
		t.Errorf("parent.id type = %s, want uuid", id.Type)
	}
	if !id.NotNull {
		t.Error("parent.id should be NOT NULL (PK)")
	}
	if id.Default == nil || !strings.Contains(id.Default.Raw, "gen_random_uuid") {
		t.Errorf("parent.id default = %v, want gen_random_uuid()", id.Default)
	}
	if c := mustCol(t, parent, "code"); c.Type.Base != sd.BaseText || !c.NotNull {
		t.Errorf("parent.code = %+v, want text NOT NULL", c)
	}
	if len(parent.Uniques) != 1 {
		t.Errorf("parent should have 1 unique constraint, got %d", len(parent.Uniques))
	}
	for _, u := range parent.Uniques {
		if !eq(u.Columns, []string{"code"}) {
			t.Errorf("parent unique columns = %v, want [code]", u.Columns)
		}
	}
	if len(parent.Indexes) != 0 {
		t.Errorf("parent should have 0 standalone indexes (the unique-constraint index is excluded), got %v", sortedKeys(parent.Indexes))
	}

	// ── child ─────────────────────────────────────────────────────────────────
	child := s.Tables["child"]
	if child.PK == nil || !eq(child.PK.Columns, []string{"seq"}) {
		t.Errorf("child PK = %+v, want [seq]", child.PK)
	}
	seq := mustCol(t, child, "seq")
	if seq.Type.Base != sd.BaseInteger {
		t.Errorf("child.seq type = %s, want integer", seq.Type)
	}
	if seq.Identity == nil || seq.Identity.Generated != "serial" {
		t.Errorf("child.seq identity = %+v, want serial", seq.Identity)
	}
	if seq.Default != nil {
		t.Errorf("child.seq default should be nil (serial captured as identity), got %v", seq.Default)
	}
	if c := mustCol(t, child, "name"); c.Type != (sd.Type{Base: sd.BaseVarchar, Size: 120}) || !c.NotNull {
		t.Errorf("child.name = %s notnull=%t, want varchar(120) NOT NULL", c.Type, c.NotNull)
	}
	if c := mustCol(t, child, "price"); c.Type != (sd.Type{Base: sd.BaseNumeric, Prec: 10, Scale: 2}) {
		t.Errorf("child.price type = %s, want numeric(10,2)", c.Type)
	}
	if c := mustCol(t, child, "active"); c.Type.Base != sd.BaseBool || c.Default == nil || !strings.Contains(c.Default.Raw, "true") {
		t.Errorf("child.active = %+v, want boolean DEFAULT true", c)
	}
	if c := mustCol(t, child, "created_at"); c.Type.Base != sd.BaseTimestamptz || c.Default == nil || c.Default.Raw != "now()" {
		t.Errorf("child.created_at = %+v, want timestamptz DEFAULT now()", c)
	}
	if c := mustCol(t, child, "note"); c.NotNull || c.Default != nil {
		t.Errorf("child.note = %+v, want nullable, no default", c)
	}
	if !eq(child.ColumnOrder, []string{"seq", "parent_id", "name", "price", "qty", "active", "created_at", "note"}) {
		t.Errorf("child column order = %v", child.ColumnOrder)
	}
	// the FK with referential actions
	if len(child.FKs) != 1 {
		t.Fatalf("child should have 1 FK, got %d", len(child.FKs))
	}
	fk := only(child.FKs)
	if !eq(fk.Columns, []string{"parent_id"}) || fk.RefTable != "parent" || !eq(fk.RefColumns, []string{"id"}) {
		t.Errorf("child FK = %+v, want parent_id -> parent(id)", fk)
	}
	if fk.OnDelete != sd.Cascade || fk.OnUpdate != sd.Restrict {
		t.Errorf("child FK actions = onDelete %s / onUpdate %s, want CASCADE / RESTRICT", fk.OnDelete, fk.OnUpdate)
	}
	// the CHECK
	if len(child.Checks) != 1 {
		t.Errorf("child should have 1 check, got %d", len(child.Checks))
	}
	// the standalone indexes
	if idx := child.Indexes["idx_child_name"]; idx == nil || idx.Unique || !eq(idx.Columns, []string{"name"}) || idx.Method != "btree" {
		t.Errorf("idx_child_name = %+v, want btree (name) non-unique", idx)
	}
	if idx := child.Indexes["uq_child_parent_name"]; idx == nil || !idx.Unique || !eq(idx.Columns, []string{"parent_id", "name"}) {
		t.Errorf("uq_child_parent_name = %+v, want unique (parent_id, name)", idx)
	}

	// ── coordinate: composite PK ───────────────────────────────────────────────
	coord := s.Tables["coordinate"]
	if coord.PK == nil || !eq(coord.PK.Columns, []string{"x", "y"}) {
		t.Errorf("coordinate PK = %+v, want composite [x y]", coord.PK)
	}

	// ── point_ref: composite FK with ON DELETE SET NULL ───────────────────────
	pr := s.Tables["point_ref"]
	if len(pr.FKs) != 1 {
		t.Fatalf("point_ref should have 1 FK, got %d", len(pr.FKs))
	}
	cfk := only(pr.FKs)
	if !eq(cfk.Columns, []string{"cx", "cy"}) || cfk.RefTable != "coordinate" || !eq(cfk.RefColumns, []string{"x", "y"}) {
		t.Errorf("point_ref FK = %+v, want (cx,cy) -> coordinate(x,y)", cfk)
	}
	if cfk.OnDelete != sd.SetNull || cfk.OnUpdate != sd.NoAction {
		t.Errorf("point_ref FK actions = onDelete %s / onUpdate %s, want SET NULL / NO ACTION", cfk.OnDelete, cfk.OnUpdate)
	}
}

// TestIntrospect_FKActions verifies every ON DELETE referential action maps to the
// right RefAction — the actions the current converger never emits (MIGRATION_DIAG §2).
func TestIntrospect_FKActions(t *testing.T) {
	pool := requireDB(t)
	const ddl = `
CREATE TABLE ref (id uuid PRIMARY KEY DEFAULT gen_random_uuid());
CREATE TABLE fks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  a uuid REFERENCES ref(id),
  b uuid REFERENCES ref(id) ON DELETE RESTRICT,
  c uuid REFERENCES ref(id) ON DELETE CASCADE,
  d uuid REFERENCES ref(id) ON DELETE SET NULL,
  e uuid DEFAULT gen_random_uuid() REFERENCES ref(id) ON DELETE SET DEFAULT
);`
	schema := setupSchema(t, pool, ddl)
	s, err := sd.Introspect(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	fks := s.Tables["fks"]
	if fks == nil || len(fks.FKs) != 5 {
		t.Fatalf("want 5 FKs, got %v", fks)
	}
	byCol := map[string]sd.RefAction{}
	for _, fk := range fks.FKs {
		if len(fk.Columns) != 1 {
			t.Fatalf("unexpected composite FK %+v", fk)
		}
		byCol[fk.Columns[0]] = fk.OnDelete
	}
	want := map[string]sd.RefAction{
		"a": sd.NoAction,
		"b": sd.Restrict,
		"c": sd.Cascade,
		"d": sd.SetNull,
		"e": sd.SetDefault,
	}
	for col, action := range want {
		if byCol[col] != action {
			t.Errorf("FK on %q: onDelete = %s, want %s", col, byCol[col], action)
		}
	}
}

// TestIntrospect_Determinism is the foundation for diff(A,A)=∅: introspecting the
// same unchanged schema twice yields reflect.DeepEqual models. Once the diff is
// built on this model, comparing a schema against itself will produce no changes.
func TestIntrospect_Determinism(t *testing.T) {
	pool := requireDB(t)
	schema := setupSchema(t, pool, diverseDDL)
	ctx := context.Background()

	s1, err := sd.Introspect(ctx, pool, schema)
	if err != nil {
		t.Fatalf("introspect #1: %v", err)
	}
	s2, err := sd.Introspect(ctx, pool, schema)
	if err != nil {
		t.Fatalf("introspect #2: %v", err)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Errorf("introspection not deterministic:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", render(s1), render(s2))
	}
}

// TestIntrospect_Enum captures a Postgres enum type and a column that uses it.
func TestIntrospect_Enum(t *testing.T) {
	pool := requireDB(t)
	const ddl = `
CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy');
CREATE TABLE feelings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  mood_value mood NOT NULL
);`
	schema := setupSchema(t, pool, ddl)
	s, err := sd.Introspect(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	en := s.Enums["mood"]
	if en == nil || !eq(en.Values, []string{"sad", "ok", "happy"}) {
		t.Fatalf("enum mood = %+v, want labels [sad ok happy]", en)
	}
	col := mustCol(t, s.Tables["feelings"], "mood_value")
	if col.Type.Base != sd.BaseUserDefined || !strings.Contains(col.Type.UserName, "mood") {
		t.Errorf("feelings.mood_value type = %+v, want user-defined containing 'mood'", col.Type)
	}
	if !col.NotNull {
		t.Error("feelings.mood_value should be NOT NULL")
	}
}

// TestIntrospect_EmptySchema introspects a schema with no tables — empty model,
// no error.
func TestIntrospect_EmptySchema(t *testing.T) {
	pool := requireDB(t)
	schema := setupSchema(t, pool, "")
	s, err := sd.Introspect(context.Background(), pool, schema)
	if err != nil {
		t.Fatalf("introspect empty: %v", err)
	}
	if len(s.Tables) != 0 || len(s.Enums) != 0 {
		t.Errorf("empty schema should yield no tables/enums, got %d tables %d enums", len(s.Tables), len(s.Enums))
	}
	if s.Name != schema {
		t.Errorf("schema name = %q, want %q", s.Name, schema)
	}
}

// ── small test helpers ────────────────────────────────────────────────────────

func mustCol(t *testing.T, tbl *sd.Table, name string) *sd.Column {
	t.Helper()
	c := tbl.Columns[name]
	if c == nil {
		t.Fatalf("table %q has no column %q (have %v)", tbl.Name, name, tbl.ColumnOrder)
	}
	return c
}

func only(m map[string]*sd.ForeignKey) *sd.ForeignKey {
	for _, v := range m {
		return v
	}
	return nil
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// render produces a stable, human-readable dump for determinism-failure diagnostics.
func render(s *sd.Schema) string {
	var b strings.Builder
	for _, tn := range sortedKeys(s.Tables) {
		tbl := s.Tables[tn]
		fmt.Fprintf(&b, "TABLE %s\n", tn)
		for _, cn := range tbl.ColumnOrder {
			c := tbl.Columns[cn]
			fmt.Fprintf(&b, "  COL %s %s notnull=%t", c.Name, c.Type, c.NotNull)
			if c.Default != nil {
				fmt.Fprintf(&b, " default=%s", c.Default.Raw)
			}
			if c.Identity != nil {
				fmt.Fprintf(&b, " identity=%s", c.Identity.Generated)
			}
			b.WriteByte('\n')
		}
		if tbl.PK != nil {
			fmt.Fprintf(&b, "  PK %v\n", tbl.PK.Columns)
		}
		for _, k := range sortedKeys(tbl.FKs) {
			f := tbl.FKs[k]
			fmt.Fprintf(&b, "  FK %s %v -> %s%v onDel=%s onUpd=%s\n", f.Symbol, f.Columns, f.RefTable, f.RefColumns, f.OnDelete, f.OnUpdate)
		}
		for _, k := range sortedKeys(tbl.Uniques) {
			fmt.Fprintf(&b, "  UNIQUE %s %v\n", k, tbl.Uniques[k].Columns)
		}
		for _, k := range sortedKeys(tbl.Checks) {
			fmt.Fprintf(&b, "  CHECK %s %s\n", k, tbl.Checks[k].Expression)
		}
		for _, k := range sortedKeys(tbl.Indexes) {
			i := tbl.Indexes[k]
			fmt.Fprintf(&b, "  INDEX %s %v unique=%t method=%s pred=%q\n", i.Name, i.Columns, i.Unique, i.Method, i.Predicate)
		}
	}
	for _, en := range sortedKeys(s.Enums) {
		fmt.Fprintf(&b, "ENUM %s %v\n", en, s.Enums[en].Values)
	}
	return b.String()
}
