package appitools

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/miguelangel/appitools/pkg/adminui"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/editorui"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/files"
	"github.com/miguelangel/appitools/pkg/flowtest"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/logging"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/platformadmin"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/schemahistory"
	"github.com/miguelangel/appitools/pkg/shutdown"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/miguelangel/appitools/pkg/userauth"
	"github.com/miguelangel/appitools/scripts"
)

// version is the build version reported by /health. The cmd binary overrides it
// via Config.Version (ldflags-injected); a plain library build reports "dev".
const defaultVersion = "dev"

// App is a constructed Appitools engine: schema-derived REST + GraphQL + OpenAPI,
// multi-tenant, plus any custom Class-1 routes registered before Start. Build it
// with New, add routes with Register, run it with Start.
type App struct {
	cfg     Config
	version string

	pool        *pgxpool.Pool
	redisClient *redis.Client
	tdb         *db.TenantDB
	hr          *extensions.HookRunner
	schema      *schema.APISchema
	eng         *engineRefs
	rbacPolicy  *rbac.Policy

	schemaCache   *tenant.SchemaCache
	responseCache *cache.ResponseCache
	eventsHub     *events.Hub
	tenantLimiter *resilience.TenantLimiter

	// observability stack
	hist      *observability.TenantHistogram
	anomaly   *observability.AnomalyDetector
	errStore  *observability.ErrorStore
	metrics   *observability.Metrics
	rings     *observability.Rings
	sloEngine *observability.SLOEngine
	obsStore  *observability.ObsStore
	synthmon  *observability.SyntheticMonitor
	geo       *observability.GeoLookup
	obsServer *observability.ObsServer

	cpSvc controlplane.Service
	cpSrv *http.Server
	ss    *shutdown.State

	files         *files.Store  // content-addressable file store (FILES-V2: local | s3 backend)
	filesMaxBytes int64         // per-upload body cap
	filesTokenTTL time.Duration // signed download URL/token lifetime

	authSvc *userauth.Service // password identity core (AUTH-CORE-V1)

	platformAdmin *platformadmin.Service // admin API backend (ADMIN-API-V1)

	corsConfig appmiddleware.CORSConfig // cross-origin policy (API-PRODUCTIVA-V1); empty origins ⇒ disabled

	// restartRequested is set by the admin self-restart endpoint (UI-F4-S2);
	// after the graceful shutdown completes, Start re-execs the process so it
	// relaunches with the persisted boot schema.
	restartRequested atomic.Bool

	// registry is the in-process app registry (MT-STRUCT-S2, the Option-B
	// foundation): Host → compiled app, lock-free reads. In S2 it holds ONE
	// app (the boot schema) and every Host resolves to it — behavior identical
	// to pre-registry, benched. S3 loads N apps; S4 hot-swaps entries.
	registry *Registry

	// noSynthetic disables the synthetic monitor for this app — set by the
	// multi-app runtime (ServeFleet), whose bare-Host canary probes would land
	// on the process-level 404 handler, not this app.
	noSynthetic bool

	// hotSwap, when non-nil, makes a deploy (POST /admin/engine/schema) rebuild
	// THIS app's router from the newly-persisted schema and swap it into the
	// registry WITHOUT restarting the process (MT-STRUCT-S4). It is set only by
	// the in-process fleet runtime (ServeFleet). When nil (single-engine), a
	// deploy falls back to the graceful whole-process re-exec (UI-F4-S2).
	hotSwap func()

	// liveResources holds the sorted resource names of the CURRENTLY-served
	// surface (updated by buildRouter on boot AND on every hot-swap). It backs
	// the platform admin's /admin/served-resources so the editor's post-deploy
	// verify sees the live surface after a hot-swap, not the boot list.
	liveResources atomic.Pointer[[]string]

	// currentRouter is the CURRENTLY-served data-plane router (published by
	// buildRouter on boot and on every hot-swap). The flow-test runner
	// (FLOWTEST-S1) executes flows against it in-process — the real chain,
	// always the live surface.
	currentRouter atomic.Pointer[chi.Mux]

	routes  []Route
	started bool
}

// New builds an engine from cfg. SchemaPath is required; the DSN, JWT secret,
// admin key, port and env fall back to DATABASE_URL / JWT_SECRET / ADMIN_KEY /
// 8080 / APPITOOLS_ENV when their Config fields are empty — so the pure binary
// and a custom binary boot identically. It performs the SAME initialization the
// `serve` command always has (pool, outbox table, hook runtime, observability,
// control plane, caches, rate limiter); the goroutines and HTTP listeners are
// started by Start.
func New(cfg Config) (*App, error) {
	if cfg.DSN == "" {
		cfg.DSN = os.Getenv("DATABASE_URL")
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = os.Getenv("JWT_SECRET")
	}
	if cfg.AdminKey == "" {
		cfg.AdminKey = os.Getenv("ADMIN_KEY")
	}
	if cfg.Env == "" {
		cfg.Env = os.Getenv("APPITOOLS_ENV")
	}
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.SchemaPath == "" {
		return nil, errors.New("appitools: Config.SchemaPath is required")
	}
	if cfg.DSN == "" {
		return nil, errors.New("appitools: DSN is required (set Config.DSN or DATABASE_URL)")
	}
	if cfg.JWTSecret == "" {
		return nil, errors.New("appitools: JWT secret is required (set Config.JWTSecret or JWT_SECRET)")
	}
	if cfg.AdminKey == "" {
		return nil, errors.New("appitools: admin key is required (set Config.AdminKey or ADMIN_KEY)")
	}

	logging.Init(cfg.Env)

	s, err := loadAndValidateSchema(cfg.SchemaPath)
	if err != nil {
		// Self-restart rollback (UI-F4-S2): if this boot follows a self-restart
		// persist (marker present) and the previous schema is backed up, restore
		// it and boot from it — the service comes back on the old structure
		// instead of staying down. A plain (non-self-restart) load failure still
		// fails loud, unchanged.
		if !recoverBootSchema(cfg.SchemaPath, err) {
			return nil, err
		}
		if s, err = loadAndValidateSchema(cfg.SchemaPath); err != nil {
			return nil, err
		}
	}
	clearBootMarker(cfg.SchemaPath)
	app := &App{cfg: cfg, version: cfg.Version, schema: s}
	if app.version == "" {
		app.version = defaultVersion
	}

	// Pool — context.Background so the pool outlives New; closed in Start's cleanup.
	pool, err := db.NewPool(context.Background(), cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("appitools: connect db: %w", err)
	}
	app.pool = pool

	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("appitools: ensure outbox table: %w", err)
	}
	log.Println("outbox: public.outbox table ready")

	// Schema version history (VERSION-S1): ensure the append-only table (same
	// idempotent pattern as the outbox — existing databases predate the canonical
	// DDL) and capture pre-versioning tenants' current schema as their v1 so the
	// history is immediately useful on an upgraded install. Backfill is
	// best-effort: a failure is loud but never blocks boot.
	if err := schemahistory.EnsureTable(context.Background(), pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("appitools: %w", err)
	}
	if n, bfErr := controlplane.BackfillSchemaHistory(context.Background(), pool); bfErr != nil {
		log.Printf("WARNING: schema history backfill: %v", bfErr)
	} else if n > 0 {
		log.Printf("schema history: backfilled v1 for %d pre-versioning tenant(s)", n)
	}

	// Flow tests (FLOWTEST-S1): the persisted multi-step scenarios + their
	// regression runs (same idempotent-ensure pattern).
	if err := flowtest.EnsureTables(context.Background(), pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("appitools: %w", err)
	}

	app.tdb = db.NewTenantDB(pool)

	// Content-addressable file store (FILES-V2): the Store (tenancy + metadata +
	// OWASP upload validation + dedup) over a swappable Backend — "local" (disk,
	// the default: everything on this VPS) or "s3" (any S3-compatible provider by
	// config: R2/Spaces/MinIO/AWS). A misconfigured s3 backend fails boot loudly;
	// the local blob root is created lazily on the first upload, so an engine
	// that never serves /api/files pays nothing.
	filesBackendName := strings.ToLower(strings.TrimSpace(coalesce(cfg.FilesBackend, os.Getenv("APPITOOLS_FILES_BACKEND"))))
	uploadPolicy := files.NewUploadPolicy(coalesceCSV(cfg.FilesAllowedExt, os.Getenv("APPITOOLS_FILES_ALLOWED_EXT")))
	var filesBackend files.Backend
	switch filesBackendName {
	case "", "local":
		filesDir := coalesce(cfg.FilesDir, os.Getenv("APPITOOLS_FILES_DIR"))
		if filesDir == "" {
			filesDir = "/var/lib/appitools/files"
		}
		filesBackend = files.NewLocalBackend(filesDir)
		log.Printf("files: local backend at %s", filesDir)
	case "s3":
		s3cfg := files.S3Config{
			Bucket:         coalesce(cfg.FilesS3Bucket, os.Getenv("APPITOOLS_FILES_S3_BUCKET")),
			Endpoint:       coalesce(cfg.FilesS3Endpoint, os.Getenv("APPITOOLS_FILES_S3_ENDPOINT")),
			Region:         coalesce(cfg.FilesS3Region, os.Getenv("APPITOOLS_FILES_S3_REGION")),
			AccessKey:      coalesce(cfg.FilesS3AccessKey, os.Getenv("APPITOOLS_FILES_S3_ACCESS_KEY")),
			SecretKey:      coalesce(cfg.FilesS3SecretKey, os.Getenv("APPITOOLS_FILES_S3_SECRET_KEY")),
			ForcePathStyle: cfg.FilesS3ForcePathStyle || envTruthy(os.Getenv("APPITOOLS_FILES_S3_FORCE_PATH_STYLE")),
			Prefix:         coalesce(cfg.FilesS3Prefix, os.Getenv("APPITOOLS_FILES_S3_PREFIX")),
			ServeMode:      files.S3ServeMode(coalesce(cfg.FilesS3ServeMode, os.Getenv("APPITOOLS_FILES_S3_SERVE"))),
		}
		s3b, s3err := files.NewS3Backend(context.Background(), s3cfg)
		if s3err != nil {
			pool.Close()
			return nil, fmt.Errorf("appitools: %w", s3err)
		}
		filesBackend = s3b
		log.Printf("files: s3 backend — bucket %q endpoint %q (serve mode %s)",
			s3cfg.Bucket, s3cfg.Endpoint, s3cfg.ServeMode)
	default:
		pool.Close()
		return nil, fmt.Errorf("appitools: unknown files backend %q (use \"local\" or \"s3\")", filesBackendName)
	}
	app.files = files.NewStore(filesBackend, files.NewPGStore(pool), files.WithUploadPolicy(uploadPolicy))
	app.filesMaxBytes = files.DefaultMaxUploadBytes
	if v := os.Getenv("APPITOOLS_FILES_MAX_BYTES"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			app.filesMaxBytes = n
		}
	}
	app.filesTokenTTL = files.DefaultSignedURLTTL
	if cfg.FilesTokenTTLSeconds > 0 {
		app.filesTokenTTL = time.Duration(cfg.FilesTokenTTLSeconds) * time.Second
	} else if v := os.Getenv("APPITOOLS_FILES_TOKEN_TTL"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			app.filesTokenTTL = time.Duration(n) * time.Second
		}
	}
	log.Printf("files: max upload %d bytes, signed URL TTL %s", app.filesMaxBytes, app.filesTokenTTL)

	// HookRunner with Capa 3 (WASM) when the runtime initializes; JS+webhook only on error.
	sandbox := extensions.NewJSSandbox()
	if wasmRunner, werr := extensions.NewWasmRunner(context.Background()); werr != nil {
		log.Printf("WARNING: WASM runtime (Capa 3) disabled: %v", werr)
		app.hr = extensions.NewHookRunner(sandbox)
	} else {
		app.hr = extensions.NewHookRunnerWithWasm(sandbox, extensions.NewWebhookDispatcher(), wasmRunner)
		log.Println("WASM runtime (Capa 3) enabled")
	}

	// Redis optional.
	if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
		if opts, perr := redis.ParseURL(redisURL); perr != nil {
			log.Printf("Warning: invalid REDIS_URL: %v", perr)
		} else {
			app.redisClient = redis.NewClient(opts)
		}
	}

	app.schemaCache = tenant.NewSchemaCache()

	// Observability stack.
	app.hist = observability.NewTenantHistogram()
	app.anomaly = observability.NewAnomalyDetector()
	app.errStore = observability.NewErrorStore()
	app.metrics = observability.NewMetrics()
	app.rings = observability.NewRings()
	alerter := observability.NewSlackAlerterFromEnv()
	app.sloEngine = observability.NewSLOEngine(app.rings, app.hist, alerter)

	if st, openErr := observability.OpenStore(coalesce(cfg.ObsDBPath, os.Getenv("OBS_DB_PATH"))); openErr != nil {
		log.Printf("WARNING: observability store disabled: %v", openErr)
	} else {
		app.obsStore = st
		if pruneErr := st.Prune(); pruneErr != nil {
			log.Printf("WARNING: obs store prune at startup: %v", pruneErr)
		}
	}

	// Synthetic monitor (health + schema-derived API canary).
	app.synthmon = observability.NewSyntheticMonitor([]observability.Check{
		{Name: "health", URL: fmt.Sprintf("http://localhost:%d/health", cfg.Port), Method: "GET", Expected: 200},
	})
	app.geo = observability.DefaultGeoLookup()
	app.obsServer = observability.NewObsServer(app.hist, app.errStore, app.anomaly, app.synthmon, app.rings, app.sloEngine, app.obsStore)
	app.obsServer.SetGeo(app.geo)

	// /debug/traces HTML page — accept ?key= OR X-Admin-Key, browser-openable.
	adminKey := cfg.AdminKey
	if len(cfg.DebugTracesHTML) > 0 {
		tracesHTML := cfg.DebugTracesHTML
		app.obsServer.SetTracesHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := req.URL.Query().Get("key")
			if key == "" {
				key = req.Header.Get("X-Admin-Key")
			}
			if subtle.ConstantTimeCompare([]byte(key), []byte(adminKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(tracesHTML) //nolint:errcheck
		}))
	}

	// Control plane router — started in Start. The port was ":9090" hardcoded
	// until MT-STRUCT-S1; it is now Config/env-parameterized (default preserved)
	// so N engines can coexist on one box. Boot config only — not the hot path.
	if cfg.ControlPort == 0 {
		if v := os.Getenv("APPITOOLS_CONTROL_PORT"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
				cfg.ControlPort = n
			}
		}
	}
	if cfg.ControlPort == 0 {
		cfg.ControlPort = 9090
	}
	app.cfg.ControlPort = cfg.ControlPort
	app.cpSvc = controlplane.NewService(pool, app.redisClient)
	cpRouter := controlplane.NewControlPlaneRouter(app.cpSvc, adminKey)
	cpRouter.Mount("/", app.obsServer.Router(adminKey))
	app.cpSrv = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.ControlPort),
		Handler:           cpRouter,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// RBAC policy.
	policyBytes, err := json.Marshal(s.RBAC)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("appitools: serialize RBAC policy: %w", err)
	}
	var rbacPolicy rbac.Policy
	if err := json.Unmarshal(policyBytes, &rbacPolicy); err != nil {
		pool.Close()
		return nil, fmt.Errorf("appitools: parse RBAC policy: %w", err)
	}
	app.rbacPolicy = &rbacPolicy

	// SSE hub.
	maxSSE := 0
	if v := os.Getenv("APPITOOLS_MAX_SSE_PER_TENANT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxSSE = n
		}
	}
	app.eventsHub = events.NewHub(maxSSE)

	// Response cache (5s TTL) + role gate. A role is cacheable only if it injects no
	// per-user row condition and no field allowlist — for BOTH the legacy role-global
	// form and the per-resource permissions form (any conditioned/allowlisted resource
	// ⇒ not cacheable, fail-safe). See rbac.Policy.RoleCacheable.
	app.responseCache = cache.New(5 * time.Second)
	app.responseCache.SetRoleCacheGate(app.rbacPolicy.RoleCacheable)
	// Scope the cache's claims-cache lookups to THIS app's JWT secret
	// (MT-STRUCT-S3): with N in-process apps, a token validated by another
	// app must never short-circuit this app's chain.
	app.responseCache.SetJWTSecret(cfg.JWTSecret)

	// Rate limiter.
	rlRPS := 1000.0
	if v := os.Getenv("RATE_LIMIT_RPS"); v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil && f > 0 {
			rlRPS = f
		}
	}
	rlBurst := 100
	if v := os.Getenv("RATE_LIMIT_BURST"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			rlBurst = n
		}
	}
	app.tenantLimiter = resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: rlRPS, Burst: rlBurst})
	log.Printf("rate limiter: %.0f RPS / %d burst per tenant", rlRPS, rlBurst)

	app.ss = shutdown.New()

	// Auth-as-product core (AUTH-CORE-V1): per-tenant password identity. Users
	// live in tenant_<id>.auth_users — email is UNIQUE per schema, not global, so
	// the same email is a distinct user in two tenants (the structural advantage
	// over Supabase's globally-unique email). The signup role + min password
	// length come from Config or env; an UNSET signup role disables public signup
	// (safe by default). A configured role MUST exist in the schema RBAC, else
	// boot fails — a typo never becomes a silent "everyone signs up as <unknown>".
	signupRole := cfg.AuthSignupRole
	if signupRole == "" {
		signupRole = os.Getenv("APPITOOLS_AUTH_SIGNUP_ROLE")
	}
	if signupRole != "" {
		if _, ok := rbacPolicy.Roles[signupRole]; !ok {
			pool.Close()
			return nil, fmt.Errorf("appitools: AuthSignupRole %q is not a role declared in the schema RBAC", signupRole)
		}
	}
	minPw := cfg.AuthMinPasswordLength
	if minPw <= 0 {
		if v := os.Getenv("APPITOOLS_AUTH_MIN_PASSWORD"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
				minPw = n
			}
		}
	}
	// AUTH-EMAIL-V1: password reset + email verification. The reset/verify flows
	// enqueue an "email.send" event to the outbox (delivered async by the email
	// worker, APPITOOLS_WORKER_MODE=email) — the topic must match the worker's
	// APPITOOLS_EMAIL_TOPIC. RequireVerified gates login on a verified email;
	// BaseURL overrides the email-link origin (else derived from the request Host).
	requireVerified := cfg.AuthRequireVerified || envTruthy(os.Getenv("APPITOOLS_AUTH_REQUIRE_VERIFIED"))
	baseURL := cfg.AuthBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("APPITOOLS_AUTH_BASE_URL")
	}
	emailTopic := os.Getenv("APPITOOLS_EMAIL_TOPIC") // empty → NewService defaults to "email.send"

	// AUTH-OAUTH-V1: social login providers from env. A provider is offered ONLY
	// when its client id is set, so leaving these unset disables OAuth with no boot
	// error. The default role (for users auto-created on first social login) must
	// exist in the RBAC if set; it falls back to the signup role.
	oauthProviders := cfg.OAuthProviders
	if oauthProviders == nil {
		oauthProviders = map[string]userauth.OAuthProviderConfig{
			"google":    {ClientID: os.Getenv("APPITOOLS_OAUTH_GOOGLE_CLIENT_ID"), ClientSecret: os.Getenv("APPITOOLS_OAUTH_GOOGLE_CLIENT_SECRET")},
			"github":    {ClientID: os.Getenv("APPITOOLS_OAUTH_GITHUB_CLIENT_ID"), ClientSecret: os.Getenv("APPITOOLS_OAUTH_GITHUB_CLIENT_SECRET")},
			"microsoft": {ClientID: os.Getenv("APPITOOLS_OAUTH_MICROSOFT_CLIENT_ID"), ClientSecret: os.Getenv("APPITOOLS_OAUTH_MICROSOFT_CLIENT_SECRET")},
		}
	}
	oauthCallbackURL := cfg.OAuthCallbackURL
	if oauthCallbackURL == "" {
		oauthCallbackURL = os.Getenv("APPITOOLS_OAUTH_CALLBACK_URL")
	}
	oauthDefaultRole := cfg.OAuthDefaultRole
	if oauthDefaultRole == "" {
		oauthDefaultRole = os.Getenv("APPITOOLS_OAUTH_DEFAULT_ROLE")
	}
	if oauthDefaultRole != "" {
		if _, ok := rbacPolicy.Roles[oauthDefaultRole]; !ok {
			pool.Close()
			return nil, fmt.Errorf("appitools: OAuthDefaultRole %q is not a role declared in the schema RBAC", oauthDefaultRole)
		}
	}
	oauthSuccessRedirect := cfg.OAuthSuccessRedirect
	if oauthSuccessRedirect == "" {
		oauthSuccessRedirect = os.Getenv("APPITOOLS_OAUTH_SUCCESS_REDIRECT")
	}

	// AUTH-MFA-V1: TOTP second factor. The secret-encryption key falls back to the
	// JWT secret (so MFA works out of the box); MFAIssuer labels the authenticator app.
	mfaKey := cfg.MFAKey
	if mfaKey == "" {
		mfaKey = os.Getenv("APPITOOLS_MFA_KEY")
	}
	mfaIssuer := cfg.MFAIssuer
	if mfaIssuer == "" {
		mfaIssuer = os.Getenv("APPITOOLS_MFA_ISSUER")
	}

	// One shared per-tenant identity store, used by BOTH the password identity core
	// and the admin API's user-management (so the lazy DDL cache is shared).
	authStore := userauth.NewStore(pool)

	// Admin API backend (ADMIN-API-V1): the platform super-admin (in the system
	// schema, ABOVE the tenants) plus the consolidated tenant/user/observability
	// admin API. It WRAPS the existing control plane and REUSES the existing
	// per-tenant identity + RBAC — it is not a second permission system. Built
	// before authSvc so the login flow can consult its tenant-suspension predicate.
	platformSuperAdminRole := os.Getenv("APPITOOLS_PLATFORM_SUPER_ADMIN_ROLE")
	platformMFAIssuer := os.Getenv("APPITOOLS_PLATFORM_MFA_ISSUER")
	// obsSentinel is a resource name no schema can declare (resource names match
	// ^[a-z][a-z0-9_]*$ — no dots, no leading underscore) — Allows(role, obsSentinel, "read") is true only
	// for a wildcard-resource admin role, the "admin-grade" test for tenant-scoped
	// observability access. This INHERITS the RBAC, it does not invent a new check.
	const obsSentinel = "__platform.observability__"
	// The resources the engine serves live come from the boot --schema (routes,
	// GraphQL and RBAC are all compiled from it). Exposed read-only so the editor can
	// warn that a deployed-but-not-booted resource needs a restart (UI-F3-S1).
	servedResources := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		servedResources = append(servedResources, name)
	}
	sort.Strings(servedResources)
	app.platformAdmin = platformadmin.NewService(
		platformadmin.NewStore(pool), authStore, app.cpSvc, pool,
		platformadmin.Config{
			JWTSecret:       cfg.JWTSecret,
			MFAKey:          mfaKey,
			MFAIssuer:       platformMFAIssuer,
			SuperAdminRole:  platformSuperAdminRole,
			ServedResources: servedResources,
			// Live served-resources for the editor's post-hot-swap verify: the
			// current surface's list (MT-STRUCT-S4), falling back to the boot
			// slice until the first buildRouter publishes it.
			ServedResourcesFn: func() []string {
				if p := app.liveResources.Load(); p != nil {
					return *p
				}
				return servedResources
			},
			// Flow-test runner target (FLOWTEST-S1): the LIVE router — set by
			// buildRouter at boot and re-set on every hot-swap.
			FlowHandlerFn: func() http.Handler {
				if m := app.currentRouter.Load(); m != nil {
					return m
				}
				return nil
			},
			// Deploy-activation mode for the editor (MT-STRUCT-S5): evaluated at
			// request time because ServeFleet wires hotSwap AFTER New.
			ActivationFn: func() string {
				if app.hotSwap != nil {
					return "hot_swap"
				}
				return "restart"
			},
			// Engine self-restart (UI-F4-S2): the editor's "Apply & restart" —
			// persist the deployed schema as the new BOOT schema (validated +
			// atomic + backed up) and activate it. In single-engine mode this
			// gracefully re-execs the process; in the in-process fleet
			// (MT-STRUCT-S4) it hot-swaps ONLY this app. triggerRestart dispatches.
			PersistBootSchema: app.persistBootSchema,
			TriggerRestart:    app.triggerRestart,
			RoleExists: func(role string) bool {
				_, ok := rbacPolicy.Roles[role]
				return ok
			},
			TenantAdminRole: func(role string) bool {
				return rbacPolicy.Allows(role, obsSentinel, "read")
			},
		})
	// Read-only data browsing (ADMIN-UI-V1.2): reuses the engine's tenant-scoped DB
	// + query builder; never touches the hot path (new /admin routes).
	app.platformAdmin.SetTenantDB(app.tdb)
	// Files manager (UI-F5-S1): the Studio files view manages a tenant's files
	// through thin /admin/tenants/{id}/files routes that delegate into the SAME
	// files.Store as /api/files — identical OWASP validation, serve strategy and
	// dedup-aware delete; same signing secret and TTL as the public signed URLs.
	app.platformAdmin.SetFileStore(app.files, app.filesMaxBytes, app.filesTokenTTL, []byte(cfg.JWTSecret))
	log.Println("admin API: platform super-admin + tenant/user/observability management enabled at /admin/*")

	app.authSvc = userauth.NewService(authStore, userauth.Config{
		JWTSecret:            cfg.JWTSecret,
		SignupRole:           signupRole,
		MinPasswordLength:    minPw,
		EmailTopic:           emailTopic,
		BaseURL:              baseURL,
		RequireVerified:      requireVerified,
		TenantActive:         app.platformAdmin.IsTenantActive,
		OAuthProviders:       oauthProviders,
		OAuthCallbackURL:     oauthCallbackURL,
		OAuthDefaultRole:     oauthDefaultRole,
		OAuthSuccessRedirect: oauthSuccessRedirect,
		MFAKey:               mfaKey,
		MFAIssuer:            mfaIssuer,
	})
	if signupRole != "" {
		log.Printf("auth: password identity enabled (public signup → role %q)", signupRole)
	} else {
		log.Println("auth: password identity enabled (public signup DISABLED — set APPITOOLS_AUTH_SIGNUP_ROLE to enable)")
	}
	log.Printf("auth: reset/verify email flows enabled (require_verified=%t, email topic via outbox)", requireVerified)
	if app.authSvc.OAuthEnabled() {
		var enabled []string
		for _, n := range []string{"google", "github", "microsoft"} {
			if app.authSvc.OAuthProviderConfigured(n) {
				enabled = append(enabled, n)
			}
		}
		log.Printf("auth: OAuth social login enabled (providers: %s)", strings.Join(enabled, ", "))
	}

	// CORS (API-PRODUCTIVA-V1): cross-origin policy for the public data-plane
	// routes. Config fields win; each falls back to its APPITOOLS_CORS_* env var.
	// An empty origin list (the default) leaves CORS DISABLED — the middleware is
	// not even wired, so a default deployment pays zero and never emits an
	// Access-Control-* header. An operator enables it by listing browser origins.
	app.corsConfig = appmiddleware.CORSConfig{
		AllowedOrigins:   coalesceCSV(cfg.CORSAllowedOrigins, os.Getenv("APPITOOLS_CORS_ORIGINS")),
		AllowedMethods:   coalesceCSV(cfg.CORSAllowedMethods, os.Getenv("APPITOOLS_CORS_METHODS")),
		AllowedHeaders:   coalesceCSV(cfg.CORSAllowedHeaders, os.Getenv("APPITOOLS_CORS_HEADERS")),
		ExposedHeaders:   coalesceCSV(cfg.CORSExposedHeaders, os.Getenv("APPITOOLS_CORS_EXPOSE_HEADERS")),
		AllowCredentials: cfg.CORSAllowCredentials || envTruthy(os.Getenv("APPITOOLS_CORS_CREDENTIALS")),
		MaxAge:           cfg.CORSMaxAge,
	}
	if app.corsConfig.MaxAge == 0 {
		if v := os.Getenv("APPITOOLS_CORS_MAX_AGE"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil {
				app.corsConfig.MaxAge = n
			}
		}
	}
	if app.corsConfig.Enabled() {
		log.Printf("CORS enabled for /api,/auth,/graphql,/openapi — origins: %s (credentials=%t)",
			strings.Join(app.corsConfig.AllowedOrigins, ", "), app.corsConfig.AllowCredentials)
	} else {
		log.Println("CORS disabled (no origins configured — set APPITOOLS_CORS_ORIGINS to enable browser cross-origin access)")
	}

	// engineRefs for custom-route Ctx helpers: compile validators once.
	validators := make(map[string]*schema.ResourceValidator, len(s.Resources))
	for name, res := range s.Resources {
		r := res
		validators[name] = schema.CompileRules(&r)
	}
	app.eng = &engineRefs{schema: s, validators: validators, policy: &rbacPolicy}

	return app, nil
}

// Register adds a custom Class-1 route. It must be called BEFORE Start and is
// validated immediately for shape and collisions against the schema's generated
// routes — a bad route returns an error here, at boot, never at request time.
// Registration is boot-only (chi has no safe post-Start mutation), so calling
// Register after Start returns an error.
func (a *App) Register(rt Route) error {
	if a.started {
		return errors.New("appitools: Register must be called before Start")
	}
	seen := make(map[string]bool, len(a.routes))
	for _, existing := range a.routes {
		seen[strings.ToUpper(existing.Method)+" "+existing.Path] = true
	}
	if err := validateRoute(rt, a.schema, seen); err != nil {
		return err
	}
	rt.Method = strings.ToUpper(rt.Method)
	a.routes = append(a.routes, rt)
	return nil
}

// Routes returns the registered custom routes (read-only view, for OpenAPI/tests).
func (a *App) Routes() []Route { return append([]Route(nil), a.routes...) }

// logf logs through the structured logger if initialized, else the std logger.
func (a *App) logf(format string, args ...any) { log.Printf(format, args...) }

// Start builds the data-plane router (the shared middleware chain + custom
// routes + generated routes), launches the background goroutines and the
// control-plane listener, and serves until SIGINT/SIGTERM, running the graceful
// shutdown sequence. It blocks until shutdown completes.
func (a *App) Start() error {
	if a.started {
		return errors.New("appitools: Start called twice")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a.startBackground(ctx)

	r := a.buildRouter(a.bootSurface())

	// MT-STRUCT-S2: the server's handler is the app REGISTRY, not the router
	// directly. With the single boot app and no domain table, Resolve is two
	// atomic loads → the same router as before (behavior identical, benched —
	// the dispatch exists so S3 can load N apps and S4 can hot-swap one).
	a.registry = NewRegistry(&compiledApp{name: a.schema.Name, handler: r}, nil)

	addr := fmt.Sprintf(":%d", a.cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.registry,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("Appitools serving on %s — Ctrl+C to stop\n", addr)
	if err := a.ss.Run(ctx, srv, 5*time.Second, a.cleanup); err != nil {
		return err
	}
	log.Println("server shut down cleanly")
	// Self-restart (UI-F4-S2): the drain above ran the normal graceful sequence;
	// now replace the process with a fresh instance that boots the persisted
	// schema. On success execRestart never returns.
	if a.restartRequested.Load() {
		a.execRestart()
	}
	return nil
}

// startBackground launches the app's non-listener services: the SLO engine,
// obs snapshot flusher, synthetic monitor, control-plane listener, optional
// migration worker, cache invalidator and the claims-cache GC. Split out of
// Start (MT-STRUCT-S3) so the multi-app runtime (ServeFleet) can run N apps'
// services in ONE process while owning the single data-plane listener itself.
// Marks the app started (Register is boot-only in both modes).
func (a *App) startBackground(ctx context.Context) {
	a.started = true

	go a.sloEngine.Run(ctx)
	if a.obsStore != nil {
		go flushObsSnapshots(ctx, a.obsStore, a.rings, a.hist, a.sloEngine)
	}

	// The synthetic monitor probes http://localhost:<port>/… with a bare Host.
	// In the multi-app runtime a bare Host resolves to the process-level 404
	// handler, so the runner disables it per app (noSynthetic) instead of
	// letting every app log canary failures.
	if os.Getenv("APPITOOLS_SYNTHETIC") != "off" && !a.noSynthetic {
		a.synthmon.AddDynamic(observability.DynamicCheck{
			Name:    "api-canary",
			Resolve: canaryResolver(a.pool, a.schema, a.cfg.JWTSecret, a.cfg.Port),
		})
		a.synthmon.Start(ctx, 60*time.Second)
	}

	go func() {
		fmt.Println("Control plane serving on " + a.cpSrv.Addr)
		if err := a.cpSrv.ListenAndServe(); err != nil {
			fmt.Fprintln(os.Stderr, "Control plane error:", err)
		}
	}()

	if a.redisClient != nil {
		worker := migration.NewMigrationWorker(a.redisClient, a.pool, a.schemaCache)
		go worker.Run(ctx)
		log.Println("Migration worker started")
	}

	go startCacheInvalidator(ctx, a.pool, a.responseCache)
	auth.StartClaimsCacheGC(ctx)
}

// bootSurface is the schema-derived surface built from the app's boot schema
// (a.schema + a.rbacPolicy) — the input buildRouter used implicitly before
// MT-STRUCT-S4 parameterized it.
func (a *App) bootSurface() builtSurface {
	return builtSurface{schema: a.schema, policy: a.rbacPolicy}
}

// cleanup releases the app's resources after its server (or the fleet server)
// has drained. Shared by Start and the multi-app runtime.
func (a *App) cleanup() {
	a.pool.Close()
	if a.obsStore != nil {
		a.obsStore.Close() //nolint:errcheck
	}
	if a.files != nil {
		if c, ok := a.files.Backend().(io.Closer); ok {
			c.Close() //nolint:errcheck
		}
	}
}

// builtSurface is the schema-DERIVED input to buildRouter: the parsed schema and
// its RBAC policy (MT-STRUCT-S4). Everything else buildRouter reads is
// schema-INDEPENDENT process infra on the App (pool, caches, SSE hub, obs,
// control plane, files, auth). Passing the surface explicitly — instead of
// reading a.schema/a.rbacPolicy — is what lets a hot-swap build a NEW router from
// a NEW schema, sharing all infra, and atomically replace the old one: the two
// routers each capture their own surface, so an in-flight request on the old
// router keeps the old schema/policy consistently.
type builtSurface struct {
	schema *schema.APISchema
	policy *rbac.Policy
}

// buildRouter assembles the outer chi router for surf: the exact same middleware
// chain generated routes have always paid (security headers → compression →
// request id → tenant → rate limit → request logger → response cache → JWT →
// RBAC → recoverer), then the infra/admin endpoints, then the /api group where
// custom routes and the generated router mount. Custom routes go through the
// IDENTICAL chain — no duplicated middleware (ADR-016 Decision 4C). The
// schema-derived bits (routes, GraphQL, RBAC policy, OpenAPI, response-cache
// gate) come from surf; the infra from a.
func (a *App) buildRouter(surf builtSurface) *chi.Mux {
	adminKey := a.cfg.AdminKey
	// Re-point the (shared) response cache's cacheability gate at THIS surface's
	// policy. Atomic (MT-STRUCT-S4), so a hot-swap that tightens a role's
	// conditions takes effect for the cache without a data race on the old
	// surface's cache middleware.
	a.responseCache.SetRoleCacheGate(surf.policy.RoleCacheable)
	// Publish this surface's resource list for /admin/served-resources (so the
	// editor's post-hot-swap verify reads the live surface).
	names := make([]string, 0, len(surf.schema.Resources))
	for name := range surf.schema.Resources {
		names = append(names, name)
	}
	sort.Strings(names)
	a.liveResources.Store(&names)
	r := chi.NewMux()
	r.Use(appmiddleware.SecurityHeaders)
	// CORS (API-PRODUCTIVA-V1) runs BEFORE tenant/cache/JWT/RBAC so a preflight
	// (no credentials, no tenant) is answered without 401/400, and so a per-request
	// Allow-Origin is never cached. Wired ONLY when origins are configured — a
	// default deployment has no CORS middleware in the chain at all (zero cost).
	if a.corsConfig.Enabled() {
		r.Use(appmiddleware.CORS(a.corsConfig))
	}
	// Response compression — SELECTIVE (FILES-FIX-SENDFILE): the file
	// byte-serving routes are routed AROUND the Compress wrapper. chi's
	// compressResponseWriter lacks io.ReaderFrom, which suppressed sendfile
	// zero-copy on downloads (FILES-BENCH: 0 sendfile calls, 53% of nginx at
	// ~5.5× the CPU/byte); binaries were never compressible anyway. JSON —
	// including the file store's /url + listing responses — stays compressed.
	// Same bypass shape as the response cache's (pkg/cache).
	r.Use(appmiddleware.SelectiveCompress(
		func(req *http.Request) bool { return files.IsByteServingPath(req.Method, req.URL.Path) },
		5, "application/json", "application/graphql+json"))
	r.Use(chimiddleware.RequestID)
	// With BareDomains set (fleet: the app's own manifest domains), a request to
	// the app's bare domain carries NO tenant — it is app-level traffic (admin,
	// docs, console), not the phantom tenant its first label would suggest.
	// Empty (single-engine) ⇒ identical to the historical TenantMiddleware.
	r.Use(tenant.MiddlewareWithBareHosts(a.cfg.BareDomains))
	r.Use(resilience.RateLimit(a.tenantLimiter))

	tracePersistSem := make(chan struct{}, 64)
	r.Use(logging.RequestLogger(
		a.hist.Record,
		func(id string, us float64) {
			if isAnomaly, zScore := a.anomaly.Observe(id, us); isAnomaly {
				a.anomaly.RecordAnomaly(id, us, zScore)
				logging.Log.Warn().
					Str("tenant_id", id).
					Float64("z_score", zScore).
					Int64("latency_us", int64(us)).
					Msg("anomaly detected")
			}
		},
		func(t logging.RequestTap) {
			if t.TenantID == "" || isInfraPath(t.Path) {
				return
			}
			a.metrics.ObserveRequest(t.TenantID, t.Method, t.Route,
				strconv.Itoa(t.Status), float64(t.DurationUS)/1e6)

			var traceID [8]byte
			if b, err := hex.DecodeString(t.TraceID); err == nil && len(b) >= 8 {
				copy(traceID[:], b[:8])
			}
			var spans [8]observability.Span
			n := len(t.Spans)
			if n > 8 {
				n = 8
			}
			copy(spans[:], t.Spans[:n])

			sample := observability.Sample{
				Start:        t.StartUS,
				DurUS:        int32(t.DurationUS),
				QueryUS:      0,
				Route:        a.rings.RouteID(t.Route),
				Status:       uint16(t.Status),
				TraceID:      traceID,
				Spans:        spans,
				NSpans:       uint8(n),
				ErrMsg:       t.ErrMsg,
				ErrorCapture: t.Capture,
				IP:           t.IP,
				UserAgent:    t.UserAgent,
			}
			a.rings.Record(t.TenantID, sample)
			a.metrics.SetActiveTenants(a.rings.Count())

			if a.obsStore != nil && observability.ShouldPersistTrace(sample) {
				tv := observability.TraceView{
					TraceID:   t.TraceID,
					TS:        t.StartUS,
					Route:     t.Route,
					TotalUS:   sample.DurUS,
					Status:    uint16(t.Status),
					Spans:     append([]observability.Span(nil), t.Spans...),
					ErrMsg:    t.ErrMsg,
					IP:        t.IP,
					UserAgent: t.UserAgent,
					Method:    t.Method,
					FullURL:   t.FullURL,
					Headers:   t.Headers,
				}
				if t.Capture != nil {
					tv.Stack = t.Capture.Stack
					tv.UserID, tv.Role = t.Capture.UserID, t.Capture.Role
					if tv.ErrMsg == "" {
						tv.ErrMsg = t.Capture.ErrMsg
					}
				}
				tenantID := t.TenantID
				ip, ua := t.IP, t.UserAgent
				select {
				case tracePersistSem <- struct{}{}:
					go func() {
						defer func() { <-tracePersistSem }()
						tv.Browser, tv.OS = observability.ParseUserAgent(ua)
						tv.Country = a.geo.Country(ip)
						if err := a.obsStore.SaveSlowTrace(tenantID, tv); err != nil {
							log.Printf("save slow trace [%s]: %v", tenantID, err)
						}
					}()
				default:
				}
			}
		},
	))
	r.Use(a.responseCache.Middleware)
	r.Use(auth.JWTMiddleware(a.cfg.JWTSecret, func(tenantID, reason string) {
		a.errStore.Record(tenantID, fmt.Errorf("jwt: %s", reason))
	}))
	r.Use(rbac.RBACMiddleware(mustMarshal(surf.schema.RBAC)))
	r.Use(chimiddleware.Recoverer)

	if a.cfg.Env == "development" {
		// Dev-only pprof. The port was ":6060" hardcoded until MT-STRUCT-S1;
		// APPITOOLS_PPROF_PORT overrides it (default preserved) so N dev
		// engines can coexist on one box.
		pprofAddr := ":6060"
		if v := os.Getenv("APPITOOLS_PPROF_PORT"); v != "" {
			if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
				pprofAddr = fmt.Sprintf(":%d", n)
			}
		}
		pprofMux := chimiddleware.Profiler()
		pprofSrv := &http.Server{Addr: pprofAddr, Handler: pprofMux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Printf("WARNING: pprof profiler enabled on %s (APPITOOLS_ENV=development)", pprofAddr)
			if err := pprofSrv.ListenAndServe(); err != nil {
				log.Println("pprof server:", err)
			}
		}()
	}

	r.Handle("/metrics", observability.AdminAuth(adminKey, a.metrics.Handler()))
	r.Mount("/debug", a.obsServer.DebugRouter(adminKey))
	r.Method(http.MethodPost, "/admin/backup", observability.AdminAuth(adminKey, backupHandler(a.pool)))
	r.Method(http.MethodPost, "/admin/tenants/{id}/reload",
		observability.AdminAuth(adminKey, reloadHandler(a.cpSvc, a.responseCache, a.schemaCache, a.schema)))

	// Admin API (ADMIN-API-V1): platform super-admin auth + tenant/user/
	// observability management. Routes live under /admin/* (JWT-skipped,
	// RBAC-passthrough — they do their OWN auth) and are registered individually so
	// they coexist with the machine endpoints above (no Mount collision). They are
	// NEW routes off the CRUD/JWT hot path — the gate measures no_change.
	if a.platformAdmin != nil {
		a.platformAdmin.Register(r, a.obsServer, adminKey)
	}

	// Admin panel UI (ADMIN-UI-V1): the embedded SolidJS SPA served under /admin
	// (shell at /admin, hashed assets at /admin/assets/*). Hash routing keeps client
	// routes in the URL fragment so they never collide with the /admin/* API above.
	// Static serving — no hot-path impact. If the bundle was not built before
	// `go build` (only the placeholder index.html is embedded), the shell still
	// loads but is empty; log a hint.
	if err := adminui.Register(r); err != nil {
		log.Printf("WARNING: admin UI not mounted: %v", err)
	} else if !adminui.HasBuiltAssets() {
		log.Println("admin UI: serving /admin (WARNING: no built assets embedded — run `make admin-ui` (npm run build) before `go build`)")
	} else {
		log.Println("admin UI: SolidJS admin panel served at /admin")
	}

	// Visual schema editor (UI-F0-S1): the embedded Svelte 5 SPA served at /editor
	// (shell at /editor, hashed assets at /editor/assets/*). Static serving — no
	// hot-path impact, tenant-agnostic, JWT-skipped (see pkg/auth.skipJWT). Built
	// with `make editor-ui` (npm run build) before `go build`; without it only the
	// placeholder shell is embedded (logged).
	if err := editorui.Register(r); err != nil {
		log.Printf("WARNING: visual editor not mounted: %v", err)
	} else if !editorui.HasBuiltAssets() {
		log.Println("editor: serving /editor (WARNING: no built assets embedded — run `make editor-ui` (npm run build) before `go build`)")
	} else {
		log.Println("editor: visual schema editor (Appitools Studio) served at /editor")
	}
	// EDITOR-BOOT-SYNC: the SOURCE schema the engine serves, so Studio boots
	// showing reality instead of a frontend sample. Same thin/read-only class
	// as /editor/validate (JWT-skipped via the /editor prefix, off hot path).
	r.Get("/editor/current-schema", a.serveCurrentSchema)

	r.Get("/healthz", a.ss.HealthzHandler)
	r.Get("/readyz", a.ss.ReadyzHandler)
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "ok",
			"version": a.version,
		})
	})

	// OpenAPI spec (/openapi.json, /openapi.yaml) + Swagger UI (/docs) — the public,
	// interactive API contract (API-PRODUCTIVA-V1). Unauthenticated (JWT-skipped),
	// off the CRUD hot path; derived from THIS surface's schema (a hot-swap
	// rebuilds it live).
	a.registerOpenAPIRoutes(r, surf.schema)

	// Password identity core (AUTH-CORE-V1): /auth/signup, /auth/login,
	// /auth/refresh. These flow through the SAME chain (tenant → rate limit →
	// JWT[skipped for /auth/] → RBAC[passes through, not /api/]) — no duplicated
	// middleware. They are unauthenticated (the JWT skip list covers "/auth/")
	// but tenant-aware (Host subdomain), so a login authenticates against ONE
	// tenant's users only.
	if a.authSvc != nil {
		r.With(appmiddleware.StrictCSP).Mount("/auth", a.authSvc.Router())
	}

	r.With(appmiddleware.StrictCSP).Handle("/graphql",
		gqlhandler.BuildHandler(surf.schema, a.tdb, a.hr, surf.policy, a.eventsHub))
	if a.cfg.Env == "development" {
		r.With(appmiddleware.PermissiveCSP).Handle("/graphiql", gqlhandler.PlaygroundHandler("/graphql"))
		log.Println("GraphiQL playground enabled at /graphiql (APPITOOLS_ENV=development)")
	}

	r.Group(func(sub chi.Router) {
		sub.Use(appmiddleware.StrictCSP)
		// Custom routes mount FIRST (literal paths; collision-checked at Register
		// so they never overlap the generated routes). They flow through the same
		// chain established above — no duplicated middleware.
		for _, rt := range a.routes {
			sub.Method(rt.Method, rt.Path, a.customHandler(rt))
		}
		// File store routes (FILES-V2): upload/download/delete/signed-URL flow
		// through the IDENTICAL chain (tenant → JWT → RBAC for the "files"
		// resource: create/read/delete) — no duplicated middleware. Download is
		// bypassed by the response cache (see cache Middleware) so a binary blob is
		// never buffered/cached in RAM. Skipped if a schema resource is literally
		// named "files" (the generated routes win).
		if _, taken := surf.schema.Resources["files"]; taken {
			log.Println("WARNING: schema declares a \"files\" resource — engine file-store routes (/api/files) are disabled for this schema")
		} else if a.files != nil {
			filesSecret := []byte(a.cfg.JWTSecret)
			policy := surf.policy // capture THIS surface's policy (a hot-swap builds a new router with the new policy)
			sub.Post("/api/files", files.UploadHandler(a.files, a.filesMaxBytes))
			sub.Get("/api/files/{id}", files.DownloadHandler(a.files))
			sub.Get("/api/files/{id}/url", files.SignedURLHandler(a.files, filesSecret, a.filesTokenTTL))
			sub.Delete("/api/files/{id}", files.DeleteHandler(a.files))
			// Signed-token downloads live OUTSIDE /api: the token IS the credential
			// (JWT is skipped for this prefix — pkg/auth.skipJWT — and the RBAC
			// middleware only guards /api/*), but the handler re-verifies signature,
			// expiry, tenant match AND that the embedded role still reads "files".
			// Any failure is a uniform 404 (anti-fingerprinting).
			sub.Get(files.SignedPathPrefix+"/{token}", files.SignedServeHandler(a.files, filesSecret,
				func(role string) bool { return policy.Allows(role, "files", "read") }))
		}
		sub.Mount("/", codegen.BuildRouter(surf.schema, a.tdb, a.hr, a.responseCache, a.eventsHub))
	})

	// Publish as the CURRENT router (flow-test runner target) — on boot and on
	// every hot-swap, so flows always run against the live surface.
	a.currentRouter.Store(r)
	return r
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// coalesce returns a when non-empty, else b.
func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// coalesceCSV returns cfgVal when non-empty, otherwise the comma-separated env
// string parsed into a trimmed, non-empty slice (nil when both are empty). Used to
// resolve the CORS list config fields against their APPITOOLS_CORS_* env vars.
func coalesceCSV(cfgVal []string, env string) []string {
	if len(cfgVal) > 0 {
		return cfgVal
	}
	if strings.TrimSpace(env) == "" {
		return nil
	}
	parts := strings.Split(env, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// envTruthy reports whether an env-var string means "on" (true/1/on/yes,
// case-insensitive). Empty or anything else is false.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// ── relocated engine helpers (moved verbatim from cmd/appitools) ────────────

// isInfraPath reports whether p is an internal infrastructure/admin endpoint
// that must not be counted as tenant application traffic.
func isInfraPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/metrics"),
		strings.HasPrefix(p, "/debug"),
		strings.HasPrefix(p, "/admin"),
		strings.HasPrefix(p, "/editor"),
		strings.HasPrefix(p, "/health"),
		strings.HasPrefix(p, "/readyz"):
		return true
	default:
		return false
	}
}

// flushObsSnapshots persists a per-tenant observability snapshot every 60s.
func flushObsSnapshots(ctx context.Context, store *observability.ObsStore, rings *observability.Rings, hist *observability.TenantHistogram, slo *observability.SLOEngine) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().Unix()
			for _, tid := range rings.TenantIDs() {
				var p50, p95 int64
				if fs := hist.FullSnapshot(tid); fs != nil && fs.Uncached != nil {
					p50 = int64(fs.Uncached.P50Us)
					p95 = int64(fs.Uncached.P95Us)
				}
				s := slo.Snapshot(tid)
				if err := store.Flush(tid, observability.Snapshot{
					TenantID:   tid,
					TS:         now,
					P50US:      p50,
					P95US:      p95,
					ErrorRatio: s.ErrorRatio5m,
					BurnRate:   s.BurnRate5m,
					SLOStatus:  s.Status,
				}); err != nil {
					log.Printf("obs store flush tenant %s: %v", tid, err)
				}
			}
			if err := store.Prune(); err != nil {
				log.Printf("obs store prune: %v", err)
			}
		}
	}
}

// backupHandler returns the POST /admin/backup handler.
func backupHandler(pool *pgxpool.Pool) http.HandlerFunc {
	outputDir := os.Getenv("BACKUP_DIR")
	if outputDir == "" {
		outputDir = "/tmp/appitools-backups"
	}
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tenantParam := req.URL.Query().Get("tenant")
		if tenantParam == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "tenant query parameter is required"}) //nolint:errcheck
			return
		}
		results, err := scripts.Backup(req.Context(), pool, tenantParam, outputDir)
		if errors.Is(err, scripts.ErrPgDumpNotFound) {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "pg_dump is not available on this host"}) //nolint:errcheck
			return
		}
		if err != nil || len(results) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("backup failed: %v", err)}) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(results[0]) //nolint:errcheck
	}
}

// reloadHandler returns the POST /admin/tenants/{id}/reload handler (ADR-014
// manual workaround): verify tenant exists → evict response cache → warm-reload
// schema; warns on hook drift (hooks are boot-compiled).
func reloadHandler(svc controlplane.Service, rc *cache.ResponseCache, sc *tenant.SchemaCache, bootSchema *schema.APISchema) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		w.Header().Set("Content-Type", "application/json")

		id := chi.URLParam(req, "id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "tenant id is required"}) //nolint:errcheck
			return
		}

		if _, err := svc.GetByID(req.Context(), id); err != nil {
			if errors.Is(err, controlplane.ErrNotFound) {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "tenant_id": id, "error": "tenant not found"}) //nolint:errcheck
				return
			}
			logging.Log.Error().Str("tenant_id", id).Err(err).Msg("admin reload: tenant lookup failed")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "tenant_id": id, "error": "reload failed"}) //nolint:errcheck
			return
		}

		rc.Invalidate(id)

		apiSchema, err := svc.GetSchema(req.Context(), id)
		if err != nil {
			logging.Log.Error().Str("tenant_id", id).Err(err).Msg("admin reload: schema reload failed")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "tenant_id": id, "error": "reload failed"}) //nolint:errcheck
			return
		}
		if apiSchema != nil {
			sc.Set(id, apiSchema)
		}

		var hookDrift []string
		if apiSchema != nil {
			hookDrift = hooksDiffering(bootSchema, apiSchema)
		}

		reloadedAt := time.Now().UTC().Format(time.RFC3339)
		logging.Log.Info().
			Str("tenant_id", id).
			Str("remote_ip", remoteIP(req)).
			Int64("latency_ms", time.Since(start).Milliseconds()).
			Msg("admin reload: tenant reloaded")

		resp := map[string]any{"ok": true, "tenant_id": id, "reloaded_at": reloadedAt}
		if len(hookDrift) > 0 {
			warning := fmt.Sprintf(
				"hooks changed for %s but hooks are compiled at boot — a process restart is required for hook changes to take effect",
				strings.Join(hookDrift, ", "))
			resp["warnings"] = []string{warning}
			logging.Log.Warn().
				Str("tenant_id", id).
				Strs("resources", hookDrift).
				Msg("admin reload: stored-schema hooks differ from boot-compiled hooks; restart required")
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}
}

// hooksDiffering returns the resource names whose hook set in the stored schema
// differs from the boot-compiled one. Sorted for stable output.
func hooksDiffering(boot, stored *schema.APISchema) []string {
	if boot == nil {
		return nil
	}
	var out []string
	for name, res := range stored.Resources {
		bootRes, known := boot.Resources[name]
		switch {
		case !known:
			if len(res.Hooks) > 0 {
				out = append(out, name)
			}
		case !reflect.DeepEqual(res.Hooks, bootRes.Hooks):
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// remoteIP returns the client IP without the port (X-Forwarded-For ignored;
// admin endpoints are localhost-only in prod).
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// startCacheInvalidator LISTENs on the "schema_updated" NOTIFY channel and
// invalidates the named tenant's cached responses on each notification.
func startCacheInvalidator(ctx context.Context, pool *pgxpool.Pool, rc *cache.ResponseCache) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Printf("cache invalidator: acquire conn: %v", err)
		return
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "LISTEN schema_updated"); err != nil {
		log.Printf("cache invalidator: LISTEN failed: %v", err)
		return
	}
	log.Println("cache invalidator: listening on schema_updated")

	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("cache invalidator: notification error: %v", err)
			return
		}
		rc.Invalidate(n.Payload)
		log.Printf("cache invalidator: invalidated tenant %q (pg_notify)", n.Payload)
	}
}
