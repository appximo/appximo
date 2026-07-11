package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"
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

// G3 (FIX-G3): aggregation (count/sum/avg/min/max + group_by) on REST and GraphQL,
// scoped by the SAME RBAC row condition + field allowlist + filters as a list read.
const (
	aggUser1 = "11111111-1111-1111-1111-111111111111"
	aggUser2 = "22222222-2222-2222-2222-222222222222"
)

func aggSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "agg",
		Resources: map[string]schema.ResourceSchema{
			"sales": {
				Fields: map[string]schema.FieldDef{
					"region":  {Type: "string", Required: true},
					"amount":  {Type: "float64", Required: true},
					"qty":     {Type: "int"},
					"secret":  {Type: "float64"},
					"user_id": {Type: "uuid"},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			// owner: row-scoped to its own user_id (the no-leak-via-aggregate case).
			"owner": {
				Resources:  json.RawMessage(`["sales"]`),
				Actions:    []string{"read", "create"},
				Conditions: &schema.Condition{Field: "user_id", Op: "eq", Val: "$user_id"},
			},
			// limited: a field allowlist WITHOUT `secret` (can't aggregate a hidden field).
			"limited": {
				Resources: json.RawMessage(`["sales"]`),
				Actions:   []string{"read"},
				Fields:    []string{"id", "region", "amount", "qty", "user_id"},
			},
		}},
	}
}

func setupAgg(t *testing.T) (*httptest.Server, http.Handler, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("aggregate: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := aggSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Agg Co", Email: "g@g.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))

	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	var rbacPolicy rbacpkg.Policy
	_ = json.Unmarshal(policyJSON, &rbacPolicy)
	gqlH := gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, events.NewHub(0), false)
	mux := chi.NewMux()
	mux.Use(tenant.TenantMiddleware)
	mux.Use(auth.JWTMiddleware(jwtSecret))
	mux.Use(rbacpkg.RBACMiddleware(policyJSON))
	mux.Handle("/graphql", gqlH)
	gqlWrap := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	})
	return rest, gqlWrap, genToken, func() { rest.Close(); cleanPG() }
}

// seed inserts: U1 region A {10}, U1 region A {20}, U2 region B {30}. So all-sum=60,
// A sum=30 count=2, B sum=30 count=1; U1's own sum=30.
func seedAgg(t *testing.T, rest *httptest.Server, super string) {
	t.Helper()
	rows := []map[string]any{
		{"region": "A", "amount": 10.0, "qty": 1, "secret": 100.0, "user_id": aggUser1},
		{"region": "A", "amount": 20.0, "qty": 2, "secret": 200.0, "user_id": aggUser1},
		{"region": "B", "amount": 30.0, "qty": 3, "secret": 300.0, "user_id": aggUser2},
	}
	for _, r := range rows {
		dpDo(t, rest, "POST", "/api/sales", super, r, http.StatusCreated)
	}
}

func TestAggregate_REST_Overall(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	got := dpDo(t, rest, "GET", "/api/sales/aggregate?count&sum=amount&avg=amount&min=amount&max=amount", super, nil, http.StatusOK)
	if got["count"].(float64) != 3 {
		t.Errorf("count: want 3, got %v", got["count"])
	}
	sum := got["sum"].(map[string]any)
	if sum["amount"].(float64) != 60 {
		t.Errorf("sum amount: want 60, got %v", sum["amount"])
	}
	if got["avg"].(map[string]any)["amount"].(float64) != 20 {
		t.Errorf("avg amount: want 20, got %v", got["avg"])
	}
	if got["min"].(map[string]any)["amount"].(float64) != 10 {
		t.Errorf("min amount: want 10, got %v", got["min"])
	}
	if got["max"].(map[string]any)["amount"].(float64) != 30 {
		t.Errorf("max amount: want 30, got %v", got["max"])
	}
}

func TestAggregate_REST_GroupByAndFilter(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	// group_by region
	got := dpDo(t, rest, "GET", "/api/sales/aggregate?count&sum=amount&group_by=region", super, nil, http.StatusOK)
	groups := got["groups"].([]any)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d (%v)", len(groups), groups)
	}
	byRegion := map[string]map[string]any{}
	for _, g := range groups {
		gm := g.(map[string]any)
		byRegion[gm["region"].(string)] = gm
	}
	if byRegion["A"]["count"].(float64) != 2 || byRegion["A"]["sum"].(map[string]any)["amount"].(float64) != 30 {
		t.Errorf("group A wrong: %v", byRegion["A"])
	}
	if byRegion["B"]["count"].(float64) != 1 || byRegion["B"]["sum"].(map[string]any)["amount"].(float64) != 30 {
		t.Errorf("group B wrong: %v", byRegion["B"])
	}

	// filter is applied to the aggregate
	f := dpDo(t, rest, "GET", "/api/sales/aggregate?sum=amount&filter[region][eq]=A", super, nil, http.StatusOK)
	if f["sum"].(map[string]any)["amount"].(float64) != 30 {
		t.Errorf("filtered sum (region=A): want 30, got %v", f["sum"])
	}
}

// The no-leak-via-aggregate guarantee: a row-scoped role aggregates ONLY its own
// rows (the RBAC row condition is in the aggregate WHERE), never the whole table.
func TestAggregate_REST_RBACRowScope(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	owner1 := tok("owner", aggUser1)
	got := dpDo(t, rest, "GET", "/api/sales/aggregate?count&sum=amount", owner1, nil, http.StatusOK)
	if got["count"].(float64) != 2 {
		t.Errorf("owner U1 count: want 2 (own rows only), got %v", got["count"])
	}
	if got["sum"].(map[string]any)["amount"].(float64) != 30 {
		t.Errorf("owner U1 sum: want 30 (own rows only, NOT 60), got %v", got["sum"])
	}
}

// A field outside the role's allowlist cannot be aggregated → 403 (no leak).
func TestAggregate_REST_FieldAllowlist(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	limited := tok("limited", superID)
	// secret is NOT in the allowlist → forbidden.
	if code, body := dpStatusBody(t, rest, "GET", "/api/sales/aggregate?sum=secret", limited, nil); code != http.StatusForbidden {
		t.Fatalf("sum of a hidden field: want 403, got %d (%s)", code, body)
	}
	// an allowed field aggregates fine.
	got := dpDo(t, rest, "GET", "/api/sales/aggregate?sum=amount", limited, nil, http.StatusOK)
	if got["sum"].(map[string]any)["amount"].(float64) != 60 {
		t.Errorf("limited sum amount: want 60, got %v", got["sum"])
	}
}

func TestAggregate_REST_BadRequests(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)

	if c := dpStatus(t, rest, "GET", "/api/sales/aggregate?sum=nope", super, nil); c != http.StatusBadRequest {
		t.Errorf("unknown field: want 400, got %d", c)
	}
	if c := dpStatus(t, rest, "GET", "/api/sales/aggregate?sum=region", super, nil); c != http.StatusBadRequest {
		t.Errorf("sum on string: want 400, got %d", c)
	}
	if c := dpStatus(t, rest, "GET", "/api/sales/aggregate?group_by=region", super, nil); c != http.StatusBadRequest {
		t.Errorf("no aggregate function: want 400, got %d", c)
	}
}

// ?count=true on the LIST adds meta.total (closes the REST↔GraphQL asymmetry).
func TestAggregate_REST_ListCount(t *testing.T) {
	rest, _, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	withCount := dpDo(t, rest, "GET", "/api/sales?count=true", super, nil, http.StatusOK)
	meta := withCount["meta"].(map[string]any)
	if meta["total"].(float64) != 3 {
		t.Errorf("list ?count=true total: want 3, got %v", meta["total"])
	}
	// default list (no count) omits total — the byte-identical default path.
	plain := dpDo(t, rest, "GET", "/api/sales", super, nil, http.StatusOK)
	if _, present := plain["meta"].(map[string]any)["total"]; present {
		t.Errorf("default list must NOT include total, got %v", plain["meta"])
	}
}

func TestAggregate_GraphQL(t *testing.T) {
	rest, gql, tok, done := setupAgg(t)
	defer done()
	super := tok("super_admin", superID)
	seedAgg(t, rest, super)

	// overall count + sum
	data, errs := gqlDo(t, gql, super, `{ salesAggregate(count:true, sum:["amount"]) { count values { fn field value } } }`)
	if len(errs) != 0 {
		t.Fatalf("graphql aggregate errors: %v", errs)
	}
	agg := data["salesAggregate"].(map[string]any)
	if agg["count"].(float64) != 3 {
		t.Errorf("gql count: want 3, got %v", agg["count"])
	}
	vals := agg["values"].([]any)
	if len(vals) != 1 || vals[0].(map[string]any)["value"].(string) != "60" {
		t.Errorf("gql sum value: want \"60\", got %v", vals)
	}

	// group_by
	gdata, gerrs := gqlDo(t, gql, super, `{ salesAggregate(sum:["amount"], group_by:["region"]) { groups { key { field value } values { field value } } } }`)
	if len(gerrs) != 0 {
		t.Fatalf("graphql group aggregate errors: %v", gerrs)
	}
	groups := gdata["salesAggregate"].(map[string]any)["groups"].([]any)
	if len(groups) != 2 {
		t.Errorf("gql groups: want 2, got %d", len(groups))
	}

	// RBAC row scope via GraphQL: owner sees only its own sum (30, not 60).
	owner1 := tok("owner", aggUser1)
	odata, oerrs := gqlDo(t, gql, owner1, `{ salesAggregate(sum:["amount"]) { values { value } } }`)
	if len(oerrs) != 0 {
		t.Fatalf("graphql owner aggregate errors: %v", oerrs)
	}
	ov := odata["salesAggregate"].(map[string]any)["values"].([]any)
	if ov[0].(map[string]any)["value"].(string) != "30" {
		t.Errorf("gql owner sum: want \"30\" (own rows only), got %v", ov)
	}
}
