#!/usr/bin/env bash
#
# Appximo — fleet audit: is THIS box protected? (CAOS-S1)
#
# Run it ON any installed box, as root. It finds every installed app (by
# /etc/<app>/<app>.env) and answers, per app, in ten seconds, WHAT IS MISSING —
# not just "ok": the backup timer, the last backup's age and completeness
# (uploads/secrets/manifest in the set), the off-box copy, the passphrase, the
# unit's restart policy, restore.sh, the binary's version, and the box-level
# facts (swap, disk, PostgreSQL data checksums).
#
#   sudo bash /opt/appximo/scripts/fleet-audit.sh            # every app on the box
#   sudo bash /opt/appximo/scripts/fleet-audit.sh --app=vetapp
#
# Exit code: 0 = everything green; 1 = at least one ✗ (something is missing);
# each ✗ line says what to do. Read-only: it changes NOTHING.
set -uo pipefail

ONLY=""
for arg in "$@"; do
	case "$arg" in
		--app=*) ONLY="${arg#*=}" ;;
		--help|-h) sed -n '3,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 2 ;;
	esac
done
if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; Y=$'\033[1;33m'; N=$'\033[0m'; else G=""; R=""; Y=""; N=""; fi
FAIL=0
ok()   { printf '  %s✓%s %s\n' "$G" "$N" "$*"; }
bad()  { printf '  %s✗%s %s\n' "$R" "$N" "$*"; FAIL=1; }
meh()  { printf '  %s!%s %s\n' "$Y" "$N" "$*"; }
envval() { { grep -E "^$2=" "$1" 2>/dev/null || true; } | head -1 | cut -d= -f2- | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }

audit_app() {
	local app="$1" envf="/etc/$1/$1.env"
	printf '%s── app %s%s%s\n' "$N" "$G" "$app" "$N"
	[ -f "$envf" ] || { bad "$envf missing — is this an installed app?"; return; }

	# 1. service + unit policy
	if systemctl is-active --quiet "$app" 2>/dev/null; then ok "service $app active"; else bad "service $app is NOT active — systemctl status $app"; fi
	local rs sl
	rs="$(systemctl show -p RestartUSec --value "$app" 2>/dev/null)"
	sl="$(systemctl show -p StartLimitIntervalUSec --value "$app" 2>/dev/null)"
	if [ "$sl" = "0" ] || [ "$sl" = "infinity" ]; then ok "unit never gives up (StartLimitIntervalSec=0; RestartSec=$rs)"
	else bad "unit CAN give up after a burst of restarts (StartLimitIntervalSec=$sl) — a database that is slow to come back can strand the app; re-run install.sh (writes RestartSec=2 + StartLimitIntervalSec=0)"; fi

	# 2. binary answers and reports a version
	local bin ver
	bin="$(systemctl show -p ExecStart --value "$app" 2>/dev/null | sed -n 's/.*path=\([^ ;]*\).*/\1/p' | head -1)"
	if [ -n "$bin" ] && [ -x "$bin" ]; then
		ver="$(APPXIMO_NO_VERSION_CHECK=1 "$bin" version 2>/dev/null | head -1)"
		[ -n "$ver" ] && ok "binary $bin → $ver" || bad "binary $bin does not answer 'version' (deployable contract)"
	else bad "cannot resolve the unit's binary (ExecStart)"; fi

	# 3. companions
	local sdir="/opt/$app/scripts" missing=""
	for sc in backup.sh restore.sh deploy-update.sh; do [ -x "$sdir/$sc" ] || missing="$missing $sc"; done
	if [ -z "$missing" ]; then ok "companions in $sdir (backup.sh, restore.sh, deploy-update.sh)"
	else bad "missing in $sdir:$missing — re-run install.sh with --scripts=DIR (or copy them from the repo)"; fi
	if [ -x "$sdir/restore.sh" ] && ! grep -q -- "--set=PREFIX" "$sdir/restore.sh" 2>/dev/null; then
		meh "$sdir/restore.sh is NOT the engine's set-restore (an app-local variant?) — the documented drill uses --set (docs/PRODUCTION.md §4.3)"
	fi

	# 4. the backup timer
	if systemctl is-active --quiet "$app-backup.timer" 2>/dev/null; then
		ok "backup timer active (next: $(systemctl list-timers "$app-backup.timer" --no-pager 2>/dev/null | sed -n 2p | awk '{print $1, $2, $3}'))"
	else bad "NO active $app-backup.timer — this app has no scheduled backup; re-run install.sh (writes + enables it) or: systemctl enable --now $app-backup.timer"; fi

	# 5. the last backup: fresh, complete, honest
	local bdir; bdir="$(envval "$envf" APPXIMO_BACKUP_DIR)"; [ -n "$bdir" ] || bdir="/var/backups/$app"
	grep -q '^APPXIMO_BACKUP_DIR=' "$envf" || bad "APPXIMO_BACKUP_DIR not in $envf — the engine's self-monitor is NOT watching backup liveness; add APPXIMO_BACKUP_DIR=$bdir and restart"
	local newest; newest="$({ ls -1t "$bdir/$app"-*.dump 2>/dev/null || true; } | head -1)"
	if [ -z "$newest" ]; then
		bad "no backup set in $bdir — nothing to restore from; run one now: bash $sdir/backup.sh --app=$app"
	else
		local age_h; age_h=$(( ( $(date +%s) - $(stat -c %Y "$newest") ) / 3600 ))
		if [ "$age_h" -le 36 ]; then ok "last backup ${age_h}h ago ($(basename "$newest"))"; else bad "last backup is ${age_h}h old (floor 36h) — the timer is not doing its job; run one and check journalctl -u $app-backup"; fi
		local base="${newest%.dump}"
		[ -f "$base.manifest" ] || bad "the set has NO manifest — restore.sh cannot verify counts; the backup.sh here is outdated, re-run install.sh"
		[ -f "$base.conf.tar" ] || [ -f "$base.conf.tar.enc" ] || bad "the set has NO conf bundle — a lost box loses JWT_SECRET (all tokens + TOTP die); outdated backup.sh"
		local st="$bdir/last-backup.status"
		if [ -f "$st" ]; then case "$(head -c 6 "$st")" in ok*) ok "last-backup.status: $(head -c 90 "$st")";; *) bad "last-backup.status says: $(head -c 120 "$st")";; esac
		else meh "no last-backup.status yet (first run of the new backup.sh writes it)"; fi
	fi

	# 6. off-box + an alert destination
	local hook; hook="$(envval "$envf" SLACK_WEBHOOK_URL)"
	if [ -n "$hook" ]; then ok "alert destination set (SLACK_WEBHOOK_URL) — backup/disk/SLO/first-error alerts reach a human"
	else bad "NO alert destination (SLACK_WEBHOOK_URL unset) — every alert (failed/stale backup, low disk, SLO burn, first occurrence of an error) is a journal line nobody reads; set it in $envf and restart (OPS-47)"; fi
	local cpto pass; cpto="$(envval "$envf" BACKUP_COPY_TO)"; pass="$(envval "$envf" BACKUP_PASSPHRASE_FILE)"
	if [ -n "$cpto" ]; then
		ok "off-box copy configured → $cpto"
		if [ -n "$pass" ] && [ -f "$pass" ]; then ok "conf passphrase file present ($pass) — secrets travel encrypted"
		else bad "BACKUP_PASSPHRASE_FILE missing/unset — the SECRETS never leave this box; a lost box needs /etc/$app from somewhere else"; fi
	else
		bad "NO off-box copy (BACKUP_COPY_TO unset) — every backup dies with this disk; set it in $envf (docs/PRODUCTION.md §4.1: Spaces \$5/mo, or scp to a box you own)"
	fi
}

# ── box-level facts ──────────────────────────────────────────────────────────
printf '%s── box %s%s\n' "$N" "$(hostname)" "$N"
swap_kb="$(awk '/^SwapTotal:/{print $2}' /proc/meminfo)"; mem_kb="$(awk '/^MemTotal:/{print $2}' /proc/meminfo)"
if [ "$swap_kb" -gt 0 ]; then ok "swap $(( swap_kb/1024 )) MiB"; elif [ "$mem_kb" -le $((2*1024*1024)) ]; then bad "NO swap on a $(( mem_kb/1024 )) MiB box — a bulk load can OOM-kill the shared PostgreSQL (docs/PRODUCTION.md §Prerequisites)"; else meh "no swap (box has $(( mem_kb/1024/1024 )) GiB — acceptable)"; fi
free_pct="$(df --output=pcent / | tail -1 | tr -d ' %')"
if [ "$free_pct" -lt 90 ]; then ok "disk / at ${free_pct}% used"; else bad "disk / at ${free_pct}% used — under 10% free; PostgreSQL dies catastrophically at 100% (free old sets, journalctl --vacuum-size=200M)"; fi
pg_unit="$(systemctl list-units --type=service --all --no-legend 'postgresql@*-main.service' 2>/dev/null | awk '{print $1}' | head -1)"
if [ -n "$pg_unit" ]; then
	pgr="$(systemctl show -p Restart --value "$pg_unit" 2>/dev/null)"
	if [ "$pgr" = "no" ] || [ -z "$pgr" ]; then bad "$pg_unit has Restart=$pgr — an OOM-killed/crashed PostgreSQL stays DOWN (every app on the box with it) until a human starts it; re-run install.sh (adds a Restart=on-failure drop-in)"
	else ok "$pg_unit Restart=$pgr — survives an OOM-kill / crash"; fi
fi
if command -v psql >/dev/null 2>&1; then
	cks="$(runuser -u postgres -- psql -tAX -c 'SHOW data_checksums' 2>/dev/null || echo '?')"
	case "$cks" in
		on)  ok "PostgreSQL data_checksums ON — page corruption is an ERROR on first read, not silent bad data" ;;
		off) meh "PostgreSQL data_checksums OFF — corruption is served silently until a full scan hits it; enabling needs the cluster stopped (pg_checksums --enable; minutes per 10 GB) — docs/PRODUCTION.md §4" ;;
		*)   meh "could not read data_checksums" ;;
	esac
fi

# ── the apps ─────────────────────────────────────────────────────────────────
found=0
for envf in /etc/*/*.env; do
	[ -f "$envf" ] || continue
	app="$(basename "$(dirname "$envf")")"
	[ "$(basename "$envf")" = "$app.env" ] || continue
	grep -q '^DATABASE_URL=' "$envf" || continue
	[ -n "$ONLY" ] && [ "$ONLY" != "$app" ] && continue
	found=$((found+1)); audit_app "$app"
done
[ "$found" -gt 0 ] || { echo "no installed apps found (/etc/<app>/<app>.env with DATABASE_URL)"; exit 2; }
echo
if [ "$FAIL" = 0 ]; then printf '%s✓ this box is protected%s (%d app(s) audited)\n' "$G" "$N" "$found"; else printf '%s✗ this box is NOT fully protected%s — fix the ✗ lines above\n' "$R" "$N"; fi
exit $FAIL
