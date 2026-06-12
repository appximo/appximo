# Deploying Appitools

Three ways to run it. Pick by goal:

| Goal | Path | TLS | Performance |
|------|------|-----|-------------|
| **Try it** in ~9 s | [`docker compose up`](#level-1--quick-try-docker-compose) | none (localhost) | dev/eval only |
| **Production, simple** | [`docker-compose.prod.yml` + Caddy](#level-2--production-simple-composeprod--caddy) | automatic (Let's Encrypt) | very good — its extra layers [measured at sub-ms](#measured-overhead-of-each-layer) |
| **Production, max throughput** | [native binary + dockerized Postgres + reverse proxy](#level-3--maximum-performance-native-binary--dockerized-postgres) | Caddy or nginx | the benchmark config |

Levels 1 and 2 use the published multi-arch image
(`neodevtrix/appitools-engine`, linux/amd64 + linux/arm64 — Apple Silicon works
natively). Level 3 runs the engine binary directly on the host — it is the
stack the [public benchmark](../context-docs/BENCHMARK_PUBLIC.md) numbers come
from.

---

## Level 1 — Quick try (docker compose)

For your laptop or an eval box. **Not for serious production** — no TLS, both
ports on localhost.

Requirements: Docker Desktop (Windows/Mac) or Docker Engine + compose v2 (Linux).

```bash
mkdir appitools && cd appitools
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/docker-compose.yml
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/.env.example
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
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme\",\"email\":\"a@acme.com\",\"plan\":\"free\",\"schema\":$(docker compose exec engine cat /etc/appitools/schema.json)}"

# 2. mint a JWT for the "admin" role the schema defines (helper ships in the image)
TOKEN=$(docker compose exec engine appitools token --secret "$JWT_SECRET" --tenant acme --role admin 2>/dev/null | tail -1)

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
      - ./myschema.json:/etc/appitools/schema.json:ro
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
mkdir appitools && cd appitools
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/docker-compose.prod.yml
curl --create-dirs -o deploy/Caddyfile https://raw.githubusercontent.com/miguel09acosta/appitools/main/deploy/Caddyfile
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/.env.example
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
   --post-data "{\"tenant_id\":\"acme\",...,\"schema\":$(cat /etc/appitools/schema.json)}" \
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
- **Pin a version**: replace `neodevtrix/appitools-engine:latest` with `neodevtrix/appitools-engine:<git-sha>` or a `v*` tag (every image is also pushed under its commit SHA).
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
                  appitools binary, NATIVE on the host  :8080
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
curl -O https://raw.githubusercontent.com/miguel09acosta/appitools/main/docker-compose.db.yml
DB_PASSWORD='a-strong-password' docker compose -f docker-compose.db.yml up -d
```

The port binds to `127.0.0.1` only — Postgres is reachable from this host
exclusively.

### 3.2 The engine binary

Download from [GitHub Releases](https://github.com/miguel09acosta/appitools/releases)
(static, no dependencies — built `CGO_ENABLED=0`, same flags as the Docker
image) and verify the checksum:

```bash
VER=v0.1.0   # pick the release you want
curl -fLO "https://github.com/miguel09acosta/appitools/releases/download/${VER}/appitools-${VER}-linux-amd64"
curl -fLO "https://github.com/miguel09acosta/appitools/releases/download/${VER}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt   # appitools-…-linux-amd64: OK
install -m 0755 "appitools-${VER}-linux-amd64" /usr/local/bin/appitools
appitools version    # appitools v0.1.0 (commit <sha>) — traceable to its tag
```

(Or build it yourself: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o
appitools ./cmd/appitools` from a clone.)

Config: a system user, the schema, and an environment file. **systemd's
`EnvironmentFile` is not a shell** — plain `KEY=value` lines, no `export`, no
quotes needed:

```bash
useradd --system --no-create-home appitools
mkdir -p /etc/appitools
# your schema (or start from the quickstart example in the repo)
cp myschema.json /etc/appitools/schema.json

cat > /etc/appitools/engine.env <<'EOF'
DATABASE_URL=postgres://appitools:a-strong-password@localhost:5432/appitools?sslmode=disable
JWT_SECRET=CHANGE_ME_minimum_32_characters_long_secret
ADMIN_KEY=CHANGE_ME_admin_key
GOMAXPROCS=2
EOF
chmod 600 /etc/appitools/engine.env
```

The systemd unit (`/etc/systemd/system/appitools.service`):

```ini
[Unit]
Description=Appitools engine
Wants=network-online.target
After=network-online.target docker.service

[Service]
User=appitools
Group=appitools
EnvironmentFile=/etc/appitools/engine.env
ExecStart=/usr/local/bin/appitools serve --schema /etc/appitools/schema.json --port 8080
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload
systemctl enable --now appitools
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
upstream appitools {
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
        proxy_pass http://appitools;
        proxy_http_version 1.1;        # ┐ required for upstream keepalive
        proxy_set_header Connection ""; # ┘ (defaults since nginx 1.29.7)
        proxy_set_header Host $host;   # tenant routing — do not drop this
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # SSE streams: don't buffer, don't time out at the default 60s
    location ~ ^/api/[a-z0-9-]+/events$ {
        proxy_pass http://appitools;
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

### CORS — current status (important for SPAs)

The engine currently ships **no CORS middleware**: it never emits
`Access-Control-Allow-*` headers. Practical consequences:

- Server-to-server, mobile apps, curl: **unaffected** (CORS is a browser thing).
- A browser SPA served from a **different origin** (e.g. `app.example.com`
  calling `api.example.com`): the browser will block the responses — preflighted
  requests (`POST`/`PUT`/`PATCH`/`DELETE` with JSON, or any request carrying
  `Authorization`) fail at the `OPTIONS` preflight.

Workaround today: serve the SPA from the **same origin** as the API (Caddy can
do both: add a `handle /app/*` block, or a separate subdomain that proxies both
to the right place — same-origin means scheme+host+port all match). Native CORS
configuration in the engine is planned as a dedicated session (engine changes go
through the measurement pipeline).
