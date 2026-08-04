package appximo

import _ "embed"

// frontendSpecMarkdown is the definitive LLM-oriented guide for building a
// production FRONTEND on an Appximo backend — where the frontend lives
// (embedded via Config.Static vs served apart), the recommended stack and its
// argument, the exact API contract a UI consumes (tenant Host, auth, filters,
// pagination, errors), the error→screen-state mapping, the mandatory screen
// states, the files/images pattern (upload → attach → display, including
// PUBLIC serving via Ctx.ServeFile), and the browser-only traps. It is the
// single source: docs/FRONTEND_SPEC_LLM.md is the human doc AND what the
// `appximo frontend-spec` command prints (embedded here so they never
// diverge). The third of the trilogy: `spec` (schema) + `backend-spec`
// (handlers) + this (the UI).
//
//go:embed docs/FRONTEND_SPEC_LLM.md
var frontendSpecMarkdown string

// FrontendSpec returns the agent guide for building a production frontend.
// Paste it into your own agent (Claude Code, Cursor) alongside `appximo
// spec` and `appximo backend-spec` and the agent has the full stack: schema,
// backend, and the UI that consumes them.
func FrontendSpec() string { return frontendSpecMarkdown }
