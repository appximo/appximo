#!/bin/sh
# build-worker.sh — THE canonical worker build (cmd/appximo-worker, ADR-016
# §Class 2 outbox consumer). The sibling of scripts/build-engine.sh, with the
# SAME flags, consumed by:
#   - Dockerfile (builder stage — the worker ships in the same image as the engine)
#   - `make worker` (local/manual builds)
# Do NOT hand-write `go build` for the worker anywhere else — keep the one source
# of truth, exactly as the engine does.
#
# Flags and why (identical to the engine for parity):
#   CGO_ENABLED=0   fully static binary (alpine/scratch; the worker is pure Go)
#   -trimpath       no local paths in the binary
#   -ldflags -s -w  drop symbol table + DWARF (~30% smaller; pclntab stays)
#   -X main.version/main.revision   the "appximo-worker starting" log reports the
#                   build identity (release tag or short SHA; "dev" otherwise)
#
# Usage: build-worker.sh <output-path> [version] [revision]
#   version/revision default to git short-SHA/SHA in a checkout, else dev/unknown
#   (the Docker build context has no .git — the Dockerfile passes both explicitly).
# Env: GOOS/GOARCH respected (cross-compile); GO overrides the toolchain path.
set -eu

OUT="${1:?usage: build-worker.sh <output-path> [version] [revision]}"
VERSION="${2:-$(git rev-parse --short HEAD 2>/dev/null || echo dev)}"
REVISION="${3:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}"

CGO_ENABLED=0 "${GO:-go}" build -trimpath \
  -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
  -o "${OUT}" ./cmd/appximo-worker
