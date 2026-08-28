package integration_test

// fields_test.go — MOTOR-FIELDS-S1: `?fields=` is pushed down to the SQL
// SELECT list, so a column the caller does not ask for is NOT READ — which is
// the point: a large json/text value lives in TOAST, and `SELECT *` detoasts
// it for every row of a page that never shows it (the migrated system's
// `GET /api/declarations`: ~940 KB per page of 20, p99 3.8 s).
//
// A projection applied in Go after `SELECT *` would pass a bytes-on-the-wire
// assertion and fix nothing, so this test pins THREE things at once for the
// same request: (1) the SQL the engine actually emitted (captured with a pgx
// tracer on the pool), (2) the buffers that SQL touches (`EXPLAIN (ANALYZE,
// BUFFERS)` of the captured statement — the TOAST relation shows up as
// blocks), and (3) the bytes on the wire. It fails on the pre-feature tree.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/cache"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/events"
	"github.com/appximo/appximo/pkg/extensions"
	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	rbacpkg "github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
)

// sqlRecorder is a pgx QueryTracer that keeps every statement the pool ran.
type sqlRecorder struct {
	mu   sync.Mutex
	stmt []string
}

func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	r.mu.Lock()
	r.stmt = append(r.stmt, d.SQL)
	r.mu.Unlock()
	return ctx
}
func (r *sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// lastMatching returns the most recent statement containing every needle.
func (r *sqlRecorder) lastMatching(needles ...string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.stmt) - 1; i >= 0; i-- {
		ok := true
		for _, n := range needles {
			if !strings.Contains(r.stmt[i], n) {
				ok = false
				break
			}
		}
		if ok {
			return r.stmt[i]
		}
	}
	return ""
}

// startPGTraced is startPG with a query tracer on the pool.
func startPGTraced(t *testing.T, rec *sqlRecorder) (*pgxpool.Pool, string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker (testcontainers); skipped in -short")
	}
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("testdb"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	connStr, _ := ctr.ConnectionString(ctx, "sslmode=disable")
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("parse: %v", err)
	}
	cfg.ConnConfig.Tracer = rec
	// ONE backend for the engine and the replay: cumulative stats are per
	// backend and published lazily, so with several connections an earlier
	// request's TOAST reads could surface inside a later measurement window.
	cfg.MaxConns, cfg.MinConns = 1, 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		ctr.Terminate(ctx)
		t.Fatalf("new pool: %v", err)
	}
	return pool, connStr, func() { pool.Close(); ctr.Terminate(ctx) }
}

func fieldsSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "reto-tributario",
		Resources: map[string]schema.ResourceSchema{
			"declarations": {
				Fields: map[string]schema.FieldDef{
					"nit":         {Type: "string", Required: true},
					"anio":        {Type: "int"},
					"estado":      {Type: "string"},
					"contador_id": {Type: "uuid", Relation: "contadores"},
					"data":        {Type: "json"},
					"attrs":       {Type: "jsonb"},
				},
				Relations: map[string]schema.RelationDef{
					"contador": {Type: "belongs_to", Target: "contadores", FK: "contador_id"},
				},
			},
			"contadores": {Fields: map[string]schema.FieldDef{
				"nombre": {Type: "string", Required: true},
				"notas":  {Type: "text"},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			// the accountant sees the declaration's metadata, never its document
			"contador": {Permissions: map[string]schema.ResourcePermission{
				"declarations": {Actions: []string{"read"}, Fields: []string{"id", "nit", "anio", "estado", "contador_id"}},
				"contadores":   {Actions: []string{"read"}, Fields: []string{"id", "nombre"}},
			}},
		}},
	}
}

// buildFieldsStack mirrors cmd_serve's chain with the response cache in it,
// plus /graphql, over the traced pool.
func buildFieldsStack(s *schema.APISchema, pool *pgxpool.Pool) http.Handler {
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	rc := cache.New(5 * time.Second)
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(rc.Middleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(policyJSON))
	mux.Handle("/graphql", gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, events.NewHub(0), false))
	mux.Mount("/", codegen.BuildRouter(s, tdb, hr, nil, nil))
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	})
}

// rawGet returns status, body bytes and the decoded body (nil when not JSON).
func rawGet(t *testing.T, srv *httptest.Server, path, tok string) (int, []byte, any) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var v any
	_ = json.Unmarshal(b, &v)
	return res.StatusCode, b, v
}

func keyList(m map[string]any) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func rowsOf(t *testing.T, v any) []map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %v", v)
	}
	arr, _ := m["data"].([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, r := range arr {
		out = append(out, r.(map[string]any))
	}
	return out
}

// toastBlocks REPLAYS a captured statement on one dedicated connection with
// its rows fully consumed (as the engine's pgx read does — the server's output
// function detoasts a value only when it is sent) and returns how many TOAST
// blocks of `table` that statement touched, from the cumulative statistics
// (`pg_stat_force_next_flush` makes the backend publish them at the end of
// the statement, so the read after it is exact). An `EXPLAIN ANALYZE` cannot
// show this: it discards the rows, so it never detoasts anything — the very
// difference this feature is about.
func toastBlocks(t *testing.T, pool *pgxpool.Pool, table, sql string, args ...any) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	counter := func() int {
		var n int
		if err := conn.QueryRow(ctx, "SELECT COALESCE(toast_blks_hit,0)+COALESCE(toast_blks_read,0) FROM pg_statio_user_tables WHERE schemaname=$1 AND relname=$2", "tenant_"+tenantID, table).Scan(&n); err != nil {
			t.Fatalf("statio: %v", err)
		}
		return n
	}
	// Publish whatever this backend still holds from the engine's own
	// requests BEFORE the baseline, so the delta is the replay's alone.
	if _, err := conn.Exec(ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
		t.Fatal(err)
	}
	before := counter()
	// The force flag is consumed at the END of the transaction it is set in,
	// so the flag and the replay share ONE transaction: the stats of the
	// replay are published at its commit and the counter read after it is
	// exact (a separate statement would have consumed the flag with nothing
	// pending, and the replay's reads would wait for the 1 s min interval).
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_stat_force_next_flush()"); err != nil {
		t.Fatal(err)
	}
	// the engine's search_path statements (get/subroute/include) are unqualified
	if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", "tenant_"+tenantID); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("replay: %v\n%s", err, sql)
	}
	for rows.Next() {
		if _, err := rows.Values(); err != nil {
			t.Fatal(err)
		}
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return counter() - before
}

func TestFields_ProjectionIsPushedToTheSelect(t *testing.T) {
	rec := &sqlRecorder{}
	pool, _, cleanPG := startPGTraced(t, rec)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := fieldsSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "RT", Email: "rt@rt.test", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildFieldsStack(s, pool))
	defer srv.Close()
	super := genToken("super_admin", superID)
	contador := genToken("contador", "cccc0000-0000-4000-8000-000000000001")

	// Seed: one accountant, 25 declarations carrying a ~60 KB document each —
	// past the ~2 KB TOAST threshold by a wide margin, like the report's rows.
	c := dpDo(t, srv, "POST", "/api/contadores", super, map[string]any{"nombre": "Ana", "notas": "senior"}, http.StatusCreated)
	contadorID := c["id"].(string)
	// Incompressible content on purpose: pglz would fold a repetitive document
	// under the 2 KB TOAST threshold and keep it inline, and the point is the
	// TOAST read. ~60 KB of random hex per row, like a real tax form's data.
	big := make([]map[string]any, 0, 600)
	for i := 0; i < 600; i++ {
		noise := make([]byte, 40)
		_, _ = rand.Read(noise)
		big = append(big, map[string]any{"r": i, "valor": i * 7919, "desc": hex.EncodeToString(noise)})
	}
	var firstID string
	for i := 0; i < 25; i++ {
		d := dpDo(t, srv, "POST", "/api/declarations", super, map[string]any{
			"nit": fmt.Sprintf("9001%05d", i), "anio": 2015 + i%10, "estado": "radicada", "contador_id": contadorID,
			"data": map[string]any{"formulario": "F110", "renglones": big}, "attrs": map[string]any{"lote": i / 10},
		}, http.StatusCreated)
		if i == 0 {
			firstID = d["id"].(string)
		}
	}

	t.Run("list: the SELECT names only the requested columns, the TOAST is not read, the bytes follow", func(t *testing.T) {
		t0 := time.Now()
		st, fullBody, _ := rawGet(t, srv, "/api/declarations?per_page=20", super)
		fullDur := time.Since(t0)
		if st != 200 {
			t.Fatalf("full list: %d %s", st, fullBody)
		}
		fullSQL := rec.lastMatching(`"declarations"`, "LIMIT")
		if !strings.HasPrefix(fullSQL, "SELECT * FROM") {
			t.Fatalf("no projection must keep SELECT * byte for byte: %s", fullSQL)
		}

		t0 = time.Now()
		st, projBody, v := rawGet(t, srv, "/api/declarations?per_page=20&fields=nit,estado", super)
		projDur := time.Since(t0)
		if st != 200 {
			t.Fatalf("projected list: %d %s", st, projBody)
		}
		rows := rowsOf(t, v)
		if len(rows) != 20 {
			t.Fatalf("expected 20 rows, got %d", len(rows))
		}
		for _, r := range rows {
			if keyList(r) != "estado,id,nit" {
				t.Fatalf("each row carries exactly the projection plus id, got %s", keyList(r))
			}
		}
		projSQL := rec.lastMatching(`"declarations"`, "LIMIT")
		if !strings.HasPrefix(projSQL, `SELECT "id", "nit", "estado" FROM`) {
			t.Fatalf("the projection must be in the SELECT list — not trimmed in Go after a SELECT *: %s", projSQL)
		}
		if len(projBody)*50 > len(fullBody) {
			t.Fatalf("bytes on the wire: projected %d B vs full %d B — expected < 2%%", len(projBody), len(fullBody))
		}
		// The disk: the projected statement never touches the TOAST relation;
		// the SELECT * reads it for every row (60 KB × 20 rows ≈ 150+ blocks of
		// 8 KB). Same statements the engine ran, same args, rows consumed.
		fullBlocks := toastBlocks(t, pool, "declarations", fullSQL, 20, 0)
		projBlocks := toastBlocks(t, pool, "declarations", projSQL, 20, 0)
		if projBlocks != 0 || fullBlocks < 100 {
			t.Fatalf("TOAST blocks: SELECT * read %d, the projection %d — the projection must read none, the SELECT * the whole document", fullBlocks, projBlocks)
		}
		t.Logf("page of 20: full %d B / %d blocks / %s — fields=nit,estado %d B / %d blocks / %s", len(fullBody), fullBlocks, fullDur, len(projBody), projBlocks, projDur)
	})

	t.Run("list: named 400s — empty, unknown, extra comma, repeated; aggregate refuses it", func(t *testing.T) {
		for path, want := range map[string]string{
			"/api/declarations?fields=":                    "fields parameter has an empty value",
			"/api/declarations?fields=nit,datax":           "unknown field in fields: datax (available: anio, attrs, contador_id, data, estado, id, nit)",
			"/api/declarations?fields=nit,":                "empty entry in the field list",
			"/api/declarations?fields=nit&fields=anio":     `parameter "fields" was sent 2 times`,
			"/api/declarations/aggregate?count&fields=nit": "fields",
		} {
			st, body, v := rawGet(t, srv, path, super)
			msg, _ := v.(map[string]any)["error"].(string)
			if st != 400 || !strings.Contains(msg, want) {
				t.Fatalf("%s: want 400 containing %q, got %d %s", path, want, st, body)
			}
		}
	})

	t.Run("RBAC: a hidden field named in fields= is omitted (the allowlist, as on every read), an allowed subset works", func(t *testing.T) {
		st, body, v := rawGet(t, srv, "/api/declarations?fields=nit,data&per_page=2", contador)
		if st != 200 {
			t.Fatalf("hidden field: the allowlist omits it, got %d %s", st, body)
		}
		for _, r := range rowsOf(t, v) {
			if keyList(r) != "id,nit" {
				t.Fatalf("data omitted, nit kept: %s", keyList(r))
			}
		}
		if sql := rec.lastMatching(`"declarations"`, "LIMIT"); !strings.HasPrefix(sql, `SELECT "id", "nit" FROM`) {
			t.Fatalf("the hidden column is not read either: %s", sql)
		}
		st, _, v = rawGet(t, srv, "/api/declarations?fields=nit&per_page=3", contador)
		if st != 200 {
			t.Fatalf("allowed subset: %d", st)
		}
		for _, r := range rowsOf(t, v) {
			if keyList(r) != "id,nit" {
				t.Fatalf("got %s", keyList(r))
			}
		}
		// the plain list for the role is unchanged: the allowlist, never the document
		st, _, v = rawGet(t, srv, "/api/declarations?per_page=1", contador)
		if st != 200 || keyList(rowsOf(t, v)[0]) != "anio,contador_id,estado,id,nit" {
			t.Fatalf("allowlist projection unchanged: %d %v", st, v)
		}
	})

	t.Run("get by id: projected, validated, hidden omitted for the role", func(t *testing.T) {
		st, _, v := rawGet(t, srv, "/api/declarations/"+firstID+"?fields=nit", super)
		if st != 200 || keyList(v.(map[string]any)) != "id,nit" {
			t.Fatalf("get with fields: %d %v", st, v)
		}
		if sql := rec.lastMatching(`"declarations" WHERE id = $1`); !strings.HasPrefix(sql, `SELECT "id", "nit" FROM`) {
			t.Fatalf("get SQL projected: %s", sql)
		}
		if st, body, _ := rawGet(t, srv, "/api/declarations/"+firstID+"?fields=ghost", super); st != 400 || !strings.Contains(string(body), "unknown field in fields: ghost") {
			t.Fatalf("get unknown: %d %s", st, body)
		}
		if st, _, v := rawGet(t, srv, "/api/declarations/"+firstID+"?fields=data,nit", contador); st != 200 || keyList(v.(map[string]any)) != "id,nit" {
			t.Fatalf("get hidden omitted: %d %v", st, v)
		}
		// without fields the record is whole, as before
		st, _, v = rawGet(t, srv, "/api/declarations/"+firstID, super)
		if st != 200 || keyList(v.(map[string]any)) != "anio,attrs,contador_id,data,estado,id,nit" {
			t.Fatalf("plain get: %d %s", st, keyList(v.(map[string]any)))
		}
	})

	t.Run("relation subroute: fields of the TARGET, its allowlist decides", func(t *testing.T) {
		st, _, v := rawGet(t, srv, "/api/declarations/"+firstID+"/contador?fields=nombre", super)
		if st != 200 || keyList(v.(map[string]any)) != "id,nombre" {
			t.Fatalf("subroute with fields: %d %v", st, v)
		}
		if sql := rec.lastMatching(`"contadores" r JOIN`); !strings.HasPrefix(sql, `SELECT r."id", r."nombre" FROM`) {
			t.Fatalf("subroute SQL projected: %s", sql)
		}
		if st, body, _ := rawGet(t, srv, "/api/declarations/"+firstID+"/contador?fields=nit", super); st != 400 || !strings.Contains(string(body), "unknown field in fields: nit (available: id, nombre, notas)") {
			t.Fatalf("subroute names the TARGET's fields: %d %s", st, body)
		}
		if st, _, v := rawGet(t, srv, "/api/declarations/"+firstID+"/contador?fields=notas,nombre", contador); st != 200 || keyList(v.(map[string]any)) != "id,nombre" {
			t.Fatalf("subroute hidden on the target omitted: %d %v", st, v)
		}
	})

	t.Run("include: the root is projected, the embed stays whole, sort by an unprojected column works", func(t *testing.T) {
		st, body, v := rawGet(t, srv, "/api/declarations?fields=nit&include=contador&per_page=2&sort=anio&order=desc", super)
		if st != 200 {
			t.Fatalf("include+fields: %d %s", st, body)
		}
		rows := rowsOf(t, v)
		if keyList(rows[0]) != "contador,id,nit" {
			t.Fatalf("root keys: %s", keyList(rows[0]))
		}
		if emb, _ := rows[0]["contador"].(map[string]any); keyList(emb) != "id,nombre,notas" {
			t.Fatalf("embed whole: %v", rows[0]["contador"])
		}
		incSQL := rec.lastMatching("json_agg", `"declarations"`)
		if strings.Contains(incSQL, `'data', _base."data"`) || !strings.Contains(incSQL, `'nit', _base."nit"`) {
			t.Fatalf("root object without data: %s", incSQL)
		}
		// the base subquery carries the requested fields + what the wrapper
		// needs (the belongs_to FK, the order column) and nothing else, and
		// the whole statement reads no TOAST.
		if !strings.Contains(incSQL, `FROM (SELECT "id", "nit", "anio", "contador_id" FROM`) {
			t.Fatalf("include base projected to fields ∪ join/order columns: %s", incSQL)
		}
		if n := toastBlocks(t, pool, "declarations", incSQL, 2, 0); n != 0 {
			t.Fatalf("include+fields: the unrequested document was detoasted (%d toast blocks)", n)
		}
		st, _, v = rawGet(t, srv, "/api/declarations/"+firstID+"?fields=estado&include=contador", super)
		if st != 200 || keyList(v.(map[string]any)) != "contador,estado,id" {
			t.Fatalf("get include+fields: %d %v", st, v)
		}
	})

	t.Run("response cache: fields is part of the key", func(t *testing.T) {
		_, _, a := rawGet(t, srv, "/api/declarations?per_page=1&fields=anio", super)
		_, _, b := rawGet(t, srv, "/api/declarations?per_page=1&fields=estado", super)
		if keyList(rowsOf(t, a)[0]) != "anio,id" || keyList(rowsOf(t, b)[0]) != "estado,id" {
			t.Fatalf("two projections, two bodies: %v / %v", a, b)
		}
	})

	t.Run("GraphQL: the selection set is the projection, pushed into the SQL", func(t *testing.T) {
		gql := func(tok, q string) map[string]any {
			body, _ := json.Marshal(map[string]string{"query": q})
			req, _ := http.NewRequest("POST", srv.URL+"/graphql", bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set("Content-Type", "application/json")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(res.Body).Decode(&out)
			if errs, ok := out["errors"]; ok {
				rec.mu.Lock()
				tail := rec.stmt[max(0, len(rec.stmt)-3):]
				rec.mu.Unlock()
				t.Fatalf("graphql errors: %v\nlast statements: %q", errs, tail)
			}
			return out
		}
		out := gql(super, `{ declarations(per_page: 3) { data { id nit estado } } }`)
		if sql := rec.lastMatching(`"declarations"`, "LIMIT"); !strings.HasPrefix(sql, `SELECT "id", "estado", "nit" FROM`) {
			t.Fatalf("GraphQL list projected (sorted): %s", sql)
		}
		data := out["data"].(map[string]any)["declarations"].(map[string]any)["data"].([]any)
		if len(data) != 3 || keyList(data[0].(map[string]any)) != "estado,id,nit" {
			t.Fatalf("graphql rows: %v", data)
		}
		gql(super, `{ declaration(id: "`+firstID+`") { nit anio } }`)
		if sql := rec.lastMatching(`"declarations" WHERE id = $1`); !strings.HasPrefix(sql, `SELECT "id", "anio", "nit" FROM`) {
			t.Fatalf("GraphQL get projected: %s", sql)
		}
		// a hidden field selected by the scoped role: still null (unchanged
		// contract), and the SQL no longer reads it
		out = gql(contador, `{ declarations(per_page: 1) { data { nit data } } }`)
		if sql := rec.lastMatching(`"declarations"`, "LIMIT"); strings.Contains(sql, `"data"`) || !strings.HasPrefix(sql, `SELECT "id", "nit" FROM`) {
			t.Fatalf("hidden field must not be read: %s", sql)
		}
		row := out["data"].(map[string]any)["declarations"].(map[string]any)["data"].([]any)[0].(map[string]any)
		if row["data"] != nil || row["nit"] == nil {
			t.Fatalf("hidden field resolves null: %v", row)
		}
		// with an embed: the root is projected, the embed whole
		out = gql(super, `{ declarations(per_page: 1) { data { nit contador { nombre } } } }`)
		if sql := rec.lastMatching("json_agg", `"declarations"`); strings.Contains(sql, `'data', _base."data"`) {
			t.Fatalf("GraphQL embed root projected: %s", sql)
		}
		if row := out["data"].(map[string]any)["declarations"].(map[string]any)["data"].([]any)[0].(map[string]any); row["contador"].(map[string]any)["nombre"] != "Ana" {
			t.Fatalf("embed: %v", row)
		}
	})
}
