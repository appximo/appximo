package appitools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/miguelangel/appitools/pkg/fleet"
	"github.com/miguelangel/appitools/pkg/shutdown"
)

// ServeFleet is the MT-STRUCT-S3 Option-B runtime: N DISTINCT apps compiled
// and served from ONE process, dispatched by Host through the S2 Registry.
//
// Each app is a full *App instance — its own schema-compiled router, GraphQL,
// OpenAPI, pgx pool (its OWN database), response cache, SSE hub, rate limiter,
// observability stack and control-plane listener — with its middleware chain
// CLOSED OVER its own config: its JWT secret, its RBAC policy, its admin key.
//
// The security ordering the design demands (app resolved BEFORE the JWT is
// validated, with THAT app's secret) holds by construction: the Registry
// resolves the Host to an app and only then does that app's chain — whose JWT
// middleware knows only that app's secret and whose RBAC middleware knows only
// that app's policy — run. A per-request "app from context" indirection was
// evaluated and rejected: N independent closure chains give the same property
// with ZERO added per-request work and a smaller blast radius (there is no
// shared auth state to mis-route; the one piece of cross-app shared state —
// the package-level claims cache — is keyed by (secret, token) since S3).
//
// Unmatched Hosts do NOT fall into an arbitrary app (the single-app default
// would be a cross-app hole here): they get a process-level handler serving
// only the health probes and a clean 404 — the same contract as the fleet
// proxy (S1).
//
// Deploy semantics in S3: `POST /admin/engine/schema` on an app persists THAT
// app's boot schema file and gracefully restarts the WHOLE process (all apps,
// ~6 s) — honest and safe; the per-app hot-swap without process restart is S4.
func ServeFleet(mf *fleet.Manifest, version string, debugTracesHTML []byte) error {
	listenPort, err := portOfListen(mf.Listen)
	if err != nil {
		return fmt.Errorf("fleet serve: listen %q: %w", mf.Listen, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Build every app BEFORE serving anything: a fleet with a broken app fails
	// boot loudly (same load-fails-loud contract as the single-app engine).
	apps := make([]*App, 0, len(mf.Apps))
	domains := map[string]*compiledApp{}
	for i := range mf.Apps {
		spec := &mf.Apps[i]
		env := spec.MergedEnv()

		// Provision the app's database (control-plane DDL, idempotent) — the
		// same bootstrap the multi-process fleet applies before spawning.
		if err := fleet.BootstrapControlPlane(ctx, env["DATABASE_URL"]); err != nil {
			closeAll(apps)
			return fmt.Errorf("fleet serve: app %q: bootstrap database: %w", spec.Name, err)
		}

		cport := spec.ControlPort
		if cport == 0 {
			if cport, err = freePort(); err != nil {
				closeAll(apps)
				return fmt.Errorf("fleet serve: app %q: control port: %w", spec.Name, err)
			}
		}

		appDir := filepath.Join(mf.DataDir, spec.Name)
		cfg := Config{
			SchemaPath: spec.Schema,
			// Per-app config from the manifest env (the S1 contract: DATABASE_URL,
			// JWT_SECRET, ADMIN_KEY required and per-app; JWT_SECRET uniqueness is
			// enforced at manifest load).
			DSN:         env["DATABASE_URL"],
			JWTSecret:   env["JWT_SECRET"],
			AdminKey:    env["ADMIN_KEY"],
			Port:        listenPort, // shared data plane (informational: canary/log URLs)
			ControlPort: cport,
			Env:         coalesce(env["APPITOOLS_ENV"], os.Getenv("APPITOOLS_ENV")),
			// Per-app state that must NOT be shared between apps.
			ObsDBPath: coalesce(env["OBS_DB_PATH"], filepath.Join(appDir, "obs.db")),
			FilesDir:  coalesce(env["APPITOOLS_FILES_DIR"], filepath.Join(appDir, "files")),
			// Optional per-app auth config (Config fields; other APPITOOLS_* env
			// entries in the manifest are process-wide in-process — warned below).
			AuthSignupRole:  env["APPITOOLS_AUTH_SIGNUP_ROLE"],
			Version:         version,
			DebugTracesHTML: debugTracesHTML,
		}
		os.MkdirAll(appDir, 0o755) //nolint:errcheck
		warnUnmappedEnv(spec.Name, env)

		app, err := New(cfg)
		if err != nil {
			closeAll(apps)
			return fmt.Errorf("fleet serve: app %q: %w", spec.Name, err)
		}
		app.noSynthetic = true
		apps = append(apps, app)

		entry := &compiledApp{name: spec.Name, handler: nil} // handler set below (after background start)
		for _, d := range spec.Domains {
			domains[d] = entry
		}
		log.Printf("fleet serve: app %q compiled — schema %s, control :%d, domains %v",
			spec.Name, spec.Schema, cport, spec.Domains)
	}

	// Start services + build routers only after every app constructed.
	for i, app := range apps {
		app.startBackground(ctx)
		router := app.buildRouter()
		for _, d := range mf.Apps[i].Domains {
			domains[d].handler = router
		}
	}

	// Process-level state for the shared listener + the unmatched-Host handler.
	fleetSS := shutdown.New()
	reg := NewRegistry(unmatchedApp(fleetSS, version, len(apps)), domains)

	srv := &http.Server{
		Addr:              mf.Listen,
		Handler:           reg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("Appitools fleet serving %d app(s) IN-PROCESS on %s — Ctrl+C to stop\n", len(apps), mf.Listen)
	cleanup := func() {
		for _, app := range apps {
			app.cleanup()
		}
	}
	if err := fleetSS.Run(ctx, srv, 5*time.Second, cleanup); err != nil {
		return err
	}
	log.Println("fleet serve: shut down cleanly")

	// A deploy-triggered self-restart on ANY app relaunches the whole fleet
	// with the persisted schema files (S3 semantics; per-app swap is S4).
	for i, app := range apps {
		if app.restartRequested.Load() {
			execRestartProcess("fleet relaunch after deploy to app " + mf.Apps[i].Name)
		}
	}
	return nil
}

// unmatchedApp is the process-level default for Hosts no app domain claims.
// UNLIKE the single-app default (S2: everything → the one app), a multi-app
// process must never route an unclaimed Host into an arbitrary app — that
// would hand app X's traffic to app Y on a DNS/Host mistake. It serves ONLY
// the process health probes (so supervisors/LBs polling a bare IP keep
// working) and answers everything else with the fleet proxy's clean 404.
func unmatchedApp(ss *shutdown.State, version string, nApps int) *compiledApp {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", ss.HealthzHandler)
	mux.HandleFunc("/readyz", ss.ReadyzHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"status": "ok", "version": version, "fleet_apps": nApps,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"unknown app domain"}` + "\n")) //nolint:errcheck
	})
	return &compiledApp{name: "__unmatched__", handler: mux}
}

// warnUnmappedEnv flags manifest env keys that CANNOT be applied per-app in
// the in-process runtime (they would be process-wide): everything except the
// keys ServeFleet maps into Config. Loud, so an operator relying on, say, a
// per-app APPITOOLS_CORS_ORIGINS knows it did not take effect.
func warnUnmappedEnv(app string, env map[string]string) {
	mapped := map[string]bool{
		"DATABASE_URL": true, "JWT_SECRET": true, "ADMIN_KEY": true,
		"OBS_DB_PATH": true, "APPITOOLS_FILES_DIR": true,
		"APPITOOLS_AUTH_SIGNUP_ROLE": true, "APPITOOLS_ENV": true,
	}
	for k := range env {
		if !mapped[k] {
			log.Printf("fleet serve: WARNING: app %q env %s is NOT applied in-process (process-wide setting; use `fleet run` for full per-app env isolation)", app, k)
		}
	}
}

func closeAll(apps []*App) {
	for _, app := range apps {
		app.cleanup()
	}
}

// portOfListen extracts the numeric port of a listen address like ":8080".
func portOfListen(listen string) (int, error) {
	_, p, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(p)
}

// freePort asks the kernel for an unused TCP port (per-app control planes).
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck
	return l.Addr().(*net.TCPAddr).Port, nil
}
