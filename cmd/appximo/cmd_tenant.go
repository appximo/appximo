package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/platformadmin"
)

// DEV-CLEANUP-S1: tenant management from the trusted machine context (this CLI
// with DATABASE_URL — the same trust boundary as `appximo admin create` and
// the X-Admin-Key). `list` is the inventory (what would the deploy modal
// show?); `delete` is the SAME destructive path the admin API uses
// (platformadmin.DeleteTenant: DROP SCHEMA CASCADE + every control-plane row —
// policies, migration log, schema history, flows, outbox — no orphans), gated
// behind --yes. Off the hot path entirely.
var tenantCmd = &cobra.Command{
	Use:   "tenant",
	Short: "Manage tenants (list / delete) — keeps the environment free of leftover test tenants",
}

func tenantPool(ctx context.Context) *pgxpool.Pool {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL environment variable is required")
		os.Exit(1)
	}
	pool, err := db.NewPool(ctx, connStr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect db:", err)
		os.Exit(1)
	}
	return pool
}

var tenantListCmd = &cobra.Command{
	Use:   "list",
	Short: "Tenant inventory: schema, resources, rows (~), users, created",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		pool := tenantPool(ctx)
		defer pool.Close()

		rows, err := pool.Query(ctx, `
			SELECT t.id,
			       COALESCE(t.json_schema::jsonb->>'name', '?')                       AS schema_name,
			       (SELECT COUNT(*) FROM jsonb_object_keys(
			           COALESCE(t.json_schema::jsonb->'resources', '{}'::jsonb)))     AS resources,
			       COALESCE((SELECT SUM(s.n_live_tup) FROM pg_stat_user_tables s
			           WHERE s.schemaname = 'tenant_'||t.id
			             AND s.relname NOT LIKE 'auth_%'), 0)                          AS approx_rows,
			       COALESCE((SELECT SUM(s.n_live_tup) FROM pg_stat_user_tables s
			           WHERE s.schemaname = 'tenant_'||t.id
			             AND s.relname = 'auth_users'), 0)                             AS users,
			       t.created_at::date::text                                            AS created
			FROM public.tenants t
			ORDER BY t.created_at, t.id`)
		if err != nil {
			fmt.Fprintln(os.Stderr, "tenant list:", err)
			os.Exit(1)
		}
		defer rows.Close()

		fmt.Printf("%-20s %-24s %9s %11s %6s %s\n", "TENANT", "SCHEMA", "RESOURCES", "ROWS(~)", "USERS", "CREATED")
		n := 0
		for rows.Next() {
			var id, schemaName, created string
			var resources, approxRows, users int64
			if err := rows.Scan(&id, &schemaName, &resources, &approxRows, &users, &created); err != nil {
				fmt.Fprintln(os.Stderr, "tenant list:", err)
				os.Exit(1)
			}
			fmt.Printf("%-20s %-24s %9d %11d %6d %s\n", id, schemaName, resources, approxRows, users, created)
			n++
		}
		fmt.Printf("%d tenant(s). Row counts are pg_stat estimates (auth_ tables excluded).\n", n)
	},
}

var tenantDeleteCmd = &cobra.Command{
	Use:   "delete <id> [<id>…]",
	Short: "Delete tenant(s) DESTRUCTIVELY: full schema + all its metadata (requires --yes)",
	Long: `Deletes tenants COMPLETELY and IRREVERSIBLY: the tenant's Postgres schema
(all its data, CASCADE) plus every control-plane row that references it
(policies, migration log, schema history, flow tests/runs, outbox) — the same
path as the admin API's DELETE /admin/tenants/{id}, no orphans left.

Without --yes it only PRINTS what would be deleted. There is no undo.`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		yes, _ := cmd.Flags().GetBool("yes")
		ctx := context.Background()
		pool := tenantPool(ctx)
		defer pool.Close()

		if !yes {
			fmt.Fprintf(os.Stderr, `tenant delete: this would IRREVERSIBLY delete %d tenant(s): %s
  - each tenant's Postgres schema (ALL its data, DROP CASCADE)
  - its control-plane metadata (policies, migration log, schema history, flows, outbox)
Re-run with --yes to confirm.
`, len(args), strings.Join(args, ", "))
			os.Exit(1)
		}

		svc := platformadmin.NewService(
			platformadmin.NewStore(pool), nil, controlplane.NewService(pool, nil), pool,
			platformadmin.Config{JWTSecret: os.Getenv("JWT_SECRET")},
		)
		failures := 0
		for _, id := range args {
			if err := svc.DeleteTenant(ctx, id, id); err != nil {
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", id, err)
				failures++
				continue
			}
			fmt.Printf("✓ deleted tenant %q (schema + all metadata)\n", id)
		}
		if failures > 0 {
			os.Exit(1)
		}
	},
}

func init() {
	tenantDeleteCmd.Flags().Bool("yes", false, "confirm the irreversible deletion (required)")
	tenantCmd.AddCommand(tenantListCmd, tenantDeleteCmd)
	rootCmd.AddCommand(tenantCmd)
}
