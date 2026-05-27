package main

import (
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate [archivo]",
	Short: "Valida un schema JSON de Appitools",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
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
	rootCmd.AddCommand(validateCmd)
}
