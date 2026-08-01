package query

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

// ErrAggForbiddenField signals that an aggregate referenced a field the role may
// not read (its RBAC field allowlist excludes it). Callers map this to 403; any
// other BuildAggregate error is a 400. Aggregating a hidden field would otherwise
// leak its values, so it is forbidden — the no-leak-via-aggregate guarantee (G3).
var ErrAggForbiddenField = errors.New("aggregate references a field not permitted for this role")

// aggFuncs is the closed allowlist of aggregation functions — the request never
// supplies SQL, only one of these names, so there is no injection surface.
var aggFuncs = []string{"sum", "avg", "min", "max"}

// AggMetric is one requested function over one field, with its SQL column alias.
type AggMetric struct {
	Fn    string // sum | avg | min | max
	Field string
	Alias string // SQL alias, e.g. "agg_sum_salario"
}

// AggregateQuery is a validated aggregation over a resource, scoped by the SAME
// filters + RBAC row condition as a list read of that resource.
type AggregateQuery struct {
	qb      *QueryBuilder
	count   bool
	metrics []AggMetric
	groupBy []string
}

// aggFieldTypeOK reports whether fn may apply to a field of fieldType.
func aggFieldTypeOK(fn, fieldType string) bool {
	switch fn {
	case "sum", "avg":
		return fieldType == "int" || fieldType == "int64" || fieldType == "float64"
	case "min", "max":
		return fieldType == "int" || fieldType == "int64" || fieldType == "float64" || fieldType == "time"
	}
	return false
}

// groupByTypeOK reports whether a field of fieldType can be a GROUP BY key.
func groupByTypeOK(fieldType string) bool {
	// json is stored as TEXT and jsonb is a document — grouping by either is
	// meaningless (and jsonb has no default btree sort class in every case);
	// everything else (string/text/int/int64/float64/bool/time/uuid) groups fine.
	return fieldType != "json" && fieldType != "jsonb"
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// fieldAllowed reports whether field is readable under allowlist (empty = all).
func fieldAllowed(field string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, a := range allowlist {
		if a == field {
			return true
		}
	}
	return false
}

// BuildAggregate parses + validates an aggregation request. It reuses BuildQuery
// for the filters + RBAC row condition (so the aggregate is scoped EXACTLY like a
// list read — same WHERE, same tenant), then validates the requested functions
// and fields against the schema, the fixed function allowlist (no arbitrary SQL),
// and the role's field allowlist (a field the role may not read cannot be
// aggregated → ErrAggForbiddenField). COUNT(*) needs no field and is always
// allowed (it counts only the rows the row condition already scopes to).
//
// Syntax (URL params): count (presence) · sum/avg/min/max=<comma-separated
// fields> · group_by=<comma-separated fields> · filter[...] (same as list).
func BuildAggregate(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
	allowedFields []string,
) (*AggregateQuery, error) {
	qb, err := BuildQuery(resource, res, params, condition)
	if err != nil {
		return nil, err
	}
	aq := &AggregateQuery{qb: qb}

	if _, ok := params["count"]; ok {
		aq.count = true
	}

	for _, fn := range aggFuncs {
		// A function present with an EMPTY value (`?count&sum=`) used to be
		// dropped without a word, and the caller got either a silent absence or
		// the generic catch-all below — neither of which mentions `sum`
		// (ADR-024).
		if _, present := params[fn]; present && len(splitCSV(params.Get(fn))) == 0 {
			return nil, fmt.Errorf("aggregate %s: no field given (use %s=<field>[,<field>…])", fn, fn)
		}
		for _, f := range splitCSV(params.Get(fn)) {
			fd, ok := res.Fields[f]
			if !ok {
				return nil, fmt.Errorf("aggregate %s: unknown field %q", fn, f)
			}
			if !aggFieldTypeOK(fn, fd.Type) {
				return nil, fmt.Errorf("aggregate %s not allowed on field %q (type %q)", fn, f, fd.Type)
			}
			if !fieldAllowed(f, allowedFields) {
				return nil, fmt.Errorf("%w: %s(%s)", ErrAggForbiddenField, fn, f)
			}
			aq.metrics = append(aq.metrics, AggMetric{Fn: fn, Field: f, Alias: "agg_" + fn + "_" + f})
		}
	}

	for _, f := range splitCSV(params.Get("group_by")) {
		fd, ok := res.Fields[f]
		if !ok {
			return nil, fmt.Errorf("group_by: unknown field %q", f)
		}
		if !groupByTypeOK(fd.Type) {
			return nil, fmt.Errorf("group_by: field %q of type %q is not groupable", f, fd.Type)
		}
		if !fieldAllowed(f, allowedFields) {
			return nil, fmt.Errorf("%w: group_by %s", ErrAggForbiddenField, f)
		}
		aq.groupBy = append(aq.groupBy, f)
	}

	// One message used to cover four different mistakes — a legitimate request
	// missing a function, an unknown function (`?median=x`), a typo'd one
	// (`?summ=x`) and an empty value — and it named none of them, so the caller
	// could not tell which they had made (ADR-024). The empty-value and
	// group_by-alone cases are now distinguished, and the generic case echoes
	// what the request actually carried, which is what makes a typo visible
	// without rejecting the unknown top-level parameters the policy
	// deliberately tolerates.
	if !aq.count && len(aq.metrics) == 0 {
		if len(aq.groupBy) > 0 {
			return nil, fmt.Errorf("group_by needs an aggregate function: add count, or one of sum, avg, min, max")
		}
		return nil, fmt.Errorf("aggregate requires at least one of: count, sum, avg, min, max (received: %s)",
			receivedParamNames(params))
	}
	return aq, nil
}

// receivedParamNames lists the query parameter names a request carried, sorted,
// for an error message. It echoes only the caller's own parameter NAMES (never
// values), so a typo like `summ` becomes visible next to the valid set.
func receivedParamNames(params url.Values) string {
	if len(params) == 0 {
		return "no parameters"
	}
	names := make([]string, 0, len(params))
	for k := range params {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// HasCount reports whether COUNT(*) was requested.
func (aq *AggregateQuery) HasCount() bool { return aq.count }

// Metrics returns the requested sum/avg/min/max metrics (with their SQL aliases).
func (aq *AggregateQuery) Metrics() []AggMetric { return aq.metrics }

// GroupBy returns the group-by field names (empty = a single overall aggregate).
func (aq *AggregateQuery) GroupBy() []string { return aq.groupBy }

// CountAlias is the SQL column alias COUNT(*) is returned under.
const CountAlias = "agg_count"

// SQL emits the aggregate SELECT, scoped by the same WHERE (filters + RBAC row
// condition + search) the list read uses, minus cursor/pagination. All
// identifiers are quoted schema-validated names (injection-inert) and every
// function comes from the fixed allowlist. With group_by, rows are GROUP BY'd and
// ORDER BY'd on the group columns for deterministic output.
func (aq *AggregateQuery) SQL() (sql string, args []any) {
	where, args := aq.qb.aggregateWhere()
	var sel, groupCols []string
	for _, g := range aq.groupBy {
		gq := quoteIdent(g)
		sel = append(sel, gq)
		groupCols = append(groupCols, gq)
	}
	if aq.count {
		sel = append(sel, "COUNT(*) AS "+CountAlias)
	}
	for _, m := range aq.metrics {
		sel = append(sel, fmt.Sprintf("%s(%s) AS %s", strings.ToUpper(m.Fn), quoteIdent(m.Field), quoteIdent(m.Alias)))
	}
	sql = fmt.Sprintf("SELECT %s FROM %s%s", strings.Join(sel, ", "), quoteIdent(aq.qb.resource), where)
	if len(groupCols) > 0 {
		gb := strings.Join(groupCols, ", ")
		sql += " GROUP BY " + gb + " ORDER BY " + gb
	}
	return sql, args
}
