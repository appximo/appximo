package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/schema"
)

// NIGHT-SWEEP-S1 — the DB-backed halves of ENG-15/16/17/18/23/24: the contracts
// only a real list/aggregate response can prove (meta shape on a cursor
// request, the opt-in total on the include path, the count flag by value).
// The pure-parse halves live in pkg/query's unit tests; the cross-binary
// behavior is pinned by scripts/binary-diff/corpus.jsonl.

func nsSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "nightsweep",
		Resources: map[string]schema.ResourceSchema{
			"boards": {
				Fields: map[string]schema.FieldDef{
					"name": {Type: "string", Required: true},
				},
				Relations: map[string]schema.RelationDef{
					"cards": {Type: schema.RelationHasMany, Target: "cards", FK: "board_id"},
				},
			},
			"cards": {
				Fields: map[string]schema.FieldDef{
					"board_id": {Type: "uuid", Relation: "boards"},
					"label":    {Type: "string"},
				},
			},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
		}},
	}
}

func setupNS(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("nightsweep: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := nsSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "NS", Email: "ns@ns.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	rest := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return rest, genToken("super_admin", superID), func() { rest.Close(); cleanPG() }
}

func seedNS(t *testing.T, rest *httptest.Server, tok string) []string {
	t.Helper()
	ids := make([]string, 0, 3)
	for _, n := range []string{"uno", "dos", "tres"} {
		got := dpDo(t, rest, "POST", "/api/boards", tok, map[string]any{"name": n}, http.StatusCreated)
		id, _ := got["id"].(string)
		if id == "" {
			if data, ok := got["data"].(map[string]any); ok {
				id, _ = data["id"].(string)
			}
		}
		if id == "" {
			t.Fatalf("seed: no id in create response: %v", got)
		}
		ids = append(ids, id)
		dpDo(t, rest, "POST", "/api/cards", tok, map[string]any{"board_id": id, "label": "c-" + n}, http.StatusCreated)
	}
	return ids
}

// TestNightSweep_CursorMetaOmitsPage — ENG-15: a cursor response's meta must
// not assert a page number the query never used. Page requests keep the full
// meta (page/has_prev present) — the two shapes are the contract.
func TestNightSweep_CursorMetaOmitsPage(t *testing.T) {
	rest, tok, done := setupNS(t)
	defer done()
	ids := seedNS(t, rest, tok)

	plain := dpDo(t, rest, "GET", "/api/boards", tok, nil, http.StatusOK)
	pm := plain["meta"].(map[string]any)
	if _, ok := pm["page"]; !ok {
		t.Errorf("page request must keep meta.page, got %v", pm)
	}
	if _, ok := pm["has_prev"]; !ok {
		t.Errorf("page request must keep meta.has_prev, got %v", pm)
	}

	cur := dpDo(t, rest, "GET", "/api/boards?after="+ids[0]+"&per_page=2", tok, nil, http.StatusOK)
	cm := cur["meta"].(map[string]any)
	if _, present := cm["page"]; present {
		t.Errorf("cursor request must NOT carry meta.page (it has no page number), got %v", cm)
	}
	if _, present := cm["has_prev"]; present {
		t.Errorf("cursor request must NOT carry meta.has_prev, got %v", cm)
	}
	if cm["per_page"].(float64) != 2 {
		t.Errorf("per_page IS honored with a cursor, got %v", cm)
	}
	if _, ok := cm["has_next"]; !ok {
		t.Errorf("cursor request keeps has_next, got %v", cm)
	}
}

// TestNightSweep_CursorConflictsRejected — ENG-15 over the wire: sort/page/
// second-cursor/count with a cursor are named 400s, never silent discards.
func TestNightSweep_CursorConflictsRejected(t *testing.T) {
	rest, tok, done := setupNS(t)
	defer done()
	ids := seedNS(t, rest, tok)

	for name, path := range map[string]string{
		"cursor+sort":   "/api/boards?after=" + ids[0] + "&sort=name",
		"cursor+page":   "/api/boards?after=" + ids[0] + "&page=2",
		"two-cursors":   "/api/boards?after=" + ids[0] + "&before=" + ids[2],
		"cursor+order[": "/api/boards?after=" + ids[0] + "&order[name]=desc",
		"cursor+count":  "/api/boards?count=true&after=" + ids[0],
		"empty cursor":  "/api/boards?after=",
	} {
		if c, body := dpStatusBody(t, rest, "GET", path, tok, nil); c != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d (%s)", name, c, body)
		}
	}
}

// TestNightSweep_CountFlagByValue — ENG-23: the list total is opt-in BY VALUE,
// works on the include path, and a garbage value is named.
func TestNightSweep_CountFlagByValue(t *testing.T) {
	rest, tok, done := setupNS(t)
	defer done()
	seedNS(t, rest, tok)

	on := dpDo(t, rest, "GET", "/api/boards?count=true", tok, nil, http.StatusOK)
	if on["meta"].(map[string]any)["total"].(float64) != 3 {
		t.Errorf("count=true total: want 3, got %v", on["meta"])
	}

	off := dpDo(t, rest, "GET", "/api/boards?count=false", tok, nil, http.StatusOK)
	if _, present := off["meta"].(map[string]any)["total"]; present {
		t.Errorf("count=false must NOT attach total (it used to turn it ON), got %v", off["meta"])
	}

	if c, body := dpStatusBody(t, rest, "GET", "/api/boards?count=maybe", tok, nil); c != http.StatusBadRequest || !strings.Contains(body, "maybe") {
		t.Errorf("count=maybe: want a 400 naming the value, got %d (%s)", c, body)
	}

	// ENG-23: ?count on the INCLUDE path used to be dropped entirely (the embed
	// branch returned before the count block).
	inc := dpDo(t, rest, "GET", "/api/boards?count=true&include=cards", tok, nil, http.StatusOK)
	im := inc["meta"].(map[string]any)
	if im["total"].(float64) != 3 {
		t.Errorf("count=true with include: want total 3, got %v", im)
	}
	data := inc["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("include list: want 3 boards, got %d", len(data))
	}
	if _, ok := data[0].(map[string]any)["cards"]; !ok {
		t.Errorf("include=cards must still embed, got %v", data[0])
	}
}

// TestNightSweep_RepeatedParams — ENG-17 over the wire.
func TestNightSweep_RepeatedParams(t *testing.T) {
	rest, tok, done := setupNS(t)
	defer done()
	seedNS(t, rest, tok)

	for name, path := range map[string]string{
		"per_page": "/api/boards?per_page=1&per_page=3",
		"filter":   "/api/boards?filter[name][eq]=uno&filter[name][eq]=dos",
		"include":  "/api/boards?include=cards&include=cards",
	} {
		if c, body := dpStatusBody(t, rest, "GET", path, tok, nil); c != http.StatusBadRequest || !strings.Contains(body, "send it once") {
			t.Errorf("%s: want 400 send-it-once, got %d (%s)", name, c, body)
		}
	}
}

// TestNightSweep_AggregateNamespace — ENG-18/24 over the wire: the aggregate
// endpoint owns its parameter namespace.
func TestNightSweep_AggregateNamespace(t *testing.T) {
	rest, tok, done := setupNS(t)
	defer done()
	seedNS(t, rest, tok)

	cases := map[string]string{
		"unknown fn":     "/api/boards/aggregate?count&median=name",
		"valid-but-list": "/api/boards/aggregate?count&sort=name",
		"page":           "/api/boards/aggregate?count&page=2",
		"empty group_by": "/api/boards/aggregate?count&group_by=",
		"count=garbage":  "/api/boards/aggregate?count=yes",
		"trailing comma": "/api/boards/aggregate?count&group_by=name,",
		"repeated fn":    "/api/boards/aggregate?count&group_by=name&group_by=name",
		"include on agg": "/api/boards/aggregate?count&include=cards",
	}
	for name, path := range cases {
		if c, body := dpStatusBody(t, rest, "GET", path, tok, nil); c != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d (%s)", name, c, body)
		}
	}

	// The working shapes stay working.
	got := dpDo(t, rest, "GET", "/api/boards/aggregate?count&group_by=name", tok, nil, http.StatusOK)
	if groups := got["groups"].([]any); len(groups) != 3 {
		t.Errorf("group_by=name: want 3 groups, got %v", got)
	}
}
