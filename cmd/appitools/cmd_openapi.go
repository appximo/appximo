package main

import (
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var openapiCmd = &cobra.Command{
	Use:   "openapi <schema.json>",
	Short: "Generate OpenAPI 3.0 YAML spec from a schema file",
	Long: `Reads schema.json and emits a complete OpenAPI 3.0.3 YAML document to stdout.

Examples:
  appitools openapi schema.json > openapi.yaml
  appitools openapi --base-url https://api.myapp.com schema.json > openapi.yaml
  appitools openapi schema.json | npx @redocly/cli lint /dev/stdin`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		baseURL, _ := cmd.Flags().GetString("base-url")

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
		out, err := codegen.GenerateOpenAPI(s, baseURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error generating OpenAPI spec:", err)
			os.Exit(1)
		}
		os.Stdout.Write(out)
	},
}

func init() {
	openapiCmd.Flags().String("base-url", "/", "Base URL for the API server (e.g. https://api.myapp.com)")
	rootCmd.AddCommand(openapiCmd)
}
