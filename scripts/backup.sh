#!/usr/bin/env bash
#
# Appitools — PostgreSQL backup (pg_dump custom format + rotation), for cron.
#
# Dumps the whole Appitools database — every tenant's schema plus the control
# plane (public.tenants, …) — as ONE compressed custom-format file, and prunes
# old dumps. Custom format (-Fc) is compressed and restores selectively with
# pg_restore. This backs up the DATA plane; the schema definitions also live in
# public.schema_history, so a restore brings both back.
#
# A backup you have never restored is not a backup. Test your restore:
#   createdb appitools_restore_test
#   pg_restore -d appitools_restore_test /var/backups/appitools/appitools-<stamp>.dump
#   # …inspect, then: dropdb appitools_restore_test
#
# Cron (nightly at 03:30, keep 14 days), as root:
#   30 3 * * *  DATABASE_URL='postgres://appitools:PASS@localhost:5432/appitools' \
#               /opt/appitools/scripts/backup.sh >> /var/log/appitools-backup.log 2>&1
#
# Config (env or flags):
#   DATABASE_URL       connection string          [or read from --env-file]
#   BACKUP_DIR         output dir                  [default /var/backups/appitools]
#   BACKUP_KEEP        how many dumps to retain    [default 14]
# Flags: --env-file=PATH (read DATABASE_URL from a KEY=value file, e.g. the
#        installer's /etc/appitools/appitools.env), --dir=PATH, --keep=N.
set -euo pipefail

BACKUP_DIR="${BACKUP_DIR:-/var/backups/appitools}"
BACKUP_KEEP="${BACKUP_KEEP:-14}"
ENV_FILE=""
for arg in "$@"; do
	case "$arg" in
		--env-file=*) ENV_FILE="${arg#*=}" ;;
		--dir=*)      BACKUP_DIR="${arg#*=}" ;;
		--keep=*)     BACKUP_KEEP="${arg#*=}" ;;
		--help|-h)    sed -n '3,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 1 ;;
	esac
done

if [ -n "$ENV_FILE" ]; then
	[ -f "$ENV_FILE" ] || { echo "env file '$ENV_FILE' not found" >&2; exit 1; }
	DATABASE_URL="$(grep -E '^DATABASE_URL=' "$ENV_FILE" | head -1 | cut -d= -f2-)"
fi
: "${DATABASE_URL:?DATABASE_URL is required (set it, or pass --env-file=/etc/appitools/appitools.env)}"
command -v pg_dump >/dev/null || { echo "pg_dump not found (apt-get install postgresql-client)" >&2; exit 1; }

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT="$BACKUP_DIR/appitools-$STAMP.dump"
TMP="$OUT.partial"

# Dump to a .partial then rename, so a crashed run never leaves a truncated file
# that looks like a good backup.
pg_dump -Fc --no-owner --dbname="$DATABASE_URL" -f "$TMP"
mv -f "$TMP" "$OUT"
SIZE="$(du -h "$OUT" | cut -f1)"
echo "$(date -u +%FT%TZ) backup ok: $OUT ($SIZE)"

# Rotation: keep the newest $BACKUP_KEEP dumps.
# shellcheck disable=SC2012
ls -1t "$BACKUP_DIR"/appitools-*.dump 2>/dev/null | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
	rm -f "$old" && echo "$(date -u +%FT%TZ) pruned old backup: $old"
done
