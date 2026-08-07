package query

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// relTestSchema mirrors the ADR-019 sales example: orders has_many lines,
// lines belongs_to products, products many_to_many orders (via order_products).
func relTestSchema() *schema.APISchema {
	return &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"orders": {
				Fields: map[string]schema.FieldDef{
					"status":      {Type: "string"},
					"customer_id": {Type: "uuid"},
				},
				Relations: map[string]schema.RelationDef{
					"lines":    {Type: "has_many", Target: "lines", FK: "order_id"},
					"customer": {Type: "belongs_to", Target: "customers", FK: "customer_id"},
				},
			},
			"lines": {
				Fields: map[string]schema.FieldDef{
					"order_id":   {Type: "uuid"},
					"product_id": {Type: "uuid"},
					"qty":        {Type: "int"},
				},
				Relations: map[string]schema.RelationDef{
					"product": {Type: "belongs_to", Target: "products", FK: "product_id"},
				},
			},
			"products": {
				Fields: map[string]schema.FieldDef{"name": {Type: "string"}},
				Relations: map[string]schema.RelationDef{
					"orders": {Type: "many_to_many", Target: "orders", Through: "order_products", FK: "product_id", TargetFK: "order_id"},
				},
			},
			"customers":      {Fields: map[string]schema.FieldDef{"name": {Type: "string"}}},
			"order_products": {Fields: map[string]schema.FieldDef{"order_id": {Type: "uuid"}, "product_id": {Type: "uuid"}}},
		},
	}
}

func allowAll(string) (bool, []string, *rbac.WhereCondition) { return true, nil, nil }

func mustList(t *testing.T, base, include string, baseArgs []any, rb RelationRBAC) (string, []any) {
	t.Helper()
	s := relTestSchema()
	sql, args, ierr := BuildListInclude(base, include, "SELECT * FROM "+base, baseArgs, "id", "ASC", s, schema.DefaultMaxIncludeDepth, rb)
	if ierr != nil {
		t.Fatalf("BuildListInclude(%q): unexpected error %d %s", include, ierr.Status, ierr.Msg)
	}
	return sql, args
}

func TestParseIncludeTree(t *testing.T) {
	root, perr := parseIncludeTree("lines.product,customer")
	if perr != nil {
		t.Fatalf("unexpected error: %v", perr)
	}
	if _, ok := root.children["lines"]; !ok {
		t.Fatal("missing lines")
	}
	if _, ok := root.children["lines"].children["product"]; !ok {
		t.Fatal("missing nested product under lines")
	}
	if _, ok := root.children["customer"]; !ok {
		t.Fatal("missing customer")
	}
}

// TestParseIncludeTree_EmptyEntriesRejected — NIGHT-SWEEP-S1: empty entries and
// segments used to be silently dropped (`include=,,,` even switched the
// response onto the include serialization path with zero embeds).
func TestParseIncludeTree_EmptyEntriesRejected(t *testing.T) {
	for _, in := range []string{"lines,", ",lines", ",,,", "lines..product", "lines,,customer"} {
		if _, perr := parseIncludeTree(in); perr == nil {
			t.Errorf("parseIncludeTree(%q): expected a named 400, got nil", in)
		} else if perr.Status != 400 {
			t.Errorf("parseIncludeTree(%q): status %d", in, perr.Status)
		}
	}
}

// TestInclude_UnknownRelationListsAlternatives — the ADR-024 second axis on the
// include error: name the offender AND the available set.
func TestInclude_UnknownRelationListsAlternatives(t *testing.T) {
	s := relTestSchema()
	_, _, ierr := BuildListInclude("orders", "ghost", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr == nil {
		t.Fatal("expected unknown-relation error")
	}
	if !strings.Contains(ierr.Msg, "ghost") || !strings.Contains(ierr.Msg, "available:") {
		t.Errorf("error must name the relation and list alternatives, got: %s", ierr.Msg)
	}
}

func TestIncludeHasMany_SingleRoundTrip(t *testing.T) {
	sql, _ := mustList(t, "orders", "lines", nil, allowAll)
	// One query: json_agg over the wrapped base rows, a LEFT JOIN LATERAL for the
	// child collection, correlated child.order_id = parent.id, top-N LIMIT 50.
	for _, want := range []string{
		"json_agg(_q.row",
		"json_build_object('id', _base.\"id\"",
		"LEFT JOIN LATERAL",
		"json_agg(_e ORDER BY _ord)",
		"COALESCE(",
		"'[]'::json",
		"\"order_id\" = _base.\"id\"",
		"LIMIT 50",
		"FROM \"lines\" _c",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("has_many SQL missing %q\nSQL: %s", want, sql)
		}
	}
	// Exactly one LATERAL (no N+1 fan-out per row).
	if n := strings.Count(sql, "LEFT JOIN LATERAL"); n != 1 {
		t.Errorf("expected 1 LATERAL for one has_many include, got %d", n)
	}
}

func TestIncludeBelongsTo(t *testing.T) {
	sql, _ := mustList(t, "lines", "product", nil, allowAll)
	for _, want := range []string{
		"LEFT JOIN LATERAL",
		"FROM \"products\" _c",
		"\"id\" = _base.\"product_id\"",
		"LIMIT 1",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("belongs_to SQL missing %q\nSQL: %s", want, sql)
		}
	}
	// belongs_to is a single object, not an aggregate.
	if strings.Contains(sql, "json_agg(_e") {
		t.Errorf("belongs_to should not aggregate\nSQL: %s", sql)
	}
}

func TestIncludeManyToMany(t *testing.T) {
	sql, _ := mustList(t, "products", "orders", nil, allowAll)
	for _, want := range []string{
		"FROM \"order_products\" _h1",
		"JOIN \"orders\" _c1",
		"_c1.\"id\" = _h1.\"order_id\"", // target join on through.target_fk
		"_h1.\"product_id\" = _base.\"id\"",
		"json_agg(_e ORDER BY _ord)",
		"LIMIT 50",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("many_to_many SQL missing %q\nSQL: %s", want, sql)
		}
	}
}

func TestIncludeNestedDepth2(t *testing.T) {
	sql, _ := mustList(t, "orders", "lines.product", nil, allowAll)
	// Two LATERALs: lines under orders, product under each line.
	if n := strings.Count(sql, "LEFT JOIN LATERAL"); n != 2 {
		t.Errorf("expected 2 LATERAL for lines.product, got %d\nSQL: %s", n, sql)
	}
	if !strings.Contains(sql, "FROM \"products\" _c") {
		t.Errorf("nested product table missing\nSQL: %s", sql)
	}
}

func TestIncludeDepthExceeded(t *testing.T) {
	s := relTestSchema()
	// orders → lines → product → (products has m2m orders) = depth 3.
	_, _, ierr := BuildListInclude("orders", "lines.product.orders", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr == nil || ierr.Status != 400 {
		t.Fatalf("expected 400 depth error, got %v", ierr)
	}
}

func TestIncludeUnknownRelation(t *testing.T) {
	s := relTestSchema()
	_, _, ierr := BuildListInclude("orders", "bogus", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr == nil || ierr.Status != 400 {
		t.Fatalf("expected 400 unknown relation, got %v", ierr)
	}
}

func TestIncludeRBACForbidden(t *testing.T) {
	s := relTestSchema()
	// Role may read orders but NOT lines → requesting include=lines is 403.
	rb := func(resource string) (bool, []string, *rbac.WhereCondition) {
		return resource == "orders", nil, nil
	}
	_, _, ierr := BuildListInclude("orders", "lines", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, rb)
	if ierr == nil || ierr.Status != 403 {
		t.Fatalf("expected 403 forbidden include, got %v", ierr)
	}
}

func TestIncludeRBACRowConditionInjected(t *testing.T) {
	s := relTestSchema()
	baseArgs := []any{"open"} // base WHERE uses $1
	rb := func(resource string) (bool, []string, *rbac.WhereCondition) {
		if resource == "lines" {
			return true, nil, &rbac.WhereCondition{Field: "order_id", Op: "eq", Value: "OWNER"}
		}
		return true, nil, nil
	}
	sql, args, ierr := BuildListInclude("orders", "lines", "SELECT * FROM orders WHERE status = $1", baseArgs, "id", "ASC", s, schema.DefaultMaxIncludeDepth, rb)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	// The target row condition appends a bound param ($2) AND a WHERE term.
	if !strings.Contains(sql, "_c1.\"order_id\" = $2") {
		t.Errorf("row condition not injected into embed WHERE\nSQL: %s", sql)
	}
	if len(args) != 2 || args[1] != "OWNER" {
		t.Errorf("expected base arg + injected OWNER, got %v", args)
	}
}

func TestIncludeFieldAllowlist(t *testing.T) {
	s := relTestSchema()
	// lines target restricted to qty only → json_build_object emits id + qty, not order_id/product_id.
	rb := func(resource string) (bool, []string, *rbac.WhereCondition) {
		if resource == "lines" {
			return true, []string{"qty"}, nil
		}
		return true, nil, nil
	}
	sql, _, ierr := BuildListInclude("orders", "lines", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, rb)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, "'qty', _c1.\"qty\"") {
		t.Errorf("allowed field qty missing\nSQL: %s", sql)
	}
	if strings.Contains(sql, "'product_id', _c1.\"product_id\"") {
		t.Errorf("forbidden field product_id should not be emitted\nSQL: %s", sql)
	}
}

func TestGetIncludeSingleObject(t *testing.T) {
	s := relTestSchema()
	sql, args, ierr := BuildGetInclude("orders", "lines", "SELECT * FROM orders WHERE id = $1", []any{"abc"}, s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, "LIMIT 1") || !strings.Contains(sql, "json_build_object('id', _base.\"id\"") {
		t.Errorf("get-include SQL malformed\nSQL: %s", sql)
	}
	if len(args) != 1 {
		t.Errorf("expected 1 base arg, got %v", args)
	}
}

func TestIncludeCustomLimit(t *testing.T) {
	s := relTestSchema()
	o := s.Resources["orders"]
	o.Relations["lines"] = schema.RelationDef{Type: "has_many", Target: "lines", FK: "order_id", Limit: 5}
	s.Resources["orders"] = o
	sql, _, ierr := BuildListInclude("orders", "lines", "SELECT * FROM orders", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, "LIMIT 5") {
		t.Errorf("custom embed limit not applied\nSQL: %s", sql)
	}
}

// refTestSchema models the evaluator's blog (PUBLIC-SURFACE-S1 Part D): the FK
// `autor_id` on articulos declares `references: "user_id"` — a non-id unique
// column of autores (the $user_id pattern the spec recommends). The embed must
// join on THAT column, exactly like the /api/articulos/{id}/autor subroute.
func refTestSchema() *schema.APISchema {
	return &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{
			"articulos": {
				Fields: map[string]schema.FieldDef{
					"titulo":   {Type: "string"},
					"autor_id": {Type: "uuid", Relation: "autores", References: "user_id"},
				},
				Relations: map[string]schema.RelationDef{
					"autor":      {Type: "belongs_to", Target: "autores", FK: "autor_id"},
					"etiquetas":  {Type: "many_to_many", Target: "etiquetas", Through: "articulo_etiquetas", FK: "articulo_id", TargetFK: "etiqueta_slug"},
					"comentarios": {Type: "has_many", Target: "comentarios", FK: "articulo_codigo"},
				},
			},
			"autores": {
				Fields: map[string]schema.FieldDef{
					"nombre":  {Type: "string"},
					"user_id": {Type: "uuid", Unique: true},
				},
				Relations: map[string]schema.RelationDef{
					// has_many whose child FK references a NON-id parent column:
					// comentarios.autor_user = autores.user_id.
					"comentarios": {Type: "has_many", Target: "comentarios", FK: "autor_user"},
				},
			},
			"comentarios": {
				Fields: map[string]schema.FieldDef{
					"texto": {Type: "string"},
					// child FK to autores via its unique user_id column.
					"autor_user": {Type: "uuid", Relation: "autores", References: "user_id"},
					// child FK to articulos via a non-id unique column.
					"articulo_codigo": {Type: "string", Relation: "articulos", References: "codigo"},
				},
			},
			"etiquetas": {
				Fields: map[string]schema.FieldDef{
					"nombre": {Type: "string"},
					"slug":   {Type: "string", Unique: true},
				},
			},
			"articulo_etiquetas": {
				Fields: map[string]schema.FieldDef{
					"articulo_id":   {Type: "uuid", Relation: "articulos"},
					"etiqueta_slug": {Type: "string", Relation: "etiquetas", References: "slug"},
				},
			},
		},
	}
}

// TestIncludeBelongsToHonorsReferences — PUBLIC-SURFACE-S1 Part D: the embed
// used to hardcode target.id = parent.<fk>, answering null for every FK that
// declares a non-id `references` (while the subroute joined correctly — two
// implementations of one rule, diverged). The join must follow the FK field's
// ReferencedColumn, the same source the subroute uses.
func TestIncludeBelongsToHonorsReferences(t *testing.T) {
	s := refTestSchema()
	sql, _, ierr := BuildListInclude("articulos", "autor", "SELECT * FROM articulos", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, `"user_id" = _base."autor_id"`) {
		t.Errorf("belongs_to embed must join on the referenced column user_id\nSQL: %s", sql)
	}
	if strings.Contains(sql, `"id" = _base."autor_id"`) {
		t.Errorf("belongs_to embed still joins on id despite references\nSQL: %s", sql)
	}
}

// TestIncludeHasManyHonorsReferences — the child FK declares references to a
// non-id parent column: child.<fk> must match parent.<referenced>, not parent.id.
func TestIncludeHasManyHonorsReferences(t *testing.T) {
	s := refTestSchema()
	sql, _, ierr := BuildListInclude("autores", "comentarios", "SELECT * FROM autores", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, `"autor_user" = _base."user_id"`) {
		t.Errorf("has_many embed must correlate child fk against the referenced parent column\nSQL: %s", sql)
	}
}

// TestIncludeManyToManyHonorsReferences — junction FK fields with `references`:
// the target side joins on the referenced target column, the parent side on the
// referenced parent column (both default id when undeclared — pinned elsewhere).
func TestIncludeManyToManyHonorsReferences(t *testing.T) {
	s := refTestSchema()
	sql, _, ierr := BuildListInclude("articulos", "etiquetas", "SELECT * FROM articulos", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	for _, want := range []string{
		`"slug" = _h`,                 // target join on etiquetas.slug (references of etiqueta_slug)
		`"articulo_id" = _base."id"`,  // parent side: articulo_id has no references → id
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("m2m embed missing %q\nSQL: %s", want, sql)
		}
	}
}

// TestIncludeNestedHonorsReferences — depth-2: the nested embed resolves its own
// FK references too (articulos.comentarios → comentarios.articulo_codigo =
// articulos.codigo would need a codigo field; here nested autor under comentarios).
func TestIncludeNestedHonorsReferences(t *testing.T) {
	s := refTestSchema()
	// comentarios needs a relation to embed under articulos.comentarios: reuse
	// autor via a belongs_to on comentarios.
	c := s.Resources["comentarios"]
	c.Relations = map[string]schema.RelationDef{
		"autor": {Type: "belongs_to", Target: "autores", FK: "autor_user"},
	}
	s.Resources["comentarios"] = c
	sql, _, ierr := BuildListInclude("articulos", "comentarios.autor", "SELECT * FROM articulos", nil, "id", "ASC", s, schema.DefaultMaxIncludeDepth, allowAll)
	if ierr != nil {
		t.Fatalf("unexpected error: %v", ierr)
	}
	if !strings.Contains(sql, `"user_id" = _c`) {
		t.Errorf("nested belongs_to embed must join on referenced user_id\nSQL: %s", sql)
	}
}
