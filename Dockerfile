# syntax=docker/dockerfile:1
#
# Multi-stage build → small Alpine runtime (~60MB total; the static binary is
# ~45MB of it). The binary is fully static (CGO_ENABLED=0): wazero (WASM) and
# modernc.org/sqlite are pure Go, so no C toolchain or libc is needed — Alpine
# is chosen over scratch for three operational wins:
#   * a real /tmp           → the SQLite observability store works out of the box
#   * busybox wget          → Docker HEALTHCHECK against /healthz
#   * a shell               → `docker exec` for the `appitools token` helper
# pg_dump-based backups (POST /admin/backup) still need postgresql16-client:
# add `RUN apk add --no-cache postgresql16-client` if you want them in-image.

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION/REVISION land in `appitools version` and /health via -X (CI injects
# the tag/SHA; local builds report "dev").
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
    -o /out/appitools ./cmd/appitools

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
# The quickstart schema baked in so `serve` starts out of the box — it is the
# EXACT schema shown in the README hook (todo-api/tasks), so the reader follows
# one coherent example end to end. Mount your own schema over this path (or
# pass a different --schema) for your project.
COPY --from=builder /app/examples/quickstart/schema.json /etc/appitools/schema.json

USER appitools

# 8080 = data plane (public REST + GraphQL). 9090 = control plane (private/admin).
EXPOSE 8080 9090

# busybox wget ships with Alpine; /healthz is the liveness endpoint.
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["appitools"]
CMD ["serve", "--schema", "/etc/appitools/schema.json", "--port", "8080"]
