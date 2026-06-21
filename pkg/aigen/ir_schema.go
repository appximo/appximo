package aigen

import "sort"

// IROutputSchema returns the JSON Schema the decoder is constrained to when the
// model generates in ARRAY-IR form (AI-F2-S2). Unlike OutputSchema (which can only
// pin the ENVELOPE because the map form is not expressible in the strict subset),
// this schema constrains the structure IN DEPTH: every arbitrary-keyed map is an
// array of objects with a FIXED item schema, which the strict subset CAN express.
//
// Every construction here is inside the structured-outputs strict subset:
//   - every object is additionalProperties:false with EVERY property in `required`
//     (the subset forbids optional properties; an optional is emulated as a
//     nullable required key — type ["T","null"] — so the model emits an absent
//     option as explicit null, which IRToMap collapses back to absent);
//   - fixed value sets are `enum` (the 9 field types, on_delete/on_update, format,
//     relation/hook/action kinds, the events) — so a wrong value is impossible at
//     decode time, not merely correctable;
//   - arrays carry a single fixed `items` schema;
//   - the two top-level constants are pinned with `const` (as in OutputSchema).
//
// It uses NONE of the disallowed keys (patternProperties / propertyNames /
// additionalProperties-as-schema / oneOf / not / pattern / min*/max* / multipleOf /
// minItems / ...) — a test (TestIROutputSchemaIsStrictSubset) walks the whole tree
// and asserts it. The deep structural error class (wrong type, unknown deep key)
// can therefore no longer occur in IR generation, leaving only the semantic,
// cross-reference class for the validator + loop.
//
// CAVEATS (documented, not bugs): a few values are genuinely polymorphic in the
// grammar and use a multi-type union rather than a single type — `resources` in a
// role ("*" string OR an array), state_machine `initial` (string OR array), and a
// field `default` (string/number/bool). A union is still strict-subset-shaped
// (it is just `type: [...]`); these stay the validator's job for the precise rule.
func IROutputSchema() map[string]any {
	actionEnum := irEnum("read", "create", "update", "delete", "*")

	condition := irObj(map[string]any{
		"field": irStr(),
		"op":    irNullable(irEnum("eq")),
		"val":   irStr(),
	})

	permissionItem := irObj(map[string]any{
		"resource":          irStr(),
		"actions":           irArr(actionEnum),
		"conditions":        irNullable(condition),
		"condition_actions": irNullable(irArr(actionEnum)),
		"fields":            irNullable(irArr(irStr())),
	})

	roleItem := irObj(map[string]any{
		"name":        irStr(),
		"resources":   irNullable(strOrArray()),
		"actions":     irNullable(irArr(actionEnum)),
		"conditions":  irNullable(condition),
		"fields":      irNullable(irArr(irStr())),
		"permissions": irNullable(irArr(permissionItem)),
	})

	transitionItem := irObj(map[string]any{
		"from": irStr(),
		"to":   irArr(irStr()),
	})
	stateMachine := irObj(map[string]any{
		"initial":     strOrArray(),
		"transitions": irArr(transitionItem),
	})

	fieldItem := irObj(map[string]any{
		"name":          irStr(),
		"type":          irEnum("string", "text", "int", "int64", "float64", "bool", "uuid", "time", "json"),
		"required":      irNullable(irBool()),
		"unique":        irNullable(irBool()),
		"auto":          irNullable(irBool()),
		"enum":          irNullable(irArr(irStr())),
		"relation":      irNullable(irStr()),
		"on_delete":     irNullable(irEnum("restrict", "cascade", "set_null")),
		"on_update":     irNullable(irEnum("restrict", "cascade", "set_null")),
		"references":    irNullable(irStr()),
		"renamed_from":  irNullable(irStr()),
		"default":       map[string]any{"type": []any{"string", "number", "boolean", "null"}},
		"min":           irNullable(irNum()),
		"max":           irNullable(irNum()),
		"minLength":     irNullable(irNum()),
		"maxLength":     irNullable(irNum()),
		"pattern":       irNullable(irStr()),
		"format":        irNullable(irEnum("email", "uuid", "url", "date")),
		"state_machine": irNullable(stateMachine),
	})

	relationItem := irObj(map[string]any{
		"name":      irStr(),
		"type":      irEnum("has_many", "belongs_to", "many_to_many"),
		"target":    irStr(),
		"fk":        irStr(),
		"through":   irNullable(irStr()),
		"target_fk": irNullable(irStr()),
		"limit":     irNullable(irNum()),
	})

	hookItem := irObj(map[string]any{
		"event":           irEnum("before_create", "after_create", "before_update", "after_update"),
		"type":            irEnum("js", "webhook", "wasm"),
		"script":          irNullable(irStr()),
		"url":             irNullable(irStr()),
		"hmac_secret_env": irNullable(irStr()),
		"wasm_module":     irNullable(irStr()),
		"wasm_fn":         irNullable(irStr()),
		"timeout":         irNullable(irStr()),
	})

	indexItem := irObj(map[string]any{
		"fields": irArr(irStr()),
		"unique": irNullable(irBool()),
	})

	fkItem := irObj(map[string]any{
		"columns":     irArr(irStr()),
		"target":      irStr(),
		"ref_columns": irArr(irStr()),
		"on_delete":   irNullable(irEnum("restrict", "cascade", "set_null")),
		"on_update":   irNullable(irEnum("restrict", "cascade", "set_null")),
	})

	resourceItem := irObj(map[string]any{
		"name":         irStr(),
		"fields":       irArr(fieldItem),
		"relations":    irNullable(irArr(relationItem)),
		"hooks":        irNullable(irArr(hookItem)),
		"indexes":      irNullable(irArr(indexItem)),
		"foreign_keys": irNullable(irArr(fkItem)),
		"events":       irNullable(irArr(irEnum("create", "update", "delete"))),
		"renamed_from": irNullable(irStr()),
	})

	rbac := irObj(map[string]any{"roles": irArr(roleItem)})

	return irObj(map[string]any{
		"$schema":     map[string]any{"type": "string", "const": SchemaURL},
		"version":     map[string]any{"type": "string", "const": SchemaVersion},
		"name":        irStr(),
		"description": irNullable(irStr()),
		"resources":   irArr(resourceItem),
		"rbac":        irNullable(rbac),
	})
}

// ── strict-subset schema builders ───────────────────────────────────────────

func irStr() map[string]any  { return map[string]any{"type": "string"} }
func irBool() map[string]any { return map[string]any{"type": "boolean"} }
func irNum() map[string]any  { return map[string]any{"type": "number"} }

func irEnum(vals ...string) map[string]any {
	e := make([]any, len(vals))
	for i, v := range vals {
		e[i] = v
	}
	return map[string]any{"type": "string", "enum": e}
}

func irArr(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

// irObj builds a strict object: additionalProperties:false and `required` listing
// EVERY property key (the strict subset has no optional properties; optionality is
// modeled by wrapping a property value with irNullable).
func irObj(props map[string]any) map[string]any {
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	req := make([]any, len(keys))
	for i, k := range keys {
		req[i] = k
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
		"required":             req,
	}
}

// irNullable makes a schema also accept null (the strict-subset optional emulation):
// it widens `type` to include "null" and, when the node is an enum, adds null as a
// member so the value is "one of the enum, OR null".
func irNullable(s map[string]any) map[string]any {
	switch t := s["type"].(type) {
	case string:
		s["type"] = []any{t, "null"}
	case []any:
		s["type"] = append(t, "null")
	}
	if e, ok := s["enum"].([]any); ok {
		s["enum"] = append(e, nil)
	}
	return s
}

// strOrArray is the polymorphic "string OR array-of-string" union used by a role's
// `resources` ("*" or a list) and a state machine's `initial` (one state or many).
func strOrArray() map[string]any {
	return map[string]any{"type": []any{"string", "array"}, "items": irStr()}
}
