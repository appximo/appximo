package controlplane

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/schema"
)

// NewControlPlaneRouter builds the chi.Mux for the control plane API (port 9090).
// adminKey is compared against the X-Admin-Key request header on all routes except /health.
func NewControlPlaneRouter(svc Service, adminKey string) *chi.Mux {
	r := chi.NewMux()

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "port": 9090})
	})

	// All management endpoints require the admin key.
	r.Group(func(r chi.Router) {
		r.Use(adminKeyMiddleware(adminKey))

		r.Post("/tenants", handleCreateTenant(svc))
		r.Get("/tenants/{id}", handleGetTenant(svc))
		r.Put("/tenants/{id}/schema", handleUpdateSchema(svc))
		r.Get("/tenants/{id}/schema", handleGetSchema(svc))
	})

	return r
}

// ── middleware ────────────────────────────────────────────────────────────────

func adminKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Admin-Key") != key {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── handlers ──────────────────────────────────────────────────────────────────

func handleCreateTenant(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}

		tenant, err := svc.Register(r.Context(), req)
		if err != nil {
			switch {
			case errors.Is(err, ErrAlreadyExists):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, ErrInvalidInput):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}
		writeJSON(w, http.StatusCreated, tenant)
	}
}

func handleGetTenant(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		tenant, err := svc.GetByID(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, tenant)
	}
}

func handleUpdateSchema(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		var body struct {
			Schema *schema.APISchema `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if body.Schema == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schema is required"})
			return
		}

		if errs := schema.Validate(body.Schema); len(errs) > 0 {
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": msgs})
			return
		}

		if err := svc.UpdateSchema(r.Context(), id, body.Schema); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "migration_queued"})
	}
}

func handleGetSchema(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		s, err := svc.GetSchema(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if s == nil {
			writeJSON(w, http.StatusOK, nil)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
