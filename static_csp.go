package appximo

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

// SEC-2 — hash-based CSP for static mounts.
//
// `DefaultStaticCSP` shipped `script-src 'self' 'unsafe-inline'`, which turns OFF
// the main protection CSP exists to give: with it, markup injected into the SPA
// shell executes. It was there by NECESSITY, not by design — SvelteKit's
// adapter-static shell (and Next export, and Astro islands) boots hydration from
// an inline <script>, and the first strict draft blanked a real SPA in every
// browser, invisible to curl.
//
// The way out is the one CSP itself defines: hash the inline scripts. At boot the
// engine parses the mount's index.html, computes the sha256 of each inline
// <script> body, and emits `'sha256-…'` for each. A browser then executes exactly
// those scripts and nothing else — the bundler keeps working AND injected markup
// is dead. Presence of a hash makes browsers IGNORE 'unsafe-inline', which is
// precisely the upgrade.
//
// Two deliberate boundaries:
//
//   - **style-src keeps 'unsafe-inline'.** Component libraries inject styles at
//     RUNTIME, from JavaScript; those strings are not in index.html, so no
//     boot-time hash can cover them. Removing it would break real apps for a
//     much smaller prize: the residual risk is CSS-based defacement and attribute
//     exfiltration, not script execution.
//   - **Event-handler attributes (`onclick="…"`) and `javascript:` URLs are NOT
//     covered by hashes** — CSP requires `'unsafe-hashes'` for those, which
//     re-opens most of the hole. If a shell uses them the hardening is skipped
//     rather than silently half-applied, and the reason is logged.
//
// The fallback is explicit and loud: anything the parser cannot account for
// leaves the policy at 'unsafe-inline' with a log line naming why, so an operator
// is never told a page is hardened when it is not. That is the same rule the
// migration honesty audit established — tolerance is fine, silent tolerance is
// not.

// inlineScriptCSP returns a script-src source list for the inline scripts in an
// index.html: `'sha256-…'` for each inline <script> found.
//
// ok is false when the document contains something a hash cannot cover, and
// reason says what — the caller then keeps the permissive policy rather than
// shipping one that would break the page.
func inlineScriptCSP(index []byte) (sources []string, ok bool, reason string) {
	if len(index) == 0 {
		return nil, false, "the mount has no index.html to hash"
	}
	doc, err := html.Parse(strings.NewReader(string(index)))
	if err != nil {
		return nil, false, fmt.Sprintf("index.html did not parse as HTML (%v)", err)
	}

	seen := make(map[string]bool)
	var walk func(*html.Node) (bool, string)
	walk = func(n *html.Node) (bool, string) {
		if n.Type == html.ElementNode {
			// An inline event-handler attribute cannot be covered by a hash
			// without 'unsafe-hashes', which would re-open most of the hole.
			for _, a := range n.Attr {
				if len(a.Key) > 2 && strings.HasPrefix(a.Key, "on") {
					return false, fmt.Sprintf("index.html uses the inline event handler %q on <%s>, "+
						"which a CSP hash cannot cover without 'unsafe-hashes'", a.Key, n.Data)
				}
			}
			if n.Data == "script" {
				hasSrc := false
				for _, a := range n.Attr {
					if a.Key == "src" {
						hasSrc = true
					}
				}
				if !hasSrc {
					// The hash is over the element's EXACT text content, byte for
					// byte, with no trimming — that is what the CSP spec hashes
					// and what the browser will compare against.
					var body strings.Builder
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.TextNode {
							body.WriteString(c.Data)
						}
					}
					if body.Len() > 0 {
						sum := sha256.Sum256([]byte(body.String()))
						src := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
						if !seen[src] {
							seen[src] = true
							sources = append(sources, src)
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if okc, why := walk(c); !okc {
				return false, why
			}
		}
		return true, ""
	}
	if okAll, why := walk(doc); !okAll {
		return nil, false, why
	}
	return sources, true, ""
}

// hardenedStaticCSP upgrades the DEFAULT static policy for one mount by replacing
// `script-src 'self' 'unsafe-inline'` with `script-src 'self' <hashes…>`.
//
// It returns the policy to serve plus a human sentence for the boot log, so the
// operator always learns which of the three outcomes happened:
//
//	no inline scripts at all → script-src 'self'                (strictest)
//	inline scripts, hashable → script-src 'self' 'sha256-…'     (hardened)
//	anything else            → unchanged, with the reason        (honest fallback)
func hardenedStaticCSP(base string, index []byte) (policy, note string) {
	const weak = "script-src 'self' 'unsafe-inline'"
	if !strings.Contains(base, weak) {
		// A custom policy the operator wrote, or one already hardened: never
		// rewrite what somebody chose deliberately.
		return base, ""
	}
	sources, ok, reason := inlineScriptCSP(index)
	if !ok {
		return base, "CSP left permissive (script-src 'unsafe-inline'): " + reason +
			". Inline script execution is NOT blocked for this mount"
	}
	if len(sources) == 0 {
		return strings.Replace(base, weak, "script-src 'self'", 1),
			"CSP hardened: the shell has no inline scripts, so script-src is 'self' only"
	}
	return strings.Replace(base, weak, "script-src 'self' "+strings.Join(sources, " "), 1),
		fmt.Sprintf("CSP hardened: %d inline script(s) pinned by sha256; injected inline script is now blocked", len(sources))
}

// prefixLabel renders a mount prefix for a log line ("/" for the root mount).
func prefixLabel(p string) string {
	if p == "" {
		return "/"
	}
	return p
}
