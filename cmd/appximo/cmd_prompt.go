package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

// LAUNCHPAD-S1: the front door. The five specs are the complete contract, but
// they are ~5000 lines a human has to know exist; the master prompt is the
// one block a user pastes into ANY coding agent to get from an idea to
// production with HTTPS. It asks the user one question block up front and
// then instructs the agent to never ask again — every friction that made a
// real evaluator's agent stumble (the embedded frontend give-up, the tenant
// registration, the non-empty-box install) is pre-empted by a line in it.
var promptCmd = &cobra.Command{
	Use:     "prompt",
	Aliases: []string{"master-prompt"},
	Short:   "Print THE prompt: paste it into your AI agent → from an idea to production with HTTPS",
	Long: `Print the master prompt (Markdown, on stdout).

This is the recommended way to build with Appximo: paste this one block into
your coding agent (Claude Code, Cursor, Copilot…), replace <describe the app>
with your idea, and answer its single question block. The agent installs the
engine, writes and validates the schema, boots everything locally, proves it
with real requests against an executable checklist — and, if you asked for
it, deploys to your VPS with a real domain and HTTPS, without asking you
anything mid-way.

The deeper contracts it draws on (spec, backend-spec, frontend-spec,
backoffice-spec, quickstart) stay available individually; the prompt tells
the agent when to print each one.

Single source: docs/MASTER_PROMPT.md (embedded).`,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(appximo.MasterPrompt())
	},
}

func init() {
	rootCmd.AddCommand(promptCmd)
}
