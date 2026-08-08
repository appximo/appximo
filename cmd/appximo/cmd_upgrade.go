package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// INSTALL-PROMPT-S1: the update path in the product, not only in a prompt.
//
// The class this closes: once releases exist, the most common state of an
// installed engine is OLD, and the failure it produces is confusing (a command
// the binary lacks reads as a typo). `version` now says a newer one exists;
// this command is the one-liner that acts on it.
//
// WINDOWS — the decision, and why. A running .exe cannot be OVERWRITTEN, but it
// CAN be RENAMED (Windows keeps the open handle to the moved file). So the swap
// is: rename the current binary to <name>.old.exe, then move the new one into
// its place. The stale .old file is removed on the NEXT upgrade — deleting it
// now would fail whenever the old binary is still running, which is exactly the
// case this dance exists for. The alternative (a helper process that waits for
// the parent to exit) buys nothing here: the rename already succeeds while
// running, and a helper is one more moving part to get wrong on the platform we
// cannot test. NOT VERIFIED LIVE — this box is Linux; the Windows path is
// reasoned from the platform's semantics and is marked as such in the docs.
var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Replace this binary with the newest published release",
	Long: `Download the newest release for this platform, verify its checksum, and
replace THIS binary in place.

  --version vX.Y.Z   install that exact tag instead of the newest
  --check            only report what would happen; change nothing
  --force            reinstall even when already current

The running binary is replaced atomically (on Windows it is renamed aside
first, since a running .exe cannot be overwritten — the leftover
<name>.old.exe is cleaned up by the next upgrade). Nothing about you or this
machine is ever sent: the download is an anonymous GET of a public URL.

If this binary is not writable (a system-wide install), the command says so and
names the exact privileged command to re-run — it never silently installs a
second copy somewhere else on the PATH.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		want, _ := cmd.Flags().GetString("version")
		checkOnly, _ := cmd.Flags().GetBool("check")
		force, _ := cmd.Flags().GetBool("force")

		self, err := selfPath()
		if err != nil {
			return fmt.Errorf("cannot locate this binary: %w", err)
		}
		asset, err := assetName()
		if err != nil {
			return err
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		defer cancel()

		target := want
		if target == "" {
			tag, err := latestTag(ctx)
			if err != nil {
				return fmt.Errorf("could not reach the release server to find the newest version: %w\n"+
					"  Check your network, or pass --version vX.Y.Z to install a specific one", err)
			}
			target = tag
		}
		if !strings.HasPrefix(target, "v") {
			target = "v" + target
		}

		fmt.Printf("installed: %s\nnewest:    %s\nbinary:    %s\n\n", version, target, self)
		switch {
		case version == target && !force:
			fmt.Println("Already on that version — nothing to do. (--force reinstalls anyway.)")
			return nil
		case checkOnly:
			fmt.Printf("An upgrade to %s is available. Run 'appximo upgrade' to apply it.\n", target)
			return nil
		}

		// Download + checksum BEFORE touching the installed binary: a failed
		// download must never leave the machine without a working engine.
		url := downloadURL(target, asset, want == "")
		fmt.Printf("downloading %s…\n", url)
		blob, err := httpGet(ctx, url)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		sums, err := httpGet(ctx, downloadURL(target, checksumsAsset, want == ""))
		if err != nil {
			return fmt.Errorf("could not fetch %s to verify the download: %w", checksumsAsset, err)
		}
		if err := verifyChecksum(blob, sums, asset, target); err != nil {
			return err
		}
		fmt.Println("checksum ok")

		if err := replaceSelf(self, blob); err != nil {
			return err
		}
		fmt.Printf("\nUpgraded to %s. Verify with:  appximo version && appximo prompt\n", target)
		return nil
	},
}

// selfPath resolves the real file behind this process, following symlinks so a
// /usr/local/bin/appximo -> /opt/... link is not itself overwritten.
func selfPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// downloadURL builds the asset URL. For the newest release it uses the stable
// UNVERSIONED alias (nothing to rewrite when a tag is cut); for an explicit
// tag it uses that release's own versioned asset.
func downloadURL(tag, asset string, useLatestAlias bool) string {
	if useLatestAlias {
		return fmt.Sprintf(releaseDownloadFmt, asset)
	}
	if asset == checksumsAsset {
		return fmt.Sprintf("https://github.com/appximo/appximo/releases/download/%s/%s", tag, asset)
	}
	versioned := strings.Replace(asset, "appximo-", "appximo-"+tag+"-", 1)
	return fmt.Sprintf("https://github.com/appximo/appximo/releases/download/%s/%s", tag, versioned)
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// verifyChecksum matches the download against the release's checksums.txt. The
// file lists BOTH the alias and the versioned name for the same bytes, so
// either spelling resolves.
func verifyChecksum(blob, sums []byte, asset, tag string) error {
	sum := sha256.Sum256(blob)
	got := hex.EncodeToString(sum[:])
	versioned := strings.Replace(asset, "appximo-", "appximo-"+tag+"-", 1)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name != asset && name != versioned {
			continue
		}
		if fields[0] == got {
			return nil
		}
		return fmt.Errorf("checksum MISMATCH for %s: expected %s, got %s — the download was not installed", name, fields[0], got)
	}
	return fmt.Errorf("no checksum entry for %q in %s — refusing to install an unverified binary", asset, checksumsAsset)
}

// replaceSelf swaps the running binary for the downloaded bytes. The new file
// is written NEXT TO the target (same filesystem, so the rename is atomic) and
// only then moved into place.
func replaceSelf(self string, blob []byte) error {
	dir := filepath.Dir(self)
	tmp, err := os.CreateTemp(dir, ".appximo-upgrade-*")
	if err != nil {
		return notWritable(dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed away
	if _, err := tmp.Write(blob); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		// A running .exe cannot be overwritten, but it CAN be renamed aside.
		old := self + ".old.exe"
		_ = os.Remove(old) // left over from a previous upgrade; may still be locked
		if err := os.Rename(self, old); err != nil {
			return fmt.Errorf("could not move the current binary aside: %w\n"+
				"  Close every terminal, editor and running 'appximo serve', then run 'appximo upgrade' again", err)
		}
		if err := os.Rename(tmpName, self); err != nil {
			_ = os.Rename(old, self) // put it back rather than leave nothing installed
			return fmt.Errorf("could not install the new binary: %w", err)
		}
		fmt.Printf("previous binary kept at %s (removed by the next upgrade)\n", old)
		return nil
	}

	if err := os.Rename(tmpName, self); err != nil {
		return notWritable(self, err)
	}
	return nil
}

// notWritable turns a permission failure into the ADR-024 shape: name the path
// that refused and the exact command that works.
func notWritable(path string, err error) error {
	if !os.IsPermission(err) {
		return fmt.Errorf("%s: %w", path, err)
	}
	return fmt.Errorf("no permission to write %s: %w\n"+
		"  Re-run with privileges:  sudo appximo upgrade\n"+
		"  (or install into a user-writable directory that is on your PATH — never a second copy the PATH finds first)",
		path, err)
}

func init() {
	upgradeCmd.Flags().String("version", "", "install this exact tag (default: the newest release)")
	upgradeCmd.Flags().Bool("check", false, "report what would happen; change nothing")
	upgradeCmd.Flags().Bool("force", false, "reinstall even when already on the target version")
	rootCmd.AddCommand(upgradeCmd)
}
