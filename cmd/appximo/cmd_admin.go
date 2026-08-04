package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/logging"
	"github.com/appximo/appximo/pkg/platformadmin"
)

// adminCmd groups platform-administration commands. The first is the BOOTSTRAP of
// the platform super-admin: a super-admin cannot be created by signing in as a
// super-admin that does not yet exist (the bootstrap problem), so the FIRST one is
// created from the trusted machine context — this CLI on the host with
// DATABASE_URL, the same trust boundary as the X-Admin-Key. There is no public
// super-admin signup endpoint.
var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Platform administration (super-admin bootstrap)",
}

var adminCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a platform super-admin (bootstrap the admin panel)",
	Long: `Creates a platform super-admin in the system schema (appximo_system),
above all tenants. Use this once to bootstrap the admin panel — the super-admin
then logs in at POST /admin/auth/login and manages everything from there.

DATABASE_URL and JWT_SECRET must be set (the same env the engine uses).
Example:
  DATABASE_URL=... JWT_SECRET=... \
    appximo admin create --email me@example.com --password 'a-strong-passphrase'`,
	Run: func(cmd *cobra.Command, args []string) {
		logging.Init(os.Getenv("APPXIMO_ENV"))

		email, _ := cmd.Flags().GetString("email")
		password, _ := cmd.Flags().GetString("password")
		role, _ := cmd.Flags().GetString("role")
		if email == "" || password == "" {
			fmt.Fprintln(os.Stderr, "both --email and --password are required")
			os.Exit(1)
		}

		connStr := os.Getenv("DATABASE_URL")
		if connStr == "" {
			fmt.Fprintln(os.Stderr, "DATABASE_URL environment variable is required")
			os.Exit(1)
		}
		jwtSecret := os.Getenv("JWT_SECRET")
		if jwtSecret == "" {
			fmt.Fprintln(os.Stderr, "JWT_SECRET environment variable is required")
			os.Exit(1)
		}

		ctx := context.Background()
		pool, err := db.NewPool(ctx, connStr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "connect db:", err)
			os.Exit(1)
		}
		defer pool.Close()

		// A bare controlplane service is enough here (the bootstrap touches only the
		// system schema; tenant ops are not used).
		svc := platformadmin.NewService(
			platformadmin.NewStore(pool), nil, controlplane.NewService(pool, nil), pool,
			platformadmin.Config{JWTSecret: jwtSecret, MFAKey: os.Getenv("APPXIMO_MFA_KEY")},
		)
		admin, err := svc.CreateAdmin(ctx, email, password, role)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create super-admin:", err)
			os.Exit(1)
		}
		fmt.Printf("Created platform super-admin %s (id=%s, role=%s)\n", admin.Email, admin.ID, admin.Role)
		fmt.Println("Log in at POST /admin/auth/login to obtain a platform token.")
	},
}

func init() {
	adminCreateCmd.Flags().String("email", "", "super-admin email")
	adminCreateCmd.Flags().String("password", "", "super-admin password (min 12 chars)")
	adminCreateCmd.Flags().String("role", "", "platform role (default platform_super_admin)")
	adminCmd.AddCommand(adminCreateCmd)
	rootCmd.AddCommand(adminCmd)
}
