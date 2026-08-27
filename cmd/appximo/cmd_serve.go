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
	// C3 (FIELD-FEEDBACK-S1): the env contract lives in the help — it used to
	// be discoverable only by failing.
	Long: `Start the multi-tenant HTTP server (foreground; Ctrl+C to stop).

Three environment variables are REQUIRED — serve refuses to boot without them
and names every missing one:

  DATABASE_URL   postgres://user:pass@host:5432/dbname
  JWT_SECRET     32+ random characters   (generate: appximo gen-secret)
  ADMIN_KEY      the operator credential (generate: appximo gen-secret --bytes 16)

A .env file in the working directory is loaded automatically (KEY=value per
line; the real environment wins on conflict). Two listeners: the API on
--port (public) and the tenant-registration control plane on --control-port
(keep it internal).

Serve your own frontend from this same binary with --static (same origin, no
CORS, the engine's SPA Content-Security-Policy):

  appximo serve --schema schema.json --static ./web/build --spa

--static is [urlpath=]dir and repeats (a bare dir mounts at "/"; "/site=./dist"
mounts a sub-path); --spa serves index.html for unmatched client routes.
Environment equivalents for systemd/Docker: APPXIMO_STATIC_DIR (comma-
separated specs), APPXIMO_STATIC_SPA, APPXIMO_STATIC_CSP (a verbatim policy,
or "off").

Optional env: APPXIMO_ENV, APPXIMO_AUTH_SIGNUP_ROLE,
APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE (default 5 — the brute-force guard;
raise it only for a shared read-only demo identity), APPXIMO_CORS_ORIGINS,
APPXIMO_FILES_DIR, GOMEMLIMIT — the full table lives in
docs/PRODUCTION.md; 'appximo quickstart' prints the operations contract.`,
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
		staticSpecs, _ := cmd.Flags().GetStringArray("static")
		spa, _ := cmd.Flags().GetBool("spa")

		// PUBLIC-SURFACE-S1 Part A: the distributed binary reaches Config.Static —
		// the same seam, same validation, same CSP as a Go consumer's mounts.
		staticMounts, err := appximo.ParseStaticSpecs(staticSpecs, spa)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		app, err := appximo.New(appximo.Config{
			SchemaPath:      schemaFile,
			Port:            port,
			ControlPort:     controlPort,
			Version:         version,
			DebugTracesHTML: debugTracesHTML,
			Static:          staticMounts,
			// DSN, JWTSecret, AdminKey, Env fall back to DATABASE_URL / JWT_SECRET /
			// ADMIN_KEY / APPXIMO_ENV inside New. An empty Static falls back to
			// APPXIMO_STATIC_DIR / _SPA / _CSP inside New too.
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
	serveCmd.Flags().StringArray("static", nil, "serve a frontend from this binary: [urlpath=]dir (repeatable; bare dir mounts at \"/\"; env: APPXIMO_STATIC_DIR)")
	serveCmd.Flags().Bool("spa", false, "client-side-routing fallback for --static mounts: unmatched paths serve index.html (env: APPXIMO_STATIC_SPA)")
	rootCmd.AddCommand(serveCmd)
}
