package handlers

import "testing"

// TestBuildInsertArgs_QuotesIdentifiers verifies that client-supplied column
// names are double-quoted (pgx.Identifier.Sanitize) so a key can never break out
// of the identifier position into executable SQL, even though the write path
// otherwise passes columns through to the DB (the schema can evolve at runtime).
func TestBuildInsertArgs_QuotesIdentifiers(t *testing.T) {
	cols, placeholders, args := BuildInsertArgs(map[string]any{"status": "a", "code": "b"})

	// Keys are sorted (code, status) and each identifier is double-quoted.
	if cols != `"code", "status"` {
		t.Fatalf("columns must be quoted and sorted, got %q", cols)
	}
	if placeholders != "$1, $2" {
		t.Fatalf("placeholders: got %q want \"$1, $2\"", placeholders)
	}
	if len(args) != 2 {
		t.Fatalf("args: got %d want 2", len(args))
	}
}

// TestBuildInsertArgs_InjectionKeyIsNeutralized confirms an injection-style key is
// rendered inert by quoting (it becomes a quoted identifier, not SQL). At the DB
// such a column does not exist, so the INSERT fails cleanly rather than executing.
func TestBuildInsertArgs_InjectionKeyIsNeutralized(t *testing.T) {
	cols, _, _ := BuildInsertArgs(map[string]any{`x") ; DROP TABLE guides --`: 1})
	if cols == `x") ; DROP TABLE guides --` {
		t.Fatal("identifier was not quoted — injection possible")
	}
	// Sanitize doubles the embedded quote and wraps the whole thing in quotes.
	if cols[0] != '"' || cols[len(cols)-1] != '"' {
		t.Fatalf("expected a single quoted identifier, got %q", cols)
	}
}
