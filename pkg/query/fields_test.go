package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// MOTOR-FIELDS-S1 — `?fields=`: the select-list projection. The thesis these
// tests pin is that the projection reaches the SQL (`SELECT "id", "nit"`),
// never a post-read trim: a column the SELECT does not name is not read, and
// a large TOASTed value is not detoasted for a list that does not show it.

func fieldsResource() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"nit":    {Type: "string"},
			"anio":   {Type: "int"},
			"estado": {Type: "string"},
			"data":   {Type: "json"},
		},
	}
}

func TestFields_AbsentKeepsSelectStar(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{}, nil)
	sel, _, _, _ := qb.SQL()
	if !strings.HasPrefix(sel, `SELECT * FROM "orders"`) {
		t.Fatalf("no projection must keep SELECT * byte for byte, got %s", sel)
	}
	if qb.Fields() != nil {
		t.Fatalf("Fields() must be nil without ?fields=, got %v", qb.Fields())
	}
}

func TestFields_ProjectsTheSelectListWithIDFirst(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{"fields": {"nit,estado"}}, nil)
	sel, cnt, _, _ := qb.SQL()
	if !strings.HasPrefix(sel, `SELECT "id", "nit", "estado" FROM "orders"`) {
		t.Fatalf("projection must be pushed into the SELECT list, id first, request order kept: %s", sel)
	}
	if strings.Contains(sel, "data") {
		t.Fatalf("an unrequested column must not appear in the SQL: %s", sel)
	}
	if !strings.HasPrefix(cnt, "SELECT COUNT(*)") {
		t.Fatalf("the COUNT statement is unaffected: %s", cnt)
	}
}

func TestFields_IDAlwaysReturnedEvenWhenNotAsked(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{"fields": {"anio"}}, nil)
	if got := qb.Fields(); len(got) != 2 || got[0] != "id" || got[1] != "anio" {
		t.Fatalf("id is always first even when not asked: %v", got)
	}
	// asking for id explicitly, in any position, is the same set
	qb = mustBuild(t, fieldsResource(), url.Values{"fields": {"anio,id"}}, nil)
	if got := qb.Fields(); len(got) != 2 || got[0] != "id" || got[1] != "anio" {
		t.Fatalf("an explicit id does not duplicate or reorder: %v", got)
	}
}

func TestFields_DuplicatesCollapse(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{"fields": {"nit, nit ,anio"}}, nil)
	if got := strings.Join(qb.Fields(), ","); got != "id,nit,anio" {
		t.Fatalf("a repeated NAME is a set (whitespace trimmed): %s", got)
	}
}

func TestFields_UnknownFieldIsNamedWithTheAvailableSet(t *testing.T) {
	msg := mustError(t, fieldsResource(), url.Values{"fields": {"nit,datax"}})
	if !strings.Contains(msg, "unknown field in fields: datax") || !strings.Contains(msg, "available: anio, data, estado, id, nit") {
		t.Fatalf("an unknown name is named and the alternatives listed, never silently dropped: %s", msg)
	}
}

func TestFields_EmptyValueIsRejected(t *testing.T) {
	msg := mustError(t, fieldsResource(), url.Values{"fields": {""}})
	if !strings.Contains(msg, "fields parameter has an empty value") {
		t.Fatalf("?fields= (an empty form field) must not silently mean everything: %s", msg)
	}
}

func TestFields_EmptyEntryNamesTheExtraComma(t *testing.T) {
	for _, v := range []string{"nit,,anio", "nit,", ",nit"} {
		msg := mustError(t, fieldsResource(), url.Values{"fields": {v}})
		if !strings.Contains(msg, "empty entry in the field list") {
			t.Fatalf("%q: expected the extra-comma error, got %s", v, msg)
		}
	}
}

func TestFields_RepeatedParameterIsRejected(t *testing.T) {
	msg := mustError(t, fieldsResource(), url.Values{"fields": {"nit", "anio"}})
	if !strings.Contains(msg, `parameter "fields" was sent 2 times`) {
		t.Fatalf("ENG-17 applies to fields too: %s", msg)
	}
}

func TestFields_HiddenFieldIsOmittedByTheAllowlist(t *testing.T) {
	// The role's allowlist wins, as on every read: the hidden name is dropped
	// from the projection (not a 403 — that is the VALUE-oracle defense of
	// filter/sort, and a projection reveals nothing; a 403 would break every
	// role-agnostic client, the /app first).
	qb, err := BuildQuery("orders", fieldsResource(), url.Values{"fields": {"nit,data,anio"}}, nil, []string{"id", "nit", "anio"})
	if err != nil {
		t.Fatalf("hidden field must not error: %v", err)
	}
	if got := strings.Join(qb.Fields(), ","); got != "id,nit,anio" {
		t.Fatalf("hidden field dropped, the rest kept in order: %s", got)
	}
	sel, _, _, _ := qb.SQL()
	if strings.Contains(sel, "data") {
		t.Fatalf("the hidden column must not be read either: %s", sel)
	}
	// only hidden names → id alone (never an empty select list)
	qb, _ = BuildQuery("orders", fieldsResource(), url.Values{"fields": {"data"}}, nil, []string{"id", "nit"})
	if got := strings.Join(qb.Fields(), ","); got != "id" {
		t.Fatalf("got %s", got)
	}
	// an UNKNOWN name is still a named error, allowlist or not
	if _, err := BuildQuery("orders", fieldsResource(), url.Values{"fields": {"ghost"}}, nil, []string{"id", "nit"}); err == nil || !strings.Contains(err.Error(), "unknown field in fields: ghost") {
		t.Fatalf("unknown stays a 400: %v", err)
	}
}

func TestFields_ComposesWithCursorAndSort(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{"fields": {"nit"}, "after": {"11111111-1111-1111-1111-111111111111"}}, nil)
	sel, _, _, _ := qb.SQL()
	if !strings.HasPrefix(sel, `SELECT "id", "nit" FROM "orders" WHERE id > $1 ORDER BY id ASC LIMIT $2`) {
		t.Fatalf("keyset path projected: %s", sel)
	}
	qb = mustBuild(t, fieldsResource(), url.Values{"fields": {"nit"}, "sort": {"anio"}, "order": {"desc"}}, nil)
	sel, _, _, _ = qb.SQL()
	if !strings.Contains(sel, `SELECT "id", "nit" FROM "orders" ORDER BY anio DESC`) {
		t.Fatalf("sorting by a column outside the projection is legal SQL: %s", sel)
	}
}

func TestFields_SQLProjectedIsTheIncludeBase(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{"fields": {"nit"}, "sort": {"anio"}}, nil)
	base, args := qb.SQLProjected([]string{"id", "nit", "anio"})
	if !strings.HasPrefix(base, `SELECT "id", "nit", "anio" FROM "orders" ORDER BY anio ASC LIMIT $1 OFFSET $2`) || len(args) != 2 {
		t.Fatalf("the embed base carries the requested fields plus what the wrapper needs: %s", base)
	}
	if b, _ := qb.SQLProjected(nil); !strings.HasPrefix(b, "SELECT * FROM") {
		t.Fatalf("nil = the historical statement: %s", b)
	}
	sel, _, _, _ := qb.SQL()
	if !strings.HasPrefix(sel, `SELECT "id", "nit" FROM`) {
		t.Fatalf("SQLProjected must not change the plain statement: %s", sel)
	}
}

func TestFields_SelectOnlyValidatesAndSelectListQuotes(t *testing.T) {
	qb := mustBuild(t, fieldsResource(), url.Values{}, nil)
	if err := qb.SelectOnly([]string{"estado", "nit", "estado"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(qb.Fields(), ","); got != "id,estado,nit" {
		t.Fatalf("got %s", got)
	}
	if err := qb.SelectOnly([]string{"ghost"}); err == nil || !strings.Contains(err.Error(), "unknown field in projection: ghost") {
		t.Fatalf("an unvalidated identifier must never reach the SQL: %v", err)
	}
	if err := qb.SelectOnly(nil); err != nil || qb.Fields() != nil {
		t.Fatalf("nil restores SELECT *: %v %v", err, qb.Fields())
	}
	if SelectList(nil) != "*" || SelectList([]string{"id", "a"}) != `"id", "a"` {
		t.Fatalf("SelectList: %q %q", SelectList(nil), SelectList([]string{"id", "a"}))
	}
	if SelectListAliased("r", nil) != "r.*" || SelectListAliased("r", []string{"id", "a"}) != `r."id", r."a"` {
		t.Fatalf("SelectListAliased: %q", SelectListAliased("r", []string{"id", "a"}))
	}
}

func TestFields_IncludeRootIsProjectedEmbedsAreWhole(t *testing.T) {
	s := &schema.APISchema{Resources: map[string]schema.ResourceSchema{
		"orders": {
			Fields:    map[string]schema.FieldDef{"status": {Type: "string"}, "blob": {Type: "json"}, "customer_id": {Type: "uuid"}},
			Relations: map[string]schema.RelationDef{"customer": {Type: "belongs_to", Target: "customers", FK: "customer_id"}},
		},
		"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}, "notes": {Type: "text"}}},
	}}
	allowAll := func(string) (bool, []string, *rbac.WhereCondition) { return true, nil, nil }
	var asked [][]string
	base := func(cols []string) (string, []any) {
		asked = append(asked, cols)
		return "SELECT " + SelectList(cols) + ` FROM "orders" ORDER BY status LIMIT $1 OFFSET $2`, []any{20, 0}
	}
	// the caller asked for status only; the embed joins on customer_id and
	// the wrapper orders by status → the base subquery carries id,status,customer_id
	sql, _, ierr := BuildListIncludeFields("orders", "customer", base, []string{"id", "status"}, "status", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatal(ierr.Msg)
	}
	if strings.Contains(sql, `'blob', _base."blob"`) || strings.Contains(sql, `'customer_id', _base."customer_id"`) {
		t.Fatalf("the root row object names only the requested fields: %s", sql)
	}
	if !strings.Contains(sql, `'status', _base."status"`) || !strings.Contains(sql, `'notes',`) {
		t.Fatalf("root keeps the projected columns and the embed stays whole: %s", sql)
	}
	if !strings.Contains(sql, `FROM (SELECT "id", "status", "customer_id" FROM "orders"`) {
		t.Fatalf("the base subquery is projected to fields ∪ join/order columns — never SELECT *: %s", sql)
	}
	if len(asked) != 2 || asked[0] != nil || strings.Join(asked[1], ",") != "id,status,customer_id" {
		t.Fatalf("base asked with nil (args) then the column list: %v", asked)
	}
	// the unprojected wrapper is byte-identical to BuildListInclude
	a, _, _ := BuildListInclude("orders", "customer", `SELECT * FROM "orders"`, nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	b, _, _ := BuildListIncludeFields("orders", "customer", func([]string) (string, []any) { return `SELECT * FROM "orders"`, nil }, nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if a != b || !strings.Contains(a, `'blob', _base."blob"::json`) || !strings.Contains(a, `FROM (SELECT * FROM "orders") _base`) {
		t.Fatalf("nil projection = the historical statement: %s", a)
	}
	// get: the belongs_to FK joins the base, so it is in the projected base
	g, _, gerr := BuildGetIncludeFields("orders", "customer", func(cols []string) (string, []any) {
		return "SELECT " + SelectList(cols) + ` FROM "orders" WHERE id = $1`, []any{"x"}
	}, []string{"id", "status"}, s, schema.DefaultMaxIncludeDepth, allowAll)
	if gerr != nil || !strings.Contains(g, `FROM (SELECT "id", "status", "customer_id" FROM "orders" WHERE id = $1) _base`) {
		t.Fatalf("get base projected: %v %s", gerr, g)
	}
}
