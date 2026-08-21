# Appximo — the OPERATIONS contract (install → tenant → users → evolve → production)

This is the fourth printable document (`appximo quickstart`, alias
`lifecycle-spec`). The build-side docs teach you to CONSTRUCT — `spec` (the
schema), `backend-spec` (custom Go), `frontend-spec` (the UI),
`backoffice-spec` (a generated admin UI). **This one teaches you to OPERATE**:
every step between "I have a binary" and "a real app is running for real
users, updated and backed up". The first third-party field evaluation showed
the build half worked flawlessly from one paste while the two most expensive
discoveries of the whole cycle — how a tenant is registered, how the first
admin exists — lived in no printable doc. They live here now, as steps 1 and 2.

Everything below is exact and verified against the running engine. If a
command is not listed here or in the other four docs, do not invent it.

---

## 0. The mental model in five lines

- One process serves **N tenants**: same routes/RBAC/validation compiled once
  from the boot `--schema`; each tenant has its own isolated Postgres schema
  (`tenant_<id>`) and its own data.
- **The tenant is the Host header's first label**: `acme.example.com` →
  tenant `acme`. There is no tenant picker and no query parameter — the HOST
  is the tenant. A request to a host with no tenant label answers a named 400.
- Two listeners: the **data plane** (`--port`, default 8080 — public) and the
  **control plane** (`--control-port`, default 9090 — tenant registration,
  gated by `X-Admin-Key`; NEVER expose it to the internet).
- Writes and reads on new columns serve HOT after a migration (read, write,
  filter/sort/search, aggregates). A brand-NEW resource, GraphQL input types,
  RBAC changes, hooks and /docs activate on an engine restart — the engine and
  Studio both tell you when one is needed.
- Auth is one JWT contract: HS256 signed with `JWT_SECRET`, claims
  `user_id`/`role`/`tenant_id`. Whether it came from `appximo token` (dev),
  `/auth/login` (your users) or the admin panel, the API validates it the
  same way.

## 0-bis. The one-command local start: `appximo up`

Steps 1–4 below are the pieces. **For a LOCAL first run, one command does all
of them**:

```bash
mkdir myapp && cd myapp
appximo up                       # interactive: two questions, then everything
appximo up --name myapp --yes    # non-interactive: all defaults
appximo up --json                # for agents: the card as ONE JSON object on stdout
```

It resolves Postgres (`DATABASE_URL` if set, else `postgres:16` in Docker —
container `appximo-pg`, loopback-only, data in a volume), generates secrets and
writes+loads `./.env`, takes `--schema`/`./schema.json`/a starter, registers
the tenant WITH the schema (step 1), bootstraps the first admin and an app
user (step 2), mints a dev token, verifies one real request, and prints the
card: URLs (`/app` `/docs` `/admin` `/editor`), credentials (ONCE), token, a
working curl. Re-running is safe (everything existing is reused; schema
changes go through `migrate`, §5). `appximo down` stops the Docker Postgres
(`--destroy-data` also removes the data volume). `appximo new "<idea>"` is
`ai-generate` → validate → `up`; with no ANTHROPIC_API_KEY it prints a prompt
for YOUR agent instead of failing. **Production never uses `up`** — that is
§6's installer.

## 1. Install and configure

**Linux/macOS:** download the release binary for your platform, `chmod +x`,
move it into PATH. **Windows:** download the `.exe`, rename to `appximo.exe`,
add its folder to PATH (open a NEW terminal after editing PATH).

Three settings are hard-required; `serve` refuses to boot without them and
names ALL the missing ones at once:

```
DATABASE_URL   postgres://user:pass@localhost:5432/dbname
JWT_SECRET     any random value of 32+ characters  →  appximo gen-secret
ADMIN_KEY      the operator credential             →  appximo gen-secret --bytes 16
```

- `appximo gen-secret` prints a crypto-random hex secret on every platform —
  no openssl needed.
- **A `.env` file in the working directory is loaded automatically** (one
  `KEY=value` per line; `#` comments; the real environment always wins on
  conflict; a Windows-editor BOM is tolerated). Write the three settings
  there once and every subcommand sees them.
- Postgres: any 14+ works. Fast dev option:
  `docker run -d --name pg -e POSTGRES_PASSWORD=dev -p 127.0.0.1:5432:5432 postgres:16-alpine`.
  Note the `127.0.0.1:` — see §7's Docker/ufw trap before doing this on a VPS.
- The engine **self-bootstraps its control-plane tables** on a fresh empty
  database. There is no migration SQL to run by hand, ever.

## 2. Boot

```bash
appximo serve --schema schema.json --port 8080
```

The process stays in the FOREGROUND serving; open a second terminal to make
requests. The boot log is the state of the system: it names what is enabled
and what is off and why (CORS, signup, SLO alerts). `/healthz` = liveness,
`/readyz` = readiness (503 while draining on SIGTERM), `/health` = version.

## 3. STEP 1 — register a tenant (the registration CARRIES the schema)

An app on the engine is a **tenant**. Registering one provisions its tables
on the spot — the schema travels IN the registration body (forgetting it is a
named `{"errors":["schema is required"]}`):

```bash
curl -s -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme Inc\",\"schema\":$(cat schema.json)}"
```

- **The tenant id rule is `^[a-z][a-z0-9]{1,29}$`** — lowercase letter first,
  then lowercase letters or digits. NO hyphens, NO underscores, NO uppercase:
  the id is simultaneously the Postgres schema (`tenant_acme` — forbids
  hyphens) and the host's first label (forbids underscores). A rejected id
  gets the rule AND a suggested valid id in the error.
- **The id must equal the first label of the domain that serves the app**: a
  tenant `acme` is reachable only at `acme.<your-domain>` (dev:
  `acme.localhost`). Creation is all-or-nothing — a failed step rolls back
  everything, no zombie tenants.
- Alternatives that do the same thing: the admin panel (`/admin` → Tenants →
  New — same validation, schema pasted in), or Studio's Deploy button
  (design on the canvas → "Deploy new app").
- Dev tokens: `appximo token --secret "$JWT_SECRET" --tenant acme --role admin
  --schema schema.json` (with `--schema` it refuses roles the schema does not
  declare). **Add `--user-id <uuid>` whenever identity matters**: without it
  the token carries an EMPTY user id, so a role whose row condition compares
  `$user_id` matches ZERO rows (no error anywhere — the list is just empty),
  an optional-auth route reads you as a guest, and a custom handler's
  `Claims().UserID` is `""`. The CLI now says this at mint time. Then every
  data call needs the tenant host + the token:
  `curl -H 'Host: acme.localhost' -H "Authorization: Bearer $TOKEN" …`.
- Before trusting a schema you did not write by hand:
  `appximo explain schema.json --lang es|en` renders it as plain-language
  prose for the app's OWNER — resources, every field's rules in words, state
  machines in flow order, each role's reach. Deterministic (read from the
  parsed schema, never guessed): the read-back step of the authoring loop.
- Inventory / cleanup: `appximo tenant list` ·
  `appximo tenant delete <id> --yes` (drops the schema CASCADE + every
  control-plane row).

## 4. STEP 2 — the first admin, and where users come from

The admin panel at **`/admin`** manages tenants, tenant users and
observability. It needs a **platform super-admin**, which no public form can
create:

- **First run:** open `/admin` — when no admin exists, the login screen
  detects it and offers **"Create the first admin"**: paste your `ADMIN_KEY`
  (the proof you are the operator), choose email + password. The route closes
  permanently once any admin exists.
- **Terminal equivalent:**
  `appximo admin create --email you@example.com --password '…'`
  (needs `DATABASE_URL` + `JWT_SECRET` in the environment / `.env`).

**Tenant users** (the people who log into YOUR app) are separate from the
platform admin, live per-tenant, and come from exactly three places:

1. The admin panel: `/admin` → Users (pick the tenant, assign a role your
   schema's RBAC declares) — or the API it wraps:
   `POST /admin/tenants/{id}/users` with `X-Admin-Key` or a platform token.
2. **Public signup**, off by default. Enable by naming the role every signup
   gets: `APPXIMO_AUTH_SIGNUP_ROLE=viewer` (a role the schema declares, or
   the boot refuses) → `POST /auth/signup {email,password}` on the tenant
   host. Off, the 403 names this exact switch.
3. Your custom Go backend (`Ctx.CreateUser` inside a handler — backend-spec).

Login is `POST /auth/login` → `{user, token}` on the tenant host; refresh via
`POST /auth/refresh`. Password reset + email verification (needs the email
worker), OAuth (Google/GitHub/Microsoft) and TOTP MFA are built in and
env-configured — they are product features, not code you write.

## 5. Evolve the schema of a LIVE tenant

Edit the schema file, then apply it to the tenant — **never only the boot
file**. The tenant's record is what Studio and future migrations read; the
one operator rule is: after changing the schema, run migrate (or deploy from
Studio), so the record and the database move together.

```bash
appximo migrate --tenant acme --schema schema.json --dry-run   # the plan + impact, applies nothing
appximo migrate --tenant acme --schema schema.json             # apply (additive by default)
```

- **Additive by default; destructive is opt-in per key.** A removed field or
  resource is NEVER dropped implicitly — the dry-run shows each would-be drop
  with its measured row loss, and only
  `--approve-drops "table.column,table2"` executes exactly those.
- **What serves hot after apply:** new COLUMNS on existing resources — read,
  write, filter/sort/search, aggregates — with no restart. **What needs a
  restart:** a brand-new RESOURCE (routes/GraphQL/docs don't exist in the
  process — the API answers `resource_not_loaded` with the fix, not a bare
  404), GraphQL input types, RBAC changes, hooks. Studio's deploy offers the
  restart as one click; on a systemd box `systemctl restart <unit>` does it.
- Fleet-wide: `appximo migrate --all-tenants --schema base.json` — sequential,
  per-tenant advisory-locked, resilient (one broken tenant never blocks the
  rest) and resumable (re-run = retry only the failed).
- Every applied schema is versioned (`GET /admin/tenants/{id}/schema/history`);
  rollback is a re-deploy of a stored version through the same
  dry-run → approval gate.

## 6. To production (a cheap VPS with real HTTPS)

The official path is native (no Docker): one command on an empty Ubuntu box —

```bash
bash install.sh --domain=app.example.com --email=you@example.com \
  --binary=/tmp/appximo --harden
```

installs PostgreSQL tuned for the box, a systemd unit, Caddy with a real
Let's Encrypt certificate, generates the secrets into
`/etc/appximo/appximo.env` (0600), and prints the tenant-registration command
with the schema in the body. `--harden` adds ufw (22/80/443) + fail2ban +
unattended-upgrades. It is idempotent — re-run the same command to resume.
Your production tenant id must be the first label of the domain (`app` for
`app.example.com`).

- **Update:** `deploy-update.sh --binary=/tmp/new-binary` — atomic swap,
  health-polled, auto-rollback that re-verifies recovery. Custom consumer
  binaries deploy the same way (`--cli` keeps the ops CLI updated beside it).
- **Backup:** `scripts/backup.sh` (pg_dump + rotation) — schedule it in cron;
  the installer copies it but does NOT schedule it. A backup you have not
  restored is a hope, not a backup: run a restore drill on a scratch box.
- **Never `git pull` on a production box.** A deploy is a binary swap.

## 7. The traps that bite operators (each verified in the field)

- **Docker publishes ports AROUND ufw.** Docker's NAT rules are evaluated
  before ufw's INPUT chain, so `docker run -p 5432:5432 postgres` on a VPS is
  internet-exposed even while `ufw status` says deny-incoming. Always publish
  on loopback: `-p 127.0.0.1:5432:5432`. `ufw status` will not warn you.
- **The control plane (:9090) is internal.** Reverse-proxy only the data
  plane. Registration from outside goes through `/admin` (authenticated) —
  not by exposing :9090.
- **A 500/401 that only happens from scripts and never from the browser** is
  almost always a missing tenant Host header. The engine now answers a named
  400 (`no tenant in host "localhost"…`) — read it, it contains the fix.
- **Windows scripting:** prefer `curl.exe` over PowerShell cmdlets
  (`Invoke-WebRequest` drops response bodies on ≥400; inline JSON loses its
  quotes — send bodies with `--data-binary @file`). CLI commands print no
  stderr noise, so `$?` is trustworthy.
- **Stateless JWTs outlive suspension**: suspending a user/tenant blocks new
  logins; already-issued tokens live until `exp`. That is the documented
  trade-off, not a bug.
- Data lives under `/var/lib/appximo` on Linux, `%LOCALAPPDATA%\Appximo` on
  Windows, `~/Library/Application Support/Appximo` on macOS — the boot log
  prints the resolved paths; `APPXIMO_FILES_DIR` / `OBS_DB_PATH` override.

## 8. When something fails — the ordered checklist

1. The boot log: every disabled feature and every misconfiguration is named
   there first.
2. `serve` exits immediately → a missing required var (the error lists ALL of
   them + how to generate each) or Postgres unreachable (the error names the
   DSN it tried).
3. `401 invalid token` → expired/wrong-secret token; `401 token tenant
   mismatch` → the token's tenant ≠ the host's label (the error names both
   and the address the token would work at).
4. `403 forbidden` → the role exists but the RBAC denies the action; an
   UNDECLARED role gets the same 403 (deliberate — the distinction is in the
   server log).
5. `400` → the error names the parameter/label and the rule; `curl` needs
   `-g` for `filter[...]` brackets.
6. `422 validation_failed` → every failing field at once, each with its rule.
7. A blank page under `/admin`/`/editor` cannot happen anymore: a binary
   without embedded UI assets answers 503 with the fix named. (If you see it,
   the binary predates ADR-025 — update.)
