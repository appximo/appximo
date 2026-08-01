package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate [archivo]",
	Short: "Valida un schema JSON de Appitools (semántico; --json = reporte unificado LLM-friendly)",
	Long: `Validates a schema file. By default it runs the Go semantic validator (the
authority) and prints human-readable errors.

With --json it emits the UNIFIED, LLM-friendly structured report (AI-F0-S2): it runs
BOTH validators — the structural meta-schema and the semantic validator — and outputs
one JSON object { "valid", "errors":[ {path, rule, message, expected, got, fix,
source} ] } an AI (or any tool) can parse and auto-correct from. Exit 1 when invalid,
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

		s, err := schema.LoadFromFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		errs := schema.Validate(s)
		if len(errs) == 0 {
			fmt.Println("Schema válido ✓")
			printSchemaWarnings(s)
			return
		}

		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
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
