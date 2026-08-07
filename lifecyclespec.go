package appximo

import _ "embed"

// lifecycleSpecMarkdown is the OPERATIONS contract (FIELD-FEEDBACK-S1, findings
// T2/C6/B8): install → configure → tenant registration (the schema travels in
// the body) → the first admin → evolving a live tenant → production. The
// build-side docs teach constructing; this one teaches operating — the half
// the first field evaluation had to discover by de-minifying a JS bundle.
// Embedded from its single source, docs/LIFECYCLE_SPEC_LLM.md, so the CLI
// (`appximo quickstart`) and the doc can never diverge.
//
//go:embed docs/LIFECYCLE_SPEC_LLM.md
var lifecycleSpecMarkdown string

// LifecycleSpec returns the printable operations contract.
func LifecycleSpec() string { return lifecycleSpecMarkdown }
