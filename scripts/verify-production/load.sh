#!/usr/bin/env bash
#
# load.sh — drive load at the PRODUCTION surface and report honest percentiles.
#
# RUNS ON THE LOAD GENERATOR. That should be a DIFFERENT machine from the server:
# a loader sharing the server's CPU competes with the thing it is measuring, and
# both numbers become fiction. If you have no second box, run it locally anyway —
# the script detects the co-location and stamps every result with a warning so
# the number is never quoted as clean.
#
#   bash load.sh --target=https://api.example.com --token=$JWT --scenario=read
#   bash load.sh --target=... --token=... --ladder="100 250 500 1000 2000"
#   bash load.sh --target=... --token=... --compare-tls --engine-url=http://127.0.0.1:8090
#
# Methodology (inherited from the engine's own bench protocol, scripts/bench-protocol.sh):
#   * one WARMUP run, discarded — the pgx pool, the Postgres buffer cache and
#     Caddy's connection pool are all cold on the first pass
#   * N measurement runs with a cooldown between them, so run-to-run variance is
#     visible instead of hidden inside one long run
#   * percentiles from the POOLED raw sample, never the mean of per-run percentiles
#   * an open workload model (constant arrival rate), so a slowing server cannot
#     quietly throttle its own offered load and hide its own saturation
#
# Flags:
#   --target=URL         public base url under test                     [required]
#   --token=JWT          data-plane JWT                                 [required]
#   --scenario=NAME      read|write|mix|heavy|aggregate|rest_include|graphql_nested|rest_n1
#   --rate=N             arrival rate, requests/sec                     [default 200]
#   --duration=T         hold per run                                   [default 30s]
#   --runs=N             measurement runs                               [default 3]
#   --warmup=T           warmup hold (0 to skip)                        [default 45s]
#   --cooldown=SEC       idle seconds between runs                      [default 10]
#   --per-page=N         page size for list reads                       [default 20]
#   --cache-bust         make every request URI unique (bypass the response cache)
#   --both-cache-arms    run the scenario twice: cached and bypassed
#   --ladder="A B C"     climb these rates, stop at saturation (the knee finder)
#   --compare-tls        paired A/B: public HTTPS via Caddy vs direct engine HTTP
#   --engine-url=URL     the direct-engine url for --compare-tls  [default http://127.0.0.1:8090]
#   --origin-ip=IP       pin the target hostname to this IP, bypassing any CDN in front
#   --label=NAME         tag for the result files
#   --out-dir=DIR        results directory
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

# WARMUP is deliberately LONGER than a measurement run: with a 30 s warmup the
# first measured run was still visibly cold (p95 82 ms vs 5 ms on the next two),
# which then dominated the pooled tail. Warm longer than you measure.
SCENARIO="read"; RATE=200; DURATION="30s"; RUNS=3; WARMUP="45s"; COOLDOWN=10
PER_PAGE=20; CACHE_BUST=0; BOTH_ARMS="no"; LADDER=""; COMPARE_TLS="no"
ENGINE_URL="http://127.0.0.1:${ENGINE_PORT}"; LABEL=""; ORIGIN_IP=

for arg in "$@"; do
	case "$arg" in
		--target=*)     TARGET="${arg#*=}" ;;
		--token=*)      TOKEN="${arg#*=}" ;;
		--token-file=*) TOKEN="$(cat "${arg#*=}")" ;;
		--scenario=*)   SCENARIO="${arg#*=}" ;;
		--rate=*)       RATE="${arg#*=}" ;;
		--duration=*)   DURATION="${arg#*=}" ;;
		--runs=*)       RUNS="${arg#*=}" ;;
		--warmup=*)     WARMUP="${arg#*=}" ;;
		--cooldown=*)   COOLDOWN="${arg#*=}" ;;
		--per-page=*)   PER_PAGE="${arg#*=}" ;;
		--cache-bust)   CACHE_BUST=1 ;;
		--both-cache-arms) BOTH_ARMS="yes" ;;
		--ladder=*)     LADDER="${arg#*=}" ;;
		--compare-tls)  COMPARE_TLS="yes" ;;
		--engine-url=*) ENGINE_URL="${arg#*=}" ;;
		--origin-ip=*)  ORIGIN_IP="${arg#*=}" ;;
		--label=*)      LABEL="${arg#*=}" ;;
		--out-dir=*)    OUT_DIR="${arg#*=}" ;;
		--tenant=*)     TENANT="${arg#*=}" ;;
		--help|-h)      sed -n '3,47p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done

[ -n "$TARGET" ] || die "--target is required (e.g. --target=https://api.example.com)"
need k6 python3 curl
TOKEN="$(resolve_token)"
[ -n "$TENANT" ] || TENANT="$(tenant_of "$TARGET")"
OUT_DIR="$(ensure_out_dir)"
K6_SCRIPT="$SELF_DIR/k6/scenarios.js"
[ -f "$K6_SCRIPT" ] || die "missing $K6_SCRIPT"

# ── Co-location detection ────────────────────────────────────────────────────
# If the target's engine port answers on THIS box's loopback, the loader and the
# server are the same machine. That is a legitimate way to run the suite, but the
# numbers carry loader/server CPU contention and must be labelled as such.
COLOCATED="no"
if on_server; then COLOCATED="yes"; fi
[ "$COLOCATED" = "yes" ] && warn "loader is CO-LOCATED with the server — results include loader CPU contention and are marked colocated=true. For clean numbers, drive load from a second machine."

# ── Pre-flight: assert the scenario actually WORKS before measuring it ────────
# Load runs discard response bodies (cheap, lets a small loader push real rates),
# which makes a body-level failure invisible — most importantly GraphQL, which
# answers HTTP 200 and reports errors inside the body. So we issue exactly one
# real request per scenario, read the body, and refuse to measure a broken one.
# CURL_RESOLVE: make the one-off curls take the same path as the load, so the
# pre-flight validates the endpoint we are actually going to measure.
curl_resolve_args() {
	[ -n "$ORIGIN_IP" ] && printf -- '--resolve\n%s:443:%s\n--resolve\n%s:80:%s' \
		"$(host_of "$TARGET")" "$ORIGIN_IP" "$(host_of "$TARGET")" "$ORIGIN_IP"
}

preflight() {
	local scen="$1" url body code
	local RA=(); while IFS= read -r a; do [ -n "$a" ] && RA+=("$a"); done <<<"$(curl_resolve_args)"
	case "$scen" in
		graphql_nested)
			body="$(curl -s "${RA[@]}" -w '\n%{http_code}' -X POST "$TARGET/graphql" \
				-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
				-d "{\"query\":\"{ orders(per_page: 2) { data { id status customer { name } items { product } } } }\"}")" ;;
		write)
			body="$(curl -s "${RA[@]}" -w '\n%{http_code}' -X POST "$TARGET/api/orders" \
				-H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
				-d '{"status":"pending","region":"us-east","total":1.23}')" ;;
		aggregate)
			body="$(curl -sg "${RA[@]}" -w '\n%{http_code}' "$TARGET/api/orders/aggregate?count&sum=total&group_by=status" \
				-H "Authorization: Bearer $TOKEN")" ;;
		rest_include)
			body="$(curl -sg "${RA[@]}" -w '\n%{http_code}' "$TARGET/api/orders?include=customer,items&per_page=2" \
				-H "Authorization: Bearer $TOKEN")" ;;
		*)
			body="$(curl -sg "${RA[@]}" -w '\n%{http_code}' "$TARGET/api/orders?filter[status][eq]=paid&per_page=2" \
				-H "Authorization: Bearer $TOKEN")" ;;
	esac
	code="$(printf '%s' "$body" | tail -1)"
	body="$(printf '%s' "$body" | sed '$d')"
	case "$code" in
		2*) : ;;
		*) die "pre-flight for scenario '$scen' returned HTTP $code — not measuring a broken endpoint. Body: $(printf '%s' "$body" | head -c 300)" ;;
	esac
	# GraphQL: HTTP 200 is not success. The body must not carry an errors array.
	if [ "$scen" = "graphql_nested" ] && printf '%s' "$body" | grep -q '"errors"'; then
		die "pre-flight: GraphQL returned an errors[] array — $(printf '%s' "$body" | head -c 300)"
	fi
	dim "  pre-flight ok ($scen, HTTP $code, $(printf '%s' "$body" | wc -c) bytes)"
}

# detect_cdn: is a CDN sitting in front of this domain?
#
# This is not a curiosity — it silently invalidates the whole benchmark. A domain
# proxied by Cloudflare/Fastly resolves to the EDGE, so every request crosses the
# public internet twice and is terminated by the CDN's TLS, not the product's.
# Measured that way, the stack looks several milliseconds slower than it is and
# the number describes the CDN. We detect it, say so loudly, and (with
# --origin-ip) measure the origin instead.
CDN_DETECTED=""
detect_cdn() {
	local h hdrs
	h="$(host_of "$TARGET")"
	hdrs="$(curl -sI --max-time 10 "$TARGET/healthz" 2>/dev/null | tr -d '\r' || true)"
	local server
	server="$(printf '%s' "$hdrs" | grep -i '^server:' | head -1 | cut -d' ' -f2- || true)"
	case "$(printf '%s' "$hdrs" | tr 'A-Z' 'a-z')" in
		*cf-ray*|*"server: cloudflare"*) CDN_DETECTED="cloudflare" ;;
		*x-served-by*|*"server: fastly"*) CDN_DETECTED="fastly" ;;
		*x-amz-cf-id*)                    CDN_DETECTED="cloudfront" ;;
		*)
			# Fall back to the origin's own signature: Caddy sets `Via: 1.1 Caddy`.
			# Seeing no Caddy AND an unfamiliar Server header is suggestive, not proof.
			case "$server" in ""|*[Cc]addy*) : ;; *) CDN_DETECTED="unknown (Server: $server)" ;; esac
			;;
	esac
	if [ -n "$CDN_DETECTED" ]; then
		if [ -n "$ORIGIN_IP" ]; then
			warn "a CDN ($CDN_DETECTED) fronts $h — measuring the ORIGIN directly via --origin-ip=$ORIGIN_IP (this is the product's own latency)"
		else
			warn "a CDN ($CDN_DETECTED) fronts $h. These numbers therefore include the CDN's hop and TLS, NOT just your stack."
			warn "  For the product's own latency, re-run with --origin-ip=<your server's IP>."
		fi
	fi
}

# cache_state: report whether the engine served that URL from its response cache,
# so a result can never silently be "we benchmarked the cache".
cache_state() {
	local hdr_out
	hdr_out="$(curl -sg -o /dev/null -D - "$TARGET/api/orders?filter[status][eq]=paid&per_page=$PER_PAGE" \
		-H "Authorization: Bearer $TOKEN" 2>/dev/null | grep -i '^x-cache:' | tr -d '\r' || true)"
	printf '%s' "${hdr_out:-X-Cache: (miss/absent)}"
}

# ── One k6 run ───────────────────────────────────────────────────────────────
# Emits: <prefix>-summary.json (always) and <prefix>-raw.json (pooled raw
# latencies) when raw capture is enabled for this rate.
RAW_MAX_RATE="${RAW_MAX_RATE:-1000}"

run_once() {
	local scen="$1" rate="$2" dur="$3" prefix="$4" url="$5" bust="$6" want_raw="$7"
	local raw_json="" k6_out=()
	if [ "$want_raw" = "yes" ]; then
		raw_json="$prefix-k6raw.json"
		k6_out=(--out "json=$raw_json")
	fi
	# ORIGIN_IP applies only to the public hostname arm; a direct-engine URL is
	# already an address and must not be re-pinned.
	local origin_for_run=""
	case "$url" in *"$(host_of "$TARGET")"*) origin_for_run="$ORIGIN_IP" ;; esac
	SCENARIO="$scen" RATE="$rate" DURATION="$dur" TARGET_URL="$url" TOKEN="$TOKEN" \
	PER_PAGE="$PER_PAGE" CACHE_BUST="$bust" HOST_HEADER="$(host_of "$TARGET")" \
	ORIGIN_IP="$origin_for_run" SUMMARY_OUT="$prefix-summary.json" \
		k6 run --quiet --no-usage-report "${k6_out[@]}" "$K6_SCRIPT" >"$prefix-k6.log" 2>&1 || {
			warn "k6 exited non-zero for $prefix (see $prefix-k6.log) — keeping whatever it produced"
		}
	[ -f "$prefix-summary.json" ] || { warn "no summary for $prefix"; return 1; }

	if [ -n "$raw_json" ] && [ -f "$raw_json" ]; then
		# Stream-extract only http_req_duration values, then DELETE the raw k6
		# dump: it is ~15 metric points per request and would fill a small disk.
		python3 - "$raw_json" "$prefix-raw.json" <<'PY'
import json, sys
src, dst = sys.argv[1], sys.argv[2]
vals = []
with open(src) as fh:
    for line in fh:
        if '"http_req_duration"' not in line:
            continue
        try:
            rec = json.loads(line)
        except Exception:
            continue
        if rec.get("metric") == "http_req_duration" and rec.get("type") == "Point":
            v = rec.get("data", {}).get("value")
            if v is not None:
                vals.append(round(float(v), 4))
json.dump(vals, open(dst, "w"))
PY
		rm -f "$raw_json"
	fi
	return 0
}

# ── A measured scenario: warmup + N runs + pooled statistics ─────────────────
measure() {
	local scen="$1" rate="$2" bust="$3" url="$4" tag="$5"
	local base="$OUT_DIR/${tag}"
	mkdir -p "$(dirname "$base")"

	local want_raw="no"
	[ "$rate" -le "$RAW_MAX_RATE" ] && want_raw="yes"

	if [ "$WARMUP" != "0" ] && [ -n "$WARMUP" ]; then
		dim "  warmup ${WARMUP} (discarded)…"
		run_once "$scen" "$rate" "$WARMUP" "$base-warmup" "$url" "$bust" "no" || true
		rm -f "$base-warmup-summary.json" "$base-warmup-k6.log"
	fi

	local i
	for i in $(seq 1 "$RUNS"); do
		printf '  run %d/%d … ' "$i" "$RUNS"
		run_once "$scen" "$rate" "$DURATION" "$base-run$i" "$url" "$bust" "$want_raw" || true
		if [ -f "$base-run$i-summary.json" ]; then
			python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
l=d["latency_ms"]
print("%d reqs, p50 %.2fms p95 %.2fms p99 %.2fms, err %.2f%%, %.0f rps achieved"
      % (d["requests"], l["p50"] or 0, l["p95"] or 0, l["p99"] or 0,
         100*(d["error_rate"] or 0), d["rps_achieved"] or 0))' "$base-run$i-summary.json"
		else
			echo "FAILED"
		fi
		[ "$i" -lt "$RUNS" ] && sleep "$COOLDOWN"
	done

	# Pool the runs into one result.
	VP_CDN="$CDN_DETECTED" VP_ORIGIN="$ORIGIN_IP" \
	python3 - "$base" "$RUNS" "$scen" "$rate" "$bust" "$url" "$COLOCATED" "$TARGET" <<'PY' > "$base-pooled.json"
import glob, json, os, sys
sys.path.insert(0, os.path.dirname(os.path.abspath(os.environ.get("VP_LIB_DIR", "."))))
base, runs, scen, rate, bust, url, colocated, target = sys.argv[1:9]

summaries, raw = [], []
for i in range(1, int(runs) + 1):
    s = f"{base}-run{i}-summary.json"
    if os.path.exists(s):
        summaries.append(json.load(open(s)))
    r = f"{base}-run{i}-raw.json"
    if os.path.exists(r):
        raw.extend(json.load(open(r)))

out = {
    "part": "B-load",
    "scenario": scen,
    "rate_requested": int(rate),
    "cache_bust": bust == "1",
    "url": url,
    "target": target,
    "colocated_loader": colocated == "yes",
    "cdn_detected": os.environ.get("VP_CDN") or None,
    "origin_ip": os.environ.get("VP_ORIGIN") or None,
    "runs": len(summaries),
    "per_run": summaries,
}
if summaries:
    out["totals"] = {
        "requests": sum(s["requests"] for s in summaries),
        "dropped_iterations": sum(s.get("dropped_iterations", 0) for s in summaries),
        "rps_achieved_mean": round(sum((s["rps_achieved"] or 0) for s in summaries) / len(summaries), 2),
        "error_rate_max": max((s["error_rate"] or 0) for s in summaries),
        "bytes_per_request": summaries[0].get("bytes_per_request"),
        # Between-run stability of p50: a high CV means the bench bench itself is
        # noisy and no delta from it should be trusted.
        "p50_per_run": [s["latency_ms"]["p50"] for s in summaries],
        "waiting_p50_mean": round(sum((s["waiting_ms"]["p50"] or 0) for s in summaries) / len(summaries), 3),
    }
    p50s = [s["latency_ms"]["p50"] or 0 for s in summaries]
    mean = sum(p50s) / len(p50s)
    var = sum((x - mean) ** 2 for x in p50s) / len(p50s)
    out["totals"]["p50_cv"] = round((var ** 0.5) / mean, 4) if mean else None
json.dump(out, open(f"{base}-pooled.json.tmp", "w"), indent=2)
os.replace(f"{base}-pooled.json.tmp", f"{base}-pooled.json")

if raw:
    json.dump(raw, open(f"{base}-raw-pooled.json", "w"))
print(json.dumps({"pooled": f"{base}-pooled.json", "raw_n": len(raw)}))
PY

	if [ -f "$base-raw-pooled.json" ]; then
		python3 "$SELF_DIR/stats.py" summarize --file "$base-raw-pooled.json" > "$base-stats.json"
		python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
ci=d.get("ci95_median") or ["?","?"]
print("  POOLED n=%d  p50 %.2fms (CI95 [%s, %s])  p90 %.2f  p95 %.2f  p99 %.2f  max %.2f"
      % (d["n"], d["p50"], ci[0], ci[1], d["p90"], d["p95"], d["p99"], d["max"]))' "$base-stats.json"
	fi
}

# ── Modes ────────────────────────────────────────────────────────────────────

do_ladder() {
	hdr "load ladder — finding the knee (scenario=$SCENARIO, cache_bust=$CACHE_BUST)"
	preflight "$SCENARIO"
	local knee="null" prev_ok=""
	for L in $LADDER; do
		info "── $L rps ──"
		measure "$SCENARIO" "$L" "$CACHE_BUST" "$TARGET" "ladder/${SCENARIO}-${L}rps"
		local pooled="$OUT_DIR/ladder/${SCENARIO}-${L}rps-pooled.json"
		[ -f "$pooled" ] || continue
		# Saturation: error rate over 1%, OR p95 over 100ms, OR k6 could not keep
		# up (dropped iterations / achieved rate far below requested).
		local verdict
		verdict="$(python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
t=d.get("totals") or {}
runs=d.get("per_run") or []
if not runs: print("no-data"); raise SystemExit
p95=sorted(r["latency_ms"]["p95"] or 0 for r in runs)[len(runs)//2]
err=t.get("error_rate_max") or 0
ach=t.get("rps_achieved_mean") or 0
req=d["rate_requested"]
shortfall = (req-ach)/req if req else 0
reasons=[]
if err>0.01: reasons.append("errors %.2f%%"%(err*100))
if p95>100: reasons.append("p95 %.1fms"%p95)
if shortfall>0.05: reasons.append("loader/server delivered only %.0f of %d rps"%(ach,req))
print(("saturated:"+"; ".join(reasons)) if reasons else "ok")' "$pooled")"
		case "$verdict" in
			saturated*) warn "  knee at $L rps — ${verdict#saturated:}"; knee="$L"; break ;;
			no-data)    warn "  no data at $L rps"; break ;;
			*)          ok "  $L rps sustained cleanly"; prev_ok="$L" ;;
		esac
	done
	python3 -c '
import json,sys
json.dump({"part":"B-ladder","scenario":sys.argv[1],"cache_bust":sys.argv[2]=="1",
           "levels":sys.argv[3].split(),"knee_rps":(None if sys.argv[4]=="null" else int(sys.argv[4])),
           "highest_clean_rps":(int(sys.argv[5]) if sys.argv[5] else None)},
          open(sys.argv[6],"w"), indent=2)' \
		"$SCENARIO" "$CACHE_BUST" "$LADDER" "$knee" "${prev_ok:-}" "$OUT_DIR/ladder-${SCENARIO}.json"
	ok "ladder written to $OUT_DIR/ladder-${SCENARIO}.json (highest clean: ${prev_ok:-none}, knee: $knee)"
}

do_compare_tls() {
	hdr "TLS + reverse-proxy overhead — paired A/B"
	dim "  A: $TARGET        (Caddy: TLS termination + proxy hop)"
	dim "  B: $ENGINE_URL    (engine directly, same Host header, same request)"
	dim "  Both arms run the SAME scenario at the SAME rate, interleaved ABBA to"
	dim "  cancel any drift in machine state between them."
	preflight "$SCENARIO"
	# Create the arm directory FIRST: run_once writes k6's summary and log straight
	# into it, and k6 does not create parent directories — without this every run
	# fails on "No such file or directory" and the comparison has nothing to pool.
	mkdir -p "$OUT_DIR/tls"

	local i
	for i in $(seq 1 "$RUNS"); do
		# ABBA ordering: A B B A. A simple A-then-B ordering would confound the
		# difference with anything that warms up or heats up over the session.
		local order="A B"
		[ $(( i % 2 )) -eq 0 ] && order="B A"
		for arm in $order; do
			if [ "$arm" = "A" ]; then
				printf '  run %d arm A (https via Caddy) … ' "$i"
				run_once "$SCENARIO" "$RATE" "$DURATION" "$OUT_DIR/tls/a-run$i" "$TARGET" "$CACHE_BUST" "yes" || true
				[ -f "$OUT_DIR/tls/a-run$i-summary.json" ] && python3 -c '
import json,sys; d=json.load(open(sys.argv[1])); print("p50 %.2fms"%(d["latency_ms"]["p50"] or 0))' "$OUT_DIR/tls/a-run$i-summary.json" || echo "?"
			else
				printf '  run %d arm B (direct engine) … ' "$i"
				run_once "$SCENARIO" "$RATE" "$DURATION" "$OUT_DIR/tls/b-run$i" "$ENGINE_URL" "$CACHE_BUST" "yes" || true
				[ -f "$OUT_DIR/tls/b-run$i-summary.json" ] && python3 -c '
import json,sys; d=json.load(open(sys.argv[1])); print("p50 %.2fms"%(d["latency_ms"]["p50"] or 0))' "$OUT_DIR/tls/b-run$i-summary.json" || echo "?"
			fi
			sleep "$COOLDOWN"
		done
	done

	python3 - "$OUT_DIR/tls" "$RUNS" <<'PY'
import glob, json, os, sys
d, runs = sys.argv[1], int(sys.argv[2])
for arm in ("a", "b"):
    vals = []
    for i in range(1, runs + 1):
        f = f"{d}/{arm}-run{i}-raw.json"
        if os.path.exists(f):
            vals.extend(json.load(open(f)))
    json.dump(vals, open(f"{d}/{arm}-pooled-raw.json", "w"))
PY
	python3 "$SELF_DIR/stats.py" compare \
		--a "$OUT_DIR/tls/b-pooled-raw.json" --b "$OUT_DIR/tls/a-pooled-raw.json" \
		--label-a "direct-engine-http" --label-b "public-https-via-caddy" \
		> "$OUT_DIR/tls-overhead.json"
	python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
a,b=d["a"],d["b"]
print()
print("  direct engine (http)   p50 %.3f ms  p95 %.3f  n=%d" % (a["p50"], a["p95"], a["n"]))
print("  public via Caddy (tls) p50 %.3f ms  p95 %.3f  n=%d" % (b["p50"], b["p95"], b["n"]))
print("  ── overhead of the TLS + proxy layer: %+.3f ms p50 (%+.1f%%), Mann-Whitney p=%s"
      % (d["delta_p50_ms"], d["delta_pct"], d["p_value"]))
print()' "$OUT_DIR/tls-overhead.json"
	ok "wrote $OUT_DIR/tls-overhead.json"
}

do_single() {
	local arms="$CACHE_BUST"
	[ "$BOTH_ARMS" = "yes" ] && arms="0 1"
	preflight "$SCENARIO"
	for bust in $arms; do
		local arm_name="cached"; [ "$bust" = "1" ] && arm_name="cache-bypassed"
		hdr "scenario=$SCENARIO rate=${RATE}rps arm=$arm_name"
		[ "$bust" = "0" ] && dim "  engine cache state for this URL: $(cache_state)"
		measure "$SCENARIO" "$RATE" "$bust" "$TARGET" "${LABEL:-$SCENARIO}-${RATE}rps-$arm_name"
	done
}

# ── Dispatch ─────────────────────────────────────────────────────────────────
detect_cdn
info "target=$TARGET tenant=$TENANT out=$OUT_DIR${ORIGIN_IP:+ origin=$ORIGIN_IP}"
if [ -n "$LADDER" ]; then      do_ladder
elif [ "$COMPARE_TLS" = "yes" ]; then do_compare_tls
else                            do_single
fi
ok "results in $OUT_DIR"
