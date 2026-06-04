package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/miguelangel/appitools/pkg/db"
)

// writeDBError maps a database error to an HTTP response without leaking
// internal details (raw SQL text, schema names) to the client. An unreachable
// database (query timeout or open circuit breaker) becomes 503 Service
// Unavailable so callers can retry; anything else is a generic 500.
func writeDBError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case db.IsMissingTenant(err):
		// A missing tenant schema/relation means the tenant is not provisioned.
		status = http.StatusBadRequest
		msg = "invalid tenant"
	case db.IsBadInput(err):
		// A value the caller supplied (e.g. a non-UUID user_id from the token,
		// or a malformed filter) that Postgres could not parse for the column.
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
