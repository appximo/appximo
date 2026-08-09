package schema

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// AI-F0-S2: the LLM-friendly validation report. It unifies the two validators —
// the structural meta-schema (ValidateAgainstMetaSchema) and the semantic Go
// validator (Validate) — into ONE machine-readable result an AI can parse and act
// on: each error carries a JSON path, a machine-readable rule, a human message, and
// (when applicable) the expected values, the offending value, and a fix hint. The
// human/interactive output and the engine's validation rules are unchanged — this is
// an additive presentation layer.

// StructuredError is one validation failure in a form an LLM can correct from.
type StructuredError struct {
	Path     string   `json:"path"`               // dotted location, e.g. resources.posts.fields.author_id.references
	Rule     string   `json:"rule"`               // machine-readable category, e.g. invalid_type
	Message  string   `json:"message"`            // human-readable explanation
	Expected []string `json:"expected,omitempty"` // allowed values, when a closed set
	Got      string   `json:"got,omitempty"`      // the offending value, when applicable
	Fix      string   `json:"fix,omitempty"`      // how to correct it
	Source   string   `json:"source"`             // "metaschema" (structural) | "semantic" (cross-reference)
}

// ValidationReport is the top-level result for a candidate schema.
type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Errors []StructuredError `json:"errors"`
	// Warnings are findings that do NOT make the schema invalid but almost always
	// mean it will not do what its author intended (SCHEMA-5). They are reported in
	// the same shape as errors so the AI correction loop can act on them with the
	// same code path, and `valid` stays true — a warning never blocks a deploy.
	//
	// ALWAYS EMITTED, even when empty — deliberately not omitempty. With the key
	// absent, "this schema has no warnings" and "this binary has no warnings
	// feature" are the same JSON, so a caller cannot tell a clean bill of health
	// from an old engine. A third-party agent reported exactly that: it could not
	// confirm zero warnings as a POSITIVE signal and pre-empted the known rules by
	// hand instead. `errors` has always been emitted unconditionally; this makes
	// the two halves of the report symmetric.
	Warnings []StructuredError `json:"warnings"`
}

// ValidateReport runs BOTH validators over raw schema bytes and returns a unified,
// LLM-friendly report. The meta-schema provides the structural errors (types, enums,
// patterns, unknown/missing keys, RBAC form); the Go validator provides the semantic
// cross-reference errors (existence, uniqueness, type compatibility, coherence). A
// path that already has a structural error suppresses the semantic error at the SAME
// path (they are the same problem reported by two layers), so the report is
// deduplicated without losing either layer's unique findings.
func ValidateReport(raw []byte) ValidationReport {
	out := []StructuredError{}

	// Structural (meta-schema) — works on any JSON.
	structuralPaths := map[string]bool{}
	for _, e := range ValidateAgainstMetaSchema(raw) {
		se := metaErrorToStructured(e)
		structuralPaths[se.Path] = true
		out = append(out, se)
	}

	// Semantic (Go) — only if the JSON parses into the schema struct. Unknown keys
	// are dropped by json.Unmarshal (the meta-schema already flagged them), so the
	// semantic pass runs over the well-formed remainder.
	warnings := []StructuredError{} // non-nil: the key is always emitted (see ValidationReport.Warnings)
	var s APISchema
	if err := json.Unmarshal(raw, &s); err == nil {
		for _, e := range Validate(&s) {
			se := semanticErrorToStructured(e)
			if structuralPaths[se.Path] {
				continue // same problem already reported structurally
			}
			out = append(out, se)
		}
		// Warnings are computed on the same parsed schema but kept OUT of Errors, so
		// `valid` is unaffected (SCHEMA-5).
		for _, w := range Warnings(&s) {
			warnings = append(warnings, semanticErrorToStructured(w))
		}
	} else if len(out) == 0 {
		// Malformed JSON and the meta-schema somehow said nothing — surface the parse error.
		out = append(out, StructuredError{
			Path: "$", Rule: "invalid_json", Source: "metaschema",
			Message: "schema is not valid JSON: " + err.Error(),
			Fix:     "fix the JSON syntax so the document parses",
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Source < out[j].Source
	})
	return ValidationReport{Valid: len(out) == 0, Errors: out, Warnings: warnings}
}

// semanticErrorToStructured maps a Go ValidationError to the structured form, using
// the enriched fields when present and deriving a sensible Rule/Fix from the path
// otherwise (so EVERY error has a machine-readable rule).
func semanticErrorToStructured(e ValidationError) StructuredError {
	rule := e.Rule
	if rule == "" {
		rule = deriveSemanticRule(e.Field)
	}
	fix := e.Fix
	if fix == "" {
		fix = defaultFixForRule[rule]
	}
	return StructuredError{
		Path:     e.Field,
		Rule:     rule,
		Message:  e.Message,
		Expected: e.Expected,
		Got:      e.Got,
		Fix:      fix,
		Source:   "semantic",
	}
}

// deriveSemanticRule classifies an un-enriched semantic error by its path suffix, so
// the structured report always carries a machine-readable rule.
func deriveSemanticRule(path string) string {
	switch {
	case strings.HasSuffix(path, ".type"):
		return "invalid_type"
	case strings.HasSuffix(path, ".on_delete"):
		return "invalid_on_delete"
	case strings.HasSuffix(path, ".on_update"):
		return "invalid_on_update"
	case strings.HasSuffix(path, ".references"):
		return "invalid_reference"
	case strings.HasSuffix(path, ".renamed_from"):
		return "invalid_renamed_from"
	case strings.HasSuffix(path, ".format"):
		return "invalid_format"
	case strings.HasSuffix(path, ".pattern"):
		return "invalid_pattern"
	case strings.HasSuffix(path, ".enum"):
		return "invalid_enum"
	case strings.HasSuffix(path, ".default"):
		return "invalid_default"
	case strings.HasSuffix(path, ".events"):
		return "invalid_events"
	case strings.HasSuffix(path, ".actions"):
		return "invalid_actions"
	case strings.HasSuffix(path, ".condition_actions"):
		return "invalid_condition_actions"
	case strings.HasSuffix(path, ".op"):
		return "invalid_condition_op"
	case strings.Contains(path, ".state_machine"):
		return "invalid_state_machine"
	case strings.Contains(path, ".relations."):
		return "invalid_relation"
	case strings.Contains(path, ".foreign_keys"):
		return "invalid_foreign_key"
	case strings.Contains(path, ".indexes"):
		return "invalid_index"
	case strings.Contains(path, ".hooks."):
		return "invalid_hook"
	case strings.Contains(path, ".conditions"):
		return "invalid_condition"
	case regexp.MustCompile(`^resources\.[^.]+$`).MatchString(path):
		return "invalid_resource_name"
	case regexp.MustCompile(`^rbac\.roles\.[^.]+$`).MatchString(path):
		return "rbac_form_conflict"
	default:
		return "schema_error"
	}
}

// defaultFixForRule is a fallback fix hint for rules whose error site did not set an
// explicit Fix. Kept short and actionable.
var defaultFixForRule = map[string]string{
	"invalid_on_delete":         "use one of: restrict, cascade, set_null (on_delete is only valid on a relation field; set_null needs a nullable column)",
	"invalid_on_update":         "use one of: restrict, cascade, set_null (on_update is only valid on a relation field; set_null needs a nullable column)",
	"invalid_format":            "use one of: email, uuid, url, date (format applies only to string/text fields)",
	"invalid_pattern":           "provide a valid RE2 regex of at most 200 chars on a string/text field",
	"invalid_enum":              "provide a non-empty array of allowed string values",
	"invalid_default":           "make the default match the field's type (and be an enum member / initial state when applicable)",
	"invalid_events":            "use a subset of: create, update, delete (no duplicates)",
	"invalid_condition_op":      "set the condition op to \"eq\" (the only operator the engine enforces) or omit it",
	"invalid_state_machine":     "state_machine needs at least one initial state on a string/text field, with every state also an enum value (when enum is set)",
	"invalid_relation":          "a relation needs type (has_many|belongs_to|many_to_many), an existing target, and fk; many_to_many also needs through + target_fk",
	"invalid_foreign_key":       "columns and ref_columns must be equal-length, exist, and ref_columns must be unique on the target",
	"invalid_index":             "each index needs at least one field, each a valid identifier",
	"invalid_renamed_from":      "renamed_from must be a valid identifier that is NOT still a declared name",
	"invalid_resource_name":     "resource names must match ^[a-z][a-z0-9_]*$, not start with auth_, and not be 'transaction'",
	"rbac_form_conflict":        "use EITHER the role-global form (resources/actions/conditions/fields) OR per-resource permissions, not both",
	"invalid_condition_actions": "every condition_actions entry must be a concrete action that is also in the permission's actions",
}

// ── meta-schema (structural) error mapping ──────────────────────────────────

var (
	reEnumOneOf = regexp.MustCompile(`must be one of (.+)$`)
	reMustBe    = regexp.MustCompile(`must be '([^']*)'$`)
	reAddlProp  = regexp.MustCompile(`additional propert(?:y|ies) '?([^' ]+)'? not allowed`)
	reMissing   = regexp.MustCompile(`missing propert(?:y|ies) '?([^' ]+)'?`)
)

// metaErrorToStructured maps a meta-schema ValidationError (Field=instance path,
// Message=the library's reason) to the structured form, classifying the rule and
// adding a fix hint where the message shape is recognizable.
func metaErrorToStructured(e ValidationError) StructuredError {
	se := StructuredError{Path: e.Field, Message: e.Message, Source: "metaschema", Rule: "structural_error"}
	msg := e.Message
	switch {
	case reEnumOneOf.MatchString(msg):
		se.Rule = "invalid_enum_value"
		se.Expected = parseQuotedList(reEnumOneOf.FindStringSubmatch(msg)[1])
		se.Fix = "use one of the allowed values listed in expected"
	case reMustBe.MatchString(msg):
		se.Rule = "invalid_value"
		se.Expected = []string{reMustBe.FindStringSubmatch(msg)[1]}
		se.Fix = "set this to the only allowed value listed in expected"
	case reAddlProp.MatchString(msg):
		se.Rule = "unknown_key"
		se.Got = reAddlProp.FindStringSubmatch(msg)[1]
		se.Fix = "remove the unknown key (it is a typo or not part of the schema grammar)"
	case reMissing.MatchString(msg):
		se.Rule = "missing_required"
		se.Got = reMissing.FindStringSubmatch(msg)[1]
		se.Fix = "add the required key"
	case strings.Contains(msg, "pattern"):
		se.Rule = "pattern_mismatch"
		se.Fix = "match the required pattern (identifiers are ^[a-z][a-z0-9_]*$; resource names also exclude the auth_ prefix and 'transaction')"
	case strings.Contains(msg, "oneOf") || strings.Contains(msg, "valid against"):
		se.Rule = "form_mismatch"
		se.Fix = "this object matched none (or more than one) of its allowed shapes — check the grammar for this block (e.g. a role is EITHER role-global OR per-resource)"
	case strings.Contains(msg, "length must be"):
		se.Rule = "length_out_of_bounds"
		se.Fix = "shorten the value to the allowed maximum"
	}
	return se
}

// parseQuotedList turns "'a', 'b', 'c'" (the jsonschema enum-error tail) into
// ["a","b","c"].
func parseQuotedList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, "'\"")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
