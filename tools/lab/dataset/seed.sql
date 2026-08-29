-- Lab dataset — LAB-CAPACIDAD-S1, evolved from the CAPACIDAD-USL-S1 seed.
--
-- Run with psql variables (BOTH sizes come from this one file, so the shape —
-- the distributions below — is identical and only the scale differs):
--
--   large (the migration-scale set: ~490 k rows / ~171 MB, the exact dataset
--   CAPACIDAD-USL-S1 measured — the baseline any re-run compares against):
--     psql -v productos=20000 -v clientes=8000 -v ordenes=60000 -f seed.sql
--
--   small (a small shop, ~8 k rows):
--     psql -v productos=400 -v clientes=200 -v ordenes=1500 -f seed.sql
--
-- DETERMINISTIC: setseed() below pins the PRNG and parallel workers are
-- disabled for the session (each parallel worker has its own PRNG state, which
-- would make row content depend on the box's core count). The same command on
-- the same PostgreSQL major version produces the same dataset, so two runs of
-- the laboratory measure the same data and their curves can be compared.
--
-- Bloque 5 of the Centinela research: UNIFORM synthetic data lies IN FAVOUR of
-- the system. PostgreSQL's planner estimates selectivity very well on uniform
-- columns and badly on clustered ones (n_distinct underestimated by 20–40× on
-- real, skewed data), so a load test over `random()` data measures a planner
-- that is never wrong. This seed reproduces, deliberately:
--   * a POWER LAW over the foreign keys (a small head of customers and of
--     best-selling products carries most of the rows, with a long tail);
--   * a realistic most-common-value distribution over the status columns;
--   * a TEMPORAL distribution with most rows in the last months, not uniform;
--   * a CORRELATION between status and age (recent orders are pending, old
--     ones delivered) and between city and department — the pairs a
--     single-column histogram gets wrong;
--   * variable-length text, so part of the table lives in TOAST.
--
-- IMPLEMENTATION NOTE, learned the hard way in this session: a random pick
-- written as `CROSS JOIN LATERAL (SELECT id FROM t OFFSET (random()*n)::int
-- LIMIT 1)` has NO reference to the outer row, so PostgreSQL evaluates it ONCE
-- for the whole statement — the first attempt produced 60 000 orders all
-- belonging to the same customer, on the same day. Every pick below is
-- therefore an index computed per row and JOINed against a numbered helper
-- table, which cannot be hoisted. pg_stats is checked afterwards against these
-- intentions; that check is the only proof the data are what they claim.

SET search_path TO tenant_lab, public;
SET max_parallel_workers_per_gather = 0;  -- determinism: one PRNG stream
SELECT setseed(0.4242);

TRUNCATE orden_lineas, pagos, ordenes, direcciones, clientes, reservas_stock,
         variantes, productos, cupones, categorias, tipos_producto CASCADE;

INSERT INTO tipos_producto (nombre, vertical, regimen_iva, tarifa_iva_pct, activo, created_at)
SELECT 'tipo ' || g, (ARRAY['retail','alimentos','servicios','moda'])[1 + g % 4],
       'comun', (ARRAY[0,5,19])[1 + g % 3], true, now() - (g * interval '1 day')
FROM generate_series(1, 8) g;

INSERT INTO categorias (nombre, slug, orden, activa)
SELECT 'categoria ' || g, 'cat-' || g, g, g % 17 <> 0
FROM generate_series(1, 40) g;

CREATE TEMP TABLE ix_cat AS
SELECT row_number() OVER (ORDER BY orden) AS ix, id FROM categorias;
CREATE TEMP TABLE ix_tipo AS
SELECT row_number() OVER (ORDER BY nombre) AS ix, id FROM tipos_producto;
CREATE INDEX ON ix_cat(ix);
CREATE INDEX ON ix_tipo(ix);

INSERT INTO productos (nombre, sku, precio_centavos, estado, destacado, descripcion,
                       categoria_id, tipo_producto_id, atributos, created_at, updated_at)
SELECT
  'producto ' || s.g || ' ' || md5(s.g::text),
  'SKU-' || lpad(s.g::text, 7, '0'),
  (500 + s.r1 * 900000)::bigint,
  CASE WHEN s.r2 < 0.88 THEN 'activo' WHEN s.r2 < 0.97 THEN 'borrador' ELSE 'agotado' END,
  s.r3 < 0.04,
  -- incompressible by construction (md5 chunks), and length spread over two
  -- orders of magnitude, so a real minority of rows lands in TOAST and a
  -- `SELECT *` list pays the detoast the way a migrated system's does.
  (SELECT string_agg(md5(s.g::text || n::text), ' ')
     FROM generate_series(1, 1 + (power(s.r4, 3.0) * 200)::int) n),
  c.id, t.id,
  jsonb_build_object('marca', 'marca-' || (power(s.r5, 2) * 60)::int,
                     'color', (ARRAY['negro','blanco','azul','rojo','verde'])[1 + (s.r6 * 5)::int % 5],
                     'peso_g', (50 + s.r7 * 4000)::int),
  now() - (power(s.r8, 2.0) * 900 * interval '1 day'),
  now() - (power(s.r8, 2.0) * 100 * interval '1 day')
FROM (
  SELECT g, random() r1, random() r2, random() r3, random() r4, random() r5,
         random() r6, random() r7, random() r8,
         1 + (power(random(), 2.2) * 40)::int % 40 AS cat_ix,
         1 + (random() * 8)::int % 8 AS tipo_ix
  FROM generate_series(1, :productos) g
) s
JOIN ix_cat c ON c.ix = s.cat_ix
JOIN ix_tipo t ON t.ix = s.tipo_ix;

INSERT INTO variantes (producto_id, nombre, sku, precio_centavos, stock, stock_reservado, activa, opciones, created_at)
SELECT p.id, 'variante ' || v, 'VAR-' || lpad((p.rn * 3 + v)::text, 8, '0'),
       p.precio_centavos + (random() * 20000)::bigint,
       (power(random(), 1.6) * 400)::int, 0, random() < 0.93,
       jsonb_build_object('talla', (ARRAY['S','M','L','XL'])[1 + (random() * 4)::int % 4]),
       p.created_at
FROM (SELECT id, precio_centavos, created_at, row_number() OVER (ORDER BY sku) rn FROM productos) p,
     generate_series(1, 2) v;

INSERT INTO clientes (nombre, email, telefono, documento_tipo, documento_numero, es_invitado, created_at)
SELECT 'cliente ' || g, 'cliente' || g || '@ejemplo.co', '3' || lpad(g::text, 9, '0'),
       'CC', lpad(g::text, 10, '0'), random() < 0.35,
       now() - (power(random(), 1.8) * 800 * interval '1 day')
FROM generate_series(1, :clientes) g;

INSERT INTO direcciones (cliente_id, linea1, ciudad, departamento, pais, codigo_postal, es_principal)
SELECT c.id, 'calle ' || (s.r1 * 180)::int || ' # ' || (s.r2 * 90)::int,
       ci.ciudad, ci.depto, 'CO', lpad(((s.r3 * 999999)::int)::text, 6, '0'), true
FROM (
  SELECT id, random() r1, random() r2, random() r3,
         1 + (power(random(), 1.7) * 8)::int % 8 AS ciu_ix
  FROM clientes
) s
JOIN clientes c ON c.id = s.id
JOIN (VALUES
   (1,'Bogota','Cundinamarca'),(2,'Medellin','Antioquia'),(3,'Cali','Valle'),
   (4,'Barranquilla','Atlantico'),(5,'Bucaramanga','Santander'),(6,'Pereira','Risaralda'),
   (7,'Manizales','Caldas'),(8,'Cartagena','Bolivar')) ci(ix, ciudad, depto) ON ci.ix = s.ciu_ix;

INSERT INTO cupones (codigo, descuento_pct, activo, vence_en, created_at)
SELECT 'CUP' || lpad(g::text, 4, '0'), (ARRAY[5,10,15,20,30])[1 + g % 5], g % 3 <> 0,
       now() + ((random() * 200 - 60) * interval '1 day'), now() - (g * interval '1 day')
FROM generate_series(1, 50) g;

CREATE TEMP TABLE ix_cli AS
SELECT row_number() OVER (ORDER BY documento_numero) AS ix, c.id, d.id AS dir_id
FROM clientes c JOIN direcciones d ON d.cliente_id = c.id;
CREATE INDEX ON ix_cli(ix);

INSERT INTO ordenes (numero, cliente_id, direccion_id, estado, moneda,
                     subtotal_centavos, impuestos_centavos, envio_centavos,
                     descuento_centavos, total_centavos, envio_metodo, created_at, updated_at)
SELECT
  'ORD-' || lpad(s.g::text, 8, '0'), k.id, k.dir_id,
  CASE
    WHEN s.age < 3  THEN (ARRAY['pendiente','pagada','pendiente'])[1 + (s.r1 * 3)::int % 3]
    WHEN s.age < 12 THEN (ARRAY['pagada','enviada','enviada'])[1 + (s.r1 * 3)::int % 3]
    WHEN s.age < 40 THEN (ARRAY['enviada','entregada','entregada','entregada'])[1 + (s.r1 * 4)::int % 4]
    ELSE                 (CASE WHEN s.r1 < 0.06 THEN 'cancelada' ELSE 'entregada' END)
  END,
  'COP', s.sub, (s.sub * 0.19)::bigint, 800000, 0, (s.sub * 1.19)::bigint + 800000,
  (ARRAY['domicilio','recoger'])[1 + (s.r2 * 2)::int % 2],
  now() - (s.age * interval '1 day'),
  now() - (s.age * 0.9 * interval '1 day')
FROM (
  SELECT g, random() r1, random() r2,
         (power(random(), 3.0) * 730)::numeric AS age,
         (200000 + random() * 5000000)::bigint AS sub,
         1 + (power(random(), 3.0) * :clientes)::int % :clientes AS cli_ix
  FROM generate_series(1, :ordenes) g
) s
JOIN ix_cli k ON k.ix = s.cli_ix;

CREATE TEMP TABLE ix_prod AS
SELECT row_number() OVER (ORDER BY sku) AS ix, id, sku, nombre, precio_centavos FROM productos;
CREATE INDEX ON ix_prod(ix);

INSERT INTO orden_lineas (orden_id, producto_id, sku, nombre_producto,
                          cantidad, precio_unitario_centavos, tarifa_iva_pct,
                          iva_centavos, descuento_centavos, total_linea_centavos)
SELECT s.orden_id, p.id, p.sku, p.nombre,
       s.cantidad, p.precio_centavos, 19,
       (p.precio_centavos * s.cantidad * 0.19)::bigint, 0,
       (p.precio_centavos * s.cantidad * 1.19)::bigint
FROM (
  SELECT o.id AS orden_id,
         1 + (power(random(), 2.0) * 4)::int AS cantidad,
         1 + (power(random(), 2.6) * :productos)::int % :productos AS prod_ix
  FROM ordenes o
  CROSS JOIN LATERAL generate_series(1, 1 + (power(random(), 2.0) * 5)::int) line
) s
JOIN ix_prod p ON p.ix = s.prod_ix;

INSERT INTO pagos (orden_id, proveedor, metodo, estado, moneda, monto_centavos,
                   referencia, signature_ok, created_at, updated_at, estado_actualizado_en)
SELECT o.id, 'mock', (ARRAY['tarjeta','pse','efectivo'])[1 + (random() * 3)::int % 3],
       CASE WHEN o.estado = 'pendiente' THEN 'pendiente'
            WHEN o.estado = 'cancelada' THEN 'rechazado' ELSE 'aprobado' END,
       'COP', o.total_centavos, 'REF-' || substr(md5(o.id::text), 1, 12), true,
       o.created_at, o.updated_at, o.updated_at
FROM ordenes o WHERE random() < 0.92;

ANALYZE;
