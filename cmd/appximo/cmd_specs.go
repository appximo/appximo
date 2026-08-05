package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
	"github.com/appximo/appximo/pkg/aigen"
)

// THIRD-PARTY-READY-S1: `appximo specs` — the whole agent trilogy in ONE
// paste. The three documents cross-reference each other, but an external
// agent's context is assembled by a human who may not know there are three;
// this command removes that failure mode (the discoverability class 5-4, ×3
// docs). Each part keeps its own single source (aigen.Spec, the two embedded
// markdowns) — this is concatenation, never a fourth document to drift.
var specsCmd = &cobra.Command{
	Use:   "specs",
	Short: "Print the FULL trilogy (spec + backend-spec + frontend-spec) at once — a single paste for your agent",
	Long: `Prints the complete agent trilogy in one stream, separated by banners:

  1. appximo spec           — the schema grammar
  2. appximo backend-spec   — custom handlers, hooks, auth, jobs
  3. appximo frontend-spec  — the frontend contract (errors→screens, files)

Use it when priming an agent that will build a FULL app; use the individual
commands when the task is only one layer (smaller context). Same sources —
this can never diverge from the three commands it concatenates.`,
	Run: func(cmd *cobra.Command, args []string) {
		const sep = "\n\n---\n\n<!-- ======================= %s ======================= -->\n\n"
		fmt.Printf(sep, "appximo spec — THE SCHEMA")
		fmt.Println(aigen.Spec())
		fmt.Printf(sep, "appximo backend-spec — THE BACKEND")
		fmt.Println(appximo.BackendSpec())
		fmt.Printf(sep, "appximo frontend-spec — THE FRONTEND")
		fmt.Println(appximo.FrontendSpec())
	},
}

func init() {
	rootCmd.AddCommand(specsCmd)
}
