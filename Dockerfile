# syntax=docker/dockerfile:1
#
# Multi-stage build. The runtime image is `scratch`: the binary is fully static
# (CGO_ENABLED=0) and Appitools' heavier dependencies are pure Go — wazero (WASM)
# and the modernc.org/sqlite driver (observability persistence) need no C toolchain.
#
# Runtime notes for the scratch image:
#   * There is no shell and no /tmp. The optional SQLite observability store
#     (default path /tmp/obs.db) is therefore disabled — this is non-fatal; the
#     server logs a warning and runs normally. Set OBS_DB_PATH to a mounted,
#     writable volume to re-enable it.
#   * pg_dump-based backups (POST /admin/backup) are unavailable (no pg_dump in the
#     image); that endpoint returns 503. Run backups from a host with the client.
# If you need a shell, /tmp, or pg_dump in the image, swap `FROM scratch` for
# `FROM alpine:3.19` and add `RUN apk add --no-cache ca-certificates postgresql16-client`.

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/appitools ./cmd/appitools

FROM scratch
# TLS roots for outbound HTTPS (SSRF-safe webhook delivery, alerting).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /out/appitools /appitools
# An example schema so `serve` starts out of the box. Mount your own schema over
# this path (or pass a different --schema) for your project.
COPY --from=builder /app/testdata/logistics/schema.json /etc/appitools/schema.json

# 8080 = data plane (public REST + GraphQL). 9090 = control plane (private/admin).
EXPOSE 8080 9090

ENTRYPOINT ["/appitools"]
CMD ["serve", "--schema", "/etc/appitools/schema.json", "--port", "8080"]
