# Quick Start — from nothing to a live API

Two tracks, side by side, for every step:

- **Manual** — the ground truth. Every command here was executed against a real
  engine before being written down. If the one-command track ever fails you,
  [§4 The manual path](#4-the-manual-path--what-up-does-step-by-step) is the net.
- **With an AI agent** (Claude Code, Cursor, Copilot…) — the shortcut. You paste
  the engine's own printed contract into your agent and ask for outcomes, not
  commands.

> Measured on a small (1 vCPU) Linux box, 2026-08-07, by a fresh agent given
> only this document and the binary: **from first command to the success
> checklist fully green = 1m53s** with the Postgres image cached (`appximo up`
> itself: 12 s; a cold first run adds the `postgres:16` download, ~½–1 min).
> A human typing and actually reading should budget **~5 minutes warm**. The
> raw minute-by-minute table is in the session record; the ten-minute script
> is [at the end of §2](#the-ten-minute-script-measured).

**Version note.** Everything below — `appximo up`, `new`, `down`, `prompt` and
the embedded `/app` back-office — ships in the current release (`v0.1.5`,
2026-08-08). `appximo upgrade` and the update check in `version` land in the
next one; until then they exist in a source build or the Docker image
(`neodevtrix/appximo`, published from `main`). **If you installed Appximo
before, update first** — see [§1-bis](#1-bis-already-had-appximo-installed).

---

## 0. What you need

| Thing | Why | Where |
|---|---|---|
| **The `appximo` binary** | the engine — one static file, no runtime deps | [GitHub Releases](https://github.com/appximo/appximo/releases) |
| **Docker** *or* **any PostgreSQL 14+** | the only external dependency; `up` starts Postgres in Docker for you, or uses your `DATABASE_URL` | [get-docker](https://docs.docker.com/get-docker/), or a native/hosted PG |
| **curl** (or any HTTP client) | to talk to your API | preinstalled on Linux/macOS; on Windows use `curl.exe` |
| Go 1.25+ | **only** if you'll write custom Go handlers (§6) or build from source | [go.dev/dl](https://go.dev/dl/) |

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

### Windows — ⚠ NOT YET VERIFIED

> **Accents from Git Bash (field report, atina):** `curl` in Git Bash sends
> inline non-ASCII text (`ñ`, `á`) in the system code page, not UTF-8 — the
> bytes are wrong before they reach the engine, and the rows store mojibake.
> Send JSON **from a file saved as UTF-8 without BOM**
> (`curl.exe --data-binary @body.json`), or seed from Go/Node. Not
> live-verified on Windows (OPS-20).

> This path is written with care but **has not been executed on a real Windows
> machine**. The Windows `.exe` ships from the **next release**. The
> genuinely-verified options today: **WSL2** (install Ubuntu from the Microsoft
> Store, follow the Linux track verbatim — the code path we test), or **Docker
> Desktop** with the README compose flow. Native (PowerShell, unverified):
> download the `.exe`, rename to `appximo.exe`, then the same `appximo up`
> below. Windows caveats that ARE known from the code: data lives under
> `%LOCALAPPDATA%\Appximo`; the engine self-restart is not supported; script
> with `curl.exe`, not `Invoke-WebRequest`; after editing PATH open a **new**
> terminal.

### With an agent (any OS)

Paste the engine's own install prompt — it handles a fresh machine **and** an
old binary already on the PATH, on Linux, macOS and Windows:

```bash
appximo prompt --install      # already have it? this is also the update path
```

Don't have it yet? The same block is the "Install Appximo" step on
[the website](https://appximo.github.io/appximo/), or read
[docs/INSTALL_PROMPT.md](INSTALL_PROMPT.md).

## 1-bis. Already had Appximo installed?

**Read this before anything else if `appximo version` already prints
something.** An old binary is the most common state once releases exist, and
it fails in the most confusing way: commands documented here simply "don't
exist", which reads like a typo.

```bash
appximo version     # what you have — and it tells you if a newer release exists
appximo upgrade     # replace this binary with the newest one (verified checksum)
```

- `upgrade` swaps the running binary in place. On **Windows** it renames the
  current `appximo.exe` aside first (a running `.exe` cannot be overwritten)
  and leaves `appximo.old.exe` behind for the next upgrade to remove.
- If the binary lives somewhere root-owned, `upgrade` says so and names the
  privileged command — it never installs a second copy elsewhere on the PATH.
- The update check in `version` sends **nothing** about you or your machine
  (it is an anonymous GET of a public URL), never runs at `serve` boot, times
  out in 2 s, and is silenced with `APPXIMO_NO_VERSION_CHECK=1` or `--no-check`.
  `version --json` never checks at all, so CI stays offline and byte-stable.

### The state your previous try left behind

Upgrading the binary does **not** touch your data. If you want to know whether
something works because of the product or because of leftovers, this is what
exists and how to clear it:

| Leftover | What it is | Start clean |
|---|---|---|
| `./.env` | the three secrets `up` generated, 0600 | delete it — `up` writes a fresh one (the DB password changes too, so drop the volume as well) |
| `./schema.json` | your app's schema | delete it and `up` writes the starter again; keep it to preserve your app |
| Docker container `appximo-pg` | the Postgres `up` started | `appximo down` (keeps data) · `appximo down --destroy-data` (removes the volume — irreversible) |
| Tenants inside that Postgres | one per app you registered | `appximo tenant list` · `appximo tenant delete <id> --yes` (drops its schema + control-plane rows) |
| A previous `serve` still running | holds the port | find it with `ss -ltnp \| grep :8080` and stop that PID |

A completely clean slate is: `appximo down --destroy-data`, then delete `.env`
and `schema.json`, then `appximo up` again.

## 2. One command: `appximo up`

From an empty directory:

```bash
mkdir myapp && cd myapp
appximo up
```

It asks its **only two questions up front** (skip both with `--yes`, or answer
them as flags: `--name myapp`; `DATABASE_URL` in the environment answers the
Postgres one):

1. **Postgres?** — if `DATABASE_URL` is set it uses it; otherwise it asks
   permission to start `postgres:16` in Docker (container `appximo-pg`,
   published on loopback only, data in a named volume that survives restarts).
2. **App name?** — defaults to the directory name, sanitized to the tenant rule.

Then it does everything the first mile used to cost by hand — and says what it
wrote where at every step:

```
  ✓ schema: wrote the starter to schema.json (todo-api — edit it, or pass --schema)
  ✓ postgres: started postgres:16 in Docker — container "appximo-pg", port 127.0.0.1:54329, data in volume appximo-pg-data
  ✓ secrets: wrote DATABASE_URL, JWT_SECRET, ADMIN_KEY to ./.env (0600, no BOM) — and loaded them into this process
  ✓ tenant "myapp" registered with the schema — its tables were just created
  ✓ first admin created: admin@myapp.local — password printed ONCE below (works in /app and /admin)
  ✓ dev token minted (role admin, 24 h)
  ✓ verified: GET /api/tasks answered 200 through the full chain

──────────────────────────────────────────────────────────────
  Your app is running.

  App      http://myapp.localhost:8080/app      ← create & edit records
  Docs     http://myapp.localhost:8080/docs     (interactive API explorer)
  Admin    http://myapp.localhost:8080/admin    (tenants, users, observability)
  Editor   http://myapp.localhost:8080/editor   (visual schema editor)

  Sign in (works in /app and /admin) — printed ONCE, save it now:
    email     admin@myapp.local
    password  9fe4f489afa42f1ed269

  Dev API token (role admin, 24 h):
    eyJ…

  Try it from a second terminal:
    curl -H 'Authorization: Bearer TOKEN' -H 'Host: myapp.localhost' \
      -H 'Content-Type: application/json' \
      -d '{"title":"hello appximo"}' http://localhost:8080/api/tasks

  Wrote  ./.env (secrets, 0600)  ·  ./schema.json (the starter — make it YOURS)
  Postgres  docker container "appximo-pg" (127.0.0.1:54329, volume appximo-pg-data)
  Stop the server: Ctrl+C · Stop the Docker Postgres too: appximo down
──────────────────────────────────────────────────────────────
```

Every line of that card is **verified before it prints** — the last ✓ is a real
request through the full tenant → JWT → RBAC → SQL chain.

Open the **App** URL: that's `/app`, a back-office generated at runtime from
your API's own OpenAPI contract — tables, forms, validation, permissions — with
**zero code and zero build**. Sign in with the printed credentials and create a
record:

![The /app back-office over the starter schema](img/quickstart/app-list.png)

It is not a demo screen: every control comes from the contract. Enums become
selects, relations become dropdowns of the target resource, state machines
offer **only the legal next states** (terminal states render read-only), a
`file` field gets an upload widget with its declared policy, and a role that
can't read a resource sees it dimmed. Same UI, any schema:

![The /app generic form on a different schema — state machine + relation + validation](img/quickstart/app-form.png)

**Success checklist** (each independently checkable — an agent knows when to
stop):

```bash
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: myapp.localhost' http://localhost:8080/docs   # → 200
# the card's curl (with the real token) → 201 with the created record
# /app (browser) → lists that record after sign-in
```

**Run it twice, nothing breaks:** `up` detects and reuses everything it already
created (the `.env`, the container, the tenant, the admin — the card then says
`already registered — reusing`), and it compares your `schema.json` against the
tenant's registered schema: unchanged → it says so; changed → it migrates
(additive changes apply live, a destructive drop is never auto-approved — it
stays gated and the card prints the exact `appximo migrate --approve-drops`
command); a failed migration is a loud failure, never an `ok` over the old
schema. `appximo migrate` (§9) remains the explicit tool for dry-runs and
approving drops. Stop the server with Ctrl+C; `appximo down` stops the Docker
Postgres too (data volume kept — `--destroy-data` removes it, irreversibly).

**For machines:** `appximo up --json` prints the whole card as ONE JSON object
on stdout (URLs, credentials, token, files written, postgres details, the smoke
result) — progress and logs go to stderr. It is the `validate --json` pattern
applied to the first mile: an agent parses data, not prose.

**When it fails, it names the way out** — no Docker on the PATH, the Docker
daemon down or permission-denied, a busy port (`--port`), a foreign container
squatting the name (`--pg-container`), an unreachable `DATABASE_URL`, an
invalid schema (with the per-field errors), an invalid app name (with a valid
suggestion). `--no-docker` refuses the Docker path explicitly; three
alternatives are printed.

### From an idea instead of a starter: `appximo new`

```bash
appximo new "class bookings for a gym"
```

With `ANTHROPIC_API_KEY` set, it generates the schema from your sentence (the
same validator-guided loop as `appximo ai-generate` — measured: ~90% valid on
the first try, ~$0.01 per schema with the default cheap model), writes
`schema.json`, and runs `up`. **Without the key it does not fail**: it prints a
ready-to-paste prompt for your own coding agent plus the exact `up` command to
run when the agent is done.

### With an agent (the whole first act as one paste)

> Goal: API + admin + editor + visual back-office running LOCALLY for:
> `<YOUR IDEA IN ONE SENTENCE>`.
>
> Before starting, ask me ONLY these questions, together: (1) a Postgres
> connection string, or my permission to start one in Docker; (2) a short app
> name. After that, no more questions: use defaults.
>
> Then: install the appximo binary (verify its checksum), generate schema.json
> for my idea with the grammar from `appximo spec`, correct it with
> `appximo validate --json schema.json` until it prints `"valid": true`, and
> run: `appximo up --name <name> --schema schema.json --yes --json`.
>
> Deliver: every URL from the card (/app /docs /admin /editor), the
> credentials, and one curl that ALREADY WORKS against a record you created.
>
> Success criteria (verify each): /docs answers 200; that curl answers 201;
> /app lists the record.

### The ten-minute script (measured)

| Minute | What happens | With what |
|---|---|---|
| 0–2 | install + `appximo up` | §1 one-liner + `up` (first run adds ~1 min of Docker image download) |
| 2–3 | "it exists": /docs, /admin, /editor open | already embedded |
| 3–6 | "it's MY app": schema from the idea, deployed | `new` / your agent + `validate --json`, then `migrate` or Studio |
| 6–8 | "I USE it": create and edit records visually | the `/app` back-office |
| 8–10 | "I INTEGRATE it": token + curl + next steps | the card's token and example |

Measured (agent-driven, image cached): **checklist green at 1m53s** — the
minute marks above hold with room to spare; a human's doc-reading time is the
main variable (budget ~5 min warm, +1 min cold for the image). The production
deploy is deliberately NOT in these ten minutes — it's the second session, and
it's already a solved ~20 minutes with `scripts/install.sh` (§8).

## 3. Make the schema YOURS

Your whole API is one JSON file — `schema.json` in your project directory.
Edit it (or regenerate it), and apply.

### Manual

The starter shape, to grow by hand:

```json
{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "todo-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "minLength": 1, "maxLength": 200 },
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

**Read it back before trusting it:**

```bash
appximo explain schema.json            # plain-language: what the app manages,
appximo explain schema.json --lang es  # who can do what, lifecycles — no JSON
```

`explain` is the step for the person who ASKED for the app: it renders the
schema as prose ("a new order starts as *pending*… *cancelled* is final — once
there it can never change"). If the prose doesn't match what you meant, the
schema is wrong — fix it now, before anything is deployed.

**Apply your edits** to the running app: new fields go live hot with
`appximo migrate --tenant <name> --schema schema.json`; a new resource needs a
restart (§9 has the details and the safety gates).

## 4. The manual path — what `up` does, step by step

Everything `up` orchestrates, by hand. **This is the ground truth and the
net**: when something fails, these are the pieces to check — and each was
executed against a real engine before being written down.

### 4.1 The three settings

The engine refuses to start without three values — and tells you exactly which
are missing and how to generate them. Set them (or put the same three lines in
a `.env` file in your working directory — every `appximo` command loads it
automatically; the real environment wins on conflict; a BOM from a Windows
editor is tolerated):

```bash
export DATABASE_URL='postgres://appuser:secret@localhost:5432/appximo'
export JWT_SECRET="$(appximo gen-secret)"            # signs every auth token (32+ chars, enforced)
export ADMIN_KEY="$(appximo gen-secret --bytes 16)"  # protects tenant registration + the first-admin bootstrap
```

(`appximo gen-secret` works identically on every platform — no openssl needed.
`appximo init myapp --env` writes a ready `.env` for you.)

PostgreSQL, if you don't have one (Docker — this is exactly what `up` runs,
minus the loopback publish and the volume):

```bash
docker run -d --name appximo-pg -p 127.0.0.1:5432:5432 \
  -e POSTGRES_USER=appuser -e POSTGRES_PASSWORD=secret -e POSTGRES_DB=appximo \
  postgres:16
```

### 4.2 Serve

```bash
appximo serve --schema schema.json --port 8080
```

(Two listeners: the API on `--port`, and the tenant-registration **control
plane** on `--control-port` — default **9090**, keep it internal.)

**You should see** (the engine binds first, then announces):

```
Appximo serving on :8080 — Ctrl+C to stop
  This process stays in the foreground. Open a SECOND terminal to make requests,
  or run it in the background (append `&`, or use a systemd unit in production).
  Try it:  http://localhost:8080/docs  ·  your app /app  ·  admin panel /admin  ·  schema editor /editor
```

That first line matters: **the terminal is now busy serving**. Open a second one.

**If it fails:** `bind: address already in use` → something owns :8080, pick
`--port 8081`. `JWT_SECRET is too short` → the floor is 32 characters,
regenerate. Postgres connection errors name the DSN — check host/password.
If you just killed a previous instance, its graceful drain can hold the port
for a few seconds — wait and retry.

### 4.3 Register the tenant, mint a token, first calls

Every app on the engine is a **tenant**, addressed by Host subdomain. Register
one and talk to it:

```bash
# 1. Register the tenant (control plane, :9090, gated by your ADMIN_KEY).
#    The registration CARRIES the schema — that's what provisions the tenant's tables.
curl -s -X POST http://localhost:9090/tenants \
  -H "X-Admin-Key: $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"acme\",\"display_name\":\"Acme Inc\",\"schema\":$(cat schema.json)}"

# 2. Mint a dev token for it. (If your schema row-scopes a role by $user_id,
#    add --user-id <uuid> — an empty user id matches no rows, silently.)
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

**If it fails:** `400 no tenant in host` → send the Host header (the tenant IS
the Host subdomain); `401 token tenant mismatch` → the `Host:` names a
different tenant than the token. `403 forbidden` → the token's role isn't in
your schema's rbac (mint with `--schema` so bad roles are refused with the
declared list). `filter` returning everything → your shell ate the brackets;
use `curl -g`.

### With an agent

> Start the engine with my schema, register a tenant called <name>, and show me
> a created record and a filtered list. The engine's own `--help` and the specs
> from `appximo specs` tell you the exact commands.

## 5. Real users + the admin panel

`up` already created the first platform admin and a first tenant user (same
credentials, printed once). Beyond that, two ways to get humans into the app:

**The admin panel** — open **http://localhost:8080/admin**.

- If no admin exists yet, the login screen detects it and offers **"Create the
  first admin"**: paste your `ADMIN_KEY`, choose email + password, and you're
  in. (The key is the proof you're the operator.)

  ![The /admin first-run screen](img/quickstart/admin-firstrun.png)
- From the terminal instead:

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

Signup is **off by default** — the 403 names this exact switch. Login is
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

You already have one: `/app` is the generic back-office, free with the engine.
When you want a **product** frontend (your screens, your brand):

```bash
appximo frontend-spec > frontend-spec.md
```

That document is the API contract a UI consumes (auth incl. MFA branch, the
exact filter grammar, keyset pagination, uploads, SSE, the error→screen-state
map) plus the recommended stack (SvelteKit static SPA embedded via `go:embed` —
one binary, same origin, no CORS). Paste it into your agent with your schema
and ask for the screens. A runnable no-build example:
[examples/frontend-guide/](../examples/frontend-guide/). And
`appximo backoffice-spec` is the recipe `/app` itself is built from — for
embedding a contract-driven admin INSIDE your own SPA with your own theme.

## 8. Production: a domain, a cheap VPS, HTTPS

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

New **fields** go live hot (no restart) — readable, writable, filterable.
`/app` and `/docs` re-shape themselves from the contract. A new **resource**
needs the engine to recompile its routes: restart `serve` with the new schema —
or use **Studio** (`/editor`): its Deploy button runs the same dry-run →
approve flow, and it offers the one-click engine restart when one is needed.
Nothing is ever dropped without an explicitly enumerated approval — a
destructive change is gated behind a dry-run that shows you the rows you'd
lose.

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
| `up`: `no DATABASE_URL and no docker on the PATH` | install Docker, or point `DATABASE_URL` at any Postgres (local or hosted) — the error lists both |
| `up`: `app port 8080 is already in use` | another server owns it — the error prints the `ss` line to find it and the `--port` alternative |
| `up`: `container "appximo-pg" exists but…` | a previous run's container (recovered automatically) or a foreign one — the error says which and the way out |
| `missing required configuration: …` | the three settings of §4.1 — the message itself tells you how to generate each |
| `JWT_SECRET is too short` | 32-character floor, enforced — `appximo gen-secret` |
| `400 no tenant in host` / `401 token tenant mismatch` | wrong `Host:` header — the tenant is the Host subdomain |
| `403 forbidden` on every call | token role not declared in the schema rbac — mint with `appximo token --schema …` |
| `403 signup is disabled` | set `APPXIMO_AUTH_SIGNUP_ROLE` (the message names it) or create users via `/admin` |
| `bind: address already in use` | another engine (or the draining previous one) owns the port — wait/`--port` |
| Terminal "frozen" after `serve`/`up` | it's the foreground server (the boot message says so) — second terminal |
| `/admin` login, no credentials | `up` printed them once; else the screen offers first-admin creation, or `appximo admin create` |
| `/app` sign-in fails from `localhost` | open it at the tenant URL (`http://<name>.localhost:8080/app`) — the login screen's banner says so |
| A filter is ignored / shell error | `curl -g` (brackets), quote the URL |
| Blank page in a browser, but curl works | you're serving a SPA under a strict CSP — see FRONTEND_SPEC §CSP |

Everything deeper: [GUIDE.md](GUIDE.md) — the full third-party guide, and
`appximo specs` — the machine-readable contract set (five printables).
