package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Injected at build time with
//
//	-ldflags "-X main.version=v0.1.0 -X main.revision=<sha>"
//
// (release.yml and the Dockerfile do; a plain local build reports "dev").
var (
	version  = "dev"
	revision = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Appximo version",
	Run: func(cmd *cobra.Command, args []string) {
		// C5: --json for automation/CI pinning — stdout carries ONLY the JSON.
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"version": version, "commit": revision,
			})
			return
		}
		fmt.Printf("appximo %s (commit %s)\n", version, revision)
	},
}

func init() {
	versionCmd.Flags().Bool("json", false, "print the version as JSON ({\"version\",\"commit\"})")
	rootCmd.AddCommand(versionCmd)
}
