# syntax=docker/dockerfile:1
#
# Multi-stage build → small Alpine runtime. The binaries are fully static
# (CGO_ENABLED=0): wazero (WASM) and modernc.org/sqlite are pure Go, so no C
# toolchain or libc is needed — Alpine is chosen over scratch for three
# operational wins:
#   * a real /tmp           → the SQLite observability store works out of the box
#   * busybox wget          → Docker HEALTHCHECK against /healthz
#   * a shell               → `docker exec` for the `appitools token` helper
# pg_dump-based backups (POST /admin/backup) still need postgresql16-client:
# add `RUN apk add --no-cache postgresql16-client` if you want them in-image.
#
# ONE image, two roles: it ships BOTH the engine and the Class-2 outbox worker
# (cmd/appitools-worker). The entrypoint runs the engine by default; pass the
# `worker` keyword (compose `command: ["worker"]`) to run the worker instead — no
# second image, no duplicated base layers.

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION/REVISION land in `appitools version` and /health via -X (CI injects
# the tag/SHA; local builds report "dev" — no .git in the build context).
# The build command itself lives in scripts/build-engine.sh — the ONE
# canonical engine build, shared with release.yml and the deploy pipeline.
ARG VERSION=dev
ARG REVISION=unknown
RUN ./scripts/build-engine.sh /out/appitools "${VERSION}" "${REVISION}"
# The worker ships in the SAME image — same canonical flags (build-worker.sh).
RUN ./scripts/build-worker.sh /out/appitools-worker "${VERSION}" "${REVISION}"

FROM alpine:3.21

# Build metadata (injected by CI; harmless defaults for local builds).
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Appitools Engine" \
      org.opencontainers.image.description="Production-grade multi-tenant REST+GraphQL APIs compiled from a JSON schema" \
      org.opencontainers.image.source="https://github.com/miguel09acosta/appitools" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.licenses="Apache-2.0"

RUN apk add --no-cache ca-certificates \
    && addgroup -S appitools && adduser -S -G appitools appitools

COPY --from=builder /out/appitools /usr/local/bin/appitools
COPY --from=builder /out/appitools-worker /usr/local/bin/appitools-worker
COPY deploy/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
# The quickstart schema baked in so `serve` starts out of the box — it is the
# EXACT schema shown in the README hook (todo-api/tasks), so the reader follows
# one coherent example end to end. Mount your own schema over this path (or
# pass a different --schema) for your project.
COPY --from=builder /app/examples/quickstart/schema.json /etc/appitools/schema.json

# Runtime state roots under /var/lib/appitools, owned by the runtime user:
#   * files/  — content-addressable file store (FILES-V1): engine VFS.Put +
#               worker VFS.Get. Mount the SAME volume on engine + worker (one CAS).
#   * obs/    — observability SQLite store (OBS_DB_PATH): trace + snapshot history.
#               Engine only; persist it so the history survives a container restart.
# Creating these here means a named volume mounted at either path inherits the
# appitools ownership on first init (Docker seeds an empty named volume from the
# image dir), so the non-root user can write without a chown sidecar.
RUN mkdir -p /var/lib/appitools/files /var/lib/appitools/obs && chown -R appitools:appitools /var/lib/appitools

USER appitools

# 8080 = data plane (public REST + GraphQL). 9090 = control plane (private/admin).
EXPOSE 8080 9090

# busybox wget ships with Alpine; /healthz is the liveness endpoint. (The worker
# has no HTTP port; this probe is a no-op for a container started as `worker` —
# override/disable it in the worker compose service.)
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

# The dispatcher runs the engine by default and the worker on the `worker`
# keyword; every other arg is prefixed with `appitools` exactly as before.
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve", "--schema", "/etc/appitools/schema.json", "--port", "8080"]
