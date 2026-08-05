# Running Appximo in production

The official path: from an **empty Ubuntu/Debian VPS to a live HTTPS API in
minutes**, with one command. The stack is **native PostgreSQL + the engine under
systemd + Caddy** (automatic Let's Encrypt TLS). Docker is a documented
[variant](#docker-variant), not the default.

> **Why not Docker by default?** On a 1 GB VPS — the box this product is designed
> for — the Docker daemon alone resident-sets **300–400 MB**, a third of the
> machine, before your app runs. Native binaries + systemd cost ~0. The engine
> and Caddy are single static binaries; PostgreSQL is one `apt install`. This is
> the same choice PocketBase, Miniflux and Caddy itself make for small-box
> self-hosting.

---

## 1. Quick start

### Option A — the installer (recommended)

One command on a fresh VPS. It installs PostgreSQL and Caddy, generates every
secret, writes the systemd unit + Caddyfile, and brings the API up on HTTPS. The
**only** thing it needs from you is your **domain** (which must already point at
the box) and an **email** for Let's Encrypt.

```bash
# Once public GitHub Releases exist:
curl -fsSL https://raw.githubusercontent.com/appximo/appximo/main/scripts/install.sh \
  | sudo bash -s -- --domain api.example.com --email you@example.com

# Today (no public release yet) — build the binary, copy it up, install with --binary:
./scripts/build-engine.sh /tmp/appximo "$(git rev-parse --short HEAD)" "$(git rev-parse HEAD)"
scp /tmp/appximo you@server:/tmp/appximo
ssh you@server 'sudo bash /path/to/install.sh --domain api.example.com --email you@example.com --binary=/tmp/appximo'
```

Flags: `--domain` `--email` `--binary=PATH` `--schema=PATH` (your model; default
is a `todo-api` starter you replace later) `--port` (internal, default 8090)
`--yes` (non-interactive) `--harden` (ufw + fail2ban + unattended-upgrades)
`--dry-run` (generate configs + print the plan, change nothing).

When it finishes it prints your API URL, the generated `ADMIN_KEY`/`JWT_SECRET`,
and the exact command to register your first tenant.

If DNS for your domain isn't pointing at the box yet, the installer **still
finishes successfully** — the engine is up locally — and prints a warning;
Caddy issues the certificate automatically the moment DNS resolves here and
ports 80/443 are reachable. It never hangs or rolls back on a pending
certificate. Run it again any time (it's idempotent, reuses your secrets);
`--uninstall` reverses it for a clean retry (add `--purge` to also drop the
database). Validated end-to-end on Ubuntu 22.04 and Debian 12, and on a real
2 GB DigitalOcean droplet where it issued a genuine Let's Encrypt certificate
for a public domain and served the full API + `/editor` + `/admin` over HTTPS.

> **If the box already has a firewall** (a `ufw` or a cloud firewall — common on
> DigitalOcean/AWS), open **80 and 443** or the Let's Encrypt HTTP-01 challenge
> can't reach Caddy. `--harden` does this for you (it detects and keeps your SSH
> port first); or manually: `ufw allow 80/tcp && ufw allow 443/tcp` (22 stays).

### Option B — manual (the same steps, by hand)

If you'd rather not run a script, `docs/DEPLOY.md` Level 3 walks the identical
setup step by step (system user, `EnvironmentFile`, systemd unit, Caddy). The
installer is just those steps, scripted and idempotent.

**Prerequisites either way:** a VPS (1 vCPU / 1 GB is enough), a domain with an
`A`/`AAAA` record pointing at it, and ports **80 + 443** reachable (Let's
Encrypt's HTTP challenge needs 80; TLS serves on 443). Keep everything else —
including the engine's own port and the control plane on 9090 — off the internet.

---

## 2. The stack

```
internet ──443──▶ Caddy  (TLS: automatic Let's Encrypt, Host header preserved)
                    │ reverse_proxy 127.0.0.1:8090
                    ▼
                  appximo engine  (systemd: Restart=always, drains on SIGTERM)
                    │ pool of ~10 connections
                    ▼
                  PostgreSQL  (native apt package, localhost only)
```

- **Caddy** terminates TLS and obtains/renews the certificate automatically (no
  certbot, no cron). It passes the `Host` header through unchanged — Appximo'
  tenant routing (`acme.example.com` → `tenant_acme`) depends on it — and
  auto-flushes SSE streams (`/api/*/events`).
- **The engine** runs as a systemd service on an internal port (8090 by default),
  never exposed directly. systemd restarts it on failure; on `systemctl restart`
  it flips `/readyz` to 503 and drains in-flight requests before exiting.
- **PostgreSQL** is the native `apt` package, listening on localhost. The engine
  talks to it over a small persistent pool, so there is no benefit to
  containerizing it and real setup/upgrade cost avoided.

**Measured cost of the layers** — decomposed on a real 2 vCPU / 2 GB box against
a live HTTPS endpoint ([docs/BENCHMARKS.md](BENCHMARKS.md)): the Caddy reverse
proxy adds **+0.71 ms** p50 and TLS a further **+0.26 ms**, so the whole
production stack costs about **+1 ms** over the bare engine. With a million rows
and every request reaching PostgreSQL it sustains **500 req/s** (knee at 750),
and a filtered, sorted, paginated page answers in **4.4 ms** end to end.

**Why `mv` + SIGTERM, not blue/green.** Updates swap the binary atomically and
`systemctl restart`; Caddy retries the upstream during the ~1 s restart and
`/readyz`→503 drains in-flight requests, so no request is lost. For this profile
(a single box, a stateless engine in front of Postgres) that is the state of the
art — `tableflip`/blue-green/socket-handoff add moving parts with nothing to buy.

### 2b. Several apps on one box (`--app`)

One VPS, two ideas is the normal case. Every path the installer writes is
namespaced by an **app name**, so a second app is one flag:

```bash
# first app — unchanged, no flag needed (the name defaults to "appximo")
sudo bash install.sh --domain=tienda.example.com --email=you@example.com --binary=./appximo

# second app on the SAME box, fully separate
sudo bash install.sh --app=vetapp --domain=petfriendly.example.com \
     --email=you@example.com --binary=./vetapp --port=8091
```

What `--app=NAME` namespaces — everything an app owns, so two apps share nothing
but the machine, PostgreSQL and Caddy:

| | default app | `--app=vetapp` |
|---|---|---|
| systemd unit | `appximo.service` | `vetapp.service` |
| service user | `appximo` | `vetapp` |
| config + boot schema | `/etc/appximo/` | `/etc/vetapp/` |
| binary | `/opt/appximo/bin/appximo` | `/opt/vetapp/bin/vetapp` |
| data (files, obs) | `/var/lib/appximo/` | `/var/lib/vetapp/` |
| database + role | `appximo` | `vetapp` |
| secrets (JWT, admin key) | its own | its own — never shared |
| control plane (localhost) | `:9090` | a stable port derived from the name (`--control-port` to pin it) |
| Caddy site | `/etc/caddy/sites/appximo.caddy` | `/etc/caddy/sites/vetapp.caddy` |

**The Caddyfile is never overwritten.** Each app owns one file under
`/etc/caddy/sites/`, and the main `Caddyfile` only carries the global options plus
`import sites/*.caddy`. Installing an app APPENDS a site; removing one removes only
its own file.

**The installer refuses to clobber a live app.** Running it for a *different*
domain without `--app` stops before touching anything and prints the exact
side-by-side command to run instead — the failure mode this replaced was a second
install replacing the first app's unit, secrets and Caddyfile and taking it offline.

The companion scripts take the same flag:

```bash
sudo bash /opt/vetapp/scripts/deploy-update.sh --app=vetapp --binary=/tmp/vetapp
sudo bash /opt/vetapp/scripts/backup.sh --app=vetapp        # its own DB → /var/backups/vetapp
sudo bash install.sh --uninstall --app=vetapp               # removes ONLY vetapp
```

Note the **ports**: the data port is yours to choose (`--port`), and two apps
cannot share one — the pre-flight checks it. The control port is derived from the
app name so re-running the installer always picks the same one; pin it with
`--control-port` if you prefer.

---

## 3. Updates & redeploys

> **Migrating an install from before the rename (Appitools → Appximo, 2026-08):**
> the installer now provisions everything under the `appximo` name — systemd unit
> `appximo.service`, user `appximo`, `/opt/appximo`, `/etc/appximo`. An existing
> server installed as `appitools` keeps working untouched: unit names are local
> to the box, and `deploy-update.sh --app appitools` targets the old unit
> explicitly. To actually migrate the name, run the new installer with
> `--app appximo` alongside, move the data (`pg_dump`/restore or keep the same
> PostgreSQL), flip the domain, then remove the old unit — or simply keep the
> old unit name; nothing in the engine depends on it.

The official flow is **build → copy → atomic swap → restart**, wrapped in
[`scripts/deploy-update.sh`](../scripts/deploy-update.sh) (which also
health-checks and **auto-rolls-back** if the new binary won't come up):

```bash
# on your dev machine
./scripts/build-engine.sh /tmp/appximo "$(git rev-parse --short HEAD)" "$(git rev-parse HEAD)"
scp /tmp/appximo you@server:/tmp/appximo

# on the server (or over ssh)
sudo bash /opt/appximo/scripts/deploy-update.sh --binary=/tmp/appximo
```

It backs up the live binary to `<dir>-rollback/`, renames the new one over it
(atomic — the running process keeps its old inode), `systemctl restart`s, and
polls `/healthz` + `/readyz`. If they don't come green it **restores the backup
and restarts** automatically (verified: a binary that won't boot rolls back and
the old one is serving again in ~1 s). Re-running the **installer** with
`--binary=` does the same swap + restart (and reuses your existing secrets), so
either path is a safe upgrade.

> `deploy-update.sh` and `backup.sh` are placed in `/opt/appximo/scripts/` by
> the installer **when you run it from a checkout** (they sit next to
> `install.sh`). Under `curl | bash` there are no sibling files to copy, so fetch
> them from the repo into that directory when you need them.

**What activates when.** A per-tenant migration (new column) is live immediately.
Anything compiled at boot — new resources, validation rules, GraphQL fields,
hooks, `/docs` — activates on the restart with the new schema. See
[docs/MENTAL_MODEL.md](MENTAL_MODEL.md).

---

## 4. Backups

[`scripts/backup.sh`](../scripts/backup.sh) dumps the whole database — every
tenant's schema **and** the control plane — as one compressed custom-format file,
and rotates old dumps. Wire it into cron:

```cron
# nightly at 03:30, keep 14 days (as root)
30 3 * * *  /opt/appximo/scripts/backup.sh --env-file=/etc/appximo/appximo.env \
            >> /var/log/appximo-backup.log 2>&1
```

**A backup you have never restored is not a backup.** Test the restore:

```bash
createdb appximo_restore_test
pg_restore -d appximo_restore_test /var/backups/appximo/appximo-<stamp>.dump
# inspect, then: dropdb appximo_restore_test
```

Point-in-time recovery (WAL archiving) is a PostgreSQL-level concern beyond this
script; for most single-box deployments nightly `pg_dump` + off-box copy is the
right trade-off. Copy the dumps off the box (another host, object storage) —
a backup on the same disk as the database is one failure away from gone.

---

## 5. Framework mode — your own binary

When you build a custom backend (import `appximo`, register handlers — see
[docs/BACKEND_SPEC_LLM.md](BACKEND_SPEC_LLM.md)), **the production path is
identical** — provided your binary honors the **deployable contract**
([ADR-023](adr/ADR-023-deployable-binary-contract.md)): `<bin> version` prints
its identity, and `<bin> serve --schema <path> --port <n>` starts it, failing
LOUD on misplaced arguments. The library gives you both in one call:

```go
var version, revision = "dev", "unknown"   // injected by the build below

func main() {
    args := appximo.ParseServeArgs("myapp", version, revision,
        appximo.ServeArgs{Port: 8099, ControlPort: 9099})
    app, err := appximo.New(appximo.Config{
        SchemaPath: args.SchemaPath, Port: args.Port,
        ControlPort: args.ControlPort, Version: version, // /health reports it
    })
    // …
}
```

Build with the canonical consumer build (it compiles your SPA first — hashed
assets are conventionally gitignored, so a bare `go build` embeds an EMPTY
shell — and injects the git version so `/health` and a rollback decision see a
real SHA):

```bash
# from your app repo; resolves the script out of your engine dependency
bash "$(go list -m -f '{{.Dir}}' github.com/appximo/appximo)/scripts/build-consumer.sh" /tmp/myapp
scp /tmp/myapp you@server:/tmp/myapp
sudo bash install.sh --domain api.example.com --email you@example.com \
  --binary=/tmp/myapp --cli=/tmp/appximo --schema=/tmp/myschema.json
# updates later:
sudo bash /opt/appximo/scripts/deploy-update.sh --binary=/tmp/myapp
```

**Production is two artifacts for a consumer app** (the honest version of the
"one binary" story): your binary SERVES everything — API, auth, GraphQL, your
frontend, health, control plane — and the engine CLI OPERATES it (register
tenants, `migrate --dry-run`, mint tokens, create the super-admin). Pass it via
`--cli` (build with `scripts/build-engine.sh`) and it lands at
`/opt/appximo/bin/appximo-cli`; when the installed binary IS the engine the
installer symlinks it automatically, so `appximo-cli …` is the one documented
invocation on every box. The systemd unit, Caddy, PostgreSQL and the env file
don't change. The installer prints WHERE the generated secrets live, never the
values (`--show-secrets` to opt in), and detects your control-plane port from
the live service.

Two more facts a consumer deploy should know:

- **One Caddy site = one tenant domain.** The installer writes a Caddyfile for
  exactly the `--domain` you gave; the tenant resolves from the Host header, so
  serving MORE tenants publicly means more site blocks (or a wildcard cert) in
  `/etc/caddy/Caddyfile` — each proxying to the same engine port.
- **Consumer boot DDL** belongs in `Config.BeforeStart` (tenants existing at
  boot) **plus `Config.OnTenantProvisioned`** (tenants registered while live) —
  with only the former, a freshly registered tenant is missing your DDL until a
  restart.

The outbox worker, if you run one, is a second systemd unit (see
[docs/DEPLOY.md § Background worker](DEPLOY.md)).

---

## 6. Serving your frontend

The Appximo binary serves an **API** (plus its own built-in UIs at `/editor`,
`/admin`, `/docs`, `/graphiql`) **and, in framework mode, your own frontend**
(`Config.Static` — LOOSE-ENDS-SWEEP-S1). Three ways to serve it, in the order you
should consider them:

**(a) Caddy serves the static build, proxies the API (recommended).** One box,
one origin, no CORS needed:

```caddy
{
    email you@example.com
}

app.example.com {
    handle /api/* {
        reverse_proxy 127.0.0.1:8090
    }
    handle /auth/* {
        reverse_proxy 127.0.0.1:8090
    }
    handle /graphql {
        reverse_proxy 127.0.0.1:8090
    }
    handle {
        root * /var/www/app        # your built SPA (Vite/Next export/etc.)
        try_files {path} /index.html
        file_server
    }
}
```

**(b) A separate host / CDN (Vercel, Netlify, Cloudflare Pages) + CORS.** The
frontend lives elsewhere and calls the API cross-origin. Enable CORS on the
engine for exactly those origins:

```bash
APPXIMO_CORS_ORIGINS="https://app.example.com"
# optional: APPXIMO_CORS_CREDENTIALS=true, APPXIMO_CORS_HEADERS=…
```

CORS is off by default and scoped to `/api`,`/auth`,`/graphql`,`/openapi` only
(never the control plane or `/admin`). Details in
[docs/DEPLOY.md § CORS](DEPLOY.md#cors--configurable-for-browser-spas-on-another-origin).

**(c) Embed it in your binary — ONE artifact, one deploy.** If you already build
a custom binary (framework mode, §5), compile the SPA into it with `go:embed` and
mount it with `Config.Static`. One file to ship, one process to run, one origin —
no CORS, no second deploy target, no proxy rule to keep in sync:

```go
//go:embed all:web/dist
var frontend embed.FS

dist, _ := fs.Sub(frontend, "web/dist")
app, err := appximo.New(appximo.Config{
    SchemaPath: "schema.json",
    Static: []appximo.StaticMount{{Path: "/", FS: dist, SPA: true}},
})
```

What the engine does for you:

- **`/` and client-side deep links** (`/orders/42`) serve `index.html` when
  `SPA: true`; a missing **file** (anything with an extension) still 404s, so a
  deleted bundle never comes back as HTML.
- **Caching is right by default**: content-hashed bundles (`assets/`, `_app/`,
  `static/`) get `immutable, max-age=31536000`; `index.html` is always
  `no-cache` — it names the current bundles, so a cached copy would point at
  files your next deploy deleted.
- **It cannot shadow the engine.** `/api`, `/auth`, `/admin`, `/editor`, `/docs`,
  `/graphql`, `/graphiql`, `/openapi`, `/metrics`, `/debug`, `/healthz`,
  `/readyz`, `/health`, `/files` and `/fleet` stay the engine's; mounting on one
  is a **boot error**, and an unknown `/api/…` path keeps its honest 404 instead
  of returning your shell.
- **No tenant transaction, no RBAC evaluation, no response-cache buffering** for
  an asset — a `.js` file needs no database.
- **Path traversal is impossible by construction**: the tree is an `fs.FS`, and
  `io/fs` cannot open outside its root.

Serve from disk instead of embedding with `os.DirFS("/var/www/app")` — useful if
the frontend is deployed on its own cadence.

> ⚠ **PCI (SAQ A) if you take card payments.** Keep the **checkout** page free of
> third-party scripts — analytics, chat widgets, tag managers. One extra script
> on the page that hosts the payment iframe moves the merchant from SAQ A to
> SAQ A-EP, a materially heavier compliance burden, because that script could
> reach the cardholder data entry surface. Put marketing tags on the pages that
> never touch payment.

Multi-app note: in `appximo fleet serve` (N apps in one process) a static mount
belongs to the **app that declares it**. The manifest-driven fleet configures
none, so no app serves static files there — a custom multi-app binary sets them
per app. Fail-closed: nothing is served unless it was declared.

The complete contract is in [BACKEND_SPEC_LLM.md](BACKEND_SPEC_LLM.md) §3.7, with
a runnable binary at [examples/fullstack/](../examples/fullstack/).

---

## 7. Docker variant

If you already run Docker or a container PaaS (Fly.io, Render, a Kubernetes
cluster), the compose stack is fully supported — it is just not the default for a
bare 1 GB VPS. `docker-compose.prod.yml` brings up Caddy + engine + worker +
PostgreSQL with automatic TLS and no host-exposed ports but 80/443:

```bash
cp .env.example .env        # set DOMAIN, ACME_EMAIL, JWT_SECRET, ADMIN_KEY, DB_PASSWORD
docker compose -f docker-compose.prod.yml up -d
```

The published multi-arch image (`neodevtrix/appximo`) **ships the built
`/editor` and `/admin` UIs** (the Dockerfile builds the SPAs in dedicated node
stages) and runs the engine or the outbox worker from one image. On a
memory-limited container (`--memory` / `mem_limit`) the engine auto-detects the
cgroup limit and sets `GOMEMLIMIT` to 90 % of it (see § 8). Full walkthrough:
[docs/DEPLOY.md](DEPLOY.md).

---

## 8. Configuration — production environment variables

Three are **required**; the rest have safe defaults. The installer sets the
required ones plus `APPXIMO_ENV`, the file/obs paths and `GOMEMLIMIT`. Full
per-field docs are in [config.go](../config.go) and the README config table.

| Variable | Req | Default | Notes |
|---|---|---|---|
| `DATABASE_URL` | **yes** | — | PostgreSQL DSN. The engine **auto-creates the control-plane tables** on boot, so a fresh empty database just works. |
| `JWT_SECRET` | **yes** | — | HS256 signing secret, ≥ 32 chars (`openssl rand -hex 32`). **Enforced since SEC-6 (2026-08-05): the engine refuses to boot below 32 characters**, naming the variable and the floor. |
| `ADMIN_KEY` | **yes** | — | `X-Admin-Key` for the control plane, `/metrics`, `/debug`, `/admin`. |
| `APPXIMO_ENV` | no | (prod) | `development` enables pprof (:6060) + GraphQL introspection + GraphiQL. **Leave unset/`production`** in prod. |
| `GOMEMLIMIT` | no | auto | Soft heap ceiling. Unset → the engine uses 90 % of an explicit **cgroup** limit if present, else warns on a small box. **Set it on a bare small box** — the installer sets **30 % of RAM** (measured: the engine's own anonymous memory peaks in the tens of MB even at 1M rows, and PostgreSQL needs the rest). Never derived from total RAM as if the engine were alone on the box. |
| `GOMAXPROCS` | no | auto | cgroup-aware (automaxprocs). |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | no | 1000 / 100 | Per-tenant token bucket. |
| `OBS_DB_PATH` | no | `/var/lib/appximo/obs/obs.db` | Trace/snapshot history (SQLite). Keep it on a persistent path. |
| `APPXIMO_FILES_DIR` | no | `/var/lib/appximo/files` | Local file-store root (or use `APPXIMO_FILES_BACKEND=s3`). |
| `APPXIMO_FILES_BACKEND` / `APPXIMO_FILES_S3_*` | no | `local` | S3/R2/Spaces/MinIO backend — see [docs/FILES.md](FILES.md). |
| `APPXIMO_FILES_MAX_BYTES` / `_TOKEN_TTL` / `_ALLOWED_EXT` | no | 256 MiB / 180 s / curated | Upload cap, signed-URL TTL, extension allowlist. |
| `DB_MAX_CONNS` | no | 10 | Postgres pool size. |
| `APPXIMO_MAX_TX_OPS` | no | 100 | Max ops per `POST /api/transaction`. |
| `APPXIMO_MAX_SSE_PER_TENANT` | no | unbounded | Cap concurrent SSE streams per tenant. |
| `APPXIMO_CORS_ORIGINS` (+ `_METHODS`/`_HEADERS`/`_EXPOSE_HEADERS`/`_CREDENTIALS`/`_MAX_AGE`) | no | off | Browser CORS; empty = disabled. |
| `APPXIMO_GRAPHQL_PLAYGROUND` | no | off | Serve GraphiQL + allow introspection outside dev. |
| `APPXIMO_AUTH_SIGNUP_ROLE` / `_MIN_PASSWORD` / `_REQUIRE_VERIFIED` / `_BASE_URL` | no | off / 8 / off / — | Public signup (opt-in, role-gated) + reset/verify email links. |
| `APPXIMO_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID`/`_SECRET`, `_CALLBACK_URL`, `_DEFAULT_ROLE`, `_SUCCESS_REDIRECT` | no | off | Social login. |
| `APPXIMO_MFA_KEY` / `APPXIMO_MFA_ISSUER` | no | JWT secret / `Appximo` | TOTP secret encryption + issuer label. |
| `APPXIMO_PLATFORM_SUPER_ADMIN_ROLE` / `_MFA_ISSUER` | no | `platform_super_admin` | Admin API super-admin. Bootstrap with `appximo admin create`. |
| `APPXIMO_EMAIL_TOPIC`, `SMTP_*` | no | — | Outbox email worker (`APPXIMO_WORKER_MODE=email`). |
| `SLACK_WEBHOOK_URL` | no | — | SLO burn-rate alerts. |
| `REDIS_URL` | no | — | Optional async migration worker. |
| `BACKUP_DIR` | no | `/tmp/appximo-backups` | Output dir for `POST /admin/backup`. |
| `APPXIMO_SAFEGO_TIMEOUT` / `APPXIMO_PUBLIC_ROUTE_RPS` / `_BURST` | no | 30 s / 5 / 10 | Library-mode custom-handler tuning. |
| `--port` / `--control-port` | flag | 8080 / 9090 | Data plane / control plane (control plane = **localhost only**). |

---

## 9. Security checklist

- **Firewall default-deny inbound.** Reachable from outside: **22** (SSH), **80**
  and **443** (Caddy). Not the engine port, **not 9090**, not 5432. `--harden`
  configures ufw for exactly this.
- **Control plane (`:9090`) is never on the internet.** Its safety model is
  *unreachable*, not *unguessable* — reach it from the box (`curl
  127.0.0.1:9090/...`) or an SSH tunnel.
- **Strong `ADMIN_KEY` and `JWT_SECRET`** (≥ 32 chars, `openssl rand -hex 32`).
  The installer generates them; never reuse a weak one.
- **Secrets file `0600`, owned `root:appximo`.** `/etc/appximo/appximo.env`
  holds every secret; the systemd unit runs the engine as the unprivileged
  `appximo` user with `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`.
- **Keep the box patched** (`--harden` enables unattended-upgrades) and copy
  backups off-box.
- **TLS is automatic** — don't disable it; don't serve the API on plain HTTP to
  the internet.

---

## 10. Troubleshooting

| Symptom | Most likely cause → fix |
|---|---|
| `https://domain` — certificate never issues | DNS for `domain` doesn't point at this box, or port **80** is blocked (Let's Encrypt's HTTP challenge needs it). Check `journalctl -u caddy -f`; confirm `dig +short domain` = your IP. |
| **502 Bad Gateway** from Caddy | The engine is down or not on the expected port. `systemctl status appximo`; `journalctl -u appximo -n 50`. A bad schema fails boot loudly there. |
| Engine **OOM-killed** / restarts under load | No `GOMEMLIMIT` on a small box. Set `GOMEMLIMIT=512MiB` (1 GB) in `/etc/appximo/appximo.env` and `systemctl restart appximo`. `dmesg | grep -i oom` confirms. |
| `serve` exits immediately | Missing a required var (`DATABASE_URL`/`JWT_SECRET`/`ADMIN_KEY`) or Postgres unreachable. The log names which. Check `DATABASE_URL` and `systemctl status postgresql`. |
| Registering a tenant returns an error about the tenant id | The id must match `^[a-z][a-z0-9_]{1,29}$` (it becomes a Postgres schema) — no hyphens/uppercase. |
| Port already in use on boot | Another process on 8090 (or your `--port`). Find it with `ss -ltnp | grep :8090` and stop it, or pick another port. |

Health endpoints for a probe/monitor (all unauthenticated): `/healthz`
(liveness), `/readyz` (readiness — 503 while draining), `/health` (JSON +
version). `/metrics` and `/debug/*` need `X-Admin-Key`.

---

## 11. Verify your own deploy

Don't take our numbers — measure yours.
[`scripts/verify-production/`](../scripts/verify-production/) is a repeatable
suite that measures **this exact stack on your box**: the RAM/CPU footprint per
service, the load it sustains and where its knee is, what TLS and the proxy
really cost, how it behaves at 100K/1M rows, REST vs GraphQL, and — optionally —
how it recovers when you kill the engine, kill Caddy, stop PostgreSQL, deploy
under load, or reboot the machine.

```bash
# from a machine that is NOT your server (a loader must not share the server's CPU)
bash scripts/verify-production/run-all.sh \
  --target=https://api.example.com \
  --server-ssh=root@YOUR.SERVER.IP
```

It writes a Markdown report plus the raw JSON behind every number. Two traps it
catches for you, because both silently invalidate a benchmark:

- **a CDN in front of your domain** — the name resolves to the edge, so you would
  be measuring Cloudflare, not your server (pass `--origin-ip` to measure the
  origin);
- **the engine's response cache** — every read is reported in both arms, cached
  and bypassed, so nobody quotes the cache as if it were the database.

Reference numbers from a 2 vCPU / 2 GB droplet, and the tuning that came out of
them: [docs/BENCHMARKS.md](BENCHMARKS.md).
