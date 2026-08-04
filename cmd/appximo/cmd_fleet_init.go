package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

// fleetInitCmd (FLEET-CONSOLE-S2) scaffolds a WORKING fleet in one command —
// no hand-written JSON, no secrets pasted into a committable file:
//
//   - fleet.json           — the manifest (committable: it holds NO secrets;
//     the operator key/admin password and every app secret live in env files)
//   - fleet-secrets/       — generated secrets, one env file per app plus
//     fleet.env (operator key + operator admin), with a .gitignore ("*") so
//     the directory can never leak into git
//   - schemas/<app>.json   — a starter schema per app (edit in Studio later)
//
// It also best-effort CREATEs each app's database when a base DATABASE_URL is
// reachable (--database-url or $DATABASE_URL), so `make fleet` boots first try.
var fleetInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Genera un fleet listo para arrancar: fleet.json + secrets prolijos + schema inicial + DBs",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath, _ := cmd.Flags().GetString("config")
		appNames, _ := cmd.Flags().GetStringSlice("app")
		baseDSN, _ := cmd.Flags().GetString("database-url")
		adminEmail, _ := cmd.Flags().GetString("admin-email")
		if baseDSN == "" {
			baseDSN = os.Getenv("DATABASE_URL")
		}

		if _, err := os.Stat(cfgPath); err == nil {
			fmt.Fprintf(os.Stderr, "fleet init: %s already exists — refusing to overwrite (edit it, or point elsewhere with --config)\n", cfgPath)
			os.Exit(1)
		}
		dir := filepath.Dir(cfgPath)
		secretsDir := filepath.Join(dir, "fleet-secrets")
		schemasDir := filepath.Join(dir, "schemas")
		for _, d := range []string{secretsDir, schemasDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				fmt.Fprintln(os.Stderr, "fleet init:", err)
				os.Exit(1)
			}
		}
		// The secrets directory must never enter git, structurally.
		if err := os.WriteFile(filepath.Join(secretsDir, ".gitignore"), []byte("*\n"), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "fleet init:", err)
			os.Exit(1)
		}

		// Fleet-level secrets: the operator key (console auth) + the operator
		// admin (ONE login for every app's /admin). Env file, not manifest.
		operatorKey := randHex(24)
		adminPass := randHex(12)
		fleetEnv := fmt.Sprintf(
			"# Fleet-level secrets — NEVER commit this directory (see .gitignore).\n"+
				"APPXIMO_FLEET_OPERATOR_KEY=%s\n"+
				"APPXIMO_FLEET_ADMIN_EMAIL=%s\n"+
				"APPXIMO_FLEET_ADMIN_PASSWORD=%s\n",
			operatorKey, adminEmail, adminPass)
		// DB-assist (FLEET-DB-ASSIST): when a base DSN is known, declare a
		// "local" instance so the console can create databases out of the box.
		// The PRIVILEGED admin DSN (base DSN → the `postgres` maintenance
		// database) lives HERE in the gitignored env-file, referenced from the
		// committable manifest by env-var name only.
		dbInstancesJSON := ""
		if baseDSN != "" {
			// The privileged admin DSN points at the `postgres` maintenance
			// database (CREATE DATABASE cannot run from inside the target db).
			fleetEnv += "APPXIMO_FLEET_DB_LOCAL_ADMIN=" + swapDBName(baseDSN, "postgres") + "\n"
			dbInstancesJSON = `,
  "db_instances": [
    { "name": "local", "label": "Local Postgres (this box)", "admin_dsn_env": "APPXIMO_FLEET_DB_LOCAL_ADMIN" }
  ]`
		}
		if err := os.WriteFile(filepath.Join(secretsDir, "fleet.env"), []byte(fleetEnv), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "fleet init:", err)
			os.Exit(1)
		}

		var appsJSON []string
		for _, name := range appNames {
			dsn, dsnNote := deriveAppDSN(baseDSN, name)
			// Per-app secrets env file (JWT_SECRET must be unique per app — the
			// manifest rule; generated, so it always is).
			appEnv := fmt.Sprintf("DATABASE_URL=%s\nJWT_SECRET=%s\nADMIN_KEY=%s\n", dsn, randHex(24), randHex(12))
			if err := os.WriteFile(filepath.Join(secretsDir, name+".env"), []byte(appEnv), 0o600); err != nil {
				fmt.Fprintln(os.Stderr, "fleet init:", err)
				os.Exit(1)
			}
			// Starter schema (edit in Studio / paste your real one later).
			schemaPath := filepath.Join(schemasDir, name+".json")
			if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
				starter := fmt.Sprintf(`{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "%s",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"], "default": "open" },
        "created_at": { "type": "time", "auto": true }
      }
    }
  },
  "rbac": {
    "roles": { "admin": { "resources": "*", "actions": ["*"] } }
  }
}
`, name)
				if err := os.WriteFile(schemaPath, []byte(starter), 0o644); err != nil {
					fmt.Fprintln(os.Stderr, "fleet init:", err)
					os.Exit(1)
				}
			}
			// Best-effort database creation so the first boot succeeds.
			if baseDSN != "" {
				if err := ensureDatabase(baseDSN, dbNameOf(dsn)); err != nil {
					fmt.Fprintf(os.Stderr, "fleet init: note: could not create database %q (%v) — create it yourself before `make fleet`\n", dbNameOf(dsn), err)
				} else {
					fmt.Printf("✓ database %s ready\n", dbNameOf(dsn))
				}
			} else {
				fmt.Fprintf(os.Stderr, "fleet init: note: no DATABASE_URL available — edit %s and set the app's DATABASE_URL%s\n", filepath.Join(secretsDir, name+".env"), dsnNote)
			}

			appsJSON = append(appsJSON, fmt.Sprintf(`    {
      "name": %q,
      "schema": %q,
      "domains": [%q],
      "env_file": %q
    }`, name, "schemas/"+name+".json", name+".localhost", "fleet-secrets/"+name+".env"))
		}

		manifest := fmt.Sprintf(`{
  "listen": ":8080",
  "data_dir": "fleet-data",
  "operator_admin_email": %q%s,
  "apps": [
%s
  ]
}
`, adminEmail, dbInstancesJSON, strings.Join(appsJSON, ",\n"))
		if err := os.WriteFile(cfgPath, []byte(manifest), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "fleet init:", err)
			os.Exit(1)
		}

		fmt.Printf(`✓ fleet scaffolded:
  %s                 — the manifest (committable: no secrets in it)
  %s/     — operator key + admin + per-app secrets (gitignored)
  %s/           — starter schema per app (design in Studio, deploy hot)

Next:
  make fleet                 # build + load secrets + serve everything on :8080
  open http://localhost:8080/fleet?key=<APPXIMO_FLEET_OPERATOR_KEY from fleet-secrets/fleet.env>

Operator admin (ONE login for every app's /admin): %s / see fleet-secrets/fleet.env
Apps serve on <app>.localhost:8080 — Studio /editor, admin /admin, docs /docs per app.
`, cfgPath, secretsDir, schemasDir, adminEmail)
	},
}

// deriveAppDSN swaps the database name of the base DSN for app_<name>.
func deriveAppDSN(baseDSN, app string) (string, string) {
	if baseDSN == "" {
		return "postgres://user:pass@localhost:5432/app_" + app, " (a placeholder was written)"
	}
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "postgres://user:pass@localhost:5432/app_" + app, " (base DATABASE_URL unparseable)"
	}
	u.Path = "/app_" + app
	return u.String(), ""
}

func dbNameOf(dsn string) string {
	if u, err := url.Parse(dsn); err == nil {
		return strings.TrimPrefix(u.Path, "/")
	}
	return dsn
}

// swapDBName returns dsn with its database (URL path) replaced by db. Used to
// point the "local" instance's admin DSN at the `postgres` maintenance database.
func swapDBName(dsn, db string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + db
	return u.String()
}

// ensureDatabase connects to the BASE database and creates dbName if missing
// (CREATE DATABASE cannot run inside a transaction; pgx simple exec is fine).
func ensureDatabase(baseDSN, dbName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, baseDSN)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background()) //nolint:errcheck
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", dbName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize()))
	return err
}

// randHex returns n bytes of crypto-random material, hex-encoded (2n chars).
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

func init() {
	fleetInitCmd.Flags().String("config", "fleet.json", "path for the generated manifest")
	fleetInitCmd.Flags().StringSlice("app", []string{"demo"}, "app name(s) to scaffold (repeatable)")
	fleetInitCmd.Flags().String("database-url", "", "base Postgres DSN — each app gets its own app_<name> database derived from it (default: $DATABASE_URL)")
	fleetInitCmd.Flags().String("admin-email", "operator@fleet.local", "the unified operator admin email (one login for every app's /admin)")
	fleetCmd.AddCommand(fleetInitCmd)
}
