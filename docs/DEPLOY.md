# Deploying Appitools

Two paths, both on the published multi-arch image (`neodevtrix/appitools-engine`,
linux/amd64 + linux/arm64 — Apple Silicon works natively):

1. **Laptop / dev** — HTTP on localhost, one command. → [Quickstart](#quickstart-laptop--dev)
2. **Production** — a VPS + your domain, automatic TLS, tenant subdomains. → [Production](#production-vps--domain--tls)

---

## Quickstart (laptop / dev)

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

## Production (VPS + domain + TLS)

What you need: a VPS (1 vCPU / 1GB is enough to start — the engine idles at
~24MB), a domain, and an `A` record pointing at the VPS. Ports 80/443 open.

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
