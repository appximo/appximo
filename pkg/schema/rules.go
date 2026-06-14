package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// MaxPatternLength bounds a field's regex source. Go's regexp is RE2 (linear
// time, no catastrophic backtracking), so the cap only limits compile cost and
// schema noise, not worst-case matching behaviour.
const MaxPatternLength = 200

// validFormats is the closed set of values accepted for FieldDef.Format.
var validFormats = map[string]bool{
	"email": true,
	"uuid":  true,
	"url":   true,
	"date":  true,
}

// emailRe is a pragmatic email shape check (local@domain.tld). Anchored and
// RE2, so linear in the input. It is not a full RFC 5322 parser on purpose.
var emailRe = regexp.MustCompile(
	"^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?" +
		"(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$")

// FieldRuleError is one declarative-validation violation, in the exact shape
// the 422 response body carries inside "fields".
type FieldRuleError struct {
	Field   string `json:"field"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// ruleFn checks one already-decoded JSON value. nil means the value passes.
type ruleFn func(v any) *FieldRuleError

// ResourceValidator holds the precompiled validation for one resource: the
// regexes are compiled, the enum sets built, and the required list resolved
// ONCE at schema load. The request path only executes these closures.
type ResourceValidator struct {
	required []string // non-auto required field names, sorted
	rules    map[string][]ruleFn
	defaults map[string]defaultSpec // field → default to fill on create (SCHEMA-CLOSE-V1)
}

// defaultSpec is a precompiled field default. literal carries a fixed value
// (type-checked at load); dynamicNow marks the one supported dynamic default —
// "now" on a time field, resolved to the insert moment.
type defaultSpec struct {
	literal    any
	dynamicNow bool
}

// CompileRules builds the ResourceValidator for one resource. It never
// panics: an invalid pattern (only reachable when schema.Validate was skipped,
// since Validate rejects it at load) compiles to a fail-closed rule that
// rejects every value for that field — never a silently dropped rule.
func CompileRules(res *ResourceSchema) *ResourceValidator {
	rv := &ResourceValidator{rules: make(map[string][]ruleFn)}

	names := make([]string, 0, len(res.Fields))
	for n := range res.Fields {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		fd := res.Fields[name]
		if fd.Required && !fd.Auto {
			rv.required = append(rv.required, name)
		}

		var rules []ruleFn
		isString := fd.Type == "string" || fd.Type == "text"
		isNumber := fd.Type == "int" || fd.Type == "int64" || fd.Type == "float64"

		if len(fd.Enum) > 0 {
			set := make(map[string]struct{}, len(fd.Enum))
			for _, e := range fd.Enum {
				set[e] = struct{}{}
			}
			msg := "must be one of: " + strings.Join(fd.Enum, ", ")
			field := name
			rules = append(rules, func(v any) *FieldRuleError {
				s, ok := v.(string)
				if !ok {
					return &FieldRuleError{Field: field, Rule: "enum", Message: msg}
				}
				if _, ok := set[s]; !ok {
					return &FieldRuleError{Field: field, Rule: "enum", Message: msg}
				}
				return nil
			})
		}

		if isString {
			if fd.MinLength != nil {
				min := *fd.MinLength
				rules = append(rules, strRule(name, "minLength",
					fmt.Sprintf("must be at least %d characters", min),
					func(s string) bool { return utf8.RuneCountInString(s) >= min }))
			}
			if fd.MaxLength != nil {
				max := *fd.MaxLength
				rules = append(rules, strRule(name, "maxLength",
					fmt.Sprintf("must be at most %d characters", max),
					func(s string) bool { return utf8.RuneCountInString(s) <= max }))
			}
			if fd.Pattern != "" {
				if re, err := regexp.Compile(fd.Pattern); err != nil || len(fd.Pattern) > MaxPatternLength {
					// Fail closed: Validate rejects this schema at load; if a
					// caller skipped Validate, the field rejects every value
					// rather than silently losing its rule.
					field := name
					rules = append(rules, func(any) *FieldRuleError {
						return &FieldRuleError{Field: field, Rule: "pattern",
							Message: "schema declares an invalid pattern for this field"}
					})
				} else {
					rules = append(rules, strRule(name, "pattern",
						"must match pattern "+fd.Pattern, re.MatchString))
				}
			}
			if fd.Format != "" {
				if check, msg := formatChecker(fd.Format); check != nil {
					rules = append(rules, strRule(name, "format", msg, check))
				}
			}
		}

		if isNumber {
			if fd.Min != nil {
				min := *fd.Min
				rules = append(rules, numRule(name, "min",
					fmt.Sprintf("must be >= %v", min),
					func(f float64) bool { return f >= min }))
			}
			if fd.Max != nil {
				max := *fd.Max
				rules = append(rules, numRule(name, "max",
					fmt.Sprintf("must be <= %v", max),
					func(f float64) bool { return f <= max }))
			}
		}

		if len(rules) > 0 {
			rv.rules[name] = rules
		}

		// Default to fill on create when the field is omitted (SCHEMA-CLOSE-V1).
		// auto fields manage their own value, so they never carry a default.
		if fd.Default != nil && !fd.Auto {
			if rv.defaults == nil {
				rv.defaults = make(map[string]defaultSpec)
			}
			if fd.Type == "time" && isNowDefault(fd.Default) {
				rv.defaults[name] = defaultSpec{dynamicNow: true}
			} else {
				rv.defaults[name] = defaultSpec{literal: fd.Default}
			}
		}
	}
	return rv
}

// isNowDefault reports whether a time field's default is the dynamic sentinel
// "now" (case-insensitive) rather than a literal timestamp string.
func isNowDefault(v any) bool {
	s, ok := v.(string)
	return ok && strings.EqualFold(s, "now")
}

// ApplyDefaults fills, in place, any field that declares a default and is ABSENT
// from body (a present key — even an explicit null — is left as the caller set
// it, matching SQL DEFAULT, which applies only when a column is omitted). Called
// on CREATE only, BEFORE required-field validation, so a required field with a
// default is satisfied by the default while a required field without one still
// 422s. A resource with no defaults pays a single length check (the create gate).
func (rv *ResourceValidator) ApplyDefaults(body map[string]any) {
	if len(rv.defaults) == 0 {
		return
	}
	for name, ds := range rv.defaults {
		if _, present := body[name]; present {
			continue
		}
		if ds.dynamicNow {
			body[name] = time.Now().UTC()
		} else {
			body[name] = ds.literal
		}
	}
}

// ValidateWrite checks a decoded JSON body against the precompiled rules and
// returns ALL violations (never just the first). requireAll is true for POST
// (create) and PUT (full replace): every required non-auto field must be
// present and non-null. PATCH passes false — only the fields present in the
// body are validated. A null value is skipped by the per-field rules (null
// semantics belong to required / the update path).
func (rv *ResourceValidator) ValidateWrite(body map[string]any, requireAll bool) []FieldRuleError {
	var errs []FieldRuleError
	if requireAll {
		for _, f := range rv.required {
			if v, present := body[f]; !present || v == nil {
				errs = append(errs, FieldRuleError{Field: f, Rule: "required", Message: "is required"})
			}
		}
	}
	if len(rv.rules) > 0 {
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic error order
		for _, k := range keys {
			v := body[k]
			if v == nil {
				continue
			}
			for _, rule := range rv.rules[k] {
				if e := rule(v); e != nil {
					errs = append(errs, *e)
				}
			}
		}
	}
	return errs
}

// strRule wraps a string predicate: a non-string value fails the rule with a
// type message (rules only apply to fields of string/text type, so a non-string
// JSON value is itself invalid input for that field).
func strRule(field, rule, msg string, ok func(string) bool) ruleFn {
	return func(v any) *FieldRuleError {
		s, isStr := v.(string)
		if !isStr {
			return &FieldRuleError{Field: field, Rule: rule, Message: "must be a string"}
		}
		if !ok(s) {
			return &FieldRuleError{Field: field, Rule: rule, Message: msg}
		}
		return nil
	}
}

// numRule wraps a numeric predicate. encoding/json decodes every JSON number
// into float64, so that is the only accepted dynamic type.
func numRule(field, rule, msg string, ok func(float64) bool) ruleFn {
	return func(v any) *FieldRuleError {
		f, isNum := v.(float64)
		if !isNum {
			return &FieldRuleError{Field: field, Rule: rule, Message: "must be a number"}
		}
		if !ok(f) {
			return &FieldRuleError{Field: field, Rule: rule, Message: msg}
		}
		return nil
	}
}

// formatChecker maps a format name to its predicate and client-safe message.
// Unknown formats return nil (Validate rejects them at load).
func formatChecker(format string) (func(string) bool, string) {
	switch format {
	case "email":
		return emailRe.MatchString, "must be a valid email"
	case "uuid":
		return func(s string) bool {
			_, err := uuid.Parse(s)
			return err == nil
		}, "must be a valid UUID"
	case "url":
		return func(s string) bool {
			u, err := url.Parse(s)
			return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
		}, "must be a valid http(s) URL"
	case "date":
		return func(s string) bool {
			if _, err := time.Parse(time.RFC3339, s); err == nil {
				return true
			}
			_, err := time.Parse("2006-01-02", s)
			return err == nil
		}, "must be an ISO-8601 date (YYYY-MM-DD or RFC 3339)"
	}
	return nil, ""
}
