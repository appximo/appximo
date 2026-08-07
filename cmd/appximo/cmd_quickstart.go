package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

// FIELD-FEEDBACK-S1 (T2/C6/B8): the OPERATE side of the printable contract.
// The build trilogy worked from one paste in the field evaluation, while the
// two most expensive discoveries of the whole cycle — how a tenant is
// registered (the schema travels IN the registration body) and how the first
// admin exists — lived in no printable doc; the evaluator de-minified the
// admin panel's JS bundle to find the endpoint. This command closes that:
// steps 1 and 2 of operating the product, printable like the rest.
var quickstartCmd = &cobra.Command{
	Use:     "quickstart",
	Aliases: []string{"lifecycle-spec"},
	Short:   "Print the operations contract: install → tenant → users → evolve → production",
	Long: `Print the OPERATIONS contract (Markdown, on stdout).

The build-side docs (spec, backend-spec, frontend-spec, backoffice-spec) teach
an agent to CONSTRUCT; this one teaches it — or you — to OPERATE: the three
settings and the .env, booting, registering a tenant (the schema travels in
the registration body), bootstrapping the first admin, where users come from,
evolving a live tenant (what serves hot vs what restarts), the production
path, and the operator traps, each verified in the field.

Single source: docs/LIFECYCLE_SPEC_LLM.md (embedded).`,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Println(appximo.LifecycleSpec())
	},
}

func init() {
	rootCmd.AddCommand(quickstartCmd)
}
