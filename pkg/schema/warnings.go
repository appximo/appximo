package schema

import (
	"encoding/json"
	"fmt"
	"sort"
)

// SCHEMA-5 — warnings: a schema that is VALID and probably wrong.
//
// The validator's job is "may this schema run?". This file answers a different
// question — "will it do what you meant?" — for the small set of mistakes that are
// legal, deployable, and produce an app that quietly does nothing.
//
// Warnings never block anything: they are printed by `appximo validate`, carried
// in `validate --json`'s report, returned by the control plane's deploy response and
// logged at boot. They exist because the failures they catch have NO error at any
// layer, so the only place they can surface is here.
//
// The bar for adding one: the mistake must be (a) legal today, (b) almost always a
// mistake, and (c) invisible in production except as "the app shows me nothing".

// Warnings returns the non-blocking findings for a schema. A nil/empty result means
// nothing suspicious was found — never that the schema is correct.
func Warnings(s *APISchema) []ValidationError {
	if s == nil {
		return nil
	}
	out := identityConditionWarnings(s)
	out = append(out, bareVariableConditionWarnings(s)...)
	out = append(out, fileGrantWarnings(s)...)
	out = append(out, requiredTextWarnings(s)...)
	out = append(out, requiredConditionFieldWarnings(s)...)
	out = append(out, timestampConventionWarnings(s)...)
	out = append(out, autoUpdateIntentWarnings(s)...)
	out = append(out, graphqlListShadowWarnings(s)...)
	return out
}

// graphqlListShadowWarnings (SILENT-CORRUPTION-S1, ENG-45 audit) flags a
// resource whose name its own singularization leaves unchanged
// (GraphQLSingular("menu") == "menu"): the GraphQL builder registers the list
// query under the resource name and the get-by-id query under the singular, so
// when the two coincide the get-by-id field silently REPLACES the list query —
// the resource has no GraphQL list/filter/pagination at all, and the emitted
// SDL carries two same-named Query fields (structurally invalid for client
// codegen). REST is unaffected. A warning, not an error: the schema works
// fully on REST and partially on GraphQL, and refusing to boot would break
// running apps at upgrade; the real fix (a collision-free get-by-id name) is a
// GraphQL contract change that earns its own increment (ENG-45).
func graphqlListShadowWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	for _, resName := range sortedNames(s.Resources) {
		if GraphQLSingular(resName) != resName {
			continue
		}
		out = append(out, ValidationError{
			Field: "resources." + resName,
			Rule:  "graphql_list_query_shadowed",
			Got:   resName,
			Message: fmt.Sprintf(
				"%q singularizes to itself, so its GraphQL get-by-id query takes the same name as its list query and silently replaces it — the resource has NO list/filter/pagination query in GraphQL (REST is unaffected).",
				resName),
			Fix: fmt.Sprintf("name the resource in plural form (e.g. %q) so the list query (%q) and the get-by-id query (%q) get distinct names — or ignore this if you never consume this resource over GraphQL", resName+"s", resName+"s", resName),
		})
	}
	return out
}

// autoUpdateIntentWarnings (SILENT-CORRUPTION-S1) flags the worst validator-clean
// data corruption the ENG-45 audit found: a LEGACY `auto: true` field whose NAME
// reads as a modification timestamp but is not the literal `updated_at`. The
// legacy boolean binds refresh-on-update to that one English name, so
// `modificado_en` — the name a Spanish schema (the AI-generation flow's primary
// audience) naturally uses — is set once at creation and FROZEN forever, while
// every response shows it as if it tracked changes; and it cannot be repaired by
// hand either (an auto field in an update body is 422 read_only). Silent wrong
// data on a schema every layer approved.
//
// It is a warning, not an error, because auto-with-any-name is a legal and
// correct CREATION timestamp (placed_at, joined_at, creada — the engine's own
// examples use it); only a name that suggests update intent is almost certainly
// wrong. The name test is a small documented marker list (the same license the
// identity-column suppression list takes) and gates a warning only — a missed
// name costs a warning, never behavior. Both fixes silence it: declare
// `"auto": "update"` (the engine refreshes it, any name) or `"auto": "create"`
// (the frozen behavior, made explicit).
func autoUpdateIntentWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	for _, resName := range sortedNames(s.Resources) {
		res := s.Resources[resName]
		for _, fieldName := range sortedNames(res.Fields) {
			fd := res.Fields[fieldName]
			if fd.Auto != AutoLegacy || fieldName == autoUpdateName {
				continue
			}
			if !nameSuggestsUpdateIntent(fieldName) {
				continue
			}
			out = append(out, ValidationError{
				Field: fmt.Sprintf("resources.%s.fields.%s.auto", resName, fieldName),
				Rule:  "auto_update_intent",
				Got:   fieldName,
				Message: fmt.Sprintf(
					"%q declares the legacy `auto: true`, which refreshes on update ONLY the literal name `updated_at` — this field will be set once at creation and FROZEN forever (and it cannot be written by hand either: an auto field in an update body is 422 read_only), while its name says it tracks modifications. Every response would show a modification timestamp that is silently wrong.",
					fieldName),
				Fix: `declare "auto": "update" — the engine then refreshes this field on every update, whatever its name. If you really meant a creation timestamp, declare "auto": "create" to make that explicit.`,
			})
		}
	}
	return out
}

// timestampConventionWarnings (FRESH-AGENT-GAPS-S1) flags a resource that
// breaks its OWN schema's timestamp convention. The engine never provides or
// needs `created_at`/`updated_at` — they are ordinary declared fields — but
// every teaching example declares them, so authors come to believe they are
// implicit like `id`. The observed failure: an agent added six resources
// without `created_at` to an app whose OTHER resources all declared it; the
// schema validated (correctly — it may run), and the app's own handlers and
// UI, written against the convention, broke at request time with
// `column "created_at" does not exist`. No layer had said a word.
//
// The rule is CONVENTION-RELATIVE to stay high-signal: it fires only when at
// least one other resource in the schema declares a timestamp and THIS
// resource declares neither. A deliberately timestamp-less schema stays
// silent, and a resource that declares either column (an `updated_at`-only
// config singleton, say) is treated as having made a choice. Pure junction
// resources (every field is a relation FK) are exempt — nothing sorts or
// displays them directly.
func timestampConventionWarnings(s *APISchema) []ValidationError {
	convention := 0
	for _, res := range s.Resources {
		if _, ok := res.Fields["created_at"]; ok {
			convention++
			continue
		}
		if _, ok := res.Fields["updated_at"]; ok {
			convention++
		}
	}
	if convention == 0 {
		return nil // a schema with no timestamps anywhere is a style, not a slip
	}
	var out []ValidationError
	for _, resName := range sortedNames(s.Resources) {
		res := s.Resources[resName]
		if _, ok := res.Fields["created_at"]; ok {
			continue
		}
		if _, ok := res.Fields["updated_at"]; ok {
			continue
		}
		junction := len(res.Fields) > 0
		for _, f := range res.Fields {
			if f.Relation == "" {
				junction = false
				break
			}
		}
		if junction {
			continue
		}
		out = append(out, ValidationError{
			Field: fmt.Sprintf("resources.%s", resName),
			Rule:  "missing_timestamps_convention",
			Got:   resName,
			Message: fmt.Sprintf(
				"%q declares neither created_at nor updated_at while %d other resource(s) in this schema do. The engine does NOT provide timestamps implicitly (only `id` is implicit) — anything written against the convention (a handler's ORDER BY created_at, a UI column, ?sort=created_at) fails at request time with a column-does-not-exist error the validator cannot see.",
				resName, convention),
			Fix: `declare "created_at": {"type":"time","auto":true} (and "updated_at" likewise) — or omit them deliberately and make sure nothing references them`,
		})
	}
	return out
}

// requiredConditionFieldWarnings (LAUNCHPAD-S1) flags the ownership column that
// is BOTH `required` and the target of a role's row condition. On create the
// engine FORCES that column to the caller's identity — but it does so in
// EnforceCreateRBAC, which runs AFTER the declarative validation that checks
// `required`. So a create by that role, omitting the column exactly as it
// should, answers 422 "<field> is required" for a value the caller was never
// supposed to send and the engine was about to fill in itself.
//
// It is a warning, not an error, because the pattern is legal: another role
// (an admin with no condition) may legitimately be required to supply the
// column. But for the scoped role it is a dead end, and the 422 blames the
// client for a schema misconfiguration — the SCHEMA-5 bar exactly. Found by a
// third-party agent building a recipe box from the master prompt: it hit the
// 422, correctly diagnosed the ordering itself, and dropped `required`.
func requiredConditionFieldWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	for _, roleName := range sortedNames(s.RBAC.Roles) {
		role := s.RBAC.Roles[roleName]
		type scoped struct{ resource, field string }
		var hits []scoped
		add := func(resName string, cond *Condition, condActions, actions []string) {
			if cond == nil || !identityVal(cond.Val) {
				return // a literal condition is not identity-forced on create
			}
			if !actionListAllows(actions, "create") {
				return
			}
			// condition_actions narrows which actions the condition gates; a
			// condition that does not gate create is never injected there.
			if len(condActions) > 0 && !actionListAllows(condActions, "create") {
				return
			}
			res, ok := s.Resources[resName]
			if !ok {
				return
			}
			if fd, ok := res.Fields[cond.Field]; ok && fd.Required {
				hits = append(hits, scoped{resName, cond.Field})
			}
		}
		if len(role.Permissions) > 0 {
			for _, resName := range sortedNames(role.Permissions) {
				p := role.Permissions[resName]
				add(resName, p.Conditions, p.ConditionActions, p.Actions)
			}
		} else {
			for _, resName := range applicableResources(role.Resources, s) {
				add(resName, role.Conditions, nil, role.Actions)
			}
		}
		for _, h := range hits {
			out = append(out, ValidationError{
				Field: fmt.Sprintf("resources.%s.fields.%s", h.resource, h.field),
				Rule:  "required_field_is_rbac_forced",
				Got:   "required: true",
				Message: fmt.Sprintf(
					"%q is required AND is the row-condition column of role %q, which may create %s. On create the engine forces this column to the caller's identity — but AFTER the required check runs, so every create by that role answers 422 %q is required for a value it was never meant to send. Nothing else reports this: the schema is valid and creates just fail.",
					h.field, roleName, h.resource, h.field),
				Fix: fmt.Sprintf("drop \"required\": true from resources.%s.fields.%s — the row condition already forces the value on create, and reads/updates stay scoped to the owner", h.resource, h.field),
			})
		}
	}
	return out
}

// identityVal reports whether a condition value is one of the two session
// variables the engine resolves per request (as opposed to a literal).
func identityVal(v string) bool {
	return v == "$user_id" || v == "$external_client_id"
}

// fileGrantWarnings (PUBLIC-SURFACE-S1 Part E) flags a role that can WRITE a
// resource carrying a `file` field but has NO grant on the built-in "files"
// store: its flow is upload (POST /api/files) → attach, and the upload answers
// 403 before the record is ever created. Legal (maybe another role uploads and
// this one only sets ids), almost always a mistake (measured in the second
// field evaluation: every upload from the role 403'd until the grant was
// added), invisible until the first user hits the button — the SCHEMA-5 bar.
func fileGrantWarnings(s *APISchema) []ValidationError {
	if _, shadowed := s.Resources["files"]; shadowed {
		return nil // a resource literally named files replaces the built-in store
	}
	var out []ValidationError
	for _, roleName := range sortedNames(s.RBAC.Roles) {
		role := s.RBAC.Roles[roleName]
		if roleGrantsFilesAction(role, s, "create") {
			continue
		}
		// The resources this role can create/update that carry a file field.
		var hits []string
		for _, resName := range roleWritableResources(role, s) {
			res := s.Resources[resName]
			for _, fieldName := range sortedNames(res.Fields) {
				if res.Fields[fieldName].Type == "file" {
					hits = append(hits, fmt.Sprintf("%s.%s", resName, fieldName))
				}
			}
		}
		if len(hits) == 0 {
			continue
		}
		fix := `add "files": { "actions": ["create", "read"] } to this role's permissions`
		if len(role.Permissions) == 0 {
			fix = `add "files" to this role's resources list (its actions already apply), or switch to the permissions form and grant "files": { "actions": ["create", "read"] }`
		}
		out = append(out, ValidationError{
			Field: "rbac.roles." + roleName,
			Rule:  "file_field_without_files_grant",
			Got:   roleName,
			Message: fmt.Sprintf(
				"role %q can write %s — file field(s) whose flow is upload first (POST /api/files), then attach — but has no grant on the built-in \"files\" store, so every upload answers 403 before the record is ever created. Nothing else reports this: the schema is valid and the form just fails.",
				roleName, joinAnd(hits)),
			Fix: fix,
		})
	}
	return out
}

// roleGrantsFilesAction reports whether the role may perform `action` on the
// built-in files store, in either RBAC form.
func roleGrantsFilesAction(role RolePolicy, s *APISchema, action string) bool {
	if len(role.Permissions) > 0 {
		p, ok := role.Permissions["files"]
		return ok && actionListAllows(p.Actions, action)
	}
	return rawResourcesInclude(role.Resources) && actionListAllows(role.Actions, action)
}

// rawResourcesInclude reports whether a role-global `resources` value covers the
// built-in files store: the wildcard "*", or a list naming "files".
func rawResourcesInclude(raw json.RawMessage) bool {
	var star string
	if json.Unmarshal(raw, &star) == nil {
		return star == "*"
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		for _, r := range list {
			if r == "files" {
				return true
			}
		}
	}
	return false
}

func actionListAllows(actions []string, action string) bool {
	for _, a := range actions {
		if a == action || a == "*" {
			return true
		}
	}
	return false
}

// roleWritableResources lists the DECLARED resources the role may create or
// update, in either RBAC form, sorted.
func roleWritableResources(role RolePolicy, s *APISchema) []string {
	var out []string
	if len(role.Permissions) > 0 {
		for _, resName := range sortedNames(role.Permissions) {
			if _, declared := s.Resources[resName]; !declared {
				continue
			}
			acts := role.Permissions[resName].Actions
			if actionListAllows(acts, "create") || actionListAllows(acts, "update") {
				out = append(out, resName)
			}
		}
		return out
	}
	if !actionListAllows(role.Actions, "create") && !actionListAllows(role.Actions, "update") {
		return nil
	}
	return applicableResources(role.Resources, s)
}

// requiredTextWarnings (PUBLIC-SURFACE-S1 Part E) flags a required string/text
// field with NO content rule: `required` rejects an ABSENT key and an explicit
// null, but the empty string "" is a present string value — an empty form field
// submits "", passes, and creates blank records with 201. The spec documents
// the trap twice (frontend-spec §4.6 and §6.4); documented twice means
// recurrent, so the validator now says it where the schema is written. A field
// with minLength/enum/pattern/format already constrains content and is not
// flagged.
func requiredTextWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	for _, resName := range sortedNames(s.Resources) {
		res := s.Resources[resName]
		for _, fieldName := range sortedNames(res.Fields) {
			f := res.Fields[fieldName]
			if f.Type != "string" && f.Type != "text" {
				continue
			}
			if !f.Required || f.MinLength != nil || len(f.Enum) > 0 || f.Pattern != "" || f.Format != "" {
				continue
			}
			out = append(out, ValidationError{
				Field: fmt.Sprintf("resources.%s.fields.%s", resName, fieldName),
				Rule:  "required_text_without_min_length",
				Got:   fieldName,
				Message: fmt.Sprintf(
					"%q is required but the empty string \"\" satisfies `required` (it is a present value — only an absent key or null is rejected): an empty form field creates blank %q records with 201.",
					fieldName, resName),
				Fix: `declare "minLength": 1 so an empty submission is a named 422 instead of a blank record`,
			})
		}
	}
	return out
}

// joinAnd renders a short list as prose: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		last := items[len(items)-1]
		out := ""
		for _, it := range items[:len(items)-1] {
			if out != "" {
				out += ", "
			}
			out += it
		}
		return out + " and " + last
	}
}

// bareVariableNames maps a condition `val` that is a known variable MINUS its `$`
// to the variable the author almost certainly meant. The dollar-PREFIXED typos
// ($userid, $tenant_id) are load ERRORS since ENG-20 — a `$` announces intent, so
// an unresolvable one rejects the schema. The bare form cannot be an error: a
// literal condition val is legitimate (`val: "published"`), and "user_id" IS a
// possible literal. But a column holding the string "user_id" is vanishingly
// rare, while a forgotten `$` produces the exact zero-rows-forever failure
// SCHEMA-5 exists to warn about — so it meets the warning bar: legal, almost
// always a mistake, invisible in production.
var bareVariableNames = map[string]string{
	"user_id":            "$user_id",
	"external_client_id": "$external_client_id",
}

// bareVariableConditionWarnings flags row conditions whose literal val is a known
// dynamic variable's name without the `$` (ENG-20's warning half).
func bareVariableConditionWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	warn := func(path string, cond *Condition) {
		if cond == nil {
			return
		}
		meant, ok := bareVariableNames[cond.Val]
		if !ok {
			return
		}
		out = append(out, ValidationError{
			Field: path + ".val",
			Rule:  "bare_condition_variable",
			Got:   cond.Val,
			Message: fmt.Sprintf(
				"val %q is the LITERAL string %q, not the dynamic variable %s — rows would only match if the column literally contains the text %q, which almost certainly means the rule matches NO rows, ever, with no error anywhere.",
				cond.Val, cond.Val, meant, cond.Val),
			Fix: fmt.Sprintf("if you meant the id of the caller, write %q (with the dollar sign); if you really compare against the literal text %q, ignore this warning", meant, cond.Val),
		})
	}
	for _, roleName := range sortedNames(s.RBAC.Roles) {
		role := s.RBAC.Roles[roleName]
		prefix := "rbac.roles." + roleName
		warn(prefix+".conditions", role.Conditions)
		for _, resName := range sortedNames(role.Permissions) {
			warn(fmt.Sprintf("%s.permissions.%s.conditions", prefix, resName), role.Permissions[resName].Conditions)
		}
	}
	return out
}

// identityColumnNames are the column names that conventionally hold the id of the
// LOGGED-IN user (the JWT subject). A relation pointing at one of these is the
// documented bridge between a catalogue table and the login — exactly the shape the
// check suggests — so it is not flagged.
var identityColumnNames = map[string]bool{
	"user_id":      true,
	"auth_user_id": true,
	"login_id":     true,
}

// identityConditionWarnings finds row conditions that compare the LOGGED-IN user's
// id against a column holding a FOREIGN KEY to another resource — and asks the
// author to CHECK which id space that column holds. It cannot know.
//
// Measured in production (docs/AUTHORING_JOURNEY.md 5-1 and 5-8): a generated schema
// scoped vets with `{field: "veterinarian_id", op: "eq", val: "$user_id"}`.
// `veterinarian_id` held the id of a row in `veterinarians` — a different id space
// from the JWT subject. The schema validated, deployed, and returned ZERO ROWS
// forever, with no error at any layer. The vet's app simply showed nothing.
//
// Measured in the field the OTHER way (MIGRACION-CONFIANZA-S1, a Symfony system
// migrated table-for-table): `declarations.related_user_id` is a relation to the
// migrated `users` table, and the JWT subject IS that table's id — the condition
// works (admin sees 6, one accountant 5, another 1, a cross read is 404). The
// previous wording asserted "matches NO rows, ever" and suggested renaming the
// column: a false verdict, and a fix that breaks parity with the source DDL to
// silence a validator that was guessing from a name.
//
// THE RULE (written here because it is the one that gets re-litigated): the
// schema cannot express "this column holds login ids" — the identity table is
// not a resource, so no relation can point at it — and a column NAME is never
// its semantics (C-DOCTRINA-2). So this check is a QUESTION, worded as one: it
// names the two id spaces that could be in play and what happens in each case,
// it never claims to know which one holds, and it NEVER asks for a rename — a
// name that comes from an existing DDL is a value (parity with the source), not
// an obstacle. The two ways to silence it are both declarations, not renames:
// point the relation at a column whose name says it holds login ids
// (`references: "user_id"`), or ignore it knowingly (it never blocks).
func identityConditionWarnings(s *APISchema) []ValidationError {
	var out []ValidationError
	for _, roleName := range sortedNames(s.RBAC.Roles) {
		role := s.RBAC.Roles[roleName]
		prefix := "rbac.roles." + roleName

		// Role-global form: the one condition applies to every resource the role lists.
		if role.Conditions != nil {
			for _, resName := range applicableResources(role.Resources, s) {
				out = append(out, identityConditionWarning(prefix+".conditions", resName, role.Conditions, s)...)
			}
		}
		// Per-resource form: each resource carries its own condition.
		for _, resName := range sortedNames(role.Permissions) {
			perm := role.Permissions[resName]
			if perm.Conditions == nil {
				continue
			}
			out = append(out, identityConditionWarning(
				fmt.Sprintf("%s.permissions.%s.conditions", prefix, resName), resName, perm.Conditions, s)...)
		}
	}
	return out
}

// identityConditionWarning checks ONE condition against ONE resource.
func identityConditionWarning(path, resName string, cond *Condition, s *APISchema) []ValidationError {
	if cond.Val != "$user_id" && cond.Val != "$external_client_id" {
		return nil // a literal condition compares values, not identities
	}
	res, ok := s.Resources[resName]
	if !ok {
		return nil
	}
	f, ok := res.Fields[cond.Field]
	if !ok {
		return nil
	}
	who := "the user who is logged in (the JWT subject)"
	if cond.Val == "$external_client_id" {
		who = "the external client making the request"
	}
	if f.Relation == "" {
		// S1 (FIELD-FEEDBACK-S1): no relation declared, but the column's NAME
		// derives from a sibling resource (instructor_id ↔ instructores) — the
		// same id-space question, one declaration short of the relation case,
		// and the shape an AI most often generates. Deliberately NOT flagged:
		// owner_id / author_id / created_by — the documented CORRECT pattern
		// stores login ids in columns whose base names no declared resource.
		if identityColumnNames[cond.Field] {
			return nil
		}
		implied := impliedRelationTarget(cond.Field, resName, s)
		if implied == "" {
			return nil
		}
		return []ValidationError{{
			Field: path + ".field",
			Rule:  "identity_condition_on_implied_relation",
			Got:   cond.Field,
			Message: fmt.Sprintf(
				"CHECK which id %q holds: this rule shows the rows of %q where %q equals the id of %s. "+
					"If %q stores login ids, the rule works as written. If it stores ids of %q rows (its name suggests so, but a name is not a contract), "+
					"the two id spaces never coincide and the rule matches no rows — with no error at any layer. The validator cannot tell which; you can.",
				cond.Field, resName, cond.Field, who, cond.Field, implied),
			Fix: fmt.Sprintf(
				"if %q already holds login ids: keep the column exactly as it is (a name that comes from your source data is worth keeping) and ignore this warning. "+
					"If it holds ids of %q rows: give %q a unique column holding the login id (e.g. \"user_id\") and declare %q with \"relation\": %q, \"references\": \"user_id\" — the warning then goes away without renaming anything.",
				cond.Field, implied, implied, cond.Field, implied),
		}}
	}
	target := f.References
	if target == "" {
		target = "id"
	}
	if identityColumnNames[target] {
		return nil // already pointed at a login id — the declared bridge; nothing to ask
	}
	return []ValidationError{{
		Field: path + ".field",
		Rule:  "identity_condition_on_relation",
		Got:   cond.Field,
		Message: fmt.Sprintf(
			"CHECK which id %q holds: this rule shows the rows of %q where %q equals the id of %s, and %q is a relation to %q (its %q column). "+
				"If %q.%q IS the login id — a migrated users table whose ids are the JWT subjects, or tokens minted with that id — the rule works as written. "+
				"If %q.%q is a different id space, the rule matches no rows — with no error at any layer. The validator cannot tell which; you can (one request as a scoped role answers it).",
			cond.Field, resName, cond.Field, who, cond.Field, f.Relation, target,
			f.Relation, target, f.Relation, target),
		Fix: fmt.Sprintf(
			"if %q.%q is the login id: keep the column and the relation exactly as they are (a name that comes from your source data is worth keeping) and ignore this warning. "+
				"If it is not: give %q a unique column holding the login id (e.g. \"user_id\"), point %q at it with \"references\": \"user_id\", and fill it in for the rows that already exist — the warning then goes away without renaming anything.",
			f.Relation, target, f.Relation, cond.Field),
	}}
}

// impliedRelationTarget reports the declared resource a column NAME points at:
// "instructor_id" matches a resource named "instructor", "instructors" or
// "instructores" (bare, +s, +es). Empty when nothing matches — an owner_id with
// no "owners" resource is the documented login-id pattern, not a suspect.
func impliedRelationTarget(field, selfRes string, s *APISchema) string {
	base, cut := stringsCutSuffix(field, "_id")
	if !cut || base == "" {
		return ""
	}
	for _, cand := range []string{base, base + "s", base + "es"} {
		if cand == selfRes {
			continue // a self-reference id column is its own topic
		}
		if _, ok := s.Resources[cand]; ok {
			return cand
		}
	}
	return ""
}

// stringsCutSuffix is strings.CutSuffix (kept local to avoid a new import line
// churning this file's diff).
func stringsCutSuffix(s, suffix string) (string, bool) {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)], true
	}
	return s, false
}

// sortedNames returns a map's keys in a stable order, so warnings are deterministic.
func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
