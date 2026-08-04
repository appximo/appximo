#!/bin/sh
# build-engine.sh — THE canonical engine build. Single source of truth for
# build flags, consumed by:
#   - Dockerfile (builder stage)
#   - .github/workflows/release.yml (cross-compiled release binaries)
#   - tools/devhub/api/deploy.go (the deploy pipeline)
#   - `make engine` (local/manual builds, incl. the PRIMER deploy protocol)
# Do NOT hand-write `go build` for the engine anywhere else: three flag
# divergences already shipped from exactly that (a dynamic prod binary, a
# version-less deploy, a drifted manual protocol).
#
# Flags and why:
#   CGO_ENABLED=0   fully static binary (runs on alpine/scratch; wazero and
#                   modernc.org/sqlite are pure Go — nothing needs cgo)
#   -trimpath       no local paths in the binary
#   -ldflags -s -w  drop symbol table + DWARF (~30% smaller; runtime pclntab
#                   stays, so symbolized stacks still work)
#   -X main.version/main.revision   `appximo version` + /health report the
#                   build identity (release tag or short SHA; "dev" otherwise)
#
# Usage: build-engine.sh <output-path> [version] [revision]
#   version/revision default to git short-SHA/SHA when in a git checkout,
#   else "dev"/"unknown" (e.g. the Docker build context has no .git — the
#   Dockerfile passes both explicitly as build args).
# Env: GOOS/GOARCH respected (cross-compile); GO overrides the toolchain path.
set -eu

OUT="${1:?usage: build-engine.sh <output-path> [version] [revision]}"
VERSION="${2:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
REVISION="${3:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}"

CGO_ENABLED=0 "${GO:-go}" build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
  -o "${OUT}" ./cmd/appximo
