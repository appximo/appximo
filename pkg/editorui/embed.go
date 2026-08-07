// Package editorui embeds the visual schema editor (Appximo Studio, UI-F0-S1)
// and serves it from the engine binary under /editor. The compiled SPA lives in
// web/build and is COMMITTED (ADR-025): the published module carries the
// prebuilt assets, so any `go build` — including a module consumer's — ships a
// working Studio. After touching web/src, rebuild with `make editor-ui` and
// commit the new assets with the src change. A binary that embeds no assets
// serves an honest 503 from the shell routes instead of a blank page (B1).
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

// noAssetsHTML answers the shell routes (503) when the binary embeds only the
// placeholder index.html. Serving the real shell in that state renders a blank
// page whose bundle 404s — a broken 200 nobody can diagnose from the browser
// (field report B1; ADR-025). The page names what is missing and the exact fix.
const noAssetsHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Appximo Studio unavailable</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:42rem;margin:4rem auto;padding:0 1rem;color:#1f2937}code{background:#f3f4f6;padding:.1em .35em;border-radius:4px}</style></head><body>
<h1>503 &mdash; Studio assets are not in this binary</h1>
<p>This build embeds only the placeholder shell: the compiled editor bundle
(<code>pkg/editorui/web/build/assets/</code>) was missing when <code>go build</code> ran.</p>
<p>Since ADR-025 the published module carries the prebuilt assets, so a normal
<code>go build</code> (or <code>go get github.com/appximo/appximo</code>) includes them.
Seeing this page means the binary was built from an engine tree without them &mdash;
run <code>make editor-ui</code> in the engine repo and recompile, or update the module.</p>
</body></html>`

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
	// JSON-EDITOR-S1: the assisted editor's validation surface (validate.go).
	// Literal routes win over the wildcard in chi, so these never shadow assets.
	r.Post("/editor/validate", serveValidate)
	r.Get("/editor/meta-schema", serveMetaSchema)
	return nil
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !h.hasAssets {
		// Honest failure over a broken 200 (ADR-025): the shell would render
		// blank because the bundle it names is not embedded.
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(noAssetsHTML))
		return
	}
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
