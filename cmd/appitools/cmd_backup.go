package main

import (
	"context"
	"fmt"
	"os"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/logging"
	"github.com/miguelangel/appitools/scripts"
	"github.com/spf13/cobra"
)

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Dump tenant schemas with pg_dump (custom format, compressed)",
	Long: `Runs pg_dump --format=custom --compress=9 for one tenant (--tenant) or for
every tenant listed in public.tenants. Connection comes from DATABASE_URL.`,
	Run: func(cmd *cobra.Command, args []string) {
		logging.Init(os.Getenv("APPITOOLS_ENV"))

		tenantID, _ := cmd.Flags().GetString("tenant")
		outputDir, _ := cmd.Flags().GetString("out")

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

		if err := scripts.RunBackup(ctx, pool, tenantID, outputDir); err != nil {
			fmt.Fprintln(os.Stderr, "Backup failed:", err)
			os.Exit(1)
		}
		fmt.Println("Done.")
	},
}

func init() {
	backupCmd.Flags().String("tenant", "", "tenant ID to back up (empty = all tenants)")
	backupCmd.Flags().String("out", "/tmp/appitools-backups", "output directory for dump files")
	rootCmd.AddCommand(backupCmd)
}
