# Exploring a running Appximo

Everything you can observe in a live instance, and how to reach it.
Every endpoint, status code and response shape below was captured from a
running engine — nothing is hypothetical. Examples assume the quickstart
setup ([README](../README.md#quick-start-30-s-with-the-image-pull)): tenant
`acme`, schema `todo-api`, `$ADMIN_KEY`/`$JWT_SECRET` from your `.env`.

## The two planes

| Plane | Port | Audience | Exposure |
|---|---|---|---|
| **Data plane** | `:8080` | your API consumers | public (behind Caddy/TLS in prod) |
| **Control plane** | `:9090` | you, the operator | **never the internet** — localhost / internal network only |

The split is deliberate: tenant registration and schema management are
gated by a single `X-Admin-Key` header, so the control plane's safety
model is "unreachable", not "unguessable". A few admin endpoints
(`/metrics`, `/debug/*`, `/admin/*`) also exist on `:8080` for
convenience behind the same `X-Admin-Key` gate — they answer
`{"error":"unauthorized"}` (401) without it.

## Health — three endpoints, three jobs

| Endpoint | Auth | Returns | Use it for |
|---|---|---|---|
| `GET /healthz` | none | `{"status":"alive"}` | **liveness** probe — never touches Postgres |
| `GET /readyz` | none | `{"status":"ready"}`, **503 during drain/shutdown** | **readiness** — take the node out of rotation |
| `GET /health` | none | `{"status":"ok","version":"v0.1.0"}` | legacy/manual check (the compose healthcheck uses it); `version` is the build version — the release tag for [GitHub Releases](https://github.com/appximo/appximo/releases) binaries and published images, `"dev"` for a plain local `go build` |

The distinction matters on deploys: on SIGTERM the engine flips `/readyz`
to 503, drains in-flight requests, then exits — a load balancer watching
`/readyz` stops sending traffic before connections are killed.

## Metrics (`/metrics`, Prometheus)

```bash
curl -s -H "X-Admin-Key: $ADMIN_KEY" localhost:8080/metrics | grep ^appximo
```

The engine-specific series (plus the standard `go_*` / `process_*` set):

| Metric | Labels | What it tells you |
|---|---|---|
| `appximo_requests_total` | `tenant, method, route, status` | traffic + error rate per tenant/route |
| `appximo_request_duration_seconds` (histogram) | `tenant, method, route` | server-side latency distribution |
| `appximo_active_tenants` | — | tenants with recent traffic |

Infra paths (`/metrics`, `/debug`, `/admin`, `/health*`) are excluded
from the per-tenant series, so dashboards show application traffic only.
Prometheus needs the header — scrape config (`http_headers` requires
Prometheus 3.x; `secrets:` keeps the key out of Prometheus' own config UI):

```yaml
scrape_configs:
  - job_name: appximo
    static_configs: [{ targets: ["your-host:8080"] }]
    http_headers:
      X-Admin-Key:
        secrets: ["<your ADMIN_KEY>"]
```

## The Trace Explorer (`/debug/traces`)

A self-contained HTML UI over the last traces of every tenant: each
request broken into stage spans. It is the fastest way to answer *"where
did this request spend its time?"* — engine vs DB vs serialization —
and to see cache behavior directly.

**Access** — this is the one route that accepts the admin key as a query
param (so it opens in a browser); everything else is header-only:

```
http://localhost:8080/debug/traces?key=<your ADMIN_KEY>
```

**Reading a trace** — a real `GET /api/tasks` (cache miss), times in µs:

```json
{ "route": "/api/tasks", "total_us": 1751, "status": 200,
  "spans": [ { "name": "jwt",       "dur_us": 145  },
             { "name": "rbac",      "dur_us": 7    },
             { "name": "query",     "dur_us": 1483 },
             { "name": "serialize", "dur_us": 62   },
             { "name": "done",      "dur_us": 46   } ] }
```

Span names you will see: `jwt` → `rbac` → then **either** `cache_hit`
(no query span — the response came from memory) **or** `cache_miss` +
`query` (Postgres was hit) → `serialize` → `done`. This is how the
benchmark verified its cache-bypass claims — the spans don't lie. A
trace dominated by `query` is a data problem; one dominated by
`serialize` is a payload problem; `jwt`/`rbac` are typically µs.

## Per-tenant state (`/debug/tenant/{id}`)

The JSON API behind the explorer — one call, the tenant's whole
observable state:

```bash
curl -s -H "X-Admin-Key: $ADMIN_KEY" localhost:8080/debug/tenant/acme | python3 -m json.tool
```

Top-level keys (all captured live):

- `latency` — `cached` / `uncached` percentiles in µs: `{"p50_us": 2187, "p95_us": 9719, "p99_us": …, "count": 11, …}`
- `errors` — top-N grouped errors with first/last seen, counts, and **symbolized stack traces** (file:line into the engine source)
- `recent_traces` — last ~10 requests with the span breakdown above
- `recent_requests` — ring buffer samples: `{"start_us", "dur_us", "route", "status"}`
- `slo` — burn-rate state: `{"error_ratio_5m", "burn_rate_5m", "error_ratio_1h", "burn_rate_1h", "status": "ok|warning|critical"}`
- `anomaly_count` — requests flagged by the per-tenant EWMA z-score detector

Two opt-in query params: `?history=<hours>` appends persisted snapshots
(p50/p95/error-ratio over time, survives restarts — SQLite-backed) and
`?traces=slow` appends persisted slow/error traces (>50 ms or status ≥ 400).

`GET /debug/synthetic` (same gate) reports the built-in monitor — a
60-second loop hitting `/health` and an API canary with a real JWT:
`{"health":{"status":"up","latency_ms":2,"uptime_pct":100}, …}`. The
canary derives from **your** loaded schema (first resource, a role that
can read it) and probes the first registered tenant; on a fresh install
it reports `"pending"` — `"no tenant registered yet"` — until one
exists, instead of failing. Overrides: `APPXIMO_SYNTHETIC_TENANT` /
`APPXIMO_SYNTHETIC_RESOURCE`; disable the monitor entirely with
`APPXIMO_SYNTHETIC=off`.

## The generated APIs

Full syntax (filters, sort, pagination, GraphQL, error shapes) is in
[AGENTS.md](../AGENTS.md#calling-the-rest-api--exact-syntax); the four
first-call commands are in the [README](../README.md#quick-start-30-s-with-the-image-pull).
The exploration-relevant part — what auth failures actually look like
(captured live, they are deliberately terse):

| You sent | Response |
|---|---|
| no `Authorization` | 401 `{"error":"missing token"}` |
| token for tenant A, `Host` of tenant B | 401 `{"error":"token tenant mismatch"}` |
| valid token, role not in the schema | 403 `{"error":"forbidden"}` |
| `Host` with no subdomain (bare IP / `localhost`) | **500**, empty body — the tenant can't be derived from the Host at all |

Live changes over SSE — subscribe, then write from another terminal:

```bash
curl -N localhost:8080/api/tasks/events \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost"
```
```
: connected

event: create
data: {"id":"397fc252-…","record":{"id":"397fc252-…","status":"open","title":"sse demo"},"resource":"tasks"}
```

Event types are `create` / `update` / `delete`; RBAC field and row rules
are applied per subscriber at delivery. The stream bypasses the response
cache and sends a comment ping every 25 s to keep proxies from reaping it.

GraphQL is `POST /graphql` — remember it **always answers HTTP 200**;
errors live in the body's `errors` array. With
`APPXIMO_ENV=development` the engine also serves the GraphiQL IDE at
`/graphiql` (404 in production — verified both ways).

## OpenAPI — probing the API interactively

The spec is generated from your schema, not handwritten:

```bash
appximo openapi schema.json > openapi.yaml   # OpenAPI 3.0.3
# from the Docker image:
docker compose exec engine appximo openapi /etc/appximo/schema.json > openapi.yaml
```

Load `openapi.yaml` into [Swagger Editor](https://editor.swagger.io) or
[Hoppscotch](https://hoppscotch.io) to browse and fire requests — set
the `Authorization: Bearer` header there, and point the target at a
tenant subdomain (e.g. `http://acme.localhost:8080`) so the Host header
is right. Note that browser-based clients are subject to the no-CORS
limit ([DEPLOY.md](DEPLOY.md#cors--current-status-important-for-spas)) —
desktop clients (Hoppscotch agent, Insomnia, Postman) are not.

An embedded Swagger UI served by the engine itself (`/docs`) is on the
post-launch roadmap — opt-in by env var, **off by default in
production**, same reasoning as GraphiQL.

## The control plane (`:9090`)

| Endpoint | Auth | Does |
|---|---|---|
| `GET /health` | none | `{"status":"ok","port":9090}` |
| `POST /tenants` | `X-Admin-Key` | register a tenant: validates the schema, creates `tenant_<id>` Postgres schema + tables, returns 201 with the tenant record (409 if it exists) |
| `GET /tenants/{id}` | `X-Admin-Key` | the stored record: `{"id","pg_schema","display_name","email","plan","created_at"}` |
| `GET /tenants/{id}/schema` | `X-Admin-Key` | the tenant's stored JSON schema |
| `PUT /tenants/{id}/schema` | `X-Admin-Key` | validate + store a new schema, apply additive DDL → `{"status":"migration_queued"}` |

Plus two operator endpoints on the **data plane** (same key):

- `POST /admin/tenants/{id}/reload` → `{"ok":true,"tenant_id":"acme","reloaded_at":"…"}` —
  evicts the tenant's response cache and warm-reloads its stored schema.
  Covers column-level changes; **adding a resource still needs a process
  restart** (routes/GraphQL are compiled at boot).
- `POST /admin/backup?tenant=acme` — `pg_dump` of that tenant's schema.
  Without `pg_dump` on the host it answers honestly:
  503 `{"error":"pg_dump is not available on this host"}`.

## From your laptop (server install)

The admin surface is reachable but key-gated on `:8080`, and `:9090`
shouldn't be exposed at all — so tunnel:

```bash
ssh -L 8080:localhost:8080 -L 9090:localhost:9090 you@your-server
```

Then, locally:

1. **Trace Explorer in the browser**: `http://localhost:8080/debug/traces?key=<ADMIN_KEY>` — works as-is, no extension needed.
2. **JSON debug / metrics**: `curl -H "X-Admin-Key: …" localhost:8080/debug/tenant/acme` (header-only — the `?key=` form is exclusive to the traces HTML).
3. **The API itself in a browser**: any `*.localhost` name resolves to
   loopback, so `http://acme.localhost:8080/api/tasks` sends the right
   Host header through the tunnel automatically. The `Authorization`
   header is the only thing a bare browser can't add — use a header
   extension (e.g. ModHeader) for it, or stick to curl/Hoppscotch.
4. **Control plane**: `curl -H "X-Admin-Key: …" localhost:9090/tenants/acme`.

Keep `ADMIN_KEY` out of shell history where you can (`set -a; source .env; set +a`
and use `$ADMIN_KEY`).

## What you will NOT find (so you don't go looking)

- **No OTLP/OpenTelemetry export** — observability is Prometheus
  `/metrics` + the internal trace ring + SQLite snapshots. (Span data
  exists internally; an exporter is a roadmap item.)
- **No hosted dashboard.** The Trace Explorer HTML and the JSON APIs are it.
- **No unauthenticated observability** — every `/metrics`, `/debug/*`,
  `/admin/*` call needs `X-Admin-Key`, even on localhost.
- **No tenant list endpoint** — the control plane is get-by-id only;
  enumerate via `public.tenants` in Postgres if you need the full list.
- **pprof** only exists with `APPXIMO_ENV=development`, on its own
  port: `localhost:6060/pprof/` (note: *not* `/debug/pprof/`). In
  production the port doesn't even listen.
