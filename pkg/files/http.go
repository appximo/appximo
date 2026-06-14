package files

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/miguelangel/appitools/pkg/tenant"
)

// DefaultMaxUploadBytes caps a single upload body. It is a disk-exhaustion guard,
// not a memory one (uploads stream to disk in 64 KiB chunks), so it is generous;
// override via the engine's APPITOOLS_FILES_MAX_BYTES.
const DefaultMaxUploadBytes int64 = 256 << 20 // 256 MiB

// UploadHandler streams a multipart upload to the VFS and returns the file handle.
// It runs AFTER the engine middleware chain (tenant → JWT → RBAC for the "files"
// resource), so it re-implements none of that — it only reads the resolved tenant
// and streams. The body is read with r.MultipartReader (no in-memory form parse),
// the file part is piped to VFS.Put in 64 KiB chunks, and an oversize body is
// rejected 413 via http.MaxBytesReader (which also frees the partial temp through
// Put's interrupted-upload cleanup).
func UploadHandler(vfs VFS, maxBytes int64) http.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxUploadBytes
	}
	return func(w http.ResponseWriter, r *http.Request) {
		tc := tenant.FromCtx(r.Context())
		if tc == nil {
			writeErr(w, http.StatusBadRequest, "invalid tenant")
			return
		}
		// Cap the whole request body; an overshoot surfaces as a read error inside
		// VFS.Put (cleaning the partial temp) which we map to 413 below.
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

		mr, err := r.MultipartReader()
		if err != nil {
			writeErr(w, http.StatusBadRequest, "expected multipart/form-data")
			return
		}
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

			meta, perr := vfs.Put(r.Context(), tc.ID, part, PutMeta{
				ContentType:  part.Header.Get("Content-Type"),
				OriginalName: part.FileName(),
			})
			_ = part.Close()
			if perr != nil {
				if isTooLarge(perr) {
					writeErr(w, http.StatusRequestEntityTooLarge, "upload too large")
					return
				}
				writeErr(w, http.StatusInternalServerError, "upload failed")
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{
				"file_id": meta.ID,
				"sha256":  meta.SHA256,
				"size":    meta.Size,
			})
			return
		}
		writeErr(w, http.StatusBadRequest, `no "file" part in multipart body`)
	}
}

// DownloadHandler streams a stored file back to the client. It is deliberately
// served OUTSIDE the response cache (the cache middleware bypasses /api/files/…
// GETs) — a binary blob is unbounded and must never be buffered or cached in RAM.
func DownloadHandler(vfs VFS) http.HandlerFunc {
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
		rc, m, err := vfs.Get(r.Context(), tc.ID, id)
		if errors.Is(err, ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "download failed")
			return
		}
		defer rc.Close() //nolint:errcheck

		ct := m.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if m.Size > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(m.Size, 10))
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\""+safeFilename(m.OriginalName)+"\"")
		w.WriteHeader(http.StatusOK)
		_, _ = io.CopyBuffer(w, rc, make([]byte, copyBufSize))
	}
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
