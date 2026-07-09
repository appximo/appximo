package main

import (
	"fmt"

	"github.com/miguelangel/appitools/pkg/aigen"
	"github.com/spf13/cobra"
)

// JSON-EDITOR-S3: `appitools spec` — the external-agent pack. It prints the
// distilled Appitools schema grammar (aigen.Spec): the SAME GrammarCore the
// internal ai-generate loop generates against (single source, pinned by
// pkg/aigen/spec_test.go), plus the advanced blocks, two validated worked
// examples, and the correction-loop instructions. Paste it into any agent's
// context (Claude Code / Cursor / a system prompt) and run the loop with
// `appitools validate --json` as the oracle — the ai-generate flow on YOUR own
// subscription, zero product API cost.
var specCmd = &cobra.Command{
	Use:   "spec",
	Short: "Imprime la gramática del schema destilada para un LLM/agente externo (pégala en tu Claude Code/Cursor)",
	Long: `Prints the Appitools schema grammar distilled for an LLM: closed sets (types,
actions, relation kinds), the strict-key rule, naming, the file field, state
machines, hooks, events, both RBAC forms, full FK coverage — plus two worked
examples (validated against the engine) and the correction loop.

Give this text to your own agent (Claude Code, Cursor, any LLM) together with a
natural-language description of your app; the agent generates the schema and
self-corrects with 'appitools validate --json <file>' until valid. Same grammar
source as the internal 'ai-generate' — they can never diverge. The full flow:
docs/SCHEMA_SPEC_LLM.md.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(aigen.Spec())
	},
}

func init() {
	rootCmd.AddCommand(specCmd)
}
