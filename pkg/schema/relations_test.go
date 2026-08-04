package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func relValidSchema() *APISchema {
	return &APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "sales",
		Resources: map[string]ResourceSchema{
			"orders": {
				Fields: map[string]FieldDef{"customer_id": {Type: "uuid"}},
				Relations: map[string]RelationDef{
					"lines":    {Type: "has_many", Target: "lines", FK: "order_id"},
					"customer": {Type: "belongs_to", Target: "customers", FK: "customer_id"},
				},
			},
			"lines": {
				Fields: map[string]FieldDef{"order_id": {Type: "uuid"}, "product_id": {Type: "uuid"}},
				Relations: map[string]RelationDef{
					"product": {Type: "belongs_to", Target: "products", FK: "product_id"},
				},
			},
			"products": {
				Fields: map[string]FieldDef{"name": {Type: "string"}},
				Relations: map[string]RelationDef{
					"orders": {Type: "many_to_many", Target: "orders", Through: "order_products", FK: "product_id", TargetFK: "order_id"},
				},
			},
			"customers": {Fields: map[string]FieldDef{"name": {Type: "string"}}},
		},
	}
}

func TestValidateRelations_Valid(t *testing.T) {
	if errs := Validate(relValidSchema()); len(errs) != 0 {
		t.Fatalf("valid relations schema rejected: %v", errs)
	}
}

func TestValidateRelations_Errors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*APISchema)
		want   string
	}{
		{"unknown type", func(s *APISchema) {
			r := s.Resources["orders"]
			r.Relations["lines"] = RelationDef{Type: "has_one", Target: "lines", FK: "order_id"}
			s.Resources["orders"] = r
		}, "unknown relation type"},
		{"unknown target", func(s *APISchema) {
			r := s.Resources["orders"]
			r.Relations["lines"] = RelationDef{Type: "has_many", Target: "ghosts", FK: "order_id"}
			s.Resources["orders"] = r
		}, "unknown resource"},
		{"bad fk", func(s *APISchema) {
			r := s.Resources["orders"]
			r.Relations["lines"] = RelationDef{Type: "has_many", Target: "lines", FK: "Order ID"}
			s.Resources["orders"] = r
		}, "fk"},
		{"m2m missing through", func(s *APISchema) {
			r := s.Resources["products"]
			r.Relations["orders"] = RelationDef{Type: "many_to_many", Target: "orders", FK: "product_id", TargetFK: "order_id"}
			s.Resources["products"] = r
		}, "through"},
		{"through on has_many", func(s *APISchema) {
			r := s.Resources["orders"]
			r.Relations["lines"] = RelationDef{Type: "has_many", Target: "lines", FK: "order_id", Through: "x"}
			s.Resources["orders"] = r
		}, "through only applies"},
		{"name collides with field", func(s *APISchema) {
			r := s.Resources["orders"]
			r.Fields["lines"] = FieldDef{Type: "string"}
			s.Resources["orders"] = r
		}, "collides"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := relValidSchema()
			c.mutate(s)
			errs := Validate(s)
			if len(errs) == 0 {
				t.Fatalf("expected error containing %q, got none", c.want)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected error containing %q, got %v", c.want, errs)
			}
		})
	}
}

func TestCheckUnknownKeys_RelationKey(t *testing.T) {
	raw := json.RawMessage(`{
		"$schema":"x","version":"1","name":"s",
		"resources":{"orders":{"fields":{"x":{"type":"uuid"}},
			"relations":{"lines":{"type":"has_many","target":"lines","fk":"order_id","bogus":true}}}},
		"rbac":{"roles":{}}
	}`)
	errs := CheckUnknownKeys(raw)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "bogus") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unknown-key error for relation key 'bogus', got %v", errs)
	}
}

func TestCheckUnknownKeys_RelationsAccepted(t *testing.T) {
	raw := json.RawMessage(`{
		"$schema":"x","version":"1","name":"s",
		"resources":{"orders":{"fields":{"x":{"type":"uuid"}},
			"relations":{"lines":{"type":"has_many","target":"lines","fk":"order_id","limit":10}}}},
		"rbac":{"roles":{}}
	}`)
	if errs := CheckUnknownKeys(raw); len(errs) != 0 {
		t.Fatalf("valid relations rejected by strict-key check: %v", errs)
	}
}
