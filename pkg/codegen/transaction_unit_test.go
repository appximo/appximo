package codegen

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// G4 unit tests for the pure SQL-building + guard safety of the transaction handler
// (no DB needed — they run in the -short lane). The end-to-end atomicity/RBAC/rollback
// behaviour is covered by pkg/integration/transaction_test.go.

func txTestRes() *schema.ResourceSchema {
	return &schema.ResourceSchema{Fields: map[string]schema.FieldDef{
		"sku":        {Type: "string"},
		"stock":      {Type: "int"},
		"updated_at": {Type: "time", Auto: schema.AutoLegacy},
	}}
}

func TestBuildUpdateSQL_GuardAndAutoUpdatedAt(t *testing.T) {
	res := txTestRes()
	// JSON numbers decode to float64 — guard values arrive that way at runtime.
	guards := []txGuard{{Field: "stock", Op: "gte", Value: float64(5)}}
	q, args, terr := buildUpdateSQL(res, `"products"`, "id-1", map[string]any{"stock": float64(95)}, nil, guards)
	if terr != nil {
		t.Fatalf("unexpected txError: %+v", terr)
	}
	// SET stock + auto updated_at, WHERE id, AND stock >= guard, RETURNING *.
	for _, want := range []string{`SET "stock" = $1`, `"updated_at" = $2`, "WHERE id = $3", "AND stock >= $4", "RETURNING *"} {
		if !strings.Contains(q, want) {
			t.Errorf("update SQL missing %q\n  got: %s", want, q)
		}
	}
	// args in order: stock=95 ($1), updated_at ($2), id ($3), guard value=5 ($4).
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if args[0] != float64(95) || args[2] != "id-1" || args[3] != float64(5) {
		t.Fatalf("arg order wrong: %v", args)
	}
}

func TestBuildUpdateSQL_RowConditionThenGuard(t *testing.T) {
	res := txTestRes()
	cond := &rbac.WhereCondition{Field: "owner_id", Op: "eq", Value: "u-1"}
	res.Fields["owner_id"] = schema.FieldDef{Type: "uuid"}
	q, args, terr := buildUpdateSQL(res, `"products"`, "id-9", map[string]any{"stock": float64(1)}, cond,
		[]txGuard{{Field: "stock", Op: "eq", Value: float64(2)}})
	if terr != nil {
		t.Fatalf("txError: %+v", terr)
	}
	// The RBAC row condition AND the guard are both appended (BOLA + compare-and-set).
	if !strings.Contains(q, "AND owner_id = $") || !strings.Contains(q, "AND stock = $") {
		t.Fatalf("expected both row condition and guard in WHERE: %s", q)
	}
	if args[len(args)-1] != float64(2) { // guard value is last
		t.Fatalf("guard value should be the last arg: %v", args)
	}
}

func TestBuildUpdateSQL_NoWritableFields(t *testing.T) {
	// A resource with NO auto updated_at: an empty writable set yields zero SET
	// clauses → 422 (with an auto updated_at the single-op path would bump it).
	res := &schema.ResourceSchema{Fields: map[string]schema.FieldDef{"sku": {Type: "string"}}}
	_, _, terr := buildUpdateSQL(res, `"products"`, "id-1", map[string]any{}, nil, nil)
	if terr == nil || terr.status != 422 {
		t.Fatalf("empty update should be 422, got %+v", terr)
	}
}

func TestAppendGuards_Safety(t *testing.T) {
	res := txTestRes()
	cases := []struct {
		name   string
		guard  txGuard
		status int // 0 = accepted
	}{
		{"valid eq", txGuard{Field: "stock", Op: "eq", Value: float64(1)}, 0},
		{"valid gte", txGuard{Field: "stock", Op: "gte", Value: float64(1)}, 0},
		{"unknown op", txGuard{Field: "stock", Op: "like", Value: float64(1)}, 400},
		{"unknown field", txGuard{Field: "nope", Op: "eq", Value: float64(1)}, 400},
		{"sql-injection field", txGuard{Field: "stock; DROP TABLE x;--", Op: "eq", Value: float64(1)}, 400},
		{"nil value", txGuard{Field: "stock", Op: "eq", Value: nil}, 400},
		{"type mismatch", txGuard{Field: "stock", Op: "eq", Value: "not-an-int"}, 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, terr := appendGuards("SELECT 1", []any{}, []txGuard{c.guard}, res)
			got := 0
			if terr != nil {
				got = terr.status
			}
			if got != c.status {
				t.Errorf("appendGuards(%+v) status=%d want %d", c.guard, got, c.status)
			}
		})
	}
}

// TestAppendGuards_BindsValues confirms the guard value is a bound parameter (never
// interpolated) and the operator comes from the allowlist mapping.
func TestAppendGuards_BindsValues(t *testing.T) {
	res := txTestRes()
	sql, args, terr := appendGuards("SELECT 1 WHERE id=$1", []any{"x"},
		[]txGuard{{Field: "stock", Op: "gt", Value: float64(7)}}, res)
	if terr != nil {
		t.Fatalf("txError: %+v", terr)
	}
	if !strings.Contains(sql, "AND stock > $2") {
		t.Fatalf("expected bound guard predicate, got: %s", sql)
	}
	if len(args) != 2 || args[1] != float64(7) {
		t.Fatalf("guard value not bound: %v", args)
	}
}
