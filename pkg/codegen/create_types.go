package codegen

import (
	"sort"

	"github.com/appximo/appximo/pkg/schema"
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
// FOUR CONSTRAINTS, all deliberate:
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
//  3. A JSON **string** is SKIPPED for every type, and this one is subtle. The
//     first version of this check did not skip it, and an adversarial regression
//     pass caught the result: `{"amount": "7"}` used to return 201 with 7 stored
//     correctly, and started returning 422. Form-encoded clients, spreadsheet
//     importers and several HTTP libraries send every scalar as a string, so that
//     was an un-versioned break of the create contract — shipped, ironically,
//     under a rule that says DO NOT BECOME STRICTER THAN THE LAYER YOU ARE
//     PROTECTING (ADR-024). Postgres parses "7" into a bigint happily; the engine
//     has no business refusing it.
//
//     A string therefore defers to Postgres exactly as before. One that does NOT
//     parse (`{"amount": "nope"}`) is still rejected — by Postgres, as a 400 —
//     which is what shipped for years. Naming that value in a 422 means
//     reproducing Postgres's accepted set per type (wider than Go's: `yes` is a
//     boolean, integers may carry whitespace, floats may be `Infinity`), tracked
//     with its evidence as ENG-25 rather than guessed at here.
//
//     What stays caught is the case Postgres would silently ACCEPT AND CORRUPT: a
//     JSON *number* with a fractional part written to an integer column.
//
//  4. A value that could NOT have come from a JSON body is SKIPPED — it is
//     engine-injected, not caller input. See fromJSONBody; this one was a total
//     outage for a documented feature and is the sharpest lesson of the set.
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
		if !fromJSONBody(v) {
			continue // see constraint 4 — engine-injected, not caller input
		}
		if _, isStr := v.(string); isStr {
			continue // see constraint 3
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

// fromJSONBody reports whether v could have been produced by decoding a JSON
// request body into map[string]any. encoding/json yields exactly these Go types
// — string, float64, bool, map[string]any, []any and nil — so anything else in
// the body map was put there by the ENGINE, not the caller.
//
// This matters because validateCreateTypes runs AFTER ApplyDefaults, which
// injects values for omitted fields. A `{"type":"time","default":"now"}` field —
// a documented engine feature — gets a Go time.Time, and validateFieldValue's
// time case requires a string. Without this guard EVERY create that omitted such
// a field was answered 422 "field \"fecha\" must be a timestamp string",
// blaming the caller for a field they never sent and breaking the endpoint
// outright for any schema using the feature (the repo's own working schema.json
// uses it in two resources).
//
// It shipped green because the test that covers it, TestSchemaDefaultsOnInsert,
// is behind the integration tag and skipped by the -short unit lane — which is
// the more useful lesson: a check added after a transform must validate what the
// CALLER sent, not what the pipeline has since put in the map.
func fromJSONBody(v any) bool {
	switch v.(type) {
	case string, float64, bool, map[string]any, []any, nil:
		return true
	}
	// CTX-PARITY-S1: the LIBRARY path (Ctx.Insert / Ctx.Update) is a second,
	// equally legitimate caller, and it passes what Go computes — int64 from a
	// previous read, int from a literal. Those are caller INPUT and must be
	// type-checked like any other value; only they are not JSON-shaped. Note
	// what is deliberately still excluded: time.Time, which the engine itself
	// injects for a `default:"now"` field and which validateFieldValue would
	// reject as "must be a timestamp string" — the outage this guard exists to
	// prevent.
	if _, isNum := schema.AsFloat64(v); isNum {
		return true
	}
	return false
}
