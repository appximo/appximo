package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// The release surface shared by `version` (is there a newer one?) and
// `upgrade` (fetch it). Both resolve the newest tag the SAME way, and it is
// deliberately NOT the GitHub API: /releases/latest answers a 302 whose
// Location ends in the tag, which costs one request, needs no token, and is
// not subject to the API's 60/hour unauthenticated rate limit.
//
// NOTHING is ever sent about the user or the machine: this is a plain,
// anonymous GET of a public URL — no identifiers, no counters, no telemetry.
// The check is opt-out (APPXIMO_NO_VERSION_CHECK) and never runs on the
// request path or at `serve` boot; only a human-facing `version` performs it,
// with a short timeout, and a failure is silent.
const (
	releasesLatestURL  = "https://github.com/appximo/appximo/releases/latest"
	releaseDownloadFmt = "https://github.com/appximo/appximo/releases/latest/download/%s"
	checksumsAsset     = "checksums.txt"
)

// versionCheckDisabled reports the documented opt-out. Any non-empty value
// turns the check off, and CI-ish environments are honored implicitly so a
// pipeline never pays for a network round trip it did not ask for.
func versionCheckDisabled() bool {
	if v := os.Getenv("APPXIMO_NO_VERSION_CHECK"); v != "" && v != "0" && v != "false" {
		return true
	}
	return os.Getenv("CI") != "" // GitHub Actions, GitLab, CircleCI… all set it
}

// latestTag resolves the newest published tag from the /releases/latest
// redirect. It returns ("", err) on any failure — callers treat that as "do
// not know", never as "up to date" and never as a hard error.
func latestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, releasesLatestURL, nil)
	if err != nil {
		return "", err
	}
	// Do NOT follow the redirect: the Location header is the whole answer, and
	// stopping here avoids downloading the release page.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect from %s (status %d)", releasesLatestURL, resp.StatusCode)
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("unexpected tag %q", tag)
	}
	return tag, nil
}

// assetName is the release asset for the running platform. The UNVERSIONED
// alias is used deliberately: it is stable across releases, so nothing here
// (or in the docs) has to be rewritten when a tag is cut.
func assetName() (string, error) {
	switch runtime.GOOS {
	case "linux", "darwin":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return fmt.Sprintf("appximo-%s-%s", runtime.GOOS, runtime.GOARCH), nil
		}
	case "windows":
		if runtime.GOARCH == "amd64" {
			return "appximo-windows-amd64.exe", nil
		}
	}
	return "", fmt.Errorf("no published binary for %s/%s — build from source (go build ./cmd/appximo)", runtime.GOOS, runtime.GOARCH)
}

// newerThan reports whether tag b is a strictly newer semver than a. Both are
// "vX.Y.Z" (a build with an unset version reports "dev", which is never
// compared — a source build is not something we tell people to replace).
func newerThan(a, b string) bool {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if !oka || !okb {
		return false
	}
	for i := 0; i < 3; i++ {
		if pb[i] != pa[i] {
			return pb[i] > pa[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 { // drop any pre-release/build metadata
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// checkForUpdate returns the newer tag, or "" when there is nothing to say
// (opt-out, a dev build, no network, already current). It NEVER returns an
// error to the caller: a version check that nags on a plane is worse than no
// check at all.
func checkForUpdate(current string) string {
	if versionCheckDisabled() {
		return ""
	}
	if _, ok := parseSemver(current); !ok {
		return "" // "dev" / source build: nothing to compare against
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tag, err := latestTag(ctx)
	if err != nil || !newerThan(current, tag) {
		return ""
	}
	return tag
}
