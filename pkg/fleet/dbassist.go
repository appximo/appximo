package fleet

// FLEET-DB-ASSIST — database assistance for the console's Add-app form, three
// levels (test a connection, suggest a DSN, create the database) built on ONE
// non-negotiable security principle: the engine NEVER scans the system, Docker,
// or the network to discover Postgres. Every capability comes from what the
// OPERATOR explicitly DECLARES in the fleet config — a set of `db_instances`,
// each naming an env var that holds a privileged DSN. Declaring an instance is
// the explicit, auditable grant of "create databases here"; not declaring one
// leaves only the manual-DSN path (+ test), which needs no stored credentials.
//
// Credentials never reach the browser: the console references an instance by
// NAME, and the server (which alone holds the DSN) derives, tests and creates
// server-side. The power is bounded to CREATE DATABASE (never DROP arbitrary
// data — a fresh-create rollback is the only drop, and only of a database this
// same operation just made) and every creation is logged.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// dbNameRe bounds a database name to a safe Postgres identifier: a lowercase
// letter first, then lowercase letters, digits or '_'. Postgres caps identifiers
// at 63 bytes. Values are ALSO pgx.Identifier.Sanitize()'d at exec — this is
// defence in depth, and it keeps suggested/created names coherent with the
// tenant/app naming rules used everywhere else.
var dbNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidDBName reports whether name is a safe database name.
func ValidDBName(name string) bool { return dbNameRe.MatchString(name) }

// SuggestDBName derives the conventional database name for an app: app_<name>,
// lowercasing and mapping '-' to '_' so a valid app name always yields a valid
// database name (mirrors deriveAppDSN / fleet-init).
func SuggestDBName(app string) string {
	s := "app_" + strings.ReplaceAll(strings.ToLower(strings.TrimSpace(app)), "-", "_")
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// DeriveDSN returns baseDSN with its database name (the URL path) replaced by
// dbName — the app's runtime DSN, reusing the instance's host/credentials. The
// operator can still edit the result before submitting (e.g. a limited user).
func DeriveDSN(baseDSN, dbName string) (string, error) {
	u, err := url.Parse(baseDSN)
	if err != nil {
		return "", fmt.Errorf("base DSN is not a valid URL: %w", err)
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

// DBNameOf extracts the database name (URL path) from a DSN, or "" if unparseable.
func DBNameOf(dsn string) string {
	if u, err := url.Parse(dsn); err == nil {
		return strings.TrimPrefix(u.Path, "/")
	}
	return ""
}

// DBTestResult is the structured verdict of a connection test — enough for the
// console to be actionable without leaking server internals.
type DBTestResult struct {
	OK            bool   `json:"ok"`            // connected AND the target database exists
	DBExists      bool   `json:"db_exists"`     // the DSN's database exists (false ⇒ needs creating)
	CanCreateDB   bool   `json:"can_create_db"` // the connected role may CREATE DATABASE
	ServerVersion string `json:"server_version,omitempty"`
	Code          string `json:"code,omitempty"`  // pg SQLSTATE (or "" for a network error)
	Error         string `json:"error,omitempty"` // actionable, client-safe message
}

// TestDSN connects with dsn, reports a structured verdict, and CLOSES — it is a
// pure probe with zero administrative effect. It classifies the common failures
// into actionable messages (database missing vs auth vs unreachable) rather than
// surfacing a raw driver error.
func TestDSN(ctx context.Context, dsn string) DBTestResult {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "3D000": // invalid_catalog_name — the database does not exist
				return DBTestResult{Code: pgErr.Code, DBExists: false,
					Error: "the database does not exist yet — turn on “Create the database”, or create it first"}
			case "28P01", "28000": // invalid_password / invalid_authorization
				return DBTestResult{Code: pgErr.Code,
					Error: "authentication failed — check the user and password"}
			}
			return DBTestResult{Code: pgErr.Code, Error: pgErr.Message}
		}
		// Not a PgError ⇒ the server was unreachable (DNS, refused, timeout).
		return DBTestResult{Error: "cannot reach the server: " + err.Error()}
	}
	defer conn.Close(context.Background()) //nolint:errcheck

	res := DBTestResult{OK: true, DBExists: true}
	// Best-effort enrichment; a failure here does not change the OK verdict.
	_ = conn.QueryRow(ctx, "SHOW server_version").Scan(&res.ServerVersion)
	_ = conn.QueryRow(ctx,
		"SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname = current_user").
		Scan(&res.CanCreateDB)
	return res
}

// CreateDatabase creates dbName on the server the ADMIN DSN points at, IF it
// does not already exist. Returns created=true only when it actually ran the
// CREATE (created=false + nil ⇒ the database already existed, reused, never
// overwritten). The name is validated AND Sanitize()'d — CREATE DATABASE cannot
// run inside a transaction, and this issues that one statement only. The caller
// audits the outcome.
func CreateDatabase(ctx context.Context, adminDSN, dbName string) (created bool, err error) {
	if !ValidDBName(dbName) {
		return false, fmt.Errorf("invalid database name %q: must match %s", dbName, dbNameRe)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return false, fmt.Errorf("connect to the instance: %w", err)
	}
	defer conn.Close(context.Background()) //nolint:errcheck

	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", dbName).Scan(&exists); err != nil {
		return false, fmt.Errorf("check database existence: %w", err)
	}
	if exists {
		return false, nil
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{dbName}.Sanitize())); err != nil {
		return false, fmt.Errorf("create database: %w", err)
	}
	log.Printf("fleet db-assist: CREATE DATABASE %q on instance (admin DSN %s) — authorized by the operator", dbName, redactDSN(adminDSN))
	return true, nil
}

// DropDatabase drops dbName — used ONLY to roll back a database this operation
// just created when the subsequent app-add fails (all-or-nothing). It is never
// exposed as an operator action: the fleet's vocabulary never destroys a
// pre-existing database (that is a deliberate, out-of-band act).
func DropDatabase(ctx context.Context, adminDSN, dbName string) error {
	if !ValidDBName(dbName) {
		return fmt.Errorf("invalid database name %q", dbName)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background()) //nolint:errcheck
	// WITH (FORCE) terminates the connection we may have just opened via bootstrap.
	_, err = conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", pgx.Identifier{dbName}.Sanitize()))
	if err == nil {
		log.Printf("fleet db-assist: rolled back — dropped freshly-created database %q (the app-add failed)", dbName)
	}
	return err
}
