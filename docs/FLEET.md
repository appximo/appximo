# `appitools fleet` — one server, N distinct apps

The fleet runs **N different APIs (different schemas) on one server**, from one
`fleet.json`, in either of two runtimes:

- **`fleet run`** — multi-process (Option A, MT-STRUCT-S1): one engine process
  per app, supervised, behind a Host-routing reverse proxy. Total isolation;
  ~25–50 MB PSS per app. The isolation escape hatch.
- **`fleet serve`** — in-process (Option B, MT-STRUCT-S3): N apps **compiled in
  ONE process**, dispatched by Host through the lock-free registry. ~1 MB of
  compiled surface per app (2 apps measured at 88 MB RSS total, vs ~154 MB as
  two processes); scales to many apps on one box. Security-reviewed cross-app
  isolation (see below).

Same manifest, same taxonomy, same per-app secret rules — pick per deployment,
or pin a noisy app to its own process (`run`) while the rest co-locate (`serve`).
Architecture + measured numbers: [docs/design/MT-STRUCT.md](design/MT-STRUCT.md).

**The engine's hot path is untouched.** Each app is exactly today's engine —
same middleware chain, same compiled routes, same benchmark. The only engine
change S1 made is configuration: the control-plane port (was `:9090` hardcoded)
and the dev pprof port (was `:6060`) are now parameterizable, so N engines can
coexist. No `bench-protocol` was needed — nothing on the request path changed.

Taxonomy (from the design doc): an **app** is one schema compiled into one API
surface; a **tenant** is an isolated data instance *inside* an app; the
**fleet** is the set of apps on one server. A request resolves the app first
(Host → proxy) and the tenant second (subdomain → engine middleware):
`acme.crm.example.com` → app `crm`, tenant `acme`.

## Quick start

`fleet.json`:

```json
{
  "listen": ":8080",
  "status_addr": "127.0.0.1:9601",
  "data_dir": "/var/lib/appitools/fleet",
  "apps": [
    {
      "name": "crm",
      "schema": "schemas/crm.json",
      "domains": ["crm.example.com"],
      "env": {
        "DATABASE_URL": "postgres://user:pass@localhost:5432/app_crm",
        "JWT_SECRET": "crm-only-secret-at-least-32-characters",
        "ADMIN_KEY": "crm-admin-key"
      }
    },
    {
      "name": "shop",
      "schema": "schemas/shop.json",
      "domains": ["shop.example.com"],
      "env_file": "secrets/shop.env"
    }
  ]
}
```

```bash
appitools fleet run --config fleet.json     # spawns one engine per app + proxy
appitools fleet status                      # APP PID HEALTH PORT RESTARTS UPTIME DOMAINS
```

The proxy serves on `listen`; each app's data/control ports are auto-allocated
internal ports (pin them with `port` / `control_port` if you prefer). Relative
`schema` / `env_file` paths resolve against the manifest's directory.

## The manifest, precisely

| Key | Meaning |
|---|---|
| `listen` | proxy's public address (default `:8080`) |
| `status_addr` | fleet status/control API (default `127.0.0.1:9601`) — **internal**, like the engine control plane; never expose it |
| `data_dir` | per-app state root (default `/var/lib/appitools/fleet`): `<data_dir>/<app>/obs.db`, `…/files`, `…/backups`, plus `logs/<app>.log` — assigned only when the app's env doesn't set `OBS_DB_PATH` / `APPITOOLS_FILES_DIR` / `BACKUP_DIR` (apps must not share these) |
| `apps[].name` | `^[a-z][a-z0-9_-]*$`, unique |
| `apps[].schema` | the app's schema JSON (the engine's `--schema`) |
| `apps[].domains` | hostnames routed to this app; a request Host matching a domain **or any subdomain of it** routes here (longest domain wins) — subdomain labels stay free for tenant resolution |
| `apps[].port` / `control_port` | pin internal ports; `0`/absent auto-allocates |
| `apps[].env_file` / `env` | the app's environment; `env` overrides `env_file`. `DATABASE_URL`, `JWT_SECRET`, `ADMIN_KEY` are **required** per app |

Validation is strict and load-fails-loud, like the engine's schema validation:
unknown manifest keys, duplicate names/domains, a missing schema file or a
missing required env var reject the manifest with an actionable error.

**Per-app secrets are enforced.** Two apps sharing a `JWT_SECRET` is a hard
error: a shared secret would let a token minted for one app *verify* on the
other (role names colliding would then authorize it). One app = one secret =
tokens valid only there — verified live: a `crm` admin token on the `shop`
domain is a `401`.

**One database per app.** Sharing a `DATABASE_URL` means sharing
`public.tenants`/`outbox` and colliding `tenant_<id>` schemas across apps — the
manifest warns loudly. Give each app its own database; the fleet **provisions
it automatically**: before spawning an app it applies the canonical
control-plane DDL (`migrations/001_control_plane.sql`, embedded via the
`migrations` Go package — the same file docker-compose's initdb applies) to the
app's database, idempotently. A fresh, empty database works out of the box.

## The supervisor

- **Restart-on-exit only, never on unhealthy.** This is the reconciliation
  contract with the engine's self-restart (UI-F4-S2): a self-restart re-execs
  with `syscall.Exec` — **same PID, no process exit** — so the supervisor sees
  nothing but a few seconds of `/readyz` 503 ("draining"). Restarting on bad
  health would kill healthy self-restarts mid-drain; restarting on exit
  composes with them for free. Verified live: a deploy + self-restart of one
  app kept its PID and its supervisor restart-count unchanged, while a
  `kill -9` was respawned in ~1 s.
- Crash restarts back off exponentially (1 s → 30 s cap, reset after 60 s of
  healthy uptime). One app crashing/restarting never touches the others
  (separate processes — verified live: the other apps' PIDs and uptimes were
  uninterrupted, requests kept answering 200 during the outage).
- Graceful stop: SIGTERM → the engine's own drain (`/readyz`→503, in-flight
  finish) → SIGKILL only after 15 s.
- Per-app engine logs: `<data_dir>/logs/<app>.log`.

## The proxy

A single-binary `httputil.ReverseProxy` (MVP choice: autonomous, no external
dependency, coherent with the one-static-binary ethos). It routes by Host and
does **nothing else** — pure transport: auth, RBAC, tenancy, validation and
rate limiting all happen in the destination engine, exactly as without a proxy.
The inbound `Host` header is forwarded verbatim (it carries the tenant);
`FlushInterval: -1` keeps SSE streaming; an app that is down answers a clean
`502 {"error":"app X is not available"}`; an unmatched domain a clean `404
{"error":"unknown app domain"}`.

**TLS/production**: the MVP proxy speaks plain HTTP. For public TLS put the
battle-tested front (Caddy/nginx, as in docs/DEPLOY.md) in front of `listen`
with the same wildcard/domain table — the fleet's routing contract (Host →
app) is unchanged under it. A Caddy config generator is a natural future
increment if fleets grow.

## Status / control API (internal)

```
GET  /fleet/status                → {"apps":[{name,pid,health,port,restarts,uptime_s,domains,…}]}
POST /fleet/apps/{name}/stop      → graceful stop; the app STAYS stopped (no auto-restart)
POST /fleet/apps/{name}/start     → start a stopped app
POST /fleet/apps/{name}/restart   → deliberate stop+start of ONE app
```

`appitools fleet status` renders the table. Default bind is loopback; treat it
like `:9090`.

## `fleet serve` — the in-process runtime (MT-STRUCT-S3)

```bash
appitools fleet serve --config fleet.json   # N apps, ONE process, one listener
```

Each app is a full engine instance inside the process: its own schema-compiled
router + GraphQL + OpenAPI, its own pgx pool (its own database), its own
response cache, SSE hub, rate limiter, observability stack and control-plane
listener — with the middleware chain closed over **its** JWT secret, RBAC
policy and admin key. The registry resolves the app from the Host **before**
any auth runs, so a token is only ever validated with the resolved app's
secret.

**Cross-app isolation (security-reviewed, 18-vector live matrix — zero
leakage):** a JWT from app X on app Y's domain is a 401 (REST and GraphQL, even
with X's caches hot); RBAC roles are per app (deny-by-default); data, response
cache, SSE streams, admin keys, control planes and signed file URLs never
cross apps — even for the same resource name and the same tenant id in both.
The process-level claims cache is keyed by (secret, token) precisely so one
app's validation can never short-circuit another's.

**Semantics that differ from `fleet run`:**

- **Unmatched Host → clean `404 {"error":"unknown app domain"}`** (never an
  arbitrary app); `/healthz`, `/readyz`, `/health` on a bare Host stay
  process-level probes.
- **Deploy + engine restart** (`POST /admin/engine/schema` on an app) persists
  that app's boot schema and gracefully relaunches the **whole process** (all
  apps, ~6 s). Per-app hot-swap without a process restart is the S4 increment.
- **Per-app env is limited to what maps into engine Config**: `DATABASE_URL`,
  `JWT_SECRET`, `ADMIN_KEY`, `OBS_DB_PATH`, `APPITOOLS_FILES_DIR`,
  `APPITOOLS_AUTH_SIGNUP_ROLE`, `APPITOOLS_ENV`. Any other manifest env key is
  process-wide in this runtime and is **loudly warned** at boot — if an app
  needs, say, its own OAuth providers or CORS origins today, run it under
  `fleet run` (full env isolation) instead.
- **No supervisor/status API** (nothing to supervise — one process; a crash is
  everyone's crash, which is exactly the blast-radius trade the design doc
  documents; pin risky apps to their own process with `fleet run`).
- **Pools**: each app opens its own pgx pool (`DB_MAX_CONNS` applies per app) —
  budget N × pool size against your Postgres `max_connections`.

## What S1 deliberately does not do

- **No federated single admin panel** — each app keeps its own `/admin` (reachable
  through its domain: `crm.example.com/admin`). One panel over N apps is the
  MT-STRUCT Stage-5 homologation work.
- **No TLS in the MVP proxy** (front it with Caddy/nginx — above).
- **No cross-app anything**: no shared users, tokens, files or observability.
  That isolation is the point.
- Memory economics: each app costs a full engine process (~25–50 MB PSS
  measured — MT-STRUCT §3.1), which is why Option B (in-process, ~1 MB/app)
  remains the target for large fleets; this fleet is the shippable now + the
  future isolation valve.
