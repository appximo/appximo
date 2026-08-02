package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "appitools",
	Short: "API engine for Go — generate production APIs from a JSON schema",
	Long: `API engine for Go — generate production APIs from a JSON schema.

Building with an AI agent? The engine prints its own agent-facing contract —
paste these into your Claude Code / Cursor and the agent can build the full
stack against the real grammar:

  appitools spec           the schema grammar (the declarative 90%)
  appitools backend-spec   custom Go handlers, hooks, auth, background jobs
  appitools frontend-spec  the API contract a UI consumes, errors→screens, files
  appitools specs          all three at once (one paste = the whole contract)

The agent self-corrects with 'appitools validate --json <schema>' as the oracle.`,
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
