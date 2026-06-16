package schema

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

var (
	// resourceNameRe matches the SAME charset as field names (G1, FIX-G1-G6):
	// a resource name becomes a GraphQL type/field name, and GraphQL identifiers
	// allow '_' but NOT '-'. Allowing '-' here let a schema PASS `validate` and
	// then PANIC the engine at boot building the GraphQL schema. Underscores give
	// readable multi-word names (order_items) that are valid end-to-end.
	resourceNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	fieldNameRe    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// throughNameRe validates a many_to_many junction TABLE name. A junction may
	// be a declared resource (now snake_case, like the resources) OR a bare join
	// table that is never a GraphQL type — so the SQL layer still tolerates a
	// hyphen here (it is quoted), even though declared resource names may not.
	throughNameRe = regexp.MustCompile(`^[a-z][a-z0-9_\-]*$`)
	// reservedResourcePrefix guards the per-tenant authentication tables
	// (auth_users, auth_tokens, auth_identities, auth_mfa, auth_backup_codes):
	// now that resource names may carry '_', a resource named "auth_users" would
	// collide with them. Reserving the prefix is the new collision guard (the old
	// guard was "resource names can't contain '_'", which G1 removed).
	reservedResourcePrefix = "auth_"

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
				Message: fmt.Sprintf("invalid resource name %q: must match ^[a-z][a-z0-9_]*$ — start with a lowercase letter and use '_' for multi-word names (e.g. order_items); '-' is not allowed (a resource name must be a valid GraphQL identifier)", resName),
			})
		} else if strings.HasPrefix(resName, reservedResourcePrefix) {
			errs = append(errs, ValidationError{
				Field:   resPrefix,
				Message: fmt.Sprintf("invalid resource name %q: the %q prefix is reserved for the engine's per-tenant authentication tables (auth_users, auth_tokens, …)", resName, reservedResourcePrefix),
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
			errs = append(errs, validateDefault(fieldPrefix, field)...)
		}

		// events: opt-in outbox emission (CRUD-EMIT-V1). Each value must be a
		// known write action; an unknown value would silently never emit, so it
		// is rejected at load — same "no dead config" contract as the rest.
		seenEvents := make(map[string]bool, len(res.Events))
		for _, action := range res.Events {
			if !validEmitActions[action] {
				errs = append(errs, ValidationError{
					Field:   resPrefix + ".events",
					Message: fmt.Sprintf("unknown event action %q: must be one of create, update, delete", action),
				})
				continue
			}
			if seenEvents[action] {
				errs = append(errs, ValidationError{
					Field:   resPrefix + ".events",
					Message: fmt.Sprintf("duplicate event action %q", action),
				})
			}
			seenEvents[action] = true
		}

		errs = append(errs, validateRelations(resPrefix, resName, res, s)...)
		errs = append(errs, validateIndexes(resPrefix, res)...)

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

// validateRelations checks the DEFINITION of every declared relation (RELATIONS-V1,
// ADR-019): a structural-only check that is exhaustive about shape so a typo never
// becomes silent dead config. The EXISTENCE of FK columns is checked separately
// against information_schema at tenant migration (a warning, since columns can be
// added to the live table at runtime — the DB stays the source of truth).
func validateRelations(resPrefix, resName string, res ResourceSchema, s *APISchema) []ValidationError {
	var errs []ValidationError
	for relName, rel := range res.Relations {
		relPrefix := resPrefix + ".relations." + relName

		// The embed name is exposed in ?include= and as a GraphQL field, and it
		// keys json_build_object — it must be a valid identifier and not shadow a
		// field of the same resource (that would make the embed ambiguous).
		if !fieldNameRe.MatchString(relName) {
			errs = append(errs, ValidationError{
				Field:   relPrefix,
				Message: fmt.Sprintf("invalid relation name %q: must match ^[a-z][a-z0-9_]*$", relName),
			})
		}
		if _, clash := res.Fields[relName]; clash {
			errs = append(errs, ValidationError{
				Field:   relPrefix,
				Message: fmt.Sprintf("relation name %q collides with a field of the same name", relName),
			})
		}

		if !validRelationTypes[rel.Type] {
			errs = append(errs, ValidationError{
				Field:   relPrefix + ".type",
				Message: fmt.Sprintf("unknown relation type %q: must be one of has_many, belongs_to, many_to_many", rel.Type),
			})
		}
		if _, ok := s.Resources[rel.Target]; !ok {
			errs = append(errs, ValidationError{
				Field:   relPrefix + ".target",
				Message: fmt.Sprintf("relation target %q references unknown resource", rel.Target),
			})
		}
		if rel.FK == "" || !fieldNameRe.MatchString(rel.FK) {
			errs = append(errs, ValidationError{
				Field:   relPrefix + ".fk",
				Message: fmt.Sprintf("relation fk %q must be a valid field name (^[a-z][a-z0-9_]*$)", rel.FK),
			})
		}
		if rel.Limit < 0 {
			errs = append(errs, ValidationError{
				Field:   relPrefix + ".limit",
				Message: "relation limit must be >= 0",
			})
		}

		// through / target_fk belong to many_to_many ONLY.
		if rel.Type == RelationManyToMany {
			if rel.Through == "" || !throughNameRe.MatchString(rel.Through) {
				errs = append(errs, ValidationError{
					Field:   relPrefix + ".through",
					Message: "many_to_many requires a valid through (junction table) name",
				})
			}
			if rel.TargetFK == "" || !fieldNameRe.MatchString(rel.TargetFK) {
				errs = append(errs, ValidationError{
					Field:   relPrefix + ".target_fk",
					Message: "many_to_many requires a valid target_fk column name",
				})
			}
		} else {
			if rel.Through != "" {
				errs = append(errs, ValidationError{
					Field:   relPrefix + ".through",
					Message: fmt.Sprintf("through only applies to many_to_many, not %q", rel.Type),
				})
			}
			if rel.TargetFK != "" {
				errs = append(errs, ValidationError{
					Field:   relPrefix + ".target_fk",
					Message: fmt.Sprintf("target_fk only applies to many_to_many, not %q", rel.Type),
				})
			}
		}
	}
	return errs
}

// validateIndexes checks each declared index DEFINITION (BUGS-V1): at least one
// field, and every referenced field a valid identifier. Column EXISTENCE is
// checked against information_schema at tenant migration (a warning, since
// columns can be added to the live table at runtime — the DB stays the source of
// truth, same pattern as relation FK indexes).
func validateIndexes(resPrefix string, res ResourceSchema) []ValidationError {
	var errs []ValidationError
	for i, idx := range res.Indexes {
		idxPrefix := fmt.Sprintf("%s.indexes[%d]", resPrefix, i)
		if len(idx.Fields) == 0 {
			errs = append(errs, ValidationError{
				Field:   idxPrefix + ".fields",
				Message: "index must list at least one field",
			})
			continue
		}
		for _, f := range idx.Fields {
			if !fieldNameRe.MatchString(f) {
				errs = append(errs, ValidationError{
					Field:   idxPrefix + ".fields",
					Message: fmt.Sprintf("invalid index field %q: must match ^[a-z][a-z0-9_]*$", f),
				})
			}
		}
	}
	return errs
}

// validateDefault checks that a field's `default` value is type-compatible with
// the field (SCHEMA-CLOSE-V1) so a bad default is rejected at load, never a
// surprise at insert. JSON numbers decode to float64, so integer fields require
// an integral float64. Dynamic "now" is accepted on time fields only. Defaults
// on auto fields are rejected (auto manages its own value).
func validateDefault(fieldPrefix string, fd FieldDef) []ValidationError {
	if fd.Default == nil {
		return nil
	}
	dp := fieldPrefix + ".default"
	if fd.Auto {
		return []ValidationError{{Field: dp, Message: "default cannot be set on an auto field"}}
	}
	// Enum (string-valued): the default must be a declared member.
	if len(fd.Enum) > 0 {
		s, ok := fd.Default.(string)
		if !ok {
			return []ValidationError{{Field: dp, Message: "default must be a string matching one of the enum values"}}
		}
		for _, e := range fd.Enum {
			if e == s {
				return nil
			}
		}
		return []ValidationError{{Field: dp, Message: fmt.Sprintf("default %q is not one of the enum values", s)}}
	}

	bad := func(msg string) []ValidationError { return []ValidationError{{Field: dp, Message: msg}} }
	switch fd.Type {
	case "string", "text":
		if _, ok := fd.Default.(string); !ok {
			return bad("default must be a string")
		}
	case "int", "int64":
		f, ok := fd.Default.(float64)
		if !ok || f != float64(int64(f)) {
			return bad("default must be an integer")
		}
	case "float64":
		if _, ok := fd.Default.(float64); !ok {
			return bad("default must be a number")
		}
	case "bool":
		if _, ok := fd.Default.(bool); !ok {
			return bad("default must be a boolean")
		}
	case "uuid":
		s, ok := fd.Default.(string)
		if !ok {
			return bad("default must be a uuid string")
		}
		if _, err := uuid.Parse(s); err != nil {
			return bad("default must be a valid uuid string")
		}
	case "time":
		if _, ok := fd.Default.(string); !ok {
			return bad(`default must be a string (an RFC3339 timestamp, or "now" for the insert moment)`)
		}
	case "json":
		// Any JSON value is acceptable for a json column.
	}
	return nil
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
