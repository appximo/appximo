package handlers

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/appximo/appximo/pkg/db"
)

func TestWriteDBError_MissingTenantIs400(t *testing.T) {
	rec := httptest.NewRecorder()
	pgErr := &pgconn.PgError{Code: "42P01", Message: `relation "tenant_11.guides" does not exist`}
	writeDBError(rec, fmt.Errorf("query: %w", pgErr))
	if rec.Code != 400 {
		t.Fatalf("missing tenant schema must be 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid tenant") {
		t.Errorf("body = %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "tenant_11") || strings.Contains(rec.Body.String(), "does not exist") {
		t.Errorf("must not leak SQL: %q", rec.Body.String())
	}
}

func TestWriteDBError_BadUUIDIs400NoLeak(t *testing.T) {
	rec := httptest.NewRecorder()
	pgErr := &pgconn.PgError{Code: "22P02", Message: `invalid input syntax for type uuid: "user123"`}
	writeDBError(rec, fmt.Errorf("query: %w", pgErr))
	if rec.Code != 400 {
		t.Fatalf("malformed UUID input must be 400, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "user123") || strings.Contains(rec.Body.String(), "uuid") {
		t.Errorf("must not leak the malformed value or type: %q", rec.Body.String())
	}
}

func TestWriteDBError_UnavailableIs503(t *testing.T) {
	rec := httptest.NewRecorder()
	writeDBError(rec, fmt.Errorf("%w: context deadline exceeded", db.ErrUnavailable))
	if rec.Code != 503 {
		t.Fatalf("unavailable DB must be 503, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "service unavailable") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestWriteDBError_GenericIs500AndHidesSQL(t *testing.T) {
	rec := httptest.NewRecorder()
	leaky := errors.New(`ERROR: relation "tenant_11.guides" does not exist (SQLSTATE 42P01)`)
	writeDBError(rec, leaky)
	if rec.Code != 500 {
		t.Fatalf("ordinary DB error must be 500, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tenant_11") || strings.Contains(rec.Body.String(), "SQLSTATE") {
		t.Errorf("must not leak SQL internals to the client: %q", rec.Body.String())
	}
}
