package appximo

import _ "embed"

// backofficeSpecMarkdown is the fourth build-side printable doc
// (FIELD-FEEDBACK-S1, Part G): a complete admin CRUD UI generated at runtime
// from /openapi.json with zero resource-specific screens — the pattern the
// first third-party field evaluation built and verified, adopted as product
// documentation now that the x-appximo-* contract extensions make it need no
// hardcoded domain knowledge. Embedded from its single source,
// docs/BACKOFFICE_SPEC_LLM.md, so the CLI and the doc can never diverge.
//
//go:embed docs/BACKOFFICE_SPEC_LLM.md
var backofficeSpecMarkdown string

// BackofficeSpec returns the printable back-office contract.
func BackofficeSpec() string { return backofficeSpecMarkdown }
