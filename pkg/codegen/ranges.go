package codegen

// Time ranges on the API doors (MOTOR-AGENDA-S1, ADR-039).
//
// Two things live here:
//
//   - RangeConflicts — the ONE query that answers "would this window, in this
//     scope, collide with a row that blocks?". It scopes exactly like a list
//     read of the resource (the role's row condition, the field allowlist), so
//     a conflict is never a row the caller could not otherwise read. Used by
//     the `GET /api/{resource}/conflicts` pre-check (what a UI shows BEFORE
//     writing — advise, not block), by the voice confirmation, and by the
//     describer below.
//   - DescribeRangeConflict — after the database refused a write with the
//     exclusion (the race-safe net), resolve the row it collided with (from
//     the key Postgres reports) and wrap the error so every renderer names it.
//     Error path only; the write hot path is untouched.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/appximo/appximo/pkg/db"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
)

// ConflictQuery is one "would this collide?" question.
type ConflictQuery struct {
	Range      string            // the declared range name
	Start, End string            // RFC 3339 instants (bound with ::timestamptz)
	Scope      map[string]string // scope column → value (a missing scope column = unknown → no scope filter on it)
	ExcludeID  string            // the row being updated, left out of its own conflicts
	Limit      int               // default 20
}

// maxConflictRows bounds the rows a conflict answer carries.
const maxConflictRows = 20

// RangeConflicts runs the conflict query for res.Ranges[q.Range] and returns
// the BLOCKING rows (those under the rule's `when`, or every scheduled row
// when the range has no rule) that overlap [q.Start, q.End), as the role may
// see them. A range without no_overlap still answers — overlap is a question a
// UI can ask about any range; only the constraint is opt-in.
func RangeConflicts(ctx context.Context, tdb *db.TenantDB, pgSchema, resource string, res *schema.ResourceSchema, q ConflictQuery, cond *rbac.WhereCondition, allowed []string) ([]map[string]any, error) {
	r, ok := res.Ranges[q.Range]
	if !ok {
		return nil, fmt.Errorf("unknown range %q (declared: %s)", q.Range, strings.Join(res.RangeNames(), ", "))
	}
	tbl := pgx.Identifier{resource}.Sanitize()
	s, e := pgx.Identifier{r.Start}.Sanitize(), pgx.Identifier{r.End}.Sanitize()
	args := []any{q.Start, q.End}
	parts := []string{
		s + " IS NOT NULL", e + " IS NOT NULL",
		schema.RangeExprSQL(s, e) + " && tstzrange($1::timestamptz, $2::timestamptz, '[)')",
	}
	if r.NoOverlap != nil {
		for _, c := range r.NoOverlap.Scope {
			v, has := q.Scope[c]
			if !has {
				continue
			}
			args = append(args, v)
			parts = append(parts, fmt.Sprintf("%s = $%d", pgx.Identifier{c}.Sanitize(), len(args)))
		}
		if w := r.NoOverlap.When; w != nil {
			parts = append(parts, schema.WhenSQL(*w, res.Fields))
		}
	}
	if q.ExcludeID != "" {
		args = append(args, q.ExcludeID)
		parts = append(parts, fmt.Sprintf("id <> $%d", len(args)))
	}
	if cond != nil {
		if !rbacCondFieldRe.MatchString(cond.Field) {
			return nil, errors.New("invalid rbac condition field")
		}
		args = append(args, cond.Value)
		parts = append(parts, fmt.Sprintf("%s = $%d", cond.Field, len(args)))
	}
	limit := q.Limit
	if limit <= 0 || limit > maxConflictRows {
		limit = maxConflictRows
	}
	sqlText := fmt.Sprintf("SELECT * FROM %s WHERE %s ORDER BY %s LIMIT %d", tbl, strings.Join(parts, " AND "), s, limit)
	rows, err := tdb.QueryTenant(ctx, pgSchema, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := pkghandlers.RowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	schema.PromoteJSONTextRows(recs, res.JSONTextColumns())
	if len(allowed) > 0 {
		for i, rec := range recs {
			recs[i] = pkghandlers.FilterFields(rec, allowed)
		}
	}
	return recs, nil
}

// rangeForConstraint resolves an engine exclusion symbol to the declared range
// that generated it (by exact symbol, else by family — a deployed schema whose
// rule changed still names the range).
func rangeForConstraint(resource string, res *schema.ResourceSchema, constraint string) (string, schema.RangeDef, bool) {
	for _, name := range res.RangeNames() {
		r := res.Ranges[name]
		if r.NoOverlap != nil && schema.RangeExclusionName(resource, name, r) == constraint {
			return name, r, true
		}
	}
	for _, name := range res.RangeNames() {
		if strings.HasPrefix(constraint, "excl_"+resource+"_"+name+"_") {
			return name, res.Ranges[name], true
		}
	}
	return "", schema.RangeDef{}, false
}

// DescribeRangeConflict turns a write's database error into the engine's
// named vocabulary WHEN it is one of the two range constraints; every other
// error is returned untouched. Error path only. cond/allowed scope the
// colliding row exactly as a read would: a row the role may not see is
// reported by its bounds only, never by its fields.
func DescribeRangeConflict(ctx context.Context, tdb *db.TenantDB, pgSchema, resource string, res *schema.ResourceSchema, err error, cond *rbac.WhereCondition, allowed []string) error {
	if err == nil || res == nil || len(res.Ranges) == 0 {
		return err
	}
	if constraint, ok := db.RangeOrderViolation(err); ok {
		field := constraint
		for _, name := range res.RangeNames() {
			if schema.RangeCheckName(resource, name) == constraint {
				field = res.Ranges[name].End
			}
		}
		return &pkghandlers.RangeOrderError{Field: field, Cause: err}
	}
	constraint, _, _, ok := db.ExclusionViolation(err)
	if !ok {
		return err
	}
	v := pkghandlers.ClassifyWriteError(err) // parses the existing key from the Detail
	c := pkghandlers.RangeConflict{Constraint: constraint}
	if v.Conflict != nil {
		c = *v.Conflict
	}
	name, r, known := rangeForConstraint(resource, res, constraint)
	if known {
		c.Range = name
	}
	if known && c.ExistingStart != "" && c.ExistingEnd != "" {
		// The colliding row is the one whose (scope, range) equals the existing
		// key Postgres reported: an exact match, an index lookup.
		tbl := pgx.Identifier{resource}.Sanitize()
		s, e := pgx.Identifier{r.Start}.Sanitize(), pgx.Identifier{r.End}.Sanitize()
		args := []any{c.ExistingStart, c.ExistingEnd}
		parts := []string{schema.RangeExprSQL(s, e) + " = tstzrange($1::timestamptz, $2::timestamptz, '[)')"}
		if r.NoOverlap != nil {
			for _, sc := range r.NoOverlap.Scope {
				if val, has := c.ExistingScope[sc]; has {
					args = append(args, val)
					parts = append(parts, fmt.Sprintf("%s = $%d", pgx.Identifier{sc}.Sanitize(), len(args)))
				}
			}
		}
		if cond != nil && rbacCondFieldRe.MatchString(cond.Field) {
			args = append(args, cond.Value)
			parts = append(parts, fmt.Sprintf("%s = $%d", cond.Field, len(args)))
		}
		rows, qerr := tdb.QueryTenant(ctx, pgSchema, fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT %d", tbl, strings.Join(parts, " AND "), maxConflictRows), args...)
		if qerr == nil {
			recs, rerr := pkghandlers.RowsToMaps(rows)
			rows.Close()
			if rerr == nil {
				schema.PromoteJSONTextRows(recs, res.JSONTextColumns())
				if len(allowed) > 0 {
					for i, rec := range recs {
						recs[i] = pkghandlers.FilterFields(rec, allowed)
					}
				}
				c.Conflicts = recs
			}
		}
	}
	if c.Conflicts == nil {
		c.Conflicts = []map[string]any{}
	}
	return &pkghandlers.RangeConflictError{Conflict: c, Cause: err}
}

// registerConflictsRoute mounts GET /api/{resource}/conflicts for a resource
// that declares at least one range: the pre-check a UI or a voice flow runs
// BEFORE writing ("¿esto choca con algo?"). Authorized as a READ of the
// resource (the RBAC middleware evaluates the path segment) and scoped like
// one. Parameters: range (optional when the resource declares exactly one),
// start, end (RFC 3339), each scope column by name, exclude_id.
func registerConflictsRoute(r chi.Router, name string, res schema.ResourceSchema, tdb *db.TenantDB) {
	if len(res.Ranges) == 0 {
		return
	}
	r.Get("/api/"+name+"/conflicts", func(w http.ResponseWriter, req *http.Request) {
		tc := tenant.MustFromCtx(req.Context())
		evalResult := rbac.EvalResultFromCtx(req.Context())
		var cond *rbac.WhereCondition
		var allowed []string
		if evalResult != nil {
			cond, allowed = evalResult.Condition, evalResult.AllowedFields
		}
		sres := readSurface(req.Context(), tc.ID, name, &res)
		params := req.URL.Query()
		q := ConflictQuery{Range: params.Get("range"), Start: params.Get("start"), End: params.Get("end"), ExcludeID: params.Get("exclude_id"), Scope: map[string]string{}}
		names := sres.RangeNames()
		if q.Range == "" {
			if len(names) != 1 {
				writeJSONErr(w, http.StatusBadRequest, "range: name the range to check (declared: "+strings.Join(names, ", ")+")")
				return
			}
			q.Range = names[0]
		}
		rg, ok := sres.Ranges[q.Range]
		if !ok {
			writeJSONErr(w, http.StatusBadRequest, "unknown range "+q.Range+" (declared: "+strings.Join(names, ", ")+")")
			return
		}
		if q.Start == "" || q.End == "" {
			writeJSONErr(w, http.StatusBadRequest, "start and end are required (RFC 3339 instants)")
			return
		}
		st, serr := parseInstantParam(q.Start)
		en, eerr := parseInstantParam(q.End)
		if serr != nil || eerr != nil {
			writeJSONErr(w, http.StatusBadRequest, "start/end must be RFC 3339 instants (e.g. 2026-09-22T16:00:00-05:00)")
			return
		}
		if !en.After(st) {
			writeJSONErr(w, http.StatusBadRequest, "end must be after start")
			return
		}
		wouldBlock := true
		if rg.NoOverlap != nil {
			for _, c := range rg.NoOverlap.Scope {
				if v := params.Get(c); v != "" {
					q.Scope[c] = v
				}
			}
			if wv := rg.NoOverlap.When; wv != nil {
				if v := params.Get(wv.Field); v != "" {
					wouldBlock = schema.WhenHolds(*wv, map[string]any{wv.Field: coerceParam(v, sres.Fields[wv.Field])})
				}
			}
		}
		recs, err := RangeConflicts(req.Context(), tdb, tc.PGSchema, name, sres, q, cond, allowed)
		if err != nil {
			if strings.HasPrefix(err.Error(), "unknown range") {
				writeJSONErr(w, http.StatusBadRequest, err.Error())
				return
			}
			writeDBErr(w, req, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		serverTiming(w, req)
		pkghandlers.WriteJSON(w, map[string]any{ //nolint:errcheck
			"range":       q.Range,
			"start":       st.UTC().Format(rfc3339Z),
			"end":         en.UTC().Format(rfc3339Z),
			"enforced":    rg.NoOverlap != nil,
			"would_block": wouldBlock && rg.NoOverlap != nil,
			"conflicts":   recs,
		})
	})
}

const rfc3339Z = "2006-01-02T15:04:05Z07:00"

// coerceParam reads a query-string literal as the field's type for the
// `when` evaluation (bool / number / string).
func coerceParam(v string, fd schema.FieldDef) any {
	switch fd.Type {
	case "bool":
		switch strings.ToLower(v) {
		case "true", "1", "t", "yes", "on":
			return true
		case "false", "0", "f", "no", "off":
			return false
		}
		return v
	case "int", "int64", "float64":
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return v
}

// parseInstantParam accepts the RFC 3339 forms a query string carries.
func parseInstantParam(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

// rbacCond / rbacAllowed read the row condition and field allowlist off an
// evaluation result that may be nil (a test without the RBAC middleware).
func rbacCond(ev *rbac.EvalResult) *rbac.WhereCondition {
	if ev == nil {
		return nil
	}
	return ev.Condition
}

func rbacAllowed(ev *rbac.EvalResult) []string {
	if ev == nil {
		return nil
	}
	return ev.AllowedFields
}
