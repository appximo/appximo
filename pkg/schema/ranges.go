package schema

// Time ranges with no-overlap (MOTOR-AGENDA-S1, ADR-039).
//
// A resource declares a RANGE over two of its own `time` fields — the moment a
// row starts and the moment it ends — and, optionally, that rows of the same
// SCOPE may not overlap:
//
//	"ranges": {
//	  "horario": {
//	    "start": "inicio", "end": "fin",
//	    "no_overlap": { "scope": ["dueno_id"], "when": { "field": "ocupa", "op": "eq", "val": true } }
//	  }
//	}
//
// The API JSON stays the two natural fields (`inicio`, `fin`); the RANGE is a
// name over them. In Postgres the range is `tstzrange(start, end, '[)')` —
// half-open, so 4–5 and 5–6 do NOT overlap — and the no-overlap rule is a real
// `EXCLUDE USING gist (<scope> WITH =, tstzrange(start,end,'[)') WITH &&)
// WHERE (<when>)` constraint (btree_gist for the `=` on the scope columns),
// which is correct under concurrency in a way an application check cannot be:
// two simultaneous inserts that would overlap → one wins, the other gets a
// clean 409 naming the row it collides with. The engine also adds a CHECK
// that start < end (a named 422 on every write door).
//
// Not a new column type, deliberately: two `time` columns already flow through
// every door (filters, sort, ?fields=, GraphQL scalars, OpenAPI, the /app
// datetime inputs, Studio, `explain`, the generator) — what a range ADDS is
// the invariant, the constraint, two filter operators on the range NAME
// (`overlaps`, `contains`), the conflicts endpoint and the voice vocabulary.
// A `tstzrange` column would expose a Postgres literal in the JSON and demand
// a compound widget everywhere. Recurrences (v2) materialize into rows with
// start/end — nothing here closes that door.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RangeDef is one named time range of a resource: two `time` fields of the
// same resource plus, optionally, the no-overlap rule.
type RangeDef struct {
	// Start / End name the resource's own `time` fields. A row with either
	// NULL is "unscheduled" — it takes part in no overlap check and matches no
	// overlaps/contains filter (never an unbounded range).
	Start string `json:"start"`
	End   string `json:"end"`
	// NoOverlap, when declared, forbids two rows of the same scope from
	// overlapping. Absent = the range is a named pair (filters, voice, order
	// check) with no exclusion constraint.
	NoOverlap *NoOverlapDef `json:"no_overlap,omitempty"`
	// TimezoneField optionally names a string field of the resource holding
	// the IANA zone the range was declared in (America/Bogota). It is stored
	// for display and for future recurrence rules; the instants themselves are
	// always timestamptz. The field must declare `format: "timezone"` so an
	// offset or a made-up zone is a 422, never data.
	TimezoneField string `json:"timezone_field,omitempty"`
	// DefaultDuration is what a range lasts when only its start is given —
	// «a las 10» by voice, a form with one datetime — as a duration ("1h",
	// "30m"). Default 1h. The engine fills the end from it before writing;
	// it never guesses a domain.
	DefaultDuration string `json:"default_duration,omitempty"`
}

// DefaultDurationValue is the parsed DefaultDuration (1h when absent).
func (r RangeDef) DefaultDurationValue() time.Duration {
	if r.DefaultDuration == "" {
		return time.Hour
	}
	d, err := ParseWorkflowDuration(r.DefaultDuration)
	if err != nil {
		return time.Hour
	}
	return d
}

// NoOverlapDef is the exclusion rule of a range.
type NoOverlapDef struct {
	// Scope lists the columns that must be EQUAL for two rows to collide
	// (`dueno_id` for a personal agenda, `sala_id` for a room). Empty = every
	// row of the table shares one scope (a single room, a single person).
	Scope []string `json:"scope,omitempty"`
	// When restricts the rule to the rows that BLOCK: a tentative or cancelled
	// row is excluded from the constraint (a partial EXCLUDE ... WHERE). Absent
	// = every scheduled row blocks.
	When *WhenDef `json:"when,omitempty"`
}

// WhenDef is the partial condition of a no-overlap rule: `{"field": "ocupa",
// "op": "eq", "val": true}` or `{"field": "estado", "op": "ne", "val":
// "cancelada"}`. Ops are exactly eq | ne; the value is a literal of the field's
// type (an enum member when the field declares one).
type WhenDef struct {
	Field string `json:"field"`
	Op    string `json:"op,omitempty"`
	Val   any    `json:"val"`
}

// validWhenOps is the closed set of ops a `when` condition accepts.
var validWhenOps = map[string]bool{"eq": true, "ne": true}

// FormatTimezone is the `format` value that validates a string field as an
// IANA zone name (America/Bogota), never a fixed offset.
const FormatTimezone = "timezone"

// validateRanges checks a resource's `ranges` block at load: names, the two
// fields (existing, `time`, distinct, not auto), the scope columns, the when
// condition (existing field, eq|ne, literal of the field's type), the
// timezone field, and that a range name shadows no field (it is a filter name
// on the API — `?filter[horario][overlaps]=`).
func validateRanges(resPrefix, resName string, res ResourceSchema) []ValidationError {
	var errs []ValidationError
	if len(res.Ranges) == 0 {
		return nil
	}
	names := make([]string, 0, len(res.Ranges))
	for n := range res.Ranges {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		r := res.Ranges[name]
		p := resPrefix + ".ranges." + name
		if !fieldNameRe.MatchString(name) {
			errs = append(errs, ValidationError{Field: p, Rule: "invalid_range_name", Got: name,
				Message: fmt.Sprintf("invalid range name %q: must match ^[a-z][a-z0-9_]*$", name),
				Fix:     "name the range like a field: lowercase, letters, digits and _"})
			continue
		}
		if _, clash := res.Fields[name]; clash {
			errs = append(errs, ValidationError{Field: p, Rule: "range_shadows_field", Got: name,
				Message: fmt.Sprintf("range %q has the same name as a field: a range is a filter name on the API (?filter[%s][overlaps]=) and cannot shadow a column", name, name),
				Fix:     "rename the range (e.g. \"horario\", \"slot\") or the field"})
		}
		for _, side := range []struct{ key, field string }{{"start", r.Start}, {"end", r.End}} {
			if side.field == "" {
				errs = append(errs, ValidationError{Field: p + "." + side.key, Rule: "missing_range_field",
					Message: fmt.Sprintf("range %q needs %q: the name of a time field of %s", name, side.key, resName),
					Fix:     "declare both start and end, each an existing field of type \"time\""})
				continue
			}
			fd, ok := res.Fields[side.field]
			if !ok {
				errs = append(errs, ValidationError{Field: p + "." + side.key, Rule: "unknown_range_field", Got: side.field,
					Message: fmt.Sprintf("range %q: %s field %q is not a field of %s", name, side.key, side.field, resName),
					Fix:     "name an existing field of the same resource"})
				continue
			}
			if fd.Type != "time" {
				errs = append(errs, ValidationError{Field: p + "." + side.key, Rule: "range_field_not_time", Got: fd.Type, Expected: []string{"time"},
					Message: fmt.Sprintf("range %q: %s field %q is %q, a range needs two \"time\" fields", name, side.key, side.field, fd.Type),
					Fix:     "declare the field as {\"type\": \"time\"}"})
			}
			if fd.Auto.Enabled() {
				errs = append(errs, ValidationError{Field: p + "." + side.key, Rule: "range_field_auto", Got: side.field,
					Message: fmt.Sprintf("range %q: %s field %q is engine-managed (auto) — a range is what the client schedules, never a creation/update stamp", name, side.key, side.field),
					Fix:     "use a plain time field the client writes"})
			}
		}
		if r.Start != "" && r.Start == r.End {
			errs = append(errs, ValidationError{Field: p + ".end", Rule: "range_same_field", Got: r.End,
				Message: fmt.Sprintf("range %q: start and end are the same field %q", name, r.End),
				Fix:     "a range spans two different time fields"})
		}
		if r.DefaultDuration != "" {
			if _, err := ParseWorkflowDuration(r.DefaultDuration); err != nil {
				errs = append(errs, ValidationError{Field: p + ".default_duration", Rule: "invalid_duration", Got: r.DefaultDuration,
					Message: fmt.Sprintf("range %q: default_duration %q: %v (use 30m, 1h, 1h30m)", name, r.DefaultDuration, err)})
			}
		}
		if r.TimezoneField != "" {
			fd, ok := res.Fields[r.TimezoneField]
			switch {
			case !ok:
				errs = append(errs, ValidationError{Field: p + ".timezone_field", Rule: "unknown_range_field", Got: r.TimezoneField,
					Message: fmt.Sprintf("range %q: timezone_field %q is not a field of %s", name, r.TimezoneField, resName),
					Fix:     "name an existing string field declaring \"format\": \"timezone\""})
			case fd.Type != "string" && fd.Type != "text":
				errs = append(errs, ValidationError{Field: p + ".timezone_field", Rule: "range_timezone_not_string", Got: fd.Type,
					Message: fmt.Sprintf("range %q: timezone_field %q must be a string holding an IANA zone name", name, r.TimezoneField),
					Fix:     "declare it {\"type\": \"string\", \"format\": \"timezone\", \"default\": \"America/Bogota\"}"})
			case fd.Format != FormatTimezone:
				errs = append(errs, ValidationError{Field: p + ".timezone_field", Rule: "range_timezone_unvalidated", Got: fd.Format,
					Message: fmt.Sprintf("range %q: timezone_field %q must declare \"format\": \"timezone\" so a fixed offset or a made-up zone is refused at write time", name, r.TimezoneField),
					Fix:     "add \"format\": \"timezone\" to the field"})
			}
		}
		if r.NoOverlap == nil {
			continue
		}
		seenScope := map[string]bool{}
		for i, col := range r.NoOverlap.Scope {
			sp := fmt.Sprintf("%s.no_overlap.scope[%d]", p, i)
			fd, ok := res.Fields[col]
			if !ok {
				errs = append(errs, ValidationError{Field: sp, Rule: "unknown_scope_field", Got: col,
					Message: fmt.Sprintf("range %q: scope column %q is not a field of %s", name, col, resName),
					Fix:     "scope lists existing fields whose EQUAL values put two rows in the same agenda/room/queue"})
				continue
			}
			if col == r.Start || col == r.End {
				errs = append(errs, ValidationError{Field: sp, Rule: "scope_is_range_field", Got: col,
					Message: fmt.Sprintf("range %q: scope column %q is the range itself", name, col),
					Fix:     "scope names the owner/resource columns, not the start or end"})
			}
			if fd.Type == "json" || fd.Type == "jsonb" || fd.Type == "text" {
				errs = append(errs, ValidationError{Field: sp, Rule: "scope_type_unsupported", Got: fd.Type,
					Message: fmt.Sprintf("range %q: scope column %q is %q — a scope column must be comparable with = in a gist index (uuid, string, int, int64, bool, time, file)", name, col, fd.Type),
					Fix:     "scope by an id, a code or a flag"})
			}
			if seenScope[col] {
				errs = append(errs, ValidationError{Field: sp, Rule: "scope_duplicate", Got: col,
					Message: fmt.Sprintf("range %q: scope column %q is listed twice", name, col)})
			}
			seenScope[col] = true
		}
		if w := r.NoOverlap.When; w != nil {
			wp := p + ".no_overlap.when"
			if w.Field == "" {
				errs = append(errs, ValidationError{Field: wp + ".field", Rule: "missing_when_field",
					Message: fmt.Sprintf("range %q: when needs a field", name),
					Fix:     "e.g. {\"field\": \"ocupa\", \"op\": \"eq\", \"val\": true}"})
			} else if fd, ok := res.Fields[w.Field]; !ok {
				errs = append(errs, ValidationError{Field: wp + ".field", Rule: "unknown_when_field", Got: w.Field,
					Message: fmt.Sprintf("range %q: when field %q is not a field of %s", name, w.Field, resName)})
			} else {
				op := w.Op
				if op == "" {
					op = "eq"
				}
				if !validWhenOps[op] {
					errs = append(errs, ValidationError{Field: wp + ".op", Rule: "invalid_when_op", Got: w.Op, Expected: []string{"eq", "ne"},
						Message: fmt.Sprintf("range %q: when op %q must be eq or ne", name, w.Op)})
				}
				if msg := whenValueMismatch(fd, w.Val); msg != "" {
					errs = append(errs, ValidationError{Field: wp + ".val", Rule: "invalid_when_value", Got: fmt.Sprint(w.Val),
						Message: fmt.Sprintf("range %q: when value %s", name, msg),
						Fix:     "val is a literal of the field's type (an enum member when the field declares one)"})
				}
			}
		}
	}
	return errs
}

// whenValueMismatch reports why v is not a literal of fd's type ("" = fine).
func whenValueMismatch(fd FieldDef, v any) string {
	if v == nil {
		return "must not be null (compare with a literal)"
	}
	switch fd.Type {
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("%v is not a boolean", v)
		}
	case "string", "text":
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("%v is not a string", v)
		}
		if len(fd.Enum) > 0 {
			for _, e := range fd.Enum {
				if e == s {
					return ""
				}
			}
			return fmt.Sprintf("%q is not one of the enum values %s", s, joinQuoted(fd.Enum))
		}
	case "int", "int64", "float64":
		if _, ok := AsFloat64(v); !ok {
			return fmt.Sprintf("%v is not a number", v)
		}
	case "uuid", "file":
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("%v is not an id", v)
		}
	default:
		return fmt.Sprintf("a when condition cannot compare a %q field", fd.Type)
	}
	return ""
}

// HasRanges reports whether any resource declares a range (the boot-time
// switch that keeps every range-free schema on the byte-identical path).
func (s *APISchema) HasRanges() bool {
	for _, r := range s.Resources {
		if len(r.Ranges) > 0 {
			return true
		}
	}
	return false
}

// HasNoOverlap reports whether any resource declares a no-overlap rule — the
// condition under which the tenant's database needs btree_gist.
func (s *APISchema) HasNoOverlap() bool {
	for _, r := range s.Resources {
		for _, rg := range r.Ranges {
			if rg.NoOverlap != nil {
				return true
			}
		}
	}
	return false
}

// RangeNames returns the resource's range names, sorted.
func (r ResourceSchema) RangeNames() []string {
	if len(r.Ranges) == 0 {
		return nil
	}
	names := make([]string, 0, len(r.Ranges))
	for n := range r.Ranges {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// RangeExprSQL is the Postgres range expression of a declared range over
// already-sanitized column identifiers: tstzrange(start, end, '[)').
func RangeExprSQL(startIdent, endIdent string) string {
	return "tstzrange(" + startIdent + ", " + endIdent + ", '[)')"
}

// RangeCheckName is the CHECK constraint that keeps start < end.
func RangeCheckName(table, rangeName string) string {
	return "chk_" + table + "_" + rangeName + "_order"
}

// RangeCheckExpression is the predicate text of that CHECK exactly as Postgres
// canonicalizes it back through pg_get_constraintdef (so the migration diff
// sees an unchanged constraint as unchanged): NULL on either side is allowed
// (an unscheduled row), otherwise start must precede end.
func RangeCheckExpression(start, end string) string {
	// Unquoted identifiers, triple-wrapped: byte-identical to what Postgres
	// stores for `CHECK ((a IS NULL) OR (b IS NULL) OR (a < b))` (verified
	// live on PG16: "CHECK (((a IS NULL) OR (b IS NULL) OR (a < b)))").
	return "(((" + start + " IS NULL) OR (" + end + " IS NULL) OR (" + start + " < " + end + ")))"
}

// RangeExclusionName is the EXCLUDE constraint's symbol. It carries a short
// hash of the rule's DEFINITION (fields, scope, when), so a changed rule is a
// different symbol — the migration drops the old and adds the new — while an
// unchanged rule diffs as unchanged without any canonical-text comparison.
func RangeExclusionName(table, rangeName string, r RangeDef) string {
	h := sha256.Sum256([]byte(rangeDefinitionKey(r)))
	return "excl_" + table + "_" + rangeName + "_" + hex.EncodeToString(h[:4])
}

func rangeDefinitionKey(r RangeDef) string {
	var b strings.Builder
	b.WriteString(r.Start + "|" + r.End + "|")
	if r.NoOverlap != nil {
		b.WriteString(strings.Join(r.NoOverlap.Scope, ","))
		if w := r.NoOverlap.When; w != nil {
			raw, _ := json.Marshal(w.Val)
			b.WriteString("|" + w.Field + "|" + w.Op + "|" + string(raw))
		}
	}
	return b.String()
}

// RangeExclusionDefinition renders the constraint body (what follows ADD
// CONSTRAINT <name>): EXCLUDE USING gist (scope WITH =, range WITH &&) WHERE
// (start IS NOT NULL AND end IS NOT NULL [AND when]). Unscheduled rows (a NULL
// bound) never take part — tstzrange with a NULL bound would be unbounded and
// collide with everything.
func RangeExclusionDefinition(r RangeDef, fields map[string]FieldDef) string {
	var elems []string
	if r.NoOverlap != nil {
		for _, c := range r.NoOverlap.Scope {
			elems = append(elems, pgIdent(c)+" WITH =")
		}
	}
	elems = append(elems, RangeExprSQL(pgIdent(r.Start), pgIdent(r.End))+" WITH &&")
	where := pgIdent(r.Start) + " IS NOT NULL AND " + pgIdent(r.End) + " IS NOT NULL"
	if r.NoOverlap != nil && r.NoOverlap.When != nil {
		where += " AND " + WhenSQL(*r.NoOverlap.When, fields)
	}
	return "EXCLUDE USING gist (" + strings.Join(elems, ", ") + ") WHERE (" + where + ")"
}

// WhenSQL renders a when condition as a literal predicate (the value is a
// load-validated literal of the field's type, quoted here — never client
// input; the constraint text has no bind parameters).
func WhenSQL(w WhenDef, fields map[string]FieldDef) string {
	return WhenSQLPrefixed(w, fields, "")
}

// WhenSQLPrefixed is WhenSQL with a table alias prefix ("a." → (a."col" = lit)).
func WhenSQLPrefixed(w WhenDef, fields map[string]FieldDef, prefix string) string {
	op := "="
	if w.Op == "ne" {
		op = "<>"
	}
	return "(" + prefix + pgIdent(w.Field) + " " + op + " " + pgLiteral(w.Val, fields[w.Field]) + ")"
}

// WhenHolds evaluates a when condition over a decoded row (used by the conflict
// pre-check to know whether the row being written would block at all).
func WhenHolds(w WhenDef, row map[string]any) bool {
	v, present := row[w.Field]
	if !present || v == nil {
		return false // NULL compares to nothing: the row does not block
	}
	eq := literalEqual(v, w.Val)
	if w.Op == "ne" {
		return !eq
	}
	return eq
}

func literalEqual(a, b any) bool {
	if fa, ok := AsFloat64(a); ok {
		if fb, ok2 := AsFloat64(b); ok2 {
			return fa == fb
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// pgIdent double-quotes a validated identifier for constraint text.
func pgIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// pgLiteral renders a load-validated literal for constraint text.
func pgLiteral(v any, fd FieldDef) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		lit := "'" + strings.ReplaceAll(x, "'", "''") + "'"
		if fd.Type == "uuid" || fd.Type == "file" {
			return lit + "::uuid"
		}
		return lit
	default:
		if f, ok := AsFloat64(v); ok {
			if IsIntegral(f) {
				return fmt.Sprintf("%d", int64(f))
			}
			return fmt.Sprintf("%g", f)
		}
		return "'" + strings.ReplaceAll(fmt.Sprint(v), "'", "''") + "'"
	}
}

// ValidateTimezoneName is the `format: "timezone"` checker: an IANA zone name
// that Go can load. Fixed offsets ("+05:00", "UTC-5") are refused on purpose —
// a zone name carries DST, an offset does not.
func ValidateTimezoneName(s string) bool {
	if s == "" || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.ContainsAny(s, " \t") {
		return false
	}
	if strings.HasPrefix(s, "UTC+") || strings.HasPrefix(s, "UTC-") || strings.HasPrefix(s, "GMT+") || strings.HasPrefix(s, "GMT-") {
		return false
	}
	_, err := time.LoadLocation(s)
	return err == nil
}

// RangeOrderViolations checks a WRITE body against the resource's ranges: when
// both bounds are present as parseable timestamps and end <= start, the
// range's end field is reported with rule "range_order" (the same 422 the DB
// CHECK would produce, but named before any SQL). A body carrying one bound
// only is left to the database CHECK (the other bound is in the row).
func RangeOrderViolations(res ResourceSchema, body map[string]any) []FieldRuleError {
	if len(res.Ranges) == 0 {
		return nil
	}
	var errs []FieldRuleError
	for _, name := range res.RangeNames() {
		r := res.Ranges[name]
		st, sok := parseTimeValue(body[r.Start])
		en, eok := parseTimeValue(body[r.End])
		if !sok || !eok {
			continue
		}
		if !en.After(st) {
			errs = append(errs, FieldRuleError{Field: r.End, Rule: "range_order",
				Message: fmt.Sprintf("must be after %s (range %q)", r.Start, name)})
		}
	}
	return errs
}

// parseTimeValue accepts the RFC3339 forms a JSON body carries for a time
// field; anything else (a Postgres-only literal, a token) is "not decidable
// here" and defers to the database.
func parseTimeValue(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, x); err == nil {
				return t, true
			}
		}
	}
	return time.Time{}, false
}
