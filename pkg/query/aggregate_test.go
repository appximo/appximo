package query

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

func aggRes() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"estado":  {Type: "string"},
			"monto":   {Type: "float64"},
			"dias":    {Type: "int"},
			"creado":  {Type: "time"},
			"blob":    {Type: "json"},
			"user_id": {Type: "uuid"},
		},
	}
}

func mustAgg(t *testing.T, q string, allow []string, cond *rbac.WhereCondition) *AggregateQuery {
	t.Helper()
	vals, _ := url.ParseQuery(q)
	aq, err := BuildAggregate("pagos", aggRes(), vals, cond, allow)
	if err != nil {
		t.Fatalf("BuildAggregate(%q): %v", q, err)
	}
	return aq
}

func TestAggregate_CountAndMetricsSQL(t *testing.T) {
	aq := mustAgg(t, "count&sum=monto&avg=monto&min=dias&max=creado", nil, nil)
	sql, args := aq.SQL()
	for _, want := range []string{
		`COUNT(*) AS agg_count`,
		`SUM("monto") AS "agg_sum_monto"`,
		`AVG("monto") AS "agg_avg_monto"`,
		`MIN("dias") AS "agg_min_dias"`,
		`MAX("creado") AS "agg_max_creado"`,
		`FROM "pagos"`,
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL missing %q\n got: %s", want, sql)
		}
	}
	if len(args) != 0 {
		t.Errorf("no filters → no args, got %v", args)
	}
	if strings.Contains(sql, "GROUP BY") {
		t.Errorf("no group_by → no GROUP BY: %s", sql)
	}
}

func TestAggregate_GroupByFilterAndCondition(t *testing.T) {
	cond := &rbac.WhereCondition{Field: "user_id", Op: "eq", Value: "u-1"}
	aq := mustAgg(t, "count&sum=monto&group_by=estado&filter[dias][gte]=3", nil, cond)
	sql, args := aq.SQL()
	if !strings.Contains(sql, "GROUP BY \"estado\" ORDER BY \"estado\"") {
		t.Errorf("expected GROUP BY+ORDER BY estado: %s", sql)
	}
	// RBAC condition + filter both in WHERE, both parameterized.
	if !strings.Contains(sql, "user_id = $1") || !strings.Contains(sql, "dias >= $2") {
		t.Errorf("expected RBAC condition + filter in WHERE: %s", sql)
	}
	if len(args) != 2 || args[0] != "u-1" || args[1] != "3" {
		t.Errorf("args want [u-1 3], got %v", args)
	}
}

func TestAggregate_RejectsBadFnOnType(t *testing.T) {
	vals, _ := url.ParseQuery("sum=estado") // sum on a string
	if _, err := BuildAggregate("pagos", aggRes(), vals, nil, nil); err == nil {
		t.Fatal("expected error summing a string field")
	}
}

func TestAggregate_RejectsUnknownField(t *testing.T) {
	vals, _ := url.ParseQuery("sum=nope")
	if _, err := BuildAggregate("pagos", aggRes(), vals, nil, nil); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestAggregate_RejectsGroupByJSON(t *testing.T) {
	vals, _ := url.ParseQuery("count&group_by=blob")
	if _, err := BuildAggregate("pagos", aggRes(), vals, nil, nil); err == nil {
		t.Fatal("expected error grouping by a json field")
	}
}

func TestAggregate_RequiresAtLeastOne(t *testing.T) {
	vals, _ := url.ParseQuery("group_by=estado") // no count, no metric
	if _, err := BuildAggregate("pagos", aggRes(), vals, nil, nil); err == nil {
		t.Fatal("expected error when no aggregate function requested")
	}
}

// The no-leak-via-aggregate guarantee: a field outside the role's allowlist
// cannot be summed/min/max'd or grouped — returns ErrAggForbiddenField (→ 403).
func TestAggregate_ForbiddenFieldViaAllowlist(t *testing.T) {
	allow := []string{"estado", "dias"} // monto NOT allowed
	vals, _ := url.ParseQuery("sum=monto")
	_, err := BuildAggregate("pagos", aggRes(), vals, nil, allow)
	if !errors.Is(err, ErrAggForbiddenField) {
		t.Fatalf("summing a non-allowlisted field must be ErrAggForbiddenField, got: %v", err)
	}
	// group_by of a hidden field is also forbidden.
	vals2, _ := url.ParseQuery("count&group_by=monto")
	if _, err := BuildAggregate("pagos", aggRes(), vals2, nil, allow); !errors.Is(err, ErrAggForbiddenField) {
		t.Fatalf("group_by a non-allowlisted field must be ErrAggForbiddenField, got: %v", err)
	}
	// COUNT(*) alone is fine under an allowlist (no field referenced).
	vals3, _ := url.ParseQuery("count")
	if _, err := BuildAggregate("pagos", aggRes(), vals3, nil, allow); err != nil {
		t.Fatalf("count(*) under an allowlist should be allowed, got: %v", err)
	}
	// An allowed field aggregates fine.
	vals4, _ := url.ParseQuery("sum=dias")
	if _, err := BuildAggregate("pagos", aggRes(), vals4, nil, allow); err != nil {
		t.Fatalf("summing an allowlisted field should work, got: %v", err)
	}
}

// ── NIGHT-SWEEP-S1: the aggregate endpoint owns its namespace ────────────────

// aggMustError builds and asserts a named error containing every want fragment.
func aggMustError(t *testing.T, q string, wants ...string) {
	t.Helper()
	vals, _ := url.ParseQuery(q)
	_, err := BuildAggregate("pagos", aggRes(), vals, nil, nil)
	if err == nil {
		t.Fatalf("BuildAggregate(%q): expected an error, got none", q)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("BuildAggregate(%q): error must contain %q, got: %s", q, w, err)
		}
	}
}

// TestAggregate_UnknownFunctionRejected — ENG-18: `?count&median=monto` used to
// return 200 with the metric simply absent (a dashboard reading a total that is
// not the one it asked for). The parameter NAME is the enumerated value here,
// so an unknown one is rejected listing the valid set.
func TestAggregate_UnknownFunctionRejected(t *testing.T) {
	aggMustError(t, "count&median=monto", "unknown aggregate parameter", "median", "count, sum, avg, min, max, group_by")
	aggMustError(t, "summ=monto", "unknown aggregate parameter", "summ")
	aggMustError(t, "count&utm_source=mail", "unknown aggregate parameter", "utm_source")
}

// TestAggregate_CountByValue — ENG-18/ENG-23: the flag was presence-only, so
// count=false and count=0 turned the total ON (and REST disagreed with
// GraphQL's real Boolean). Bare ?count stays the documented on-switch.
func TestAggregate_CountByValue(t *testing.T) {
	if aq := mustAgg(t, "count&sum=monto", nil, nil); !aq.HasCount() {
		t.Error("bare ?count must be ON")
	}
	if aq := mustAgg(t, "count=true&sum=monto", nil, nil); !aq.HasCount() {
		t.Error("count=true must be ON")
	}
	if aq := mustAgg(t, "count=false&sum=monto", nil, nil); aq.HasCount() {
		t.Error("count=false must be OFF (used to turn the total ON)")
	}
	if aq := mustAgg(t, "count=0&sum=monto", nil, nil); aq.HasCount() {
		t.Error("count=0 must be OFF")
	}
	aggMustError(t, "count=maybe&sum=monto", "invalid count value", "maybe")
	// count=false with nothing else recognized: an explicit request for nothing.
	aggMustError(t, "count=false", "at least one of")
}

// TestAggregate_ListParamsNamedUnsupported — ENG-24: page/sort/cursors were
// fully VALIDATED through BuildQuery and then thrown away, so `?count&sort=ghost`
// 400'd over a parameter the endpoint never honors while `?count&sort=estado`
// was accepted-and-ignored. Both now get the same named rejection.
func TestAggregate_ListParamsNamedUnsupported(t *testing.T) {
	for _, q := range []string{
		"count&sort=ghost", "count&sort=estado", "count&page=2", "count&per_page=5",
		"count&order=desc", "count&order[estado]=asc",
		"count&after=aaaaaaaa-0000-0000-0000-000000000000", "count&include=lineas",
	} {
		aggMustError(t, q, "not supported on the aggregate endpoint")
	}
}

// TestAggregate_EmptyGroupByAndCSVEntries — ENG-18/ENG-24: `?group_by=` used to
// silently change the response SHAPE; a trailing comma in an active list was
// dropped without a word. `?count&sum=` (wholly empty, caller may not be using
// it) keeps its written tolerance.
func TestAggregate_EmptyGroupByAndCSVEntries(t *testing.T) {
	aggMustError(t, "count&group_by=", "group_by has an empty value")
	aggMustError(t, "count&sum=monto,", "empty entry", "sum")
	aggMustError(t, "count&group_by=estado,,dias", "empty entry", "group_by")
	if aq := mustAgg(t, "count&sum=", nil, nil); !aq.HasCount() || len(aq.Metrics()) != 0 {
		t.Error("?count&sum= must keep working (the reviewed tolerance)")
	}
	aggMustError(t, "sum=", "no field given")
}

// TestAggregate_RepeatedParamsRejected — ENG-17 on the aggregate surface.
func TestAggregate_RepeatedParamsRejected(t *testing.T) {
	aggMustError(t, "sum=monto&sum=dias", "send it once")
	aggMustError(t, "count&count=true", "send it once")
	aggMustError(t, "count&group_by=estado&group_by=dias", "send it once")
}
