package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"

	"github.com/redis/go-redis/v9"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	gqlhandler "github.com/miguelangel/appitools/pkg/graphql"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Levanta el servidor HTTP multi-tenant",
	Run: func(cmd *cobra.Command, args []string) {
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

		// Control plane (port 9090) — start in background goroutine.
		cpSvc := controlplane.NewService(pool, redisClient)
		cpRouter := controlplane.NewControlPlaneRouter(cpSvc, adminKey)
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

		// Outer router: middleware must be registered before routes.
		r := chi.NewMux()
		r.Use(appmiddleware.SecurityHeaders)
		r.Use(chimiddleware.RealIP)
		r.Use(chimiddleware.RequestID)
		r.Use(tenant.TenantMiddleware)
		r.Use(auth.JWTMiddleware(jwtSecret))
		r.Use(rbac.RBACMiddleware(policyBytes))
		r.Use(chimiddleware.Logger)
		r.Use(chimiddleware.Recoverer)

		// pprof endpoints — only when APPITOOLS_ENV=development (never in production)
		if os.Getenv("APPITOOLS_ENV") == "development" {
			r.Mount("/debug/pprof", chimiddleware.Profiler())
			log.Println("WARNING: pprof profiler enabled (APPITOOLS_ENV=development)")
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
