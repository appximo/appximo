# Appitools

> **Production-ready APIs, fast.**
> REST + GraphQL + OpenAPI, natively compiled in Go, on any server you choose.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://golang.org/dl/)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-24%2F24%20passing-brightgreen)](#testing)

## What is Appitools?

Define your data model → Appitools serves a native Go API that runs anywhere.
No Node.js. No ORM overhead. No DevOps complexity.

Built for software agencies and fintechs in Latin America that need to ship
production APIs fast, with compliance features (DIAN CUFE/CUNE, NIT validation,
Bre-B-ready webhooks) built in from day one.

A single JSON schema becomes a multi-tenant REST + GraphQL API — with JWT auth,
RBAC, per-tenant Postgres isolation, hooks, and observability — compiled into one
~46 MB static binary that uses ~24 MB RAM at cold start (~50 MB under sustained load).

## Quick Start (self-hosted, 5 minutes)

```bash
git clone https://github.com/miguelangel/appitools
cd appitools
cp .env.example .env          # set JWT_SECRET, ADMIN_KEY, DB_PASSWORD
docker compose up             # builds the engine, starts Postgres
```

The data plane is live at `http://localhost:8080` (REST + GraphQL) and the control
plane at `http://localhost:9090` (tenant/schema admin — keep it private). Health
check:

```bash
curl http://localhost:8080/healthz      # {"status":"alive"}
```

The engine boots with the bundled `logistics` example schema. To serve your own
model, mount your schema over `/etc/appitools/schema.json` (or run the binary with
`--schema yourschema.json`). To create live tenant tables, register a tenant through
the control plane — see [Deployment](#deployment).

> The optional visual schema editor (ReactFlow canvas + dashboards) ships from the
> separate [`appitools-ui`] repo and is wired into compose under an opt-in profile:
> `docker compose --profile ui up` → http://localhost:3100.

## Features

**API generation**
- REST endpoints: `GET` (list), `GET` by id, `POST`, `PUT`, `PATCH`, `DELETE`
- GraphQL: queries, mutations, filters, sorting, pagination (with depth/complexity limits)
- OpenAPI 3.0 spec auto-generated (`appitools openapi schema.json`)
- Keyset pagination (production-ready, no `OFFSET`): `?after=<uuid>` / `?before=<uuid>`
- Filters per field type: `eq` (all), `partial` / `start` (ILIKE, for string/text), `gt` `gte` `lt` `lte` (numbers & time), `after` / `before` (time)

**Multi-tenancy**
- Schema-per-tenant isolation (`SET LOCAL search_path`), not Row Level Security
- Multiple isolated tenants on a single Postgres instance (schema-per-tenant isolation)
- Real-time schema cache invalidation via `pg_notify`

**Security**
- JWT HS256, constant-time validation, `exp` enforced
- RBAC with JSON policies: per-role, per-resource, per-field, dynamic row conditions
- SSRF-safe webhook delivery
- OWASP API Top 10 hardening (body limits, masked DB errors, no cross-tenant leakage)

**Extensibility**
- Webhooks: HMAC-signed, async, bounded dispatch with retries + backoff
- JS sandbox (Goja): custom validation, watchdog timeout, no CGO
- WASM (Wazero): heavy computation, language-agnostic, no CGO
- Built-in: DIAN CUFE/CUNE (SHA-384), NIT mod-11 validation

**Resilience & observability**
- Circuit breaker (gobreaker), per-tenant rate limiting
- Graceful shutdown with request draining
- Query timeouts, `MaxBytesReader`, slowloris timeouts
- Prometheus `/metrics`, per-tenant `/debug/tenant/{id}`, SLO burn-rate alerting

## Benchmarks

Measured on a DigitalOcean **2 vCPU / 4 GB / $16/month** droplet — with JWT + RBAC +
multi-tenancy + rate limiting + circuit breaker **all active**:

| RPS   | p50    | p95    | p99    | Errors |
|-------|--------|--------|--------|--------|
| 500   | 0.5ms  | 0.72ms | 1.6ms  | 0      |
| 1000  | 0.45ms | 1.32ms | 4.9ms  | 0      |
| 2000  | 0.46ms | 10.6ms | 33ms   | 0      |
| 3000  | 0.91ms | 96ms   | 153ms  | 0.6%*  |

<sub>*rate limited by design</sub>

RAM: **~24 MB cold start, ~50 MB under sustained load** · CPU at 2000 RPS: **40% of one core**

**vs NestJS** on the same hardware (no auth, no RBAC, no multi-tenancy — a deliberately
favorable baseline for NestJS): it saturated and collapsed at **1092 RPS** real
throughput. The fact that NestJS ran *without* the security and isolation Appitools
runs *with* makes the gap conservative, not inflated.

Reproduce these numbers: see [benchmarks/README.md](benchmarks/README.md).

## Configuration

The server is configured by **environment variables**; the schema file and HTTP port
are **CLI flags** on `appitools serve`.

| Setting | Kind | Required | Description |
|---------|------|----------|-------------|
| `DATABASE_URL` | env | **yes** | PostgreSQL connection string |
| `JWT_SECRET` | env | **yes** | HS256 signing secret (use ≥ 32 chars) |
| `ADMIN_KEY` | env | **yes** | `X-Admin-Key` for `/metrics`, `/debug`, `/admin`, control plane |
| `REDIS_URL` | env | no | Enables the async migration worker (optional; DDL is applied synchronously without it) |
| `DB_MAX_CONNS` | env | no | Max DB connections (default: `cores*2+2`, capped sensibly) |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | env | no | Per-tenant token bucket (default 1000 / 100) |
| `GOMAXPROCS` | env | no | OS threads (auto-detected via automaxprocs) |
| `OBS_DB_PATH` | env | no | SQLite path for observability persistence (default `/tmp/obs.db`) |
| `SLACK_WEBHOOK_URL` | env | no | SLO burn-rate alerts (noop if unset) |
| `--schema` | flag | **yes** | Path to the JSON schema file |
| `--port` | flag | no | Data-plane HTTP port (default `8080`) |

> The control plane listens on a fixed port **9090**. Keep it private (firewall /
> localhost only); it is not meant to be internet-exposed.

## Deployment

**Self-hosted with Docker (recommended)**

```bash
docker compose -f docker-compose.yml up -d
```

**Binary**

```bash
go build -o appitools ./cmd/appitools
export DATABASE_URL="postgres://user:pass@localhost:5432/db?sslmode=disable"
export JWT_SECRET="your-32-char-minimum-secret" ADMIN_KEY="your-admin-key"
./appitools serve --schema schema.json --port 8080
```

**Register a tenant** (creates the per-tenant Postgres schema + tables) through the
control plane on `:9090`:

```bash
curl -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d '{"tenant_id":"acme","display_name":"Acme","email":"a@acme.com","plan":"free","schema":<your schema JSON>}'
```

Tenants are then addressed by the `Host` subdomain (`acme.localhost` →
`tenant_acme`). Mint a token with `appitools token --secret "$JWT_SECRET" --tenant acme --role super_admin`.

## Testing

```bash
go test ./... -race            # 24/24 packages
go vet ./...
docker build -t appitools .
```

## License

Apache 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).

[`appitools-ui`]: https://github.com/miguelangel/appitools-ui
