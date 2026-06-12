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
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
