#!/usr/bin/env bash
#
# chaos.sh — break the production stack on purpose and MEASURE what a user loses.
#
# RUNS ON THE LOAD GENERATOR (or anywhere that can reach the server and SSH into
# it). That is deliberate: the probe has to survive the server rebooting, and
# "what does the user see" is only answerable from outside the box.
#
#   bash chaos.sh --target=https://api.example.com --server-ssh=root@1.2.3.4 --case=engine-kill
#   bash chaos.sh --target=... --server-ssh=... --case=all
#
# Every case follows the same shape, so the numbers are comparable:
#
#   1. start a continuous probe against the PUBLIC url at a fixed rate
#   2. let it settle (baseline)
#   3. inject the fault at a recorded timestamp
#   4. keep probing through the failure and the recovery
#   5. report: time-to-first-error, outage duration, requests lost, which status
#      codes the user actually saw, and time-to-full-recovery
#
# A chaos test that does not measure the hole it punched is theatre. The probe is
# the point.
#
# Cases:
#   engine-kill      SIGKILL the engine (does systemd bring it back? how fast?)
#   caddy-kill       SIGKILL Caddy (the TLS front door disappears)
#   postgres-stop    stop PostgreSQL, wait, start it (does the engine survive and reconnect?)
#   pool-exhaust     drive far more concurrency than the pgx pool allows (graceful or collapse?)
#   memory-pressure  large concurrent result sets, pushing the engine toward GOMEMLIMIT
#   deploy-update    swap the binary under live traffic (the real update path)
#   reboot           reboot the whole box (do all three services come back alone?)
#   all              every case above, in that order
#
# Flags:
#   --target=URL       public url under test                       [required]
#   --server-ssh=HOST  ssh destination for the fault commands (e.g. root@1.2.3.4)
#   --case=NAME        which case (default: all)
#   --rate=N           probe requests/sec                          [default 20]
#   --token=JWT        JWT (probe hits an authenticated endpoint when given)
#   --origin-ip=IP     bypass a CDN in front of the domain
#   --settle=SEC       baseline seconds before the fault           [default 8]
#   --observe=SEC      seconds to keep probing after the fault     [default 45]
#   --out-dir=DIR      results directory
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

CASE="all"; RATE=20; SETTLE=8; OBSERVE=45; SERVER_SSH=""; ORIGIN_IP=""
for arg in "$@"; do
	case "$arg" in
		--target=*)     TARGET="${arg#*=}" ;;
		--server-ssh=*) SERVER_SSH="${arg#*=}" ;;
		--case=*)       CASE="${arg#*=}" ;;
		--rate=*)       RATE="${arg#*=}" ;;
		--token=*)      TOKEN="${arg#*=}" ;;
		--token-file=*) TOKEN="$(cat "${arg#*=}")" ;;
		--origin-ip=*)  ORIGIN_IP="${arg#*=}" ;;
		--settle=*)     SETTLE="${arg#*=}" ;;
		--observe=*)    OBSERVE="${arg#*=}" ;;
		--out-dir=*)    OUT_DIR="${arg#*=}" ;;
		--service=*)    SERVICE="${arg#*=}" ;;
		--port=*)       ENGINE_PORT="${arg#*=}" ;;
		--help|-h)      sed -n '3,52p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done

[ -n "$TARGET" ] || die "--target is required"
need python3 curl
OUT_DIR="$(ensure_out_dir)"
mkdir -p "$OUT_DIR/chaos"

# remote: run a command on the server (over ssh, or locally if no --server-ssh).
remote() {
	if [ -n "$SERVER_SSH" ]; then
		# BatchMode so a missing key fails fast instead of hanging on a prompt.
		ssh -o BatchMode=yes -o ConnectTimeout=15 ${VP_SSH_OPTS:-} "$SERVER_SSH" "$@"
	else
		bash -c "$*"
	fi
}
remote_ok() { remote "$@" >/dev/null 2>&1; }

if [ -z "$SERVER_SSH" ] && ! on_server; then
	die "the fault commands need the server: pass --server-ssh=user@host (or run this ON the server, though then --case=reboot cannot be measured)"
fi

# ── The probe ────────────────────────────────────────────────────────────────
# A fixed-rate prober written in the standard library. Deliberately NOT k6: it
# must keep probing (and keep RECORDING) while the target refuses connections,
# resets, and reboots — and it must never die because of that. Every outcome,
# including "connection refused", is a data point.
write_probe() {
	cat > "$OUT_DIR/chaos/probe.py" <<'PY'
import json, os, socket, ssl, sys, threading, time
from urllib.parse import urlparse
import http.client

target   = sys.argv[1]
out_path = sys.argv[2]
rate     = float(sys.argv[3])
duration = float(sys.argv[4])
token    = os.environ.get("PROBE_TOKEN", "")
origin   = os.environ.get("PROBE_ORIGIN_IP", "")
path     = os.environ.get("PROBE_PATH", "/healthz")

u = urlparse(target)
host = u.hostname
port = u.port or (443 if u.scheme == "https" else 80)
connect_host = origin or host

def one_request():
    """Return (status:int|str, latency_ms). Never raises."""
    t0 = time.time()
    conn = None
    try:
        if u.scheme == "https":
            # Dial the ORIGIN address but present the DOMAIN as SNI, or the
            # certificate will not validate. http.client derives SNI from its
            # `host` argument AND connects to it, so the two cannot differ —
            # hence the socket is built by hand and handed to the connection.
            ctx = ssl.create_default_context()
            raw = socket.create_connection((connect_host, port), timeout=5)
            sock = ctx.wrap_socket(raw, server_hostname=host)
            conn = http.client.HTTPSConnection(host, port, timeout=5, context=ctx)
            conn.sock = sock
        else:
            conn = http.client.HTTPConnection(connect_host, port, timeout=5)
        headers = {"Host": host}
        if token:
            headers["Authorization"] = "Bearer " + token
        req_path = path
        if path.startswith("/api/"):
            # Unique per request: defeat the response cache so the request really
            # reaches PostgreSQL and the outage is visible.
            sep = "&" if "?" in path else "?"
            req_path = "%s%s_cb=%d" % (path, sep, int(t0 * 1000))
        conn.request("GET", req_path, headers=headers)
        r = conn.getresponse()
        r.read()
        return r.status, (time.time() - t0) * 1000.0
    except socket.timeout:
        return "timeout", (time.time() - t0) * 1000.0
    except ConnectionRefusedError:
        return "refused", (time.time() - t0) * 1000.0
    except ssl.SSLError as e:
        return "tls_error", (time.time() - t0) * 1000.0
    except OSError as e:
        # Covers connection reset, no route to host, DNS failure while rebooting.
        return "neterr:%s" % type(e).__name__, (time.time() - t0) * 1000.0
    except Exception as e:
        return "err:%s" % type(e).__name__, (time.time() - t0) * 1000.0
    finally:
        try:
            if conn:
                conn.close()
        except Exception:
            pass

records, lock = [], threading.Lock()

def worker(scheduled_at):
    # Record the time the request was ACTUALLY issued, not the time it was
    # scheduled for. Under an outage every request blocks or spawns a thread, the
    # loop falls behind its schedule, and a request issued at +10 s could carry a
    # scheduled stamp of +6 s. That drift made failures appear to happen BEFORE
    # the fault that caused them, and the analysis then counted them as baseline.
    issued_at = time.time()
    st, ms = one_request()
    with lock:
        records.append({"t": issued_at, "t_scheduled": scheduled_at,
                        "status": st, "ms": round(ms, 3)})

start = time.time()
interval = 1.0 / rate
threads = []
i = 0
# Open model: fire on a fixed schedule regardless of how slow responses are, so a
# stalled server produces MISSING successes rather than a self-throttled probe
# that quietly stops asking.
while True:
    now = time.time()
    if now - start >= duration:
        break
    due = start + i * interval
    if due > now:
        time.sleep(min(due - now, 0.25))
        continue
    th = threading.Thread(target=worker, args=(due,), daemon=True)
    th.start()
    threads.append(th)
    i += 1
    # Bound the thread count so a long outage (every request hitting the 5s
    # timeout) cannot explode into thousands of live threads.
    if len(threads) > 400:
        threads = [t for t in threads if t.is_alive()]

for t in threads:
    t.join(timeout=6)

records.sort(key=lambda r: r["t"])
json.dump({"start": start, "rate": rate, "duration": duration,
           "path": path, "records": records}, open(out_path, "w"))
PY
}

# ── Analysis ─────────────────────────────────────────────────────────────────
analyse() {
	local probe_json="$1" fault_ts="$2" name="$3" notes="$4" out="$5"
	python3 - "$probe_json" "$fault_ts" "$name" "$notes" "$out" <<'PY'
import json, sys, collections
probe_path, fault_ts, name, notes, out = sys.argv[1:6]
d = json.load(open(probe_path))
fault_ts = float(fault_ts)
recs = d["records"]

def ok(r):
    return isinstance(r["status"], int) and 200 <= r["status"] < 400

before = [r for r in recs if r["t"] < fault_ts]
after   = [r for r in recs if r["t"] >= fault_ts]

# The user-visible hole: from the fault to the first success that is followed by
# a sustained run of successes. Requiring a RUN (not a single lucky 200) avoids
# calling a flapping service "recovered" on its first good response.
RECOVERY_RUN = 5
first_error_t = None
recovered_t = None
run = 0
for idx, r in enumerate(after):
    if not ok(r):
        run = 0
        if first_error_t is None:
            first_error_t = r["t"]
    else:
        run += 1
        if first_error_t is not None and recovered_t is None and run >= RECOVERY_RUN:
            # Recovery began at the FIRST success of this clean run, not the Nth.
            recovered_t = after[max(0, idx - (RECOVERY_RUN - 1))]["t"]

failed = [r for r in after if not ok(r)]
codes = collections.Counter(str(r["status"]) for r in after if not ok(r))

def pct(vals, q):
    if not vals: return None
    v = sorted(vals); k = int(len(v) * q)
    return round(v[min(k, len(v) - 1)], 2)

base_lat = [r["ms"] for r in before if ok(r)]
post_lat = [r["ms"] for r in after if ok(r)]

res = {
    "part": "C-chaos",
    "case": name,
    "notes": notes,
    "probe_rate_rps": d["rate"],
    "probe_path": d["path"],
    "requests_before_fault": len(before),
    "requests_after_fault": len(after),
    "requests_failed_after_fault": len(failed),
    "failure_codes": dict(codes),
    "time_to_first_error_s": round(first_error_t - fault_ts, 3) if first_error_t else None,
    "outage_s": round(recovered_t - first_error_t, 3) if (first_error_t and recovered_t) else None,
    "time_to_recovery_from_fault_s": round(recovered_t - fault_ts, 3) if recovered_t else None,
    "recovered": recovered_t is not None or not failed,
    "user_impact": (
        "none — no failed request" if not failed else
        "%d requests failed over %.1fs" % (
            len(failed),
            (recovered_t - first_error_t) if (first_error_t and recovered_t) else 0.0)
    ),
    "baseline_latency_ms": {"p50": pct(base_lat, .5), "p95": pct(base_lat, .95)},
    "post_recovery_latency_ms": {"p50": pct(post_lat, .5), "p95": pct(post_lat, .95)},
}
json.dump(res, open(out, "w"), indent=2)

print("    requests after fault : %d (%d failed)" % (res["requests_after_fault"], res["requests_failed_after_fault"]))
print("    codes the user saw   : %s" % (res["failure_codes"] or "none — zero failed requests"))
print("    time to first error  : %s" % (("%.2fs" % res["time_to_first_error_s"]) if res["time_to_first_error_s"] is not None else "n/a (no error)"))
print("    outage duration      : %s" % (("%.2fs" % res["outage_s"]) if res["outage_s"] is not None else "n/a"))
print("    recovered            : %s" % ("YES" if res["recovered"] else "NO"))
PY
}

# healthy: is the target answering right now?
#
# This guard exists because of a real failure of an earlier run of this very
# script: one case left a service dead, and every SUBSEQUENT case then measured a
# target that was already down — six cases reporting "100% of requests refused,
# recovered: NO", which looks like six catastrophic findings and is actually one.
# A case that starts unhealthy is recorded as SKIPPED, never as a result.
healthy() {
	local code
	code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 \
		${ORIGIN_IP:+--resolve "$(host_of "$TARGET"):443:$ORIGIN_IP"} \
		${ORIGIN_IP:+--resolve "$(host_of "$TARGET"):80:$ORIGIN_IP"} \
		"$TARGET/healthz" 2>/dev/null || true)"
	case "$code" in 2*|3*) return 0 ;; *) return 1 ;; esac
}

# run_case NAME NOTES FAULT_CMD  — the shared harness for every chaos case.
run_case() {
	local name="$1" notes="$2" fault_fn="$3"
	hdr "chaos: $name"
	dim "  $notes"

	if ! healthy; then
		warn "  target is NOT healthy before this case — skipping it rather than reporting a"
		warn "  fault we did not cause. Fix the previous failure first (systemctl status caddy appitools)."
		python3 -c '
import json, sys
json.dump({"part": "C-chaos", "case": sys.argv[1], "notes": sys.argv[2],
           "skipped": True,
           "skip_reason": "the target was already unhealthy before this case ran",
           "recovered": None}, open(sys.argv[3], "w"), indent=2)' \
			"$name" "$notes" "$OUT_DIR/chaos/$name.json"
		return 0
	fi
	local probe_out="$OUT_DIR/chaos/$name-probe.json"
	local total=$(( SETTLE + OBSERVE ))

	write_probe
	PROBE_TOKEN="${TOKEN:-}" PROBE_ORIGIN_IP="$ORIGIN_IP" PROBE_PATH="${PROBE_PATH:-/healthz}" \
		python3 "$OUT_DIR/chaos/probe.py" "$TARGET" "$probe_out" "$RATE" "$total" &
	local probe_pid=$!

	sleep "$SETTLE"
	local fault_ts; fault_ts="$(python3 -c 'import time; print(time.time())')"
	info "  injecting fault…"
	"$fault_fn" || warn "  fault command reported an error (continuing — the probe is the measurement)"

	wait "$probe_pid" || warn "  probe exited non-zero"
	analyse "$probe_out" "$fault_ts" "$name" "$notes" "$OUT_DIR/chaos/$name.json"
}

# ── Fault injectors ──────────────────────────────────────────────────────────
# NEVER pkill/pgrep -f: -f matches the invoking shell's own command line, so it
# can match and kill the session running this script. Every kill here resolves an
# EXACT pid from the listening socket or from systemd.

fault_engine_kill() {
	remote "pid=\$(ss -ltnpH '( sport = :${ENGINE_PORT} )' 2>/dev/null | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2); \
	        [ -n \"\$pid\" ] || pid=\$(systemctl show -p MainPID --value ${SERVICE}.service); \
	        echo \"killing engine pid \$pid (SIGKILL)\"; kill -9 \"\$pid\""
}

fault_caddy_kill() {
	remote "pid=\$(systemctl show -p MainPID --value caddy.service); \
	        echo \"killing caddy pid \$pid (SIGKILL)\"; kill -9 \"\$pid\""
}

fault_postgres_stop() {
	# Stop, hold it down long enough for the engine to notice, then bring it back
	# WITHOUT touching the engine — the question is whether the engine reconnects
	# on its own or needs a restart.
	remote "systemctl stop postgresql; echo 'postgres stopped'; sleep 15; systemctl start postgresql; echo 'postgres started again'"
}

fault_pool_exhaust() {
	# Far more concurrent in-flight requests than the pgx pool (DB_MAX_CONNS,
	# default 10) can serve at once. The question is whether excess load degrades
	# politely (queued, slow, clean errors) or takes the process down.
	if command -v k6 >/dev/null 2>&1; then
		info "  driving a burst far above the connection pool's width…"
		SCENARIO=read RATE=1500 DURATION=25s TARGET_URL="$TARGET" TOKEN="${TOKEN:-}" \
		PER_PAGE=100 CACHE_BUST=1 HOST_HEADER="$(host_of "$TARGET")" ORIGIN_IP="$ORIGIN_IP" \
		SUMMARY_OUT="$OUT_DIR/chaos/pool-exhaust-load.json" \
			k6 run --quiet --no-usage-report "$SELF_DIR/k6/scenarios.js" >"$OUT_DIR/chaos/pool-exhaust-k6.log" 2>&1 || true
	else
		warn "  k6 not present — cannot generate the saturating burst"
	fi
}

fault_memory_pressure() {
	# Big pages, all distinct (cache bypassed), concurrently: the shape most
	# likely to inflate the engine's heap toward GOMEMLIMIT.
	if command -v k6 >/dev/null 2>&1; then
		info "  requesting large result sets concurrently (heap pressure)…"
		SCENARIO=rest_include RATE=400 DURATION=30s TARGET_URL="$TARGET" TOKEN="${TOKEN:-}" \
		PER_PAGE=100 CACHE_BUST=1 HOST_HEADER="$(host_of "$TARGET")" ORIGIN_IP="$ORIGIN_IP" \
		SUMMARY_OUT="$OUT_DIR/chaos/memory-pressure-load.json" \
			k6 run --quiet --no-usage-report "$SELF_DIR/k6/scenarios.js" >"$OUT_DIR/chaos/memory-pressure-k6.log" 2>&1 || true
		# Was the engine OOM-killed, and did its heap ceiling hold?
		remote "echo '--- oom kills (dmesg) ---'; dmesg 2>/dev/null | grep -ci 'killed process' || echo 0; \
		        echo '--- engine restarts ---'; systemctl show -p NRestarts --value ${SERVICE}.service" \
			> "$OUT_DIR/chaos/memory-pressure-server.txt" 2>&1 || true
	fi
}

fault_deploy_update() {
	# The REAL update path under live traffic: copy the running binary over
	# itself through deploy-update.sh (atomic swap + restart + health check).
	# Same bits in and out, so this measures the DEPLOY MECHANICS, not a version
	# change — exactly the variable we want isolated.
	remote "set -e
	  BIN=${BIN}
	  cp \"\$BIN\" /tmp/redeploy-same.bin
	  if [ -x /opt/appitools/scripts/deploy-update.sh ]; then
	    bash /opt/appitools/scripts/deploy-update.sh --binary=/tmp/redeploy-same.bin --port=${ENGINE_PORT} 2>&1 | tail -3
	  else
	    echo 'deploy-update.sh not installed — falling back to a plain systemctl restart'
	    systemctl restart ${SERVICE}
	  fi
	  rm -f /tmp/redeploy-same.bin"
}

fault_reboot() {
	[ -n "$SERVER_SSH" ] || { warn "  --case=reboot needs --server-ssh (the box goes away)"; return 1; }
	remote "systemctl reboot" || true   # ssh dies with the box; that is expected
	return 0
}

# ── Dispatch ─────────────────────────────────────────────────────────────────
info "target=$TARGET case=$CASE probe=${RATE}rps out=$OUT_DIR"

# db_probe_path: the endpoint used for faults whose whole point is the DATABASE.
# Requires a token (it is a real, authorized, RBAC-checked read).
db_probe_path() {
	if [ -n "$TOKEN" ]; then printf '/api/orders?per_page=1'; else
		warn "  no --token: falling back to /healthz, which never touches PostgreSQL — this case cannot show database impact"
		printf '/healthz'
	fi
}

run_one() {
	case "$1" in
		engine-kill)     run_case engine-kill     "SIGKILL the engine — does systemd's Restart=always bring it back, and what does Caddy return meanwhile?" fault_engine_kill ;;
		caddy-kill)      run_case caddy-kill      "SIGKILL Caddy — the TLS front door disappears; how long until it is answering again?" fault_caddy_kill ;;
		postgres-stop)   PROBE_PATH="$(db_probe_path)" OBSERVE=$(( OBSERVE > 60 ? OBSERVE : 60 )) run_case postgres-stop "stop PostgreSQL for 15s, then start it — does the engine survive the loss and reconnect by itself?" fault_postgres_stop ;;
		pool-exhaust)    PROBE_PATH="$(db_probe_path)" run_case pool-exhaust    "offer far more concurrency than the pgx pool is wide — graceful degradation or collapse?" fault_pool_exhaust ;;
		memory-pressure) PROBE_PATH="$(db_probe_path)" run_case memory-pressure "large concurrent result sets — does GOMEMLIMIT hold the heap, or does the OOM killer fire?" fault_memory_pressure ;;
		deploy-update)   run_case deploy-update   "swap the binary under live traffic via deploy-update.sh — the real update path" fault_deploy_update ;;
		reboot)          OBSERVE=$(( OBSERVE > 180 ? OBSERVE : 180 )) run_case reboot "reboot the whole box — do all three services come back with no human?" fault_reboot ;;
		*) die "unknown case '$1' (see --help)" ;;
	esac
}

if [ "$CASE" = "all" ]; then
	# Ordered least to most destructive; the reboot goes last so everything else
	# is already measured if the box comes back unhappy.
	for c in engine-kill caddy-kill postgres-stop pool-exhaust memory-pressure deploy-update reboot; do
		run_one "$c"
		sleep 5
	done
else
	run_one "$CASE"
fi

ok "chaos results in $OUT_DIR/chaos/"
