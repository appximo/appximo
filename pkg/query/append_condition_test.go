package query

import (
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/rbac"
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
