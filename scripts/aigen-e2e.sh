#!/usr/bin/env bash
# ============================================================================
# Appximo — AI schema generation end-to-end proof (AI-F0-S3)
#
# Proves the democratization loop for FOUR real app archetypes:
#   describe (NL)  →  AI generates schema  →  validate --json  →  self-correct
#   →  VALID schema  →  provision a real tenant  →  CRUD works  →  cleanup
#
# Two modes, auto-selected:
#   • ANTHROPIC_API_KEY set  → LIVE generation: each description is sent through
#     `appximo ai-generate` (the real loop on the CHEAP model), and the script
#     records the ECONOMIC instrumentation (iterations, tokens, cost) — the data
#     that validates the thesis. The generated schema is then provisioned + CRUDed.
#   • no key                 → PROVISION-ONLY demo: uses the committed golden
#     schemas in examples/aigen/ (representative of valid generations) to prove the
#     provision → CRUD half live. The generation/economics half is clearly skipped.
#
# Routes are compiled at engine boot from --schema (the documented model: one
# schema per engine, multi-tenant within it), so the script boots the engine ONCE
# PER ARCHETYPE with that archetype's schema, then provisions a tenant and CRUDs.
#
# Usage:
#   export PATH=$PATH:/usr/local/go/bin
#   set -a; source .env.dev; set +a   # DATABASE_URL/JWT_SECRET/ADMIN_KEY
#   export ANTHROPIC_API_KEY=sk-ant-...                   # optional (enables live gen)
#   bash scripts/aigen-e2e.sh
# ============================================================================
set -u
cd "$(dirname "$0")/.."
G='\033[0;32m'; R='\033[0;31m'; Y='\033[1;33m'; N='\033[0m'
PASS=0; FAIL=0
ok()  { PASS=$((PASS+1)); echo -e "  ${G}✓${N} $1"; }
bad() { FAIL=$((FAIL+1)); echo -e "  ${R}✗${N} $1"; }

: "${DATABASE_URL:?source the dev secrets first (DATABASE_URL/JWT_SECRET/ADMIN_KEY)}"
: "${JWT_SECRET:?JWT_SECRET required}"
: "${ADMIN_KEY:?ADMIN_KEY required}"
BIN=${APPXIMO_BIN:-./appximo-dev}
[ -x "$BIN" ] || { echo "build first: go build -o appximo-dev ./cmd/appximo"; exit 1; }

LIVE=0
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then LIVE=1; fi
MODEL=${AIGEN_MODEL:-claude-haiku-4-5}
RUN=$(date +%s)

# psql for cleanup: derive user/db from DATABASE_URL (postgres://user:pass@host/db).
PG_USER=$(echo "$DATABASE_URL" | sed -E 's#^[a-z]+://([^:]+):.*#\1#')
PG_DB=$(echo "$DATABASE_URL" | sed -E 's#.*@[^/]+/([^?]+).*#\1#')

# archetype: name|tenant|primary_resource|create_json|description
# {RUN} in create_json is replaced with a per-run id so re-runs don't collide on
# unique columns (email/handle) — a 409 on a stale row is correct, not a failure.
ARCHE=(
  "optica|aigenoptica|clientes|{\"nombre\":\"Ana\",\"email\":\"ana{RUN}@x.com\"}|Un CRM para una óptica: clientes, citas, ventas, con roles admin y vendedor."
  "trello|aigenboard|proyectos|{\"nombre\":\"Lanzamiento {RUN}\"}|Una app de tareas tipo Trello: proyectos, tareas con estados, asignados."
  "ecommerce|aigenshop|productos|{\"nombre\":\"Camisa {RUN}\",\"precio\":19.9}|Un e-commerce: productos, categorías, órdenes con líneas, inventario."
  "social|aigensocial|usuarios|{\"handle\":\"ana_{RUN}\",\"nombre\":\"Ana\"}|Una red social simple: usuarios, posts, comentarios, likes."
)

stop_engine() {
  local pid
  pid=$(ss -ltnp 2>/dev/null | grep ':8080' | grep -o 'pid=[0-9]*' | head -1 | cut -d= -f2)
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  for _ in $(seq 1 10); do ss -ltn | grep -q ':8080' || break; sleep 1; done
}
boot_engine() { # $1 = schema file
  nohup "$BIN" serve --schema "$1" --port 8080 >/tmp/aigen-e2e-engine.log 2>&1 &
  for _ in $(seq 1 15); do ss -ltn | grep -q ':8080' && return 0; sleep 1; done
  echo "engine failed to boot"; tail /tmp/aigen-e2e-engine.log; return 1
}

declare -a SUMMARY
echo "═══ AI schema generation e2e — mode: $([ $LIVE -eq 1 ] && echo "LIVE ($MODEL)" || echo "PROVISION-ONLY (no API key)") ═══"

for entry in "${ARCHE[@]}"; do
  IFS='|' read -r NAME TENANT RES CREATE DESC <<< "$entry"
  echo; echo "── $NAME ──────────────────────────────"
  SCHEMA_FILE="examples/aigen/$NAME.json"
  METRIC="(golden schema; live gen skipped — no API key)"

  if [ $LIVE -eq 1 ]; then
    echo "  describe: $DESC"
    GENJSON=/tmp/aigen-$NAME.result.json
    if "$BIN" ai-generate "$DESC" --model "$MODEL" --json >"$GENJSON" 2>/tmp/aigen-$NAME.err; then
      conv=$(python3 -c "import json;print(json.load(open('$GENJSON'))['converged'])" 2>/dev/null)
    else
      conv=$(python3 -c "import json;print(json.load(open('$GENJSON'))['converged'])" 2>/dev/null || echo False)
    fi
    if [ "$conv" = "True" ]; then
      python3 -c "import json;d=json.load(open('$GENJSON'));open('/tmp/aigen-$NAME.schema.json','w').write(json.dumps(d['schema']))" 2>/dev/null
      SCHEMA_FILE=/tmp/aigen-$NAME.schema.json
      read -r IT FT TOK COST <<< "$(python3 -c "import json;d=json.load(open('$GENJSON'));print(d['iterations'],d['first_try'],d['usage']['input_tokens']+d['usage']['output_tokens'],round(d['cost_usd'],5))")"
      ok "generado y VÁLIDO — iteraciones=$IT primera=$FT tokens=$TOK costo=\$$COST"
      METRIC="iter=$IT first=$FT tok=$TOK cost=\$$COST"
    else
      bad "NO convergió en el budget — usando golden schema para el resto del e2e"
    fi
  fi

  # provision + CRUD against an engine booted with THIS schema
  stop_engine
  if ! boot_engine "$SCHEMA_FILE"; then bad "$NAME: engine no arrancó"; SUMMARY+=("$NAME: ENGINE FAIL"); continue; fi
  SCH=$(cat "$SCHEMA_FILE")
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST localhost:9090/tenants \
    -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
    -d "{\"tenant_id\":\"$TENANT\",\"display_name\":\"$NAME\",\"email\":\"$NAME@x.com\",\"plan\":\"free\",\"schema\":$SCH}")
  case "$code" in 200|201|409) ok "tenant provisionado ($code)";; *) bad "provision $code"; SUMMARY+=("$NAME: PROVISION $code"); continue;; esac

  TOKEN=$("$BIN" token --secret "$JWT_SECRET" --tenant "$TENANT" --role admin 2>/dev/null | tail -1)
  AUTH="Authorization: Bearer $TOKEN"; HOST="Host: $TENANT.localhost"
  CREATE=${CREATE//\{RUN\}/$RUN}
  cc=$(curl -s -o /tmp/aigen-cr.json -w '%{http_code}' -X POST "localhost:8080/api/$RES" -H "$AUTH" -H "$HOST" -H "Content-Type: application/json" -d "$CREATE")
  [ "$cc" = "201" ] && ok "CREATE /api/$RES → 201" || bad "CREATE /api/$RES → $cc $(head -c 100 /tmp/aigen-cr.json)"
  lc=$(curl -sg -o /dev/null -w '%{http_code}' "localhost:8080/api/$RES?per_page=5" -H "$AUTH" -H "$HOST")
  [ "$lc" = "200" ] && ok "LIST /api/$RES → 200" || bad "LIST → $lc"
  gq=$(curl -s -X POST localhost:8080/graphql -H "$AUTH" -H "$HOST" -H "Content-Type: application/json" -d "{\"query\":\"{ $RES { data { id } } }\"}")
  echo "$gq" | grep -q "\"$RES\"" && ok "GraphQL { $RES }" || bad "GraphQL: $(echo "$gq" | head -c 100)"

  SUMMARY+=("$NAME: provision+CRUD OK | $METRIC")
done

# cleanup test tenants (drop schemas via admin delete)
echo; echo "── cleanup ──────────────────────────────"
stop_engine
boot_engine examples/aigen/optica.json >/dev/null 2>&1 || true
# bootstrap a platform admin would be needed for /admin delete; instead drop tenant schemas via psql if available
if command -v docker >/dev/null && docker ps --format '{{.Names}}' | grep -q appitools-pg; then
  for t in aigenoptica aigenboard aigenshop aigensocial; do
    docker exec -i appitools-pg psql -U "$PG_USER" -d "$PG_DB" -c "DROP SCHEMA IF EXISTS tenant_$t CASCADE; DELETE FROM public.tenants WHERE id='$t';" >/dev/null 2>&1 \
      && echo "  cleaned tenant_$t" || echo "  (could not clean tenant_$t)"
  done
fi
stop_engine

echo; echo "════════════════════════════════════════"
for s in "${SUMMARY[@]}"; do echo "  $s"; done
echo "════════════════════════════════════════"
echo -e "  ${G}PASS: $PASS${N}  ${R}FAIL: $FAIL${N}"
[ $LIVE -eq 0 ] && echo -e "  ${Y}ℹ economía (iter/tokens/costo) requiere ANTHROPIC_API_KEY${N}"
exit $FAIL
