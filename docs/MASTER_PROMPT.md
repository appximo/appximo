<!-- The Appximo MASTER PROMPT (LAUNCHPAD-S1). This file IS the prompt: it is
     embedded verbatim into the binary (`appximo prompt`) and pasted verbatim
     into coding agents by users. Everything below this comment is the paste.
     Keep it agent-facing: imperative, checklist-driven, no marketing. Every
     line exists because a real user's agent stumbled without it — see
     docs/FIELD_FEEDBACK_RESPONSE.md before removing any. -->
You are going to build me a complete, working application on **Appximo** — an
engine that compiles a JSON schema into a multi-tenant REST + GraphQL +
OpenAPI server with an embedded admin panel (`/admin`), visual schema editor
(`/editor`), generated back-office (`/app`) and interactive docs (`/docs`).
One static binary + PostgreSQL. You will take it from my idea to a running
app, and — if I ask for it — to production on a real domain with HTTPS.

MY IDEA: <describe the app in one or two sentences>

## Rules of engagement (read first, they override your habits)

- Ask me ONLY the question block below, all questions together, before doing
  anything. After I answer, make every remaining decision yourself with
  sensible defaults and **do not ask me anything else** — if something fails,
  read the error (Appximo errors name the problem and the way out) and fix it.
- A step is DONE when its checklist item passes, not when a command exits 0.
  Verify each item with a real request and show me the evidence.
- The engine prints its own contracts — when you need exact syntax, run the
  matching command instead of guessing, and **never invent API surface**:
  - `appximo spec` — the schema grammar (types, relations, state machines, RBAC)
  - `appximo backend-spec` — custom Go handlers, hooks, background jobs
  - `appximo frontend-spec` — the API contract a UI consumes (auth, filters,
    pagination, uploads, error→screen map)
  - `appximo backoffice-spec` — a CRUD admin UI generated from /openapi.json
  - `appximo quickstart` — OPERATING it: tenants, users, migrate, production
- Never serve any part of this app from a second server or port: the engine
  serves API, frontend, admin and docs from ONE binary, same origin. If you
  are tempted to run `npx serve`, `python -m http.server` or a Node server
  next to it, you took a wrong turn — go to "Custom screens" below.
- Never write to the database with raw SQL. Schema changes go through
  `appximo validate` → `appximo migrate` (Act 2 §3); data changes go through
  the API.

## Question block — the ONLY questions, asked together, then silence

1. **Postgres**: do you have a connection string I should use, or may I start
   PostgreSQL 16 in Docker locally?
2. **Production**: do you want this on the internet with HTTPS now? If YES,
   give me: (a) the domain (and confirm you can edit its DNS records),
   (b) SSH access to an Ubuntu VPS (`user@host`), and (c) whether anything
   else already runs on that box. If NO, I stop after Act 1 and print what
   production will take when you're ready.

# ACT 1 — from the idea to a running app, locally

1. **Check the engine is installed AND current enough** — do not install it
   here, and do NOT accept "a version prints, therefore we're fine": an old
   binary is the usual state and it fails later, in ways that read like typos.
   Run both:

   ```
   appximo version     # must print a version
   appximo prompt      # must print a long prompt, NOT "unknown command"
   ```

   If either fails, **stop and tell me to run the install prompt first**
   (`appximo prompt --install`, or the "Install Appximo" block on the
   website). Do not work around it, do not build from source, do not proceed
   with an older binary.
2. **Write the schema from MY IDEA**:
   - `appximo spec > /tmp/appximo-spec.md`, read it, then write `schema.json`
     using ONLY that grammar.
   - Correction loop: `appximo validate --json schema.json`, fix every entry
     it reports, repeat until `"valid": true` **and `warnings` is empty**.
     Warnings are real bugs waiting to happen — two you will likely hit:
     a `required` string field also needs `"minLength": 1` (an empty string
     satisfies `required`), and a role that writes a resource with a `file`
     field also needs a grant on the `files` resource (or uploads 403).
   - If any part of the app must be readable WITHOUT login (a public catalog,
     published posts, a landing's data), declare it in the schema's
     `rbac.public` block (it's in the grammar) — do not invent an "anonymous"
     role and do not proxy around auth.
3. **Boot everything with ONE command**:
   `appximo up --name <shortname> --schema schema.json --yes --json`
   - stdout is one JSON object: every URL, one-time admin credentials, a dev
     API token, and a smoke-test result. Save the credentials for me.
   - The name must match `^[a-z][a-z0-9]{1,29}$` (no hyphens/underscores).
   - The printed token and the first user carry the **most privileged role
     your schema declares** (`token_role` in the JSON). To act as any OTHER
     role — to prove a restricted role really is restricted — mint one:
     `appximo token --secret "$JWT_SECRET" --tenant <name> --role <role> --schema schema.json`
     (the secret is in the `.env` `up` wrote).
   - Re-running `up` after editing schema.json is safe: it migrates the
     tenant to the new schema (destructive drops stay gated and print the
     exact approval command).
4. **Prove it with real requests.** The tenant is addressed by Host header:
   use the printed `http://<name>.localhost:<port>` URLs, or add
   `-H 'Host: <name>.localhost'`. Filters need `curl -g`.

**ACT 1 CHECKLIST** — verify each, then show me the table with evidence:

- [ ] `appximo validate schema.json` → valid, zero warnings
- [ ] `GET /docs` → 200
- [ ] `POST /api/<main resource>` with a token whose role may write it → 201
      (the printed token, or one minted for the right role — see step 3)
- [ ] The filtered list (`?filter[...]`) returns the record just created
- [ ] Anonymous access behaves as declared: if the schema has an `rbac.public`
      block, that resource reads with NO token (200) while a non-public one
      does not (401/403). No public block? Say **N/A** and prove the negative
      (a tokenless read is 401) — never leave this row silently unchecked
- [ ] `GET /app` → 200 (the generated back-office; sign-in works with the
      printed credentials at the `<name>.localhost` URL)
- [ ] `appximo explain schema.json` reads back as MY IDEA — paste its output
      for me so I can confirm the rules are what I meant

Then STOP and show me: the URLs, the one-time credentials, one curl that
already works, and the explain output. **Call out every place you extended
my idea** (a state, a field, a role I never named) in one short list, so I
can confirm or reject it before it reaches production. If I asked for
production, continue.

## Custom screens or endpoints (only if MY IDEA needs them)

- **Your own frontend** (brand, screens): build a STATIC SPA (no SSR) and
  serve it from the same binary — two ways, pick one, never a second server:
  - No Go toolchain: `appximo serve --schema schema.json --static ./dist --spa`
  - Go route (one binary containing everything): `appximo init <name>` emits a
    compilable project (main.go + go:embed) on `go get github.com/appximo/appximo`.
  The UI's exact API contract is `appximo frontend-spec` — read it before
  writing fetch calls (tenant Host, 422 shape, keyset pagination, uploads).
- **Custom endpoints** (checkout, signed webhooks, reports): read
  `appximo backend-spec`; handlers run in-process with `Ctx` (validation +
  RBAC + transaction included). Grant the route to roles with the RBAC
  `routes` block — and know that the plain `appximo` binary refuses a schema
  granting routes nothing registers: that schema belongs to YOUR binary.

# ACT 2 — production with HTTPS (only if I said yes)

0. **DNS first** (propagation takes time — start it before touching the VPS):
   an A record `<app>.<domain>` → the VPS IP, and a wildcard
   `*.<app>.<domain>` → same IP if tenants get their own subdomains. The
   tenant id must EQUAL the first DNS label it is served at.
1. **Put the binary on the VPS**: `scp` the SAME binary you ran locally (or
   your custom one from "Custom screens") to the VPS, plus your
   `schema.json` — any file transfer works, `scp` is just the usual one.
   Then fetch the installer AND its companion scripts into the same
   directory on the VPS (the installer only installs the companions it finds
   NEXT TO ITSELF — fetched alone, backups and updates have no script):
   ```
   base=https://raw.githubusercontent.com/appximo/appximo/main/scripts
   curl -fsSLO $base/install.sh -O $base/backup.sh -O $base/restore.sh -O $base/deploy-update.sh -O $base/fleet-audit.sh
   ```
2. **Install** (as root). One script covers both box states:
   - Empty box:
     `sudo bash install.sh --domain=<app>.<domain> --email=<your email> --binary=./appximo --schema=schema.json --harden --yes`
   - Box that ALREADY runs something (or a second Appximo app): add
     `--app=<name>` — everything (service, user, config dir, db, ports) is
     namespaced under that name next to what's there, untouched. If ports
     8090/9090 are taken, add `--port`/`--control-port`.
   - Custom binary from Act 1's Go route: pass it as `--binary=` and add
     `--cli=./appximo` (the stock engine as ops companion — your binary
     serves, the CLI operates: migrate, token, backup).
   - It installs native PostgreSQL, a systemd unit, and Caddy with automatic
     Let's Encrypt. It prints every name it created and where config lives
     (`/etc/<name>/<name>.env`). Read the summary; don't re-derive paths.
   - **It is idempotent**: if it stops with a named error, fix exactly what
     it named and run the SAME command again — it detects and reuses
     everything it already created. Never start over by hand.
   - If the certificate never issues, it is almost always DNS not yet
     pointing here or port 80 blocked — `dig +short <app>.<domain>` must
     return this box's IP, and `journalctl -u caddy -f` says the rest. Wait
     for DNS rather than working around TLS; **never** verify with `-k`.
3. **First production tenant + admin** (on the VPS — the control plane is
   localhost-only by design, never exposed):
   - Register the tenant WITH the schema in the body:
     `curl -s -X POST http://localhost:<control-port>/tenants -H "X-Admin-Key: $ADMIN_KEY" -H 'Content-Type: application/json' -d "{\"tenant_id\":\"<app>\",\"display_name\":\"<App>\",\"schema\":$(cat schema.json)}"`
     (ADMIN_KEY and the control port are in `/etc/<name>/<name>.env`.)
   - First admin: open `https://<app>.<domain>/admin` — the login screen
     offers "Create the first admin" (paste the ADMIN_KEY) — or run
     `appximo admin create --email … --password …` on the VPS.
   - Mint a token and create one real record over HTTPS.
4. **Every future schema change** (now and forever): edit schema.json →
   `appximo validate` → `appximo migrate --tenant <app> --schema schema.json --dry-run`
   → run it again without `--dry-run` to apply. New fields go live hot; a new
   resource needs a service restart (`systemctl restart <service>` after
   updating the schema file the unit points at). Keep the deployed schema and
   the tenant record in sync by ALWAYS going through `migrate`.

**ACT 2 CHECKLIST** — verify each, then show me the table with evidence:

- [ ] `curl https://<app>.<domain>/health` (NO `-k`) → 200 `{"status":"ok",…}`
      — real certificate, valid chain
- [ ] `POST /api/<main resource>` over HTTPS with a fresh token → 201
- [ ] `https://<app>.<domain>/app` → 200 and lists that record after sign-in;
      `/docs` → 200
- [ ] `systemctl is-active <service>` → active, and
      `systemctl is-enabled <service>` → enabled (survives reboot)
- [ ] `journalctl -u <service> -n 50` shows a clean boot (no errors)
- [ ] Backups work: run `/opt/<name>/scripts/backup.sh --env-file=/etc/<name>/<name>.env`
      once and show the dump file it produced (if the installer reported it
      couldn't find the companion scripts, fetch them as in §1 first)

Deliver at the end: the live HTTPS URLs, credentials (shown once), where
config and backups live on the VPS, and the exact three commands for my next
schema change (validate → migrate --dry-run → migrate).

## When something fails

**If the Postgres I gave you is unreachable** (it times out, DNS does not
resolve, a firewall drops it): that failure is the network, not Appximo, so
its errors cannot help you. Do not silently change the connection string I
gave you and do not disable anything — fix the reachability at your end if
you can (a hosts entry, the right address for this machine), and say in one
line what you changed. If you cannot, stop and tell me the exact address that
did not answer.

Read the error first — this engine's errors are written to be acted on: they
name the missing thing and the way out. Fix exactly what is named and re-run
the same command (every installer and `up` is idempotent). If a step fails
twice, run `appximo quickstart` and search its output for the symptom before
improvising. Three dead ends to never take: a second
server/port for the frontend, raw SQL against the database, and disabling
auth "temporarily". If the box refuses something (a port, a permission), the
installer summary and `journalctl -u <service>` name the owner — fix the
named thing, don't work around it.
