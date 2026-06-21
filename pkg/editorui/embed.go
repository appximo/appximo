// Package editorui embeds the visual schema editor (Appitools Studio, UI-F0-S1)
// and serves it from the engine binary under /editor. The compiled SPA lives in
// web/build; following the admin-UI / DevHub pattern, web/build/index.html is
// committed (so go:embed always has something) while web/build/assets/ is
// gitignored and produced by `npm run build` (make editor-ui) before
// `go build`/release.
//
// The editor is a STATIC single-page Svelte 5 app (plain Vite, no SvelteKit) —
// there is NO Node process in production: the browser downloads HTML/JS/CSS and
// runs the editor entirely client-side. The engine only hands out the files.
//
// Real server paths:
//   - GET /editor and /editor/   → index.html (no-cache; always points at the
//     current hashed assets)
//   - GET /editor/assets/<hash>  → immutable (cached forever by the browser)
//   - GET /editor/<other>        → the matching build file (e.g. favicon.svg)
//
// These are NEW static routes, off the CRUD/JWT hot path (JWT-skipped via the
// "/editor" prefix in pkg/auth.skipJWT; tenant-agnostic like /admin).
package editorui

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// all: is REQUIRED: Vite emits hashed assets and Svelte chunks whose names may be
// skipped by the default go:embed (which ignores files matching `_*`/`.*`). `all:`
// embeds every file under web/build verbatim.
//
//go:embed all:web/build
var distFS embed.FS

// cspEditor scopes the editor to its own origin. style-src allows 'unsafe-inline'
// because Svelte and Svelte Flow inject <style> blocks and inline style attributes
// (node transforms); scripts are self-only (Vite emits external module scripts).
const cspEditor = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; connect-src 'self'; font-src 'self' data:; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// mimeByExt maps the extensions the bundle emits to a content type (independent of
// the host's mime database, which varies).
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

// newHandler builds the serving state from the embedded build. It tolerates a
// build dir that only contains the committed placeholder index.html (no assets):
// the shell route still works and asset routes 404 until `npm run build` runs.
func newHandler() (*handler, error) {
	sub, err := fs.Sub(distFS, "web/build")
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

// Register mounts the editor routes on r. The shell is public (JWT-skipped via the
// "/editor" prefix); the SPA runs client-side and talks to the engine only when a
// future "deploy" action is wired.
func Register(r chi.Router) error {
	h, err := newHandler()
	if err != nil {
		return err
	}
	r.Get("/editor", h.serveIndex)
	r.Get("/editor/", h.serveIndex)
	r.Get("/editor/*", h.serveAsset)
	return nil
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html must never be cached: it names the current hashed assets, so a
	// stale copy would point at deleted bundles after a deploy.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Security-Policy", cspEditor)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.indexHTML))
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/editor/")
	rel = path.Clean(rel)
	if rel == "." || rel == "" {
		h.serveIndex(w, r)
		return
	}
	// path.Clean leaves no leading "../"; reject anything that still tries to escape.
	if rel == ".." || strings.HasPrefix(rel, "../") {
		http.NotFound(w, r)
		return
	}
	f, err := h.sub.Open(rel)
	if err != nil {
		// A path with no extension is a client-side route → serve the SPA shell.
		// A missing real asset (has an extension) is a genuine 404.
		if path.Ext(rel) == "" {
			h.serveIndex(w, r)
			return
		}
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
	if strings.HasPrefix(rel, "assets/") {
		// Hashed filenames are content-addressed → cache forever.
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, rel, st.ModTime(), rs)
		return
	}
	_, _ = io.Copy(w, f)
}
