package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/miguelangel/appitools/pkg/db"
)

// WriteDBError maps a database error to an HTTP response without leaking internal
// details (raw SQL, schema names) to the client:
//   - missing tenant schema/relation (42P01/3F000) → 400 "invalid tenant"
//   - caller-supplied bad input (22P02, e.g. a non-UUID id)  → 400 "invalid request"
//   - unreachable database (timeout / open breaker)          → 503 "service unavailable"
//   - anything else                                          → 500 "internal error"
func WriteDBError(w http.ResponseWriter, err error) {
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
