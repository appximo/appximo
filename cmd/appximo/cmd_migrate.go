package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/migration"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate one or ALL tenants' schema (safe diff + drop gate + resumable fan-out)",
	Long: `Converges the tenant(s)' tables to their schema with the real migration engine
(introspection → diff → safe apply): renames preserve data, NOT NULL is applied
faithfully, declared indexes/constraints are materialized.

The default policy is ADDITIVE: it creates/adds/alters/renames but NEVER drops —
a destructive operation (removing a resource/table or a field/column) stays
GATED as drift, with no data loss. Applying a drop requires ENUMERATING it
explicitly with --approve-drops (informed consent — never a "yes to everything").

ONE tenant:
    appximo migrate --tenant acme --schema schema.json [--dry-run]
    appximo migrate --tenant acme --schema schema.json --approve-drops "empleados.telefono"

ALL tenants (resumable fan-out — the differentiator vs Prisma):
    appximo migrate --all-tenants --schema base.json --dry-run   # plan + AGGREGATE impact
    appximo migrate --all-tenants --schema base.json             # apply to all N
    appximo migrate --tenants acme,globex --schema base.json     # a subset

The fan-out is RESILIENT (a failing tenant does not abort the healthy ones; it is
recorded and reported) and RESUMABLE (re-running skips the already-migrated —
empty diff = no-op — and retries the failed). It NEVER auto-approves destructives:
a mass drop requires --approve-drops (applied to EVERY tenant; the dry-run shows
the aggregate impact first). Sequential in v1.

Useful for on-premise installs or debugging without a Redis worker.`,
	Run: func(cmd *cobra.Command, args []string) {
		schemaFile, _ := cmd.Flags().GetString("schema")
		tenantID, _ := cmd.Flags().GetString("tenant")
		allTenants, _ := cmd.Flags().GetBool("all-tenants")
		tenantsRaw, _ := cmd.Flags().GetString("tenants")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		approved := parseCSV(mustString(cmd, "approve-drops"))
		tenantList := parseCSV(tenantsRaw)

		// Mode dispatch: exactly one target selector.
		fanout := allTenants || len(tenantList) > 0
		switch {
		case fanout && tenantID != "":
			fatal("use --tenant for a single tenant, OR --all-tenants/--tenants for a fan-out — not both")
		case allTenants && len(tenantList) > 0:
			fatal("use --all-tenants OR --tenants, not both")
		case !fanout && tenantID == "":
			fatal("one of --tenant, --all-tenants or --tenants is required")
		}

		s, err := schema.LoadFromFile(schemaFile)
		if err != nil {
			fatal("Error reading schema: " + err.Error())
		}
		if errs := schema.Validate(s); len(errs) > 0 {
			fmt.Fprintln(os.Stderr, "Invalid schema:")
			for _, e := range errs {
				fmt.Fprintln(os.Stderr, " ", e.Error())
			}
			os.Exit(1)
		}

		connStr := os.Getenv("DATABASE_URL")
		if connStr == "" {
			fatal("DATABASE_URL environment variable is required")
		}
		ctx := context.Background()
		pool, err := db.NewPool(ctx, connStr)
		if err != nil {
			fatal("Error connecting to the DB: " + err.Error())
		}
		defer pool.Close()

		if fanout {
			runFanout(ctx, pool, s, tenantList, approved, dryRun)
			return
		}
		runSingleTenant(ctx, pool, s, "tenant_"+tenantID, approved, dryRun)
	},
}

// ── single-tenant path (unchanged behavior) ────────────────────────────────────

func runSingleTenant(ctx context.Context, pool *pgxpool.Pool, s *schema.APISchema, pgSchema string, approved []string, dryRun bool) {
	if dryRun {
		pv, err := migration.PreviewTenantMigration(ctx, pool, pgSchema, s, approved)
		if err != nil {
			fatal("Dry-run failed: " + err.Error())
		}
		printPreview(pgSchema, pv)
		return
	}
	fmt.Printf("Applying migration to schema %q...\n", pgSchema)
	outcome, err := migration.ApplyTenantMigrationApproved(ctx, pool, pgSchema, s, approved)
	if err != nil {
		fatal("Migration failed: " + err.Error())
	}
	// ENG-13: a PARTIAL apply — the database does not have everything the schema
	// declares — is a FAILURE, not a ✓ with a footnote. Report it before anything
	// else, do NOT persist a schema the database cannot back, and exit non-zero.
	if outcome.Partial() {
		fmt.Println()
		fmt.Println("✗ PARTIAL MIGRATION — the database does NOT have everything this schema declares.")
		fmt.Println("  These changes were NOT applied (verified by reading the database, not the migration log):")
		for _, u := range outcome.Unapplied {
			fmt.Printf("    · %s\n", u)
		}
		fmt.Println()
		fmt.Println("  The schema was NOT saved to the tenant record, so the engine keeps serving the")
		fmt.Println("  previous one — declared and applied stay in agreement. Fix the cause above and")
		fmt.Println("  re-run the same command.")
		os.Exit(1)
	}
	// Persist the applied schema to the tenant record + history — the same
	// contract as the fan-out and the control-plane PUT (DOC-1): the UPDATE
	// fires pg_notify(schema_updated), so a running engine serves the new
	// FIELDS hot (new resources/routes still need a restart).
	tenantID := strings.TrimPrefix(pgSchema, "tenant_")
	if perr := migration.PersistTenantSchema(ctx, pool, tenantID, s); perr != nil {
		fmt.Printf("  ⚠ schema applied but NOT persisted to the tenant record (%v)\n"+
			"    the running engine keeps validating against the previous schema — re-run, or deploy via the control plane\n", perr)
	} else {
		fmt.Println("  ✓ schema persisted to the tenant record (running engine notified — new fields serve hot on REST: read, write, filter/sort/search and aggregates; a NEW resource, GraphQL input types, /docs and RBAC changes still need a restart)")
	}
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
}

// ── fan-out path (multi-tenant orchestrator) ────────────────────────────────────

func runFanout(ctx context.Context, pool *pgxpool.Pool, s *schema.APISchema, tenantIDs, approved []string, dryRun bool) {
	scope := "ALL tenants"
	if len(tenantIDs) > 0 {
		scope = fmt.Sprintf("%d tenant(s): %s", len(tenantIDs), strings.Join(tenantIDs, ", "))
	}
	mode := "APPLY"
	if dryRun {
		mode = "DRY-RUN (nothing is applied)"
	}
	fmt.Printf("Fan-out migration — %s — %s\n", mode, scope)
	if len(approved) > 0 && !dryRun {
		fmt.Printf("⚠ MASS DESTRUCTIVE approval active — these drops will be applied to EVERY targeted tenant: %s\n",
			strings.Join(approved, ", "))
	}
	fmt.Println()

	opts := migration.FanoutOptions{
		Schema:        s,
		TenantIDs:     tenantIDs,
		ApprovedDrops: approved,
		DryRun:        dryRun,
		OnTenant:      func(tr migration.TenantFanoutResult) { printTenantLine(tr, dryRun) },
	}
	res, err := migration.RunFanout(ctx, pool, opts)
	if err != nil {
		fatal("Fan-out failed: " + err.Error())
	}

	fmt.Println()
	if len(res.MissingTenants) > 0 {
		fmt.Printf("⚠ requested tenants NOT found (ignored): %s\n", strings.Join(res.MissingTenants, ", "))
	}
	fmt.Printf("── Summary [%s] ──  total=%d  applied=%d  noop=%d  failed=%d\n",
		res.RunID, res.Total, res.Applied, res.Noop, res.Failed)

	if dryRun {
		if imp := res.AggregateDestructive(); len(imp) > 0 {
			fmt.Println("\nAGGREGATE destructive impact (data lost if approved across the fan-out):")
			for _, d := range imp {
				fmt.Printf("  ! %s (%s) — %d row(s) across %d tenant(s) [approve with --approve-drops %s]\n",
					d.Key, d.Kind, d.RowsLost, d.Tenants, d.Key)
			}
		}
	}
	if res.Failed > 0 {
		fmt.Printf("\n%d tenant(s) failed — fix the cause and RE-RUN the same command to resume "+
			"(already-migrated tenants are no-ops, only the failed ones retry).\n", res.Failed)
		os.Exit(1)
	}
}

// printTenantLine streams one tenant's outcome as it completes.
func printTenantLine(tr migration.TenantFanoutResult, dryRun bool) {
	switch tr.Status {
	case migration.FanoutApplied:
		verb := "applied"
		if dryRun {
			verb = "would apply"
		}
		extra := ""
		if len(tr.AppliedDrops) > 0 {
			extra = " (dropped: " + strings.Join(tr.AppliedDrops, ", ") + ")"
		} else if len(tr.GatedDrops) > 0 {
			extra = " (gated drops: " + strings.Join(tr.GatedDrops, ", ") + ")"
		}
		fmt.Printf("  ✓ %-20s %s%s  (%dms)\n", tr.TenantID, verb, extra, tr.DurationMS)
	case migration.FanoutNoop:
		fmt.Printf("  · %-20s no-op (already converged)  (%dms)\n", tr.TenantID, tr.DurationMS)
	case migration.FanoutFailed:
		fmt.Printf("  ✗ %-20s FAILED: %s  (%dms)\n", tr.TenantID, tr.Error, tr.DurationMS)
	}
}

// ── shared helpers ──────────────────────────────────────────────────────────────

// mustString reads a string flag (ignoring the always-nil "not found" error).
func mustString(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}

// parseCSV splits a comma-separated value into trimmed, non-empty items.
func parseCSV(raw string) []string {
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

// printPreview renders a single-tenant dry-run preview: the safe ops that would
// apply, the data-losing drops with their impact + approval status, the additive
// drift, concerns, and any approval token that matched nothing.
func printPreview(pgSchema string, pv *migration.Preview) {
	fmt.Printf("Dry-run for schema %q — NOTHING was applied.\n\n", pgSchema)
	if pv.Empty {
		fmt.Println("  ✓ already converged — no changes.")
		for _, e := range pv.External {
			fmt.Printf("  · external (consumer-owned, untouched): %s\n", e)
		}
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

	if len(pv.External) > 0 {
		fmt.Printf("EXTERNAL — consumer-owned objects (never declared by any schema version; out of migration scope, left untouched):\n")
		for _, e := range pv.External {
			fmt.Printf("  · %s\n", e)
		}
		fmt.Println()
	}

	if len(pv.UnmatchedApprovals) > 0 {
		fmt.Printf("Approvals that matched NOTHING (typo, or already applied): %s\n\n",
			strings.Join(pv.UnmatchedApprovals, ", "))
	}

	if pv.PendingDestructive() {
		fmt.Println("To apply a destructive drop, re-run without --dry-run and enumerate it:")
		fmt.Printf("  appximo migrate --tenant <id> --schema <file> --approve-drops \"%s\"\n",
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

// printOutcome reports what a single-tenant apply did with the data-losing drops.
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
	for _, fk := range o.UnvalidatedFKs {
		fmt.Printf("  ⚠ %s — added, but rows that already existed break it. New writes ARE checked;\n"+
			"      the old rows are not. Fix them, then re-run to finish checking.\n", fk)
	}
}

func init() {
	migrateCmd.Flags().String("schema", "schema.json", "path to schema.json")
	migrateCmd.Flags().String("tenant", "", "single tenant ID to migrate")
	migrateCmd.Flags().Bool("all-tenants", false, "fan-out: migrate EVERY tenant in public.tenants (resumable)")
	migrateCmd.Flags().String("tenants", "", "fan-out: migrate this comma-separated subset of tenant IDs")
	migrateCmd.Flags().Bool("dry-run", false, "show the migration plan + destructive impact WITHOUT applying anything")
	migrateCmd.Flags().String("approve-drops", "", "comma-separated destructive drop keys to apply (e.g. \"empleados.telefono,proyectos\"); in a fan-out they apply to EVERY tenant")
	rootCmd.AddCommand(migrateCmd)
}
