//go:build go1.18

package query

import (
	"net/url"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// FuzzBuildQuery feeds attacker-controlled query parameters (the field name, the
// operator, and the value all come from the request URL) through BuildQuery and
// SQL(). It must never panic and must never error out of bounds. SQL-injection
// safety is enforced structurally (fields are schema-validated, values are bound
// parameters); this fuzz guards the parsing/validation path against crashes.
func FuzzBuildQuery(f *testing.F) {
	type seed struct{ field, op, value string }
	for _, s := range []seed{
		{"status", "eq", "pending"},
		{"'; DROP TABLE guides; --", "eq", "x"},
		{"status", "'; DROP", "x"},
		{"code", "partial", `%_\`},
		{"weight", "gte", "-9223372036854775809"},
		{"__proto__", "eq", "injected"},
		{"status", "eq", ""},
	} {
		f.Add(s.field, s.op, s.value)
	}

	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"status":     {Type: "string"},
		"code":       {Type: "string"},
		"weight":     {Type: "float64"},
		"created_at": {Type: "time"},
	}}

	f.Fuzz(func(t *testing.T, field, op, value string) {
		params := url.Values{}
		params.Set("filter["+field+"]["+op+"]", value)
		params.Set("filter["+field+"]", value)
		params.Set("search", value)
		params.Set("sort", field)
		params.Set("order["+field+"]", value)
		params.Set("page", value)
		params.Set("per_page", value)
		params.Set("after", value)
		params.Set("before", value)

		qb, err := BuildQuery("guides", res, params, nil, nil)
		if err != nil || qb == nil {
			return
		}
		_, _, _, _ = qb.SQL() // must never panic
	})
}
