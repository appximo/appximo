package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

func rangeRes() *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"titulo": {Type: "string"}, "inicio": {Type: "time"}, "fin": {Type: "time"}, "dueno_id": {Type: "uuid"},
		},
		Ranges: map[string]schema.RangeDef{"horario": {Start: "inicio", End: "fin"}},
	}
}

// TestBuildQuery_RangeFilters (MOTOR-AGENDA-S1): a declared range filters by
// NAME with overlaps (an ISO 8601 interval) and contains (an instant), both
// half-open and NULL-safe; anything else on the name is a named 400.
func TestBuildQuery_RangeFilters(t *testing.T) {
	res := rangeRes()
	q, err := BuildQuery("eventos", res, url.Values{"filter[horario][overlaps]": {"2026-09-22T16:00:00-05:00/2026-09-22T17:00:00-05:00"}}, nil, nil)
	if err != nil {
		t.Fatalf("overlaps: %v", err)
	}
	sqlText, _, args, _ := q.SQL()
	if !strings.Contains(sqlText, `tstzrange(inicio, fin, '[)') && tstzrange($1::timestamptz, $2::timestamptz, '[)')`) || !strings.Contains(sqlText, "inicio IS NOT NULL AND fin IS NOT NULL") {
		t.Fatalf("overlaps SQL: %s", sqlText)
	}
	if len(args) < 2 || args[0] != "2026-09-22T16:00:00-05:00" || args[1] != "2026-09-22T17:00:00-05:00" {
		t.Fatalf("overlaps args: %v", args)
	}
	q, err = BuildQuery("eventos", res, url.Values{"filter[horario][contains]": {"2026-09-22T16:30:00-05:00"}}, nil, nil)
	if err != nil {
		t.Fatalf("contains: %v", err)
	}
	sqlText, _, args, _ = q.SQL()
	if !strings.Contains(sqlText, `tstzrange(inicio, fin, '[)') @> $1::timestamptz`) || len(args) < 1 {
		t.Fatalf("contains SQL: %s %v", sqlText, args)
	}
	for param, want := range map[string]string{
		"filter[horario][eq]":       `operator "eq" not allowed for a time range`,
		"filter[horario][overlaps]": "is not an interval",
		"filter[horario][contains]": "empty value",
	} {
		v := "x"
		if strings.HasSuffix(param, "[contains]") {
			v = ""
		}
		_, err := BuildQuery("eventos", res, url.Values{param: {v}}, nil, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s=%q: want %q, got %v", param, v, want, err)
		}
	}
	if _, err := BuildQuery("eventos", res, url.Values{"filter[horario][overlaps]": {"2026-09-22T17:00:00Z/2026-09-22T16:00:00Z"}}, nil, nil); err == nil || !strings.Contains(err.Error(), "not after its start") {
		t.Errorf("reversed interval must be named: %v", err)
	}
	// RBAC: a role that may not read a bound may not filter by the range.
	if _, err := BuildQuery("eventos", res, url.Values{"filter[horario][contains]": {"2026-09-22T16:30:00Z"}}, nil, []string{"id", "titulo"}); err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Errorf("forbidden bound: %v", err)
	}
	// The unknown-field message lists the ranges.
	if _, err := BuildQuery("eventos", res, url.Values{"filter[ghost]": {"1"}}, nil, nil); err == nil || !strings.Contains(err.Error(), "ranges: horario (overlaps, contains)") {
		t.Errorf("available names must list ranges: %v", err)
	}
}
