package schema

import (
	"strings"
	"testing"
)

func TestRelationSubroute_Derivation(t *testing.T) {
	for in, want := range map[string]string{
		"customer_id": "customer",
		"customer":    "customer",
		"cliente_ref": "cliente_ref",
		"id_cliente":  "id_cliente", // prefix form: used as-is (no suffix to strip)
	} {
		if got := RelationSubroute(in); got != want {
			t.Errorf("RelationSubroute(%q) = %q, want %q", in, got, want)
		}
	}
}

func subrouteSchema(fields map[string]FieldDef) *APISchema {
	fields["title"] = FieldDef{Type: "string"}
	return &APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
		Resources: map[string]ResourceSchema{
			"orders":    {Fields: fields},
			"customers": {Fields: map[string]FieldDef{"name": {Type: "string"}}},
		},
	}
}

func TestValidate_RelationSubrouteCollision(t *testing.T) {
	s := subrouteSchema(map[string]FieldDef{
		"customer":    {Type: "uuid", Relation: "customers"},
		"customer_id": {Type: "uuid", Relation: "customers"},
	})
	// The collision must be a STABLE load error — the pre-fix behavior was chi
	// silently keeping a per-boot-random winner. Validate repeatedly: the same
	// verdict, byte-identical, every time (the ENG-16 proof shape).
	var first string
	for i := 0; i < 25; i++ {
		var hit *ValidationError
		for _, e := range Validate(s) {
			if e.Rule == "relation_subroute_collision" {
				e := e
				hit = &e
			}
		}
		if hit == nil {
			t.Fatal("relation_subroute_collision not reported")
		}
		if i == 0 {
			first = hit.Message
			for _, want := range []string{"'customer'", "'customer_id'", "/api/orders/{id}/customer"} {
				if !strings.Contains(hit.Message, want) {
					t.Errorf("collision error should contain %s: %s", want, hit.Message)
				}
			}
			continue
		}
		if hit.Message != first {
			t.Fatalf("collision error is not stable across runs:\n  %s\n  %s", first, hit.Message)
		}
	}
}

func TestValidate_RelationSubrouteNoFalsePositives(t *testing.T) {
	// Two relations with distinct segments — no finding.
	s := subrouteSchema(map[string]FieldDef{
		"customer_id": {Type: "uuid", Relation: "customers"},
		"seller_id":   {Type: "uuid", Relation: "customers"},
	})
	for _, e := range Validate(s) {
		if e.Rule == "relation_subroute_collision" {
			t.Fatalf("unexpected collision: %v", e)
		}
	}
	// A non-relation field sharing the segment registers no subroute — no finding.
	s2 := subrouteSchema(map[string]FieldDef{
		"customer":    {Type: "string"},
		"customer_id": {Type: "uuid", Relation: "customers"},
	})
	for _, e := range Validate(s2) {
		if e.Rule == "relation_subroute_collision" {
			t.Fatalf("unexpected collision with non-relation sibling: %v", e)
		}
	}
}

func TestValidate_ReservedFieldNameID(t *testing.T) {
	s := subrouteSchema(map[string]FieldDef{"id": {Type: "string"}})
	found := false
	for _, e := range Validate(s) {
		if e.Rule == "reserved_field_name" {
			found = true
		}
	}
	if !found {
		t.Fatal("a declared field named id must be rejected")
	}
	// Relation named id in the relations block.
	s2 := &APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
		Resources: map[string]ResourceSchema{
			"orders": {
				Fields: map[string]FieldDef{"customer_id": {Type: "uuid"}},
				Relations: map[string]RelationDef{
					"id": {Type: "belongs_to", Target: "customers", FK: "customer_id"},
				},
			},
			"customers": {Fields: map[string]FieldDef{"name": {Type: "string"}}},
		},
	}
	found = false
	for _, e := range Validate(s2) {
		if e.Rule == "reserved_field_name" {
			found = true
		}
	}
	if !found {
		t.Fatal("a relation named id must be rejected")
	}
}

func TestValidate_DeterministicErrorOrder(t *testing.T) {
	// Two invalid resources — the error LIST used to reach HTTP consumers in
	// map order (and one path embedded errs[0], so the reported reason was a
	// coin flip). Same schema, 25 validations, identical order every time.
	s := &APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
		Resources: map[string]ResourceSchema{
			"alpha": {Fields: map[string]FieldDef{"x": {Type: "number"}}},
			"beta":  {Fields: map[string]FieldDef{"y": {Type: "decimal"}}},
		},
	}
	var first []string
	for i := 0; i < 25; i++ {
		var got []string
		for _, e := range Validate(s) {
			got = append(got, e.Field+"|"+e.Rule)
		}
		if i == 0 {
			first = got
			if len(first) < 2 {
				t.Fatalf("expected at least 2 errors, got %v", first)
			}
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("error count changed between runs: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("error order not deterministic at %d: %s vs %s", j, got[j], first[j])
			}
		}
	}
}
