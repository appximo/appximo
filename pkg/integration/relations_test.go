package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/events"
	"github.com/appximo/appximo/pkg/extensions"
	gqlhandler "github.com/appximo/appximo/pkg/graphql"
	rbacpkg "github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	chi "github.com/go-chi/chi/v5"
)

const (
	relOpID    = "aaaa0000-0000-0000-0000-000000000001"
	relOtherID = "bbbb0000-0000-0000-0000-000000000002"
)

// relSchema is the ADR-019 sales example plus the bits the tests need: an
// operator_id on orders and lines (row-level RBAC), a many_to_many via the
// hyphenated junction resource orderproducts (so migration creates the table and
// its FK indexes), and a "firstlines" has_many with a small Limit for the top-N
// test (kept separate so the main "lines" embed stays unbounded).
func relSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "sales-rel",
		Resources: map[string]schema.ResourceSchema{
			"orders": {
				Fields: map[string]schema.FieldDef{
					"status":      {Type: "string"},
					"customer_id": {Type: "uuid"},
					"operator_id": {Type: "uuid"},
				},
				Relations: map[string]schema.RelationDef{
					"lines":      {Type: "has_many", Target: "lines", FK: "order_id"},
					"firstlines": {Type: "has_many", Target: "lines", FK: "order_id", Limit: 2},
					"customer":   {Type: "belongs_to", Target: "customers", FK: "customer_id"},
				},
			},
			"lines": {
				Fields: map[string]schema.FieldDef{
					"order_id":    {Type: "uuid", Required: true},
					"product_id":  {Type: "uuid"},
					"qty":         {Type: "int"},
					"operator_id": {Type: "uuid"},
				},
				Relations: map[string]schema.RelationDef{
					"product": {Type: "belongs_to", Target: "products", FK: "product_id"},
				},
			},
			"products": {
				Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}},
				Relations: map[string]schema.RelationDef{
					"orders": {Type: "many_to_many", Target: "orders", Through: "orderproducts", FK: "product_id", TargetFK: "order_id"},
				},
			},
			"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string", Required: true}}},
			// note (string) gives the junction an orderable/filterable field so the
			// generated GraphQL Order/Filter input types are non-empty (graphql-go
			// rejects an empty input object — a resource of only-uuid fields).
			"orderproducts": {Fields: map[string]schema.FieldDef{"order_id": {Type: "uuid"}, "product_id": {Type: "uuid"}, "note": {Type: "string"}}},
		},
		RBAC: schema.RBACPolicy{
			Roles: map[string]schema.RolePolicy{
				"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
				// can read orders/lines/products but NOT customers (forbidden-include test)
				"noref": {Resources: json.RawMessage(`["orders","lines","products"]`), Actions: []string{"read"}},
				// row-level: only own rows by operator_id (orders AND lines embed)
				"op": {
					Resources:  json.RawMessage(`["orders","lines"]`),
					Actions:    []string{"read"},
					Conditions: &schema.Condition{Field: "operator_id", Op: "eq", Val: "$user_id"},
				},
			},
		},
	}
}

func setupRel(t *testing.T) (*httptest.Server, string, *schema.APISchema, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("relations: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	s := relSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Sales Co", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return srv, genToken("super_admin", superID), s, func() { srv.Close(); cleanPG() }
}

// relData seeds one order owned by relOpID with 3 lines (2 owned by relOpID, 1 by
// relOtherID), a customer, three products, and two m2m links. Returns the order id.
func relSeed(t *testing.T, srv *httptest.Server, tok string) (orderID, custID string, prodIDs []string) {
	t.Helper()
	cust := dpDo(t, srv, "POST", "/api/customers", tok, map[string]any{"name": "Acme"}, http.StatusCreated)
	custID = cust["id"].(string)
	for _, n := range []string{"P1", "P2", "P3"} {
		p := dpDo(t, srv, "POST", "/api/products", tok, map[string]any{"name": n}, http.StatusCreated)
		prodIDs = append(prodIDs, p["id"].(string))
	}
	ord := dpDo(t, srv, "POST", "/api/orders", tok, map[string]any{"status": "open", "customer_id": custID, "operator_id": relOpID}, http.StatusCreated)
	orderID = ord["id"].(string)
	dpDo(t, srv, "POST", "/api/lines", tok, map[string]any{"order_id": orderID, "product_id": prodIDs[0], "qty": 1, "operator_id": relOpID}, http.StatusCreated)
	dpDo(t, srv, "POST", "/api/lines", tok, map[string]any{"order_id": orderID, "product_id": prodIDs[1], "qty": 2, "operator_id": relOpID}, http.StatusCreated)
	dpDo(t, srv, "POST", "/api/lines", tok, map[string]any{"order_id": orderID, "product_id": prodIDs[2], "qty": 3, "operator_id": relOtherID}, http.StatusCreated)
	dpDo(t, srv, "POST", "/api/orderproducts", tok, map[string]any{"order_id": orderID, "product_id": prodIDs[0]}, http.StatusCreated)
	dpDo(t, srv, "POST", "/api/orderproducts", tok, map[string]any{"order_id": orderID, "product_id": prodIDs[1]}, http.StatusCreated)
	return orderID, custID, prodIDs
}

func TestRelations(t *testing.T) {
	srv, super, _, done := setupRel(t)
	defer done()
	orderID, _, _ := relSeed(t, srv, super)

	t.Run("has_many: order with nested lines (one round-trip)", func(t *testing.T) {
		orders := dpList(t, srv, "/api/orders?include=lines", super)
		if len(orders) != 1 {
			t.Fatalf("expected 1 order, got %d", len(orders))
		}
		lines, ok := orders[0]["lines"].([]any)
		if !ok || len(lines) != 3 {
			t.Fatalf("expected 3 nested lines, got %v", orders[0]["lines"])
		}
	})

	t.Run("gate: no include → flat row, no relation keys", func(t *testing.T) {
		orders := dpList(t, srv, "/api/orders", super)
		if _, has := orders[0]["lines"]; has {
			t.Fatalf("plain list must not embed lines: %v", orders[0])
		}
		if orders[0]["status"] != "open" {
			t.Fatalf("plain list lost scalar fields: %v", orders[0])
		}
	})

	t.Run("belongs_to: line.product nested object", func(t *testing.T) {
		lines := dpList(t, srv, "/api/lines?include=product", super)
		if len(lines) != 3 {
			t.Fatalf("expected 3 lines, got %d", len(lines))
		}
		for _, l := range lines {
			p, ok := l["product"].(map[string]any)
			if !ok || p["name"] == nil {
				t.Fatalf("line missing nested product: %v", l)
			}
		}
	})

	t.Run("belongs_to: order.customer nested object", func(t *testing.T) {
		orders := dpList(t, srv, "/api/orders?include=customer", super)
		cust, ok := orders[0]["customer"].(map[string]any)
		if !ok || cust["name"] != "Acme" {
			t.Fatalf("order missing nested customer: %v", orders[0]["customer"])
		}
	})

	t.Run("many_to_many: product.orders via junction", func(t *testing.T) {
		products := dpList(t, srv, "/api/products?include=orders", super)
		linked := 0
		for _, p := range products {
			if os, ok := p["orders"].([]any); ok {
				linked += len(os)
			}
		}
		// P1 and P2 are each linked to O1 → 2 link rows total.
		if linked != 2 {
			t.Fatalf("expected 2 m2m links total across products, got %d (%v)", linked, products)
		}
	})

	t.Run("nesting depth 2: lines.product", func(t *testing.T) {
		orders := dpList(t, srv, "/api/orders?include=lines.product", super)
		lines := orders[0]["lines"].([]any)
		first := lines[0].(map[string]any)
		if _, ok := first["product"].(map[string]any); !ok {
			t.Fatalf("nested line missing product: %v", first)
		}
	})

	t.Run("depth exceeded → 400", func(t *testing.T) {
		if got := dpStatus(t, srv, "GET", "/api/orders?include=lines.product.orders", super, nil); got != 400 {
			t.Fatalf("expected 400 for depth>2, got %d", got)
		}
	})

	t.Run("top-N: firstlines capped at relation Limit", func(t *testing.T) {
		orders := dpList(t, srv, "/api/orders?include=firstlines", super)
		fl, ok := orders[0]["firstlines"].([]any)
		if !ok || len(fl) != 2 {
			t.Fatalf("expected firstlines capped at 2, got %v", orders[0]["firstlines"])
		}
	})

	t.Run("get-by-id with include", func(t *testing.T) {
		got := dpDo(t, srv, "GET", "/api/orders/"+orderID+"?include=lines", super, nil, http.StatusOK)
		if lines, ok := got["lines"].([]any); !ok || len(lines) != 3 {
			t.Fatalf("get-by-id include=lines wrong: %v", got["lines"])
		}
	})

	t.Run("RBAC: include of a forbidden target → 403", func(t *testing.T) {
		noref := genToken("noref", superID)
		if got := dpStatus(t, srv, "GET", "/api/orders?include=customer", noref, nil); got != 403 {
			t.Fatalf("expected 403 for forbidden include, got %d", got)
		}
	})

	t.Run("RBAC row-level: embed filtered to caller's own rows", func(t *testing.T) {
		opTok := genToken("op", relOpID)
		orders := dpList(t, srv, "/api/orders?include=lines", opTok)
		if len(orders) != 1 {
			t.Fatalf("op should see its own order, got %d", len(orders))
		}
		lines, ok := orders[0]["lines"].([]any)
		if !ok || len(lines) != 2 {
			t.Fatalf("op should see only its 2 own lines in embed, got %v", orders[0]["lines"])
		}
		for _, l := range lines {
			if l.(map[string]any)["operator_id"] != relOpID {
				t.Fatalf("embed leaked a line not owned by caller: %v", l)
			}
		}
	})

	// GraphQL nested coverage lives in TestRelationsGraphQL (its own server).
}

// TestRelationsGraphQL proves a nested GraphQL selection compiles to the same
// json_agg+LATERAL path and returns the same nested data as REST ?include=.
func TestRelationsGraphQL(t *testing.T) {
	if testing.Short() {
		t.Skip("relations gql: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := relSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Sales Co", Email: "s@s.com", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	defer rest.Close()
	super := genToken("super_admin", superID)
	relSeed(t, rest, super)

	// GraphQL server (tenant → jwt → rbac → /graphql), same chain as cmd_serve.
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
	gsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		mux.ServeHTTP(w, req)
	}))
	defer gsrv.Close()

	q := `{"query":"{ orders { data { id status lines { id qty } customer { name } } } }"}`
	req, _ := http.NewRequest("POST", gsrv.URL+"/graphql", strings.NewReader(q))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+super)
	resp, err := gsrv.Client().Do(req)
	if err != nil {
		t.Fatalf("graphql: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Orders struct {
				Data []struct {
					ID    string `json:"id"`
					Lines []struct {
						ID  string `json:"id"`
						Qty int    `json:"qty"`
					} `json:"lines"`
					Customer struct {
						Name string `json:"name"`
					} `json:"customer"`
				} `json:"data"`
			} `json:"orders"`
		} `json:"data"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Errors) > 0 {
		t.Fatalf("graphql errors: %v", out.Errors)
	}
	if len(out.Data.Orders.Data) != 1 {
		t.Fatalf("expected 1 order, got %d", len(out.Data.Orders.Data))
	}
	o := out.Data.Orders.Data[0]
	if len(o.Lines) != 3 {
		t.Fatalf("expected 3 nested lines via GraphQL, got %d", len(o.Lines))
	}
	if o.Customer.Name != "Acme" {
		t.Fatalf("expected nested customer Acme, got %q", o.Customer.Name)
	}
}
