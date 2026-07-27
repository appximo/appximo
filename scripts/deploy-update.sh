#!/usr/bin/env bash
#
# Appitools — update an installed engine to a new binary, safely.
#
# Run this ON the production box, after copying the new binary up (scp). It backs
# up the live binary, swaps atomically, restarts the systemd service, and
# health-checks — rolling back automatically if the new binary fails to come up.
# The mirror of the DevHub deploy pipeline, standalone (no DevHub needed).
#
# The full official flow (from your dev machine):
#   1. build:  ./scripts/build-engine.sh /tmp/appitools "$(git rev-parse --short HEAD)" "$(git rev-parse HEAD)"
#   2. copy:   scp /tmp/appitools you@server:/tmp/appitools
#   3. swap:   ssh you@server 'sudo bash /opt/appitools/scripts/deploy-update.sh --binary=/tmp/appitools'
#
# Caddy keeps answering during the ~1s systemd restart (it retries the upstream),
# and /readyz flips to 503 while the old process drains — so no request is lost
# mid-flight. This is the state of the art for this profile; blue/green is
# deliberately NOT used (see docs/PRODUCTION.md).
#
# Flags:
#   --binary=PATH   the new engine binary to install                 [required]
#   --service=NAME  systemd unit name                                [default appitools]
#   --dest=PATH     installed binary path                            [default /opt/appitools/bin/appitools]
#   --port=PORT     engine port for the health check                 [default 8090]
#   --help
set -euo pipefail

BINARY=""; SERVICE="appitools"; DEST="/opt/appitools/bin/appitools"; PORT="8090"
for arg in "$@"; do
	case "$arg" in
		--binary=*)  BINARY="${arg#*=}" ;;
		--service=*) SERVICE="${arg#*=}" ;;
		--dest=*)    DEST="${arg#*=}" ;;
		--port=*)    PORT="${arg#*=}" ;;
		--help|-h)   sed -n '3,27p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 1 ;;
	esac
done

if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; Y=$'\033[1;33m'; N=$'\033[0m'; else G=""; R=""; Y=""; N=""; fi
ok()  { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
die() { printf '%s✗%s %s\n' "$R" "$N" "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "run as root (sudo)"
[ -n "$BINARY" ] || die "--binary is required"
[ -f "$BINARY" ] || die "--binary '$BINARY' not found"
command -v systemctl >/dev/null || die "systemctl not found (this box is not systemd-managed)"

# Sanity: the new file must be a runnable appitools binary of the right kind.
"$BINARY" version >/dev/null 2>&1 || die "'$BINARY' does not run 'appitools version' — wrong file?"

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

# 3 — restart + health check.
systemctl restart "$SERVICE"
ok "restarted $SERVICE — waiting for health…"
healthy="no"
for _ in $(seq 1 30); do
	if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 \
		&& curl -fsS "http://127.0.0.1:${PORT}/readyz" >/dev/null 2>&1; then
		healthy="yes"; break
	fi
	sleep 1
done

if [ "$healthy" = "yes" ]; then
	VER="$("$DEST" version 2>/dev/null | tail -1 || true)"
	ok "healthy — now running: ${VER:-unknown}"
	exit 0
fi

# 4 — rollback: the new binary never became healthy.
printf '%s! new binary unhealthy — rolling back%s\n' "$Y" "$N" >&2
LATEST="$(ls -1t "$BACKUP_DIR/$(basename "$DEST")".* 2>/dev/null | head -1 || true)"
if [ -n "$LATEST" ]; then
	install -m 0755 "$LATEST" "$DEST.new" && mv -f "$DEST.new" "$DEST"
	systemctl restart "$SERVICE"
	die "rolled back to $LATEST and restarted. Investigate: journalctl -u $SERVICE -n 60"
fi
die "no rollback backup available. Investigate: journalctl -u $SERVICE -n 60"
