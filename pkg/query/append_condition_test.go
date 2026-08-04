package query

import (
	"strings"
	"testing"

	"github.com/appximo/appximo/pkg/rbac"
)

func TestAppendRowCondition(t *testing.T) {
	// nil condition → unchanged.
	q, args, err := AppendRowCondition("SELECT * FROM guides WHERE id = $1", []any{"x"}, nil)
	if err != nil || q != "SELECT * FROM guides WHERE id = $1" || len(args) != 1 {
		t.Fatalf("nil condition should pass through unchanged, got %q args=%v err=%v", q, args, err)
	}

	// valid condition → appended as a bound parameter at the next index.
	q, args, err = AppendRowCondition("SELECT * FROM guides WHERE id = $1", []any{"id1"},
		&rbac.WhereCondition{Field: "operator_id", Value: "user-42"})
	if err != nil {
		t.Fatalf("valid condition errored: %v", err)
	}
	if !strings.HasSuffix(q, "AND operator_id = $2") {
		t.Fatalf("condition not appended correctly: %q", q)
	}
	if len(args) != 2 || args[1] != "user-42" {
		t.Fatalf("value not bound as a parameter: %v", args)
	}

	// injection-style field → rejected (fail closed), query untouched.
	_, _, err = AppendRowCondition("DELETE FROM guides WHERE id = $1", []any{"id1"},
		&rbac.WhereCondition{Field: "operator_id = '' OR 1=1 --", Value: "x"})
	if err == nil {
		t.Fatal("expected invalid condition field to be rejected")
	}
}

// TestAppendAliasedRowCondition covers the multi-table subresource variant
// (SEC-AUDIT-V1 Hallazgo 2): the column is qualified by the table alias so it scopes
// the RELATED table unambiguously, and the value is still a bound parameter.
func TestAppendAliasedRowCondition(t *testing.T) {
	// nil condition → unchanged.
	base := "SELECT r.* FROM customers r JOIN orders src ON src.customer_id = r.id WHERE src.id = $1"
	q, args, err := AppendAliasedRowCondition(base, []any{"pid"}, "r", nil)
	if err != nil || q != base || len(args) != 1 {
		t.Fatalf("nil condition should pass through unchanged, got %q args=%v err=%v", q, args, err)
	}

	// valid condition → appended as `r.<field> = $N`, value bound.
	q, args, err = AppendAliasedRowCondition(base, []any{"pid"}, "r",
		&rbac.WhereCondition{Field: "owner_id", Value: "user-7"})
	if err != nil {
		t.Fatalf("valid condition errored: %v", err)
	}
	if !strings.HasSuffix(q, "AND r.owner_id = $2") {
		t.Fatalf("aliased condition not appended correctly: %q", q)
	}
	if len(args) != 2 || args[1] != "user-7" {
		t.Fatalf("value not bound as a parameter: %v", args)
	}

	// injection-style field → rejected (fail closed).
	if _, _, err = AppendAliasedRowCondition(base, []any{"pid"}, "r",
		&rbac.WhereCondition{Field: "owner_id = '' OR 1=1 --", Value: "x"}); err == nil {
		t.Fatal("expected invalid condition field to be rejected")
	}
}
