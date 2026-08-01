package codegen

import (
	"sort"

	"github.com/miguelangel/appitools/pkg/schema"
)

// validateCreateTypes type-checks the values of a CREATE body against the
// resource's declared field types, returning one error per offending field in
// the same S44 shape the rest of create validation uses.
//
// WHY THIS EXISTS. The update path has always called validateFieldValue; the
// create path never did. schema.ValidateWrite only enforces presence
// (`required`) and the DECLARED rules (enum/min/max/pattern/format), so a field
// that declares no rule had NOTHING checking its value on create. The same value
// therefore behaved differently depending on the verb — measured on the live
// engine before this fix, on an int64 field:
//
//	PATCH {"amount": 1.9}  → 422 field "amount" must be an integer
//	POST  {"amount": 1.9}  → 201 Created, and the stored value is 1
//
// The POST case is the one that matters. It is not a bad message: it is SILENT
// DATA CORRUPTION. The client sent 1.9, the engine truncated it to 1, and
// answered "created" — no error, no warning, nothing to grep for. The sibling
// cases were merely loud in the wrong way ({"amount": true} and {"done": 1} both
// produced a masked 500 — an engine fault logged and billed against the SLO for
// what is plainly a caller mistake).
//
// TWO CONSTRAINTS, both deliberate:
//
//  1. A key that is NOT a declared field is SKIPPED, never rejected. That is the
//     ENG-12 contract: a migration can add a column without rebuilding the
//     router, so the database is the source of truth for what exists, and an
//     unknown column must keep flowing through to Postgres (42703 → the 422
//     `unknown_field` shape). Type-check what we know; pass on what we do not.
//
//  2. An explicit null is SKIPPED. `{"amount": null}` means "no value", which is
//     a legitimate write to a nullable column; `required` already governs
//     whether that is allowed.
//
// The check lives here rather than inside ValidateWrite so it costs the PATCH
// path nothing — PATCH already type-checks through CollectUpdate, and adding it
// to the shared validator would run it twice on every update.
func validateCreateTypes(res *schema.ResourceSchema, body map[string]any) []schema.FieldRuleError {
	if res == nil || len(body) == 0 {
		return nil
	}
	var errs []schema.FieldRuleError
	for k, v := range body {
		if v == nil {
			continue // explicit null — `required` governs this, not the type
		}
		fd, ok := res.Fields[k]
		if !ok {
			continue // not a declared field — see constraint 1
		}
		if msg, valid := validateFieldValue(k, fd, v); !valid {
			errs = append(errs, schema.FieldRuleError{Field: k, Rule: "type", Message: msg})
		}
	}
	// Map iteration is random; sort so the same body always reports its errors
	// in the same order. A response that reshuffles between identical requests
	// is untestable and unreadable in a log.
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}
