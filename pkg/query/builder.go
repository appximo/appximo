package query

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
)

const (
	DefaultPage    = 1
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10_000
)

// filterParamRe matches filter[field] or filter[field][op].
//
// Both groups are DELIBERATELY permissive (`[^\[\]]+`, anything but a bracket)
// and the strictness lives in the validation below. It used to be the other way
// round — `[a-z][a-z0-9_]*` for the field and `[a-z]+` for the op — and that was
// ENG-14: a parameter the regex did not match was skipped by the parse loop with
// NO error, so `?filter[title][is_null]=true` (an underscore in the op) and
// `?filter[Title][eq]=x` (a capital in the field) both answered **200 with the
// full, unfiltered list**. The caller believed it had filtered and was shown
// everything.
//
// A pattern that decides what is VALID silently discards what it does not match.
// A pattern that decides only what is a FILTER hands everything else to code that
// can produce an error naming the problem. See ADR-024.
var filterParamRe = regexp.MustCompile(`^filter\[([^\[\]]+)\](?:\[([^\[\]]+)\])?$`)

// filterParamPrefix identifies a query parameter the caller MEANT as a filter.
// Anything starting with it must parse as one or be rejected — never dropped.
const filterParamPrefix = "filter["

// uuidRe validates cursor values passed to ?after and ?before.
var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// orderParamRe matches order[field]=asc|desc
// Permissive by the same reasoning as filterParamRe: the pattern decides what is
// an ORDER parameter, the validation below decides whether it is a valid one.
var orderParamRe = regexp.MustCompile(`^order\[([^\[\]]+)\]$`)

// orderParamPrefix identifies a parameter the caller MEANT as a sort direction.
const orderParamPrefix = "order["

// conditionFieldRe validates RBAC WhereCondition.Field before SQL interpolation.
var conditionFieldRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// operatorsForType lists valid filter operators per schema field type.
var operatorsForType = map[string]map[string]bool{
	"string":  {"eq": true, "partial": true, "start": true},
	"text":    {"eq": true, "partial": true, "start": true},
	"int":     {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"int64":   {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"float64": {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"time":    {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "after": true, "before": true},
	"uuid":    {"eq": true},
	"bool":    {"eq": true},
	"file":    {"eq": true}, // a file_id column — filters like uuid (FILES-LINK-S1)
}

type filterClause struct {
	field string
	op    string // "eq", "partial", "gte", "lte", "gt", "lt", "after", "before"
	value string
}

// QueryBuilder holds a parsed, validated query ready to emit SQL.
type QueryBuilder struct {
	resource  string
	res       *schema.ResourceSchema
	page      int
	perPage   int
	filters   []filterClause
	sortField string
	sortOrder string
	search    string
	condition *rbac.WhereCondition
	// Keyset (cursor) pagination — when set, OFFSET is skipped and an index range scan is used.
	afterID  string // ?after=UUID  → WHERE id > UUID ORDER BY id ASC  LIMIT N
	beforeID string // ?before=UUID → WHERE id < UUID ORDER BY id DESC LIMIT N
}

// BuildQuery parses url.Values and returns a validated QueryBuilder.
// Returns error for unknown filter fields, type-incompatible operators, or non-integer page/per_page.
func BuildQuery(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
) (*QueryBuilder, error) {
	if condition != nil && !conditionFieldRe.MatchString(condition.Field) {
		return nil, fmt.Errorf("invalid condition field: %q", condition.Field)
	}

	qb := &QueryBuilder{
		resource:  resource,
		res:       res,
		page:      DefaultPage,
		perPage:   DefaultPerPage,
		condition: condition,
	}

	if pageStr := params.Get("page"); pageStr != "" {
		p, err := strconv.Atoi(pageStr)
		if err != nil {
			return nil, fmt.Errorf("invalid page parameter: must be a positive integer")
		}
		if p > 0 {
			if p > MaxPage {
				p = MaxPage
			}
			qb.page = p
		}
	}

	if ppStr := params.Get("per_page"); ppStr != "" {
		pp, err := strconv.Atoi(ppStr)
		if err != nil {
			return nil, fmt.Errorf("invalid per_page parameter: must be a positive integer")
		}
		if pp > 0 {
			if pp > MaxPerPage {
				pp = MaxPerPage
			}
			qb.perPage = pp
		}
	}

	// filters: filter[field]=value or filter[field][op]=value
	//
	// ENG-14 / ADR-024: a parameter that ANNOUNCES ITSELF as a filter is either
	// understood or rejected. It is never skipped, because skipping it returns an
	// unfiltered list under a 200 — the caller cannot tell the difference between
	// "no rows matched your filter" and "your filter was thrown away".
	for key, vals := range params {
		if !strings.HasPrefix(key, filterParamPrefix) {
			continue
		}
		m := filterParamRe.FindStringSubmatch(key)
		if m == nil {
			return nil, fmt.Errorf("malformed filter parameter %q: expected filter[field] or filter[field][op]", key)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("filter parameter %q has no value", key)
		}
		field := m[1]
		op := "eq"
		if m[2] != "" {
			op = m[2]
		}

		fd, ok := res.Fields[field]
		if !ok {
			return nil, fmt.Errorf("unknown filter field: %s (available: %s)", field, availableFieldNames(res))
		}

		if err := validateFilterOp(op, fd.Type); err != nil {
			return nil, fmt.Errorf("filter[%s][%s]: %s", field, op, err.Error())
		}

		qb.filters = append(qb.filters, filterClause{field: field, op: op, value: vals[0]})
	}
	// sort for deterministic SQL output
	sort.Slice(qb.filters, func(i, j int) bool {
		if qb.filters[i].field != qb.filters[j].field {
			return qb.filters[i].field < qb.filters[j].field
		}
		return qb.filters[i].op < qb.filters[j].op
	})

	// old sort syntax: ?sort=campo&order=asc|desc
	//
	// ADR-024: an unknown sort field used to be dropped, which returned rows in an
	// arbitrary order under a 200 — the caller had no way to tell their sort was
	// discarded, and the docs had to warn "verify result order, don't trust the
	// param". A sort that cannot be honored is now an error.
	if sf := params.Get("sort"); sf != "" {
		if _, ok := res.Fields[sf]; !ok && sf != "id" {
			return nil, fmt.Errorf("unknown sort field: %s (available: %s)", sf, availableFieldNames(res))
		}
		qb.sortField = sf
		qb.sortOrder = "ASC"
		if dir := params.Get("order"); dir != "" {
			d, err := sortDirection(dir)
			if err != nil {
				return nil, err
			}
			qb.sortOrder = d
		}
	}

	// new order syntax: ?order[campo]=asc|desc (overrides old if both present)
	for key, vals := range params {
		if !strings.HasPrefix(key, orderParamPrefix) {
			continue
		}
		m := orderParamRe.FindStringSubmatch(key)
		if m == nil {
			return nil, fmt.Errorf("malformed order parameter %q: expected order[field]=asc|desc", key)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("order parameter %q has no value", key)
		}
		field := m[1]
		if _, ok := res.Fields[field]; !ok && field != "id" {
			return nil, fmt.Errorf("unknown sort field: %s (available: %s)", field, availableFieldNames(res))
		}
		qb.sortField = field
		d, err := sortDirection(vals[0])
		if err != nil {
			return nil, err
		}
		qb.sortOrder = d
		break // first valid order param wins
	}

	qb.search = params.Get("search")

	// Cursor pagination — mutually exclusive; ?after takes precedence if both given.
	if after := params.Get("after"); after != "" {
		if !uuidRe.MatchString(after) {
			return nil, fmt.Errorf("invalid after cursor: must be a lowercase UUID")
		}
		qb.afterID = after
	} else if before := params.Get("before"); before != "" {
		if !uuidRe.MatchString(before) {
			return nil, fmt.Errorf("invalid before cursor: must be a lowercase UUID")
		}
		qb.beforeID = before
	}

	return qb, nil
}

// EffectiveOrder returns the column and direction (ASC|DESC) that SQL() orders
// the base rows by — so the include path (RELATIONS-V1) can re-impose the SAME
// order on its json_agg of wrapped rows. It mirrors SQL()'s ORDER BY logic:
// keyset cursors order by id (DESC for ?before), an explicit ?sort wins
// otherwise, and the default is id ASC.
func (qb *QueryBuilder) EffectiveOrder() (field, dir string) {
	switch {
	case qb.beforeID != "":
		return "id", "DESC"
	case qb.afterID != "":
		return "id", "ASC"
	case qb.sortField != "":
		return qb.sortField, qb.sortOrder
	default:
		return "id", "ASC"
	}
}

// Page returns the current page number (1-based).
func (qb *QueryBuilder) Page() int { return qb.page }

// PerPage returns the current page size.
func (qb *QueryBuilder) PerPage() int { return qb.perPage }

// SQL returns the SELECT and COUNT queries with their respective arg slices.
// selectArgs contains LIMIT/OFFSET appended after WHERE args; countArgs does not.
// When afterID or beforeID is set, keyset pagination is used: no OFFSET, one round-trip.
func (qb *QueryBuilder) SQL() (selectQ, countQ string, selectArgs, countArgs []any) {
	whereClause, whereArgs := qb.buildWhere()
	countArgs = whereArgs

	// Table identifier is ALWAYS quoted (consistent quoting — BUG1). The tenant
	// list path (db.QueryDirect) re-qualifies a quoted-or-unquoted table name;
	// the search_path paths (GraphQL, include, ctx.Query) need the quotes so a
	// hyphenated resource name (e.g. "order-products") resolves.
	tbl := quoteIdent(qb.resource)

	// Keyset pagination — no OFFSET, ORDER BY driven by cursor direction.
	if qb.afterID != "" || qb.beforeID != "" {
		var orderClause string
		if qb.beforeID != "" {
			orderClause = "ORDER BY id DESC" // reversed; client reverses again to restore order
		} else {
			orderClause = "ORDER BY id ASC"
		}
		limitIdx := len(whereArgs) + 1
		selectArgs = make([]any, len(whereArgs)+1)
		copy(selectArgs, whereArgs)
		selectArgs[len(whereArgs)] = qb.perPage
		selectQ = fmt.Sprintf("SELECT * FROM %s%s %s LIMIT $%d",
			tbl, whereClause, orderClause, limitIdx)
		countQ = fmt.Sprintf("SELECT COUNT(*) FROM %s%s", tbl, whereClause)
		return
	}

	// Default offset-based pagination.
	orderClause := "ORDER BY id ASC"
	if qb.sortField != "" {
		orderClause = fmt.Sprintf("ORDER BY %s %s", qb.sortField, qb.sortOrder)
	}

	offset := (qb.page - 1) * qb.perPage
	limitIdx := len(whereArgs) + 1
	offsetIdx := len(whereArgs) + 2

	selectArgs = make([]any, len(whereArgs)+2)
	copy(selectArgs, whereArgs)
	selectArgs[len(whereArgs)] = qb.perPage
	selectArgs[len(whereArgs)+1] = offset

	selectQ = fmt.Sprintf("SELECT * FROM %s%s %s LIMIT $%d OFFSET $%d",
		tbl, whereClause, orderClause, limitIdx, offsetIdx)
	countQ = fmt.Sprintf("SELECT COUNT(*) FROM %s%s", tbl, whereClause)
	return
}

// AppendRowCondition appends a row-level RBAC WhereCondition to a single-row SQL
// statement that is already parameterized at $1..$len(args) (e.g. a GET-by-id or
// DELETE filtered on id). The condition field is validated as a bare identifier
// (the value is always a bound parameter); an invalid field returns an error so
// the caller fails closed. This is the one canonical implementation used by both
// the REST and GraphQL get-by-id/delete paths so they enforce the SAME row-level
// RBAC the list path applies via buildWhere.
func AppendRowCondition(sql string, args []any, cond *rbac.WhereCondition) (string, []any, error) {
	if cond == nil {
		return sql, args, nil
	}
	if !conditionFieldRe.MatchString(cond.Field) {
		return sql, args, fmt.Errorf("invalid rbac condition field: %q", cond.Field)
	}
	sql += fmt.Sprintf(" AND %s = $%d", cond.Field, len(args)+1)
	args = append(args, cond.Value)
	return sql, args, nil
}

// AppendAliasedRowCondition is AppendRowCondition for a statement that JOINs more
// than one table, so the condition column must be qualified by its table alias to be
// unambiguous (the relation subresource route: SELECT r.* FROM <target> r JOIN
// <parent> src …). alias is an engine-controlled identifier (e.g. "r"), never user
// input; the condition field is validated as a bare identifier and the value is
// always a bound parameter. The operator is equality — the only operator an RBAC
// condition may declare (enforced at schema load; see schema.validateConditionOp).
func AppendAliasedRowCondition(sql string, args []any, alias string, cond *rbac.WhereCondition) (string, []any, error) {
	if cond == nil {
		return sql, args, nil
	}
	if !conditionFieldRe.MatchString(cond.Field) {
		return sql, args, fmt.Errorf("invalid rbac condition field: %q", cond.Field)
	}
	sql += fmt.Sprintf(" AND %s.%s = $%d", alias, cond.Field, len(args)+1)
	args = append(args, cond.Value)
	return sql, args, nil
}

func (qb *QueryBuilder) buildWhere() (clause string, args []any) {
	var parts []string
	idx := 1

	// Cursor conditions must come first so parameter indices are stable.
	if qb.afterID != "" {
		parts = append(parts, fmt.Sprintf("id > $%d", idx))
		args = append(args, qb.afterID)
		idx++
	} else if qb.beforeID != "" {
		parts = append(parts, fmt.Sprintf("id < $%d", idx))
		args = append(args, qb.beforeID)
		idx++
	}

	parts, args, _ = qb.appendConditions(parts, args, idx)

	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// appendConditions emits the RBAC row condition, the filters, and the search
// predicate into parts/args starting at parameter index idx, returning the
// updated parts, args, and next index. It is the shared core of both the list
// WHERE (called after the cursor clause — buildWhere) and the aggregate WHERE
// (called with no cursor — aggregateWhere), so both apply the SAME RBAC scope and
// filters. The list SQL is byte-identical to before this extraction.
func (qb *QueryBuilder) appendConditions(parts []string, args []any, idx int) ([]string, []any, int) {
	if qb.condition != nil {
		parts = append(parts, fmt.Sprintf("%s = $%d", qb.condition.Field, idx))
		args = append(args, qb.condition.Value)
		idx++
	}

	for _, f := range qb.filters {
		switch f.op {
		case "partial":
			parts = append(parts, fmt.Sprintf("%s ILIKE $%d", f.field, idx))
			args = append(args, "%"+f.value+"%")
		case "start":
			parts = append(parts, fmt.Sprintf("%s ILIKE $%d", f.field, idx))
			args = append(args, f.value+"%")
		default:
			parts = append(parts, fmt.Sprintf("%s %s $%d", f.field, filterToSQL(f.op), idx))
			args = append(args, f.value)
		}
		idx++
	}

	if qb.search != "" {
		// collect string fields sorted for deterministic SQL output
		var strFields []string
		for name, fd := range qb.res.Fields {
			if fd.Type == "string" || fd.Type == "text" {
				strFields = append(strFields, name)
			}
		}
		sort.Strings(strFields)

		escaped := strings.NewReplacer(`%`, `\%`, `_`, `\_`).Replace(qb.search)
		var searchParts []string
		for _, name := range strFields {
			searchParts = append(searchParts, fmt.Sprintf("%s ILIKE $%d ESCAPE '\\'", name, idx))
			args = append(args, "%"+escaped+"%")
			idx++
		}
		if len(searchParts) > 0 {
			parts = append(parts, "("+strings.Join(searchParts, " OR ")+")")
		}
	}

	return parts, args, idx
}

// aggregateWhere builds the SAME condition+filter+search WHERE as the list path
// (buildWhere) but WITHOUT the cursor clause or pagination — an aggregate covers
// the whole filtered, RBAC-scoped set, not a page.
func (qb *QueryBuilder) aggregateWhere() (clause string, args []any) {
	var parts []string
	parts, args, _ = qb.appendConditions(parts, nil, 1)
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// validateFilterOp checks whether op is valid for the given schema field type.
func validateFilterOp(op, fieldType string) error {
	ops, ok := operatorsForType[fieldType]
	if !ok {
		// unknown type — allow eq only
		if op != "eq" {
			return fmt.Errorf("operator %q not allowed for type %q", op, fieldType)
		}
		return nil
	}
	if !ops[op] {
		allowed := make([]string, 0, len(ops))
		for o := range ops {
			allowed = append(allowed, o)
		}
		sort.Strings(allowed)
		return fmt.Errorf("operator %q not allowed for type %q (allowed: %s)", op, fieldType, strings.Join(allowed, ", "))
	}
	return nil
}

// filterToSQL maps a filter operator to its SQL comparison symbol.
func filterToSQL(op string) string {
	switch op {
	case "gte":
		return ">="
	case "lte":
		return "<="
	case "gt", "after":
		return ">"
	case "lt", "before":
		return "<"
	default:
		return "="
	}
}

// availableFieldNames lists a resource's filterable columns, sorted, for the
// "unknown filter field" error. Naming the alternatives is the difference
// between an error a caller can act on and one they have to go read the schema
// for — the same contract the schema loader's strict-key errors already keep
// (ADR-024).
func availableFieldNames(res *schema.ResourceSchema) string {
	names := make([]string, 0, len(res.Fields)+1)
	names = append(names, "id")
	for n := range res.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// sortDirection maps a client's ?order= value to SQL. An unrecognised direction
// is an ERROR rather than a silent fall-back to ASC: `?order=descending` used to
// sort ascending and say nothing, which is the same defect class as a dropped
// filter — the caller reads the first page and believes it is the newest
// (ADR-024).
func sortDirection(v string) (string, error) {
	switch strings.ToLower(v) {
	case "asc":
		return "ASC", nil
	case "desc":
		return "DESC", nil
	default:
		return "", fmt.Errorf("invalid sort direction %q: use asc or desc", v)
	}
}
