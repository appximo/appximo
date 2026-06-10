package schema

import (
	"fmt"
	"regexp"
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

var (
	resourceNameRe = regexp.MustCompile(`^[a-z][a-z0-9\-]*$`)
	fieldNameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	validFieldTypes = map[string]bool{
		"string":  true,
		"int":     true,
		"int64":   true,
		"float64": true,
		"bool":    true,
		"uuid":    true,
		"time":    true,
		"text":    true,
		"json":    true,
	}
)

func Validate(s *APISchema) []ValidationError {
	var errs []ValidationError

	for resName, res := range s.Resources {
		resPrefix := "resources." + resName

		if !resourceNameRe.MatchString(resName) {
			errs = append(errs, ValidationError{
				Field:   resPrefix,
				Message: fmt.Sprintf("invalid resource name %q: must match ^[a-z][a-z0-9-]*$", resName),
			})
		}

		for fieldName, field := range res.Fields {
			fieldPrefix := resPrefix + ".fields." + fieldName

			if !fieldNameRe.MatchString(fieldName) {
				errs = append(errs, ValidationError{
					Field:   fieldPrefix,
					Message: fmt.Sprintf("invalid field name %q: must match ^[a-z][a-z0-9_]*$", fieldName),
				})
			}

			if !validFieldTypes[field.Type] {
				errs = append(errs, ValidationError{
					Field:   fieldPrefix + ".type",
					Message: fmt.Sprintf("unknown field type %q", field.Type),
				})
			}

			if field.Relation != "" {
				if _, ok := s.Resources[field.Relation]; !ok {
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".relation",
						Message: fmt.Sprintf("relation %q references unknown resource", field.Relation),
					})
				}
			}

			// Enum key present but empty
			if field.Enum != nil && len(field.Enum) == 0 {
				errs = append(errs, ValidationError{
					Field:   fieldPrefix + ".enum",
					Message: "enum must not be empty",
				})
			}

			errs = append(errs, validateFieldRules(fieldPrefix, field)...)
		}

		for hookName, hook := range res.Hooks {
			hookPrefix := resPrefix + ".hooks." + hookName

			switch hook.Type {
			case "js":
				if hook.Script == "" {
					errs = append(errs, ValidationError{
						Field:   hookPrefix + ".script",
						Message: "js hook requires a non-empty script",
					})
				}
			case "webhook":
				if hook.URL == "" {
					errs = append(errs, ValidationError{
						Field:   hookPrefix + ".url",
						Message: "webhook hook requires a non-empty url",
					})
				}
			case "wasm":
				if hook.WasmModule == "" {
					errs = append(errs, ValidationError{
						Field:   hookPrefix + ".wasm_module",
						Message: "wasm hook requires a non-empty wasm_module",
					})
				}
			default:
				errs = append(errs, ValidationError{
					Field:   hookPrefix + ".type",
					Message: fmt.Sprintf("unknown hook type %q: must be \"js\", \"webhook\", or \"wasm\"", hook.Type),
				})
			}
		}
	}

	return errs
}

// numericTypes are the field types min/max apply to.
var numericTypes = map[string]bool{"int": true, "int64": true, "float64": true}

// stringTypes are the field types minLength/maxLength/pattern/format apply to.
var stringTypes = map[string]bool{"string": true, "text": true}

// validateFieldRules checks the DEFINITION of the declarative validation keys
// (S44) so a bad rule is rejected cleanly at schema load — never a panic at
// compile time nor a surprise at request time. All keys are optional; a field
// declaring none of them produces no errors here.
func validateFieldRules(fieldPrefix string, field FieldDef) []ValidationError {
	var errs []ValidationError

	if field.Pattern != "" {
		switch {
		case !stringTypes[field.Type]:
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".pattern",
				Message: fmt.Sprintf("pattern only applies to string/text fields, not %q", field.Type),
			})
		case len(field.Pattern) > MaxPatternLength:
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".pattern",
				Message: fmt.Sprintf("pattern is %d chars; max is %d", len(field.Pattern), MaxPatternLength),
			})
		default:
			if _, err := regexp.Compile(field.Pattern); err != nil {
				errs = append(errs, ValidationError{
					Field:   fieldPrefix + ".pattern",
					Message: fmt.Sprintf("invalid pattern: %v", err),
				})
			}
		}
	}

	if field.MinLength != nil || field.MaxLength != nil {
		if !stringTypes[field.Type] {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix,
				Message: fmt.Sprintf("minLength/maxLength only apply to string/text fields, not %q", field.Type),
			})
		}
		if field.MinLength != nil && *field.MinLength < 0 {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".minLength",
				Message: "minLength must be >= 0",
			})
		}
		if field.MaxLength != nil && *field.MaxLength < 0 {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".maxLength",
				Message: "maxLength must be >= 0",
			})
		}
		if field.MinLength != nil && field.MaxLength != nil && *field.MinLength > *field.MaxLength {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix,
				Message: "minLength must be <= maxLength",
			})
		}
	}

	if field.Min != nil || field.Max != nil {
		if !numericTypes[field.Type] {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix,
				Message: fmt.Sprintf("min/max only apply to numeric fields (int, int64, float64), not %q", field.Type),
			})
		}
		if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix,
				Message: "min must be <= max",
			})
		}
	}

	if field.Format != "" {
		if !stringTypes[field.Type] {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".format",
				Message: fmt.Sprintf("format only applies to string/text fields, not %q", field.Type),
			})
		} else if !validFormats[field.Format] {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".format",
				Message: fmt.Sprintf("unknown format %q: must be one of email, uuid, url, date", field.Format),
			})
		}
	}

	return errs
}
