package handlers

// Time-range conflicts (MOTOR-AGENDA-S1, ADR-039): the client-facing shape of a
// write that collided with a declared no-overlap rule, and the parser that
// turns Postgres's exclusion_violation Detail into the EXISTING key (scope
// values + the exact half-open range) so the engine can name the row the
// write collided with — on every door, from the one classifier.

import (
	"errors"
	"strings"
)

// RangeConflict is the classified verdict of an exclusion violation: which
// constraint, and — when the Detail could be parsed — the existing row's key.
type RangeConflict struct {
	// Constraint is the engine symbol (excl_<table>_<range>_<hash>).
	Constraint string
	// Range is the declared range name (filled by the describer that knows the
	// schema; empty when only the raw driver error was classified).
	Range string
	// ExistingScope maps each scope column to the colliding row's value text.
	ExistingScope map[string]string
	// ExistingStart / ExistingEnd are the colliding row's bounds as Postgres
	// printed them ("2026-09-22 21:00:00+00"), empty when unparseable.
	ExistingStart, ExistingEnd string
	// Conflicts are the colliding rows as the caller's role may see them
	// (filled by the describer; nil when the verdict came from the raw error).
	Conflicts []map[string]any
}

// RangeConflictError is the error a write site wraps a 23P01 into once it has
// resolved the colliding row(s) against the tenant database. ClassifyWriteError
// recognizes it before the raw-driver ladder, so every renderer names the row.
type RangeConflictError struct {
	Conflict RangeConflict
	Cause    error
}

func (e *RangeConflictError) Error() string { return RangeConflictMessage(e.Conflict) }
func (e *RangeConflictError) Unwrap() error { return e.Cause }

// RangeOrderError is the described form of the range-order CHECK violation:
// the describer replaces the constraint symbol with the range's END field so
// the S44 422 addresses the field the client can act on.
type RangeOrderError struct {
	Field string
	Cause error
}

func (e *RangeOrderError) Error() string { return e.Field + " " + RangeOrderMessage }
func (e *RangeOrderError) Unwrap() error { return e.Cause }

// RangeConflictMessage is the one human message every renderer uses.
func RangeConflictMessage(c RangeConflict) string {
	name := c.Range
	if name == "" {
		name = c.Constraint
	}
	if c.ExistingStart != "" && c.ExistingEnd != "" {
		return "time range " + name + " overlaps an existing row (" + pgTimestampToRFC3339(c.ExistingStart) + " – " + pgTimestampToRFC3339(c.ExistingEnd) + ")"
	}
	return "time range " + name + " overlaps an existing row"
}

// RangeConflictBody is the JSON body of the 409 (REST + batch share it):
// {"error":"time_range_conflict","range":…,"message":…,"conflicts":[…]}.
func RangeConflictBody(c RangeConflict) map[string]any {
	body := map[string]any{
		"error":   "time_range_conflict",
		"message": RangeConflictMessage(c),
	}
	if c.Range != "" {
		body["range"] = c.Range
	}
	if c.ExistingStart != "" && c.ExistingEnd != "" {
		body["existing"] = map[string]any{"start": pgTimestampToRFC3339(c.ExistingStart), "end": pgTimestampToRFC3339(c.ExistingEnd)}
	}
	if c.Conflicts != nil {
		body["conflicts"] = c.Conflicts
	} else {
		body["conflicts"] = []map[string]any{}
	}
	return body
}

// RangeOrderRule / RangeOrderMessage are the 422 vocabulary of the CHECK
// (chk_<table>_<range>_order): the same rule the declarative validator names
// before any SQL when both bounds travel in the body.
const (
	RangeOrderRule    = "range_order"
	RangeOrderMessage = "must be after the range's start"
)

// parseExclusionKey turns the Detail's "(col, tstzrange(a, b, '[)'::text))" +
// "(v1, [\"x\",\"y\"))" texts into the scope values and the range bounds. It
// tolerates any number of scope columns before the range expression and gives
// up (ok=false) on anything it does not understand — the message then names
// the range without bounds, never a wrong row.
func parseExclusionKey(cols, vals string) (scope map[string]string, start, end string, ok bool) {
	colNames := splitTopLevel(cols)
	values := splitTopLevel(vals)
	if len(colNames) == 0 || len(colNames) != len(values) {
		return nil, "", "", false
	}
	scope = map[string]string{}
	for i, c := range colNames {
		c = strings.TrimSpace(c)
		v := strings.TrimSpace(values[i])
		if strings.HasPrefix(c, "tstzrange(") {
			// ["2026-09-22 21:00:00+00","2026-09-22 22:00:00+00")
			inner := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(v, "["), "("), ")"), "]")
			parts := splitTopLevel(inner)
			if len(parts) != 2 {
				return nil, "", "", false
			}
			start = strings.Trim(strings.TrimSpace(parts[0]), `"`)
			end = strings.Trim(strings.TrimSpace(parts[1]), `"`)
			continue
		}
		scope[c] = v
	}
	if start == "" || end == "" {
		return nil, "", "", false
	}
	return scope, start, end, true
}

// splitTopLevel splits on commas that are outside parentheses, brackets and
// double quotes.
func splitTopLevel(s string) []string {
	var out []string
	depth := 0
	inQ := false
	startIdx := 0
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '"':
			inQ = !inQ
		case inQ:
		case ch == '(' || ch == '[':
			depth++
		case ch == ')' || ch == ']':
			depth--
		case ch == ',' && depth == 0:
			out = append(out, s[startIdx:i])
			startIdx = i + 1
		}
	}
	out = append(out, s[startIdx:])
	return out
}

// pgTimestampToRFC3339 rewrites Postgres's key text ("2026-09-22 21:00:00+00")
// into the RFC 3339 form the API speaks. Anything unexpected is returned as is.
func pgTimestampToRFC3339(s string) string {
	if len(s) < 19 || s[10] != ' ' {
		return s
	}
	out := s[:10] + "T" + s[11:]
	// "+00" → "+00:00"
	if n := len(out); n >= 3 && (out[n-3] == '+' || out[n-3] == '-') {
		out += ":00"
	}
	return out
}

// classifyRangeErrors is the range branch of the write-error ladder: a wrapped
// RangeConflictError (already described) or the raw 23P01 / 23514.
func classifyRangeErrors(err error) (WriteErrorVerdict, bool) {
	var rc *RangeConflictError
	if errors.As(err, &rc) {
		return WriteErrorVerdict{Kind: WriteErrRangeConflict, Field: rc.Conflict.Constraint, Message: RangeConflictMessage(rc.Conflict), Conflict: &rc.Conflict}, true
	}
	var ro *RangeOrderError
	if errors.As(err, &ro) {
		return WriteErrorVerdict{Kind: WriteErrRangeOrder, Field: ro.Field, Message: RangeOrderMessage}, true
	}
	return WriteErrorVerdict{}, false
}

// rangeOrderField derives the END field's name from an order CHECK symbol when
// the describer did not (chk_<table>_<range>_order → "<range>"): the range
// name is what the caller can act on; the describer replaces it with the real
// end field when the schema is at hand.
func rangeOrderField(constraint string) string {
	s := strings.TrimSuffix(strings.TrimPrefix(constraint, "chk_"), "_order")
	if i := strings.Index(s, "_"); i > 0 && i < len(s)-1 {
		return s[i+1:]
	}
	return s
}
