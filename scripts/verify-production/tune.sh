#!/usr/bin/env bash
#
# tune.sh — apply (or revert) the measured production tuning for THIS box, so you
# can A/B it yourself instead of trusting a table in a README.
#
# RUNS ON THE SERVER.
#
#   bash tune.sh --show                # what is set now vs what would be applied
#   bash tune.sh --dry-run             # print every change, touch nothing
#   bash tune.sh --apply               # apply + restart PostgreSQL and the engine
#   bash tune.sh --revert              # restore the previous values
#
# The intended loop is: measure → apply → measure again → keep it only if the
# numbers moved. `load.sh` + `stats.py compare` give the verdict, and the gate is
# "statistically significant AND practically material" — see stats.py.
#
# What it changes, and why (all derived from the box's own RAM):
#
#   shared_buffers        25% of RAM. PostgreSQL's default (128 MB) is sized for a
#                         machine much smaller than a modern VPS; once the working
#                         set exceeds it, scans re-read from the OS cache.
#   effective_cache_size  ~55% of RAM. A PLANNER HINT ONLY — it allocates nothing.
#                         PostgreSQL 16+ ships 4 GB regardless of the machine, so
#                         on a 2 GB box the planner believes in cache that cannot
#                         exist and mis-prices index scans.
#   work_mem              per-sort/hash working memory, kept deliberately modest:
#                         it is per OPERATION, not per connection, so a generous
#                         value times many concurrent sorts is a classic OOM.
#   maintenance_work_mem  used by VACUUM / CREATE INDEX; larger is simply faster
#                         and it is only ever used by one maintenance job at a time.
#   max_connections       the engine uses a small pool (DB_MAX_CONNS, default 10);
#                         hundreds of allowed backends on a 2-core box only invite
#                         a thundering herd.
#   GOMEMLIMIT            a soft ceiling for the Go heap. On the single-box stack
#                         PostgreSQL shares the RAM, so this must leave room for
#                         it — see the note in the code below.
#
# Flags:
#   --show / --dry-run / --apply / --revert
#   --env-file=PATH   installer env file      [default /etc/appitools/appitools.env]
#   --service=NAME    engine unit             [default appitools]
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

MODE="show"
for arg in "$@"; do
	case "$arg" in
		--show)       MODE="show" ;;
		--dry-run)    MODE="dry-run" ;;
		--apply)      MODE="apply" ;;
		--revert)     MODE="revert" ;;
		--env-file=*) ENV_FILE="${arg#*=}" ;;
		--service=*)  SERVICE="${arg#*=}" ;;
		--help|-h)    sed -n '3,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done
need python3 psql

[ "$(id -u)" = "0" ] || die "run as root (it edits PostgreSQL's config and restarts services)"

MEM_MB=$(( $(awk '/^MemTotal:/{print $2}' /proc/meminfo) / 1024 ))
NPROC="$(nproc)"

# ── The recommended values, derived from this box ────────────────────────────
SHARED_BUFFERS_MB=$(( MEM_MB / 4 ))
EFFECTIVE_CACHE_MB=$(( MEM_MB * 55 / 100 ))
WORK_MEM_MB=4
[ "$MEM_MB" -ge 4096 ] && WORK_MEM_MB=8
MAINT_WORK_MEM_MB=$(( MEM_MB / 16 ))
[ "$MAINT_WORK_MEM_MB" -lt 64 ] && MAINT_WORK_MEM_MB=64
MAX_CONNECTIONS=50
[ "$MEM_MB" -ge 4096 ] && MAX_CONNECTIONS=100

# GOMEMLIMIT — the important one, and the one most easily got wrong.
#
# It is a SOFT ceiling: as the heap approaches it the Go GC works harder rather
# than the kernel OOM-killing the process. So it must sit BELOW the memory the
# engine can actually have, which on this stack is (RAM − shared_buffers − the OS
# − Caddy). Setting it near total RAM is worse than not setting it: the GC stays
# relaxed right up to the point the machine is already dead.
#
# Measured on the reference box: the engine's own anonymous memory is tens of MB
# even with a million rows and live traffic, so a ceiling of ~30% of RAM is
# generous headroom AND leaves PostgreSQL its 25% plus room for the page cache.
GOMEMLIMIT_MB=$(( MEM_MB * 30 / 100 ))
[ "$GOMEMLIMIT_MB" -lt 256 ] && GOMEMLIMIT_MB=256

PG_CONF_D=""
detect_pg_conf() {
	local dir
	dir="$(psql_super -tAc "SHOW config_file" 2>/dev/null | head -1 || true)"
	[ -n "$dir" ] || die "could not locate postgresql.conf (is PostgreSQL running?)"
	PG_CONF_D="$(dirname "$dir")/conf.d"
	mkdir -p "$PG_CONF_D"
}
psql_super() { runuser -u postgres -- psql -tAX "$@"; }

TUNE_FILE=""
current_of() { psql_super -c "SELECT setting || ' ' || COALESCE(unit,'') FROM pg_settings WHERE name='$1'" 2>/dev/null | tr -s ' '; }

show_table() {
	printf '\n  box: %s MiB RAM, %s vCPU\n\n' "$MEM_MB" "$NPROC"
	printf '  %-22s %-22s %s\n' "setting" "current" "recommended"
	printf '  %s\n' "------------------------------------------------------------------"
	printf '  %-22s %-22s %s\n' "shared_buffers"       "$(current_of shared_buffers)"       "${SHARED_BUFFERS_MB}MB"
	printf '  %-22s %-22s %s\n' "effective_cache_size" "$(current_of effective_cache_size)" "${EFFECTIVE_CACHE_MB}MB"
	printf '  %-22s %-22s %s\n' "work_mem"             "$(current_of work_mem)"             "${WORK_MEM_MB}MB"
	printf '  %-22s %-22s %s\n' "maintenance_work_mem" "$(current_of maintenance_work_mem)" "${MAINT_WORK_MEM_MB}MB"
	printf '  %-22s %-22s %s\n' "max_connections"      "$(current_of max_connections)"      "$MAX_CONNECTIONS"
	printf '  %-22s %-22s %s\n' "GOMEMLIMIT (engine)"  "$(load_env_secret GOMEMLIMIT || echo '<unset>')" "${GOMEMLIMIT_MB}MiB"
	echo
}

write_pg_conf() {
	cat > "$TUNE_FILE" <<EOF
# Appitools production tuning — generated by scripts/verify-production/tune.sh
# Derived from this box: ${MEM_MB} MiB RAM, ${NPROC} vCPU.
# Remove this file (and restart PostgreSQL) to return to the packaged defaults.
shared_buffers = ${SHARED_BUFFERS_MB}MB
effective_cache_size = ${EFFECTIVE_CACHE_MB}MB
work_mem = ${WORK_MEM_MB}MB
maintenance_work_mem = ${MAINT_WORK_MEM_MB}MB
max_connections = ${MAX_CONNECTIONS}
EOF
}

set_env_var() {
	local key="$1" val="$2"
	[ -f "$ENV_FILE" ] || return 0
	if grep -qE "^${key}=" "$ENV_FILE"; then
		sed -i -E "s|^${key}=.*|${key}=${val}|" "$ENV_FILE"
	else
		printf '%s=%s\n' "$key" "$val" >> "$ENV_FILE"
	fi
}

detect_pg_conf
TUNE_FILE="$PG_CONF_D/99-appitools-tuning.conf"

# PostgreSQL only reads conf.d if postgresql.conf includes it. Debian/Ubuntu
# packages do by default, but a hand-built cluster may not — check rather than
# write a file that is silently ignored.
ensure_include() {
	local main; main="$(psql_super -tAc 'SHOW config_file' | head -1)"
	grep -qE "^[[:space:]]*include_dir[[:space:]]*=[[:space:]]*'conf\.d'" "$main" && return 0
	if [ "$MODE" = "apply" ]; then
		printf "\n# added by appitools tune.sh\ninclude_dir = 'conf.d'\n" >> "$main"
		info "added include_dir = 'conf.d' to $main"
	else
		warn "$main does not include conf.d — --apply would add it"
	fi
}

case "$MODE" in
	show)
		hdr "tuning — current vs recommended"
		show_table
		dim "  apply with: sudo bash tune.sh --apply    (revert: --revert)"
		;;
	dry-run)
		hdr "tuning — dry run (nothing is changed)"
		show_table
		info "would write $TUNE_FILE:"
		SAVE="$TUNE_FILE"; TUNE_FILE="/dev/stdout"; write_pg_conf; TUNE_FILE="$SAVE"
		info "would set GOMEMLIMIT=${GOMEMLIMIT_MB}MiB in $ENV_FILE"
		info "would restart: postgresql, $SERVICE"
		ensure_include
		;;
	apply)
		hdr "tuning — applying"
		show_table
		ensure_include
		[ -f "$TUNE_FILE" ] && cp -p "$TUNE_FILE" "${TUNE_FILE}.bak.$(date +%s)"
		[ -f "$ENV_FILE" ] && cp -p "$ENV_FILE" "${ENV_FILE}.pre-tune"
		write_pg_conf
		ok "wrote $TUNE_FILE"
		set_env_var GOMEMLIMIT "${GOMEMLIMIT_MB}MiB"
		ok "set GOMEMLIMIT=${GOMEMLIMIT_MB}MiB in $ENV_FILE"
		# shared_buffers and max_connections need a full restart, not a reload.
		systemctl restart postgresql || die "PostgreSQL failed to restart — check: journalctl -u postgresql -n 40. Revert with: bash tune.sh --revert"
		# Wait for PostgreSQL to accept connections again before restarting the engine.
		tries=0
		while [ "$tries" -lt 20 ] && ! psql_super -c 'SELECT 1' >/dev/null 2>&1; do
			tries=$(( tries + 1 )); sleep 1
		done
		systemctl restart "$SERVICE" || die "the engine failed to restart — journalctl -u $SERVICE -n 40"
		sleep 2
		ok "applied. Now measure again and compare — a change you cannot measure is not an improvement."
		show_table
		;;
	revert)
		hdr "tuning — reverting"
		rm -f "$TUNE_FILE" && ok "removed $TUNE_FILE"
		if [ -f "${ENV_FILE}.pre-tune" ]; then
			mv -f "${ENV_FILE}.pre-tune" "$ENV_FILE"; ok "restored $ENV_FILE"
		fi
		systemctl restart postgresql || warn "PostgreSQL restart failed"
		systemctl restart "$SERVICE" || warn "engine restart failed"
		ok "reverted to the packaged defaults"
		;;
esac
