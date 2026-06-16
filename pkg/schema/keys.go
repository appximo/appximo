package schema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CheckUnknownKeys validates the RAW schema JSON against the known key set of
// every level of the schema contract. Go's json.Unmarshal silently drops keys
// the structs don't declare, which turns typos into silent no-ops — fatal for
// a product whose entire contract IS the schema (a user writing "webhooks"
// instead of "hooks" must get an error, not a quietly dead feature).
//
// It must be called with the raw bytes BEFORE/alongside unmarshalling (after
// parsing, unknown keys are already gone). Returns one error per unknown key,
// each listing the valid keys for that level. WorkflowStep.Config is the one
// deliberately free-form map (step-specific configuration).
func CheckUnknownKeys(raw json.RawMessage) []ValidationError {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return []ValidationError{{Field: "$", Message: "schema is not a JSON object: " + err.Error()}}
	}

	var errs []ValidationError
	addUnknown := func(path string, got map[string]json.RawMessage, valid ...string) {
		allowed := make(map[string]bool, len(valid))
		for _, k := range valid {
			allowed[k] = true
		}
		for k := range got {
			if !allowed[k] {
				errs = append(errs, ValidationError{
					Field:   joinPath(path, k),
					Message: fmt.Sprintf("unknown key %q (valid keys: %s)", k, strings.Join(valid, ", ")),
				})
			}
		}
	}
	// object parses a level into a key→raw map; nil (with no error appended)
	// when the value is absent. A non-object value is reported, not ignored.
	object := func(path string, v json.RawMessage) map[string]json.RawMessage {
		if v == nil {
			return nil
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			errs = append(errs, ValidationError{Field: path, Message: "must be a JSON object"})
			return nil
		}
		return m
	}

	addUnknown("$", top, "$schema", "version", "name", "resources", "rbac", "workflows")

	for resName, rawRes := range object("resources", top["resources"]) {
		resPath := "resources." + resName
		res := object(resPath, rawRes)
		addUnknown(resPath, res, "fields", "hooks", "indexes", "events", "relations")

		for relName, rawRel := range object(resPath+".relations", res["relations"]) {
			relPath := resPath + ".relations." + relName
			addUnknown(relPath, object(relPath, rawRel),
				"type", "target", "fk", "through", "target_fk", "limit")
		}

		// indexes: array of {fields, unique}. Strict-key each entry so a typo
		// (e.g. "field" instead of "fields", or "uniqe") is rejected, not silently
		// dropped — same contract as every other level.
		var idxArr []json.RawMessage
		if res["indexes"] != nil && json.Unmarshal(res["indexes"], &idxArr) == nil {
			for i, rawIdx := range idxArr {
				idxPath := fmt.Sprintf("%s.indexes[%d]", resPath, i)
				addUnknown(idxPath, object(idxPath, rawIdx), "fields", "unique")
			}
		}

		for fieldName, rawField := range object(resPath+".fields", res["fields"]) {
			fieldPath := resPath + ".fields." + fieldName
			fld := object(fieldPath, rawField)
			addUnknown(fieldPath, fld,
				"type", "required", "unique", "auto", "enum", "relation", "default",
				"min", "max", "minLength", "maxLength", "pattern", "format", "state_machine")
			// state_machine has a fixed key set; `transitions` keys are user state
			// names (free-form), so only the top-level keys are strict-checked.
			if sm := object(fieldPath+".state_machine", fld["state_machine"]); sm != nil {
				addUnknown(fieldPath+".state_machine", sm, "initial", "transitions")
			}
		}

		hooks := object(resPath+".hooks", res["hooks"])
		for event := range hooks {
			if !ValidHookEvents[event] {
				errs = append(errs, ValidationError{
					Field: resPath + ".hooks." + event,
					Message: fmt.Sprintf("unknown hook event %q (valid events: %s)",
						event, strings.Join(sortedKeys(ValidHookEvents), ", ")),
				})
			}
		}
		for event, rawHook := range hooks {
			addUnknown(resPath+".hooks."+event, object(resPath+".hooks."+event, rawHook),
				"type", "script", "url", "hmac_secret_env", "wasm_module", "wasm_fn", "timeout")
		}
	}

	rbac := object("rbac", top["rbac"])
	addUnknown("rbac", rbac, "roles")
	for roleName, rawRole := range object("rbac.roles", rbac["roles"]) {
		rolePath := "rbac.roles." + roleName
		role := object(rolePath, rawRole)
		addUnknown(rolePath, role, "resources", "actions", "conditions", "fields", "permissions")
		if cond := object(rolePath+".conditions", role["conditions"]); cond != nil {
			addUnknown(rolePath+".conditions", cond, "field", "op", "val")
		}
		// Per-resource permissions (G2): each entry is keyed by a resource name and
		// has its own fixed key set, including a nested condition.
		for resName, rawPerm := range object(rolePath+".permissions", role["permissions"]) {
			permPath := rolePath + ".permissions." + resName
			perm := object(permPath, rawPerm)
			addUnknown(permPath, perm, "actions", "conditions", "condition_actions", "fields")
			if cond := object(permPath+".conditions", perm["conditions"]); cond != nil {
				addUnknown(permPath+".conditions", cond, "field", "op", "val")
			}
		}
	}

	for wfName, rawWf := range object("workflows", top["workflows"]) {
		wfPath := "workflows." + wfName
		wf := object(wfPath, rawWf)
		addUnknown(wfPath, wf, "trigger", "steps")
		if trig := object(wfPath+".trigger", wf["trigger"]); trig != nil {
			addUnknown(wfPath+".trigger", trig, "type", "event", "resource", "cron", "path")
		}
		// Steps: each step's keys are fixed, but step.config is free-form.
		var steps []json.RawMessage
		if wf["steps"] != nil && json.Unmarshal(wf["steps"], &steps) == nil {
			for i, rawStep := range steps {
				stepPath := fmt.Sprintf("%s.steps[%d]", wfPath, i)
				addUnknown(stepPath, object(stepPath, rawStep), "name", "type", "ref", "config", "next")
			}
		}
	}

	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

// ValidHookEvents is the set of lifecycle events the engine actually consults
// (codegen.BuildRouter + the GraphQL create resolver). A hook on any other
// event name would never fire — so any other name is a validation error.
var ValidHookEvents = map[string]bool{
	"before_create": true,
	"after_create":  true,
	"before_update": true,
	"after_update":  true,
}

func joinPath(base, key string) string {
	if base == "$" {
		return key
	}
	return base + "." + key
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
