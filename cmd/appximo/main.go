package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "appximo",
	Short: "API engine for Go — generate production APIs from a JSON schema",
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

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
