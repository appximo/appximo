package aigen

import "encoding/json"

// OutputSchema returns the JSON Schema passed to Anthropic structured outputs
// (output_config.format) so the model's response is DECODE-CONSTRAINED to it.
//
// ARCHIVED / EXPERIMENTAL (AI-F2-S4): the real Anthropic strict-outputs subset
// REJECTS this envelope — it requires additionalProperties:false on EVERY object, so
// the deliberately-open `resources`/`rbac` here cannot be expressed, and the request
// errors → the generator falls back to the plain validator-guided loop (the default).
// Kept opt-in for measurement (pkg/aigen/eval) and as the documented record of the
// boundary; the plain loop is the product path. See docs/AI_SCHEMA_GENERATION.md.
//
// Why only the ENVELOPE. The strict-outputs subset is narrow: every object must
// declare additionalProperties:false, and it offers no patternProperties /
// propertyNames / additionalProperties-as-schema. The Appitools schema is an
// arbitrary-keyed map (resources keyed by resource name, fields by field name,
// roles by role name) — that shape is simply not expressible in the subset.
// So this schema constrains the part that IS expressible — the top-level
// envelope — and deliberately leaves resources/rbac as open objects.
//
// What the decoder therefore GUARANTEES by construction (these error classes can
// no longer occur, so the loop never spends an iteration on them):
//   - the output is well-formed JSON (no fences, no prose, no truncated garbage);
//   - $schema is exactly the v1 const, version is exactly "1";
//   - name is present and a string; resources is present;
//   - no UNKNOWN top-level key (additionalProperties:false on the root).
//
// What still falls to schema.ValidateReport + the correction loop (the deep,
// map-keyed structure the subset cannot reach, plus all cross-reference
// semantics): field types, strict field/resource keys, enums, relations/FKs to
// existing targets, RBAC fields, state machines, defaults. The model keeps
// emitting the canonical MAP form, so the validator's error paths
// (resources.X.fields.Y...) stay coherent for in-context correction.
//
// NOTE: resources/rbac are typed "object" with no additionalProperties here, so
// the model can fill them with arbitrary keys. If a stricter live interpretation
// forced them empty, the generator detects an empty-resources result and falls
// back to plain generation (HasResources / the loop's guard) — never a silent
// convergence to an empty schema.
func OutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"$schema", "version", "name", "resources"},
		"properties": map[string]any{
			"$schema":     map[string]any{"type": "string", "const": SchemaURL},
			"version":     map[string]any{"type": "string", "const": SchemaVersion},
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"resources":   map[string]any{"type": "object"},
			"rbac":        map[string]any{"type": "object"},
		},
	}
}

// SchemaURL / SchemaVersion are the two required top-level constants the envelope
// pins (a frequent source of model error, eliminated by construction).
const (
	SchemaURL     = "https://appitools.dev/schema/v1"
	SchemaVersion = "1"
)

// HasResources reports whether raw schema bytes carry at least one resource. The
// generator uses it as a guard: structured outputs forcing an EMPTY resources map
// would otherwise "validate" (the engine accepts zero resources) and the loop
// would wrongly converge to a useless schema — so an empty result disables the
// structured path and regenerates plainly.
func HasResources(raw []byte) bool {
	var doc struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return len(doc.Resources) > 0
}

// disallowedInStrictSubset are JSON-Schema keys the structured-outputs strict
// subset does NOT support. OutputSchema must avoid all of them; a test asserts it.
var disallowedInStrictSubset = []string{
	"oneOf", "not", "patternProperties", "propertyNames",
	"pattern", "minLength", "maxLength", "minimum", "maximum",
	"multipleOf", "minItems", "maxItems", "minProperties", "maxProperties",
}
