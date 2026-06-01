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
			"code":       {Type: "string"},
			"nombre":     {Type: "string"},
			"amount":     {Type: "float64"},
			"precio":     {Type: "float64"},
			"created_at": {Type: "time", Auto: true},
			"user_id":    {Type: "uuid"},
		},
	}
}

// helpers

func mustBuild(t *testing.T, res *schema.ResourceSchema, params url.Values, cond *rbac.WhereCondition) *QueryBuilder {
	t.Helper()
	qb, err := BuildQuery("orders", res, params, cond)
	if err != nil {
		t.Fatalf("BuildQuery unexpected error: %v", err)
	}
	return qb
}

func mustError(t *testing.T, res *schema.ResourceSchema, params url.Values) string {
	t.Helper()
	_, err := BuildQuery("orders", res, params, nil)
	if err == nil {
		t.Fatal("BuildQuery expected error, got nil")
	}
	return err.Error()
}

// ── TAREA 2: paginación ──────────────────────────────────────────────────────

func TestBuildQuery_Defaults(t *testing.T) {
	qb := mustBuild(t, testResource(), url.Values{}, nil)

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
	// 25 rows, per_page=10 → page 1 args: LIMIT=10 OFFSET=0
	params := url.Values{"page": {"3"}, "per_page": {"10"}}
	qb := mustBuild(t, testResource(), params, nil)

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
	// per_page=200 → silently clipped to 100
	params := url.Values{"per_page": {"200"}}
	qb := mustBuild(t, testResource(), params, nil)
	if qb.PerPage() != MaxPerPage {
		t.Errorf("per_page should be capped at %d, got %d", MaxPerPage, qb.PerPage())
	}
}

func TestBuildQuery_PageNonIntegerReturnsError(t *testing.T) {
	// ?page=abc → 400
	msg := mustError(t, testResource(), url.Values{"page": {"abc"}})
	if !strings.Contains(msg, "invalid page parameter") {
		t.Errorf("expected 'invalid page parameter' in error, got %q", msg)
	}
}

// ── TAREA 1: filtros básicos ─────────────────────────────────────────────────

func TestBuildQuery_FilterEqStatus(t *testing.T) {
	// ?filter[status]=pending → status = $1
	params := url.Values{"filter[status]": {"pending"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, countQ, selectArgs, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "status = $1") {
		t.Errorf("selectQ: want 'status = $1' in %q", selectQ)
	}
	if !strings.Contains(countQ, "status = $1") {
		t.Errorf("countQ: want 'status = $1' in %q", countQ)
	}
	if len(countArgs) != 1 || countArgs[0] != "pending" {
		t.Errorf("countArgs: got %v", countArgs)
	}
	_ = selectArgs
}

func TestBuildQuery_FilterPartialCode(t *testing.T) {
	// ?filter[code][partial]=GU → code ILIKE '%GU%'
	params := url.Values{"filter[code][partial]": {"GU"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "code ILIKE $1") {
		t.Errorf("selectQ: want 'code ILIKE $1' in %q", selectQ)
	}
	if len(countArgs) != 1 || countArgs[0] != "%GU%" {
		t.Errorf("countArgs: got %v, want [%%GU%%]", countArgs)
	}
}

func TestBuildQuery_FilterAfterDate(t *testing.T) {
	// ?filter[created_at][after]=2025-01-01 → created_at > $1
	params := url.Values{"filter[created_at][after]": {"2025-01-01"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "created_at > $1") {
		t.Errorf("selectQ: want 'created_at > $1' in %q", selectQ)
	}
	if len(countArgs) != 1 || countArgs[0] != "2025-01-01" {
		t.Errorf("countArgs: got %v", countArgs)
	}
}

func TestBuildQuery_FilterNumericRange(t *testing.T) {
	// ?filter[precio][gte]=100&filter[precio][lte]=500 → precio >= $1 AND precio <= $2
	params := url.Values{
		"filter[precio][gte]": {"100"},
		"filter[precio][lte]": {"500"},
	}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, countArgs := qb.SQL()

	// filters sorted by (field, op): gte before lte
	if !strings.Contains(selectQ, "precio >= $1") {
		t.Errorf("selectQ: want 'precio >= $1' in %q", selectQ)
	}
	if !strings.Contains(selectQ, "precio <= $2") {
		t.Errorf("selectQ: want 'precio <= $2' in %q", selectQ)
	}
	if len(countArgs) != 2 {
		t.Errorf("countArgs len: got %d, want 2", len(countArgs))
	}
}

func TestBuildQuery_UnknownFilterFieldReturnsError(t *testing.T) {
	// ?filter[campo_inexistente]=x → 400
	msg := mustError(t, testResource(), url.Values{"filter[campo_inexistente]": {"x"}})
	if !strings.Contains(msg, "unknown filter field: campo_inexistente") {
		t.Errorf("expected 'unknown filter field' error, got %q", msg)
	}
}

func TestBuildQuery_WrongOperatorTypeReturnsError(t *testing.T) {
	// ?filter[nombre][gte]=texto → 400 (gte invalid for string)
	msg := mustError(t, testResource(), url.Values{"filter[nombre][gte]": {"texto"}})
	if !strings.Contains(msg, "not allowed for type") {
		t.Errorf("expected type incompatibility error, got %q", msg)
	}
}

func TestBuildQuery_SQLInjectionInValueIsParameterized(t *testing.T) {
	// Value is always $N — never concatenated
	params := url.Values{"filter[status]": {"'; DROP TABLE orders; --"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, countArgs := qb.SQL()

	if strings.Contains(selectQ, "DROP") {
		t.Errorf("SQL injection must not appear in query: %s", selectQ)
	}
	if len(countArgs) < 1 || countArgs[0] != "'; DROP TABLE orders; --" {
		t.Errorf("injected value must be in args: %v", countArgs)
	}
}

func TestBuildQuery_TwoFiltersAlphabeticOrder(t *testing.T) {
	// filters sorted alphabetically: origin before status
	params := url.Values{
		"filter[status]": {"pending"},
		"filter[origin]": {"Bogota"},
	}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, countQ, selectArgs, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "WHERE origin = $1 AND status = $2") {
		t.Errorf("selectQ: want alphabetical filter order: %q", selectQ)
	}
	if !strings.Contains(countQ, "WHERE origin = $1 AND status = $2") {
		t.Errorf("countQ: want alphabetical filter order: %q", countQ)
	}
	if len(countArgs) != 2 {
		t.Errorf("countArgs len: got %d, want 2", len(countArgs))
	}
	if len(selectArgs) != 4 {
		t.Errorf("selectArgs len: got %d, want 4", len(selectArgs))
	}
}

func TestBuildQuery_Sort(t *testing.T) {
	params := url.Values{"sort": {"created_at"}, "order": {"desc"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()
	if !strings.Contains(selectQ, "ORDER BY created_at DESC") {
		t.Errorf("expected ORDER BY created_at DESC in %q", selectQ)
	}
}

func TestBuildQuery_OrderBracketSyntax(t *testing.T) {
	// new syntax: ?order[created_at]=desc
	params := url.Values{"order[created_at]": {"desc"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()
	if !strings.Contains(selectQ, "ORDER BY created_at DESC") {
		t.Errorf("order[] syntax not applied: %q", selectQ)
	}
}

func TestBuildQuery_SortUnknownFieldFallsBackToDefault(t *testing.T) {
	params := url.Values{"sort": {"password"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()
	if !strings.Contains(selectQ, "ORDER BY id ASC") {
		t.Errorf("unknown sort field should fall back to id ASC: %q", selectQ)
	}
}

func TestBuildQuery_Search(t *testing.T) {
	params := url.Values{"search": {"bogota"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, countQ, _, countArgs := qb.SQL()

	if !strings.Contains(selectQ, "ILIKE") {
		t.Errorf("search ILIKE missing from selectQ: %s", selectQ)
	}
	if !strings.Contains(countQ, "ILIKE") {
		t.Errorf("search ILIKE missing from countQ: %s", countQ)
	}
	for i, a := range countArgs {
		if a != "%bogota%" {
			t.Errorf("countArgs[%d] = %v, want %%bogota%%", i, a)
		}
	}
}

func TestBuildQuery_RBACConditionCombinedWithFilter(t *testing.T) {
	cond := &rbac.WhereCondition{Field: "user_id", Op: "eq", Value: "abc-123"}
	params := url.Values{"filter[status]": {"pending"}}
	qb := mustBuild(t, testResource(), params, cond)
	selectQ, _, _, countArgs := qb.SQL()

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
	qb := mustBuild(t, testResource(), params, nil)
	if qb.Page() != DefaultPage {
		t.Errorf("page=0 should fall back to default %d, got %d", DefaultPage, qb.Page())
	}
}

// ── Keyset (cursor) pagination ────────────────────────────────────────────────

func TestBuildQuery_AfterCursor_GeneratesCorrectSQL(t *testing.T) {
	const cursor = "01234567-89ab-cdef-0123-456789abcdef"
	params := url.Values{"after": {cursor}, "filter[status]": {"pending"}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, selectArgs, _ := qb.SQL()

	if !strings.Contains(selectQ, "id > $1") {
		t.Errorf("after cursor must produce 'id > $1': %s", selectQ)
	}
	if !strings.Contains(selectQ, "ORDER BY id ASC") {
		t.Errorf("after cursor must use ORDER BY id ASC: %s", selectQ)
	}
	if strings.Contains(selectQ, "OFFSET") {
		t.Errorf("after cursor must not have OFFSET: %s", selectQ)
	}
	if selectArgs[0] != cursor {
		t.Errorf("first arg must be cursor UUID, got %v", selectArgs[0])
	}
}

func TestBuildQuery_BeforeCursor_GeneratesCorrectSQL(t *testing.T) {
	const cursor = "01234567-89ab-cdef-0123-456789abcdef"
	params := url.Values{"before": {cursor}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()

	if !strings.Contains(selectQ, "id < $1") {
		t.Errorf("before cursor must produce 'id < $1': %s", selectQ)
	}
	if !strings.Contains(selectQ, "ORDER BY id DESC") {
		t.Errorf("before cursor must use ORDER BY id DESC: %s", selectQ)
	}
	if strings.Contains(selectQ, "OFFSET") {
		t.Errorf("before cursor must not have OFFSET: %s", selectQ)
	}
}

func TestBuildQuery_InvalidCursorRejected(t *testing.T) {
	bad := []string{
		"not-a-uuid",
		"XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX", // uppercase
		"01234567-89ab-cdef-0123-456789abcde",   // too short
		"",
	}
	for _, v := range bad {
		if v == "" {
			continue // empty string is just ignored by the param parser
		}
		mustError(t, testResource(), url.Values{"after": {v}})
		mustError(t, testResource(), url.Values{"before": {v}})
	}
}

func TestBuildQuery_AfterTakesPrecedenceOverBefore(t *testing.T) {
	const after  = "aaaaaaaa-0000-0000-0000-000000000000"
	const before = "bbbbbbbb-0000-0000-0000-000000000000"
	params := url.Values{"after": {after}, "before": {before}}
	qb := mustBuild(t, testResource(), params, nil)
	selectQ, _, _, _ := qb.SQL()

	if !strings.Contains(selectQ, "id > $1") {
		t.Errorf("after must take precedence: %s", selectQ)
	}
	if strings.Contains(selectQ, "id < $") {
		t.Errorf("before must be ignored when after is present: %s", selectQ)
	}
}
