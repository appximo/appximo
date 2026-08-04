package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Inyectadas en build con
//
//	-ldflags "-X main.version=v0.1.0 -X main.revision=<sha>"
//
// (release.yml y el Dockerfile lo hacen; un build local plano reporta "dev").
var (
	version  = "dev"
	revision = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Imprime la versión de Appximo",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("appximo %s (commit %s)\n", version, revision)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
