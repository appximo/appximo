package backofficeui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func testRouter(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	if err := Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The bare /app redirects to /app/ so the SPA's relative module references
// ("./app.js" → /app/app.js) resolve regardless of how the URL was typed.
func TestAppRedirectsToSlash(t *testing.T) {
	rec := get(t, testRouter(t), "/app")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /app = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/app/" {
		t.Fatalf("Location = %q, want /app/", loc)
	}
}

func TestShellServesWithCSP(t *testing.T) {
	rec := get(t, testRouter(t), "/app/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /app/ = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("missing same-origin CSP, got %q", csp)
	}
	if body := rec.Body.String(); !strings.Contains(body, "./app.js") {
		t.Fatalf("shell does not reference ./app.js:\n%s", body)
	}
}

// Every file the shell references must be embedded and typed — a missing one
// here is the blank-page class the browser sees and curl does not (B1/ENG-5).
func TestModuleFilesServeWithTypes(t *testing.T) {
	r := testRouter(t)
	cases := []struct{ path, ct, marker string }{
		{"/app/app.js", "text/javascript", "loadContract"},
		{"/app/contract.js", "text/javascript", "x-appximo-references"},
		{"/app/style.css", "text/css", ".shell"},
	}
	for _, c := range cases {
		rec := get(t, r, c.path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.ct) {
			t.Fatalf("%s Content-Type = %q, want %s", c.path, ct, c.ct)
		}
		if !strings.Contains(rec.Body.String(), c.marker) {
			t.Fatalf("%s does not contain %q", c.path, c.marker)
		}
	}
}

// The generic form's five rules and the contract-extension readers are the
// load-bearing behavior of the SPA — pin their presence so an edit that drops
// one fails here, not in a browser three sessions later.
func TestGenericBehaviorsPinned(t *testing.T) {
	r := testRouter(t)
	app := get(t, r, "/app/app.js").Body.String()
	for _, marker := range []string{
		"OMIT empty on create", // rule 1
		"PATCH",                // rule 2
		"explicit null clears", // rule 3
		"paint them ALL",       // rule 4
		"only legal moves",     // rule 5
		"x-appximo",            // extension-driven (via contract.js import)
		// APP-PODER-S1: what the contract already allowed, now used
		"parseServerTiming",                    // the engine's query time shown, not guessed
		"PER_CHOICES = [15, 25, 50, 100, 250]", // the page-size selector; 15 stays the default
		"total_pages",                          // honest paging: "page N of M · n of total"
		"renderDetail",                         // the detail with relations both ways
		"jsonPrecisionRisks",                   // ENG-50 warned before the value is lost
		"JSON_MAX_BYTES = 1048576",             // the 1 MiB request cap said in the UI
		"stateToHash",                          // views live in the URL, never in the engine
		"TX_MAX = 100",                         // bulk actions batched at the engine's cap
		"retried row by row",                   // partial failure named, never a silent 'done'
	} {
		if !strings.Contains(app, marker) && marker != "x-appximo" {
			t.Errorf("app.js lost the %q behavior marker", marker)
		}
	}
	contract := get(t, r, "/app/contract.js").Body.String()
	for _, ext := range []string{
		"x-appximo-relation", "x-appximo-references", "x-appximo-file",
		"x-appximo-transitions", "x-appximo-initial", "x-appximo-virtual-resources",
		"x-appximo-json", // ADR-028: the JSON editor keys off the tag
		"children",       // APP-PODER-S1: reverse relations derived from the FKs
		"subroute",       // the published /api/{res}/{id}/{seg} path used for parents
	} {
		if !strings.Contains(contract, ext) {
			t.Errorf("contract.js lost the %s reader", ext)
		}
	}
}

func TestUnknownFileIs404(t *testing.T) {
	rec := get(t, testRouter(t), "/app/nope.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /app/nope.js = %d, want 404", rec.Code)
	}
}

// The chrome added by DEMO-SHOWCASE-S1: i18n (Spanish + English dictionaries,
// browser-derived), the responsive/table-card layout, and the theme tokens.
func TestChromeBehaviorsPinned(t *testing.T) {
	r := testRouter(t)
	i18n := get(t, r, "/app/i18n.js")
	if i18n.Code != http.StatusOK {
		t.Fatalf("GET /app/i18n.js = %d, want 200", i18n.Code)
	}
	for _, marker := range []string{"es:", "en:", "navigator.language"} {
		if !strings.Contains(i18n.Body.String(), marker) {
			t.Errorf("i18n.js lost %q", marker)
		}
	}
	css := get(t, r, "/app/style.css").Body.String()
	// The mobile breakpoint moved 720 → 900 px in APP-VITRINA-S1 (the ink
	// sidebar needs the room); the pin follows deliberately.
	for _, marker := range []string{"--app-accent", "prefers-color-scheme", "data-theme", "max-width: 900px"} {
		if !strings.Contains(css, marker) {
			t.Errorf("style.css lost %q (theme tokens / dark mode / responsive)", marker)
		}
	}
}

// APP-VITRINA-S1: the design system is embedded and self-contained. The CSP
// is font-src 'self' / style-src 'self', so the font MUST ship in the binary
// (a CDN @font-face would silently fall back to the system font, and an
// inline style attribute is blocked and logged — both invisible to curl).
func TestDesignSystemSelfContained(t *testing.T) {
	r := testRouter(t)
	font := get(t, r, "/app/fonts/inter-latin-var.woff2")
	if font.Code != http.StatusOK {
		t.Fatalf("GET /app/fonts/inter-latin-var.woff2 = %d, want 200", font.Code)
	}
	if ct := font.Header().Get("Content-Type"); ct != "font/woff2" {
		t.Fatalf("font Content-Type = %q, want font/woff2", ct)
	}
	if cc := font.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=604800") {
		t.Fatalf("font Cache-Control = %q, want a week", cc)
	}
	if n := font.Body.Len(); n < 20000 || n > 120000 {
		t.Fatalf("font size %d bytes: not the latin subset (expected ~39 KB)", n)
	}
	css := get(t, r, "/app/style.css").Body.String()
	for _, marker := range []string{
		"@font-face", "./fonts/inter-latin-var.woff2", // bundled Inter
		"--app-s1", "--app-s8", // the positional lifecycle palette
		"--app-ease",                                               // the system's easing
		".board", ".kcard", ".drawer", ".toast", ".skel", ".empty", // the designed states
	} {
		if !strings.Contains(css, marker) {
			t.Errorf("style.css lost %q", marker)
		}
	}
	for _, f := range []string{"/app/style.css", "/app/app.js", "/app/contract.js", "/app/i18n.js", "/app/"} {
		body := get(t, r, f).Body.String()
		for _, bad := range []string{"https://fonts.", "cdn.", "unpkg.com", "jsdelivr"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s references an external host (%q) — the CSP forbids it", f, bad)
			}
		}
	}
	app := get(t, r, "/app/app.js").Body.String()
	if strings.Contains(app, `style="`) {
		t.Errorf("app.js renders an inline style attribute — blocked by style-src 'self'")
	}
	for _, marker := range []string{"orderedStates", "renderBoard", "moveCard", "loadRelLabels", "toast("} {
		if !strings.Contains(app, marker) && !strings.Contains(get(t, r, "/app/contract.js").Body.String(), marker) {
			t.Errorf("app.js lost %q (board / positional states / resolved relations / toasts)", marker)
		}
	}
}

// theme.css + ui-config.json are the customization seams: always 200 (the SPA
// links/fetches them unconditionally), defaults embedded, options override.
func TestThemeAndUIConfigSeams(t *testing.T) {
	r := chi.NewRouter()
	if err := RegisterOpts(r, Options{
		ThemeCSS:  []byte(":root { --app-accent: #1c9d5b; }"),
		DemoRoles: []string{"demo"},
	}); err != nil {
		t.Fatalf("RegisterOpts: %v", err)
	}
	theme := get(t, r, "/app/theme.css")
	if theme.Code != http.StatusOK || !strings.Contains(theme.Body.String(), "#1c9d5b") {
		t.Fatalf("custom theme not served: %d %q", theme.Code, theme.Body.String())
	}
	if ct := theme.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Fatalf("theme.css Content-Type = %q", ct)
	}
	cfg := get(t, r, "/app/ui-config.json")
	if cfg.Code != http.StatusOK || !strings.Contains(cfg.Body.String(), `"demo_roles":["demo"]`) {
		t.Fatalf("ui-config not served: %d %q", cfg.Code, cfg.Body.String())
	}

	// Defaults: embedded theme (a comment, still 200 text/css) and empty config.
	rd := testRouter(t)
	dtheme := get(t, rd, "/app/theme.css")
	if dtheme.Code != http.StatusOK || !strings.Contains(dtheme.Body.String(), "--app-accent") {
		t.Fatalf("default theme.css: %d", dtheme.Code)
	}
	dcfg := get(t, rd, "/app/ui-config.json")
	if dcfg.Code != http.StatusOK || strings.TrimSpace(dcfg.Body.String()) != "{}" {
		t.Fatalf("default ui-config = %d %q, want 200 {}", dcfg.Code, dcfg.Body.String())
	}
}

// Demo mode is a safety-relevant behavior: a demo-role session must SIMULATE
// writes (never send them) and say so. Pin the markers.
func TestDemoModePinned(t *testing.T) {
	r := testRouter(t)
	app := get(t, r, "/app/app.js").Body.String()
	for _, marker := range []string{
		"never reaches the server", // the write intercept
		"demoMergeList",            // session coherence: created rows visible
		"ui-config.json",           // activation comes from the served config
	} {
		if !strings.Contains(app, marker) {
			t.Errorf("app.js lost the %q demo behavior", marker)
		}
	}
	i18n := get(t, r, "/app/i18n.js").Body.String()
	if !strings.Contains(i18n, "demo.banner") {
		t.Error("i18n.js lost the demo banner string")
	}
}

// ENG-46: the consumer's return bar travels in ui-config.json as text + ONE
// validated link; the SPA renders it as text nodes. A javascript: href is
// dropped (text-only bar), an empty text disables the bar entirely.
func TestBannerSeam(t *testing.T) {
	r := chi.NewRouter()
	if err := RegisterOpts(r, Options{Banner: &Banner{Text: "← Volver a la tienda", Href: "https://tiendita.example.com/?from=app"}}); err != nil {
		t.Fatalf("RegisterOpts: %v", err)
	}
	cfg := get(t, r, "/app/ui-config.json").Body.String()
	if !strings.Contains(cfg, `"banner":{"text":"← Volver a la tienda","href":"https://tiendita.example.com/?from=app"}`) {
		t.Fatalf("banner not published: %s", cfg)
	}
	unsafe := chi.NewRouter()
	_ = RegisterOpts(unsafe, Options{Banner: &Banner{Text: "x", Href: "javascript:alert(1)"}})
	if body := get(t, unsafe, "/app/ui-config.json").Body.String(); strings.Contains(body, "javascript") || !strings.Contains(body, `"banner":{"text":"x"}`) {
		t.Fatalf("unsafe href must be dropped, got %s", body)
	}
	for _, h := range []string{"//evil.example.com", "data:text/html,x", ""} {
		if safeBannerHref(h) {
			t.Errorf("safeBannerHref(%q) = true, want false", h)
		}
	}
	for _, h := range []string{"https://a.b/c", "http://a.b", "/", "/tienda", "mailto:x@y.z", "tel:+57", "#top"} {
		if !safeBannerHref(h) {
			t.Errorf("safeBannerHref(%q) = false, want true", h)
		}
	}
	none := chi.NewRouter()
	_ = RegisterOpts(none, Options{Banner: &Banner{Text: "   "}})
	if body := get(t, none, "/app/ui-config.json").Body.String(); strings.Contains(body, "banner") {
		t.Fatalf("blank text must not publish a banner: %s", body)
	}
	app := get(t, r, "/app/app.js").Body.String()
	if !strings.Contains(app, "consumer-bar") || !strings.Contains(app, "bannerHTML") {
		t.Errorf("app.js lost the return-bar renderer")
	}
}
