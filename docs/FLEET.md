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

## Quick start — `make fleet-init` + `make fleet` (no hand-written JSON)

From a clone, the whole flow is two commands (FLEET-CONSOLE-S2):

```bash
make fleet-init    # scaffolds a WORKING fleet:
                   #   fleet.json            — the manifest (committable: holds NO secrets)
                   #   fleet-secrets/        — GENERATED operator key + admin + per-app
                   #                           secrets (gitignored by construction)
                   #   schemas/<app>.json    — a starter schema per app
                   #   app_<name> databases  — created from your $DATABASE_URL base
make fleet         # build UIs+engine, load the fleet secrets, serve everything on :8080
                   # make fleet PORT=9000 / FLEET_CONFIG=other.json — like make dev
```

Then open the **console** (`http://localhost:8080/fleet?key=…` — the operator key
is in `fleet-secrets/fleet.env`) and manage every app from there: inventory,
one-click Studio//admin//docs per app, add/remove apps. Secrets never enter git:
the manifest references per-app `env_file`s under the gitignored `fleet-secrets/`.
Customize the scaffold: `appitools fleet init --app crm --app shop --admin-email you@x.com`.

## The manifest by hand

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
- **Deploy = per-app HOT-SWAP** (`POST /admin/engine/schema` on an app,
  MT-STRUCT-S4): the engine persists that app's boot schema, recompiles **only
  that app's** router from the new schema (reusing the pool — a schema deploy
  never changes the DSN — and all other infra), and atomically swaps it into the
  registry. **The process is not restarted and the other apps are not touched**
  (measured: an app under load saw zero disruption and unchanged p50 during 66
  swaps of another app; the swap itself is ~tens of ms). New resources/columns,
  their GraphQL types, RBAC, `/openapi.json` and `/docs`, and
  `/admin/served-resources` all go live from the swap — no downtime. In-flight
  requests on the old surface finish consistently on it; the tenant DATA
  migration is still a separate per-tenant step (control-plane `PUT
  /tenants/{id}/schema`), exactly as in single-engine. (Single-engine keeps the
  graceful re-exec of UI-F4-S2 — there are no other apps to protect.)
- **Per-app env covers the full engine Config surface** (FLEET-CONSOLE-S2 closed
  the old gap): `DATABASE_URL`, `JWT_SECRET`, `ADMIN_KEY`, `OBS_DB_PATH`,
  `APPITOOLS_ENV`, the whole file store (`APPITOOLS_FILES_*` — local dir or a
  per-app S3 bucket), the auth knobs (`APPITOOLS_AUTH_*`), **OAuth per app**
  (`APPITOOLS_OAUTH_*` including per-provider client ids/secrets), **MFA key/
  issuer** and **CORS** (`APPITOOLS_CORS_*`) — each app is a product with its
  own identity providers, storage and browser origins. What remains
  process-wide (and is **loudly warned** at boot if set per app): the process
  infra — `RATE_LIMIT_*`, `DB_MAX_CONNS`, `GOMAXPROCS`, `REDIS_URL`,
  `SLACK_WEBHOOK_URL`. An app needing those isolated runs under `fleet run`.
- **The app's bare domain is not a tenant**: a request whose Host is exactly an
  app domain (`erp.example.com/admin` — no tenant label) carries no tenant, so
  it no longer records a phantom tenant (`erp`) in that app's observability
  (the S1 finding, fixed). Tenant subdomains resolve exactly as before.
- **No supervisor/status API** (nothing to supervise — one process; a crash is
  everyone's crash, which is exactly the blast-radius trade the design doc
  documents; pin risky apps to their own process with `fleet run`).
- **Pools**: each app opens its own pgx pool (`DB_MAX_CONNS` applies per app) —
  budget N × pool size against your Postgres `max_connections`.

## The unified fleet console (`/fleet`, MT-STRUCT-S5)

`fleet serve` ships **one face over the N apps**: a read-only console at
`GET /fleet` on the **process level** — the same handler that serves the
health probes on a bare Host (an app domain never serves it; there the request
goes to the app, which answers 401/404 like any unknown path). It shows, per
app: domains, **live** resources (hot-swaps reflected), tenants, the hot-swap
count, and **latency by (app, tenant)** — plus links into that app's own
Studio (`/editor`), admin (`/admin`) and `/docs` on its domain. Choosing an
app = entering its surface: the per-domain surfaces ARE the app-scoped
consoles (Studio loads that app's schema and deploys to it via the S4
hot-swap), and the fleet console is the level above them. Sober tokens, light
and dark.

**Unified operator identity (FLEET-CONSOLE-S2):** set `operator_admin_email` in
the manifest (or `APPITOOLS_FLEET_ADMIN_EMAIL`) plus the
`APPITOOLS_FLEET_ADMIN_PASSWORD` env var — the password is deliberately NOT a
manifest key, so the manifest stays committable — and the runtime ensures a
platform super-admin with those credentials exists in **every** app's database
(idempotent at boot and on hot-add; an existing account is never overwritten).
Result: **one login opens every app's `/admin`** — the console header names it —
without weakening the S3 isolation (each app keeps its own admin row, own DB,
own tokens; there is still no cross-app token). `make fleet-init` generates all
of it. Declaring the email without the password env fails the manifest load
loudly (never a silently-disabled feature).

**Fleet-operator auth (the taxonomy):** the console is gated by
`operator_key` in the manifest (or `APPITOOLS_FLEET_OPERATOR_KEY`) — the
credential of the **server owner**, one level above the apps. It is validated
at manifest load to be distinct from every app's `ADMIN_KEY`/`JWT_SECRET`;
a missing or wrong key gets a **uniform 404** (the console is not
fingerprintable), and leaving it empty **disables** the console (safe
default). The fleet key opens **no** app API — per-app JWT/RBAC/admin auth
still applies underneath — and no app credential opens the console: the S3
isolation is not bypassable from this level. Like `/metrics`, treat the
console as an internal surface (firewall it or front it with your proxy).

**Observability by (app, tenant):** S3 gave each app its OWN observability
stack keyed by tenant, so the (app, tenant) dimension exists structurally —
the console namespaces each app's per-tenant snapshots with zero re-keying
and zero hot-path change. The same tenant id in two apps shows independent
counters (verified live). *Memory at scale:* the cost is one ring/histogram
set per (app × tenant) — the familiar per-tenant cost, multiplied by apps;
with tens of apps × tens of active tenants this is fine, with hundreds ×
hundreds budget accordingly (the rings are fixed-size; idle tenants age out
of relevance but not memory until restart).

**Editor (Studio) awareness:** `/admin/served-resources` reports
`activation: "hot_swap"` in this runtime (vs `"restart"` in single-engine),
and the deploy UI adapts — new resources show "Activates after deploy
(hot-swap)" and a one-click **"Activate now (hot-swap)"** that swaps only
that app, with no downtime and no drain wait, then verifies the resources
are served.

**DevHub:** every in-process app runs its own control plane (with the full
`/debug` observability surface) on its own port — register each one as a
server in DevHub's existing multi-server registry to navigate N apps; no
DevHub coupling to the engine.

## Database assistance in Add-app (FLEET-DB-ASSIST)

Creating an app needs a `DATABASE_URL`. The console's Add-app form assists with
it at three levels, on ONE security principle: **the engine never scans the
system, Docker or the network to discover Postgres.** Everything comes from what
the operator explicitly declares — a `db_instances` list — and credentials never
reach the browser (the console names an instance; the server holds the DSN).

**1 — Test connection** (always available). Probe a DSN (or an instance +
database name) and get an actionable verdict: connects & the database exists
(+ server version, + whether the role can create databases), the database
doesn't exist yet (the cue to create it), auth failed, or the server is
unreachable. Pure probe — zero administrative effect.

**2 — Suggest the DSN from a declared instance.** Pick an instance from the
operator's declared list; the server derives the app's runtime DSN (host +
credentials from the instance, database name `app_<name>`) — no DSN typed from
memory. The derived DSN, with its credentials, is computed and stored
server-side; the browser only ever sends the instance NAME and the database name.

**3 — Create the database** (explicit checkbox, only for instances with admin
credentials). On add, the server runs `CREATE DATABASE` on that instance with
the declared privileged DSN — bounded to `CREATE DATABASE` (no other power), it
warns-and-reuses if the database already exists (never overwrites), it is logged
(audited), and it is **all-or-nothing**: a database this add creates fresh is
dropped again if the app-add then fails, so a failure leaves the server exactly
as it was.

### Declaring instances (secure by design)

`db_instances` in the manifest is **committable** — it holds no credentials,
only each instance's name/label and the NAME of the env var carrying its
privileged DSN. The DSN itself (a role that may `CREATE DATABASE`, pointing at a
maintenance database like `postgres`) lives in the gitignored env-file.

```json
{
  "operator_admin_email": "operator@fleet.local",
  "db_instances": [
    { "name": "local", "label": "Local Postgres", "admin_dsn_env": "APPITOOLS_FLEET_DB_LOCAL_ADMIN" }
  ],
  "apps": [ … ]
}
```

```bash
# in fleet-secrets/fleet.env (gitignored) — the privileged DSN, → the postgres db:
APPITOOLS_FLEET_DB_LOCAL_ADMIN=postgres://appuser:secret@localhost:5432/postgres?sslmode=disable
```

- **`make fleet-init` scaffolds this automatically** when it knows a base
  `DATABASE_URL`: a `local` instance in the manifest + the admin DSN in
  `fleet.env`, so the console can create databases out of the box.
- **Declared-but-unwired fails loud**: an instance whose `admin_dsn_env` is unset
  rejects the manifest at load (never a silently-powerless instance) — same
  contract as `operator_admin_email` + its password env.
- **No instances declared ⇒ clean degradation**: the form is manual-DSN + Test
  connection only. Test needs no stored credentials (it probes the DSN the
  operator typed), so it always works.
- **The database is NOT on the app's own server credentials by necessity** — it
  is on whichever instance the operator declared, which may be this box, another
  instance, or a managed Postgres in the cloud. The instance's DSN is the only
  authority the console has, and only for what was declared.

Console-added apps now write their secrets (DATABASE_URL / JWT_SECRET /
ADMIN_KEY, including a server-derived DSN) to a per-app **env-file** under the
data dir (0600) referenced by `env_file` — never inline in the committable
manifest.

## App lifecycle — `fleet add / remove / list` (FLEET-LIFECYCLE-S1)

Adding or removing an app is a command (or a console action), not a manual
manifest edit + relaunch. Against a running `fleet serve` the operations are
**HOT** — they expose the registry's copy-on-write `AddApp`/`RemoveApp` (built
in MT-STRUCT-S4): the process is not restarted and the other apps are never
touched (verified live: an app under continuous load saw 200/200 while a third
app was added).

```bash
appitools fleet list   --config fleet.json     # LIVE inventory (or the manifest, if not running)
appitools fleet add    --config fleet.json --name optica --schema optica.json \
    --domain optica.example.com \
    --env DATABASE_URL=postgres://… --env JWT_SECRET=… --env ADMIN_KEY=…
appitools fleet remove --config fleet.json --name optica --yes
```

- **Validation before anything goes live** — the same rules a fleet boot
  enforces, so a hot add can never create a composition the next start refuses:
  the schema through the real validator (`ValidateReport` — an invalid schema
  is rejected with the located error report), unclaimed domains, unique name,
  required per-app env, the per-app `JWT_SECRET` rule, and the operator-key
  separation.
- **Manifest ↔ live coherence, no drift.** The manifest (`fleet.json`) is the
  persistent source of truth; the registry is the live state. A lifecycle
  operation persists the manifest FIRST (atomic file replace, surgical edit —
  untouched fields survive verbatim) and then publishes the live change, so a
  fleet restart always reloads exactly the post-operation composition
  (verified live: an app added hot still serves after a full fleet restart).
- **How the CLI reaches the live fleet:** the operator-gated console API on the
  process-level Host (`POST /fleet/api/apps`, `DELETE /fleet/api/apps/{name}` —
  the S5 channel, `X-Fleet-Key`). The CLI probes `/health` for the runtime's
  `fleet_apps` marker; wrong/missing operator key is an ERROR, never a silent
  manifest-only fallback. Without a live fleet (or under `fleet run`, which has
  no hot lifecycle) the commands edit the manifest for the next start.
- **Remove semantics (deliberate):** removing an app takes it OUT OF THE FLEET
  — its domains stop serving (clean 404, immediately), its background services
  stop, its infra is released after a short drain grace — but **its database
  is NEVER touched**. Re-adding the app with the same `DATABASE_URL` restores
  it intact. Destroying data is outside the fleet's vocabulary. The CLI
  requires `--yes` (and prints exactly this) and the console a typed
  name confirmation.
- **The console** (`/fleet`, fleet-operator key) exposes the same lifecycle:
  **Add app** pastes the schema JSON directly (the natural handoff from
  Studio's Code view or an external agent — see docs/SCHEMA_SPEC_LLM.md) plus
  domains and the app's env; **Remove** sits on each app card behind the typed
  confirmation. An added app's pasted schema is persisted under
  `<data_dir>/<app>/schema.json` and the manifest references it.
- **Isolation is unchanged**: an added app is compiled through the exact same
  per-app path as boot (own pool/secret/policy/caches); the S3 cross-app
  matrix holds after an add (smoked live: a cross-app JWT is still a 401, the
  operator key still opens no app API).

### Editing an existing app's env — "Edit env" (FLEET-EDIT-S1)

Add app/Remove app cover creating and destroying membership; **Edit env**
covers the third case — changing an app **already in the fleet** (e.g. adding
`APPITOOLS_GRAPHQL_PLAYGROUND=on` to one app without touching its siblings),
hot, no restart, console-only (no CLI subcommand yet — see the note below).

Each app card in `/fleet` has an **"Edit env"** button: a panel to `set` new/
changed keys and `unset` others, submitted as
`PATCH /fleet/api/apps/{name}` `{set_env, unset_env}` (operator-gated, same as
Add/Remove). It is **write-only by design** — existing secret VALUES
(`DATABASE_URL`/`JWT_SECRET`/`ADMIN_KEY`) are never read back to the browser;
rows start blank, and only a key you type is changed.

- **Not a remove-then-add.** A naive "remove, then re-add" would open a race
  window (another operator's add/remove interleaving between the two calls)
  and briefly 404 the app's domains. Edit takes the SAME lifecycle lock
  (`fleetLifecycle.mu`) as Add/Remove and, in one hold: merges the change into
  the app's CURRENTLY-resolved env, re-validates the same rules a boot would
  (`DATABASE_URL`/`JWT_SECRET`/`ADMIN_KEY` required, JWT_SECRET unique across
  apps, distinct from the operator key), compiles a FRESH `*App` instance off
  to the side (a bad new value — an unreachable `DATABASE_URL` — fails HERE,
  before anything is swapped; the OLD instance keeps serving untouched), then
  atomically takes over the app's domains with the SAME `Registry.SwapApp`
  primitive a schema hot-swap uses. The old instance drains for the same grace
  period a remove gives (in-flight requests on the old surface finish; a
  long-lived SSE stream on the edited app is cut when the grace ends) before
  its pool/control-plane listener are released.
- **Not gated by a typed-name confirmation** like Remove — editing env isn't
  destructive (nothing in Postgres is touched, and the previous env is not
  deleted, only superseded in this process's live config).
- **Manifest ↔ live coherence, same discipline as Add/Remove:** the app's full
  new env is written to its OWN env-file under `<data_dir>/<app>/<app>.env`
  (never inline in `fleet.json` — secrets stay out of the committable
  manifest) and the manifest entry is persisted FIRST, surgically, before the
  live swap — a restart after an edit reloads the exact post-edit env.
- A **live finding this feature surfaced**: the shared secrets-file helper
  used to colocate the generated `.env` file with wherever `spec.Schema`
  pointed — safe for a console-added app (its schema is always the persisted
  `<data_dir>/<app>/schema.json` copy) but WRONG for editing an app whose
  schema is a plain external path (a `schemas/` file, or any hand-added
  manifest entry) — it would write the generated secrets file into that
  external directory. Fixed: the file always lands under
  `<data_dir>/<app>/`, regardless of where the schema lives.
- **No CLI yet.** `appitools fleet add/remove/list` exist; there is no
  `fleet edit` subcommand — env edits are console-only today. A CLI
  counterpart would call the same `PATCH` endpoint and is a natural, small
  follow-up if you want it scripted.

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
