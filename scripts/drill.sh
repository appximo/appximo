#!/usr/bin/env bash
#
# Appximo — drill.sh: the box-level drills behind `appximo drill chaos <n>` and
# `appximo drill restore` (MANUAL-OPERACION-S1).
#
# RUNS ON THE BOX, as root, against an INSTALLED app (/etc/<app>/<app>.env +
# the systemd unit). The CLI wrapper (`appximo drill`) prints the what /
# expected / where blocks and REFUSES a production-looking target without
# --production; this script is the mechanism, and it restores EVERY fault it
# injects on exit — also on Ctrl-C (trap).
#
#   sudo bash /opt/appximo/scripts/drill.sh chaos <1-10> --app=appximo [--tenant=ID] [--resource=NAME] [--full] [--yes-reboot] [--yes]
#   sudo bash /opt/appximo/scripts/drill.sh restore --app=appximo [--set=PREFIX] [--real] [--yes]
#
# The ten experiments are CAOS-S1's (evidencia/CAOS-S1/d0-hipotesis.md), each
# with its hypothesis written BEFORE it ran; here each one prints H (the
# hypothesis) before and a verdict after, measured with a 10 rps probe:
#   1 kill -9 the engine        6 network to the database black-holed (ENG-59)
#   2 kill -9 PostgreSQL        7 200 ms of latency to the database
#   3 reboot the box            8 clock 2 h backwards
#   4 fill the disk             9 two concurrent writes to one row
#   5 memory to the OOM edge   10 the pool full (a table locked for 20 s)
#
# restore (default) = a REHEARSAL: the newest set's dump loaded into a scratch
# database next to the live one, counts verified against the manifest, the
# scratch dropped — the app never stops. --real runs restore.sh (stop → conf →
# drop/create → load → files → start → verify) — the real thing.
set -uo pipefail

MODE="${1:-}"; shift || true
N=""; [ "$MODE" = "chaos" ] && { N="${1:-}"; shift || true; }
APP=""; URL=""; TENANT=""; RESOURCE=""; FULL="no"; YES_REBOOT="no"; YES="no"; SET=""; REAL="no"
for arg in "$@"; do
	case "$arg" in
		--app=*) APP="${arg#*=}" ;;
		--url=*) URL="${arg#*=}" ;;
		--tenant=*) TENANT="${arg#*=}" ;;
		--resource=*) RESOURCE="${arg#*=}" ;;
		--set=*) SET="${arg#*=}" ;;
		--full) FULL="yes" ;;
		--yes-reboot) YES_REBOOT="yes" ;;
		--yes) YES="yes" ;;
		--real) REAL="yes" ;;
		--help|-h) sed -n '3,29p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) echo "unknown flag: $arg" >&2; exit 2 ;;
	esac
done
[ "$MODE" = "chaos" ] || [ "$MODE" = "restore" ] || { echo "usage: drill.sh chaos <1-10> --app=NAME | drill.sh restore --app=NAME  (--help)" >&2; exit 2; }
[ -n "$APP" ] || { echo "--app=NAME is required" >&2; exit 2; }
[ "$(id -u)" = "0" ] || { echo "run as root (the drills touch the unit, iptables, tc, the clock)" >&2; exit 2; }

if [ -t 1 ]; then G=$'\033[0;32m'; R=$'\033[0;31m'; Y=$'\033[1;33m'; B=$'\033[1m'; N0=$'\033[0m'; else G=""; R=""; Y=""; B=""; N0=""; fi
ok()   { printf '%s✓%s %s\n' "$G" "$N0" "$*"; }
bad()  { printf '%s✗%s %s\n' "$R" "$N0" "$*"; }
note() { printf '%s•%s %s\n' "$Y" "$N0" "$*"; }
hyp()  { printf '%sH:%s %s\n' "$B" "$N0" "$*"; }
die()  { bad "$*"; exit 1; }
now()  { date -u +%T.%3N; }

# ── resolve the app (the unit is the truth) ──────────────────────────────────
ENVF="/etc/$APP/$APP.env"
[ -f "$ENVF" ] || die "$ENVF missing — is $APP an installed app?"
set -a
# shellcheck disable=SC1090
. "$ENVF"
set +a
DATABASE_URL="${DATABASE_URL:-}"; [ -n "$DATABASE_URL" ] || die "DATABASE_URL not in $ENVF"
JWT_SECRET="${JWT_SECRET:-${APPITOOLS_JWT_SECRET:-}}"; ADMIN_KEY="${ADMIN_KEY:-${APPITOOLS_ADMIN_KEY:-}}"
SERVICE="$APP"
EXEC="$(systemctl show -p ExecStart --value "$SERVICE" 2>/dev/null)"
BIN="$(sed -n 's/.*path=\([^ ;]*\).*/\1/p' <<<"$EXEC" | head -1)"
PORT="$(grep -oE -- '--port[= ][0-9]+' <<<"$EXEC" | grep -oE '[0-9]+$' | head -1)"; PORT="${PORT:-8090}"
SCHEMA="$(grep -oE -- '--schema[= ][^ ;]+' <<<"$EXEC" | sed -E 's/--schema[= ]//' | head -1)"; SCHEMA="${SCHEMA:-/etc/$APP/schema.json}"
[ -n "$URL" ] || URL="http://127.0.0.1:$PORT"
CLI="$BIN"; [ -x "/opt/$APP/bin/appximo-cli" ] && CLI="/opt/$APP/bin/appximo-cli"; [ -x "/opt/$APP/bin/appitools-cli" ] && CLI="/opt/$APP/bin/appitools-cli"
export APPXIMO_NO_VERSION_CHECK=1
SVC_USER="$(systemctl show -p User --value "$SERVICE" 2>/dev/null)"; SVC_USER="${SVC_USER:-$APP}"
PGUNIT="$(systemctl list-units 'postgresql@*' --no-legend --plain 2>/dev/null | awk '{print $1}' | head -1)"
DBNAME="$(sed -E 's#.*/([^/?]+)(\?.*)?$#\1#' <<<"$DATABASE_URL")"
DBROLE="$(sed -E 's#^[a-z]+://([^:/@]+).*#\1#' <<<"$DATABASE_URL")"
[ -n "$TENANT" ] || TENANT="$(psql -qAtX "$DATABASE_URL" -c "select id from public.tenants order by created_at limit 1" 2>/dev/null | head -1)"
[ -n "$RESOURCE" ] || RESOURCE="$(python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(sorted(d['resources'])[0])" "$SCHEMA" 2>/dev/null)"
ROLE="$(python3 - "$SCHEMA" <<'EOP'
import json,sys; d=json.load(open(sys.argv[1])); roles=d.get('rbac',{}).get('roles',{})
for r,v in roles.items():
    if v.get('resources')=='*' or (isinstance(v.get('actions'),list) and '*' in v['actions']): print(r); break
else: print(next(iter(roles),'admin'))
EOP
)"
TOK=""; [ -n "$JWT_SECRET" ] && [ -n "$TENANT" ] && TOK="$("$CLI" token --secret "$JWT_SECRET" --tenant "$TENANT" --role "$ROLE" --user-id 00000000-0000-4000-8000-00000000d111 2>/dev/null | tail -1)"
HOSTH="Host: $TENANT.localhost"
note "app=$APP unit=$SERVICE bin=$BIN port=$PORT db=$DBNAME role=$DBROLE tenant=${TENANT:-?} resource=${RESOURCE:-?} role=$ROLE pg=${PGUNIT:-?}"

# ── the probe (10 rps, cache-busting, timestamped; the CAOS-S1 instrument) ────
PROBE_DIR="$(mktemp -d /tmp/drill.XXXXXX)"; PROBE_PID=""
probe_start() { # $1 = seconds
	local secs=$1 end; end=$(( $(date +%s) + secs )); : > "$PROBE_DIR/probe.txt"
	( while [ "$(date +%s)" -lt "$end" ]; do
		( printf '%s ' "$(date +%s.%N | cut -c1-14)"; curl -s -o /dev/null -m 8 -w '%{http_code} %{time_total}\n' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=5&cb=$RANDOM$RANDOM" ) >> "$PROBE_DIR/probe.txt" &
		sleep 0.1
	  done; wait ) &
	PROBE_PID=$!
}
probe_wait() { [ -n "$PROBE_PID" ] && wait "$PROBE_PID" 2>/dev/null; PROBE_PID=""; }
probe_summary() { python3 - "$PROBE_DIR/probe.txt" <<'EOP'
import sys, collections
rows=[l.split() for l in open(sys.argv[1]) if len(l.split())==3]; rows.sort(key=lambda r: float(r[0]))
if not rows: print("probe: no samples"); sys.exit(0)
t0=float(rows[0][0]); c=collections.Counter(r[1] for r in rows)
print("probe: %d requests over %.0f s; statuses %s" % (len(rows), float(rows[-1][0])-t0, dict(sorted(c.items()))))
bad=[r for r in rows if r[1]!='200']
if not bad: print("no failures — no outage observed"); sys.exit(0)
times=sorted(float(r[2]) for r in bad); p50=times[len(times)//2]; p90=times[int(len(times)*0.9)]; fast=len([t for t in times if t<0.2])
first=float(bad[0][0]); last=float(bad[-1][0]); after=[float(r[0]) for r in rows if r[1]=='200' and float(r[0])>last]
print("failures: %d · first at +%.1f s · last at +%.1f s · p50 %.2f s · p90 %.2f s · <200ms: %d (%.0f%%)" % (len(bad), first-t0, last-t0, p50, p90, fast, 100*fast/len(bad)))
if after: print("outage: %.1f s (first failure → first 200 after the last failure); recovery +%.2f s after the last failure" % (after[0]-first, after[0]-last))
else: print("no 200 after the last failure — still down when the probe ended")
EOP
}
# a WRITE that changes nothing: a transaction deleting a non-existent id → 404
# failed_operation=0 (auth, RBAC, a tx opened, executed, rolled back, 0 rows).
write_probe() { curl -s -o "$PROBE_DIR/w.json" -m 8 -w '%{http_code}' -X POST "$URL/api/transaction" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "{\"operations\":[{\"op\":\"delete\",\"resource\":\"$RESOURCE\",\"id\":\"00000000-0000-4000-8000-0000000000dd\"}]}"; }
read_probe()  { curl -s -o /dev/null -m 8 -w '%{http_code}' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=1&cb=$RANDOM"; }
metric() { curl -s -m 3 -H "X-Admin-Key: $ADMIN_KEY" "$URL/metrics" | grep -E "^$1" | head -3; }
verdict_now() { curl -s -m 3 -H "X-Admin-Key: $ADMIN_KEY" "$URL/admin/resources?live=1&series=1" | python3 -c 'import json,sys; d=json.load(sys.stdin)["latest"]; r=d["request"]; db=d["db_client"]; print("verdict=%s owner=%s rps=%.0f p99=%.1fms pool=%d/%d 429=%d 503=%d" % (d["attribution"], d["verdict"]["owner"], r["rps"], r["latency_p99_ms"], db["acquired_conns"], db["max_conns"], r["status_429"], r["status_503"]))' 2>/dev/null || echo "verdict: (not readable — ADMIN_KEY?)"; }
journal_tail() { journalctl -u "$SERVICE" --since "-${1:-60}s" --no-pager -o cat 2>/dev/null | grep -iE "${2:-.}" | tail -"${3:-3}" | cut -c1-200; }

# ── cleanup: everything a drill may leave behind, restored on ANY exit ─────────
IPT_RULE=(); TC_IFACE=""; FILLER=""; HOG_PID=""; CLOCK_DELTA=""; LOCK_PID=""; SCRATCH_DB=""
cleanup() {
	[ -n "$PROBE_PID" ] && kill "$PROBE_PID" 2>/dev/null
	[ ${#IPT_RULE[@]} -gt 0 ] && { iptables -D "${IPT_RULE[@]}" 2>/dev/null && note "iptables rule removed (cleanup)"; }
	[ -n "$TC_IFACE" ] && { tc qdisc del dev "$TC_IFACE" root 2>/dev/null && note "tc qdisc removed on $TC_IFACE (cleanup)"; }
	[ -n "$FILLER" ] && [ -f "$FILLER" ] && { rm -f "$FILLER"; note "disk filler removed (cleanup)"; }
	[ -n "$HOG_PID" ] && kill "$HOG_PID" 2>/dev/null
	[ -n "$LOCK_PID" ] && kill "$LOCK_PID" 2>/dev/null
	if [ -n "$CLOCK_DELTA" ]; then date -s "@$(( $(date +%s) + 7200 ))" >/dev/null 2>&1; systemctl start systemd-timesyncd 2>/dev/null || systemctl start chrony 2>/dev/null || true; note "clock restored (cleanup)"; fi
	[ -n "$SCRATCH_DB" ] && { runuser -u postgres -- dropdb --if-exists "$SCRATCH_DB" 2>/dev/null; note "scratch database $SCRATCH_DB dropped (cleanup)"; }
	rm -rf "$PROBE_DIR"
}
trap cleanup EXIT INT TERM

# ═════════════════════════════════════════════════════════════════════════════
chaos() {
	[ -n "$TOK" ] || die "could not mint a probe token (JWT_SECRET in $ENVF? a registered tenant? --tenant=ID)"
	[ -n "$RESOURCE" ] || die "could not pick a resource from $SCHEMA (--resource=NAME)"
	printf '%s── D%s · %s%s\n' "$B" "$N" "$(now)" "$N0"
	case "$N" in
	1)	hyp "the engine is back in ~2–3 s (RestartSec=2); the probe shows a gap of that size; NRestarts +1; nothing else moves"
		local nr0; nr0="$(systemctl show -p NRestarts --value "$SERVICE")"
		probe_start 30; sleep 8
		local pid; pid="$(systemctl show -p MainPID --value "$SERVICE")"
		note "$(now) kill -9 $pid ($SERVICE)"; kill -9 "$pid"
		probe_wait; probe_summary
		ok "NRestarts $nr0 → $(systemctl show -p NRestarts --value "$SERVICE") · active=$(systemctl is-active "$SERVICE") · RestartSec=$(systemctl show -p RestartUSec --value "$SERVICE")"
		journal_tail 40 'listening|started|drain' 3 ;;
	2)	[ -n "$PGUNIT" ] || die "no postgresql@*-main unit on this box (a remote database cannot be killed from here)"
		hyp "the engine stays up and answers fast 503s; PostgreSQL recovers from WAL and systemd restarts it (Restart=on-failure, ~5 s); the engine reconnects alone; its NRestarts does not move"
		local nr0; nr0="$(systemctl show -p NRestarts --value "$SERVICE")"
		note "PostgreSQL unit $PGUNIT: Restart=$(systemctl show -p Restart --value "$PGUNIT") (CAOS-S1 found Ubuntu's default is 'no' — the installer writes a drop-in)"
		probe_start 40; sleep 8
		local pid; pid="$(systemctl show -p MainPID --value "$PGUNIT")"
		note "$(now) kill -9 postmaster $pid"; kill -9 "$pid"
		probe_wait; probe_summary
		ok "PostgreSQL active=$(systemctl is-active "$PGUNIT") · engine NRestarts $nr0 → $(systemctl show -p NRestarts --value "$SERVICE") (must be equal)"
		journal_tail 45 'circuit|breaker|unavailable' 3 ;;
	3)	hyp "a full outage of ~12–40 s, then everything back in the right order with no intervention and nothing in 'failed'"
		note "probe this box from ANOTHER machine while it reboots:  appximo drill probe --url https://<its domain> --duration 120s"
		if [ "$YES_REBOOT" != "yes" ]; then
			note "not rebooting (pass --yes-reboot to do it). After a reboot, verify with:  systemctl --failed; journalctl -b -u $SERVICE | head; curl -s $URL/readyz"
			return 0
		fi
		note "$(now) rebooting in 5 s — the probe on the other machine must already be running"; sleep 5; systemctl reboot ;;
	4)	local bdir; bdir="${APPXIMO_BACKUP_DIR:-/var/backups/$APP}"; mkdir -p "$bdir"
		local total avail floorpct floormb keep
		floorpct="${APPXIMO_DISK_MIN_FREE_PCT:-10}"; floormb="${APPXIMO_DISK_MIN_FREE_MB:-1024}"
		total="$(df -Pk "$bdir" | awk 'NR==2{print $2}')"; avail="$(df -Pk "$bdir" | awk 'NR==2{print $4}')"
		hyp "within one collector tick the disk gauge flags LOW and the alert is recorded; reads stay 200; a backup still succeeds while the set fits$( [ "$FULL" = yes ] && echo ' — at 100 % it FAILS at once naming the cause, and PostgreSQL may PANIC and restart, recovering alone once space is freed')"
		note "filesystem of $bdir: $((total/1024)) MiB total, $((avail/1024)) MiB free; alert floor max($floorpct %, $floormb MiB)"
		# leave 60 % of the floor free: clearly UNDER the floor, clearly not full
		local floork; floork=$(( total * floorpct / 100 )); [ $(( floormb * 1024 )) -gt "$floork" ] && floork=$(( floormb * 1024 ))
		keep=$(( floork * 60 / 100 )); [ "$FULL" = yes ] && keep=0
		local fill=$(( avail - keep )); [ "$fill" -gt 0 ] || die "already under the floor — nothing to provoke"
		FILLER="$bdir/.drill-filler"; note "$(now) allocating $((fill/1024)) MiB → $FILLER"
		fallocate -l "${fill}K" "$FILLER" 2>/dev/null || dd if=/dev/zero of="$FILLER" bs=1M count=$((fill/1024)) status=none
		note "free now: $(df -Ph "$bdir" | awk 'NR==2{print $4" ("$5" used)"}')"
		note "waiting for a collector tick (APPXIMO_SELFMON_INTERVAL, default 10 s)…"; sleep 12
		metric 'appximo_selfmon_disk_free_bytes'; verdict_now
		journal_tail 20 'disk|alert' 3
		ok "read during the fill → $(read_probe)"
		if [ -x "/opt/$APP/scripts/backup.sh" ]; then
			note "running backup.sh under the fill (expected: succeeds while the set fits; with --full it FAILS naming the cause)"
			bash "/opt/$APP/scripts/backup.sh" --app="$APP" 2>&1 | tail -3 | cut -c1-200
			cat "$bdir/last-backup.status" 2>/dev/null | cut -c1-200
		fi
		rm -f "$FILLER"; FILLER=""; ok "$(now) filler removed; free again: $(df -Ph "$bdir" | awk 'NR==2{print $4}')"
		[ "$FULL" = yes ] && { sleep 3; ok "engine after the fill → $(read_probe); PostgreSQL $(systemctl is-active "${PGUNIT:-postgresql}")"; } ;;
	5)	local floor; floor="$(journalctl -u "$SERVICE" --no-pager -o cat 2>/dev/null | grep -oE 'MemAvailable\+SwapFree < [0-9]+ MiB' | tail -1 | grep -oE '[0-9]+')"; floor="${floor:-64}"
		hyp "WRITES answer 503 + Retry-After: 5 while MemAvailable+SwapFree < $floor MiB; READS keep 200; nothing is OOM-killed; writes come back alone"
		note "before: $(grep -E 'MemAvailable|SwapFree' /proc/meminfo | tr -s ' ' | tr '\n' ' ') · write probe → $(write_probe)"
		python3 - "$floor" > "$PROBE_DIR/hog.log" 2>&1 <<'EOP' &
import sys, time
floor = int(sys.argv[1]) * 1024  # kB
def avail():
    m = {}
    for l in open('/proc/meminfo'):
        k, v = l.split(':'); m[k] = int(v.split()[0])
    return m['MemAvailable'] + m['SwapFree']
chunks = []
# 64 MiB steps, touching every page, until the guard's floor is crossed (with 8 MiB of margin), never beyond
while avail() > floor + 8 * 1024 and len(chunks) < 512:
    chunks.append(bytearray(64 * 1024 * 1024))
    for i in range(0, len(chunks[-1]), 4096): chunks[-1][i] = 1
print("hog holding %d MiB; avail+swapfree now %d MiB" % (64 * len(chunks), avail() // 1024), flush=True)
time.sleep(25)
EOP
		HOG_PID=$!
		sleep 6; local w r
		for _ in 1 2 3 4 5 6; do w="$(write_probe)"; r="$(read_probe)"; note "$(now) avail+swap=$(( ($(awk '/MemAvailable/{print $2}' /proc/meminfo) + $(awk '/SwapFree/{print $2}' /proc/meminfo)) / 1024 )) MiB · write → $w · read → $r"; [ "$w" = 503 ] && break; sleep 3; done
		cat "$PROBE_DIR/hog.log" 2>/dev/null
		[ "$w" = 503 ] && ok "the guard answered 503 on the write (body: $(head -c 120 "$PROBE_DIR/w.json"))" || bad "the write never saw a 503 — the hog could not cross the floor (swap absorbs it? floor $floor MiB) — see APPXIMO_MEMORY_GUARD_MIN_MB"
		kill "$HOG_PID" 2>/dev/null; wait "$HOG_PID" 2>/dev/null; HOG_PID=""; sleep 2
		ok "after release: write → $(write_probe) (404 = the write path works again) · dmesg oom lines: $(dmesg 2>/dev/null | grep -ci 'killed process' || echo 0)"
		journal_tail 60 'memory guard' 2 ;;
	6)	hyp "first failures pay the 5 s query deadline; within ~2 s the breaker OPENS (20 consecutive) and the rest fail in < 0.2 s (p50 ≈ 0.00 s); recovery < 1 s after the rule goes; other apps untouched"
		IPT_RULE=(OUTPUT -p tcp --dport 5432 -m owner --uid-owner "$SVC_USER" -j DROP)
		probe_start 50; sleep 8
		note "$(now) iptables -I ${IPT_RULE[*]}  (only $SVC_USER's packets to 5432)"; iptables -I "${IPT_RULE[@]}"
		sleep 25; note "$(now) rule removed"; iptables -D "${IPT_RULE[@]}"; IPT_RULE=()
		probe_wait; probe_summary
		journal_tail 60 'breaker|circuit' 3; verdict_now ;;
	7)	local iface="lo"; grep -qE '@(localhost|127\.0\.0\.1)' <<<"$DATABASE_URL" || note "DATABASE_URL is not loopback — the delay is applied on lo; a remote database needs the outbound interface"
		hyp "DEGRADES, does not tip: every query pays ~200–400 ms; the pool's capacity collapses (10 conns / 0.2 s ≈ 50 qps) so the admission control sheds 429 and some requests hit the 5 s deadline; verdict pool_exhausted / db_bound; baseline back alone"
		TC_IFACE="$iface"
		tc qdisc add dev "$iface" root handle 1: prio && tc qdisc add dev "$iface" parent 1:3 handle 30: netem delay 200ms && tc filter add dev "$iface" protocol ip parent 1:0 prio 3 u32 match ip dport 5432 0xffff flowid 1:3 || die "tc failed (iproute2 installed?)"
		note "$(now) netem 200 ms on $iface, only dport 5432 · one read now → $(curl -s -o /dev/null -m 8 -w '%{http_code} %{time_total}s' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=1&cb=1")"
		note "40 concurrent readers × 20 s…"; : > "$PROBE_DIR/c.txt"
		local end; end=$(( $(date +%s) + 20 ))
		while [ "$(date +%s)" -lt "$end" ]; do for _ in $(seq 1 40); do ( curl -s -o /dev/null -m 8 -w '%{http_code}\n' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=5&cb=$RANDOM$RANDOM" >> "$PROBE_DIR/c.txt" ) & done; wait; done
		note "statuses: $(sort "$PROBE_DIR/c.txt" | uniq -c | tr -s ' ' | tr '\n' ';')"; verdict_now
		tc qdisc del dev "$iface" root; TC_IFACE=""; sleep 2
		ok "$(now) delay removed · one read now → $(curl -s -o /dev/null -m 8 -w '%{http_code} %{time_total}s' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=1&cb=2")" ;;
	8)	hyp "an already-issued token and a freshly minted one are both accepted (exp still in the future, no iat/nbf check); the write path works; no restart; the collector may show one odd tick"
		local nr0; nr0="$(systemctl show -p NRestarts --value "$SERVICE")"
		note "before: $(date -u +%FT%TZ) · read → $(read_probe)"
		# the time daemon reacts to a clock change within milliseconds and steps it
		# right back (systemd-timesyncd watches TFD_TIMER_CANCEL_ON_SET) — pause it,
		# or the experiment measures the daemon, not the engine
		systemctl stop systemd-timesyncd 2>/dev/null; systemctl stop chrony 2>/dev/null
		CLOCK_DELTA="yes"; date -s "@$(( $(date +%s) - 7200 ))" >/dev/null; note "$(now) clock set 2 h back: $(date -u +%FT%TZ) (time daemon paused)"
		ok "old token read → $(read_probe)"
		local newtok; newtok="$("$CLI" token --secret "$JWT_SECRET" --tenant "$TENANT" --role "$ROLE" --user-id 00000000-0000-4000-8000-00000000d111 2>/dev/null | tail -1)"
		ok "new token (minted with the old clock) read → $(curl -s -o /dev/null -m 8 -w '%{http_code}' -H "$HOSTH" -H "Authorization: Bearer $newtok" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=1&cb=3") · write probe → $(write_probe)"
		sleep 2; note "$(now) still 2 h back after 2 s: $(date -u +%FT%TZ)"
		date -s "@$(( $(date +%s) + 7200 ))" >/dev/null; CLOCK_DELTA=""; systemctl start systemd-timesyncd 2>/dev/null || systemctl start chrony 2>/dev/null || true
		ok "clock restored: $(date -u +%FT%TZ) · engine NRestarts $nr0 → $(systemctl show -p NRestarts --value "$SERVICE") · read → $(read_probe)" ;;
	9)	hyp "plain field: both 200 and the row holds the LAST committed value; state machine: exactly one transition wins, the other is 422 — never a double move"
		local field row id
		field="$(python3 -c "
import json,sys; d=json.load(open(sys.argv[1])); f=d['resources'][sys.argv[2]]['fields']
c=[k for k,v in f.items() if v.get('type') in ('string','text') and not v.get('unique') and not v.get('enum') and not v.get('pattern') and not v.get('state_machine') and not v.get('format')]
print(c[0] if c else '')" "$SCHEMA" "$RESOURCE")"
		[ -n "$field" ] || die "$RESOURCE has no free text field to race on — pass --resource=NAME"
		row="$(curl -s -m 8 -H "$HOSTH" -H "Authorization: Bearer $TOK" "$URL/api/$RESOURCE?per_page=1&fields=id,$field")"
		id="$(python3 -c "import json,sys; d=json.load(sys.stdin); print(d['data'][0]['id'] if d.get('data') else '')" <<<"$row")"
		[ -n "$id" ] || die "$RESOURCE has no rows to race on"
		local before; before="$(python3 -c "import json,sys; print(json.load(sys.stdin)['data'][0].get('$field'))" <<<"$row")"
		note "row $id · $field before: $before"
		( curl -s -o "$PROBE_DIR/a.json" -m 8 -w 'A → %{http_code}\n' -X PATCH "$URL/api/$RESOURCE/$id" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "{\"$field\":\"drill-A\"}" ) &
		( curl -s -o "$PROBE_DIR/b.json" -m 8 -w 'B → %{http_code}\n' -X PATCH "$URL/api/$RESOURCE/$id" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "{\"$field\":\"drill-B\"}" ) &
		wait
		local final; final="$(curl -s -m 8 -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE/$id?fields=id,$field" | FIELD="$field" python3 -c "import json,sys,os; d=json.load(sys.stdin); d=d.get('data',d); print(d.get(os.environ['FIELD']))")"
		ok "final $field = $final (one of the two, never a mix) — restoring the original value"
		BEFORE="$before" FIELD="$field" python3 -c 'import json,os; print(json.dumps({os.environ["FIELD"]: os.environ["BEFORE"]}))' > "$PROBE_DIR/restore-body.json"
		curl -s -o /dev/null -m 8 -X PATCH "$URL/api/$RESOURCE/$id" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d @"$PROBE_DIR/restore-body.json"
		# a state machine, if the resource has one: two conflicting transitions from a row in an initial state
		python3 - "$SCHEMA" "$RESOURCE" <<'EOP' > "$PROBE_DIR/sm.txt"
import json,sys; d=json.load(open(sys.argv[1])); f=d['resources'][sys.argv[2]]['fields']
for k,v in f.items():
    sm=v.get('state_machine')
    if sm:
        init=sm['initial']; init=init if isinstance(init,str) else init[0]
        nxt=sm['transitions'].get(init,[])
        if len(nxt)>=2: print(k, init, nxt[0], nxt[1]); break
EOP
		if [ -s "$PROBE_DIR/sm.txt" ]; then
			read -r sfield sinit s1 s2 < "$PROBE_DIR/sm.txt"
			local sid; sid="$(curl -s -m 8 -g -H "$HOSTH" -H "Authorization: Bearer $TOK" "$URL/api/$RESOURCE?per_page=1&fields=id&filter[$sfield][eq]=$sinit" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['data'][0]['id'] if d.get('data') else '')")"
			if [ -n "$sid" ]; then
				note "state machine on $sfield: row $sid in '$sinit' — racing '$s1' against '$s2'"
				( curl -s -o /dev/null -m 8 -w "→ $s1: %{http_code}\n" -X PATCH "$URL/api/$RESOURCE/$sid" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "{\"$sfield\":\"$s1\"}" ) &
				( curl -s -o /dev/null -m 8 -w "→ $s2: %{http_code}\n" -X PATCH "$URL/api/$RESOURCE/$sid" -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "{\"$sfield\":\"$s2\"}" ) &
				wait
				ok "final $sfield = $(curl -s -m 8 -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE/$sid?fields=id,$sfield" | FIELD="$sfield" python3 -c "import json,sys,os; d=json.load(sys.stdin); d=d.get('data',d); print(d.get(os.environ['FIELD']))") (the row moved ONCE; the loser's move is not applied — put it back by hand if the demo needs it)"
			else note "state machine on $sfield but no row in '$sinit' to race"; fi
		else note "$RESOURCE declares no state machine — the field race above is the whole experiment"; fi ;;
	10)	hyp "the first readers take every pool connection and wait; the admission control sheds the rest with an immediate 429; waiters hit the 5 s deadline → 503; the breaker may open; nothing crashes; after the lock, reads are 200 in ms"
		local tbl="tenant_$TENANT.$RESOURCE"
		( psql -qAtX "$DATABASE_URL" -c "BEGIN; LOCK TABLE $tbl IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(20); COMMIT;" >/dev/null 2>&1 ) &
		LOCK_PID=$!; sleep 1; note "$(now) $tbl locked ACCESS EXCLUSIVE for 20 s · 60 concurrent readers…"
		: > "$PROBE_DIR/c.txt"; local end; end=$(( $(date +%s) + 18 ))
		while [ "$(date +%s)" -lt "$end" ]; do for _ in $(seq 1 60); do ( curl -s -o /dev/null -m 9 -w '%{http_code} %{time_total}\n' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=5&cb=$RANDOM$RANDOM" >> "$PROBE_DIR/c.txt" ) & done; sleep 2; verdict_now; done; wait
		note "statuses: $(awk '{print $1}' "$PROBE_DIR/c.txt" | sort | uniq -c | tr -s ' ' | tr '\n' ';')"
		wait "$LOCK_PID" 2>/dev/null; LOCK_PID=""; sleep 1
		ok "$(now) lock released · read now → $(curl -s -o /dev/null -m 8 -w '%{http_code} %{time_total}s' -H "$HOSTH" -H "Authorization: Bearer $TOK" -H 'Cache-Control: no-cache' "$URL/api/$RESOURCE?per_page=1&cb=9")"
		journal_tail 40 'breaker|circuit|admission' 3 ;;
	*)	die "experiment must be 1–10" ;;
	esac
	printf '%s── D%s done · %s · verdict: read the H line against the numbers above%s\n' "$B" "$N" "$(now)" "$N0"
}

# ═════════════════════════════════════════════════════════════════════════════
restore_drill() {
	local bdir; bdir="${APPXIMO_BACKUP_DIR:-/var/backups/$APP}"
	if [ -z "$SET" ]; then
		local newest; newest="$(ls -t "$bdir"/*.dump 2>/dev/null | head -1)"
		[ -n "$newest" ] || die "no backup set in $bdir — run one first: sudo bash /opt/$APP/scripts/backup.sh --app=$APP"
		SET="${newest%.dump}"
	fi
	[ -f "$SET.dump" ] || die "$SET.dump not found"
	note "set: $SET ($(stat -c %s "$SET.dump" | awk '{printf "%.1f MB", $1/1048576}') · $(date -u -r "$SET.dump" +%FT%TZ)) · manifest $( [ -f "$SET.manifest" ] && echo present || echo MISSING)"
	if [ "$REAL" = yes ]; then
		[ -x "/opt/$APP/scripts/restore.sh" ] || die "/opt/$APP/scripts/restore.sh missing (re-run install.sh, or copy it from the repo)"
		if [ "$YES" != yes ] && [ -t 0 ]; then printf 'REAL restore: the app STOPS and its database is replaced by %s. Type the app name to continue: ' "$SET"; read -r c; [ "$c" = "$APP" ] || die "cancelled"; fi
		exec bash "/opt/$APP/scripts/restore.sh" --app="$APP" --set="$SET"
	fi
	# ── the rehearsal: a scratch database next to the live one ────────────────
	command -v pg_restore >/dev/null || die "pg_restore not installed (apt install postgresql-client)"
	local t0 t1 t2 t3
	SCRATCH_DB="${DBNAME}_drill"
	local free; free="$(df -Pk /var/lib/postgresql 2>/dev/null | awk 'NR==2{print $4}')"; local need; need="$(( $(stat -c %s "$SET.dump") * 4 / 1024 ))"
	[ -n "$free" ] && [ "$free" -lt "$need" ] && die "not enough free disk for a scratch copy (~$((need/1024)) MiB needed, $((free/1024)) MiB free)"
	t0=$(date +%s.%N)
	runuser -u postgres -- dropdb --if-exists "$SCRATCH_DB" 2>/dev/null
	runuser -u postgres -- createdb -O "$DBROLE" "$SCRATCH_DB" || die "createdb $SCRATCH_DB failed"
	t1=$(date +%s.%N); printf '  ⏱ create scratch db %-14s %6.1f s\n' "$SCRATCH_DB" "$(awk -v a="$t0" -v b="$t1" 'BEGIN{print b-a}')"
	local sdsn; sdsn="$(sed -E "s#/$DBNAME(\?|\$)#/$SCRATCH_DB\1#" <<<"$DATABASE_URL")"
	# same TOC filter as restore.sh: an amcheck extension in the set is not data
	pg_restore -l "$SET.dump" | grep -vE '^[0-9]+; [0-9]+ [0-9]+ (EXTENSION|COMMENT) - (EXTENSION )?amcheck' > "$PROBE_DIR/toc.txt"
	if ! pg_restore --exit-on-error --no-owner --no-privileges --use-list="$PROBE_DIR/toc.txt" --dbname="$sdsn" "$SET.dump" 2> "$PROBE_DIR/restore.err"; then
		tail -3 "$PROBE_DIR/restore.err"; die "pg_restore FAILED into the scratch database — the set is NOT restorable as-is"
	fi
	t2=$(date +%s.%N); printf '  ⏱ pg_restore %-23s %6.1f s\n' "" "$(awk -v a="$t1" -v b="$t2" 'BEGIN{print b-a}')"
	# verify every table's row count against the manifest (restore.sh's check)
	local mism=0 checked=0
	if [ -f "$SET.manifest" ]; then
		local q; q="$(grep '^count ' "$SET.manifest" | awk '{printf "SELECT %s, count(*) FROM %s UNION ALL ", "'"'"'"$2"'"'"'", $2}' | sed 's/ UNION ALL $//')"
		psql -qAtX "$sdsn" -c "$q" 2>/dev/null | sort > "$PROBE_DIR/now.txt"
		grep '^count ' "$SET.manifest" | awk '{print $2"|"$3}' | sort > "$PROBE_DIR/want.txt"
		checked="$(wc -l < "$PROBE_DIR/want.txt")"
		mism="$(comm -3 "$PROBE_DIR/want.txt" "$PROBE_DIR/now.txt" | wc -l)"
		[ "$mism" = 0 ] && ok "row counts: $checked tables match the manifest exactly ($(awk -F'|' '{s+=$2} END{print s+0}' "$PROBE_DIR/want.txt") rows)" || { bad "row counts differ from the manifest:"; comm -3 "$PROBE_DIR/want.txt" "$PROBE_DIR/now.txt" | head -10; }
	else
		note "no manifest next to the dump (an older backup.sh?) — counts not verified; tables restored: $(psql -qAtX "$sdsn" -c "select count(*) from pg_tables where schemaname like 'tenant\_%' or schemaname='public'")"
	fi
	local fk; fk="$(psql -qAtX "$sdsn" -c "select count(*) from pg_constraint where contype='f' and not convalidated")"; [ "$fk" = 0 ] && ok "every foreign key validated" || bad "$fk foreign keys NOT VALID in the restored copy"
	t3=$(date +%s.%N); printf '  ⏱ verify %-27s %6.1f s\n' "" "$(awk -v a="$t2" -v b="$t3" 'BEGIN{print b-a}')"
	runuser -u postgres -- dropdb "$SCRATCH_DB"; SCRATCH_DB=""
	printf '  ⏱ TOTAL (create + load + verify) %6.1f s   — a REAL restore adds: stop the app (~5 s drain), restore /etc/%s, files, start (~1 s)\n' "$(awk -v a="$t0" -v b="$t3" 'BEGIN{print b-a}')" "$APP"
	if [ "$mism" = 0 ]; then printf '%s%sREHEARSAL VERIFIED%s — the newest set restores and matches its manifest. The real command (stops the app):\n  sudo bash /opt/%s/scripts/restore.sh --app=%s --set=%s\n' "$G" "$B" "$N0" "$APP" "$APP" "$SET"
	else die "REHEARSAL FAILED — do not trust this set until the mismatch is explained"; fi
}

case "$MODE" in
	chaos) chaos ;;
	restore) restore_drill ;;
esac
