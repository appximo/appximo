#!/usr/bin/env bash
# api-cert.sh — run the Appximo API certification suite (Newman) against a live
# engine serving the ERP demo (tenant "nimbus"). It mints the JWTs the collection
# needs (an external consumer cannot mint its own — only the engine holds the
# secret), writes a Postman environment, and runs `newman run`. Any failed
# assertion makes newman exit non-zero, so this is a reproducible PASS/FAIL gate.
#
# Usage:  bash scripts/api-cert.sh
# Env:    TARGET_URL (default http://localhost:8080), TENANT_ID (default nimbus),
#         APPXIMO_SECRETS (default .env.dev), BIN
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

TARGET_URL="${TARGET_URL:-http://localhost:8080}"
TENANT="${TENANT_ID:-nimbus}"
SECRETS="${APPXIMO_SECRETS:-.env.dev}"
COLLECTION="examples/erp-demo/api-cert.postman_collection.json"
ENV_FILE="/tmp/api-cert-env-$$.json"

# Fixed, well-known UUIDs for the owner-scoped (mass-assignment) test. The empleado
# token's user_id == EMPLEADO_UID; the foreign value must differ.
EMPLEADO_UID="22222222-2222-2222-2222-222222222222"
WRONG_UID="33333333-3333-3333-3333-333333333333"

# shellcheck disable=SC1090
[ -f "$SECRETS" ] && source "$SECRETS"
if [ -z "${JWT_SECRET:-}" ]; then
  echo "ERROR: JWT_SECRET not set (missing $SECRETS?)" >&2
  exit 1
fi

BIN="${BIN:-./appximo-dev}"; [ -x "$BIN" ] || BIN="./appximo"
mint() { "$BIN" token --tenant "$TENANT" --secret "$JWT_SECRET" --role "$1" ${2:+--user-id "$2"} 2>/dev/null | tail -1; }

echo "▶ minting tokens for tenant '$TENANT' (roles: rrhh-admin, auditor, empleado)…"
ADMIN_TOKEN="$(mint rrhh-admin)"
AUDITOR_TOKEN="$(mint auditor)"
EMPLEADO_TOKEN="$(mint empleado "$EMPLEADO_UID")"
for v in ADMIN_TOKEN AUDITOR_TOKEN EMPLEADO_TOKEN; do
  if [ -z "${!v}" ]; then echo "ERROR: failed to mint $v" >&2; exit 1; fi
done

cat >"$ENV_FILE" <<JSON
{
  "name": "api-cert-$TENANT",
  "values": [
    { "key": "base_url", "value": "$TARGET_URL", "enabled": true },
    { "key": "host", "value": "$TENANT.localhost", "enabled": true },
    { "key": "admin_token", "value": "$ADMIN_TOKEN", "enabled": true },
    { "key": "auditor_token", "value": "$AUDITOR_TOKEN", "enabled": true },
    { "key": "empleado_token", "value": "$EMPLEADO_TOKEN", "enabled": true },
    { "key": "empleado_user_id", "value": "$EMPLEADO_UID", "enabled": true },
    { "key": "wrong_user_id", "value": "$WRONG_UID", "enabled": true }
  ]
}
JSON
trap 'rm -f "$ENV_FILE"' EXIT

NEWMAN="newman"
command -v newman >/dev/null 2>&1 || NEWMAN="npx --yes newman"
echo "▶ running newman against $TARGET_URL (Host: $TENANT.localhost)…"
$NEWMAN run "$COLLECTION" --environment "$ENV_FILE" --reporters cli
