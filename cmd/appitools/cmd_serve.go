package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"

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
		// GOGC / GOMEMLIMIT from env (process-level knobs, set before the engine).
		if v := os.Getenv("GOGC"); v != "" {
			if pct, err := strconv.Atoi(v); err == nil {
				debug.SetGCPercent(pct)
			}
		}
		if v := os.Getenv("GOMEMLIMIT"); v != "" {
			if bytes, err := parseMemLimit(v); err == nil {
				debug.SetMemoryLimit(bytes)
			}
		}
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

// parseMemLimit parses strings like "512MiB", "1GiB", "1073741824" into bytes.
func parseMemLimit(s string) (int64, error) {
	units := map[string]int64{
		"GiB": 1 << 30, "MiB": 1 << 20, "KiB": 1 << 10,
		"GB": 1e9, "MB": 1e6, "KB": 1e3,
	}
	for suffix, mult := range units {
		if strings.HasSuffix(s, suffix) {
			n, err := strconv.ParseInt(strings.TrimSuffix(s, suffix), 10, 64)
			if err != nil {
				return 0, err
			}
			return n * mult, nil
		}
	}
	return strconv.ParseInt(s, 10, 64)
}
