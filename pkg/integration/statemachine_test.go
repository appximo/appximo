package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
)

// G5 (FIX-G5): declarative state-machine transitions. A status field declares its
// initial state(s) and allowed moves; the engine forces them on create (must start
// in an initial state) and update (a state may only move along a declared
// transition; a state with no outgoing transitions is terminal/immutable). Enforced
// race-safely in the UPDATE WHERE, on REST, GraphQL, and inside a G4 transaction.

func smSchema() *schema.APISchema {
	sm := func(initial string, trans map[string][]string) *schema.StateMachine {
		return &schema.StateMachine{Initial: []string{initial}, Transitions: trans}
	}
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "sm-lab",
		Resources: map[string]schema.ResourceSchema{
			"orders": {
				Fields: map[string]schema.FieldDef{
					"code":  {Type: "string", Required: true, Unique: true},
					"total": {Type: "float64"},
					"status": {
						Type: "string", Default: "pending",
						Enum: []string{"pending", "paid", "shipped", "delivered", "cancelled"},
						StateMachine: sm("pending", map[string][]string{
							"pending":   {"paid", "cancelled"},
							"paid":      {"shipped"},
							"shipped":   {"delivered"},
							"delivered": {},
							"cancelled": {},
						}),
					},
				},
			},
			"ledger_entries": {
				Fields: map[string]schema.FieldDef{
					"ref": {Type: "string", Required: true, Unique: true},
					"status": {
						Type: "string", Default: "pending",
						Enum:         []string{"pending", "posted", "void"},
						StateMachine: sm("pending", map[string][]string{"pending": {"posted", "void"}, "posted": {}, "void": {}}),
					},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func setupSM(t *testing.T) (*httptest.Server, *httptest.Server, *pgxpool.Pool, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("statemachine: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("outbox: %v", err)
	}
	s := smSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "SM", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))

	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var pol rbacpkg.Policy
	pj, _ := json.Marshal(s.RBAC)
	_ = json.Unmarshal(pj, &pol)
	gqlH := gqlhandler.BuildHandler(s, tdb, hr, &pol, events.NewHub(0), false)
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(pj))
	mux.Handle("/graphql", gqlH)
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	}))
	return rest, gql, pool, genToken, func() { rest.Close(); gql.Close(); cleanPG() }
}

func createOrder(t *testing.T, rest *httptest.Server, tok, code string) string {
	t.Helper()
	out := dpDo(t, rest, "POST", "/api/orders", tok, map[string]any{"code": code, "total": 9}, 201)
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("no id from create: %v", out)
	}
	if out["status"] != "pending" {
		t.Fatalf("new order should default to pending, got %v", out["status"])
	}
	return id
}

// TestSM_CreateInitialState: default → pending; an advanced state at create is 422.
func TestSM_CreateInitialState(t *testing.T) {
	rest, _, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	createOrder(t, rest, admin, "o-init-1") // default pending OK
	st, body := dpStatusBody(t, rest, "POST", "/api/orders", admin, map[string]any{"code": "o-init-2", "status": "shipped"})
	if st != 422 || !strings.Contains(body, "initial state") {
		t.Fatalf("creating in 'shipped' should be 422 initial-state error; got %d %s", st, body)
	}
}

// TestSM_ValidAndInvalidTransitions: the declared chain advances; a skip is 422.
func TestSM_ValidAndInvalidTransitions(t *testing.T) {
	rest, _, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-chain")

	for _, next := range []string{"paid", "shipped", "delivered"} {
		out := dpDo(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": next}, 200)
		if out["status"] != next {
			t.Fatalf("transition to %s: status is %v", next, out["status"])
		}
	}

	// Fresh order: pending → shipped is NOT declared → 422 with a clear message.
	id2 := createOrder(t, rest, admin, "o-skip")
	st, body := dpStatusBody(t, rest, "PATCH", "/api/orders/"+id2, admin, map[string]any{"status": "shipped"})
	if st != 422 || !strings.Contains(body, "invalid transition") {
		t.Fatalf("pending→shipped should be 422 invalid transition; got %d %s", st, body)
	}
	// status unchanged.
	got := dpDo(t, rest, "GET", "/api/orders/"+id2, admin, nil, 200)
	if got["status"] != "pending" {
		t.Fatalf("rejected transition must not change status; got %v", got["status"])
	}
}

// TestSM_TerminalImmutable: a terminal state cannot move (fintech-style immutability).
func TestSM_TerminalImmutable(t *testing.T) {
	rest, _, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-term")
	for _, next := range []string{"paid", "shipped", "delivered"} {
		dpDo(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": next}, 200)
	}
	// delivered is terminal: any move out is rejected.
	for _, bad := range []string{"pending", "paid", "cancelled"} {
		if st := dpStatus(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": bad}); st != 422 {
			t.Fatalf("delivered→%s should be 422 (terminal), got %d", bad, st)
		}
	}
	// A no-op self-set of the terminal state is allowed (changes nothing).
	if st := dpStatus(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": "delivered"}); st != 200 {
		t.Fatalf("no-op delivered→delivered should be 200, got %d", st)
	}
}

// TestSM_NoopAndNonStateFields: re-sending the current state is a no-op, and a
// non-state field updates freely even on a terminal-state row.
func TestSM_NoopAndNonStateFields(t *testing.T) {
	rest, _, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-noop")
	// Full-object-style PATCH re-sending status=pending + changing total → OK.
	out := dpDo(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": "pending", "total": 42}, 200)
	if out["total"].(float64) != 42 {
		t.Fatalf("non-state field should update with a no-op state: %v", out)
	}
	// Advance to terminal, then update a NON-state field — allowed (G5 gates only the state field).
	for _, next := range []string{"paid", "shipped", "delivered"} {
		dpDo(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": next}, 200)
	}
	if st := dpStatus(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"total": 99}); st != 200 {
		t.Fatalf("updating a non-state field on a terminal-state row should be 200, got %d", st)
	}
}

// TestSM_GuardReReadsCurrentState: the transition is checked against the row's
// CURRENT state at write time (the basis of race-safety). After pending→paid, an
// attempt to cancel (only valid from pending) is rejected — the guard re-read paid.
func TestSM_GuardReReadsCurrentState(t *testing.T) {
	rest, _, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-race")
	dpDo(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": "paid"}, 200)
	// cancelled is reachable only from pending; the row is now paid → rejected.
	if st := dpStatus(t, rest, "PATCH", "/api/orders/"+id, admin, map[string]any{"status": "cancelled"}); st != 422 {
		t.Fatalf("paid→cancelled (not declared) should be 422, got %d", st)
	}
}

// TestSM_GraphQLTransition: GraphQL update enforces the same transitions.
func TestSM_GraphQLTransition(t *testing.T) {
	rest, gql, _, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-gql")

	// Valid: pending → paid.
	resp := smGqlDo(t, gql, admin, `mutation { updateOrder(id: "`+id+`", input: { status: "paid" }) { id status } }`)
	if errs, _ := resp["errors"].([]any); len(errs) != 0 {
		t.Fatalf("valid GraphQL transition errored: %v", errs)
	}
	// Invalid: paid → delivered (not declared) → error mentioning transition.
	resp = smGqlDo(t, gql, admin, `mutation { updateOrder(id: "`+id+`", input: { status: "delivered" }) { id status } }`)
	errs, _ := resp["errors"].([]any)
	if len(errs) == 0 {
		t.Fatal("invalid GraphQL transition should produce an error")
	}
	if !strings.Contains(toJSON(errs), "invalid transition") {
		t.Fatalf("GraphQL error should mention the invalid transition: %s", toJSON(errs))
	}
}

// TestSM_TransactionEnforcesTransitions: a G4 batch op that violates a transition
// fails the WHOLE transaction (nothing applied); a valid one applies.
func TestSM_TransactionEnforcesTransitions(t *testing.T) {
	rest, _, pool, tok, done := setupSM(t)
	defer done()
	admin := tok("super_admin", superID)
	id := createOrder(t, rest, admin, "o-tx")

	// Batch: create a ledger entry + an INVALID order transition (pending→delivered).
	st, body := dpStatusBody(t, rest, "POST", "/api/transaction", admin, map[string]any{"operations": []any{
		map[string]any{"op": "create", "resource": "ledger_entries", "data": map[string]any{"ref": "tx-ref", "amount": 1}},
		map[string]any{"op": "update", "resource": "orders", "id": id, "data": map[string]any{"status": "delivered"}},
	}})
	if st < 400 {
		t.Fatalf("batch with an invalid transition should fail; got %d %s", st, body)
	}
	// Nothing applied: the ledger entry was NOT created and the order is still pending.
	var n int
	pool.QueryRow(context.Background(), "SELECT count(*) FROM tenant_"+tenantID+".ledger_entries").Scan(&n)
	if n != 0 {
		t.Fatalf("invalid-transition batch persisted a ledger entry (count=%d) — not atomic", n)
	}
	got := dpDo(t, rest, "GET", "/api/orders/"+id, admin, nil, 200)
	if got["status"] != "pending" {
		t.Fatalf("invalid-transition batch changed order status to %v", got["status"])
	}

	// A VALID transition in a batch applies.
	dpDo(t, rest, "POST", "/api/transaction", admin, map[string]any{"operations": []any{
		map[string]any{"op": "update", "resource": "orders", "id": id, "data": map[string]any{"status": "paid"}},
	}}, 200)
	got = dpDo(t, rest, "GET", "/api/orders/"+id, admin, nil, 200)
	if got["status"] != "paid" {
		t.Fatalf("valid batch transition did not apply; status=%v", got["status"])
	}
}

// smGqlDo posts a GraphQL operation and returns the decoded response (data + errors).
func smGqlDo(t *testing.T, srv *httptest.Server, token, query string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest("POST", srv.URL+"/graphql", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("graphql: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	body, _ := io.ReadAll(resp.Body)
	json.Unmarshal(body, &out)
	return out
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
