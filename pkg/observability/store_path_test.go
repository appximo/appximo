package observability

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// cleanupSQLite removes a SQLite db file and its WAL siblings (best effort).
func cleanupSQLite(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		_ = os.Remove(p)
	}
}

// TestDefaultObsDBPathIsPersistent is the R1 regression guard: the out-of-the-box
// default must be a persistent path, never /tmp (which systemd-tmpfiles or a
// container tmpfs can wipe on restart).
func TestDefaultObsDBPathIsPersistent(t *testing.T) {
	if defaultObsDBPath == "/tmp/obs.db" || strings.HasPrefix(defaultObsDBPath, "/tmp/") {
		t.Fatalf("default obs path %q is ephemeral (under /tmp) — R1 regression", defaultObsDBPath)
	}
	if !filepath.IsAbs(defaultObsDBPath) {
		t.Errorf("default obs path %q must be absolute", defaultObsDBPath)
	}
}

func TestIsEphemeralPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/tmp/obs.db", true},
		{"/tmp", true},
		{"/tmp/nested/dir/obs.db", true},
		{"/var/lib/appximo/obs.db", false},
		{"/data/app/obs.db", false},
		{"/opt/appximo/obs.db", false},
	}
	for _, c := range cases {
		if got := isEphemeralPath(c.path); got != c.want {
			t.Errorf("isEphemeralPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// TestOpenStore_CreatesMissingDirectory: a default/configured path whose parent
// directory does not exist yet is created on open (no manual mkdir needed).
func TestOpenStore_CreatesMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deeper")
	p := filepath.Join(dir, "obs.db")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("precondition: %s should not exist yet (err=%v)", dir, err)
	}

	st, err := OpenStore(p)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	if st.Path() != p {
		t.Errorf("Path() = %q, want %q", st.Path(), p)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("parent dir not created: stat err=%v", err)
	}
	// Still usable through the freshly created directory.
	if err := st.Flush("t", Snapshot{TenantID: "t", TS: time.Now().Unix(), SLOStatus: "ok"}); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if h, err := st.History("t", 24); err != nil || len(h) != 1 {
		t.Fatalf("History: err=%v len=%d, want 1", err, len(h))
	}
}

// TestOpenStore_UnwritableDirFallsBackNoCrash: when the configured directory
// cannot be created (here a path component is a regular file → ENOTDIR for ANY
// uid, root included), OpenStore must NOT fail — it falls back to an ephemeral
// temp file so the engine keeps booting and observability keeps working.
func TestOpenStore_UnwritableDirFallsBackNoCrash(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "iam-a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(blocker, "sub", "obs.db") // MkdirAll(blocker/sub) → ENOTDIR

	st, err := OpenStore(p)
	if err != nil {
		t.Fatalf("OpenStore must not fail on an unwritable dir (best-effort fallback), got %v", err)
	}
	defer st.Close()

	fallback := filepath.Join(os.TempDir(), "appximo-obs.db")
	defer cleanupSQLite(fallback)
	if st.Path() == p {
		t.Errorf("store used the unwritable path %q; expected the fallback", p)
	}
	if st.Path() != fallback {
		t.Errorf("Path() = %q, want fallback %q", st.Path(), fallback)
	}
	// The fallback store is fully functional.
	if err := st.Flush("t", Snapshot{TenantID: "t", TS: time.Now().Unix(), SLOStatus: "ok"}); err != nil {
		t.Fatalf("Flush after fallback: %v", err)
	}
}

func TestPlanObsDBPath_EphemeralWarning(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "appximo-obs-eph-")
	if err != nil {
		t.Skipf("/tmp unavailable for the ephemeral case: %v", err)
	}
	defer os.RemoveAll(dir)

	p := filepath.Join(dir, "obs.db")
	got, warning := planObsDBPath(p)
	if got != p {
		t.Errorf("path = %q, want %q (an ephemeral but writable path is honored)", got, p)
	}
	if !strings.Contains(warning, "ephemeral") {
		t.Errorf("want an ephemeral warning for a /tmp path, got %q", warning)
	}
}

func TestPlanObsDBPath_FallbackWarning(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(blocker, "obs.db")

	got, warning := planObsDBPath(p)
	fallback := filepath.Join(os.TempDir(), "appximo-obs.db")
	if got != fallback {
		t.Errorf("fallback path = %q, want %q", got, fallback)
	}
	if !strings.Contains(warning, "falling back") || !strings.Contains(warning, "ephemeral") {
		t.Errorf("want a fallback+ephemeral warning, got %q", warning)
	}
}

// TestOpenStore_LogsEphemeralWarning verifies the WARNING is actually emitted to
// the log when the resolved path is ephemeral (PASO 3 — risk visibility for R1).
func TestOpenStore_LogsEphemeralWarning(t *testing.T) {
	var buf bytes.Buffer
	oldOut, oldFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() { log.SetOutput(oldOut); log.SetFlags(oldFlags) }()

	dir, err := os.MkdirTemp("/tmp", "appximo-obs-log-")
	if err != nil {
		t.Skipf("/tmp unavailable: %v", err)
	}
	defer os.RemoveAll(dir)

	st, err := OpenStore(filepath.Join(dir, "obs.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer st.Close()

	if !strings.Contains(buf.String(), "ephemeral") {
		t.Errorf("expected an ephemeral WARNING in the log, got: %q", buf.String())
	}
}

// TestOpenStore_SnapshotSurvivesReopen proves observability keeps working through
// the new path logic AND that data on a persistent path survives a "restart"
// (close + reopen of the same file) — the R1 fix, end to end.
func TestOpenStore_SnapshotSurvivesReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "obsdir", "obs.db") // missing dir → created on open

	st, err := OpenStore(p)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	now := time.Now().Unix()
	if err := st.Flush("acme", Snapshot{TenantID: "acme", TS: now, P50US: 123, SLOStatus: "ok"}); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	st.Close()

	// Reopen the same path — simulates a process/container restart.
	st2, err := OpenStore(p)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	h, err := st2.History("acme", 24)
	if err != nil {
		t.Fatalf("History after reopen: %v", err)
	}
	if len(h) != 1 || h[0].P50US != 123 {
		t.Fatalf("snapshot did not survive reopen: %+v", h)
	}
}
