#!/usr/bin/env bash
#
# Appitools — official production installer.
#
#   curl -fsSL https://get.appitools.dev/install.sh | bash -s -- --domain api.example.com --email you@example.com
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
# only swaps the binary + restarts).
#
# Flags:
#   --domain=DOMAIN      public domain pointing at this box (A/AAAA record)   [required]
#   --email=EMAIL        Let's Encrypt account email                          [required]
#   --binary=PATH        install THIS local appitools binary (see note below) [required until releases]
#   --schema=PATH        boot schema JSON (default: a todo-api starter you replace later)
#   --port=PORT          internal engine port Caddy proxies to               [default 8090]
#   --yes                non-interactive (don't prompt to confirm)
#   --harden             also apply ufw (22/80/443) + fail2ban + unattended-upgrades
#   --dry-run            generate every config file + print the plan, but run NO
#                        apt/systemctl/postgres/health steps (preview & testing)
#   --root=DIR           prefix all system paths with DIR (for --dry-run testing)
#   --help
#
# Binary source: there are no public GitHub Releases yet, so pass --binary=/path
# to the appitools binary you built/copied (scripts/build-engine.sh, or scp it
# up). The URL-download + checksum path below is written and ready — it activates
# automatically once RELEASE_VERSION is set to a published tag.
set -euo pipefail

# ── Constants ────────────────────────────────────────────────────────────────
readonly SERVICE_USER="appitools"
readonly REPO="miguel09acosta/appitools"
# Set to a published tag (e.g. "v0.1.0") to enable the download path. Empty means
# "no public release" → the installer requires --binary.
readonly RELEASE_VERSION=""

# ── Defaults (overridable by flags) ──────────────────────────────────────────
DOMAIN=""; EMAIL=""; BINARY=""; SCHEMA=""; PORT="8090"
ASSUME_YES="no"; HARDEN="no"; DRY_RUN="no"; PREFIX=""

# ── Output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ]; then C_G=$'\033[0;32m'; C_Y=$'\033[1;33m'; C_R=$'\033[0;31m'; C_B=$'\033[1;34m'; C_N=$'\033[0m'
else C_G=""; C_Y=""; C_R=""; C_B=""; C_N=""; fi
info() { printf '%s→%s %s\n' "$C_B" "$C_N" "$*"; }
ok()   { printf '%s✓%s %s\n' "$C_G" "$C_N" "$*"; }
warn() { printf '%s!%s %s\n' "$C_Y" "$C_N" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$C_R" "$C_N" "$*" >&2; exit 1; }

# run: execute a command, or just print it in --dry-run.
run() { if [ "$DRY_RUN" = "yes" ]; then printf '  [dry-run] %s\n' "$*"; else "$@"; fi; }

# ── Arg parsing ──────────────────────────────────────────────────────────────
usage() { sed -n '3,44p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

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
			--help|-h)  usage 0 ;;
			*) die "unknown flag: $arg (see --help)" ;;
		esac
	done
	# Derived paths (PREFIX lets --dry-run stage them under a test root).
	ETC_DIR="$PREFIX/etc/appitools"
	ENV_FILE="$ETC_DIR/appitools.env"
	SCHEMA_FILE="$ETC_DIR/schema.json"
	OPT_DIR="$PREFIX/opt/appitools"
	BIN_PATH="$OPT_DIR/bin/appitools"
	VARLIB="$PREFIX/var/lib/appitools"
	CADDYFILE="$PREFIX/etc/caddy/Caddyfile"
	UNIT_FILE="$PREFIX/etc/systemd/system/appitools.service"
}

# ── OS / arch detection ──────────────────────────────────────────────────────
detect_os() {
	[ "$(uname -s)" = "Linux" ] || die "this installer supports Linux only (found $(uname -s))"
	[ -r /etc/os-release ] || die "cannot read /etc/os-release — unsupported distro"
	# shellcheck disable=SC1091
	. /etc/os-release
	case "${ID:-}:${ID_LIKE:-}" in
		ubuntu:*|debian:*|*:*debian*) : ;;
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
		die "must run as root (sudo). Re-run: sudo bash $0 $*"
	fi
	# Detect RAM → a soft memory ceiling on small boxes. On the single-box stack
	# Postgres shares the RAM, so we size GOMEMLIMIT to ~half on a 1 GB box rather
	# than a fraction of total (which would starve Postgres). Bigger boxes are left
	# to the engine's own cgroup-aware auto-detect.
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

# ── Interactive input (the ONE required decision) ────────────────────────────
gather_input() {
	if [ -z "$DOMAIN" ]; then
		[ "$ASSUME_YES" = "yes" ] && die "--domain is required (non-interactive)"
		printf 'Domain for this API (must already point at this server) : '
		read -r DOMAIN
	fi
	[ -n "$DOMAIN" ] || die "a domain is required"
	# Reject an obviously-wrong domain early (a scheme/path/space is a common paste error).
	case "$DOMAIN" in
		*://*|*/*|*" "*) die "domain looks wrong: '$DOMAIN' (want a bare hostname like api.example.com)" ;;
	esac
	if [ -z "$EMAIL" ] && [ "$DRY_RUN" != "yes" ]; then
		[ "$ASSUME_YES" = "yes" ] && die "--email is required for Let's Encrypt (non-interactive)"
		printf "Email for Let's Encrypt (cert expiry notices)          : "
		read -r EMAIL
	fi
	[ -n "$EMAIL" ] || [ "$DRY_RUN" = "yes" ] || die "an email is required for Let's Encrypt"
	if [ -z "$BINARY" ] && [ -z "$RELEASE_VERSION" ]; then
		die "no public release yet — pass --binary=/path/to/appitools (build it with scripts/build-engine.sh and scp it up)"
	fi
	[ -z "$BINARY" ] || [ -f "$BINARY" ] || die "--binary '$BINARY' not found"

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
		printf 'Proceed? [y/N] '; read -r reply
		case "$reply" in y|Y|yes) : ;; *) die "aborted" ;; esac
	fi
}

# ── Secrets: reuse on re-run, generate on first install ──────────────────────
rand_hex() { openssl rand -hex "${1:-32}" 2>/dev/null || head -c "${1:-32}" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

load_or_generate_secrets() {
	if [ -f "$ENV_FILE" ]; then
		info "existing $ENV_FILE — reusing its secrets (idempotent re-run)"
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
	run apt-get install -y -qq ca-certificates curl gnupg openssl postgresql
	# Caddy from its official apt repository (per the Caddy docs).
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] add caddy apt repo + apt-get install caddy\n'
	elif ! command -v caddy >/dev/null 2>&1; then
		curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/gpg.key \
			| gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
		curl -fsSL https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt \
			| tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
		apt-get update -qq
		apt-get install -y -qq caddy
	fi
	ok "packages ready"
}

# ── Service user + directories ───────────────────────────────────────────────
setup_user_dirs() {
	if [ "$DRY_RUN" != "yes" ] && ! id "$SERVICE_USER" >/dev/null 2>&1; then
		useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
	fi
	run mkdir -p "$ETC_DIR" "$OPT_DIR/bin" "$VARLIB/files" "$VARLIB/obs"
	run chown -R "$SERVICE_USER:$SERVICE_USER" "$VARLIB"
	ok "service user + directories ready"
}

# ── PostgreSQL: role + database (idempotent) ─────────────────────────────────
setup_postgres() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] create role %s + database appitools (if absent)\n' "$SERVICE_USER"
		return
	fi
	systemctl enable --now postgresql >/dev/null 2>&1 || true
	local psql="sudo -u postgres psql -tAX"
	if [ "$($psql -c "SELECT 1 FROM pg_roles WHERE rolname='${SERVICE_USER}'")" != "1" ]; then
		$psql -c "CREATE ROLE ${SERVICE_USER} LOGIN PASSWORD '${DB_PASS}'"
	else
		# Re-run with a reused password is a no-op; if the env was lost, realign it.
		$psql -c "ALTER ROLE ${SERVICE_USER} WITH PASSWORD '${DB_PASS}'"
	fi
	if [ "$($psql -c "SELECT 1 FROM pg_database WHERE datname='appitools'")" != "1" ]; then
		$psql -c "CREATE DATABASE appitools OWNER ${SERVICE_USER}"
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
	if [ "$DRY_RUN" = "yes" ]; then
		mkdir -p "$(dirname "$path")"
		printf '%s\n' "$1" > "$path"
		printf '  [dry-run] wrote %s\n' "$path"
	else
		mkdir -p "$(dirname "$path")"
		printf '%s\n' "$1" > "$path"
	fi
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
	local gomem=""
	# GOMEMLIMIT rides in the EnvironmentFile; nothing extra needed in the unit.
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
WantedBy=multi-user.target${gomem}"
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
		printf '  [dry-run] systemctl daemon-reload; enable --now appitools; reload caddy\n'
		return
	fi
	systemctl daemon-reload
	systemctl enable --now appitools
	systemctl restart appitools   # pick up a re-run's new binary/config
	# Caddy is a system service after apt install; hand it the new Caddyfile.
	systemctl reload caddy 2>/dev/null || systemctl restart caddy
	ok "services started (appitools + caddy)"
}

# ── Health verification ──────────────────────────────────────────────────────
verify() {
	if [ "$DRY_RUN" = "yes" ]; then
		printf '  [dry-run] would poll https://%s/healthz until 200\n' "$DOMAIN"
		return
	fi
	info "waiting for the engine to answer locally…"
	local i
	for i in $(seq 1 30); do
		if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
			ok "engine healthy on 127.0.0.1:${PORT}"; break
		fi
		[ "$i" = "30" ] && { journalctl -u appitools -n 40 --no-pager || true; die "engine did not become healthy — see the log above (journalctl -u appitools -f)"; }
		sleep 1
	done
	info "waiting for public HTTPS (Caddy is issuing the Let's Encrypt certificate — first issue can take ~30s)…"
	for i in $(seq 1 40); do
		if curl -fsS "https://${DOMAIN}/healthz" >/dev/null 2>&1; then
			ok "public HTTPS live: https://${DOMAIN}/healthz"; return
		fi
		sleep 3
	done
	warn "https://${DOMAIN}/healthz not answering yet. Common causes: DNS for ${DOMAIN} not pointing here, or ports 80/443 blocked. Check: journalctl -u caddy -f"
}

# ── Optional hardening ───────────────────────────────────────────────────────
maybe_harden() {
	[ "$HARDEN" = "yes" ] || return 0
	info "hardening: ufw (22/80/443) + fail2ban + unattended-upgrades"
	run apt-get install -y -qq ufw fail2ban unattended-upgrades
	if [ "$DRY_RUN" != "yes" ]; then
		ufw allow 22/tcp >/dev/null; ufw allow 80/tcp >/dev/null; ufw allow 443/tcp >/dev/null
		ufw --force enable >/dev/null
		systemctl enable --now fail2ban unattended-upgrades >/dev/null 2>&1 || true
	fi
	ok "hardening applied (SSH 22, HTTP 80, HTTPS 443 open; everything else denied)"
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
	printf '  Logs     journalctl -u appitools -f\n'
	printf '  Update   build a new binary → scp up → sudo bash %s --binary=/path --domain=%s --email=%s --yes\n' "$0" "$DOMAIN" "${EMAIL:-you@example.com}"
	printf '  Guide    docs/PRODUCTION.md\n'
	echo
}

# ── main ─────────────────────────────────────────────────────────────────────
main() {
	parse_args "$@"
	printf '%s== Appitools installer ==%s\n\n' "$C_B" "$C_N"
	detect_os
	preflight "$@"
	gather_input
	load_or_generate_secrets
	install_packages
	setup_user_dirs
	setup_postgres
	install_binary
	write_schema
	write_env_file
	write_systemd_unit
	write_caddyfile
	start_services
	verify
	maybe_harden
	summary
}

main "$@"
