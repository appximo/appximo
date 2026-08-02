package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/miguelangel/appitools"
)

// FRONTEND-SPEC-S1: `appitools frontend-spec` — the third document of the agent
// trilogy (`spec` teaches the schema, `backend-spec` the handlers, this one the
// FRONTEND). It prints docs/FRONTEND_SPEC_LLM.md (embedded, single source):
// where the frontend lives (embedded one-binary vs served apart), the
// recommended stack with its argument, the exact API contract a UI consumes,
// the error→screen-state mapping (422 field-by-field, 409 keeping the user's
// work, 503 with retry), the mandatory screen states, the files/images pattern
// (upload → attach → display, public serving included), and the traps that
// only show up in a real browser. Distilled from a shipped production
// storefront, not from theory.
var frontendSpecCmd = &cobra.Command{
	Use:   "frontend-spec",
	Short: "Imprime la guía para construir un FRONTEND productivo (stack/contrato API/errores→UI/archivos) para un agente — pégala en tu Claude Code/Cursor",
	Long: `Prints the definitive agent guide for building a production frontend on an
Appitools backend: the embedded-vs-apart decision, the recommended stack
(SvelteKit + adapter-static as a pure SPA — and WHY), the complete API contract
(tenant = Host, auth, the exact filter/sort/pagination grammar, embeds,
aggregation, SSE), the error contract mapped to screen states (the multi-field
422, the work-preserving 409, 401 vs 403, the honest 503), the mandatory screen
states with worked patterns, the files/images flow (upload with progress →
attach via the file field → display, including PUBLIC images via a ByteServing
route), and the traps only a real browser reveals (CSP, the empty-shell build
trap, rate-limit budgets).

This is the third of the trilogy: give an agent 'appitools spec' (the schema),
'appitools backend-spec' (the handlers) and this doc, and it can build the full
stack. The doc lives at docs/FRONTEND_SPEC_LLM.md; the runnable example at
examples/frontend-guide/.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(appitools.FrontendSpec())
	},
}

func init() {
	rootCmd.AddCommand(frontendSpecCmd)
}
