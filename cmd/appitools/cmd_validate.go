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
			return
		}

		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e.Error())
		}
		os.Exit(1)
	},
}

func init() {
	validateCmd.Flags().Bool("json", false, "emit the unified structured (LLM-friendly) JSON report — both structural + semantic validators")
	rootCmd.AddCommand(validateCmd)
}
