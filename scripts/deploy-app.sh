#!/usr/bin/env bash
#
# Appximo — deploy ONE app on ONE box, with the whole protocol, from the outside
# (DEPLOY-FLOTA-S1). Run it from your machine (or the operator box); it does
# over SSH what an operator used to do by memory — and a step nobody remembers
# is a step that eventually gets skipped:
#
#   0. the deployable contract — `<binary> version` must answer, and the version
#      it prints is what /health must say afterwards (nothing is trusted later)
#   1. inventory of the app ON the box: unit, binary path, port, env — derived
#      from systemd, never guessed (a hand-installed app keeps its own names)
#   2. a full BACKUP SET first (backup.sh: dump + uploads + secrets + manifest);
#      a backup that fails aborts the deploy before anything is touched; plus a
#      copy of the binary/env/schema about to be replaced (/root/<app>-*.pre-<tag>)
#   3. the swap through deploy-update.sh (atomic rename, restart, health polled,
#      its own rollback for a binary that cannot boot)
#   4. VERIFICATION FROM OUTSIDE, over the public URL, as a client would:
#        - /health through the proxy says the EXPECTED version (a site proxying
#          a neighbour's port answers with the neighbour's version)
#        - /readyz is 200
#        - an AUTHENTICATED READ: a token minted ON the box with the app's own
#          secret (never printed) → GET /api/<resource>?per_page=1 → 200 + data
#        - a WRITE PROBE that changes nothing by construction: one-op
#          POST /api/transaction deleting a uuid that does not exist → the
#          engine answers 404 with failed_operation=0 ONLY after authenticating,
#          authorizing the delete, opening the tenant transaction and running the
#          statement — the write path executed and rolled back, zero rows moved
#      Any failure → AUTOMATIC ROLLBACK to the pre-deploy binary → the same
#      verification again → the outcome is reported as what it is
#   5. a file that must not change (--keep, e.g. a golden dump) is md5'd before
#      and after — a mismatch is reported loudly (data is never "rolled back")
#   6. fleet-audit.sh --app=<app> at the end: a box left with gaps is exit 3,
#      even though the deploy itself succeeded (the ✗ lines say what to fix)
#
# A /health 200 is NOT a verified deploy. This script's verdict is the read +
# the write probe + the version through the proxy, or a verified rollback.
#
# Usage:
#   scripts/deploy-app.sh --host=root@BOX --app=NAME --binary=PATH --url=https://app.example.com [options]
#
# Flags:
#   --host=USER@IP        ssh target (the box)                                  [required]
#   --app=NAME            the installed app (unit + /etc/NAME/NAME.env)         [required]
#   --binary=PATH         the new binary (engine or consumer, ADR-023 contract) [required]
#   --url=BASE            the PUBLIC base URL the verification uses (through
#                         the proxy; the tenant host is its hostname)           [required]
#   --cli=PATH            also update the ops companion (<dir>/appximo-cli)
#   --tenant=ID           tenant for the probes          [default: first label of the URL host]
#   --tenant-host=HOST    Host header for the probes     [default: the URL host]
#   --resolve=IP          curl --resolve for the URL host (a lab box without DNS)
#   --insecure            curl -k (a lab box with Caddy `tls internal`) — never production
#   --role=ROLE           role of the probe token         [default: admin]
#   --resource=NAME       resource for the read + write probe [default: first list route in /openapi.json]
#   --keep=PATH           a file on the box whose md5 must be identical before and after
#   --tag=LABEL           label for the pre-deploy copies [default: the new version]
#   --no-audit            skip the final fleet-audit
#   --timeout=S           health timeout passed to deploy-update.sh [default 30]
#
# Exit: 0 deployed + verified (+ box protected) · 1 verification failed, rolled
# back and re-verified · 2 rollback did not recover — a human NOW · 3 deployed +
# verified but the audit found gaps (or --keep changed) · 4 aborted before any
# change (contract, backup, inventory).
set -uo pipefail

HOST=""; APP=""; BINARY=""; URL=""; CLI=""; TENANT=""; THOST=""; RESOLVE=""; INSECURE=0
ROLE="admin"; RESOURCE=""; KEEP=""; TAG=""; AUDIT=1; TIMEOUT=30
for arg in "$@"; do
	case "$arg" in
		--host=*) HOST="${arg#*=}" ;;
		--app=*) APP="${arg#*=}" ;;
		--binary=*) BINARY="${arg#*=}" ;;
		--url=*) URL="${arg#*=}" ;;
		--cli=*) CLI="${arg#*=}" ;;
		--tenant=*) TENANT="${arg#*=}" ;;
		--tenant-host=*) THOST="${arg#*=}" ;;
		--resolve=*) RESOLVE="${arg#*=}" ;;
		--insecure) INSECURE=1 ;;
		--role=*) ROLE="${arg#*=}" ;;
		--resource=*) RESOURCE="${arg#*=}" ;;
		--keep=*) KEEP="${arg#*=}" ;;
		--tag=*) TAG="${arg#*=}" ;;
		--no-audit) AUDIT=0 ;;
		--timeout=*) TIMEOUT="${arg#*=}" ;;
		--help|-h) sed -n '3,66p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 4 ;;
	esac
done
if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; B=$'\033[1m'; N=$'\033[0m'; else G=""; R=""; B=""; N=""; fi
T0=$(date +%s.%N)
now() { printf '%6.1fs' "$(echo "$(date +%s.%N) - $T0" | bc)"; }
ok()   { printf '%s %s✓%s %s\n' "$(now)" "$G" "$N" "$*"; }
bad()  { printf '%s %s✗%s %s\n' "$(now)" "$R" "$N" "$*" >&2; }
step() { printf '\n%s%s── %s%s\n' "$(now)" "$B" "$*" "$N"; }
die()  { bad "$*"; exit 4; }

[ -n "$HOST" ] && [ -n "$APP" ] && [ -n "$BINARY" ] && [ -n "$URL" ] || die "--host, --app, --binary and --url are required (see --help)"
[ -f "$BINARY" ] || die "--binary '$BINARY' not found"
[ -z "$CLI" ] || [ -f "$CLI" ] || die "--cli '$CLI' not found"
command -v bc >/dev/null || die "bc is required"
command -v python3 >/dev/null || die "python3 is required"
URLHOST="$(printf '%s' "$URL" | sed -E 's#^[a-z]+://##; s#/.*$##; s#:[0-9]+$##')"
[ -n "$THOST" ] || THOST="$URLHOST"
[ -n "$TENANT" ] || TENANT="${THOST%%.*}"
SSH=(ssh -o BatchMode=yes -o ConnectTimeout=15 "$HOST")
CURL=(curl -sS -m 20 -H "Host: $THOST")
[ "$INSECURE" = 1 ] && CURL+=(-k)
[ -n "$RESOLVE" ] && CURL+=(--resolve "$URLHOST:443:$RESOLVE" --resolve "$URLHOST:80:$RESOLVE")

# ── 0. the contract, locally ────────────────────────────────────────────────
step "0 · the deployable contract"
NEW_ID="$("$BINARY" version 2>/dev/null | head -1)"
[ -n "$NEW_ID" ] || die "'$BINARY' does not honor the deployable contract ('<binary> version' must print an identity line — docs/adr/ADR-023)"
EXPECT="$(printf '%s' "$NEW_ID" | awk '{print $2}')"
[ -n "$TAG" ] || TAG="$EXPECT"
ok "new binary: $NEW_ID → /health must say version \"$EXPECT\""

# ── 1. inventory on the box ─────────────────────────────────────────────────
step "1 · inventory of $APP on $HOST (from systemd, not from memory)"
INV="$("${SSH[@]}" bash -s "$APP" <<'REMOTE'
set -u; APP=$1
ENVF=/etc/$APP/$APP.env; [ -f "$ENVF" ] || { echo "ERR env file $ENVF missing — is $APP installed here?"; exit 0; }
systemctl cat "$APP" >/dev/null 2>&1 || { echo "ERR unit $APP not found"; exit 0; }
EXEC="$(systemctl show -p ExecStart --value "$APP" | sed -n 's/.*argv\[\]=\([^;]*\);.*/\1/p' | head -1)"
BIN="$(printf '%s' "$EXEC" | awk '{print $1}')"
PORT="$(printf '%s' "$EXEC" | sed -n 's/.*--port[= ]\([0-9]*\).*/\1/p')"
[ -n "$PORT" ] || PORT="$(grep -E '^(APPXIMO|APPITOOLS)_PORT=' "$ENVF" | head -1 | cut -d= -f2-)"
[ -n "$PORT" ] || PORT=8090
DIR="$(dirname "$BIN")"; CLIB=""
for c in "$DIR"/appximo-cli "$DIR"/appitools-cli; do [ -x "$c" ] && CLIB="$c" && break; done
SCHEMA="$(printf '%s' "$EXEC" | sed -n 's/.*--schema[= ]\([^ ]*\).*/\1/p')"; [ -n "$SCHEMA" ] || SCHEMA=/etc/$APP/schema.json
SDIR=/opt/$APP/scripts
echo "BIN=$BIN"; echo "PORT=$PORT"; echo "CLIB=$CLIB"; echo "SCHEMA=$SCHEMA"; echo "SDIR=$SDIR"; echo "ENVF=$ENVF"
echo "PID0=$(systemctl show -p MainPID --value "$APP")"; echo "NR0=$(systemctl show -p NRestarts --value "$APP")"
echo "VER0=$("$BIN" version 2>/dev/null | head -1)"
for s in backup.sh deploy-update.sh fleet-audit.sh; do [ -x "$SDIR/$s" ] || echo "ERR companion $SDIR/$s missing (or not executable) — install the companions first (docs/PRODUCTION.md §4.5b)"; done
REMOTE
)"
if printf '%s\n' "$INV" | grep -q '^ERR'; then printf '%s\n' "$INV" | grep '^ERR' | sed 's/^ERR /  /' >&2; die "inventory failed — nothing was touched"; fi
inv() { printf '%s\n' "$INV" | sed -n "s/^$1=//p" | head -1; }
R_BIN="$(inv BIN)"; R_PORT="$(inv PORT)"; R_CLIB="$(inv CLIB)"; R_SCHEMA="$(inv SCHEMA)"; R_SDIR="$(inv SDIR)"; R_ENVF="$(inv ENVF)"
R_PID0="$(inv PID0)"; R_NR0="$(inv NR0)"; R_VER0="$(inv VER0)"
ok "unit $APP · binary $R_BIN · port $R_PORT · schema $R_SCHEMA"
ok "running now: ${R_VER0:-?} (PID $R_PID0, NRestarts $R_NR0)"
[ -n "$R_CLIB" ] && ok "ops companion: $R_CLIB" || ok "no ops companion — the engine binary itself mints the probe token"

# the probe resource, from the contract the box actually serves
if [ -z "$RESOURCE" ]; then
	RESOURCE="$("${CURL[@]}" "$URL/openapi.json" 2>/dev/null | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: sys.exit(0)
for p,ops in sorted(d.get("paths",{}).items()):
    if p.startswith("/api/") and "{" not in p and p.count("/")==2 and "get" in ops and p not in ("/api/files","/api/transaction"):
        print(p[5:]); break')"
	[ -n "$RESOURCE" ] || die "could not pick a probe resource from $URL/openapi.json — pass --resource=NAME"
fi
ok "probe: tenant $TENANT (Host $THOST) · role $ROLE · resource $RESOURCE"

# ── 2. backup first ─────────────────────────────────────────────────────────
step "2 · backup set + copies of what the deploy replaces"
if ! "${SSH[@]}" bash -s "$APP" "$R_ENVF" "$R_SDIR" "$R_BIN" "$R_SCHEMA" "$TAG" "$R_CLIB" <<'REMOTE'
set -u; APP=$1; ENVF=$2; SDIR=$3; BIN=$4; SCHEMA=$5; TAG=$6; CLIB=$7
out="$(bash "$SDIR/backup.sh" --app="$APP" 2>&1)"; rc=$?
printf '%s\n' "$out" | tail -3 | sed 's/^/  /'
[ $rc -eq 0 ] || { echo "  backup.sh exit $rc — DEPLOY ABORTED before touching anything"; exit 1; }
keep() { [ -e "$2" ] || cp -p "$1" "$2"; }   # the FIRST pre-deploy copy of a tag wins (a re-run never overwrites it)
keep "$BIN" "/root/$APP-bin.pre-$TAG"; keep "$ENVF" "/root/$APP-env.pre-$TAG"; keep "$SCHEMA" "/root/$APP-schema.pre-$TAG"
[ -n "$CLIB" ] && keep "$CLIB" "/root/$APP-cli.pre-$TAG"
echo "  pre-deploy copies: /root/$APP-{bin,env,schema}.pre-$TAG"
REMOTE
then die "backup failed — nothing was touched"; fi
ok "backup set written; pre-deploy copies kept"
KEEP0=""
if [ -n "$KEEP" ]; then KEEP0="$("${SSH[@]}" "md5sum '$KEEP' 2>/dev/null | cut -d' ' -f1")"; [ -n "$KEEP0" ] || die "--keep $KEEP not found on the box"; ok "keep: $KEEP md5 $KEEP0"; fi

# ── 3. the swap ─────────────────────────────────────────────────────────────
step "3 · swap through deploy-update.sh (atomic, health-polled, self-rolling-back)"
RB="/tmp/$APP-deploy-$TAG"; RC="/tmp/$APP-cli-$TAG"
scp -q -o BatchMode=yes "$BINARY" "$HOST:$RB" || die "scp of the binary failed"
CLIFLAG=""
if [ -n "$CLI" ]; then scp -q -o BatchMode=yes "$CLI" "$HOST:$RC" || die "scp of the cli failed"; CLIFLAG="--cli=$RC"; fi
swap() { # $1 = binary path ON the box
	# pipefail on the REMOTE shell too — without it `| sed` hides the script's exit code
	"${SSH[@]}" "set -o pipefail; sudo bash '$R_SDIR/deploy-update.sh' --binary='$1' --service='$APP' --dest='$R_BIN' --port='$R_PORT' --health-timeout='$TIMEOUT' $2 2>&1 | sed 's/^/  /'"
}
if ! swap "$RB" "$CLIFLAG"; then
	bad "deploy-update.sh reported failure — it rolls back and re-checks health itself (above)"
	VNOW="$("${SSH[@]}" "'$R_BIN' version 2>/dev/null | head -1")"
	bad "serving now: $VNOW"
	exit 1
fi
PID1="$("${SSH[@]}" "systemctl show -p MainPID --value '$APP'")"
ok "swapped and healthy on the box — PID $R_PID0 → $PID1"

# ── 4. verification from OUTSIDE ────────────────────────────────────────────
mint_token() { # never printed; the secret never leaves the box
	"${SSH[@]}" bash -s "$R_ENVF" "${R_CLIB:-$R_BIN}" "$TENANT" "$ROLE" "$R_SCHEMA" <<'REMOTE'
set -u; ENVF=$1; CLIB=$2; TENANT=$3; ROLE=$4; SCHEMA=$5
SEC="$( { grep -E '^JWT_SECRET=' "$ENVF" || grep -E '^APPITOOLS_JWT_SECRET=' "$ENVF"; } | head -1 | cut -d= -f2- | sed -e 's/^"\(.*\)"$/\1/' -e "s/^'\(.*\)'$/\1/")"
[ -n "$SEC" ] || { echo "ERR no JWT_SECRET in $ENVF"; exit 0; }
# A fixed, documented probe identity: the token is minted by the box's own
# secret, lives in a shell variable on the operator side and is NEVER printed.
TOK="$(APPXIMO_NO_VERSION_CHECK=1 "$CLIB" token --secret "$SEC" --tenant "$TENANT" --role "$ROLE" --schema "$SCHEMA" --user-id 00000000-0000-4000-8000-00000000dead 2>/tmp/.mint.err | tail -1)"
if [ -z "$TOK" ]; then echo "ERR $(head -3 /tmp/.mint.err | tr '\n' ' ')"; else printf '%s\n' "$TOK"; fi
rm -f /tmp/.mint.err
REMOTE
}
verify() { # prints ✓/✗ lines; returns 0 when every probe passed
	local fails=0 code body ver
	body="$("${CURL[@]}" "$URL/health" 2>/dev/null)"
	ver="$(printf '%s' "$body" | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("version",""))
except Exception: print("")')"
	if [ "$ver" = "$EXPECT" ]; then ok "/health through the proxy → version \"$ver\" (expected)"; else bad "/health through the proxy → version \"$ver\", expected \"$EXPECT\" — body: $(printf '%s' "$body" | head -c 160)"; fails=$((fails+1)); fi
	code="$("${CURL[@]}" -o /dev/null -w '%{http_code}' "$URL/readyz" 2>/dev/null)"
	if [ "$code" = 200 ]; then ok "/readyz → 200"; else bad "/readyz → $code"; fails=$((fails+1)); fi
	local tok; tok="$(mint_token)"
	if [ -z "$tok" ]; then bad "could not mint the probe token on the box (empty answer)"; return 1; fi
	if printf '%s' "$tok" | grep -q '^ERR'; then bad "could not mint the probe token on the box: ${tok#ERR }"; return 1; fi
	local hdr=(-H "Authorization: Bearer $tok")
	body="$("${CURL[@]}" "${hdr[@]}" -o /tmp/.dap.body -w '%{http_code}' "$URL/api/$RESOURCE?per_page=1" 2>/dev/null)"
	if [ "$body" = 200 ] && python3 -c 'import json,sys; d=json.load(open("/tmp/.dap.body")); sys.exit(0 if isinstance(d.get("data"),list) else 1)' 2>/dev/null; then
		ok "authenticated READ: GET /api/$RESOURCE?per_page=1 → 200 with data[] (role $ROLE, tenant $TENANT)"
	else bad "authenticated READ: GET /api/$RESOURCE?per_page=1 → $body $(head -c 200 /tmp/.dap.body 2>/dev/null)"; fails=$((fails+1)); fi
	body="$("${CURL[@]}" "${hdr[@]}" -H 'Content-Type: application/json' -o /tmp/.dap.body -w '%{http_code}' -X POST "$URL/api/transaction" \
		-d "{\"operations\":[{\"op\":\"delete\",\"resource\":\"$RESOURCE\",\"id\":\"00000000-0000-4000-8000-000000000000\"}]}" 2>/dev/null)"
	local fo; fo="$(python3 -c 'import json; d=json.load(open("/tmp/.dap.body")); print(d.get("failed_operation","?"))' 2>/dev/null)"
	if [ "$body" = 404 ] && [ "$fo" = 0 ]; then
		ok "WRITE PROBE: POST /api/transaction (delete a uuid that does not exist) → 404 failed_operation=0 — authenticated, authorized, the tenant tx ran the statement and rolled back; nothing changed"
	elif [ "$body" = 403 ]; then bad "WRITE PROBE: 403 — role $ROLE may not delete on $RESOURCE; pass --role/--resource so the probe is real"; fails=$((fails+1))
	else bad "WRITE PROBE: POST /api/transaction → $body $(head -c 200 /tmp/.dap.body 2>/dev/null)"; fails=$((fails+1)); fi
	rm -f /tmp/.dap.body
	[ "$fails" = 0 ]
}
step "4 · verification from OUTSIDE ($URL, Host $THOST)"
if verify; then
	ok "VERIFIED: $NEW_ID is serving $URL"
	RESULT=0
else
	step "4b · verification FAILED → automatic rollback to the pre-deploy binary"
	PRE="/root/$APP-bin.pre-$TAG"; PRECLI=""
	[ -n "$R_CLIB" ] && "${SSH[@]}" "test -f /root/$APP-cli.pre-$TAG" && PRECLI="--cli=/root/$APP-cli.pre-$TAG"
	EXPECT_NEW="$EXPECT"
	EXPECT="$(printf '%s' "$R_VER0" | awk '{print $2}')"
	if swap "$PRE" "$PRECLI"; then
		step "4c · verifying the ROLLBACK from outside (expected version \"$EXPECT\")"
		if verify; then
			bad "ROLLED BACK and VERIFIED: $R_VER0 is serving again; the new binary ($EXPECT_NEW) failed verification — investigate: ssh $HOST journalctl -u $APP -n 80"
			RESULT=1
		else
			bad "ROLLBACK DID NOT RECOVER from outside — the box needs a human NOW: ssh $HOST journalctl -u $APP -n 80"
			exit 2
		fi
	else
		bad "rollback swap failed — the box needs a human NOW: ssh $HOST journalctl -u $APP -n 80"
		exit 2
	fi
fi

# ── 5. what must not change ─────────────────────────────────────────────────
if [ -n "$KEEP" ]; then
	step "5 · $KEEP unchanged?"
	KEEP1="$("${SSH[@]}" "md5sum '$KEEP' 2>/dev/null | cut -d' ' -f1")"
	if [ "$KEEP1" = "$KEEP0" ]; then ok "md5 $KEEP1 — identical before and after"; else bad "md5 CHANGED: $KEEP0 → $KEEP1 — data is never rolled back by this script; look NOW"; [ "$RESULT" = 0 ] && RESULT=3; fi
fi

# ── 6. the audit ────────────────────────────────────────────────────────────
if [ "$AUDIT" = 1 ]; then
	step "6 · fleet-audit.sh --app=$APP (what is MISSING on this box)"
	if "${SSH[@]}" "set -o pipefail; sudo bash '$R_SDIR/fleet-audit.sh' --app='$APP' 2>&1 | sed 's/^/  /'"; then ok "box protected"; else bad "the box is NOT fully protected — the ✗ lines above say what to fix (deploy itself: $( [ "$RESULT" = 0 ] && echo verified || echo 'rolled back'))"; [ "$RESULT" = 0 ] && RESULT=3; fi
fi

step "summary"
"${SSH[@]}" "echo \"  PID $R_PID0 → \$(systemctl show -p MainPID --value '$APP') · NRestarts $R_NR0 → \$(systemctl show -p NRestarts --value '$APP') · serving: \$('$R_BIN' version 2>/dev/null | head -1)\"; echo \"  rollback copies: /root/$APP-bin.pre-$TAG (+ env/schema) and \$(dirname '$R_BIN')-rollback/\""
case "$RESULT" in
	0) ok "deployed, verified from outside, box protected — exit 0" ;;
	1) bad "verification failed; rolled back and re-verified — exit 1" ;;
	3) bad "deployed and verified, but the box has gaps (audit/keep) — exit 3" ;;
esac
exit "$RESULT"
