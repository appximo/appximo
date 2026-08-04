package appximo

import _ "embed"

// backendSpecMarkdown is the definitive LLM-oriented guide for building a
// COMPLETE Appximo backend — schema + custom handlers + hooks + auth + jobs —
// with the Phase-0 safety patterns as rules and compiling examples. It is the
// single source: docs/BACKEND_SPEC_LLM.md is the human doc AND what the
// `appximo backend-spec` command prints (embedded here so they never diverge).
// The companion `appximo spec` / aigen.Spec() covers only the SCHEMA.
//
//go:embed docs/BACKEND_SPEC_LLM.md
var backendSpecMarkdown string

// BackendSpec returns the agent guide for building a complete backend. Paste it
// into your own agent (Claude Code, Cursor) alongside `appximo spec` and the
// agent has everything it needs to write handlers, hooks, auth and jobs safely.
func BackendSpec() string { return backendSpecMarkdown }
