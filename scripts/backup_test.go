package scripts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPgDumpArgs verifies the pg_dump command is built with the expected flags.
func TestPgDumpArgs(t *testing.T) {
	args := pgDumpArgs("tenant_10", "postgres://u:p@h/db", "/out/backup_10.dump")
	want := []string{
		"postgres://u:p@h/db",
		"--schema=tenant_10",
		"--format=custom",
		"--compress=9",
		"--file=/out/backup_10.dump",
	}
	if len(args) != len(want) {
		t.Fatalf("arg count: want %d, got %d (%v)", len(want), len(args), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("arg[%d]: want %q, got %q", i, want[i], args[i])
		}
	}
}

// TestDump_PgDumpNotInPath: a missing binary returns a descriptive ErrPgDumpNotFound,
// never a panic.
func TestDump_PgDumpNotInPath(t *testing.T) {
	r := pgDumpRunner{binary: "definitely-not-a-real-binary-xyz123"}
	err := r.Dump(context.Background(), "tenant_10", "conn", filepath.Join(t.TempDir(), "x.dump"))
	if !errors.Is(err, ErrPgDumpNotFound) {
		t.Fatalf("want ErrPgDumpNotFound, got %v", err)
	}
}

// blockingRunner blocks until its context is cancelled, simulating a long pg_dump.
type blockingRunner struct{ started chan struct{} }

func (b blockingRunner) Dump(ctx context.Context, _, _, _ string) error {
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

// TestRunBackups_ContextCancelTerminates: cancelling the parent context unblocks the
// dump and surfaces the cancellation error (the process would be killed).
func TestRunBackups_ContextCancelTerminates(t *testing.T) {
	runner := blockingRunner{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := runBackups(ctx, runner, "conn", []target{{id: "10", schema: "tenant_10"}}, t.TempDir())
		done <- err
	}()

	<-runner.started // ensure the dump is in-flight
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runBackups did not return after context cancel")
	}
}

// recordingRunner captures the schema/outPath it was asked to dump and writes a stub
// file so size_bytes is populated.
type recordingRunner struct {
	schema  string
	outPath string
}

func (r *recordingRunner) Dump(_ context.Context, schemaName, _, outPath string) error {
	r.schema = schemaName
	r.outPath = outPath
	return os.WriteFile(outPath, []byte("STUBDUMP"), 0o600)
}

// TestRunBackups_ProducesResult: a successful dump yields a BackupResult with the
// correct tenant, path, and size.
func TestRunBackups_ProducesResult(t *testing.T) {
	dir := t.TempDir()
	runner := &recordingRunner{}
	results, err := runBackups(context.Background(), runner, "conn",
		[]target{{id: "10", schema: "tenant_10"}}, dir)
	if err != nil {
		t.Fatalf("runBackups: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Tenant != "10" {
		t.Errorf("tenant: want 10, got %q", res.Tenant)
	}
	if res.SizeBytes != int64(len("STUBDUMP")) {
		t.Errorf("size: want %d, got %d", len("STUBDUMP"), res.SizeBytes)
	}
	if runner.schema != "tenant_10" {
		t.Errorf("dumped schema: want tenant_10, got %q", runner.schema)
	}
	if !strings.HasPrefix(filepath.Base(res.Path), "backup_10_") || !strings.HasSuffix(res.Path, ".dump") {
		t.Errorf("path format unexpected: %q", res.Path)
	}
}
