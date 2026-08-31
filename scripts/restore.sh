#!/usr/bin/env bash
#
# Appximo — restore a backup SET produced by scripts/backup.sh, and PROVE it.
#
# RUNS ON THE SERVER, as root. Born in PROD-JOURNEY-1B as commerce's restore
# drill and made the engine's own in RESILIENCIA-S1, where it was timed on a
# 490 k-row set in both scenarios (a corrupt database; a lost box reinstalled
# with install.sh first). The numbers are in docs/PRODUCTION.md §4.
#
#   sudo bash /opt/appximo/scripts/restore.sh --set=/var/backups/appximo/appximo-20260830-033000
#   sudo bash /opt/vetapp/scripts/restore.sh  --app=vetapp --set=/var/backups/vetapp/vetapp-<stamp>
#
# --set=PREFIX names the four files of one backup: PREFIX.dump (required),
# PREFIX.files.tar.gz, PREFIX.conf.tar[.enc] and PREFIX.manifest (each used
# when present; --no-files / --no-conf skip them; --dump= takes a bare dump).
#
# What it does, in order — every step is the lesson of doing this on a box you
# care about, and every step is TIMED (the numbers you tell a customer):
#   1. STOPS the engine (writes mid-restore would interleave with the load)
#   2. restores /etc/<app> from the conf bundle — the JWT_SECRET, ADMIN_KEY
#      and the boot schema of the box the backup came from — KEEPING this
#      box's DATABASE_URL (a reinstalled box has a new database password)
#   3. drops and recreates the database (the dump is a full-database
#      snapshot; restoring over live objects half-merges two worlds) and
#      loads the dump AS THE SERVICE ROLE, so every object is owned by the
#      role the engine connects as (loading as postgres and handing ownership
#      back afterwards is what crash-looped the first drill: SQLSTATE 42501
#      on the engine's own CREATE OR REPLACE at boot)
#   4. restores the uploaded files (APPXIMO_FILES_DIR) from the files archive
#   5. starts the engine and waits for /healthz + /readyz
#   6. VERIFIES against the manifest: every table's row count, the files on
#      disk, every FK validated, sequences ahead of their tables, and the
#      engine answering — a pg_restore that exits 0 proves nothing by itself
#
# It never deletes or rotates backups, and it refuses to run without a dump.
set -euo pipefail

APP_NAME="appximo"; SET=""; DUMP=""; FILES_TAR=""; CONF_TAR=""; MANIFEST=""
WITH_FILES="yes"; WITH_CONF="yes"; PORT=""
for arg in "$@"; do
	case "$arg" in
		--app=*)      APP_NAME="${arg#*=}" ;;
		--set=*)      SET="${arg#*=}" ;;
		--dump=*)     DUMP="${arg#*=}" ;;
		--files=*)    FILES_TAR="${arg#*=}" ;;
		--conf=*)     CONF_TAR="${arg#*=}" ;;
		--manifest=*) MANIFEST="${arg#*=}" ;;
		--port=*)     PORT="${arg#*=}" ;;
		--no-files)   WITH_FILES="no" ;;
		--no-conf)    WITH_CONF="no" ;;
		--help|-h)    sed -n '3,38p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 1 ;;
	esac
done

if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; Y=$'\033[1;33m'; N=$'\033[0m'; else G=""; R=""; Y=""; N=""; fi
ok()   { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '%s!%s %s\n' "$Y" "$N" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$R" "$N" "$*" >&2; exit 1; }
T_START=$(date +%s.%N); T_STAGE=$T_START
stage() { # stage NAME — prints the elapsed time of the previous stage and starts the next
	local now; now=$(date +%s.%N)
	[ -n "${STAGE_NAME:-}" ] && printf '  %s⏱%s %-28s %6.1f s\n' "$Y" "$N" "$STAGE_NAME" "$(awk -v a="$T_STAGE" -v b="$now" 'BEGIN{print b-a}')"
	STAGE_NAME="$1"; T_STAGE=$now
	# MUST be an `if`: a trailing `[ … ] && …` that short-circuits returns 1, and
	# under `set -e` that ended the script at the final `stage ""` — AFTER every
	# check had passed and BEFORE "RESTORE VERIFIED" (caught in the drill).
	if [ -n "$1" ]; then echo "→ $1…"; fi
}

[ "$(id -u)" = "0" ] || die "run as root"
if [ -n "$SET" ]; then
	[ -n "$DUMP" ] || DUMP="$SET.dump"
	[ -n "$FILES_TAR" ] || { [ -f "$SET.files.tar.gz" ] && FILES_TAR="$SET.files.tar.gz"; } || true
	[ -n "$CONF_TAR" ] || { [ -f "$SET.conf.tar" ] && CONF_TAR="$SET.conf.tar"; } || { [ -f "$SET.conf.tar.enc" ] && CONF_TAR="$SET.conf.tar.enc"; } || true
	[ -n "$MANIFEST" ] || { [ -f "$SET.manifest" ] && MANIFEST="$SET.manifest"; } || true
fi
[ -n "$DUMP" ] || die "--set=PREFIX (or --dump=FILE) is required — a backup.sh custom-format dump"
[ -f "$DUMP" ] || die "dump '$DUMP' not found"
[ "$WITH_FILES" = "yes" ] || FILES_TAR=""
[ "$WITH_CONF" = "yes" ] || CONF_TAR=""

ENV_FILE="/etc/$APP_NAME/$APP_NAME.env"
[ -f "$ENV_FILE" ] || die "$ENV_FILE not found — on a NEW box run install.sh --app=$APP_NAME first (it creates the role, the database, the unit and the env), then this script"
envval() { { grep -E "^$1=" "$ENV_FILE" || true; } | head -1 | cut -d= -f2- | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/"; }
DATABASE_URL="$(envval DATABASE_URL)"
[ -n "$DATABASE_URL" ] || die "no DATABASE_URL in $ENV_FILE"
FILES_DIR="$(envval APPXIMO_FILES_DIR)"; [ -n "$FILES_DIR" ] || FILES_DIR="/var/lib/$APP_NAME/files"
# db name + role from the DSN (postgres://ROLE:pass@host:port/DB?…)
DB="$(printf '%s' "$DATABASE_URL" | sed -E 's#^[a-z]+://[^/]+/([^?]+).*#\1#')"
ROLE="$(printf '%s' "$DATABASE_URL" | sed -E 's#^[a-z]+://([^:/@]+).*#\1#')"
SERVICE="$APP_NAME"
if [ -z "$PORT" ]; then
	PORT="$(systemctl show -p ExecStart --value "$SERVICE" 2>/dev/null | grep -oE -- '--port[= ][0-9]+' | grep -oE '[0-9]+' | head -1)"
	[ -n "$PORT" ] || PORT=8090
fi
SVC_USER="$(systemctl show -p User --value "$SERVICE" 2>/dev/null)"; [ -n "$SVC_USER" ] || SVC_USER="$APP_NAME"
STAMP="$(date +%Y%m%d-%H%M%S)"

# As ROOT, not as postgres: the backup dir is 0700 root (the sets carry the
# secrets) — the first literal run of the 3 a.m. procedure died right here
# because the postgres user could not read the dump (RESILIENCIA-S1).
pg_restore --list "$DUMP" >/dev/null 2>&1 || die "'$DUMP' is not a valid pg_dump custom-format archive (pg_restore --list failed)"
PSQL="runuser -u postgres -- psql -tAX"
echo "restore: app=$APP_NAME db=$DB role=$ROLE service=$SERVICE port=$PORT"
echo "         dump=$DUMP"
[ -n "$FILES_TAR" ] && echo "         files=$FILES_TAR → $FILES_DIR"
[ -n "$CONF_TAR" ]  && echo "         conf=$CONF_TAR → /etc/$APP_NAME (DATABASE_URL of THIS box kept)"
[ -n "$MANIFEST" ]  && echo "         manifest=$MANIFEST (verified after)"
[ -n "$MANIFEST" ]  || warn "no manifest — row counts cannot be verified against the source (backup.sh writes one next to the dump)"

# ── 1. stop ──────────────────────────────────────────────────────────────────
stage "stopping $SERVICE (no writes during the restore)"
systemctl stop "$SERVICE"
ok "$SERVICE stopped"

# ── 2. conf: secrets + boot schema ───────────────────────────────────────────
if [ -n "$CONF_TAR" ]; then
	stage "restoring /etc/$APP_NAME (secrets + schema)"
	[ -f "$CONF_TAR" ] || die "conf bundle '$CONF_TAR' not found"
	tmp="$(mktemp -d)"; chmod 700 "$tmp"
	src="$CONF_TAR"
	case "$CONF_TAR" in
		*.enc)
			: "${BACKUP_PASSPHRASE_FILE:?the conf bundle is encrypted — set BACKUP_PASSPHRASE_FILE=<the 0600 passphrase file used at backup time>}"
			openssl enc -d -aes-256-cbc -pbkdf2 -pass "file:$BACKUP_PASSPHRASE_FILE" -in "$CONF_TAR" -out "$tmp/conf.tar" || die "could not decrypt $CONF_TAR (wrong passphrase?)"
			src="$tmp/conf.tar" ;;
	esac
	tar -C "$tmp" -xf "$src"
	sub="$(find "$tmp" -mindepth 1 -maxdepth 1 -type d | head -1)"
	[ -n "$sub" ] && [ -f "$sub/$(basename "$ENV_FILE")" ] || die "the conf bundle does not contain $(basename "$ENV_FILE") (it holds: $(ls "$tmp"))"
	cp -p "$ENV_FILE" "$ENV_FILE.pre-restore-$STAMP"
	[ -f "/etc/$APP_NAME/schema.json" ] && cp -p "/etc/$APP_NAME/schema.json" "/etc/$APP_NAME/schema.json.pre-restore-$STAMP"
	# The archived env with THIS box's DATABASE_URL: a reinstalled box has a new
	# database password; everything else (JWT_SECRET, ADMIN_KEY, ports, paths,
	# tuning) comes from the backup, so issued tokens and TOTP enrolments work.
	{ printf 'DATABASE_URL=%s\n' "$DATABASE_URL"; grep -vE '^DATABASE_URL=' "$sub/$(basename "$ENV_FILE")"; } > "$ENV_FILE.new"
	chmod 600 "$ENV_FILE.new"; chown "root:$SVC_USER" "$ENV_FILE.new"; mv -f "$ENV_FILE.new" "$ENV_FILE"
	if [ -f "$sub/schema.json" ]; then install -m 0644 "$sub/schema.json" "/etc/$APP_NAME/schema.json"; fi
	rm -rf "$tmp"
	ok "/etc/$APP_NAME restored from the backup (previous env + schema kept as *.pre-restore-$STAMP)"
fi

# ── 3. database ──────────────────────────────────────────────────────────────
stage "dropping + recreating database $DB"
$PSQL -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='$DB' AND pid<>pg_backend_pid()" >/dev/null
$PSQL -c "DROP DATABASE IF EXISTS \"$DB\"" >/dev/null
$PSQL -c "CREATE DATABASE \"$DB\" OWNER \"$ROLE\"" >/dev/null
ok "fresh database $DB (owner $ROLE)"

stage "loading the dump as $ROLE (pg_restore, exit-on-error)"
# As the service role, through the same DSN the engine uses: every restored
# object is owned by the role that will operate it — no ownership hand-back.
# A set taken while the amcheck extension existed (backup.sh between OPS-44 and
# MANUAL-OPERACION-S1) carries CREATE EXTENSION amcheck + COMMENT ON EXTENSION,
# which the service role cannot run; they are not data — filter them out of the
# TOC instead of failing the whole restore (`pg_restore -L`).
RESTORE_LIST="$(mktemp)"; pg_restore -l "$DUMP" | grep -vE '^[0-9]+; [0-9]+ [0-9]+ (EXTENSION|COMMENT) - (EXTENSION )?amcheck' > "$RESTORE_LIST"
if ! pg_restore --exit-on-error --no-owner --no-privileges --use-list="$RESTORE_LIST" --dbname="$DATABASE_URL" "$DUMP"; then
	rm -f "$RESTORE_LIST"
	die "restore FAILED loading the dump — the database is fresh/empty and $SERVICE is STOPPED. Investigate the dump (pg_restore --list), then re-run."
fi
rm -f "$RESTORE_LIST"
ok "dump loaded"

# ── 4. files ─────────────────────────────────────────────────────────────────
if [ -n "$FILES_TAR" ]; then
	stage "restoring uploaded files → $FILES_DIR"
	[ -f "$FILES_TAR" ] || die "files archive '$FILES_TAR' not found"
	mkdir -p "$(dirname "$FILES_DIR")"
	[ -d "$FILES_DIR" ] && mv "$FILES_DIR" "$FILES_DIR.pre-restore-$STAMP"
	tar -C "$(dirname "$FILES_DIR")" -xzf "$FILES_TAR"
	[ -d "$FILES_DIR" ] || die "the files archive did not produce $FILES_DIR (it holds: $(tar -tzf "$FILES_TAR" | head -3 | tr '\n' ' ')…)"
	chown -R "$SVC_USER:$SVC_USER" "$FILES_DIR"
	[ -d "$FILES_DIR.pre-restore-$STAMP" ] && rm -rf "$FILES_DIR.pre-restore-$STAMP"
	ok "files restored ($(find "$FILES_DIR" -type f | wc -l) files)"
fi

# ── 5. start ─────────────────────────────────────────────────────────────────
stage "starting $SERVICE and waiting for /healthz + /readyz"
systemctl start "$SERVICE"
for i in $(seq 1 120); do
	if curl -fsS -m 2 "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && curl -fsS -m 2 "http://127.0.0.1:${PORT}/readyz" >/dev/null 2>&1; then break; fi
	[ "$i" = "120" ] && die "engine did not become healthy after the restore (journalctl -u $SERVICE -n 60)"
	sleep 0.5
done
ok "engine healthy: $(curl -fsS -m 2 "http://127.0.0.1:${PORT}/health" 2>/dev/null || echo '?')"

# ── 6. verify ────────────────────────────────────────────────────────────────
stage "verifying the restore"
FAIL=0
# 6a. counts against the manifest — every table, exact.
if [ -n "$MANIFEST" ]; then
	want="$(grep '^count ' "$MANIFEST" | sort)"
	q="$($PSQL -d "$DB" -c "
	  SELECT string_agg(format('SELECT %L || '' '' || count(*) FROM %I.%I', n.nspname||'.'||c.relname, n.nspname, c.relname), ' UNION ALL ')
	    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
	   WHERE c.relkind='r' AND (n.nspname LIKE 'tenant\_%' OR n.nspname='public')")"
	have="$([ -n "$q" ] && $PSQL -d "$DB" -c "$q" | sort | sed 's/^/count /' || true)"
	if [ "$want" = "$have" ]; then
		ok "row counts: every table matches the manifest ($(printf '%s\n' "$want" | wc -l) tables, $(printf '%s\n' "$want" | awk '{s+=$3} END{print s+0}') rows)"
	else
		FAIL=1; warn "row counts DIFFER from the manifest:"
		diff <(printf '%s\n' "$want") <(printf '%s\n' "$have") | sed 's/^/    /' >&2 || true
	fi
	# 6b. files on disk against the manifest.
	mf="$(grep '^files_count=' "$MANIFEST" | cut -d= -f2)"
	if [ -n "$mf" ] && [ "$mf" != "0" ]; then
		have_f="$(find "$FILES_DIR" -type f 2>/dev/null | wc -l)"
		if [ "$have_f" = "$mf" ]; then ok "files: $have_f on disk == manifest"; else FAIL=1; warn "files: $have_f on disk, manifest says $mf ($( [ -n "$FILES_TAR" ] || echo 'no files archive was restored — ')see $FILES_DIR)"; fi
	fi
fi
# 6c. every foreign key validated (an FK left NOT VALID would silently skip checks).
nv="$($PSQL -d "$DB" -c "SELECT count(*) FROM pg_constraint WHERE contype='f' AND NOT convalidated")"
[ "$nv" = "0" ] && ok "foreign keys: all validated" || { FAIL=1; warn "foreign keys: $nv constraint(s) NOT validated"; }
# 6d. sequences ahead of their columns (a restored sequence behind its table
# makes the next insert collide with an existing id).
seqq="$($PSQL -d "$DB" -c "
  SELECT string_agg(format('SELECT %L AS seq, (SELECT last_value FROM %I.%I) AS lv, (SELECT max(%I)::bigint FROM %I.%I) AS mx',
                           s.schemaname||'.'||s.sequencename, s.schemaname, s.sequencename, a.attname, n.nspname, c.relname), ' UNION ALL ')
    FROM pg_sequences s
    JOIN pg_class sc ON sc.relname = s.sequencename AND sc.relnamespace = s.schemaname::regnamespace
    JOIN pg_depend d ON d.objid = sc.oid AND d.deptype = 'a' AND d.classid = 'pg_class'::regclass
    JOIN pg_class c ON c.oid = d.refobjid
    JOIN pg_namespace n ON n.oid = c.relnamespace
    JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = d.refobjsubid" 2>/dev/null || echo "")"
if [ -n "$seqq" ]; then
	behind="$($PSQL -d "$DB" -c "SELECT count(*) FROM ($seqq) t WHERE mx IS NOT NULL AND (lv IS NULL OR lv < mx)" 2>/dev/null || echo "?")"
else
	behind="0"
fi
case "$behind" in
	0) ok "sequences: none behind its table" ;;
	"?") warn "sequences: could not be checked" ;;
	*) FAIL=1; warn "sequences: $behind sequence(s) BEHIND their table's max id — the next insert would collide" ;;
esac
# 6e. the engine reads the restored control plane through its own DSN: the
# admin tenant list (data plane, X-Admin-Key) must agree with public.tenants.
ADMIN_KEY="$(envval ADMIN_KEY)"
tn="$(curl -fsS -m 5 -H "X-Admin-Key: $ADMIN_KEY" "http://127.0.0.1:$PORT/admin/tenants" 2>/dev/null | grep -o '"resource_count"' | wc -l | tr -d ' ')"
db_tn="$($PSQL -d "$DB" -c "SELECT count(*) FROM public.tenants" 2>/dev/null || echo "?")"
if [ -n "$tn" ] && [ "$tn" = "$db_tn" ]; then ok "engine lists $tn tenant(s) == public.tenants"; else FAIL=1; warn "engine lists ${tn:-?} tenant(s), the database has $db_tn (GET /admin/tenants on :$PORT with the ADMIN_KEY of $ENV_FILE)"; fi

stage ""
TOTAL="$(awk -v a="$T_START" -v b="$(date +%s.%N)" 'BEGIN{printf "%.1f", b-a}')"
if [ "$FAIL" = "0" ]; then
	ok "RESTORE VERIFIED in ${TOTAL}s — the data is back and the engine serves it. Now open the app and read one real record."
else
	die "restore finished in ${TOTAL}s but VERIFICATION FAILED (see above) — do not trust this box until it is explained"
fi
