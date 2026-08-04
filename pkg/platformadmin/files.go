package platformadmin

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/files"
)

// The Studio files manager (UI-F5-S1). The SPA is served on ONE origin with a
// platform JWT, so it cannot reach the Host-scoped /api/files (the same reason
// data browsing got /admin/tenants/{id}/data — ADMIN-UI-V1.2). These routes are
// the manager's seam: THIN delegates into the engine's real files.Store, so
// every operation pays the IDENTICAL validation and behavior as the tenant
// API — the same OWASP upload rejections (422 with the reason, 413 over the
// cap), the same serve strategy (local ServeContent proxy / S3 presigned 302),
// the same dedup-aware delete. Nothing is reimplemented; nothing bypasses the
// Store. Authorization is the platform super-admin (any tenant), exactly like
// the rest of the tenant-scoped admin surface.

// platformFilesRole is the role stamped into download tokens minted by the
// ADMIN url route. The admin download route accepts ONLY this role, and the
// public /files/signed route never does (no schema RBAC grant exists for it),
// so an admin-minted capability cannot be replayed on the tenant route with a
// different tenant Host, and a tenant-minted token (a real RBAC role) cannot
// be replayed here. Either way a token grants exactly its one file.
const platformFilesRole = "__platform_files__"

// SetFileStore wires the engine's file store so the admin API can manage a
// tenant's files (the Studio files manager). Set by app.go after NewService —
// like SetTenantDB. When unset, the files routes answer 503 (the CLI/bootstrap
// path never needs them). secret signs the short-lived download tokens (the
// SAME engine JWT secret files.SignedURLHandler uses); ttl bounds both those
// tokens and S3 presigned URLs.
func (s *Service) SetFileStore(store *files.Store, maxBytes int64, ttl time.Duration, secret []byte) {
	s.files = store
	s.filesMaxBytes = maxBytes
	s.filesTokenTTL = ttl
	s.filesSecret = secret
}

// filesReady guards the manager routes when the store is not wired.
func (s *Service) filesReady(w http.ResponseWriter) bool {
	if s.files == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "file store not configured"})
		return false
	}
	return true
}

// tenantForFiles validates the {id} path param and confirms the tenant exists
// (a manager operation must never lazily create metadata tables for an
// unregistered tenant). Returns "" after writing the error response.
func (s *Service) tenantForFiles(w http.ResponseWriter, r *http.Request) string {
	id := chi.URLParam(r, "id")
	if !tenantIDRe.MatchString(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		return ""
	}
	if _, err := s.cp.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, controlplane.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tenant lookup failed"})
		}
		return ""
	}
	return id
}

// fileIDParam validates the {fid} path param (a UUID, like the tenant routes).
func fileIDParam(w http.ResponseWriter, r *http.Request) string {
	fid := chi.URLParam(r, "fid")
	if _, err := uuid.Parse(fid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file id"})
		return ""
	}
	return fid
}

// handleListFiles is GET /admin/tenants/{id}/files?page=&per_page= — a page of
// the tenant's file METADATA (the authoritative tenant_<id>.files table; never
// a storage List call), newest first, plus the active storage backend name
// (informational). A tenant with no files yet is an empty page.
func (s *Service) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if !s.filesReady(w) {
		return
	}
	id := s.tenantForFiles(w, r)
	if id == "" {
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 {
		perPage = 50
	}
	if perPage > 200 {
		perPage = 200
	}
	metas, total, err := s.files.ListMeta(r.Context(), id, perPage, (page-1)*perPage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "listing failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":    metas,
		"total":    total,
		"page":     page,
		"per_page": perPage,
		"backend":  files.BackendName(s.files.Backend()),
	})
}

// handleUploadFile is POST /admin/tenants/{id}/files (multipart, field "file").
// It delegates to the SAME upload core the tenant route uses (files.
// ProcessUpload → Store.Put), so the OWASP validation and its real errors —
// 422 with the rejection reason, 413 over the cap — surface identically. A
// malicious upload from the manager dies exactly like one from the API.
func (s *Service) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if !s.filesReady(w) {
		return
	}
	id := s.tenantForFiles(w, r)
	if id == "" {
		return
	}
	files.ProcessUpload(s.files, s.filesMaxBytes, id, w, r)
}

// handleDeleteFile is DELETE /admin/tenants/{id}/files/{fid} — the Store's
// dedup-aware delete (the blob goes only when no other upload references the
// same content). 204 / 404.
func (s *Service) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if !s.filesReady(w) {
		return
	}
	id := s.tenantForFiles(w, r)
	if id == "" {
		return
	}
	fid := fileIDParam(w, r)
	if fid == "" {
		return
	}
	if err := s.files.Delete(r.Context(), id, fid); err != nil {
		if errors.Is(err, files.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		// FILES-LINK-S1: a file attached to a record (RESTRICT file FK) — the same
		// clean 409 the public delete route answers with.
		if fkMsg, ok := db.ForeignKeyViolation(err); ok {
			writeJSON(w, http.StatusConflict, map[string]string{"error": fkMsg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFileURL is GET /admin/tenants/{id}/files/{fid}/url — mints the
// short-lived signed download URL for the manager: the backend's NATIVE
// presigned URL on S3 (the bucket serves the bytes), or an engine token URL on
// local. The local URL points at the admin download route below (the public
// /files/signed route is tenant-Host-bound by design, and the manager runs on
// the bare origin) — but the TOKEN is the same files.MintDownloadToken
// capability with the same TTL, and the serve path is the same Store.Serve.
func (s *Service) handleFileURL(w http.ResponseWriter, r *http.Request) {
	if !s.filesReady(w) {
		return
	}
	id := s.tenantForFiles(w, r)
	if id == "" {
		return
	}
	fid := fileIDParam(w, r)
	if fid == "" {
		return
	}
	// The file must exist BEFORE a URL is handed out (same 404 the download gives).
	if _, err := s.files.Stat(r.Context(), id, fid); err != nil {
		if errors.Is(err, files.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "signing failed"})
		return
	}
	ttl := s.filesTokenTTL
	if ttl <= 0 {
		ttl = files.DefaultSignedURLTTL
	}
	u, err := s.files.SignedURL(r.Context(), id, fid, ttl)
	if errors.Is(err, files.ErrSignedURLUnsupported) {
		tok, terr := files.MintDownloadToken(s.filesSecret, id, fid, platformFilesRole, ttl)
		if terr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "signing failed"})
			return
		}
		u, err = adminOrigin(r)+"/admin/tenants/"+id+"/files/"+fid+"/download?token="+tok, nil
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "signing failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": u, "expires_in": int(ttl.Seconds())})
}

// handleFileDownload is GET /admin/tenants/{id}/files/{fid}/download?token=…
// — the local-backend serve leg of the manager's signed URLs. It is NOT behind
// requirePlatform: a browser navigation (the download click) cannot send an
// Authorization header, so the short-lived token IS the credential — minted
// only by the requirePlatform url route above. Verification mirrors the public
// signed route: signature + expiry (files.VerifyDownloadToken), the token's
// tenant and file must match the PATH, and the role must be the platform
// sentinel (a tenant-minted token is useless here, and vice versa). EVERY
// failure is a uniform 404 (anti-fingerprinting). The bytes then flow through
// the IDENTICAL Store.Serve as any download (ServeContent: Range/ETag; S3
// proxy mode reader).
func (s *Service) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	id := chi.URLParam(r, "id")
	fid := chi.URLParam(r, "fid")
	tokTenant, tokFile, role, err := files.VerifyDownloadToken(s.filesSecret, r.URL.Query().Get("token"))
	if err != nil || tokTenant != id || tokFile != fid || role != platformFilesRole {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	files.ServeStored(s.files, id, fid, w, r)
}

// adminOrigin rebuilds the caller-facing origin for minted URLs (forwarded
// proto honored when a proxy terminated TLS).
func adminOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		scheme = xf
	}
	return scheme + "://" + r.Host
}
