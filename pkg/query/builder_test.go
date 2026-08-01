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

	want := `SELECT * FROM "orders" ORDER BY id ASC LIMIT $1 OFFSET $2`
	if selectQ != want {
		t.Errorf("selectQ:\n  got  %q\n  want %q", selectQ, want)
	}
	if countQ != `SELECT COUNT(*) FROM "orders"` {
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

// ADR-024 / ENG-14: an unknown sort field is REJECTED, not silently dropped.
//
// This test previously asserted the opposite — that `?sort=password` fell back to
// `ORDER BY id ASC` — and that fallback was the defect: the caller asked for an
// order, got a different one, and the response was a 200 with no way to tell.
// The docs had to carry the warning "verify result order, don't trust the param".
// Rejecting is also the stronger answer for the original intent of this case
// (a caller probing for a column that is not in the schema).
func TestBuildQuery_SortUnknownFieldIsRejected(t *testing.T) {
	params := url.Values{"sort": {"password"}}
	_, err := BuildQuery("guides", testResource(), params, nil)
	if err == nil {
		t.Fatal("an unknown sort field must be an error, not a silent fallback")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("the error must name the offending field, got: %v", err)
	}
	if !strings.Contains(err.Error(), "available:") {
		t.Errorf("the error must list the valid alternatives (ADR-024), got: %v", err)
	}
}

// The direction is validated too: `?order=descending` used to sort ASCENDING and
// say nothing.
func TestBuildQuery_InvalidSortDirectionIsRejected(t *testing.T) {
	_, err := BuildQuery("guides", testResource(), url.Values{"sort": {"title"}, "order": {"descending"}}, nil)
	if err == nil {
		t.Fatal("an invalid sort direction must be an error, not a silent fallback to ASC")
	}
	if !strings.Contains(err.Error(), "asc") || !strings.Contains(err.Error(), "desc") {
		t.Errorf("the error must name the valid directions, got: %v", err)
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

// A non-positive page is REJECTED, not silently served as page 1.
//
// This assertion is INVERTED from the original (ADR-024). The engine has always
// answered `?page=abc` with "must be a positive integer" — and then accepted
// `?page=0` and `?page=-4` and served page 1. The error message stated the rule
// that the silent path broke, which is the strongest possible evidence that the
// silence was an oversight rather than a contract. A 0-indexed client is the
// case that matters: it sends page=0 and page=1 and is served page 1 twice,
// silently skipping nothing and duplicating everything, with no way to notice.
func TestBuildQuery_NonPositivePageIsRejected(t *testing.T) {
	for _, v := range []string{"0", "-1", "-4"} {
		_, err := BuildQuery("notes", testResource(), url.Values{"page": {v}}, nil)
		if err == nil {
			t.Errorf("page=%s was accepted; a non-positive page must be rejected", v)
			continue
		}
		if !strings.Contains(err.Error(), v) {
			t.Errorf("page=%s: error %q does not name the offending value", v, err)
		}
	}
	for _, v := range []string{"0", "-1"} {
		_, err := BuildQuery("notes", testResource(), url.Values{"per_page": {v}}, nil)
		if err == nil {
			t.Errorf("per_page=%s was accepted; a non-positive per_page must be rejected", v)
			continue
		}
		if !strings.Contains(err.Error(), v) {
			t.Errorf("per_page=%s: error %q does not name the offending value", v, err)
		}
	}
}

// The over-max CLAMP stays: "max 100" is documented and the response reports the
// effective value in meta, which is reported tolerance rather than silence.
func TestBuildQuery_OverMaxIsClampedNotRejected(t *testing.T) {
	qb, err := BuildQuery("notes", testResource(), url.Values{"per_page": {"5000"}}, nil)
	if err != nil {
		t.Fatalf("per_page over the cap must be clamped, not rejected: %v", err)
	}
	if qb.PerPage() != MaxPerPage {
		t.Errorf("per_page = %d, want the documented cap %d", qb.PerPage(), MaxPerPage)
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
		"01234567-89ab-cdef-0123-456789abcde",  // too short
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
	const after = "aaaaaaaa-0000-0000-0000-000000000000"
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

// ADR-024 — the guarantee for the input layer.
//
// This is the query-parameter equivalent of TestIntegration_DeclaredEqualsApplied:
// it does not enumerate every way input can be wrong, it pins the contract that
// being wrong must be AUDIBLE. Each case is an input the parser used to drop with
// no error, returning a 200 the caller could not distinguish from a real result.
func TestBuildQuery_UnrecognizedInputIsAlwaysRejected(t *testing.T) {
	cases := []struct {
		name   string
		params url.Values
		// mustName is a substring the error MUST contain: an error that does not
		// name the offending input is only half the fix.
		mustName string
	}{
		{"op with an underscore (ENG-14)", url.Values{"filter[title][is_null]": {"true"}}, "is_null"},
		{"op with an underscore, negated", url.Values{"filter[title][not_null]": {"true"}}, "not_null"},
		{"uppercase operator", url.Values{"filter[title][EQ]": {"x"}}, "EQ"},
		{"uppercase field", url.Values{"filter[Title][eq]": {"x"}}, "Title"},
		{"unknown operator", url.Values{"filter[title][in]": {"a,b"}}, "in"},
		{"unknown field", url.Values{"filter[ghost][eq]": {"x"}}, "ghost"},
		{"malformed filter, three levels", url.Values{"filter[a][b][c]": {"1"}}, "malformed"},
		{"unknown sort field", url.Values{"sort": {"ghost"}}, "ghost"},
		{"unknown order field", url.Values{"order[ghost]": {"desc"}}, "ghost"},
		{"invalid sort direction", url.Values{"sort": {"title"}, "order": {"descending"}}, "descending"},
		{"invalid order[] direction", url.Values{"order[title]": {"sideways"}}, "sideways"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildQuery("guides", testResource(), tc.params, nil)
			if err == nil {
				t.Fatalf("%v was ACCEPTED — unrecognized input must never be silently dropped (ADR-024)", tc.params)
			}
			if !strings.Contains(err.Error(), tc.mustName) {
				t.Errorf("the error does not name the offending input %q: %v", tc.mustName, err)
			}
		})
	}
}

// The other half of the contract: the fix must not have made VALID input fail.
func TestBuildQuery_ValidInputStillWorks(t *testing.T) {
	ok := []url.Values{
		{"filter[title][eq]": {"x"}},
		{"filter[title]": {"x"}}, // implicit eq
		{"filter[title][partial]": {"x"}},
		{"filter[title][start]": {"x"}},
		{"filter[precio][gte]": {"10"}},
		{"filter[created_at][after]": {"2020-01-01T00:00:00Z"}},
		{"sort": {"title"}, "order": {"desc"}},
		{"sort": {"title"}, "order": {"DESC"}}, // case-insensitive
		{"order[title]": {"asc"}},
		{"sort": {"id"}},               // the implicit primary key
		{"utm_source": {"newsletter"}}, // an unknown TOP-LEVEL param stays tolerated
	}
	for _, p := range ok {
		if _, err := BuildQuery("guides", testResource(), p, nil); err != nil {
			t.Errorf("valid input %v was rejected: %v", p, err)
		}
	}
}

// ADR-024 second axis: an EMPTY value on a typed column is not a value. It used
// to be bound verbatim, which Postgres answered with a data exception — an opaque
// 400 on an int column and, before db.IsBadInput was widened, a 500 on a time
// column. The check runs before any SQL is built, so the error names the field
// and its declared type without consulting a Postgres error at all (the "Postgres
// errors are masked" property holds by construction).
func TestBuildQuery_EmptyValueOnTypedFieldIsRejected(t *testing.T) {
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"title":  {Type: "string"},
		"body":   {Type: "text"},
		"amount": {Type: "int64"},
		"due":    {Type: "time"},
		"done":   {Type: "bool"},
	}}
	for _, f := range []string{"amount", "due", "done"} {
		_, err := BuildQuery("notes", res, url.Values{"filter[" + f + "][eq]": {""}}, nil)
		if err == nil {
			t.Errorf("filter[%s][eq]= was accepted; an empty value is not valid for that type", f)
			continue
		}
		if !strings.Contains(err.Error(), f) {
			t.Errorf("filter[%s]: error %q does not name the field", f, err)
		}
	}
	// An empty value on a TEXT column is legitimate — it asks for the empty
	// string — and must keep working.
	for _, f := range []string{"title", "body"} {
		if _, err := BuildQuery("notes", res, url.Values{"filter[" + f + "][eq]": {""}}, nil); err != nil {
			t.Errorf("filter[%s][eq]= must remain valid on a text column: %v", f, err)
		}
	}
}

// The message must not contradict itself. availableFieldNames used to be shared
// between the filter and sort errors, so rejecting `filter[id]` produced
// "unknown filter field: id (available: …, id, …)" — naming id as available in
// the very sentence that rejected it. A caller who believes an error message
// retries the same request.
func TestBuildQuery_FilterErrorDoesNotClaimIDIsAvailable(t *testing.T) {
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{"title": {Type: "string"}}}
	_, err := BuildQuery("notes", res, url.Values{"filter[id][eq]": {"x"}}, nil)
	if err == nil {
		t.Fatal("filter[id] is not supported today and must be rejected (see backlog ENG-26)")
	}
	if strings.Contains(err.Error(), "available: ") && strings.Contains(err.Error(), "id") {
		// "id" may not appear in the available list of a FILTER error.
		after := err.Error()[strings.Index(err.Error(), "available: "):]
		for _, n := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(after, "available: "), ")"), ", ") {
			if strings.TrimSpace(n) == "id" {
				t.Errorf("the filter error lists id as available while rejecting it: %q", err)
			}
		}
	}
	// Sorting DOES accept id, so its error must keep listing it.
	_, serr := BuildQuery("notes", res, url.Values{"sort": {"ghost"}}, nil)
	if serr == nil || !strings.Contains(serr.Error(), "id") {
		t.Errorf("the sort error must still list id, which sorting accepts: %v", serr)
	}
}
