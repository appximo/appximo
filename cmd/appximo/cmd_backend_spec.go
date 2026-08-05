package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

// LIBRARY-HARDEN-S1: `appximo backend-spec` — the agent guide for building a
// COMPLETE backend, the companion to `appximo spec` (which covers the schema).
// It prints docs/BACKEND_SPEC_LLM.md (embedded, single source): the decision
// framework for where each piece of logic goes, the custom-handler Ctx surface
// with compiling examples, the Phase-0 safety rules (SafeGo, post-commit side
// effects, public-route hardening), hooks, auth and jobs. Paste it into your own
// Claude Code / Cursor together with `appximo spec` and the agent can write
// handlers/hooks/jobs safely — the in-process power made agent-accessible.
var backendSpecCmd = &cobra.Command{
	Use:   "backend-spec",
	Short: "Print the guide for building a COMPLETE backend (handlers/hooks/auth/jobs) for an agent — paste it into your Claude Code/Cursor",
	Long: `Prints the definitive agent guide for building a complete Appximo backend:
the decision framework (schema vs hook vs custom handler vs job vs service), the
custom-handler surface (the whole Ctx, with compiling examples), the Phase-0
safety rules (SafeGo for goroutines, side effects after commit, public-route
hardening), hooks, auth, and background jobs — plus the end-to-end walkthrough.

This is the companion to 'appximo spec' (which teaches the SCHEMA): give an
agent BOTH and it can build a complete, secure backend. The full doc, with the
compiling example, lives at docs/BACKEND_SPEC_LLM.md and examples/backend-guide/.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(appximo.BackendSpec())
	},
}

func init() {
	rootCmd.AddCommand(backendSpecCmd)
}
