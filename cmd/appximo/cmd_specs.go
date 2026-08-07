package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
	"github.com/appximo/appximo/pkg/aigen"
)

// THIRD-PARTY-READY-S1: `appximo specs` — the whole agent contract in ONE
// paste. The documents cross-reference each other, but an external agent's
// context is assembled by a human who may not know how many there are; this
// command removes that failure mode. Each part keeps its own single source
// (aigen.Spec, the embedded markdowns) — this is concatenation, never a
// separate document to drift. FIELD-FEEDBACK-S1 grew the trilogy to five:
// backoffice-spec (the generated admin UI) and quickstart (the OPERATE side —
// the field evaluation showed the build docs worked from one paste while
// tenant registration and the first admin had to be reverse-engineered).
var specsCmd = &cobra.Command{
	Use:   "specs",
	Short: "Print ALL five docs (spec + backend + frontend + backoffice + quickstart) — a single paste for your agent",
	Long: `Prints the complete agent contract in one stream, separated by banners:

  1. appximo spec            — the schema grammar
  2. appximo backend-spec    — custom handlers, hooks, auth, jobs
  3. appximo frontend-spec   — the frontend contract (errors→screens, files)
  4. appximo backoffice-spec — a CRUD admin UI generated from /openapi.json
  5. appximo quickstart      — OPERATING it: install → tenant → users → production

Use it when priming an agent that will build and run a FULL app; use the
individual commands when the task is only one layer (smaller context). Same
sources — this can never diverge from the five commands it concatenates.`,
	Run: func(cmd *cobra.Command, args []string) {
		const sep = "\n\n---\n\n<!-- ======================= %s ======================= -->\n\n"
		fmt.Printf(sep, "appximo spec — THE SCHEMA")
		fmt.Println(aigen.Spec())
		fmt.Printf(sep, "appximo backend-spec — THE BACKEND")
		fmt.Println(appximo.BackendSpec())
		fmt.Printf(sep, "appximo frontend-spec — THE FRONTEND")
		fmt.Println(appximo.FrontendSpec())
		fmt.Printf(sep, "appximo backoffice-spec — THE GENERATED ADMIN UI")
		fmt.Println(appximo.BackofficeSpec())
		fmt.Printf(sep, "appximo quickstart — OPERATING IT")
		fmt.Println(appximo.LifecycleSpec())
	},
}

func init() {
	rootCmd.AddCommand(specsCmd)
}
