package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Imprime la versión de Appitools",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Appitools v0.1.0")
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
