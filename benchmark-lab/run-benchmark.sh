#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"
mkdir -p results

wait_http_200() {
  local url="$1" label="$2" timeout="${3:-120}"
  local elapsed=0
  echo -n "Esperando $label"
  until curl -sf -o /dev/null "$url" 2>/dev/null; do
    sleep 2; elapsed=$((elapsed + 2)); echo -n "."
    [ "$elapsed" -ge "$timeout" ] && { echo " TIMEOUT (continuando)"; return 0; }
  done
  echo " listo."
}

wait_nestjs() {
  local timeout="${1:-120}"
  local elapsed=0
  echo -n "Esperando NestJS :3000/api/guides"
  until curl -sf -o /dev/null \
    -H "Authorization: Bearer bench" \
    -H "X-Tenant-ID: tenant_10" \
    "http://localhost:3000/api/guides?per_page=1" 2>/dev/null; do
    sleep 2; elapsed=$((elapsed + 2)); echo -n "."
    [ "$elapsed" -ge "$timeout" ] && { echo " TIMEOUT (continuando)"; return 0; }
  done
  echo " listo."
}

echo "======================================="
echo "APPITOOLS vs NestJS — Benchmark Justo"
echo "======================================="

echo ""
echo "=== FASE 0: Levantar Postgres + seed ==="
docker compose up postgres -d
echo "Esperando healthcheck (seed de ~1.1M registros puede tardar 60s)..."
until docker compose ps postgres 2>/dev/null | grep -q "healthy"; do
  sleep 3
done
echo "Postgres healthy. Estabilizando 8s..."
sleep 8

TENANT_ID="10"

echo ""
echo "=== Generando JWT firmado para tenant $TENANT_ID ==="
BENCH_TOKEN=$(docker run --rm appitools-bench:latest token \
  --tenant "$TENANT_ID" --role super_admin --user-id bench-user --secret benchsecret)
echo "Token OK: ${BENCH_TOKEN:0:40}..."

echo ""
echo "=== RONDA 1: Appitools ==="
docker compose --profile appitools up -d
wait_http_200 "http://localhost:8081/health" "Appitools :8081/health"

TARGET_URL=http://localhost:8081 TENANT_ID=$TENANT_ID BENCH_TOKEN=$BENCH_TOKEN \
  k6 run k6-load.js > results/appitools-results.json || true

docker compose --profile appitools down
echo "Pausa de 30s para liberar recursos..."
sleep 30

echo ""
echo "=== RONDA 2: NestJS ==="
docker compose --profile nestjs up -d
wait_nestjs 120
sleep 5

TARGET_URL=http://localhost:3000 TENANT_ID=$TENANT_ID BENCH_TOKEN=$BENCH_TOKEN \
  k6 run k6-load.js > results/nestjs-results.json || true

docker compose --profile nestjs down

echo ""
echo "======================================="
echo "=== RESULTADOS FINALES ==="
echo "======================================="
echo ""
echo "--- Appitools ---"
python3 -c "
import json, sys
c = open('results/appitools-results.json').read()
j = json.loads(c[c.rfind('{'):c.rfind('}')+1])
print(json.dumps(j, indent=2))
" 2>/dev/null || echo "(sin resultados)"

echo ""
echo "--- NestJS ---"
python3 -c "
import json, sys
c = open('results/nestjs-results.json').read()
j = json.loads(c[c.rfind('{'):c.rfind('}')+1])
print(json.dumps(j, indent=2))
" 2>/dev/null || echo "(sin resultados)"
