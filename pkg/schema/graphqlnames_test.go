package schema

import "testing"

// The collision detector and the GraphQL builder MUST agree on the derived type
// name, or the validator would reject schemas the builder accepts (or worse,
// pass ones it panics on). This pins the contract at the value level; the
// graphql package builds every type from these same functions (delegating
// singular/toPascalCase), so this table is the anti-divergence guard.
func TestGraphQLTypeName_Derivation(t *testing.T) {
	cases := map[string]string{
		"guides":      "Guide",
		"guide":       "Guide", // collides with guides — the bug this guards
		"categorias":  "Categoria",
		"categoria":   "Categoria",
		"branches":    "Branch",  // -ches → -ch
		"addresses":   "Address", // -ses → -s
		"companies":   "Company", // -ies → -y
		"order_items": "OrderItem",
		"tasks":       "Task",
	}
	for in, want := range cases {
		if got := GraphQLTypeName(in); got != want {
			t.Errorf("GraphQLTypeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate_GraphQLTypeCollision(t *testing.T) {
	raw := `{
	  "$schema":"x","version":"1","name":"x",
	  "resources": {
	    "categoria":  { "fields": { "n": {"type":"string"} } },
	    "categorias": { "fields": { "n": {"type":"string"} } },
	    "tasks":      { "fields": { "n": {"type":"string"} } }
	  }
	}`
	s, err := LoadFromBytes([]byte(raw))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	errs := Validate(s)
	var hit *ValidationError
	for i := range errs {
		if errs[i].Rule == "graphql_type_collision" {
			hit = &errs[i]
		}
	}
	if hit == nil {
		t.Fatalf("want a graphql_type_collision error, got %v", errs)
	}
	if hit.Got != "categoria, categorias" {
		t.Errorf("collision should name both resources, got %q", hit.Got)
	}

	// A schema with no collision must stay clean.
	rawOK := `{"$schema":"x","version":"1","name":"x","resources":{"tasks":{"fields":{"n":{"type":"string"}}}}}`
	sOK, _ := LoadFromBytes([]byte(rawOK))
	for _, e := range Validate(sOK) {
		if e.Rule == "graphql_type_collision" {
			t.Fatalf("false collision: %v", e)
		}
	}
}
