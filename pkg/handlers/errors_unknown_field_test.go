package handlers_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/handlers"
)

func undefinedColumnErr(col, table string) error {
	return fmt.Errorf("exec: %w", &pgconn.PgError{
		Code:    "42703",
		Message: fmt.Sprintf("column %q of relation %q does not exist", col, table),
	})
}

func TestUndefinedColumnField(t *testing.T) {
	if f, ok := db.UndefinedColumnField(undefinedColumnErr("priority", "tasks")); !ok || f != "priority" {
		t.Fatalf("got (%q,%v), want (priority,true)", f, ok)
	}
	if _, ok := db.UndefinedColumnField(errors.New("plain")); ok {
		t.Fatal("plain error must not classify as undefined column")
	}
	if _, ok := db.UndefinedColumnField(&pgconn.PgError{Code: "23505"}); ok {
		t.Fatal("other SQLSTATE must not classify as undefined column")
	}
}

func TestWriteDBErrorMapsUnknownFieldTo422(t *testing.T) {
	w := httptest.NewRecorder()
	handlers.WriteDBError(w, undefinedColumnErr("priority", "tasks"))

	if w.Code != 422 {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	var body struct {
		Error  string `json:"error"`
		Fields []struct {
			Field, Rule string
		} `json:"fields"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if body.Error != "validation_failed" || len(body.Fields) != 1 ||
		body.Fields[0].Field != "priority" || body.Fields[0].Rule != "unknown_field" {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestUnknownColumnIsNotAServerError(t *testing.T) {
	if handlers.IsServerError(undefinedColumnErr("x", "t")) {
		t.Fatal("42703 must not count as a server error (no stack capture)")
	}
}
