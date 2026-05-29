package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Aplica migraciones a un tenant directamente (sin Redis)",
	Long: `Aplica CREATE TABLE IF NOT EXISTS para cada entidad del schema
al schema de Postgres del tenant especificado.

Útil para instalaciones on-premise o para depurar sin un worker de Redis.`,
	Run: func(cmd *cobra.Command, args []string) {
		schemaFile, _ := cmd.Flags().GetString("schema")
		tenantID, _ := cmd.Flags().GetString("tenant")

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

		pgSchema := "tenant_" + tenantID
		fmt.Printf("Applying migrations to schema %q...\n", pgSchema)

		if err := migration.ApplyTenantMigration(ctx, pool, pgSchema, s); err != nil {
			fmt.Fprintln(os.Stderr, "Migration failed:", err)
			os.Exit(1)
		}

		// Print each table applied (sorted for deterministic output).
		tables := make([]string, 0, len(s.Resources))
		for name := range s.Resources {
			tables = append(tables, name)
		}
		sort.Strings(tables)
		for _, t := range tables {
			fmt.Printf("  ✓ %s.%s\n", pgSchema, t)
		}
		fmt.Println("Done.")
	},
}

func init() {
	migrateCmd.Flags().String("schema", "schema.json", "path to schema.json")
	migrateCmd.Flags().String("tenant", "", "tenant ID (required)")
	migrateCmd.MarkFlagRequired("tenant")
	rootCmd.AddCommand(migrateCmd)
}
