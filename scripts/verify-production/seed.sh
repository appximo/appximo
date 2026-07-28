#!/usr/bin/env bash
#
# seed.sh — fill a tenant with N realistic rows, so the load suite measures a
# database that looks like a running business instead of an empty table.
#
# RUNS ON THE SERVER (it talks to PostgreSQL directly over the local socket).
#
# Why not seed through the API? Because 1,000,000 HTTP POSTs would take hours and
# would measure the loader, not the database. The rows this writes are byte-
# identical in shape to rows the engine writes (the engine's own migration
# created these tables: `id uuid default gen_random_uuid()`, declared fields,
# then `auto` timestamps) — the API reads them exactly like its own.
#
#   sudo bash seed.sh --tenant=api --orders=1000000
#   sudo bash seed.sh --tenant=api --orders=100000 --reset
#
# Flags:
#   --tenant=ID          tenant to seed (its Postgres schema is tenant_<ID>)  [required]
#   --orders=N           number of orders to create                    [default 100000]
#   --customers=N        number of customers                    [default orders/20, min 100]
#   --items-per-order=N  order_items per order                          [default 2]
#   --reset              TRUNCATE the three tables first (destructive, that tenant only)
#   --env-file=PATH      installer env file (for DATABASE_URL)  [default /etc/appitools/appitools.env]
#   --out=PATH           write the JSON result here
#   --help
set -euo pipefail

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$SELF_DIR/lib.sh"

TENANT=""; ORDERS=100000; CUSTOMERS=""; ITEMS=2; RESET="no"; OUT=""
# ENV_FILE (set by --env-file below) is consumed by load_env_secret in lib.sh.
# The linter cannot follow the `source`, so it reports the assignment as unused.
# shellcheck disable=SC2034
for arg in "$@"; do
	case "$arg" in
		--tenant=*)          TENANT="${arg#*=}" ;;
		--orders=*)          ORDERS="${arg#*=}" ;;
		--customers=*)       CUSTOMERS="${arg#*=}" ;;
		--items-per-order=*) ITEMS="${arg#*=}" ;;
		--reset)             RESET="yes" ;;
		--env-file=*)        ENV_FILE="${arg#*=}" ;;
		--out=*)             OUT="${arg#*=}" ;;
		--help|-h)           sed -n '3,26p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
		*) die "unknown flag: $arg (see --help)" ;;
	esac
done

[ -n "$TENANT" ] || die "--tenant is required"
case "$ORDERS" in ''|*[!0-9]*) die "--orders must be a number" ;; esac
case "$ITEMS"  in ''|*[!0-9]*) die "--items-per-order must be a number" ;; esac
if [ -z "$CUSTOMERS" ]; then
	CUSTOMERS=$(( ORDERS / 20 ))
	[ "$CUSTOMERS" -lt 100 ] && CUSTOMERS=100
fi

need psql python3
PGSCHEMA="tenant_${TENANT}"

# Prefer the installer's DATABASE_URL; fall back to a local `postgres` superuser
# session (how a hand-rolled install may be set up).
DBURL="${DATABASE_URL:-}"
[ -z "$DBURL" ] && DBURL="$(load_env_secret DATABASE_URL || true)"
psql_run() {
	if [ -n "$DBURL" ]; then psql -v ON_ERROR_STOP=1 -qtAX -d "$DBURL" "$@"
	else runuser -u postgres -- psql -v ON_ERROR_STOP=1 -qtAX -d appitools "$@"; fi
}

hdr "seed — tenant '$TENANT' ($PGSCHEMA)"
psql_run -c "SELECT 1 FROM information_schema.tables WHERE table_schema='$PGSCHEMA' AND table_name='orders'" \
	| grep -q 1 || die "tenant '$TENANT' has no 'orders' table. Register it first, or seed a tenant whose schema has customers/orders/order_items (see schema/bench-schema.json)."

info "target: $CUSTOMERS customers · $ORDERS orders · $(( ORDERS * ITEMS )) order_items"

START="$(now_ms)"

if [ "$RESET" = "yes" ]; then
	warn "--reset: truncating $PGSCHEMA.{order_items,orders,customers}"
	psql_run -c "TRUNCATE $PGSCHEMA.order_items, $PGSCHEMA.orders, $PGSCHEMA.customers CASCADE"
fi

# The whole seed is ONE server-side statement set. Data is deterministic-ish by
# construction (modulo arithmetic on generate_series, not random()) so two runs
# with the same N produce the same distribution — a benchmark whose dataset
# shifts between runs cannot support an A/B claim.
info "inserting customers…"
psql_run <<SQL
INSERT INTO ${PGSCHEMA}.customers (name, email, city, tier, created_at)
SELECT
  'Customer ' || i,
  'customer' || i || '@example.com',
  (ARRAY['New York','Los Angeles','Chicago','Houston','Miami','Seattle','Denver','Boston'])[1 + (i % 8)],
  (ARRAY['free','pro','enterprise'])[1 + (i % 3)],
  now() - ((i % 720) || ' days')::interval
FROM generate_series(1, ${CUSTOMERS}) i;
SQL

info "inserting orders…"
# Customer ids are drawn from an array built once, so each order gets a real FK
# without a per-row subquery. 'paid' is deliberately the most common status (2 of
# 6 slots): the read scenario filters on it, and a filter that matches ~1/3 of a
# large table is the realistic case — a filter matching nothing would make every
# query trivially fast and the benchmark a lie.
psql_run <<SQL
DO \$\$
DECLARE ids uuid[];
BEGIN
  SELECT array_agg(id) INTO ids FROM ${PGSCHEMA}.customers;
  INSERT INTO ${PGSCHEMA}.orders (customer_id, status, region, total, created_at)
  SELECT
    ids[1 + (i % array_length(ids, 1))],
    (ARRAY['pending','paid','paid','shipped','delivered','cancelled'])[1 + (i % 6)],
    (ARRAY['us-east','us-west','eu-central','ap-south'])[1 + (i % 4)],
    round((10 + (i % 99000) / 100.0)::numeric, 2)::float8,
    now() - ((i % 525600) || ' minutes')::interval
  FROM generate_series(1, ${ORDERS}) i;
END \$\$;
SQL

if [ "$ITEMS" -gt 0 ]; then
	info "inserting order_items…"
	# Only orders that have NO items yet. Without this guard, growing a dataset
	# additively (seed 100k, then another 400k) re-runs the cross join over EVERY
	# order and multiplies the items of the rows that already had them — the
	# dataset would silently stop matching what the report claims it is.
	psql_run <<SQL
INSERT INTO ${PGSCHEMA}.order_items (order_id, product, qty, price)
SELECT o.id,
       'SKU-' || lpad(((g.g * 7 + length(o.id::text)) % 500)::text, 4, '0'),
       1 + (g.g % 5),
       round((5 + (g.g % 400))::numeric, 2)::float8
FROM ${PGSCHEMA}.orders o
CROSS JOIN generate_series(1, ${ITEMS}) g(g)
WHERE NOT EXISTS (
  SELECT 1 FROM ${PGSCHEMA}.order_items i WHERE i.order_id = o.id
);
SQL
fi

# ANALYZE is not optional. Without fresh statistics the planner may pick a
# sequential scan over the index we just filled, and the "slow at 1M rows"
# conclusion would be an artifact of the seeding, not of the product.
info "ANALYZE (fresh planner statistics)…"
psql_run -c "ANALYZE ${PGSCHEMA}.customers, ${PGSCHEMA}.orders, ${PGSCHEMA}.order_items"

END="$(now_ms)"
ELAPSED_MS=$(( END - START ))

read -r N_CUST N_ORD N_ITEM <<<"$(psql_run -c "
SELECT (SELECT count(*) FROM ${PGSCHEMA}.customers),
       (SELECT count(*) FROM ${PGSCHEMA}.orders),
       (SELECT count(*) FROM ${PGSCHEMA}.order_items)" | tr '|' ' ')"

SIZES="$(psql_run -c "
SELECT string_agg(rel || '=' || sz, ',') FROM (
  SELECT 'customers' AS rel, pg_total_relation_size('${PGSCHEMA}.customers') AS sz
  UNION ALL SELECT 'orders', pg_total_relation_size('${PGSCHEMA}.orders')
  UNION ALL SELECT 'order_items', pg_total_relation_size('${PGSCHEMA}.order_items')) t")"
TOTAL_BYTES="$(psql_run -c "
SELECT pg_total_relation_size('${PGSCHEMA}.customers')
     + pg_total_relation_size('${PGSCHEMA}.orders')
     + pg_total_relation_size('${PGSCHEMA}.order_items')")"

ok "seeded in $(( ELAPSED_MS / 1000 ))s — customers=$N_CUST orders=$N_ORD order_items=$N_ITEM"
dim "  on-disk (incl. indexes): $(( TOTAL_BYTES / 1024 / 1024 )) MiB  [$SIZES]"

RESULT="$(python3 -c '
import json, sys
print(json.dumps({
  "part": "D-seed",
  "tenant": sys.argv[1],
  "pg_schema": sys.argv[2],
  "requested": {"customers": int(sys.argv[3]), "orders": int(sys.argv[4]), "items_per_order": int(sys.argv[5])},
  "actual": {"customers": int(sys.argv[6]), "orders": int(sys.argv[7]), "order_items": int(sys.argv[8])},
  "elapsed_ms": int(sys.argv[9]),
  "total_relation_bytes": int(sys.argv[10]),
  "reset": sys.argv[11] == "yes",
}))' "$TENANT" "$PGSCHEMA" "$CUSTOMERS" "$ORDERS" "$ITEMS" "$N_CUST" "$N_ORD" "$N_ITEM" "$ELAPSED_MS" "$TOTAL_BYTES" "$RESET")"

if [ -n "$OUT" ]; then printf '%s' "$RESULT" | write_json "$OUT"; ok "wrote $OUT"; else printf '%s\n' "$RESULT"; fi
