package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/miguelangel/appitools/pkg/db"
)

// IsServerError reports whether err would be answered as HTTP 500 by WriteDBError
// (i.e. it is not a missing-tenant / bad-input / unavailable error). Used to decide
// whether to capture a stack trace — client/availability errors are not bugs.
func IsServerError(err error) bool {
	if err == nil {
		return false
	}
	if _, unknownCol := db.UndefinedColumnField(err); unknownCol {
		return false // client sent a field no column backs — 422, not a bug
	}
	if _, fk := db.ForeignKeyViolation(err); fk {
		return false // referential conflict (RESTRICT delete / bad ref) — 409, not a bug
	}
	return !db.IsMissingTenant(err) && !db.IsBadInput(err) && !db.IsUnavailable(err)
}

// WriteDBError maps a database error to an HTTP response without leaking internal
// details (raw SQL, schema names) to the client:
//   - missing tenant schema/relation (42P01/3F000) → 400 "invalid tenant"
//   - caller-supplied bad input (22P02, e.g. a non-UUID id)  → 400 "invalid request"
//   - unknown column on a write (42703)            → 422 validation_failed/unknown_field
//   - unreachable database (timeout / open breaker)          → 503 "service unavailable"
//   - anything else                                          → 500 "internal error"
func WriteDBError(w http.ResponseWriter, err error) {
	// Unknown field → the same 422 shape as the declarative validator (S44),
	// so clients handle one error contract for every rejected write.
	if field, ok := db.UndefinedColumnField(err); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error": "validation_failed",
			"fields": []map[string]string{
				{"field": field, "rule": "unknown_field", "message": "is not a field of this resource"},
			},
		})
		return
	}
	// A write whose `file` field references no file of the tenant (FILES-LINK-S1)
	// → the SAME field-addressed 422 the declarative validator uses: to the
	// client it is input validation on that field (an id that isn't one of the
	// tenant's files — nonexistent or another tenant's), even though the guard is
	// the real FK. Checked BEFORE the generic FK 409, which keeps handling every
	// resource↔resource referential conflict.
	if column, ok := db.FileReferenceViolation(err); ok {
		if column == "" {
			column = "file"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error": "validation_failed",
			"fields": []map[string]string{
				{"field": column, "rule": "file_not_found", "message": "does not reference an existing file of this tenant"},
			},
		})
		return
	}
	// Foreign-key violation → 409 Conflict (MIG-F1-S1): a RESTRICT delete of a
	// still-referenced row, or a write referencing a non-existent row. A clear,
	// safe message — never a masked 500.
	if fkMsg, ok := db.ForeignKeyViolation(err); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": fkMsg}) //nolint:errcheck
		return
	}
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case db.IsMissingTenant(err):
		status = http.StatusBadRequest
		msg = "invalid tenant"
	case db.IsBadInput(err):
		status = http.StatusBadRequest
		msg = "invalid request"
	case db.IsUnavailable(err):
		status = http.StatusServiceUnavailable
		msg = "service unavailable"
		// Tell the client this is transient and roughly when to come back. A 503
		// WITHOUT Retry-After leaves an SDK guessing (most default to an immediate
		// retry, which is exactly what a saturated database does not need).
		w.Header().Set("Retry-After", "1")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
