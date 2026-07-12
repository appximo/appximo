package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"runtime"

	_ "go.uber.org/automaxprocs"

	"github.com/spf13/cobra"

	"github.com/miguelangel/appitools"
)

// debugTracesHTML is the embedded trace-explorer UI served at /debug/traces.
// It is injected into the engine via appitools.Config.DebugTracesHTML.
//
//go:embed static/debug_traces.html
var debugTracesHTML []byte

// serveCmd is the thin entry point of the PURE binary: it reads flags + env,
// builds an appitools.Config, and runs the engine via the library (New + Start)
// with ZERO custom handlers. The whole engine bootstrap lives in package
// appitools (app.go) so a custom binary boots through the exact same path —
// see examples/custom-handler/main.go for the import-and-Register model.
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Levanta el servidor HTTP multi-tenant",
	Run: func(cmd *cobra.Command, args []string) {
		// GOMAXPROCS is set by the automaxprocs blank import (cgroup-aware). GOGC /
		// GOMEMLIMIT are applied inside appitools.New (applyRuntimeLimits), in the
		// library, so a custom-handler binary gets the same soft memory ceiling.
		log.Printf("GOMAXPROCS=%d NumCPU=%d", runtime.GOMAXPROCS(0), runtime.NumCPU())

		schemaFile, _ := cmd.Flags().GetString("schema")
		port, _ := cmd.Flags().GetInt("port")
		controlPort, _ := cmd.Flags().GetInt("control-port")

		app, err := appitools.New(appitools.Config{
			SchemaPath:      schemaFile,
			Port:            port,
			ControlPort:     controlPort,
			Version:         version,
			DebugTracesHTML: debugTracesHTML,
			// DSN, JWTSecret, AdminKey, Env fall back to DATABASE_URL / JWT_SECRET /
			// ADMIN_KEY / APPITOOLS_ENV inside New.
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
	serveCmd.Flags().Int("control-port", 0, "control-plane port (0 = APPITOOLS_CONTROL_PORT, then 9090)")
	rootCmd.AddCommand(serveCmd)
}
