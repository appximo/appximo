package query

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
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
//
// `is_null` (SCHEMA-6) is valid on EVERY declared type — nullability is a
// property of the column (`required`), not of the type, and the required check
// lives in the parse loop where the FieldDef is at hand. `json`/`jsonb` gained
// explicit entries with it: they used to ride the unknown-type eq-only
// fallback, but "rows with no attrs document" is exactly the question is_null
// answers.
var operatorsForType = map[string]map[string]bool{
	"string":  {"eq": true, "partial": true, "start": true, "is_null": true},
	"text":    {"eq": true, "partial": true, "start": true, "is_null": true},
	"int":     {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "is_null": true},
	"int64":   {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "is_null": true},
	"float64": {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "is_null": true},
	"time":    {"eq": true, "gte": true, "lte": true, "gt": true, "lt": true, "after": true, "before": true, "is_null": true},
	"uuid":    {"eq": true, "is_null": true},
	"bool":    {"eq": true, "is_null": true},
	"file":    {"eq": true, "is_null": true}, // a file_id column — filters like uuid (FILES-LINK-S1)
	"json":    {"eq": true, "is_null": true},
	"jsonb":   {"eq": true, "is_null": true},
}

type filterClause struct {
	field string
	op    string // "eq", "partial", "gte", "lte", "gt", "lt", "after", "before", "is_null"
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

	// allowed is the role's RBAC field allowlist (nil = unrestricted). It bounds
	// which fields may be filtered/sorted (ErrForbiddenField at parse) and which
	// text columns the ?search= sweep touches (searchableFields).
	allowed []string

	// fields is the projected SELECT list (MOTOR-FIELDS-S1, `?fields=`): nil
	// means every column (`SELECT *`, byte-identical to before the feature);
	// otherwise the validated, deduplicated column names with `id` first. The
	// projection is pushed into the SQL — a column that is not listed is not
	// read, which is the whole point: a large json/text value lives in TOAST
	// and is detoasted only when the SELECT names it. Trimming in Go after a
	// `SELECT *` would save bytes on the wire and none of the disk time.
	fields []string
}

// searchableFields lists the string/text columns the role may read, sorted —
// the exact set the ?search= OR sweeps.
func (qb *QueryBuilder) searchableFields() []string {
	var out []string
	for name, fd := range qb.res.Fields {
		if (fd.Type == "string" || fd.Type == "text") && fieldAllowed(name, qb.allowed) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ErrForbiddenField signals that a list request named a field the role's RBAC
// allowlist excludes — in a filter or a sort. Callers map it to 403; every other
// BuildQuery error stays a 400. SEC-5's lesson, closed generally in
// PUBLIC-SURFACE-S1: the response allowlist alone left `?filter[hidden][eq]=x`
// as a value oracle over a column the role could never read (match/no-match
// reveals its contents by binary search), and `?sort=hidden` as an ordering
// oracle. The defense must exist wherever a field can be NAMED, not only where
// it is returned. Same contract as ErrAggForbiddenField on the aggregate path.
var ErrForbiddenField = errors.New("request references a field not permitted for this role")

// BuildQuery parses url.Values and returns a validated QueryBuilder.
// Returns error for unknown filter fields, type-incompatible operators, or
// non-integer page/per_page; naming a field outside allowedFields (nil = no
// restriction; the implicit id is always allowed, as in every other surface)
// returns ErrForbiddenField.
func BuildQuery(
	resource string,
	res *schema.ResourceSchema,
	params url.Values,
	condition *rbac.WhereCondition,
	allowedFields []string,
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
		allowed:   allowedFields,
	}
	roleAllows := func(field string) bool {
		return field == "id" || fieldAllowed(field, allowedFields)
	}

	// Cursor pagination is parsed FIRST because it changes what the other
	// parameters may mean (ENG-15). Three silences used to live here:
	//   - `?after=A&before=B` kept `after` and dropped `before` without a word
	//     (two contradictory directions, one silently discarded);
	//   - `?after=…&sort=x` / `&page=7` dropped the sort and the page — and the
	//     response's meta.page still ECHOED the page it ignored, actively
	//     asserting something false;
	//   - `?after=` (an empty cursor — what an empty form field produces) was
	//     treated as absent, the same presence-vs-empty split ENG-30 closed for
	//     page/sort.
	// A cursor request now owns its shape: one direction, no page, no sort
	// (cursor pagination orders by id — that is what makes the cursor total),
	// and meta reports no page number (see UsesCursor).
	afterVal, afterPresent, err := singleValue(params, "after")
	if err != nil {
		return nil, err
	}
	beforeVal, beforePresent, err := singleValue(params, "before")
	if err != nil {
		return nil, err
	}
	if afterPresent && beforePresent {
		return nil, fmt.Errorf("conflicting cursors: after and before cannot be combined — a request paginates in one direction, send one of them")
	}
	if afterPresent {
		if !uuidRe.MatchString(afterVal) {
			return nil, fmt.Errorf("invalid after cursor: must be a lowercase UUID")
		}
		qb.afterID = afterVal
	}
	if beforePresent {
		if !uuidRe.MatchString(beforeVal) {
			return nil, fmt.Errorf("invalid before cursor: must be a lowercase UUID")
		}
		qb.beforeID = beforeVal
	}
	cursorParam := ""
	if afterPresent {
		cursorParam = "after"
	} else if beforePresent {
		cursorParam = "before"
	}

	// ADR-024. `?page=abc` has always answered "must be a positive integer" —
	// and `?page=0` and `?page=-4` were then silently served as page 1. The
	// engine's own error message stated the rule its silent path broke. A
	// non-positive page is now rejected with that same message, so the contract
	// the message describes is the contract the code enforces.
	//
	// The over-max CLAMP is deliberately NOT rejected: "max 100" is a documented
	// part of the contract, and the response REPORTS the effective value in
	// meta.per_page / meta.page, which is the "tolerance must be reported" half
	// of the policy (ADR-024 §5) rather than a silent substitution.
	// The gate is PRESENCE, not non-emptiness (ENG-30). `?page=` — what an empty
	// form field produces — used to be silently served as page 1 while `?page=0`
	// was a named 400: the same function applied two policies to sibling
	// parameters. An empty value now fails the same "must be a positive integer"
	// check its non-empty siblings do.
	if pageStr, present, err := singleValue(params, "page"); err != nil {
		return nil, err
	} else if present {
		if cursorParam != "" {
			return nil, fmt.Errorf("page cannot be combined with the %s cursor: cursor pagination has no page number — remove page (size the window with per_page)", cursorParam)
		}
		p, perr := strconv.Atoi(pageStr)
		if perr != nil || p <= 0 {
			return nil, fmt.Errorf("invalid page parameter %q: must be a positive integer", pageStr)
		}
		if p > MaxPage {
			p = MaxPage
		}
		qb.page = p
	}

	if ppStr, present, err := singleValue(params, "per_page"); err != nil {
		return nil, err
	} else if present {
		pp, perr := strconv.Atoi(ppStr)
		if perr != nil || pp <= 0 {
			return nil, fmt.Errorf("invalid per_page parameter %q: must be a positive integer", ppStr)
		}
		if pp > MaxPerPage {
			pp = MaxPerPage
		}
		qb.perPage = pp
	}

	// filters: filter[field]=value or filter[field][op]=value
	//
	// ENG-14 / ADR-024: a parameter that ANNOUNCES ITSELF as a filter is either
	// understood or rejected. It is never skipped, because skipping it returns an
	// unfiltered list under a 200 — the caller cannot tell the difference between
	// "no rows matched your filter" and "your filter was thrown away".
	// Sorted like the order[…] loop below (ENG-16 class, SILENT-CORRUPTION-S1):
	// with two invalid filter parameters, the 400 used to name whichever key
	// Go's map iteration reached first — a different error per identical
	// request. Valid filters are ANDed, so their order never changed results;
	// only the error message was random. Sorted keys make it deterministic.
	var filterKeys []string
	for key := range params {
		if strings.HasPrefix(key, filterParamPrefix) {
			filterKeys = append(filterKeys, key)
		}
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		vals := params[key]
		m := filterParamRe.FindStringSubmatch(key)
		if m == nil {
			return nil, fmt.Errorf("malformed filter parameter %q: expected filter[field] or filter[field][op]", key)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("filter parameter %q has no value", key)
		}
		if len(vals) > 1 {
			return nil, repeatedParamErr(key, len(vals))
		}
		field := m[1]
		op := "eq"
		if m[2] != "" {
			op = m[2]
		}

		fd, ok := res.Fields[field]
		if !ok {
			// ENG-26: `id` is the implicit primary key — not in res.Fields, but
			// `?sort=id` and the keyset cursors are built on it, so two of the
			// three places that take a field name accepted it and this one did
			// not. It filters as the uuid column it is (only legal op: eq, via
			// operatorsForType), and `?filter[id][eq]=<uuid>` composes with
			// other filters in a way `GET /{id}` cannot.
			if field == "id" {
				fd = schema.FieldDef{Type: "uuid"}
			} else {
				return nil, fmt.Errorf("unknown filter field: %s (available: %s)", field, availableFieldNames(res))
			}
		}
		if !roleAllows(field) {
			return nil, fmt.Errorf("%w: filter[%s]", ErrForbiddenField, field)
		}

		if err := validateFilterOp(op, fd.Type); err != nil {
			return nil, fmt.Errorf("filter[%s][%s]: %s", field, op, err.Error())
		}

		// is_null (SCHEMA-6): the value is not a column value — it selects
		// between IS NULL (true) and IS NOT NULL (false), so it is read by
		// value with the same vocabulary as ?count (ENG-23) and validated
		// here, never bound. A column that can never be null — the implicit
		// primary key, or a `required` (NOT NULL) field — is a named 400: the
		// filter would match zero rows forever (is_null=true) or every row
		// (is_null=false), the exact "declared but meaningless" class SCHEMA-5
		// warns about elsewhere.
		if op == "is_null" {
			if field == "id" {
				return nil, fmt.Errorf("filter[id][is_null]: id is the primary key — it can never be null")
			}
			if fd.Required {
				return nil, fmt.Errorf("filter[%s][is_null]: %q is declared required (NOT NULL) — it can never be null", field, field)
			}
			var want string
			switch vals[0] {
			case "true", "1":
				want = "true"
			case "false", "0":
				want = "false"
			default:
				return nil, fmt.Errorf("filter[%s][is_null]: %q is not a valid value (use true for IS NULL, false for IS NOT NULL)", field, vals[0])
			}
			qb.filters = append(qb.filters, filterClause{field: field, op: op, value: want})
			continue
		}

		// An EMPTY value is only meaningful for a text column, where it asks for
		// the empty string. On any other type it is not a value at all: it used
		// to be bound verbatim, and Postgres answered with a data-exception that
		// surfaced as an opaque 400 (or, for a time column, a 500). Rejecting it
		// here — in code that knows the field's declared type — names the field
		// without exposing anything about the database (ADR-024; the
		// "Postgres errors are masked" property is preserved because this check
		// runs BEFORE any SQL is built).
		//
		// Deliberately narrow: this rejects the EMPTY value only, not every
		// wrongly-typed one. A full type check would have to reproduce
		// Postgres's own accepted set exactly, and it does not match Go's —
		// measured on the live engine, `?filter[done][eq]=yes` returns 200 today
		// because Postgres accepts "yes" as a boolean while strconv.ParseBool
		// rejects it. Being stricter than the database would break working
		// clients, so the wider check is a separate, evidence-backed decision
		// (backlog ENG-25).
		// `json` counts as text here: it is stored as TEXT (the type table says so
		// two sections away), so an empty value is a legitimate exact match on the
		// empty string. `jsonb` is a real document and cannot take one. (Since
		// SCHEMA-6 closed, "no value" questions also have a real operator:
		// filter[field][is_null], handled above before this check.)
		if vals[0] == "" && fd.Type != "string" && fd.Type != "text" && fd.Type != "json" {
			return nil, fmt.Errorf("filter[%s][%s]: empty value is not valid for a %s field", field, op, fd.Type)
		}

		// ENG-25: a value Postgres could not cast is named HERE, before any SQL
		// exists — `?filter[amount][gt]=abc` used to be an anonymous
		// `400 invalid request` from the masked database error. The acceptors
		// reproduce Postgres's input grammar (never Go's) so no working request
		// is rejected; see validateFilterValue.
		if vals[0] != "" {
			if err := validateFilterValue(field, op, fd.Type, vals[0]); err != nil {
				return nil, err
			}
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
	// Presence-gated like page/per_page (ENG-30): `?sort=` used to be silently
	// ignored while `?sort=ghost` was a named 400 — two policies on one param.
	if sf, present, err := singleValue(params, "sort"); err != nil {
		return nil, err
	} else if present {
		if cursorParam != "" {
			// ENG-15: a keyset cursor orders by id — that ordering is what makes
			// "rows after X" well-defined — so a sort cannot be honored with one.
			// It used to be silently dropped: the caller asked for newest-first
			// and read an id-ordered page believing it was sorted.
			return nil, fmt.Errorf("sort cannot be combined with the %s cursor: cursor pagination orders by id — remove sort, or paginate with page instead of a cursor", cursorParam)
		}
		if sf == "" {
			return nil, fmt.Errorf("sort parameter has an empty value (available: %s)", availableFieldNames(res))
		}
		if _, ok := res.Fields[sf]; !ok && sf != "id" {
			return nil, fmt.Errorf("unknown sort field: %s (available: %s)", sf, availableFieldNames(res))
		}
		if !roleAllows(sf) {
			return nil, fmt.Errorf("%w: sort=%s", ErrForbiddenField, sf)
		}
		qb.sortField = sf
		qb.sortOrder = "ASC"
		if dir, dirPresent, derr := singleValue(params, "order"); derr != nil {
			return nil, derr
		} else if dirPresent {
			d, serr := sortDirection(dir)
			if serr != nil {
				return nil, serr
			}
			qb.sortOrder = d
		}
	} else if dir, present, err := singleValue(params, "order"); err != nil {
		return nil, err
	} else if present {
		// `?order=desc` with no `?sort=` names a direction but no field — it
		// used to be read by nothing and dropped without a word, the exact
		// sibling of the dropped sort field (ENG-30). Saying so costs one map
		// lookup on requests that carry the parameter.
		return nil, fmt.Errorf("order parameter %q requires sort (use ?sort=field&order=asc|desc, or ?order[field]=asc|desc)", dir)
	}

	// new order syntax: ?order[campo]=asc|desc (overrides ?sort= if both present —
	// deterministic and documented, unlike the case below).
	//
	// ENG-16: with TWO order[…] parameters the loop used to take whichever key Go's
	// map iteration yielded first and break — measured 174/26 across 200 builds of
	// the SAME URL, a coin flip visible to the client. The keys are now collected
	// and sorted first: one is the documented surface, more than one is an error
	// naming them all (multi-field sort does not exist — see AGENTS "Does not
	// exist"), and the single-key path is byte-identical SQL to before.
	var orderKeys []string
	for key := range params {
		if strings.HasPrefix(key, orderParamPrefix) {
			orderKeys = append(orderKeys, key)
		}
	}
	sort.Strings(orderKeys)
	if len(orderKeys) > 1 {
		return nil, fmt.Errorf("multiple order parameters (%s): one sort field is supported — multi-field sort does not exist", strings.Join(orderKeys, ", "))
	}
	for _, key := range orderKeys {
		vals := params[key]
		m := orderParamRe.FindStringSubmatch(key)
		if m == nil {
			return nil, fmt.Errorf("malformed order parameter %q: expected order[field]=asc|desc", key)
		}
		if len(vals) == 0 {
			return nil, fmt.Errorf("order parameter %q has no value", key)
		}
		if len(vals) > 1 {
			return nil, repeatedParamErr(key, len(vals))
		}
		if cursorParam != "" {
			return nil, fmt.Errorf("%s cannot be combined with the %s cursor: cursor pagination orders by id — remove it, or paginate with page instead of a cursor", key, cursorParam)
		}
		field := m[1]
		if _, ok := res.Fields[field]; !ok && field != "id" {
			return nil, fmt.Errorf("unknown sort field: %s (available: %s)", field, availableFieldNames(res))
		}
		if !roleAllows(field) {
			return nil, fmt.Errorf("%w: order[%s]", ErrForbiddenField, field)
		}
		qb.sortField = field
		d, err := sortDirection(vals[0])
		if err != nil {
			return nil, err
		}
		qb.sortOrder = d
	}

	if search, _, err := singleValue(params, "search"); err != nil {
		return nil, err
	} else if search != "" {
		// NIGHT-SWEEP-S1: search scans only string/text columns, so on a
		// resource with none it used to be a silent no-op — the full unfiltered
		// list under a 200, indistinguishable from "everything matched". The
		// documented tolerance was reported nowhere; ADR-024 §5 says tolerance
		// must be reported, and here the honest report is a rejection naming why.
		if !resourceHasTextField(res) {
			return nil, fmt.Errorf("search is not supported on this resource: it has no string/text fields (search scans only text columns), so the term %q could never match anything", search)
		}
		// The sweep is bounded by the role's allowlist (SEC-5: a hidden text
		// column must not be probeable through the search OR either). A role
		// whose readable set has no text column gets the same honest rejection
		// as a text-less resource.
		if len(qb.searchableFields()) == 0 {
			return nil, fmt.Errorf("search is not available for this role on this resource: none of the fields it may read are string/text (search scans only readable text columns)")
		}
		qb.search = search
	}

	// `?fields=` (MOTOR-FIELDS-S1): the select-list projection, validated
	// against the same field universe as filter/sort (an unknown name is a
	// named 400) and intersected with the role's allowlist (a hidden name is
	// omitted, as on every read).
	fields, err := ParseFields(res, params, allowedFields)
	if err != nil {
		return nil, err
	}
	qb.fields = fields

	return qb, nil
}

// ErrFieldsEmpty is the ParseFields error for a present-but-empty `?fields=`
// (ENG-30's presence rule: an empty form field is a sent parameter).
var errFieldsEmpty = errors.New("fields parameter has an empty value (use fields=a,b,c — id is always included)")

// ParseFields reads the `?fields=` projection (MOTOR-FIELDS-S1) and returns the
// validated column list — `id` first, duplicates collapsed, request order kept
// — or nil when the parameter is absent (the caller keeps `SELECT *`).
//
// Rules, each the engine's existing rule for a NAMED field re-applied here:
//   - absent → nil, nil (no projection; the SQL is byte-identical to before);
//   - present but empty (`?fields=`) → a named error (ENG-30: presence is the
//     gate, an empty form field must not silently mean "everything");
//   - repeated (`?fields=a&fields=b`) → the ENG-17 repeated-parameter error;
//   - an empty ENTRY (`a,,b`, `a,`) → an error naming the extra comma (ENG-24);
//   - a name that is not a declared field of res (nor the implicit `id`) → an
//     error naming it and listing the available set (ADR-024, like sort);
//   - a name outside allowedFields (nil = unrestricted; `id` always allowed)
//     is DROPPED from the projection — the role's allowlist wins, exactly as
//     it does on a read without `fields=` (the column is omitted from the
//     response; RBAC-2 registers that hidden-attempt contract). Not the
//     ErrForbiddenField of filter/sort: that 403 defends against a VALUE
//     oracle (`?filter[hidden][eq]=x` reveals a hidden column by match), and
//     a projection reveals nothing. A 403 here would also break every
//     generic client — the contract is role-agnostic, so the embedded /app
//     cannot know the allowlist before asking (it broke exactly so).
//
// The names are validated identifiers from the schema, so SelectList may quote
// them straight into SQL; values never enter the statement.
func ParseFields(res *schema.ResourceSchema, params url.Values, allowedFields []string) ([]string, error) {
	raw, present, err := singleValue(params, "fields")
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errFieldsEmpty
	}
	out := []string{"id"}
	seen := map[string]bool{"id": true}
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("fields: empty entry in the field list %q — remove the extra comma", raw)
		}
		if name != "id" {
			if _, ok := res.Fields[name]; !ok {
				return nil, fmt.Errorf("unknown field in fields: %s (available: %s)", name, availableFieldNames(res))
			}
			if !fieldAllowed(name, allowedFields) {
				continue // hidden by the role's allowlist: omitted, as on every read
			}
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

// Fields returns the `?fields=` projection (id first) or nil when the request
// asked for every column. Callers that wrap the base SELECT (the ?include=
// builders) need it to project the root row object the same way.
func (qb *QueryBuilder) Fields() []string { return qb.fields }

// SQLProjected is the BaseSelect of the ?include= wrappers (MOTOR-FIELDS-S1):
// the list statement with the given column list in place of the `?fields=`
// projection — nil is the historical `SELECT *`. The wrapper asks for the
// requested fields plus the FK/order columns its joins need; the builder is
// left unchanged.
func (qb *QueryBuilder) SQLProjected(cols []string) (selectQ string, selectArgs []any) {
	saved := qb.fields
	qb.fields = cols
	selectQ, _, selectArgs, _ = qb.SQL()
	qb.fields = saved
	return selectQ, selectArgs
}

// SelectOnly sets the projection from a caller that already knows which
// columns it will use — the GraphQL resolvers, whose selection set IS the
// projection (a GraphQL client can only name fields the generated type has).
// Every name must be a declared field of the resource or `id`; anything else
// is an error so no unvalidated identifier can reach the SQL. nil/empty
// restores `SELECT *`. The role's allowlist is NOT re-checked here: GraphQL's
// contract for a hidden selected field is `null` (the result scrubber), not an
// error, so its callers intersect with the allowlist before calling.
func (qb *QueryBuilder) SelectOnly(cols []string) error {
	if len(cols) == 0 {
		qb.fields = nil
		return nil
	}
	out := []string{"id"}
	seen := map[string]bool{"id": true}
	for _, name := range cols {
		if name != "id" {
			if _, ok := qb.res.Fields[name]; !ok {
				return fmt.Errorf("unknown field in projection: %s (available: %s)", name, availableFieldNames(qb.res))
			}
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	qb.fields = out
	return nil
}

// SelectList renders a projection as a SQL select list: `*` for nil (every
// column — the historical statement, byte for byte), else the quoted column
// names. The names come from ParseFields/SelectOnly, i.e. validated schema
// identifiers, never client text.
func SelectList(cols []string) string {
	if len(cols) == 0 {
		return "*"
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}
	return strings.Join(quoted, ", ")
}

// SelectListAliased is SelectList with every column qualified by a table alias
// (`r.*` / `r."id", r."name"`) for the JOINed relation subroute statement.
func SelectListAliased(alias string, cols []string) string {
	if len(cols) == 0 {
		return alias + ".*"
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = alias + "." + quoteIdent(c)
	}
	return strings.Join(quoted, ", ")
}

// resourceHasTextField reports whether the resource has at least one column
// ?search= could scan.
func resourceHasTextField(res *schema.ResourceSchema) bool {
	for _, fd := range res.Fields {
		if fd.Type == "string" || fd.Type == "text" {
			return true
		}
	}
	return false
}

// UsesCursor reports whether this query paginates by keyset cursor (?after/
// ?before) rather than by page/offset. The list handlers use it to shape meta:
// a cursor request has NO page number, so meta must not invent one (ENG-15 —
// meta.page used to echo the default or even a sent-and-ignored page, actively
// asserting a page the query never used).
func (qb *QueryBuilder) UsesCursor() bool { return qb.afterID != "" || qb.beforeID != "" }

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
		selectQ = fmt.Sprintf("SELECT %s FROM %s%s %s LIMIT $%d",
			SelectList(qb.fields), tbl, whereClause, orderClause, limitIdx)
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

	selectQ = fmt.Sprintf("SELECT %s FROM %s%s %s LIMIT $%d OFFSET $%d",
		SelectList(qb.fields), tbl, whereClause, orderClause, limitIdx, offsetIdx)
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
		case "is_null":
			// No bound parameter — the clause is structural, and the value was
			// normalized to "true"/"false" at parse time. idx must NOT advance.
			if f.value == "true" {
				parts = append(parts, f.field+" IS NULL")
			} else {
				parts = append(parts, f.field+" IS NOT NULL")
			}
			continue
		default:
			parts = append(parts, fmt.Sprintf("%s %s $%d", f.field, filterToSQL(f.op), idx))
			args = append(args, f.value)
		}
		idx++
	}

	if qb.search != "" {
		// The role-readable string fields, sorted for deterministic SQL output.
		strFields := qb.searchableFields()

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

// availableFieldNames lists the fields a caller may name — the declared columns
// plus the implicit `id` primary key — sorted, for an error message. Naming the
// alternatives is the difference between an error a caller can act on and one
// they have to go read the schema for (ADR-024).
//
// The list must mirror what the calling paths actually accept. It once took a
// withID flag because filtering rejected `id` while sorting accepted it, and
// sharing one list produced a message that contradicted itself (`unknown filter
// field: id (available: …, id, …)`). ENG-26 closed that gap — filter, sort and
// the cursors all accept `id` now — so every caller wants the same list again.
func availableFieldNames(res *schema.ResourceSchema) string {
	names := make([]string, 0, len(res.Fields)+1)
	names = append(names, "id")
	for n := range res.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// singleValue reports whether a parameter was SENT, separately from its value —
// url.Values.Get collapses "absent" and "empty" into "", which is exactly the
// distinction ENG-30 is about: `?page=` is a sent parameter with an empty
// value, and treating it as absent silently applied the default.
//
// A REPEATED parameter is an error (ENG-17, ADR-024): vals[0] used to serve the
// FIRST value everywhere, so a client appending a corrected value
// (`?per_page=20&per_page=100`) was served the stale one with nothing said —
// silent last-write-loses, except it was first-write-wins, which nobody
// expects either. Identical duplicates are rejected too: the check is about
// one parameter carrying one meaning, not about adjudicating which duplicate
// was intended.
func singleValue(params url.Values, key string) (string, bool, error) {
	vals, present := params[key]
	if !present || len(vals) == 0 {
		return "", false, nil
	}
	if len(vals) > 1 {
		return "", true, repeatedParamErr(key, len(vals))
	}
	return vals[0], true, nil
}

// repeatedParamErr names a parameter that arrived more than once — the engine
// cannot know which value was meant, so it refuses to guess (ENG-17).
func repeatedParamErr(key string, n int) error {
	return fmt.Errorf("parameter %q was sent %d times: send it once — the engine will not guess which value you meant", key, n)
}

// SingleParam is singleValue for callers OUTSIDE this package that read an
// engine-owned parameter directly (codegen's ?include=): same ENG-17 contract —
// present-or-not plus a named rejection when repeated.
func SingleParam(params url.Values, key string) (string, bool, error) {
	return singleValue(params, key)
}

// listOnlyParams are the engine-owned parameters that only mean something on a
// LIST read: they select, order and window a result SET.
var listOnlyParams = []string{"page", "per_page", "sort", "order", "after", "before", "search", "count"}

// RejectListParams returns a named error when params carry an engine-owned LIST
// parameter (or a filter[/order[ prefix) on a route that would silently discard
// it (NIGHT-SWEEP-S1 audit finding, the ENG-14/ADR-024 class on the
// single-record and SSE surfaces): GET /api/notes/{id}?filter[status][eq]=paid
// used to return the row REGARDLESS of the filter — the caller believed they
// performed a conditional read — while the identical parameter on the list
// route is a named 400. route names the surface for the message
// ("a single-record route", "the event stream"); alsoInclude adds ?include= to
// the rejected set (the event stream embeds nothing). Unknown top-level
// parameters keep their ADR-024 tolerance — only what the engine OWNS is
// checked.
func RejectListParams(params url.Values, route string, alsoInclude bool) error {
	// Sorted (ENG-16 class, SILENT-CORRUPTION-S1): with two rejected
	// parameters the 400 used to name a random one per identical request.
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		owned := strings.HasPrefix(key, filterParamPrefix) || strings.HasPrefix(key, orderParamPrefix)
		if !owned {
			for _, p := range listOnlyParams {
				if key == p {
					owned = true
					break
				}
			}
		}
		if !owned && alsoInclude && key == "include" {
			owned = true
		}
		if owned {
			return fmt.Errorf("%s is not supported on %s: filters, sort, search and pagination select rows on the LIST route — here they would be silently ignored, so they are rejected instead", key, route)
		}
	}
	return nil
}

// countFlagValues maps the accepted ?count= spellings to on/off. Bare `?count`
// (no value — the documented aggregate syntax) is ON and handled by the caller.
var countFlagValues = map[string]bool{
	"true": true, "1": true,
	"false": false, "0": false,
}

// ParseCountFlag reads the ?count flag by VALUE (ENG-23/ENG-18, ADR-024 §4).
// The flag used to be tested for presence only — `?count=false` and `?count=0`
// turned the total ON — and REST disagreed with GraphQL, whose count argument
// is a real Boolean. Accepted: bare `?count` / `?count=` (on — the documented
// syntax), true/1 (on), false/0 (off), case-insensitive; anything else is an
// error naming the value and the accepted set. Absent ⇒ off. A repeated count
// is rejected like every other engine-owned parameter.
func ParseCountFlag(params url.Values) (bool, error) {
	v, present, err := singleValue(params, "count")
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	if v == "" {
		return true, nil // bare ?count — presence as a switch, the documented form
	}
	on, ok := countFlagValues[strings.ToLower(v)]
	if !ok {
		return false, fmt.Errorf("invalid count value %q: use count, count=true or count=false (also 1/0)", v)
	}
	return on, nil
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
