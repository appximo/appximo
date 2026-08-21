package codegen

import (
	"github.com/appximo/appximo/pkg/schema"
)

// The SHARED body-preparation core for every write path (CTX-PARITY-S1).
//
// WHY THIS FILE EXISTS. The engine has two write paths that the documentation
// presents as equivalent: the GENERATED REST handlers (plus the GraphQL
// mutations and the batch transaction, which already share cores with them) and
// the LIBRARY path a consumer's custom Go handler calls (Ctx.Insert /
// Ctx.Update). backend-spec told consumers that Ctx.Insert validates "exactly
// like the generated POST" — and it did not: the generated handler applied the
// schema's `default` values, ran the create type check and enforced the
// state-machine initial states, and Ctx.Insert did none of the three.
//
// The consequence measured in the field (VecinGo, 18 resources, 13 custom
// handlers): rows written through a custom handler landed with a NULL status,
// and the NEXT transition failed with `invalid transition from ""`. The row was
// not merely missing a value — it was outside its own declared lifecycle, with
// no error at write time.
//
// This is the DUPLICATED_RULES class the project has closed twice before
// (AppendStateTransitionGuard for transitions, FieldDef.ReferencedColumn for
// relation joins). The cure is the same and it is not "patch the other path":
// ONE function that both paths call, so a step added here reaches both by
// construction and cannot be forgotten in one of them.

// PrepareCreate applies, IN THE GENERATED POST'S EXACT ORDER, everything that
// must happen to a create body before it reaches the database:
//
//  1. schema defaults for omitted fields — BEFORE validation, so a required
//     field WITH a default is satisfied by it;
//  2. the governed-field rule (WRITE-ASYMMETRY-S1) — `id` and `auto` fields
//     in the body are rejected 422 read_only unless the resource's `import`
//     declaration grants them to the caller's role (schema.
//     GovernedFieldViolations, the ONE implementation every door consults) —
//     PLUS the declarative rules (required, enum, min/max, length, pattern,
//     format) AND the value type check, all collected together so one
//     response carries every failing field (the S44 contract — reporting
//     them in phases makes a form UI mark two fields, then two different
//     ones);
//  3. the state-machine initial states — a row may only be CREATED in a
//     declared initial state.
//
// role is the caller's RBAC role, consulted ONLY by the governed-field rule
// (the import grant); pass the authenticated role, never a guess.
//
// It returns every violation at once; an empty slice means the body is ready
// for RBAC enforcement, the file-policy check and the INSERT.
//
// It deliberately does NOT do RBAC, hooks or the insert itself: those differ
// legitimately between the paths (a custom handler is mid-transaction and owns
// its own authorization decisions), and mixing them here would hide that.
func PrepareCreate(res *schema.ResourceSchema, rv *schema.ResourceValidator, body map[string]any, role string) []schema.FieldRuleError {
	if rv != nil {
		rv.ApplyDefaults(body)
	}
	var errs []schema.FieldRuleError
	errs = append(errs, schema.GovernedFieldViolations(res, body, schema.GovernedCreate, role)...)
	if rv != nil {
		errs = append(errs, rv.ValidateWrite(body, true)...)
	}
	errs = append(errs, validateCreateTypes(res, body)...)
	if len(errs) > 0 {
		return errs
	}
	if rv != nil {
		if se := rv.ValidateInitialStates(body); len(se) > 0 {
			return se
		}
	}
	return nil
}

// PrepareUpdate is the update-side counterpart: PATCH semantics (only the
// fields present are validated), plus the same value type check the create path
// runs, plus the governed-field rule — `id` and `auto` fields in an update
// body are ALWAYS rejected (import is create-only; see schema.governed.go).
// Before WRITE-ASYMMETRY-S1 this pass was missing here, so Ctx.Update accepted
// `{"id": …}` and generated `SET id = …` — a primary-key rewrite the REST
// PATCH answers 422 read_only for. Defaults are deliberately absent — they are
// create-only, on both paths, matching SQL DEFAULT.
//
// The state-machine TRANSITION is not checked here: it is enforced inside the
// UPDATE's WHERE by AppendStateTransitionGuard (ENG-7), which both paths
// already share, because doing it in SQL is what makes it race-safe.
func PrepareUpdate(res *schema.ResourceSchema, rv *schema.ResourceValidator, body map[string]any) []schema.FieldRuleError {
	var errs []schema.FieldRuleError
	errs = append(errs, schema.GovernedFieldViolations(res, body, schema.GovernedUpdate, "")...)
	if rv != nil {
		errs = append(errs, rv.ValidateWrite(body, false)...)
	}
	errs = append(errs, validateCreateTypes(res, body)...)
	return errs
}
