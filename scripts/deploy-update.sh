#!/usr/bin/env bash
#
# Appximo — update an installed binary (engine OR consumer app) to a new one, safely.
#
# Run this ON the production box, after copying the new binary up (scp). It backs
# up the live binary, swaps atomically, restarts the systemd service, and
# health-checks — rolling back automatically if the new binary fails to come up,
# AND verifying the rollback actually recovered (a rollback that does not
# re-check health is a report, not a recovery — CONSUMER-PATH-S1).
#
# The full official flow (from your dev machine):
#   1. build:  ./scripts/build-engine.sh /tmp/appximo "$(git rev-parse --short HEAD)" "$(git rev-parse HEAD)"
#              (consumer apps: scripts/build-consumer.sh — same flags, plus the SPA)
#   2. copy:   scp /tmp/appximo you@server:/tmp/appximo
#   3. swap:   ssh you@server 'sudo bash /opt/appximo/scripts/deploy-update.sh --binary=/tmp/appximo'
#
# Health is POLLED every 250 ms with an early exit — the wait costs what the boot
# costs, not a fixed 30 s (the old fixed loop made a failed deploy's rollback take
# 30+ s of user-visible 502s; measured in PROD-JOURNEY-1B). A unit systemd reports
# as failed short-circuits the wait immediately.
#
# Flags:
#   --binary=PATH        the new binary to install                     [required]
#   --cli=PATH           also update the ops companion (appximo-cli) [optional]
#   --app=NAME           the app to update on a multi-app box (OPS-10): sets
#                        --service=NAME and --dest=/opt/NAME/bin/NAME
#   --service=NAME       systemd unit name                             [default appximo]
#   --dest=PATH          installed binary path                         [default /opt/appximo/bin/appximo]
#   --port=PORT          engine port for the health check              [default 8090]
#   --health-timeout=S   max seconds to wait for health                [default 30]
#   --help
set -euo pipefail

BINARY=""; CLI=""; SERVICE="appximo"; DEST="/opt/appximo/bin/appximo"; PORT="8090"
# OPS-10: --app=NAME derives the unit and the installed path for a box running
# several apps, so you never have to spell both out (and never point one app's
# deploy at another app's binary).
APP_NAME=""
HEALTH_TIMEOUT="30"
for arg in "$@"; do
	case "$arg" in
		--binary=*)  BINARY="${arg#*=}" ;;
		--cli=*)     CLI="${arg#*=}" ;;
		--app=*)     APP_NAME="${arg#*=}" ;;
		--service=*) SERVICE="${arg#*=}" ;;
		--dest=*)    DEST="${arg#*=}" ;;
		--port=*)    PORT="${arg#*=}" ;;
		--health-timeout=*) HEALTH_TIMEOUT="${arg#*=}" ;;
		--help|-h)   sed -n '3,31p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 1 ;;
	esac
done

# --app derives the unit + destination unless they were given explicitly (OPS-10).
if [ -n "$APP_NAME" ]; then
	[ "$SERVICE" = "appximo" ] && SERVICE="$APP_NAME"
	[ "$DEST" = "/opt/appximo/bin/appximo" ] && DEST="/opt/$APP_NAME/bin/$APP_NAME"
fi

if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; Y=$'\033[1;33m'; N=$'\033[0m'; else G=""; R=""; Y=""; N=""; fi
ok()  { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
die() { printf '%s✗%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "run as root (sudo)"
[ -n "$BINARY" ] || die "--binary is required"
[ -f "$BINARY" ] || die "--binary '$BINARY' not found"
command -v systemctl >/dev/null || die "systemctl not found (this box is not systemd-managed)"

# Sanity: the deployable contract (docs/adr/ADR-023) — `version` must run and
# print an identity line. Engine and consumer binaries both qualify; a wrong
# file or wrong arch fails HERE, before anything is touched.
NEW_ID="$("$BINARY" version 2>/dev/null | head -1)"
[ -n "$NEW_ID" ] || die "'$BINARY' does not honor the deployable contract ('<binary> version' must exit 0 and print identity) — wrong file/arch? See docs/adr/ADR-023"
ok "deploying: $NEW_ID"

# wait_healthy TIMEOUT_S — poll /healthz + /readyz every 250 ms; early exit on
# success, early exit on a binary that cannot boot. Two crash signals, because
# units with Restart=always (ours) NEVER enter systemd's "failed" state while
# crash-looping — systemd just keeps relaunching them (measured: the is-failed
# check alone let a broken deploy wait out the full 30 s):
#   1. is-failed        — covers units without auto-restart
#   2. NRestarts +2     — two AUTOMATIC restarts since we began means the new
#                         binary is dying at boot; decide now, not at timeout
wait_healthy() {
	local timeout_s="$1" waited=0
	local restarts0 r
	restarts0="$(systemctl show -p NRestarts --value "$SERVICE" 2>/dev/null || echo "")"
	while :; do
		if curl -fsS -m 2 "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 \
			&& curl -fsS -m 2 "http://127.0.0.1:${PORT}/readyz" >/dev/null 2>&1; then
			return 0
		fi
		if systemctl is-failed --quiet "$SERVICE" 2>/dev/null; then
			return 1 # crashed at boot — fail fast, do not wait out the timeout
		fi
		if [ -n "$restarts0" ]; then
			r="$(systemctl show -p NRestarts --value "$SERVICE" 2>/dev/null || echo "")"
			if [ -n "$r" ] && [ "$r" -ge $((restarts0 + 2)) ] 2>/dev/null; then
				return 1 # crash-looping under Restart=always — the binary is dead on boot
			fi
		fi
		waited=$((waited + 1))
		[ $((waited / 4)) -ge "$timeout_s" ] && return 1
		sleep 0.25
	done
}

BACKUP_DIR="$(dirname "$DEST")-rollback"
STAMP="$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"

# 1 — back up the live binary (skip on a first install with nothing to back up).
if [ -f "$DEST" ]; then
	cp -p "$DEST" "$BACKUP_DIR/$(basename "$DEST").$STAMP"
	ok "backed up live binary → $BACKUP_DIR/$(basename "$DEST").$STAMP"
	# Retain the newest 10 timestamped backups.
	# shellcheck disable=SC2012
	ls -1t "$BACKUP_DIR/$(basename "$DEST")".* 2>/dev/null | tail -n +11 | xargs -r rm -f
fi

# 2 — atomic swap: copy beside the target, then rename over it (rename is atomic;
# the running process keeps its old inode until the restart).
install -m 0755 "$BINARY" "$DEST.new"
mv -f "$DEST.new" "$DEST"
ok "swapped in new binary at $DEST"

# 2b — optionally update the ops companion (skipped when it is a symlink to the
# engine binary itself — already updated by the swap above).
if [ -n "$CLI" ]; then
	[ -f "$CLI" ] || die "--cli '$CLI' not found"
	install -m 0755 "$CLI" "$(dirname "$DEST")/appximo-cli"
	ok "ops CLI updated"
fi

# 3 — restart + health check (polling, early exit).
systemctl restart "$SERVICE"
ok "restarted $SERVICE — waiting for health (poll 250 ms, timeout ${HEALTH_TIMEOUT}s)…"
if wait_healthy "$HEALTH_TIMEOUT"; then
	VER="$("$DEST" version 2>/dev/null | tail -1 || true)"
	ok "healthy — now running: ${VER:-unknown}"
	exit 0
fi

# 4 — rollback: the new binary never became healthy. Restore, restart, and
# VERIFY the recovery — "rolled back" only means something if the old binary is
# actually answering again.
printf '%s! new binary unhealthy — rolling back%s\n' "$Y" "$N" >&2
LATEST="$(ls -1t "$BACKUP_DIR/$(basename "$DEST")".* 2>/dev/null | head -1 || true)"
[ -n "$LATEST" ] || die "no rollback backup available. Investigate: journalctl -u $SERVICE -n 60"
install -m 0755 "$LATEST" "$DEST.new" && mv -f "$DEST.new" "$DEST"
systemctl reset-failed "$SERVICE" >/dev/null 2>&1 || true
systemctl restart "$SERVICE"
if wait_healthy "$HEALTH_TIMEOUT"; then
	VER="$("$DEST" version 2>/dev/null | tail -1 || true)"
	printf '%s!%s rolled back to %s and VERIFIED healthy — again serving: %s\n' "$Y" "$N" "$LATEST" "${VER:-unknown}" >&2
	printf '%s!%s the NEW binary failed to boot. Investigate: journalctl -u %s -n 60\n' "$Y" "$N" "$SERVICE" >&2
	exit 1
fi
die "ROLLBACK DID NOT RECOVER: restored $LATEST but the service is still unhealthy — the box needs a human NOW. journalctl -u $SERVICE -n 80"
