#!/bin/bash
# Sweep open-model: mide latencia bajo carga controlada (no VUs ilimitados)
# Uso: TARGET=appximo ./sweep.sh   o   TARGET=nestjs ./sweep.sh
set -euo pipefail

cd "$(dirname "$0")"

TARGET_SVC="${TARGET:-appximo}"

if [ "$TARGET_SVC" = "nestjs" ]; then
  TARGET_URL="http://localhost:3000"
  # NestJS solo verifica presencia del header — cualquier token sirve
  BENCH_TOKEN="bench"
else
  TARGET_URL="http://localhost:8081"
  # Appximo valida firma JWT — generar token fresco
  BENCH_TOKEN=$(docker run --rm appximo-bench:latest token \
    --tenant 10 --role super_admin --user-id bench-user \
    --secret "${JWT_SECRET:-benchsecret}")
fi

echo "=== SWEEP OPEN-MODEL — $TARGET_SVC ($TARGET_URL) ==="
echo ""

for rate in 50 100 150 200 220 240; do
  echo "--- ${rate} RPS ---"
  RATE=$rate \
  TARGET_URL=$TARGET_URL \
  TENANT_ID=10 \
  BENCH_TOKEN=$BENCH_TOKEN \
    k6 run k6-load.js 2>&1 | grep -E '"rate"|"p50"|"p95"|"p99"|"errors"' || true
  sleep 15
done
