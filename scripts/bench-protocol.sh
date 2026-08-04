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
# Usage: bash scripts/bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION] [SCRIPT]
#   SCRIPT: k6 script path (default tests/performance/sustained_2krps.js;
#           e.g. tests/performance/sustained_writes.js for the write path)
# Env overrides: DEVHUB_URL TARGET_URL TENANT_ID ENDPOINT BENCH_TOKEN K6_SCRIPT
#                APPXIMO_SECRETS WARMUP_DURATION
#
# NOTE on constrained / shared hosts (e.g. the 1-vCPU dev box): keep RATE low
# (≤100). Co-locating the k6 load generator with the engine on a single core
# injects scheduling noise that the (correctly powerful) Mann-Whitney test then
# flags as false "significance" between two identical runs. At RATE=50 the runs
# are reproducible and same-system comparisons land on no_change as they should.
#
# ── THE CANONICAL BASELINE (OPS-9, AI-JOURNEY-S1) ────────────────────────────
# The comparable series every future session measures against:
#   engine:   make dev-fast          (serves examples/blank/schema.json on :8080)
#   tenant:   benchblank            (DEDICATED, permanent — never piggyback on
#                                     acme/nimbus, whose tables belong to other
#                                     fixtures; create it once:
#                                     curl -X POST localhost:9090/tenants -H "X-Admin-Key: $ADMIN_KEY" \
#                                       -d '{"tenant_id":"benchblank","display_name":"Bench","email":"b@x.co","plan":"free","schema":'"$(cat examples/blank/schema.json)"'}' )
#   endpoint: AUTO-DERIVED from the served /openapi.json → /api/items?per_page=20
#   rate:     100 rps · 30 s · RUNS=10   (the shared 105 is unreliable above ~100)
#   role:     admin                  (BENCH_ROLE to override)
# Series anchor (re-run 2026-08-01, AI-JOURNEY-S1, this exact fixture at 100 rps):
#   p50 ≈ 0.60–0.66 ms · base-vs-base control: Δp50 +0.5 %, MWU p=0.73 (arm vs
#   arm) · between-run CV WITHIN an arm 8.7–10.4 % — the 105's real floor. A
#   delta needs to clear BOTH the max(0.5ms,3%) gate and that floor to be code. The old default endpoint
#   (/api/guides, role super_admin) was a RETIRED fixture: 100 % errors, zero
#   datapoints, twice. Defaults now derive from the SERVED surface and the
#   pre-flight refuses to measure a broken endpoint, loudly.
set -euo pipefail

RUNS="${1:?Uso: bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION] [SCRIPT]}"
LABEL="${2:?LABEL requerido — Uso: bench-protocol.sh <RUNS> <LABEL> [RATE] [DURATION] [SCRIPT]}"
RATE="${3:-100}"
DURATION="${4:-30s}"
SCRIPT_ARG="${5:-}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

DEVHUB_URL="${DEVHUB_URL:-http://localhost:3099}"
TARGET_URL="${TARGET_URL:-http://localhost:8080}"
TENANT="${TENANT_ID:-benchblank}"
BENCH_ROLE="${BENCH_ROLE:-admin}"
# ENDPOINT: derived from the SERVED surface (first /api/{resource} in
# /openapi.json) unless overridden — never a hardcoded fixture (OPS-9).
if [ -z "${ENDPOINT:-}" ]; then
  FIRST_RES="$(curl -fsS -m 5 "$TARGET_URL/openapi.json" 2>/dev/null     | python3 -c "import json,sys;paths=json.load(sys.stdin).get('paths',{});cands=sorted(p.split('/')[2] for p in paths if p.startswith('/api/') and p.count('/')==2 and '{' not in p and p not in ('/api/files','/api/transaction'));print(cands[0] if cands else '')" 2>/dev/null || true)"
  if [ -z "$FIRST_RES" ]; then
    echo "ERROR: no pude derivar un endpoint de $TARGET_URL/openapi.json (¿el motor está corriendo?)." >&2
    echo "  Levantalo (make dev-fast) o pasá ENDPOINT='/api/<recurso>?per_page=20' explícito." >&2
    exit 1
  fi
  ENDPOINT="/api/${FIRST_RES}?per_page=20"
fi
# 5th positional arg wins over the K6_SCRIPT env var; default is the read bench.
K6_SCRIPT="${SCRIPT_ARG:-${K6_SCRIPT:-tests/performance/sustained_2krps.js}}"
if [ ! -f "$K6_SCRIPT" ]; then
  echo "ERROR: k6 script no existe: $K6_SCRIPT" >&2
  exit 1
fi
SECRETS="${APPXIMO_SECRETS:-.env.dev}"
# A single warmup of the measurement length isn't enough to reach steady state on
# a small / shared host (the Postgres buffer cache and the pgx pool keep warming
# across the first runs, biasing run-1 slow). Warm longer than we measure.
WARMUP_DURATION="${WARMUP_DURATION:-45s}"
# Seconds to idle between measurement runs (S42 protocol used 15; the S46
# public benchmark uses 20).
COOLDOWN_S="${COOLDOWN_S:-15}"

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
  BIN="./appximo-dev"
  [ -x "$BIN" ] || BIN="./appximo"
  if [ ! -x "$BIN" ]; then
    echo "ERROR: no hay binario para mintear el token (./appximo-dev ni ./appximo)." >&2
    echo "  Compilá uno (go build -o appximo ./cmd/appximo) o pasá BENCH_TOKEN directo." >&2
    exit 1
  fi
  BENCH_TOKEN="$("$BIN" token --tenant "$TENANT" --secret "$JWT_SECRET" --role "$BENCH_ROLE" 2>/dev/null | tail -1)"
fi
export BENCH_TOKEN

# ── Fixture self-heal (OPS-9, CERTIFY-S1) ───────────────────────────────────
# The canonical baseline tenant has now been deleted TWICE by routine session
# cleanups, and each time the historical series was cut: the run before and the
# run after are not comparable because the fixture in between was recreated by
# hand, differently. A documented "do not delete this tenant" rule already
# existed and did not survive contact with reality.
#
# So the durable fix is not a rule, it is idempotence: if the bench tenant is
# missing, recreate it — from the schema the TARGET ENGINE ACTUALLY SERVES
# (GET /editor/current-schema, the byte-faithful boot schema), so the fixture is
# correct by construction for whatever app is under test, not just the blank one.
#
# Deliberately conservative: it only ever CREATES a missing tenant. It never
# migrates, never deletes, never touches an existing one — a bench script must
# not be able to mutate a real app. Set BENCH_NO_AUTOFIX=1 to disable.
ensure_bench_tenant() {
  [ "${BENCH_NO_AUTOFIX:-0}" = "1" ] && return 0
  local control="${CONTROL_URL:-http://localhost:9090}"
  # shellcheck disable=SC1090
  [ -z "${ADMIN_KEY:-}" ] && [ -f "$SECRETS" ] && source "$SECRETS"
  [ -z "${ADMIN_KEY:-}" ] && return 0   # no admin key → cannot check or create

  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$control/tenants/$TENANT" \
          -H "X-Admin-Key: $ADMIN_KEY" 2>/dev/null || echo 000)"
  case "$code" in
    200) return 0 ;;                       # fixture present — nothing to do
    404) : ;;                              # missing → recreate below
    *)   return 0 ;;                       # no reachable control plane → let the pre-flight speak
  esac

  echo "  [0] fixture ausente: el tenant \"$TENANT\" no existe — recreándolo (OPS-9 auto-heal)…"
  local schema
  schema="$(curl -fsS -m 5 "$TARGET_URL/editor/current-schema" 2>/dev/null || true)"
  if [ -z "$schema" ]; then
    echo "      ✗ no pude leer el schema servido ($TARGET_URL/editor/current-schema)." >&2
    echo "        Creá el tenant a mano o pasá TENANT_ID= de uno existente." >&2
    return 0
  fi
  local body resp
  body="$(TENANT="$TENANT" python3 -c '
import json,os,sys
print(json.dumps({"tenant_id":os.environ["TENANT"],"display_name":"Bench baseline",
                  "email":"bench@example.com","plan":"free",
                  "schema":json.loads(sys.stdin.read())}))' <<<"$schema")"
  resp="$(curl -s -X POST "$control/tenants" -H "X-Admin-Key: $ADMIN_KEY" \
          -H 'Content-Type: application/json' -d "$body")"
  if grep -q '"pg_schema"' <<<"$resp"; then
    echo "      ✓ tenant \"$TENANT\" recreado desde el schema servido — la serie histórica sigue siendo comparable"
  else
    echo "      ✗ no se pudo recrear: $(head -c 200 <<<"$resp")" >&2
  fi
}
ensure_bench_tenant

# Pre-flight (OPS-9): ONE real request before measuring anything. A broken
# endpoint used to produce 10 silent runs of 100% errors and "no datapoints".
PF_CODE="$(curl -gs -o /tmp/bench-preflight.body -w '%{http_code}' -m 5   "$TARGET_URL$ENDPOINT" -H "Authorization: Bearer $BENCH_TOKEN" -H "Host: ${TENANT}.localhost")"
case "$PF_CODE" in
  2*) : ;;
  *)
    echo "ERROR: pre-flight HTTP $PF_CODE en $TARGET_URL$ENDPOINT (tenant=$TENANT rol=$BENCH_ROLE)" >&2
    echo "  body: $(head -c 200 /tmp/bench-preflight.body)" >&2
    echo "  Ajustá: ENDPOINT= (recurso que la app SIRVE) · TENANT_ID= (tenant existente)" >&2
    echo "          BENCH_ROLE= (rol declarado en el schema servido) · o BENCH_TOKEN= directo" >&2
    exit 1 ;;
esac
echo "  pre-flight OK: $ENDPOINT → $PF_CODE (tenant=$TENANT rol=$BENCH_ROLE)"

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
echo "  script=$K6_SCRIPT"
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
    echo "  [3] cooldown ${COOLDOWN_S}s entre runs…"
    sleep "$COOLDOWN_S"
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
import json, sys, math
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
intra = []
p50s = []
for d in rows:
    cv = d.get("cv", 0.0) or 0.0
    intra.append(cv)
    p50s.append(d.get("p50_ms", 0.0) or 0.0)
    print(f"  {d['run_id']:>6}  {d.get('n_requests',0):>7}  "
          f"{d.get('p50_ms',0):>8.3f}  {d.get('p95_ms',0):>8.3f}  "
          f"{d.get('p99_ms',0):>8.3f}  {cv*100:>6.1f}")

# Intra-run CV: spread of the RAW latencies within a run. Always high under a
# heavy tail (p99 >> p50) — it does NOT measure protocol reproducibility, so it
# is reported for context only and does not gate the WARNING.
mean_intra = sum(intra) / len(intra)
print(f"  CV intra-run promedio: {mean_intra*100:.1f}%  (normal, cola pesada)")

# Between-run CV: spread of the per-run p50 medians ACROSS the N runs. THIS is
# the reproducibility signal — if the runs agree on their central tendency it is
# low. Sample stddev (n-1). Threshold 5%. Needs >=2 runs.
if len(p50s) >= 2:
    m = sum(p50s) / len(p50s)
    var = sum((x - m) ** 2 for x in p50s) / (len(p50s) - 1)
    cv_between = (math.sqrt(var) / m) if m else 0.0
    verdict = "REPRODUCIBLE" if cv_between <= 0.05 else "INESTABLE"
    print(f"  CV entre-runs (p50): {cv_between*100:.1f}% — {verdict}  (runs: {len(rows)})")
    if cv_between > 0.05:
        print("  ⚠️  WARNING: CV entre-runs (p50) > 5% — los runs no concuerdan en su "
              "mediana; benchmark poco reproducible (ruido del host?).")
else:
    print(f"  CV entre-runs (p50): n/a (se necesita >=2 runs; hay {len(rows)})")
PY

# ── The historical measurement log (CERTIFY-S1) ──────────────────────────────
# Every protocol run appends ONE line to benchmarks/history.tsv: date, commit,
# label, fixture, rate, duration, runs, and the resulting p50/p95 medians. It
# exists because the project has twice published a number whose measurement
# conditions were reconstructed from memory afterwards — a series only exists if
# something writes it down at the moment of measuring.
#
# Append-only by contract: a wrong line is corrected by appending a new one, the
# same rule the schema history follows.
HIST_FILE="${BENCH_HISTORY:-$REPO_ROOT/benchmarks/history.tsv}"
mkdir -p "$(dirname "$HIST_FILE")"
if [ ! -f "$HIST_FILE" ]; then
  printf 'date\tcommit\tlabel\ttarget\ttenant\tendpoint\tscript\trate\tduration\truns\tp50_median_ms\tp95_median_ms\tcv_between_pct\terr_rate\tnote\n' >"$HIST_FILE"
fi
python3 - "$RESULTS" "$HIST_FILE" "$LABEL" "$TARGET_URL" "$TENANT" "$ENDPOINT" "$K6_SCRIPT" "$RATE" "$DURATION" "$RUNS" "${BENCH_NOTE:-}" <<'PY2'
import json, sys, math, subprocess, datetime
res, hist, label, target, tenant, endpoint, script, rate, dur, runs, note = sys.argv[1:12]
rows = []
for line in open(res):
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
    except json.JSONDecodeError:
        continue
    if "run_id" in d:
        rows.append(d)
if not rows:
    sys.exit(0)
def med(v):
    v = sorted(v)
    n = len(v)
    return v[n//2] if n % 2 else (v[n//2-1]+v[n//2])/2
p50 = med([r.get("p50_ms", 0.0) or 0.0 for r in rows])
p95 = med([r.get("p95_ms", 0.0) or 0.0 for r in rows])
errs = max((r.get("error_rate", 0.0) or 0.0) for r in rows)
ps = [r.get("p50_ms", 0.0) or 0.0 for r in rows]
cvb = 0.0
if len(ps) >= 2:
    m = sum(ps)/len(ps)
    cvb = (math.sqrt(sum((x-m)**2 for x in ps)/(len(ps)-1))/m*100) if m else 0.0
try:
    sha = subprocess.check_output(["git","rev-parse","--short","HEAD"], text=True).strip()
except Exception:
    sha = "unknown"
day = datetime.datetime.now().strftime("%Y-%m-%d")
with open(hist, "a") as f:
    f.write("\t".join([day, sha, label, target, tenant, endpoint, script, rate, dur,
                       str(len(rows)), f"{p50:.3f}", f"{p95:.3f}", f"{cvb:.1f}",
                       f"{errs:.4f}", note]) + "\n")
print(f"  ✓ registrado en {hist} ({day} {sha} {label}: p50 {p50:.3f} ms, p95 {p95:.3f} ms)")
PY2
echo "────────────────────────────────────────────────────────"
echo "✓ protocolo completo. Comparar con: "
echo "  curl -s -X POST $DEVHUB_URL/api/bench/compare -H 'Content-Type: application/json' -d '{\"run_a\":A,\"run_b\":B}'"
