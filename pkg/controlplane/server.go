package controlplane

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/go-chi/chi/v5"
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

// schemaWarnings returns non-fatal notices for a VALID schema: findings that do not
// block the deploy but almost certainly mean the schema will not behave as intended
// (schema.Warnings — SCHEMA-5). They ride in the deploy response so the caller
// (Studio, the CLI, an AI agent) sees them at the moment of the change, which is the
// only moment they are actionable.
func schemaWarnings(s *schema.APISchema) []string {
	warns := schema.Warnings(s)
	if len(warns) == 0 {
		return nil
	}
	out := make([]string, 0, len(warns))
	for _, w := range warns {
		msg := w.Field + ": " + w.Message
		if w.Fix != "" {
			msg += " → " + w.Fix
		}
		out = append(out, msg)
	}
	return out
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
		// Strict decode (NIGHT-SWEEP-S1): this was the one operator body on the
		// control plane still decoding leniently — a misspelled envelope key
		// ("scheme" for "schema", "tenantid") was silently dropped while the
		// schema INSIDE the envelope was strict-key-checked. Same F-3/F-8
		// treatment as every other operator body: reject, name the key.
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&raw); err != nil {
			msg := "invalid JSON"
			if e := err.Error(); strings.HasPrefix(e, "json: unknown field ") {
				msg += ": " + e + " (valid keys: tenant_id, display_name, email, plan, schema)"
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
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

		// dry_run → return the classified plan + destructive impact, apply nothing.
		// approved_drops → the enumerated destructive keys (DropTable "<table>" /
		// DropColumn "<table>.<column>") permitted to apply; everything else stays
		// gated. The two compose: dry_run with approved_drops previews exactly what an
		// apply with those approvals would do.
		var body struct {
			Schema        json.RawMessage `json:"schema"`
			DryRun        bool            `json:"dry_run"`
			ApprovedDrops []string        `json:"approved_drops"`
		}
		// ADR-024: the bodies that carry a SAFETY FLAG are decoded strictly. A
		// misspelled `dry_run` ("dryrun", "dry-run") used to decode to false and turn
		// a PREVIEW into a real migration — the operator asked to look and the engine
		// applied. Rejecting the unknown key is the difference between a typo and an
		// unintended deploy.
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		parsed, errs := parseAndValidateSchema(body.Schema)
		if len(errs) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
			return
		}

		if body.DryRun {
			pv, err := svc.PreviewSchema(r.Context(), id, parsed, body.ApprovedDrops)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			resp := map[string]any{"status": "dry_run", "preview": pv}
			if warns := schemaWarnings(parsed); len(warns) > 0 {
				resp["warnings"] = warns
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		outcome, err := svc.UpdateSchemaApproved(r.Context(), id, parsed, body.ApprovedDrops)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		resp := map[string]any{"status": "migration_queued"}
		if outcome != nil {
			if len(outcome.AppliedDrops) > 0 {
				resp["applied_drops"] = outcome.AppliedDrops
			}
			if len(outcome.GatedDrops) > 0 {
				resp["gated_drops"] = outcome.GatedDrops
			}
			if len(outcome.UnmatchedApprovals) > 0 {
				resp["unmatched_approvals"] = outcome.UnmatchedApprovals
			}
		}
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
