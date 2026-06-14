package appitools

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/files"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/logging"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/outbox"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/shutdown"
	"github.com/miguelangel/appitools/pkg/tenant"
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

	files         files.VFS // content-addressable file store (FILES-V1)
	filesMaxBytes int64     // per-upload body cap

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

	s, err := schema.LoadFromFile(cfg.SchemaPath)
	if err != nil {
		return nil, fmt.Errorf("appitools: read schema: %w", err)
	}
	if errs := schema.Validate(s); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return nil, fmt.Errorf("appitools: invalid schema:\n  %s", strings.Join(msgs, "\n  "))
	}
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

	app.tdb = db.NewTenantDB(pool)

	// Content-addressable file store (FILES-V1). The blob root is created lazily
	// on the first upload, so an engine that never serves /api/files pays nothing.
	filesDir := cfg.FilesDir
	if filesDir == "" {
		filesDir = os.Getenv("APPITOOLS_FILES_DIR")
	}
	if filesDir == "" {
		filesDir = "/var/lib/appitools/files"
	}
	app.files = files.NewLocal(filesDir, files.NewPGStore(pool))
	app.filesMaxBytes = files.DefaultMaxUploadBytes
	if v := os.Getenv("APPITOOLS_FILES_MAX_BYTES"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			app.filesMaxBytes = n
		}
	}
	log.Printf("files: content-addressable store at %s (max upload %d bytes)", filesDir, app.filesMaxBytes)

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

	if st, openErr := observability.OpenStore(os.Getenv("OBS_DB_PATH")); openErr != nil {
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

	// Control plane (:9090) router — started in Start.
	app.cpSvc = controlplane.NewService(pool, app.redisClient)
	cpRouter := controlplane.NewControlPlaneRouter(app.cpSvc, adminKey)
	cpRouter.Mount("/", app.obsServer.Router(adminKey))
	app.cpSrv = &http.Server{
		Addr:              ":9090",
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

	// Response cache (5s TTL) + role gate.
	app.responseCache = cache.New(5 * time.Second)
	app.responseCache.SetRoleCacheGate(func(role string) bool {
		rp, ok := rbacPolicy.Roles[role]
		if !ok {
			return false
		}
		return rp.Conditions == nil && len(rp.FieldsAllow) == 0
	})

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
	a.started = true

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go a.sloEngine.Run(ctx)
	if a.obsStore != nil {
		go flushObsSnapshots(ctx, a.obsStore, a.rings, a.hist, a.sloEngine)
	}

	if os.Getenv("APPITOOLS_SYNTHETIC") != "off" {
		a.synthmon.AddDynamic(observability.DynamicCheck{
			Name:    "api-canary",
			Resolve: canaryResolver(a.pool, a.schema, a.cfg.JWTSecret, a.cfg.Port),
		})
		a.synthmon.Start(ctx, 60*time.Second)
	}

	go func() {
		fmt.Println("Control plane serving on :9090")
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

	r := a.buildRouter()

	addr := fmt.Sprintf(":%d", a.cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("Appitools serving on %s — Ctrl+C to stop\n", addr)
	cleanup := func() {
		a.pool.Close()
		if a.obsStore != nil {
			a.obsStore.Close() //nolint:errcheck
		}
	}
	if err := a.ss.Run(ctx, srv, 5*time.Second, cleanup); err != nil {
		return err
	}
	log.Println("server shut down cleanly")
	return nil
}

// buildRouter assembles the outer chi router: the exact same middleware chain
// generated routes have always paid (security headers → compression → request
// id → tenant → rate limit → request logger → response cache → JWT → RBAC →
// recoverer), then the infra/admin endpoints, then the /api group where custom
// routes and the generated router mount. Custom routes go through the IDENTICAL
// chain — no duplicated middleware (ADR-016 Decision 4C).
func (a *App) buildRouter() *chi.Mux {
	adminKey := a.cfg.AdminKey
	r := chi.NewMux()
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(chimiddleware.Compress(5, "application/json", "application/graphql+json"))
	r.Use(chimiddleware.RequestID)
	r.Use(tenant.TenantMiddleware)
	r.Use(resilience.RateLimit(a.tenantLimiter))

	tracePersistSem := make(chan struct{}, 64)
	r.Use(logging.RequestLogger(
		a.hist.Record,
		func(id string, us float64) {
			if isAnomaly, zScore := a.anomaly.Observe(id, us); isAnomaly {
				a.anomaly.IncrCounter(id)
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
	r.Use(rbac.RBACMiddleware(mustMarshal(a.schema.RBAC)))
	r.Use(chimiddleware.Recoverer)

	if a.cfg.Env == "development" {
		pprofMux := chimiddleware.Profiler()
		pprofSrv := &http.Server{Addr: ":6060", Handler: pprofMux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			log.Println("WARNING: pprof profiler enabled on :6060 (APPITOOLS_ENV=development)")
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

	r.Get("/healthz", a.ss.HealthzHandler)
	r.Get("/readyz", a.ss.ReadyzHandler)
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"status":  "ok",
			"version": a.version,
		})
	})

	r.With(appmiddleware.StrictCSP).Handle("/graphql",
		gqlhandler.BuildHandler(a.schema, a.tdb, a.hr, a.rbacPolicy, a.eventsHub))
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
		// File store routes (FILES-V1): upload/download flow through the IDENTICAL
		// chain (tenant → JWT → RBAC for the "files" resource) — no duplicated
		// middleware. Download is bypassed by the response cache (see cache
		// Middleware) so a binary blob is never buffered/cached in RAM. Skipped if a
		// schema resource is literally named "files" (the generated routes win).
		if _, taken := a.schema.Resources["files"]; taken {
			log.Println("WARNING: schema declares a \"files\" resource — engine file-store routes (/api/files) are disabled for this schema")
		} else if a.files != nil {
			sub.Post("/api/files", files.UploadHandler(a.files, a.filesMaxBytes))
			sub.Get("/api/files/{id}", files.DownloadHandler(a.files))
		}
		sub.Mount("/", codegen.BuildRouter(a.schema, a.tdb, a.hr, a.responseCache, a.eventsHub))
	})

	return r
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ── relocated engine helpers (moved verbatim from cmd/appitools) ────────────

// isInfraPath reports whether p is an internal infrastructure/admin endpoint
// that must not be counted as tenant application traffic.
func isInfraPath(p string) bool {
	switch {
	case strings.HasPrefix(p, "/metrics"),
		strings.HasPrefix(p, "/debug"),
		strings.HasPrefix(p, "/admin"),
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
