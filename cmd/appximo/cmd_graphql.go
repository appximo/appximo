package main

import (
	"fmt"
	"os"

	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/spf13/cobra"
)

var graphqlCmd = &cobra.Command{
	Use:   "graphql <schema.json>",
	Short: "Generate GraphQL SDL schema from a schema file",
	Long: `Reads schema.json and emits a complete GraphQL SDL document to stdout.

Examples:
  appximo graphql schema.json > schema.graphql
  appximo graphql schema.json | head -30`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		s, err := schema.LoadFromFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error reading schema:", err)
			os.Exit(1)
		}
		if errs := schema.Validate(s); len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "Invalid schema:")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, " ", e.Error())
			}
			os.Exit(1)
		}
		fmt.Print(codegen.GenerateGraphQL(s))
	},
}

func init() {
	rootCmd.AddCommand(graphqlCmd)
}
