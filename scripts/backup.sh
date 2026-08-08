#!/usr/bin/env bash
#
# Appximo — PostgreSQL backup (pg_dump custom format + rotation), for cron.
#
# Dumps the whole Appximo database — every tenant's schema plus the control
# plane (public.tenants, …) — as ONE compressed custom-format file, and prunes
# old dumps. Custom format (-Fc) is compressed and restores selectively with
# pg_restore. This backs up the DATA plane; the schema definitions also live in
# public.schema_history, so a restore brings both back.
#
# A backup you have never restored is not a backup. Test your restore:
#   createdb appximo_restore_test
#   pg_restore -d appximo_restore_test /var/backups/appximo/appximo-<stamp>.dump
#   # …inspect, then: dropdb appximo_restore_test
#
# Cron (nightly at 03:30, keep 14 days), as root:
#   30 3 * * *  DATABASE_URL='postgres://appximo:PASS@localhost:5432/appximo' \
#               /opt/appximo/scripts/backup.sh >> /var/log/appximo-backup.log 2>&1
#
# Config (env or flags):
#   DATABASE_URL       connection string          [or read from --env-file]
#   BACKUP_DIR         output dir                  [default /var/backups/appximo]
#   BACKUP_KEEP        how many dumps to retain    [default 14]
# Flags: --env-file=PATH (read DATABASE_URL from a KEY=value file, e.g. the
#        installer's /etc/appximo/appximo.env), --dir=PATH, --keep=N.
set -euo pipefail

# OPS-10: --app=NAME points the backup at THAT app's env file and its own backup
# directory, so a box running several apps backs each one up separately instead of
# silently dumping the same database twice.
APP_NAME=""
BACKUP_DIR="${BACKUP_DIR:-/var/backups/appximo}"
BACKUP_KEEP="${BACKUP_KEEP:-14}"
ENV_FILE=""
for arg in "$@"; do
	case "$arg" in
		--app=*)      APP_NAME="${arg#*=}" ;;
		--env-file=*) ENV_FILE="${arg#*=}" ;;
		--dir=*)      BACKUP_DIR="${arg#*=}" ;;
		--keep=*)     BACKUP_KEEP="${arg#*=}" ;;
		--help|-h)    sed -n '3,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 1 ;;
	esac
done

# LAUNCHPAD-S1: infer the app from --env-file when --app was not given. The
# installed copy lives at /opt/<app>/scripts/backup.sh and is naturally invoked
# with just its own env file (/etc/<app>/<app>.env), and without this every app
# on the box wrote "appximo-<stamp>.dump" into ONE shared directory — two apps'
# backups interleaved under one prefix, which a third-party deploy hit on the
# first run. --app still wins when passed.
if [ -z "$APP_NAME" ] && [ -n "$ENV_FILE" ]; then
	case "$ENV_FILE" in
		/etc/*/*.env) APP_NAME="$(basename "$(dirname "$ENV_FILE")")" ;;
	esac
fi

# --app derives this app's env file + its own backup directory (OPS-10).
if [ -n "$APP_NAME" ]; then
	[ -n "$ENV_FILE" ] || ENV_FILE="/etc/$APP_NAME/$APP_NAME.env"
	[ "$BACKUP_DIR" = "/var/backups/appximo" ] && BACKUP_DIR="/var/backups/$APP_NAME"
fi

if [ -n "$ENV_FILE" ]; then
	[ -f "$ENV_FILE" ] || { echo "env file '$ENV_FILE' not found" >&2; exit 1; }
	DATABASE_URL="$(grep -E '^DATABASE_URL=' "$ENV_FILE" | head -1 | cut -d= -f2-)"
fi
: "${DATABASE_URL:?DATABASE_URL is required (set it, or pass --env-file=/etc/appximo/appximo.env)}"
command -v pg_dump >/dev/null || { echo "pg_dump not found (apt-get install postgresql-client)" >&2; exit 1; }

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$BACKUP_DIR/${APP_NAME:-appximo}-$STAMP.dump"
TMP="$OUT.partial"

# Dump to a .partial then rename, so a crashed run never leaves a truncated file
# that looks like a good backup.
pg_dump -Fc --no-owner --dbname="$DATABASE_URL" -f "$TMP"
mv -f "$TMP" "$OUT"
SIZE="$(du -h "$OUT" | cut -f1)"
echo "$(date -u +%FT%TZ) backup ok: $OUT ($SIZE)"

# Rotation: keep the newest $BACKUP_KEEP dumps OF THIS APP. The glob must match
# what this run writes — hardcoding "appximo-*" meant a namespaced app's dumps
# were never pruned, and (when two apps shared a directory) that the wrong app's
# dumps were the only ones eligible.
# shellcheck disable=SC2012
ls -1t "$BACKUP_DIR/${APP_NAME:-appximo}"-*.dump 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
	rm -f "$old" && echo "$(date -u +%FT%TZ) pruned old backup: $old"
done
