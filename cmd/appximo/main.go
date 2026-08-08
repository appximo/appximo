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

First time here? ONE command starts everything locally:

  appximo up               Postgres + secrets + tenant + admin + server, in one go
  appximo new "<idea>"     same, with the schema AI-generated from your idea

Have an AI agent (Claude Code, Cursor, Copilot…)? Then start here instead:

  appximo prompt           THE prompt: paste it into your agent → from an idea
                           to production with HTTPS, one question block, zero
                           questions after it

The engine also prints the deeper contracts the prompt draws on — paste these
individually when the task is only one layer:

  appximo spec             the schema grammar (the declarative 90%)
  appximo backend-spec     custom Go handlers, hooks, auth, background jobs
  appximo frontend-spec    the API contract a UI consumes, errors→screens, files
  appximo backoffice-spec  a CRUD admin UI generated from /openapi.json
  appximo quickstart       OPERATING it: install → tenant → users → production
  appximo specs            all five at once (one paste = the whole contract)

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
