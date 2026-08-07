package appximo

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDotEnv pins the F1/F1-bis contract: a .env in the working directory
// is loaded with the real environment winning, and a UTF-8 BOM — what
// PowerShell 5.1's Set-Content writes — must NOT glue itself to the first
// variable's name (the field report lost hours to a BOM-prefixed
// DATABASE_URL, visually identical to the right one).
func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	content := "\uFEFF" + "FIRST_VAR=from-dotenv\r\n" + // BOM + CRLF (the F1-bis file)
		"# a comment\n" +
		"\n" +
		"export EXPORTED_VAR=works\n" +
		"QUOTED_VAR=\"hello world\"\n" +
		"SINGLE_QUOTED='single'\n" +
		"ALREADY_SET=from-dotenv\n" +
		"not-a-kv-line\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	old, _ := os.Getwd()
	defer os.Chdir(old) //nolint:errcheck
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"FIRST_VAR", "EXPORTED_VAR", "QUOTED_VAR", "SINGLE_QUOTED"} {
		os.Unsetenv(k) //nolint:errcheck
	}
	t.Setenv("ALREADY_SET", "from-environment")

	n := LoadDotEnv()
	if n != 4 {
		t.Fatalf("loaded %d variables, want 4 (FIRST_VAR, EXPORTED_VAR, QUOTED_VAR, SINGLE_QUOTED)", n)
	}
	// The BOM must be stripped from the NAME — the exact F1-bis failure.
	if got := os.Getenv("FIRST_VAR"); got != "from-dotenv" {
		t.Fatalf("FIRST_VAR = %q — the BOM glued itself to the variable name (F1-bis)", got)
	}
	if got := os.Getenv("EXPORTED_VAR"); got != "works" {
		t.Fatalf("export-prefixed line: got %q", got)
	}
	if got := os.Getenv("QUOTED_VAR"); got != "hello world" {
		t.Fatalf("double-quoted value: got %q", got)
	}
	if got := os.Getenv("SINGLE_QUOTED"); got != "single" {
		t.Fatalf("single-quoted value: got %q", got)
	}
	// Precedence: the real environment always wins.
	if got := os.Getenv("ALREADY_SET"); got != "from-environment" {
		t.Fatalf("ALREADY_SET = %q — .env must never override the environment", got)
	}

	// No .env at all is a quiet no-op.
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if n := LoadDotEnv(); n != 0 {
		t.Fatalf("no .env: loaded %d, want 0", n)
	}
}
