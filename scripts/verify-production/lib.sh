#!/usr/bin/env bash
# lib.sh — shared plumbing for the production verification suite.
#
# Sourced by footprint.sh / seed.sh / load.sh / chaos.sh / run-all.sh. Holds the
# things every script in the suite must agree on: argument parsing conventions,
# the results directory layout, structured JSON emission, and the "am I on the
# server or on the loader?" question.
#
# Nothing here runs on its own.

# ── Output helpers ───────────────────────────────────────────────────────────
if [ -t 1 ]; then
	C_G=$'\033[0;32m'; C_Y=$'\033[1;33m'; C_R=$'\033[0;31m'
	C_B=$'\033[1;34m'; C_D=$'\033[2m'; C_N=$'\033[0m'
else
	C_G=""; C_Y=""; C_R=""; C_B=""; C_D=""; C_N=""
fi
info() { printf '%s→%s %s\n' "$C_B" "$C_N" "$*"; }
ok()   { printf '%s✓%s %s\n' "$C_G" "$C_N" "$*"; }
warn() { printf '%s!%s %s\n' "$C_Y" "$C_N" "$*" >&2; }
die()  { printf '%s✗%s %s\n' "$C_R" "$C_N" "$*" >&2; exit 1; }
dim()  { printf '%s%s%s\n' "$C_D" "$*" "$C_N"; }
hdr()  { printf '\n%s══ %s ══%s\n' "$C_B" "$*" "$C_N"; }

# ── Defaults every script shares ─────────────────────────────────────────────
: "${TARGET:=}"          # https://api.example.com — the PUBLIC url under test
: "${OUT_DIR:=}"         # results directory (default: ./verify-results/<stamp>)
: "${TENANT:=}"          # tenant id; default derived from TARGET's first label
: "${TOKEN:=}"           # JWT for the data plane
: "${ENV_FILE:=/etc/appximo/appximo.env}"
: "${ENGINE_PORT:=8090}"
: "${SERVICE:=appximo}"
: "${BIN:=/opt/appximo/bin/appximo}"

# suite_stamp: a single UTC timestamp per invocation, used for the results dir.
suite_stamp() { date -u +%Y%m%dT%H%M%SZ; }

# ensure_out_dir: create (and echo) the results directory.
ensure_out_dir() {
	[ -n "$OUT_DIR" ] || OUT_DIR="./verify-results/$(suite_stamp)"
	mkdir -p "$OUT_DIR"
	printf '%s' "$OUT_DIR"
}

# host_of URL -> the bare hostname (no scheme, no port, no path).
host_of() {
	local u="${1#*://}"; u="${u%%/*}"; printf '%s' "${u%%:*}"
}

# tenant_of URL -> the first label of the host, which IS the tenant (the engine
# resolves the tenant from the Host subdomain — see pkg/tenant/middleware.go).
tenant_of() {
	local h; h="$(host_of "$1")"
	printf '%s' "${h%%.*}"
}

# need CMD… — fail with an actionable message if a tool is missing.
need() {
	local c
	for c in "$@"; do
		command -v "$c" >/dev/null 2>&1 || die "'$c' not found. Install it and re-run. (k6: https://k6.io/docs/get-started/installation/)"
	done
}

# on_server: true when this box looks like the Appximo server (the env file and
# the systemd unit exist). Scripts that must read /proc or run systemctl check it.
on_server() { [ -f "$ENV_FILE" ] && [ -r "$ENV_FILE" ]; }

# load_env_secret KEY — read one value out of the installer's env file. Used to
# mint a token or reach the control plane without the operator pasting secrets.
load_env_secret() {
	[ -r "$ENV_FILE" ] || return 1
	grep -E "^$1=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2-
}

# resolve_token: use --token / $TOKEN if given; otherwise, ON THE SERVER, mint a
# short-lived admin JWT from the installed binary + the env file's JWT_SECRET.
# Off the server with no token we fail loudly rather than silently measuring 401s.
resolve_token() {
	[ -n "$TOKEN" ] && { printf '%s' "$TOKEN"; return 0; }
	if on_server && [ -x "$BIN" ]; then
		local secret; secret="$(load_env_secret JWT_SECRET)"
		if [ -n "$secret" ]; then
			"$BIN" token --secret "$secret" --tenant "${TENANT:-$(tenant_of "$TARGET")}" --role admin 2>/dev/null | tail -1
			return 0
		fi
	fi
	die "no JWT available: pass --token=<jwt> (mint one on the server with: appximo token --secret \"\$JWT_SECRET\" --tenant <id> --role admin)"
}

# json_escape: minimal string escaping for hand-built JSON values.
json_escape() { printf '%s' "$1" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))'; }

# write_json FILE — read JSON on stdin, pretty-print it to FILE (and validate it;
# a malformed result file is worse than none).
write_json() {
	local f="$1"
	python3 -c '
import json, sys
data = json.load(sys.stdin)
with open(sys.argv[1], "w") as fh:
    json.dump(data, fh, indent=2, sort_keys=True)
    fh.write("\n")
' "$f" || die "could not write valid JSON to $f"
}

# now_ms: epoch milliseconds (used to timestamp chaos events precisely).
now_ms() { date +%s%3N; }
