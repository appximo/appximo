// Package backofficeui embeds the generic back-office SPA and serves it from
// the engine binary under /app (ENG-38, the first-10-minutes path). It is the
// PRODUCT face of the backoffice-spec pattern: a CRUD admin UI generated
// ENTIRELY from /openapi.json at runtime — zero resource-specific screens,
// zero hardcoded domain knowledge — so ONE prebuilt bundle serves every schema
// this engine will ever boot. In the minute-3 of a first contact, /app is the
// screen that feels like *your* app: your resources, your fields, your RBAC.
//
// The SPA is no-build vanilla JS (ES modules), so unlike /admin and /editor
// there is no "assets not built" state: the source files ARE the bundle,
// committed and always embedded. It is the same pattern verified in
// examples/backoffice-guide/ (the teaching copy consumers adapt into their own
// SPA — that copy stays deliberately minimal and self-contained; this one may
// grow product polish). Everything either copy knows comes from the contract:
// x-appximo-relation/references, x-appximo-file(+policy),
// x-appximo-initial/transitions, x-appximo-virtual-resources.
//
// Serving model (mirrors pkg/adminui): /app redirects to /app/ so the SPA's
// RELATIVE asset references resolve under the mount; /app/ serves the shell;
// /app/<file> serves the module files. All static, JWT-skipped (the shell
// loads before any token exists — the API calls it makes carry the Bearer),
// off the CRUD hot path.
package backofficeui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"embed"

	"github.com/go-chi/chi/v5"
)

//go:embed web
var webFS embed.FS

// cspApp scopes the back-office to its own origin, like the admin panel: the
// SPA only ever talks to the engine that served it (connect-src 'self').
// img-src allows data:/blob: for future inline file previews.
const cspApp = "default-src 'self'; img-src 'self' data: blob:; style-src 'self'; " +
	"script-src 'self'; connect-src 'self'; font-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// mimeByExt pins content types for the files the SPA ships (avoids the host's
// mime database, which varies across systems).
var mimeByExt = map[string]string{
	".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".html": "text/html; charset=utf-8", ".svg": "image/svg+xml", ".json": "application/json",
}

type handler struct {
	sub       fs.FS
	indexHTML []byte
}

func newHandler() (*handler, error) {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	idx, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	return &handler{sub: sub, indexHTML: idx}, nil
}

// Register mounts the back-office routes on r: /app (redirect), /app/ (shell)
// and /app/<file> (the ES-module files). The /app prefix is JWT-skipped (like
// /admin and /editor) and reserved against user static mounts.
func Register(r chi.Router) error {
	h, err := newHandler()
	if err != nil {
		return err
	}
	// The redirect makes the SPA's relative references ("./app.js") resolve
	// under /app/ regardless of how the user typed the URL.
	r.Get("/app", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/app/", http.StatusMovedPermanently)
	})
	r.Get("/app/", h.serveIndex)
	r.Get("/app/*", h.serveFile)
	return nil
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The shell is tiny and names unhashed files — always revalidate so a
	// binary upgrade is picked up on the next load.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Content-Security-Policy", cspApp)
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(h.indexHTML))
}

func (h *handler) serveFile(w http.ResponseWriter, r *http.Request) {
	rel := path.Clean(strings.TrimPrefix(r.URL.Path, "/app/"))
	if rel == "." || rel == "index.html" || strings.Contains(rel, "..") {
		h.serveIndex(w, r)
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
	// Unhashed filenames: revalidate on each load (they are a few KB; a stale
	// app.js against a new contract reader would be a debugging trap).
	w.Header().Set("Cache-Control", "no-cache")
	if rs, ok := f.(interface {
		Read(p []byte) (int, error)
		Seek(offset int64, whence int) (int64, error)
	}); ok {
		http.ServeContent(w, r, rel, st.ModTime(), rs)
		return
	}
	http.NotFound(w, r)
}
