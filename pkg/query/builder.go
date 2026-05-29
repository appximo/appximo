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
)

// filterParamRe matches filter[field] or filter[field][op]
var filterParamRe = regexp.MustCompile(`^filter\[([a-z][a-z0-9_]*)\](?:\[([a-z]+)\])?$`)

// orderParamRe matches order[field]=asc|desc
var orderParamRe = regexp.MustCompile(`^order\[([a-z][a-z0-9_]*)\]$`)

// operatorsForType lists valid filter operators per schema field type.
var operatorsForType = map[string]map[string]bool{
	"string":  {"eq": true, "partial": true},
	"text":    {"eq": true, "partial": true},
	"int":     {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"int64":   {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"float64": {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true},
	"time":    {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "after": true, "before": true},
	"uuid":    {"eq": true},
	"bool":    {"eq": true},
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
}

// BuildQuery parses url.Values and returns a validated QueryBuilder.
// Returns error for unknown filter fields, type-incompatible operators, or non-integer page/per_page.
func BuildQuery(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
) (*QueryBuilder, error) {
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
	for key, vals := range params {
		m := filterParamRe.FindStringSubmatch(key)
		if m == nil || len(vals) == 0 {
			continue
		}
		field := m[1]
		op := "eq"
		if m[2] != "" {
			op = m[2]
		}

		fd, ok := res.Fields[field]
		if !ok {
			return nil, fmt.Errorf("unknown filter field: %s", field)
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
	if sf := params.Get("sort"); sf != "" {
		if _, ok := res.Fields[sf]; ok {
			qb.sortField = sf
			qb.sortOrder = "ASC"
			if strings.ToLower(params.Get("order")) == "desc" {
				qb.sortOrder = "DESC"
			}
		}
	}

	// new order syntax: ?order[campo]=asc|desc (overrides old if both present)
	for key, vals := range params {
		m := orderParamRe.FindStringSubmatch(key)
		if m == nil || len(vals) == 0 {
			continue
		}
		field := m[1]
		if _, ok := res.Fields[field]; !ok {
			continue // silently ignore unknown order fields
		}
		qb.sortField = field
		if strings.ToLower(vals[0]) == "desc" {
			qb.sortOrder = "DESC"
		} else {
			qb.sortOrder = "ASC"
		}
		break // first valid order param wins
	}

	qb.search = params.Get("search")
	return qb, nil
}

// Page returns the current page number (1-based).
func (qb *QueryBuilder) Page() int { return qb.page }

// PerPage returns the current page size.
func (qb *QueryBuilder) PerPage() int { return qb.perPage }

// SQL returns the SELECT and COUNT queries with their respective arg slices.
// selectArgs contains LIMIT/OFFSET appended after WHERE args; countArgs does not.
func (qb *QueryBuilder) SQL() (selectQ, countQ string, selectArgs, countArgs []any) {
	whereClause, whereArgs := qb.buildWhere()

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
	countArgs = whereArgs

	selectQ = fmt.Sprintf("SELECT * FROM %s%s %s LIMIT $%d OFFSET $%d",
		qb.resource, whereClause, orderClause, limitIdx, offsetIdx)
	countQ = fmt.Sprintf("SELECT COUNT(*) FROM %s%s", qb.resource, whereClause)
	return
}

func (qb *QueryBuilder) buildWhere() (clause string, args []any) {
	var parts []string
	idx := 1

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

		var searchParts []string
		for _, name := range strFields {
			searchParts = append(searchParts, fmt.Sprintf("%s ILIKE $%d", name, idx))
			args = append(args, "%"+qb.search+"%")
			idx++
		}
		if len(searchParts) > 0 {
			parts = append(parts, "("+strings.Join(searchParts, " OR ")+")")
		}
	}

	if len(parts) == 0 {
		return "", args
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
