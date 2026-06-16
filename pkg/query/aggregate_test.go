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
