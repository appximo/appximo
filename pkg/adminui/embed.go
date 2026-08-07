// Package adminui embeds the SolidJS admin panel (ADMIN-UI-V1) and serves it from
// the engine binary under /admin. The compiled bundle lives in web/dist and is
// COMMITTED (ADR-025): the published module carries the prebuilt assets, so any
// `go build` — including a module consumer's — ships a working panel. After
// touching web/src, rebuild with `make admin-ui` and commit the new assets with
// the src change. A binary that embeds no assets serves an honest 503 from the
// shell routes instead of a blank page (field report B1).
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

// faviconSVG is the panel's icon, served WITHOUT auth at /favicon.ico (and
// /admin/favicon.svg) so the browser's root probe never gets a 401 — keeping the
// console clean (ADMIN-UI-V1.2). A tiny inline SVG: a rounded brand-blue tile.
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">` +
	`<rect width="32" height="32" rx="7" fill="#3b5bdb"/>` +
	`<rect x="8" y="8" width="16" height="4" rx="2" fill="#fff"/>` +
	`<rect x="8" y="14" width="16" height="4" rx="2" fill="#fff" opacity="0.75"/>` +
	`<rect x="8" y="20" width="10" height="4" rx="2" fill="#fff" opacity="0.5"/></svg>`

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

// noAssetsHTML answers the shell routes (503) when the binary embeds only the
// placeholder index.html. Serving the real shell in that state renders a blank
// page whose bundle 404s — a broken 200 nobody can diagnose from the browser
// (field report B1; ADR-025). The page names what is missing and the exact fix.
const noAssetsHTML = `<!doctype html><html><head><meta charset="utf-8"><title>admin panel unavailable</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:42rem;margin:4rem auto;padding:0 1rem;color:#1f2937}code{background:#f3f4f6;padding:.1em .35em;border-radius:4px}</style></head><body>
<h1>503 &mdash; admin panel assets are not in this binary</h1>
<p>This build embeds only the placeholder shell: the compiled admin panel bundle
(<code>pkg/adminui/web/dist/assets/</code>) was missing when <code>go build</code> ran.</p>
<p>Since ADR-025 the published module carries the prebuilt assets, so a normal
<code>go build</code> (or <code>go get github.com/appximo/appximo</code>) includes them.
Seeing this page means the binary was built from an engine tree without them &mdash;
run <code>make admin-ui</code> in the engine repo and recompile, or update the module.</p>
</body></html>`

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
	// Public favicon (no auth — /favicon.ico is JWT-skipped) so a browser root
	// probe never 401s. Also at /admin/favicon.svg for the index <link>.
	r.Get("/favicon.ico", serveFavicon)
	r.Get("/admin/favicon.svg", serveFavicon)
	return nil
}

func serveFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(faviconSVG))
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
