#!/usr/bin/env bash
# run-pub-bench.sh — S46 public comparative benchmark driver (load ladder).
#
# Runs the load ladder for ONE stack window (the two stacks must NEVER take
# load simultaneously — they share the SUT's 2 vCPUs):
#
#   levels: 250 500 1000 1500 2000 2500 RPS
#   per level: 1 warmup (45s, discarded) + 3 runs x 30s, 20s cooldown between
#   saturation: median p95 of the level > 100ms OR median error_rate > 1%
#               -> stop climbing for this stack
#
# Results import into the DevHub statistics store on the load-generator box
# with labels  pub-<stack>-<level>#<run>.
#
# Usage:
#   BENCH_TOKEN=<jwt> bash benchmark-lab/run-pub-bench.sh appximo http://SUT:8080
#   BENCH_TOKEN=<jwt> bash benchmark-lab/run-pub-bench.sh nestjs    http://SUT:3000
#
# The load generator must be a DIFFERENT machine than the SUT (PRIMER
# methodology: the loader never competes for CPU with the system under test).
set -euo pipefail

STACK="${1:?Uso: run-pub-bench.sh <appximo|nestjs> <TARGET_URL>}"
TARGET="${2:?TARGET_URL requerido}"
LEVELS="${LEVELS:-250 500 1000 1500 2000 2500}"
DEVHUB_URL="${DEVHUB_URL:-http://localhost:3099}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

ENDPOINT='/api/guides?filter[status]=pending&sort=created_at&order=desc&per_page=20'

for L in $LEVELS; do
  echo "════ $STACK @ $L RPS ════"
  COOLDOWN_S=20 TARGET_URL="$TARGET" ENDPOINT="$ENDPOINT" DEVHUB_URL="$DEVHUB_URL" \
    bash scripts/bench-protocol.sh 3 "pub-$STACK-$L" "$L" 30s benchmark-lab/k6-pub.js

  # Saturation gate: median p95 > 100ms or median error_rate > 1% over the
  # 3 runs of this level.
  SAT=$(curl -sg "$DEVHUB_URL/api/bench/export?prefix=pub-$STACK-$L#" | python3 -c '
import csv, statistics, sys
rows = list(csv.DictReader(sys.stdin))
if not rows: print("no-data"); sys.exit(0)
p95 = statistics.median(float(r["p95_ms"]) for r in rows)
err = statistics.median(float(r["error_rate"]) for r in rows)
print("saturated" if (p95 > 100 or err > 0.01) else "ok", round(p95,2), round(err,4))
')
  echo "── nivel $L: $SAT"
  case "$SAT" in saturated*) echo "── $STACK saturó en $L RPS — fin de la escalera"; break;; esac
done
