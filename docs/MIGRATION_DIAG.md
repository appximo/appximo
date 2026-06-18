# Diagnóstico de migraciones — ¿qué hace el motor cuando el schema EVOLUCIONA sobre datos reales?

> ⚠️ **ACTUALIZACIÓN (Fase 0 INTEGRADA, MIG-F0-S5).** Este diagnóstico describe el
> **convergedor histórico** (`CREATE TABLE / ADD COLUMN IF NOT EXISTS`), que **ya fue
> reemplazado** por el motor de migraciones real (`pkg/schemadiff`) en
> `migration.ApplyTenantMigration` (introspect → desired → diff → apply seguro). Lo que
> el motor hace HOY frente a cada caso:
> - **#5 (sin diff/plan):** CERRADO — ahora hay introspect + diff tipado + `Validate`
>   (concerns logueados antes de aplicar).
> - **#3 (`required`/NOT NULL sobre datos):** CERRADO — el NOT NULL se aplica FIEL: en
>   tabla vacía es real, sobre datos **falla fuerte y hace rollback atómico** (nunca la
>   divergencia silenciosa nullable).
> - **#1 (rename):** CERRADO (MIG-F1-S2) — `renamed_from` (campo y recurso) en el schema JSON
>   declara el nombre anterior; el motor emite `ALTER … RENAME COLUMN`/`RENAME TO` (metadata-only,
>   **los datos quedan en la columna/tabla renombrada, accesibles bajo el nombre nuevo**; FK/
>   unique/índice la siguen, sin churn). Validado al load (el nombre viejo no debe seguir
>   existiendo). Inerte tras aplicar → re-provision no-op. Cierra el peor bug del caso F.
> - **#4 (cambio de tipo):** la capacidad existe (`ALTER … TYPE … USING`) y se aplica si el diff
>   lo detecta (cambiar el `type` de un campo); falla fuerte sobre datos no convertibles.
> - **#6/#7 (drop de campo/recurso / locking):** el DROP sigue **GATEADO por defecto**
>   (política v1 aditiva, nunca borra → caso D = drift), PERO ahora hay un **gate de
>   aprobación** (MIG-F1-S3) que lo hace aplicable de forma CONTROLADA: un `--dry-run` /
>   `PUT {"dry_run":true}` reporta cada drop destructivo (DropTable/DropColumn) con su
>   IMPACTO (filas que se pierden) sin aplicar nada, y la operación se ejecuta SOLO si se
>   ENUMERA explícitamente (`--approve-drops "empleados.telefono,proyectos"` /
>   `{"approved_drops":[…]}`). Sin la enumeración exacta sigue gateado (cero pérdida por
>   accidente); el worker/registro NUNCA auto-aprueba. Todo DDL aplicado pasa por
>   `lock_timeout`+retry y los índices por `CONCURRENTLY`.
> - **#2 (integridad referencial / FK):** CERRADO (MIG-F1-S1) — un campo con `relation`
>   ahora crea una FOREIGN KEY real con `on_delete` declarativo (`restrict` por defecto
>   = seguro, `cascade`, `set_null`). Borrar un referenciado ya NO orfaniza en silencio:
>   restrict → **409** claro (REST + GraphQL), cascade borra hijos, set_null anula la FK.
>   Aplicada segura (NOT VALID/VALIDATE); datos previos inconsistentes → FK queda NOT VALID
>   (protege adelante), no rompe el provisioning. Columna FK auto-indexada.
> - **Gate de aprobación de destructivas:** CERRADO (MIG-F1-S3) — ver #6/#7 arriba (dry-run
>   con impacto + aprobación enumerada explícita; default gateado, worker no auto-aprueba).
> - **#8 (orquestador multi-tenant):** CERRADO (MIG-F1-S4) — `migration.RunFanout` +
>   `appitools migrate --all-tenants`/`--tenants` aplican un cambio de schema a los N tenants
>   de forma RESILIENTE (un tenant que falla NO aborta a los sanos; se registra en
>   `public.migration_log` y se reporta) y REANUDABLE (re-correr salta los ya migrados —
>   diff vacío = no-op — y reintenta los fallidos; el diff idempotente hace que "reanudar"
>   == "volver a correr"). Cada tenant se aplica bajo SU advisory lock (el mismo del worker)
>   y atómicamente (rollback por batch → un fallo deja al tenant en su estado previo, nunca a
>   medias). ADITIVO por defecto (NUNCA auto-aprueba un drop, como el worker); un drop masivo
>   exige `--approve-drops` enumerado y el dry-run muestra el impacto AGREGADO (filas perdidas
>   × tenants). Secuencial en v1. Es el diferencial vs Prisma (sin fan-out nativo) y
>   django-tenants (frágil ante fallo parcial). Pendiente: el paralelismo acotado del fan-out
>   (optimización).
> - **Cobertura COMPLETA de FKs:** CERRADA (MIG-F1-S5) — `on_update` declarativo
>   (restrict/cascade/set_null, default NO ACTION = sin churn sobre FKs existentes), FK a una
>   columna `unique` que no sea `id` (`references`), y FKs COMPUESTAS multi-columna (bloque
>   `foreign_keys` a nivel de recurso → PK/unique compuesta del target). El modelo canónico
>   (S1) y el render seguro (S4) ya soportaban estas formas; S5 puentea la sintaxis + la
>   validación al load + el mapeo en buildDesiredSchema, y arregla un bug de introspección (un
>   índice unique referenciado por una FK quedaba excluido vía `conindid` → re-add espurio;
>   ahora sólo se excluyen los índices que respaldan constraints p/u/x). Ya no quedan límites
>   v1 de FKs.
>   **Con #1 (rename) + #2 (FK) + #3 (NOT NULL) + #5 (diff/plan) cerrados, el locking
>   protegido, el gate de aprobación de destructivas Y el orquestador multi-tenant reanudable,
>   los tres grandes 🔴 del diagnóstico están cerrados, evolucionar un schema (incl. drops) es
>   seguro, y propagar ese cambio a N tenants es resiliente y reanudable** — el motor es
>   production-safe e invocable desde el schema. La IA puede PROPONER cambios, el dry-run
>   los MUESTRA (con el impacto, por-tenant y agregado), y un humano APRUEBA lo destructivo
>   enumerándolo: ningún error destruye datos en silencio, y un fallo en un tenant no
>   bloquea a los demás.
>
> El resto del documento queda como **registro histórico** del comportamiento PRE-integración.

> **Tipo:** diagnóstico empírico (como el model-lab). **No** cambia el motor — descubre
> y documenta el comportamiento actual con evidencia reproducible.
> **Fecha:** 2026-06-16. **Commit base:** `1777538` (G1–G6 cerrados).
> **Método:** se levantó una segunda instancia del binario (`--schema base.json --port 8090`,
> plano de control compartido `:9090`), se registró un tenant por caso con datos reales
> insertados vía la API, se aplicó un schema modificado vía `PUT /tenants/{id}/schema`
> (que ejecuta el mismo `migration.ApplyTenantMigration` del registro) y se inspeccionó
> el estado de las tablas con `psql` (`information_schema`, `pg_constraint`, `pg_indexes`)
> y la API. Esquemas y evidencia: cada caso abajo.

---

## TL;DR

**Appitools no tiene un sistema de migraciones. Tiene un _convergedor_ de tablas
idempotente.** El DDL que aplica al evolucionar un schema se reduce a exactamente dos
operaciones, ambas aditivas y "si no existe":

```
CREATE TABLE IF NOT EXISTS …
ALTER TABLE … ADD COLUMN IF NOT EXISTS … <tipo>   (siempre NULLABLE, sin DEFAULT, sin UNIQUE)
```

No existe `DROP COLUMN`, ni `ALTER COLUMN … TYPE`, ni `RENAME`, ni `ADD CONSTRAINT`, ni
`FOREIGN KEY`, ni backfill, ni historial de versiones, ni un _diff_ que compare el schema
nuevo contra el estado actual. Consecuencia directa: las operaciones **seguras** funcionan
bien y en vivo; las **peligrosas** (NOT NULL sobre datos, default sobre datos, cambio de
tipo, rename, borrar campo) se **ignoran o divergen en silencio** — en lugar de avisar,
bloquear o fallar al aplicar, como haría un ORM maduro. Además **no se genera ninguna
integridad referencial** (cero
foreign keys): borrar un registro referenciado **orfaniza en silencio** a sus hijos.

| Caso | Operación | Veredicto |
|---|---|---|
| A | Agregar campo nullable | 🟢 seguro |
| B | Agregar campo `required` (NOT NULL) sin default | 🔴 NOT NULL se descarta en silencio; el DB acepta NULL |
| C | Agregar campo `required` **con** default | 🔴 sin backfill: filas viejas quedan NULL, no toman el default |
| D | Eliminar un campo | 🟡 no se borra la columna ni los datos (drift permanente; PII no se elimina) |
| E | Cambiar el tipo de un campo | 🔴 no-op silencioso: la columna conserva el tipo viejo (diverge del schema) |
| F | Renombrar un campo | 🔴 drop+add: datos quedan varados en la columna vieja **y se rompen todos los INSERT futuros** |
| G | Agregar una relación (FK) | 🟢 columna + índice; pero **sin** constraint FK (sin integridad) |
| — | Integridad referencial / borrado en cascada | 🔴 sin FK: borrar lo referenciado **orfaniza en silencio** (no RESTRICT, no CASCADE, no configurable) |
| — | Locking en producción | 🟡 sin `lock_timeout`/`statement_timeout`/reintento/`CONCURRENTLY` en el path síncrono |
| — | Multi-tenant | 🟡 solo per-tenant; sin orquestador para migrar N tenants (pero bien posicionado) |

---

## PASO 0 — El mecanismo real

### Dónde y cómo se aplica el schema a las tablas

Hay **un solo** motor de DDL, [`migration.ApplyTenantMigration`](../pkg/migration/runner.go#L20),
invocado desde dos lugares:

1. **Al registrar un tenant** — [`controlplane.RegisterTenant` paso 8](../pkg/controlplane/tenant_service.go#L148):
   tras `CREATE SCHEMA tenant_<id>`, corre `ApplyTenantMigration` (CREATE TABLE por recurso).
2. **Al actualizar el schema de un tenant existente** —
   [`controlplane.UpdateSchema`](../pkg/controlplane/service.go#L57) (`PUT /tenants/{id}/schema`,
   plano de control `:9090`): hace `UPDATE public.tenants SET json_schema` y **acto seguido
   corre el mismo `ApplyTenantMigration` síncronamente** (líneas 73–80). Es el único camino
   de "evolucionar el schema de un tenant con datos". (Si hay Redis, además encola un job
   async que vuelve a correr el mismo DDL idempotente; es redundante, no distinto.)

### Qué hace exactamente `ApplyTenantMigration` ([runner.go](../pkg/migration/runner.go))

Para cada recurso (orden alfabético):

```go
CREATE TABLE IF NOT EXISTS "<schema>"."<recurso>" ( … )   // buildCreateTable
ALTER TABLE … ADD COLUMN IF NOT EXISTS …                   // addMissingColumns
```

luego crea los índices de FK de relaciones (`ensureRelationIndexes`) y los `indexes`
declarados (`ensureDeclaredIndexes`).

**No compara el schema nuevo contra el estado de la tabla.** No hay _diff_. La única
"comparación" es: `addMissingColumns` lee `information_schema.columns`, y para cada campo
del schema que **no** está en la tabla, emite un `ADD COLUMN`. Todo lo demás
(columnas que sobran, columnas cuyo tipo/constraint cambió, renames) es **invisible** para
el convergedor: si la columna ya existe por nombre, se la salta por completo.

Tres hechos del código que explican todos los casos de abajo:

- **`addMissingColumns` ([runner.go:196](../pkg/migration/runner.go#L196)) agrega SIEMPRE
  columnas nullable, sin default, sin unique.** El comentario lo dice literal: _"New columns
  are always nullable — adding NOT NULL to an existing table with rows requires a DEFAULT."_
  El `ADD COLUMN` solo lleva nombre + tipo Postgres; `required`/`unique`/`default` del
  campo **se descartan** en este path.
- **`buildCreateTable` ([runner.go:247](../pkg/migration/runner.go#L247)) aplica `NOT NULL`
  y `UNIQUE` solo en el CREATE inicial, y NUNCA un `DEFAULT`** para campos normales (solo los
  `auto` reciben `DEFAULT now()`). El `default` del schema es de capa-app (Go `ApplyDefaults`,
  solo en create), nunca un `DEFAULT` de Postgres — verificado: la columna `status` con
  `default:"active"` quedó `default=<none>` en la tabla.
- **No existe ningún `DROP`, `ALTER … TYPE`, `RENAME`, `REFERENCES`, `FOREIGN KEY`,
  `ON DELETE`, ni `CONCURRENTLY`** en todo `pkg/migration` (grep verificado). El convergedor
  solo sabe **crear** y **agregar**.

### Boot-schema vs stored-schema (divergencia estructural de fondo)

Hay **dos fuentes de schema** que se actualizan por separado:

- El **`--schema` de boot** compila rutas, tipos GraphQL, RBAC y los **validadores de
  capa-app** (tipo/`required`/`enum`) — **fijo durante el proceso**.
- El **stored-schema por tenant** (`public.tenants.json_schema`) maneja el **DDL de las
  tablas** vía `ApplyTenantMigration`.

`UpdateSchema` cambia el segundo y re-corre el DDL, pero el primero solo cambia al
reiniciar el proceso (el propio README lo dice: _"column-level only; adding a new resource
requires a process restart"_). El plano de datos toma el **DB como fuente de verdad de las
columnas escribibles** (sin whitelist — ver el NOTE en `pool.go:37` y `db.UndefinedColumnField`),
así que una columna nueva agregada por `UpdateSchema` es escribible en vivo, pero **el tipo
y el `required` que la validan viven en el boot-schema**. Esta separación es el telón de
fondo de los casos B/C/E: el constraint del schema y el de la columna pueden discrepar.

---

## PASO 1 — Cada caso de evolución sobre datos reales

Base (3 filas reales insertadas vía API en `widgets`): columnas
`id(uuid,NOT NULL) name(text,NOT NULL) note(text) price(double) qty(integer) status(text)`.
Nótese ya que `status` (con `default:"active"` en el schema) quedó **sin** `DEFAULT` en el DB.

### A — Agregar un campo nullable → 🟢 SEGURO

`ADD COLUMN color text` (nullable). Las 3 filas viejas quedan `color = NULL`. Es la
operación segura canónica y funciona perfecto, en vivo, idempotente.

```
color | text | nullable=YES | default=<none>
alpha|color_is_null=t   beta|t   gamma|t
```

### B — Agregar `required` (NOT NULL) **sin** default → 🔴 DIVERGENCIA SILENCIOSA

Postgres rechazaría `ADD COLUMN sku TEXT NOT NULL` sobre una tabla con filas. El motor lo
**esquiva descartando el NOT NULL**: agrega `sku text` **nullable**. No hay error ni crash
(a diferencia de un ORM maduro como Prisma, que ante esto avisa/bloquea o la migración
**falla al aplicar** contra los datos existentes, en vez de divergir en silencio), pero:

```
sku | text | nullable=YES | default=<none>          ← el schema dice required:true
-- INSERT directo con sku OMITIDO:
nullsku_test | sku_null=t   → INSERT 0 1             ← el DB ACEPTA NULL en un campo "required"
alpha|<vacío>  beta|<vacío>  gamma|<vacío>           ← filas viejas NULL
```

El `required` queda como **única garantía la capa-app** (el validador del boot-schema, solo
para writes nuevos por la API). El DB no lo refleja, las filas viejas son NULL, y cualquier
write directo o futuro que confíe en el constraint del DB estaría mal. **El schema miente
sobre el estado real de la tabla.**

### C — Agregar `required` **con** default (`"us"`) → 🔴 SIN BACKFILL

Es la operación que un ORM maduro hace **segura** (`ADD COLUMN region TEXT NOT NULL DEFAULT
'us'` rellena todas las filas viejas con `'us'`). El motor la hace **insegura**: ignora
tanto el `required` como el `default` en el DDL. Resultado:

```
region | text | nullable=YES | default=<none>       ← sin DEFAULT en el DB
alpha|<NULL>  beta|<NULL>  gamma|<NULL>              ← NO se backfillea; quedan NULL, no "us"
```

Leer una fila vieja devuelve `region: null` para un campo que el schema declara
required-con-default. El default es create-only de capa-app, así que **las filas existentes
nunca lo toman**. Esta es, paradójicamente, la divergencia más sorpresiva: la migración
"segura por excelencia" de los ORMs aquí deja datos inconsistentes.

### D — Eliminar un campo → 🟡 NO BORRA (drift permanente)

Quitar `note` del schema **no dropea la columna**: `note` sigue en la tabla con **todos sus
datos intactos**.

```
-- columnas AFTER: note | text | nullable=YES   (sigue presente)
alpha|first  beta|second  gamma|third            (datos intactos)
```

No hay pérdida de datos (bien), pero:
- La columna queda huérfana **para siempre** (no hay forma de borrarla por schema) → drift.
- Un campo "eliminado" con PII **no se elimina realmente** (ángulo cumplimiento/GDPR: "borrá
  esta columna" no la borra).
- Re-agregar `note` después es un no-op (`IF NOT EXISTS`) que **resucita los datos viejos**.

### E — Cambiar el tipo de un campo → 🔴 NO-OP SILENCIOSO

`qty` de `int` → `string`. Como la columna ya existe por nombre, `addMissingColumns` la
**saltea**; no hay `ALTER … TYPE`. El tipo del DB **no cambia**:

```
qty BEFORE: integer        qty AFTER: integer       ← el schema ahora dice string
```

Ni error, ni conversión, ni crash: el cambio de tipo es **imposible** por el motor y se
ignora. La divergencia (validador/query-builder del schema creen `string`, la columna es
`integer`) puede producir, tras un reinicio con el schema nuevo, errores de tipo en
filtros numéricos o en la serialización, o coerciones silenciosas. El caso "string→int con
datos no convertibles" nunca corrompe **porque nunca se intenta** el ALTER.

### F — Renombrar un campo → 🔴 DROP+ADD: DATOS VARADOS + ESCRITURAS ROTAS

`name` → `title`. El motor lo trata como dos operaciones independientes: agrega `title`
(nullable, NULL en filas viejas pese a `required`) y **conserva `name`** con sus datos:

```
-- AFTER: name (text, NOT NULL, con datos) Y title (text, nullable, NULL en todas)
name_col=alpha title_col=<NULL>   beta|<NULL>   gamma|<NULL>
```

Dos consecuencias, ambas graves:
1. **Pérdida de datos efectiva desde la API**: todas las filas viejas leen `title = NULL`;
   los valores reales quedan varados en la columna `name`, ya invisible para la API.
2. **🔴 Se rompen TODOS los INSERT futuros.** La columna vieja `name` sigue siendo `NOT NULL`
   y nadie la rellena (la API ahora manda `title`). Verificado: un INSERT estilo-nuevo
   (solo `title`) **falla**:

   ```
   ERROR: null value in column "name" of relation "widgets" violates not-null constraint
   ```

   O sea: tras un rename, el recurso queda con escritura **caída** hasta intervención manual.

### G — Agregar una relación (FK) → 🟢 SE APLICA, pero SIN integridad

Agregar `category_id` (uuid, `relation:"categories"`) + bloque `relations` belongs_to:

```
category_id | uuid | nullable=YES                    (filas viejas NULL)
idx_widgets_category_id  CREATE INDEX … (category_id) (índice btree creado)
fk_constraints = 0                                    (CERO foreign keys)
```

La columna se agrega y se indexa, sin crash ni pérdida. Pero **no se crea ningún constraint
FK**: nada valida que `category_id` apunte a una categoría real, ni en las filas viejas
(NULL) ni en las nuevas. Es "seguro" de aplicar, pero la relación es puramente convencional.

---

## PASO 2 — Integridad referencial y borrado en cascada → 🔴 ORFANATO SILENCIOSO

Tenant `mdrel` con `categories` (padre), `products` (hijo, `category_id`) y `product_tags`
(junction m2m `products`↔`tags`). Datos reales: 2 categorías, 3 productos (2 bajo "hardware"),
2 tags, 2 filas de junction ligando "bolt" a ambos tags.

**Hecho base: cero foreign keys en todo el schema del tenant** (`pg_constraint contype='f' = 0`).
El motor **nunca** emite `REFERENCES`/`ON DELETE` — confirmado en código y en runtime. Una
relación declarativa (ADR-019) crea **un índice** sobre la columna FK, nada más.

**(1) Borrar una categoría que TIENE hijos:**

```
DELETE /api/categories/<hardware>  → HTTP 204   (¡se borra sin chistar!)
-- productos hijos sobreviven con category_id colgando:
bolt | parent_missing=t      nut | parent_missing=t
```

No es RESTRICT (no bloqueó), no es CASCADE (no borró los hijos), no es SET NULL (no anuló la
FK). Es **orfanato**: los hijos quedan apuntando a un padre que ya no existe.

**(2) Borrar un producto referenciado por la junction m2m:**

```
DELETE /api/products/<bolt>  → HTTP 204
-- las 2 filas de product_tags sobreviven como huérfanas:
product_missing=t   product_missing=t
```

**(3) ¿El embed anidado sobrevive a la FK colgante?** Un `GET …/products/<nut>?include=category`
sobre un producto huérfano devuelve grácilmente `"category": null` (el `LEFT JOIN LATERAL` no
encuentra padre) — **sin 500**. Las lecturas degradan a `null`, pero el modelo quedó
silenciosamente inconsistente.

**Resumen integridad referencial:**
- **Acción de borrado:** ninguna declarada → el efecto **no es ni siquiera el NO ACTION de
  Postgres** (que requiere una FK para bloquear); al no haber FK, el borrado **siempre
  procede** y orfaniza. Peor que un RESTRICT (que al menos protegería) y peor que un 500
  (que al menos avisaría): corrompe en silencio.
- **¿Configurable?** No. No hay `onDelete`/`on_delete` en el schema (grep: no existe). No se
  puede elegir CASCADE/RESTRICT/SET NULL.
- **¿Seguro?** El borrado no da error (no hay 500 feo), pero acumula huérfanos y rompe la
  consistencia referencial sin que nadie se entere. Para una app real (un ecommerce que
  borra una categoría, un fintech que borra una cuenta con asientos) esto es peligroso.

---

## PASO 3 — Locking en producción → 🟡 SIN PROTECCIÓN (oportunidad)

Inspección de código (no se generó carga, como pide el brief):

- **No hay `lock_timeout` ni `statement_timeout` en ningún lado** (grep en todo el repo: 0
  resultados). Ni en el path de DDL, ni en la configuración del pool
  ([`db.NewPool`](../pkg/db/pool.go#L15) setea `MaxConns`, lifetimes y `DescribeExec`, pero
  **ningún** timeout de lock/statement).
- **No hay reintento** ante fallo de adquisición de lock en el path síncrono
  (`ApplyTenantMigration` corre el `Exec` directo, sin envoltura de retry).
- **Los índices se crean NO concurrentemente** ([runner.go:139](../pkg/migration/runner.go#L139)):
  el comentario asume que "en el registro la tabla es nueva/vacía, así que el build es
  instantáneo y libre de lock", y deja el `CREATE INDEX CONCURRENTLY` sobre tablas grandes
  como "una migración manual separada, documentada".
- El **advisory lock** que menciona el brief existe **solo en el worker de Redis**
  ([worker.go:137](../pkg/migration/worker.go#L137), `pg_try_advisory_lock`) y solo serializa
  migraciones concurrentes del **mismo** tenant; **no** protege el path síncrono de
  `UpdateSchema` ni es un `lock_timeout`.

**Riesgo:** el propio pool documenta que se espera DDL en vivo
([pool.go:37](../pkg/db/pool.go#L37): _"a control-plane deploy runs ALTER TABLE ADD COLUMN
under live traffic"_). Un `ADD COLUMN` (aunque nullable y metadata-only en PG11+) igual toma
brevemente un `ACCESS EXCLUSIVE` que debe **encolar detrás de cualquier transacción larga en
curso** y, mientras espera, **bloquea toda nueva query** sobre esa tabla. Un `CREATE INDEX`
no concurrente toma un `SHARE` que bloquea writes durante todo el build. Sin `lock_timeout`
que aborte rápido ni reintento, sobre una tabla grande y ocupada esto puede degradar la
disponibilidad. Hoy el impacto es bajo porque casi todo el DDL ocurre en el registro
(tabla vacía), pero el `ADD COLUMN` sobre tablas con datos sí corre por este path
desprotegido.

**Oportunidad / diferencial:** como el motor **genera el DDL a mano**, podría inyectar
`SET lock_timeout` + reintento con backoff **por defecto** en cada statement de evolución —
algo que Prisma/Doctrine dejan como responsabilidad del usuario. Sería seguridad de
producción "gratis" para todos los tenants.

---

## PASO 4 — El ángulo multi-tenant → 🟡 solo per-tenant (el diferencial más grande, sin construir)

Estado actual:
- `UpdateSchema` opera sobre **un** tenant (`tenant_<id>`). El plano de control expone
  `POST /tenants`, `PUT /tenants/{id}/schema`, `GET …` — todo per-id. **No hay ninguna ruta
  ni rutina que aplique un cambio de schema a TODOS los tenants.**
- El stream de migraciones de Redis ([worker.go](../pkg/migration/worker.go)) transporta jobs
  **por-tenant** (un `tenant_id` por mensaje); no hay un productor que enumere todos los
  tenants y los encole.
- Hoy cada tenant se actualiza solo cuando alguien llama explícitamente a `UpdateSchema` para
  ese id (o al registrarse). Un cambio en el `--schema` de boot **no** se propaga a los
  tenants existentes; solo cambia el plano de datos del proceso (validadores/rutas) al
  reiniciar.

**Pero la base es la correcta y está mejor posicionada que la de Prisma:** schema-per-tenant
+ DDL idempotente (`IF NOT EXISTS`) + advisory lock per-tenant (ya existe en el worker) +
`public.tenants` como inventario de tenants. Todas las piezas para un **orquestador de
migración multi-tenant seguro** ya están; falta el lazo que las una: enumerar
`public.tenants`, y para cada uno aplicar el DDL idempotente bajo su advisory lock, con
reporte de progreso y reanudable (idempotente ⇒ reintentable sin daño).

**Por qué es el diferencial:** Prisma **no tiene un orquestador de migración multi-tenant
nativo** — su datasource es single-schema y aplicar a N tenants es manual/externo (loop +
reconexión; `multiSchema` cubre solo un set fijo declarado, no un fan-out dinámico por
tenant); Django lo hace secuencial y pesado (una migración por tenant, orquestada por el
usuario con django-tenants). Appitools, por diseño, ya es schema-per-tenant
con DDL convergente — "migrar N tenants de forma segura, idempotente y reanudable" es una
capacidad que le sale natural y que los ORMs maduros manejan mal.

---

## Tabla de GAPS priorizada (pérdida de datos / crash primero)

| # | Gap | Gravedad | Qué pasa hoy | Qué hace un ORM maduro |
|---|---|---|---|---|
| 1 | **Rename de campo** (caso F) | 🔴 CRÍTICO | drop+add: datos viejos varados en la columna vieja **y todos los INSERT futuros fallan** (NOT NULL huérfano) | detecta/pregunta el rename y hace `ALTER … RENAME COLUMN` (preserva datos) |
| 2 | **Integridad referencial / cascada** (Paso 2) | 🔴 ALTO | sin FK: borrar lo referenciado orfaniza hijos y junctions en silencio; no configurable | declara la FK + `onDelete` (CASCADE/RESTRICT/SET NULL) |
| 3 | **`required`/`default` al agregar sobre datos** (casos B, C) | 🔴 ALTO | NOT NULL descartado, default sin backfill → filas viejas NULL en campos "required"; el schema miente | `ADD COLUMN NOT NULL DEFAULT x` (backfillea) o falla con mensaje claro |
| 4 | **Cambio de tipo** (caso E) | 🔴 ALTO | no-op silencioso: columna y schema divergen → posibles errores de tipo tras reinicio | `ALTER … TYPE … USING` (con plan de conversión) o falla explícito |
| 5 | **Sin _diff_ / plan / gate de seguridad** (transversal) | 🔴 ALTO | el validador aprueba CUALQUIER cambio (valida el schema aislado, sin comparar contra el estado) → los 4 de arriba pasan sin aviso | genera un plan, clasifica cada cambio seguro/destructivo y exige confirmación |
| 6 | **Eliminar campo/recurso** (caso D) | 🟢 CERRADO (MIG-F1-S3) | gateado por defecto (drift), pero aplicable con **aprobación enumerada** tras un dry-run que muestra el impacto (filas perdidas); PII borrable de forma controlada | `DROP COLUMN`/`DROP TABLE` con consentimiento informado |
| 7 | **Locking sin protección** (Paso 3) | 🟡 MEDIO | sin `lock_timeout`/reintento/`CONCURRENTLY` en el path síncrono | (la mayoría tampoco; oportunidad de superar) |
| 8 | **Migración multi-tenant** (Paso 4) | 🟢 CERRADO (MIG-F1-S4) | orquestador de fan-out reanudable (`migrate --all-tenants`): resiliente ante fallo parcial (no aborta, registra, reanuda), aditivo por defecto, drop masivo requiere aprobación enumerada | Prisma sin fan-out nativo; Django secuencial/frágil |

> Nota: 1–5 pueden **perder datos, corromper integridad o tirar la escritura** y deberían
> cerrarse antes de la etapa de IA (que evolucionará schemas constantemente). 6–8 son de
> higiene/operación. El gap **#5 es el paraguas**: sin un _diff_ que compare schema-nuevo vs
> estado-actual, el motor no puede ni siquiera **detectar** que un cambio es peligroso.

---

## Diferenciales posibles (oportunidades para el roadmap)

1. **`lock_timeout` + reintento automáticos en el DDL generado.** Como el motor escribe el
   SQL a mano, puede envolver cada `ALTER`/`CREATE INDEX` con `SET lock_timeout` + backoff y
   usar `CREATE INDEX CONCURRENTLY` sobre tablas no vacías — seguridad de producción que
   Prisma/Doctrine dejan al usuario. **Esfuerzo bajo, impacto alto.**
2. **Orquestador de migración multi-tenant seguro y reanudable.** Enumerar `public.tenants`,
   aplicar el DDL idempotente por tenant bajo su advisory lock, con progreso. Es el
   diferencial más grande vs Prisma (sin fan-out multi-tenant nativo). **Esfuerzo medio.**
3. **Un _planner_/_diff_ de migraciones (el gate de seguridad tipo Prisma).** Comparar el
   schema entrante contra `information_schema` y clasificar cada cambio: seguro (add nullable),
   destructivo (drop, type change), o que-requiere-estrategia (NOT NULL/default → expand-
   backfill-contract; rename → detección explícita). Rechazar o guiar los peligrosos. Esto
   **convierte el convergedor en un sistema de migraciones de verdad** y es el prerequisito
   real para la etapa de IA. **Esfuerzo alto, pero es el corazón del problema.**
4. **Expand/contract + rename con preservación de datos.** `ADD COLUMN nullable` → backfill →
   `SET NOT NULL` generado; detección de rename → `RENAME COLUMN`. Cierra los casos B/C/F sin
   downtime.

---

## Comparación honesta vs Prisma / Doctrine / Django

**Por debajo de los ORMs maduros (hoy Appitools no compite en migraciones):**
- Sin historial/versionado de migraciones (Prisma `_prisma_migrations`, Django `django_migrations`,
  Doctrine `migration_versions`). Appitools no recuerda qué se aplicó.
- Sin _diff_/plan: no genera ni muestra el SQL del cambio antes de aplicarlo.
- Sin detección de rename, sin _down_/rollback, sin backfill, sin guard de cambios destructivos.
- Sin FK ni `onDelete`. Sin `DROP COLUMN`, sin `ALTER … TYPE`, sin `RENAME`.
- Los cuatro cambios peligrosos (B/C/E/F) divergen **en silencio**; un ORM maduro al menos
  **avisa/bloquea** (o la migración **falla al aplicar**) ante un cambio que perdería datos,
  en vez de aceptarlo callado.

**A la par:**
- Agregar una **columna nullable bajo tráfico en vivo** (zero-downtime additive): Appitools lo
  hace limpio e idempotente (`ADD COLUMN IF NOT EXISTS`), igual que los maduros — y su
  idempotencia `IF NOT EXISTS` es de hecho cómoda para convergencia/reintentos.

**Donde podría superarlos:**
- **Migración multi-tenant** (schema-per-tenant + DDL idempotente + advisory lock per-tenant):
  estructuralmente mejor posicionado que Prisma (sin fan-out multi-tenant nativo; datasource
  single-schema) y que Django (secuencial/pesado). Es el terreno donde Appitools puede ganar.
- **`lock_timeout` + reintento por defecto** en el DDL generado: una protección de producción
  que casi ningún ORM aplica de fábrica, y que aquí sale gratis porque el SQL es generado.

---

## Apéndice — reproducir

```bash
# Engine-2 (plano de datos en :8090; el control plane :9090 lo sirve engine-1, DB compartido)
set -a; source /root/.appitools-secrets-dev; set +a
OBS_DB_PATH=/tmp/migdiag/obs2.db APPITOOLS_SYNTHETIC=off APPITOOLS_ENV=production \
  ./appitools-dev serve --schema /tmp/migdiag/base.json --port 8090 &

# Por caso: registrar tenant con base.json vía :9090, poblar vía API :8090,
# PUT /tenants/{id}/schema con el schema modificado, e inspeccionar con:
docker exec appitools-pg psql -U appuser -d testdb -tAc \
  "select column_name,data_type,is_nullable,column_default
   from information_schema.columns
   where table_schema='tenant_<id>' and table_name='widgets' order by ordinal_position;"
```

Esquemas de cada caso y el helper completo quedaron en `/tmp/migdiag/` durante el
diagnóstico (scratch, no versionado). El comportamiento es determinístico y se reproduce
con cualquier dato real en las tablas.
