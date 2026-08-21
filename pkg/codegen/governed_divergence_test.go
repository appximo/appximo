package codegen

import (
	"reflect"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// WRITE-ASYMMETRY-S1 — the anti-divergence tripwire for the governed-field
// write rule (`id` + `auto` fields).
//
// The rule has ONE implementation, schema.GovernedFieldViolations, and every
// write door delegates to it: PrepareCreate (REST POST, batch create,
// Ctx.Insert), the GraphQL create resolver, CollectUpdate (REST PUT/PATCH,
// GraphQL update, batch update) and PrepareUpdate (Ctx.Update). These tests
// assert each codegen entry point's governed verdicts are EXACTLY the single
// source's output — field, rule and message — so a path that re-inlines the
// rule (or stops calling it) fails here before it ships a divergence. The
// full-engine, all-doors HTTP proof lives in the root package's
// governed_write_integration_test.go.

func govRes(imp *schema.ImportConfig) *schema.ResourceSchema {
	return &schema.ResourceSchema{
		Fields: map[string]schema.FieldDef{
			"title":      {Type: "string", Required: true},
			"created_at": {Type: "time", Auto: schema.AutoCreate},
			"updated_at": {Type: "time", Auto: schema.AutoLegacy},
		},
		Import: imp,
	}
}

func governedOnly(errs []schema.FieldRuleError, res *schema.ResourceSchema) []schema.FieldRuleError {
	var out []schema.FieldRuleError
	for _, e := range errs {
		if res.IsGovernedWriteField(e.Field) && e.Rule == "read_only" {
			out = append(out, e)
		}
	}
	return out
}

var forgedBody = map[string]any{
	"title":      "x",
	"id":         "99999999-9999-4999-8999-999999999999",
	"created_at": "1999-01-01T00:00:00Z",
	"updated_at": "1999-01-01T00:00:00Z",
}

func cloneBody() map[string]any {
	m := make(map[string]any, len(forgedBody))
	for k, v := range forgedBody {
		m[k] = v
	}
	return m
}

// TestGoverned_PrepareCreateDelegates: the create core's governed verdicts are
// byte-identical to the single source's.
func TestGoverned_PrepareCreateDelegates(t *testing.T) {
	res := govRes(nil)
	got := governedOnly(PrepareCreate(res, nil, cloneBody(), "admin"), res)
	want := schema.GovernedFieldViolations(res, cloneBody(), schema.GovernedCreate, "admin")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PrepareCreate diverged from the single source:\n got %v\nwant %v", got, want)
	}
	if len(want) != 3 {
		t.Fatalf("source must reject all 3 governed fields, got %v", want)
	}
}

// TestGoverned_PrepareCreateHonorsImport: the granted role passes through the
// create core with zero governed violations — the import door.
func TestGoverned_PrepareCreateHonorsImport(t *testing.T) {
	res := govRes(&schema.ImportConfig{Roles: []string{"importer"}})
	if got := governedOnly(PrepareCreate(res, nil, cloneBody(), "importer"), res); len(got) != 0 {
		t.Fatalf("granted role must import, got %v", got)
	}
	if got := governedOnly(PrepareCreate(res, nil, cloneBody(), "viewer"), res); len(got) != 3 {
		t.Fatalf("non-granted role must be rejected on all 3, got %v", got)
	}
}

// TestGoverned_PrepareUpdateDelegates: Ctx.Update's core now rejects id/auto
// (it used to pass them through — `{"id": …}` became `SET id = …`, a
// primary-key rewrite), with the single source's exact verdicts, and the
// import declaration NEVER applies on update.
func TestGoverned_PrepareUpdateDelegates(t *testing.T) {
	res := govRes(&schema.ImportConfig{Roles: []string{"importer"}})
	got := governedOnly(PrepareUpdate(res, nil, cloneBody()), res)
	want := schema.GovernedFieldViolations(res, cloneBody(), schema.GovernedUpdate, "")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PrepareUpdate diverged from the single source:\n got %v\nwant %v", got, want)
	}
	if len(want) != 3 {
		t.Fatalf("update must reject all 3 governed fields regardless of import, got %v", want)
	}
}

// TestGoverned_CollectUpdateDelegates: the REST/GraphQL/batch update path's
// governed verdicts are the single source's, byte for byte (including the
// historical message text, which is a pinned public contract).
func TestGoverned_CollectUpdateDelegates(t *testing.T) {
	res := govRes(&schema.ImportConfig{Roles: []string{"importer"}})
	_, errs := CollectUpdate(res, cloneBody(), false, func(string) bool { return true })
	got := governedOnly(errs, res)
	want := schema.GovernedFieldViolations(res, cloneBody(), schema.GovernedUpdate, "")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CollectUpdate diverged from the single source:\n got %v\nwant %v", got, want)
	}
}

// TestGoverned_CreateAndUpdateAgree: THE symmetry this session exists for —
// outside an import grant, the same forged body gets the same verdict (per
// field: rule read_only, 422 shape) on create and on update. Message texts
// differ deliberately (create names the declarable way out); field set and
// rule may not.
func TestGoverned_CreateAndUpdateAgree(t *testing.T) {
	res := govRes(nil)
	create := schema.GovernedFieldViolations(res, cloneBody(), schema.GovernedCreate, "any")
	update := schema.GovernedFieldViolations(res, cloneBody(), schema.GovernedUpdate, "any")
	if len(create) != len(update) {
		t.Fatalf("create and update disagree on the governed set: %v vs %v", create, update)
	}
	for i := range create {
		if create[i].Field != update[i].Field || create[i].Rule != update[i].Rule {
			t.Fatalf("asymmetry at %d: create %v vs update %v", i, create[i], update[i])
		}
	}
}
