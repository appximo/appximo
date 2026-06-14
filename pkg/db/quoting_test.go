package db

import (
	"strings"
	"testing"
)

// BUG1: table-name validation must accept hyphenated resource names (e.g. a
// many-to-many junction "order-products") while still rejecting anything that
// could break out of the identifier position — quoting was unified, security was
// not relaxed.
func TestValidateTableName(t *testing.T) {
	ok := []string{"orders", "order-products", "items_x", "a", "a1-b_2"}
	for _, n := range ok {
		if err := validateTableName(n); err != nil {
			t.Errorf("validateTableName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{
		"", "Orders", "1items", "a b", "items;--",
		`items"; DROP TABLE x; --`, "items)", "órdenes", "a.b",
	}
	for _, n := range bad {
		if err := validateTableName(n); err == nil {
			t.Errorf("validateTableName(%q) = nil, want rejection", n)
		}
	}
}

// qualifyTableNames must qualify the table whether the query references it quoted
// (the builder's new output) or bare (older callers / QueryDirect contract), and
// it must handle hyphenated names.
func TestQualifyTableNames_QuotedAndBare(t *testing.T) {
	cases := []struct {
		query, schema, table, wantContains string
	}{
		{`SELECT * FROM "orders" WHERE x=$1`, "tenant_a", "orders", `FROM "tenant_a"."orders"`},
		{`SELECT name FROM orders`, "tenant_a", "orders", `FROM "tenant_a"."orders"`},
		{`SELECT * FROM "order-products"`, "tenant_a", "order-products", `FROM "tenant_a"."order-products"`},
		{`SELECT COUNT(*) FROM "orders"`, "tenant_b", "orders", `FROM "tenant_b"."orders"`},
	}
	for _, c := range cases {
		got := qualifyTableNames(c.query, c.schema, c.table)
		if !strings.Contains(got, c.wantContains) {
			t.Errorf("qualifyTableNames(%q,...) = %q; want contains %q", c.query, got, c.wantContains)
		}
	}
}
