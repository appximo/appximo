package appximo

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"
)

// LOOSE-ENDS-SWEEP-S1 — the static mount: "one binary = backend + frontend".
//
// The security properties are the point of most of these tests: a static tree
// must never shadow the API or the engine's own UIs, must never escape its FS,
// and must never turn an unknown /api path into a 200 HTML page.

func spaFS() fs.FS {
	return fstest.MapFS{
		"index.html":            {Data: []byte("<!doctype html><div id=app>")},
		"assets/app-abc123.js":  {Data: []byte("console.log(1)")},
		"assets/app-abc123.css": {Data: []byte("body{}")},
		"favicon.ico":           {Data: []byte("icon")},
		"nested/page.html":      {Data: []byte("<p>nested")},
		"secret.txt":            {Data: []byte("in-tree, still servable")},
	}
}

func mustCompile(t *testing.T, mounts ...StaticMount) []*staticHandler {
	t.Helper()
	hs, err := validateStaticMounts(mounts)
	if err != nil {
		t.Fatalf("validateStaticMounts: %v", err)
	}
	return hs
}

// staticRouter mounts hs the way buildRouter does: static first, then a stand-in
// for the generated API router at "/", so the NotFound propagation is exercised
// exactly as in production.
func staticRouter(hs []*staticHandler) *chi.Mux {
	r := chi.NewMux()
	registerStatic(r, hs)
	api := chi.NewMux()
	api.Get("/api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	r.Mount("/", api)
	return r
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec
}

func TestStaticMount_RootServesTheSPA(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS(), SPA: true}))

	t.Run("the root serves index.html, never cached", func(t *testing.T) {
		rec := get(t, h, "/")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=app") {
			t.Fatalf("root: %d %q", rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("index.html must never be cached (it names the hashed bundles), got %q", cc)
		}
	})

	t.Run("hashed assets are immutable and typed", func(t *testing.T) {
		rec := get(t, h, "/assets/app-abc123.js")
		if rec.Code != 200 {
			t.Fatalf("asset: %d", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("a content-hashed asset must be immutable, got %q", cc)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Fatalf("wrong Content-Type: %q", ct)
		}
	})

	t.Run("a non-hashed file gets a short max-age, not immutable", func(t *testing.T) {
		rec := get(t, h, "/favicon.ico")
		cc := rec.Header().Get("Cache-Control")
		if rec.Code != 200 || strings.Contains(cc, "immutable") {
			t.Fatalf("favicon: %d %q", rec.Code, cc)
		}
	})

	t.Run("a client-side deep link falls back to index.html", func(t *testing.T) {
		rec := get(t, h, "/orders/42/detail")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=app") {
			t.Fatalf("deep link must serve the shell: %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("a MISSING asset 404s — it never returns HTML", func(t *testing.T) {
		// The classic "Unexpected token <" bug: an SPA fallback that answers a
		// missing .js with index.html.
		if rec := get(t, h, "/assets/gone-999.js"); rec.Code != 404 {
			t.Fatalf("a missing file WITH an extension must 404, got %d", rec.Code)
		}
	})
}

// The root mount must never mask the API's own 404s: a typo'd endpoint has to
// stay a 404, not become a 200 HTML page that sends the client debugging the
// wrong layer.
func TestStaticMount_RootNeverShadowsTheEngine(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS(), SPA: true}))

	if rec := get(t, h, "/api/tasks"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"data"`) {
		t.Fatalf("the real API route must still answer: %d %q", rec.Code, rec.Body.String())
	}
	for _, p := range []string{
		"/api/nope", "/api/tasks/extra", "/auth/login", "/admin/tenants",
		"/editor/x", "/docs", "/openapi.json", "/metrics", "/debug/traces",
		"/healthz", "/readyz", "/health", "/graphql", "/graphiql", "/files/signed/x", "/fleet",
	} {
		rec := get(t, h, p)
		if rec.Code == 200 && strings.Contains(rec.Body.String(), "id=app") {
			t.Errorf("%s served the SPA shell — the engine's own paths must never be shadowed", p)
		}
	}
	// A non-GET on an unknown path is not a page either.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/whatever", nil))
	if rec.Code == 200 {
		t.Fatalf("POST to an unknown path must not serve the SPA, got %d", rec.Code)
	}
}

func TestStaticMount_SubPath(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/ui", FS: spaFS(), SPA: true}))

	for _, p := range []string{"/ui", "/ui/"} {
		if rec := get(t, h, p); rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=app") {
			t.Fatalf("%s: %d", p, rec.Code)
		}
	}
	if rec := get(t, h, "/ui/assets/app-abc123.css"); rec.Code != 200 {
		t.Fatalf("sub-path asset: %d", rec.Code)
	}
	if rec := get(t, h, "/ui/deep/link"); rec.Code != 200 {
		t.Fatalf("sub-path SPA fallback: %d", rec.Code)
	}
	// Outside the mount, the API still owns the tree.
	if rec := get(t, h, "/api/tasks"); rec.Code != 200 {
		t.Fatalf("api under a sub-path mount: %d", rec.Code)
	}
	// And with NO root mount, an unknown path is a plain 404 (no fallback).
	if rec := get(t, h, "/somewhere-else"); rec.Code == 200 {
		t.Fatalf("a sub-path mount must not answer outside its prefix, got %d", rec.Code)
	}
}

// SPA is opt-in: without it a typo must 404, which is right for a static site.
func TestStaticMount_SPAIsOptIn(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS()}))
	if rec := get(t, h, "/orders/42"); rec.Code != 404 {
		t.Fatalf("without SPA a client route must 404, got %d", rec.Code)
	}
	if rec := get(t, h, "/"); rec.Code != 200 {
		t.Fatalf("the index is still served: %d", rec.Code)
	}
}

// Path traversal is impossible by construction: path.Clean collapses "..",
// fs.ValidPath rejects what is left, and io/fs never opens outside its root.
func TestStaticMount_PathTraversalBlocked(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/ui", FS: spaFS(), SPA: true}))
	for _, p := range []string{
		"/ui/../../../etc/passwd",
		"/ui/../../etc/passwd",
		"/ui/..%2f..%2fetc%2fpasswd",
		"/ui/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/ui/./../../secret",
		"/ui/nested/../../../../root/.ssh/id_rsa",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		body := rec.Body.String()
		if strings.Contains(body, "root:") || strings.Contains(body, "PRIVATE KEY") {
			t.Fatalf("%s ESCAPED THE FS: %q", p, body)
		}
		// It may 404 or serve the shell (a cleaned client route) — it must never
		// serve a file from outside the declared FS, which the check above proves.
		if rec.Code != 200 && rec.Code != 404 && rec.Code != 301 {
			t.Errorf("%s: unexpected status %d", p, rec.Code)
		}
	}
}

// A directory must never be listed — that would leak the tree.
func TestStaticMount_NoDirectoryListing(t *testing.T) {
	h := staticRouter(mustCompile(t, StaticMount{Path: "/", FS: spaFS(), SPA: true}))
	rec := get(t, h, "/assets/")
	if strings.Contains(rec.Body.String(), "app-abc123.js") {
		t.Fatalf("directory listing leaked the tree: %q", rec.Body.String())
	}
}

func TestStaticMount_BootValidation(t *testing.T) {
	cases := []struct {
		name  string
		mount StaticMount
		want  string
	}{
		{"no FS", StaticMount{Path: "/ui"}, "FS is required"},
		{"relative path", StaticMount{Path: "app", FS: spaFS()}, "must start with"},
		{"a pattern, not a prefix", StaticMount{Path: "/ui/*", FS: spaFS()}, "literal prefix"},
		{"collides with /api", StaticMount{Path: "/api", FS: spaFS()}, "collides with the engine"},
		{"collides under /api", StaticMount{Path: "/api/ui", FS: spaFS()}, "collides with the engine"},
		{"collides with /admin", StaticMount{Path: "/admin", FS: spaFS()}, "collides with the engine"},
		{"collides with /editor", StaticMount{Path: "/editor", FS: spaFS()}, "collides with the engine"},
		{"collides with /health", StaticMount{Path: "/health", FS: spaFS()}, "collides with the engine"},
		// 1B-2 (LIBRARY-GAPS-S2): a missing index is an error only when SPA is
		// on — the index IS the fallback document. An assets-only mount without
		// one is valid (covered in static_csp_test.go).
		{"no index in an SPA FS", StaticMount{Path: "/ui", SPA: true, FS: fstest.MapFS{"a.js": {Data: []byte("x")}}}, "cannot read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := validateStaticMounts([]StaticMount{c.mount})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected an error containing %q, got: %v", c.want, err)
			}
		})
	}

	t.Run("a prefix that merely starts like a reserved one is fine", func(t *testing.T) {
		if _, err := validateStaticMounts([]StaticMount{{Path: "/apixyz", FS: spaFS()}}); err != nil {
			t.Fatalf("/apixyz is not /api: %v", err)
		}
	})

	t.Run("the same path twice is rejected", func(t *testing.T) {
		_, err := validateStaticMounts([]StaticMount{
			{Path: "/ui", FS: spaFS()}, {Path: "/ui/", FS: spaFS()},
		})
		if err == nil || !strings.Contains(err.Error(), "mounted twice") {
			t.Fatalf("expected a duplicate-mount error, got: %v", err)
		}
	})

	t.Run("no mounts is a no-op", func(t *testing.T) {
		hs, err := validateStaticMounts(nil)
		if err != nil || hs != nil {
			t.Fatalf("nil mounts must compile to nothing: %v %v", hs, err)
		}
	})
}

// The predicate handed to the JWT skip and the cache bypass decides which paths
// a mount owns. It must cover the mount, and — critically — must NEVER cover a
// path the engine owns, or a static mount would silently disable authentication
// for the API. (A live test caught the first version of this: with a prefix list,
// a root-mounted SPA answered 401 for its own index.html.)
func TestStaticMatcher_CoversTheMountAndNeverTheEngine(t *testing.T) {
	t.Run("no mounts ⇒ nil (middlewares pay one nil check)", func(t *testing.T) {
		if staticMatcher(nil) != nil {
			t.Fatal("expected nil")
		}
	})

	t.Run("a sub-path mount owns only its own prefix", func(t *testing.T) {
		m := staticMatcher(mustCompile(t, StaticMount{Path: "/ui", FS: spaFS()}))
		for _, p := range []string{"/ui", "/ui/", "/ui/assets/x.js", "/ui/deep/link"} {
			if !m(p) {
				t.Errorf("%s should belong to the mount", p)
			}
		}
		for _, p := range []string{"/", "/api/tasks", "/appendix", "/other"} {
			if m(p) {
				t.Errorf("%s must NOT be treated as static", p)
			}
		}
	})

	t.Run("a root mount owns everything the engine does not", func(t *testing.T) {
		m := staticMatcher(mustCompile(t, StaticMount{Path: "/", FS: spaFS()}))
		for _, p := range []string{"/", "/index.html", "/assets/app.js", "/orders/42"} {
			if !m(p) {
				t.Errorf("%s should belong to the root mount", p)
			}
		}
		// The security property: authentication is never skipped for the API or
		// any engine surface.
		for _, p := range []string{
			"/api/tasks", "/api/nope", "/auth/login", "/admin/tenants", "/editor",
			"/docs", "/graphql", "/graphiql", "/openapi.json", "/metrics",
			"/debug/traces", "/healthz", "/readyz", "/health", "/files/signed/x", "/fleet",
		} {
			if m(p) {
				t.Errorf("SECURITY: %s was treated as static — the JWT skip would cover it", p)
			}
		}
	})
}

// Longest-prefix-first ordering: a nested mount must win over its parent.
func TestStaticMount_LongestPrefixWins(t *testing.T) {
	inner := fstest.MapFS{"index.html": {Data: []byte("INNER")}}
	hs := mustCompile(t,
		StaticMount{Path: "/ui", FS: spaFS(), SPA: true},
		StaticMount{Path: "/ui/admin", FS: inner, SPA: true},
	)
	if hs[0].prefix != "/ui/admin" {
		t.Fatalf("longest prefix must sort first, got %q", hs[0].prefix)
	}
	h := staticRouter(hs)
	if rec := get(t, h, "/ui/admin"); !strings.Contains(rec.Body.String(), "INNER") {
		t.Fatalf("the nested mount must win: %q", rec.Body.String())
	}
}

// TestParseStaticSpecs — PUBLIC-SURFACE-S1 Part A: the CLI/env grammar for
// Config.Static ("[urlpath=]dir"). A bare dir mounts at "/", "path=dir" at the
// sub-path, a missing directory is a loud error at parse (boot) time, and the
// result flows into the SAME validateStaticMounts as code-declared mounts.
func TestParseStaticSpecs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/index.html", []byte("<!doctype html><title>x</title>"), 0644); err != nil {
		t.Fatal(err)
	}

	mounts, err := ParseStaticSpecs([]string{dir}, true)
	if err != nil {
		t.Fatalf("bare dir: %v", err)
	}
	if len(mounts) != 1 || mounts[0].Path != "/" || !mounts[0].SPA || mounts[0].FS == nil {
		t.Fatalf("bare dir mount = %+v", mounts)
	}
	// It compiles through the one true validator (no second implementation).
	if _, err := validateStaticMounts(mounts); err != nil {
		t.Fatalf("parsed mount must validate: %v", err)
	}

	mounts, err = ParseStaticSpecs([]string{"/site=" + dir, "assets=" + dir}, false)
	if err != nil {
		t.Fatalf("sub-path specs: %v", err)
	}
	if mounts[0].Path != "/site" || mounts[1].Path != "/assets" {
		t.Fatalf("sub-path mounts = %+v", mounts)
	}

	if _, err := ParseStaticSpecs([]string{dir + "/definitely-missing"}, false); err == nil {
		t.Fatal("a missing directory must be a loud error")
	}
	if _, err := ParseStaticSpecs([]string{dir + "/index.html"}, false); err == nil {
		t.Fatal("a file (not a directory) must be a loud error")
	}
	if _, err := ParseStaticSpecs([]string{"/site="}, false); err == nil {
		t.Fatal("a spec with no directory must be a loud error")
	}

	// Empty entries (a trailing comma in APPXIMO_STATIC_DIR) are skipped, not errors.
	mounts, err = ParseStaticSpecs([]string{dir, ""}, false)
	if err != nil || len(mounts) != 1 {
		t.Fatalf("empty entries must be skipped: %+v err=%v", mounts, err)
	}
}
