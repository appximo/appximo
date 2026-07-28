#!/usr/bin/env bash
#
# footprint.sh — what does the whole production stack actually COST on this box?
#
# RUNS ON THE SERVER. Answers the question that decides whether Appitools fits on
# a $6 VPS: with Caddy + the engine + PostgreSQL + the OS all running, how much
# RAM is really used, by which piece, and how much is left?
#
#   bash footprint.sh                          # snapshot right now
#   bash footprint.sh --label=under-load       # same, tagged (run it during load)
#   bash footprint.sh --watch=60 --interval=5  # sample for 60s, report the PEAK
#
# It reports PSS, not RSS, per service. RSS double-counts shared pages (every
# forked PostgreSQL backend "has" the whole shared_buffers), so summing RSS
# across processes invents memory that does not exist — the classic way to make
# PostgreSQL look 5× larger than it is. PSS (proportional set size) divides each
# shared page among its sharers, so the per-service numbers SUM correctly.
#
# systemd puts each service in its own cgroup, so the second, independent source
# is `memory.current` per unit — the number the kernel would use to OOM-kill.
# Both are reported; they should agree within a few MB.
#
# Flags:
#   --label=NAME     tag this snapshot (e.g. idle / under-load)   [default idle]
#   --watch=SECONDS  sample repeatedly for this long and keep the peak  [default 0 = one shot]
#   --interval=SEC   seconds between samples when watching        [default 2]
#   --out=PATH       write the JSON result here
#   --service=NAME   engine systemd unit name                     [default appitools]
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

LABEL="idle"; WATCH=0; INTERVAL=2; OUT=""
for arg in "$@"; do
	case "$arg" in
		--label=*)    LABEL="${arg#*=}" ;;
		--watch=*)    WATCH="${arg#*=}" ;;
		--interval=*) INTERVAL="${arg#*=}" ;;
		--out=*)      OUT="${arg#*=}" ;;
		--service=*)  SERVICE="${arg#*=}" ;;
		--help|-h)    sed -n '3,28p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done
need python3

# The three units of the official stack. postgresql.service is a systemd wrapper
# whose cgroup is usually empty (the real work lives in postgresql@<ver>-<cluster>),
# so we resolve the actual instance unit rather than reporting a misleading 0.
resolve_pg_unit() {
	local u
	u="$(systemctl list-units --type=service --state=active --no-legend 'postgresql@*' 2>/dev/null | awk '{print $1}' | head -1)"
	[ -n "$u" ] && { printf '%s' "$u"; return; }
	printf 'postgresql.service'
}
PG_UNIT="$(resolve_pg_unit)"
UNITS="${SERVICE}.service caddy.service ${PG_UNIT}"

# cgroup_mem UNIT -> bytes currently charged to that unit's cgroup, or empty.
cgroup_mem() {
	local unit="$1" path
	path="$(systemctl show -p ControlGroup --value "$unit" 2>/dev/null || true)"
	[ -n "$path" ] || return 0
	local f="/sys/fs/cgroup${path}/memory.current"
	[ -r "$f" ] && cat "$f" 2>/dev/null || true
}

# cgroup_cpu UNIT -> cumulative CPU microseconds for the unit's cgroup.
cgroup_cpu() {
	local unit="$1" path
	path="$(systemctl show -p ControlGroup --value "$unit" 2>/dev/null || true)"
	[ -n "$path" ] || return 0
	local f="/sys/fs/cgroup${path}/cpu.stat"
	[ -r "$f" ] && awk '/^usage_usec/{print $2}' "$f" 2>/dev/null || true
}

# unit_pss UNIT -> "totalPss anonPss" (KiB) summed over every PID in the cgroup.
# smaps_rollup is one read per process (vs parsing all of smaps) and is present
# on every kernel this product supports.
#
# The split matters and is the difference between an honest and a scary number.
# Total PSS includes FILE-BACKED pages — chiefly the mapped text of a ~60 MB
# static Go binary. Those pages are page cache: the kernel evicts them under
# pressure and re-reads them from disk. Pss_Anon is the memory that is really
# the process's own and can never be reclaimed without swapping. For "will this
# fit in 1 GB?", Pss_Anon is the number that decides, so we report both rather
# than letting a big static binary masquerade as a big memory appetite.
unit_pss() {
	local unit="$1" path pids total=0 anon=0 vals
	path="$(systemctl show -p ControlGroup --value "$unit" 2>/dev/null || true)"
	[ -n "$path" ] || { printf '0 0'; return; }
	local pf="/sys/fs/cgroup${path}/cgroup.procs"
	[ -r "$pf" ] || { printf '0 0'; return; }
	pids="$(cat "$pf" 2>/dev/null || true)"
	for p in $pids; do
		# Pss_Anon exists since Linux 5.5; on older kernels it reads as 0 and the
		# report simply falls back to total PSS being the only signal.
		vals="$(awk '/^Pss:/{t+=$2} /^Pss_Anon:/{a+=$2} END{print (t+0)" "(a+0)}' \
			"/proc/$p/smaps_rollup" 2>/dev/null || echo "0 0")"
		total=$(( total + $(printf '%s' "$vals" | cut -d' ' -f1) ))
		anon=$((  anon  + $(printf '%s' "$vals" | cut -d' ' -f2) ))
	done
	printf '%s %s' "$total" "$anon"
}

# unit_procs / unit_fds — process count and open file descriptors, the other two
# resources a small box runs out of.
unit_procs() {
	local path; path="$(systemctl show -p ControlGroup --value "$1" 2>/dev/null || true)"
	[ -n "$path" ] && [ -r "/sys/fs/cgroup${path}/cgroup.procs" ] \
		&& wc -l < "/sys/fs/cgroup${path}/cgroup.procs" | tr -d ' ' || printf '0'
}
unit_fds() {
	local path pids total=0
	path="$(systemctl show -p ControlGroup --value "$1" 2>/dev/null || true)"
	[ -n "$path" ] || { printf '0'; return; }
	pids="$(cat "/sys/fs/cgroup${path}/cgroup.procs" 2>/dev/null || true)"
	for p in $pids; do
		total=$(( total + $(ls -1 "/proc/$p/fd" 2>/dev/null | wc -l) ))
	done
	printf '%s' "$total"
}

meminfo_kb() { awk -v k="$1:" '$1==k{print $2}' /proc/meminfo; }

# sample -> one line of "unit=pss_kb;cgroup_bytes;procs;fds" plus the system totals.
sample_once() {
	local u pss_pair
	for u in $UNITS; do
		pss_pair="$(unit_pss "$u")"
		printf '%s|%s|%s|%s|%s|%s\n' "$u" "${pss_pair%% *}" "${pss_pair##* }" \
			"$(cgroup_mem "$u")" "$(unit_procs "$u")" "$(unit_fds "$u")"
	done
	printf 'SYS|%s|%s|%s|%s|0\n' "$(meminfo_kb MemTotal)" "$(meminfo_kb MemAvailable)" "$(meminfo_kb MemFree)" "$(meminfo_kb Cached)"
}

hdr "footprint — label='$LABEL'"

# CPU is a RATE, so it needs two readings over a known interval. We always take
# a short baseline delta even for a one-shot snapshot, otherwise "CPU %" would be
# the average since boot, which is meaningless under load.
declare -A CPU0
for u in $UNITS; do CPU0[$u]="$(cgroup_cpu "$u")"; done
T0="$(now_ms)"

SAMPLES_FILE="$(mktemp)"
trap 'rm -f "$SAMPLES_FILE"' EXIT

if [ "$WATCH" -gt 0 ]; then
	info "sampling every ${INTERVAL}s for ${WATCH}s (peak is reported)…"
	END=$(( $(date +%s) + WATCH ))
	while [ "$(date +%s)" -lt "$END" ]; do
		sample_once >> "$SAMPLES_FILE"
		sleep "$INTERVAL"
	done
else
	sleep 1   # let the CPU delta window be non-zero
	sample_once >> "$SAMPLES_FILE"
fi

T1="$(now_ms)"
CPU_WINDOW_MS=$(( T1 - T0 ))
CPU_DELTA=""
for u in $UNITS; do
	c1="$(cgroup_cpu "$u")"; c0="${CPU0[$u]}"
	if [ -n "$c1" ] && [ -n "$c0" ]; then
		CPU_DELTA="${CPU_DELTA}${u}=$(( c1 - c0 ));"
	fi
done

# GOMEMLIMIT as the engine actually sees it (the env file is the source of truth
# the systemd unit loads), so the report can say whether the guard is armed.
GOMEMLIMIT_SET="$(load_env_secret GOMEMLIMIT || true)"
NPROC="$(nproc)"

RESULT="$(python3 - "$SAMPLES_FILE" "$LABEL" "$CPU_DELTA" "$CPU_WINDOW_MS" "$NPROC" "$GOMEMLIMIT_SET" <<'PY'
import json, sys, collections

path, label, cpu_delta, window_ms, nproc, gomemlimit = sys.argv[1:7]
window_ms = int(window_ms); nproc = int(nproc)

peak = collections.defaultdict(lambda: {"pss_kb": 0, "anon_kb": 0, "cgroup_bytes": 0, "procs": 0, "fds": 0})
sys_peak = {"mem_total_kb": 0, "mem_available_kb": None, "mem_free_kb": None, "cached_kb": 0}
samples = 0

for line in open(path):
    parts = line.strip().split("|")
    if len(parts) != 6:
        continue
    if parts[0] == "SYS":
        total, avail, free, cached = (int(x) if x else 0 for x in parts[1:5])
        sys_peak["mem_total_kb"] = max(sys_peak["mem_total_kb"], total)
        # The WORST case for availability is the MINIMUM available.
        sys_peak["mem_available_kb"] = avail if sys_peak["mem_available_kb"] is None else min(sys_peak["mem_available_kb"], avail)
        sys_peak["mem_free_kb"] = free if sys_peak["mem_free_kb"] is None else min(sys_peak["mem_free_kb"], free)
        sys_peak["cached_kb"] = max(sys_peak["cached_kb"], cached)
        samples += 1
        continue
    unit, pss, anon, cg, procs, fds = parts
    e = peak[unit]
    e["pss_kb"] = max(e["pss_kb"], int(pss or 0))
    e["anon_kb"] = max(e["anon_kb"], int(anon or 0))
    e["cgroup_bytes"] = max(e["cgroup_bytes"], int(cg or 0))
    e["procs"] = max(e["procs"], int(procs or 0))
    e["fds"] = max(e["fds"], int(fds or 0))

cpu = {}
for kv in cpu_delta.split(";"):
    if "=" in kv:
        u, v = kv.split("=", 1)
        usec = int(v or 0)
        # Percent of ONE core, and of the whole box.
        cpu[u] = {
            "cpu_usec": usec,
            "pct_one_core": round(100.0 * usec / (window_ms * 1000.0), 2) if window_ms else None,
            "pct_box": round(100.0 * usec / (window_ms * 1000.0 * nproc), 2) if window_ms else None,
        }

services = {}
stack_pss = 0
stack_anon = 0
for unit, e in peak.items():
    name = unit.replace(".service", "").split("@")[0]
    services[name] = {
        "unit": unit,
        "pss_mib": round(e["pss_kb"] / 1024.0, 1),
        "anon_mib": round(e["anon_kb"] / 1024.0, 1),
        "cgroup_mib": round(e["cgroup_bytes"] / 1048576.0, 1) if e["cgroup_bytes"] else None,
        "processes": e["procs"],
        "open_fds": e["fds"],
        "cpu": cpu.get(unit),
    }
    stack_pss += e["pss_kb"]
    stack_anon += e["anon_kb"]

total_kb = sys_peak["mem_total_kb"]
avail_kb = sys_peak["mem_available_kb"] or 0
used_kb = total_kb - avail_kb

print(json.dumps({
    "part": "A-footprint",
    "label": label,
    "samples": samples,
    "cpu_window_ms": window_ms,
    "nproc": nproc,
    "gomemlimit_env": gomemlimit or None,
    "services": services,
    "stack_pss_mib": round(stack_pss / 1024.0, 1),
    "stack_anon_mib": round(stack_anon / 1024.0, 1),
    "system": {
        "mem_total_mib": round(total_kb / 1024.0, 1),
        # MemAvailable is the honest "how much can a new process get" number —
        # it counts reclaimable page cache, which `free`'s "free" column does not.
        "mem_available_mib": round(avail_kb / 1024.0, 1),
        "mem_free_mib": round((sys_peak["mem_free_kb"] or 0) / 1024.0, 1),
        "page_cache_mib": round(sys_peak["cached_kb"] / 1024.0, 1),
        "mem_used_mib": round(used_kb / 1024.0, 1),
        "pct_used": round(100.0 * used_kb / total_kb, 1) if total_kb else None,
    },
}, indent=2))
PY
)"

# Rendered with %-formatting and single-quoted keys on purpose: nested same-quote
# f-strings are Python 3.12+ (PEP 701) and this must run on a stock Ubuntu 22.04
# box (Python 3.10).
printf '%s\n' "$RESULT" | python3 -c '
import json, sys
d = json.load(sys.stdin)
s = d["system"]
print()
print("  %-12s %9s %9s %9s %6s %6s %10s" % ("service", "PSS MiB", "anon MiB", "cgroup", "procs", "fds", "CPU %core"))
print("  " + "-" * 70)
for name, v in sorted(d["services"].items(), key=lambda kv: -kv[1]["pss_mib"]):
    cg = "%.1f" % v["cgroup_mib"] if v["cgroup_mib"] is not None else "-"
    cp = "%.1f" % v["cpu"]["pct_one_core"] if v.get("cpu") else "-"
    print("  %-12s %9.1f %9.1f %9s %6d %6d %10s" % (name, v["pss_mib"], v["anon_mib"], cg, v["processes"], v["open_fds"], cp))
print("  " + "-" * 70)
print("  %-12s %9.1f %9.1f" % ("STACK", d["stack_pss_mib"], d["stack_anon_mib"]))
print("  (anon = memory truly owned by the process; the rest is mapped binary /")
print("   shared-lib page cache the kernel can reclaim under pressure)")
print()
print("  RAM total %.0f MiB - used %.0f MiB (%s%%) - AVAILABLE %.0f MiB - page cache %.0f MiB"
      % (s["mem_total_mib"], s["mem_used_mib"], s["pct_used"], s["mem_available_mib"], s["page_cache_mib"]))
print()
'

if [ -n "$OUT" ]; then printf '%s' "$RESULT" | write_json "$OUT"; ok "wrote $OUT"; fi
