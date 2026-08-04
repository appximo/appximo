# PERSISTENCE.md — Mapa de persistencia SQLite del motor

Auditoría de todo almacenamiento fuera de Postgres. Fuentes: código fuente
(`pkg/observability/store.go`, `tools/devhub/api/bench.go`, `app.go`).
Cada afirmación cita **archivo + símbolo** (función/tipo/var), no número de
línea — los símbolos no derivan con cada edit (misma convención que
[SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md) y [MENTAL_MODEL.md](MENTAL_MODEL.md)).

---

## MOTOR (producción)

### Observability Store — `pkg/observability/store.go`

El motor crea **un único archivo SQLite** para observabilidad. Es el único
almacenamiento persistente fuera de Postgres que existe en producción.

#### Path en disco

| Env var | Valor por defecto | Código |
|---------|-------------------|--------|
| `OBS_DB_PATH` | `/var/lib/appximo/obs.db` (**persistente**) | [store.go](../pkg/observability/store.go) `defaultObsDBPath` |

Si la var está vacía se usa el default **persistente** `/var/lib/appximo/obs.db`
— el mismo root que el file store, **no `/tmp`**. El directorio padre se crea al
abrir (`os.MkdirAll` + sonda de escritura). Si NO se puede crear o escribir (p.ej.
el proceso no corre como el usuario dueño del path), el store **cae a un archivo
efímero** bajo el temp del sistema y **loguea un WARNING** explícito — la
observabilidad es best-effort y un path inválido **nunca interrumpe el boot**.
Si el path resuelto vive en `/tmp` o un tmpfs detectable, también se loguea un
WARNING avisando que el historial no sobrevivirá un restart (visibilidad de R1).
La apertura se hace en `New()` ([app.go](../app.go)):

```go
if st, openErr := observability.OpenStore(os.Getenv("OBS_DB_PATH")); openErr != nil {
    log.Printf("WARNING: observability store disabled: %v", openErr)
} else {
    app.obsStore = st
    ...
}
```

Un fallo de apertura que ni el fallback efímero resuelve (p.ej. todo el disco
lleno) produce un WARNING y **no interrumpe el boot**: `app.obsStore` queda nil,
la observabilidad queda deshabilitada y el motor continúa sirviendo sin
degradación funcional.
Ninguna escritura posterior ocurre si `obsStore == nil` (el tap de trazas en
`buildRouter` y el lanzamiento de `flushObsSnapshots` en `Start`, [app.go](../app.go)).

#### Conexión SQLite

```go
// store.go
dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
db, err := sql.Open("sqlite", dsn)
...
db.SetMaxOpenConns(1)
```

- `busy_timeout(5000)`: 5 s de espera antes de devolver "database is locked".
- `journal_mode(WAL)`: Write-Ahead Logging — permite lecturas concurrentes
  mientras hay un escritor; reduce contención.
- `SetMaxOpenConns(1)`: serializa todos los accesos desde un único pool de
  conexión para que Flush/History/SlowTraces nunca compitan ([store.go](../pkg/observability/store.go)).

Driver: `modernc.org/sqlite` — CGO-free, consistente con la restricción del
proyecto ([store.go](../pkg/observability/store.go)).

#### Tablas

**`obs_snapshots`** — serie temporal de métricas por tenant (el DDL de `OpenStore`, [store.go](../pkg/observability/store.go)):

```sql
CREATE TABLE IF NOT EXISTS obs_snapshots (
    tenant_id   TEXT    NOT NULL,
    ts          INTEGER NOT NULL,   -- unix seconds
    p50_us      INTEGER,
    p95_us      INTEGER,
    error_ratio REAL,
    burn_rate   REAL,
    slo_status  TEXT,
    PRIMARY KEY (tenant_id, ts)
);
CREATE INDEX IF NOT EXISTS idx_tenant_ts ON obs_snapshots(tenant_id, ts DESC);
```

Qué guarda: el snapshot de p50/p95 latencia + ratio de error + burn rate SLO +
estado SLO (`ok`/`warning`/`critical`) de cada tenant activo, un registro
cada 60 s.

**`slow_traces`** — trazas de requests lentos y errores (el DDL de `OpenStore`, [store.go](../pkg/observability/store.go)):

```sql
CREATE TABLE IF NOT EXISTS slow_traces (
    trace_id     TEXT    NOT NULL,
    tenant_id    TEXT    NOT NULL,
    ts           INTEGER NOT NULL,   -- request start, unix microseconds
    route        TEXT,
    total_us     INTEGER,
    status       INTEGER NOT NULL DEFAULT 0,
    err_msg      TEXT,
    stack_json   TEXT,   -- JSON array de StackFrame (solo 500s)
    ip           TEXT,
    user_agent   TEXT,
    browser      TEXT,
    os           TEXT,
    country      TEXT,
    method       TEXT,
    full_url     TEXT,
    headers_json TEXT,
    spans_json   TEXT,   -- JSON array de {name, dur_us}
    PRIMARY KEY (trace_id)
);
CREATE INDEX IF NOT EXISTS idx_slow_tenant_ts ON slow_traces(tenant_id, ts DESC);
```

Qué guarda: cada request que supera 50 ms **o** retorna HTTP >= 400, con su
descomposición de spans (auth, db, cache, etc.), IP, User-Agent, país
(GeoLite2), headers filtrados y, para los 500, el stack frame.

Hay ALTER TABLE idempotentes para columnas añadidas en versiones posteriores
([store.go](../pkg/observability/store.go)) — errores "duplicate column name"
se descartan silenciosamente.

#### ¿Toca el hot path? — NO (con cita)

**`obs_snapshots`:** Escrito por `flushObsSnapshots()`, goroutine de
background lanzada en `Start()` ([app.go](../app.go)):

```go
if a.obsStore != nil {
    go flushObsSnapshots(ctx, a.obsStore, a.rings, a.hist, a.sloEngine)
}
```

Este goroutine llama `store.Flush()` cada **60 s** ([app.go](../app.go)).
No existe ningún camino desde un request HTTP hasta `Flush()`.

**`slow_traces`:** El RequestLogger calcula si un trace debe persistirse y,
si es así, lo lanza en un goroutine separado vía semáforo ([app.go](../app.go)
y [app.go](../app.go)):

```go
tracePersistSem := make(chan struct{}, 64)  // app.go
// ... dentro del tap post-request:
select {
case tracePersistSem <- struct{}{}:
    go func() {
        defer func() { <-tracePersistSem }()
        tv.Browser, tv.OS = observability.ParseUserAgent(ua)
        tv.Country = a.geo.Country(ip)
        if err := a.obsStore.SaveSlowTrace(tenantID, tv); err != nil {
            log.Printf("save slow trace [%s]: %v", tenantID, err)
        }
    }()
default:   // 64 goroutines activos → drop silencioso, no bloqueo
}
```

El response ya fue entregado al cliente antes de que este tap se ejecute
(la tap se invoca post-response en `RequestLogger`). El `select/default`
garantiza que si los 64 slots están llenos, el trace **se descarta** en vez
de bloquear. El hot path nunca espera SQLite. Impact en p50: **cero**.

Optimización adicional: los headers y la URL completa solo se capturan si
`ShouldPersistTrace()` es `true` ([pkg/logging/logger.go](../pkg/logging/logger.go)):

```go
if observability.ShouldPersistTrace(...) {
    reqHeaders = observability.FilterHeaders(r.Header)
    fullURL = requestFullURL(r)
}
```

Los requests 200-OK rápidos no iteran headers en ningún momento.

**Predicado de persistencia** ([store.go](../pkg/observability/store.go)):

```go
func ShouldPersistTrace(s Sample) bool {
    if s.DurUS > SlowTraceThresholdUS { return true }  // > 50 ms
    if PersistErrors && s.Status >= 400 { return true } // cualquier error
    return false
}
```

`SlowTraceThresholdUS = 50_000` ([store.go](../pkg/observability/store.go)),
`PersistErrors = true` ([store.go](../pkg/observability/store.go)).

#### Retención

Gestionada por `Prune()` ([store.go](../pkg/observability/store.go)), llamado
en el boot ([app.go](../app.go)) y cada 60 s junto con `flushObsSnapshots`
([app.go](../app.go)):

| Tabla | Política | Código |
|-------|----------|--------|
| `obs_snapshots` | DELETE donde `ts < now - 7 días` | [store.go](../pkg/observability/store.go) |
| `slow_traces` (por tiempo) | DELETE donde `ts < now - 7 días` (en µs) | [store.go](../pkg/observability/store.go) |
| `slow_traces` (por volumen) | DELETE las filas más antiguas si hay más de 50 000 | [store.go](../pkg/observability/store.go) |

El cap de 50 000 filas (`maxSlowTraceRows = 50_000`, [store.go](../pkg/observability/store.go))
es la guardia contra floods de errores: si un tenant emite miles de 401/429
en pocos minutos, el archivo no crece sin tope dentro de la ventana de 7 días.
Estimación de tamaño máximo: ~50 000 traces × ~2 KB por fila ≈ **~100 MB
como cota superior**. En operación normal (ratio de error bajo, tráfico a
< 500 RPS) el archivo es de decenas de MB.

#### Aislamiento multi-tenant

Un único archivo SQLite. Las tablas usan `tenant_id` como parte de la
clave primaria y hay índices `(tenant_id, ts DESC)`. El motor **nunca
cruza datos de un tenant en la respuesta de otro**: `History()` y
`SlowTraces()` siempre filtran `WHERE tenant_id = ?` ([store.go](../pkg/observability/store.go),
[store.go](../pkg/observability/store.go)). Los endpoints de observabilidad están protegidos
por `X-Admin-Key` y el tenant se pasa explícitamente en el path —
`/debug/tenant/{id}/...` — no puede ser inferido por el caller.

**Aislamiento: lógico (filas separadas), no físico (un solo archivo).**
No hay riesgo de escape de datos en las rutas de la API de observabilidad
tal como están implementadas. Si se añadieran endpoints que leyeran sin
filtrar `tenant_id`, eso sería una vulnerabilidad.

---

## DEVHUB (tooling de desarrollo — solo el 105)

### DevHub Benchmark Store — `tools/devhub/api/bench.go`

El DevHub es el dashboard de desarrollo local. Su SQLite es completamente
independiente del motor y no existe en producción.

#### Path en disco

Hardcodeado ([bench.go](../tools/devhub/api/bench.go)):

```
/root/appitools/tools/devhub/db/devhub.db
```

No hay env var. El directorio se crea con `os.MkdirAll()` en el arranque.
En el 105 el archivo vive en el repo (`tools/devhub/db/`) y está gitignoreado.

#### Conexión SQLite

```go
// bench.go
sql.Open("sqlite", "file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
```

Igual que el motor pero añade `foreign_keys(1)` para hacer cumplir `ON DELETE
CASCADE` de `run_datapoints` ([db/schema.sql](../tools/devhub/db/schema.sql)).
Singleton process-wide vía `sync.Once` ([bench.go](../tools/devhub/api/bench.go)).

#### Tablas (schema en `tools/devhub/db/schema.sql`)

| Tabla | Propósito |
|-------|-----------|
| `benchmark_runs` | Un registro por ejecución de bench-protocol: label, RPS objetivo, p50/p95/p99, error_rate, CV |
| `run_datapoints` | Las latencias individuales (hasta N por run); `ON DELETE CASCADE` de `benchmark_runs` |
| `comparisons` | Resultados de Mann-Whitney U entre pares de runs: U-stat, p-value, CI en ms |
| `baselines` | Baselines nombrados para comparación delta-pct |
| `servers` | Registro de servidores remotos (105, 58, etc.) — SSH key path, URLs, nombre de env var del admin key (NO el valor) |
| `deploys` | Historial de deploys con estado SSE pipeline |
| `secret_access` | Auditoría de uso de secrets (operación + timestamp) — NUNCA los valores |

#### ¿Toca el hot path del motor?

No aplica — el DevHub es un proceso separado en `:3099`. Su SQLite no tiene
ninguna relación con el motor en `:8080`. Las escrituras del DevHub ocurren
en los handlers HTTP del DevHub (síncronas en esos handlers), no en el
camino de request del motor.

#### Retención

**No hay Prune.** Las tablas crecen sin límite. En la práctica:
- `benchmark_runs` + `comparisons` + `baselines`: crecimiento despreciable
  (unos pocos miles de filas en todo el ciclo de vida del proyecto).
- `run_datapoints`: cada run de 60 s a 2000 RPS captura ~120 000 latencias.
  Varios runs/día durante meses → potencialmente millones de filas. A ~8 bytes
  por fila: 1 M de filas ≈ 8 MB. No es urgente, pero es deuda.

---

## Tabla resumen

| Store | Archivo | Tablas | Cuándo escribe | Hot path | Retención | Aislamiento |
|-------|---------|--------|----------------|----------|-----------|-------------|
| **Motor — obs snapshots** | `$OBS_DB_PATH` (def. `/var/lib/appximo/obs.db`) | `obs_snapshots` | Goroutine background cada 60 s | **NO** | 7 días | Filas por tenant_id |
| **Motor — slow traces** | (mismo archivo) | `slow_traces` | Goroutine async post-response (sem 64) | **NO** | 7 días + cap 50 k filas | Filas por tenant_id |
| **DevHub** | `tools/devhub/db/devhub.db` | 7 tablas de benchmark + ops | Handler HTTP del DevHub (síncrono en el DevHub) | N/A (proceso distinto) | Sin límite | Global (server-scoped) |

---

## RIESGOS / DEUDA

### R1 — Default path `/tmp/obs.db` en producción ✅ RESUELTO

**Antes:** sin `OBS_DB_PATH` configurado el archivo vivía en `/tmp`. En un
contenedor con `tmpfs` o en un sistema con `systemd-tmpfiles` que limpia `/tmp`
en el arranque, el archivo se perdía con cada restart (sin corrupción, pero con
pérdida de todo el historial de traces y snapshots).

**Ahora:** el default es **persistente** — `/var/lib/appximo/obs.db` (el mismo
root que el file store). El directorio se crea al abrir. Si el path resuelto cae
en `/tmp` o un tmpfs (porque el operador lo configuró así a propósito en dev), el
motor **loguea un WARNING** al boot avisando que el historial no sobrevivirá un
restart — visibilidad del riesgo, sin bloquear. En Docker el path persistente se
monta en el volumen `obs_data` (compose) para que sobreviva al contenedor; en
systemd nativo `StateDirectory=appximo` crea `/var/lib/appximo` con el dueño
correcto. Ver [docs/DEPLOY.md → OBS_DB_PATH](DEPLOY.md#observability-store-obs_db_path).

### R2 — Flood de errores puede llenar `/tmp` si OBS_DB_PATH no está configurado ✅ RESUELTO

Un DDoS de 429 o un bug que devuelva 500 a gran escala genera trazas a la
velocidad de los requests. El semáforo de 64 goroutines limita la concurrencia
de escrituras, pero no el volumen acumulado. El cap de 50 000 filas + el Prune
cada 60 s limitan el tamaño, pero entre Prunes un flood sostenido puede crecer.

**Ahora:** con el default en `/var/lib/appximo` (disco real, no la RAM/cuota
limitada de `/tmp`) el riesgo de OOM/ENOSPC silencioso del caso `/tmp` desaparece
para la configuración por defecto, y el cap de 50 000 filas + Prune lo mantienen
acotado (~100 MB cota superior). El riesgo residual de **disco lleno** (cualquier
partición) sigue cubierto por R3 (degradación silenciosa, no crash) — mitigación:
monitorear el tamaño del archivo. Si alguien deja el store en `/tmp` a propósito,
el WARNING de R1 lo hace visible.

### R3 — Disco lleno: degradación silenciosa, no crash

Si el disco se llena y `SaveSlowTrace` falla, el error se loguea
(`log.Printf("save slow trace [%s]: %v", tenantID, err)` en [app.go](../app.go))
pero el request ya fue respondido al cliente. **El motor sigue sirviendo**;
simplemente deja de persistir trazas. No hay alerting automático sobre
este estado. Mitigación: monitorear el tamaño de `OBS_DB_PATH` en Prometheus
o poner el archivo en una partición dedicada.

### R4 — DevHub `run_datapoints` sin pruning

Runs de bench frecuentes durante meses acumulan millones de filas. A escala
del uso actual (pocas decenas de runs históricos) no es urgente. Pero si el
benchmarking se automatiza en CI, puede convertirse en un problema. **Deuda:
añadir DELETE de run_datapoints con más de N semanas al DevHub.**

### R5 — Aislamiento multi-tenant: lógico, no físico

Un único archivo SQLite guarda los datos de todos los tenants. Si en el futuro
se añaden endpoints de observabilidad que no filtren correctamente `tenant_id`,
habría riesgo de fuga de datos entre tenants. Hoy todas las consultas filtran
por `tenant_id`, pero no hay una garantía estructural (como un archivo por
tenant). Esto es una deuda de diseño para cuando la observabilidad se exponga
a los propios tenants (vs. al operador vía `X-Admin-Key`).

---

## ¿Afecta el p50?

**No.** Ambos caminos de escritura al SQLite del motor están fuera del hot path:

1. `obs_snapshots`: goroutine background, no invocado por ningún request.
2. `slow_traces`: goroutine async lanzado post-response con semáforo; el
   `select/default` en [app.go](../app.go) garantiza que si los 64 slots están
   ocupados, el trace se descarta en vez de bloquear. La respuesta ya fue
   enviada al cliente antes de que el goroutine arranque.

El RequestLogger además optimiza el 200-OK rápido: solo captura headers y URL
completa cuando `ShouldPersistTrace()` es `true` ([pkg/logging/logger.go](../pkg/logging/logger.go)),
por lo que los requests del hot path rápido no pagan ni el coste de iterar headers.

Los benchmarks de referencia (S46: 2000 RPS, p50 1.58 ms) se midieron con el
motor completo incluyendo la observabilidad activa; no hay baseline pre/post
SQLite específico porque las escrituras son async y su impacto en p50 es cero
por construcción.
