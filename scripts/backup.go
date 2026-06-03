// Package scripts holds operational commands that run pg_dump and other external
// tooling on behalf of the appitools engine (CLI `appitools backup` and the admin
// /admin/backup endpoint).
package scripts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/miguelangel/appitools/pkg/logging"
)

// ErrPgDumpNotFound is returned (wrapped) when the pg_dump binary is not on PATH,
// so callers (the HTTP endpoint) can map it to 503 instead of a generic 500.
var ErrPgDumpNotFound = errors.New("pg_dump not found in PATH")

// perTenantTimeout bounds a single schema dump.
const perTenantTimeout = 5 * time.Minute

// BackupResult describes one completed schema dump.
type BackupResult struct {
	Tenant    string `json:"tenant"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// DumpRunner abstracts the pg_dump invocation so tests can substitute a fake and
// avoid shelling out to a real binary.
type DumpRunner interface {
	Dump(ctx context.Context, schemaName, connStr, outPath string) error
}

// defaultRunner is the production pg_dump executor.
var defaultRunner DumpRunner = pgDumpRunner{}

// pgDumpRunner runs the real pg_dump binary. binary is overridable in tests.
type pgDumpRunner struct{ binary string }

// Dump shells out to pg_dump. A missing binary yields ErrPgDumpNotFound; a context
// cancellation (timeout) returns the context error so the process is killed cleanly.
func (r pgDumpRunner) Dump(ctx context.Context, schemaName, connStr, outPath string) error {
	bin := r.binary
	if bin == "" {
		bin = "pg_dump"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return fmt.Errorf("%w: %v", ErrPgDumpNotFound, err)
	}
	cmd := exec.CommandContext(ctx, bin, pgDumpArgs(schemaName, connStr, outPath)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err() // timeout/cancel — CommandContext already killed the process
		}
		return fmt.Errorf("pg_dump failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// pgDumpArgs builds the pg_dump argument vector: custom format, max compression,
// single schema, output file. connStr is passed as the dbname positional argument.
func pgDumpArgs(schemaName, connStr, outPath string) []string {
	return []string{
		connStr,
		"--schema=" + schemaName,
		"--format=custom",
		"--compress=9",
		"--file=" + outPath,
	}
}

// target is one schema to dump, with the tenant label used in the filename.
type target struct {
	id     string
	schema string
}

// RunBackup is the CLI entrypoint: dumps one tenant (or all tenants when tenantID is
// empty) into outputDir, printing one line per completed dump.
func RunBackup(ctx context.Context, pool *pgxpool.Pool, tenantID, outputDir string) error {
	results, err := Backup(ctx, pool, tenantID, outputDir)
	if err != nil {
		return err
	}
	for _, res := range results {
		fmt.Printf("  ✓ tenant %s → %s (%d bytes)\n", res.Tenant, res.Path, res.SizeBytes)
	}
	return nil
}

// Backup dumps one tenant (tenantID set) or every tenant (tenantID empty) and returns
// a result per schema. Uses the production pg_dump runner.
func Backup(ctx context.Context, pool *pgxpool.Pool, tenantID, outputDir string) ([]BackupResult, error) {
	return backupWith(ctx, pool, defaultRunner, tenantID, outputDir)
}

// backupWith is Backup with an injectable runner (used by tests).
func backupWith(ctx context.Context, pool *pgxpool.Pool, runner DumpRunner, tenantID, outputDir string) ([]BackupResult, error) {
	connStr := pool.Config().ConnString()
	targets, err := resolveTargets(ctx, pool, tenantID)
	if err != nil {
		return nil, err
	}
	return runBackups(ctx, runner, connStr, targets, outputDir)
}

// runBackups dumps each target with a per-tenant timeout. No DB access here, so it is
// unit-testable with a mock runner.
func runBackups(ctx context.Context, runner DumpRunner, connStr string, targets []target, outputDir string) ([]BackupResult, error) {
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}
	ts := time.Now().Format("20060102_150405")
	results := make([]BackupResult, 0, len(targets))
	for _, t := range targets {
		outPath := filepath.Join(outputDir, fmt.Sprintf("backup_%s_%s.dump", t.id, ts))
		start := time.Now()

		tctx, cancel := context.WithTimeout(ctx, perTenantTimeout)
		err := runner.Dump(tctx, t.schema, connStr, outPath)
		cancel()
		if err != nil {
			return results, fmt.Errorf("backup tenant %s (schema %s): %w", t.id, t.schema, err)
		}

		var size int64
		if fi, statErr := os.Stat(outPath); statErr == nil {
			size = fi.Size()
		}
		logging.Log.Info().
			Str("tenant_id", t.id).
			Str("path", outPath).
			Int64("size_bytes", size).
			Int64("dur_ms", time.Since(start).Milliseconds()).
			Msg("backup completed")

		results = append(results, BackupResult{Tenant: t.id, Path: outPath, SizeBytes: size})
	}
	return results, nil
}

// resolveTargets determines which schemas to dump. A specific tenant maps directly to
// tenant_<id>. With no tenant, it lists public.tenants; if that table is absent it
// falls back to dumping the public (control-plane) schema.
func resolveTargets(ctx context.Context, pool *pgxpool.Pool, tenantID string) ([]target, error) {
	if tenantID != "" {
		return []target{{id: tenantID, schema: "tenant_" + tenantID}}, nil
	}

	rows, err := pool.Query(ctx, "SELECT pg_schema FROM public.tenants")
	if err != nil {
		logging.Log.Warn().Err(err).Msg("public.tenants not available — backing up public schema only")
		return []target{{id: "public", schema: "public"}}, nil
	}
	defer rows.Close()

	var targets []target
	for rows.Next() {
		var pgSchema string
		if scanErr := rows.Scan(&pgSchema); scanErr != nil {
			return nil, fmt.Errorf("scan tenant schema: %w", scanErr)
		}
		id := strings.TrimPrefix(pgSchema, "tenant_")
		targets = append(targets, target{id: id, schema: pgSchema})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tenants: %w", err)
	}
	if len(targets) == 0 {
		return []target{{id: "public", schema: "public"}}, nil
	}
	return targets, nil
}
