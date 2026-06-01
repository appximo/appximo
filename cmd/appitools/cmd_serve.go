package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
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
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	"github.com/miguelangel/appitools/pkg/logging"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/observability"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/spf13/cobra"
)

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

		ctx := context.Background()
		pool, err := db.NewPool(ctx, connStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error conectando a la DB:", err)
			os.Exit(1)
		}
		defer pool.Close()

		tdb := db.NewTenantDB(pool)
		hr := extensions.NewHookRunner(extensions.NewJSSandbox())

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
		synthmon := observability.NewSyntheticMonitor(nil) // no synthetic checks by default
		obsServer := observability.NewObsServer(hist, errStore, anomaly, synthmon)

		// Control plane (port 9090) — start in background goroutine.
		cpSvc := controlplane.NewService(pool, redisClient)
		cpRouter := controlplane.NewControlPlaneRouter(cpSvc, adminKey)
		// Mount debug endpoints on the control plane (admin-protected via obs router's own key check).
		cpRouter.Mount("/", obsServer.Router(adminKey))
		go func() {
			fmt.Println("Control plane serving on :9090")
			if err := http.ListenAndServe(":9090", cpRouter); err != nil {
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

		// Response cache: 5-second TTL, invalidated by pg_notify schema_updated.
		responseCache := cache.New(5 * time.Second)
		go startCacheInvalidator(ctx, pool, responseCache)

		// Outer router: middleware must be registered before routes.
		r := chi.NewMux()
		r.Use(appmiddleware.SecurityHeaders)
		r.Use(chimiddleware.Compress(5, "application/json", "application/graphql+json"))
		r.Use(chimiddleware.RealIP)
		r.Use(chimiddleware.RequestID)
		r.Use(tenant.TenantMiddleware)
		// RequestLogger BEFORE cache: every request (including cache hits) is logged and measured.
		r.Use(logging.RequestLogger(hist.Record, func(id string, us float64) {
			anomaly.Observe(id, us) //nolint:errcheck
		}))
		r.Use(responseCache.Middleware)
		r.Use(auth.JWTMiddleware(jwtSecret))
		r.Use(rbac.RBACMiddleware(policyBytes))
		r.Use(chimiddleware.Recoverer)

		// pprof on a separate port — only in development, no auth, never reachable in production
		if os.Getenv("APPITOOLS_ENV") == "development" {
			pprofMux := chimiddleware.Profiler()
			go func() {
				log.Println("WARNING: pprof profiler enabled on :6060 (APPITOOLS_ENV=development)")
				if err := http.ListenAndServe(":6060", pprofMux); err != nil {
					log.Println("pprof server:", err)
				}
			}()
		}

		r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"version": "0.1.0",
			})
		})

		// GraphQL endpoint (always available).
		r.Handle("/graphql", gqlhandler.BuildHandler(s, tdb, hr, &rbacPolicy))

		// GraphiQL playground — only in development.
		if os.Getenv("APPITOOLS_ENV") == "development" {
			r.Handle("/graphiql", gqlhandler.PlaygroundHandler("/graphql"))
			log.Println("GraphiQL playground enabled at /graphiql (APPITOOLS_ENV=development)")
		}

		// Mount API routes (BuildRouter registers /api/* routes only).
		r.Mount("/", codegen.BuildRouter(s, tdb, hr))

		addr := fmt.Sprintf(":%d", port)
		fmt.Printf("Appitools serving on %s — Ctrl+C to stop\n", addr)
		if err := http.ListenAndServe(addr, r); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

func init() {
	serveCmd.Flags().String("schema", "schema.json", "path to schema.json")
	serveCmd.Flags().Int("port", 8080, "HTTP port to listen on")
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
