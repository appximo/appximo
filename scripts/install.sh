#!/usr/bin/env bash
#
# Appximo — official production installer.
#
#   curl -fsSL https://get.appximo.com/install.sh | sudo bash -s -- --domain api.example.com --email you@example.com
#
# From an EMPTY Ubuntu/Debian VPS to a live HTTPS API in minutes: native
# PostgreSQL + the engine under systemd + Caddy for automatic Let's Encrypt TLS.
# No Docker (dockerd would eat 300-400 MB — unacceptable as the default on a
# 1 GB box). Docker remains a documented VARIANT (docs/PRODUCTION.md).
#
# ONE required decision: your domain (+ an email for Let's Encrypt). Everything
# else is inferred or generated: an internal port, a service user, a data dir,
# random PostgreSQL credentials, and the JWT/admin secrets.
#
# The whole program is wrapped in main() and only invoked on the LAST line, so a
# truncated download (the classic curl|bash failure mode) never executes a
# half-written script. Idempotent: safe to re-run. `--uninstall` reverses it for
# a clean retry.
#
# THE UPGRADE-IN-PLACE CRITERION (MIGRACION-CONFIANZA-S1 — written down because
# it used to be implicit, and an implicit criterion is what let a re-run keep
# another application's schema under a new domain):
#   KEPT on purpose   secrets (JWT/admin key/db password — every issued token
#                     stays valid), the database and its data, the data dir,
#                     the control port, an existing Caddy/Postgres setup.
#   ALWAYS REPLACED   the binary (from --binary / the release download), the
#                     systemd unit, the env file (same secrets, current layout),
#                     this app's Caddy site, the companion scripts.
#   THE SCHEMA        replaced when --schema is given; KEPT when it is not — and
#                     a kept schema is VERIFIED to belong to THIS app (a schema
#                     that is byte-identical to, or carries the `name` of,
#                     another app installed on this box stops the install).
# And the installer does not trust its own log: at the end it VERIFIES what it
# installed — the installed binary's checksum against --binary, the running
# service's /health version against that binary (locally AND through Caddy —
# a proxy pointed at a neighbour's port answers with the neighbour's version),
# and the schema on disk against what was asked. A mismatch FAILS the install,
# loudly, even though the service is up. Measured field failure that motivated
# it: a re-install left `GET /api/asambleas` (another app's resource) answering
# 200 under the new app's domain.
#
# Binary source: there are no public GitHub Releases yet, so pass --binary=/path
# to the appximo binary you built/copied (scripts/build-engine.sh, or scp it
# up). The URL-download + checksum path below is written and ready — it activates
# automatically once RELEASE_VERSION is set to a published tag.
set -euo pipefail

# ── Constants ────────────────────────────────────────────────────────────────
# OPS-10 — every path this installer writes used to be a FIXED constant, so a
# second install.sh run on a box that already served an app REPLACED its unit,
# OVERWROTE its secrets (invalidating its JWTs and pointing it at another
# database) and REWROTE the whole Caddyfile — taking the running app down. The
# app name now namespaces all of it: unit, service user, /etc, /opt, /var/lib,
# database, role, control port and the Caddy site. --app is unset by default, so
# a single-app box behaves byte-identically to before.
APP_NAME="appximo"
SERVICE_USER="appximo"   # derived from APP_NAME in derive_paths
SERVICE_NAME="appximo"   # derived from APP_NAME in derive_paths
readonly REPO="appximo/appximo"
# Set to a published tag (e.g. "v0.1.0") to enable the download path. Empty means
# "no public release" → the installer requires --binary.
readonly RELEASE_VERSION="v0.1.1"

# ── Defaults (overridable by flags) ──────────────────────────────────────────
DOMAIN=""; EMAIL=""; BINARY=""; CLI=""; SCHEMA=""; PORT="8090"; CONTROL_PORT=""
ASSUME_YES="no"; HARDEN="no"; DRY_RUN="no"; PREFIX=""; UNINSTALL="no"; PURGE="no"
SHOW_SECRETS="no"; APP_EXPLICIT="no"; SCRIPTS_DIR=""; INTERNAL_TLS="no"; UPGRADE="no"; SWAP_MB=0; PUBLIC_OK="no"; CURL_TLS=""
# RESILIENCIA-S1: the nightly backup is INSTALLED, not suggested. A backup that
# depends on someone remembering a cron line is the one that is missing the
# night it is needed (the 58 had its timer written by hand; every other install
# had none). systemd OnCalendar syntax; --no-backup-timer opts out.
BACKUP_SCHEDULE="*-*-* 03:30:00"; BACKUP_TIMER="yes"
# The directory this script was started from — resolved BEFORE any `cd` (the
# postgres steps cd /tmp, and a relative $0 resolved after that pointed the
# companion-script lookup at /tmp: "deploy-update.sh weren't next to the
# installer" while they were — field report, MIGRACION-CONFIANZA-S1).
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
# The installer's own invocations of the binary must never wait on the
# release-check network call (the service env is untouched by this).
export APPXIMO_NO_VERSION_CHECK=1

# ── Output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ]; then C_G=$'\033[0;32m'; C_Y=$'\033[1;33m'; C_R=$'\033[0;31m'; C_B=$'\033[1;34m'; C_N=$'\033[0m'
else C_G=""; C_Y=""; C_R=""; C_B=""; C_N=""; fi
info() { printf '%s→%s %s\n' "$C_B" "$C_N" "$*"; }
ok()   { printf '%s✓%s %s\n' "$C_G" "$C_N" "$*"; }
warn() { printf '%s!%s %s\n' "$C_Y" "$C_N" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$C_R" "$C_N" "$*" >&2; exit 1; }

# run: execute a command, or just print it in --dry-run.
run() { if [ "$DRY_RUN" = "yes" ]; then printf '  [dry-run] %s\n' "$*"; else "$@"; fi; }

# ask: prompt on the controlling terminal (/dev/tty), so it works even under
# `curl … | bash` where stdin is the piped SCRIPT, not the keyboard. Sets REPLY.
ask() {
	if [ -r /dev/tty ]; then printf '%s' "$1" >/dev/tty; IFS= read -r REPLY </dev/tty || REPLY="";
	else REPLY=""; fi
}

usage() {
	cat <<'EOF'
Appximo installer — empty Ubuntu/Debian VPS → live HTTPS API.

Usage: sudo bash install.sh --domain DOMAIN --email EMAIL --binary PATH [options]

Required (until public releases exist):
  --domain=DOMAIN      public domain pointing at this box (A/AAAA record)
  --email=EMAIL        Let's Encrypt account email
  --binary=PATH        the binary to install — the engine, OR any consumer app
                       honoring the deployable contract (docs/adr/ADR-023):
                       `<bin> version` exits 0, `<bin> serve --schema --port` runs it

Options:
  --cli=PATH           the engine CLI to install as the OPS COMPANION at
                       /opt/appximo/bin/appximo-cli (tenant, migrate, token,
                       admin create). When --binary IS the engine it is symlinked
                       automatically and this flag is unnecessary; a consumer
                       binary serves but cannot operate its database (ADR-023)
  --schema=PATH        boot schema JSON (default: a todo-api starter you replace later).
                       On a re-run: given → replaces the installed schema; omitted →
                       the installed schema is KEPT and verified to be THIS app's
  --scripts=DIR        where deploy-update.sh / backup.sh / restore.sh live (default:
                       next to this installer); they are installed into /opt/<app>/scripts
  --backup-schedule=C  when the installed <app>-backup.timer runs (systemd OnCalendar
                       syntax; default "*-*-* 03:30:00" = nightly at 03:30). Sets
                       are kept 14 days in /var/backups/<app>; off-box copy via
                       BACKUP_COPY_TO in the env file (docs/PRODUCTION.md §4)
  --no-backup-timer    do not install the backup timer (you schedule backup.sh yourself)
  --port=PORT          internal engine port Caddy proxies to        [default 8090]
  --app=NAME           install as a SEPARATE app on this box (OPS-10). Namespaces
                       everything: unit <NAME>.service, /etc/<NAME>, /opt/<NAME>,
                       /var/lib/<NAME>, the postgres role+database, the control
                       port, and its own Caddy site file — so a second app never
                       touches the first. Default "appximo" (unchanged behavior).
                       Lowercase letters, digits and '-', starting with a letter.
  --control-port=PORT  internal control-plane port (localhost only). Default 9090
                       for the default app, 9090+offset derived from --app for a
                       named one — two apps cannot share it.
  --yes                non-interactive (don't prompt to confirm)
  --show-secrets       print the generated secret VALUES in the summary (default:
                       only their path — transcripts and CI logs are forever)
  --harden             also apply ufw (SSH+80+443) + fail2ban + unattended-upgrades
  --dry-run            generate every config file + print the plan, run NO system steps
  --internal-tls       Caddy issues a LOCAL (self-signed) certificate instead of
                       Let's Encrypt — for a LAN/staging box whose domain is not
                       public. The verification then talks to Caddy with -k.
  --root=DIR           prefix all paths with DIR (for --dry-run testing only)
  --uninstall          stop + remove THIS app's service, unit, files and Caddy site
                       (combine with --app=NAME to remove a specific app; other
                       apps on the box are never touched)
  --purge              with --uninstall: ALSO drop the database + data dir (destructive)
  --help
EOF
	exit "${1:-0}"
}

# ── Arg parsing ──────────────────────────────────────────────────────────────
parse_args() {
	for arg in "$@"; do
		case "$arg" in
			--domain=*) DOMAIN="${arg#*=}" ;;
			--email=*)  EMAIL="${arg#*=}" ;;
			--binary=*) BINARY="${arg#*=}" ;;
			--cli=*)    CLI="${arg#*=}" ;;
			--schema=*) SCHEMA="${arg#*=}" ;;
			--scripts=*) SCRIPTS_DIR="${arg#*=}" ;;
			--backup-schedule=*) BACKUP_SCHEDULE="${arg#*=}" ;;
			--no-backup-timer) BACKUP_TIMER="no" ;;
			--internal-tls) INTERNAL_TLS="yes" ;;
			--show-secrets) SHOW_SECRETS="yes" ;;
			--port=*)   PORT="${arg#*=}" ;;
			--app=*)    APP_NAME="${arg#*=}"; APP_EXPLICIT="yes" ;;
			--control-port=*) CONTROL_PORT="${arg#*=}" ;;
			--root=*)   PREFIX="${arg#*=}" ;;
			--yes|-y)   ASSUME_YES="yes" ;;
			--harden)   HARDEN="yes" ;;
			--dry-run)  DRY_RUN="yes" ;;
			--uninstall) UNINSTALL="yes" ;;
			--purge)    PURGE="yes" ;;
			--help|-h)  usage 0 ;;
			*) die "unknown flag: $arg (see --help)" ;;
		esac
	done
	# Validate --port early (it flows into the unit, Caddyfile and health checks).
	case "$PORT" in
		''|*[!0-9]*) die "--port must be a number (got '$PORT')" ;;
	esac
	[ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "--port out of range (1-65535): $PORT"
	# --root only makes sense for --dry-run staging; a real run under a prefix would
	# write configs systemctl/apt never see. Refuse the footgun.
	if [ -n "$PREFIX" ] && [ "$DRY_RUN" != "yes" ]; then
		die "--root is only for --dry-run testing (it stages files under a prefix systemctl won't read)"
	fi
	valid_app_name "$APP_NAME" || die "invalid --app '$APP_NAME': use lowercase letters, digits and '-', starting with a letter (it becomes a systemd unit, a unix user, a directory and a postgres role)"
	# Every path is namespaced by the app name (OPS-10). With the default name the
	# values are IDENTICAL to the historical constants, so an existing single-app
	# box is untouched. PREFIX lets --dry-run stage them under a test root.
	SERVICE_USER="$APP_NAME"
	SERVICE_NAME="$APP_NAME"
	DB_NAME="$(printf '%s' "$APP_NAME" | tr '-' '_')"   # postgres identifiers take no hyphen
	DB_ROLE="$DB_NAME"
	ETC_DIR="$PREFIX/etc/$APP_NAME"
	ENV_FILE="$ETC_DIR/${APP_NAME}.env"
	SCHEMA_FILE="$ETC_DIR/schema.json"
	OPT_DIR="$PREFIX/opt/$APP_NAME"
	BIN_PATH="$OPT_DIR/bin/$APP_NAME"
	VARLIB="$PREFIX/var/lib/$APP_NAME"
	CADDYFILE="$PREFIX/etc/caddy/Caddyfile"
	# Each app owns ONE site file; the main Caddyfile only imports the directory,
	# so installing an app APPENDS a site and can never erase a sibling's.
	CADDY_SITES_DIR="$PREFIX/etc/caddy/sites"
	CADDY_SITE_FILE="$CADDY_SITES_DIR/${APP_NAME}.caddy"
	UNIT_FILE="$PREFIX/etc/systemd/system/${SERVICE_NAME}.service"
	BACKUP_SERVICE_FILE="$PREFIX/etc/systemd/system/${SERVICE_NAME}-backup.service"
	BACKUP_TIMER_FILE="$PREFIX/etc/systemd/system/${SERVICE_NAME}-backup.timer"
	BACKUP_DIR="$PREFIX/var/backups/$APP_NAME"
	[ -n "$CONTROL_PORT" ] || CONTROL_PORT="$(default_control_port "$APP_NAME")"
}

# valid_app_name: what can simultaneously be a systemd unit, a unix user, a
# directory and (after '-'→'_') a postgres identifier.
valid_app_name() {
	case "$1" in
		[a-z]*) : ;;
		*) return 1 ;;
	esac
	case "$1" in *[!a-z0-9-]*) return 1 ;; esac
	[ "${#1}" -le 32 ]
}

# default_control_port: the control plane is localhost-only, but two apps on one
# box still cannot share a port. The default app keeps 9090 (unchanged); a named
# app gets a stable, name-derived port in 9091-9189 so re-running the installer
# always picks the same one.
default_control_port() {
	if [ "$1" = "appximo" ]; then printf '9090'; return; fi
	local sum=0 i c
	for i in $(seq 1 ${#1}); do
		c=$(printf '%s' "$1" | cut -c"$i")
		sum=$(( (sum * 31 + $(printf '%d' "'$c")) % 99 ))
	done
	printf '%d' $(( 9091 + sum ))
}

# ── Validators ───────────────────────────────────────────────────────────────
# valid_domain: a real, cert-eligible hostname — no scheme/path/space, has a dot,
# and is not a bare IPv4 (Let's Encrypt issues for names, never for IPs).
valid_domain() {
	case "$1" in
		''|*://*|*/*|*" "*|*'*'*) return 1 ;;   # empty/scheme/path/space/wildcard
	esac
	case "$1" in *.*) : ;; *) return 1 ;; esac  # must contain a dot
	case "$1" in *[!0-9.]*) return 0 ;; *) return 1 ;; esac  # all-digits+dots → an IP
}
# valid_email: something before '@' and a dotted domain after. Deliberately loose
# (real validation is Let's Encrypt accepting it), just catches paste garbage.
valid_email() { case "$1" in ?*@?*.?*) return 0 ;; *) return 1 ;; esac; }

# guard_existing_app REFUSES to install a DIFFERENT app over a live one (OPS-10).
#
# The installer used to write six fixed paths, so a second run on a box that
# already served an app replaced its unit, overwrote its secrets (invalidating its
# JWTs and pointing it at another database) and rewrote the Caddyfile — killing the
# running app, with only the PORT guarded. The scenario is the normal one: one VPS,
# two ideas.
#
# The rule: if this app name is already installed for a DIFFERENT domain, stop and
# explain. Re-installing/upgrading the SAME app on the SAME domain is the intended
# idempotent path and proceeds untouched.
guard_existing_app() {
	[ -f "$ENV_FILE" ] || return 0
	local existing=""
	[ -f "$CADDY_SITE_FILE" ] && existing="$(awk '/^[a-z0-9.-]+ \{/{sub(/ \{.*/,""); print; exit}' "$CADDY_SITE_FILE" 2>/dev/null || true)"
	if [ -z "$existing" ] && [ -f "$CADDYFILE" ]; then
		existing="$(awk '/^[a-z0-9.-]+\.[a-z]+ \{/{sub(/ \{.*/,""); print; exit}' "$CADDYFILE" 2>/dev/null || true)"
	fi
	[ -z "$existing" ] && return 0
	[ "$existing" = "$DOMAIN" ] && return 0
	local suggestion; suggestion="$(printf '%s' "${DOMAIN%%.*}" | tr -cd 'a-z0-9-')"
	[ -z "$suggestion" ] && suggestion="app2"
	die "this box already runs the app \"$APP_NAME\" on $existing, and you asked to install $DOMAIN under the SAME name.

Continuing would replace its systemd unit, overwrite its secrets (breaking every token
it issued and pointing it at another database) and take $existing OFFLINE.

Install the new app SIDE BY SIDE with its own name instead:

  sudo bash $0 --app=$suggestion --domain=$DOMAIN --email=$EMAIL --binary=... --port=<free port>

That gives it its own unit, /etc/$suggestion, /opt/$suggestion, /var/lib/$suggestion,
database, control port and Caddy site — and never touches $existing.

To genuinely MOVE \"$APP_NAME\" to $DOMAIN, remove the old site first
(rm ${CADDY_SITE_FILE#"$PREFIX"}) and re-run."
}

# ── OS / arch detection ──────────────────────────────────────────────────────
detect_os() {
	[ "$(uname -s)" = "Linux" ] || die "this installer supports Linux only (found $(uname -s))"
	[ -r /etc/os-release ] || die "cannot read /etc/os-release — unsupported distro. See docs/PRODUCTION.md for manual steps."
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}:${ID_LIKE:-}" in
		ubuntu:*|debian:*|*:*debian*|*:*ubuntu*) : ;;
		*) die "unsupported distro '${ID:-?}'. This installer targets Ubuntu/Debian (apt). See docs/PRODUCTION.md for the manual steps on other distros." ;;
	esac
	case "$(uname -m)" in
		x86_64|amd64) ARCH="amd64" ;;
		aarch64|arm64) ARCH="arm64" ;;
		*) die "unsupported architecture '$(uname -m)' (supported: x86_64, arm64)" ;;
	esac
	ok "detected ${PRETTY_NAME:-$ID} on ${ARCH}"
}

# ── Pre-flight ───────────────────────────────────────────────────────────────
preflight() {
	if [ "$DRY_RUN" != "yes" ] && [ "$(id -u)" != "0" ]; then
		die "must run as root (sudo). Re-run: sudo bash $0 $ORIG_ARGS"
	fi
	# Detect RAM → a soft memory ceiling. On the single-box stack PostgreSQL shares
	# the RAM, so GOMEMLIMIT must be a conservative FRACTION, never ~90 %-of-total
	# (which would starve Postgres).
	#
	# 30 % is measured, not guessed (scripts/verify-production): with a million rows
	# and live traffic the engine's own anonymous memory stays in the tens of MB, so
	# 30 % is generous headroom while still leaving PostgreSQL its 25 % shared_buffers
	# plus room for the page cache. The earlier 1536 MiB on a 2 GB box was worse than
	# useless — a soft ceiling above what the machine can actually give means the GC
	# never tightens before the box is already dead.
	local mem_kb; mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
	MEM_MB=$(( mem_kb / 1024 ))
	local swap_kb; swap_kb="$(awk '/^SwapTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
	SWAP_MB=$(( swap_kb / 1024 ))
	warn_about_swap
	GOMEMLIMIT_VAL=""
	if [ "$MEM_MB" -gt 0 ]; then
		local lim=$(( MEM_MB * 30 / 100 ))
		[ "$lim" -lt 256 ] && lim=256
		GOMEMLIMIT_VAL="${lim}MiB"
		if [ "$MEM_MB" -le 1280 ]; then
			warn "small box (~${MEM_MB} MiB RAM) — setting GOMEMLIMIT=${GOMEMLIMIT_VAL} (30 % of RAM) to protect against OOM"
		else
			info "~${MEM_MB} MiB RAM — setting GOMEMLIMIT=${GOMEMLIMIT_VAL} (30 % of RAM, leaving room for PostgreSQL)"
		fi
	fi
}

# warn_about_swap (MIGRACION-CONFIANZA-S1, D-bis). A real migration on a 957 MiB
# box with NO swap: the kernel could not page anything out, so under a bulk load
# it OOM-killed PostgreSQL — the PostgreSQL that FIVE apps on that box shared.
# All five went down. With 2 GB of swap the same load was absorbed. The engine
# did not cause that OOM, but this installer deployed onto that box without a
# word about it. It warns — loudly, with the recipe — and never blocks: the
# operator decides; what they cannot do is not know.
SWAP_WARNING=""
warn_about_swap() {
	[ "${MEM_MB:-0}" -gt 0 ] || return 0
	if [ "$MEM_MB" -le 2048 ] && [ "${SWAP_MB:-0}" -eq 0 ]; then
		SWAP_WARNING="this box has ~${MEM_MB} MiB RAM and NO swap"
		warn "$SWAP_WARNING.
  With no swap the kernel cannot page anything out under pressure: a bulk load (a data
  import, a big migration) can make it OOM-kill PostgreSQL — and PostgreSQL is SHARED by
  every app on this box, so one app loading data takes ALL of them down (measured in the
  field: 5 apps, 957 MiB, no swap → postgresql 'oom-kill'). Add swap BEFORE loading data:
    fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
    echo '/swapfile none swap sw 0 0' >> /etc/fstab && sysctl -w vm.swappiness=10
  The engine refuses new writes with a 503 when MemAvailable+SwapFree drops under a floor
  (APPXIMO_MEMORY_GUARD_MIN_MB) — that is degradation, not capacity. Swap is what gives the
  kernel room; the guard only keeps the failure from being silent."
	elif [ "$MEM_MB" -le 1280 ]; then
		info "small box (~${MEM_MB} MiB RAM, ${SWAP_MB} MiB swap) — load data in batches; see docs/PRODUCTION.md §Prerequisites"
	fi
}

# preflight_conflicts: DETECT before we mutate. A port held by a stranger, an
# existing non-ours Caddyfile, or a prior install are reported (and the risky
# ones abort with a clear fix) rather than blindly clobbered.
preflight_conflicts() {
	[ "$DRY_RUN" = "yes" ] && return 0
	# Existing appximo install → this is an upgrade; say so (secrets are reused).
	if [ -f "$ENV_FILE" ] || [ -f "$UNIT_FILE" ]; then
		UPGRADE="yes"
		info "existing install of \"$APP_NAME\" detected — upgrading in place:
    KEPT      secrets, database + data, data dir, control port
    REPLACED  binary ($( [ -n "$BINARY" ] && printf '%s' "$BINARY" || printf 'release download')), unit, env layout, Caddy site, companion scripts
    SCHEMA    $( [ -n "$SCHEMA" ] && printf 'replaced by %s' "$SCHEMA" || printf 'KEPT (no --schema) — verified to belong to this app before the service starts' )"
	fi
	# PORT PREFLIGHT (CTX-PARITY-S1, field report C2). EVERY port this install
	# will bind is checked HERE — before a single file is written — so a
	# collision cannot leave a half-installed app behind (the field report ended
	# a failed attempt with an orphan /etc/<app>/<app>.env). The control port
	# used to be unchecked, which is precisely the one that collides when a
	# SECOND app lands on a box: the data port is chosen by the operator, the
	# control port is derived from the app name.
	if ! command -v ss >/dev/null 2>&1; then
		info "'ss' is not installed (iproute2) — skipping the port preflight; a collision will surface later as a failed start"
	else
		local busy="" p label pair owner
		for pair in "$PORT:--port" "$CONTROL_PORT:--control-port"; do
			p="${pair%%:*}"; label="${pair##*:}"
			ss -ltn "( sport = :$p )" 2>/dev/null | grep -q ":$p " || continue
			# Our OWN service holding it is an upgrade, not a conflict — decided by
			# the PID that holds the port, never by "our unit is active" (that
			# test let a re-run of a live app take a NEIGHBOUR's port: the unit
			# was active, the port was somebody else's, and Caddy was then pointed
			# at the neighbour's engine — MIGRACION-CONFIANZA-S1).
			local our_pid; our_pid="$(systemctl show -p MainPID --value "$SERVICE_NAME" 2>/dev/null || true)"
			if [ -n "$our_pid" ] && [ "$our_pid" != "0" ] && ss -ltnp "( sport = :$p )" 2>/dev/null | grep -q "pid=${our_pid},"; then continue; fi
			owner="$(ss -ltnp "( sport = :$p )" 2>/dev/null | awk 'NR>1{print $NF}' | head -1)"
			[ -n "$owner" ] || owner="an unidentified process"
			busy="${busy}
    port ${p} (${label}) is held by ${owner}"
		done
		if [ -n "$busy" ]; then
			die "these ports are already in use, so nothing was installed:${busy}
  Pick free ones and re-run — the installer is idempotent:
    sudo bash $0 --app=${APP_NAME} --port=<free> --control-port=<free> ...
  Find what is listening:  ss -ltnp | grep -E ':(${PORT}|${CONTROL_PORT})'"
		fi
	fi
	# An existing Caddyfile that isn't ours would be overwritten — back it up first.
	# "Ours" = it proxies this port (pre-OPS-10 inline layout) OR it already
	# imports the per-app site files (the OPS-10 layout, where the port lives in
	# sites/<app>.caddy — the old test backed the file up on EVERY re-run).
	if [ -f "$CADDYFILE" ] && ! grep -q "127.0.0.1:${PORT}" "$CADDYFILE" 2>/dev/null && ! grep -qF "import sites/*.caddy" "$CADDYFILE" 2>/dev/null; then
		local bak; bak="${CADDYFILE}.pre-appximo.$(date +%Y%m%d-%H%M%S)"
		warn "an existing Caddyfile is present and is NOT ours — backing it up to ${bak} before writing appximo' config"
		cp -p "$CADDYFILE" "$bak"
	fi
}

# ── Interactive input (the ONE required decision) ────────────────────────────
gather_input() {
	if [ -z "$DOMAIN" ]; then
		[ "$ASSUME_YES" = "yes" ] && die "--domain is required (non-interactive)"
		ask 'Domain for this API (must already point at this server) : '; DOMAIN="$REPLY"
	fi
	[ -n "$DOMAIN" ] || die "a domain is required"
	valid_domain "$DOMAIN" || die "invalid domain '$DOMAIN' — want a real hostname like api.example.com (not a URL, an IP, a bare name, or one with spaces)"
	if [ -z "$EMAIL" ] && [ "$DRY_RUN" != "yes" ]; then
		[ "$ASSUME_YES" = "yes" ] && die "--email is required for Let's Encrypt (non-interactive)"
		ask "Email for Let's Encrypt (cert expiry notices)          : "; EMAIL="$REPLY"
	fi
	if [ "$DRY_RUN" != "yes" ]; then
		[ -n "$EMAIL" ] || die "an email is required for Let's Encrypt"
		valid_email "$EMAIL" || die "invalid email '$EMAIL' — want something like you@example.com"
	fi
	if [ -z "$BINARY" ] && [ -z "$RELEASE_VERSION" ]; then
		die "no public release yet — pass --binary=/path/to/appximo (build it with scripts/build-engine.sh and scp it up)"
	fi
	if [ -n "$BINARY" ]; then
		[ -f "$BINARY" ] || die "--binary '$BINARY' not found"
		[ -x "$BINARY" ] || die "--binary '$BINARY' is not executable (chmod +x it)"
		# The deployable-binary contract (docs/adr/ADR-023): `<bin> version` must
		# RUN (right arch, real executable) and print an identity line. Behavioral,
		# not branding — the engine and ANY consumer app both qualify; /bin/true
		# and a wrong-arch ELF both fail. The systemd unit will invoke
		# `<bin> serve --schema … --port …`, which the same contract guarantees.
		BIN_ID="$("$BINARY" version 2>/dev/null | head -1)"
		[ -n "$BIN_ID" ] \
			|| die "--binary '$BINARY' does not honor the deployable contract: '<binary> version' must exit 0 and print an identity line (wrong architecture? not an Appximo engine/consumer build?). If it is YOUR app on the appximo framework, wire appximo.ParseServeArgs in main() — see docs/adr/ADR-023-deployable-binary-contract.md"
		ok "binary identifies as: $BIN_ID"
	fi
	if [ -n "$CLI" ]; then
		[ -f "$CLI" ] || die "--cli '$CLI' not found"
		[ -x "$CLI" ] || die "--cli '$CLI' is not executable (chmod +x it)"
		"$CLI" version 2>/dev/null | grep -qi appximo \
			|| die "--cli '$CLI' is not the appximo engine CLI ('version' did not identify it) — build it with scripts/build-engine.sh"
	fi

	echo
	info "About to install Appximo:"
	printf '    domain        %s\n' "$DOMAIN"
	printf '    tls email     %s\n' "${EMAIL:-<dry-run>}"
	printf '    engine        %s (internal port %s)\n' "$BIN_PATH" "$PORT"
	printf '    postgres      native, database "%s" (local, generated password)\n' "$DB_NAME"
	printf '    data dir      %s\n' "$VARLIB"
	printf '    gomemlimit    %s\n' "${GOMEMLIMIT_VAL:-<engine auto/none>}"
	[ "$HARDEN" = "yes" ] && printf '    hardening     ufw + fail2ban + unattended-upgrades\n'
	[ "$DRY_RUN" = "yes" ] && printf '    %s(dry-run: configs generated under %s, no system changes)%s\n' "$C_Y" "${PREFIX:-/}" "$C_N"
	echo
	if [ "$ASSUME_YES" != "yes" ] && [ "$DRY_RUN" != "yes" ]; then
		ask 'Proceed? [y/N] '
		case "$REPLY" in y|Y|yes|YES) : ;; *) die "aborted (re-run with --yes to skip this prompt)" ;; esac
	fi
}

# ── Secrets: reuse on re-run, generate on first install ──────────────────────
rand_hex() { openssl rand -hex "${1:-32}" 2>/dev/null || head -c "${1:-32}" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

load_or_generate_secrets() {
	if [ -f "$ENV_FILE" ]; then
		info "reusing secrets from $ENV_FILE (idempotent re-run)"
		JWT_SECRET="$(grep -E '^JWT_SECRET=' "$ENV_FILE" | head -1 | cut -d= -f2-)"
		ADMIN_KEY="$(grep -E '^ADMIN_KEY=' "$ENV_FILE" | head -1 | cut -d= -f2-)"
		DB_PASS="$(grep -E '^DATABASE_URL=' "$ENV_FILE" | head -1 | sed -E 's#.*://[^:]+:([^@]+)@.*#\1#')"
	fi
	JWT_SECRET="${JWT_SECRET:-$(rand_hex 32)}"
	ADMIN_KEY="${ADMIN_KEY:-$(rand_hex 24)}"
	DB_PASS="${DB_PASS:-$(rand_hex 16)}"
	DATABASE_URL="postgres://${DB_ROLE}:${DB_PASS}@localhost:5432/${DB_NAME}?sslmode=disable"
}

# ── System packages ──────────────────────────────────────────────────────────
install_packages() {
	info "installing packages (postgresql, prerequisites)…"
	# NEEDRESTART_* keeps Ubuntu's needrestart from prompting / slowly deferring
	# service restarts mid-install (observed on a fresh box in PROD-PATH-GOLD-S1).
	export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1
	# `apt-get update` is NON-fatal: a single slow/broken THIRD-PARTY repo left on
	# the box (e.g. a stale docker/cloudsmith list) must not block installing
	# postgresql from the working Ubuntu mirrors. The install below still guards.
	# DPkg::Lock::Timeout: on a fresh box unattended-upgrades often holds the
	# dpkg lock for the first minutes after boot; without a timeout apt fails
	# instantly ("Could not get lock … held by process N (unattended-upgr)")
	# and the install aborts for a reason that fixes itself — wait instead.
	run apt-get -o Acquire::Retries=3 -o DPkg::Lock::Timeout=300 update -qq \
		|| warn "apt-get update had an issue (a slow extra repo?); continuing — postgresql installs from the base mirrors"
	run apt-get install -y -qq -o Acquire::Retries=3 -o DPkg::Lock::Timeout=300 ca-certificates curl gnupg openssl tar postgresql \
		|| die "apt-get install failed. Check network/apt sources (and that no other apt/dpkg is running: ps -C apt-get,dpkg,unattended-upgr), then re-run this installer (it resumes safely)."
	ok "packages ready"
}

# install_caddy: the official Caddy STATIC BINARY + a systemd unit — NOT the
# cloudsmith apt repo. On a fresh box that repo made `apt-get update` hang for
# minutes (PROD-PATH-GOLD-S1), while the binary downloads in <1s and works on any
# distro. AmbientCapabilities lets the non-root caddy user bind 80/443.
install_caddy() {
	if command -v caddy >/dev/null 2>&1; then
		info "caddy already installed — keeping it"
		ensure_caddy_restart_policy
		return
	fi
	if [ "$DRY_RUN" = "yes" ]; then printf '  [dry-run] download Caddy static binary + create caddy user + systemd unit (Restart=always)\n'; return; fi
	local ver="2.8.4"
	local url="https://github.com/caddyserver/caddy/releases/download/v${ver}/caddy_${ver}_linux_${ARCH}.tar.gz"
	info "installing Caddy ${ver} (official static binary)…"
	curl -fsSL -m 90 "$url" -o /tmp/caddy.tgz || die "could not download Caddy ($url). Check network and re-run."
	tar -xzf /tmp/caddy.tgz -C /tmp caddy || die "the Caddy download was not a valid tarball — re-run to retry."
	install -m 0755 /tmp/caddy /usr/bin/caddy; rm -f /tmp/caddy.tgz /tmp/caddy
	id caddy >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/caddy --shell /usr/sbin/nologin caddy
	mkdir -p /etc/caddy
	write_file /etc/systemd/system/caddy.service "[Unit]
Description=Caddy
Documentation=https://caddyserver.com/docs/
After=network.target network-online.target
Requires=network-online.target
StartLimitIntervalSec=0

[Service]
Type=notify
User=caddy
Group=caddy
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force
Restart=always
RestartSec=2s
TimeoutStopSec=5s
LimitNOFILE=1048576
PrivateTmp=true
ProtectSystem=full
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target"
	ensure_caddy_restart_policy
	systemctl daemon-reload
	ok "Caddy ${ver} installed (static binary + systemd unit, Restart=always)"
}

# ensure_caddy_restart_policy: guarantee Caddy comes back if it ever dies.
#
# This is not hypothetical. Measured (scripts/verify-production/chaos.sh): with the
# stock unit, SIGKILLing Caddy took the site down PERMANENTLY — every subsequent
# request was connection-refused until a human intervened, because neither the
# upstream packaged unit nor our own set `Restart=`. The engine had `Restart=always`
# and recovered by itself in seconds; the front door did not.
#
# Written as a systemd DROP-IN rather than by editing the unit, so it applies
# whether Caddy came from our static-binary install or from the distro package,
# and survives a package upgrade replacing the unit file. StartLimitIntervalSec=0
# disables the start-rate limiter — without it systemd stops trying after 5
# restarts in 10 s, which is exactly the situation you need it to keep trying.
ensure_caddy_restart_policy() {
	[ "$DRY_RUN" = "yes" ] && { printf '  [dry-run] ensure caddy.service Restart=always via a systemd drop-in\n'; return 0; }
	local dir="/etc/systemd/system/caddy.service.d"
	mkdir -p "$dir"
	write_file "$dir/10-appximo-restart.conf" "# Managed by the Appximo installer.
# Caddy is the front door: if it dies and does not come back, the site is down
# even though the engine is healthy. See docs/BENCHMARKS.md (resilience).
[Unit]
StartLimitIntervalSec=0

[Service]
Restart=always
RestartSec=2s"
	systemctl daemon-reload >/dev/null 2>&1 || true
	ok "caddy restart policy ensured (Restart=always)"
}

# ── Service user + directories ───────────────────────────────────────────────
setup_user_dirs() {
	if [ "$DRY_RUN" != "yes" ] && ! id "$SERVICE_USER" >/dev/null 2>&1; then
		useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" \
			|| die "could not create the '$SERVICE_USER' system user"
	fi
	run mkdir -p "$ETC_DIR" "$OPT_DIR/bin" "$OPT_DIR/scripts" "$VARLIB/files" "$VARLIB/obs"
	run chown -R "$SERVICE_USER:$SERVICE_USER" "$VARLIB"
	# Explicit modes, NEVER the umask's (LAUNCHPAD-S1, field finding: on a
	# CIS-hardened box with umask 027 the bare mkdir left /etc/<app> at 0750
	# root:root — the service user could not traverse it, and the unit sat in
	# an activating↔restart loop that reads as a silent hang).
	run chmod 0755 "$OPT_DIR" "$OPT_DIR/bin" "$OPT_DIR/scripts"
	# /etc/<app>: root:<service> with the sticky bit (1775). The GROUP write
	# bit is what lets the engine's own deploy path (Studio "restart engine
	# now" → persist boot schema + .bak + marker) work under
	# ProtectSystem=strict; the STICKY bit is what keeps the service from
	# unlinking root's files there — the 0600 root-owned env file stays
	# untouchable even though the directory is group-writable.
	run chown "root:$SERVICE_USER" "$ETC_DIR"
	run chmod 1775 "$ETC_DIR"
	ok "service user + directories ready"
}

# verify_service_can_read: fail AT INSTALL TIME, loudly, if the service user
# cannot read its own schema/config — the alternative is a unit that loops in
# "activating" after every boot with the cause one journalctl away but nobody
# looking (real third-party field report, 2026-08). Root can read anything, so
# the check impersonates the service user.
verify_service_can_read() {
	[ "$DRY_RUN" = "yes" ] && return 0
	local f
	for f in "$SCHEMA_FILE" "$BIN_PATH"; do
		if ! runuser -u "$SERVICE_USER" -- test -r "$f"; then
			die "the '$SERVICE_USER' service user cannot read $f — the service would crash-loop at boot.
  Fix:  chmod 0755 $(dirname "$f")  &&  chmod a+r $f
  (a restrictive umask on this box likely tightened the directory; re-run the installer after fixing)"
		fi
	done
	ok "service user can read its schema + binary (verified as $SERVICE_USER)"
}

# install_companion_scripts: place deploy-update.sh + backup.sh + restore.sh in
# $OPT_DIR/scripts (the path docs/PRODUCTION.md references) when they sit next to
# this installer — i.e. `bash scripts/install.sh` from a checkout, or scp'd
# together. Under `curl | bash` there are no sibling files, so it skips with a
# hint instead of pretending. (Caught in PROD-PATH-HARDEN-S1: the docs pointed at
# /opt/appximo/scripts but nothing put the scripts there.)
install_companion_scripts() {
	# Resolved at parse time (SELF_DIR) — never from a relative $0 after the
	# postgres steps `cd /tmp`. The exec bit of the SOURCE is irrelevant:
	# install(1) sets 0755 on the copy.
	local dir="${SCRIPTS_DIR:-$SELF_DIR}" any="no" missing="" s
	for s in deploy-update.sh backup.sh restore.sh fleet-audit.sh; do
		if [ -n "$dir" ] && [ -f "$dir/$s" ]; then
			run install -m 0755 "$dir/$s" "$OPT_DIR/scripts/$s"; any="yes"
		else
			missing="$missing $s"
		fi
	done
	if [ "$any" = "yes" ] && [ -z "$missing" ]; then
		ok "companion scripts installed in ${OPT_DIR#"$PREFIX"}/scripts (deploy-update.sh, backup.sh, restore.sh, fleet-audit.sh — from $dir)"
	elif [ "$any" = "yes" ]; then
		warn "companion scripts: installed what was in $dir; MISSING:$missing — pass --scripts=DIR or copy them into ${OPT_DIR#"$PREFIX"}/scripts"
	else
		info "companion scripts (deploy-update.sh/backup.sh/restore.sh) not found in ${dir:-<unknown>} — pass --scripts=DIR pointing at the repo's scripts/ (or copy them into ${OPT_DIR#"$PREFIX"}/scripts) when you need updates/backups"
	fi
}

# enable_checksums_if_fresh: turn on PostgreSQL data_checksums so page corruption
# is a loud ERROR on first read instead of silent bad data (CAOS-S1 / OPS-42:
# a corrupt table was served 200s; the nightly backup was the only detector).
# Cost measured: 0.9 s on a 372 MB cluster — instant on the EMPTY cluster of a
# fresh install. But it is CLUSTER-WIDE and needs the cluster stopped, so it runs
# ONLY when this is a fresh cluster (no user database but the one we just made):
# on a box already serving another app, enabling means downtime over its data —
# the operator's maintenance window, not the installer's. Otherwise: warn + recipe.
enable_checksums_if_fresh() {
	[ "$DRY_RUN" = "yes" ] && { printf '  [dry-run] enable data_checksums if this is a fresh cluster
'; return 0; }
	command -v pg_checksums >/dev/null 2>&1 || return 0
	local cur; cur="$(runuser -u postgres -- psql -tAX -c 'SHOW data_checksums' 2>/dev/null || echo '?')"
	[ "$cur" = "on" ] && { ok "PostgreSQL data_checksums already on (corruption is a loud error, not silent)"; return 0; }
	[ "$cur" = "off" ] || { info "could not read data_checksums — skipping"; return 0; }
	# Fresh? No user DB other than the one this install created (and it is empty).
	local others; others="$(runuser -u postgres -- psql -tAX -c "SELECT count(*) FROM pg_database WHERE datname NOT IN ('template0','template1','postgres','${DB_NAME}') AND datistemplate=false" 2>/dev/null || echo '?')"
	local mysize; mysize="$(runuser -u postgres -- psql -tAX -d "${DB_NAME}" -c "SELECT count(*) FROM pg_stat_user_tables" 2>/dev/null || echo '?')"
	if [ "$others" != "0" ] || [ "${mysize:-1}" != "0" ]; then
		warn "data_checksums is OFF and this cluster already holds data (other apps / existing tables) — NOT enabling (it needs the whole cluster stopped, downtime over that data). Enable in your window: systemctl stop postgresql@*-main; runuser -u postgres -- pg_checksums --enable -D <datadir>; systemctl start postgresql@*-main  (~1 s per 400 MB). docs/PRODUCTION.md §4"
		return 0
	fi
	local unit datadir t0 t1
	unit="$(systemctl list-units --type=service --all --no-legend 'postgresql@*-main.service' 2>/dev/null | awk '{print $1}' | head -1)"
	datadir="$(runuser -u postgres -- psql -tAX -c 'SHOW data_directory' 2>/dev/null)"
	[ -n "$unit" ] && [ -n "$datadir" ] || { info "could not resolve the cluster unit/datadir — skipping checksums"; return 0; }
	info "fresh cluster — enabling data_checksums (page-corruption becomes a loud error)…"
	systemctl stop "$unit"
	t0="$(date +%s.%N)"
	if runuser -u postgres -- pg_checksums --enable -D "$datadir" >/dev/null 2>&1; then
		t1="$(date +%s.%N)"
		systemctl start "$unit"; for _ in $(seq 1 30); do runuser -u postgres -- psql -tAX -c 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
		ok "data_checksums enabled ($(awk -v a="$t0" -v b="$t1" 'BEGIN{printf "%.1f", b-a}') s) — corruption is now an ERROR on read, not silent"
	else
		systemctl start "$unit"
		warn "pg_checksums --enable failed — cluster restarted with checksums still off; enable manually later (docs/PRODUCTION.md §4)"
	fi
}

# ensure_postgres_restart: Ubuntu/Debian ship postgresql@NN-main with Restart=no
# ("restarting automatically will prevent pg_ctlcluster stop from working"), so an
# OOM-KILLED or crashed POSTMASTER stays down — and with it every app on the box —
# until a human runs `systemctl start`. That is precisely the field OOM incident
# (MIGRACION-CONFIANZA-S1: 5 apps down on an oom-kill) and CAOS-S1 D2 reproduced
# it. A drop-in restores auto-recovery WITHOUT fighting an intentional stop
# (RestartPreventExitStatus keeps SIGINT/SIGTERM from triggering a restart), and
# the default start limit (5/10 s) still lets a genuinely broken cluster give up
# instead of crash-looping. Idempotent; only touches the instance systemd finds.
ensure_postgres_restart() {
	[ "$DRY_RUN" = "yes" ] && { printf '  [dry-run] systemd drop-in: postgresql instance Restart=on-failure (survive an OOM-kill)
'; return 0; }
	local unit; unit="$(systemctl list-units --type=service --all --no-legend 'postgresql@*-main.service' 2>/dev/null | awk '{print $1}' | head -1)"
	[ -n "$unit" ] || unit="$(ls /lib/systemd/system/postgresql@*.service 2>/dev/null | head -1 | xargs -r basename)"
	[ -n "$unit" ] || { info "no postgresql@NN-main unit found — skipping the restart drop-in (is Postgres native systemd-managed?)"; return 0; }
	local dir="/etc/systemd/system/${unit}.d"
	mkdir -p "$dir"
	cat > "$dir/restart.conf" <<-EOF
		# Appximo installer (CAOS-S1): survive an OOM-kill / postmaster crash so the
		# apps that share this database come back without a human. An intentional
		# stop (pg_ctlcluster / systemctl stop → SIGINT/SIGTERM) still stops.
		[Service]
		Restart=on-failure
		RestartSec=2
		RestartPreventExitStatus=SIGINT SIGTERM
	EOF
	systemctl daemon-reload
	ok "postgresql auto-restart ensured ($unit: Restart=on-failure — survives an OOM-kill; intentional stop still stops)"
}

# ── PostgreSQL: role + database (idempotent) ─────────────────────────────────
setup_postgres() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] create role %s + database %s (if absent)\n' "$DB_ROLE" "$DB_NAME"
		printf '  [dry-run] tune postgresql for this box: shared_buffers=%sMB effective_cache_size=%sMB max_connections=%s\n' \
			"$(( MEM_MB / 4 ))" "$(( MEM_MB * 55 / 100 ))" "$([ "$MEM_MB" -ge 4096 ] && echo 100 || echo 50)"
		return
	fi
	systemctl enable --now postgresql >/dev/null 2>&1 || die "postgresql did not start (systemctl status postgresql). Re-run after fixing."
	# Wait for the socket (fresh installs take a moment to accept connections).
	local i; for i in $(seq 1 15); do pg_ready && break; sleep 1; done
	# cd /tmp: see pg_ready — postgres cannot chdir into /root, and the warning
	# it prints on every call pollutes an output the operator is asked to read.
	cd /tmp || true
	local psql="runuser -u postgres -- psql -tAX"
	if [ "$($psql -c "SELECT 1 FROM pg_roles WHERE rolname='${DB_ROLE}'")" != "1" ]; then
		$psql -c "CREATE ROLE ${DB_ROLE} LOGIN PASSWORD '${DB_PASS}'" || die "could not create the postgres role"
	else
		# Re-run realigns the password to the (reused) env file, so they never drift.
		$psql -c "ALTER ROLE ${DB_ROLE} WITH PASSWORD '${DB_PASS}'" || die "could not align the postgres role password"
	fi
	if [ "$($psql -c "SELECT 1 FROM pg_database WHERE datname='${DB_NAME}'")" != "1" ]; then
		$psql -c "CREATE DATABASE ${DB_NAME} OWNER ${DB_ROLE}" || die "could not create the ${DB_NAME} database"
	fi
	tune_postgres
	ok "postgresql role + database ready (control plane is bootstrapped by the engine on boot)"
}

# tune_postgres: size PostgreSQL for THIS box instead of leaving the packaged
# defaults, which are calibrated for a machine much smaller than a modern VPS.
#
# Two of the defaults are actively wrong on a small box and were measured as such
# (scripts/verify-production, docs/BENCHMARKS.md):
#
#   shared_buffers        ships at 128 MB regardless of the machine. A real
#                         dataset outgrows it immediately and scans fall back to
#                         re-reading through the OS cache.
#   effective_cache_size  ships at 4 GB regardless of the machine. On a 2 GB box
#                         the planner is told about cache that cannot exist, and
#                         prices plans against a fiction. It allocates NOTHING —
#                         it is purely a hint — so correcting it is free.
#
# The values are deliberately conservative: this stack shares the box with the
# engine and Caddy, so 25 % shared_buffers (the standard starting point), a modest
# per-operation work_mem (it is per SORT, not per connection — the classic way to
# OOM a small box), and a connection ceiling in proportion to the engine's own
# small pool.
#
# Written to conf.d as a separate file so it is obvious, revertible (delete the
# file) and never fights the package's own postgresql.conf. Idempotent.
tune_postgres() {
	local conf_file conf_dir
	conf_file="$( (cd /tmp && runuser -u postgres -- psql -tAX -c 'SHOW config_file') 2>/dev/null | head -1 || true)"
	[ -n "$conf_file" ] || { warn "could not locate postgresql.conf — skipping PostgreSQL tuning (defaults kept)"; return 0; }
	conf_dir="$(dirname "$conf_file")/conf.d"
	mkdir -p "$conf_dir"

	# conf.d is only read if postgresql.conf includes it (Debian/Ubuntu packages do).
	if ! grep -qE "^[[:space:]]*include_dir[[:space:]]*=[[:space:]]*'conf\.d'" "$conf_file"; then
		printf "\n# added by the Appximo installer\ninclude_dir = 'conf.d'\n" >> "$conf_file"
	fi

	local sb ecs wm mwm mc
	sb=$(( MEM_MB / 4 ))
	ecs=$(( MEM_MB * 55 / 100 ))
	wm=4; [ "$MEM_MB" -ge 4096 ] && wm=8
	mwm=$(( MEM_MB / 16 )); [ "$mwm" -lt 64 ] && mwm=64
	mc=50; [ "$MEM_MB" -ge 4096 ] && mc=100
	# Below ~700 MiB the derived shared_buffers would drop under PostgreSQL's own
	# default; leave the packaged config alone rather than making things worse.
	if [ "$sb" -lt 128 ]; then
		info "tiny box (~${MEM_MB} MiB) — keeping PostgreSQL's packaged defaults"
		return 0
	fi

	local tuning_file="$conf_dir/99-appximo-tuning.conf"
	local desired="# Appximo — PostgreSQL sizing for this box (${MEM_MB} MiB RAM, $(nproc) vCPU).
# Written by scripts/install.sh. Delete this file and restart PostgreSQL to
# return to the packaged defaults. See docs/BENCHMARKS.md for the measurements.
shared_buffers = ${sb}MB
effective_cache_size = ${ecs}MB
work_mem = ${wm}MB
maintenance_work_mem = ${mwm}MB
max_connections = ${mc}"

	# NO-CHANGE GUARD (CTX-PARITY-S1, field report C1). PostgreSQL here is a
	# SHARED service: restarting it interrupts every app on the box. Installing a
	# second app used to rewrite this file unconditionally and restart anyway —
	# and when the values were already identical (an earlier install put them
	# there), two neighbouring production apps took a ~3 s blackout for nothing.
	# A restart is defensible when something changed; a restart when nothing
	# changed is pure damage to somebody else's users.
	if [ -f "$tuning_file" ] && [ "$(cat "$tuning_file" 2>/dev/null)" = "$desired" ]; then
		ok "postgresql tuning already matches this box — left untouched, NOT restarted (no neighbour is disturbed)"
		return 0
	fi

	if [ "$DRY_RUN" != "yes" ] && [ -f "$tuning_file" ]; then
		info "postgresql tuning differs from the desired sizing — updating it (this DOES restart the shared PostgreSQL)"
	fi
	write_file "$tuning_file" "$desired"
	chown postgres:postgres "$tuning_file" 2>/dev/null || true
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] systemctl restart postgresql (tuning changed)\n'
		return 0
	fi
	# shared_buffers and max_connections need a full restart, not a reload.
	systemctl restart postgresql >/dev/null 2>&1 \
		|| warn "PostgreSQL did not restart after tuning — check journalctl -u postgresql (remove $tuning_file to revert)"
	local i; for i in $(seq 1 20); do pg_ready && break; sleep 1; done
	ok "postgresql tuned for this box (shared_buffers=${sb}MB, effective_cache_size=${ecs}MB, work_mem=${wm}MB, max_connections=${mc})"
}

# pg_ready is the quiet "is PostgreSQL answering?" probe. `runuser -u postgres`
# inherits the caller's cwd, and when that is /root (the normal place to run the
# installer from) postgres cannot chdir there, so EVERY invocation prints
# "could not change directory to /root: Permission denied" — noise in an output
# the installer explicitly asks the operator to read (field report C3). Running
# from a directory postgres can enter removes the message at the source instead
# of hiding it with 2>/dev/null, which would also hide real errors.
pg_ready() {
	(cd /tmp && runuser -u postgres -- psql -tAc 'SELECT 1' >/dev/null 2>&1)
}

# ── Engine binary ────────────────────────────────────────────────────────────
install_binary() {
	if [ -n "$BINARY" ]; then
		info "installing engine binary from $BINARY"
		run install -m 0755 "$BINARY" "$BIN_PATH"
		install_ops_cli
	else
		# Ready for when releases exist (RELEASE_VERSION set): download + checksum.
		local base="https://github.com/${REPO}/releases/download/${RELEASE_VERSION}"
		local asset="appximo-${RELEASE_VERSION}-linux-${ARCH}"
		info "downloading engine ${RELEASE_VERSION} (${ARCH})…"
		run curl -fLo /tmp/appximo "${base}/${asset}"
		run curl -fLo /tmp/appximo.checksums "${base}/checksums.txt"
		if [ "$DRY_RUN" != "yes" ]; then
			( cd /tmp && grep " ${asset}\$" appximo.checksums | sed "s/${asset}/appximo/" | sha256sum -c - ) \
				|| die "checksum verification failed"
		fi
		run install -m 0755 /tmp/appximo "$BIN_PATH"
	fi
	ok "engine installed at $BIN_PATH"
}

# install_ops_cli: the OPS COMPANION (ADR-023). A production deploy is the app
# binary (serves) plus the engine CLI (operates: tenant, migrate, token, admin
# create). When the installed binary IS the engine, appximo-cli is a symlink
# to it — one documented invocation works on every box. A consumer binary has no
# ops subcommands (its `version`-contract error even says so), so --cli installs
# the real engine CLI beside it; without --cli the gap is NAMED, not silent.
install_ops_cli() {
	local cli_path="$OPT_DIR/bin/appximo-cli"
	if [ -n "$CLI" ]; then
		run install -m 0755 "$CLI" "$cli_path"
		ok "ops CLI installed at ${cli_path#"$PREFIX"} (tenant / migrate / token / admin create)"
		return
	fi
	[ "$DRY_RUN" = "yes" ] && { printf '  [dry-run] detect ops subcommands; symlink appximo-cli if the binary is the engine\n'; return; }
	# Detect: the engine's CLI answers `tenant --help`; a consumer binary refuses
	# any subcommand but version/serve (per the contract).
	if "$BIN_PATH" tenant --help >/dev/null 2>&1; then
		ln -sfn "$BIN_PATH" "$cli_path"
		ok "ops CLI: this binary IS the engine — appximo-cli symlinked to it"
	else
		# A symlink left by an EARLIER install of the stock engine now points at
		# a consumer binary that has no ops subcommands: the CLI is broken
		# silently ("appximo-cli tenant list" → unknown subcommand). Remove it
		# and say so — a dangling companion is worse than a named gap.
		if [ -L "$cli_path" ] && [ "$(readlink -f "$cli_path" 2>/dev/null)" = "$(readlink -f "$BIN_PATH" 2>/dev/null)" ]; then
			rm -f "$cli_path"
			warn "removed the stale ${cli_path#"$PREFIX"} symlink: it pointed at this app's own binary, which is a CONSUMER build with no ops subcommands"
		fi
		warn "this is a CONSUMER binary (serves, but has no ops subcommands). To operate its database (register tenants, migrate, mint tokens, create the super-admin) install the engine CLI: re-run with --cli=/path/to/appximo (build: scripts/build-engine.sh) — see docs/adr/ADR-023"
	fi
}

# ── Boot schema ──────────────────────────────────────────────────────────────
# schema_name FILE — the top-level "name" of a schema JSON (python3 when
# present, else a first-key grep — the top-level name precedes any resource).
schema_name() {
	[ -f "$1" ] || { printf ''; return; }
	if command -v python3 >/dev/null 2>&1; then
		python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1])).get("name",""))
except Exception: print("")' "$1" 2>/dev/null
	else
		grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' "$1" 2>/dev/null | head -1 | sed -E 's/.*:[[:space:]]*"([^"]*)"/\1/'
	fi
}

# foreign_schema_guard FILE — refuse a schema that belongs to ANOTHER app on
# this box: byte-identical to, or carrying the `name` of, a sibling
# /etc/<other>/schema.json. This is the check that would have stopped
# "GET /api/asambleas → 200 under the new domain": a re-run without --schema
# kept a schema a previous session had left there, and the tenant registration
# the summary prints (`$(cat /etc/<app>/schema.json)`) then served another
# application's resources. Never registers a tenant with someone else's schema.
foreign_schema_guard() {
	local file="$1" name other oname osum sum
	[ -f "$file" ] || return 0
	name="$(schema_name "$file")"
	# A schema whose `name` IS this app's name is this app's, whatever a sibling
	# holds: when two apps carry identical bytes, the one whose name matches its
	# app is the owner and the other one is the copy — the guard fires on the
	# copy, never on the owner (a legitimate upgrade of the victim must proceed).
	[ -n "$name" ] && [ "$name" = "$APP_NAME" ] && return 0
	sum="$(sha256sum "$file" 2>/dev/null | cut -d' ' -f1)"
	for other in "$PREFIX"/etc/*/schema.json; do
		[ -f "$other" ] || continue
		[ "$other" = "$file" ] && continue
		case "$other" in "$PREFIX/etc/$APP_NAME/"*) continue ;; esac
		osum="$(sha256sum "$other" 2>/dev/null | cut -d' ' -f1)"
		oname="$(schema_name "$other")"
		if [ -n "$sum" ] && [ "$sum" = "$osum" ]; then
			die "the schema at ${file#"$PREFIX"} is BYTE-IDENTICAL to ${other#"$PREFIX"} — it is another application's schema, not \"$APP_NAME\"'s.
  Continuing would register tenants of \"$APP_NAME\" with that other app's resources and serve them under $DOMAIN.
  Fix: re-run with --schema=/path/to/${APP_NAME}-schema.json (the model THIS app should serve)."
		fi
		if [ -n "$name" ] && [ "$name" = "$oname" ]; then
			die "the schema at ${file#"$PREFIX"} is named \"$name\" — the same name as ${other#"$PREFIX"}, another application on this box.
  Continuing would serve that application's resources under $DOMAIN.
  Fix: re-run with --schema=/path/to/${APP_NAME}-schema.json (the model THIS app should serve)."
		fi
	done
	return 0
}

write_schema() {
	if [ -f "$SCHEMA_FILE" ] && [ -z "$SCHEMA" ]; then
		foreign_schema_guard "$SCHEMA_FILE"
		local kept; kept="$(schema_name "$SCHEMA_FILE")"
		info "keeping existing $SCHEMA_FILE (schema name: \"${kept:-?}\"; pass --schema to replace it)"
		if [ -n "$kept" ] && [ "$kept" != "$APP_NAME" ]; then
			info "note: the schema's name (\"$kept\") differs from the app name (\"$APP_NAME\") — fine if intended; no other app on this box carries it"
		fi
		schema_perms
		return
	fi
	if [ -n "$SCHEMA" ]; then
		[ -f "$SCHEMA" ] || die "--schema '$SCHEMA' not found"
		run install -m 0644 "$SCHEMA" "$SCHEMA_FILE"
	else
		# A minimal, valid starter so the engine boots immediately. Replace it via
		# the visual editor (/editor) or re-run with --schema, then restart.
		write_file "$SCHEMA_FILE" '{
  "$schema": "https://appximo.com/schema/v1",
  "version": "1",
  "name": "starter-api",
  "resources": {
    "tasks": {
      "fields": {
        "title":  { "type": "string", "required": true, "maxLength": 200 },
        "status": { "type": "string", "enum": ["open", "done"] }
      }
    }
  },
  "rbac": { "roles": { "admin": { "resources": "*", "actions": ["*"] } } }
}'
	fi
	schema_perms
	ok "boot schema at $SCHEMA_FILE"
}

# schema_perms: the boot schema belongs to the SERVICE user, explicitly —
# (a) umask-proof (write_file inherits the umask; 027 would make it 0640
# root:root and unreadable to the service), and (b) owner-writable by the
# service so the engine's own deploy-persist path (Studio one-click restart)
# can update it in place.
schema_perms() {
	[ -f "$SCHEMA_FILE" ] || return 0
	run chown "$SERVICE_USER:$SERVICE_USER" "$SCHEMA_FILE"
	run chmod 0644 "$SCHEMA_FILE"
}

# ── Config file writers ──────────────────────────────────────────────────────
# write_file PATH CONTENT — writes (or prints, in dry-run) then reports.
write_file() {
	local path="$1"; shift
	mkdir -p "$(dirname "$path")"
	printf '%s\n' "$1" > "$path" || die "could not write $path"
	# MUST be an `if` (not `[ … ] && …`): a trailing `&&` that short-circuits
	# returns 1, and under `set -e` this function is called as a plain statement,
	# so a non-zero return aborts the whole install — a bug that only bites the
	# REAL (non-dry-run) path (caught live in PROD-PATH-HARDEN-S1).
	if [ "$DRY_RUN" = "yes" ]; then printf '  [dry-run] wrote %s\n' "$path"; fi
}

write_env_file() {
	# CAOS-S1: a re-run must not destroy OPERATOR CONFIG. The 58's real envs
	# carry keys this template never writes (a theme, demo roles, a banner, a
	# raised login limit, legacy dual-prefix keys) — the old wholesale rewrite
	# silently dropped them all, which would have stripped the public demo of
	# its demo mode on the first upgrade. Every line of an EXISTING env that is
	# not one of the MANAGED keys below (and not the template's own header) is
	# carried over verbatim — comments included, order kept.
	local managed=" DATABASE_URL JWT_SECRET ADMIN_KEY APPXIMO_ENV APPXIMO_CONTROL_PORT APPXIMO_FILES_DIR OBS_DB_PATH GOMEMLIMIT APPXIMO_BACKUP_DIR "
	local extra=""
	if [ -f "$ENV_FILE" ]; then
		local line key
		while IFS= read -r line; do
			case "$line" in
				"# Appximo engine environment"*|"# Appitools engine environment"*|"# Generated by scripts/install.sh"*|"# Where backup.sh writes its sets"*|"# the newest one and last-backup.status"*|"# missing or failed (docs/PRODUCTION.md"*|"# remote:bucket/path ships each set"*|"# include the encrypted secrets)."*|"# ── kept from the previous env"*) continue ;;
				"") continue ;;
				"#"*) extra="${extra}${line}
"; continue ;;
			esac
			key="${line%%=*}"
			case "$managed" in
				*" $key "*) : ;;
				*) extra="${extra}${line}
" ;;
			esac
		done < "$ENV_FILE"
	fi
	local body="# Appximo engine environment (systemd EnvironmentFile — plain KEY=value, no export, no quotes).
# Generated by scripts/install.sh. Secrets are reused on re-run; keep this file 0600.
DATABASE_URL=${DATABASE_URL}
JWT_SECRET=${JWT_SECRET}
ADMIN_KEY=${ADMIN_KEY}
APPXIMO_ENV=production
APPXIMO_CONTROL_PORT=${CONTROL_PORT}
APPXIMO_FILES_DIR=${VARLIB#"$PREFIX"}/files
OBS_DB_PATH=${VARLIB#"$PREFIX"}/obs/obs.db
# Where backup.sh writes its sets; the engine's self-monitor reads the age of
# the newest one and last-backup.status here and alerts when a backup is
# missing or failed (docs/PRODUCTION.md §4). BACKUP_COPY_TO=user@host:/dir or
# remote:bucket/path ships each set off this box (BACKUP_PASSPHRASE_FILE to
# include the encrypted secrets).
APPXIMO_BACKUP_DIR=${BACKUP_DIR#"$PREFIX"}"
	[ -n "$GOMEMLIMIT_VAL" ] && body="${body}
GOMEMLIMIT=${GOMEMLIMIT_VAL}"
	if [ -n "$extra" ]; then
		body="${body}

# ── kept from the previous env (operator config — not managed by the installer) ──
${extra%
}"
	fi
	write_file "$ENV_FILE" "$body"
	run chmod 600 "$ENV_FILE"
	run chown "root:$SERVICE_USER" "$ENV_FILE"
	ok "wrote $ENV_FILE (0600 root:$SERVICE_USER)"
}

write_systemd_unit() {
	write_file "$UNIT_FILE" "[Unit]
Description=Appximo app ${APP_NAME}
Documentation=https://github.com/${REPO}/blob/main/docs/PRODUCTION.md
Wants=network-online.target
After=network-online.target postgresql.service
# RESILIENCIA-S1: never give up restarting. A database that fails to come up
# at boot makes the engine exit every RestartSec; with systemd's default start
# limit (5 in 10 s) a 2 s RestartSec would reach it and the unit would stay
# DOWN after PostgreSQL recovered. Provoked and measured: with the limit off
# the engine loops harmlessly and is serving 6 s after the database returns.
StartLimitIntervalSec=0

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=${ENV_FILE#"$PREFIX"}
ExecStart=${BIN_PATH#"$PREFIX"} serve --schema ${SCHEMA_FILE#"$PREFIX"} --port ${PORT} --control-port ${CONTROL_PORT}
Restart=always
RestartSec=2
LimitNOFILE=4096
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${VARLIB#"$PREFIX"} ${ETC_DIR#"$PREFIX"}
StateDirectory=${APP_NAME}

[Install]
WantedBy=multi-user.target"
	ok "wrote $UNIT_FILE"
}

# write_backup_timer: <app>-backup.service (oneshot → backup.sh --app) + a
# calendar timer. Persistent=true runs a missed backup at the next boot;
# RandomizedDelaySec spreads several apps on one box. The unit exists only when
# backup.sh was installed (no silent timer firing a missing script).
write_backup_timer() {
	if [ "$BACKUP_TIMER" != "yes" ]; then info "backup timer not installed (--no-backup-timer)"; return 0; fi
	if [ ! -x "$OPT_DIR/scripts/backup.sh" ] && [ "$DRY_RUN" != "yes" ]; then
		warn "backup timer NOT installed: ${OPT_DIR#"$PREFIX"}/scripts/backup.sh is missing (pass --scripts=DIR) — this app has NO scheduled backup"
		return 0
	fi
	write_file "$BACKUP_SERVICE_FILE" "[Unit]
Description=Appximo backup of app ${APP_NAME} (pg_dump + files + config → ${BACKUP_DIR#"$PREFIX"})
Documentation=https://github.com/${REPO}/blob/main/docs/PRODUCTION.md
After=postgresql.service

[Service]
Type=oneshot
ExecStart=${OPT_DIR#"$PREFIX"}/scripts/backup.sh --app=${APP_NAME}
Nice=10
IOSchedulingClass=idle"
	write_file "$BACKUP_TIMER_FILE" "[Unit]
Description=Nightly backup timer of app ${APP_NAME}

[Timer]
OnCalendar=${BACKUP_SCHEDULE}
Persistent=true
RandomizedDelaySec=300

[Install]
WantedBy=timers.target"
	ok "wrote ${BACKUP_SERVICE_FILE#"$PREFIX"} + ${BACKUP_TIMER_FILE#"$PREFIX"} (OnCalendar=${BACKUP_SCHEDULE}, keeps 14 sets in ${BACKUP_DIR#"$PREFIX"})"
}

write_caddyfile() {
	# Caddy terminates TLS (automatic Let's Encrypt) and reverse-proxies to the
	# engine, passing the Host header through unchanged (tenant routing depends on
	# it). request_body caps uploads at 25MB at the edge; the engine also enforces
	# its own APPXIMO_FILES_MAX_BYTES. SSE (text/event-stream) is auto-flushed by
	# reverse_proxy, so /api/*/events works with no extra config.
	#
	# OPS-10: this app's site lives in ITS OWN file under /etc/caddy/sites, and the
	# main Caddyfile only IMPORTS that directory. Installing a second app appends a
	# file; it can never erase the first app's site — which is exactly what the
	# previous wholesale overwrite did.
	local tls_line=""
	[ "$INTERNAL_TLS" = "yes" ] && tls_line="
	tls internal"
	write_file "$CADDY_SITE_FILE" "# Appximo app: ${APP_NAME}  (managed by scripts/install.sh — edits are overwritten on re-run)
${DOMAIN} {${tls_line}
	request_body {
		max_size 25MB
	}
	reverse_proxy 127.0.0.1:${PORT}
}"
	ok "wrote $CADDY_SITE_FILE"
	ensure_caddy_imports
}

# ensure_caddy_imports guarantees /etc/caddy/Caddyfile pulls in the per-app site
# files. It is deliberately ADDITIVE on an existing file: a box that already had a
# hand-written or previously-generated Caddyfile keeps every line it had, and only
# gains the import — so upgrading an existing install never drops a site.
ensure_caddy_imports() {
	local import_line="import sites/*.caddy"
	if [ ! -f "$CADDYFILE" ]; then
		write_file "$CADDYFILE" "{
	email ${EMAIL}
}

${import_line}"
		ok "wrote $CADDYFILE (global options + per-app site imports)"
		return
	fi
	if grep -qF "$import_line" "$CADDYFILE" 2>/dev/null; then
		return
	fi
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] append %s to %s (existing content preserved)\n' "$import_line" "$CADDYFILE"
		return
	fi
	local bak; bak="${CADDYFILE}.pre-${APP_NAME}.$(date +%Y%m%d-%H%M%S)"
	cp -p "$CADDYFILE" "$bak"
	# A pre-OPS-10 Caddyfile has this app's site inline; the site file now owns it,
	# so drop the inline block to avoid Caddy refusing a duplicate site address.
	if grep -qE "^[[:space:]]*${DOMAIN//./\\.}[[:space:]]*\{" "$CADDYFILE"; then
		awk -v d="$DOMAIN" '
			$0 ~ "^[[:space:]]*" d "[[:space:]]*\\{" { skip=1; depth=0 }
			skip { depth += gsub(/\{/,"{"); depth -= gsub(/\}/,"}"); if (depth<=0) skip=0; next }
			{ print }' "$bak" > "$CADDYFILE"
		info "moved the inline ${DOMAIN} block into ${CADDY_SITE_FILE#"$PREFIX"} (backup: $bak)"
	fi
	printf '\n%s\n' "$import_line" >> "$CADDYFILE"
	ok "Caddyfile now imports per-app sites (backup: $bak)"
}

# ── Start / reload ───────────────────────────────────────────────────────────
start_services() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] systemctl daemon-reload; enable --now %s; reload caddy\n' "$SERVICE_NAME"
		return
	fi
	systemctl daemon-reload
	systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
	systemctl restart "$SERVICE_NAME" || { journalctl -u "$SERVICE_NAME" -n 30 --no-pager 2>/dev/null || true; die "the engine failed to start — see the log above (journalctl -u $SERVICE_NAME -f)"; }
	# Caddy (installed by install_caddy): enable for reboot, then (re)start with the
	# new Caddyfile. reload fails if it isn't running yet → restart cold-starts it.
	systemctl enable caddy >/dev/null 2>&1 || true
	systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null \
		|| warn "could not (re)start caddy — check: caddy validate --config $CADDYFILE ; journalctl -u caddy -f"
	ok "services started (appximo + caddy)"
	if [ -f "$BACKUP_TIMER_FILE" ]; then
		mkdir -p "$BACKUP_DIR" && chmod 711 "$BACKUP_DIR"  # 0711: the engine (unprivileged) must traverse to read last-backup.status; conf bundle stays 0600
		systemctl enable --now "${SERVICE_NAME}-backup.timer" >/dev/null 2>&1 \
			&& ok "backup timer enabled (next: $(systemctl show -p NextElapseUSecRealtime --value "${SERVICE_NAME}-backup.timer" 2>/dev/null || echo '?'))" \
			|| warn "could not enable ${SERVICE_NAME}-backup.timer — systemctl status ${SERVICE_NAME}-backup.timer"
	fi
}

# ── Health verification ──────────────────────────────────────────────────────
verify() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] would poll http://127.0.0.1:%s/healthz then https://%s/healthz\n' "$PORT" "$DOMAIN"
		return
	fi
	info "waiting for the engine to answer locally…"
	local i
	for i in $(seq 1 30); do
		if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
			ok "engine healthy on 127.0.0.1:${PORT}"; break
		fi
		[ "$i" = "30" ] && { journalctl -u "$SERVICE_NAME" -n 40 --no-pager 2>/dev/null || true; die "engine did not become healthy — see the log above (journalctl -u $SERVICE_NAME -f)"; }
		sleep 1
	done
	CURL_TLS=""; [ "$INTERNAL_TLS" = "yes" ] && CURL_TLS="-k"
	PUBLIC_OK="no"
	if [ "$INTERNAL_TLS" = "yes" ]; then info "waiting for public HTTPS (Caddy is issuing a LOCAL certificate — --internal-tls)…"
	else info "waiting for public HTTPS (Caddy is issuing the Let's Encrypt certificate — first issue can take ~30s)…"; fi
	for i in $(seq 1 40); do
		# shellcheck disable=SC2086
		if curl -fsS $CURL_TLS "https://${DOMAIN}/healthz" >/dev/null 2>&1; then
			ok "public HTTPS live: https://${DOMAIN}/healthz"; PUBLIC_OK="yes"; return
		fi
		sleep 3
	done
	warn "https://${DOMAIN}/healthz not answering yet. The engine is up locally; this is almost always DNS or the firewall. Check: dig +short ${DOMAIN} (should be this box's IP); ports 80+443 open; journalctl -u caddy -f"
}

# ── Post-install verification: what was ASKED == what is INSTALLED ───────────
# The installer's log said "✓ engine installed" on a box whose /health kept
# answering an older version and whose schema belonged to another app. A log
# line is a claim; this is the check. Every mismatch is FATAL (exit 1) even
# though the service is up — an install that is not what was asked for is not
# a success with a footnote.
VERIFIED_LINES=""
vline() { VERIFIED_LINES="${VERIFIED_LINES}
    $*"; }
health_version() { curl -fsS -m 5 "$@" 2>/dev/null | grep -o '"version":"[^"]*"' | head -1 | cut -d'"' -f4; }
verify_installed() {
	[ "$DRY_RUN" = "yes" ] && return 0
	info "verifying the install against what was asked…"
	# 1. The installed binary IS the one given (checksum, not name).
	if [ -n "$BINARY" ]; then
		local want have
		want="$(sha256sum "$BINARY" | cut -d' ' -f1)"; have="$(sha256sum "$BIN_PATH" | cut -d' ' -f1)"
		[ "$want" = "$have" ] || die "VERIFY FAILED: ${BIN_PATH#"$PREFIX"} (sha256 ${have:0:12}…) is NOT the --binary you gave (${want:0:12}…). Nothing else was checked. Re-run; if it persists, inspect ${OPT_DIR#"$PREFIX"}/bin by hand."
		vline "binary    ${BIN_PATH#"$PREFIX"} == $BINARY (sha256 ${have:0:12}…)"
	fi
	# 2. The RUNNING service reports the installed binary's version — locally…
	local expect_id got
	expect_id="$("$BIN_PATH" version 2>/dev/null | head -1)"
	got="$(health_version "http://127.0.0.1:${PORT}/health")"
	if [ -z "$got" ]; then
		die "VERIFY FAILED: http://127.0.0.1:${PORT}/health returned no version (the service answered /healthz a moment ago). journalctl -u $SERVICE_NAME -n 40"
	fi
	case "$expect_id" in
		*"$got"*) vline "version   /health on 127.0.0.1:${PORT} reports \"$got\" — matches the installed binary (\"$expect_id\")" ;;
		*) die "VERIFY FAILED: the service on 127.0.0.1:${PORT} reports version \"$got\" but the installed binary identifies as \"$expect_id\".
  Another process is answering on that port, or the restart did not pick the new binary.
    ss -ltnp | grep ':${PORT} '        # who holds the port
    systemctl status $SERVICE_NAME     # MainPID + the binary it runs" ;;
	esac
	# …and through Caddy, when the public door answered: a site file that proxies
	# to a NEIGHBOUR's port serves the neighbour's app under this domain.
	if [ "${PUBLIC_OK:-no}" = "yes" ]; then
		local pub
		# shellcheck disable=SC2086
		pub="$(health_version $CURL_TLS "https://${DOMAIN}/health")"
		if [ -n "$pub" ] && [ "$pub" != "$got" ]; then
			die "VERIFY FAILED: https://${DOMAIN}/health reports version \"$pub\" but the engine on 127.0.0.1:${PORT} reports \"$got\" — Caddy is proxying $DOMAIN to ANOTHER upstream (another app on this box?).
  Check ${CADDY_SITE_FILE#"$PREFIX"} (reverse_proxy must be 127.0.0.1:${PORT}) and every other site in ${CADDY_SITES_DIR#"$PREFIX"} / ${CADDYFILE#"$PREFIX"} for a duplicate of $DOMAIN."
		fi
		[ -n "$pub" ] && vline "public    https://${DOMAIN}/health reports \"$pub\" — same engine"
	fi
	# 3. The schema on disk is the one asked for, and this app's.
	if [ -n "$SCHEMA" ]; then
		local sw sh; sw="$(sha256sum "$SCHEMA" | cut -d' ' -f1)"; sh="$(sha256sum "$SCHEMA_FILE" | cut -d' ' -f1)"
		[ "$sw" = "$sh" ] || die "VERIFY FAILED: ${SCHEMA_FILE#"$PREFIX"} is not the --schema you gave ($SCHEMA)."
		vline "schema    ${SCHEMA_FILE#"$PREFIX"} == $SCHEMA (name \"$(schema_name "$SCHEMA_FILE")\")"
	else
		foreign_schema_guard "$SCHEMA_FILE"
		local nm; nm="$(schema_name "$SCHEMA_FILE")"
		if [ -n "$nm" ] && [ "$nm" != "$APP_NAME" ]; then
			vline "schema    ${SCHEMA_FILE#"$PREFIX"} kept — name \"$nm\" (app \"$APP_NAME\"; no other app on this box carries that schema)"
		else
			vline "schema    ${SCHEMA_FILE#"$PREFIX"} kept — name \"${nm:-?}\""
		fi
	fi
	# 4. The companions: executable, and the ops CLI actually operates.
	local s present=""
	for s in deploy-update.sh backup.sh restore.sh fleet-audit.sh; do [ -x "$OPT_DIR/scripts/$s" ] && present="$present $s"; done
	[ -n "$present" ] && vline "scripts   ${OPT_DIR#"$PREFIX"}/scripts:$present (executable)"
	if [ "$BACKUP_TIMER" = "yes" ]; then
		if systemctl is-active --quiet "${SERVICE_NAME}-backup.timer" 2>/dev/null; then vline "backup    ${SERVICE_NAME}-backup.timer active (OnCalendar=${BACKUP_SCHEDULE} → ${BACKUP_DIR#"$PREFIX"})"
		else warn "${SERVICE_NAME}-backup.timer is NOT active — this app has no scheduled backup (systemctl status ${SERVICE_NAME}-backup.timer)"; fi
	fi
	if [ -e "$OPT_DIR/bin/appximo-cli" ]; then
		if "$OPT_DIR/bin/appximo-cli" tenant --help >/dev/null 2>&1; then vline "ops CLI   ${OPT_DIR#"$PREFIX"}/bin/appximo-cli operates (tenant/migrate/token/admin)"
		else warn "${OPT_DIR#"$PREFIX"}/bin/appximo-cli does not answer 'tenant --help' — it is not the engine CLI; re-run with --cli=/path/to/appximo"; fi
	fi
	ok "verified — installed == asked:${VERIFIED_LINES}"
}

# ── Optional hardening ───────────────────────────────────────────────────────
maybe_harden() {
	[ "$HARDEN" = "yes" ] || return 0
	info "hardening: ufw + fail2ban + unattended-upgrades"
	run apt-get install -y -qq -o DPkg::Lock::Timeout=300 ufw fail2ban unattended-upgrades
	if [ "$DRY_RUN" != "yes" ]; then
		command -v ufw >/dev/null 2>&1 || die "hardening needs ufw and apt could not provide it on this box.
  Fix:  install it yourself (apt-get install -y ufw) and re-run, or drop --harden and firewall this box your own way.
  Nothing was firewalled; the app itself is installed and running."
		# CRITICAL: allow the CURRENT SSH port(s) BEFORE enabling ufw, or we lock
		# ourselves out. Detect them from the live listeners (covers a non-22
		# sshd), else from sshd_config. EVERY probe is optional and
		# failure-tolerant: under `set -euo pipefail` a missing `ss` binary
		# (exit 127) or an absent /etc/ssh/sshd_config (awk exit 2) used to
		# abort the WHOLE install with a bare exit code and no message — a
		# third-party evaluator had to diagnose both by hand on a minimal
		# image (LAUNCHPAD-S1). What is missing is now named, and only a
		# genuinely dangerous unknown stops the run.
		local sshports=""
		if command -v ss >/dev/null 2>&1; then
			sshports="$(ss -ltnH 'sport = :ssh' 2>/dev/null | awk '{print $4}' | sed -E 's/.*:([0-9]+)$/\1/' | sort -u || true)"
		else
			info "hardening: 'ss' is not installed (iproute2), so live SSH listeners can't be read — falling back to /etc/ssh/sshd_config"
		fi
		if [ -z "$sshports" ] && [ -f /etc/ssh/sshd_config ]; then
			sshports="$(awk '/^[[:space:]]*Port[[:space:]]+[0-9]+/{print $2}' /etc/ssh/sshd_config 2>/dev/null | sort -u || true)"
		fi
		if [ -z "$sshports" ]; then
			# Nothing detected. Harmless when this box has no sshd at all
			# (containers, consoles-only VMs); a LOCKOUT when sshd is running on
			# a port we failed to read — so that case stops, named.
			if pgrep -x sshd >/dev/null 2>&1 || systemctl is-active --quiet ssh 2>/dev/null || systemctl is-active --quiet sshd 2>/dev/null; then
				local missing=""
				command -v ss >/dev/null 2>&1 || missing="the 'ss' command (apt-get install -y iproute2)"
				if [ ! -f /etc/ssh/sshd_config ]; then
					[ -n "$missing" ] && missing="$missing, and "
					missing="${missing}/etc/ssh/sshd_config"
				fi
				[ -z "$missing" ] && missing="neither 'ss' nor sshd_config reported a Port line"
				die "hardening would enable ufw but the SSH port could not be determined, and sshd IS running on this box — enabling the firewall now could lock you out.
  Missing: ${missing}
  Fix:  install what is named above and re-run (the installer is idempotent), or run WITHOUT --harden and apply ufw yourself:
          ufw allow <your-ssh-port>/tcp && ufw allow 80/tcp && ufw allow 443/tcp && ufw --force enable
  Nothing was firewalled; the app itself is installed and running."
			fi
			info "hardening: no sshd found on this box — opening the conventional 22/tcp anyway (harmless) plus 80 and 443"
			sshports="22"
		fi
		local p; for p in $sshports; do ufw allow "${p}/tcp" >/dev/null; done
		ufw allow 80/tcp >/dev/null; ufw allow 443/tcp >/dev/null
		ufw --force enable >/dev/null
		systemctl enable --now fail2ban unattended-upgrades >/dev/null 2>&1 || true
		ok "hardening applied (SSH port(s) [$(echo "$sshports" | tr '\n' ' ')], HTTP 80, HTTPS 443 open; everything else denied)"
	else
		printf '  [dry-run] ufw allow <ssh>/80/443 + enable; fail2ban + unattended-upgrades\n'
	fi
}

# ── Uninstall ────────────────────────────────────────────────────────────────
uninstall() {
	info "uninstalling the app \"$APP_NAME\"…"
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] stop+disable %s; rm %s %s %s; rm Caddyfile; %s\n' \
			"$SERVICE_NAME" "$UNIT_FILE" "$OPT_DIR" "$ETC_DIR" \
			"$([ "$PURGE" = yes ] && echo 'DROP DATABASE + rm data dir' || echo '(DB kept — pass --purge to drop it)')"
		return
	fi
	[ "$(id -u)" = "0" ] || die "must run as root (sudo)"
	systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
	systemctl disable --now "${SERVICE_NAME}-backup.timer" >/dev/null 2>&1 || true
	rm -f "$UNIT_FILE" "$BACKUP_SERVICE_FILE" "$BACKUP_TIMER_FILE"; systemctl daemon-reload >/dev/null 2>&1 || true
	rm -rf "$OPT_DIR" "$ETC_DIR"
	# OPS-10: removing an app removes ITS site file and nothing else — a sibling
	# app's site (and the shared Caddyfile) survive untouched.
	if [ -f "$CADDY_SITE_FILE" ]; then
		rm -f "$CADDY_SITE_FILE"; systemctl reload caddy 2>/dev/null || true
		ok "removed this app's Caddy site ($CADDY_SITE_FILE)"
	else
		# Pre-OPS-10 layout: this app's block lived inline in the Caddyfile.
		local bak; bak="$(ls -1t "${CADDYFILE}".pre-appximo.* 2>/dev/null | head -1 || true)"
		if [ -n "$bak" ]; then
			mv -f "$bak" "$CADDYFILE"; systemctl reload caddy 2>/dev/null || true
			ok "restored the pre-appximo Caddyfile"
		elif [ -f "$CADDYFILE" ] && grep -q "127.0.0.1:${PORT}" "$CADDYFILE" 2>/dev/null; then
			rm -f "$CADDYFILE"; systemctl reload caddy 2>/dev/null || systemctl stop caddy 2>/dev/null || true
		fi
	fi
	ok "service, unit, binary and config removed"
	if [ "$PURGE" = "yes" ]; then
		warn "purging the database + data dir (destructive)"
		(cd /tmp && runuser -u postgres -- psql -tAX -c "DROP DATABASE IF EXISTS ${DB_NAME}") >/dev/null 2>&1 || true
		(cd /tmp && runuser -u postgres -- psql -tAX -c "DROP ROLE IF EXISTS ${DB_ROLE}") >/dev/null 2>&1 || true
		rm -rf "$VARLIB"
		ok "database, role and data dir dropped"
	else
		info "database + data dir kept (they hold your data). To also drop them: --uninstall --purge"
	fi
	[ -d "$BACKUP_DIR" ] && info "backups in $BACKUP_DIR were KEPT (even with --purge — they are the last copy; rm -rf it yourself if you mean it)"
	info "postgresql and caddy packages were left installed (they may be shared). Remove with apt if unused."
	ok "the app \"$APP_NAME\" was uninstalled (other apps on this box are untouched)."
}

# detect_control_port: read the CONTROL-PLANE port from the LIVE service's
# listening sockets instead of assuming the engine's 9090 — a consumer binary
# picks its own (ADR-023: detected, never assumed; commerce uses 9099 and the
# old summary printed a dead endpoint). The control port is "whatever the
# service listens on that is not the data port". Falls back to 9090 with a note.
detect_control_port() {
	# Keep the CONFIGURED port as the fallback (OPS-10: a named app is not on 9090).
	local configured="$CONTROL_PORT" detected=""
	[ "$DRY_RUN" = "yes" ] && return
	local pid; pid="$(systemctl show -p MainPID --value "$SERVICE_NAME" 2>/dev/null)"
	if [ -n "$pid" ] && [ "$pid" != "0" ]; then
		detected="$(ss -ltnpH 2>/dev/null | grep "pid=$pid," | grep -oE ':[0-9]+ ' | tr -d ': ' | grep -v "^$PORT$" | sort -u | head -1)"
	fi
	CONTROL_PORT="${detected:-${configured:-9090}}"
}

# ── Summary ──────────────────────────────────────────────────────────────────
summary() {
	detect_control_port
	echo
	ok "Appximo is installed."
	echo
	printf '  %sAPI%s        https://%s\n' "$C_B" "$C_N" "$DOMAIN"
	printf '  %sdocs%s       https://%s/docs   %seditor%s https://%s/editor   %sadmin%s https://%s/admin\n' \
		"$C_B" "$C_N" "$DOMAIN" "$C_B" "$C_N" "$DOMAIN" "$C_B" "$C_N" "$DOMAIN"
	echo
	# Secrets live in the 0600 env file, and ONLY there by default: an install
	# driven by an agent or logged by CI would otherwise burn them into a
	# transcript forever (it happened — PROD-JOURNEY-1B rotated them on the spot).
	if [ "$SHOW_SECRETS" = "yes" ]; then
		printf '  %sAdmin key%s  %s\n' "$C_B" "$C_N" "$ADMIN_KEY"
		printf '  %sJWT secret%s %s\n' "$C_B" "$C_N" "$JWT_SECRET"
		printf '             (both saved in %s — 0600)\n' "${ENV_FILE#"$PREFIX"}"
	else
		printf '  %sSecrets%s    generated and saved in %s (0600 — values not printed; --show-secrets to print)\n' \
			"$C_B" "$C_N" "${ENV_FILE#"$PREFIX"}"
	fi
	echo
	printf '  Register your first tenant (control plane on 127.0.0.1:%s — detected from the live service):\n' "$CONTROL_PORT"
	printf '    set -a; . %s; set +a\n' "${ENV_FILE#"$PREFIX"}"
	printf '    curl -X POST http://127.0.0.1:%s/tenants -H "X-Admin-Key: $ADMIN_KEY" \\\n' "$CONTROL_PORT"
	printf '      -H "Content-Type: application/json" \\\n'
	printf '      -d "{\\"tenant_id\\":\\"acme\\",\\"display_name\\":\\"Acme\\",\\"email\\":\\"a@acme.com\\",\\"plan\\":\\"free\\",\\"schema\\":$(cat %s)}"\n' "${SCHEMA_FILE#"$PREFIX"}"
	echo
	printf '  Logs     journalctl -u %s -f\n' "$SERVICE_NAME"
	if [ -f "$BACKUP_TIMER_FILE" ]; then
		printf '  Backup   %s-backup.timer (OnCalendar=%s) → %s — 14 sets kept; NOT off-box until you set BACKUP_COPY_TO in %s\n' "$SERVICE_NAME" "$BACKUP_SCHEDULE" "${BACKUP_DIR#"$PREFIX"}" "${ENV_FILE#"$PREFIX"}"
		printf '  Restore  sudo bash %s/scripts/restore.sh --app=%s --set=%s/%s-<stamp>   (timed + verified; drill it once: docs/PRODUCTION.md §4)\n' "${OPT_DIR#"$PREFIX"}" "$APP_NAME" "${BACKUP_DIR#"$PREFIX"}" "$APP_NAME"
	fi
	printf '  Ops CLI  %s/bin/appximo-cli  (tenant / migrate / token / admin create)\n' "${OPT_DIR#"$PREFIX"}"
	printf '  Schema   %s (name "%s") — a re-run KEEPS it unless you pass --schema=PATH\n' "${SCHEMA_FILE#"$PREFIX"}" "$(schema_name "$SCHEMA_FILE")"
	printf '  Update   build a new binary → scp up → sudo bash %s --binary=/path --domain=%s --email=%s --yes   (add --schema=PATH to change the model; secrets + data are kept, binary is replaced, the end is verified)\n' "$0" "$DOMAIN" "${EMAIL:-you@example.com}"
	[ -n "$SWAP_WARNING" ] && printf '  %s!%s Memory   %s — add swap before loading data (see the warning above / docs/PRODUCTION.md §Prerequisites)\n' "$C_Y" "$C_N" "$SWAP_WARNING"
	printf '  Remove   sudo bash %s --uninstall   (add --purge to also drop the database)\n' "$0"
	printf '  Guide    docs/PRODUCTION.md\n'
	printf '  AI agent building on this app?  appximo-cli spec | backend-spec | frontend-spec | backoffice-spec | quickstart\n'
	printf '           (or "specs" for all five) — paste into the agent; live contract at https://%s/openapi.json\n' "$DOMAIN"
	echo
}

# on_install_exit: on a mid-way abort, tell the operator it is safe to re-run
# (idempotent) or how to start clean. Cleared just before summary on success.
on_install_exit() {
	local rc=$?
	[ "$rc" -ne 0 ] && printf '%s✗ install failed (exit %d).%s Fix the error above, then re-run the same command — the installer is idempotent and resumes. To start over clean: sudo bash %s --uninstall\n' "$C_R" "$rc" "$C_N" "$0" >&2
	return 0
}

# ── main ─────────────────────────────────────────────────────────────────────
main() {
	ORIG_ARGS="$*"
	parse_args "$@"
	printf '%s== Appximo installer ==%s\n\n' "$C_B" "$C_N"
	detect_os
	if [ "$UNINSTALL" = "yes" ]; then uninstall; exit 0; fi
	# A clear message + recovery guidance if any step aborts mid-way.
	trap on_install_exit EXIT
	preflight
	gather_input
	guard_existing_app   # OPS-10: never clobber a DIFFERENT app that is already live
	preflight_conflicts
	# A kept schema is judged BEFORE any mutation (not when it is written, by
	# which point the binary has already been replaced): a foreign schema stops
	# the run with the box exactly as it was.
	if [ -z "$SCHEMA" ] && [ "$DRY_RUN" != "yes" ]; then foreign_schema_guard "$SCHEMA_FILE"; fi
	load_or_generate_secrets
	install_packages
	install_caddy
	setup_user_dirs
	setup_postgres
	enable_checksums_if_fresh
	ensure_postgres_restart
	install_binary
	install_companion_scripts
	write_schema
	write_env_file
	write_systemd_unit
	write_backup_timer
	write_caddyfile
	verify_service_can_read
	start_services
	verify
	verify_installed
	maybe_harden
	trap - EXIT
	summary
}

main "$@"
