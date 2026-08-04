package appximo

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// StaticMount serves a static file tree from the binary (LOOSE-ENDS-SWEEP-S1) —
// the seam that makes "one binary = backend + frontend + admin + docs" real.
//
// Before this, a custom route (appximo.Route) had to live under /api/ AND ran
// inside a per-request tenant TRANSACTION, which is exactly wrong for an asset:
// a .js file needs no database. A StaticMount is therefore NOT a Route — it is
// mounted like the engine's own embedded UIs (/editor, /admin), outside /api/,
// on the static path: no transaction, no RBAC evaluation, no response-cache
// buffering.
//
//	//go:embed all:web/dist
//	var frontend embed.FS
//
//	sub, _ := fs.Sub(frontend, "web/dist")
//	app, err := appximo.New(appximo.Config{
//		SchemaPath: "schema.json",
//		Static: []appximo.StaticMount{{Path: "/", FS: sub, SPA: true}},
//	})
//
// ⚠ PCI / SAQ A (payments): if the app takes card payments through a hosted
// widget or an iframe (Stripe Elements, Wompi, Mercado Pago), the CHECKOUT page
// must stay free of third-party scripts — analytics, chat widgets, tag managers.
// A single extra script on that page moves the merchant from SAQ A to SAQ A-EP,
// a materially heavier compliance burden, because that script could read the
// cardholder data entry surface. Serve the checkout route from a bundle whose
// third-party dependencies you control, and keep the marketing tags on the pages
// that do not touch payment.
type StaticMount struct {
	// Path is the URL prefix this tree is served at: "/" for the whole site, or
	// a sub-path like "/app". It must not be under, or collide with, any prefix
	// the engine owns (/api, /auth, /admin, /editor, /docs, /graphql, /graphiql,
	// /openapi, /metrics, /debug, /healthz, /readyz, /health, /files, /fleet) —
	// a collision is a BOOT error, never a silently shadowed route.
	Path string

	// FS is the file tree. An embed.FS sub-tree (fs.Sub) compiles the frontend
	// INTO the binary; os.DirFS(dir) serves a directory from disk. Either way the
	// handler can only ever read inside this FS: paths are cleaned and io/fs
	// itself rejects anything that escapes the root, so traversal is impossible
	// by construction rather than by filtering.
	FS fs.FS

	// SPA opts into client-side-routing fallback: a request that matches no file
	// serves Index instead of 404, so /orders/42 reaches the router in the
	// browser. It is OPT-IN because it is wrong for a plain static site, where a
	// typo should 404. Requests under a prefix the engine owns are NEVER given
	// the fallback — an unknown /api/… path stays a real 404.
	SPA bool

	// Index is the document served for the mount root and (when SPA) for
	// unmatched client routes. Empty means "index.html".
	Index string

	// ImmutablePrefixes are path prefixes, RELATIVE to this mount, whose files
	// carry content-hashed names and may be cached forever
	// (Cache-Control: immutable). Empty means the Vite/webpack default
	// ["assets/", "_app/", "static/"]. Everything else gets a short max-age, and
	// the index document is ALWAYS no-cache — it names the current hashed
	// bundles, so a stale copy would point at files a deploy already deleted.
	ImmutablePrefixes []string

	// CSP is this mount's Content-Security-Policy (LIBRARY-GAPS-S2, ENG-5).
	//
	// The static handler OWNS the header on everything it serves, for BOTH
	// mount forms. Before this, the two forms diverged invisibly: a root mount
	// is chi's NotFound handler, which chi copies into the API subrouter that
	// lives inside the StrictCSP group — so the SPA shell shipped the API's
	// `default-src 'none'` and every browser blocked the app's own scripts
	// (a blank page curl can never see, because curl does not enforce CSP);
	// a sub-path mount bypassed the group and shipped NO policy at all.
	//
	//   ""  (default) → DefaultStaticCSP: a same-origin SPA policy (the shape
	//        the engine's own embedded UIs use — external self scripts, inline
	//        styles allowed for component libraries, no framing).
	//   any other string → emitted verbatim, replacing the default (e.g. add
	//        img-src for a CDN).
	//   CSPOff ("off") → NO Content-Security-Policy header at all: the handler
	//        DELETES any policy inherited from the chain, so opting out is the
	//        same on a root and a sub-path mount. For apps that set their own
	//        policy via <meta http-equiv>.
	CSP string
}

// DefaultStaticCSP is the policy a StaticMount serves when CSP is unset:
// same-origin everything, no framing, no external script/connect targets.
//
// script-src carries 'unsafe-inline' DELIBERATELY: SvelteKit's adapter-static
// shell boots hydration from an INLINE <script> (so do Next export and Astro
// islands), and the first strict draft of this default (script-src 'self',
// copied from the embedded admin UI, whose Vite build emits only external
// modules) blanked a SvelteKit app exactly the way the original ENG-5 bug did
// — caught by the commerce browser suite, invisible to curl. A default must
// run the mainstream bundlers' output; an app whose bundle has no inline
// scripts should tighten per mount:
//
//	StaticMount{CSP: strings.Replace(appximo.DefaultStaticCSP, "script-src 'self' 'unsafe-inline'", "script-src 'self'", 1)}
//
// The load-bearing protections remain: no external script sources, no
// external connect targets (exfil), no framing, no foreign form action.
const DefaultStaticCSP = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"script-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// CSPOff is the StaticMount.CSP sentinel that disables the header entirely.
const CSPOff = "off"

// reservedStaticPrefixes are the URL prefixes the engine itself serves. A
// StaticMount may not take one — the boot check below rejects it with the list,
// so a frontend can never silently shadow the API, the admin panel or a health
// probe (and an operator never debugs "why does /admin show my SPA?").
var reservedStaticPrefixes = []string{
	"/api", "/auth", "/admin", "/editor", "/docs", "/graphql", "/graphiql",
	"/openapi", "/metrics", "/debug", "/healthz", "/readyz", "/health",
	"/files", "/fleet",
}

// defaultImmutablePrefixes covers what the common bundlers emit with hashed
// filenames: Vite/webpack ("assets/"), SvelteKit ("_app/"), CRA ("static/").
var defaultImmutablePrefixes = []string{"assets/", "_app/", "static/"}

// staticHandler is one compiled mount: the FS, the index bytes read once at
// boot, and the resolved cache policy.
type staticHandler struct {
	mount    StaticMount
	prefix   string // normalized, no trailing slash ("" for root)
	index    []byte // nil for an assets-only mount (no index, SPA off)
	indexTag string
	immutabl []string
	csp      string // resolved policy; "" means emit nothing (CSPOff)
}

// setCSP stamps the mount's policy on a response about to be served. It runs on
// EVERY static response (documents are where CSP matters; on assets it is inert
// but keeps the surface uniform), and it OVERRIDES whatever the middleware chain
// left — that inherited header is exactly the bug this fixes. CSPOff deletes it
// so "no policy" is achievable on a root mount too.
func (h *staticHandler) setCSP(w http.ResponseWriter) {
	if h.csp == "" {
		w.Header().Del("Content-Security-Policy")
		return
	}
	w.Header().Set("Content-Security-Policy", h.csp)
}

// validateStaticMounts checks every declared mount at BOOT and compiles it.
// Everything that could be a silent runtime surprise — a bad path, a missing
// index, a collision with an engine prefix or with another mount — is an error
// here, before the listener opens.
func validateStaticMounts(mounts []StaticMount) ([]*staticHandler, error) {
	if len(mounts) == 0 {
		return nil, nil
	}
	out := make([]*staticHandler, 0, len(mounts))
	seen := make(map[string]bool, len(mounts))

	for i, m := range mounts {
		where := fmt.Sprintf("Config.Static[%d]", i)
		if m.FS == nil {
			return nil, fmt.Errorf("appximo: %s: FS is required (an embed.FS sub-tree or os.DirFS)", where)
		}
		if m.Path == "" || !strings.HasPrefix(m.Path, "/") {
			return nil, fmt.Errorf("appximo: %s: Path must start with \"/\" (got %q); use \"/\" to serve the whole site", where, m.Path)
		}
		// Normalize: "/app/" and "/app" are the same mount; "/" becomes "".
		prefix := strings.TrimSuffix(m.Path, "/")
		if strings.Contains(prefix, "*") || strings.Contains(prefix, "{") {
			return nil, fmt.Errorf("appximo: %s: Path must be a literal prefix, not a pattern (got %q)", where, m.Path)
		}
		if seen[prefix] {
			return nil, fmt.Errorf("appximo: %s: Path %q is mounted twice", where, m.Path)
		}
		seen[prefix] = true

		// Collision with an engine-owned prefix — the SAME test the request path
		// uses, so "what the boot rejects" and "what the root mount refuses to
		// serve" can never drift apart. "/apixyz" is fine; "/api", "/api/ui" and
		// "/openapi.json" are not.
		if prefix != "" && isEngineOwnedPath(prefix) {
			return nil, fmt.Errorf(
				"appximo: %s: Path %q collides with the engine's own routes — the reserved prefixes are: %s",
				where, m.Path, strings.Join(reservedStaticPrefixes, " "))
		}

		idxName := m.Index
		if idxName == "" {
			idxName = "index.html"
		}
		// An assets-only mount (SPA off) may have no index document — its root
		// simply 404s (1B-2: a tree of hashed bundles has nothing to index).
		// The SPA fallback NEEDS the index (it IS the fallback), so there a
		// missing one stays a boot error.
		idx, err := fs.ReadFile(m.FS, idxName)
		if err != nil {
			if m.SPA {
				return nil, fmt.Errorf("appximo: %s: cannot read %q from the mounted FS: %w "+
					"(did the frontend build run before `go build`? An SPA mount needs its index — it is the fallback document)", where, idxName, err)
			}
			idx = nil
		}

		imm := m.ImmutablePrefixes
		if len(imm) == 0 {
			imm = defaultImmutablePrefixes
		}
		csp := m.CSP
		switch csp {
		case "":
			// SEC-2: the DEFAULT policy is hardened per mount by hashing the
			// shell's inline scripts, so `'unsafe-inline'` is only kept when the
			// document genuinely cannot be covered — and then the reason is
			// logged, never assumed.
			var note string
			csp, note = hardenedStaticCSP(DefaultStaticCSP, idx)
			if note != "" {
				log.Printf("static mount %q: %s", prefixLabel(prefix), note)
			}
		case CSPOff:
			csp = ""
		}
		m.Index = idxName
		out = append(out, &staticHandler{
			mount: m, prefix: prefix, index: idx,
			indexTag: staticETag(idx), immutabl: imm, csp: csp,
		})
	}
	// Longest prefix first so "/app/admin-ui" is matched before "/app".
	sort.Slice(out, func(i, j int) bool { return len(out[i].prefix) > len(out[j].prefix) })
	return out, nil
}

// staticETag derives a weak ETag from the content length and a cheap FNV hash —
// enough for the index document, whose bytes are fixed for the process lifetime.
func staticETag(b []byte) string {
	var h uint64 = 14695981039346656037
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf(`W/"%x-%x"`, len(b), h)
}

// staticMatcher reports which request paths belong to a static mount — used by
// the JWT skip and the response-cache bypass. nil when nothing is mounted, so
// both middlewares pay a single nil check.
//
// It must be a PREDICATE, not a prefix list. The middleware chain runs BEFORE
// chi routes the request, so it only sees the raw path: a ROOT mount cannot be
// expressed as the prefix "/" (that would skip authentication for /api too).
// The honest rule is "a path this mount would serve", which for a root mount is
// "anything the engine does not own" — exactly the same test serveRoot applies,
// so the skip can never cover a path the static handler would refuse.
//
// This was a live-test finding: with a prefix list, a root-mounted SPA answered
// 401 for its own index.html, because a frontend loads before any token exists.
func staticMatcher(hs []*staticHandler) func(path string) bool {
	if len(hs) == 0 {
		return nil
	}
	return func(p string) bool {
		for _, h := range hs {
			if h.prefix == "" {
				// Root mount: every path the engine does not claim.
				if !isEngineOwnedPath(p) {
					return true
				}
				continue
			}
			if p == h.prefix || strings.HasPrefix(p, h.prefix+"/") {
				return true
			}
		}
		return false
	}
}

// registerStatic mounts every compiled tree on r. Sub-path mounts become normal
// chi routes; a ROOT mount is installed as the mux's NotFound handler, because
// the generated API router is already mounted at "/" and chi allows only one
// handler per pattern. chi propagates a parent's NotFound into a subrouter at
// Mount time, so registering it BEFORE the generated router is mounted is what
// makes a path neither the engine nor the API claims fall through to the SPA.
func registerStatic(r chi.Router, hs []*staticHandler) {
	for _, h := range hs {
		if h.prefix == "" {
			r.NotFound(h.serveRoot)
			continue
		}
		r.Get(h.prefix, h.serveIndex)
		r.Get(h.prefix+"/", h.serveIndex)
		r.Get(h.prefix+"/*", h.serveFile)
	}
}

// serveRoot is the root-mount entry point (the mux NotFound handler). It refuses
// to answer for anything under an engine-owned prefix: an unknown /api/… path
// must stay a real 404, never the SPA shell — otherwise a typo'd endpoint returns
// 200 HTML and a client debugs the wrong layer. Non-GET falls through too: a POST
// to a nonexistent route is a 404/405, not a page.
func (h *staticHandler) serveRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.NotFound(w, r)
		return
	}
	if isEngineOwnedPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	h.serve(w, r, strings.TrimPrefix(r.URL.Path, "/"))
}

// isEngineOwnedPath reports whether p belongs to a surface the engine serves.
// Used in TWO places that must agree: the boot collision check (a mount may not
// take an engine prefix) and the root mount's request path (the SPA fallback must
// never mask an API 404, and — the security half — the JWT skip must never cover
// an engine surface).
//
// A prefix owns a path when the path IS it, is under it ("/api/tasks"), or is it
// with an extension ("/openapi.json", "/openapi.yaml" — the engine serves those
// two at the root). Matching on the first segment alone missed the extension
// case: a test caught /openapi.json being classified as static, which would have
// skipped authentication for it. A merely similar name ("/apixyz") is NOT owned.
func isEngineOwnedPath(p string) bool {
	for _, res := range reservedStaticPrefixes {
		if p == res || strings.HasPrefix(p, res+"/") || strings.HasPrefix(p, res+".") {
			return true
		}
	}
	return false
}

func (h *staticHandler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if h.index == nil {
		// Assets-only mount: there is no document to serve at the root.
		http.NotFound(w, r)
		return
	}
	h.setCSP(w)
	// The index names the current hashed bundles: a cached copy would point at
	// files the next deploy deletes. Always revalidate.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("ETag", h.indexTag)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, h.mount.Index, time.Time{}, bytes.NewReader(h.index))
}

func (h *staticHandler) serveFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, h.prefix+"/")
	h.serve(w, r, rel)
}

// serve resolves rel INSIDE the mount's FS and writes it. Path traversal is
// impossible twice over: path.Clean collapses any "..", the result is rejected if
// it still escapes, and io/fs.Open itself refuses a path that is not slash-clean
// and rooted. There is no os.Open of a caller-controlled string anywhere here.
func (h *staticHandler) serve(w http.ResponseWriter, r *http.Request, rel string) {
	rel = path.Clean("/" + rel)[1:] // absolute-clean, then drop the leading "/"
	if rel == "" || rel == "." {
		h.serveIndex(w, r)
		return
	}
	if !fs.ValidPath(rel) {
		http.NotFound(w, r)
		return
	}

	f, err := h.mount.FS.Open(rel)
	if err != nil {
		h.miss(w, r, rel)
		return
	}
	defer f.Close() //nolint:errcheck
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if st.IsDir() {
		// A directory request serves its index if there is one, else the SPA
		// fallback / 404 — never a directory listing (which would leak the tree).
		if idx, ierr := fs.ReadFile(h.mount.FS, path.Join(rel, h.mount.Index)); ierr == nil {
			h.setCSP(w)
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, h.mount.Index, time.Time{}, bytes.NewReader(idx))
			return
		}
		h.miss(w, r, rel)
		return
	}

	h.setCSP(w)
	if ct := staticMIME[path.Ext(rel)]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", h.cacheControl(rel))
	if rs, ok := f.(io.ReadSeeker); ok {
		// ServeContent gives Range + If-Modified-Since for free.
		http.ServeContent(w, r, rel, st.ModTime(), rs)
		return
	}
	_, _ = io.Copy(w, f)
}

// miss handles "no such file": the SPA fallback when opted in AND the request
// looks like a client route, else a plain 404. A path WITH an extension is never
// given the fallback — a missing .js must 404 loudly, not return HTML that the
// browser then fails to parse (the classic "Unexpected token <" debugging hour).
func (h *staticHandler) miss(w http.ResponseWriter, r *http.Request, rel string) {
	if h.mount.SPA && path.Ext(rel) == "" {
		h.serveIndex(w, r)
		return
	}
	http.NotFound(w, r)
}

// cacheControl picks the caching policy for one file: content-hashed bundles are
// immutable for a year, everything else revalidates hourly.
func (h *staticHandler) cacheControl(rel string) string {
	for _, p := range h.immutabl {
		if strings.HasPrefix(rel, p) {
			return "public, max-age=31536000, immutable"
		}
	}
	return "public, max-age=3600"
}

// staticMIME maps the extensions a frontend build emits to a content type,
// independent of the host's mime database (which varies between distros and is
// missing entirely in a scratch container).
var staticMIME = map[string]string{
	".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8",
	".mjs": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".json": "application/json", ".map": "application/json",
	".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg",
	".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".ico": "image/x-icon",
	".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf",
	".txt": "text/plain; charset=utf-8", ".xml": "application/xml",
	".webmanifest": "application/manifest+json", ".wasm": "application/wasm",
	".mp4": "video/mp4", ".webm": "video/webm", ".pdf": "application/pdf",
}
