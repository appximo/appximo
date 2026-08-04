package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runBackup executes the exact remote shell that backupRemoteCmd produces,
// locally via sh, against a fake binary at binPath. Returns stdout + exit code.
func runBackup(t *testing.T, binPath string) (string, int) {
	t.Helper()
	cmd := exec.Command("sh", "-c", backupRemoteCmd(binPath))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("sh run error: %v", err)
		}
	}
	return string(out), code
}

func writeFakeBinary(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBackup_CopiesAndVerifies(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "appximo", "appximo")
	writeFakeBinary(t, bin, 4096)

	out, code := runBackup(t, bin)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d; out:\n%s", code, out)
	}
	if !strings.Contains(out, "BACKED_UP ") {
		t.Fatalf("expected BACKED_UP, got:\n%s", out)
	}
	// the rollback dir is the binary dir's sibling, base.<timestamp>
	rb := filepath.Join(root, "appximo-rollback")
	entries, _ := os.ReadDir(rb)
	var found string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "appximo.") {
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatalf("no backup file created in %s", rb)
	}
	// same byte size as source (cp -p fidelity)
	fi, err := os.Stat(filepath.Join(rb, found))
	if err != nil || fi.Size() != 4096 {
		t.Fatalf("backup size mismatch: %v size=%d", err, fi.Size())
	}
}

func TestBackup_NoPreviousSkips(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "appximo", "appximo") // never created
	out, code := runBackup(t, bin)
	if code != 0 {
		t.Fatalf("first deploy must NOT fail, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "NO_PREVIOUS") {
		t.Fatalf("expected NO_PREVIOUS, got:\n%s", out)
	}
}

func TestBackup_RetentionKeeps10AndSparesManual(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "appximo", "appximo")
	writeFakeBinary(t, bin, 1024)
	rb := filepath.Join(root, "appximo-rollback")
	if err := os.MkdirAll(rb, 0o755); err != nil {
		t.Fatal(err)
	}
	// 11 pre-existing AUTOMATIC timestamped backups (older than the new one).
	for _, ts := range []string{
		"20260101-000000", "20260102-000000", "20260103-000000", "20260104-000000",
		"20260105-000000", "20260106-000000", "20260107-000000", "20260108-000000",
		"20260109-000000", "20260110-000000", "20260111-000000",
	} {
		if err := os.WriteFile(filepath.Join(rb, "appximo."+ts), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Two MANUAL labels that must NEVER be pruned.
	for _, m := range []string{"appximo.pre_realip", "appximo.pre_s33"} {
		if err := os.WriteFile(filepath.Join(rb, m), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	out, code := runBackup(t, bin)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d:\n%s", code, out)
	}

	entries, _ := os.ReadDir(rb)
	var autos, manuals int
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "appximo.pre_"):
			manuals++
		case strings.HasPrefix(e.Name(), "appximo."):
			autos++
		}
	}
	// 11 old + 1 new = 12 auto; retention keeps the newest 10.
	if autos != 10 {
		t.Fatalf("retention should keep 10 automatic backups, got %d:\n%s", autos, out)
	}
	if manuals != 2 {
		t.Fatalf("manual pre_* backups must be untouched, got %d", manuals)
	}
	if !strings.Contains(out, "PRUNED ") {
		t.Fatalf("expected PRUNED lines, got:\n%s", out)
	}
	// The oldest auto (20260101) must be gone; the newest old (20260111) kept.
	if _, err := os.Stat(filepath.Join(rb, "appximo.20260101-000000")); !os.IsNotExist(err) {
		t.Fatalf("oldest backup should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(rb, "appximo.20260111-000000")); err != nil {
		t.Fatalf("recent backup should have been kept: %v", err)
	}
}

func TestBackup_AbortsWhenCopyImpossible(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "appximo", "appximo")
	writeFakeBinary(t, bin, 2048)
	// Make the rollback dir path a regular FILE so mkdir -p can't create it →
	// the backup must FAIL (non-zero), which makes the caller abort the swap.
	rbPath := filepath.Join(root, "appximo-rollback")
	if err := os.WriteFile(rbPath, []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runBackup(t, bin)
	if code == 0 {
		t.Fatalf("backup must exit non-zero when it cannot write the rollback copy; out:\n%s", out)
	}
	if !strings.Contains(out, "MKDIR_FAILED") && !strings.Contains(out, "CP_FAILED") && !strings.Contains(out, "VERIFY_FAILED") {
		t.Fatalf("expected a failure token, got:\n%s", out)
	}
}
