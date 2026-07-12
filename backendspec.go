package appitools

import _ "embed"

// backendSpecMarkdown is the definitive LLM-oriented guide for building a
// COMPLETE Appitools backend — schema + custom handlers + hooks + auth + jobs —
// with the Phase-0 safety patterns as rules and compiling examples. It is the
// single source: docs/BACKEND_SPEC_LLM.md is the human doc AND what the
// `appitools backend-spec` command prints (embedded here so they never diverge).
// The companion `appitools spec` / aigen.Spec() covers only the SCHEMA.
//
//go:embed docs/BACKEND_SPEC_LLM.md
var backendSpecMarkdown string

// BackendSpec returns the agent guide for building a complete backend. Paste it
// into your own agent (Claude Code, Cursor) alongside `appitools spec` and the
// agent has everything it needs to write handlers, hooks, auth and jobs safely.
func BackendSpec() string { return backendSpecMarkdown }
