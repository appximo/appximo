package query

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
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

// splitCSVStrict splits a CSV field list for a parameter the caller is ACTIVELY using: an
// empty ENTRY inside the list (`sum=a,` — a trailing comma from a string-join
// bug, `group_by=x,,y`) is an error naming the parameter, instead of being
// dropped without a word (ENG-24). The WHOLLY empty value (`sum=`) is not this
// function's case — the caller may not be using the parameter at all, and that
// tolerance is decided (with its reason) where emptyFuncs is handled.
func splitCSVStrict(param, s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p == "" {
			return nil, fmt.Errorf("aggregate %s: empty entry in the field list %q — remove the extra comma", param, s)
		} else {
			out = append(out, p)
		}
	}
	return out, nil
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
// aggUnsupportedListParams are list-endpoint parameters BuildQuery would parse
// and validate but an aggregate can NEVER honor — an aggregate covers the whole
// filtered set, so it has no page, no order and no cursor. They used to be
// fully validated (against a schema the endpoint would not use) and then thrown
// away, producing the confusing pair `?count&sort=ghost` → 400 while
// `?count&sort=status` was accepted-and-ignored (ENG-24). They are rejected by
// NAME now, before BuildQuery ever sees them, with a message that says why.
var aggUnsupportedListParams = []string{"page", "per_page", "sort", "order", "after", "before", "include"}

// aggOwnedParams is the aggregate endpoint's own vocabulary.
var aggOwnedParams = map[string]bool{
	"count": true, "sum": true, "avg": true, "min": true, "max": true,
	"group_by": true, "search": true,
}

func BuildAggregate(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
	allowedFields []string,
) (*AggregateQuery, error) {
	// The aggregate endpoint OWNS its query-parameter namespace (ENG-18/ENG-24;
	// the written exception-to-the-exception in ADR-024): here the parameter
	// NAME is the enumerated value — the function requested — so an
	// unrecognized name is a typo'd function (`?median=x`, `?summ=x`), not a
	// cache-buster, and it used to produce a 200 with the metric silently
	// missing: a dashboard reading a total that is not the one it asked for.
	// The general top-level tolerance exists for decorated LINKS (utm tags,
	// cache-busters browsers and mail clients append); an authenticated
	// aggregate XHR is never a decorated link, so the tolerance buys nothing
	// here and hides typos.
	for key := range params {
		if aggOwnedParams[key] || strings.HasPrefix(key, filterParamPrefix) {
			continue
		}
		if isAggUnsupportedListParam(key) {
			return nil, fmt.Errorf("%s is not supported on the aggregate endpoint: an aggregate covers the whole filtered set — it has no page, order or cursor (valid parameters: count, sum, avg, min, max, group_by, filter[...], search)", key)
		}
		return nil, fmt.Errorf("unknown aggregate parameter %q (valid: count, sum, avg, min, max, group_by, filter[...], search)", key)
	}

	qb, err := BuildQuery(resource, res, params, condition)
	if err != nil {
		return nil, err
	}
	aq := &AggregateQuery{qb: qb}

	aq.count, err = ParseCountFlag(params)
	if err != nil {
		return nil, err
	}

	var emptyFuncs []string
	for _, fn := range aggFuncs {
		// A function present with a WHOLLY EMPTY value (`?sum=`) is recorded and
		// reported only if the request ends up with NOTHING to aggregate.
		//
		// The first version rejected it outright, and an adversarial review was
		// right to push back: `?count&sum=` was a working request that returned a
		// count, and a form-built client that always emits every function key
		// would have started getting a hard 400 for a parameter it never intended
		// to use. The distinction that matters for ADR-024 is VISIBILITY — a
		// dropped filter is invisible (rows come back and the caller cannot tell
		// they are unfiltered), whereas a dropped aggregate function is plain in
		// the response, which simply has no `sum` key. Silence is only the defect
		// when the caller cannot see it. (An empty ENTRY in a non-empty list —
		// `sum=a,` — is different: the caller IS using the parameter, and
		// splitCSVStrict rejects it by name.)
		val, present, verr := singleValue(params, fn)
		if verr != nil {
			return nil, verr
		}
		fields, cerr := splitCSVStrict(fn, val)
		if cerr != nil {
			return nil, cerr
		}
		if present && len(fields) == 0 {
			emptyFuncs = append(emptyFuncs, fn)
		}
		for _, f := range fields {
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

	// group_by is NOT in the emptyFuncs tolerance: an empty `?group_by=` used to
	// silently change the response SHAPE — the caller asked for {"groups":[…]}
	// and got the flat object — which is not a visibly-missing key, it is a
	// different contract served without a word (ENG-18).
	gbVal, gbPresent, err := singleValue(params, "group_by")
	if err != nil {
		return nil, err
	}
	gbFields, err := splitCSVStrict("group_by", gbVal)
	if err != nil {
		return nil, err
	}
	if gbPresent && len(gbFields) == 0 {
		return nil, fmt.Errorf("group_by has an empty value (use group_by=<field>[,<field>…]) — an empty group_by would silently change the response shape")
	}
	for _, f := range gbFields {
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
	// (`?summ=x`) and an empty value — and it named none of them (ADR-024).
	// Unknown names are rejected up front now (the namespace scan above), so
	// what reaches here is a request whose recognized parameters name nothing
	// to aggregate; each remaining case is distinguished.
	if !aq.count && len(aq.metrics) == 0 {
		if len(emptyFuncs) > 0 {
			return nil, fmt.Errorf("aggregate %s: no field given (use %s=<field>[,<field>…])",
				strings.Join(emptyFuncs, ", "), emptyFuncs[0])
		}
		if len(aq.groupBy) > 0 {
			return nil, fmt.Errorf("group_by needs an aggregate function: add count, or one of sum, avg, min, max")
		}
		return nil, fmt.Errorf("aggregate requires at least one of: count, sum, avg, min, max (received: %s)",
			receivedParamNames(params))
	}
	return aq, nil
}

// isAggUnsupportedListParam reports whether key is a LIST parameter the
// aggregate endpoint recognizes but cannot honor (page/order/cursor family,
// including the order[field] form).
func isAggUnsupportedListParam(key string) bool {
	if strings.HasPrefix(key, orderParamPrefix) {
		return true
	}
	for _, p := range aggUnsupportedListParams {
		if key == p {
			return true
		}
	}
	return false
}

// maxEchoedParams / maxEchoedParamLen bound what receivedParamNames reflects.
// A query string is caller-controlled and can be very long, so echoing it back
// unbounded is a response amplifier — the same defect that was fixed in the
// tenant middleware's 400, and it would have been inconsistent to guard one and
// not the other. A typo is visible in the first handful of names.
const (
	maxEchoedParams   = 12
	maxEchoedParamLen = 40
)

// receivedParamNames lists the query parameter names a request carried, sorted,
// for an error message. It echoes only the caller's own parameter NAMES (never
// values), so a typo like `summ` becomes visible next to the valid set — bounded
// in both count and length.
func receivedParamNames(params url.Values) string {
	if len(params) == 0 {
		return "no parameters"
	}
	names := make([]string, 0, len(params))
	for k := range params {
		if r := []rune(k); len(r) > maxEchoedParamLen {
			k = string(r[:maxEchoedParamLen]) + "…"
		}
		names = append(names, k)
	}
	sort.Strings(names)
	if len(names) > maxEchoedParams {
		return strings.Join(names[:maxEchoedParams], ", ") +
			fmt.Sprintf(", … (%d more)", len(names)-maxEchoedParams)
	}
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
