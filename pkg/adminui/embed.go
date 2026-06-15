// Package adminui embeds the SolidJS admin panel (ADMIN-UI-V1) and serves it from
// the engine binary under /admin. The compiled bundle lives in web/dist; following
// the DevHub's pattern, web/dist/index.html is committed (so go:embed always has
// something) while web/dist/assets/ is gitignored and produced by `npm run build`
// before `go build`/release (see Makefile target `admin-ui`).
//
// The SPA uses HASH routing (/admin#/…), so the only real server paths are:
//   - GET /admin and /admin/        → index.html (no-cache; always fresh so it
//     points at the latest hashed assets)
//   - GET /admin/assets/<hashed>    → immutable (cached forever by the browser)
//
// Client routes live in the URL fragment and never collide with the /admin/*
// ADMIN-API-V1 routes. These are NEW static routes, off the CRUD/JWT hot path.
package adminui

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"embed"

	"github.com/go-chi/chi/v5"
)

//go:embed web/dist
var distFS embed.FS

// cspAdmin scopes the panel to its own origin. style-src allows 'unsafe-inline'
// because component libraries inject style tags; scripts are self-only (Vite emits
// external module scripts), so no inline-script execution is permitted.
const cspAdmin = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// mimeByExt maps the handful of extensions the bundle emits to a content type
// (avoids depending on the host's mime database, which varies).
var mimeByExt = map[string]string{
	".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json",
	".woff2": "font/woff2", ".woff": "font/woff", ".ico": "image/x-icon", ".map": "application/json",
	".png": "image/png", ".webp": "image/webp",
}

type handler struct {
	sub       fs.FS
	indexHTML []byte
	hasAssets bool
}

// newHandler builds the serving state from the embedded dist. It is tolerant of a
// dist that only contains the committed placeholder index.html (no built assets):
// the shell route still works and asset routes 404 until `npm run build` runs.
func newHandler() (*handler, error) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		return nil, err
	}
	idx, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	hasAssets := false
	if entries, derr := fs.ReadDir(sub, "assets"); derr == nil && len(entries) > 0 {
		hasAssets = true
	}
	return &handler{sub: sub, indexHTML: idx, hasAssets: hasAssets}, nil
}

// HasBuiltAssets reports whether real (hashed) assets are embedded — false means
// only the placeholder index.html is present (the bundle was not built before
// `go build`). Used by the engine to log a helpful warning.
func HasBuiltAssets() bool {
	h, err := newHandler()
	return err == nil && h.hasAssets
}

// Register mounts the admin panel routes on r. The routes sit under /admin/* (which
// the engine already JWT-skips and RBAC-passes-through); the SPA shell is public,
// and every API call it makes carries the platform JWT.
func Register(r chi.Router) error {
	h, err := newHandler()
	if err != nil {
		return err
	}
	r.Get("/admin", h.serveIndex)
	r.Get("/admin/", h.serveIndex)
	r.Get("/admin/assets/*", h.serveAsset)
	return nil
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html must never be cached: it names the current hashed assets, so a
	// stale copy would point at deleted bundles after a deploy.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Security-Policy", cspAdmin)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.indexHTML))
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	// /admin/assets/index-xxxx.js → assets/index-xxxx.js inside the embed.
	rel := strings.TrimPrefix(r.URL.Path, "/admin/")
	rel = path.Clean(rel)
	if !strings.HasPrefix(rel, "assets/") {
		http.NotFound(w, r)
		return
	}
	f, err := h.sub.Open(rel)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close() //nolint:errcheck
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if ct := mimeByExt[path.Ext(rel)]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// Hashed filenames are content-addressed → cache forever.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, rel, st.ModTime(), rs)
		return
	}
	_, _ = io.Copy(w, f)
}
