// Package flowtest is the multi-step flow-test engine (FLOWTEST-S1) — the last
// piece of the productivity-confidence layer: persisted, re-runnable scenarios
// ("login as role X → create → attach → assert") that a deploy can re-run for a
// PASS/FAIL regression verdict anchored to the schema version it ran against.
//
// It GENERALIZES the molds STATE-AUDIT-V1 §4 mapped — it is not a new idea:
//   - the FORMAT is api-cert.postman_collection.json formalized: steps as DATA
//     (method/path/body/headers + assertions) with CAPTURED variables flowing
//     between steps ({{token}} from the login, {{cita_id}} from the create);
//   - the RUNNER is the acceptance-test/DevHub pattern: execute in order
//     against the live app, emit each step's PASS/FAIL live (SSE), final verdict;
//   - the ASSERTIONS are the e2e/httpexpect vocabulary: status, field exists,
//     field equals/contains.
//
// A flow authenticates as a TENANT USER (a real POST /auth/login step, or a
// flow-level role that pre-mints a tenant JWT) — never the super-admin — so it
// exercises the REAL RBAC the app enforces.
package flowtest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Flow is one named multi-step scenario — the persistable "flow as data".
type Flow struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Role, when set, pre-mints a tenant JWT for that RBAC role into {{token}}
	// before step 1 (the convenience path). A flow that wants to exercise the
	// REAL login instead makes step 1 a POST /auth/login and captures
	// {{token}} from the response — a capture overwrites the pre-minted var.
	Role  string `json:"role,omitempty"`
	Steps []Step `json:"steps"`
}

// Step is one request + its expectations + what it captures for later steps.
// {{var}} placeholders are substituted in Path, Body and header values.
type Step struct {
	Name    string            `json:"name"`
	Method  string            `json:"method"`
	Path    string            `json:"path"` // e.g. /api/citas or /auth/login
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Upload, when set, sends a multipart file (POST /api/files) instead of Body.
	Upload *Upload `json:"upload,omitempty"`
	Expect Expect  `json:"expect"`
	// Capture maps {{variable}} names to dot-paths into the response JSON
	// (e.g. "cita_id": "id", "token": "token", "first": "data.0.id").
	Capture map[string]string `json:"capture,omitempty"`
}

// Upload describes a multipart file part built from inline text content.
type Upload struct {
	Field    string `json:"field,omitempty"` // form field; default "file"
	Filename string `json:"filename"`
	Content  string `json:"content"` // inline text content (e.g. "%PDF-1.4 ...")
}

// Expect is a step's assertions: the HTTP status plus optional field checks.
type Expect struct {
	Status  int      `json:"status"`
	Asserts []Assert `json:"asserts,omitempty"`
}

// Assert checks one field of the response JSON, addressed by dot-path.
// Ops (FLOWTEST-POWER-S1 — the full vocabulary, mirrored by the Studio
// assertion controls):
//   - "exists" / "not_exists" — the field is (not) present. not_exists is how a
//     GraphQL step asserts success ("errors" not_exists — GraphQL is always 200).
//   - "eq" / "ne" — the stringified value equals / differs from Value
//     ({{var}} substitution applies to Value, so a response field can be
//     compared against a captured variable).
//   - "contains" — substring on the stringified value.
//   - "gt" / "gte" / "lt" / "lte" — numeric comparison (both sides must parse
//     as numbers; a non-numeric side is a clear failure, never a silent pass).
//   - "len" — the field is an array (or string) whose length equals Value.
type Assert struct {
	Path  string `json:"path"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

var (
	validMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}
	validOps     = map[string]bool{
		"exists": true, "not_exists": true,
		"eq": true, "ne": true, "contains": true,
		"gt": true, "gte": true, "lt": true, "lte": true,
		"len": true,
	}
	// opsNeedingValue: ops where an empty Value can't mean anything ("eq"
	// against "" is a legitimate check; "gt" against "" never is).
	opsNeedingValue = map[string]bool{
		"gt": true, "gte": true, "lt": true, "lte": true, "len": true,
	}
	varRe = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)
)

// Validate rejects a malformed flow with an actionable error (the same
// fail-at-save philosophy as the schema validator — a bad step never becomes a
// confusing runtime failure).
func (f *Flow) Validate() error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("flow name is required")
	}
	if len(f.Steps) == 0 {
		return fmt.Errorf("flow %q needs at least one step", f.Name)
	}
	if len(f.Steps) > 50 {
		return fmt.Errorf("flow %q has %d steps (max 50)", f.Name, len(f.Steps))
	}
	for i, st := range f.Steps {
		where := fmt.Sprintf("step %d (%s)", i+1, st.Name)
		if !validMethods[strings.ToUpper(st.Method)] {
			return fmt.Errorf("%s: method %q is not one of GET/POST/PUT/PATCH/DELETE", where, st.Method)
		}
		if !strings.HasPrefix(st.Path, "/") {
			return fmt.Errorf("%s: path must start with / (got %q)", where, st.Path)
		}
		if st.Expect.Status < 100 || st.Expect.Status > 599 {
			return fmt.Errorf("%s: expect.status %d is not a valid HTTP status", where, st.Expect.Status)
		}
		if st.Upload != nil && strings.TrimSpace(st.Upload.Filename) == "" {
			return fmt.Errorf("%s: upload.filename is required", where)
		}
		for _, a := range st.Expect.Asserts {
			if !validOps[a.Op] {
				return fmt.Errorf("%s: assert op %q is not one of exists/not_exists/eq/ne/contains/gt/gte/lt/lte/len", where, a.Op)
			}
			if a.Path == "" {
				return fmt.Errorf("%s: assert path is required", where)
			}
			if opsNeedingValue[a.Op] && a.Value == "" {
				return fmt.Errorf("%s: assert op %q needs a value", where, a.Op)
			}
		}
		for v, p := range st.Capture {
			if !varRe.MatchString("{{"+v+"}}") || p == "" {
				return fmt.Errorf("%s: capture %q → %q is invalid (name: letters/digits/_; path required)", where, v, p)
			}
		}
	}
	return nil
}

// substitute replaces every {{var}} in s with its captured value; an unknown
// variable is left verbatim (so the failure surfaces in the request, visibly).
func substitute(s string, vars map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.TrimSpace(strings.Trim(m, "{}"))
		if v, ok := vars[name]; ok {
			return v
		}
		return m
	})
}

// lookupPath walks a parsed JSON value by dot-path ("data.0.nombre"). Numeric
// segments index arrays. Returns (value, true) or (nil, false).
func lookupPath(doc any, path string) (any, bool) {
	cur := doc
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// stringify renders a captured/asserted JSON value the way a next step wants to
// embed it: strings verbatim, numbers without a trailing ".0" when integral,
// everything else compact JSON.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
