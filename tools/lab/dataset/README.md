# The lab dataset — data that does not lie in the system's favour

Uniform `random()` data makes PostgreSQL's planner look infallible: it
estimates selectivity on uniform columns almost perfectly and under-estimates
`n_distinct` by 20–40× on real, clustered data (Bloque 5 of the Centinela
research). A capacity number measured over uniform data is therefore
optimistic in a way production will not honour.

`seed.sql` generates, deterministically (`setseed(0.4242)`, parallel workers
off), the same SHAPE at two SIZES:

| size | command (`psql -v …`) | rows | what it models |
|---|---|--:|---|
| **large** | `productos=20000 clientes=8000 ordenes=60000` | ~490 k / ~171 MB | the real migration CAPACIDAD-USL-S1 measured — the baseline every re-run compares against |
| **small** | `productos=400 clientes=200 ordenes=1500` | ~8 k | a small shop just after launch |

The deliberate realism, and how to verify each claim after seeding:

* **Power law over the FKs** — the top 1 % of customers hold ~21 % of the
  orders (`power(random(), 3.0)` pick index). Verify:
  `SELECT count(*) FROM ordenes GROUP BY cliente_id ORDER BY 1 DESC LIMIT 5;`
* **Status correlated with age** — recent orders are `pendiente`, old ones
  `entregada`; a single-column histogram cannot see the pair.
* **City correlated with department** — always consistent pairs, the
  functional dependency `pg_stats` per-column stats miss.
* **Temporal clustering** — `power(random(), 3.0) * 730 days` puts most rows
  in the recent months.
* **TOAST** — a third of `productos.descripcion` is long, incompressible md5
  text, so `SELECT *` pays real detoasting.
* **The planner error is present, on purpose.** Verify:
  `SELECT n_distinct FROM pg_stats WHERE tablename='orden_lineas' AND attname='producto_id';`
  On the large set this estimates ~15 k against ~19.8 k actual — the
  under-estimate uniform data would hide.

`schema.json` is the 14-resource commerce schema the engine serves in the
laboratory — the same schema the CAPACIDAD-USL-S1 baseline ran, kept verbatim
so curves stay comparable point by point.

Implementation gotcha (kept from the original seed, do not regress it): a
random pick written as `CROSS JOIN LATERAL (SELECT … OFFSET random()… LIMIT 1)`
references no outer column, so PostgreSQL hoists it and evaluates it ONCE for
the whole statement — the first attempt produced 60 000 orders belonging to one
customer on one day. Every pick here is a per-row computed index JOINed against
a numbered temp table, which cannot be hoisted.
