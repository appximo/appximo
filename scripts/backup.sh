#!/usr/bin/env bash
#
# Appximo — backup (pg_dump custom format + uploads + config + manifest,
# rotation, optional off-box copy, failure notification), for a systemd timer
# or cron.
#
# ONE run produces ONE backup SET, four files with one stamp:
#   <app>-<stamp>.dump          the whole database (every tenant schema + the
#                               control plane), pg_dump -Fc, compressed
#   <app>-<stamp>.files.tar.gz  the uploaded files (APPXIMO_FILES_DIR) — the
#                               dump holds only their METADATA; without this a
#                               restored app answers 404 for every attachment
#   <app>-<stamp>.conf.tar      /etc/<app>: the env (JWT_SECRET, ADMIN_KEY, …)
#                               and the boot schema — 0600; without the SAME
#                               JWT_SECRET a restored box invalidates every
#                               issued token and every enrolled TOTP secret
#   <app>-<stamp>.manifest      what the set contains: per-table row counts,
#                               sizes, sha256 of the dump — restore.sh VERIFIES
#                               the restored database against it, count by count
#
# Restore with scripts/restore.sh (installed next to this one):
#   sudo bash /opt/<app>/scripts/restore.sh --app=<app> --set=/var/backups/<app>/<app>-<stamp>
# A backup you have never restored is not a backup — RESILIENCIA-S1 measured
# the restore of a 490 k-row set; the numbers are in docs/PRODUCTION.md §4.
#
# Scheduling: install.sh writes <app>-backup.timer (03:30 daily by default).
# cron alternative, as root:
#   30 3 * * *  /opt/appximo/scripts/backup.sh --env-file=/etc/appximo/appximo.env >> /var/log/appximo-backup.log 2>&1
#
# Config (env or flags):
#   DATABASE_URL           connection string        [or read from --env-file]
#   BACKUP_DIR             output dir               [default /var/backups/<app>]
#   BACKUP_KEEP            sets to retain           [default 14]
#   BACKUP_COPY_TO         off-box destination: "user@host:/dir" (scp) or an
#                          rclone remote "remote:bucket/path"
#   BACKUP_PASSPHRASE_FILE 0600 file; when set, the conf bundle (secrets) is
#                          encrypted with openssl aes-256-cbc before it is
#                          copied off-box. WITHOUT it the conf bundle NEVER
#                          leaves the box — the dump and the files still do.
#   SLACK_WEBHOOK_URL      a FAILED run posts one message here (read from the
#                          env file too — the same webhook the engine alerts on)
# Flags: --app=NAME --env-file=PATH --dir=PATH --keep=N --copy-to=DEST
#        --files-dir=PATH --no-files --no-conf
set -euo pipefail

# OPS-10: --app=NAME points the backup at THAT app's env file and its own backup
# directory, so a box running several apps backs each one up separately instead of
# silently dumping the same database twice.
APP_NAME=""
BACKUP_DIR="${BACKUP_DIR:-/var/backups/appximo}"
BACKUP_KEEP="${BACKUP_KEEP:-14}"
COPY_TO="${BACKUP_COPY_TO:-}"
ENV_FILE=""
FILES_DIR=""
WITH_FILES="yes"
WITH_CONF="yes"
for arg in "$@"; do
	case "$arg" in
		--app=*)       APP_NAME="${arg#*=}" ;;
		--env-file=*)  ENV_FILE="${arg#*=}" ;;
		--dir=*)       BACKUP_DIR="${arg#*=}" ;;
		--keep=*)      BACKUP_KEEP="${arg#*=}" ;;
		--copy-to=*)   COPY_TO="${arg#*=}" ;;
		--files-dir=*) FILES_DIR="${arg#*=}" ;;
		--no-files)    WITH_FILES="no" ;;
		--no-conf)     WITH_CONF="no" ;;
		--help|-h)     sed -n '3,45p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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

# envval KEY — one value from the env file (plain KEY=value; surrounding quotes stripped).
envval() {
	[ -n "$ENV_FILE" ] && [ -f "$ENV_FILE" ] || return 0
	{ grep -E "^$1=" "$ENV_FILE" || true; } | head -1 | cut -d= -f2- | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"
}

if [ -n "$ENV_FILE" ]; then
	[ -f "$ENV_FILE" ] || { echo "env file '$ENV_FILE' not found" >&2; exit 1; }
	DATABASE_URL="$(envval DATABASE_URL)"
	[ -n "$FILES_DIR" ] || FILES_DIR="$(envval APPXIMO_FILES_DIR)"
	[ -n "${SLACK_WEBHOOK_URL:-}" ] || SLACK_WEBHOOK_URL="$(envval SLACK_WEBHOOK_URL)"
	[ -n "$COPY_TO" ] || COPY_TO="$(envval BACKUP_COPY_TO)"
	[ -n "${BACKUP_PASSPHRASE_FILE:-}" ] || BACKUP_PASSPHRASE_FILE="$(envval BACKUP_PASSPHRASE_FILE)"
fi
: "${DATABASE_URL:?DATABASE_URL is required (set it, or pass --env-file=/etc/appximo/appximo.env)}"
command -v pg_dump >/dev/null || { echo "pg_dump not found (apt-get install postgresql-client)" >&2; exit 1; }
[ -n "$FILES_DIR" ] || { [ -n "$APP_NAME" ] && FILES_DIR="/var/lib/$APP_NAME/files"; } || true

mkdir -p "$BACKUP_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
PREFIX="${APP_NAME:-appximo}"
SET="$BACKUP_DIR/$PREFIX-$STAMP"
STATUS_FILE="$BACKUP_DIR/last-backup.status"
T0=$(date +%s.%N)

# ── failure path: status file + one notification, never silent ──────────────
# The status file is what the engine's self-monitor reads (APPXIMO_BACKUP_DIR):
# "failed" or a stale "ok" both raise its backup alert (RESILIENCIA-S1 §D).
notify() {
	[ -n "${SLACK_WEBHOOK_URL:-}" ] || return 0
	curl -fsS -m 10 -X POST -H 'Content-Type: application/json' \
		-d "$(printf '{"text":"%s"}' "$(printf '%s' "$1" | sed 's/"/\\"/g')")" "$SLACK_WEBHOOK_URL" >/dev/null 2>&1 || true
}
on_fail() {
	local rc=$? line=$1
	[ "$rc" -eq 0 ] && return 0
	rm -f "$SET".*.partial 2>/dev/null || true
	printf 'failed %s app=%s exit=%d line=%d\n' "$(date -u +%FT%TZ)" "$PREFIX" "$rc" "$line" > "$STATUS_FILE" 2>/dev/null || true
	echo "$(date -u +%FT%TZ) backup FAILED (exit $rc at line $line) — status written to $STATUS_FILE" >&2
	notify "🔴 [$PREFIX] backup FAILED on $(hostname) (exit $rc at line $line of backup.sh) — no new backup set exists; the last good one is $(ls -1t "$BACKUP_DIR/$PREFIX"-*.dump 2>/dev/null | head -1 || echo none)"
	exit "$rc"
}
trap 'on_fail $LINENO' ERR

# ── 1. the database ──────────────────────────────────────────────────────────
# Dump to a .partial then rename, so a crashed run never leaves a truncated file
# that looks like a good backup.
pg_dump -Fc --no-owner --dbname="$DATABASE_URL" -f "$SET.dump.partial"
mv -f "$SET.dump.partial" "$SET.dump"
DUMP_SHA="$(sha256sum "$SET.dump" | cut -d' ' -f1)"
echo "$(date -u +%FT%TZ) dump ok: $SET.dump ($(du -h "$SET.dump" | cut -f1))"

# ── 2. the uploads ───────────────────────────────────────────────────────────
# The database holds the files' metadata (tenant_<id>.files); the bytes live in
# APPXIMO_FILES_DIR (content-addressed). A restore without them answers 404 on
# every attachment while the rows claim the files exist.
FILES_COUNT=0; FILES_BYTES=0
if [ "$WITH_FILES" = "yes" ] && [ -n "$FILES_DIR" ] && [ -d "$FILES_DIR" ]; then
	FILES_COUNT="$(find "$FILES_DIR" -type f | wc -l)"
	FILES_BYTES="$(du -sb "$FILES_DIR" | cut -f1)"
	if [ "$FILES_COUNT" -gt 0 ]; then
		tar -C "$(dirname "$FILES_DIR")" -czf "$SET.files.tar.gz.partial" "$(basename "$FILES_DIR")"
		mv -f "$SET.files.tar.gz.partial" "$SET.files.tar.gz"
		echo "$(date -u +%FT%TZ) files ok: $SET.files.tar.gz ($FILES_COUNT files, $(du -h "$SET.files.tar.gz" | cut -f1))"
	else
		echo "$(date -u +%FT%TZ) files: $FILES_DIR is empty — no files archive in this set"
	fi
elif [ "$WITH_FILES" = "yes" ]; then
	echo "$(date -u +%FT%TZ) files: no files dir (${FILES_DIR:-unset}) — no files archive in this set"
fi

# ── 3. the config (secrets + boot schema) ────────────────────────────────────
# /etc/<app> is what a NEW box cannot regenerate: the JWT_SECRET (every issued
# token, every TOTP enrolment and the platform MFA are bound to it), the
# ADMIN_KEY, and the schema the service boots with. 0600, root only.
CONF_DIR=""
[ -n "$ENV_FILE" ] && CONF_DIR="$(dirname "$ENV_FILE")"
if [ "$WITH_CONF" = "yes" ] && [ -n "$CONF_DIR" ] && [ -d "$CONF_DIR" ]; then
	( umask 077; tar -C "$(dirname "$CONF_DIR")" -cf "$SET.conf.tar.partial" "$(basename "$CONF_DIR")" )
	mv -f "$SET.conf.tar.partial" "$SET.conf.tar"
	chmod 600 "$SET.conf.tar"
	echo "$(date -u +%FT%TZ) conf ok: $SET.conf.tar (0600 — holds the secrets)"
fi

# ── 4. the manifest: what a restore must reproduce ───────────────────────────
# Exact per-table counts of every tenant + control-plane table, so restore.sh
# can prove "the rows are back" instead of "pg_restore exited 0".
{
	printf 'app=%s\nstamp=%s\nhost=%s\ntaken_at=%s\n' "$PREFIX" "$STAMP" "$(hostname)" "$(date -u +%FT%TZ)"
	printf 'pg_version=%s\n' "$(psql -tAX --dbname="$DATABASE_URL" -c 'SHOW server_version')"
	printf 'db_size_bytes=%s\n' "$(psql -tAX --dbname="$DATABASE_URL" -c 'SELECT pg_database_size(current_database())')"
	printf 'dump_sha256=%s\ndump_bytes=%s\n' "$DUMP_SHA" "$(stat -c %s "$SET.dump")"
	printf 'files_dir=%s\nfiles_count=%s\nfiles_bytes=%s\n' "${FILES_DIR:-}" "$FILES_COUNT" "$FILES_BYTES"
	[ -n "$CONF_DIR" ] && [ "$WITH_CONF" = "yes" ] && printf 'conf_dir=%s\n' "$CONF_DIR"
	# One statement, exact counts (a full scan per table — milliseconds for a
	# shop, a couple of seconds for a million rows; taken once a night).
	psql -tAX --dbname="$DATABASE_URL" -c "
	  SELECT string_agg(format('SELECT %L || '' '' || count(*) FROM %I.%I', n.nspname||'.'||c.relname, n.nspname, c.relname), ' UNION ALL ')
	    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
	   WHERE c.relkind='r' AND (n.nspname LIKE 'tenant\_%' OR n.nspname='public')" \
	| { read -r q; [ -n "$q" ] && psql -tAX --dbname="$DATABASE_URL" -c "$q" | sort | sed 's/^/count /'; }
} > "$SET.manifest.partial"
mv -f "$SET.manifest.partial" "$SET.manifest"
TABLES="$(grep -c '^count ' "$SET.manifest" || true)"
ROWS="$(awk '/^count /{s+=$3} END{print s+0}' "$SET.manifest")"
echo "$(date -u +%FT%TZ) manifest ok: $SET.manifest ($TABLES tables, $ROWS rows)"

# ── 5. off-box copy (optional) ───────────────────────────────────────────────
# A backup on the disk that holds the database is one failure away from gone —
# a dead host takes both. scp to a box you own, or rclone to object storage
# (DO Spaces / S3 / R2 / B2). The conf bundle carries the secrets, so it goes
# ONLY encrypted (BACKUP_PASSPHRASE_FILE); without a passphrase it stays here
# and this run says so — loudly, every time.
COPIED="no"
if [ -n "$COPY_TO" ]; then
	to_copy="$SET.dump $SET.manifest"
	[ -f "$SET.files.tar.gz" ] && to_copy="$to_copy $SET.files.tar.gz"
	if [ -f "$SET.conf.tar" ]; then
		if [ -n "${BACKUP_PASSPHRASE_FILE:-}" ] && [ -f "$BACKUP_PASSPHRASE_FILE" ]; then
			openssl enc -aes-256-cbc -pbkdf2 -salt -pass "file:$BACKUP_PASSPHRASE_FILE" -in "$SET.conf.tar" -out "$SET.conf.tar.enc"
			chmod 600 "$SET.conf.tar.enc"
			to_copy="$to_copy $SET.conf.tar.enc"
		else
			echo "$(date -u +%FT%TZ) conf NOT copied off-box: set BACKUP_PASSPHRASE_FILE (a 0600 file) to ship the secrets encrypted — without it a lost box needs /etc/$PREFIX from somewhere else" >&2
		fi
	fi
	case "$COPY_TO" in
		*@*:*|*:/*)
			# shellcheck disable=SC2086
			scp -q -o BatchMode=yes -o ConnectTimeout=15 $to_copy "$COPY_TO/" ;;
		*:*)
			command -v rclone >/dev/null || { echo "rclone not found for destination '$COPY_TO' (apt-get install rclone; rclone config)" >&2; false; }
			for f in $to_copy; do rclone copyto "$f" "$COPY_TO/$(basename "$f")"; done ;;
		*)
			echo "unrecognized copy destination '$COPY_TO' (use user@host:/dir or remote:bucket/path)" >&2; false ;;
	esac
	COPIED="yes"
	echo "$(date -u +%FT%TZ) copied off-box → $COPY_TO ($(echo "$to_copy" | wc -w) files)"
else
	echo "$(date -u +%FT%TZ) NOT copied off-box (no --copy-to / BACKUP_COPY_TO): this backup dies with the disk it sits on" >&2
fi

# ── 6. status + rotation ─────────────────────────────────────────────────────
ELAPSED="$(awk -v a="$T0" -v b="$(date +%s.%N)" 'BEGIN{printf "%.1f", b-a}')"
printf 'ok %s app=%s set=%s rows=%s files=%s offbox=%s seconds=%s\n' "$(date -u +%FT%TZ)" "$PREFIX" "$SET" "$ROWS" "$FILES_COUNT" "$COPIED" "$ELAPSED" > "$STATUS_FILE"
echo "$(date -u +%FT%TZ) backup ok: $SET.* in ${ELAPSED}s (rows=$ROWS files=$FILES_COUNT offbox=$COPIED)"

# Rotation: keep the newest $BACKUP_KEEP SETS of this app — every file of an
# older stamp goes together. The glob must match what this run writes —
# hardcoding "appximo-*" meant a namespaced app's dumps were never pruned, and
# (when two apps shared a directory) that the wrong app's dumps were the only
# ones eligible.
# shellcheck disable=SC2012
{ ls -1t "$BACKUP_DIR/$PREFIX"-*.dump 2>/dev/null || true; } | tail -n +"$((BACKUP_KEEP + 1))" | while read -r old; do
	base="${old%.dump}"
	rm -f "$base.dump" "$base.files.tar.gz" "$base.conf.tar" "$base.conf.tar.enc" "$base.manifest" && echo "$(date -u +%FT%TZ) pruned old backup set: $base.*"
done
