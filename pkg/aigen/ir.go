package aigen

import (
	"sort"
	"strconv"
	"strings"
)

// ── Array-IR (AI-F2-S2) ─────────────────────────────────────────────────────
//
// STATUS (AI-F2-S4): ARCHIVED for constrained decoding, PRESERVED for the visual
// editor. The original goal — feed IROutputSchema to Anthropic structured outputs so
// the deep structure is decode-guaranteed — does NOT work on the real API (the strict
// subset caps union params at 16; the field grammar exceeds it), so the array-IR loop
// falls back to plain (see docs §The first real measurement). The transforms below
// (MapToIR / IRToMap, lossless round-trip) are kept because they are exactly what the
// VISUAL EDITOR needs: a stable, array-shaped, index-addressable representation of the
// schema for manipulating resources/fields/relations as ordered objects in a UI, then
// converting back to the canonical map the engine consumes. This is live, tested code
// (the round-trip identity test stays green) — archived from the decode path, not dead.
//
// The HONEST limit of AI-F1-S1 (see output_schema.go): the Appximo schema is an
// arbitrary-keyed map (resources keyed by resource name, fields by field name,
// roles by role name), and the structured-outputs strict subset cannot express a
// map of arbitrary keys (it forbids patternProperties / additionalProperties-as-
// schema and demands additionalProperties:false everywhere). So the envelope is
// all the decoder can guarantee — the DEEP structure (a field's `type`, the strict
// key set of a field/resource) still falls to the validator + loop.
//
// The array-IR fixes that. It rewrites every arbitrary-keyed map as an ARRAY of
// objects with an explicit "name" (or "resource"/"from"/"event") key:
//
//	MAP form (what the engine consumes):
//	  "resources": { "empleados": { "fields": { "email": {"type":"string"} } } }
//	ARRAY-IR (what the model generates under strict):
//	  "resources": [ { "name":"empleados", "fields":[ {"name":"email","type":"string"} ] } ]
//
// An array of objects with a FIXED item schema IS expressible in the strict subset
// (additionalProperties:false on the item, a type enum on `type`, etc.), so the
// decoder now constrains the structure IN DEPTH — a wrong type or an unknown deep
// key becomes impossible BY CONSTRUCTION, not merely correctable. The transform
// IRToMap converts the model's IR back to the map form the engine validates and
// consumes; MapToIR is the inverse (for converting golds / few-shot examples to
// IR). The pair is a deterministic, total, lossless round-trip (a property test
// asserts map→IR→map == identity over every corpus gold and repo example).
//
// The arbitrary-keyed maps converted to arrays (every map of arbitrary keys in the
// schema grammar) and their explicit key field:
//
//	resources                         → array, key "name"
//	resources[].fields                → array, key "name"
//	resources[].relations             → array, key "name"
//	resources[].hooks                 → array, key "event"
//	rbac.roles                        → array, key "name"
//	rbac.roles[].permissions          → array, key "resource"
//	fields[].state_machine.transitions→ array, key "from" (value under "to")
//
// (indexes / foreign_keys / events are already arrays in the map form; resources[].
// resources lists, actions, fields allowlists are arrays of scalars — all pass
// through unchanged.)

// irArrayOrder is the deterministic ordering for the arrays MapToIR produces: a map
// has no order, an array does, so the conversion sorts by the explicit key — making
// the round-trip stable (the same map always yields the same IR).
func irSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}

// MapToIR converts a decoded Appximo schema (map form) to the array-IR. It is
// total and deterministic: arbitrary-keyed maps become arrays sorted by their
// explicit key; every other value passes through unchanged.
func MapToIR(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		switch k {
		case "resources":
			out["resources"] = mapToIRArray(asMap(v), "name", resourceToIR)
		case "rbac":
			out["rbac"] = rbacToIR(asMap(v))
		default:
			out[k] = v // $schema, version, name, description, workflows
		}
	}
	return out
}

// IRToMap is the inverse: the array-IR back to the map form the engine consumes.
// Optional keys whose value is nil are DROPPED (strict structured outputs emulates
// an optional as a nullable required key, so a real model emits absent options as
// explicit null — collapsing them to absent restores the canonical map; MapToIR
// never emits nil, so this is invisible to the round-trip).
func IRToMap(ir map[string]any) map[string]any {
	out := make(map[string]any, len(ir))
	for k, v := range ir {
		if v == nil {
			continue
		}
		switch k {
		case "resources":
			out["resources"] = irArrayToMap(asArray(v), "name", resourceFromIR)
		case "rbac":
			out["rbac"] = rbacFromIR(asMap(v))
		default:
			out[k] = v
		}
	}
	return out
}

// ── map → IR ────────────────────────────────────────────────────────────────

// mapToIRArray turns an arbitrary-keyed map into a sorted array, calling item for
// each (key, value) to build the array element (which carries the key under nameKey).
func mapToIRArray(m map[string]any, nameKey string, item func(name string, v any) map[string]any) []any {
	out := make([]any, 0, len(m))
	for _, k := range irSortedKeys(m) {
		out = append(out, item(k, m[k]))
	}
	_ = nameKey // nameKey is set by item; kept in the signature for documentation
	return out
}

func resourceToIR(name string, v any) map[string]any {
	res := asMap(v)
	out := map[string]any{"name": name}
	for k, val := range res {
		switch k {
		case "fields":
			out["fields"] = mapToIRArray(asMap(val), "name", fieldToIR)
		case "relations":
			out["relations"] = mapToIRArray(asMap(val), "name", scalarNamedToIR("name"))
		case "hooks":
			out["hooks"] = mapToIRArray(asMap(val), "event", scalarNamedToIR("event"))
		default:
			out[k] = val // indexes, foreign_keys, events, renamed_from
		}
	}
	return out
}

// scalarNamedToIR builds an item transform that simply injects the name under
// nameKey and copies every other key verbatim (for maps whose values have no
// nested arbitrary-keyed maps of their own — relations, hooks).
func scalarNamedToIR(nameKey string) func(name string, v any) map[string]any {
	return func(name string, v any) map[string]any {
		out := map[string]any{nameKey: name}
		for k, val := range asMap(v) {
			out[k] = val
		}
		return out
	}
}

func fieldToIR(name string, v any) map[string]any {
	f := asMap(v)
	out := map[string]any{"name": name}
	for k, val := range f {
		if k == "state_machine" {
			out["state_machine"] = stateMachineToIR(asMap(val))
		} else {
			out[k] = val
		}
	}
	return out
}

func stateMachineToIR(sm map[string]any) map[string]any {
	out := make(map[string]any, len(sm))
	for k, v := range sm {
		if k == "transitions" {
			out["transitions"] = transitionsToIR(asMap(v))
		} else {
			out[k] = v // initial (string or array) passes through
		}
	}
	return out
}

func transitionsToIR(t map[string]any) []any {
	out := make([]any, 0, len(t))
	for _, from := range irSortedKeys(t) {
		out = append(out, map[string]any{"from": from, "to": t[from]})
	}
	return out
}

func rbacToIR(rbac map[string]any) map[string]any {
	out := make(map[string]any, len(rbac))
	for k, v := range rbac {
		if k == "roles" {
			out["roles"] = mapToIRArray(asMap(v), "name", roleToIR)
		} else {
			out[k] = v
		}
	}
	return out
}

func roleToIR(name string, v any) map[string]any {
	role := asMap(v)
	out := map[string]any{"name": name}
	for k, val := range role {
		if k == "permissions" {
			out["permissions"] = mapToIRArray(asMap(val), "resource", scalarNamedToIR("resource"))
		} else {
			out[k] = val // resources, actions, conditions, fields
		}
	}
	return out
}

// ── IR → map ────────────────────────────────────────────────────────────────

// irArrayToMap turns an IR array back into a map keyed by each item's nameKey value
// (which is removed from the item). Items missing the key are skipped (defensive).
func irArrayToMap(a []any, nameKey string, item func(m map[string]any) any) map[string]any {
	out := make(map[string]any, len(a))
	for _, raw := range a {
		m := asMap(raw)
		name, ok := m[nameKey].(string)
		if !ok {
			continue
		}
		out[name] = item(m)
	}
	return out
}

func resourceFromIR(m map[string]any) any {
	out := map[string]any{}
	for k, v := range m {
		if k == "name" || v == nil {
			continue
		}
		switch k {
		case "fields":
			out["fields"] = irArrayToMap(asArray(v), "name", fieldFromIR)
		case "relations":
			out["relations"] = irArrayToMap(asArray(v), "name", scalarNamedFromIR("name"))
		case "hooks":
			out["hooks"] = irArrayToMap(asArray(v), "event", scalarNamedFromIR("event"))
		default:
			out[k] = v
		}
	}
	return out
}

func scalarNamedFromIR(nameKey string) func(m map[string]any) any {
	return func(m map[string]any) any {
		out := map[string]any{}
		for k, v := range m {
			if k == nameKey || v == nil {
				continue
			}
			out[k] = v
		}
		return out
	}
}

func fieldFromIR(m map[string]any) any {
	out := map[string]any{}
	for k, v := range m {
		if k == "name" || v == nil {
			continue
		}
		if k == "state_machine" {
			out["state_machine"] = stateMachineFromIR(asMap(v))
		} else {
			out[k] = v
		}
	}
	return out
}

func stateMachineFromIR(sm map[string]any) any {
	out := map[string]any{}
	for k, v := range sm {
		if v == nil {
			continue
		}
		if k == "transitions" {
			out["transitions"] = transitionsFromIR(asArray(v))
		} else {
			out[k] = v
		}
	}
	return out
}

func transitionsFromIR(a []any) map[string]any {
	out := make(map[string]any, len(a))
	for _, raw := range a {
		m := asMap(raw)
		from, ok := m["from"].(string)
		if !ok {
			continue
		}
		out[from] = m["to"]
	}
	return out
}

func rbacFromIR(rbac map[string]any) any {
	out := map[string]any{}
	for k, v := range rbac {
		if v == nil {
			continue
		}
		if k == "roles" {
			out["roles"] = irArrayToMap(asArray(v), "name", roleFromIR)
		} else {
			out[k] = v
		}
	}
	return out
}

func roleFromIR(m map[string]any) any {
	out := map[string]any{}
	for k, v := range m {
		if k == "name" || v == nil {
			continue
		}
		if k == "permissions" {
			out["permissions"] = irArrayToMap(asArray(v), "resource", scalarNamedFromIR("resource"))
		} else {
			out[k] = v
		}
	}
	return out
}

// ── error-path translation (map-path → IR-path) ─────────────────────────────
//
// The validator runs on the MAP form and emits map paths (resources.empleados.
// fields.email.type). The model generated the IR, so a correction it can act on
// must reference the IR path (resources[0].fields[1].type). TranslateMapPathToIR
// walks the supplied IR document, mapping each arbitrary-keyed segment to the array
// index of the item whose explicit key matches — using the ACTUAL IR the model
// produced (its array order, not an assumed one), so the index is always correct.

// irContainer describes a path segment that, in the IR, is an array of named
// objects: the next path segment is a name to be resolved to an array index.
type irContainer struct {
	nameKey string // the IR item key carrying the map key ("name"/"resource"/"from"/"event")
}

// irContainers maps a map-path segment key to its IR container descriptor. A
// segment not present here is a plain key (or already an [index]) and passes through.
var irContainers = map[string]irContainer{
	"resources":   {nameKey: "name"},
	"fields":      {nameKey: "name"},
	"relations":   {nameKey: "name"},
	"hooks":       {nameKey: "event"},
	"roles":       {nameKey: "name"},
	"permissions": {nameKey: "resource"},
	"transitions": {nameKey: "from"},
}

// TranslateMapPathToIR rewrites a validator map path into the equivalent IR path,
// using ir (the IR document the model produced) to resolve each named segment to
// its array index. It is best-effort and total: a segment it cannot resolve is
// emitted verbatim (never a crash), so an unrecognized path degrades to the map
// path rather than failing the correction round.
func TranslateMapPathToIR(mapPath string, ir map[string]any) string {
	if mapPath == "" || mapPath == "$" {
		return mapPath
	}
	segs := strings.Split(mapPath, ".")
	var out []string
	var cur any = ir

	for i := 0; i < len(segs); i++ {
		seg := segs[i]

		// An already-indexed segment (e.g. "indexes[0]"): emit verbatim, descend.
		if base, idx, ok := splitIndexSeg(seg); ok {
			out = append(out, seg)
			cur = descendKey(cur, base)
			cur = descendIndex(cur, idx)
			continue
		}

		c, isContainer := irContainers[seg]
		if !isContainer {
			out = append(out, seg)
			cur = descendKey(cur, seg)
			continue
		}

		// Container: the IR has an array under seg; the NEXT segment is the name.
		arr := asArray(descendKey(cur, seg))
		if i+1 >= len(segs) || arr == nil {
			out = append(out, seg) // nothing to resolve — emit the container alone
			cur = arr
			continue
		}
		name := segs[i+1]
		idx, item := findByNameKey(arr, c.nameKey, name)
		if idx < 0 {
			// name not found in the IR array — keep both segments verbatim.
			out = append(out, seg, name)
			cur = nil
			i++
			continue
		}
		out = append(out, seg+"["+strconv.Itoa(idx)+"]")
		cur = item
		i++ // consumed the name segment
	}
	return strings.Join(out, ".")
}

// splitIndexSeg parses "base[idx]" → (base, idx, true); a plain segment → ("",0,false).
func splitIndexSeg(seg string) (string, int, bool) {
	lb := strings.IndexByte(seg, '[')
	if lb < 0 || !strings.HasSuffix(seg, "]") {
		return "", 0, false
	}
	n, err := strconv.Atoi(seg[lb+1 : len(seg)-1])
	if err != nil {
		return "", 0, false
	}
	return seg[:lb], n, true
}

func descendKey(cur any, key string) any {
	m := asMap(cur)
	if m == nil {
		return nil
	}
	return m[key]
}

func descendIndex(cur any, idx int) any {
	a := asArray(cur)
	if idx < 0 || idx >= len(a) {
		return nil
	}
	return a[idx]
}

// findByNameKey returns the index and item of the array element whose nameKey value
// equals name, or (-1, nil) if absent.
func findByNameKey(a []any, nameKey, name string) (int, any) {
	for i, raw := range a {
		if s, _ := asMap(raw)[nameKey].(string); s == name {
			return i, raw
		}
	}
	return -1, nil
}
