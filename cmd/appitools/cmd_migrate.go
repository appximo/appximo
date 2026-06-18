package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migra el schema de un tenant (diff seguro + gate de aprobación de drops)",
	Long: `Converge las tablas del tenant a su schema con el motor de migraciones real
(introspección → diff → apply seguro): renames preservan datos, NOT NULL se
aplica fiel, los índices/constraints declarados se materializan.

Por defecto la política es ADITIVA: crea/agrega/altera/renombra pero NUNCA
dropea — una operación destructiva (eliminar un recurso/tabla o un campo/columna)
queda GATEADA como drift, sin pérdida de datos.

Para ver qué haría sin aplicar nada (incluido el impacto de cada drop):
    appitools migrate --tenant acme --schema schema.json --dry-run

Para APLICAR un drop destructivo hay que ENUMERARLO explícitamente (consentimiento
informado — nunca un "sí a todo"):
    appitools migrate --tenant acme --schema schema.json \
        --approve-drops "empleados.telefono,proyectos"

Útil para instalaciones on-premise o para depurar sin un worker de Redis.`,
	Run: func(cmd *cobra.Command, args []string) {
		schemaFile, _ := cmd.Flags().GetString("schema")
		tenantID, _ := cmd.Flags().GetString("tenant")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		approveRaw, _ := cmd.Flags().GetString("approve-drops")
		approved := parseApprovedDrops(approveRaw)

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

		// ── dry-run: classify and report, apply nothing ──
		if dryRun {
			pv, err := migration.PreviewTenantMigration(ctx, pool, pgSchema, s, approved)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Dry-run failed:", err)
				os.Exit(1)
			}
			printPreview(pgSchema, pv)
			return
		}

		// ── apply (only enumerated destructives are dropped; the rest gated) ──
		fmt.Printf("Applying migration to schema %q...\n", pgSchema)
		outcome, err := migration.ApplyTenantMigrationApproved(ctx, pool, pgSchema, s, approved)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Migration failed:", err)
			os.Exit(1)
		}

		// Print each resource table (sorted for deterministic output).
		tables := make([]string, 0, len(s.Resources))
		for name := range s.Resources {
			tables = append(tables, name)
		}
		sort.Strings(tables)
		for _, t := range tables {
			fmt.Printf("  ✓ %s.%s\n", pgSchema, t)
		}
		printOutcome(outcome)
		fmt.Println("Done.")
	},
}

// parseApprovedDrops splits a comma-separated --approve-drops value into trimmed,
// non-empty keys.
func parseApprovedDrops(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// printPreview renders a dry-run preview: the safe ops that would apply, the data-
// losing drops with their impact + approval status, the additive drift, concerns, and
// any approval token that matched nothing.
func printPreview(pgSchema string, pv *migration.Preview) {
	fmt.Printf("Dry-run for schema %q — NOTHING was applied.\n\n", pgSchema)
	if pv.Empty {
		fmt.Println("  ✓ already converged — no changes.")
		return
	}

	if len(pv.Apply) > 0 {
		fmt.Printf("Will APPLY (%d safe operation(s)):\n", len(pv.Apply))
		for _, op := range pv.Apply {
			fmt.Printf("  + %s\n", op)
		}
		fmt.Println()
	}

	if len(pv.Destructive) > 0 {
		fmt.Printf("DESTRUCTIVE — data-losing drops (require explicit approval):\n")
		for _, d := range pv.Destructive {
			state := "GATED   (approve with --approve-drops " + d.Key + ")"
			if d.Approved {
				state = "APPROVED (will be applied)"
			}
			fmt.Printf("  ! %-10s %s\n      %s\n", state, d.Key, d.Summary)
		}
		fmt.Println()
	}

	if len(pv.Drift) > 0 {
		fmt.Printf("Left as additive DRIFT (safe drops, not applied in v1):\n")
		for _, op := range pv.Drift {
			fmt.Printf("  ~ %s\n", op)
		}
		fmt.Println()
	}

	if len(pv.Concerns) > 0 {
		fmt.Printf("CONCERNS on existing data:\n")
		for _, c := range pv.Concerns {
			fmt.Printf("  ⚠ %s\n", c)
		}
		fmt.Println()
	}

	if len(pv.UnmatchedApprovals) > 0 {
		fmt.Printf("Approvals that matched NOTHING (typo, or already applied): %s\n\n",
			strings.Join(pv.UnmatchedApprovals, ", "))
	}

	if pv.PendingDestructive() {
		fmt.Println("To apply a destructive drop, re-run without --dry-run and enumerate it:")
		fmt.Printf("  appitools migrate --tenant <id> --schema <file> --approve-drops \"%s\"\n",
			strings.Join(pendingKeys(pv), ","))
	}
}

// pendingKeys returns the keys of the destructive ops still awaiting approval.
func pendingKeys(pv *migration.Preview) []string {
	var out []string
	for _, d := range pv.Destructive {
		if !d.Approved {
			out = append(out, d.Key)
		}
	}
	return out
}

// printOutcome reports what an apply did with the data-losing drops.
func printOutcome(o *migration.ApplyOutcome) {
	if o == nil {
		return
	}
	for _, k := range o.AppliedDrops {
		fmt.Printf("  ✗ DROPPED (approved): %s\n", k)
	}
	for _, k := range o.GatedDrops {
		fmt.Printf("  ~ gated drop (not approved — drift): %s (approve with --approve-drops %s)\n", k, k)
	}
	for _, k := range o.UnmatchedApprovals {
		fmt.Printf("  · approval %q matched nothing (typo, or already applied)\n", k)
	}
}

func init() {
	migrateCmd.Flags().String("schema", "schema.json", "path to schema.json")
	migrateCmd.Flags().String("tenant", "", "tenant ID (required)")
	migrateCmd.Flags().Bool("dry-run", false, "show the migration plan + destructive impact WITHOUT applying anything")
	migrateCmd.Flags().String("approve-drops", "", "comma-separated destructive drop keys to apply (e.g. \"empleados.telefono,proyectos\"); unlisted drops stay gated")
	migrateCmd.MarkFlagRequired("tenant") //nolint:errcheck
	rootCmd.AddCommand(migrateCmd)
}
