package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

func testResource() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"title":      {Type: "string"},
			"origin":     {Type: "string"},
			"status":     {Type: "string"},
			"amount":     {Type: "float64"},
			"created_at": {Type: "time", Auto: true},
			"user_id":    {Type: "uuid"},
		},
	}
}

func TestBuildQuery_Defaults(t *testing.T) {
	qb := BuildQuery("orders", testResource(), url.Values{}, nil)

	if qb.Page() != DefaultPage {
		t.Errorf("page: got %d, want %d", qb.Page(), DefaultPage)
	}
	if qb.PerPage() != DefaultPerPage {
		t.Errorf("per_page: got %d, want %d", qb.PerPage(), DefaultPerPage)
	}

	selectQ, countQ, selectArgs, countArgs := qb.SQL()

	want := "SELECT * FROM orders ORDER BY id ASC LIMIT $1 OFFSET $2"
	if selectQ != want {
		t.Errorf("selectQ:\n  got  %q\n  want %q", selectQ, want)
	}
	if countQ != "SELECT COUNT(*) FROM orders" {
		t.Errorf("countQ: got %q", countQ)
	}
	if len(selectArgs) != 2 || selectArgs[0] != DefaultPerPage || selectArgs[1] != 0 {
		t.Errorf("selectArgs: got %v", selectArgs)
	}
	if len(countArgs) != 0 {
		t.Errorf("countArgs should be empty, got %v", countArgs)
	}
}

func TestBuildQuery_CustomPagination(t *testing.T) {
	params := url.Values{"page": {"3"}, "per_page": {"10"}}
	qb := BuildQuery("orders", testResource(), params, nil)

	if qb.Page() != 3 {
		t.Errorf("page: got %d, want 3", qb.Page())
	}
	if qb.PerPage() != 10 {
		t.Errorf("per_page: got %d, want 10", qb.PerPage())
	}

	_, _, selectArgs, _ := qb.SQL()
	// LIMIT=10, OFFSET=(3-1)*10=20
	if selectArgs[0] != 10 || selectArgs[1] != 20 {
		t.Errorf("LIMIT/OFFSET args: got %v, want [10 20]", selectArgs)
	}
}

func TestBuildQuery_PerPageCappedAt100(t *testing.T) {
	params := url.Values{"per_page": {"999"}}
	qb := BuildQuery("orders", testResource(), params, nil)
	if qb.PerPage() != MaxPerPage {
		t.Errorf("per_page should be capped at %d, got %d", MaxPerPage, qb.PerPage())
	}
}

func TestBuildQuery_Filter(t *testing.T) {
	// Two filters — alphabetical: origin before status
	params := url.Values{
		"filter[status]": {"pending"},
		"filter[origin]": {"Bogota"},
	}
	qb := BuildQuery("orders", testResource(), params, nil)
	selectQ, countQ, selectArgs, countArgs := qb.SQL()

	wantWhere := "WHERE origin = $1 AND status = $2"
	if !strings.Contains(selectQ, wantWhere) {
		t.Errorf("selectQ: want %q in %q", wantWhere, selectQ)
	}
	if !strings.Contains(countQ, wantWhere) {
		t.Errorf("countQ: want %q in %q", wantWhere, countQ)
	}
	if len(countArgs) != 2 {
		t.Errorf("countArgs len: got %d, want 2", len(countArgs))
	}
	if len(selectArgs) != 4 { // 2 filter args + LIMIT + OFFSET
		t.Errorf("selectArgs len: got %d, want 4", len(selectArgs))
	}
}

func TestBuildQuery_UnknownFilterIgnored(t *testing.T) {
	params := url.Values{
		"filter[status]":    {"pending"},     // valid
		"filter[password]":  {"secret"},      // not in schema
		"filter[injected]":  {"' OR 1=1--"}, // not in schema
	}
	qb := BuildQuery("orders", testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()

	if strings.Contains(selectQ, "password") || strings.Contains(selectQ, "injected") {
		t.Errorf("unknown fields must not appear in SQL: %s", selectQ)
	}
	if !strings.Contains(selectQ, "status") {
		t.Errorf("known field should appear in SQL: %s", selectQ)
	}
}

func TestBuildQuery_Sort(t *testing.T) {
	params := url.Values{"sort": {"created_at"}, "order": {"desc"}}
	qb := BuildQuery("orders", testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()
	if !strings.Contains(selectQ, "ORDER BY created_at DESC") {
		t.Errorf("expected ORDER BY created_at DESC in %q", selectQ)
	}
}

func TestBuildQuery_SortUnknownFieldFallsBackToDefault(t *testing.T) {
	params := url.Values{"sort": {"password"}}
	qb := BuildQuery("orders", testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()
	if !strings.Contains(selectQ, "ORDER BY id ASC") {
		t.Errorf("unknown sort field should fall back to id ASC: %q", selectQ)
	}
}

func TestBuildQuery_Search(t *testing.T) {
	params := url.Values{"search": {"bogota"}}
	qb := BuildQuery("orders", testResource(), params, nil)
	selectQ, countQ, _, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "ILIKE") {
		t.Errorf("search ILIKE missing from selectQ: %s", selectQ)
	}
	if !strings.Contains(countQ, "ILIKE") {
		t.Errorf("search ILIKE missing from countQ: %s", countQ)
	}
	// all search args should be wrapped with %
	for i, a := range countArgs {
		if a != "%bogota%" {
			t.Errorf("countArgs[%d] = %v, want %%bogota%%", i, a)
		}
	}
}

func TestBuildQuery_RBACConditionCombinedWithFilter(t *testing.T) {
	cond := &rbac.WhereCondition{Field: "user_id", Op: "eq", Value: "abc-123"}
	params := url.Values{"filter[status]": {"pending"}}
	qb := BuildQuery("orders", testResource(), params, cond)
	selectQ, _, _, countArgs := qb.SQL()

	// RBAC condition must be $1, filter must be $2
	if !strings.Contains(selectQ, "user_id = $1") {
		t.Errorf("RBAC condition must be $1: %s", selectQ)
	}
	if !strings.Contains(selectQ, "status = $2") {
		t.Errorf("filter must be $2 after RBAC: %s", selectQ)
	}
	if len(countArgs) < 1 || countArgs[0] != "abc-123" {
		t.Errorf("RBAC value not first in countArgs: %v", countArgs)
	}
}

func TestBuildQuery_PageZeroFallsToDefault(t *testing.T) {
	params := url.Values{"page": {"0"}}
	qb := BuildQuery("orders", testResource(), params, nil)
	if qb.Page() != DefaultPage {
		t.Errorf("page=0 should fall back to default %d, got %d", DefaultPage, qb.Page())
	}
}
