package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

var rootCmd = &cobra.Command{
	Use:   "appximo",
	Short: "API engine for Go — generate production APIs from a JSON schema",
	// F1: a `.env` in the working directory is loaded for EVERY subcommand
	// (the real environment always wins; a BOM is stripped — F1-bis). Silent
	// here so --json commands keep byte-clean output; `serve` announces what
	// it loaded in its boot log.
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		dotenvLoaded = appximo.LoadDotEnv()
	},
	Long: `API engine for Go — generate production APIs from a JSON schema.

Building with an AI agent? The engine prints its own agent-facing contract —
paste these into your Claude Code / Cursor and the agent can build the full
stack against the real grammar:

  appximo spec           the schema grammar (the declarative 90%)
  appximo backend-spec   custom Go handlers, hooks, auth, background jobs
  appximo frontend-spec  the API contract a UI consumes, errors→screens, files
  appximo specs          all three at once (one paste = the whole contract)

The agent self-corrects with 'appximo validate --json <schema>' as the oracle.`,
}

// dotenvLoaded is how many variables the .env fill-in actually set this run —
// server commands announce it in their boot log (machine-output commands don't).
var dotenvLoaded int

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
