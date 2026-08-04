package schema_test

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/schemadiff"
)

// LIBRARY-GAPS-S1 — the `jsonb` field type and the index access method.

func jsonbSchema(t *testing.T, fieldType, indexJSON string) *schema.APISchema {
	t.Helper()
	return parseSchema(t, `{
      "$schema":"https://appximo.com/schema/v1","version":"1","name":"x",
      "resources": { "products": {
        "fields": {
          "name":      { "type": "string", "required": true },
          "atributos": { "type": "`+fieldType+`" }
        },
        "indexes": [ `+indexJSON+` ]
      } }
    }`)
}

func TestJSONB_TypeAcceptedAndMapsToARealJSONBColumn(t *testing.T) {
	s := jsonbSchema(t, "jsonb", `{ "fields": ["name"] }`)
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Fatalf("jsonb must be a valid field type, got: %v", errs)
	}
	// The whole point: a REAL jsonb column, not the TEXT that `json` maps to.
	if got := schemadiff.TypeForAPIType("jsonb"); got.Base != schemadiff.BaseJSONB {
		t.Fatalf("jsonb must map to BaseJSONB, got %v", got.Base)
	}
	// …and `json` is untouched, so no existing tenant's column changes.
	if got := schemadiff.TypeForAPIType("json"); got.Base != schemadiff.BaseText {
		t.Fatalf("json must still map to TEXT (retrocompat), got %v", got.Base)
	}
}

func TestIndexMethod_GINOverJSONB(t *testing.T) {
	s := jsonbSchema(t, "jsonb", `{ "fields": ["atributos"], "method": "gin", "opclass": "jsonb_path_ops" }`)
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Fatalf("gin + jsonb_path_ops over a jsonb column must be valid, got: %v", errs)
	}
	idx := s.Resources["products"].Indexes[0]
	if idx.Method != "gin" || idx.Opclass != "jsonb_path_ops" {
		t.Fatalf("method/opclass did not round-trip: %+v", idx)
	}
}

func TestIndexMethod_RejectedShapes(t *testing.T) {
	cases := []struct {
		name, fieldType, index, want string
	}{
		{
			name: "gin over a non-jsonb column", fieldType: "text",
			index: `{ "fields": ["atributos"], "method": "gin" }`,
			want:  "a gin index requires jsonb columns",
		},
		{
			name: "gin + unique", fieldType: "jsonb",
			index: `{ "fields": ["atributos"], "method": "gin", "unique": true }`,
			want:  "a gin index cannot be unique",
		},
		{
			name: "an unknown method", fieldType: "jsonb",
			index: `{ "fields": ["atributos"], "method": "brin" }`,
			want:  `unknown index method "brin"`,
		},
		{
			name: "opclass without gin", fieldType: "jsonb",
			index: `{ "fields": ["atributos"], "opclass": "jsonb_path_ops" }`,
			want:  "opclass is not supported for index method",
		},
		{
			name: "an unknown opclass", fieldType: "jsonb",
			index: `{ "fields": ["atributos"], "method": "gin", "opclass": "drop table" }`,
			want:  "unknown opclass",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := schema.Validate(jsonbSchema(t, c.fieldType, c.index))
			if !hasError(errs, c.want) {
				t.Fatalf("expected an error containing %q, got: %v", c.want, errs)
			}
		})
	}
}

// An index without a method is byte-identical to before: btree, unsuffixed name.
func TestIndexMethod_DefaultUnchanged(t *testing.T) {
	s := jsonbSchema(t, "jsonb", `{ "fields": ["name"] }`)
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Fatalf("a plain index must stay valid, got: %v", errs)
	}
	if m := s.Resources["products"].Indexes[0].Method; m != "" {
		t.Fatalf("an undeclared method must stay empty (it defaults to btree at migration), got %q", m)
	}
}

func TestIndexMethod_UnknownKeyRejected(t *testing.T) {
	raw := `{
      "$schema":"https://appximo.com/schema/v1","version":"1","name":"x",
      "resources": { "products": {
        "fields": { "atributos": { "type": "jsonb" } },
        "indexes": [ { "fields": ["atributos"], "using": "gin" } ]
      } }
    }`
	if _, err := schema.LoadFromBytes([]byte(raw)); err == nil ||
		!strings.Contains(err.Error(), "unknown key") {
		t.Fatalf(`"using" is not a key — expected rejection, got: %v`, err)
	}
}

// The opclass reaches DDL, so it must never be free-form text: the closed
// allowlist above is the only thing that can be rendered.
func TestIndexOpclass_RenderedFromTheAllowlistOnly(t *testing.T) {
	stmts, err := schemadiff.Render(&schemadiff.Plan{Ops: []schemadiff.Operation{
		schemadiff.AddIndex{
			Table: "products",
			Index: &schemadiff.Index{
				Name: "idx_products_atributos_gin", Columns: []string{"atributos"},
				Method: "gin", Opclass: "jsonb_path_ops",
			},
		},
	}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	sql := stmts[0].SQL
	if !strings.Contains(sql, `USING gin ("atributos" jsonb_path_ops)`) {
		t.Fatalf("expected the opclass on the key column, got: %s", sql)
	}
}
