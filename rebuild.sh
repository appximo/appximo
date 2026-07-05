#!/bin/bash
set -e
cd /root/appitools
export PATH=$PATH:/usr/local/go/bin

echo "→ Compilando UI..."
(cd pkg/adminui/web && npm run build)

echo "→ Compilando motor..."
go build -o appitools-dev ./cmd/appitools

echo "→ Matando motor viejo..."
pkill -x appitools-dev 2>/dev/null || true
for i in $(seq 1 10); do
  ss -ltn | grep -q ':8080' || break
  sleep 1
done

echo "→ Arrancando motor nuevo..."
set -a; source /root/.appitools-secrets-dev; set +a
nohup ./appitools-dev serve --schema examples/quickstart/schema.json --port 8080 > /tmp/appitools.log 2>&1 &
sleep 2

if ss -ltn | grep -q ':8080'; then
  echo "✓ Motor arriba en :8080 — refrescá el navegador"
else
  echo "✗ Falló — revisá: tail /tmp/appitools.log"
fi
