package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundtripAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.Set("1", "admin_key", "topsecret-roundtrip-123"); err != nil {
		t.Fatalf("set: %v", err)
	}

	// A fresh Store must read the same value back from disk.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	v, ok := s2.Get("1", "admin_key", "test")
	if !ok || v != "topsecret-roundtrip-123" {
		t.Fatalf("got (%q,%v), want the stored value", v, ok)
	}
	if _, ok := s2.Get("2", "admin_key", "test"); ok {
		t.Fatal("unknown server must not resolve")
	}
}

func TestCiphertextDoesNotContainPlaintext(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	const secret = "PLAINTEXT-MUST-NOT-LEAK-9f8e7d"
	if err := s.Set("42", "admin_key", secret); err != nil {
		t.Fatalf("set: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, secretsFile))
	if err != nil {
		t.Fatalf("read secrets file: %v", err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("secrets.age contains the plaintext value")
	}
	// The map key (server id) and field name must not appear either: the whole
	// JSON document is inside the ciphertext.
	if bytes.Contains(raw, []byte("admin_key")) {
		t.Fatal("secrets.age leaks structure (field names) in the clear")
	}
}

func TestFilePermissions(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set("1", "admin_key", "x"); err != nil {
		t.Fatalf("set: %v", err)
	}
	for _, name := range []string{keyFile, secretsFile} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s permissions = %o, want 0600", name, perm)
		}
	}
}

func TestDeleteRemovesServerSecrets(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Set("1", "admin_key", "a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("2", "admin_key", "b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := s.Get("1", "admin_key", "test"); ok {
		t.Fatal("deleted server still resolves")
	}
	if v, ok := s.Get("2", "admin_key", "test"); !ok || v != "b" {
		t.Fatal("unrelated server lost its secret")
	}
	// Deletion must survive a reopen (the file was re-encrypted).
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, ok := s2.Get("1", "admin_key", "test"); ok {
		t.Fatal("deleted server resolves after reopen")
	}
}

func TestAuditCallbackOnGet(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var gotServer, gotOp string
	calls := 0
	s.Audit = func(serverID, operation string) { gotServer, gotOp = serverID, operation; calls++ }
	if err := s.Set("7", "admin_key", "v"); err != nil {
		t.Fatal(err)
	}
	s.Get("7", "admin_key", "metrics_scrape")
	if calls != 1 || gotServer != "7" || gotOp != "metrics_scrape" {
		t.Fatalf("audit got (%q,%q,calls=%d)", gotServer, gotOp, calls)
	}
	// A miss must not audit.
	s.Get("99", "admin_key", "metrics_scrape")
	if calls != 1 {
		t.Fatalf("miss audited (calls=%d)", calls)
	}
}
