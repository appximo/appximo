-- ============================================================
-- PUBLIC SCHEMA — NestJS baseline (single-schema, tenant_id col)
-- Statuses aligned with Appximo schema.json enum:
--   pending, in_transit, delivered, returned
-- ============================================================
CREATE TABLE IF NOT EXISTS guides (
    id         SERIAL PRIMARY KEY,
    tenant_id  VARCHAR(50) NOT NULL,
    title      VARCHAR(255) NOT NULL,
    content    TEXT NOT NULL,
    status     VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

TRUNCATE TABLE guides RESTART IDENTITY;

-- Status keyed off (i/10) — NOT i — so it does not correlate with the tenant
-- residue MOD(i, 10). With CASE MOD(i, 4) every tenant only ever saw 2 of the
-- 4 statuses (e.g. tenant_10 had zero 'pending' rows), which broke parity with
-- the Appximo per-tenant seed (25k rows per status per tenant).
INSERT INTO guides (tenant_id, title, content, status, created_at)
SELECT
    'tenant_' || (1 + MOD(g.i, 10))::text,
    'Guide Title ' || g.i,
    'Here is some content for guide ' || g.i,
    CASE MOD(g.i / 10, 4)
        WHEN 0 THEN 'pending'
        WHEN 1 THEN 'in_transit'
        WHEN 2 THEN 'delivered'
        ELSE        'returned'
    END,
    NOW() - (g.i || ' seconds')::INTERVAL
FROM generate_series(1, 1000000) AS g(i);

CREATE INDEX IF NOT EXISTS idx_guides_tenant_status_created
ON guides(tenant_id, status, created_at DESC);

-- ============================================================
-- TENANT SCHEMAS — Appximo multi-tenant
-- Columns match testdata/logistics/schema.json exactly.
-- 100k rows per tenant (10 tenants = 1M rows total).
-- ============================================================
DO $outer$
DECLARE i INT;
BEGIN
  FOR i IN 1..10 LOOP
    EXECUTE 'CREATE SCHEMA IF NOT EXISTS tenant_' || i;

    -- Mirror the guides resource from schema.json (no FK constraints
    -- to clients/users since those tables are empty in bench mode).
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS tenant_%s.guides (
        id            UUID DEFAULT gen_random_uuid() PRIMARY KEY,
        code          TEXT NOT NULL UNIQUE,
        status        TEXT NOT NULL,
        origin        TEXT NOT NULL,
        destination   TEXT NOT NULL,
        weight_kg     FLOAT,
        declared_value FLOAT,
        client_id     UUID,
        operator_id   UUID,
        created_at    TIMESTAMPTZ DEFAULT now(),
        updated_at    TIMESTAMPTZ DEFAULT now()
      )', i);

    EXECUTE format(
      'INSERT INTO tenant_%s.guides
        (code, status, origin, destination, weight_kg, created_at, updated_at)
       SELECT
         ''GD-T%s-'' || g.i,
         CASE MOD(g.i, 4)
           WHEN 0 THEN ''pending''
           WHEN 1 THEN ''in_transit''
           WHEN 2 THEN ''delivered''
           ELSE        ''returned''
         END,
         ''Origin-'' || g.i,
         ''Dest-''   || g.i,
         (MOD(g.i, 100) + 1)::FLOAT,
         NOW() - (g.i || '' seconds'')::INTERVAL,
         NOW() - (g.i || '' seconds'')::INTERVAL
       FROM generate_series(1, 100000) AS g(i)', i, i);

    EXECUTE format(
      'CREATE INDEX IF NOT EXISTS idx_tenant_%s_guides_status_created
       ON tenant_%s.guides(status, created_at DESC)', i, i);

    -- Índice para ORDER BY id ASC (el default del codegen de Appximo)
    EXECUTE format(
      'CREATE INDEX IF NOT EXISTS idx_tenant_%s_guides_status_id
       ON tenant_%s.guides(status, id ASC)', i, i);
  END LOOP;
END $outer$;
