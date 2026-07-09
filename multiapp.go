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
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/miguelangel/appitools/pkg/fleet"
	"github.com/miguelangel/appitools/pkg/schema"
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
		app, err := buildFleetApp(ctx, mf, spec, listenPort, version, debugTracesHTML)
		if err != nil {
			closeAll(apps)
			return fmt.Errorf("fleet serve: %w", err)
		}
		apps = append(apps, app)

		entry := &compiledApp{name: spec.Name, handler: nil} // handler set below (after background start)
		for _, d := range spec.Domains {
			domains[d] = entry
		}
		log.Printf("fleet serve: app %q compiled — schema %s, control :%d, domains %v",
			spec.Name, spec.Schema, app.cfg.ControlPort, spec.Domains)
	}

	// Start services + build routers only after every app constructed. Each app
	// gets its OWN cancelable context (FLEET-LIFECYCLE-S1) so a hot remove can
	// stop just that app's background services.
	cancels := make([]context.CancelFunc, len(apps))
	for i, app := range apps {
		appCtx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		app.startBackground(appCtx)
		router := app.buildRouter(app.bootSurface())
		for _, d := range mf.Apps[i].Domains {
			domains[d].handler = router
		}
	}

	// The unified fleet console (MT-STRUCT-S5): the fleet-operator's read-only
	// view over all apps, mounted on the process-level handler below. Disabled
	// (uniform 404) unless the manifest/env provides an operator key.
	panel := &fleetPanel{operatorKey: mf.OperatorKey, version: version}
	for i := range apps {
		panel.apps = append(panel.apps, &panelApp{
			name:        mf.Apps[i].Name,
			domains:     mf.Apps[i].Domains,
			schemaName:  apps[i].schema.Name,
			controlPort: apps[i].cfg.ControlPort,
			app:         apps[i],
			cancel:      cancels[i],
		})
	}
	if mf.OperatorKey != "" {
		log.Println("fleet serve: unified fleet console enabled at /fleet (fleet-operator key required; process-level Host)")
	} else {
		log.Println("fleet serve: unified fleet console DISABLED (set operator_key in the manifest or APPITOOLS_FLEET_OPERATOR_KEY to enable /fleet)")
	}

	// Process-level state for the shared listener + the unmatched-Host handler.
	fleetSS := shutdown.New()
	reg := NewRegistry(unmatchedApp(fleetSS, version, len(apps), panel), domains)

	// Wire per-app HOT-SWAP (MT-STRUCT-S4): a deploy on app X
	// (POST /admin/engine/schema) recompiles X's router from its newly-persisted
	// schema and atomically swaps X's registry entries — the process is NOT
	// restarted and the OTHER apps are untouched. Each app's swaps are
	// serialized by its own mutex (deploys are rare; the read path never takes
	// it). The whole-process re-exec (S3) is gone for fleet-serve.
	for i := range apps {
		wireHotSwap(reg, apps[i], mf.Apps[i].Name, mf.Apps[i].Domains, panel.apps[i])
	}

	// The app LIFECYCLE (FLEET-LIFECYCLE-S1): hot add/remove of whole apps —
	// the registry's AddApp/RemoveApp (S4) finally exposed. Reached through the
	// operator-gated console API (fleetpanel.go) and `appitools fleet
	// add/remove`; every operation keeps the MANIFEST in sync (persistent
	// source) with the REGISTRY (live state) — no drift.
	panel.lifecycle = &fleetLifecycle{
		ctx: ctx, mf: mf, reg: reg, panel: panel,
		listenPort: listenPort, version: version, debugTracesHTML: debugTracesHTML,
	}

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
	// No post-drain re-exec here: a fleet-serve deploy is a per-app HOT-SWAP
	// (MT-STRUCT-S4), not a process restart. The process only exits on SIGINT/
	// SIGTERM (operator/supervisor), never on a deploy.
	return nil
}

// unmatchedApp is the process-level default for Hosts no app domain claims.
// UNLIKE the single-app default (S2: everything → the one app), a multi-app
// process must never route an unclaimed Host into an arbitrary app — that
// would hand app X's traffic to app Y on a DNS/Host mistake. It serves ONLY
// the process health probes (so supervisors/LBs polling a bare IP keep
// working), the operator-gated fleet console (S5, uniform-404 without the
// key), and answers everything else with the fleet proxy's clean 404.
func unmatchedApp(ss *shutdown.State, version string, nApps int, panel *fleetPanel) *compiledApp {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", ss.HealthzHandler)
	mux.HandleFunc("/readyz", ss.ReadyzHandler)
	if panel != nil {
		panel.register(mux)
	}
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

// buildFleetApp compiles ONE manifest app into a ready *App — the per-app slice
// of ServeFleet's boot, extracted (FLEET-LIFECYCLE-S1) so a HOT ADD builds the
// new app through the exact same path as boot: database bootstrap, per-app
// config (own DSN/secret/admin key/state dirs), control port, New().
func buildFleetApp(ctx context.Context, mf *fleet.Manifest, spec *fleet.AppSpec, listenPort int, version string, debugTracesHTML []byte) (*App, error) {
	env := spec.MergedEnv()

	// Provision the app's database (control-plane DDL, idempotent) — the
	// same bootstrap the multi-process fleet applies before spawning.
	if err := fleet.BootstrapControlPlane(ctx, env["DATABASE_URL"]); err != nil {
		return nil, fmt.Errorf("app %q: bootstrap database: %w", spec.Name, err)
	}

	cport := spec.ControlPort
	if cport == 0 {
		var err error
		if cport, err = freePort(); err != nil {
			return nil, fmt.Errorf("app %q: control port: %w", spec.Name, err)
		}
	}

	appDir := filepath.Join(mf.DataDir, spec.Name)
	cfg := Config{
		SchemaPath: spec.Schema,
		// Per-app config from the manifest env (the S1 contract: DATABASE_URL,
		// JWT_SECRET, ADMIN_KEY required and per-app; JWT_SECRET uniqueness is
		// enforced at manifest load / ValidateNewApp).
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
		return nil, fmt.Errorf("app %q: %w", spec.Name, err)
	}
	app.noSynthetic = true
	return app, nil
}

// wireHotSwap installs the per-app S4 hot-swap closure: a deploy on this app
// recompiles its router and atomically swaps its registry entries — no process
// restart, other apps untouched. Extracted so a hot-ADDED app gets the exact
// same deploy semantics as a boot app.
func wireHotSwap(reg *Registry, app *App, name string, appDomains []string, pa *panelApp) {
	var swapMu sync.Mutex
	app.hotSwap = func() {
		swapMu.Lock()
		defer swapMu.Unlock()
		router, err := app.rebuildRouter()
		if err != nil {
			log.Printf("fleet serve: app %q hot-swap FAILED — keeping the current surface live: %v", name, err)
			return
		}
		reg.SwapApp(appDomains, &compiledApp{name: name, handler: router})
		pa.swaps.Add(1)
		log.Printf("fleet serve: app %q hot-swapped — new schema live, process untouched, other apps undisturbed", name)
	}
}

// ── the app lifecycle (FLEET-LIFECYCLE-S1) ───────────────────────────────────

// fleetLifecycle performs hot add/remove of WHOLE apps on the live in-process
// fleet, exposing the registry's AddApp/RemoveApp (built in S4, unwired until
// now). Every operation keeps the two states coherent:
//
//   - the MANIFEST (fleet.json) is the persistent source of truth — it is
//     updated FIRST (atomic file replace), so a fleet restart always reloads
//     the post-operation composition;
//   - the REGISTRY is the live state — the copy-on-write AddApp/RemoveApp
//     publishes the change without touching any other app's entry, and the
//     request hot path (Resolve) is never locked.
//
// Remove semantics (deliberate, documented): removing an app takes it out of
// the fleet — its domains stop serving (clean 404) and its background services
// stop — but its DATABASE IS NEVER TOUCHED. The data outlives the membership;
// re-adding the app with the same DATABASE_URL brings it back intact. Dropping
// data is a separate, deliberate act outside the fleet's vocabulary.
type fleetLifecycle struct {
	mu              sync.Mutex // serializes add/remove (rare, operator-driven)
	ctx             context.Context
	mf              *fleet.Manifest
	reg             *Registry
	panel           *fleetPanel
	listenPort      int
	version         string
	debugTracesHTML []byte
}

// lifecycleError distinguishes the caller's fault (bad spec/schema → 4xx with
// the actionable message) from a server-side failure (5xx).
type lifecycleError struct {
	status int
	msg    string
	report *schema.ValidationReport // set when the schema failed validation
}

func (e *lifecycleError) Error() string { return e.msg }

// AddApp validates and hot-adds one app: schema through the REAL validator
// (ValidateReport — the same oracle as everywhere), the manifest rules
// (unique name, unclaimed domains, per-app JWT_SECRET) via ValidateNewApp,
// then manifest persist → compile → serve. rawSchema, when non-nil, is an
// inline schema document (the console's paste path — JSON-EDITOR's Code view
// output lands here); it is persisted under the fleet's data dir and the
// manifest references that file. Otherwise spec.Schema names a readable file.
func (l *fleetLifecycle) AddApp(spec *fleet.AppSpec, rawSchema []byte) (*panelAppJSON, *lifecycleError) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(spec.Name) {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("app name %q must match ^[a-z][a-z0-9_-]*$", spec.Name)}
	}

	// The schema goes through the SAME validator as ai-generate / the Code
	// view / deploy — an invalid schema is a 422 with the located report,
	// never a half-added app.
	if rawSchema != nil {
		if rep := schema.ValidateReport(rawSchema); !rep.Valid {
			return nil, &lifecycleError{status: http.StatusUnprocessableEntity, msg: "schema is invalid", report: &rep}
		}
		dir := filepath.Join(l.mf.DataDir, spec.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist schema: " + err.Error()}
		}
		path := filepath.Join(dir, "schema.json")
		if err := os.WriteFile(path, rawSchema, 0o644); err != nil {
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist schema: " + err.Error()}
		}
		spec.Schema = path
	} else {
		raw, err := os.ReadFile(spec.Schema)
		if err != nil {
			return nil, &lifecycleError{status: http.StatusBadRequest, msg: "read schema: " + err.Error()}
		}
		if rep := schema.ValidateReport(raw); !rep.Valid {
			return nil, &lifecycleError{status: http.StatusUnprocessableEntity, msg: "schema is invalid", report: &rep}
		}
	}

	// The manifest rules — the same ones a fleet restart would enforce, so a
	// hot add can never create a composition the next boot refuses.
	if err := l.mf.ValidateNewApp(spec); err != nil {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: err.Error()}
	}

	// Compile the app through the SAME path as boot (no routing side effects yet).
	app, err := buildFleetApp(l.ctx, l.mf, spec, l.listenPort, l.version, l.debugTracesHTML)
	if err != nil {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: err.Error()}
	}

	// Persist FIRST (the manifest is the source of truth); only then go live.
	if path := l.mf.Path(); path != "" {
		if err := fleet.AddAppToManifestFile(path, spec); err != nil {
			app.cleanup()
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist manifest: " + err.Error()}
		}
	}
	l.mf.Apps = append(l.mf.Apps, *spec)

	appCtx, cancel := context.WithCancel(l.ctx)
	app.startBackground(appCtx)
	router := app.buildRouter(app.bootSurface())

	pa := &panelApp{
		name:        spec.Name,
		domains:     spec.Domains,
		schemaName:  app.schema.Name,
		controlPort: app.cfg.ControlPort,
		app:         app,
		cancel:      cancel,
	}
	wireHotSwap(l.reg, app, spec.Name, spec.Domains, pa)

	// The hot add itself: S4's copy-on-write AddApp — the new domains start
	// routing, every other app's entry copied byte-identical, reads lock-free.
	l.reg.AddApp(spec.Domains, &compiledApp{name: spec.Name, handler: router})
	l.panel.addApp(pa)

	log.Printf("fleet serve: app %q hot-ADDED — domains %v serving, other apps untouched, manifest persisted", spec.Name, spec.Domains)
	row := l.panel.appRow(pa)
	return &row, nil
}

// removeGrace is how long a removed app keeps its infra (pool, control plane)
// alive after its domains stop routing — in-flight requests that resolved the
// old registry map finish cleanly; long-lived SSE streams on a REMOVED app are
// deliberately cut when the grace ends.
const removeGrace = 10 * time.Second

// RemoveApp hot-removes the app named name. confirm must equal the app name
// (the destructive-action gate — same contract as tenant delete). The app's
// database is NOT touched (see the type comment).
func (l *fleetLifecycle) RemoveApp(name, confirm string) (*lifecycleError, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if confirm != name {
		return &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("confirm must be the app name %q — removing takes the app out of the fleet (its database is NOT deleted)", name)}, nil
	}
	spec := l.mf.AppByName(name)
	pa := l.panel.byName(name)
	if spec == nil || pa == nil {
		return &lifecycleError{status: http.StatusNotFound, msg: fmt.Sprintf("no app named %q in the fleet", name)}, nil
	}

	// Persist FIRST (source of truth), then unroute live.
	if path := l.mf.Path(); path != "" {
		if err := fleet.RemoveAppFromManifestFile(path, name); err != nil {
			return &lifecycleError{status: http.StatusInternalServerError, msg: "persist manifest: " + err.Error()}, nil
		}
	}
	for i := range l.mf.Apps {
		if l.mf.Apps[i].Name == name {
			l.mf.Apps = append(l.mf.Apps[:i], l.mf.Apps[i+1:]...)
			break
		}
	}

	domains := spec.Domains
	l.reg.RemoveApp(domains) // hosts fall to the process-level 404, other apps untouched
	l.panel.removeApp(name)

	// Stop the app's background services now; release its infra after the
	// grace so in-flight requests on the old surface finish.
	app := pa.app
	if pa.cancel != nil {
		pa.cancel()
	}
	time.AfterFunc(removeGrace, func() {
		sctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		app.cpSrv.Shutdown(sctx) //nolint:errcheck
		app.cleanup()
		log.Printf("fleet serve: app %q resources released (%s after removal)", name, removeGrace)
	})

	log.Printf("fleet serve: app %q hot-REMOVED — domains %v now 404, other apps untouched, manifest persisted; its database was NOT deleted", name, domains)
	return nil, domains
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
