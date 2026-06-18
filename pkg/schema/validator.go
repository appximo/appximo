package schema

import (
	"fmt"
	"regexp"
	"sort"
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
	// reservedTransactionResource is the one resource name the engine claims for the
	// atomic multi-resource transaction endpoint (G4): POST /api/transaction. A
	// resource so named would have its collection route shadowed by the batch
	// handler, so it is rejected at load (the plural "transactions" is unaffected).
	reservedTransactionResource = "transaction"

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
		} else if resName == reservedTransactionResource {
			errs = append(errs, ValidationError{
				Field:   resPrefix,
				Message: fmt.Sprintf("invalid resource name %q: reserved for the atomic multi-resource transaction endpoint (POST /api/transaction)", resName),
			})
		}

		// renamed_from (MIG-F1-S2): the resource's previous table name. It must be a
		// valid identifier, differ from the current name, and NOT still be a declared
		// resource (you cannot rename from a name that still exists).
		if res.RenamedFrom != "" {
			switch {
			case !resourceNameRe.MatchString(res.RenamedFrom):
				errs = append(errs, ValidationError{
					Field:   resPrefix + ".renamed_from",
					Message: fmt.Sprintf("invalid renamed_from %q: must match ^[a-z][a-z0-9_]*$", res.RenamedFrom),
				})
			case res.RenamedFrom == resName:
				errs = append(errs, ValidationError{
					Field:   resPrefix + ".renamed_from",
					Message: "renamed_from must differ from the resource's own name",
				})
			default:
				if _, exists := s.Resources[res.RenamedFrom]; exists {
					errs = append(errs, ValidationError{
						Field:   resPrefix + ".renamed_from",
						Message: fmt.Sprintf("renamed_from %q is still a declared resource — you cannot rename from a name that still exists (remove the old resource)", res.RenamedFrom),
					})
				}
			}
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

			// on_delete (MIG-F1-S1): the FK's referential action. Only valid on a
			// relation field; set_null requires the column be nullable (else
			// Postgres rejects the SET NULL at delete time — caught at load here).
			if field.OnDelete != "" {
				switch {
				case field.Relation == "":
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_delete",
						Message: "on_delete is only valid on a field that declares a relation",
					})
				case !validOnDeleteActions[field.OnDelete]:
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_delete",
						Message: fmt.Sprintf("unknown on_delete %q: must be one of restrict, cascade, set_null", field.OnDelete),
					})
				case field.OnDelete == OnDeleteSetNull && field.Required:
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_delete",
						Message: "on_delete set_null requires a nullable column, but this field is required (NOT NULL)",
					})
				}
			}

			// on_update (MIG-F1-S5): the FK's ON UPDATE action. Same shape as on_delete
			// (only valid on a relation; set_null requires a nullable column). The unset
			// default is NO ACTION (no-churn), resolved in buildDesiredSchema.
			if field.OnUpdate != "" {
				switch {
				case field.Relation == "":
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_update",
						Message: "on_update is only valid on a field that declares a relation",
					})
				case !validOnUpdateActions[field.OnUpdate]:
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_update",
						Message: fmt.Sprintf("unknown on_update %q: must be one of restrict, cascade, set_null", field.OnUpdate),
					})
				case field.OnUpdate == OnUpdateSetNull && field.Required:
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".on_update",
						Message: "on_update set_null requires a nullable column, but this field is required (NOT NULL)",
					})
				}
			}

			// references (MIG-F1-S5): the FK's target COLUMN. Only valid on a relation;
			// must be "id" (the default) or a column that is UNIQUE on the target
			// (Postgres requires a unique/PK destination), and type-compatible with this
			// field. The target's existence was already checked above.
			if field.References != "" && field.References != "id" {
				if field.Relation == "" {
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".references",
						Message: "references is only valid on a field that declares a relation",
					})
				} else if target, ok := s.Resources[field.Relation]; ok {
					if !columnsAreUniqueOnTarget(target, []string{field.References}) {
						errs = append(errs, ValidationError{
							Field:   fieldPrefix + ".references",
							Message: fmt.Sprintf("references %q must be the target's id or a UNIQUE column of %q (a foreign key may only point at a primary key or unique column)", field.References, field.Relation),
						})
					} else if k, tk := pgKindForAPIType(field.Type), refColumnKind(target, field.References); k != tk {
						errs = append(errs, ValidationError{
							Field:   fieldPrefix + ".references",
							Message: fmt.Sprintf("references %q has an incompatible type: this field is %q but %s.%s is %q", field.References, field.Type, field.Relation, field.References, targetFieldType(target, field.References)),
						})
					}
				}
			}

			// renamed_from (MIG-F1-S2): the field's previous column name. Valid
			// identifier, differs from the current name, and NOT still a declared
			// field of this resource (cannot rename from a name that still exists).
			if field.RenamedFrom != "" {
				switch {
				case !fieldNameRe.MatchString(field.RenamedFrom):
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".renamed_from",
						Message: fmt.Sprintf("invalid renamed_from %q: must match ^[a-z][a-z0-9_]*$", field.RenamedFrom),
					})
				case field.RenamedFrom == fieldName:
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".renamed_from",
						Message: "renamed_from must differ from the field's own name",
					})
				case field.RenamedFrom == "id":
					errs = append(errs, ValidationError{
						Field:   fieldPrefix + ".renamed_from",
						Message: "cannot rename from \"id\" (the implicit primary key)",
					})
				default:
					if _, exists := res.Fields[field.RenamedFrom]; exists {
						errs = append(errs, ValidationError{
							Field:   fieldPrefix + ".renamed_from",
							Message: fmt.Sprintf("renamed_from %q is still a declared field — you cannot rename from a name that still exists (remove the old field)", field.RenamedFrom),
						})
					}
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
			errs = append(errs, validateStateMachine(fieldPrefix, field)...)
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
		errs = append(errs, validateForeignKeys(resPrefix, res, s)...)

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

	errs = append(errs, validateRBAC(s)...)

	return errs
}

// validRBACActions is the closed set accepted in an RBAC actions/condition_actions
// list. "*" grants all (not meaningful in condition_actions — see validateRBAC).
var validRBACActions = map[string]bool{
	"read": true, "create": true, "update": true, "delete": true, "*": true,
}

// validateRBAC checks the per-resource permissions form (G2): mutual exclusivity
// with the legacy role-global form, and — for each per-resource grant — that the
// resource exists, the actions are known, the condition references a field that
// EXISTS ON THAT RESOURCE (so a condition over a column the resource lacks fails at
// load, never a masked 500 at runtime), condition_actions is a valid subset, and the
// field allowlist names real fields.
//
// The LEGACY role-global form is intentionally NOT newly validated — existing
// schemas must keep behaving identically (a global condition over a wildcard
// resource set cannot be field-checked and was never an error). Only the new
// surface gets the stricter checks.
func validateRBAC(s *APISchema) []ValidationError {
	var errs []ValidationError
	for roleName, role := range s.RBAC.Roles {
		if len(role.Permissions) == 0 {
			continue // legacy-only (or empty) role: unchanged behaviour
		}
		rolePrefix := "rbac.roles." + roleName

		// Mutual exclusivity: a role uses one form or the other, never both — mixing
		// would make "which condition wins" ambiguous, unacceptable for authorization.
		if len(role.Resources) > 0 || len(role.Actions) > 0 || role.Conditions != nil || len(role.Fields) > 0 {
			errs = append(errs, ValidationError{
				Field:   rolePrefix,
				Message: "a role uses EITHER the role-global form (resources/actions/conditions/fields) OR per-resource permissions, not both — move the role-global keys into permissions entries",
			})
		}

		for resName, perm := range role.Permissions {
			permPrefix := rolePrefix + ".permissions." + resName
			res, ok := s.Resources[resName]
			if !ok {
				errs = append(errs, ValidationError{
					Field:   permPrefix,
					Message: fmt.Sprintf("permission references unknown resource %q", resName),
				})
				continue
			}

			if len(perm.Actions) == 0 {
				errs = append(errs, ValidationError{
					Field:   permPrefix + ".actions",
					Message: "at least one action is required (read, create, update, delete, or *)",
				})
			}
			for _, a := range perm.Actions {
				if !validRBACActions[a] {
					errs = append(errs, ValidationError{
						Field:   permPrefix + ".actions",
						Message: fmt.Sprintf("unknown action %q: must be one of read, create, update, delete, *", a),
					})
				}
			}

			if perm.Conditions != nil {
				if perm.Conditions.Field == "" {
					errs = append(errs, ValidationError{
						Field:   permPrefix + ".conditions.field",
						Message: "condition field is required",
					})
				} else if !rbacFieldExists(res, perm.Conditions.Field) {
					errs = append(errs, ValidationError{
						Field:   permPrefix + ".conditions.field",
						Message: fmt.Sprintf("condition field %q does not exist on resource %q", perm.Conditions.Field, resName),
					})
				}
			}

			// condition_actions scopes the condition to a subset of the granted
			// actions; it is meaningless without a condition, and every entry must be a
			// concrete granted action ("*" not allowed — omit the list for "all").
			if len(perm.ConditionActions) > 0 {
				if perm.Conditions == nil {
					errs = append(errs, ValidationError{
						Field:   permPrefix + ".condition_actions",
						Message: "condition_actions requires conditions to be set",
					})
				}
				grantsAll := false
				for _, a := range perm.Actions {
					if a == "*" {
						grantsAll = true
					}
				}
				for _, a := range perm.ConditionActions {
					switch {
					case a == "*" || !validRBACActions[a]:
						errs = append(errs, ValidationError{
							Field:   permPrefix + ".condition_actions",
							Message: fmt.Sprintf("invalid condition action %q: must be a concrete action (read, create, update, delete)", a),
						})
					case !grantsAll && !containsStr(perm.Actions, a):
						errs = append(errs, ValidationError{
							Field:   permPrefix + ".condition_actions",
							Message: fmt.Sprintf("condition_actions lists %q which is not in actions", a),
						})
					}
				}
			}

			for _, f := range perm.Fields {
				if !rbacFieldExists(res, f) {
					errs = append(errs, ValidationError{
						Field:   permPrefix + ".fields",
						Message: fmt.Sprintf("field %q does not exist on resource %q", f, resName),
					})
				}
			}
		}
	}
	return errs
}

// rbacFieldExists reports whether name is a column the resource exposes: a declared
// field or the implicit `id` primary key (an allowlist or owner condition may name
// either).
func rbacFieldExists(res ResourceSchema, name string) bool {
	if name == "id" {
		return true
	}
	_, ok := res.Fields[name]
	return ok
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
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

// validateForeignKeys checks each resource-level COMPOSITE foreign key (MIG-F1-S5):
// at least one source column (all existing on this resource), a known target, a
// matching count of ref_columns that together form a PK/unique on the target, valid
// on_delete/on_update actions, set_null only over nullable source columns, and
// per-position type compatibility. A single-column FK should use the field-level
// `relation` instead; the block is for the genuinely multi-column case.
func validateForeignKeys(resPrefix string, res ResourceSchema, s *APISchema) []ValidationError {
	var errs []ValidationError
	for i, fk := range res.ForeignKeys {
		fkPrefix := fmt.Sprintf("%s.foreign_keys[%d]", resPrefix, i)

		if len(fk.Columns) == 0 {
			errs = append(errs, ValidationError{Field: fkPrefix + ".columns", Message: "foreign key must list at least one column"})
		}
		anyRequired := false
		for _, c := range fk.Columns {
			if c == "id" {
				continue // the implicit PK is a valid source column
			}
			f, ok := res.Fields[c]
			if !ok {
				errs = append(errs, ValidationError{Field: fkPrefix + ".columns", Message: fmt.Sprintf("column %q does not exist on this resource", c)})
				continue
			}
			if f.Required {
				anyRequired = true
			}
		}

		target, ok := s.Resources[fk.Target]
		if !ok {
			errs = append(errs, ValidationError{Field: fkPrefix + ".target", Message: fmt.Sprintf("foreign key target %q references unknown resource", fk.Target)})
		}

		if len(fk.RefColumns) == 0 {
			errs = append(errs, ValidationError{Field: fkPrefix + ".ref_columns", Message: "foreign key must list at least one ref_column"})
		}
		if len(fk.Columns) != len(fk.RefColumns) {
			errs = append(errs, ValidationError{Field: fkPrefix, Message: fmt.Sprintf("columns (%d) and ref_columns (%d) must have the same length", len(fk.Columns), len(fk.RefColumns))})
		}

		if ok { // target exists — check ref_columns existence, uniqueness and types
			for _, rc := range fk.RefColumns {
				if rc != "id" {
					if _, has := target.Fields[rc]; !has {
						errs = append(errs, ValidationError{Field: fkPrefix + ".ref_columns", Message: fmt.Sprintf("ref_column %q does not exist on target %q", rc, fk.Target)})
					}
				}
			}
			if !columnsAreUniqueOnTarget(target, fk.RefColumns) {
				errs = append(errs, ValidationError{Field: fkPrefix + ".ref_columns", Message: fmt.Sprintf("ref_columns must together form the target's primary key or a UNIQUE constraint/index on %q (a foreign key may only point at a unique key)", fk.Target)})
			}
			// Per-position type compatibility (Postgres requires the column types to match).
			if len(fk.Columns) == len(fk.RefColumns) {
				for j := range fk.Columns {
					srcKind := refColumnKind(res, fk.Columns[j])
					refKind := refColumnKind(target, fk.RefColumns[j])
					if srcKind != "" && refKind != "" && srcKind != refKind {
						errs = append(errs, ValidationError{Field: fkPrefix, Message: fmt.Sprintf("type mismatch: %s (%s) references %s.%s (%s)", fk.Columns[j], targetFieldType(res, fk.Columns[j]), fk.Target, fk.RefColumns[j], targetFieldType(target, fk.RefColumns[j]))})
					}
				}
			}
		}

		if fk.OnDelete != "" && !validOnDeleteActions[fk.OnDelete] {
			errs = append(errs, ValidationError{Field: fkPrefix + ".on_delete", Message: fmt.Sprintf("unknown on_delete %q: must be one of restrict, cascade, set_null", fk.OnDelete)})
		}
		if fk.OnUpdate != "" && !validOnUpdateActions[fk.OnUpdate] {
			errs = append(errs, ValidationError{Field: fkPrefix + ".on_update", Message: fmt.Sprintf("unknown on_update %q: must be one of restrict, cascade, set_null", fk.OnUpdate)})
		}
		// set_null on either action requires every source column to be nullable.
		if (fk.OnDelete == OnDeleteSetNull || fk.OnUpdate == OnUpdateSetNull) && anyRequired {
			errs = append(errs, ValidationError{Field: fkPrefix, Message: "set_null requires all source columns to be nullable, but at least one is required (NOT NULL)"})
		}
	}
	return errs
}

// columnsAreUniqueOnTarget reports whether the column set cols is guaranteed unique
// on res — the precondition Postgres enforces for a foreign-key destination. True
// when cols is exactly the implicit primary key (["id"]), a single field declared
// `unique`, or the column set of some declared UNIQUE index (composite or single).
func columnsAreUniqueOnTarget(res ResourceSchema, cols []string) bool {
	if len(cols) == 1 {
		if cols[0] == "id" {
			return true
		}
		if f, ok := res.Fields[cols[0]]; ok && f.Unique {
			return true
		}
	}
	for _, idx := range res.Indexes {
		if idx.Unique && sameStringSet(idx.Fields, cols) {
			return true
		}
	}
	return false
}

// sameStringSet reports whether a and b contain the same elements (order-independent;
// a foreign key may reference a unique constraint's columns in any order).
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}

// pgKindForAPIType maps an Appitools field type to a coarse Postgres compatibility
// class for foreign-key type checking. Two columns may be joined by an FK only when
// their classes match. string/text/json all land on TEXT; the implicit id is uuid.
func pgKindForAPIType(t string) string {
	switch t {
	case "string", "text", "json":
		return "text"
	case "int":
		return "integer"
	case "int64":
		return "bigint"
	case "float64":
		return "double"
	case "bool":
		return "bool"
	case "uuid":
		return "uuid"
	case "time":
		return "timestamptz"
	default:
		return ""
	}
}

// refColumnKind returns the FK-compatibility class of a column on res — "uuid" for
// the implicit id, else the field's type class. Empty if the column is unknown.
func refColumnKind(res ResourceSchema, col string) string {
	if col == "id" {
		return "uuid"
	}
	if f, ok := res.Fields[col]; ok {
		return pgKindForAPIType(f.Type)
	}
	return ""
}

// targetFieldType returns the human-readable type of a column on res (for error
// messages): "uuid" for the implicit id, else the declared field type.
func targetFieldType(res ResourceSchema, col string) string {
	if col == "id" {
		return "uuid"
	}
	if f, ok := res.Fields[col]; ok {
		return f.Type
	}
	return "?"
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

// validateStateMachine checks a field's `state_machine` DEFINITION (G5) at load so a
// bad lifecycle is rejected cleanly, never a surprise at request time: the field must
// be string/text, at least one `initial` state, every referenced state must be an
// `enum` value when an enum is declared (coherence), and a string `default` must be
// one of the initial states (you can only default to a state a row may be created
// in). A field without `state_machine` produces no errors.
func validateStateMachine(fieldPrefix string, fd FieldDef) []ValidationError {
	if fd.StateMachine == nil {
		return nil
	}
	sm := fd.StateMachine
	smPrefix := fieldPrefix + ".state_machine"
	var errs []ValidationError

	if !stringTypes[fd.Type] {
		errs = append(errs, ValidationError{
			Field:   smPrefix,
			Message: fmt.Sprintf("state_machine only applies to string/text fields, not %q", fd.Type),
		})
		return errs // the rest of the checks assume string states
	}
	if len(sm.Initial) == 0 {
		errs = append(errs, ValidationError{
			Field:   smPrefix + ".initial",
			Message: "state_machine requires at least one initial state",
		})
	}
	for _, s := range sm.Initial {
		if s == "" {
			errs = append(errs, ValidationError{Field: smPrefix + ".initial", Message: "initial state must be a non-empty string"})
		}
	}

	known := sm.KnownStates()

	// Coherence with enum: every state the machine names must be an allowed enum
	// value (a transition to a state the field can never hold is dead config).
	if len(fd.Enum) > 0 {
		enumSet := make(map[string]bool, len(fd.Enum))
		for _, e := range fd.Enum {
			enumSet[e] = true
		}
		states := make([]string, 0, len(known))
		for s := range known {
			states = append(states, s)
		}
		sort.Strings(states)
		for _, s := range states {
			if !enumSet[s] {
				errs = append(errs, ValidationError{
					Field:   smPrefix,
					Message: fmt.Sprintf("state %q is not one of the field's enum values", s),
				})
			}
		}
	}

	// A string default must be an initial state — a row's create-time value.
	if ds, ok := fd.Default.(string); ok {
		if !sm.IsInitial(ds) {
			errs = append(errs, ValidationError{
				Field:   fieldPrefix + ".default",
				Message: fmt.Sprintf("default %q must be one of the state_machine initial states", ds),
			})
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
