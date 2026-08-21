package codegen

import (
	"encoding/json"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
)

// TestSubroutePathsDeriveFromSchemaRelationSubroute is the anti-divergence pin
// for the relation-subroute derivation (SILENT-CORRUPTION-S1): the OpenAPI
// document must expose EXACTLY the paths schema.RelationSubroute derives for
// every relation field — including a field that carries no `_id` suffix. The
// route builder and the write-files generator delegate to the same function;
// if any emitter grows its own derivation again, the published contract and
// the served routes can silently disagree, which is the bug this closes.
func TestSubroutePathsDeriveFromSchemaRelationSubroute(t *testing.T) {
	s := &schema.APISchema{
		Schema: "https://appximo.com/schema/v1", Version: "1", Name: "t",
		Resources: map[string]schema.ResourceSchema{
			"orders": {Fields: map[string]schema.FieldDef{
				"customer_id": {Type: "uuid", Relation: "customers"},
				"vendedor":    {Type: "uuid", Relation: "customers"}, // no _id suffix
				"title":       {Type: "string"},
			}},
			"customers": {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
		},
	}
	out, err := GenerateOpenAPIJSON(s, "/")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	for _, fname := range []string{"customer_id", "vendedor"} {
		want := "/api/orders/{id}/" + schema.RelationSubroute(fname)
		if _, ok := doc.Paths[want]; !ok {
			t.Errorf("OpenAPI missing subroute path %s (derived from %q)", want, fname)
		}
	}
	// The raw field name of a suffixed FK must NOT appear as a path — that
	// would mean an emitter stopped delegating to the shared derivation.
	if _, ok := doc.Paths["/api/orders/{id}/customer_id"]; ok {
		t.Error("OpenAPI exposes the raw field name customer_id — an emitter no longer delegates to schema.RelationSubroute")
	}
}
