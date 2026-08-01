package platformadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// Schema version history + rollback (VERSION-S1) — the Studio "History" view's
// backend. Thin delegates into the control plane service (the single schema
// authority): the history is APPEND-ONLY (a rollback creates a new version whose
// content equals an old one) and a rollback is a RE-DEPLOY of a stored version
// through the SAME diff→destructive-gate→apply migration path as a deploy — not
// a second engine, and never a "magic" restore: what later versions added is
// reverted as gated drops (data loss shown and explicitly approved), and data
// already lost to an approved forward drop is NOT recoverable.

// handleSchemaHistory is GET /admin/tenants/{id}/schema/history?page=&per_page=.
func (s *Service) handleSchemaHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	pg, err := s.cp.ListSchemaHistory(r.Context(), id, page, perPage)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, pg)
	case errors.Is(err, controlplane.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list schema history failed"})
	}
}

// handleSchemaVersion is GET /admin/tenants/{id}/schema/history/{version}: one
// recorded version WITH its full schema (for viewing / loading into the editor).
func (s *Service) handleSchemaVersion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	version, err := strconv.Atoi(chi.URLParam(r, "version"))
	if err != nil || version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version must be a positive integer"})
		return
	}
	v, err := s.cp.GetSchemaVersion(r.Context(), id, version)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"version": v.Version, "hash": v.Hash, "source": v.Source, "note": v.Note,
			"created_at": v.CreatedAt, "resources": v.Resources,
			"schema": json.RawMessage(v.SchemaJSON),
		})
	case errors.Is(err, controlplane.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	case errors.Is(err, schemahistory.ErrVersionNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "schema version not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get schema version failed"})
	}
}

// handleSchemaRollback is POST /admin/tenants/{id}/schema/rollback with
// {version, dry_run, approved_drops} — the same two-step contract as a deploy:
//
//   - dry_run:true → the classified plan of REVERTING to that version, computed
//     by the existing migration preview against the live DB: what later versions
//     added shows up as DESTRUCTIVE drops with their measured rows_lost (gated,
//     approval required), plus the safe ops and concerns. Applies nothing.
//   - dry_run:false → applies the rollback, executing ONLY the enumerated
//     approved_drops (everything else stays gated), and appends a NEW history
//     version whose content is the target's (append-only — the trace survives).
//
// Like the deploy endpoint, failures return the engine's ACTIONABLE error.
func (s *Service) handleSchemaRollback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	var body struct {
		Version       int      `json:"version"`
		DryRun        bool     `json:"dry_run"`
		ApprovedDrops []string `json:"approved_drops"`
	}
	// ADR-024: strict — a rollback is a deploy, and its dry_run is a safety flag.
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.Version < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version must be a positive integer"})
		return
	}

	if body.DryRun {
		v, err := s.cp.GetSchemaVersion(r.Context(), id, body.Version)
		if err != nil {
			writeRollbackErr(w, err)
			return
		}
		var target schema.APISchema
		if err := json.Unmarshal(v.SchemaJSON, &target); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "stored schema is unreadable: " + err.Error(), "stage": "preview"})
			return
		}
		pv, err := s.cp.PreviewSchema(r.Context(), id, &target, body.ApprovedDrops)
		if err != nil {
			if errors.Is(err, controlplane.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
				return
			}
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "stage": "preview"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "dry_run", "target_version": v.Version, "target_hash": v.Hash,
			"preview": pv,
		})
		return
	}

	res, err := s.cp.RollbackSchema(r.Context(), id, body.Version, body.ApprovedDrops)
	if err != nil {
		writeRollbackErr(w, err)
		return
	}
	resp := map[string]any{
		"status": "rolled_back", "tenant_id": id,
		"target_version": res.TargetVersion, "new_version": res.NewVersion,
	}
	if res.Outcome != nil {
		if len(res.Outcome.AppliedDrops) > 0 {
			resp["applied_drops"] = res.Outcome.AppliedDrops
		}
		if len(res.Outcome.GatedDrops) > 0 {
			resp["gated_drops"] = res.Outcome.GatedDrops
		}
		if len(res.Outcome.UnmatchedApprovals) > 0 {
			resp["unmatched_approvals"] = res.Outcome.UnmatchedApprovals
		}
	}
	// The now-live schema rides along so the editor can load it / activate it
	// (restart or hot-swap) without a second fetch.
	if res.Schema != nil {
		if b, mErr := json.Marshal(res.Schema); mErr == nil {
			resp["schema"] = json.RawMessage(b)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeRollbackErr maps the rollback error taxonomy: unknown tenant/version →
// 404; anything else → the engine's actionable message as 422 (this is the
// authenticated super-admin path — never a masked 500).
func writeRollbackErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, controlplane.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	case errors.Is(err, schemahistory.ErrVersionNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "schema version not found"})
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "stage": "rollback"})
	}
}
