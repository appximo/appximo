package files

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/tenant"
)

// DefaultMaxUploadBytes caps a single upload body. It is a disk-exhaustion guard,
// not a memory one (uploads stream to disk in 64 KiB chunks), so it is generous;
// override via the engine's APPXIMO_FILES_MAX_BYTES.
const DefaultMaxUploadBytes int64 = 256 << 20 // 256 MiB

// SignedPathPrefix is where the engine serves signed-token downloads
// (GET /files/signed/{token}). It sits OUTSIDE /api on purpose: the token IS
// the credential, so the route skips JWT (pkg/auth.skipJWT) and the response
// cache (a blob must never be buffered), while still flowing through the
// tenant + rate-limit middleware.
const SignedPathPrefix = "/files/signed"

// IsByteServingPath reports whether (method, path) is one of the routes that
// serve raw file BYTES — the complete set:
//
//	GET /api/files/{id}                              (tenant download)
//	GET /files/signed/{token}                        (signed-token download)
//	GET /admin/tenants/{id}/files/{fid}/download     (Studio manager download)
//
// These routes are routed AROUND the response-compression wrapper
// (FILES-BENCH finding: chi Compress's writer lacks io.ReaderFrom, which
// suppressed sendfile zero-copy on the shipped path — and compressing binary
// blobs was counterproductive anyway). The JSON file routes (upload result,
// /url, the manager listing) deliberately do NOT match — they stay
// compressible like the rest of the API.
func IsByteServingPath(method, path string) bool {
	if method != http.MethodGet {
		return false
	}
	switch {
	case strings.HasPrefix(path, SignedPathPrefix+"/"):
		return true
	case strings.HasPrefix(path, "/api/files/"):
		// /api/files/{id} serves bytes; /api/files/{id}/url is JSON.
		return !strings.HasSuffix(path, "/url")
	case strings.HasPrefix(path, "/admin/tenants/") && strings.HasSuffix(path, "/download"):
		return true
	default:
		return false
	}
}

// UploadHandler streams a multipart upload to the store and returns the file
// handle. It runs AFTER the engine middleware chain (tenant → JWT → RBAC for
// the "files" resource), so it re-implements none of that — it only reads the
// resolved tenant and streams. The body is read with r.MultipartReader (no
// in-memory form parse); the OWASP validation (extension allowlist + magic
// bytes + size cap + name sanitization) happens inside Store.Put, and a
// rejected upload surfaces as 422 with the reason.
func UploadHandler(store *Store, maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		ProcessUpload(store, maxBytes, tc.ID, w, r)
	}
}

// ProcessUpload is the shared multipart-upload core: it streams the "file"
// part of r into the store for tenantID and writes the HTTP result (201 with
// the handle; 413/422/400 on the engine's real rejections). UploadHandler
// (tenant from Host) and the platform-admin manager route (tenant from the
// authenticated admin path) both delegate here, so EVERY ingestion surface
// pays the identical OWASP validation and returns the identical errors.
func ProcessUpload(store *Store, maxBytes int64, tenantID string, w http.ResponseWriter, r *http.Request) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	// Cap the whole request body; an overshoot surfaces as a read error inside
	// Store.Put (cleaning the partial temp) which we map to 413 below.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		writeErr(w, http.StatusBadRequest, "expected multipart/form-data")
		return
	}
	// One "file" part, exactly. Other field names are tolerated (a browser form
	// legitimately carries extra fields — the same reasoning as ADR-024's
	// unknown-top-level-query-param exception, written down in the ADR). What is
	// NOT tolerated is a SECOND "file" part (NIGHT-SWEEP-S1): the handler used
	// to store the first and answer 201 without ever reading the second — DATA
	// LOSS behind a success status, the worst shape of the class. The scan now
	// continues after the first store; a duplicate rolls the stored file back
	// (metadata row removed; the content-addressed blob only if unreferenced)
	// and names the problem.
	var stored *Meta
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if isTooLarge(err) {
				writeErr(w, http.StatusRequestEntityTooLarge, "upload too large")
				return
			}
			writeErr(w, http.StatusBadRequest, "malformed multipart body")
			return
		}
		if part.FormName() != "file" {
			_ = part.Close()
			continue
		}
		if stored != nil {
			_ = part.Close()
			if derr := store.Delete(r.Context(), tenantID, stored.ID); derr != nil {
				log.Printf("files: upload rollback of %s failed: %v", stored.ID, derr)
			}
			writeErr(w, http.StatusBadRequest, `multipart body carries more than one "file" part: send exactly one file per upload (the second used to be silently discarded behind a 201)`)
			return
		}

		meta, perr := store.Put(r.Context(), tenantID, part, PutMeta{
			ContentType:  part.Header.Get("Content-Type"),
			OriginalName: part.FileName(),
		})
		_ = part.Close()
		if perr != nil {
			switch {
			case isTooLarge(perr):
				writeErr(w, http.StatusRequestEntityTooLarge, "upload too large")
			case errors.Is(perr, ErrUploadRejected):
				writeErr(w, http.StatusUnprocessableEntity, perr.Error())
			default:
				writeErr(w, http.StatusInternalServerError, "upload failed")
			}
			return
		}
		stored = &meta
	}
	if stored == nil {
		writeErr(w, http.StatusBadRequest, `no "file" part in multipart body`)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"file_id": stored.ID,
		"sha256":  stored.SHA256,
		"size":    stored.Size,
	})
}

// DownloadHandler serves a stored file through the backend's strategy: the
// local driver proxies with http.ServeContent (Range/206, strong ETag/304,
// sendfile zero-copy); the S3 driver 302-redirects to a short-lived presigned
// URL (default) or proxies. It is deliberately served OUTSIDE the response
// cache (the cache middleware bypasses /api/files/… GETs) — a binary blob is
// unbounded and must never be buffered or cached in RAM.
func DownloadHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := uuid.Parse(id); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id format")
			return
		}
		serveFile(w, r, store, tc.ID, id)
	}
}

// DeleteHandler removes a stored file (metadata row + blob when unreferenced).
// RBAC has already required the `delete` action on the "files" resource.
func DeleteHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := uuid.Parse(id); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id format")
			return
		}
		if err := store.Delete(r.Context(), tc.ID, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			// A file attached to a record through a `file` field with the default
			// RESTRICT (FILES-LINK-S1): the FK blocks the delete — a clean 409
			// naming the referencing resource, never a masked 500. Detach first
			// (null the field / delete the record), or declare on_delete set_null.
			if fkMsg, ok := db.ForeignKeyViolation(err); ok {
				writeErr(w, http.StatusConflict, fkMsg)
				return
			}
			writeErr(w, http.StatusInternalServerError, "delete failed")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// SignedURLHandler mints a short-lived signed download URL for a file the
// caller may read (tenant + JWT + RBAC `read files` already enforced by the
// chain). S3 backends answer with a NATIVE presigned URL (Signature v4, the
// bucket serves the bytes); the local backend answers with an engine-token URL
// (/files/signed/{token}, HMAC-signed, validated + RBAC-rechecked at serve).
// Response: {"url","expires_in"}.
func SignedURLHandler(store *Store, secret []byte, ttl time.Duration) http.HandlerFunc {
	if ttl <= 0 {
		ttl = DefaultSignedURLTTL
	}
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		id := chi.URLParam(r, "id")
		if _, err := uuid.Parse(id); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id format")
			return
		}

		u, err := store.SignedURL(r.Context(), tc.ID, id, ttl)
		if errors.Is(err, ErrSignedURLUnsupported) {
			// Local backend: mint the engine's own capability token. The role is
			// embedded and re-checked at serve time.
			role := roleFromRequest(r)
			tok, terr := MintDownloadToken(secret, tc.ID, id, role, ttl)
			if terr != nil {
				writeErr(w, http.StatusInternalServerError, "signing failed")
				return
			}
			// Confirm the file exists BEFORE handing out a URL (same 404 the
			// download would give).
			if _, serr := store.Stat(r.Context(), tc.ID, id); serr != nil {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			// M2 (FIELD-FEEDBACK-S1): a RELATIVE URL. The engine serves the
			// signed path itself, so relative works in an <img src> on any
			// host and any port — the absolute form was rebuilt from the Host
			// header and came out portless behind dev proxies, unusable as
			// returned. (S3 URLs stay absolute by necessity: another origin.)
			u, err = SignedPathPrefix+"/"+tok, nil
		}
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeErr(w, http.StatusNotFound, "not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "signing failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"url":        u,
			"expires_in": int(ttl.Seconds()),
		})
	}
}

// SignedServeHandler serves GET /files/signed/{token}: verifies the HMAC
// token (signature, expiry, claim shape), that the token's tenant matches the
// request's tenant (a token minted for tenant A is useless on tenant B's
// host), and that the embedded role STILL may read files (a revoked grant does
// not outlive the policy) — then streams through the same backend Serve as the
// authenticated download. EVERY failure is a uniform 404 (never 403): an
// invalid token must be indistinguishable from a missing file
// (anti-fingerprinting, OWASP).
func SignedServeHandler(store *Store, secret []byte, allowRead func(role string) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		tok := chi.URLParam(r, "token")
		tokTenant, fileID, role, err := VerifyDownloadToken(secret, tok)
		if err != nil || tokTenant != tc.ID || !allowRead(role) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		serveFile(w, r, store, tc.ID, fileID)
	}
}

// ServeStored is the shared serve tail: the backend Serve strategy (local
// ServeContent proxy / S3 presigned 302) with the 404/500 mapping. Exported so
// the platform-admin manager route serves through the IDENTICAL path as the
// tenant download routes.
func ServeStored(store *Store, tenantID, id string, w http.ResponseWriter, r *http.Request) {
	if err := store.Serve(w, r, tenantID, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "download failed")
	}
}

func serveFile(w http.ResponseWriter, r *http.Request, store *Store, tenantID, id string) {
	ServeStored(store, tenantID, id, w, r)
}

// roleFromRequest reads the caller's role the same way the RBAC middleware
// does (JWT claims first, X-User-Role fallback for tests).
func roleFromRequest(r *http.Request) string {
	if claims := auth.ClaimsFromCtx(r.Context()); claims != nil {
		return claims.Role
	}
	return r.Header.Get("X-User-Role")
}

// safeFilename reduces a stored original_name to a header-safe basename: no path
// separators (defence in depth — it is never a path), no quotes/CR/LF that could
// break out of the Content-Disposition value or inject a header.
func safeFilename(name string) string {
	if name == "" {
		return "download"
	}
	// Keep only the last path component, then strip dangerous characters.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '"', '\\', '\r', '\n':
			return -1
		}
		if r < 0x20 {
			return -1
		}
		return r
	}, name)
	if name == "" {
		return "download"
	}
	return name
}

func isTooLarge(err error) bool {
	if err == nil {
		return false
	}
	// http.MaxBytesReader returns *http.MaxBytesError (Go 1.19+) and historically a
	// plain "request body too large" string; match both.
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe) || strings.Contains(err.Error(), "request body too large")
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
