package schema

import (
	"fmt"
	"sort"
)

// This file is THE single source for "which write-body fields does the engine
// govern, and what may a caller do with them" (WRITE-ASYMMETRY-S1, ENG-45
// family 1).
//
// THE ASYMMETRY THIS CLOSES. The governed fields — the implicit `id` primary
// key and every `auto` timestamp — were rejected on update (422 read_only,
// CollectUpdate) and silently ACCEPTED on create: `POST {"id":"…",
// "created_at":"1999-…"}` answered 201 with both stored, on REST, the batch
// transaction and Ctx.Insert alike, while GraphQL rejected the same input
// structurally. Same input, three answers — and the accepting one lets any
// client with create permission forge a row's audit timestamps.
//
// THE CONTRACT DECISION (doctrine C-DOCTRINA-3, option (b) of the session
// brief). Supplying governed fields on create IS a real operation — importing
// rows from another system, restoring a fixture whose id other artifacts
// reference — so banning it outright breaks working use. Allowing it
// implicitly is the hole. The resolution follows C-DOCTRINA-2: the intention
// becomes DECLARABLE. A resource may declare
//
//	"import": { "roles": ["admin"], "fields": ["id", "created_at"] }
//
// and then, for exactly the enumerated roles (and optionally the enumerated
// subset of governed fields), CREATE accepts explicit values for the governed
// fields at every create door. Everywhere else — no declaration, another
// role, a field outside the subset, and ALWAYS on update — the governed
// fields are rejected with the same 422 read_only at every door. Update is
// never importable by design: import brings rows INTO existence with their
// history; rewriting the history of a row that already exists is not import.
//
// Every write door delegates here: PrepareCreate (REST POST, the batch
// transaction create, Ctx.Insert), the GraphQL create resolver, CollectUpdate
// (REST PUT/PATCH, GraphQL update, batch update) and PrepareUpdate
// (Ctx.Update). A door that stops delegating fails the anti-divergence tests.

// ImportConfig is the resource-level `import` declaration: which roles may
// supply the engine-governed fields (`id` + every `auto` field) when CREATING
// rows, and optionally which subset of those fields.
type ImportConfig struct {
	// Roles is the closed list of RBAC roles granted import. Required,
	// non-empty; every entry must be a role the schema's rbac block declares
	// (a typo is a load error, never a silently dead grant — the ENG-27 mold).
	// There is no wildcard: an auditor reads exactly who may import.
	Roles []string `json:"roles"`
	// Fields optionally narrows WHICH governed fields the grant covers, e.g.
	// ["id"] for client-generated ids without opening timestamp forgery.
	// Each entry must be "id" or an auto field of the resource. Absent = the
	// full governed set.
	Fields []string `json:"fields,omitempty"`
}

// GovernedWriteFields returns, sorted, the fields of this resource whose
// values the engine owns on the write path: the implicit "id" plus every
// field with an enabled `auto` role.
func (r *ResourceSchema) GovernedWriteFields() []string {
	cols := []string{"id"}
	for name, fd := range r.Fields {
		if fd.Auto.Enabled() {
			cols = append(cols, name)
		}
	}
	sort.Strings(cols)
	return cols
}

// IsGovernedWriteField reports whether a write-body key is engine-governed —
// the implicit primary key or an auto timestamp. The predicate every door's
// own key loop uses to skip keys the single source already judged.
func (r *ResourceSchema) IsGovernedWriteField(name string) bool {
	if name == "id" {
		return true
	}
	fd, ok := r.Fields[name]
	return ok && fd.Auto.Enabled()
}

// ImportableOnCreate reports whether `role` may supply governed field `field`
// on create under this resource's `import` declaration. False when there is
// no declaration, when the role is not granted, or when a declared fields
// subset excludes the field.
func (r *ResourceSchema) ImportableOnCreate(role, field string) bool {
	imp := r.Import
	if imp == nil || role == "" {
		return false
	}
	granted := false
	for _, ro := range imp.Roles {
		if ro == role {
			granted = true
			break
		}
	}
	if !granted {
		return false
	}
	if len(imp.Fields) == 0 {
		return true // no subset → the full governed set
	}
	for _, f := range imp.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// ImportDeclaredFields returns, sorted, the governed fields this resource's
// `import` declaration actually covers — the declared subset when one is
// given, else the full governed set. Empty when the resource declares no
// import. Consumed by the GraphQL input-type builder (which fields exist on
// the create input at all) and the OpenAPI generator (`x-appximo-import`),
// so both surfaces publish exactly what the one predicate enforces.
func (r *ResourceSchema) ImportDeclaredFields() []string {
	if r.Import == nil {
		return nil
	}
	if len(r.Import.Fields) == 0 {
		return r.GovernedWriteFields()
	}
	out := append([]string(nil), r.Import.Fields...)
	sort.Strings(out)
	return out
}

// GovernedOp selects which write door's contract GovernedFieldViolations
// enforces.
type GovernedOp int

const (
	// GovernedCreate — a create body: governed fields are rejected unless the
	// resource's `import` declaration grants them to the caller's role.
	GovernedCreate GovernedOp = iota
	// GovernedUpdate — an update body: governed fields are ALWAYS rejected
	// (import is a create-time concept; an existing row's engine-managed
	// values are immutable through every door).
	GovernedUpdate
)

// GovernedFieldViolations is the ONE implementation of the governed-field
// write rule. It returns a 422-shaped read_only violation for every governed
// field present in the body that the operation does not permit, sorted by
// field name (deterministic responses, the ENG-16 class). An empty result
// means the body carries no forbidden governed field.
//
// The update-side messages are byte-compatible with what CollectUpdate
// answered before this file existed (a pinned public contract); the
// create-side messages additionally say how to make the write legal, because
// there IS a legal way (ADR-024: name the field, say what to do).
func GovernedFieldViolations(r *ResourceSchema, body map[string]any, op GovernedOp, role string) []FieldRuleError {
	var errs []FieldRuleError
	for k := range body {
		if !r.IsGovernedWriteField(k) {
			continue
		}
		if op == GovernedCreate && r.ImportableOnCreate(role, k) {
			continue
		}
		errs = append(errs, FieldRuleError{Field: k, Rule: "read_only", Message: governedMessage(r, k, op)})
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Field < errs[j].Field })
	return errs
}

func governedMessage(r *ResourceSchema, field string, op GovernedOp) string {
	if op == GovernedUpdate {
		// Byte-compatible with the historical CollectUpdate messages.
		if field == "id" {
			return `field "id" cannot be set`
		}
		return fmt.Sprintf("field %q is set automatically and cannot be written", field)
	}
	// Create side. Three situations, each with its own way out; none names the
	// granted roles (the ENG-27 anti-enumeration asymmetry — the declaration's
	// existence is public in /openapi.json, its role list is not).
	if r.Import == nil {
		if field == "id" {
			return `field "id" is engine-generated and cannot be supplied on create; to import rows that keep their ids, declare "import" (with the granted roles) on this resource`
		}
		return fmt.Sprintf("field %q is engine-managed and cannot be supplied on create; to import rows that keep their timestamps, declare \"import\" (with the granted roles) on this resource", field)
	}
	if len(r.Import.Fields) > 0 && !importFieldsContain(r.Import.Fields, field) {
		return fmt.Sprintf("field %q is engine-managed and outside this resource's declared \"import\" fields", field)
	}
	return fmt.Sprintf("field %q is engine-managed; this resource accepts it on create only from an import-granted role", field)
}

func importFieldsContain(fields []string, f string) bool {
	for _, x := range fields {
		if x == f {
			return true
		}
	}
	return false
}
