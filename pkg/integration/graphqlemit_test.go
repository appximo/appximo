package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/outbox"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/miguelangel/appitools/pkg/worker"
)

// emitSchema (BUGS-2) exercises the REST/GraphQL create-emission consistency fix:
//   - "notes" opts into events:["create"] (and is unique-keyed so a duplicate
//     create fails at the DB, proving same-tx atomicity of the emission);
//   - "memos" declares NO events — the no-opt-in gate: create must never emit.
func emitSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "graphqlemit",
		Resources: map[string]schema.ResourceSchema{
			"notes": {
				Fields: map[string]schema.FieldDef{
					"body": {Type: "string", Required: true, Unique: true},
				},
				Events: []string{"create"},
			},
			"memos": {
				Fields: map[string]schema.FieldDef{
					"body": {Type: "string", Required: true},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

// setupEmit builds the REST data plane + the GraphQL handler over emitSchema on a
// fresh Postgres (mirrors setupSC; both surfaces share the SAME engine so an
// outbox row written by either is visible to the same tdb).
func setupEmit(t *testing.T) (*httptest.Server, http.Handler, *db.TenantDB, *pgxpool.Pool, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("graphqlemit: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := emitSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Emit Co", Email: "e@e.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))

	// GraphQL server (same chain as cmd_serve): tenant → jwt → rbac → /graphql.
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	gqlH := gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, events.NewHub(0))
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(policyJSON))
	mux.Handle("/graphql", gqlH)
	gqlWrap := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	})

	return rest, gqlWrap, tdb, pool, genToken, func() { rest.Close(); cleanPG() }
}

// lastOutboxPayload returns the most recent outbox row's topic and decoded payload
// for (topic, tenant). public.outbox is shared (not tenant-scoped); QueryTenant
// sets search_path to <tenant>,public so the explicit public.outbox resolves.
func lastOutboxPayload(t *testing.T, tdb *db.TenantDB, topic string) (string, map[string]any) {
	t.Helper()
	rows, err := tdb.QueryTenant(context.Background(), "tenant_"+tenantID,
		"SELECT topic, payload::text FROM public.outbox WHERE topic = $1 AND tenant_id = $2 ORDER BY id DESC LIMIT 1",
		topic, tenantID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no outbox row for topic %q", topic)
	}
	var gotTopic, payloadJSON string
	if err := rows.Scan(&gotTopic, &payloadJSON); err != nil {
		t.Fatalf("scan outbox: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	return gotTopic, payload
}

// TestGraphQLCreateEmitsOutbox is the BUGS-2 regression suite: the GraphQL createX
// resolver must emit the {resource}.created outbox event for an opt-in resource —
// IDENTICALLY to the REST POST handler — and never emit for a no-opt-in resource,
// always inside the write transaction (a rolled-back create emits nothing).
func TestGraphQLCreateEmitsOutbox(t *testing.T) {
	rest, gql, tdb, pool, tok, done := setupEmit(t)
	defer done()
	super := tok("super_admin", superID)

	gqlCreateNote := func(t *testing.T, body string) string {
		t.Helper()
		data, errs := gqlCreate(t, gql, super, `mutation { createNote(input: { body: "`+body+`" }) { id } }`, "createNote")
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		return data
	}

	t.Run("opt-in: GraphQL create emits notes.created with the lean payload", func(t *testing.T) {
		before := countOutbox(t, tdb, "notes.created")
		id := gqlCreateNote(t, "gql-emit-1")
		if id == "" {
			t.Fatalf("no id returned")
		}
		if after := countOutbox(t, tdb, "notes.created"); after != before+1 {
			t.Fatalf("GraphQL create must emit notes.created: before=%d after=%d", before, after)
		}
		topic, payload := lastOutboxPayload(t, tdb, "notes.created")
		if topic != "notes.created" {
			t.Errorf("topic: got %q", topic)
		}
		if payload["tenant_id"] != tenantID {
			t.Errorf("payload tenant_id: want %q, got %v", tenantID, payload["tenant_id"]) // isolation
		}
		if payload["resource"] != "notes" {
			t.Errorf("payload resource: got %v", payload["resource"])
		}
		if payload["action"] != "created" {
			t.Errorf("payload action: want created, got %v", payload["action"])
		}
		if payload["id"] != id {
			t.Errorf("payload id: want %q, got %v", id, payload["id"])
		}
	})

	t.Run("GraphQL create == REST create in emission (topic + payload)", func(t *testing.T) {
		// REST create → capture its emitted payload.
		restBefore := countOutbox(t, tdb, "notes.created")
		rec := dpDo(t, rest, "POST", "/api/notes", super, map[string]any{"body": "rest-emit-1"}, http.StatusCreated)
		restID := rec["id"].(string)
		if countOutbox(t, tdb, "notes.created") != restBefore+1 {
			t.Fatalf("REST create did not emit notes.created")
		}
		restTopic, restPayload := lastOutboxPayload(t, tdb, "notes.created")

		// GraphQL create → capture its emitted payload.
		gqlBefore := countOutbox(t, tdb, "notes.created")
		gqlID := gqlCreateNote(t, "gql-emit-2")
		if countOutbox(t, tdb, "notes.created") != gqlBefore+1 {
			t.Fatalf("GraphQL create did not emit notes.created")
		}
		gqlTopic, gqlPayload := lastOutboxPayload(t, tdb, "notes.created")

		// Same topic, same identity fields → the two surfaces are indistinguishable
		// to a consumer (the consistency the bug broke).
		if restTopic != gqlTopic {
			t.Errorf("topic differs: rest=%q gql=%q", restTopic, gqlTopic)
		}
		for _, k := range []string{"tenant_id", "resource", "action"} {
			if restPayload[k] != gqlPayload[k] {
				t.Errorf("payload[%q] differs: rest=%v gql=%v", k, restPayload[k], gqlPayload[k])
			}
		}
		if restPayload["id"] != restID {
			t.Errorf("REST payload id %v != row id %v", restPayload["id"], restID)
		}
		if gqlPayload["id"] != gqlID {
			t.Errorf("GraphQL payload id %v != row id %v", gqlPayload["id"], gqlID)
		}
	})

	t.Run("no-opt-in: GraphQL create on memos emits nothing (gate)", func(t *testing.T) {
		before := countOutbox(t, tdb, "memos.created")
		data, errs := gqlCreate(t, gql, super, `mutation { createMemo(input: { body: "no-emit" }) { id } }`, "createMemo")
		if len(errs) > 0 {
			t.Fatalf("graphql errors: %v", errs)
		}
		if data == "" {
			t.Fatalf("memo not created")
		}
		if after := countOutbox(t, tdb, "memos.created"); after != before {
			t.Errorf("a resource without events:[create] must not emit: before=%d after=%d", before, after)
		}
	})

	t.Run("atomicity: a rolled-back GraphQL create (unique violation) emits nothing", func(t *testing.T) {
		_ = gqlCreateNote(t, "dup-key") // first succeeds + emits
		mid := countOutbox(t, tdb, "notes.created")
		// Second create with the same unique body fails at the DB → tx rolls back.
		data, errs := gqlCreate(t, gql, super, `mutation { createNote(input: { body: "dup-key" }) { id } }`, "createNote")
		if len(errs) == 0 {
			t.Fatalf("expected a unique-violation error, got id=%q", data)
		}
		if after := countOutbox(t, tdb, "notes.created"); after != mid {
			t.Errorf("a rolled-back create must not emit: before=%d after=%d", mid, after)
		}
	})

	t.Run("worker consumes the GraphQL-emitted event (e2e, same path as REST)", func(t *testing.T) {
		// Clear the backlog accumulated by earlier subtests so this drain only
		// reflects the row we are about to emit.
		noop := worker.ProcessorFunc(func(context.Context, worker.Row) error { return nil })
		if _, err := worker.Drain(context.Background(), pool, noop, 100, 5, nil); err != nil {
			t.Fatalf("pre-drain: %v", err)
		}
		// A fresh GraphQL create emits one notes.created.
		id := gqlCreateNote(t, "worker-consume")
		// The EXISTING worker drains it via the same SELECT … FOR UPDATE SKIP LOCKED
		// path it uses for REST-emitted rows — the producing surface is invisible to
		// it, so consuming a GraphQL-emitted event needs no new worker code.
		var seen []worker.Row
		res, err := worker.Drain(context.Background(), pool, worker.ProcessorFunc(func(_ context.Context, row worker.Row) error {
			seen = append(seen, row)
			return nil
		}), 100, 5, nil)
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
		if res.Processed < 1 {
			t.Fatalf("worker processed nothing: %+v", res)
		}
		found := false
		for _, row := range seen {
			if row.Topic != "notes.created" || row.TenantID != tenantID {
				continue
			}
			var p map[string]any
			if err := json.Unmarshal(row.Payload, &p); err != nil {
				t.Fatalf("worker saw a non-JSON payload: %v", err)
			}
			if p["id"] == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("worker did not consume the GraphQL-emitted notes.created for id %s (saw %d rows)", id, len(seen))
		}
	})
}

// gqlCreate posts a create mutation and returns the created row's id (or "" plus
// the errors array). Thin wrapper over gqlDo (defined in schemaclose_test.go) that
// digs the id out of the named mutation field.
func gqlCreate(t *testing.T, h http.Handler, token, query, field string) (string, []any) {
	t.Helper()
	data, errs := gqlDo(t, h, token, query)
	if len(errs) > 0 {
		return "", errs
	}
	m, _ := data[field].(map[string]any)
	id, _ := m["id"].(string)
	return id, nil
}
