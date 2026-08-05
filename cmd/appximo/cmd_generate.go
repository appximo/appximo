package main

import (
	"fmt"
	"os"

	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

var generateCmd = &cobra.Command{
	Use:   "generate [schema.json]",
	Short: "Generate REST handlers from a JSON schema",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		s, err := schema.LoadFromFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading schema:", err)
			os.Exit(1)
		}

		errs := schema.Validate(s)
		if len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "Invalid schema:")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, " ", e.Error())
			}
			os.Exit(1)
		}

		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error getting current directory:", err)
			os.Exit(1)
		}

		generated, err := codegen.Generate(s, cwd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error generating code:", err)
			os.Exit(1)
		}

		for _, f := range generated {
			fmt.Printf("  ✓ generated %s\n", f)
		}
		fmt.Printf("Generate complete. %d handlers generated.\n", len(s.Resources))
	},
}

func init() {
	rootCmd.AddCommand(generateCmd)
}
