package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
)

// debugTracesHTML is the embedded trace-explorer UI served at /debug/traces.
// It is injected into the engine via appximo.Config.DebugTracesHTML.
//
//go:embed static/debug_traces.html
var debugTracesHTML []byte

// serveCmd is the thin entry point of the PURE binary: it reads flags + env,
// builds an appximo.Config, and runs the engine via the library (New + Start)
// with ZERO custom handlers. The whole engine bootstrap lives in package
// appximo (app.go) so a custom binary boots through the exact same path —
// see examples/custom-handler/main.go for the import-and-Register model.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the multi-tenant HTTP server",
	// ADR-024: `serve` takes NO positional arguments. It used to accept and
	// silently ignore them, so `appximo serve myapp.json` booted whatever
	// ./schema.json happened to be in the working directory — the operator pointed
	// at one app and the engine served another, with nothing said. The schema is
	// named with --schema; anything else is a mistake worth stopping for.
	Args: func(_ *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("serve takes no positional arguments (got %q) — name the schema with --schema %s", args[0], args[0])
		}
		return nil
	},
	Run: func(cmd *cobra.Command, args []string) {
		// GOMAXPROCS (automaxprocs, cgroup-aware), GOGC and GOMEMLIMIT are all
		// applied inside appximo.New (applyRuntimeLimits) — in the LIBRARY, so a
		// custom-handler binary gets the identical runtime setup, and non-serve
		// CLI commands emit zero stderr noise (C1: PowerShell treats native
		// stderr as an error, so the old per-invocation maxprocs line made
		// every wrapped call look failed on Windows).
		if dotenvLoaded > 0 {
			log.Printf(".env: loaded %d variable(s) from ./.env (existing environment wins on conflict)", dotenvLoaded)
		}

		schemaFile, _ := cmd.Flags().GetString("schema")
		port, _ := cmd.Flags().GetInt("port")
		controlPort, _ := cmd.Flags().GetInt("control-port")

		app, err := appximo.New(appximo.Config{
			SchemaPath:      schemaFile,
			Port:            port,
			ControlPort:     controlPort,
			Version:         version,
			DebugTracesHTML: debugTracesHTML,
			// DSN, JWTSecret, AdminKey, Env fall back to DATABASE_URL / JWT_SECRET /
			// ADMIN_KEY / APPXIMO_ENV inside New.
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if err := app.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

func init() {
	serveCmd.Flags().String("schema", "schema.json", "path to schema.json")
	serveCmd.Flags().Int("port", 8080, "HTTP port to listen on")
	serveCmd.Flags().Int("control-port", 0, "control-plane port (0 = APPXIMO_CONTROL_PORT, then 9090)")
	rootCmd.AddCommand(serveCmd)
}
