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
`--app=NAME` (a second app on the same box, §2b) `--yes` (non-interactive)
`--harden` (ufw + fail2ban + unattended-upgrades) `--scripts=DIR` (where
`deploy-update.sh`/`backup.sh`/`restore.sh` live; default: next to the
installer) `--backup-schedule=CAL` (the nightly backup timer's `OnCalendar`,
default 03:30; `--no-backup-timer` to opt out — §4)
`--internal-tls` (Caddy issues a local certificate — a LAN/staging box whose
domain is not public) `--dry-run` (generate configs + print the plan, change
nothing).

**The installer verifies what it installed** and fails loudly on a mismatch,
even with the service up: the installed binary's sha256 against `--binary`,
the running service's `/health` version against that binary — locally **and
through Caddy** (a site that proxies to a neighbour's port answers with the
neighbour's version) — and the schema on disk against `--schema`. The summary's
`✓ verified — installed == asked` block lists what was checked.

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

**Prerequisites either way:** a VPS (1 vCPU / 1 GB is enough **to serve**), a
domain with an `A`/`AAAA` record pointing at it, and ports **80 + 443**
reachable (Let's Encrypt's HTTP challenge needs 80; TLS serves on 443). Keep
everything else — including the engine's own port and the control plane on
9090 — off the internet.

**RAM and swap — read this before loading data.** The engine itself idles at
~70–80 MB; the memory that grows under a bulk load (a migration, an import, a
big schema change) is **PostgreSQL's**, and on this stack PostgreSQL is
**shared by every app on the box**. Measured in the field (2026-08): a 957 MiB
box with **no swap** serving five apps took a 46k-row migration through one of
them; the kernel could not page anything out and OOM-killed PostgreSQL —
`postgresql@14-main.service: Failed with result 'oom-kill'` — and **all five
apps went down**. The same load on the same box was absorbed once 2 GB of swap
existed. So: **give a box of ≤ 2 GiB swap before you load data** —

```bash
fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab && sysctl -w vm.swappiness=10
```

The installer detects RAM and swap and **warns loudly** (never blocks) when a
small box has none. The engine, for its part, refuses NEW writes with an
explained `503` when `MemAvailable + SwapFree` drops under a floor
(`APPXIMO_MEMORY_GUARD_MIN_MB`, §8) — that is **degradation, not capacity**: it
keeps the failure from being silent, it does not make the box hold the load.
Load in batches (`POST /api/transaction`, ≤ 100 ops), pause on `503`/`429`.

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

**The one command (DEPLOY-FLOTA-S1) — from your machine, the whole
protocol, verified from outside, rolled back by itself:**

```bash
scripts/deploy-app.sh --host=root@BOX --app=appximo --binary=/tmp/appximo \
  --url=https://api.example.com [--cli=/tmp/appximo] [--keep=/var/backups/appximo/golden.dump]
```

[`scripts/deploy-app.sh`](../scripts/deploy-app.sh) runs over SSH what an
operator used to do from memory — and a step nobody remembers is a step that
eventually gets skipped: (0) the deployable contract (`<binary> version`
answers, and what it prints is what `/health` must say afterwards); (1) the
inventory of the app ON the box from systemd — unit, binary path, port,
schema — never guessed, so a hand-installed app keeps its own names; (2) a
full backup SET first (§4.1) — a backup that fails ABORTS the deploy before
anything is touched — plus copies of the binary/env/schema about to be
replaced (`/root/<app>-{bin,env,schema}.pre-<version>`); (3) the swap through
`deploy-update.sh` below; (4) **verification from OUTSIDE, over the public
URL, as a client would**: `/health` through the proxy says the expected
version (a site proxying a neighbour's port answers with the neighbour's
version), `/readyz` is 200, an **authenticated read** with a token minted on
the box by the app's own secret (never printed) → `GET /api/<resource>` →
200 with data, and a **write probe that changes nothing by construction** —
a one-op `POST /api/transaction` deleting a uuid that does not exist answers
`404 failed_operation=0` only after authenticating, authorizing the delete,
opening the tenant transaction and running the statement, so the write path
executed and rolled back with zero rows moved. **Any failure → automatic
rollback to the pre-deploy binary → the same verification again**, and the
outcome is reported as what it is (exit 1 = rolled back and re-verified; exit
2 = the rollback did not recover, a human now). (5) `--keep` md5's a file
that must not change (a golden dump) before and after. (6) `fleet-audit.sh
--app=<app>` at the end — a box left with gaps is exit 3 even though the
deploy succeeded, with the ✗ lines saying what to fix. **A `/health` 200 is
not a verified deploy**; the script's verdict is the read + the write probe
+ the version through the proxy, or a verified rollback. Drilled on the lab
box both ways: a good binary (17 s, exit 0) and a binary that boots and
answers `/health` but 500s every data-plane request — a health-only check
would have blessed it; the read probe failed at 15 s, the rollback ran, the
old binary was re-verified from outside at 23 s (exit 1). Lab/staging flags:
`--tenant-host`, `--resolve=IP`, `--insecure`.

The building block it wraps — **build → copy → atomic swap → restart** —
is [`scripts/deploy-update.sh`](../scripts/deploy-update.sh) (which also
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
`--binary=` does the same swap + restart, so either path is a safe upgrade —
under a **written criterion** (the script's header says the same):

| On a re-run over an existing app | |
|---|---|
| **KEPT** on purpose | secrets (every issued token stays valid), the database and its data, the data dir, the control port |
| **ALWAYS REPLACED** | the binary, the systemd unit, the env file layout (same secrets), this app's Caddy site, the companion scripts |
| **The schema** | replaced when `--schema` is given; **KEPT** when it is not — and a kept schema is verified to be THIS app's: one byte-identical to, or carrying the `name` of, another app's `/etc/<other>/schema.json` **stops the install**, naming that app. It used to be kept silently, which once left another application's resources answering `200` under a new domain. |

The summary prints the kept schema's `name`; the "Update" line it prints
omits `--schema` on purpose (your model is kept) — pass it when the model
changed.

> `deploy-update.sh` and `backup.sh` are placed in `/opt/appximo/scripts/` by
> the installer when they sit next to `install.sh` (a checkout, or scp'd
> together — their exec bit does not matter) or under `--scripts=DIR`. Under
> `curl | bash` there are no sibling files to copy, so fetch them from the repo
> into that directory when you need them; the verification block says which
> ones are installed and executable.

**What activates when.** A per-tenant migration (new column) is live immediately.
Anything compiled at boot — new resources, validation rules, GraphQL fields,
hooks, `/docs` — activates on the restart with the new schema. See
[docs/MENTAL_MODEL.md](MENTAL_MODEL.md).

---

## 4. Backups, restore and recovery — the promise, measured

> **A backup you have never restored is not a backup; it is a folder.** Every
> number in this section was MEASURED in RESILIENCIA-S1 (2026-08-30) on a box
> installed with `install.sh` — a 2 vCPU / 2 GB DigitalOcean droplet, the box a
> customer buys — holding a realistic dataset (**251 248 rows, 124 MB, 23
> tables, 3 uploaded files**; the migration-scale set the capacity laboratory
> uses). Nothing here is estimated. The evidence (logs, timings, screenshots)
> is in the internal package under `evidencia/RESILIENCIA-S1/`.

### 4.1 What a backup is (one SET, four files)

`install.sh` installs [`scripts/backup.sh`](../scripts/backup.sh) and
[`scripts/restore.sh`](../scripts/restore.sh) into `/opt/<app>/scripts/` and
writes **`<app>-backup.timer`** — a nightly backup at **03:30** (`--backup-schedule`
to change it, `--no-backup-timer` to opt out). One run writes one **set** with
one stamp into `/var/backups/<app>/`:

| file | holds | why it is in the set |
|---|---|---|
| `<app>-<stamp>.dump` | the whole database: every tenant schema + the control plane (`pg_dump -Fc`, compressed) | the data |
| `<app>-<stamp>.files.tar.gz` | the uploaded files (`APPXIMO_FILES_DIR`) | the database holds only their **metadata**; without the bytes a restored app answers 404 on every attachment while the rows claim they exist |
| `<app>-<stamp>.conf.tar` (0600) | `/etc/<app>`: the env (**`JWT_SECRET`**, `ADMIN_KEY`, ports, paths, tuning) and the boot schema | a new box cannot regenerate these: every issued token, every TOTP enrolment and the platform MFA are bound to the JWT secret |
| `<app>-<stamp>.manifest` | exact per-table row counts, sizes, the dump's sha256, the files count | what `restore.sh` VERIFIES the restored database against — count by count |

Plus `last-backup.status` (`ok …` / `failed …`), which the engine's self-monitor
reads (§4.6). **14 sets are kept**; the sets of one stamp are pruned together.

**Measured on the reference box: a backup takes 7.3 s** (dump 38 MB in 6 s +
files + conf + manifest); `Nice=10` / `IOSchedulingClass=idle`, so the app
does not feel it. A failed run leaves **no partial file**, writes `failed` to
the status file and posts ONE message to the Telegram chat
(`APPXIMO_TELEGRAM_BOT_TOKEN`/`_CHAT_ID`, in Spanish) and/or
`SLACK_WEBHOOK_URL` if the env has them (§4.6c).

**Off-box copy — set it, or the backup dies with the disk.** A backup on the
disk that holds the database is one failure away from gone: a dead host takes
both. In `/etc/<app>/<app>.env`:

```bash
BACKUP_COPY_TO=user@otherbox:/var/backups/myapp     # scp to a box you own …
BACKUP_COPY_TO=spaces:mybucket/myapp                 # … or an rclone remote (DO Spaces, S3, R2, B2)
BACKUP_PASSPHRASE_FILE=/root/.backup-passphrase      # 0600; the conf bundle (secrets) goes ONLY encrypted
```

Every set is then copied after it is written (measured: a 38 MB set to another
box over the private network added under a second to the run). **Without `BACKUP_PASSPHRASE_FILE` the conf bundle
never leaves the box** — the dump and the files still do, and the run says so
every night. Keep the passphrase in your password manager, not on the box: it
is what turns the encrypted bundle back into your secrets on the day the box
is gone. Where it should live, and what it costs (DO list prices, 2026-08):

| destination | cost | what it survives | what it does not |
|---|---|---|---|
| the same disk (the default when nothing is set) | $0 | a corrupt database, a bad migration, a deleted tenant | **the disk, the host, the account** |
| another VPS you already run (`scp`) | $0 if it exists, else from $4/mo | the host | the provider/account, a fire in the region |
| **DO Spaces / S3-compatible object storage (`rclone`)** — recommended | **$5/mo for 250 GB** (a year of nightly sets of this app is ~15 GB) | the host, the region if you pick another, accidental deletion if versioning is on | the account, unless the bucket is in another one |
| a second provider (Backblaze B2 via `rclone`) | ~$0.006/GB/mo → cents | the provider | — |

A one-liner to set up the recommended one: `apt-get install rclone && rclone
config` (an S3-type remote named `spaces` with your Spaces key), then the two
lines above and `systemctl start <app>-backup.service` to see the first copy
land.

### 4.2 The two numbers: how long it takes to come back, how much is lost

Two scenarios, because they cost differently. Both were executed, timed stage by
stage, and verified in a browser — not "pg_restore exited 0".

**Scenario 1 — the database is corrupt or destroyed, the box is fine.** The
heap of the biggest table (`ordenes`, 60 000 rows) was overwritten with random
bytes while PostgreSQL was stopped, then PostgreSQL started again. The engine
kept answering **200** — the first page of results did not touch the damaged
blocks. The nightly `pg_dump` did: `invalid page in block 0 of relation …`,
`backup FAILED` — and since CAOS-S1 the failure NAMES ITS CAUSE (the exact
table, from pg_dump's own stderr) in `last-backup.status` and in the alert,
so the 3 a.m. reader knows it is corruption and which table to restore. With
PostgreSQL data checksums ON (§4.8) the FIRST read of a damaged page is an
ERROR instead of silently served data — the detector moves from "tonight's
backup" to "the next touch". **Either way the failure must reach a human
(§4.6).**

```
sudo bash /opt/appximo/scripts/restore.sh --app=appximo --set=/var/backups/appximo/appximo-20260830-174054
```

| stage | measured |
|---|--:|
| stop the engine (graceful drain — a fixed 5 s while `/readyz` says 503) | 5.0 s |
| restore `/etc/appximo` from the conf bundle (secrets + schema) | 0.1 s |
| drop + recreate the database | 0.5 s |
| `pg_restore` of 251 248 rows / 124 MB, as the service role | 6.5 s |
| restore the 3 uploaded files | 0.0 s |
| start the engine, `/healthz` + `/readyz` green | 0.6 s |
| verify: 23 tables count-for-count against the manifest, files on disk, every FK validated, sequences ahead of their tables, the engine listing its tenants | 0.7 s |
| **total, corrupt → verified back** | **13.6 s** |

Add the human: noticing, opening a terminal, finding the set. **Promise for a
corrupt database on a live box: back in under 15 s of execution once someone
runs the command; a few minutes end to end.** Verified afterwards in a real
browser (the same product list, the same detail, the same counts as before the
corruption) and with a NEW write (`201`).

**Scenario 2 — the box is gone (host dead, account lost, disk fried).**
A fresh droplet, the installer, the set from off-box storage, the restore —
following §4.3 literally, on a clean machine, with a stopwatch:

| stage | measured |
|---|--:|
| create the droplet (DigitalOcean, until SSH answers) | 71 s |
| copy `install.sh` + companions + the binary up | 0.7 s |
| `install.sh` (PostgreSQL + Caddy + the engine, from `apt`, verified) | 150.0 s |
| copy the set to the new box (38 MB over the private network) + the passphrase | 2.1 s |
| `restore.sh --set` (the same stages: stop 5.0 · conf 0.0 · drop/create 0.2 · load 5.5 · files 0.0 · start 0.6 · verify 0.4) | 12.1 s |
| **total, empty machine → verified back** | **≈ 4 min (236 s)** — 165 s of it hands-on |

**plus DNS.** The A record has to move to the new IP; with a 300 s TTL that is
minutes, with a day-long TTL it is a day. **Set the TTL of the app's record to
300 s now, while nothing is wrong** — it is the only part of this number you
cannot buy back later. Caddy issues the certificate by itself the moment the
name resolves to the new box.

**How much is lost — the RPO.** Everything written after the last set. With
the default nightly timer that is **up to 24 h** (on average 12 h): the drill
wrote a row after the last backup and, as promised, it was NOT on the restored
box — the manifest said 42 categories, the live box had 43. The number scales
with the cadence, and the cadence is one line:

| cadence (`--backup-schedule=…` / the timer's `OnCalendar`) | worst-case loss | what it costs on the reference box |
|---|---|---|
| nightly (`*-*-* 03:30:00`, the default) | 24 h | 7 s of `nice` CPU + 38 MB a night; 14 sets = 530 MB |
| every 6 h (`00/6:30`) | 6 h | 4× that: ~2 GB kept, still nothing the app notices |
| **hourly** (`hourly`) | **1 h** | 7 s and 38 MB an hour; keep 48 sets (`BACKUP_KEEP=48`): 1.8 GB. Off-box: 38 MB × 24 a day, cents |
| every 15 min | 15 min | still 7 s each; 14 GB for a week of sets — object storage, not the box |
| **point-in-time (WAL archiving)** | seconds | a PostgreSQL-level setup this script does not do (`archive_command` + base backups, or `pgBackRest`) — the right tool when minutes of loss are unacceptable; see §4.5 |

The engine does nothing between backups that would help; the loss is exactly
the interval. **Pick the interval from the value of an hour of your data, not
from what feels frequent.**

### 4.3 The recovery procedure — for 3 a.m., no memory, no shortcuts

This was followed letter by letter on a clean machine (§4.2 scenario 2) by the
session that wrote it. If a step is wrong, the step is wrong — fix the text,
not the operator.

**A. The app is down or broken and the box answers SSH — restore the last set**

```bash
# 1. see what is wrong (30 s) — most outages are not a restore
systemctl status appximo postgresql caddy --no-pager | head -30
journalctl -u appximo -n 40 --no-pager
curl -s http://127.0.0.1:8090/readyz; echo

# 2. if the DATA is wrong (corrupt table, dropped tenant, bad migration, garbage writes):
ls -lt /var/backups/appximo/ | head          # the newest set is first; pick the stamp BEFORE the damage
cat /var/backups/appximo/last-backup.status  # "ok …" = last night's set is complete

# 3. restore it — the script stops the app, restores secrets+schema+database+files,
#    starts the app and VERIFIES; it prints every stage with its time
sudo bash /opt/appximo/scripts/restore.sh --app=appximo --set=/var/backups/appximo/appximo-<stamp>

# 4. "RESTORE VERIFIED" → open the app in a browser, log in, read one record you know.
#    Anything else → it says what did not match; the app is STOPPED; do not guess — read the message.
```

A named app (`--app=vetapp` at install) uses `/opt/vetapp/scripts/restore.sh
--app=vetapp --set=/var/backups/vetapp/vetapp-<stamp>`. The previous env and
schema are kept next to the restored ones as `*.pre-restore-<stamp>`.

**B. The box is gone — rebuild on a new one**

You need: a new VPS (Ubuntu 22.04/24.04 or Debian 12), the **last set** from
off-box storage (four files), the **passphrase** of the conf bundle (your
password manager), the **binary** you were running (a release, or build it),
and the repo's `scripts/` (`install.sh`, `backup.sh`, `restore.sh`,
`deploy-update.sh`).

```bash
# 1. from your machine: put the installer, the companions and the binary on the new box
scp scripts/install.sh scripts/backup.sh scripts/restore.sh scripts/deploy-update.sh appximo root@NEW:/root/recover/

# 2. on the new box: install the app EMPTY, with the SAME domain and app name it had
#    (secrets are generated fresh here and replaced by the restore in step 4)
ssh root@NEW
bash /root/recover/install.sh --domain=api.example.com --email=you@example.com --binary=/root/recover/appximo --yes
#    (a named app: add --app=NAME; a box without public DNS yet: add --internal-tls, re-run without it later)

# 3. bring the set and the passphrase
mkdir -p /var/backups/appximo && chmod 700 /var/backups/appximo
#    from wherever the sets live — scp from the other box, or: rclone copy spaces:mybucket/myapp/<stamp>… /var/backups/appximo/
scp otherbox:/var/backups/myapp/appximo-<stamp>.* /var/backups/appximo/
printf '%s' '<the passphrase>' > /root/.backup-passphrase && chmod 600 /root/.backup-passphrase

# 4. restore — same command as scenario A, plus the passphrase for the encrypted secrets
BACKUP_PASSPHRASE_FILE=/root/.backup-passphrase \
  bash /opt/appximo/scripts/restore.sh --app=appximo --set=/var/backups/appximo/appximo-<stamp>

# 5. "RESTORE VERIFIED" → move the DNS A record to the new IP. Until it propagates the app
#    is unreachable at the old address; Caddy issues the certificate when the name lands here.

# 6. in a browser: log in with the OLD credentials (they came back with the database), read one
#    record. Then run one backup by hand and check the copy lands off-box:
bash /opt/appximo/scripts/backup.sh --app=appximo && cat /var/backups/appximo/last-backup.status
```

Users' passwords, MFA enrolments and issued tokens all keep working: the
password hashes are in the database and the JWT secret came back with the conf
bundle. What is NOT back: anything written after the set (§4.2), and the
observability history (`obs.db` — traces, not data; it starts empty).

### 4.4 What comes back on its own, and what does not (verified by provoking it)

Every case below was PROVOKED on the reference box, not read from a unit file.

| what happens | what the box does by itself | measured | needs a human? |
|---|---|---|---|
| the engine process dies (`kill -9`) | systemd restarts it (`Restart=always`, `RestartSec=2` — it was 5: the same kill measured 5.6 s / 103 failed) | back in **2.96 s**; a 20 rps client saw **52 of 547** requests fail in a 2.8 s window (through Caddy those are 502s) | no |
| the box reboots (kernel update, provider maintenance) | everything comes up in the right order: `postgresql@16-main` (17.6 s after the kernel), then the engine 40 ms later, Caddy, the backup timer; `NRestarts=0` | **25.8 s** of outage seen by a 10 rps client (200 failed requests); SSH back after 28 s | no |
| **PostgreSQL is slow to start** (crash recovery, a big WAL replay, a slow disk) | the engine **waits** — the unit is ordered after the PostgreSQL instance, whose `Type=forking` start completes only when it accepts connections | provoked with a 60 s delay in the instance's start: the engine started 50 ms after PostgreSQL was ready, `NRestarts=0`, no request touched a half-up database | no |
| **PostgreSQL fails to start at boot** (a bad config, a full disk, a broken upgrade) — the case that strands most single-box apps | the engine exits (it refuses to serve without its database), systemd relaunches it every 2 s **and never gives up** (`StartLimitIntervalSec=0` — with systemd's default limit of 5 starts per 10 s the unit would go `failed` and STAY down after PostgreSQL was fixed); the moment the database accepts connections the next attempt succeeds | provoked twice: 60 s down → +16 restarts, unit `activating (auto-restart)`, never `failed`; serving **0.5 s** after PostgreSQL came back, nobody touched the engine | **yes, for PostgreSQL** (`journalctl -u postgresql@16-main`); the engine needs nothing |
| the disk fills up | **the app does not notice at first** — at 100 % full, reads answered 200 and small writes 201 (PostgreSQL recycles pre-allocated WAL segments and fills free space in existing pages); the failure comes later and is catastrophic (a new WAL segment or a checkpoint → `PANIC … No space left` → PostgreSQL restarts, the engine answers 503). What DID fail at once: the backup (`could not write to output file` → `failed` in the status file → alert), and journald stopped writing. Freed the space: everything kept working, zero restarts | the guard is §4.6 — the disk alert fires at 10 % / 1 GiB, hours or days before this | **yes** — free space (old sets, `journalctl --vacuum-size`, apt cache); if PostgreSQL already panicked: free space, `systemctl start postgresql` |
| **PostgreSQL is OOM-killed or its postmaster crashes** — the field OOM incident's failure mode | since CAOS-S1 the installer adds a `Restart=on-failure` drop-in for the `postgresql@NN-main` instance, so systemd **brings PostgreSQL back by itself** (Ubuntu/Debian ship it `Restart=no`, which left it — and every app on the box — down until a human ran `systemctl start`). The engine answers fast 503s meanwhile and reconnects | provoked (SIGKILL the postmaster): **without any intervention** PostgreSQL + the engine were serving again in **5 s**; an intentional `systemctl stop` still stops (`RestartPreventExitStatus`) | **no** (was: yes — the pre-CAOS-S1 gap) |
| PostgreSQL is stopped/killed while the app runs (the connection refused) | the engine **stays up** and answers **503 + Retry-After** fast, then reconnects by itself | 20 s stop → clean 503s (≤ 0.1 s each), first 200 ~4 s after PostgreSQL is back, engine never restarted | no |
| **the network to a REMOTE database is black-holed** (link down / packets dropped, not refused) | the engine stays up and sheds fast: since CAOS-S1 (ENG-59) the circuit breaker trips on a run of consecutive failures, not only a lost-count ratio | provoked (30 s `iptables DROP` under a warmed process): **p50 of a failed request 5.00 s → 0.00 s, 70 % under 200 ms**; recovery still immediate (+0.1 s after the link returns) | no |
| the network to the database drops packets (a remote database) | the engine stays up and answers 503 — but **slowly**: a black-holed connection is not a refusal, each request waits the 5 s query/acquire deadline before its 503, and the breaker does not shed them (ENG-59) | 30 s drop → **28.4 s** of 503s at 10 rps (248 requests, **p50 5.0 s each**); first 200 **250 ms** after the link returned, engine never restarted | no — but until ENG-59 a dead link costs 5 s per request instead of 0.1 s |
| a data page is corrupt | with **data_checksums on** (installer default on a fresh cluster since CAOS-S1) any query that READS the bad page gets a loud error instead of silent wrong data — but index/count-only plans can skip it, so the **guaranteed** detector remains the nightly backup (it COPYs every block), whose failure now **names the table** (`Dumping the contents of table "X" failed … invalid page in block N`) and alerts (§4.6) | enabling checksums measured **0.9 s per ~372 MB** offline; restore the affected data in 13.6 s (§4.2) | **yes** — run §4.3-A |
| **the host is gone** | **nothing.** The app is DOWN until someone acts | rebuild in ≈ 4 min + DNS (§4.2) | **yes** — run §4.3-B |

### 4.5 What ONE box does not cover — said, not hidden

This product runs one app on one box. That is a decision (cost, simplicity,
the customer it is for), and it has a price that has to be on the table:

- **If the host dies, the app is down until a person rebuilds it** (§4.3-B) —
  minutes of work plus the DNS TTL, but ZERO of it happens by itself. There is
  no standby, no failover, no second box. Provider host failures are rare, not
  impossible (DigitalOcean's SLA credits start at 99.99 %, i.e. ~1 h a year).
- **Everything written since the last backup is lost** when a restore is
  needed (§4.2). The nightly default means a day; the cadence is yours to set.
  Seconds-level loss needs WAL archiving (PostgreSQL's `archive_command` or
  `pgBackRest` to the same object storage) — supported by PostgreSQL, not
  wired by these scripts; a documented next step for a customer who needs it.
- **A backup that stays on the box protects against the database, not the
  box.** Until `BACKUP_COPY_TO` is set, the status line says so every night.
- **The restore is a full replace** — the whole database, all tenants. A
  per-tenant selective restore is not built (`pg_restore --schema=tenant_x`
  into a scratch database is the manual path).

If any of those is unacceptable for an app, the answer is a second box — which
is the next design, not a flag in this one.

**PostgreSQL data checksums** are ON by default on a cluster the installer
creates fresh (CAOS-S1): a corrupt page is then a loud error on read, not
silent wrong data. On a cluster that already holds data the installer will not
enable them (it needs the whole cluster stopped — your maintenance window):
`systemctl stop postgresql@*-main; runuser -u postgres -- pg_checksums --enable
-D <datadir>; systemctl start postgresql@*-main` (measured ~0.9 s per 372 MB
offline). `fleet-audit.sh` reports the state. The RUNTIME cost is negligible —
measured A/B on the same box: read p50 1.83→1.82 ms, write p50 3.26→3.22 ms
(both `no_change`), +1 MiB of WAL over a 30 s write arm. Checksums catch
corruption **on access** (an index-only or count-by-index plan can skip the
bad block); the nightly backup remains the guaranteed full-scan detector.

### 4.5b Bringing an OLD install up to date (and auditing any box in ten seconds)

Fixing the installer does not fix what it already installed. Every box
installed before 2026-08-30 is missing some of §4: no backup timer, a backup
that was never a full set, a unit that can give up after a burst of restarts.
Two tools close the gap — both were followed literally on a degraded lab box
and then on the production demo box before landing here:

**Audit first.** `scripts/fleet-audit.sh` (installed alongside the other companions in `/opt/<app>/scripts/`) says
per app WHAT IS MISSING, never just "ok" — service + unit policy, binary
contract, companions, timer, the last set's age and completeness, off-box +
passphrase — plus the box facts (swap, disk, PostgreSQL checksums):

```bash
sudo bash /opt/<app>/scripts/fleet-audit.sh          # every app on the box; exit 1 = something missing
```

**Then upgrade — it is just the installer, re-run.** For EACH app on the box:

```bash
# 0. safety: a backup with what exists today, and copies of what the run replaces
sudo bash /opt/<app>/scripts/backup.sh --env-file=/etc/<app>/<app>.env 2>/dev/null   || pg_dump -Fc -f /root/pre-upgrade-<app>.dump --dbname="$(grep ^DATABASE_URL= /etc/<app>/<app>.env | cut -d= -f2-)"
cp -a /etc/systemd/system/<app>.service /root/<app>.service.pre-upgrade
cp -a /etc/<app>/<app>.env /root/<app>.env.pre-upgrade

# 1. the same install command, with the binary it ALREADY runs (or a new one):
sudo bash install.sh --app=<app> --domain=<its domain> --email=<email>   --binary=/opt/<app>/bin/<its binary> --port=<its port> --control-port=<its control port> --yes
```

What the re-run does (§3's criterion table still governs): secrets, database,
data and schema KEPT; the unit REWRITTEN with the current policy
(`RestartSec=2`, `StartLimitIntervalSec=0`); the companions and the backup
timer installed; `APPXIMO_BACKUP_DIR` added to the env — and **every env key
the installer does not manage is carried over verbatim** (a theme, demo roles,
a raised limit, comments included) under a "kept from the previous env"
marker. Pass `--port`/`--control-port` explicitly on a hand-installed box —
the derived defaults may not match what it runs.

Re-run the audit; it must end `✓ this box is protected` except what needs
YOUR input (`BACKUP_COPY_TO` — §4.1). From then on, every binary goes out
with `scripts/deploy-app.sh` (§3), which runs this audit at the end of every
deploy — a box that regresses is exit 3, not a surprise next quarter. **Rollback** (drilled): restore the two
`.pre-upgrade` copies, `systemctl daemon-reload && systemctl restart <app>` —
the data was never touched.

Boxes with units under names the current installer would not derive (a
pre-rename `appitools`, a by-hand second app) work the same — `--app=<that
name>`. One box-specific caveat from the field: an app whose OTHER units
reference a companion script by path (a demo-reset calling `restore.sh`)
keeps working only if those references match the NEW script's flags; the
audit's `!` line about a non-set restore.sh is that warning.

### 4.6 Knowing BEFORE it is too late — backup and disk alerts

The two silent killers are a backup that stopped running weeks ago and a disk
that fills up. Nothing new was built to watch them: the engine's self-monitor
(§8 `APPXIMO_SELFMON`, the collector that already reads the runtime, the
cgroup, PSI and the pool once a tick, out of the request path) gained a
fifth layer, and the alert goes out through the **same alerter** the SLO and
first-occurrence error alerts use — Telegram and/or Slack, §4.6c; without a
destination, a log line `alert (no webhook configured — recorded only)` plus
a LOUD boot banner naming what to set (that silence was OPS-47):

| condition | how it is read (every tick, ~10 s, allocation-free) | the alert | on `/metrics` |
|---|---|---|---|
| **the last backup FAILED** | `last-backup.status` in `APPXIMO_BACKUP_DIR` (the installer sets it) says `failed …` | critical, at once, once per 6 h: the status line + where to look | `appximo_selfmon_backup_ok 0` |
| **the backup is STALE** — no run for longer than `APPXIMO_BACKUP_MAX_AGE` (36 h) | the status file's age | critical: the age, the floor, `systemctl list-timers '*backup*'`, the command to run one now | `appximo_selfmon_backup_age_seconds` |
| **no backup has ever run** and the app has been up longer than the floor | no status file | critical: "is the timer installed?" | `backup_ok -1` until the first run |
| **disk low** — under `APPXIMO_DISK_MIN_FREE_PCT` (10 %) or `_MB` (1024) on the filesystem under the files dir, the obs db, the backup dir or `/` | one `statfs` per path, deduplicated by filesystem | warning (critical under half the floor), once per 6 h: path, free of total, and what to free first | `appximo_selfmon_disk_free_bytes{path}` / `_total_bytes{path}` |

**The indexes too (OPS-44, DEPLOY-FLOTA-S1).** After the dump, `backup.sh`
runs `pg_amcheck --heapallindexed` over the whole database: every heap page
AND every btree index, cross-checked against the heap. `pg_dump` reads every
heap block (COPY) but never an index — a corrupt index page keeps serving
index-only and count plans with no error until something rebuilds it, and
`data_checksums` fire only on the page being read. Cost measured on the
customer-size lab box: **0.9 s for 124 MB / 251 k rows / 406 relations**. A
finding FAILS the backup naming the relation (`cause=amcheck: btree index
"…productos_sku_key"`), on the same status line and alert as a bad dump —
provoked with one random 8 KiB page written into an index: the app's list
kept answering 200 (the corruption was invisible to it), the backup failed
and named the index, `REINDEX` fixed it, the next run said `amcheck=ok`. Off
with `BACKUP_AMCHECK=off` / `--no-amcheck`; a box without `pg_amcheck` or the
`amcheck` extension gets a `skipped(…)` with the reason, never silence.
Sub-day detection is the same tool on a shorter cadence: an `hourly` timer
(§4.7) runs the check every hour with the backup.

A failed backup's status now **names the failing table** when the cause is
corruption (`failed … cause=Dumping the contents of table "orders" failed …
invalid page in block N`) — that line IS the corruption report, and it is the
guaranteed one (the backup reads every block; a live query may not). The
status file lives in the backup dir at mode 0644 and the dir is **0711** (not
0700): the engine's self-monitor runs as the unprivileged service user and
must traverse the dir to read the status — a 0700 dir silently made the watch
report "none" and never alert (CAOS-S1 fixed it; the 0600 conf bundle stays
unreadable). `backup.sh` itself posts to the same webhook when it fails — so a
failed run is heard twice: by the script, and by the engine on the next tick,
whether or not the script was still alive to say it. The JSON of the tick is
`/admin/resources` → `latest.host`. Provoked on the reference box: a `failed`
status line → alert within one tick; a status touched to 3 days old → the
stale alert; the floor raised to 99 % → the disk alert naming `/var/lib/appximo`
and the free bytes. An unparseable floor refuses to boot, naming the variable.

### 4.6b Rehearsing all of it with one command — `appximo drill`

Every scenario in this section can be REPEATED on demand, with the engine
telling you what will happen and where to look before it runs
(MANUAL-OPERACION-S1): `appximo drill restore --app=<app>` restores the
newest set into a scratch database next to the live one and verifies it
against the manifest (the app never stops; `--real` runs `restore.sh`);
`appximo drill chaos <1-10>` runs one of the ten CAOS-S1 experiments (engine
kill, PostgreSQL kill, reboot, disk full, memory to the OOM edge, database
black-hole, 200 ms of latency, clock skew, concurrent writes, a full pool)
and restores what it broke on exit; `appximo drill error` provokes a real
500 on an ephemeral tenant; `drill load` / `drill saturate` read the
self-monitor's verdict live; `drill audit` is `fleet-audit.sh` with a
legend. A drill that loads, breaks or restores refuses a production-looking
target without `--production`. The operator's manual (Spanish) that walks
each one with its screen: [docs/MANUAL_OPERACION.md](MANUAL_OPERACION.md).

The first `drill restore` found that every set taken since OPS-44 carried the
`amcheck` extension (created for `pg_amcheck`, never dropped), which
`pg_restore` as the service role cannot recreate — `backup.sh` now drops it
after the check and `restore.sh` filters those TOC entries, so older sets
restore too.

### 4.6c Alerts on your phone — the Telegram destination (ALERTAS-TELEGRAM-S1)

Every alert the engine emits — SLO burn, the first occurrence of a new error
group, failed/stale backup, low disk, a stuck outbox, an overdue workflow —
goes to **every configured destination**. Telegram is the one that reaches a
phone; Slack keeps working for whoever uses it. The Telegram message is
rendered for a small screen, in Spanish: what happened, in WHICH app, what to
do, severity at a glance, and a panel link when configured.

**Setup (once per box, ~3 minutes):**

1. **Create a bot**: in Telegram, talk to `@BotFather` → `/newbot` → it gives
   you the token (`123456789:AA…`). One bot can serve every app you run.
2. **Get your chat id**: open a chat with your new bot, send it any message,
   then `curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates"` — the
   `"chat":{"id":…}` number is your chat id.
3. **Configure the app** — in `/etc/<app>/<app>.env` (mode 0600; the token is
   a credential — it goes HERE and nowhere else, never in a repo or a log):

   ```
   APPXIMO_TELEGRAM_BOT_TOKEN=123456789:AA…
   APPXIMO_TELEGRAM_CHAT_ID=8851136988
   APPXIMO_ALERT_APP_NAME=La Tiendita          # names the app in every message
   APPXIMO_ALERT_PANEL_URL=https://tienda.example.com   # optional: "Ver el panel" link
   ```

   then `systemctl restart <app>`.
4. **Prove it works**: the boot journal must say
   `telegram alert destination verified (getMe+getChat)`. Then provoke one
   real alert: `printf 'failed test\n' > $APPXIMO_BACKUP_DIR/last-backup.status`
   → within one tick (~10 s) the phone buzzes and the journal logs
   `alert delivered sink=telegram` (restore the status file after — the next
   nightly run rewrites it anyway). `fleet-audit.sh` now verifies the
   destination LIVE (two read-only calls, `getMe` + `getChat`, no message
   sent) and marks ✗ when there is none, whatever the channel.

**Discipline (the worker-env rules, applied):** a malformed token or chat id
— or only one of the pair — **refuses to boot** naming the variable. A
syntactically valid but revoked token boots (the boot never depends on
api.telegram.org being reachable) and screams `TELEGRAM ALERT DESTINATION NOT
WORKING` in the journal; `fleet-audit.sh` marks it ✗ naming the fix. With NO
destination at all the engine boots and prints a loud multi-line banner —
the silent journal-only default was OPS-47.

**Delivery is out-of-band and lossless:** nothing alert-related ever runs on
the request path; every alert is journaled (`alert emitted`) BEFORE delivery
is attempted, retried with backoff (honoring Telegram's own `retry_after` on
a 429), and a delivery that still fails names the sink and points back at
the journal — the alert is late, never lost. The noise brake is unchanged:
at most 5 new-error alerts per tenant per minute plus one storm summary, one
host alert per condition per 6 h, one outbox alert per kind per hour.

**If alerts stop arriving:** check the journal for `alert delivered` vs
`alert delivery FAILED` (`journalctl -u <app> -o cat | grep -i alert`); run
`fleet-audit.sh --app=<app>` — it tells you whether the token was rejected
(rotate it with @BotFather → `/revoke`, update the env, restart) or the chat
is unreachable (the chat must have STARTED the bot). `backup.sh` posts its
own failure to the same chat, so a backup alert reaches you even if the
engine is down at 3 a.m.

**Rotating the token** (do it if it ever leaks): @BotFather → `/revoke` →
pick the bot → it prints a NEW token; update `APPXIMO_TELEGRAM_BOT_TOKEN` in
every `/etc/<app>/<app>.env` that used it and restart each app. The old
token dies the moment BotFather revokes it.

### 4.6d "Mandame el resumen de hoy" — the Telegram command channel (VOZ-ESCALON1-S1)

The same bot that DELIVERS alerts also RECEIVES a small set of read-only
commands and answers with an owner-language daily digest — the first rung of
the voice plan (A-70): what happened today, in the owner's words, on a phone.

**The digest endpoint — `GET /api/summary`.** Generic and derived from the
schema (the engine does not know what a "sale" is — it knows which resources
you declared and what happened to them today): per resource the caller's role
may read, **created today** (a resource with an `auto:"create"` timestamp),
**updated today** (an `auto:"update"` timestamp), and the state-machine rows in
three tiers (ADR-032): **esperan acción** — the states the schema declares as
`state_machine.pending` (the red light); **sin avanzar** — when nothing is
declared, rows still in an initial state, worded as the inference it is
("recién creados, nadie los movió", amber); **en curso** — every other
non-terminal state as a neutral count in the schema's own words (never called
"pendiente"); terminal states are never counted. Deterministic — a template
over real numbers, no language model. RBAC-scoped: it evaluates `read` per
resource and applies that role's row condition and field allowlist, so a
row-scoped role counts only its own rows and never sees a resource it cannot
read. Empty day → "Sin movimiento hoy", never a wall of zeros. `?view=census`
returns the "estado" view (how many of each thing there are right now). It is
a reserved route (a schema resource may not be named `summary`).

**The digest as a PICTURE (VOZ-VISUAL-S1).** `GET /api/summary?format=png`
(the only door — a separate URL keeps the JSON and the image apart in the
response cache) answers the same digest rendered on the server as a
PNG — 800 px wide (2× a phone), a traffic light and a headline you read in
three seconds, one big row per resource that waits, today's motion, the rest
folded — so a twenty-resource app is still one glance. Rendered in pure Go
(no browser; the binary grows ~860 KB, mostly two fonts — ADR-032), only when
asked, never on a CRUD request; ~50 ms. **The bot sends `resumen` as picture +
text**: the text rides as the caption (or follows as a second message when it
exceeds Telegram's 1024-character caption) — never image-only, so a reader
without the picture still has everything.

**The digest says what CHANGED, and the morning send stays quiet when nothing
did (VOZ-DELTA-S1, ADR-034).** Every digest is compared against yesterday's —
the engine keeps ONE snapshot per tenant/role/day in `public.summary_snapshots`
(the previous day's row is the baseline; older rows are pruned; there is no
history) — and words the change: `16 facturas esperan acción (+3 desde ayer ·
3 llegaron hoy)`, `igual que ayer` (folded into one line, small), `nuevo desde
ayer`, `Nada que atender — ayer esperaban 26`. The traffic light answers "is
there NEWS?": red = something waits AND it is new (grew, or arrived today);
amber = the same stock as yesterday, nothing new; green = nothing waits. The
first digest ever says `primer resumen, sin comparación todavía` — never an
invented `+16`.

**The scheduled send speaks only when it matters.** The cron workflow's
consumer asks the engine for the SCHEDULED evaluation (`?mode=scheduled`);
the engine applies the policy the schema declares and records the decision:

```json
"summary": { "resources": ["ordenes", "pagos", "facturas"], "notify": "changes", "quiet_days": 7 }
```

- `notify: "changes"` (default) — the morning digest goes out only when
  something changed since the last one: an attention count, its states, rows
  that arrived today, or the light going up or landing on green. Plain
  motion (`9 nuevos`) is not a change; a light that goes from red to amber
  because the novelty aged is not a change either. `"always"` — the daily
  report regardless, for the owner who wants the morning paper.
- `quiet_days: 7` (default; `0` = never) — the heartbeat: after seven silent
  mornings one short message goes out (`🔕 7 días sin novedad. Sigo acá — todo
  igual que la última vez.`) and the count restarts. **A quiet channel must
  be distinguishable from a dead one**, and there are three ways to tell:
  the heartbeat; the `estado` command, which ends with the last scheduled
  evaluation (`⏰ Último parte automático: 2026-09-18 07:00 — callado a
  propósito, 3 días sin novedad.` / `— enviado (changes)` / `nunca corrió
  todavía`); and the workflow observability (§8b — `GET /admin/workflows`,
  `appximo_workflow_overdue_seconds` climbing when no worker fires).
- The manual `resumen` ALWAYS answers, changed or not. Silence belongs to
  the automatic send only.

**Choosing what enters — `summary.resources`.** A wide schema declares which
resources the digest reports and in what order:

```json
"summary": { "resources": ["ordenes", "pagos", "facturas", "clientes"] }
```

Absent ⇒ every resource the role may read, ranked attention-first. A name
that is not a declared resource is a **load error** (never a silently empty
digest). And **what "waiting" means is declared per lifecycle** —
`"state_machine": { …, "pending": ["pagada", "preparando"] }` — the states
whose rows wait for someone; `"pending": []` says nothing here waits; absent
lets the engine infer (and say so). Both are documented in the schema
reference §1.5 and §5.

**The commands.** Set these in `/etc/<app>/<app>.env` (in addition to the
alert token/chat from §4.6c) and restart:

```
APPXIMO_TELEGRAM_SUMMARY_TENANT=<tenant>   # which tenant the digest covers (also ENABLES the channel)
APPXIMO_TELEGRAM_SUMMARY_ROLE=<role>       # the digest is computed AS this role (must be a declared role)
APPXIMO_TELEGRAM_SUMMARY_USER_ID=<uuid>   # optional (APP-AGENDA-S2): the identity the channel and the scheduled digest act AS —
                                          #   a personal app scopes rows by `dueno_id = $user_id`: set the owner's auth_users id
                                          #   and the chat IS the owner (same role, same rows, same attribution as their Siri
                                          #   token); unset = "telegram:summary" (fine for an unscoped role)
```

The engine then long-polls Telegram (getUpdates) and answers, from the ONE
authorized chat only:

- **`resumen`** — today's movement.
- **`estado`** — the census (how many of each now).
- **`ayuda`** — the command list. Any unrecognized word gets the same help,
  never an error.

**Access control** is the whole channel's security: only
`APPXIMO_TELEGRAM_CHAT_ID` may command; a message from any other chat is
**ignored and logged**, never answered (no reply = no oracle to a stranger).
**Fail-fast:** with `APPXIMO_TELEGRAM_SUMMARY_TENANT` set, a missing/invalid
token, chat id or role — or a chat id that is not numeric (a `@channel` can
receive alerts but cannot be a command source) — **refuses to boot** naming
it. The receiver runs off the request hot path in its own goroutine.

**One bot answers for ONE app.** getUpdates is a single-consumer stream: two
engines polling the same bot token would steal each other's updates. So the
command channel (`APPXIMO_TELEGRAM_SUMMARY_TENANT`) may be enabled on only ONE
app per bot — give each app that needs interactive commands its own
`@BotFather` bot, or enable commands on one and let the others use the
**scheduled** digest (which has no such limit — each app enqueues its own
topic). ALERTS and the scheduled digest are send-only and safely share one bot
across every app; only the inbound command channel is single-consumer.

**Why getUpdates, not a webhook (the decision).** A single idle long-poll
returns sub-second on a new message (well under the 5s budget), adds **zero
inbound attack surface**, needs no public URL and no `setWebhook` moving part,
and works behind any NAT. A webhook's only edge — no held connection — matters
at high message volume or many bots, not one small box; and it would put a new
public unauthenticated route on the data plane. For the fleet's scale,
getUpdates is simpler and safer. (An operator who prefers a webhook can front
`/api/summary` with their own tiny receiver; the endpoint is the contract.)

**The same digest, scheduled (the canonical `workflows` example).** A cron
workflow enqueues a topic each morning; the worker's digest consumer drains it
and sends the same summary — cron → enqueue → consumer, the canonical ADR-031
shape, pure schema on the workflow side:

```json
"workflows": {
  "resumen_matinal": {
    "trigger": { "type": "cron", "cron": "0 7 * * 1-5", "timezone": "America/Bogota" },
    "steps": [ { "name": "enviar_resumen", "type": "enqueue",
                 "config": { "topic": "summary.telegram", "data": {} } } ],
    "overlap": "skip"
  }
}
```

**Today's agenda in the digest (VOZ-16, APP-AGENDA-S2).** A resource that
declares a `ranges` block (§2.7 of the schema reference) opens the digest with
"📅 Hoy en agenda (N)" — the rows whose block touches the report's day, in the
report's zone ("10:00–11:00 dentista"), ordered by start, at most 10 per
resource, in the text AND the picture. It is computed as the digest's role and
identity (row condition + allowlist), so a personal agenda needs
`APPXIMO_TELEGRAM_SUMMARY_USER_ID`. A day with appointments is news for the
`changes` policy; a free day prints nothing and stays quiet.

Run `appximo-worker` in `auto` mode with the same Telegram env plus
`APPXIMO_TELEGRAM_SUMMARY_ROLE` (and optionally
`APPXIMO_TELEGRAM_SUMMARY_TOPIC`, default `summary.telegram`): it fetches
`GET /api/summary?mode=scheduled` as that role and sends it as **picture +
text** when the engine says `should_send` (an engine that predates the image
door still gets the text; one that predates the decision is treated as
"always"). **The workflow must be in the tenant's DEPLOYED schema** (the
worker reads `public.tenants.json_schema`, not the boot file): deploy it with
`appximo migrate --tenant <id> --schema <file>` or the admin `PUT`. **On a box
deployed with `deploy-app.sh`, pass `--worker-binary=/path/to/appximo-worker`**:
it installs the worker beside the engine, writes `<app>-worker.service` (the
same unit `install.sh` writes) when the box has none, adds the worker's env
keys when missing, enables it and verifies it is ACTIVE — `fleet-audit.sh`
then reports ✓. The 58's apps had no worker for weeks for exactly this reason:
they predate the installer's worker and the deploy path carried none. At-least-once ⇒ a rare
double morning summary on a retry is accepted (harmless for a read-only
digest); a transient engine/Telegram failure keeps the row pending and it
delivers on recovery (provoked: Telegram unreachable → `pending`, attempts
climbing with the reason in `last_error`; reachable again → delivered with
the image) — and if the worker is down, the enqueued digest sits `pending`
and the outbox age alert (§8b) says so. DST follows ADR-031 (the
declared timezone; a spring-forward run fires once, a fall-back can't fire
twice).

**Siri / an iPhone Shortcut** can ask for the same digest without Telegram —
build a Shortcut with one "Get Contents of URL" action, then "Get Dictionary
Value `text`" → "Show/Speak":

```
GET https://<tenant>.<your-domain>/api/summary
Headers:
  Authorization: Bearer <a token minted with `appximo token --tenant <t> --role <r>`>
Method: GET
```

The response is `{"text": "...", "has_motion": true, "level": "red|amber|green",
"headline": "...", ...}`; read `text` (or `headline` for a one-line Siri
answer). Add `?format=png` to get the picture instead.
(Do NOT build the Shortcut for the user — this is the exact request they wire
in five minutes; mint a long-lived token for it, or a dedicated read-only
role.)

### 4.6e "Cuántas órdenes hay hoy" — asking the bot a QUESTION (VOZ-PREGUNTAS-S1, ADR-033)

The same bot — and the same `/api` — now answers a READ question in the
owner's own words: «cuántas órdenes hay hoy», «qué pedidos están sin pagar»,
«cuánto vendimos esta semana», «las órdenes de Ana Gómez», «órdenes por
estado». Any word sent to the bot that is not `resumen`, `estado` or `ayuda`
is a question. It is the second rung of the voice plan (A-70) and the first
place a language model enters the product — through the narrowest seam the
engine could give it:

- **The model never writes SQL, never sees a row, never produces a number.**
  It receives the schema's VOCABULARY for the asking role (resource names,
  fields with their types, enum values, the state machine's waiting/final
  states, relation targets — nothing else) and the question, and answers with
  a PLAN over a closed grammar: one resource, filters in the REST filter
  grammar, a period token (`today`, `this_week`, `last_month`…), one of
  count/list/sum/avg/min/max, an optional group_by. There is no plan kind
  that writes.
- **The engine validates every name in the plan against the schema and
  REFUSES what does not exist** — a resource, a field, an enum value, an
  operator that does not fit the type — with the exact reason; the model gets
  ONE correction round, then the owner gets «No entendí» plus the list of
  what CAN be asked. A corrected plan that answers a *different* question
  (the model swapping `clientes` for `ordenes`) is refused too. Never a
  guess: a wrong number with a confident face is worse than no answer.
- **Proper names are matched against what exists.** Dictation mangles them
  (`Gomes`, `Jimenes`, `Yeison`, `Baldes`), so a name never goes into a
  filter as said: the engine fetches candidates through its own `?search=`
  (RBAC-scoped like any list), folds Spanish homophones (b/v, s/z/c, ll/y,
  silent h, g/j, accents) and scores them. One clear match → used and SAID
  BACK («Entendí «Ana Gomes» como **Ana Gómez**»); several → «¿Cuál? • Ana
  Gómez • Luis Gómez»; none → «No encuentro ningún cliente que se llame
  «Wilfredo Pacheco»» — never a zero presented as the answer.
- **RBAC first.** The plan runs as the asking role through the SAME query
  builders `GET /api/{resource}` uses, with that role's row condition and
  field allowlist: a customer role asks «cuántas órdenes hay» and gets ITS
  five; it asks «cuántos clientes tenemos» and — `clientes` not being in its
  vocabulary — gets «No entendí». A resource the role cannot read is not
  even a word the model receives.
- **The reply is a template over the engine's JSON**, the number first,
  then what was understood in small print («ordenes · esta semana · estado =
  pagada») so the owner can see when the question was read differently. A
  grouped answer («órdenes por estado») also arrives as a PICTURE — the
  digest's own renderer, same card, same fonts.
- **Cost and latency, measured live** (Haiku 4.5, the tiendita's 14-resource
  schema, 23 questions): **p50 ≈ 0.9–1.0 s, max ≈ 1.8 s**, **≈ US$ 0.003 per
  question** (≈ 2 400 input tokens for the vocabulary + ~80 output; a
  correction round doubles it). At ten questions a day that is under a
  dollar a month. Every reply carries `usage`, `cost_usd`, `model_ms`,
  `total_ms`, and the engine logs one line per question — the owner can see
  what a question costs. Note: the vocabulary prompt (~2 400 tokens) is
  BELOW Haiku 4.5's prompt-cache minimum, so the cache does not engage on a
  small schema; a wider schema (more resources) crosses it and gets cheaper
  per question, not dearer.
- **If the model does not answer, nothing breaks.** Each model call is bounded
  (`APPXIMO_ASK_TIMEOUT`, 8 s); on a timeout or an API error the bot says «No
  pude pensar la pregunta ahora … los comandos fijos siguen: resumen, estado,
  ayuda» — the three fixed commands never go through the model. While the
  engine thinks the chat shows Telegram's «escribiendo…» indicator, re-sent
  every 4 s, so the owner is never looking at nothing.

**Most questions never reach the model (VOZ-SIN-IA-S1, ADR-035).** Before
the model, two free layers answer:

- **The deterministic parser** reads the question against the schema alone —
  a counting/listing/summing verb, ONE resource by its schema name (singular
  or plural), a declared enum/state value («pendiente de pago» ≡
  `pendiente_pago`, «canceladas» ≡ `cancelada`), a period phrase, a proper
  name after «de/para/con», «por <campo>» for a breakdown — and answers only
  when it is SURE: every word accounted for, one resource, one operation, one
  place for the name. One leftover word («vendimos», «vigentes», «nuevos») and
  the question goes to the model. It never guesses to save a call. A write
  verb («borrá», «cancelá») is refused without any call. **Measured on the
  corpus of real questions: 33 of 49 (67 %) answered with no model, in
  milliseconds**, with the same answers the model gave.
- **The plan cache** remembers the PLAN a question translated to (per
  tenant and role, normalized text, 24 h, 2 000 entries) — never the data:
  the number is recomputed on every question, and a cached «hoy» plan asked
  tomorrow counts tomorrow (a period is a token the engine resolves at run
  time). Hit rate on `/metrics` and `/admin/ask`.

**The wallet guard — caps the owner is told about.** Every model call is
billed, so the engine keeps a ledger per tenant and day (`public.ask_spend`,
survives a restart) and applies two caps, read fail-fast at boot:

| knob | default | what it does |
|---|---|---|
| `APPXIMO_ASK_PER_MINUTE` | `6` | MODEL calls per tenant per minute — a human by voice at full speed; parser/cache answers are not counted. Over it: «demasiadas preguntas al modelo este minuto», retry in seconds. |
| `APPXIMO_ASK_DAILY_USD` | `0.50` | MODEL spend per tenant per day (≈ 150 model questions). At the cap **the model is off until tomorrow** in the declared timezone; the parser, the cache and the fixed commands keep answering. `0` disables. |
| `APPXIMO_ASK_ALERT_PCT` | `80` | ONE alert per tenant per day at that share of the cap, through the same alerter as every other alert (Telegram in Spanish with «Qué hacer»; Slack) — and one more when the cap is reached. `0` disables. |

A non-number, a negative, a 0 per-minute or a percent outside 0..100
**refuses to boot** naming the variable. **Worst case per day with the
defaults: the cap plus one question, ≈ US$ 0.50** (before: 30 calls a
minute all day ≈ US$ 1 300).

**Where to see what it costs** — never in the provider's console:

- `GET /admin/ask` (platform token or `X-Admin-Key`): per tenant, today /
  this month / the last 30 days — questions, model calls, dollars, how many
  the parser and the cache answered, whether the tenant is capped — plus the
  caps in force and the plan cache's hit rate.
- `/metrics`: `appximo_ask_spend_usd{tenant,window="day"|"month"}`,
  `appximo_ask_questions{tenant,source="parser"|"cache"|"model"}`,
  `appximo_ask_daily_cap_usd`, `appximo_ask_plan_cache{what}`.
- every reply carries `source` (`parser` | `cache` | `model`) and a `spend`
  block (`day_usd`, `day_model_calls`, `daily_cap_usd`, `per_minute`).

**Knowing where the money goes (VOZ-TRAZABILIDAD-S1).** Four things, all
for whoever administers, off by default where they would bother a shop owner:

- **The trace in every reply** — `APPXIMO_ASK_TRACE=on`: the reply's TEXT
  ends with `⚙︎ parser · 2 ms · US$ 0`, `⚙︎ caché · 3 ms · US$ 0`, or
  `⚙︎ modelo · 1,1 s · US$ 0,0029 · el parser pasó: palabra fuera del
  schema «vendimos»`. **Never in `speech`**: a voice that reads "tres
  centavos" after every answer wears out in two days — the cost is for the
  eyes on the phone, not the ears. The JSON always carries `source`,
  `cost_usd`, `fallback` (the parser's reason code) and `fallback_es`
  (the same in words), switch or not.
- **The question history** — `public.ask_history`, one row per question:
  when, tenant, role, user id (the JWT subject — an id, never a name), the
  question (see below), who answered, kind, resource, cost, latency, the
  plan, the parser's reason, cache hit. Written OFF the answer path (a
  buffered channel and one writer; a full buffer drops rows and says so —
  the answer is never delayed). **Retention `APPXIMO_ASK_HISTORY_DAYS`**
  (30; `0` disables), pruned hourly and at boot. **No IP, ever (A-53).**
  **The text follows `APPXIMO_ASK_HISTORY_TEXT`:** `redacted` (default —
  the proper names the plan identified become `[nombre]`: «las órdenes de
  [nombre]» keeps the SHAPE the parser needs to learn from without the
  person), `full`, or `none` (the plan only). Same discipline as request
  bodies: personal data is opt-in, not default. A name the engine could not
  identify (a question that went unclear) stays as typed under `redacted`;
  a tenant that cannot accept that sets `none`.
- **The three lists that matter** — `GET /admin/ask?tenant=<id>&days=<n>`
  adds `top_cost` (the phrases that cost the most), `top_repeated`,
  `model_fallbacks` (the phrases that went to the model, each with the
  parser's reason — what the parser should learn next) and `share` (the
  REAL parser / cache / model split — VOZ-9's number lives here, not in
  the lab corpus).
- **`gasto` on Telegram** — a fourth command beside `resumen`, `estado`,
  `ayuda`: today and this month, how far the cap is, who answered how
  many, the phrases that cost the most — as the digest's own census card
  (picture + text). Served by `GET /api/ask/spend[?format=png]`, **for
  admin-grade roles only** (a wildcard-resource role, the same inherited
  test as the tenant observability routes): a listed or row-scoped role is
  403 — the spend of a platform is the administrator's business, not a
  clerk's. The bot answers with the configured role, so `gasto` works where
  that role is admin-grade (the 58's `dueno`/`owner`) and says «tu rol no
  puede ver el gasto» elsewhere.
- **A cap per user** — `APPXIMO_ASK_DAILY_USD_PER_USER` (default `0` =
  off): composes with the tenant cap, whichever is reached first wins; at
  the user cap only THAT user degrades to parser/cache/fixed commands («ya
  usaste tu cupo diario del modelo»), the rest of the tenant goes on, and
  the administrator gets one alert per user per day. Off by default
  because a single-owner app must not meet a second, silent ceiling.

**Enable the model** — add the key to the app's env (`/etc/<app>/<app>.env`,
0600, never in a repo, a log or a report) and restart. Without it the parser
still answers every shape it is sure of; only a question that needs the model
gets «no activadas»:

```
ANTHROPIC_API_KEY=sk-ant-…              # enables POST /api/ask and the bot's question branch
APPXIMO_SUMMARY_TIMEZONE=America/Bogota # the owner's "hoy" / "esta semana" (a UTC box is otherwise a day off after 7 pm)
# optional:
APPXIMO_ASK_MODEL=claude-haiku-4-5      # the cheap model is the default; any Anthropic model id
APPXIMO_ASK_TIMEOUT=8s                  # per model call
APPXIMO_ASK_PER_MINUTE=6                # MODEL calls per tenant per minute (parser/cache answers are free and uncounted)
APPXIMO_ASK_DAILY_USD=0.50              # MODEL spend per tenant per day; at the cap the model is off, the rest keeps answering
APPXIMO_ASK_ALERT_PCT=80                # one Telegram/Slack alert per tenant per day at this share of the cap
APPXIMO_ASK_DAILY_USD_PER_USER=0        # per-user daily cap (0 = off); composes with the tenant cap
APPXIMO_ASK_TRACE=off                   # on: who answered / latency / cost / why in every reply's TEXT (never the voice)
APPXIMO_ASK_HISTORY_DAYS=30             # question history retention (0 = off); pruned hourly
APPXIMO_ASK_HISTORY_TEXT=redacted       # redacted | full | none — what of the question text the history keeps
APPXIMO_ASK=off                         # disable the question path even with a key
```

Without a key the path is DISABLED and says so: `POST /api/ask` answers
`503 {"error":"ask_disabled", …}` naming the variable, and the bot answers a
question with that sentence plus the help. `appximo-worker` needs nothing
new. **The key is the operator's:** the engine reads `ANTHROPIC_API_KEY`
from its environment only — it is never written by any tool, never printed,
never part of a backup manifest.

**`POST /api/ask`** — the HTTP door (the bot uses it too, as the configured
role). Bearer required (an anonymous or `$public` caller is 403 — a question
spends a model call, so it needs an identity); body `{"q": "<the question>"}`
(≤ 500 characters). The answer:

```json
{ "kind": "answer",                                 // unclear | ambiguous | not_found | write_refused | forbidden | unavailable
  "headline": "7 ordenes",                          // the number first — one line for a voice assistant
  "text": "<b>7</b> ordenes\n<i>ordenes · estado = pendiente_pago</i>",   // Telegram HTML
  "speech": "7 ordenes. ordenes · estado = pendiente_pago",               // plain words, for Siri
  "number": 7, "understood": "ordenes · estado = pendiente_pago",
  "plan": { "kind": "count", "resource": "ordenes", "filters": [ { "field": "estado", "op": "eq", "value": "pendiente_pago" } ] },
  "groups": null, "png": "<base64, grouped answers only>",
  "usage": { "input_tokens": 2357, "output_tokens": 77 }, "cost_usd": 0.0027, "model": "claude-haiku-4-5",
  "model_ms": 1198, "total_ms": 1201, "corrected": false }
```

A question that implies a write («cancelá la orden ORD-1003», «borrá los
pedidos viejos») is `write_refused`: «Por acá solo leo …» — writes with
confirmation are the next rung (VOZ-4) and are not anticipated here.

**Siri — "pregúntale a la tienda".** One Shortcut, three actions (build it
yourself in two minutes — nothing here needs a developer):

1. **Dictate Text** (language: Spanish) → its output is the question.
2. **Get Contents of URL**:
   - URL: `https://<tenant>.<your-domain>/api/ask`
   - Method: `POST` · Headers: `Authorization: Bearer <a token minted with
     appximo token --tenant <t> --role <r> --ttl …, or a dedicated read-only
     role>` · `Content-Type: application/json`
   - Request Body: JSON → one field `q` = *Dictated Text*.
3. **Get Dictionary Value** `speech` from *Contents of URL* → **Speak Text**
   (or `headline` for the shortest answer).
4. *(optional, VOZ-AHORRO-S2)* **Get Dictionary Value** `display` from the
   same *Contents of URL* → **Show Result**. `display` is the answer as
   plain text — line breaks kept, no HTML — and, when the app runs with
   `APPXIMO_ASK_TRACE=on`, it ENDS with the `⚙︎ parser · 2 ms · US$ 0`
   line: the trace is READ on the screen, never heard (`speech` never
   carries it; a voice that says «tres centavos» after every answer is
   muted in two days). A long answer (a list of ten rows) fits: Show Result
   is a scrollable card; `speech` stays the full text for the voice.

Ask «cuántas órdenes hay hoy» and Siri reads the engine's number back. A
misheard name comes back as «Entendí X como Y» or as a question, never as a
wrong count. (Do NOT build the Shortcut for the user — these are the exact
three actions they wire; mint the token for them.)

**The token a phone shortcut should carry (TOKEN-SCOPE, 2026-09-20).** A
dev token expires in 24 h — a shortcut breaks every morning. Mint one on the
box, with the app's own secret, that lives long AND can do little:

```bash
set -a; . /etc/<app>/<app>.env; set +a
appximo token --secret "$JWT_SECRET" --tenant <t> --role <r> --user-id <auth user id> \
  --ttl 365d --paths /api/ask,/api/summary
```

- `--ttl 365d` (days, or a Go duration) — the token still carries `exp`;
  there is no immortal token.
- `--paths /api/ask,/api/summary` — the token is REFUSED on any other path
  with a 401 naming its scope, even though its role could reach
  `/api/<resource>`: a phone that is lost can ask and read the digest,
  never list customers or write a row. An entry ending in `/*` is a
  prefix; the exact form is safer (`/api/ask/spend` is NOT in `/api/ask`).
- **Revocable without rotating `JWT_SECRET`:** every minted token carries an
  id (`jti`, printed on stderr, or `--id`). To revoke one, add its id to
  `APPXIMO_JWT_REVOKED` (comma-separated) in the app's env and restart:
  that token is 401 «token revoked» on the next request, every other token
  keeps working. A malformed list refuses to boot.

The two checks cost one nil test and one map lookup per request, after
the claims cache. The receiver's own self-call tokens (60 s, no scope) are
untouched.

**Synonyms are declared, never wired (VOZ-AHORRO-S2, ADR-038).** The parser
answers at zero cost only in the schema's words; an owner says «pedidos»
for `ordenes` and «mascotas» for `pets`. Declare those words in the schema
— `"ordenes": {"aliases": ["pedidos", "ventas"]}` and, per state,
`"estado": {"aliases": {"pendiente_pago": ["sin pagar"]}}` — and the parser
recognizes them like the schema name, for questions and for orders. They
are validated unique at load (an alias on two resources, or one that is
also a state, refuses to boot naming the fix). Measured on the 58's real
questions: parser share 50 % → 75 % after the two apps declared theirs.
`gasto` shows what the model spend BOUGHT: `Gasto útil … · desperdiciado …`
(a paid «no entendí» is waste), and a sentence that is not a question
(«sí pero mejor el viernes» after a confirmation, «hola», «qué puedo
preguntar») is answered without the model. Declaring aliases is a schema
change: `scp` + `validate` + `install` + restart, and a rollback restores
the previous schema FIRST (OPS-55 — the previous binary rejects the key).

**What it cannot do (v1, by design):** one resource per question (no joins —
«citas de pacientes de Bogotá» is «No entendí» unless the relation resolves
to a name), no comparisons across periods («más que el mes pasado»), no
rankings («el producto más vendido»), no percentages, no follow-ups («¿y
ayer?» — each question stands alone), and «vendimos» means whatever the
model maps it to in the schema's states — the small print says which.

### 4.6f «Anotá llamar a Fabián para mañana» — WRITING by voice, with confirmation (VOZ-ESCRITURAS-S1, ADR-037)

The same door (`POST /api/ask`, the bot, the Siri shortcut) now also
**creates and changes** rows. Never deletes. And **nothing is written until
the owner reads exactly what will be written and says yes**:

```
you:  Anotá llamar a Fabián para arreglar el techo, urgente, para mañana
bot:  📝 Voy a crear tarea:
      • persona: Fabián Gómez
      • prioridad: urgente
      • titulo: llamar a Fabián para arreglar el techo
      • vence en: mañana (dom 20 sep)
      ¿Confirmás? (sí / no)          [✅ Sí] [✖ No]
you:  sí
bot:  ✅ Listo: creé tarea llamar a Fabián para arreglar el techo (pendiente, 19 Sep).
…
you:  marcá como hecha la tarea de Fabián
bot:  ✏️ Voy a cambiar tarea «llamar a Fabián… (pendiente, 19 Sep)»:
      • estado: pendiente → hecha
      ¿Confirmás? (sí / no)
you:  dale
bot:  ✅ Listo: tarea … : estado → hecha.
```

What holds, in order of what it protects:

- **The yes is exact.** `sí`, `dale`, `ok`, `confirmo`, `listo`, `de
  acuerdo`, `hacelo`… «sí pero mejor el viernes» or «creo que sí» is NOT a
  yes: the write is cancelled (the bot says so in one line and why) and,
  when the sentence carries nothing the grammar could execute, it is
  settled by the parser at zero cost (VOZ-AHORRO-S2 — it used to buy a
  «no entendí» from the model); a sentence that carries an order («sí,
  anotá … para el viernes») is read as a new order. `no` / `cancelar`
  cancels. A confirmation waits
  **5 minutes**, one per person; a new order replaces the previous one and
  says so; a question asked meanwhile cancels it and is answered.
- **It goes through the engine's own write path.** The same RBAC, the same
  validators, the same state machines, the same outbox events as
  `POST /api/<resource>`. A role that may not create gets «solo leo»; a
  transition the schema forbids («la tarea ya está hecha») is refused BEFORE
  the confirmation, with the schema's words; whatever the API would reject
  (a 409, a 422) comes back as «No pude … No escribí nada». The `demo` role
  of a showcase app cannot write by voice — the RBAC is the boundary, as in
  the `/app`.
- **Names are matched first, and shown.** «Fabi» with Fabián Gómez AND
  Fabiana Torres in the table is a numbered pick («¿Cuál? 1. … 2. …»); a
  name nobody has is an offer: «No encuentro ninguna persona "Rocío Paz".
  sí para crearla» — the person is created (its own yes), then the task's
  confirmation shows «Rocío Paz (nuevo)». A dictated name is never stored as
  text in a relation.
- **What you did not say is asked, not invented.** «anotá una tarea para
  Marta» → «Para crear tarea me falta titulo. ¿Qué pongo?» — one field at a
  time; the answer is the value. A priority you did not say is not filled.
- **Time is resolved by the engine** in the app's zone: «mañana», «pasado
  mañana», «el viernes», «el viernes a las 3», «la semana que viene», «fin
  de mes», or a date you literally said. The confirmation shows the date.
- **Cost.** A create needs the model: **≈ US$ 0.0023, ≈ 0.8 s** (Haiku,
  one call) — **once per sentence per user**: the PLAN (never the result)
  is cached per tenant|role|user (VOZ-AHORRO-S2), and the same order said
  again is re-prepared against the database of the moment — names matched
  again, the row looked up again, «mañana» resolved on the day it runs, the
  required fields checked again — before a fresh confirmation is asked; a
  cached plan can never confirm stale data. A state change («marcá como
  hecha…», «cancelá el pedido ORD-1003») is settled by the parser: **US$ 0,
  ≈ 15 ms**. The confirmation itself costs nothing.
  The same caps, trace (`⚙︎`) and history apply (§4.6e); the history keeps
  only the shape of a write (`[create tareas: titulo, vence_en]`), never
  its text.

**Telegram** shows **✅ Sí / ✖ No** buttons under the confirmation (and
`1 2 3` buttons for a pick); pressing one answers exactly like typing it, and
the buttons disappear so it cannot be pressed twice. **Siri / a shortcut:**
the reply JSON carries `pending_id`, `stage` (`confirm`, `ask_field`,
`which`, `create_ref`) and `expires_in`. The simplest shortcut: after
*Speak Text* of `speech`, **Ask for Input** («¿Confirmás?») and POST the
answer as `{"q": "<answer>"}` to the same URL — the pending is per identity,
so the plain door resolves it. A shortcut that kept the id may post
`{"pending_id": "…", "answer": "sí"}` instead. The token a shortcut carries
must have an identity (`--user-id`, §4.6e): a token without one can read
but is told it cannot write.

**Off switch:** `APPXIMO_ASK_WRITES=off` in the app's env makes every voice
channel read-only again (a write order answers «por acá solo leo»).

**What it will not do (by design):** delete anything (`borrá` is refused on
every channel, even for an admin); empty a field; change several rows at
once («cancelá todas…»); write files, json or ids; a per-transition
permission («only the owner may mark paid» — the `update` grant governs,
as on the API). A restart forgets pending confirmations (a write that was
not confirmed did not happen).

Example schema with the whole cycle — tasks with people, a state machine,
events, a workflow that sends the digest when an URGENT task is created, and
a morning reminder: `examples/model-lab/agenda-voz.json`.

### 4.6g «Agendame una reunión de 4 a 5» — an AGENDA by voice, with the collision said before writing (MOTOR-AGENDA-S1, ADR-039)

An app whose rows occupy a block of time declares it in the schema (no code):

```json
"eventos": { "fields": { "titulo": {…}, "inicio": {"type":"time","required":true}, "fin": {"type":"time","required":true},
                         "ocupa": {"type":"bool","default":true}, "estado": {…} },
  "ranges": { "horario": { "start": "inicio", "end": "fin", "default_duration": "1h",
                           "no_overlap": { "scope": [], "when": { "field": "ocupa", "op": "eq", "val": true } } } },
  "events": ["create", "update"] },
"workflows": { "aviso_15_min": { "trigger": { "type": "time", "resource": "eventos", "field": "inicio", "before": "15m",
                                              "when": { "field": "estado", "op": "ne", "val": "cancelado" } },
  "steps": [ { "name": "avisar", "type": "enqueue", "config": { "topic": "message.telegram", "data": { "text": "=\"⏰ En 15 min: \" + record.titulo" } } } ],
  "role": "dueno" } }
```

What the operator gets, verified live (`evidencia/MOTOR-AGENDA-S1/provocaciones/`):

- **The database refuses a collision, naming the row** — `409 time_range_conflict` with the colliding row on REST, the batch, GraphQL and a custom handler; twenty simultaneous writes on one slot → one `201`, nineteen `409`. Adjacent blocks (4–5, 5–6) never collide; a row with `ocupa: false` never blocks. It needs the `btree_gist` extension: `install.sh` installs it at setup, the engine installs it at tenant provisioning (trusted extension, the database owner may), `fleet-audit` reports a schema that declares `no_overlap` without it.
- **The bot says the collision BEFORE writing**: «agendá reunión de planificación mañana de 4 a 5» → «⚠️ Ya tenés «reunión con Fabián» de 16:00 a 17:00. … ocupa: no (no bloquea el horario: ya había algo) … ¿Igual lo agendo?» — the deterministic parser settles it (US$ 0); a `sí` writes the row as NOT blocking (`ocupa: false`) so it never blocks what comes next. «qué tengo mañana», «tengo algo mañana a las 4», «cuándo estoy libre el jueves» are the parser's too. A bare hour 1–6 is read as the afternoon («a las 4» = 16:00); the confirmation prints the hour, so a wrong reading is caught before anything is written.
- **The reminder arrives once, on time, and follows the row**: the worker's leader (the same lock the cron uses) sweeps the rows entering the window every 30 s and claims each (row, instant) in `public.workflow_reminders` atomically with the outbox row; a worker restarted inside the window neither loses nor duplicates it; a moved row fires at its new time, a cancelled one never. The text goes out through `message.telegram` — the same bot and chat as the digest (needs `APPXIMO_TELEGRAM_BOT_TOKEN` + `_CHAT_ID` + `_SUMMARY_ROLE` on the worker, §4.6d). Watch it: `GET /admin/workflows` (trigger `time:eventos.inicio -15m`, last run), `appximo_workflow_reminders_fired_24h` / `_failed_24h` on `/metrics`.
- **Adding the rule over data that already collides is refused, naming the pairs**: the dry-run (`PUT /tenants/{id}/schema {"dry_run": true}`, `appximo migrate --dry-run`) lists `[blocked] no_overlap "horario" on eventos: N existing row pair(s) already overlap — <a> × <b>`; the apply lands everything else and answers **422** with those words (the tenant keeps its previous schema; nothing half-applied). Fix the rows, re-apply, it converges.
- **The `/app` panel** keeps the two datetime fields side by side, refuses an end before the start, and shows «Ya hay algo en ese horario: …» while you edit — advice, never a block; the 409 names the row if you save anyway.

The generator declares all of it from the description («que no se me crucen», «avisame 15 minutos antes de cada compromiso»): `appximo ai-generate "Mi agenda personal…"` came out with `ranges` + `no_overlap`, the `time` workflow and the morning cron, valid first try (US$ 0,014). Example to start from: `examples/model-lab/agenda-choques.json`.

### 4.6h «Cómo creo algo» — the assistant TEACHES how to use it: a living guide, a fixed form for creating, corrections on a pending write (AGENDA-ASISTENTE-S1, ADR-040)

An assistant that does not teach how to use it forces guessing, and guessing
costs money (the agenda's real history after two days: every creation went to
the model, «cómo creo algo» bought a «no entendí»). Four things changed, all
derived from the schema — the engine wires no domain word:

**The living guide, by levels — US$ 0, always.** `ayuda` answers the menu
(what the app has, the four doors: ask / create / change / the digest) and the
levels to ask for; each level is generated from THIS app's schema and rows and
delivered in parts of six items on the screen AND in the voice (the same six —
«más» continues both from the same place):

```
you:  cómo creo algo
bot:  ✍️ Para crear una tarea decí: «crear tarea: [qué], [30 minutos], [urgente], [mañana / el viernes]».
      Por ejemplo: «crear tarea: revisar el contrato, 30 minutos, urgente, el viernes» — la entiendo al instante, gratis.
      Los datos van en cualquier orden, separados por comas o pausas; el que no digas, lo pregunto o lo dejo vacío.
      Antes de escribir te muestro todo y espero tu sí.
      Hay más (1 de 4). Decí más para seguir.            ← compromiso, registro, persona; «cómo creo una tarea» for the detail
you:  qué puedo preguntar          → the question shapes ON THIS SCHEMA, six per page («cuántas tareas hay», «tareas por hacer», «tareas de salud», …)
you:  qué campos tiene una tarea   → every field in words with its form («area (una de tus areas: salud, casa…)», «estado (pendiente, en_curso…) — también entiendo por hacer, abierta…»)
you:  cómo filtro por fecha        → periods, ranges, combinations, «los últimos 3…»
```

Every example promised as free is **parsed on that schema before it is
shown** (the spoken variant too — a voice says a pause where the screen shows
a colon); one the parser cannot settle is never promised. Free and paid are
always apart, with the price. Measured on the agenda: 43 of 43 examples the
guide shows answer `source: parser`, US$ 0.

**The fixed form for creating — `crear <recurso>: <qué>, <datos en
cualquier orden>`.** A verb (`crear`, `nueva`, `anotá`, `agregá`, `registrá`…)
or the resource word itself, then WHAT it is, then the data in ANY order, with
or without the field word, separated by commas, «y» or the pause dictation
leaves. Every datum is recognized by its FORM: a field's own word, a declared
value or alias, a bool by its name («urgente»), a day or a clock («el
viernes», «mañana a las 3», «de 12 a 1», «a las 4 pm», «a las cuatro de la
tarde», «9 y media»), a number with its unit («30 minutos»), a name after
«con» / «para», a bare name tried against every target (VOZ-20, below). What
is not a datum is the title, kept as said. Three cousins: «anotá que <lo que
pasó>» is the note resource (creatable, no lifecycle, a range without
no-overlap or a time defaulting to now); «anotá <infinitivo>…» / «tengo que…»
is the to-do; «reunión con Fabián mañana a las 3 por una hora» (an agenda
word, a clock, no question word) is a block. Chosen over `campo: valor` pairs
(exhausting to dictate) and a positional order (memorized, breaks when one
datum is skipped). **The confirmation is unchanged**: the saving is the model
call, never the control. What the form cannot settle stays the model's.

**A correction re-issues the confirmation.** «no, mejor el viernes», «mejor a
las 5», «sí pero urgente», «que sea con Marta» after a confirmation: when the
words after the lead are data the form recognizes, they are applied to the
pending (a clock keeps the day, a day keeps the clock, a range keeps its
length) and the confirmation is shown again saying what changed. A «sí pero…»
is still never a yes.

**Names tried against every target (VOZ-20).** «las tareas de Esposa», «crear
tarea: pagar el seguro, Casa»: on a resource with an area AND a person the
parser now says which fields the name could belong to and the engine tries
each — relation targets first, the row's own title only when no target holds
the name. One → used and said with its kind; several → a numbered pick naming
each kind; none → said. An exact whole token IS the row («Fabián» with a
Fabiana around; «Fabi» still asks).

**`resumen` / `estado` / `gasto` through `/api/ask` (VOZ-21)** are served
in-process by the engine's own endpoints (the same digest and card the bot
sends), US$ 0. **«Ya hice…», «terminé de…», «… está lista»** move the named
to-do to its finished state; **«poné en curso la declaración de renta»** finds
the resource by the state value said.

**The voice.** `speech` (and `display` = speech + the cost line when tracing)
is composed, never derived: short sentences, clocks and dates and small counts
in words («de las diez a las once de la mañana», «el martes veintinueve de
septiembre», «dos tareas»), no bullet, guillemet, pictograph, underscore or
raw digit; a list reads at most five items then «y N más; mirá el panel»; a
numbered pick is read «Uno, Fabián Gómez. Dos, Fabiana Torres.»; a long guide
in parts. The box has no synthesizer, so the criterion is declared and
measured (`ask.SpeechMetrics` + every spoken reply of the provocations: zero
digits, zero symbols). What Siri does with the punctuation is the shortcut's
business: if it reads too fast, lower the Speak action's rate.

**VOZ-15 stays as the written rule** («a las 4» = 16:00; 7–12 = morning): the
sentence bank had 25 bare hours in 37 clocks, every one meaning what the rule
reads; the confirmation now SAYS «a las cuatro de la tarde», so a misread is
heard before anything is written and fixed with «mejor a las 4 de la mañana».

### 4.7 Recommended cadence by kind of app

| the app | cadence | why |
|---|---|---|
| a content site, a catalogue, a brochure with a form | nightly (default) | a day of lost submissions is recoverable by asking |
| a back-office (orders, appointments, inventory) — the typical app | **every 6 h, or hourly** | an afternoon of orders re-typed from paper is a bad day; an hour is a phone call |
| money movements, invoices with legal numbering, anything a regulator can ask for | **hourly + WAL archiving** | the sequence of legal invoice numbers cannot have a hole; minutes of loss are the ceiling |
| a demo, a staging box | nightly, `BACKUP_KEEP=3` | it exists to be reset |

And, for every kind: **off-box, always** (§4.1), and **run the restore once**
after the first real week of data — `restore.sh` on a scratch app (`install.sh
--app=drill --port=8095` on the same box, restore the set into it, look, then
`install.sh --uninstall --purge --app=drill`) takes ten minutes and turns the
folder into a backup.

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
| `GOMEMLIMIT` | no | auto | Soft heap ceiling. Unset → the engine uses 90 % of an explicit **cgroup** limit if present, else warns on a small box. **Set it on a bare small box** — the installer sets **30 % of RAM** (min 256 MiB). Field-verified range on a 1 GB / 1 vCPU droplet (2026-08): the engine idles at ~70–80 MB RSS with `GOMEMLIMIT=180MiB`, so anything in **180–300 MiB works comfortably on 1 GB** — the installer's 287 MiB default included; below ~128 MiB the GC starts thrashing under load. Never derived from total RAM as if the engine were alone on the box (PostgreSQL needs the rest). |
| `GOMAXPROCS` | no | auto | cgroup-aware (automaxprocs). |
| `APPXIMO_NO_VERSION_CHECK` | no | (off) | Set to `1` to silence the "a newer release exists" line in `appximo version`. That check runs **only** in a human `version` run — never at `serve` boot, never on the request path — sends nothing about you or the machine (an anonymous GET of a public URL), times out in 2 s, and is skipped automatically whenever `CI` is set or `--json` is used. |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | no | 350 × vCPU / 100 | Per-tenant token bucket — fairness between tenants, not capacity. The default is DERIVED from the measured per-core ceiling (ENG-53, [BENCHMARKS §4e](BENCHMARKS.md)); the boot log prints it. |
| `APPXIMO_MAX_INFLIGHT` | no | auto = max(32, 4 × (vCPU + pool)) | Admission control (ENG-52): the cap on requests in flight; the excess is shed with `429` + `Retry-After: 1` BEFORE any work. `0` disables. |
| `APPXIMO_MEMORY_GUARD_MIN_MB` | no | max(32, 2 % of RAM) | **Host memory guard.** While `MemAvailable + SwapFree` (from `/proc/meminfo`, sampled ≤ 1/s) is under this many MiB, data-plane WRITES answer `503` + `Retry-After: 5` with a body naming the measurement, the floor and this knob; reads and probes keep flowing. Measured with swap included on purpose: on a Postgres box `shared_buffers` is Cached-but-not-reclaimable, so `MemAvailable` alone sits at tens of MiB at rest. `0` disables; a non-integer refuses to boot. Degradation, not capacity — give the box swap (§Prerequisites). |
| `OBS_DB_PATH` | no | `/var/lib/appximo/obs/obs.db` | Trace/snapshot history (SQLite). Keep it on a persistent path. |
| `APPXIMO_BACKUP_DIR` | no | — (installer: `/var/backups/<app>`) | Where `backup.sh` writes its sets. **Setting it turns the self-monitor's backup watch on**: `last-backup.status` is read every tick (out of band); `failed`, or an `ok` older than `APPXIMO_BACKUP_MAX_AGE` (default `36h`), or no run ever after that much uptime → ONE alert per 6 h on the configured destinations (§4.6c) + `appximo_selfmon_backup_ok` / `_backup_age_seconds` on `/metrics` + `host.backup` in `/admin/resources` (§4.6). |
| `APPXIMO_DISK_MIN_FREE_PCT` / `APPXIMO_DISK_MIN_FREE_MB` | no | `10` / `1024` | The disk floor of the self-monitor: the filesystems under `APPXIMO_FILES_DIR`, `OBS_DB_PATH`, `APPXIMO_BACKUP_DIR` and `/` (deduplicated) are `statfs`'d every tick; under either floor → ONE alert per 6 h (critical under half the floor) naming the path, the free bytes and what to delete, plus `appximo_selfmon_disk_free_bytes{path}`. `0` disables a floor. Nothing on the request path. |
| `APPXIMO_SELFMON` | no | on | **The engine's own resource collector** (ADR-030): runtime / cgroup v2 / PSI / pool read out of band by one goroutine, and a deterministic bottleneck verdict (`cpu_throttled`, `memory_pressure`, `gc_pressure`, `cpu_saturated`, `pool_exhausted`, `db_bound`, `lock_contention`, `healthy`) at `/admin` → Resources, `/debug/resources` and `appximo_selfmon_*` on `/metrics`. `off` disables it. On a systemd unit the cgroup is the service's own; `cpu.stat throttled_usec` is what says "the plan's quota, not the code". |
| `APPXIMO_SELFMON_INTERVAL` | no | `10s` | Background cadence (floor 250ms). The view's polling switches to `APPXIMO_SELFMON_LIVE_INTERVAL` (`1s`) for a minute after each poll. A value that does not parse refuses to boot. |
| `APPXIMO_SELFMON_P99_MS` | no | `50` | The verdict's absolute "slow" floor in ms ("what is slow for MY app"); the relative rule (3× the healthy baseline) applies regardless. |
| `APPXIMO_FILES_DIR` | no | `/var/lib/appximo/files` | Local file-store root (or use `APPXIMO_FILES_BACKEND=s3`). |
| `APPXIMO_FILES_BACKEND` / `APPXIMO_FILES_S3_*` | no | `local` | S3/R2/Spaces/MinIO backend — see [docs/FILES.md](FILES.md). |
| `APPXIMO_FILES_MAX_BYTES` / `_TOKEN_TTL` / `_ALLOWED_EXT` | no | 256 MiB / 180 s / curated | Upload cap, signed-URL TTL, extension allowlist. |
| `DB_MAX_CONNS` | no | 10 | Postgres pool size. |
| `APPXIMO_MAX_TX_OPS` | no | 100 | Max ops per `POST /api/transaction`. |
| `APPXIMO_MAX_SSE_PER_TENANT` | no | 1000 | Cap concurrent SSE streams per tenant (`429` at the cap). |
| `APPXIMO_CORS_ORIGINS` (+ `_METHODS`/`_HEADERS`/`_EXPOSE_HEADERS`/`_CREDENTIALS`/`_MAX_AGE`) | no | off | Browser CORS; empty = disabled. |
| `APPXIMO_GRAPHQL_PLAYGROUND` | no | off | Serve GraphiQL + allow introspection outside dev. |
| `APPXIMO_AUTH_SIGNUP_ROLE` / `_MIN_PASSWORD` / `_REQUIRE_VERIFIED` / `_BASE_URL` | no | off / 8 / off / — | Public signup (opt-in, role-gated) + reset/verify email links. |
| `APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE` / `APPXIMO_AUTH_LOGIN_BURST` | no | **5 / 5** | Login (and MFA-verify) attempts per **(tenant, email)** before `429`. **This is the online brute-force guard on every account — raising it weakens it by the same factor** (60/min = 86 400 guesses a day at one identity). Raise it ONLY for a deliberately shared identity (a public read-only demo account) and keep that identity's role read-only; the engine logs a warning at boot when the value is above the default, and refuses to boot on a value that is not a positive integer. |
| `APPXIMO_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID`/`_SECRET`, `_CALLBACK_URL`, `_DEFAULT_ROLE`, `_SUCCESS_REDIRECT` | no | off | Social login. |
| `APPXIMO_MFA_KEY` / `APPXIMO_MFA_ISSUER` | no | JWT secret / `Appximo` | TOTP secret encryption + issuer label. |
| `APPXIMO_PLATFORM_SUPER_ADMIN_ROLE` / `_MFA_ISSUER` | no | `platform_super_admin` | Admin API super-admin. Bootstrap with `appximo admin create`. |
| `APPXIMO_OUTBOX_MAX_PENDING_AGE` | no | `15m` | The outbox age alert (AUTOMATIZACION-S1): when the OLDEST pending event is older than this, one alert per hour on the configured destinations (§4.6c) naming the topic and the fix. `0` disables; an invalid duration refuses to boot. The gauges (`appximo_outbox_*`, `appximo_workflow_*`) and `GET /admin/outbox` are always on. |
| `APPXIMO_EMAIL_TOPIC`, `SMTP_*` | no | — | Email delivery by the worker (mode `auto` delivers `email.send` when `SMTP_HOST` is set; mode `email` is the single-purpose variant). |
| `APPXIMO_WORKER_MODE` + `APPXIMO_WORKER_{BATCH,MAX_ATTEMPTS,POLL,SCHEMA_REFRESH,ROLE,RESOURCE}`, `APPXIMO_ENGINE_URL`, `APPXIMO_TENANT_DOMAIN` | no | `auto` / 50 / 5 / 5s / 1m / `service_worker` / `filejobs` / `http://127.0.0.1:<port>` / (domain minus first label) | `appximo-worker`'s env (§8b). STRICT: an invalid value or a misspelled `APPXIMO_WORKER_*` refuses to boot naming every offender; unset values are reported in one "defaults in effect" boot line. |
| `ANTHROPIC_API_KEY` | no | — | Enables the **question path** (§4.6e): `POST /api/ask` and the bot's free-text branch — a language model translates the question into a validated read plan. The engine reads it from its environment only; never written, printed or backed up. Without it the path answers `503 ask_disabled` and the bot says so; the fixed commands never depend on it. `APPXIMO_ASK=off` disables the path even with a key. |
| `APPXIMO_ASK_MODEL` / `APPXIMO_ASK_TIMEOUT` | no | `claude-haiku-4-5` / `8s` | The question path's model (any Anthropic model id; the cheap one is the default and measured at ≈ US$ 0.003 per MODEL question) and the bound per model call. |
| `APPXIMO_ASK_TRACE` / `APPXIMO_ASK_HISTORY_DAYS` / `APPXIMO_ASK_HISTORY_TEXT` / `APPXIMO_ASK_DAILY_USD_PER_USER` | no | `off` / `30` / `redacted` / `0` | Where the money goes (§4.6e, VOZ-TRAZABILIDAD-S1): the ⚙︎ trace line in every reply's text (never the voice), the question history's retention and what of the text it keeps (proper names → `[nombre]` by default; no IP ever), and the per-user daily cap that composes with the tenant's. Fail-fast on a bad value. `gasto` on Telegram and `GET /api/ask/spend` read them. |
| `APPXIMO_ASK_PER_MINUTE` / `APPXIMO_ASK_DAILY_USD` / `APPXIMO_ASK_ALERT_PCT` | no | `6` / `0.50` / `80` | The wallet guard (§4.6e, ADR-035): model calls per tenant per minute, model spend per tenant per day (at the cap the model is off until tomorrow — the parser, the plan cache and the fixed commands keep answering), and the share of the cap that fires ONE alert per tenant per day. Fail-fast: a bad value refuses to boot. Worst case per day = the cap + one question. |
| `APPXIMO_SUMMARY_TIMEZONE` | no | the process's local zone | The IANA zone «hoy» / «esta semana» / the digest's day are computed in (`America/Bogota`). A production box runs UTC, so an owner in Bogotá asking at 8 pm was answered about tomorrow. An invalid name refuses to boot. Set it on every app with an owner in one place. |
| `APPXIMO_JWT_REVOKED` | no | — | Comma-separated token ids (`jti`) to refuse with 401 «token revoked» — revocation without rotating `JWT_SECRET` (§4.6e TOKEN-SCOPE). Mint long-lived or path-scoped tokens with `appximo token --ttl 365d --paths /api/ask,/api/summary`; a malformed list refuses to boot. |
| `APPXIMO_TELEGRAM_BOT_TOKEN` + `APPXIMO_TELEGRAM_CHAT_ID` | no | — | The **Telegram alert destination** (§4.6c) — every alert (SLO burn, first-occurrence errors, backup failed/stale, disk low, stuck outbox, overdue workflows) reaches that chat, in Spanish, phone-first, with what-to-do. Both or neither: half a pair, or a malformed value, **refuses to boot** naming the variable; a revoked token is detected out-of-band at boot (read-only `getMe`+`getChat`) and screams in the journal. `backup.sh` posts its own failure here too. |
| `APPXIMO_ALERT_APP_NAME` | no | the schema `name` | The app name every alert message carries (a fleet of apps alerting to ONE chat needs to say who is talking). |
| `APPXIMO_ALERT_PANEL_URL` | no | — | Public origin (`https://app.example.com`) — when set, alerts carry a "Ver el panel" deep link (`/admin#/observability`, `/admin#/resources`, `/admin/outbox` by kind). |
| `SLACK_WEBHOOK_URL` | no | — | The Slack destination: the same alerts, the historical English one-liner. Works alongside Telegram — every configured destination receives every alert. `backup.sh` posts its own failure here too. Without ANY destination every alert is only a log line, and the boot says so loudly (OPS-47). |
| `REDIS_URL` | no | — | Optional async migration worker. |
| `BACKUP_DIR` | no | `/tmp/appximo-backups` | Output dir for `POST /admin/backup`. |
| `APPXIMO_SAFEGO_TIMEOUT` / `APPXIMO_PUBLIC_ROUTE_RPS` / `_BURST` | no | 30 s / 5 / 10 | Library-mode custom-handler tuning. |
| `--port` / `--control-port` | flag | 8080 / 9090 | Data plane / control plane (control plane = **localhost only**). |

---

## 8b. The worker — outbox consumer + workflow executor (`appximo-worker`)

A schema that declares `events: […]` or a `workflows` block PROMISES a
consumer; `appximo-worker` is what honors it (decision A-67 — the worker is
product capability, not a Go library). The installer installs it automatically
when the schema declares automation and a worker binary is available:

```bash
# build both (or download both release assets: appximo-<os>-<arch> + appximo-worker-<os>-<arch>)
./scripts/build-engine.sh /tmp/appximo
./scripts/build-worker.sh /tmp/appximo-worker
sudo bash scripts/install.sh --app=myapp --domain=api.example.com --email=you@example.com \
  --binary=/tmp/appximo --worker-binary=/tmp/appximo-worker --yes
```

What that installs: the binary at `/opt/<app>/bin/appximo-worker` and the unit
`<app>-worker.service` (same guarantees as the engine: `RestartSec=2`,
`StartLimitIntervalSec=0`, after PostgreSQL; sharing the app's env file). Force
with `--worker`, opt out with `--no-worker`; a schema that declares automation
with no worker running is a NAMED warning at install and a ✗ in
`fleet-audit.sh`.

**What it does (mode `auto`, the default):** executes the tenants' declared
`workflows` (event triggers as outbox consumers; cron triggers on a
leader-elected scheduler — run N workers, `pg_try_advisory_lock` picks one
leader, failover is automatic) and delivers `email.send` when `SMTP_HOST` is
configured. **And deliberately nothing else**: the claim is topic-scoped, so an
event whose topic has no consumer here is never claimed, never acknowledged,
never lost — it stays `pending`, the worker names it in its log once a minute,
and the engine's `appximo_outbox_oldest_pending_age_seconds` alert
(`APPXIMO_OUTBOX_MAX_PENDING_AGE`, default 15m) tells a human. App-specific
topics (a `factura.emitir`, a `jobs.render`) need an app consumer — a
`consumers.Router` in a consumer binary (`appximo backend-spec` §6).

**Is it healthy?** Three places, in order of habit:

1. `journalctl -u <app>-worker -f` — every run, every claim scope, every
   foreign-pending warning.
2. `GET /admin/outbox` (platform token or admin key) — pending/failed counts,
   the OLDEST pending age (the number that matters), per-topic backlog, every
   `failed` row WITH its `last_error`, and the circuit breakers' state.
3. `GET /admin/workflows` — per workflow: last run (status, error, per-step
   detail), next run, 24h counters. On `/metrics`: `appximo_outbox_*` and
   `appximo_workflow_*` (a growing `appximo_workflow_overdue_seconds` means the
   worker — or its scheduler — is not running).

**Changing a cron** in the deployed schema takes effect at the scheduler's
next tick (≤ 30 s): the stored `next_run` is re-armed when the spec differs
(VOZ-DELTA-S1 — before, the old occurrence had to pass first, up to a day).

**A `discarded` row** (VOZ-DELTA-S1) is a third terminal state: the consumer
DECIDED the event can never be delivered — a malformed payload, an event whose
subject no longer exists (an invoice job for an order a demo reset removed), a
topic the consumer registers with `Router.Discard` — and parked it with the
reason in `last_error` (`worker.Discard(reason)`). It is never `sent` (that
would be a success face on work that never happened) and never retried.
Counted apart — `discarded` in `GET /admin/outbox`, `appximo_outbox_discarded`
on `/metrics` — and it never alerts: it is a record, not an incident. Export
the rows first if you want an acta (`COPY (SELECT … FROM public.outbox WHERE
state='discarded') TO STDOUT WITH CSV HEADER`).

**Recovering a `failed` row** (retries exhausted; the error is in
`last_error`): fix the cause, then re-arm it —
`UPDATE public.outbox SET state='pending', attempts=0 WHERE id=<id>;` — the
worker redelivers on its next poll. An exhausted after-webhook dead-letters
itself as topic `webhook.dead` (payload: url, event, body, error): re-fire it
by hand and delete the row, or leave it as the record of the loss.

---

## 9. Security checklist

- **Firewall default-deny inbound.** Reachable from outside: **22** (SSH), **80**
  and **443** (Caddy). Not the engine port, **not 9090**, not 5432. `--harden`
  configures ufw for exactly this.
- **Control plane (`:9090`) is never on the internet.** Its safety model is
  *unreachable*, not *unguessable* — reach it from the box (`curl
  127.0.0.1:9090/...`) or an SSH tunnel.
- **Docker publishes ports AROUND ufw** (field report I1 — the classic VPS
  trap). Docker inserts NAT rules evaluated BEFORE ufw's INPUT chain, so
  `docker run -p 5432:5432 postgres` is internet-exposed even while
  `ufw status` says deny-incoming — and ufw will not warn you. Always publish
  on loopback: `-p 127.0.0.1:5432:5432`. This applies to ANY container on the
  box, not only Postgres.
- **Cloud images ship pre-opened ports** (field report I3): DigitalOcean's
  Docker image, for example, allows 2375/2376 (the Docker API — 2375 is
  unauthenticated) in ufw even though the daemon only listens on the unix
  socket. Audit `ufw status numbered` on a fresh droplet and delete what you
  don't serve.
- **Strong `ADMIN_KEY` and `JWT_SECRET`** (≥ 32 chars, `appximo gen-secret` —
  or `openssl rand -hex 32`). The installer generates them; never reuse a
  weak one.
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
| Writes answer **503 "host memory pressure"** during a load | The box is at the kernel's OOM edge (`MemAvailable+SwapFree` under the floor). Add swap (§Prerequisites), load in smaller batches, or — knowing the risk — raise/disable `APPXIMO_MEMORY_GUARD_MIN_MB`. `free -m` and `dmesg | grep -i oom` tell the story. |
| **PostgreSQL OOM-killed** during an import (`postgresql: Failed with result 'oom-kill'`) — every app on the box down | No swap on a small box: the kernel could not page out and killed the biggest process, which is the SHARED PostgreSQL. Add swap (§Prerequisites) BEFORE re-running the load; `systemctl restart postgresql` brings the apps back. |
| Engine **OOM-killed** / restarts under load | No `GOMEMLIMIT` on a small box. Set `GOMEMLIMIT=512MiB` (1 GB) in `/etc/appximo/appximo.env` and `systemctl restart appximo`. `dmesg | grep -i oom` confirms. |
| `serve` exits immediately | Missing a required var (`DATABASE_URL`/`JWT_SECRET`/`ADMIN_KEY`) or Postgres unreachable. The log names which. Check `DATABASE_URL` and `systemctl status postgresql`. |
| Registering a tenant returns an error about the tenant id | The id must match `^[a-z][a-z0-9]{1,29}$` (it is BOTH the Postgres schema and the host's first label) — no hyphens, no underscores, no uppercase. |
| Port already in use on boot | Another process on 8090 (or your `--port`). Find it with `ss -ltnp | grep :8090` and stop it, or pick another port. |
| Service loops between `active` and `activating (auto-restart)` — looks hung, `systemctl status` shows no error | The service user can't READ its config: a restrictive umask (027 on CIS-hardened images) left `/etc/<app>` at 0750 root:root, so `serve` dies on `open /etc/<app>/schema.json: permission denied` **every 5 s** — the message IS in `journalctl -u <app> -n 50`, but each attempt exits so fast the status line never settles on `failed`. Fix: `chmod 0755 /etc/<app>` (the installer now sets explicit modes and verifies readability **as the service user** at install time, so this only bites hand-rolled units). |

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
