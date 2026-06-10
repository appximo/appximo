package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	_ "go.uber.org/automaxprocs"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/events"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/logging"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/resilience"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/shutdown"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/miguelangel/appitools/scripts"
	"github.com/spf13/cobra"
)

// debugTracesHTML is the embedded trace-explorer UI served at /debug/traces.
//
//go:embed static/debug_traces.html
var debugTracesHTML []byte

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Levanta el servidor HTTP multi-tenant",
	Run: func(cmd *cobra.Command, args []string) {
		// TAREA 4: GOGC / GOMEMLIMIT from env.
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

		// TAREA 3: init structured logger.
		logging.Init(os.Getenv("APPITOOLS_ENV"))

		// TAREA 1: automaxprocs already applied via blank import; log the result.
		log.Printf("GOMAXPROCS=%d NumCPU=%d", runtime.GOMAXPROCS(0), runtime.NumCPU())

		schemaFile, _ := cmd.Flags().GetString("schema")
		port, _ := cmd.Flags().GetInt("port")

		s, err := schema.LoadFromFile(schemaFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error leyendo schema:", err)
			os.Exit(1)
		}
		if errs := schema.Validate(s); len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "Schema inválido:")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, " ", e.Error())
			}
			os.Exit(1)
		}

		connStr := os.Getenv("DATABASE_URL")
		if connStr == "" {
			fmt.Fprintln(os.Stderr, "DATABASE_URL environment variable is required")
			os.Exit(1)
		}

		// Signal-aware context: cancelled on SIGINT or SIGTERM.
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		pool, err := db.NewPool(ctx, connStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error conectando a la DB:", err)
			os.Exit(1)
		}
		// Pool is closed in the shutdown sequence — not deferred here.

		tdb := db.NewTenantDB(pool)
		// HookRunner with Capa 3 (WASM) enabled when the runtime initializes; falls
		// back to JS+webhook only on error so a WASM problem never blocks startup.
		sandbox := extensions.NewJSSandbox()
		var hr *extensions.HookRunner
		if wasmRunner, werr := extensions.NewWasmRunner(ctx); werr != nil {
			log.Printf("WARNING: WASM runtime (Capa 3) disabled: %v", werr)
			hr = extensions.NewHookRunner(sandbox)
		} else {
			hr = extensions.NewHookRunnerWithWasm(sandbox, extensions.NewWebhookDispatcher(), wasmRunner)
			log.Println("WASM runtime (Capa 3) enabled")
		}

		jwtSecret := os.Getenv("JWT_SECRET")
		if jwtSecret == "" {
			fmt.Fprintln(os.Stderr, "JWT_SECRET environment variable is required")
			os.Exit(1)
		}

		adminKey := os.Getenv("ADMIN_KEY")
		if adminKey == "" {
			fmt.Fprintln(os.Stderr, "ADMIN_KEY environment variable is required")
			os.Exit(1)
		}

		// Redis is optional — enqueueing skipped when not configured.
		var redisClient *redis.Client
		if redisURL := os.Getenv("REDIS_URL"); redisURL != "" {
			opts, parseErr := redis.ParseURL(redisURL)
			if parseErr != nil {
				fmt.Fprintln(os.Stderr, "Warning: invalid REDIS_URL:", parseErr)
			} else {
				redisClient = redis.NewClient(opts)
			}
		}

		schemaCache := tenant.NewSchemaCache()

		// Stage 1 observability components (created before routers so RequestLogger can use them).
		hist := observability.NewTenantHistogram()
		anomaly := observability.NewAnomalyDetector()
		errStore := observability.NewErrorStore()
		// Stage 2: Prometheus metrics + per-tenant ring buffer of recent requests.
		metrics := observability.NewMetrics()
		rings := observability.NewRings()
		// Stage 3: SLO burn-rate engine + alerter (Slack if SLACK_WEBHOOK_URL set, else noop).
		alerter := observability.NewSlackAlerterFromEnv()
		sloEngine := observability.NewSLOEngine(rings, hist, alerter)
		go sloEngine.Run(ctx)

		// Stage 4: persist observability snapshots to SQLite (survives restarts).
		// Empty OBS_DB_PATH falls back to /tmp/obs.db. Open failure is non-fatal.
		var obsStore *observability.ObsStore
		if st, openErr := observability.OpenStore(os.Getenv("OBS_DB_PATH")); openErr != nil {
			log.Printf("WARNING: observability store disabled: %v", openErr)
		} else {
			obsStore = st
			if pruneErr := obsStore.Prune(); pruneErr != nil {
				log.Printf("WARNING: obs store prune at startup: %v", pruneErr)
			}
			go flushObsSnapshots(ctx, obsStore, rings, hist, sloEngine)
		}

		// Synthetic monitor: JWT signed at startup (24h), never hardcoded.
		syntheticToken, syntheticErr := auth.GenerateToken(auth.Claims{
			UserID:   "synthetic-monitor",
			Role:     "super_admin",
			TenantID: "10",
		}, jwtSecret)
		if syntheticErr != nil {
			fmt.Fprintln(os.Stderr, "Warning: could not generate synthetic token:", syntheticErr)
		}
		synthChecks := []observability.Check{
			{
				Name:     "health",
				URL:      "http://localhost:8080/health",
				Method:   "GET",
				Expected: 200,
			},
			{
				Name:   "guides-api",
				URL:    "http://localhost:8080/api/guides",
				Method: "GET",
				Headers: map[string]string{
					"Host":          "10.localhost",
					"Authorization": "Bearer " + syntheticToken,
				},
				Expected: 200,
			},
		}
		synthmon := observability.NewSyntheticMonitor(synthChecks)
		synthmon.Start(ctx, 60*time.Second)
		// GeoLite2 country lookup (embedded mmdb, ~1µs, graceful if absent).
		geo := observability.DefaultGeoLookup()
		obsServer := observability.NewObsServer(hist, errStore, anomaly, synthmon, rings, sloEngine, obsStore)
		obsServer.SetGeo(geo)
		// /debug/traces visual: accept auth via ?key= OR X-Admin-Key — ONLY for this
		// HTML route, so it opens directly in a browser. The JSON debug APIs remain
		// header-only (handled by the DebugRouter's admin group).
		obsServer.SetTracesHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			key := req.URL.Query().Get("key")
			if key == "" {
				key = req.Header.Get("X-Admin-Key")
			}
			if subtle.ConstantTimeCompare([]byte(key), []byte(adminKey)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(debugTracesHTML) //nolint:errcheck
		}))

		// Control plane (port 9090) — start in background goroutine.
		cpSvc := controlplane.NewService(pool, redisClient)
		cpRouter := controlplane.NewControlPlaneRouter(cpSvc, adminKey)
		// Mount debug endpoints on the control plane (admin-protected via obs router's own key check).
		cpRouter.Mount("/", obsServer.Router(adminKey))
		// Same timeouts as the data plane (:8080) — a bare ListenAndServe has none,
		// leaving the control plane open to Slowloris.
		cpSrv := &http.Server{
			Addr:              ":9090",
			Handler:           cpRouter,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       20 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			fmt.Println("Control plane serving on :9090")
			if err := cpSrv.ListenAndServe(); err != nil {
				fmt.Fprintln(os.Stderr, "Control plane error:", err)
			}
		}()

		// Migration worker — only started when Redis is available.
		if redisClient != nil {
			worker := migration.NewMigrationWorker(redisClient, pool, schemaCache)
			go worker.Run(ctx)
			log.Println("Migration worker started")
		}

		policyBytes, err := json.Marshal(s.RBAC)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error serializing RBAC policy:", err)
			os.Exit(1)
		}

		var rbacPolicy rbac.Policy
		if err := json.Unmarshal(policyBytes, &rbacPolicy); err != nil {
			fmt.Fprintln(os.Stderr, "Error parsing RBAC policy:", err)
			os.Exit(1)
		}

		// SSE events hub (S45): in-process pub/sub behind GET /api/{r}/events.
		// Per-tenant subscriber cap via APPITOOLS_MAX_SSE_PER_TENANT (default 1000).
		maxSSE := 0
		if v := os.Getenv("APPITOOLS_MAX_SSE_PER_TENANT"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				maxSSE = n
			}
		}
		eventsHub := events.NewHub(maxSSE)

		// Response cache: 5-second TTL, invalidated by pg_notify schema_updated.
		responseCache := cache.New(5 * time.Second)
		// Only cache roles whose responses are identical for every user: no
		// row-level conditions and no field restrictions. Roles like operario
		// (conditions) or public (fields) bypass the cache so RBAC runs on every
		// request — otherwise a HIT would serve one user's rows to another.
		responseCache.SetRoleCacheGate(func(role string) bool {
			rp, ok := rbacPolicy.Roles[role]
			if !ok {
				return false // unknown role → never cache (fail safe)
			}
			return rp.Conditions == nil && len(rp.FieldsAllow) == 0
		})
		go startCacheInvalidator(ctx, pool, responseCache)
		auth.StartClaimsCacheGC(ctx)

		// Per-tenant token-bucket rate limiter. Each tenant has an independent
		// bucket, so a saturated tenant cannot starve another. Configurable via
		// RATE_LIMIT_RPS (default 1000) and RATE_LIMIT_BURST (default 100).
		rlRPS := 1000.0
		if v := os.Getenv("RATE_LIMIT_RPS"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				rlRPS = f
			}
		}
		rlBurst := 100
		if v := os.Getenv("RATE_LIMIT_BURST"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				rlBurst = n
			}
		}
		tenantLimiter := resilience.NewConfiguredLimiter(resilience.RateLimitConfig{RPS: rlRPS, Burst: rlBurst})
		log.Printf("rate limiter: %.0f RPS / %d burst per tenant", rlRPS, rlBurst)

		// Graceful-shutdown state: controls /readyz and the shutdown sequence.
		ss := shutdown.New()

		// Outer router: middleware must be registered before routes.
		r := chi.NewMux()
		r.Use(appmiddleware.SecurityHeaders)
		r.Use(chimiddleware.Compress(5, "application/json", "application/graphql+json"))
		// NOTE: chimiddleware.RealIP intentionally omitted. It overwrites
		// r.RemoteAddr from the spoofable X-Forwarded-For / X-Real-IP headers
		// (GHSA-3fxj-6jh8-hvhx). The only consumer of RemoteAddr is logging.clientIP,
		// which already reads those headers itself for the (best-effort) trace IP, so
		// RealIP added nothing but a spoofable RemoteAddr. Without it, RemoteAddr is
		// the real TCP peer. If a trusted reverse proxy is ever put in front, restore
		// a proxy-aware extraction here instead of RealIP.
		r.Use(chimiddleware.RequestID)
		r.Use(tenant.TenantMiddleware)
		// Rate limit per tenant, after the tenant is resolved but before any work
		// (cache lookup, DB) so an over-limit tenant is shed early with 429.
		r.Use(resilience.RateLimit(tenantLimiter))
		// tracePersistSem bounds in-flight slow/error-trace persistence goroutines.
		// Without it, an error storm spawns one goroutine per request, each writing
		// to SQLite — unbounded goroutines plus write contention. When the backlog
		// is full the trace is dropped rather than queued.
		tracePersistSem := make(chan struct{}, 64)

		// RequestLogger BEFORE cache: every request (including cache hits) is logged and measured.
		r.Use(logging.RequestLogger(
			hist.Record,
			func(id string, us float64) {
				if isAnomaly, zScore := anomaly.Observe(id, us); isAnomaly {
					anomaly.IncrCounter(id)
					logging.Log.Warn().
						Str("tenant_id", id).
						Float64("z_score", zScore).
						Int64("latency_us", int64(us)).
						Msg("anomaly detected")
				}
			},
			func(t logging.RequestTap) {
				// Feed Prometheus + the per-tenant ring with application traffic only.
				// Skip requests with no tenant and infra/admin endpoints (which, when hit
				// over the raw IP, would otherwise be tagged with a phantom tenant derived
				// from the first IP octet and pollute per-tenant series).
				if t.TenantID == "" || isInfraPath(t.Path) {
					return
				}
				metrics.ObserveRequest(t.TenantID, t.Method, t.Route,
					strconv.Itoa(t.Status), float64(t.DurationUS)/1e6)

				// Decode the 16-hex trace id into 8 raw bytes and copy the span
				// breakdown into the fixed-size Sample (value copies, no alloc).
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
					QueryUS:      0, // DB query time not yet threaded through the middleware
					Route:        rings.RouteID(t.Route),
					Status:       uint16(t.Status),
					TraceID:      traceID,
					Spans:        spans,
					NSpans:       uint8(n),
					ErrMsg:       t.ErrMsg,
					ErrorCapture: t.Capture, // nil unless a 500 captured a stack
					IP:           t.IP,
					UserAgent:    t.UserAgent,
				}
				rings.Record(t.TenantID, sample)
				metrics.SetActiveTenants(rings.Count())

				// Persist slow OR error traces (>50ms, or status >= 400)
				// asynchronously so the request path never blocks on SQLite.
				if obsStore != nil && observability.ShouldPersistTrace(sample) {
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
							// Parse UA + geo-resolve country off the request path.
							tv.Browser, tv.OS = observability.ParseUserAgent(ua)
							tv.Country = geo.Country(ip)
							if err := obsStore.SaveSlowTrace(tenantID, tv); err != nil {
								log.Printf("save slow trace [%s]: %v", tenantID, err)
							}
						}()
					default:
						// Persistence backlog full — drop rather than spawn an
						// unbounded goroutine or overwhelm SQLite.
					}
				}
			},
		))
		r.Use(responseCache.Middleware)
		r.Use(auth.JWTMiddleware(jwtSecret, func(tenantID, reason string) {
			errStore.Record(tenantID, fmt.Errorf("jwt: %s", reason))
		}))
		r.Use(rbac.RBACMiddleware(policyBytes))
		r.Use(chimiddleware.Recoverer)

		// pprof on a separate port — only in development, no auth, never reachable in production.
		if os.Getenv("APPITOOLS_ENV") == "development" {
			pprofMux := chimiddleware.Profiler()
			// ReadHeaderTimeout guards against Slowloris; no WriteTimeout so long
			// `profile?seconds=N` captures still work. Dev-only, never in prod.
			pprofSrv := &http.Server{Addr: ":6060", Handler: pprofMux, ReadHeaderTimeout: 10 * time.Second}
			go func() {
				log.Println("WARNING: pprof profiler enabled on :6060 (APPITOOLS_ENV=development)")
				if err := pprofSrv.ListenAndServe(); err != nil {
					log.Println("pprof server:", err)
				}
			}()
		}

		// Prometheus metrics — admin-key gated (same gate as /debug/tenant), JWT-exempt
		// via skipJWT. Never exposed without the X-Admin-Key header in production.
		r.Handle("/metrics", observability.AdminAuth(adminKey, metrics.Handler()))

		// Debug endpoints on the public listener too (admin-key gated, JWT-exempt).
		// The control plane (:9090) also exposes these, but it is not publicly reachable.
		r.Mount("/debug", obsServer.DebugRouter(adminKey))

		// Admin backup endpoint — admin-key gated, JWT-exempt. Runs pg_dump for the
		// requested tenant regardless of whether it is loaded in cache.
		r.Method(http.MethodPost, "/admin/backup",
			observability.AdminAuth(adminKey, backupHandler(pool)))

		// Admin schema reload — admin-key gated, JWT-exempt. The manual ADR-014
		// workaround: evict the tenant's response cache (so the next request
		// re-queries Postgres) and warm-reload its schema from public.tenants.
		// It does NOT hot-swap the compiled GraphQL/REST (deferred to Phase 2);
		// new resources/types still require a restart.
		r.Method(http.MethodPost, "/admin/tenants/{id}/reload",
			observability.AdminAuth(adminKey, reloadHandler(cpSvc, responseCache, schemaCache)))

		// Liveness — never touches Postgres.
		r.Get("/healthz", ss.HealthzHandler)
		// Readiness — returns 503 during drain/shutdown.
		r.Get("/readyz", ss.ReadyzHandler)
		// Legacy health endpoint (kept for backward compat).
		r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
				"status":  "ok",
				"version": "0.1.0",
			})
		})

		// GraphQL endpoint — strict CSP (JSON only, no HTML rendering).
		r.With(appmiddleware.StrictCSP).Handle("/graphql", gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy, eventsHub))

		// GraphiQL playground — only in development, permissive CSP for IDE assets.
		if os.Getenv("APPITOOLS_ENV") == "development" {
			r.With(appmiddleware.PermissiveCSP).Handle("/graphiql", gqlhandler.PlaygroundHandler("/graphql"))
			log.Println("GraphiQL playground enabled at /graphiql (APPITOOLS_ENV=development)")
		}

		// Mount API routes with strict CSP — /api/* serves JSON exclusively.
		r.Group(func(sub chi.Router) {
			sub.Use(appmiddleware.StrictCSP)
			sub.Mount("/", codegen.BuildRouter(s, tdb, hr, responseCache, eventsHub))
		})

		addr := fmt.Sprintf(":%d", port)
		srv := &http.Server{
			Addr:    addr,
			Handler: r,
			// Without these a slow client can hold connections open indefinitely
			// (Slowloris) and exhaust the server. ReadHeaderTimeout is the primary
			// Slowloris defense; WriteTimeout (> the 5s DB query timeout, with
			// serialization headroom) bounds slow readers; IdleTimeout reaps idle
			// keep-alive connections.
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       20 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		fmt.Printf("Appitools serving on %s — Ctrl+C to stop\n", addr)

		// Run blocks until the server is shut down, executing the mandated sequence:
		//   ready=0 → sleep 5s (LB drain) → Shutdown(10s) → cleanup (pool + obs store)
		cleanup := func() {
			pool.Close()
			if obsStore != nil {
				obsStore.Close() //nolint:errcheck
			}
		}
		if err := ss.Run(ctx, srv, 5*time.Second, cleanup); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		log.Println("server shut down cleanly")
	},
}

func init() {
	serveCmd.Flags().String("schema", "schema.json", "path to schema.json")
	serveCmd.Flags().Int("port", 8080, "HTTP port to listen on")
	rootCmd.AddCommand(serveCmd)
}

// isInfraPath reports whether p is an internal infrastructure/admin endpoint that
// must not be counted as tenant application traffic in metrics or the ring buffer.
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

// flushObsSnapshots persists a per-tenant observability snapshot every 60s until ctx
// is cancelled, combining histogram latency with SLO burn-rate state.
func flushObsSnapshots(
	ctx context.Context,
	store *observability.ObsStore,
	rings *observability.Rings,
	hist *observability.TenantHistogram,
	slo *observability.SLOEngine,
) {
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
			// Enforce retention on every tick (cheap indexed DELETEs): the 7-day
			// window AND the slow_traces row cap. Without this the table only ever
			// pruned at startup, growing unbounded on a long-running server.
			if err := store.Prune(); err != nil {
				log.Printf("obs store prune: %v", err)
			}
		}
	}
}

// backupHandler returns the POST /admin/backup handler. It requires a ?tenant= param,
// runs pg_dump for that schema, and returns the result as JSON. A missing pg_dump
// binary yields 503 so operators get a clear, actionable signal.
func backupHandler(pool *pgxpool.Pool) http.HandlerFunc {
	outputDir := os.Getenv("BACKUP_DIR")
	if outputDir == "" {
		outputDir = "/tmp/appitools-backups"
	}
	return func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tenant := req.URL.Query().Get("tenant")
		if tenant == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "tenant query parameter is required"}) //nolint:errcheck
			return
		}

		results, err := scripts.Backup(req.Context(), pool, tenant, outputDir)
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

// reloadHandler returns the POST /admin/tenants/{id}/reload handler — the manual
// ADR-014 workaround. For the tenant in the {id} path param it:
//  1. verifies the tenant exists in public.tenants (404 if not);
//  2. evicts that tenant's response-cache entries so the NEXT request re-queries
//     Postgres — REST (SELECT *) then reflects new columns immediately, with no 5s
//     TTL wait and no process restart; and
//  3. warm-loads + validates the tenant's stored schema from public.tenants into
//     the in-memory SchemaCache.
//
// It deliberately does NOT regenerate the compiled GraphQL schema or REST routes:
// those are built once at startup from a fixed schema, and hot-swapping them under
// load is deferred to Phase 2 (ADR-014). Adding a *resource* or GraphQL *type*
// therefore still requires a restart; this endpoint covers column-level changes and
// warms the schema cache without one. It never touches other tenants and never
// interrupts in-flight requests — Invalidate only drops cached copies, and Set
// replaces a map entry. DB errors are logged internally and never echoed into the
// response body.
func reloadHandler(svc controlplane.Service, rc *cache.ResponseCache, sc *tenant.SchemaCache) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		w.Header().Set("Content-Type", "application/json")

		id := chi.URLParam(req, "id")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "tenant id is required"}) //nolint:errcheck
			return
		}

		// 1. Tenant must exist in public.tenants.
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

		// 2. Evict cached responses so the next request re-queries the DB.
		rc.Invalidate(id)

		// 3. Warm-reload + validate the schema from the DB.
		apiSchema, err := svc.GetSchema(req.Context(), id)
		if err != nil {
			logging.Log.Error().Str("tenant_id", id).Err(err).Msg("admin reload: schema reload failed")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "tenant_id": id, "error": "reload failed"}) //nolint:errcheck
			return
		}
		// A nil schema means the tenant exists but has no schema set yet — not an
		// error; the cache eviction above still stands. Only store a real schema.
		if apiSchema != nil {
			sc.Set(id, apiSchema)
		}

		reloadedAt := time.Now().UTC().Format(time.RFC3339)
		logging.Log.Info().
			Str("tenant_id", id).
			Str("remote_ip", remoteIP(req)).
			Int64("latency_ms", time.Since(start).Milliseconds()).
			Msg("admin reload: tenant reloaded")

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"ok":          true,
			"tenant_id":   id,
			"reloaded_at": reloadedAt,
		})
	}
}

// remoteIP returns the client IP without the port. X-Forwarded-For / X-Real-IP are
// intentionally ignored (spoofable, dropped in a prior commit) and these admin
// endpoints are localhost-only in prod, so the peer address is the right source.
func remoteIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
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

// startCacheInvalidator listens on the Postgres "schema_updated" NOTIFY channel
// and calls rc.Invalidate with the payload (tenant ID) on each notification.
// Runs until ctx is cancelled; logs and returns on connection errors.
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
				return // normal shutdown
			}
			log.Printf("cache invalidator: notification error: %v", err)
			return
		}
		rc.Invalidate(n.Payload)
		log.Printf("cache invalidator: invalidated tenant %q (pg_notify)", n.Payload)
	}
}
