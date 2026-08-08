package appximo

import _ "embed"

// installPromptMarkdown is the INSTALL PROMPT (INSTALL-PROMPT-S1): the short
// block a user pastes into their agent BEFORE the master prompt, whose only
// job is to leave the right binary on the machine.
//
// It exists because the master prompt assumes a clean install. The moment
// releases exist, the most common starting state is the opposite: an OLD
// appximo already on the PATH. An agent handed the build prompt finds it
// installed, proceeds happily, and then fails on commands that binary does not
// have — a failure that reads like a typo, not like a stale install. Splitting
// the two prompts is the fix: one job each, and the install one owns the three
// starting states (absent / old / already correct) and the three platforms.
//
//go:embed docs/INSTALL_PROMPT.md
var installPromptMarkdown string

// InstallPrompt returns the paste-ready install prompt. Like MasterPrompt, the
// maintainer-facing HTML comment at the top of the source file is stripped so
// the printed text is exactly what a user should paste.
func InstallPrompt() string { return stripPromptPreamble(installPromptMarkdown) }
