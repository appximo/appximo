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
	Short: "Print the Appximo version (and whether a newer one exists)",
	Long: `Print the Appximo version.

A HUMAN run also checks whether a newer release exists and, if so, prints one
line naming it and how to update. That check:

  • sends NOTHING about you or this machine — it is an anonymous GET of a
    public URL (the /releases/latest redirect), with no identifiers and no
    counters. It is not telemetry;
  • runs ONLY here. Never at 'serve' boot, never on the request path;
  • times out in 2 s and stays silent on any failure (offline is not an error);
  • is off with APPXIMO_NO_VERSION_CHECK=1, with --no-check, and automatically
    whenever CI is set;
  • never runs with --json, so machine output stays offline and byte-stable.`,
	Run: func(cmd *cobra.Command, args []string) {
		// C5: --json for automation/CI pinning — stdout carries ONLY the JSON,
		// and deliberately performs NO network call: a CI pin must not depend
		// on GitHub being reachable, or be slowed by it.
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
				"version": version, "commit": revision,
			})
			return
		}
		fmt.Printf("appximo %s (commit %s)\n", version, revision)
		if noCheck, _ := cmd.Flags().GetBool("no-check"); noCheck {
			return
		}
		if newer := checkForUpdate(version); newer != "" {
			fmt.Printf("\nA newer release is available: %s (you have %s)\n", newer, version)
			fmt.Println("  Update:  appximo upgrade        (or: appximo prompt --install, to hand it to your agent)")
			fmt.Println("  Silence: APPXIMO_NO_VERSION_CHECK=1")
		}
	},
}

func init() {
	versionCmd.Flags().Bool("json", false, "print the version as JSON ({\"version\",\"commit\"}) — never checks for updates")
	versionCmd.Flags().Bool("no-check", false, "do not check whether a newer release exists")
	rootCmd.AddCommand(versionCmd)
}
