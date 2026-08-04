package appximo

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// SEC-2 — the default static CSP pins inline scripts by hash instead of allowing
// all of them.
//
// `DefaultStaticCSP` shipped `script-src 'self' 'unsafe-inline'`, which turns off
// the main protection CSP exists to give: markup injected into the SPA shell
// executes. It was there because SvelteKit's shell boots hydration from an inline
// <script> and the first strict draft blanked a real app in every browser
// (invisible to curl — see the sibling file for that lesson).
//
// The bar these tests hold: the hardened policy must (a) actually block injected
// inline script, (b) still run the shells the mainstream bundlers emit, and
// (c) when it cannot do both, fall back LOUDLY rather than shipping a policy that
// silently breaks the page or silently fails to protect it.

func sha256Src(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
}

// TestHardenedCSP_SvelteKitShell is the shape that forced 'unsafe-inline' in the
// first place: an external module plus an inline hydration bootstrap.
func TestHardenedCSP_SvelteKitShell(t *testing.T) {
	const boot = `
			const el = document.currentScript.parentElement;
			import("/_app/immutable/entry/start.js").then(m => m.start(el));
		`
	index := []byte(`<!doctype html><html><head>
<script type="module" src="/_app/immutable/entry/app.js"></script>
</head><body><div id="svelte"></div>
<script>` + boot + `</script>
</body></html>`)

	policy, note := hardenedStaticCSP(DefaultStaticCSP, index)

	if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src still allows ALL inline scripts — the hardening did not apply:\n  %s\n  note: %s", policy, note)
	}
	want := sha256Src(boot)
	if !strings.Contains(policy, want) {
		t.Errorf("the inline bootstrap is not pinned by hash.\n  policy: %s\n  want to contain: %s", policy, want)
	}
	// The rest of the policy must be untouched — hardening one directive must not
	// quietly relax or drop another.
	for _, keep := range []string{
		"default-src 'self'", "connect-src 'self'", "frame-ancestors 'none'",
		"base-uri 'self'", "form-action 'self'", "style-src 'self' 'unsafe-inline'",
	} {
		if !strings.Contains(policy, keep) {
			t.Errorf("the hardening changed an unrelated directive: %q missing from\n  %s", keep, policy)
		}
	}
	if note == "" {
		t.Error("a policy change must be announced in the boot log (ADR-024: no silent tolerance)")
	}
}

// TestHardenedCSP_NoInlineScripts: a Vite build with only external modules gets
// the strictest policy, not merely a hashed one.
func TestHardenedCSP_NoInlineScripts(t *testing.T) {
	index := []byte(`<!doctype html><html><head>
<script type="module" crossorigin src="/assets/index-abc123.js"></script>
<link rel="stylesheet" href="/assets/index-abc123.css">
</head><body><div id="root"></div></body></html>`)

	policy, note := hardenedStaticCSP(DefaultStaticCSP, index)
	if !strings.Contains(policy, "script-src 'self';") {
		t.Errorf("a shell with no inline scripts should get script-src 'self' only, got:\n  %s", policy)
	}
	if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("unsafe-inline survived on script-src: %s", policy)
	}
	if !strings.Contains(note, "no inline scripts") {
		t.Errorf("note should say why it could be strictest, got %q", note)
	}
}

// TestHardenedCSP_MultipleInlineScriptsAllPinned: every inline block is covered,
// not just the first — a shell that emits two and gets one hash would be broken
// in the browser, which is the exact failure mode this area already produced once.
func TestHardenedCSP_MultipleInlineScriptsAllPinned(t *testing.T) {
	a, b := `window.__ENV={api:"/api"};`, `console.log("boot");`
	index := []byte(`<html><head><script>` + a + `</script></head><body><script>` + b + `</script></body></html>`)
	policy, _ := hardenedStaticCSP(DefaultStaticCSP, index)
	for _, body := range []string{a, b} {
		if !strings.Contains(policy, sha256Src(body)) {
			t.Errorf("inline block %q is not pinned:\n  %s", body, policy)
		}
	}
}

// TestHardenedCSP_EventHandlerFallsBackLoudly: an `onclick=` attribute cannot be
// covered by a hash without 'unsafe-hashes'. Rather than ship a policy that would
// break the page, keep the permissive one AND say so — tolerance is fine, silent
// tolerance is not.
func TestHardenedCSP_EventHandlerFallsBackLoudly(t *testing.T) {
	index := []byte(`<html><body><button onclick="doThing()">go</button>
<script>var x=1;</script></body></html>`)
	policy, note := hardenedStaticCSP(DefaultStaticCSP, index)
	if policy != DefaultStaticCSP {
		t.Errorf("a shell with inline event handlers must keep the permissive policy, got:\n  %s", policy)
	}
	if !strings.Contains(note, "onclick") {
		t.Errorf("the fallback must name what blocked it, got %q", note)
	}
	if !strings.Contains(note, "NOT blocked") {
		t.Errorf("the fallback must state plainly that inline script is still allowed, got %q", note)
	}
}

// TestHardenedCSP_NoIndexFallsBackLoudly: an assets-only mount has no shell to
// hash; keep the default and say why.
func TestHardenedCSP_NoIndexFallsBackLoudly(t *testing.T) {
	policy, note := hardenedStaticCSP(DefaultStaticCSP, nil)
	if policy != DefaultStaticCSP {
		t.Errorf("with no index the policy must be unchanged, got %s", policy)
	}
	if !strings.Contains(note, "no index.html") {
		t.Errorf("note should explain, got %q", note)
	}
}

// TestHardenedCSP_CustomPolicyIsNeverRewritten: an operator who set CSP by hand
// owns it. Silently rewriting somebody's explicit policy would be its own surprise.
func TestHardenedCSP_CustomPolicyIsNeverRewritten(t *testing.T) {
	custom := "default-src 'self'; script-src 'self' https://cdn.example.com"
	index := []byte(`<html><body><script>var x=1;</script></body></html>`)
	policy, note := hardenedStaticCSP(custom, index)
	if policy != custom {
		t.Errorf("a custom policy must be served verbatim, got %s", policy)
	}
	if note != "" {
		t.Errorf("no note expected for a custom policy, got %q", note)
	}
}

// TestHardenedCSP_ExternalScriptsAreNotHashed: a <script src=…> is covered by
// 'self', not by a hash. Emitting one would be meaningless noise.
func TestHardenedCSP_ExternalScriptsAreNotHashed(t *testing.T) {
	index := []byte(`<html><head><script src="/app.js"></script></head><body></body></html>`)
	policy, _ := hardenedStaticCSP(DefaultStaticCSP, index)
	if strings.Contains(policy, "sha256-") {
		t.Errorf("an external script must not produce a hash: %s", policy)
	}
	if !strings.Contains(policy, "script-src 'self';") {
		t.Errorf("expected the strictest script-src, got %s", policy)
	}
}

// TestHardenedCSP_RealEmbeddedShells runs the hardening over the engine's OWN
// built UIs. They are the shells shipped in every binary, so if the hardening
// cannot handle them it cannot handle anything.
func TestHardenedCSP_RealEmbeddedShells(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"studio", "pkg/editorui/web/build/index.html"},
		{"admin", "pkg/adminui/web/dist/index.html"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx := readIfPresent(t, tc.path)
			if idx == nil {
				t.Skipf("%s not built in this tree", tc.path)
			}
			policy, note := hardenedStaticCSP(DefaultStaticCSP, idx)
			t.Logf("policy: %s", policy)
			t.Logf("note:   %s", note)
			if strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
				t.Errorf("%s: the real shell could not be hardened — %s", tc.name, note)
			}
		})
	}
}

// readIfPresent reads a repo file, returning nil when it does not exist (the UI
// build is gitignored except for index.html, so a fresh clone may lack it).
func readIfPresent(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
