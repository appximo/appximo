#!/usr/bin/env bash
# bench-protocol.sh — scientific benchmark protocol for the DevHub statistical
# engine (S42). Turns single-shot k6 runs into defensible evidence:
#
#   1. initial cooldown 10s
#   2. one warmup run                       → discarded
#   3. N measurement runs, 15s cooldown between each
#   4. each run: k6 --out json=/tmp/bench-$i.json
#   5. import each run via POST /api/bench/import  (label "LABEL#i")
#   6. print a summary table: run_id, p50, p95, p99, cv
#   7. if mean CV > 15%, warn that the benchmark bench is unstable
#
# Usage: bash scripts/bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION]
# Env overrides: DEVHUB_URL TARGET_URL TENANT_ID ENDPOINT BENCH_TOKEN K6_SCRIPT
#                APPITOOLS_SECRETS WARMUP_DURATION
#
# NOTE on constrained / shared hosts (e.g. the 1-vCPU dev box): keep RATE low
# (≤100). Co-locating the k6 load generator with the engine on a single core
# injects scheduling noise that the (correctly powerful) Mann-Whitney test then
# flags as false "significance" between two identical runs. At RATE=50 the runs
# are reproducible and same-system comparisons land on no_change as they should.
set -euo pipefail

RUNS="${1:?Uso: bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION]}"
LABEL="${2:?LABEL requerido — Uso: bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION]}"
RATE="${3:-500}"
DURATION="${4:-30s}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEVHUB_URL="${DEVHUB_URL:-http://localhost:3099}"
TARGET_URL="${TARGET_URL:-http://localhost:8080}"
TENANT="${TENANT_ID:-acme}"
ENDPOINT="${ENDPOINT:-/api/guides?filter[status][eq]=pending&per_page=20}"
K6_SCRIPT="${K6_SCRIPT:-tests/performance/sustained_2krps.js}"
SECRETS="${APPITOOLS_SECRETS:-/root/.appitools-secrets-dev}"
# A single warmup of the measurement length isn't enough to reach steady state on
# a small / shared host (the Postgres buffer cache and the pgx pool keep warming
# across the first runs, biasing run-1 slow). Warm longer than we measure.
WARMUP_DURATION="${WARMUP_DURATION:-45s}"

# Duration in whole seconds for the stored row (e.g. "30s" -> 30).
DURATION_S="$(grep -oE '^[0-9]+' <<<"$DURATION" || true)"
: "${DURATION_S:=0}"

# Mint a BENCH_TOKEN from the dev secrets if one wasn't provided.
if [ -z "${BENCH_TOKEN:-}" ]; then
  # shellcheck disable=SC1090
  [ -f "$SECRETS" ] && source "$SECRETS"
  if [ -z "${JWT_SECRET:-}" ]; then
    echo "ERROR: BENCH_TOKEN no seteado y no hay JWT_SECRET en $SECRETS" >&2
    exit 1
  fi
  BIN="./appitools-dev"; [ -x "$BIN" ] || BIN="./appitools"
  BENCH_TOKEN="$("$BIN" token --tenant "$TENANT" --secret "$JWT_SECRET" --role super_admin 2>/dev/null | tail -1)"
fi
export BENCH_TOKEN

# run_k6 <output.json> <logfile> — never aborts the protocol on a k6 non-zero
# exit (the SLO thresholds in the script use abortOnFail; here we only want the
# latency distribution, so a breach is logged, not fatal).
run_k6() {
  local out="$1" logf="$2" dur="${3:-$DURATION}" rc=0
  set +e
  RATE="$RATE" DURATION="$dur" TARGET_URL="$TARGET_URL" TENANT_ID="$TENANT" ENDPOINT="$ENDPOINT" \
    k6 run --quiet --out "json=$out" "$K6_SCRIPT" >"$logf" 2>&1
  rc=$?
  set -e
  return $rc
}

echo "▶ bench-protocol  RUNS=$RUNS  LABEL=$LABEL  RATE=$RATE  DURATION=$DURATION"
echo "  target=$TARGET_URL  endpoint=$ENDPOINT  devhub=$DEVHUB_URL"
echo "  [1] cooldown inicial 10s…"
sleep 10

echo "  [2] warmup run ${WARMUP_DURATION} (descartado)…"
run_k6 /tmp/bench-warmup.json /tmp/bench-warmup.k6log "$WARMUP_DURATION" || echo "      (warmup k6 rc≠0 — ignorado)"

RESULTS="/tmp/bench-results-$$.ndjson"
: >"$RESULTS"

for i in $(seq 1 "$RUNS"); do
  # Cooldown BETWEEN runs only (spec: "cooldown 15s entre cada uno"). A cooldown
  # before run 1 would let the box cool right after warmup and bias run 1 slow.
  if [ "$i" -gt 1 ]; then
    echo "  [3] cooldown 15s entre runs…"
    sleep 15
  fi
  out="/tmp/bench-$i.json"
  echo "      run $i/$RUNS → $out"
  run_k6 "$out" "/tmp/bench-$i.k6log" || echo "      (k6 rc≠0: threshold/abort — importo lo recolectado)"
  resp="$(curl -s -X POST "$DEVHUB_URL/api/bench/import" -H 'Content-Type: application/json' \
        -d "{\"path\":\"$out\",\"label\":\"$LABEL#$i\",\"rps\":$RATE,\"duration\":$DURATION_S}")"
  echo "$resp" >>"$RESULTS"
  echo "      import: $resp"
done

echo ""
echo "── Resumen ─────────────────────────────────────────────"
python3 - "$RESULTS" <<'PY'
import json, sys
rows = []
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
    except json.JSONDecodeError:
        continue
    if "run_id" not in d:
        print("  ! import error:", d.get("error", d))
        continue
    rows.append(d)

if not rows:
    print("  (sin runs importados)")
    sys.exit(0)

print(f"  {'run_id':>6}  {'n':>7}  {'p50':>8}  {'p95':>8}  {'p99':>8}  {'cv%':>6}")
cvs = []
for d in rows:
    cv = d.get("cv", 0.0) or 0.0
    cvs.append(cv)
    print(f"  {d['run_id']:>6}  {d.get('n_requests',0):>7}  "
          f"{d.get('p50_ms',0):>8.3f}  {d.get('p95_ms',0):>8.3f}  "
          f"{d.get('p99_ms',0):>8.3f}  {cv*100:>6.1f}")

mean_cv = sum(cvs) / len(cvs)
print(f"  CV promedio: {mean_cv*100:.1f}%  (runs: {len(rows)})")
if mean_cv > 0.15:
    print("  ⚠️  WARNING: CV promedio > 15% — benchmark INESTABLE; "
          "los resultados pueden no ser reproducibles (ruido del host?).")
PY
echo "────────────────────────────────────────────────────────"
echo "✓ protocolo completo. Comparar con: "
echo "  curl -s -X POST $DEVHUB_URL/api/bench/compare -H 'Content-Type: application/json' -d '{\"run_a\":A,\"run_b\":B}'"
