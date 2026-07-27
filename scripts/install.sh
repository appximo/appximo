#!/usr/bin/env bash
#
# Appitools — official production installer.
#
#   curl -fsSL https://get.appitools.dev/install.sh | sudo bash -s -- --domain api.example.com --email you@example.com
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
# half-written script. Idempotent: safe to re-run (it reuses existing secrets and
# only swaps the binary + restarts). `--uninstall` reverses it for a clean retry.
#
# Binary source: there are no public GitHub Releases yet, so pass --binary=/path
# to the appitools binary you built/copied (scripts/build-engine.sh, or scp it
# up). The URL-download + checksum path below is written and ready — it activates
# automatically once RELEASE_VERSION is set to a published tag.
set -euo pipefail

# ── Constants ────────────────────────────────────────────────────────────────
readonly SERVICE_USER="appitools"
readonly SERVICE_NAME="appitools"
readonly REPO="miguel09acosta/appitools"
# Set to a published tag (e.g. "v0.1.0") to enable the download path. Empty means
# "no public release" → the installer requires --binary.
readonly RELEASE_VERSION=""

# ── Defaults (overridable by flags) ──────────────────────────────────────────
DOMAIN=""; EMAIL=""; BINARY=""; SCHEMA=""; PORT="8090"
ASSUME_YES="no"; HARDEN="no"; DRY_RUN="no"; PREFIX=""; UNINSTALL="no"; PURGE="no"

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
Appitools installer — empty Ubuntu/Debian VPS → live HTTPS API.

Usage: sudo bash install.sh --domain DOMAIN --email EMAIL --binary PATH [options]

Required (until public releases exist):
  --domain=DOMAIN      public domain pointing at this box (A/AAAA record)
  --email=EMAIL        Let's Encrypt account email
  --binary=PATH        the appitools binary to install (build with scripts/build-engine.sh)

Options:
  --schema=PATH        boot schema JSON (default: a todo-api starter you replace later)
  --port=PORT          internal engine port Caddy proxies to        [default 8090]
  --yes                non-interactive (don't prompt to confirm)
  --harden             also apply ufw (SSH+80+443) + fail2ban + unattended-upgrades
  --dry-run            generate every config file + print the plan, run NO system steps
  --root=DIR           prefix all paths with DIR (for --dry-run testing only)
  --uninstall          stop + remove the appitools service, unit, files, and Caddyfile
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
			--schema=*) SCHEMA="${arg#*=}" ;;
			--port=*)   PORT="${arg#*=}" ;;
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
	# Derived paths (PREFIX lets --dry-run stage them under a test root).
	ETC_DIR="$PREFIX/etc/appitools"
	ENV_FILE="$ETC_DIR/appitools.env"
	SCHEMA_FILE="$ETC_DIR/schema.json"
	OPT_DIR="$PREFIX/opt/appitools"
	BIN_PATH="$OPT_DIR/bin/appitools"
	VARLIB="$PREFIX/var/lib/appitools"
	CADDYFILE="$PREFIX/etc/caddy/Caddyfile"
	UNIT_FILE="$PREFIX/etc/systemd/system/${SERVICE_NAME}.service"
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
	# Detect RAM → a soft memory ceiling on small boxes. On the single-box stack
	# Postgres shares the RAM, so we size GOMEMLIMIT to a conservative fraction
	# rather than 90 %-of-total (which would starve Postgres). Bigger boxes are
	# left to the engine's own cgroup-aware auto-detect.
	local mem_kb; mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
	MEM_MB=$(( mem_kb / 1024 ))
	GOMEMLIMIT_VAL=""
	if [ "$MEM_MB" -gt 0 ] && [ "$MEM_MB" -le 1280 ]; then
		GOMEMLIMIT_VAL="512MiB"
		warn "small box (~${MEM_MB} MiB RAM) — setting GOMEMLIMIT=${GOMEMLIMIT_VAL} to protect against OOM"
	elif [ "$MEM_MB" -gt 1280 ] && [ "$MEM_MB" -le 2560 ]; then
		GOMEMLIMIT_VAL="1536MiB"
		info "~${MEM_MB} MiB RAM — setting GOMEMLIMIT=${GOMEMLIMIT_VAL}"
	fi
}

# preflight_conflicts: DETECT before we mutate. A port held by a stranger, an
# existing non-ours Caddyfile, or a prior install are reported (and the risky
# ones abort with a clear fix) rather than blindly clobbered.
preflight_conflicts() {
	[ "$DRY_RUN" = "yes" ] && return 0
	# Existing appitools install → this is an upgrade; say so (secrets are reused).
	if [ -f "$ENV_FILE" ] || [ -f "$UNIT_FILE" ]; then
		info "existing Appitools install detected — upgrading in place (secrets reused)"
	fi
	# Port already in use by something that ISN'T our own service → refuse.
	if command -v ss >/dev/null 2>&1 && ss -ltn "( sport = :$PORT )" 2>/dev/null | grep -q ":$PORT "; then
		if ! systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
			die "port $PORT is already in use by another process (not the appitools service). Free it (ss -ltnp | grep :$PORT) or pass --port=<other>."
		fi
	fi
	# An existing Caddyfile that isn't ours would be overwritten — back it up first.
	if [ -f "$CADDYFILE" ] && ! grep -q "127.0.0.1:${PORT}" "$CADDYFILE" 2>/dev/null; then
		local bak; bak="${CADDYFILE}.pre-appitools.$(date +%Y%m%d-%H%M%S)"
		warn "an existing Caddyfile is present and is NOT ours — backing it up to ${bak} before writing appitools' config"
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
		die "no public release yet — pass --binary=/path/to/appitools (build it with scripts/build-engine.sh and scp it up)"
	fi
	if [ -n "$BINARY" ]; then
		[ -f "$BINARY" ] || die "--binary '$BINARY' not found"
		[ -x "$BINARY" ] || die "--binary '$BINARY' is not executable (chmod +x it)"
		# Confirm it's really an appitools binary of the right arch — its own version
		# subcommand must run AND name appitools (so /bin/true or a wrong ELF is
		# rejected, not just a non-exec). Runs on the target, so same arch as deploy.
		"$BINARY" version 2>/dev/null | grep -qi appitools \
			|| die "--binary '$BINARY' is not an appitools binary (or wrong architecture) — 'appitools version' did not identify it"
	fi

	echo
	info "About to install Appitools:"
	printf '    domain        %s\n' "$DOMAIN"
	printf '    tls email     %s\n' "${EMAIL:-<dry-run>}"
	printf '    engine        %s (internal port %s)\n' "$BIN_PATH" "$PORT"
	printf '    postgres      native, database "appitools" (local, generated password)\n'
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
	DATABASE_URL="postgres://${SERVICE_USER}:${DB_PASS}@localhost:5432/appitools?sslmode=disable"
}

# ── System packages ──────────────────────────────────────────────────────────
install_packages() {
	info "installing packages (postgresql, caddy, prerequisites)…"
	export DEBIAN_FRONTEND=noninteractive
	run apt-get update -qq
	run apt-get install -y -qq ca-certificates curl gnupg openssl postgresql \
		|| die "apt-get install failed. Check network/apt sources, then re-run this installer (it resumes safely)."
	# Caddy from its official apt repository (per the Caddy docs).
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] add caddy apt repo + apt-get install caddy\n'
	elif command -v caddy >/dev/null 2>&1; then
		info "caddy already installed — keeping it"
	else
		curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
			| gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg \
			|| die "could not fetch the Caddy signing key (network?). Re-run to retry."
		curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
			> /etc/apt/sources.list.d/caddy-stable.list \
			|| die "could not add the Caddy apt repo. Re-run to retry."
		apt-get update -qq
		apt-get install -y -qq caddy || die "apt-get install caddy failed. Re-run to retry."
	fi
	ok "packages ready"
}

# ── Service user + directories ───────────────────────────────────────────────
setup_user_dirs() {
	if [ "$DRY_RUN" != "yes" ] && ! id "$SERVICE_USER" >/dev/null 2>&1; then
		useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER" \
			|| die "could not create the '$SERVICE_USER' system user"
	fi
	run mkdir -p "$ETC_DIR" "$OPT_DIR/bin" "$OPT_DIR/scripts" "$VARLIB/files" "$VARLIB/obs"
	run chown -R "$SERVICE_USER:$SERVICE_USER" "$VARLIB"
	ok "service user + directories ready"
}

# install_companion_scripts: place deploy-update.sh + backup.sh in
# $OPT_DIR/scripts (the path docs/PRODUCTION.md references) when they sit next to
# this installer — i.e. `bash scripts/install.sh` from a checkout, or scp'd
# together. Under `curl | bash` there are no sibling files, so it skips with a
# hint instead of pretending. (Caught in PROD-PATH-HARDEN-S1: the docs pointed at
# /opt/appitools/scripts but nothing put the scripts there.)
install_companion_scripts() {
	local self_dir; self_dir="$(cd "$(dirname "$0")" 2>/dev/null && pwd)" || { self_dir=""; }
	local any="no" s
	for s in deploy-update.sh backup.sh; do
		if [ -n "$self_dir" ] && [ -f "$self_dir/$s" ]; then
			run install -m 0755 "$self_dir/$s" "$OPT_DIR/scripts/$s"; any="yes"
		fi
	done
	if [ "$any" = "yes" ]; then
		ok "companion scripts installed in ${OPT_DIR#"$PREFIX"}/scripts (deploy-update.sh, backup.sh)"
	else
		info "companion scripts (deploy-update.sh/backup.sh) weren't next to the installer — grab them from the repo into ${OPT_DIR#"$PREFIX"}/scripts when you need updates/backups"
	fi
}

# ── PostgreSQL: role + database (idempotent) ─────────────────────────────────
setup_postgres() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] create role %s + database appitools (if absent)\n' "$SERVICE_USER"
		return
	fi
	systemctl enable --now postgresql >/dev/null 2>&1 || die "postgresql did not start (systemctl status postgresql). Re-run after fixing."
	# Wait for the socket (fresh installs take a moment to accept connections).
	local i; for i in $(seq 1 15); do sudo -u postgres psql -tAc 'SELECT 1' >/dev/null 2>&1 && break; sleep 1; done
	local psql="sudo -u postgres psql -tAX"
	if [ "$($psql -c "SELECT 1 FROM pg_roles WHERE rolname='${SERVICE_USER}'")" != "1" ]; then
		$psql -c "CREATE ROLE ${SERVICE_USER} LOGIN PASSWORD '${DB_PASS}'" || die "could not create the postgres role"
	else
		# Re-run realigns the password to the (reused) env file, so they never drift.
		$psql -c "ALTER ROLE ${SERVICE_USER} WITH PASSWORD '${DB_PASS}'" || die "could not align the postgres role password"
	fi
	if [ "$($psql -c "SELECT 1 FROM pg_database WHERE datname='appitools'")" != "1" ]; then
		$psql -c "CREATE DATABASE appitools OWNER ${SERVICE_USER}" || die "could not create the appitools database"
	fi
	ok "postgresql role + database ready (control plane is bootstrapped by the engine on boot)"
}

# ── Engine binary ────────────────────────────────────────────────────────────
install_binary() {
	if [ -n "$BINARY" ]; then
		info "installing engine binary from $BINARY"
		run install -m 0755 "$BINARY" "$BIN_PATH"
	else
		# Ready for when releases exist (RELEASE_VERSION set): download + checksum.
		local base="https://github.com/${REPO}/releases/download/${RELEASE_VERSION}"
		local asset="appitools-${RELEASE_VERSION}-linux-${ARCH}"
		info "downloading engine ${RELEASE_VERSION} (${ARCH})…"
		run curl -fLo /tmp/appitools "${base}/${asset}"
		run curl -fLo /tmp/appitools.checksums "${base}/checksums.txt"
		if [ "$DRY_RUN" != "yes" ]; then
			( cd /tmp && grep " ${asset}\$" appitools.checksums | sed "s/${asset}/appitools/" | sha256sum -c - ) \
				|| die "checksum verification failed"
		fi
		run install -m 0755 /tmp/appitools "$BIN_PATH"
	fi
	ok "engine installed at $BIN_PATH"
}

# ── Boot schema ──────────────────────────────────────────────────────────────
write_schema() {
	if [ -f "$SCHEMA_FILE" ] && [ -z "$SCHEMA" ]; then
		info "keeping existing $SCHEMA_FILE"
		return
	fi
	if [ -n "$SCHEMA" ]; then
		[ -f "$SCHEMA" ] || die "--schema '$SCHEMA' not found"
		run install -m 0644 "$SCHEMA" "$SCHEMA_FILE"
	else
		# A minimal, valid starter so the engine boots immediately. Replace it via
		# the visual editor (/editor) or re-run with --schema, then restart.
		write_file "$SCHEMA_FILE" '{
  "$schema": "https://appitools.dev/schema/v1",
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
	ok "boot schema at $SCHEMA_FILE"
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
	local body="# Appitools engine environment (systemd EnvironmentFile — plain KEY=value, no export, no quotes).
# Generated by scripts/install.sh. Secrets are reused on re-run; keep this file 0600.
DATABASE_URL=${DATABASE_URL}
JWT_SECRET=${JWT_SECRET}
ADMIN_KEY=${ADMIN_KEY}
APPITOOLS_ENV=production
APPITOOLS_FILES_DIR=${VARLIB#"$PREFIX"}/files
OBS_DB_PATH=${VARLIB#"$PREFIX"}/obs/obs.db"
	[ -n "$GOMEMLIMIT_VAL" ] && body="${body}
GOMEMLIMIT=${GOMEMLIMIT_VAL}"
	write_file "$ENV_FILE" "$body"
	run chmod 600 "$ENV_FILE"
	run chown "root:$SERVICE_USER" "$ENV_FILE"
	ok "wrote $ENV_FILE (0600 root:$SERVICE_USER)"
}

write_systemd_unit() {
	write_file "$UNIT_FILE" "[Unit]
Description=Appitools engine
Documentation=https://github.com/${REPO}/blob/main/docs/PRODUCTION.md
Wants=network-online.target
After=network-online.target postgresql.service

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
EnvironmentFile=${ENV_FILE#"$PREFIX"}
ExecStart=${BIN_PATH#"$PREFIX"} serve --schema ${SCHEMA_FILE#"$PREFIX"} --port ${PORT}
Restart=always
RestartSec=5
LimitNOFILE=4096
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${VARLIB#"$PREFIX"}
StateDirectory=appitools

[Install]
WantedBy=multi-user.target"
	ok "wrote $UNIT_FILE"
}

write_caddyfile() {
	# Caddy terminates TLS (automatic Let's Encrypt) and reverse-proxies to the
	# engine, passing the Host header through unchanged (tenant routing depends on
	# it). request_body caps uploads at 25MB at the edge; the engine also enforces
	# its own APPITOOLS_FILES_MAX_BYTES. SSE (text/event-stream) is auto-flushed by
	# reverse_proxy, so /api/*/events works with no extra config.
	write_file "$CADDYFILE" "{
	email ${EMAIL}
}

${DOMAIN} {
	request_body {
		max_size 25MB
	}
	reverse_proxy 127.0.0.1:${PORT}
}"
	ok "wrote $CADDYFILE"
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
	# Caddy is a system service after apt install; hand it the new Caddyfile.
	systemctl reload caddy 2>/dev/null || systemctl restart caddy 2>/dev/null \
		|| warn "could not (re)start caddy — check: caddy validate --config $CADDYFILE ; journalctl -u caddy -f"
	ok "services started (appitools + caddy)"
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
	info "waiting for public HTTPS (Caddy is issuing the Let's Encrypt certificate — first issue can take ~30s)…"
	for i in $(seq 1 40); do
		if curl -fsS "https://${DOMAIN}/healthz" >/dev/null 2>&1; then
			ok "public HTTPS live: https://${DOMAIN}/healthz"; return
		fi
		sleep 3
	done
	warn "https://${DOMAIN}/healthz not answering yet. The engine is up locally; this is almost always DNS or the firewall. Check: dig +short ${DOMAIN} (should be this box's IP); ports 80+443 open; journalctl -u caddy -f"
}

# ── Optional hardening ───────────────────────────────────────────────────────
maybe_harden() {
	[ "$HARDEN" = "yes" ] || return 0
	info "hardening: ufw + fail2ban + unattended-upgrades"
	run apt-get install -y -qq ufw fail2ban unattended-upgrades
	if [ "$DRY_RUN" != "yes" ]; then
		# CRITICAL: allow the CURRENT SSH port(s) BEFORE enabling ufw, or we lock
		# ourselves out. Detect them from the live listeners (covers a non-22 sshd);
		# fall back to 22.
		local sshports; sshports="$(ss -ltnH 'sport = :ssh' 2>/dev/null | awk '{print $4}' | sed -E 's/.*:([0-9]+)$/\1/' | sort -u)"
		[ -z "$sshports" ] && sshports="$(awk '/^[[:space:]]*Port[[:space:]]+[0-9]+/{print $2}' /etc/ssh/sshd_config 2>/dev/null | sort -u)"
		[ -z "$sshports" ] && sshports="22"
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
	info "uninstalling Appitools…"
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] stop+disable %s; rm %s %s %s; rm Caddyfile; %s\n' \
			"$SERVICE_NAME" "$UNIT_FILE" "$OPT_DIR" "$ETC_DIR" \
			"$([ "$PURGE" = yes ] && echo 'DROP DATABASE + rm data dir' || echo '(DB kept — pass --purge to drop it)')"
		return
	fi
	[ "$(id -u)" = "0" ] || die "must run as root (sudo)"
	systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
	rm -f "$UNIT_FILE"; systemctl daemon-reload >/dev/null 2>&1 || true
	rm -rf "$OPT_DIR" "$ETC_DIR"
	# Restore the pre-appitools Caddyfile if we backed one up, else remove ours.
	local bak; bak="$(ls -1t "${CADDYFILE}".pre-appitools.* 2>/dev/null | head -1 || true)"
	if [ -n "$bak" ]; then
		mv -f "$bak" "$CADDYFILE"; systemctl reload caddy 2>/dev/null || true
		ok "restored the pre-appitools Caddyfile"
	elif [ -f "$CADDYFILE" ] && grep -q "127.0.0.1:${PORT}" "$CADDYFILE" 2>/dev/null; then
		rm -f "$CADDYFILE"; systemctl reload caddy 2>/dev/null || systemctl stop caddy 2>/dev/null || true
	fi
	ok "service, unit, binary and config removed"
	if [ "$PURGE" = "yes" ]; then
		warn "purging the database + data dir (destructive)"
		sudo -u postgres psql -tAX -c "DROP DATABASE IF EXISTS appitools" >/dev/null 2>&1 || true
		sudo -u postgres psql -tAX -c "DROP ROLE IF EXISTS ${SERVICE_USER}" >/dev/null 2>&1 || true
		rm -rf "$VARLIB"
		ok "database, role and data dir dropped"
	else
		info "database + data dir kept (they hold your data). To also drop them: --uninstall --purge"
	fi
	info "postgresql and caddy packages were left installed (they may be shared). Remove with apt if unused."
	ok "Appitools uninstalled."
}

# ── Summary ──────────────────────────────────────────────────────────────────
summary() {
	echo
	ok "Appitools is installed."
	echo
	printf '  %sAPI%s        https://%s\n' "$C_B" "$C_N" "$DOMAIN"
	printf '  %sdocs%s       https://%s/docs   %seditor%s https://%s/editor   %sadmin%s https://%s/admin\n' \
		"$C_B" "$C_N" "$DOMAIN" "$C_B" "$C_N" "$DOMAIN" "$C_B" "$C_N" "$DOMAIN"
	echo
	printf '  %sAdmin key%s  %s\n' "$C_B" "$C_N" "$ADMIN_KEY"
	printf '  %sJWT secret%s %s\n' "$C_B" "$C_N" "$JWT_SECRET"
	printf '             (both saved in %s — 0600)\n' "${ENV_FILE#"$PREFIX"}"
	echo
	printf '  Register your first tenant (control plane is localhost-only):\n'
	printf '    curl -X POST http://127.0.0.1:9090/tenants -H "X-Admin-Key: %s" \\\n' "$ADMIN_KEY"
	printf '      -H "Content-Type: application/json" \\\n'
	printf '      -d "{\\"tenant_id\\":\\"acme\\",\\"display_name\\":\\"Acme\\",\\"email\\":\\"a@acme.com\\",\\"plan\\":\\"free\\",\\"schema\\":$(cat %s)}"\n' "${SCHEMA_FILE#"$PREFIX"}"
	echo
	printf '  Logs     journalctl -u %s -f\n' "$SERVICE_NAME"
	printf '  Update   build a new binary → scp up → sudo bash %s --binary=/path --domain=%s --email=%s --yes\n' "$0" "$DOMAIN" "${EMAIL:-you@example.com}"
	printf '  Remove   sudo bash %s --uninstall   (add --purge to also drop the database)\n' "$0"
	printf '  Guide    docs/PRODUCTION.md\n'
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
	printf '%s== Appitools installer ==%s\n\n' "$C_B" "$C_N"
	detect_os
	if [ "$UNINSTALL" = "yes" ]; then uninstall; exit 0; fi
	# A clear message + recovery guidance if any step aborts mid-way.
	trap on_install_exit EXIT
	preflight
	gather_input
	preflight_conflicts
	load_or_generate_secrets
	install_packages
	setup_user_dirs
	setup_postgres
	install_binary
	install_companion_scripts
	write_schema
	write_env_file
	write_systemd_unit
	write_caddyfile
	start_services
	verify
	maybe_harden
	trap - EXIT
	summary
}

main "$@"
