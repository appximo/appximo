package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// A `json` field holds a JSON VALUE (ADR-028, MOTOR-TIPO-JSON-S1) — on every
// write door and in every HTTP read — not "a TEXT column with a JSON name".
//
// WHY THIS FILE EXISTS. The `json` type maps to a TEXT column (kept on purpose
// since LIBRARY-GAPS-S1 so no existing tenant column churns). The write path
// handed the decoded Go value straight to pgx, which binds a Go string into
// TEXT and nothing else: an object, array, number or boolean was a 500
// ("cannot find encode plan"), uncaptured in the log, and — being counted by
// the query breaker as a database failure — a way to take every write of the
// process down for 8 s. A string, ANY string, was stored verbatim (`"hola
// mundo"` → 201), and every read returned the stored text as an ESCAPED
// STRING. A migration of 46k rows of nested objects hit the 500 on its first
// request (docs/audits/JSON_TYPE_AUDIT_S1.md).
//
// The cure is ONE function each way. CoerceJSONFields runs inside the shared
// write cores (PrepareCreate / PrepareUpdate / CollectUpdate and the GraphQL
// create), so REST, GraphQL, the batch transaction and Ctx.Insert/Update
// cannot diverge; PromoteJSONText runs where a row leaves the engine over
// HTTP, so a stored JSON text is emitted as the value it is.

// JSONValueMessage is the one 422 message every door uses for a string that
// is not valid JSON on a json/jsonb field (rule "type").
const JSONValueMessage = "must be a JSON value (object, array, number, boolean, or a string containing valid JSON text — a string is read as JSON text, and this one is not valid JSON)"

// JSONTextColumns returns, sorted, the `json` (TEXT-backed) fields of the
// resource — the columns whose stored text must be promoted to a native JSON
// value on the way out. Empty for the vast majority of resources, which is
// what keeps the read path free for them: callers precompute it once per
// resource at boot and skip everything when it is empty.
func (r *ResourceSchema) JSONTextColumns() []string {
	var cols []string
	for name, fd := range r.Fields {
		if fd.Type == "json" {
			cols = append(cols, name)
		}
	}
	sort.Strings(cols)
	return cols
}

// HasJSONFields reports whether the resource declares any json or jsonb field
// — the precomputed gate for the write-side coercion (one map scan at boot,
// nothing per request for a resource without them).
func (r *ResourceSchema) HasJSONFields() bool {
	for _, fd := range r.Fields {
		if fd.Type == "json" || fd.Type == "jsonb" {
			return true
		}
	}
	return false
}

// CoerceJSONFields normalizes, IN PLACE, every json/jsonb field present in a
// write body to the ONE representation the column takes, and reports the
// values that are not JSON:
//
//   - `json` (TEXT): the value becomes canonical compact JSON text — an
//     object/array/number/boolean is encoded (Go's encoding: keys sorted,
//     numbers through float64, the HTTP path's documented limit); a string is
//     read as JSON TEXT (the document's source, the convention Postgres and
//     pgx use for jsonb) and compacted, keeping its numeric text and key order.
//   - `jsonb`: the decoded value is left for pgx (it encodes a map/slice as
//     jsonb natively); a string is validated as JSON text (pgx passes it
//     through, and Postgres would answer an anonymous 22P02 for a bad one).
//
// A string that is not valid JSON is a FieldRuleError{Rule: "type"} naming
// the field — on both types, the same message. `null` is left alone (it is
// SQL NULL, governed by `required`). Fields the resource does not declare, or
// declares as any other type, are untouched. Idempotent: a canonical string
// coerced again stays byte-identical, so a body that goes through two cores
// (e.g. after a before-hook) is safe. Errors are sorted by field.
func CoerceJSONFields(res *ResourceSchema, body map[string]any) []FieldRuleError {
	if res == nil || len(body) == 0 {
		return nil
	}
	var errs []FieldRuleError
	for name, v := range body {
		if v == nil {
			continue
		}
		fd, ok := res.Fields[name]
		if !ok || (fd.Type != "json" && fd.Type != "jsonb") {
			continue
		}
		switch fd.Type {
		case "json":
			text, err := CanonicalJSONText(v)
			if err != nil {
				errs = append(errs, FieldRuleError{Field: name, Rule: "type", Message: fmt.Sprintf("field %q %s", name, JSONValueMessage)})
				continue
			}
			body[name] = text
		case "jsonb":
			if s, isStr := v.(string); isStr && !json.Valid([]byte(s)) {
				errs = append(errs, FieldRuleError{Field: name, Rule: "type", Message: fmt.Sprintf("field %q %s", name, JSONValueMessage)})
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

// CanonicalJSONText renders one write value of a `json` field as the compact
// JSON text the TEXT column stores. A string is JSON TEXT: validated and
// compacted verbatim (numeric text and key order preserved); a []byte /
// json.RawMessage likewise; anything else is encoded. The error is the
// caller's 422 — never a driver error.
func CanonicalJSONText(v any) (string, error) {
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	case []byte:
		raw = x
	case json.RawMessage:
		raw = x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if !json.Valid(raw) {
		return "", fmt.Errorf("not valid JSON text")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// PromoteJSONText turns, IN PLACE, the stored text of each listed `json`
// column of a row into a json.RawMessage, so the encoder emits the VALUE
// (`"data": {"nit":"900"}`) instead of an escaped string. A text that is not
// valid JSON — a row written by an engine before ADR-028, when the column
// took any string — is left as the string it is: readable, never a 500.
// The one read-side rule REST, GraphQL, SSE, the batch results and the admin
// browse share; `?include=` embeds get the same effect from a `::json` cast
// in SQL because Postgres builds those rows.
func PromoteJSONText(row map[string]any, cols []string) {
	if len(cols) == 0 || row == nil {
		return
	}
	for _, c := range cols {
		s, ok := row[c].(string)
		if !ok {
			continue
		}
		if json.Valid([]byte(s)) {
			row[c] = json.RawMessage(s)
		}
	}
}

// PromoteJSONTextRows applies PromoteJSONText to every row.
func PromoteJSONTextRows(rows []map[string]any, cols []string) {
	if len(cols) == 0 {
		return
	}
	for _, r := range rows {
		PromoteJSONText(r, cols)
	}
}
