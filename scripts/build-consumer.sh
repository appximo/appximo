#!/bin/sh
# build-consumer.sh — THE canonical build for a CONSUMER app on the appitools
# framework (ADR-023; the consumer twin of build-engine.sh). Run it from the
# CONSUMER repo root. It exists because the deploy journey (PROD-JOURNEY-1B)
# found every consumer re-discovers the same three traps:
#
#   1. The SPA-embed trap: hashed frontend assets are conventionally gitignored,
#      so a bare `go build` embeds an EMPTY shell that "works" until a browser
#      opens it. The web build must run first — this script runs it.
#   2. The version trap: without ldflags the binary reports "dev" and /health
#      cannot tell an operator WHICH build serves — a rollback becomes
#      guesswork. This script injects the git identity; pass it to
#      Config.Version in your main().
#   3. The static-binary flags: CGO_ENABLED=0 + -trimpath + -s -w, same as the
#      engine, for a portable, reproducible artifact.
#
# A consumer main() should use appitools.ParseServeArgs so the binary honors
# the deployable contract (`version`, `serve --schema --port`, fail-loud args);
# this script verifies the contract on the produced binary and warns if not.
#
# Usage: build-consumer.sh <output-path> [version] [revision]
#   version/revision default to git short-SHA/SHA of the CONSUMER repo.
# Env:
#   WEB_DIR    frontend dir with an npm build (default "web"; "" = no frontend)
#   SKIP_WEB=1 skip the SPA build (only if WEB_DIR's build output is fresh)
#   MAIN_PKG   package to build (default ".")
#   GOOS/GOARCH respected (cross-compile); GO overrides the toolchain path.
#
# From a consumer repo, invoke it out of your engine dependency (resolves the
# replace directive AND the module cache):
#   bash "$(go list -m -f '{{.Dir}}' github.com/miguelangel/appitools)/scripts/build-consumer.sh" out/app
set -eu

OUT="${1:?usage: build-consumer.sh <output-path> [version] [revision]}"
VERSION="${2:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
REVISION="${3:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}"
WEB_DIR="${WEB_DIR-web}"
MAIN_PKG="${MAIN_PKG:-.}"

if [ -n "$WEB_DIR" ] && [ -d "$WEB_DIR" ] && [ "${SKIP_WEB:-}" != "1" ]; then
	echo "→ building the SPA ($WEB_DIR/ — hashed assets are gitignored; a bare go build would embed an empty shell)…"
	(cd "$WEB_DIR" && npm run build >/dev/null)
fi

echo "→ building the binary (version=${VERSION})…"
CGO_ENABLED=0 "${GO:-go}" build -trimpath \
	-ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
	-o "${OUT}" "${MAIN_PKG}"

# Contract check (ADR-023): `<bin> version` must run and identify the build.
# Skipped when cross-compiling (the artifact cannot run here).
if [ "${GOOS:-$(go env GOOS)}" = "$(go env GOHOSTOS)" ] && [ "${GOARCH:-$(go env GOARCH)}" = "$(go env GOHOSTARCH)" ]; then
	ID="$("$OUT" version 2>/dev/null | head -1 || true)"
	if [ -n "$ID" ]; then
		echo "✓ ${OUT} — $ID"
	else
		echo "! ${OUT} built, but it does NOT honor the deployable contract ('$OUT version' failed)." >&2
		echo "  install.sh will refuse it. Wire appitools.ParseServeArgs in main() — docs/adr/ADR-023." >&2
		exit 1
	fi
else
	echo "✓ ${OUT} (cross-compiled — contract check skipped)"
fi
