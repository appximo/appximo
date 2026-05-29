package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
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

		policyBytes, err := json.Marshal(s.RBAC)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error serializing RBAC policy:", err)
			os.Exit(1)
		}

		// Outer router: middleware must be registered before routes.
		r := chi.NewMux()
		r.Use(chimiddleware.RealIP)
		r.Use(chimiddleware.RequestID)
		r.Use(tenant.TenantMiddleware)
		r.Use(auth.JWTMiddleware(jwtSecret))
		r.Use(rbac.RBACMiddleware(policyBytes))
		r.Use(chimiddleware.Logger)
		r.Use(chimiddleware.Recoverer)

		r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"version": "0.1.0",
			})
		})

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
