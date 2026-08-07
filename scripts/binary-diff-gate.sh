#!/usr/bin/env bash
# binary-diff-gate.sh — behavioral diff of two engine binaries over a paired
# request corpus. THE GATE `make test` IS NOT.
#
# WHY THIS EXISTS. On 2026-08-01 a commit shipped with a create-path type check
# that rejected a value the ENGINE ITSELF injects (`default: "now"` → a Go
# time.Time), so every POST that omitted the field answered 422 blaming the
# caller for a field they never sent — with `make test` green, because the test
# covering it is DB-backed and the -short unit lane skips it. Two of that
# commit's four defects were found not by reading code but by DIFFING the old
# binary against the new one over ~340 paired requests. This script is that
# technique, made repo infrastructure:
#
#   1. boots BASE and NEW on high ports with their own scratch databases,
#   2. registers the same tenant + seeds the same rows in both,
#   3. fires every corpus request at both,
#   4. reports EVERY behavioral difference (status, headers, normalized body).
#
# A reported DIFF is not automatically a bug — a session that intentionally
# changes a contract EXPECTS diffs. The rule is: every diff is READ and
# EXPLAINED, case by case, in the session report. An unexplained diff is a
# defect by definition. Exit code: 0 = no diffs, 1 = diffs (go explain them),
# 2 = harness failure.
#
# USAGE
#   scripts/binary-diff-gate.sh <base-binary> <new-binary> [corpus.jsonl]
#
#   DATABASE_URL   required — a Postgres superuser URL; the gate creates and
#                  drops its own scratch databases (appximo_bdg_base/_new).
#   BDG_PORT_BASE / BDG_PORT_NEW    data ports (default 8501 / 8502)
#   BDG_CTRL_BASE / BDG_CTRL_NEW    control ports (default 9501 / 9502)
#   BDG_KEEP=1     keep the scratch databases after the run (debugging)
#
# The corpus is DATA (scripts/binary-diff/corpus.jsonl), one JSON object per
# line — extend it there, not here. Fields: name, method, path (may contain
# {{ID}} = the seeded note's id), auth (admin|viewer|ghost|none), authstyle
# (lowercase|basic, optional), host (optional override), body (optional JSON),
# expect (prose: the CURRENT contract, for the human reading a diff).
#
# SAFETY: this script kills ONLY the PIDs it started (never by port or name),
# and refuses to start if its ports are already listening.
set -euo pipefail

die() { echo "binary-diff-gate: $*" >&2; exit 2; }

BASE_BIN=${1:-} ; NEW_BIN=${2:-}
[ -x "$BASE_BIN" ] && [ -x "$NEW_BIN" ] || die "usage: binary-diff-gate.sh <base-binary> <new-binary> [corpus.jsonl] (both must be executable)"
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
CORPUS=${3:-$SCRIPT_DIR/binary-diff/corpus.jsonl}
SCHEMA=${BDG_SCHEMA:-$SCRIPT_DIR/binary-diff/schema.json}
[ -f "$CORPUS" ] || die "corpus not found: $CORPUS"
[ -f "$SCHEMA" ] || die "schema not found: $SCHEMA"
command -v jq >/dev/null || die "jq is required"
[ -n "${DATABASE_URL:-}" ] || die "DATABASE_URL must be set (superuser; scratch DBs are created/dropped)"

PORT_BASE=${BDG_PORT_BASE:-8501}; PORT_NEW=${BDG_PORT_NEW:-8502}
CTRL_BASE=${BDG_CTRL_BASE:-9501}; CTRL_NEW=${BDG_CTRL_NEW:-9502}
JWT_SECRET_GATE="binary-diff-gate-dev-secret-0123456789abcdef"
ADMIN_KEY_GATE="bdg-admin"
TENANT="bdg"
HOST_DEFAULT="$TENANT.localhost"

for p in "$PORT_BASE" "$PORT_NEW" "$CTRL_BASE" "$CTRL_NEW"; do
  if ss -ltn 2>/dev/null | awk '{print $4}' | grep -q ":$p\$"; then
    die "port $p is already listening — refuse to start (never reuse a busy port)"
  fi
done

# ── scratch databases ─────────────────────────────────────────────────────────
# Run SQL through psql if available, else through the dev Postgres container.
PG_ADMIN_URL=$(echo "$DATABASE_URL" | sed -E 's#(/)[^/?]+(\?|$)#\1postgres\2#')
sql() {
  if command -v psql >/dev/null; then
    psql "$PG_ADMIN_URL" -qAtc "$1"
  else
    local user; user=$(echo "$DATABASE_URL" | sed -E 's#.*//([^:]+):.*#\1#')
    docker exec "${BDG_PG_CONTAINER:-appitools-pg}" psql -U "$user" -d postgres -qAtc "$1"
  fi
}
db_url_for() { echo "$DATABASE_URL" | sed -E "s#(/)[^/?]+(\?|\$)#\1$1\2#"; }

PIDS=()
cleanup() {
  for pid in "${PIDS[@]:-}"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  if [ "${BDG_KEEP:-0}" != "1" ]; then
    sql "DROP DATABASE IF EXISTS appximo_bdg_base" >/dev/null 2>&1 || true
    sql "DROP DATABASE IF EXISTS appximo_bdg_new"  >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

WORK=$(mktemp -d)

# ── boot one side ─────────────────────────────────────────────────────────────
boot() { # $1=side $2=binary $3=port $4=ctrl
  local side=$1 bin=$2 port=$3 ctrl=$4 dbname="appximo_bdg_$1"
  sql "DROP DATABASE IF EXISTS $dbname" >/dev/null
  sql "CREATE DATABASE $dbname" >/dev/null
  DATABASE_URL="$(db_url_for "$dbname")" JWT_SECRET="$JWT_SECRET_GATE" ADMIN_KEY="$ADMIN_KEY_GATE" \
    APPXIMO_CONTROL_PORT="$ctrl" APPXIMO_ENV="" \
    "$bin" serve --schema "$SCHEMA" --port "$port" >"$WORK/$side.log" 2>&1 &
  PIDS+=($!)
  for _ in $(seq 1 100); do
    curl -sf "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 && break
    sleep 0.2
  done
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null || { tail -20 "$WORK/$side.log" >&2; die "$side did not become healthy"; }

  # Same tenant, same seed, both sides.
  curl -sf -X POST "http://127.0.0.1:$ctrl/tenants" \
    -H "X-Admin-Key: $ADMIN_KEY_GATE" -H "Content-Type: application/json" \
    -d "{\"tenant_id\":\"$TENANT\",\"display_name\":\"BDG\",\"email\":\"bdg@example.com\",\"plan\":\"free\",\"schema\":$(cat "$SCHEMA")}" \
    >/dev/null || { tail -20 "$WORK/$side.log" >&2; die "$side: tenant registration failed"; }

  # An author with a FIXED login uuid — the notes.author_login FK declares
  # `references: login` (PUBLIC-SURFACE-S1 Part D), so the include/subroute
  # corpus rows exercise a non-id references join with real data.
  curl -sf -X POST "http://127.0.0.1:$port/api/authors" \
    -H "Authorization: Bearer $TOKEN_ADMIN" -H "Host: $HOST_DEFAULT" -H "Content-Type: application/json" \
    -d '{"name":"seed author","login":"44444444-4444-4444-4444-444444444444"}' >/dev/null \
    || { tail -20 "$WORK/$side.log" >&2; die "$side: author seeding failed"; }

  local seed
  seed=$(curl -s -X POST "http://127.0.0.1:$port/api/notes" \
    -H "Authorization: Bearer $TOKEN_ADMIN" -H "Host: $HOST_DEFAULT" -H "Content-Type: application/json" \
    -d '{"title":"seed","amount":10,"ratio":1.5,"done":true,"code":"C1","attrs":{"k":"v"},"author_login":"44444444-4444-4444-4444-444444444444"}')
  echo "$seed" | jq -er '.id // .data.id' >"$WORK/$side.id" 2>/dev/null \
    || { echo "seed response: $seed" >&2; die "$side: seeding failed"; }
}

# Tokens: same secret on both sides → one mint serves both. `ghost` is a role
# NO schema role declares (the ENG-27 corpus rows).
TOKEN_ADMIN=$("$BASE_BIN" token --secret "$JWT_SECRET_GATE" --tenant "$TENANT" --role admin --user-id 11111111-1111-1111-1111-111111111111 2>/dev/null | tail -1)
TOKEN_VIEWER=$("$BASE_BIN" token --secret "$JWT_SECRET_GATE" --tenant "$TENANT" --role viewer --user-id 22222222-2222-2222-2222-222222222222 2>/dev/null | tail -1)
TOKEN_GHOST=$("$BASE_BIN" token --secret "$JWT_SECRET_GATE" --tenant "$TENANT" --role ghost_role --user-id 33333333-3333-3333-3333-333333333333 2>/dev/null | tail -1)

echo "── booting base ($BASE_BIN) :$PORT_BASE and new ($NEW_BIN) :$PORT_NEW"
boot base "$BASE_BIN" "$PORT_BASE" "$CTRL_BASE"
boot new  "$NEW_BIN"  "$PORT_NEW"  "$CTRL_NEW"
ID_BASE=$(cat "$WORK/base.id"); ID_NEW=$(cat "$WORK/new.id")

# ── hot migration (M1) ────────────────────────────────────────────────────────
# Deploy a v2 schema (one new column, `hotcol`) to the SAME tenant on both sides
# WITHOUT restarting, then poll until the hot WRITE path serves it (ENG-12 — both
# binaries have it). The hot-migrated-* corpus rows then pin whether a deployed
# column filters/sorts/searches/aggregates live (M1). NOTE: rows whose 400 body
# prints an "(available: …)" field list will legitimately DIFF across the M1
# change — the list is now the tenant's deployed truth; explain them as such.
jq '.resources.notes.fields.hotcol = {"type":"string"}' "$SCHEMA" >"$WORK/schema-hot.json"
hotmigrate() { # $1=side $2=port $3=ctrl $4=seeded-id
  local side=$1 port=$2 ctrl=$3 id=$4 code
  curl -sf -X PUT "http://127.0.0.1:$ctrl/tenants/$TENANT/schema" \
    -H "X-Admin-Key: $ADMIN_KEY_GATE" -H "Content-Type: application/json" \
    -d "{\"schema\":$(cat "$WORK/schema-hot.json")}" >/dev/null \
    || die "$side: hot schema deploy failed"
  for _ in $(seq 1 150); do
    code=$(curl -s -o /dev/null -w '%{http_code}' -X PATCH "http://127.0.0.1:$port/api/notes/$id" \
      -H "Authorization: Bearer $TOKEN_ADMIN" -H "Host: $HOST_DEFAULT" -H "Content-Type: application/json" \
      -d '{"hotcol":"hot"}')
    [ "$code" = "200" ] && return 0
    sleep 0.2
  done
  die "$side: hot-migrated column never became writable (30s) — ENG-12 regression?"
}
echo "── hot-migrating both sides (M1 fixture: +notes.hotcol)"
hotmigrate base "$PORT_BASE" "$CTRL_BASE" "$ID_BASE"
hotmigrate new  "$PORT_NEW"  "$CTRL_NEW"  "$ID_NEW"

# ── normalization ─────────────────────────────────────────────────────────────
# Bodies: JSON is key-sorted and every uuid/timestamp replaced by a placeholder
# (seeded rows have different ids per instance — that is not a behavioral
# difference). A top-level `data` ARRAY is sorted after scrubbing: the default
# list order is `id ASC` over RANDOM uuids, so two correct instances return the
# same rows in different orders — sorting compares the SET, which is what the
# corpus can pin. (A case about ordering itself must construct a deterministic
# order, e.g. sort by a seeded column.) Headers: volatile ones excluded (Date,
# Content-Length/Transfer-Encoding track the unnormalized body, Etag is a
# content hash, trace/rate headers are per-run).
normalize_body() {
  jq -S 'walk(
    if type == "string" then
      gsub("[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"; "<uuid>")
      | gsub("[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9]{2}:[0-9]{2}:[0-9]{2}[0-9:.+Z-]*"; "<time>")
    else . end)
    | if (type == "object") and (.data | type == "array") then .data |= sort else . end' \
    2>/dev/null || cat
}
normalize_headers() {
  # cache-control and x-cache are excluded because the response cache emits them on a HIT and
  # not on a MISS, and hit-vs-miss is timing-dependent — measured flipping
  # between identical binaries across runs. A contract change to the header
  # itself would still show up in a dedicated corpus case, not as run noise.
  tr -d '\r' | awk 'NR>1 && NF' | tr 'A-Z' 'a-z' \
    | grep -Ev '^(date|content-length|transfer-encoding|etag|x-trace-id|x-ratelimit|traceparent|vary|cache-control|x-cache):' \
    | sort
}

# ── fire one request at one side ──────────────────────────────────────────────
fire() { # $1=port $2=seeded-id $3=case-json  → writes status/headers/body files under $4 prefix
  local port=$1 id=$2 c=$3 out=$4
  local method path auth style host body
  method=$(echo "$c" | jq -r '.method // "GET"')
  path=$(echo "$c" | jq -r '.path' | sed "s/{{ID}}/$id/g")
  auth=$(echo "$c" | jq -r '.auth // "admin"')
  style=$(echo "$c" | jq -r '.authstyle // ""')
  host=$(echo "$c" | jq -r '.host // ""'); [ -z "$host" ] && host="$HOST_DEFAULT"
  body=$(echo "$c" | jq -c 'if has("body") then .body else null end' | sed "s/{{ID}}/$id/g")

  # HEAD needs curl --head, never `-X HEAD`: with -X curl still WAITS for the
  # announced Content-Length body a HEAD response never carries, and hangs into
  # curl-error (a harness lesson, like the uuid-order and cache-marker ones —
  # found when the 405 handler gained a JSON body and HEAD started announcing
  # its length, which is RFC-correct).
  local -a args=( -s -g -o "$out.body" -D "$out.hdr" -w '%{http_code}'
                  -H "Host: $host" "http://127.0.0.1:$port$path" )
  if [ "$method" = "HEAD" ]; then
    args+=( --head )
  else
    args+=( -X "$method" )
  fi
  local tok=""
  case "$auth" in
    admin)  tok=$TOKEN_ADMIN ;;
    viewer) tok=$TOKEN_VIEWER ;;
    ghost)  tok=$TOKEN_GHOST ;;
    none)   tok="" ;;
  esac
  case "$style" in
    lowercase) [ -n "$tok" ] && args+=( -H "Authorization: bearer $tok" ) ;;
    basic)     args+=( -H "Authorization: Basic Zm9vOmJhcg==" ) ;;
    *)         [ -n "$tok" ] && args+=( -H "Authorization: Bearer $tok" ) ;;
  esac
  if [ "$body" != "null" ]; then
    local ct; ct=$(echo "$c" | jq -r '.ct // "application/json"')
    if [ "$(echo "$body" | jq -r 'type')" = "string" ]; then
      args+=( -H "Content-Type: $ct" --data-binary "$(echo "$body" | jq -r '.')" )
    else
      args+=( -H "Content-Type: $ct" --data-binary "$body" )
    fi
  fi
  curl "${args[@]}" >"$out.status" || echo "curl-error" >"$out.status"
}

# ── the loop ──────────────────────────────────────────────────────────────────
total=0; same=0; diff=0
DIFF_NAMES=()
while IFS= read -r line; do
  [ -z "$line" ] && continue
  echo "$line" | jq -e . >/dev/null 2>&1 || die "corpus line is not JSON: $line"
  name=$(echo "$line" | jq -r '.name')
  total=$((total+1))

  fire "$PORT_BASE" "$ID_BASE" "$line" "$WORK/b"
  fire "$PORT_NEW"  "$ID_NEW"  "$line" "$WORK/n"

  bs=$(cat "$WORK/b.status"); ns=$(cat "$WORK/n.status")
  # curl --head writes the header block as the OUTPUT (-o), so a HEAD case's
  # .body file IS a header dump — normalize it as headers, or the random
  # X-Trace-Id/Date inside it flags DIFF even between identical binaries
  # (measured: base-vs-base flagged head-generated-list before this).
  if [ "$(echo "$line" | jq -r '.method // "GET"')" = "HEAD" ]; then
    bb=$(normalize_headers <"$WORK/b.body"); nb=$(normalize_headers <"$WORK/n.body")
  else
    bb=$(normalize_body <"$WORK/b.body"); nb=$(normalize_body <"$WORK/n.body")
  fi
  bh=$(normalize_headers <"$WORK/b.hdr"); nh=$(normalize_headers <"$WORK/n.hdr")

  if [ "$bs" = "$ns" ] && [ "$bb" = "$nb" ] && [ "$bh" = "$nh" ]; then
    same=$((same+1))
    printf 'SAME  %-38s %s\n' "$name" "$bs"
  else
    diff=$((diff+1)); DIFF_NAMES+=("$name")
    printf 'DIFF  %-38s base=%s new=%s\n' "$name" "$bs" "$ns"
    if [ "$bb" != "$nb" ]; then
      printf '      base body: %s\n' "$(echo "$bb" | head -c 400)"
      printf '      new  body: %s\n' "$(echo "$nb" | head -c 400)"
    fi
    if [ "$bh" != "$nh" ]; then
      echo "      header diff:"
      diff <(echo "$bh") <(echo "$nh") | sed 's/^/        /' || true
    fi
    printf '      expect: %s\n' "$(echo "$line" | jq -r '.expect // "(no note)"')"
  fi
done <"$CORPUS"

echo
echo "── $total cases: $same same, $diff diff"
if [ "$diff" -gt 0 ]; then
  echo "── every DIFF above must be EXPLAINED in the session report (an unexplained diff is a defect):"
  printf '   - %s\n' "${DIFF_NAMES[@]}"
  exit 1
fi
