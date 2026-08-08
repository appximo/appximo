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

  --install   print the INSTALL prompt instead: the short block that installs
              or UPDATES the engine itself. Paste that one first — an agent
              handed the build prompt on a machine with an OLD appximo will
              happily use the old one and then fail on commands it lacks.

Single sources: docs/MASTER_PROMPT.md and docs/INSTALL_PROMPT.md (embedded).`,
	Run: func(cmd *cobra.Command, _ []string) {
		if install, _ := cmd.Flags().GetBool("install"); install {
			fmt.Println(appximo.InstallPrompt())
			return
		}
		fmt.Println(appximo.MasterPrompt())
	},
}

func init() {
	// A FLAG, not a second command: there is one front door ("the prompt"),
	// with two moments. `appximo prompt --install` is also the update path for
	// someone who already has the binary and wants their agent to refresh it.
	promptCmd.Flags().Bool("install", false,
		"print the INSTALL/UPDATE prompt instead of the build prompt (paste this one first)")
	rootCmd.AddCommand(promptCmd)
}
