package appximo

import (
	_ "embed"
	"strings"
)

// masterPromptMarkdown is THE MASTER PROMPT (LAUNCHPAD-S1): the single block a
// user pastes into their own coding agent to go from an idea in one sentence
// to production with HTTPS, with exactly one question block at the start and
// zero questions after it. It exists because three real users reached
// production and all three stumbled in the same gaps BETWEEN documents — the
// give-up point on the embedded frontend, the tenant registration, the
// non-empty-box deploy. Every line traces to a measured friction; the
// verification record lives with the session notes. Embedded from its single
// source, docs/MASTER_PROMPT.md, so `appximo prompt` and the doc (and the
// website hero, which copies it) can never diverge.
//
//go:embed docs/MASTER_PROMPT.md
var masterPromptMarkdown string

// MasterPrompt returns the paste-ready master prompt. The source file opens
// with an HTML comment addressed to maintainers, not agents — it is stripped
// here so the printed text is exactly what a user should paste.
func MasterPrompt() string { return stripPromptPreamble(masterPromptMarkdown) }

// stripPromptPreamble drops the leading maintainer-only HTML comment that every
// prompt source carries, so what the binary prints is exactly the paste.
func stripPromptPreamble(md string) string {
	if _, rest, found := strings.Cut(md, "-->\n"); found {
		return strings.TrimLeft(rest, "\n")
	}
	return md
}
