#!/usr/bin/env bash
# worker-e2e.sh — the WORKER-V1 milestone proof (ADR-016 §Class 2).
#
# Boots the engine + the outbox worker as TWO SEPARATE PROCESSES against one
# Postgres, POSTs a single event to /api/_echo, and shows it consumed by the
# worker — the first event that enters via a POST and leaves processed by a
# separate process. The engine only WRITES to public.outbox; the worker LISTENs
# on outbox_notify (wake-up hint) + polls (durable source of truth) and drains it.
#
# Requires: a reachable Postgres (DATABASE_URL) and Go on PATH. The control plane
# (:9090) is NOT used here — the tenant comes from the Host header and the outbox
# lives in the public schema, so no tenant registration is needed.
#
# Spin a throwaway Postgres if you don't have one:
#   docker run -d --name pg-e2e -e POSTGRES_USER=e2e -e POSTGRES_PASSWORD=e2e \
#     -e POSTGRES_DB=e2e -p 55433:5432 postgres:16-alpine
#   export DATABASE_URL='postgres://e2e:e2e@localhost:55433/e2e?sslmode=disable'
#
# For the precise enqueue→processed latency (sub-ms to single-digit ms via NOTIFY),
# query the row directly (psql or docker exec):
#   SELECT id, state, attempts,
#          round(EXTRACT(EPOCH FROM (sent_at - created_at))*1000, 2) AS ms
#   FROM public.outbox WHERE id = <event_id>;
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
export PATH="$PATH:/usr/local/go/bin:/root/go/bin"

PORT="${PORT:-8095}"
TENANT="${TENANT:-e2e}"
SCHEMA="${SCHEMA:-examples/quickstart/schema.json}"
DATABASE_URL="${DATABASE_URL:?set DATABASE_URL to a reachable Postgres}"
export DATABASE_URL
export JWT_SECRET="${JWT_SECRET:-a-very-long-test-secret-of-32plus-chars}"
export ADMIN_KEY="${ADMIN_KEY:-e2e-admin}"
export APPXIMO_SYNTHETIC=off

WORK="$(mktemp -d)"
ENGINE_PID="" WORKER_PID=""
cleanup() {
  [ -n "$WORKER_PID" ] && kill "$WORKER_PID" 2>/dev/null || true
  [ -n "$ENGINE_PID" ] && kill "$ENGINE_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> building engine + worker"
sh scripts/build-engine.sh "$WORK/appximo" >/dev/null 2>&1
go build -o "$WORK/appximo-worker" ./cmd/appximo-worker

echo "==> starting engine on :$PORT (control plane :9090 unused here)"
"$WORK/appximo" serve --schema "$SCHEMA" --port "$PORT" >"$WORK/engine.log" 2>&1 &
ENGINE_PID=$!

echo "==> starting worker (separate process)"
"$WORK/appximo-worker" >"$WORK/worker.log" 2>&1 &
WORKER_PID=$!

echo -n "==> waiting for /health "
for _ in $(seq 1 30); do
  if curl -fsS "http://localhost:$PORT/health" >/dev/null 2>&1; then echo "ok"; break; fi
  echo -n "."; sleep 1
done

TOKEN="$("$WORK/appximo" token --secret "$JWT_SECRET" --tenant "$TENANT" --role admin 2>/dev/null | tail -1)"

echo "==> POST /api/_echo (Host: $TENANT.localhost)"
t0=$(date +%s.%N)
RESP="$(curl -fsS -X POST "http://localhost:$PORT/api/_echo" \
  -H "Host: $TENANT.localhost" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"msg":"worker-e2e milestone"}')"
echo "    response: $RESP"
EVENT_ID="$(echo "$RESP" | sed 's/[^0-9]//g')"
[ -n "$EVENT_ID" ] || { echo "FAIL: no event_id in response"; exit 1; }

echo -n "==> waiting for the worker process to consume event $EVENT_ID "
t1=""
for _ in $(seq 1 100); do
  if grep "processed outbox event" "$WORK/worker.log" 2>/dev/null | grep -q "\"id\":$EVENT_ID,"; then
    t1=$(date +%s.%N); echo "ok"; break
  fi
  echo -n "."; sleep 0.05
done

if [ -z "$t1" ]; then
  echo "FAIL: worker did not process event $EVENT_ID"
  echo "--- worker.log ---"; cat "$WORK/worker.log"
  exit 1
fi

echo
echo "MILESTONE PASSED — Class 2 async loop is live:"
echo "  event $EVENT_ID: POST (engine) -> public.outbox -> NOTIFY -> worker (separate process) -> sent"
echo "  end-to-end (POST -> worker log line): $(awk "BEGIN{printf \"%.0f\", ($t1-$t0)*1000}") ms (shell wall-clock, upper bound)"
echo "  worker log: $(grep 'processed outbox event' "$WORK/worker.log" | grep "\"id\":$EVENT_ID," )"
