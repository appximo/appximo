package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseSemverAndNewerThan(t *testing.T) {
	cases := []struct {
		a, b  string
		newer bool
		why   string
	}{
		{"v0.1.2", "v0.1.5", true, "patch bump"},
		{"v0.1.5", "v0.1.5", false, "identical"},
		{"v0.1.5", "v0.1.2", false, "older is not newer"},
		{"v0.9.9", "v1.0.0", true, "major bump"},
		{"v1.2.0", "v1.10.0", true, "minor compares numerically, not lexically"},
		{"dev", "v0.1.5", false, "a source build is never told to upgrade"},
		{"v0.1.5", "garbage", false, "an unparseable remote tag is not an upgrade"},
		{"v0.1.5-rc1", "v0.1.5", false, "pre-release metadata is dropped, so equal"},
	}
	for _, c := range cases {
		if got := newerThan(c.a, c.b); got != c.newer {
			t.Errorf("newerThan(%q,%q) = %v, want %v (%s)", c.a, c.b, got, c.newer, c.why)
		}
	}
}

// The check must be silenceable, and must never fire in CI — a pipeline should
// not pay for a network round trip it did not ask for.
func TestVersionCheckOptOut(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("APPXIMO_NO_VERSION_CHECK", "")
	if versionCheckDisabled() {
		t.Error("with neither variable set the check should be enabled")
	}
	for _, v := range []string{"1", "true", "yes", "on"} {
		t.Setenv("APPXIMO_NO_VERSION_CHECK", v)
		if !versionCheckDisabled() {
			t.Errorf("APPXIMO_NO_VERSION_CHECK=%q must disable the check", v)
		}
	}
	for _, v := range []string{"0", "false"} {
		t.Setenv("APPXIMO_NO_VERSION_CHECK", v)
		if versionCheckDisabled() {
			t.Errorf("APPXIMO_NO_VERSION_CHECK=%q must NOT disable the check", v)
		}
	}
	t.Setenv("APPXIMO_NO_VERSION_CHECK", "")
	t.Setenv("CI", "true")
	if !versionCheckDisabled() {
		t.Error("CI must disable the check implicitly")
	}
}

// A dev/source build has nothing to compare against and must never reach the
// network — this also keeps the unit lane offline.
func TestCheckForUpdateIsOfflineForDevBuilds(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("APPXIMO_NO_VERSION_CHECK", "")
	if got := checkForUpdate("dev"); got != "" {
		t.Errorf("a dev build must not report an update, got %q", got)
	}
	t.Setenv("APPXIMO_NO_VERSION_CHECK", "1")
	if got := checkForUpdate("v0.0.1"); got != "" {
		t.Errorf("opted out, must be silent, got %q", got)
	}
}

func TestAssetNameCoversPublishedPlatforms(t *testing.T) {
	name, err := assetName()
	if err != nil {
		t.Skipf("no published binary for %s/%s — nothing to assert", runtime.GOOS, runtime.GOARCH)
	}
	if !strings.HasPrefix(name, "appximo-") {
		t.Errorf("asset name %q does not look like a release asset", name)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(name, ".exe") {
		t.Errorf("windows asset %q must end in .exe", name)
	}
	// It must be the UNVERSIONED alias: a versioned name would rot at each tag.
	if strings.Contains(name, "v0.") || strings.Contains(name, "v1.") {
		t.Errorf("asset name %q must be the stable alias, not a versioned file", name)
	}
}

func TestDownloadURLAliasVsPinnedTag(t *testing.T) {
	// latest → the stable alias, no version anywhere in the URL
	got := downloadURL("v0.1.5", "appximo-linux-amd64", true)
	if strings.Contains(got, "v0.1.5") {
		t.Errorf("the latest path must not pin a version: %s", got)
	}
	if !strings.Contains(got, "/releases/latest/download/appximo-linux-amd64") {
		t.Errorf("unexpected alias URL: %s", got)
	}
	// an explicit tag → that release's versioned asset
	got = downloadURL("v0.1.5", "appximo-linux-amd64", false)
	if !strings.Contains(got, "/releases/download/v0.1.5/appximo-v0.1.5-linux-amd64") {
		t.Errorf("unexpected pinned URL: %s", got)
	}
	// checksums.txt keeps its name in both forms
	if got := downloadURL("v0.1.5", checksumsAsset, false); !strings.HasSuffix(got, "/v0.1.5/checksums.txt") {
		t.Errorf("unexpected checksums URL: %s", got)
	}
}

// The real checksums.txt lists BOTH the alias and the versioned name for the
// same bytes; either spelling must verify, and a mismatch must be loud.
func TestVerifyChecksum(t *testing.T) {
	blob := []byte("pretend this is a binary")
	// sha256 of exactly those bytes (printf … | sha256sum)
	const sum = "a68531b40a1fe0aa3628a3f98893831c74a9044ad1722c81453d2680798f9c06"
	sums := "deadbeef  some-other-file\n" + sum + "  appximo-linux-amd64\n" + sum + "  appximo-v0.1.5-linux-amd64\n"

	if err := verifyChecksum(blob, []byte(sums), "appximo-linux-amd64", "v0.1.5"); err != nil {
		t.Errorf("alias spelling must verify: %v", err)
	}
	bad := "0000000000000000000000000000000000000000000000000000000000000000  appximo-linux-amd64\n"
	err := verifyChecksum(blob, []byte(bad), "appximo-linux-amd64", "v0.1.5")
	if err == nil || !strings.Contains(err.Error(), "MISMATCH") {
		t.Errorf("a wrong checksum must fail loudly, got %v", err)
	}
	err = verifyChecksum(blob, []byte("deadbeef  unrelated\n"), "appximo-linux-amd64", "v0.1.5")
	if err == nil || !strings.Contains(err.Error(), "no checksum entry") {
		t.Errorf("a missing entry must refuse to install, got %v", err)
	}
}

// replaceSelf must swap the file atomically and leave it executable. (The
// Windows branch — rename-aside, because a running .exe cannot be overwritten —
// is NOT exercised here: this lane is Linux. It is documented as such.)
func TestReplaceSelfSwapsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the unix path is what this lane can exercise")
	}
	dir := t.TempDir()
	self := filepath.Join(dir, "appximo")
	if err := os.WriteFile(self, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceSelf(self, []byte("NEW BINARY")); err != nil {
		t.Fatalf("replaceSelf: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW BINARY" {
		t.Errorf("binary not replaced, got %q", got)
	}
	info, err := os.Stat(self)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("replacement is not executable: %v", info.Mode())
	}
	// no temp files left behind
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("upgrade left files behind: %v", names)
	}
}

// A read-only destination must produce the ADR-024 shape: the path that
// refused AND the command that works — never a silent second install.
func TestReplaceSelfUnwritableIsActionable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions; nothing to assert")
	}
	dir := t.TempDir()
	self := filepath.Join(dir, "appximo")
	if err := os.WriteFile(self, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()

	err := replaceSelf(self, []byte("NEW"))
	if err == nil {
		t.Fatal("expected a permission error")
	}
	for _, want := range []string{dir, "sudo appximo upgrade"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must name %q: %v", want, err)
		}
	}
	if got, _ := os.ReadFile(self); string(got) != "OLD" {
		t.Errorf("a failed upgrade must leave the working binary intact, got %q", got)
	}
}

// The message shape itself, asserted regardless of uid — the test above skips
// under root (which ignores directory permissions), and this is the part that
// must never regress: the path that refused AND the command that works — which
// is PER PLATFORM (OPS-25: `sudo` on Windows is a lie; there the advice is an
// elevated prompt).
func TestNotWritableNamesPathAndFix(t *testing.T) {
	privileged := "sudo appximo upgrade"
	if runtime.GOOS == "windows" {
		privileged = "Run as administrator"
	}
	err := notWritable("/usr/local/bin/appximo", os.ErrPermission)
	for _, want := range []string{"/usr/local/bin/appximo", privileged, "on your PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("permission error must contain %q:\n%v", want, err)
		}
	}
	// A non-permission failure keeps its own cause instead of suggesting
	// privileges.
	other := notWritable("/tmp/x", errors.New("no space left on device"))
	if strings.Contains(other.Error(), "sudo") || strings.Contains(other.Error(), "administrator") {
		t.Errorf("a non-permission error must not suggest privileges: %v", other)
	}
}

// ADR-024 on the version axis: an unknown command must name the possibility
// that the binary is simply too old, without inventing a per-command catalogue.
func TestAnnotateUnknownCommand(t *testing.T) {
	err := annotateUnknownCommand(errors.New(`unknown command "prompt" for "appximo"`))
	msg := err.Error()
	for _, want := range []string{`unknown command "prompt"`, "too old", "appximo version", "appximo upgrade"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message must contain %q:\n%s", want, msg)
		}
	}
	// Unrelated errors pass through untouched.
	other := errors.New("missing required configuration: DATABASE_URL")
	if got := annotateUnknownCommand(other); got.Error() != other.Error() {
		t.Errorf("unrelated errors must not be annotated: %v", got)
	}
	if annotateUnknownCommand(nil) != nil {
		t.Error("nil must stay nil")
	}
}
