# Deploying Appximo

Three ways to run it. Pick by goal:

| Goal | Path | TLS | Performance |
|------|------|-----|-------------|
| **Try it** in ~9 s | [`docker compose up`](#level-1--quick-try-docker-compose) | none (localhost) | dev/eval only |
| **Production, simple** | [`docker-compose.prod.yml` + Caddy](#level-2--production-simple-composeprod--caddy) | automatic (Let's Encrypt) | very good — its extra layers [measured at sub-ms](#measured-overhead-of-each-layer) |
| **Production, max throughput** | [native binary + dockerized Postgres + reverse proxy](#level-3--maximum-performance-native-binary--dockerized-postgres) | Caddy or nginx | the benchmark config |

Levels 1 and 2 use the published multi-arch image
(`neodevtrix/appximo`, linux/amd64 + linux/arm64 — Apple Silicon works
natively). Level 3 runs the engine binary directly on the host — it is the
stack the [public benchmark](../context-docs/BENCHMARK_PUBLIC.md) numbers come
from.

---

## Level 1 — Quick try (docker compose)

For your laptop or an eval box. **Not for serious production** — no TLS, both
ports on localhost.

Requirements: Docker Desktop (Windows/Mac) or Docker Engine + compose v2 (Linux).

```bash
mkdir appximo && cd appximo
curl -O https://raw.githubusercontent.com/appximo/appximo/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/appximo/appximo/main/.env.example
cp .env.example .env        # edit: JWT_SECRET (≥32 chars), ADMIN_KEY, DB_PASSWORD
docker compose up -d        # pulls the image + postgres
curl localhost:8080/health  # {"status":"ok",...}
```

First API call in four commands (the engine boots with the bundled quickstart
schema — `todo-api`, one `tasks` resource, the same one the README shows;
`acme` here is your first tenant):

```bash
# 0. load your .env values into the shell
set -a; source .env; set +a

# 1. register a tenant — creates its isolated Postgres schema + tables
curl -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme\",\"email\":\"a@acme.com\",\"plan\":\"free\",\"schema\":$(docker compose exec engine cat /etc/appximo/schema.json)}"

# 2. mint a JWT for the "admin" role the schema defines (helper ships in the image)
TOKEN=$(docker compose exec engine appximo token --secret "$JWT_SECRET" --tenant acme --role admin 2>/dev/null | tail -1)

# 3. create a record
curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost" \
  -H "Content-Type: application/json" \
  -d '{"title":"ship the launch","status":"open"}'

# 4. read it back — note curl's -g: the filter brackets need globbing off
curl -g "http://localhost:8080/api/tasks?filter[status][eq]=open&per_page=20" \
  -H "Authorization: Bearer $TOKEN" -H "Host: acme.localhost"
```

> First-run plumbing is automatic: the compose file seeds the control-plane
> tables (`public.tenants` …) into Postgres on the first boot of the data
> volume. If you reuse a pre-existing volume from an older setup, apply
> `migrations/001_control_plane.sql` once by hand.

Tenants are addressed by Host subdomain (`acme.localhost` → schema
`tenant_acme`). To serve **your own** data model, mount your schema over the
baked example:

```yaml
# docker-compose.override.yml
services:
  engine:
    volumes:
      - ./myschema.json:/etc/appximo/schema.json:ro
```

---

## Level 2 — Production, simple (compose.prod + Caddy)

What you need: a VPS (1 vCPU / 1GB is enough to start — the engine idles at
~24MB), a domain, and an `A` record pointing at the VPS. Ports 80/443 open.

> **Performance trade-off, measured:** this path adds Docker bridge networking
> and a TLS proxy hop in front of the engine. We measured both layers
> individually on the benchmark hardware
> ([details below](#measured-overhead-of-each-layer)): the Docker bridge costs
> **~0.05 ms** at the median and the proxy hop **~0.5 ms** — what this path
> adds that we did *not* measure is TLS encryption itself. For most workloads
> that is a non-issue; for the configuration the published benchmark numbers
> come from, see
> [Level 3](#level-3--maximum-performance-native-binary--dockerized-postgres).

```bash
mkdir appximo && cd appximo
curl -O https://raw.githubusercontent.com/appximo/appximo/main/docker-compose.prod.yml
curl --create-dirs -o deploy/Caddyfile https://raw.githubusercontent.com/appximo/appximo/main/deploy/Caddyfile
curl -O https://raw.githubusercontent.com/appximo/appximo/main/.env.example
cp .env.example .env        # set DOMAIN, ACME_EMAIL + real secrets
docker compose -f docker-compose.prod.yml up -d
curl https://$DOMAIN/health
```

The stack:

```
internet ──443──▶ Caddy (TLS automático Let's Encrypt)
                    │ reverse_proxy (Host header preserved → tenant routing)
                    ▼
                  engine:8080      ← no host ports
                    ▼
                  postgres:5432    ← no host ports
```

- **TLS is automatic**: Caddy obtains and renews the Let's Encrypt certificate
  for `$DOMAIN` via the HTTP challenge. No certbot, no cron.
- **The engine and Postgres publish no ports** — only Caddy is reachable.
- **The control plane (:9090) stays internal.** Register tenants from the VPS:

```bash
docker compose -f docker-compose.prod.yml exec engine sh -c \
  'wget -q -O- --header "X-Admin-Key: $ADMIN_KEY" --header "Content-Type: application/json" \
   --post-data "{\"tenant_id\":\"acme\",...,\"schema\":$(cat /etc/appximo/schema.json)}" \
   http://127.0.0.1:9090/tenants'
```

(or SSH-tunnel 9090 to your machine: `ssh -L 9090:localhost:9090 you@vps` after
adding a `ports: ["127.0.0.1:9090:9090"]` mapping — keep it bound to localhost).

### Tenant subdomains

Tenants are routed by Host header: `acme.your-domain.com` → `tenant_acme`.
Caddy passes the Host through by default. The only question is **certificates**
for the subdomains — two options, documented inline in `deploy/Caddyfile`:

| | How | Trade-off |
|---|---|---|
| **A. Explicit list** (default-friendly) | Uncomment the `acme.{$DOMAIN}, beta.{$DOMAIN}` block and list your tenants | Works with stock Caddy; add a line + reload per new tenant |
| **B. Wildcard `*.{$DOMAIN}`** | DNS challenge — needs a Caddy image built with your DNS provider's module (`deploy/Dockerfile.caddy` has a DigitalOcean example) | Any tenant with zero config, but custom Caddy build + DNS API token |

Start with A; move to B when adding tenants becomes routine.

### Operations

- **Upgrade**: `docker compose -f docker-compose.prod.yml pull && docker compose -f docker-compose.prod.yml up -d` (the engine drains in-flight requests on SIGTERM).
- **Pin a version**: replace `neodevtrix/appximo:latest` with `neodevtrix/appximo:<git-sha>` or a `v*` tag (every image is also pushed under its commit SHA).
- **Backups**: `pg_dump` from the db container, or `POST /admin/backup?tenant=ID` if you add `postgresql16-client` to the engine image (see Dockerfile note).
- **Metrics**: `GET /metrics` (Prometheus) is admin-gated with `X-Admin-Key`.

---

## Level 3 — Maximum performance (native binary + dockerized Postgres)

This is the stack behind the published numbers — 2,000 req/s sustained, p50
1.58 ms on a $16/mo 2-vCPU droplet
([BENCHMARK_PUBLIC.md §2](../context-docs/BENCHMARK_PUBLIC.md)). The benchmark
ran the binary directly; this section adds a TLS reverse proxy in front of it
(a small, declared cost the benchmark did not pay).

**The architecture is deliberately hybrid:**

```
internet ──443──▶ Caddy or nginx (TLS, keepalive to upstream)
                    ▼
                  appximo binary, NATIVE on the host  :8080
                    ▼ pool of ~10 persistent connections
                  Postgres in DOCKER, volume-backed     127.0.0.1:5432
```

- **The engine runs native** because it is what receives the thousands of
  requests per second. Measured honestly, the cost of the container layer is
  smaller than folk wisdom suggests — **~54 µs p50 at 500 RPS** for the
  bridge NAT ([numbers below](#measured-overhead-of-each-layer)) — so the
  native choice is less about that hop and more about what it removes at
  rates we *couldn't* measure per-layer: one fewer moving part between the
  NIC and the engine when you push toward the 2,000 RPS ceiling.
- **Postgres runs in Docker** because its traffic pattern doesn't care: the
  engine talks to it over a small pool (~10) of persistent connections, not
  high-throughput connection churn. A native Postgres install adds setup and
  upgrade pain without measurable performance gain here.

### 3.1 Postgres

One file, one command — [`docker-compose.db.yml`](../docker-compose.db.yml)
ships in the repo (Postgres 16 + persistent volume + the control-plane seed +
the exact server settings the benchmark ran with: `max_connections=300`,
`shared_buffers=256MB`, `work_mem=16MB`):

```bash
curl -O https://raw.githubusercontent.com/appximo/appximo/main/docker-compose.db.yml
DB_PASSWORD='a-strong-password' docker compose -f docker-compose.db.yml up -d
```

The port binds to `127.0.0.1` only — Postgres is reachable from this host
exclusively.

### 3.2 The engine binary

Download from [GitHub Releases](https://github.com/appximo/appximo/releases)
(static, no dependencies — built `CGO_ENABLED=0`, same flags as the Docker
image) and verify the checksum:

```bash
VER=v0.1.0   # pick the release you want
curl -fLO "https://github.com/appximo/appximo/releases/download/${VER}/appximo-${VER}-linux-amd64"
curl -fLO "https://github.com/appximo/appximo/releases/download/${VER}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt   # appximo-…-linux-amd64: OK
install -m 0755 "appximo-${VER}-linux-amd64" /usr/local/bin/appximo
appximo version    # appximo v0.1.0 (commit <sha>) — traceable to its tag
```

(Or build it yourself: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o
appximo ./cmd/appximo` from a clone.)

Config: a system user, the schema, and an environment file. **systemd's
`EnvironmentFile` is not a shell** — plain `KEY=value` lines, no `export`, no
quotes needed:

```bash
useradd --system --no-create-home appximo
mkdir -p /etc/appximo
# your schema (or start from the quickstart example in the repo)
cp myschema.json /etc/appximo/schema.json

cat > /etc/appximo/engine.env <<'EOF'
DATABASE_URL=postgres://appximo:a-strong-password@localhost:5432/appximo?sslmode=disable
JWT_SECRET=CHANGE_ME_minimum_32_characters_long_secret
ADMIN_KEY=CHANGE_ME_admin_key
GOMAXPROCS=2
EOF
chmod 600 /etc/appximo/engine.env
```

The systemd unit (`/etc/systemd/system/appximo.service`):

```ini
[Unit]
Description=Appximo engine
Wants=network-online.target
After=network-online.target docker.service

[Service]
User=appximo
Group=appximo
EnvironmentFile=/etc/appximo/engine.env
ExecStart=/usr/local/bin/appximo serve --schema /etc/appximo/schema.json --port 8080
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
LimitNOFILE=65536
# Creates /var/lib/appximo owned by the service user before start — used by the
# observability store (OBS_DB_PATH default) and the file store (APPXIMO_FILES_DIR).
StateDirectory=appximo

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable --now appximo
curl localhost:8080/health   # {"status":"ok","version":"v0.1.0"}
```

On SIGTERM (`systemctl restart`/`stop`) the engine flips `/readyz` to 503,
drains in-flight requests, then exits — a proxy or LB watching `/readyz`
drops it from rotation before connections die.

**The control plane (`:9090`) listens on this host too. Never proxy it, never
open it in the firewall.** Register tenants from the box itself
(`curl -H "X-Admin-Key: …" http://127.0.0.1:9090/tenants …`) or over an SSH
tunnel (`ssh -L 9090:localhost:9090 you@server`). Only 80/443 (the proxy) —
and your SSH port — should be reachable from outside; **not 8080** either,
unless you are intentionally serving plain HTTP.

### 3.3 Reverse proxy with upstream keepalive

The detail that matters for throughput: **keep-alive connections between the
proxy and the engine**. Without them the proxy opens a fresh TCP connection
per request — at 2,000 req/s that is 2,000 handshakes/s of pure overhead and
ephemeral-port churn. With keepalive, requests ride a warm pool.

**Option A — Caddy** (simplest: automatic TLS, and upstream keepalive is on
by default — 2-minute idle timeout, 32 idle connections per host). One file,
`/etc/caddy/Caddyfile`:

```caddy
api.example.com, acme.api.example.com {
	reverse_proxy 127.0.0.1:8080 {
		transport http {
			keepalive 2m
			keepalive_idle_conns_per_host 128
		}
	}
}
```

The `transport http` block just raises the idle pool for high-RPS use; the
defaults already keep connections alive. Caddy passes the Host header through
unchanged (tenant routing depends on it) and auto-detects streaming responses,
so SSE (`/api/*/events`) works without extra config. For wildcard tenant
certificates (`*.api.example.com`) you need the DNS challenge — same story as
Level 2, see `deploy/Caddyfile` Option B.

**Option B — nginx** (bring your own certificates, e.g. certbot). The
`keepalive` directive lives in the `upstream` block, and — on nginx older than
1.29.7 — requires HTTP/1.1 + a cleared `Connection` header toward the
upstream, exactly as below (since 1.29.7 those two are the defaults; keeping
them is harmless):

```nginx
upstream appximo {
    server 127.0.0.1:8080;
    keepalive 64;                      # idle connections kept warm to the engine
}

server {
    listen 443 ssl;
    http2 on;
    server_name api.example.com *.api.example.com;   # wildcard cert via DNS-01

    # ssl_certificate     /etc/letsencrypt/live/.../fullchain.pem;
    # ssl_certificate_key /etc/letsencrypt/live/.../privkey.pem;

    location / {
        proxy_pass http://appximo;
        proxy_http_version 1.1;        # ┐ required for upstream keepalive
        proxy_set_header Connection ""; # ┘ (defaults since nginx 1.29.7)
        proxy_set_header Host $host;   # tenant routing — do not drop this
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # SSE streams: don't buffer, don't time out at the default 60s
    location ~ ^/api/[a-z0-9-]+/events$ {
        proxy_pass http://appximo;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_buffering off;
        proxy_read_timeout 1h;
    }
}
```

Two things both configs share, because the engine depends on them: the **Host
header reaches the engine unmodified** (it is the tenant selector), and
**`:9090` appears nowhere**.

### What this level gives you

These are the conditions under which we measured 2,000 RPS sustained with
p50 1.58 ms and 0 errors (see
[BENCHMARK_PUBLIC.md §2](../context-docs/BENCHMARK_PUBLIC.md) for the full
methodology — the benchmark hit the binary directly; the TLS proxy in front
adds the measured ~0.5 ms hop below, plus the unmeasured cost of TLS
itself). The trade against Level 2: you manage a systemd unit and (with
nginx) certificates yourself, instead of `docker compose up`.

---

## Background worker (Class-2 outbox consumer)

The engine writes durable jobs to `public.outbox` (a resource opts in with
`"events": [...]`, or a custom handler calls `ctx.Enqueue`). A **separate
process** — the worker — drains that table and runs each event through a consumer.
It is shipped in the **same image** as the engine (run via the `worker`
entrypoint keyword), but it is a distinct service: it never touches the engine's
request hot path, and it survives engine restarts.

**Compose (Levels 1 & 2): it's already wired.** Both `docker-compose.yml` and
`docker-compose.prod.yml` define a `worker` service that comes up with
`docker compose up -d`. It defaults to `echo` mode (log + ack — no engine calls,
safe out of the box). Switch modes in `.env`:

```bash
APPXIMO_WORKER_MODE=xlsx   # echo (default) | writeback | xlsx | email
```

| Env var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | — | the SAME Postgres as the engine (the outbox lives in `public`) |
| `JWT_SECRET` | — | **must equal the engine's** — the worker mints a short-lived, scoped service JWT to write results back through the engine API (writeback/xlsx; email needs no JWT) |
| `APPXIMO_WORKER_MODE` | `echo` | `echo` (log+ack) · `writeback` (demo status PATCH) · `xlsx` (FileJob consumer) · `email` (transactional email) |
| `APPXIMO_ENGINE_URL` | `http://engine:8080` | engine data-plane URL the worker calls for write-back |
| `APPXIMO_WORKER_ROLE` | `service_worker` | scoped RBAC role the worker assumes — **never admin**; must exist in your schema (writeback/xlsx only) |
| `APPXIMO_TENANT_DOMAIN` | `localhost` | internal Host-header suffix (`{tenant}.{suffix}`); the engine reads the **subdomain**, so `localhost` is correct even in prod — it is not a DNS name |
| `APPXIMO_FILES_DIR` | `/var/lib/appximo/files` | CAS root for `xlsx` mode — the SAME volume the engine mounts |

**Email mode (`email`) — external SMTP.** The email consumer sends transactional
mail (verification, welcome, reset) asynchronously: a handler enqueues an
`email.send` outbox event in its transaction, the user gets their HTTP response
immediately, and the worker renders an HTML template and sends it via an
**external SMTP provider** — Brevo, Resend, Mailgun, SES… (self-hosting SMTP is
not supported; deliverability/SPF/DKIM is the provider's job). It writes nothing
back to the engine, so it needs **no `JWT_SECRET`** — only:

| Env var | Default | Meaning |
|---|---|---|
| `SMTP_HOST` | — | provider host, e.g. `smtp-relay.brevo.com` |
| `SMTP_PORT` | `587` | STARTTLS submission port |
| `SMTP_FROM` | — | header/envelope From, e.g. `My App <no-reply@myapp.com>` |
| `SMTP_USER` / `SMTP_PASS` | — | provider credentials (omit for an open/test relay) |
| `APPXIMO_EMAIL_TOPIC` | `email.send` | outbox topic the consumer drains |

The event payload is `{"to","template","subject?","data":{…}}`; `data` fills the
template variables (`html/template`, auto-escaped). Built-in demo templates:
`verification`, `welcome`. Idempotency: delivery is at-least-once, so a worker
crash between the provider's `250 OK` and the outbox `COMMIT` resends on
redelivery — for transactional email a rare duplicate is the accepted trade-off
(vs. dropping the mail), and every message carries a **deterministic Message-ID**
(from the outbox row id) so a provider can dedupe.

```yaml
# An email worker (compose). Coexisting with an xlsx worker? Read the next section.
  worker-email:
    image: neodevtrix/appximo:latest
    command: ["worker"]
    environment:
      DATABASE_URL: postgres://appximo:${DB_PASSWORD}@db:5432/appximo?sslmode=disable
      APPXIMO_WORKER_MODE: email
      SMTP_HOST: ${SMTP_HOST}
      SMTP_PORT: ${SMTP_PORT:-587}
      SMTP_FROM: ${SMTP_FROM}
      SMTP_USER: ${SMTP_USER}
      SMTP_PASS: ${SMTP_PASS}
    depends_on:
      db: { condition: service_healthy }
    restart: unless-stopped
```

**Shared file volume (xlsx mode).** The XLSX consumer resolves a job's `file_ref`
to a blob via the content-addressable store (`pkg/files`). The compose files mount
the **same** named volume `files_data` on both `engine` and `worker` at
`/var/lib/appximo/files`, so the worker's `VFS.Get` reads exactly what the
engine's `VFS.Put` wrote. Do **not** give them separate volumes — they must share
one CAS.

**Horizontal scaling — free.** The worker claims rows with
`SELECT … FOR UPDATE SKIP LOCKED`, so N workers claim **disjoint** rows and never
double-process (delivery is at-least-once; consumers are idempotent). Scale out
with:

```bash
docker compose up -d --scale worker=3
```

**Multiple event types — one dispatching worker, not many single-mode workers.**
A single-mode worker (`xlsx`, `email`, …) **acks (and thus drops) topics it does
not own**. Because every worker drains the SAME outbox under `SKIP LOCKED`,
running an `xlsx` worker *and* an `email` worker against one database is unsafe:
whichever claims a row first acks it, so the other consumer never sees its events
— silent loss. Two correct patterns:

- **Single event type:** run ONE mode (e.g. only `xlsx`), scaled to N replicas.
  Safe because that worker is the sole consumer of the outbox.
- **Multiple event types (recommended):** run ONE *dispatching* worker that
  handles all of them, scaled to N identical replicas. Compose the consumers
  behind a `consumers.Router` (topic → consumer) in a small custom `main.go`
  (the ADR-016 library model — import `appximo`/`pkg/consumers`, build your own
  worker binary):

  ```go
  router := consumers.NewRouter(log).
      Handle("email.send", emailProc).
      HandlePrefix("filejobs.", xlsxProc)
  w := worker.New(connect, router, worker.Config{}, log)
  ```

  Every row reaches its real consumer; `SKIP LOCKED` still spreads rows across the
  N replicas with no double-processing. (A future per-worker topic-claim filter
  would let specialized workers coexist and scale independently; not yet built.)

**Native (Level 3).** Run the worker as a second systemd unit alongside the
engine — same binary set, built `CGO_ENABLED=0` (`scripts/build-worker.sh`, or
`make worker`):

```ini
# /etc/systemd/system/appximo-worker.service
[Unit]
Description=Appximo outbox worker
After=network-online.target docker.service appximo.service

[Service]
User=appximo
Group=appximo
EnvironmentFile=/etc/appximo/engine.env      # same DATABASE_URL + JWT_SECRET
Environment=APPXIMO_WORKER_MODE=echo
Environment=APPXIMO_ENGINE_URL=http://127.0.0.1:8080
ExecStart=/usr/local/bin/appximo-worker
Restart=on-failure
RestartSec=3
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

On SIGTERM (`systemctl stop`) the worker finishes its current batch, closes its
DB connections, and exits cleanly. It reconnects on its own (capped backoff) if
Postgres blips, so `Restart=on-failure` is a backstop, not the primary recovery.

---

## Observability store (`OBS_DB_PATH`)

The engine persists its trace + snapshot history to one SQLite file (modernc, no
CGO — full map in [docs/PERSISTENCE.md](PERSISTENCE.md)). It is best-effort: a bad
path is logged and degrades to disabled, **never a boot failure**.

- **The default is persistent.** With `OBS_DB_PATH` unset the store lives at
  `/var/lib/appximo/obs.db` (the same root as the file store), so the history
  **survives a process or container restart out of the box**. The parent directory
  is created on open.
- **Ephemeral-path warning.** If the resolved path is under `/tmp` or a tmpfs, the
  engine logs a boot WARNING (`observability store at <path> is ephemeral …`), so
  an accidental `/tmp` in production is visible. Using `/tmp` in dev is fine — the
  warning is informational, not a block.
- **An unwritable path is safe.** If the directory cannot be created or written
  (e.g. a stricter user/permission setup), the store falls back to an ephemeral
  temp file and logs a WARNING. Observability is reduced; the engine keeps serving.

**Docker — persist it with a volume.** Inside a container `/var/lib/appximo/obs.db`
would live in the ephemeral writable layer and vanish on `docker rm`. The shipped
compose files therefore put it on a dedicated named volume and point `OBS_DB_PATH`
at it (engine only — the worker does not write observability):

```yaml
services:
  engine:
    environment:
      OBS_DB_PATH: /var/lib/appximo/obs/obs.db
    volumes:
      - obs_data:/var/lib/appximo/obs   # history survives the container
volumes:
  obs_data:
```

The image pre-creates `/var/lib/appximo/obs` owned by the runtime user, so the
named volume inherits that ownership on first init — the same pattern as
`files_data`.

**Native (systemd).** The Level-3 unit above adds `StateDirectory=appximo`, which
makes systemd create `/var/lib/appximo` (owned by the service user) before start
— covering both the observability store and the file store. To put the history on a
different disk, set `OBS_DB_PATH=/your/persistent/path/obs.db` in
`/etc/appximo/engine.env`.

Retention is automatic (a 7-day window plus a 50 000-row cap on slow traces), so
the file stays bounded (tens of MB in normal operation).

---

## Measured overhead of each layer

The claims above are measurements, not folklore. On the same 2-vCPU droplet
the public benchmark used, same engine binary, same env, same PostgreSQL and
dataset in every variant — the only thing that changes per row is the layer
under test. Load: 500 RPS × 30 s × 10 runs per configuration, external
loader on a separate box, warmup discarded, 20 s cooldowns
(`scripts/bench-protocol.sh`); verdicts from pooled Mann-Whitney + bootstrap
CI with a 0.5 ms practical-significance threshold (the S42 engine behind
[BENCHMARK_PUBLIC.md](../context-docs/BENCHMARK_PUBLIC.md)).

Baseline (native binary, direct): p50 **1.30 ms** / p95 1.66 ms across the
10 runs (medians of per-run values; the ~1.2 ms network RTT floor between
loader and SUT is included, as in the public benchmark).

| Layer added vs native binary | Δ p50 | Δ p95 | Verdict |
|---|---|---|---|
| **Docker bridge networking** (same binary in a container, published port) | **+0.05 ms** — CI95 [+0.053, +0.056], p≈0 | +0.06 ms | Statistically detectable, practically negligible (≪ 0.5 ms threshold). Containerizing the engine costs ~54 µs per request at this rate. |
| **Caddy reverse proxy in front** (HTTP, upstream keepalive per the snippet above) | **+0.48 ms** — CI95 [+0.479, +0.482], p≈0 | +1.02 ms | Real but sub-millisecond, just under the 0.5 ms practical bar. This is the proxy hop **without TLS** — encryption adds its own (keepalive-amortized) cost on top. |

Honest readings of those numbers:

- **The container itself is nearly free at this rate.** The "native binary"
  recommendation buys ~54 µs at 500 RPS — its real value is removing a
  variable when you push toward the multi-thousand-RPS ceiling, where we
  did not measure per-layer.
- **The proxy hop is the bigger of the two costs** (~10× the bridge), and
  it roughly doubles the p95. Still sub-millisecond with keepalive — the
  config shown above is the one measured.
- Host networking (`network_mode: host`) and the TLS-termination delta were
  **not measured** in this pass; no numbers are claimed for them.
- Raw data: [`benchmarks/data/deploy-overhead-runs.csv`](../benchmarks/data/deploy-overhead-runs.csv)
  — all 30 runs, including 2 where the loader missed its k6 schedule
  (`dropped > 0`, inflated tails); nothing was deleted, and both verdict
  methods (pooled MWU, median-of-run-medians) are robust to them.

---

## Hardening checklist (any level)

- **Control plane `:9090` — never the internet.** Localhost / internal
  network / SSH tunnel only. Its safety model is *unreachable*, not
  unguessable.
- **Never expose the Docker daemon API (`2375`/`2376`).** If you enable
  remote Docker access, port 2375 is **unauthenticated root** on the host
  (2376 is only safe with mutual TLS configured). The compose paths here
  never need it — keep both ports firewalled, and audit old firewall rules:
  an ALLOW rule with no process behind it is a standing invitation, not
  harmless.
- **Default-deny inbound firewall.** What should be reachable from outside:
  80/443 (the proxy) and your SSH port. Not 8080 (unless intentionally
  serving plain HTTP), not 9090, not 5432.
- **Strong `ADMIN_KEY` / `JWT_SECRET`** (≥32 chars, `openssl rand -hex 32`)
  — they gate the entire admin surface and token forgery respectively.
- **Keep secrets out of git and shell history**: `.env` is gitignored;
  `set -a; source .env; set +a` instead of pasting keys into commands.

---

### CORS — configurable (for browser SPAs on another origin)

The engine ships **configurable CORS**, disabled by default. With no origins
configured it emits **no `Access-Control-Allow-*` header** (the safe default —
server-to-server / mobile / curl are unaffected, CORS is a browser concept). An
operator opts in by listing the browser origins allowed to call the API:

```bash
APPXIMO_CORS_ORIGINS="https://app.example.com,https://admin.example.com"
# optional:
APPXIMO_CORS_METHODS="GET,POST,PUT,PATCH,DELETE,OPTIONS"   # default shown
APPXIMO_CORS_HEADERS="Authorization,Content-Type"          # default shown
APPXIMO_CORS_EXPOSE_HEADERS="X-Cache"                      # default: none
APPXIMO_CORS_CREDENTIALS=true                              # cookies/Authorization; default false
APPXIMO_CORS_MAX_AGE=600                                   # preflight cache seconds; default 600
```

Behaviour:

- **Scope**: CORS applies ONLY to the public data-plane routes a browser consumes
  — `/api/*`, `/auth/*`, `/graphql`, `/openapi*`. The control plane (`:9090`),
  `/admin`, `/metrics` and `/debug` are operation surfaces (same-origin or machine
  callers) and are **never** given cross-origin access.
- **Preflight**: `OPTIONS` requests are answered directly (`204`) with the CORS
  headers, before auth runs — a preflight carries no credentials, so it never 401s.
- **Origins**: an exact allowlist, or the single value `"*"` for any origin. With
  `APPXIMO_CORS_CREDENTIALS=true`, a `"*"` allowlist **reflects** the request
  origin (the Fetch spec forbids `*` with credentials). A disallowed origin gets
  **no** `Access-Control-Allow-Origin` (the browser blocks the response).
- **Cost**: the middleware runs only when an `Origin` header is present and the
  path is in scope, and is wired only when origins are configured — measured
  `no_change` on the read and write hot paths (group Mann-Whitney, 0.5 ms gate).

You can still serve the SPA **same-origin** as the API (Caddy proxying both) and
skip CORS entirely — that remains the simplest setup.
