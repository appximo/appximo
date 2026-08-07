package schema

import (
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
	return out
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
// documented bridge between a catalogue table and the login — exactly the fix this
// warning recommends — so it is not flagged.
var identityColumnNames = map[string]bool{
	"user_id":      true,
	"auth_user_id": true,
	"login_id":     true,
}

// identityConditionWarnings finds row conditions that compare the LOGGED-IN user's
// id against a column holding a FOREIGN KEY to another resource.
//
// Measured in production (docs/AUTHORING_JOURNEY.md 5-1 and 5-8): a generated schema
// scoped vets with `{field: "veterinarian_id", op: "eq", val: "$user_id"}`.
// `veterinarian_id` holds the id of a row in `veterinarians` — a different id space
// from the JWT subject. The schema validated, deployed, and returned ZERO ROWS
// forever, with no error at any layer. The vet's app simply showed nothing.
//
// The pattern is legal (an FK column CAN legitimately hold login ids — that is the
// bridge pattern), so this is a warning, not an error. It is suppressed when the
// relation points at a column whose name says it holds a login id, which is what the
// suggested fix produces: apply the fix and the warning goes away.
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
	if f.Relation == "" {
		// S1 (FIELD-FEEDBACK-S1): no relation declared, but the column's NAME
		// derives from a sibling resource (instructor_id ↔ instructores) — the
		// same id-space mismatch, one declaration short of the relation case,
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
		who := "the user who is logged in"
		if cond.Val == "$external_client_id" {
			who = "the external client making the request"
		}
		return []ValidationError{{
			Field: path + ".field",
			Rule:  "identity_condition_on_implied_relation",
			Got:   cond.Field,
			Message: fmt.Sprintf(
				"this rule only shows rows of %q where %q equals the id of %s — but the column's name says it holds ids of %q rows, a different kind of id. "+
					"As written the rule would match NO rows, ever, and nothing reports an error: the app just shows nothing.",
				resName, cond.Field, who, implied),
			Fix: fmt.Sprintf(
				"if %q holds ids of %q: give %q a unique login-id column (\"user_id\") and declare %q with \"relation\": %q, \"references\": \"user_id\". "+
					"If %q really stores login ids already, rename it (e.g. \"user_id\") or ignore this warning.",
				cond.Field, implied, implied, cond.Field, implied, cond.Field),
		}}
	}
	target := f.References
	if target == "" {
		target = "id"
	}
	if identityColumnNames[target] {
		return nil // already pointed at a login id — this is the fix, not the bug
	}

	who := "the user who is logged in"
	if cond.Val == "$external_client_id" {
		who = "the external client making the request"
	}
	bridge := "user_id"
	return []ValidationError{{
		Field: path + ".field",
		Rule:  "identity_condition_on_relation",
		Got:   cond.Field,
		Message: fmt.Sprintf(
			"this rule only shows rows of %q where %q equals the id of %s — but %q holds the id of a row in %q, which is a different kind of id. "+
				"As written the rule matches NO rows, ever, and nothing reports an error: the app just shows nothing.",
			resName, cond.Field, who, cond.Field, f.Relation),
		Fix: fmt.Sprintf(
			"give %q a column that stores the login id (call it %q and mark it unique), point %q at it with \"references\": %q, and fill it in for the rows that already exist. "+
				"If %q already holds login ids, this warning does not apply.",
			f.Relation, bridge, cond.Field, bridge, cond.Field),
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
