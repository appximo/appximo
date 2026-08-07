package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate [file]",
	Short: "Validate a schema — BOTH layers (structural + semantic); --json = the LLM-friendly report",
	Long: `Validates a schema file, running BOTH layers in order: the structural
meta-schema check first (the deterministic net), then the Go semantic validator
(the authority: cross-references, RBAC, state machines). One command answers
"may this run?" completely — you never need to guess which of two commands to
call (validate-schema remains available as the structural half alone).

Default output is human-readable errors plus the SCHEMA-5 warnings ("valid but
almost certainly not what you meant"). With --json it emits the UNIFIED,
LLM-friendly structured report (AI-F0-S2): one JSON object { "valid",
"errors":[ {path, rule, message, expected, got, fix, source} ], "warnings":[…] }
an AI (or any tool) can parse and auto-correct from. Exit 1 when invalid,
0 when valid (both modes).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			raw, err := os.ReadFile(args[0])
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error reading schema:", err)
				os.Exit(1)
			}
			rep := schema.ValidateReport(raw)
			b, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Println(string(b))
			if !rep.Valid {
				os.Exit(1)
			}
			return
		}

		// C2 (FIELD-FEEDBACK-S1): the human mode runs BOTH layers too — the
		// unified report (structural + semantic), rendered as prose. Before,
		// plain `validate` was semantic-only and nothing said which of the two
		// near-identically-named commands to run, or whether one included the
		// other.
		raw, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading schema:", err)
			os.Exit(1)
		}
		rep := schema.ValidateReport(raw)
		if rep.Valid {
			fmt.Println("Schema valid ✓")
			if s, lerr := schema.LoadFromFile(args[0]); lerr == nil {
				printSchemaWarnings(s)
			}
			return
		}
		for _, e := range rep.Errors {
			loc := e.Path
			if loc == "" || loc == "$" {
				loc = "schema"
			}
			line := fmt.Sprintf("%s: %s", loc, e.Message)
			if e.Fix != "" {
				line += " — fix: " + e.Fix
			}
			fmt.Fprintln(os.Stderr, line)
		}
		os.Exit(1)
	},
}

// printSchemaWarnings reports the findings that do NOT make a schema invalid but
// almost certainly make it behave differently from what its author meant (SCHEMA-5).
// "Valid" and "does what you asked" are different questions, and only one of them
// had an answer before.
func printSchemaWarnings(s *schema.APISchema) {
	warns := schema.Warnings(s)
	if len(warns) == 0 {
		return
	}
	fmt.Println()
	fmt.Printf("%d warning(s) — the schema is valid, but this will probably not do what you meant:\n", len(warns))
	for _, w := range warns {
		fmt.Printf("\n  ⚠ %s\n    %s\n", w.Field, w.Message)
		if w.Fix != "" {
			fmt.Printf("    → %s\n", w.Fix)
		}
	}
}

func init() {
	validateCmd.Flags().Bool("json", false, "emit the unified structured (LLM-friendly) JSON report — both structural + semantic validators")
	rootCmd.AddCommand(validateCmd)
}
