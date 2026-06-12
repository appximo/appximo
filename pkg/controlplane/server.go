package controlplane

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/schema"
)

// maxControlPlaneBody caps control-plane request bodies. Schemas are bounded
// documents; without a limit an admin request could stream an arbitrarily large
// body (OWASP API4).
const maxControlPlaneBody = 1 << 20 // 1 MiB

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
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Key")), []byte(key)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── handlers ──────────────────────────────────────────────────────────────────

// parseAndValidateSchema runs the full schema gate on raw JSON: required,
// unknown-key strictness (CheckUnknownKeys — typos must error, not silently
// no-op), then the semantic validator. Returns the parsed schema, or the
// error strings for the 400 response.
func parseAndValidateSchema(raw json.RawMessage) (*schema.APISchema, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, []string{"schema is required"}
	}
	if errs := schema.CheckUnknownKeys(raw); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, msgs
	}
	var s schema.APISchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, []string{"schema is not valid JSON: " + err.Error()}
	}
	if errs := schema.Validate(&s); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, msgs
	}
	return &s, nil
}

// schemaWarnings returns non-fatal notices for a VALID schema — keys that
// parse (forward compatibility) but do not act yet. Silence here would bless
// dead config; an error would break schemas that must stay loadable when the
// feature ships (same reasoning as the hooks-compiled-at-boot reload warning).
func schemaWarnings(s *schema.APISchema) []string {
	if idx := schema.IndexedResources(s); len(idx) > 0 {
		return []string{fmt.Sprintf(
			"indexes on %s are parsed but not yet applied — DB index creation is coming in a future release",
			strings.Join(idx, ", "))}
	}
	return nil
}

func handleCreateTenant(svc Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxControlPlaneBody)
		// The schema is decoded as RAW JSON first: unknown-key validation
		// (CheckUnknownKeys) must see the bytes before unmarshalling drops
		// unrecognized keys — a typo like "webhooks" has to be an error, not
		// a silently ignored block.
		var raw struct {
			TenantID    string          `json:"tenant_id"`
			DisplayName string          `json:"display_name"`
			Email       string          `json:"email"`
			Plan        string          `json:"plan"`
			Schema      json.RawMessage `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		// Require and validate the schema BEFORE provisioning. Without this,
		// RegisterTenant dereferences req.Schema (nil → panic) and would feed an
		// unvalidated schema into CREATE TABLE DDL. schema.Validate enforces the
		// resource/field name allowlist.
		parsed, errs := parseAndValidateSchema(raw.Schema)
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
			return
		}
		req := RegisterRequest{
			TenantID:    raw.TenantID,
			DisplayName: raw.DisplayName,
			Email:       raw.Email,
			Plan:        raw.Plan,
			Schema:      parsed,
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
		if warns := schemaWarnings(parsed); len(warns) > 0 {
			writeJSON(w, http.StatusCreated, map[string]any{
				"id": tenant.ID, "pg_schema": tenant.PGSchema, "display_name": tenant.DisplayName,
				"email": tenant.Email, "plan": tenant.Plan, "created_at": tenant.CreatedAt,
				"warnings": warns,
			})
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
		r.Body = http.MaxBytesReader(w, r.Body, maxControlPlaneBody)

		var body struct {
			Schema json.RawMessage `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		parsed, errs := parseAndValidateSchema(body.Schema)
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
			return
		}

		if err := svc.UpdateSchema(r.Context(), id, parsed); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		resp := map[string]any{"status": "migration_queued"}
		if warns := schemaWarnings(parsed); len(warns) > 0 {
			resp["warnings"] = warns
		}
		writeJSON(w, http.StatusOK, resp)
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
