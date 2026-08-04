package appximo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/fleet"
	"github.com/appximo/appximo/pkg/platformadmin"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/shutdown"
	"github.com/appximo/appximo/pkg/userauth"
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
	panel := &fleetPanel{operatorKey: mf.OperatorKey, version: version, operatorAdminEmail: mf.OperatorAdminEmail}
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
		log.Println("fleet serve: unified fleet console DISABLED (set operator_key in the manifest or APPXIMO_FLEET_OPERATOR_KEY to enable /fleet)")
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
	// operator-gated console API (fleetpanel.go) and `appximo fleet
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

	// ENG-34: bind first, announce after — same contract as the single app.
	ln, err := shutdown.Listen(mf.Listen)
	if err != nil {
		return fmt.Errorf("fleet serve: cannot listen on %s: %w", mf.Listen, err)
	}
	fmt.Printf("Appximo fleet serving %d app(s) IN-PROCESS on %s — Ctrl+C to stop\n", len(apps), mf.Listen)
	cleanup := func() {
		for _, app := range apps {
			app.cleanup()
		}
	}
	if err := fleetSS.Serve(ctx, srv, ln, 5*time.Second, cleanup); err != nil {
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

// fleetMappedEnv is the authoritative set of per-app env keys the in-process
// runtime applies through Config (FLEET-CONSOLE-S2 closed the S1 gap: CORS,
// OAuth, MFA, files backend and the auth knobs are now per-app too — every
// engine setting that exists as a Config field). Keys OUTSIDE this set are
// process-wide in `fleet serve` and warned loudly below (today that is the
// process-level infra: RATE_LIMIT_*, DB_MAX_CONNS, GOMAXPROCS, REDIS_URL,
// SLACK_WEBHOOK_URL — use `fleet run` if an app needs those isolated).
var fleetMappedEnv = map[string]bool{
	"DATABASE_URL": true, "JWT_SECRET": true, "ADMIN_KEY": true,
	"OBS_DB_PATH": true, "APPXIMO_ENV": true,
	// files (FILES-V1/V2): local root + the whole S3 backend, per app.
	"APPXIMO_FILES_DIR": true, "APPXIMO_FILES_BACKEND": true,
	"APPXIMO_FILES_S3_BUCKET": true, "APPXIMO_FILES_S3_ENDPOINT": true,
	"APPXIMO_FILES_S3_REGION": true, "APPXIMO_FILES_S3_ACCESS_KEY": true,
	"APPXIMO_FILES_S3_SECRET_KEY": true, "APPXIMO_FILES_S3_FORCE_PATH_STYLE": true,
	"APPXIMO_FILES_S3_PREFIX": true, "APPXIMO_FILES_S3_SERVE": true,
	"APPXIMO_FILES_TOKEN_TTL": true, "APPXIMO_FILES_ALLOWED_EXT": true,
	// auth-as-product knobs, per app.
	"APPXIMO_AUTH_SIGNUP_ROLE": true, "APPXIMO_AUTH_MIN_PASSWORD": true,
	"APPXIMO_AUTH_REQUIRE_VERIFIED": true, "APPXIMO_AUTH_BASE_URL": true,
	// OAuth social login, per app (each app is a product with its own providers).
	"APPXIMO_OAUTH_CALLBACK_URL": true, "APPXIMO_OAUTH_DEFAULT_ROLE": true,
	"APPXIMO_OAUTH_SUCCESS_REDIRECT": true,
	// MFA key material + issuer label, per app.
	"APPXIMO_MFA_KEY": true, "APPXIMO_MFA_ISSUER": true,
	// CORS, per app.
	"APPXIMO_CORS_ORIGINS": true, "APPXIMO_CORS_METHODS": true,
	"APPXIMO_CORS_HEADERS": true, "APPXIMO_CORS_EXPOSE_HEADERS": true,
	"APPXIMO_CORS_CREDENTIALS": true, "APPXIMO_CORS_MAX_AGE": true,
	// GraphQL explorer opt-in (GRAPHQL-EXPLORER-S1), per app — one app in the
	// fleet can expose GraphiQL/introspection for exploration while a sibling
	// stays locked down, without either needing the broader APPXIMO_ENV.
	"APPXIMO_GRAPHQL_PLAYGROUND": true,
}

// warnUnmappedEnv flags manifest env keys that CANNOT be applied per-app in
// the in-process runtime (they would be process-wide): everything except
// fleetMappedEnv. Loud, so an operator relying on a per-app RATE_LIMIT_RPS
// knows it did not take effect. (The OAuth provider client ids/secrets are
// read per provider at boot — mapped via their APPXIMO_OAUTH_* prefix.)
func warnUnmappedEnv(app string, env map[string]string) {
	for k := range env {
		if fleetMappedEnv[k] || strings.HasPrefix(k, "APPXIMO_OAUTH_") {
			continue
		}
		log.Printf("fleet serve: WARNING: app %q env %s is NOT applied in-process (process-wide setting; use `fleet run` for full per-app env isolation)", app, k)
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
		Env:         coalesce(env["APPXIMO_ENV"], os.Getenv("APPXIMO_ENV")),
		// Per-app state that must NOT be shared between apps.
		ObsDBPath: coalesce(env["OBS_DB_PATH"], filepath.Join(appDir, "obs.db")),
		FilesDir:  coalesce(env["APPXIMO_FILES_DIR"], filepath.Join(appDir, "files")),
		// The app's own hostnames: a request to the BARE domain carries no
		// tenant label — without this, "erp.example.com" recorded a phantom
		// tenant "erp" in observability (FLEET-CONSOLE-S2 fix b).
		BareDomains: spec.Domains,
		// The FULL per-app surface (FLEET-CONSOLE-S2 fix a): every engine
		// setting that exists as a Config field is applied per app — files
		// backend, auth knobs, OAuth, MFA, CORS. Keys outside fleetMappedEnv
		// stay process-wide and are warned below.
		FilesBackend:          env["APPXIMO_FILES_BACKEND"],
		FilesS3Bucket:         env["APPXIMO_FILES_S3_BUCKET"],
		FilesS3Endpoint:       env["APPXIMO_FILES_S3_ENDPOINT"],
		FilesS3Region:         env["APPXIMO_FILES_S3_REGION"],
		FilesS3AccessKey:      env["APPXIMO_FILES_S3_ACCESS_KEY"],
		FilesS3SecretKey:      env["APPXIMO_FILES_S3_SECRET_KEY"],
		FilesS3ForcePathStyle: envTruthy(env["APPXIMO_FILES_S3_FORCE_PATH_STYLE"]),
		FilesS3Prefix:         env["APPXIMO_FILES_S3_PREFIX"],
		FilesS3ServeMode:      env["APPXIMO_FILES_S3_SERVE"],
		FilesTokenTTLSeconds:  envInt(env["APPXIMO_FILES_TOKEN_TTL"]),
		FilesAllowedExt:       envList(env["APPXIMO_FILES_ALLOWED_EXT"]),
		AuthSignupRole:        env["APPXIMO_AUTH_SIGNUP_ROLE"],
		AuthMinPasswordLength: envInt(env["APPXIMO_AUTH_MIN_PASSWORD"]),
		AuthRequireVerified:   envTruthy(env["APPXIMO_AUTH_REQUIRE_VERIFIED"]),
		AuthBaseURL:           env["APPXIMO_AUTH_BASE_URL"],
		OAuthCallbackURL:      env["APPXIMO_OAUTH_CALLBACK_URL"],
		OAuthDefaultRole:      env["APPXIMO_OAUTH_DEFAULT_ROLE"],
		OAuthSuccessRedirect:  env["APPXIMO_OAUTH_SUCCESS_REDIRECT"],
		OAuthProviders:        oauthProvidersFromEnv(env),
		MFAKey:                env["APPXIMO_MFA_KEY"],
		MFAIssuer:             env["APPXIMO_MFA_ISSUER"],
		CORSAllowedOrigins:    envList(env["APPXIMO_CORS_ORIGINS"]),
		CORSAllowedMethods:    envList(env["APPXIMO_CORS_METHODS"]),
		CORSAllowedHeaders:    envList(env["APPXIMO_CORS_HEADERS"]),
		CORSExposedHeaders:    envList(env["APPXIMO_CORS_EXPOSE_HEADERS"]),
		CORSAllowCredentials:  envTruthy(env["APPXIMO_CORS_CREDENTIALS"]),
		CORSMaxAge:            envInt(env["APPXIMO_CORS_MAX_AGE"]),
		GraphQLPlayground:     envTruthy(coalesce(env["APPXIMO_GRAPHQL_PLAYGROUND"], os.Getenv("APPXIMO_GRAPHQL_PLAYGROUND"))),
		Version:               version,
		DebugTracesHTML:       debugTracesHTML,
	}
	os.MkdirAll(appDir, 0o755) //nolint:errcheck
	warnUnmappedEnv(spec.Name, env)

	app, err := New(cfg)
	if err != nil {
		return nil, fmt.Errorf("app %q: %w", spec.Name, err)
	}
	app.noSynthetic = true

	// Unified operator identity (FLEET-CONSOLE-S2): when the manifest declares
	// an operator admin, ensure a platform super-admin with those credentials
	// exists in THIS app's database — so ONE login works on every app's /admin.
	// Idempotent: an existing account (same email) is left untouched. Isolation
	// holds: each app still has its own admin row, own DB, own tokens; this
	// only removes the "N apps = N credential sets" friction.
	if email, pass := mf.OperatorAdmin(); email != "" {
		if err := ensureOperatorAdmin(ctx, app, email, pass); err != nil {
			log.Printf("fleet serve: WARNING: app %q: operator admin %q not provisioned: %v", spec.Name, email, err)
		}
	}
	return app, nil
}

// envInt parses a positive int; 0 (the Config zero → fall back) on empty/bad.
func envInt(v string) int {
	n, _ := strconv.Atoi(v)
	if n < 0 {
		return 0
	}
	return n
}

// envList splits a comma-separated env value into trimmed entries; nil on empty.
func envList(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// oauthProvidersFromEnv builds the per-app social-login provider set from the
// app's manifest env. Nil when the app configures no provider (Config then
// falls back to the process env, preserving single-engine behavior).
func oauthProvidersFromEnv(env map[string]string) map[string]userauth.OAuthProviderConfig {
	out := map[string]userauth.OAuthProviderConfig{}
	for _, p := range []string{"google", "github", "microsoft"} {
		up := strings.ToUpper(p)
		id, secret := env["APPXIMO_OAUTH_"+up+"_CLIENT_ID"], env["APPXIMO_OAUTH_"+up+"_CLIENT_SECRET"]
		if id != "" || secret != "" {
			out[p] = userauth.OAuthProviderConfig{ClientID: id, ClientSecret: secret}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ensureOperatorAdmin provisions the fleet-wide operator identity in one app's
// database (platform super-admin, appximo_system schema) if absent.
func ensureOperatorAdmin(ctx context.Context, app *App, email, password string) error {
	store := platformadmin.NewStore(app.pool)
	if _, err := store.GetAdminByEmail(ctx, email); err == nil {
		return nil // already provisioned — never overwrite an existing account
	} else if !errors.Is(err, platformadmin.ErrAdminNotFound) {
		return err
	}
	svc := platformadmin.NewService(store, nil, controlplane.NewService(app.pool, nil), app.pool,
		platformadmin.Config{JWTSecret: app.cfg.JWTSecret, MFAKey: app.cfg.MFAKey})
	_, err := svc.CreateAdmin(ctx, email, password, "")
	if err == nil {
		log.Printf("fleet serve: operator admin %q provisioned in app's admin panel (one login for every app)", email)
	}
	return err
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

// dbProvision carries the console's DB-assist choice (FLEET-DB-ASSIST): use a
// DECLARED instance for the app's DATABASE_URL instead of a raw DSN, optionally
// creating the database. nil ⇒ the DSN came in spec.Env verbatim (manual path).
type dbProvision struct {
	instance string
	dbName   string
	create   bool
}

// AddApp validates and hot-adds one app: schema through the REAL validator
// (ValidateReport — the same oracle as everywhere), the manifest rules
// (unique name, unclaimed domains, per-app JWT_SECRET) via ValidateNewApp,
// then manifest persist → compile → serve. rawSchema, when non-nil, is an
// inline schema document (the console's paste path — JSON-EDITOR's Code view
// output lands here); it is persisted under the fleet's data dir and the
// manifest references that file. Otherwise spec.Schema names a readable file.
//
// db (FLEET-DB-ASSIST), when non-nil, resolves DATABASE_URL from a DECLARED
// instance (never from a browser-supplied DSN) and may CREATE the database —
// all-or-nothing: a database this call creates fresh is dropped again if the
// app-add then fails, so a failure leaves the system exactly as it was.
func (l *fleetLifecycle) AddApp(spec *fleet.AppSpec, rawSchema []byte, db *dbProvision) (*panelAppJSON, *lifecycleError) {
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

	// DB assist (FLEET-DB-ASSIST): resolve DATABASE_URL from a declared instance
	// BEFORE ValidateNewApp (which requires it present). The instance's DSN is
	// held server-side; the browser only ever named the instance.
	var adminDSN, createdDB string // set when this call must roll back a fresh create
	if db != nil {
		inst := l.mf.DBInstanceByName(db.instance)
		if inst == nil {
			return nil, &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("unknown db instance %q — declare it in the manifest's db_instances", db.instance)}
		}
		dbName := db.dbName
		if dbName == "" {
			dbName = fleet.SuggestDBName(spec.Name)
		}
		if !fleet.ValidDBName(dbName) {
			return nil, &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("invalid database name %q", dbName)}
		}
		dsn, derr := fleet.DeriveDSN(inst.AdminDSN(), dbName)
		if derr != nil {
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "derive DSN: " + derr.Error()}
		}
		if spec.Env == nil {
			spec.Env = map[string]string{}
		}
		spec.Env["DATABASE_URL"] = dsn
		adminDSN = inst.AdminDSN()
		// Create the database now (idempotent) so buildFleetApp can connect.
		if db.create {
			created, cerr := fleet.CreateDatabase(l.ctx, adminDSN, dbName)
			if cerr != nil {
				return nil, &lifecycleError{status: http.StatusBadRequest, msg: "create database: " + cerr.Error()}
			}
			if created {
				createdDB = dbName // roll back only what we made fresh
			}
		}
	}

	// rollback drops a freshly-created database (all-or-nothing). No-op when the
	// database pre-existed (createdDB == "") — the fleet never destroys data it
	// did not just make.
	rollbackDB := func() {
		if createdDB != "" {
			if derr := fleet.DropDatabase(l.ctx, adminDSN, createdDB); derr != nil {
				log.Printf("fleet serve: WARNING: app %q add failed AND rolling back its fresh database %q failed — drop it manually: %v", spec.Name, createdDB, derr)
			}
		}
	}

	// The manifest rules — the same ones a fleet restart would enforce, so a
	// hot add can never create a composition the next boot refuses.
	if err := l.mf.ValidateNewApp(spec); err != nil {
		rollbackDB()
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: err.Error()}
	}

	// Compile the app through the SAME path as boot (no routing side effects yet).
	app, err := buildFleetApp(l.ctx, l.mf, spec, l.listenPort, l.version, l.debugTracesHTML)
	if err != nil {
		rollbackDB()
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: err.Error()}
	}

	// Route the app's secrets (DATABASE_URL/JWT_SECRET/ADMIN_KEY, incl. the
	// server-derived DSN) into a per-app ENV-FILE under the data dir instead of
	// inline in the committable manifest — secrets never enter fleet.json. The
	// running app already has them (buildFleetApp used MergedEnv); the manifest
	// then references env_file, so a fleet restart reloads the same values.
	if len(spec.Env) > 0 {
		if err := l.writeAppEnvFile(spec); err != nil {
			app.cleanup()
			rollbackDB()
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist app secrets: " + err.Error()}
		}
	}

	// Persist FIRST (the manifest is the source of truth); only then go live.
	if path := l.mf.Path(); path != "" {
		if err := fleet.AddAppToManifestFile(path, spec); err != nil {
			app.cleanup()
			rollbackDB()
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

// EditApp hot-EDITS an existing app's env — the FLEET-EDIT-S1 counterpart to
// AddApp/RemoveApp: an operator changes/adds env keys (e.g. the
// APPXIMO_GRAPHQL_PLAYGROUND opt-in) on an app already in the fleet, without
// touching its name/domains/schema and without a process restart. Existing
// SECRET VALUES are never read back to the caller (setEnv/unsetEnv are
// write-only) — the merge happens server-side against the resolved env this
// process already holds.
//
// Unlike a schema deploy (wireHotSwap: rebuild the ROUTER in place), an env
// change can touch DATABASE_URL/JWT_SECRET — values baked into the *App at
// construction (its pool, its JWT-validating middleware) — so this compiles a
// FRESH *App (the same buildFleetApp path AddApp uses) and atomically swaps it
// into the registry for the SAME domains (Registry.SwapApp — the same
// primitive a schema hot-swap uses), then retires the OLD instance after the
// same grace RemoveApp gives a removed app, so in-flight requests on the old
// surface finish cleanly. One lock acquisition end to end — no remove-then-add
// race window, and no destructive "type the name to confirm" gate (this isn't
// destructive: the previous env is never deleted from Postgres, only this
// process's live config changes).
func (l *fleetLifecycle) EditApp(name string, setEnv map[string]string, unsetEnv []string) (*panelAppJSON, *lifecycleError) {
	l.mu.Lock()
	defer l.mu.Unlock()

	spec := l.mf.AppByName(name)
	oldPA := l.panel.byName(name)
	if spec == nil || oldPA == nil {
		return nil, &lifecycleError{status: http.StatusNotFound, msg: fmt.Sprintf("no app named %q in the fleet", name)}
	}
	if len(setEnv) == 0 && len(unsetEnv) == 0 {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: "provide at least one env key to set or unset"}
	}

	// Start from the env THIS PROCESS already has resolved for the app (never
	// re-read from disk, so a stale/hand-edited file can't silently override a
	// prior hot edit), apply the requested changes on top.
	current := spec.MergedEnv()
	next := make(map[string]string, len(current)+len(setEnv))
	for k, v := range current {
		next[k] = v
	}
	for k, v := range setEnv {
		next[k] = v
	}
	for _, k := range unsetEnv {
		delete(next, k)
	}

	for _, req := range []string{"DATABASE_URL", "JWT_SECRET", "ADMIN_KEY"} {
		if next[req] == "" {
			return nil, &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("env %s is required — unsetting it would leave the engine unable to boot", req)}
		}
	}
	for i := range l.mf.Apps {
		if l.mf.Apps[i].Name == name {
			continue // comparing against itself is not a collision
		}
		if l.mf.Apps[i].MergedEnv()["JWT_SECRET"] == next["JWT_SECRET"] {
			return nil, &lifecycleError{status: http.StatusBadRequest, msg: fmt.Sprintf("would share app %q's JWT_SECRET — each app must have its own", l.mf.Apps[i].Name)}
		}
	}
	if l.mf.OperatorKey != "" && (l.mf.OperatorKey == next["ADMIN_KEY"] || l.mf.OperatorKey == next["JWT_SECRET"]) {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: "ADMIN_KEY/JWT_SECRET must differ from the fleet operator_key"}
	}

	// Persist the FULL new env to the app's env-file (never inline — same
	// secrets-never-in-fleet.json discipline as a console-added app) and
	// resolve the in-memory cache BEFORE compiling, so buildFleetApp's
	// MergedEnv() read sees the new values.
	spec.SetMergedEnv(next)
	spec.Env = next
	if err := l.writeAppEnvFile(spec); err != nil {
		return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist app secrets: " + err.Error()}
	}

	if path := l.mf.Path(); path != "" {
		if err := fleet.EditAppInManifestFile(path, spec); err != nil {
			return nil, &lifecycleError{status: http.StatusInternalServerError, msg: "persist manifest: " + err.Error()}
		}
	}

	// Compile a FRESH instance off to the side (no routing effect yet) — same
	// path as boot / AddApp. A bad new value (e.g. an unreachable DATABASE_URL)
	// fails HERE, before anything is swapped live; the OLD instance keeps serving.
	newApp, err := buildFleetApp(l.ctx, l.mf, spec, l.listenPort, l.version, l.debugTracesHTML)
	if err != nil {
		return nil, &lifecycleError{status: http.StatusBadRequest, msg: err.Error()}
	}

	appCtx, cancel := context.WithCancel(l.ctx)
	newApp.startBackground(appCtx)
	router := newApp.buildRouter(newApp.bootSurface())

	newPA := &panelApp{
		name:        spec.Name,
		domains:     spec.Domains,
		schemaName:  newApp.schema.Name,
		controlPort: newApp.cfg.ControlPort,
		app:         newApp,
		cancel:      cancel,
	}
	wireHotSwap(l.reg, newApp, spec.Name, spec.Domains, newPA)

	// The atomic takeover: SAME copy-on-write primitive AddApp uses — the new
	// instance starts serving these domains in one publish, every other app's
	// entry untouched.
	l.reg.AddApp(spec.Domains, &compiledApp{name: spec.Name, handler: router})
	l.panel.replaceApp(newPA)

	// The OLD instance stops taking new work now; its infra (DB pool, control
	// plane) is released after the same grace RemoveApp uses, so requests that
	// already resolved the OLD registry entry finish on it cleanly.
	oldApp := oldPA.app
	if oldPA.cancel != nil {
		oldPA.cancel()
	}
	time.AfterFunc(removeGrace, func() {
		sctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		oldApp.cpSrv.Shutdown(sctx) //nolint:errcheck
		oldApp.cleanup()
		log.Printf("fleet serve: app %q old instance released (%s after edit)", name, removeGrace)
	})

	log.Printf("fleet serve: app %q hot-EDITED — env updated, new instance serving %v, other apps untouched, manifest persisted", name, spec.Domains)
	row := l.panel.appRow(newPA)
	return &row, nil
}

// writeAppEnvFile persists an app's full env (DATABASE_URL / JWT_SECRET /
// ADMIN_KEY plus anything else set) to a per-app env-file under the fleet's
// OWN data dir (never wherever the app's schema happens to live — a live
// finding: for a console-added app spec.Schema is always the persisted
// <data_dir>/<name>/schema.json copy, so colocating with it used to be
// equivalent to data_dir/<name>/ anyway; but EditApp (FLEET-EDIT-S1) can
// target an app whose schema is a plain file path OUTSIDE data_dir — e.g. a
// shared examples/ file, or any hand-added manifest entry — and colocating
// there would write generated secrets into a directory the fleet doesn't
// own). sets spec.EnvFile to it, and CLEARS spec.Env so the committable
// manifest references the file instead of inlining secrets. Shared by AddApp
// (spec.Env is exactly what the operator just provided) and EditApp
// (spec.Env is set to the full new MERGED env just before this call, so an
// edited app's file stays complete, not a diff). spec.mergedEnv is left as
// the caller set it (AddApp via ValidateNewApp, EditApp via SetMergedEnv) —
// the running fleet's in-memory state, so future JWT-collision checks
// against OTHER apps still see this one correctly.
func (l *fleetLifecycle) writeAppEnvFile(spec *fleet.AppSpec) error {
	dir := filepath.Join(l.mf.DataDir, spec.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, spec.Name+".env")
	// Deterministic key order keeps the file stable across re-adds.
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Fleet app secrets (FLEET-DB-ASSIST) — generated, NEVER commit.\n")
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(spec.Env[k])
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	spec.EnvFile = path
	spec.Env = nil // manifest references env_file; secrets never inline
	return nil
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
