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

var filterParamRe = regexp.MustCompile(`^filter\[([a-z][a-z0-9_]*)\]$`)

type filterClause struct {
	field string
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
// Unknown filter/sort fields are silently ignored — no SQL injection possible.
func BuildQuery(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
) *QueryBuilder {
	qb := &QueryBuilder{
		resource:  resource,
		res:       res,
		page:      DefaultPage,
		perPage:   DefaultPerPage,
		condition: condition,
	}

	if p, err := strconv.Atoi(params.Get("page")); err == nil && p > 0 {
		qb.page = p
	}
	if pp, err := strconv.Atoi(params.Get("per_page")); err == nil && pp > 0 {
		if pp > MaxPerPage {
			pp = MaxPerPage
		}
		qb.perPage = pp
	}

	// filters: filter[field]=value — only schema fields, sorted for determinism
	for key, vals := range params {
		m := filterParamRe.FindStringSubmatch(key)
		if m == nil || len(vals) == 0 {
			continue
		}
		field := m[1]
		if _, ok := res.Fields[field]; !ok {
			continue
		}
		qb.filters = append(qb.filters, filterClause{field: field, value: vals[0]})
	}
	sort.Slice(qb.filters, func(i, j int) bool {
		return qb.filters[i].field < qb.filters[j].field
	})

	// sort — only schema fields allowed
	if sf := params.Get("sort"); sf != "" {
		if _, ok := res.Fields[sf]; ok {
			qb.sortField = sf
			if strings.ToLower(params.Get("order")) == "desc" {
				qb.sortOrder = "DESC"
			} else {
				qb.sortOrder = "ASC"
			}
		}
	}

	qb.search = params.Get("search")
	return qb
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
		parts = append(parts, fmt.Sprintf("%s = $%d", f.field, idx))
		args = append(args, f.value)
		idx++
	}

	if qb.search != "" {
		// collect string fields sorted for deterministic SQL output
		var strFields []string
		for name, fd := range qb.res.Fields {
			if fd.Type == "string" {
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
