package db

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestFileReferenceViolation covers the classifier that turns a file-FK
// insert/update violation (FILES-LINK-S1) into a field-addressed 422: only a
// 23503 whose Detail says "is not present in table \"files\"" matches, and the
// violating column is parsed from the Detail's Key(...) list.
func TestFileReferenceViolation(t *testing.T) {
	mk := func(detail string) error {
		return fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23503", Detail: detail})
	}

	col, ok := FileReferenceViolation(mk(`Key (formula)=(3b241101-e2bb-4255-8caf-4136c566a962) is not present in table "files".`))
	if !ok || col != "formula" {
		t.Errorf("expected (formula, true), got (%q, %v)", col, ok)
	}

	// A missing reference to a normal resource is NOT a file violation.
	if _, ok := FileReferenceViolation(mk(`Key (departamento_id)=(x) is not present in table "departamentos".`)); ok {
		t.Error("non-files FK must not classify as a file reference violation")
	}
	// The DELETE direction (file still referenced) is the generic 409, not this.
	if _, ok := FileReferenceViolation(mk(`Key (id)=(x) is still referenced from table "pacientes".`)); ok {
		t.Error("still-referenced must not classify as a file reference violation")
	}
	// Non-FK errors never match.
	if _, ok := FileReferenceViolation(fmt.Errorf("plain error")); ok {
		t.Error("plain error must not classify")
	}
	if _, ok := FileReferenceViolation(fmt.Errorf("wrap: %w", &pgconn.PgError{Code: "23505"})); ok {
		t.Error("unique violation must not classify")
	}
	// But the generic ForeignKeyViolation still handles both directions.
	if msg, ok := ForeignKeyViolation(mk(`Key (formula)=(x) is not present in table "files".`)); !ok || msg == "" {
		t.Error("file FK violation must still be a ForeignKeyViolation for callers that want the 409")
	}
}
