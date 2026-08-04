#!/usr/bin/env bash
#
# run-all.sh — run the whole production verification suite and produce ONE report.
#
# This is the entry point. Point it at your server and it measures the footprint,
# the load capacity, the cost of the production layers, the behaviour at scale,
# REST vs GraphQL, and (optionally) the resilience of the stack under deliberate
# faults — then writes a Markdown report with every number's conditions attached.
#
#   # from a SECOND machine (recommended — the loader must not share the server's CPU)
#   bash run-all.sh --target=https://api.example.com --server-ssh=root@1.2.3.4
#
#   # quick pass, no chaos, no seeding
#   bash run-all.sh --target=... --server-ssh=... --quick --skip-chaos
#
# What it needs:
#   * k6 and python3 on THIS machine (the loader)
#   * ssh access to the server for the footprint / seed / chaos phases
#     (without --server-ssh those phases are skipped and the load phases still run)
#   * a JWT for the tenant, or --server-ssh so one can be minted on the server
#
# Phases (each can be skipped):
#   footprint  RAM/CPU per service, idle and under load           --skip-footprint
#   seed       fill the tenant with N rows                        --seed=N (default: skip)
#   load       read/write/mix scenarios, both cache arms          --skip-load
#   ladder     climb the rates to find the knee                   --skip-ladder
#   layers     TLS + proxy overhead, paired A/B                   --skip-layers
#   restgql    REST vs GraphQL, same logical query                --skip-restgql
#   chaos      deliberate faults + recovery measurement           --skip-chaos (DESTRUCTIVE)
#
# Flags:
#   --target=URL        public base url                                   [required]
#   --server-ssh=HOST   ssh destination of the server (e.g. root@1.2.3.4)
#   --origin-ip=IP      pin the hostname to the origin, bypassing a CDN
#   --token=JWT         data-plane JWT (else minted over ssh)
#   --tenant=ID         tenant id (default: the target's first host label)
#   --seed=N            seed the tenant with N orders before measuring
#   --rate=N            base rate for the scenario phase                  [default 200]
#   --duration=T        hold per run                                      [default 30s]
#   --runs=N            measurement runs per scenario                     [default 3]
#   --ladder="A B C"    rates to climb            [default "100 250 500 1000 2000 3000"]
#   --quick             short runs, fewer repetitions (a smoke pass, not evidence)
#   --out-dir=DIR       results directory
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

SERVER_SSH=""; ORIGIN_IP=""; SEED_ROWS=""; RATE=200; DURATION="30s"; RUNS=3
LADDER="100 250 500 1000 2000 3000"; QUICK="no"
SKIP_FOOTPRINT="no"; SKIP_LOAD="no"; SKIP_LADDER="no"; SKIP_LAYERS="no"
SKIP_RESTGQL="no"; SKIP_CHAOS="yes"   # chaos is destructive: opt IN, never by default

for arg in "$@"; do
	case "$arg" in
		--target=*)      TARGET="${arg#*=}" ;;
		--server-ssh=*)  SERVER_SSH="${arg#*=}" ;;
		--origin-ip=*)   ORIGIN_IP="${arg#*=}" ;;
		--token=*)       TOKEN="${arg#*=}" ;;
		--token-file=*)  TOKEN="$(cat "${arg#*=}")" ;;
		--tenant=*)      TENANT="${arg#*=}" ;;
		--seed=*)        SEED_ROWS="${arg#*=}" ;;
		--rate=*)        RATE="${arg#*=}" ;;
		--duration=*)    DURATION="${arg#*=}" ;;
		--runs=*)        RUNS="${arg#*=}" ;;
		--ladder=*)      LADDER="${arg#*=}" ;;
		--quick)         QUICK="yes" ;;
		--skip-footprint) SKIP_FOOTPRINT="yes" ;;
		--skip-load)     SKIP_LOAD="yes" ;;
		--skip-ladder)   SKIP_LADDER="yes" ;;
		--skip-layers)   SKIP_LAYERS="yes" ;;
		--skip-restgql)  SKIP_RESTGQL="yes" ;;
		--skip-chaos)    SKIP_CHAOS="yes" ;;
		--with-chaos)    SKIP_CHAOS="no" ;;
		--out-dir=*)     OUT_DIR="${arg#*=}" ;;
		--help|-h)       sed -n '3,48p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done

[ -n "$TARGET" ] || die "--target is required (e.g. --target=https://api.example.com)"
need k6 python3 curl

if [ "$QUICK" = "yes" ]; then
	DURATION="10s"; RUNS=2; LADDER="100 250 500"
	warn "--quick: short runs and few repetitions. Good for checking the suite works; NOT evidence."
fi

OUT_DIR="$(ensure_out_dir)"
[ -n "$TENANT" ] || TENANT="$(tenant_of "$TARGET")"

# remote: run a command on the server, if we were given one.
HAVE_SERVER="no"; [ -n "$SERVER_SSH" ] && HAVE_SERVER="yes"
remote() { ssh -o BatchMode=yes -o ConnectTimeout=15 ${VP_SSH_OPTS:-} "$SERVER_SSH" "$@"; }

# A token is needed by nearly every phase. Mint one on the server if we can.
if [ -z "$TOKEN" ] && [ "$HAVE_SERVER" = "yes" ]; then
	info "minting a JWT on the server…"
	TOKEN="$(remote "secret=\$(grep -E '^JWT_SECRET=' ${ENV_FILE} | head -1 | cut -d= -f2-); \
	                 ${BIN} token --secret \"\$secret\" --tenant ${TENANT} --role admin 2>/dev/null | tail -1")" || true
fi
[ -n "$TOKEN" ] || die "no JWT: pass --token=<jwt> or --server-ssh so one can be minted"

COMMON=(--target="$TARGET" --token="$TOKEN" --tenant="$TENANT" --out-dir="$OUT_DIR")
[ -n "$ORIGIN_IP" ] && COMMON+=(--origin-ip="$ORIGIN_IP")

# ── Run metadata: every report must say what it measured, and how ────────────
CDN="$(curl -sI --max-time 10 "$TARGET/healthz" 2>/dev/null | tr -d '\r' | tr 'A-Z' 'a-z' \
	| grep -Eo 'server: (cloudflare|fastly)|cf-ray' | head -1 | sed 's/server: //' || true)"
SERVER_DESC=""; DATASET=""
if [ "$HAVE_SERVER" = "yes" ]; then
	SERVER_DESC="$(remote "echo \"\$(nproc) vCPU, \$(awk '/MemTotal/{printf \"%.0f MiB\", \$2/1024}' /proc/meminfo), \$(. /etc/os-release; echo \$PRETTY_NAME)\"" 2>/dev/null || true)"
fi
python3 - "$OUT_DIR/run-meta.json" "$TARGET" "$ORIGIN_IP" "$CDN" "$TENANT" "$SERVER_DESC" \
	"$(nproc) vCPU loader" "$(date -u +%FT%TZ)" <<'PY'
import json, sys
out, target, origin, cdn, tenant, server, loader, started = sys.argv[1:9]
json.dump({"started_utc": started, "target": target, "origin_ip": origin or None,
           "cdn_detected": cdn or None, "tenant": tenant, "server": server or None,
           "loader": loader}, open(out, "w"), indent=2)
PY

hdr "Appximo production verification"
dim "  target   $TARGET${ORIGIN_IP:+  (origin $ORIGIN_IP)}"
dim "  tenant   $TENANT"
dim "  server   ${SERVER_DESC:-<not reachable — server-side phases skipped>}"
dim "  results  $OUT_DIR"
[ -n "$CDN" ] && [ -z "$ORIGIN_IP" ] && warn "a CDN ($CDN) fronts this domain and is NOT bypassed — pass --origin-ip=<server ip> to measure the stack itself"

# ── Phase: seed ──────────────────────────────────────────────────────────────
if [ -n "$SEED_ROWS" ]; then
	[ "$HAVE_SERVER" = "yes" ] || die "--seed needs --server-ssh (seeding runs on the server, over the local socket)"
	hdr "seed — $SEED_ROWS orders"
	remote "mkdir -p /tmp/vp" >/dev/null
	scp -q -o BatchMode=yes ${VP_SSH_OPTS:-} "$SELF_DIR/lib.sh" "$SELF_DIR/seed.sh" "$SERVER_SSH:/tmp/vp/" \
		|| die "could not copy the seeder to the server"
	remote "bash /tmp/vp/seed.sh --tenant=$TENANT --orders=$SEED_ROWS --out=/tmp/vp/seed.json" || warn "seed reported an error"
	remote "cat /tmp/vp/seed.json" > "$OUT_DIR/seed.json" 2>/dev/null || true
fi

if [ "$HAVE_SERVER" = "yes" ]; then
	DATASET="$(remote "grep -E '^DATABASE_URL=' ${ENV_FILE} >/dev/null 2>&1 && \
		psql -tAX -d \"\$(grep -E '^DATABASE_URL=' ${ENV_FILE} | head -1 | cut -d= -f2-)\" \
		-c \"select 'orders=' || count(*) from tenant_${TENANT}.orders\" 2>/dev/null" || true)"
	[ -n "$DATASET" ] && python3 -c '
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d["dataset"]=sys.argv[2].strip(); json.dump(d,open(p,"w"),indent=2)' \
		"$OUT_DIR/run-meta.json" "$DATASET"
fi

# ── Phase: footprint (idle) ──────────────────────────────────────────────────
copy_server_tools() {
	remote "mkdir -p /tmp/vp" >/dev/null
	scp -q -o BatchMode=yes ${VP_SSH_OPTS:-} "$SELF_DIR/lib.sh" "$SELF_DIR/footprint.sh" "$SERVER_SSH:/tmp/vp/"
}
if [ "$SKIP_FOOTPRINT" = "no" ] && [ "$HAVE_SERVER" = "yes" ]; then
	hdr "footprint — idle"
	copy_server_tools || warn "could not copy footprint.sh to the server"
	remote "bash /tmp/vp/footprint.sh --label=idle --out=/tmp/vp/fp-idle.json" || warn "footprint (idle) failed"
	remote "cat /tmp/vp/fp-idle.json" > "$OUT_DIR/footprint-idle.json" 2>/dev/null || true
fi

# ── Phase: load scenarios ────────────────────────────────────────────────────
if [ "$SKIP_LOAD" = "no" ]; then
	# Only GETs are cacheable, so only the read-bearing scenarios get both arms.
	# Running a "cached" arm for a POST-only scenario would double the runtime to
	# produce two identical numbers under two different labels.
	for scen in read mix; do
		bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario="$scen" --rate="$RATE" \
			--duration="$DURATION" --runs="$RUNS" --both-cache-arms \
			|| warn "scenario $scen failed"
	done
	bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario=write --rate="$RATE" \
		--duration="$DURATION" --runs="$RUNS" || warn "scenario write failed"
	# Aggregates are O(table): an unfiltered GROUP BY reads every row, so they are
	# driven at a deliberately lower rate. Pushing them at the read rate measures
	# nothing but a queue.
	bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario=aggregate --rate=10 \
		--duration="$DURATION" --runs="$RUNS" --cache-bust || warn "scenario aggregate failed"
fi

# ── Phase: footprint (under load) ────────────────────────────────────────────
if [ "$SKIP_FOOTPRINT" = "no" ] && [ "$HAVE_SERVER" = "yes" ]; then
	hdr "footprint — under load"
	( bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario=mix --rate="$RATE" \
		--duration=45s --runs=1 --warmup=0 --cache-bust \
		--out-dir="$OUT_DIR/fp-load" >/dev/null 2>&1 ) &
	LOADPID=$!
	sleep 8
	remote "bash /tmp/vp/footprint.sh --label=under-load --watch=30 --interval=3 --out=/tmp/vp/fp-load.json" \
		|| warn "footprint (under load) failed"
	remote "cat /tmp/vp/fp-load.json" > "$OUT_DIR/footprint-under-load.json" 2>/dev/null || true
	wait "$LOADPID" 2>/dev/null || true
fi

# ── Phase: ladder ────────────────────────────────────────────────────────────
if [ "$SKIP_LADDER" = "no" ]; then
	bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario=read --ladder="$LADDER" \
		--duration="$DURATION" --runs=2 --cache-bust || warn "ladder failed"
fi

# ── Phase: layers (TLS + proxy overhead) ─────────────────────────────────────
if [ "$SKIP_LAYERS" = "no" ]; then
	if [ "$HAVE_SERVER" = "yes" ]; then
		# The engine port is (correctly) not exposed to the internet, so the paired
		# A/B has to run ON the server. It is co-located and therefore inflated in
		# ABSOLUTE terms, but both arms pay the same inflation and the DIFFERENCE —
		# which is what this phase is for — stays valid.
		hdr "layers — TLS + proxy overhead (paired A/B, run on the server)"
		remote "mkdir -p /tmp/vp/k6" >/dev/null
		scp -q -o BatchMode=yes ${VP_SSH_OPTS:-} "$SELF_DIR/lib.sh" "$SELF_DIR/load.sh" "$SELF_DIR/stats.py" "$SERVER_SSH:/tmp/vp/" || true
		scp -q -o BatchMode=yes ${VP_SSH_OPTS:-} "$SELF_DIR/k6/scenarios.js" "$SERVER_SSH:/tmp/vp/k6/" || true
		if remote "command -v k6 >/dev/null 2>&1"; then
			remote "bash /tmp/vp/load.sh --target=$TARGET --token='$TOKEN' --tenant=$TENANT \
				--compare-tls --engine-url=http://127.0.0.1:${ENGINE_PORT} --origin-ip=127.0.0.1 \
				--rate=100 --duration=20s --runs=3 --warmup=10s --cooldown=5 --out-dir=/tmp/vp/layers" \
				|| warn "layer comparison failed"
			remote "cat /tmp/vp/layers/tls-overhead.json" > "$OUT_DIR/tls-overhead.json" 2>/dev/null || true
		else
			warn "k6 is not installed on the server — skipping the layer A/B (install k6 there to measure it)"
		fi
	else
		warn "skipping the layer A/B: it needs --server-ssh (the engine port is not public, by design)"
	fi
fi

# ── Phase: REST vs GraphQL ───────────────────────────────────────────────────
if [ "$SKIP_RESTGQL" = "no" ]; then
	hdr "REST vs GraphQL — the same logical query, both ways"
	# The response cache is GET-only, so it would serve the REST arm and never the
	# GraphQL POST. Both arms therefore run with the cache bypassed; otherwise the
	# comparison would be "REST's cache vs GraphQL's database".
	for arm in rest_include graphql_nested; do
		bash "$SELF_DIR/load.sh" "${COMMON[@]}" --scenario="$arm" --rate=100 \
			--duration="$DURATION" --runs="$RUNS" --cache-bust --label="rgq-$arm" \
			>/dev/null 2>&1 || warn "arm $arm failed"
	done
	python3 - "$OUT_DIR" <<'PY' || true
import glob, json, os, sys
d = sys.argv[1]
arms = []
for name, label in (("rest_include", "REST  ?include=customer,items (1 request)"),
                    ("graphql_nested", "GraphQL nested query (1 request)")):
    # Exclude "-raw-pooled.json": it is a bare ARRAY of latencies, not a result
    # object, and the wildcard matches it too.
    pooled = [f for f in glob.glob(os.path.join(d, f"rgq-{name}-*-pooled.json"))
              if not f.endswith("-raw-pooled.json")]
    stats  = glob.glob(os.path.join(d, f"rgq-{name}-*-stats.json"))
    if not pooled:
        continue
    p = json.load(open(pooled[0]))
    s = json.load(open(stats[0])) if stats else {}
    runs = p.get("per_run") or [{}]
    arms.append({
        "name": label,
        "round_trips": 1,
        "p50": s.get("p50") or (runs[0].get("latency_ms") or {}).get("p50"),
        "p95": s.get("p95") or (runs[0].get("latency_ms") or {}).get("p95"),
        "bytes_per_request": (p.get("totals") or {}).get("bytes_per_request"),
    })
verdict = ""
if len(arms) == 2:
    r, g = arms[0], arms[1]
    if r["p50"] and g["p50"]:
        faster, slower = ("REST", "GraphQL") if r["p50"] <= g["p50"] else ("GraphQL", "REST")
        delta = abs(r["p50"] - g["p50"])
        verdict = (
            f"At one round trip each, **{faster} is {delta:.2f} ms faster at the median** "
            f"(REST {r['p50']:.2f} ms / {r['bytes_per_request']} B, "
            f"GraphQL {g['p50']:.2f} ms / {g['bytes_per_request']} B).\n\n"
            "Both shapes fetch the nested data in ONE request and ONE database round trip "
            "(`?include=` and the GraphQL resolver share the same LATERAL query), so the "
            "usual argument for GraphQL — fewer round trips — does not apply here: REST "
            "already collapses them. What GraphQL still buys is **field selection**: the "
            "client names exactly the fields it wants, which shrinks the payload when a "
            "view needs a few columns of a wide resource.")
json.dump({"part": "E-rest-vs-graphql", "arms": arms, "verdict": verdict},
          open(os.path.join(d, "rest-vs-graphql.json"), "w"), indent=2)
PY
fi

# ── Phase: chaos ─────────────────────────────────────────────────────────────
if [ "$SKIP_CHAOS" = "no" ]; then
	[ "$HAVE_SERVER" = "yes" ] || die "--with-chaos needs --server-ssh"
	warn "chaos phase is DESTRUCTIVE: it kills services and reboots the box. Ctrl-C within 5s to abort."
	sleep 5
	bash "$SELF_DIR/chaos.sh" --target="$TARGET" --server-ssh="$SERVER_SSH" \
		${ORIGIN_IP:+--origin-ip="$ORIGIN_IP"} --token="$TOKEN" --case=all \
		--out-dir="$OUT_DIR" || warn "chaos phase reported errors"
fi

# ── Report ───────────────────────────────────────────────────────────────────
hdr "report"
python3 "$SELF_DIR/report.py" --dir "$OUT_DIR" --out "$OUT_DIR/report.md"
ok "done — $OUT_DIR/report.md"
dim "  raw JSON for every number is in $OUT_DIR"
