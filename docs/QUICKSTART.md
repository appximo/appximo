# Quick Start — from nothing to a live API

Two tracks, side by side, for every step:

- **Manual** — the ground truth. Every command here was executed against a real
  engine before being written down. If the agent track ever fails you, this is
  the net.
- **With an AI agent** (Claude Code, Cursor, Copilot…) — the shortcut. You paste
  the engine's own printed contract into your agent and ask for outcomes, not
  commands.

> Rough budget, measured on a small Linux VPS: **with an agent ~20 minutes** to a
> running API you can click through; **by hand ~45–60 minutes** reading as you go.
> Production deploy (domain + VPS + HTTPS) adds ~15 minutes on top of either.

**Version note.** Steps marked **(next release)** describe features merged after
`v0.1.1`: the `explain` command, the `/admin` first-run screen, the Windows
binary, the multi-line boot banner, the consolidated missing-configuration
error, the enforced 32-character `JWT_SECRET` floor, and the signup-403 message
that names its switch. Until the next tag they exist when you build from source
or use the Docker image (`neodevtrix/appximo`, published from `main`);
everything else below works with `v0.1.1` exactly as written.

---

## 0. What you need

| Thing | Why | Where |
|---|---|---|
| **PostgreSQL 14+** | the only external dependency | Docker one-liner below, or a native install |
| **The `appximo` binary** | the engine — one static file, no runtime deps | [GitHub Releases](https://github.com/appximo/appximo/releases) |
| **curl** (or any HTTP client) | to talk to your API | preinstalled on Linux/macOS; on Windows use PowerShell's built-in `curl` alias or Invoke-RestMethod |
| Go 1.25+ | **only** if you'll write custom Go handlers (step 6) or build from source | [go.dev/dl](https://go.dev/dl/) |

No Node, no Redis, no message broker. Go is *not* needed to run the engine.

## 1. Install

### Linux / macOS (verified)

```bash
# Pick your platform: linux-amd64, linux-arm64, darwin-amd64, darwin-arm64
curl -LO https://github.com/appximo/appximo/releases/download/v0.1.1/appximo-v0.1.1-linux-amd64
chmod +x appximo-v0.1.1-linux-amd64
sudo mv appximo-v0.1.1-linux-amd64 /usr/local/bin/appximo

appximo version
```

**You should see:** `appximo v0.1.1 (commit a587095…)`.

**If it fails:** `Permission denied` → you skipped `chmod +x`. `cannot execute
binary file` → wrong platform; `uname -m` says which one you need (`x86_64` →
amd64, `aarch64`/`arm64` → arm64).

PostgreSQL, if you don't have one (Docker):

```bash
docker run -d --name appximo-pg -p 5432:5432 \
  -e POSTGRES_USER=appuser -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=appximo \
  postgres:16-alpine
```

### Windows — ⚠ NOT YET VERIFIED

> This path is written with care but **has not been executed on a real Windows
> machine**. The Windows `.exe` ships from the **next release** (the `v0.1.1`
> release has no Windows asset — download the source or use WSL2 meanwhile).
> If something below is wrong, please open an issue.

The genuinely-verified Windows options **today**:

1. **WSL2** (recommended): install Ubuntu from the Microsoft Store and follow
   the Linux track above verbatim inside it. This is the same code path we test.
2. **Docker Desktop**: follow the README quick start's compose flow (it wires
   PostgreSQL + the three settings for you) — the image alone won't boot
   without them.

Native Windows (next release, unverified — PowerShell, not CMD):

```powershell
# Download appximo-vX.Y.Z-windows-amd64.exe from Releases, then:
Move-Item .\appximo-vX.Y.Z-windows-amd64.exe .\appximo.exe
.\appximo.exe version

# PostgreSQL: Docker Desktop (as above), or the EDB installer from
# https://www.postgresql.org/download/windows/ (remember the password you set).

# Environment variables are per-session in PowerShell:
$env:DATABASE_URL = "postgres://appuser:secret@localhost:5432/appximo"
$env:JWT_SECRET   = [guid]::NewGuid().ToString("N") + [guid]::NewGuid().ToString("N")  # 64 chars
$env:ADMIN_KEY    = [guid]::NewGuid().ToString("N")                                     # 32 chars

.\appximo.exe serve --schema schema.json --port 8080
```

Known Windows caveats (by design, from the code):

- The engine self-restart (Studio's one-click "Restart engine now") is
  **not supported on Windows** — stop and start the process by hand.
- `appximo fleet run` (multi-process fleet) is a unix deployment shape; on
  Windows use plain `serve`.
- Paths with spaces: quote `--schema "C:\My Apps\schema.json"`.

### With an agent (any OS)

Paste this into your agent:

> Install the Appximo engine on this machine: download the right binary for this
> platform from https://github.com/appximo/appximo/releases, verify its
> checksum against checksums.txt, put it on the PATH, and start a PostgreSQL 16
> in Docker for it. Then show me `appximo version`.

## 2. The three settings

The engine refuses to start without three values — and tells you exactly which
are missing and how to generate them (run it once with nothing set to see the
message). Set them:

```bash
export DATABASE_URL='postgres://appuser:secret@localhost:5432/appximo'
export JWT_SECRET="$(openssl rand -hex 32)"    # signs every auth token (32+ chars; enforced from the next release)
export ADMIN_KEY="$(openssl rand -hex 16)"     # protects tenant registration + the first-admin bootstrap
```

Keep them in a `.env` file you `source` — you'll need the same values every run.

**You should see (next release)** if you skip this and run `serve` anyway
(v0.1.1 reports the missing variables one at a time, with shorter text):

```
appximo: missing required configuration:
  DATABASE_URL — the PostgreSQL connection string, e.g. postgres://user:pass@localhost:5432/appximo …
  JWT_SECRET   — signs every auth token; any random value of 32+ characters. Generate one: openssl rand -hex 32 …
  ADMIN_KEY    — protects tenant registration and the first-admin bootstrap. Generate one: openssl rand -hex 16 …
```

## 3. The schema — your whole API in one JSON file

### Manual

Create `schema.json`:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"], "default": "open" },
        "due":    { "type": "time" }
      }
    }
  },
  "rbac": {
    "roles": {
      "admin":  { "resources": "*", "actions": ["*"] },
      "viewer": { "resources": ["tasks"], "actions": ["read"], "fields": ["id", "title", "status"] }
    }
  }
}
```

Check it:

```bash
appximo validate schema.json
```

**You should see:** `Schema valid ✓`. An invalid schema gets a named, per-field
error — fix and re-run. The full grammar (all types, relations, state machines,
RBAC): `appximo spec`, or [GUIDE.md](GUIDE.md) ch. 4.

### With an agent

```bash
appximo spec > appximo-spec.md     # the schema grammar, written for an LLM
```

Paste `appximo-spec.md` into your agent and ask, in your own words:

> Using ONLY this grammar, write schema.json for <your app: a gym's class
> bookings / a bakery's orders / …>. Validate it with
> `appximo validate --json schema.json` and fix every error it reports until
> it is valid.

The `--json` validator output is designed as an error-correction oracle for
agents — path, rule, expected, got, and a suggested fix per error.

**(next release) Read it back before trusting it:**

```bash
appximo explain schema.json            # plain-language: what the app manages,
appximo explain schema.json --lang es  # who can do what, lifecycles — no JSON
```

`explain` is the step for the person who ASKED for the app: it renders the
schema as prose ("a new order starts as *pending*… *cancelled* is final — once
there it can never change; *member* can only see rows whose `owner_id` is the
signed-in user"). If the prose doesn't match what you meant, the schema is
wrong — fix it now, before anything is deployed.

## 4. Run it and make the first call

```bash
appximo serve --schema schema.json --port 8080
```

(Two listeners: the API on `--port`, and the tenant-registration **control
plane** on `--control-port` — default **9090**, keep it internal. If 8080 or
9090 are taken, both flags exist.)

**You should see** (the last lines — the engine binds first, then announces;
the two helper lines below the first one are **(next release)** — v0.1.1 prints
only the "serving on" line):

```
Appximo serving on :8080 — Ctrl+C to stop
  This process stays in the foreground. Open a SECOND terminal to make requests,
  or run it in the background (append `&`, or use a systemd unit in production).
  Try it:  http://localhost:8080/docs  ·  admin panel /admin  ·  schema editor /editor
```

That first line matters: **the terminal is now busy serving**. Open a second one.

**If it fails:** `bind: address already in use` → something owns :8080, pick
`--port 8081`. `JWT_SECRET is too short` → the floor is 32 characters,
regenerate. Postgres connection errors name the DSN — check host/password.
If you just killed a previous instance, its graceful drain can hold the port
for a few seconds — wait and retry.

Now, in the second terminal — every app on the engine is a **tenant**, addressed
by Host subdomain. Register one and talk to it:

```bash
# 1. Register the tenant (control plane, :9090, gated by your ADMIN_KEY).
#    The registration CARRIES the schema — that's what provisions the tenant's tables.
curl -s -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme Inc\",\"schema\":$(cat schema.json)}"

# 2. Mint a dev token for it
TOKEN=$(appximo token --secret "$JWT_SECRET" --tenant acme --role admin --schema schema.json | tail -1)

# 3. Create a record
curl -s -X POST http://localhost:8080/api/tasks \
  -H 'Host: acme.localhost' -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"title": "first task"}'

# 4. Read it back, filtered
curl -sg 'http://localhost:8080/api/tasks?filter[status][eq]=open' \
  -H 'Host: acme.localhost' -H "Authorization: Bearer $TOKEN"
```

**You should see:** step 1 → `{"id":"acme","pg_schema":"tenant_acme",…}` (its
tables are created on the spot; forgetting the `schema` field is a named
`{"errors":["schema is required"]}`); step 3 → `201` with the record, `status`
filled in by its default; step 4 → `{"data":[…],"meta":{…}}`.

Open **http://localhost:8080/docs** in a browser — the interactive OpenAPI
explorer for *your* API, generated from your schema, no flag needed. GraphQL
lives at `/graphql`.

![Swagger UI over the todo-api schema](img/quickstart/docs-swagger.png)

And **http://localhost:8080/editor** is Studio — the same schema as a visual
canvas (edit, validate, deploy from the browser):

![Appximo Studio showing the tasks entity](img/quickstart/studio-editor.png)

**If it fails:** `400 invalid tenant` → the Host's subdomain isn't a valid
tenant label; `401 token tenant mismatch` → the `Host:` header names a
different tenant than the token (the tenant IS the Host — always send it).
`403 forbidden` → the token's role isn't in your schema's
rbac (mint with `--schema` so bad roles are refused with the declared list).
`filter` returning everything → your shell ate the brackets; use `curl -g`.

### With an agent

> Start the engine with my schema, register a tenant called <name>, and show me
> a created record and a filtered list. The engine's own `--help` and the specs
> from `appximo specs` tell you the exact commands.

## 5. Real users + the admin panel

Two ways to get your first human into the app:

**The admin panel** — open **http://localhost:8080/admin**.

- **(next release)** If no admin exists yet, the login screen detects it and
  offers **"Create the first admin"**: paste your `ADMIN_KEY`, choose email +
  password, and you're in. (The key is the proof you're the operator.)

  ![The /admin first-run screen](img/quickstart/admin-firstrun.png)
- On `v0.1.1`, create it from the terminal first, then sign in:

```bash
appximo admin create --email you@example.com --password 'a-strong-passphrase'
```

From the panel: create tenants, create **tenant users** with a role from your
schema, browse data read-only, watch per-tenant latency/SLO/traces.

**Public signup**, if your app should let people register themselves:

```bash
export APPXIMO_AUTH_SIGNUP_ROLE=viewer   # a role your schema's rbac declares
# restart serve, then:
curl -s -X POST http://localhost:8080/auth/signup -H 'Host: acme.localhost' \
  -H 'Content-Type: application/json' \
  -d '{"email":"ana@example.com","password":"a-password-123"}'
```

Signup is **off by default** — a 403 that, from the next release, names this
exact switch. Login is
`POST /auth/login` → `{user, token}`; the token is the same JWT the API
validates. Password reset, email verification, OAuth (Google/GitHub/Microsoft)
and TOTP MFA are all built in — [GUIDE.md](GUIDE.md) §2.7.

## 6. The 10% the schema can't say: a custom Go handler

The generated CRUD covers ~90% of a backend. For the rest (a checkout that
debits stock, a signed-webhook receiver, a computed report) you import the
engine as a Go library and register handlers **in the same process and
transaction**:

```bash
appximo backend-spec > backend-spec.md   # the complete guide, with compiling examples
```

Paste it (plus `appximo spec`) into your agent and ask for the endpoint you
need — or read it yourself; the `Ctx` API is ~10 calls. Requires Go 1.25+:

```go
import "github.com/appximo/appximo"   // go get github.com/appximo/appximo@latest
```

The runnable skeleton is
[examples/backend-guide/](../examples/backend-guide/) and the full walk is
[GUIDE.md](GUIDE.md) ch. 5.

## 7. A frontend on the same binary

```bash
appximo frontend-spec > frontend-spec.md
```

That document is the API contract a UI consumes (auth incl. MFA branch, the
exact filter grammar, keyset pagination, uploads, SSE, the error→screen-state
map) plus the recommended stack (SvelteKit static SPA embedded via `go:embed` —
one binary, same origin, no CORS). Paste it into your agent with your schema
and ask for the screens. A runnable no-build example:
[examples/frontend-guide/](../examples/frontend-guide/).

## 8. Production: a domain, a $6 VPS, HTTPS

On a fresh Ubuntu VPS (as root), the **one-command installer** takes you from
empty box to HTTPS: native PostgreSQL, systemd unit, Caddy with automatic
Let's Encrypt:

```bash
git clone https://github.com/appximo/appximo && cd appximo
./scripts/install.sh --domain=api.yourdomain.com --email=you@example.com
```

Before running it: buy the domain and point an **A record** at the VPS IP
(`api.yourdomain.com` → the IP; add a wildcard `*.api.yourdomain.com` if you
want each tenant on its own subdomain — remember, **tenant = subdomain**).

**You should see:** the installer's summary with your service names, and
`https://api.yourdomain.com/health` answering `{"status":"ok","version":…}`
with a real certificate. Live examples of exactly this setup:
[tiendita.appximo.com](https://tiendita.appximo.com) ·
[petfriendly.appximo.com](https://petfriendly.appximo.com).

![A production Appximo app behind HTTPS](img/quickstart/live-https-demo.png)

Updates are `scripts/deploy-update.sh` (atomic swap, auto-rollback — measured
0.28 s of unavailability), backups `scripts/backup.sh` (schedule it yourself —
see step 10), and the full runbook is [PRODUCTION.md](PRODUCTION.md).

## 9. Change something and redeploy

Add a field to `schema.json`, then:

```bash
appximo validate schema.json
appximo migrate --tenant acme --schema schema.json --dry-run   # see the plan first
appximo migrate --tenant acme --schema schema.json             # apply
```

New **fields** go live hot (no restart). A new **resource** needs the engine to
recompile its routes: restart `serve` with the new schema — or use **Studio**
(`/editor`): it's the visual editor over the same schema JSON, its Deploy button
runs the same dry-run → approve flow, and it offers the one-click engine
restart when one is needed. Nothing is ever dropped without an explicitly
enumerated approval — a destructive change is gated behind a dry-run that shows
you the rows you'd lose.

## 10. Backup (before you need it)

```bash
appximo backup --out backups/           # pg_dump per tenant, compressed
```

It shells out to `pg_dump`, so the PostgreSQL **client tools** must be on the
PATH (`apt install postgresql-client` — the engine itself doesn't need them;
without them the command fails naming exactly this).

In production, wire `scripts/backup.sh` into cron or a systemd timer yourself —
the installer copies the script but does **not** schedule it (a tracked
backlog item, ENG-3). Restore is a manual `pg_restore` drill, documented in
[PRODUCTION.md](PRODUCTION.md) §backups — run it once before you need it: the
restore you never tested is not a backup.

---

## When something fails — the short list

| Symptom | Cause → fix |
|---|---|
| `missing required configuration: …` | the three settings of step 2 — the message itself tells you how to generate each |
| `JWT_SECRET is too short` | 32-character floor, enforced from the next release — `openssl rand -hex 32` |
| `400 invalid tenant` / `401 token tenant mismatch` | wrong `Host:` header — the tenant is the Host subdomain |
| `403 forbidden` on every call | token role not declared in the schema rbac — mint with `appximo token --schema …` |
| `403 signup is disabled` | set `APPXIMO_AUTH_SIGNUP_ROLE` (the next release's message names it) or create users via `/admin` |
| `bind: address already in use` | another engine (or the draining previous one) owns the port — wait/`--port` |
| Terminal "frozen" after `serve` | it's the foreground server (the boot message says so) — second terminal |
| `/admin` login, no credentials | **(next release)** the screen offers first-admin creation; on v0.1.1: `appximo admin create` |
| A filter is ignored / shell error | `curl -g` (brackets), quote the URL |
| Blank page in a browser, but curl works | you're serving a SPA under a strict CSP — see FRONTEND_SPEC §CSP |

Everything deeper: [GUIDE.md](GUIDE.md) — the full third-party guide, and
`appximo specs` — the machine-readable contract trilogy.
