#!/usr/bin/env bash
# ============================================================================
# Appitools — Acceptance Test (la guía de aprendizaje del droplet, codificada)
# Smoke de aceptación end-to-end contra CUALQUIER instancia corriendo:
# salud, CRUD, validación 422, queries, GraphQL, seguridad, RBAC viewer,
# multi-tenant, SSE, observabilidad, reload en caliente, strict keys.
#
# Uso (quickstart docker compose, el default):
#   cd <dir con docker-compose.yml + .env> && bash scripts/acceptance-test.sh
#
# Uso (instancia arbitraria — binario nativo, otro host, otra DB):
#   JWT_SECRET=… ADMIN_KEY=… \
#   BASE=http://host:8080 ADMIN=http://host:9090 \
#   APPITOOLS_CLI=./appitools SCHEMA_FILE=examples/quickstart/schema.json \
#   PSQL_CMD="docker exec -i mi-pg psql -U user -d db" \      # opcional
#   bash scripts/acceptance-test.sh
#
# Overrides:
#   BASE / ADMIN     data plane / control plane (default localhost:8080/9090)
#   APPITOOLS_CLI    cómo invocar el CLI appitools (default: via compose exec)
#   SCHEMA_FILE      schema local en vez de leerlo del contenedor engine
#   PSQL_CMD         psql para el check físico de schemas (vacío = se omite)
#
# Idempotente: tolera tenants ya creados; cada run usa títulos únicos.
# Requiere el schema quickstart (todo-api: tasks con title/status/due).
# ============================================================================
set -u
PASS=0; FAIL=0; INFO=0
G='\033[0;32m'; R='\033[0;31m'; Y='\033[1;33m'; N='\033[0m'
RUN_ID=$(date +%s)

BASE="${BASE:-http://localhost:8080}"
TENANT_A="${TENANT_A:-acme}"      # tenant principal del smoke
TENANT_B="${TENANT_B:-globex}"    # segundo tenant (aislamiento)
ADMIN="${ADMIN:-http://localhost:9090}"
APPITOOLS_CLI="${APPITOOLS_CLI:-docker compose exec -T engine appitools}"
PSQL_CMD="${PSQL_CMD-docker compose exec -T db psql -U appitools -d appitools}"

ok()   { PASS=$((PASS+1)); echo -e "${G}✓ PASS${N}  $1"; }
bad()  { FAIL=$((FAIL+1)); echo -e "${R}✗ FAIL${N}  $1"; }
info() { INFO=$((INFO+1)); echo -e "${Y}ℹ INFO${N}  $1"; }
hdr()  { echo; echo "── $1 ──────────────────────────────"; }

# ── Setup ───────────────────────────────────────────────────────────────────
hdr "SETUP"
[ -f .env ] && { set -a; source .env; set +a; }
: "${JWT_SECRET:?JWT_SECRET requerido (en .env o en el entorno)}"
: "${ADMIN_KEY:?ADMIN_KEY requerido (en .env o en el entorno)}"
TOKEN=$($APPITOOLS_CLI token --secret "$JWT_SECRET" --tenant "$TENANT_A" --role admin 2>/dev/null | tail -1)
[ -n "$TOKEN" ] && ok "token admin $TENANT_A minteado" || { bad "no se pudo mintear token"; exit 1; }
A_AUTH="Authorization: Bearer $TOKEN"; A_HOST="Host: $TENANT_A.localhost"
if [ -n "${SCHEMA_FILE:-}" ]; then SCH=$(cat "$SCHEMA_FILE"); else SCH=$($APPITOOLS_CLI sh -c 'cat /etc/appitools/schema.json' 2>/dev/null || docker compose exec -T engine cat /etc/appitools/schema.json); fi
echo "$SCH" | python3 -c "import sys,json;json.load(sys.stdin)" 2>/dev/null && ok "schema fuente legible" || { bad "no pude leer el schema (SCHEMA_FILE o contenedor)"; exit 1; }

# ── 1. Salud y versión ──────────────────────────────────────────────────────
hdr "1. SALUD"
H=$(curl -s "$BASE/health")
echo "   /health → $H"
echo "$H" | grep -q '"status":"ok"' && ok "health ok" || bad "health no ok"
VER=$(echo "$H" | grep -o '"version":"[^"]*"')
info "versión del binario: $VER (un SHA/tag = build canónico; \"dev\" = build local)"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz")" = "200" ] && ok "healthz 200" || bad "healthz"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/readyz")" = "200" ] && ok "readyz 200" || bad "readyz"

# ── 2. Tenant (idempotente) ─────────────────────────────────────────────────
hdr "2. TENANT $TENANT_A"
C=$(curl -s -o /tmp/at-tenant.json -w '%{http_code}' -X POST "$ADMIN/tenants" \
  -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"$TENANT_A\",\"display_name\":\"Acme\",\"email\":\"a@acme.com\",\"plan\":\"free\",\"schema\":$SCH}")
case "$C" in 200|201) ok "tenant $TENANT_A creado";; 409) ok "tenant $TENANT_A ya existía (409 correcto)";; *) bad "tenant $TENANT_A → $C $(cat /tmp/at-tenant.json)";; esac

# ── 3. CRUD completo ────────────────────────────────────────────────────────
hdr "3. CRUD"
B=$(curl -s -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" \
  -d "{\"title\":\"crud-$RUN_ID\",\"status\":\"open\"}")
TID=$(echo "$B" | python3 -c "import sys,json;print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
[ -n "$TID" ] && ok "CREATE 201 → id $TID" || bad "CREATE: $B"
curl -s "$BASE/api/tasks/$TID" -H "$A_AUTH" -H "$A_HOST" | grep -q "crud-$RUN_ID" && ok "GET by id" || bad "GET by id"
curl -s -X PATCH "$BASE/api/tasks/$TID" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d '{"status":"done"}' | grep -q '"status":"done"' && ok "PATCH parcial" || bad "PATCH"
[ "$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE/api/tasks/$TID" -H "$A_AUTH" -H "$A_HOST")" = "204" ] && ok "DELETE 204" || bad "DELETE"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/tasks/$TID" -H "$A_AUTH" -H "$A_HOST")" = "404" ] && ok "GET tras delete → 404" || bad "GET tras delete"

# ── 4. Validación declarativa (S44) ─────────────────────────────────────────
hdr "4. VALIDACIÓN 422"
V=$(curl -s -w '|%{http_code}' -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d '{"title":"x","status":"INVALIDO"}')
echo "$V" | grep -q '422' && echo "$V" | grep -q '"rule":"enum"' && ok "enum inválido → 422 con rule:enum" || bad "enum: $V"
V=$(curl -s -w '|%{http_code}' -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d '{"status":"open"}')
echo "$V" | grep -q '"rule":"required"' && ok "required faltante → 422" || bad "required: $V"
# FIX 7: campo desconocido → 422 (imagen nueva) o 500 (vieja)
V=$(curl -s -o /tmp/at-unk.json -w '%{http_code}' -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d "{\"title\":\"unk-$RUN_ID\",\"status\":\"open\",\"campo_falso\":1}")
case "$V" in
  422) ok "FIX 7 presente: campo desconocido → 422 $(cat /tmp/at-unk.json)";;
  500) info "FIX 7 AUSENTE (imagen vieja): campo desconocido → 500";;
  201) info "campo desconocido aceptado con 201 — ¿la columna existe de una prueba previa?";;
  *)   bad "campo desconocido → $V";;
esac

# ── 5. Filtros, sort, keyset ────────────────────────────────────────────────
hdr "5. QUERIES"
for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
  curl -s -o /dev/null -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d "{\"title\":\"pag-$RUN_ID-$i\",\"status\":\"open\"}"
done
curl -sg "$BASE/api/tasks?filter[status][eq]=open&per_page=5" -H "$A_AUTH" -H "$A_HOST" | grep -q '"data":\[' && ok "filter eq" || bad "filter eq"
curl -sg "$BASE/api/tasks?filter[title][partial]=pag-$RUN_ID" -H "$A_AUTH" -H "$A_HOST" | grep -q "pag-$RUN_ID" && ok "filter partial" || bad "filter partial"
curl -sg "$BASE/api/tasks?sort=title&order=desc&per_page=3" -H "$A_AUTH" -H "$A_HOST" | grep -q '"data"' && ok "sort + order" || bad "sort"
P1=$(curl -sg "$BASE/api/tasks?per_page=5" -H "$A_AUTH" -H "$A_HOST")
LAST=$(echo "$P1" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d['data'][-1]['id'] if d['data'] else '')")
HN=$(echo "$P1" | python3 -c "import sys,json;print(json.load(sys.stdin)['meta']['has_next'])")
P2N=$(curl -sg "$BASE/api/tasks?per_page=5&after=$LAST" -H "$A_AUTH" -H "$A_HOST" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['data']))")
[ "$HN" = "True" ] && [ "$P2N" -gt 0 ] && ok "keyset: pág1 has_next + pág2 con $P2N items" || bad "keyset (has_next=$HN p2=$P2N)"

# ── 6. GraphQL ──────────────────────────────────────────────────────────────
hdr "6. GRAPHQL"
GQ=$(curl -s -X POST "$BASE/graphql" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d '{"query":"{ tasks { data { id title } } }"}')
echo "$GQ" | grep -q '"tasks"' && ok "query GraphQL devuelve datos" || bad "GraphQL: $GQ"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/graphiql")" = "404" ] && ok "GraphiQL 404 (prod mode, correcto)" || info "GraphiQL accesible (¿modo development?)"

# ── 7. Seguridad básica ─────────────────────────────────────────────────────
hdr "7. SEGURIDAD"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/tasks")" = "401" ] && ok "sin token → 401" || bad "sin token"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/tasks" -H "$A_AUTH" -H "Host: $TENANT_B.localhost")" = "401" ] && ok "cross-tenant → 401" || bad "cross-tenant"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/metrics")" = "401" ] && ok "/metrics sin key → 401" || bad "/metrics sin key"
curl -s -H "X-Admin-Key: $ADMIN_KEY" "$BASE/metrics" | grep -q appitools_requests_total && ok "/metrics con key" || bad "/metrics con key"

# ── 8. RBAC: rol viewer (deny-by-default + allowlist de campos) ─────────────
# La sintaxis CORRECTA de un rol de solo-lectura con campos restringidos
# (verificada contra pkg/schema/types.go — "fields" es el allowlist):
#
#   "rbac": { "roles": {
#     "admin":  { "resources": "*", "actions": ["*"] },
#     "viewer": { "resources": ["tasks"], "actions": ["read"],
#                 "fields": ["id", "title", "status"] }
#   }}
#
# OJO: el RBAC se compila al BOOT desde el --schema (igual que rutas/hooks).
# Agregar el rol vía PUT+reload NO lo activa — por eso este test es
# adaptativo: con viewer en el schema de boot verifica read/create/allowlist;
# sin él, verifica el deny-by-default (rol fuera del schema → 403).
hdr "8. RBAC VIEWER"
TV=$($APPITOOLS_CLI token --secret "$JWT_SECRET" --tenant "$TENANT_A" --role viewer 2>/dev/null | tail -1)
V_AUTH="Authorization: Bearer $TV"
HAS_VIEWER=$(echo "$SCH" | python3 -c "import sys,json;print('yes' if 'viewer' in json.load(sys.stdin).get('rbac',{}).get('roles',{}) else 'no')")
if [ "$HAS_VIEWER" = "yes" ]; then
  RV=$(curl -sg "$BASE/api/tasks?per_page=1" -H "$V_AUTH" -H "$A_HOST")
  echo "$RV" | grep -q '"data"' && ok "viewer: read → 200" || bad "viewer read: $RV"
  echo "$RV" | grep -q '"due"' && bad "viewer: allowlist NO aplicada (due visible)" || ok "viewer: fields allowlist (sin due)"
  [ "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/tasks" -H "$V_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d '{"title":"no","status":"open"}')" = "403" ] \
    && ok "viewer: create → 403 (read-only)" || bad "viewer pudo crear"
else
  [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/tasks" -H "$V_AUTH" -H "$A_HOST")" = "403" ] \
    && ok "deny-by-default: rol viewer fuera del schema → 403" || bad "rol fuera del schema NO dio 403"
  info "schema de boot sin rol viewer — para el test completo (read 200 + allowlist + create 403) agregalo al schema del --schema y reiniciá (sintaxis arriba)"
fi

# ── 9. Multi-tenant: aislamiento ────────────────────────────────────────────
hdr "9. MULTI-TENANT"
C=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$ADMIN/tenants" -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" \
  -d "{\"tenant_id\":\"$TENANT_B\",\"display_name\":\"Globex\",\"email\":\"g@globex.com\",\"plan\":\"free\",\"schema\":$SCH}")
case "$C" in 200|201|409) ok "tenant $TENANT_B listo ($C)";; *) bad "$TENANT_B → $C";; esac
TG=$($APPITOOLS_CLI token --secret "$JWT_SECRET" --tenant "$TENANT_B" --role admin 2>/dev/null | tail -1)
curl -s -o /dev/null -X POST "$BASE/api/tasks" -H "Authorization: Bearer $TG" -H "Host: $TENANT_B.localhost" -H "Content-Type: application/json" -d "{\"title\":\"isolated-secret-$RUN_ID\",\"status\":\"open\"}"
# Sanity del propio test: B SÍ ve su fila (si no, el "0 rows" de abajo sería vacuo)
BSEES=$(curl -sg "$BASE/api/tasks?filter[title][partial]=isolated-secret-$RUN_ID" -H "Authorization: Bearer $TG" -H "Host: $TENANT_B.localhost" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['data']))" 2>/dev/null)
[ "$BSEES" = "1" ] || bad "sanity: $TENANT_B no ve su propia fila (test de leak inválido)"
LEAK=$(curl -sg "$BASE/api/tasks?filter[title][partial]=isolated-secret-$RUN_ID" -H "$A_AUTH" -H "$A_HOST" | python3 -c "import sys,json;print(len(json.load(sys.stdin)['data']))")
[ "$LEAK" = "0" ] && ok "aislamiento: $TENANT_A NO ve datos de $TENANT_B (data:[])" || bad "LEAK: $TENANT_A ve $LEAK rows de $TENANT_B"
if [ -n "$PSQL_CMD" ]; then
  $PSQL_CMD -tAc "SELECT count(*) FROM information_schema.schemata WHERE schema_name IN ('tenant_'||'$TENANT_A','tenant_'||'$TENANT_B')" 2>/dev/null | grep -q '^2$' \
    && ok "aislamiento físico: 2 schemas Postgres" || bad "schemas en pg"
else
  info "PSQL_CMD vacío — check físico de schemas omitido"
fi

# ── 10. SSE: entrega + aislamiento entre tenants ────────────────────────────
hdr "10. SSE"
SSE_OUT=/tmp/at-sse-$RUN_ID.log
( timeout 8 curl -sN "$BASE/api/tasks/events" -H "$A_AUTH" -H "$A_HOST" > "$SSE_OUT" 2>&1 ) &
SSEPID=$!
sleep 2
curl -s -o /dev/null -X POST "$BASE/api/tasks" -H "Authorization: Bearer $TG" -H "Host: $TENANT_B.localhost" -H "Content-Type: application/json" -d "{\"title\":\"sse-b-$RUN_ID\",\"status\":\"open\"}"
sleep 1
curl -s -o /dev/null -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d "{\"title\":\"sse-a-$RUN_ID\",\"status\":\"open\"}"
wait $SSEPID 2>/dev/null
grep -q "sse-a-$RUN_ID" "$SSE_OUT" && ok "SSE: evento de $TENANT_A recibido" || bad "SSE: evento de $TENANT_A NO llegó"
grep -q "sse-b-$RUN_ID" "$SSE_OUT" && bad "SSE LEAK: evento de $TENANT_B cruzó al stream de $TENANT_A" || ok "SSE: aislamiento ($TENANT_B en silencio)"

# ── 11. Rate limiter (informativo) ──────────────────────────────────────────
hdr "11. RATE LIMIT (smoke)"
CODES=$(for i in $(seq 1 40); do curl -s -o /dev/null -w "%{http_code} " "$BASE/api/tasks?per_page=1" -H "$A_AUTH" -H "$A_HOST" & done; wait)
N429=$(echo "$CODES" | grep -o 429 | wc -l); N200=$(echo "$CODES" | grep -o 200 | wc -l)
info "40 concurrentes → $N200×200, $N429×429 (bajo el límite 1000rps/100burst, 0×429 es correcto)"

# ── 12. Observabilidad ──────────────────────────────────────────────────────
hdr "12. OBSERVABILIDAD"
DT=$(curl -s -H "X-Admin-Key: $ADMIN_KEY" "$BASE/debug/tenant/$TENANT_A")
echo "$DT" | grep -q '"recent_traces"' && ok "/debug/tenant con traces" || bad "/debug/tenant"
echo "$DT" | grep -q '"slo"' && ok "SLO state presente" || bad "SLO"
SYN=$(curl -s -H "X-Admin-Key: $ADMIN_KEY" "$BASE/debug/synthetic")
if echo "$SYN" | grep -q 'guides-api'; then info "FIX 1 AUSENTE (imagen vieja): synthetic aún chequea guides-api → $SYN"
elif echo "$SYN" | grep -q 'api-canary'; then ok "FIX 1 presente: synthetic = api-canary derivado del schema"
else info "synthetic: $SYN"; fi

# ── 13. Schema reload en caliente (campo nuevo) ─────────────────────────────
hdr "13. SCHEMA RELOAD"
FNAME="hotfield_$RUN_ID"
NEW_SCHEMA=$(echo "$SCH" | python3 -c "
import sys,json
s=json.load(sys.stdin)
s['resources']['tasks']['fields']['$FNAME']={'type':'int','min':0,'max':9}
print(json.dumps(s))")
PUTR=$(curl -s -X PUT "$ADMIN/tenants/$TENANT_A/schema" -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" -d "{\"schema\":$NEW_SCHEMA}")
echo "$PUTR" | grep -q migration_queued && ok "PUT schema (campo nuevo $FNAME)" || bad "PUT schema: $PUTR"
sleep 1
curl -s -X POST "$BASE/admin/tenants/$TENANT_A/reload" -H "X-Admin-Key: $ADMIN_KEY" | grep -q '"ok":true' && ok "reload ok" || bad "reload"
HOT=$(curl -s -X POST "$BASE/api/tasks" -H "$A_AUTH" -H "$A_HOST" -H "Content-Type: application/json" -d "{\"title\":\"hot-$RUN_ID\",\"status\":\"open\",\"$FNAME\":5}")
echo "$HOT" | grep -q "\"$FNAME\":5" && ok "LA MAGIA: campo nuevo aceptado en caliente, sin restart" || bad "hot field: $HOT"

# ── 14. Validación estricta de claves (FIX 8a, imagen nueva) ────────────────
hdr "14. SCHEMA STRICT KEYS"
BADSCHEMA=$(echo "$SCH" | python3 -c "
import sys,json
s=json.load(sys.stdin)
s['resources']['tasks']['bloque_inventado']={'x':1}
print(json.dumps(s))")
BK=$(curl -s -o /tmp/at-bad.json -w '%{http_code}' -X PUT "$ADMIN/tenants/$TENANT_A/schema" -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" -d "{\"schema\":$BADSCHEMA}")
case "$BK" in
  400) ok "FIX 8a presente: clave inventada → 400 $(head -c 120 /tmp/at-bad.json)";;
  200) info "FIX 8a AUSENTE (imagen vieja): clave inventada aceptada en silencio";;
  *)   info "strict keys → $BK $(head -c 120 /tmp/at-bad.json)";;
esac

# ── 15. Cleanup: schema original de vuelta ──────────────────────────────────
# Deja el schema ALMACENADO como el original (la columna hotfield_* creada por
# la migración aditiva queda en la tabla — es esperado, el DDL nunca borra).
hdr "15. CLEANUP"
curl -s -X PUT "$ADMIN/tenants/$TENANT_A/schema" -H "X-Admin-Key: $ADMIN_KEY" -H "Content-Type: application/json" -d "{\"schema\":$SCH}" | grep -q migration_queued \
  && curl -s -X POST "$BASE/admin/tenants/$TENANT_A/reload" -H "X-Admin-Key: $ADMIN_KEY" | grep -q '"ok":true' \
  && ok "schema original restaurado + reload" || info "no se pudo restaurar el schema original (revisar a mano)"

# ── Resumen ─────────────────────────────────────────────────────────────────
echo
echo "════════════════════════════════════════"
echo -e "  ${G}PASS: $PASS${N}   ${R}FAIL: $FAIL${N}   ${Y}INFO: $INFO${N}"
echo "  versión probada: $VER"
echo "════════════════════════════════════════"
[ "$FAIL" -eq 0 ] && echo "  ✓ ACEPTACIÓN COMPLETA" || echo "  ✗ revisar los FAIL de arriba"
exit $FAIL
