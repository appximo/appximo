# APPITOOLS — TESTING PLAN
> Archivo de contexto para todas las sesiones de testing (S37–S40+).
> Leer junto a PRIMER.md al inicio de cada sesión de testing.
> Última actualización: 2026-06-06 · Pre S37

---

## DECISIÓN DE STACK — VERIFICADO CONTRA FUENTES PRIMARIAS

| Capa | Herramienta | Por qué |
|---|---|---|
| Unit | `testing` + `testify/require` | Estándar de facto Go, fail-fast inline (testify se agrega en S38; S37 usa `testing` plano) |
| Integration DB real | `testcontainers-go` **v0.42.0** (real en go.mod, no v0.39.x) | Postgres real con snapshot/restore, activo 2025 |
| E2E HTTP in-process | `httpexpect/v2` v2.17.0 | API fluida, httptest.NewServer o Binder directo (se agrega en S38, aún no es dep) |
| Performance + SLO gate | `k6` + `xk6-dashboard` | exit code 99 si threshold falla, HTML autocontenido |
| Regresión histórica | `benchmark-action` + `benchstat` | chart gh-pages, alert-threshold 130%, Mann-Whitney |
| Security DAST | `zaproxy/action-api-scan` + `nuclei` | OpenAPI → OWASP API Top 10, nightly |
| Fuzzing CI | Go nativo `testing.F` | Desde Go 1.18, 30s en PR, scheduled long-run |
| Resiliencia/Chaos | `Shopify/toxiproxy` | <100µs overhead sin toxics, HTTP API :8474 |
| Observabilidad métricas | `prometheus/testutil.CollectAndCompare` | End-to-end metric assertion oficial |
| Observabilidad alertas | `promtool test rules` | Mock series temporales, verifica firing/not-firing |
| Observabilidad trazas | SpanTracker propio (`pkg/observability/span.go`) | El motor NO usa OTel → `tracetest.SpanRecorder` no aplica |
| Scenarios giteables | `hurl` (opcional) | Plain text, --report-html, post-deploy smoke |

**NO usar:** godog/BDD (4% adopción, overhead innecesario), Pact/contract testing
(solo útil con consumers externos independientes), httptest out-of-process salvo
tests de startup/shutdown/signals/k6.

---

## ESTRUCTURA DE CARPETAS

```
tests/
├── integration/           # //go:build integration — DB real via testcontainers
│   └── observability_test.go    ← S37 ✅/❌
├── e2e/                   # //go:build e2e — escenarios cliente completos
│   ├── crm_scenario_test.go     ← S38
│   ├── dian_scenario_test.go    ← S38
│   ├── webhook_scenario_test.go ← S38
│   └── attack_scenario_test.go  ← S38
├── performance/           # k6 scripts con thresholds versionados
│   ├── sustained_2krps.js       ← S37 ✅/❌
│   └── README.md
├── security/              # nuclei + ZAP (nightly, no en cada PR)
│   └── .gitkeep
├── resilience/            # //go:build resilience — toxiproxy scenarios
│   └── circuit_breaker_test.go  ← S39
├── fixtures/              # datos de prueba reutilizables
│   └── schemas/
│       ├── crm_schema.json
│       └── logistics_schema.json
├── helpers/               # utilidades compartidas entre suites
│   └── server.go          # //go:build integration || e2e
└── observability/
    └── promtool/
        └── slo_test.yml   ← S37 (si reglas SLO existen como YAML)

Makefile (raíz del repo):
  make test             → unit -race -short (<3s)
  make test-integration → integration + DB real (testcontainers)
  make test-e2e         → escenarios cliente completos
  make test-perf        → k6 SLO gate (exit 99 si falla p95>15ms)
  make test-security    → nuclei + ZAP (manual/nightly)
  make test-all         → test + test-integration + test-perf
  make bench            → go benchmark + benchstat
```

---

## LOS 5 ESCENARIOS E2E (S38)

### Escenario 1 — Agencia CRM (httpexpect + testcontainers)
Entidades: Client, Contact, Deal
- Crear client con nombre
- Agregar contact vinculado al client
- Crear deal con status=pending
- Filtrar deals por status (eq) → solo los pending
- RBAC: rol gerente puede todo, rol operario solo lee
- Verificar: `testutil` confirma requests_total incrementado

### Escenario 2 — Fintech DIAN/CUFE (httpexpect + Goja real)
Entidades: Invoice, Client (con NIT)
- Crear client con NIT válido (800197268-4) → 201
- Crear client con NIT inválido → hook JS before_create rechaza → 422
- Crear invoice con calculateCUFE → verificar hash SHA-384
- Verificar span hook.before_create en pkg/observability/span.go SpanTracker propio
  (el motor NO usa OTel → tracetest.SpanRecorder no aplica; adaptar aserción en S38)

### Escenario 3 — Webhook ERP (httpexpect + httptest mock receptor)
- Crear registro → webhook debe dispararse
- Mock receptor verifica HMAC-SHA256 correcto
- Receptor falla 2 veces → retry con backoff → éxito en 3er intento
- SSRF: intentar webhook a http://169.254.169.254 → bloqueado

### Escenario 4 — Ataques simulados (httpexpect payloads adversariales)
- SQLi en filtro: `?filter[code][eq]='; DROP TABLE guides; --` → 200/0 rows tabla viva
- JWT alg:none → 401
- JWT expirado → 401
- Body 1.1MB → 413
- Cross-tenant: token de tenant A contra endpoint de tenant B → 403
- Verificar: appitools_requests_total{status=401/403/413} se incrementa
  (security_blocked_total NO existe en el motor)

### Escenario 5 — Carga sostenida (k6, no httpexpect)
- constant-arrival-rate 2000 RPS, 60s
- JWT + RBAC + multi-tenancy activos
- Thresholds: p95<15ms, error_rate<1%
- abortOnFail: true → exit code 99

---

## SLOs ENFORCED (CÓDIGO, NO ASPIRACIONES)

```
p95 < 15ms     bajo 2000 RPS sostenido (k6 threshold, abortOnFail)
error_rate < 1% (k6 threshold, abortOnFail)
RAM < 100MB    bajo carga (verificación post-k6, no gate automático aún)
```

---

## PLAN DE SESIONES

| Sesión | Deliverable | Estado |
|---|---|---|
| **S37** | Folder structure + Makefile + observability tests + k6 CI | ✅ hecho (2026-06-07) |
| **S38** | E2E escenarios 1-4 (httpexpect + testcontainers) | ✅ hecho (2026-06-07) |
| **S39** | Resilience (toxiproxy) + benchmark-action gh-pages + CI reporting | ✅ hecho (2026-06-07) |
| **S40** | Pre-launch verification: Short() guards + fuzzing CI + compose dry-run | ✅ hecho (2026-06-07) |

### Hallazgos S40 (pre-launch verification)

```
✅ testing.Short() guard agregado a 8 test files con testcontainers (los 6 del brief
   — db, graphql, migration, controlplane, internal/handlers, benchmark — MÁS
   pkg/integration/e2e_test.go y pkg/security/isolation_test.go, que también
   levantaban Docker en el lane -short). Guard puesto en el helper startPostgres/
   startPG/startRedis (cubre todos los tests del archivo de un solo edit).
   pkg/benchmark NO levantaba Docker en -short (benchOnce.Do(setupBench) solo corre
   con -bench) — se le puso b.Skip igual para coherencia con -bench -short.
✅ make test (cache caliente, SIN -count=1) = 6.8s, CERO contenedores nuevos. OJO:
   el comando literal del brief `go test ./... -race -count=1 -short` NO baja de
   ~80s porque -count=1 desactiva la cache de test y hay un piso ~1s/paquete × 60
   paquetes con -race — eso es overhead de compilación/-race, NO Docker. El objetivo
   real (lane -short sin Docker) se cumple; el "<10s" se logra con la cache (make test).
🐞 FIX real expuesto al instalar gotestfmt: `make test-e2e` pasaba salida `-v`
   (humana) a gotestfmt, que EXIGE `-json` → panic ("Did you use -json?"). Antes
   "funcionaba" solo porque gotestfmt no estaba instalado (caía a `cat`). Arreglado:
   usa `-json` cuando gotestfmt está presente, `-v` plano si no. Y `SHELL := bash`
   en el Makefile (dash no soporta `set -o pipefail`). ci.yml ya usaba -json (no afectado).
✅ fuzzing.yml: matrix de 4 targets (auth/query/schema/extensions) 30s/PR vía
   jidicula/go-fuzz-action. `go test -fuzz` NO cruza paquetes → un leg por target.
   Los 5 fuzz targets ya existían (FuzzWasmRunner excluido del PR gate por lento).
   Smoke local 5s c/u = 0 crashers.
✅ docker compose dry-run desde clone limpio: build (Go en Docker, scratch) +
   db healthy + engine → /health {"status":"ok"} /healthz 200 /readyz 200. El :8080
   literal choca con un appitools-verif preexistente en ESTA box (remap host a 18080
   para verificar; NO es bug del Quick Start). Nota compose: `ports` en override se
   MERGEAN (no reemplazan) sin tag !override → editar el clone directo.
✅ make test-all redefinido = test + test-integration + test-e2e + test-resilience
   (turnkey, solo Docker; respalda cada claim del Show HN). test-perf queda aparte
   (necesita server + token). Corrida completa: exit 0, ~41s, 0 fallos.
✅ govulncheck ./... = 0 vulnerabilidades (deps nuevas S38/S39 limpias).
ℹ️ CI en GitHub NO verificable desde este entorno (repo privado, sin gh CLI ni token,
   remote SSH). Se reprodujo el job `test` localmente paso a paso (build, vet, install
   gotestfmt, 5 gates, full suite -json|gotestfmt pipefail exit 0, integration) — verde.
   El dueño debe confirmar el check verde en la pestaña Actions.
```

### Hallazgos S39 (leer antes de S40)

```
✅ Circuit breaker REAL y FUNCIONAL (no era inspección de código): db.NewTenantDB
   cablea resilience.NewQueryBreaker; QueryDirect/ExecRowsTenant/QueryTenant todos
   pasan por exec()→breaker con context.WithTimeout(5s). toxiproxy in-process
   (server NewServer+Listen + client.CreateProxy) inyecta 6s downstream → queries
   timeout 5s → fallan → tras 15 requests concurrentes el breaker ABRE → 503
   INMEDIATO (<2s, no espera 5s) → quitar toxic + esperar ventana 8s → 200. PASS -race.
⚠️ Config REAL del breaker (pkg/resilience/circuitbreaker.go) ≠ PRIMER viejo:
   ReadyToTrip = Requests≥10 Y TotalFailures/Requests≥0.6; Timeout (open→half-open)
   = 8s (NO 30s); MaxRequests half-open = 2. PRIMER corregido. NO es bug — funciona.
✅ Graceful shutdown probado bajo carga vía shutdown.State.Run (el MISMO path que
   cmd_serve.go dispara con el ctx cancelado por SIGTERM): in-flight completan 200,
   cierra en ~200ms (<15s), nuevos requests→connection refused, load bad=0.
   Test in-process (no subprocess) — Run() es fiel al SIGTERM real.
✅ toxiproxy v2.12.0: server in-process = toxiproxy.NewServer(NewMetricsContainer(
   prometheus.NewRegistry()), zerolog.Nop()) + go server.Listen(addr); client =
   toxiproxy/v2/client.NewClient(addr); proxy.AddToxic("latency","latency",
   "downstream",1.0, client.Attributes{"latency":6000,"jitter":0}). Helpers ahora
   compilan bajo tag `resilience` (server.go: integration||e2e||resilience).
⚠️ Benchmarks: VARIANZA MEDIDA con -count=10 (anti-patrón del brief): JWTValidation
   ~10µs ±5% y RBACCheck ~72ns ±5% son ESTABLES; GETListHandler (HTTP loopback)
   95µs mediana pero un run saltó a 257µs (~2.7x) por GC/scheduler. ∴ benchmark.yml
   SOLO gatea JWT+RBAC (fail-on-alert, threshold 150%); GETList queda en el paquete
   para profiling local pero NO en el gate. `go test ./...-bench=.` del brief se
   acotó a ./tests/performance/... (evita los benchmarks testcontainers de pkg/benchmark).
ℹ️ ZAP api-scan es ACTIVO (-a manda payloads de ataque) → NUNCA contra prod. La
   security.yml genera el OpenAPI con `appitools openapi --base-url <TARGET>` y exige
   un target staging (rechaza PROD-VPS). Nightly + manual. .zap/rules.tsv creado.
ℹ️ gotestfmt en ci.yml: el step "Full test suite" ahora hace `go test ./... -json |
   gotestfmt` con `set -o pipefail` (sin pipefail el fallo de un test se perdería en
   el exit de gotestfmt) + archiva el JSON crudo como artifact.
```

### Hallazgos S38 (leer antes de S39)

```
✅ 4/4 escenarios E2E PASS (-race) vs Postgres real (un contenedor compartido por
   TestMain en tests/e2e/). httpexpect/v2 v2.17.0 + el Host se fija con
   e.Builder(req.WithHost("<tenant>.localhost")) — Config NO tiene campo `Builders`.
   testify queda // indirect (lo arrastra httpexpect.NewRequireReporter), no lo
   importamos directo.
✅ DIAN real (no mocks): validateNIT (mod-11) + calculateCUFE (SHA-384, 96 hex)
   son funciones built-in del JSSandbox → fixture nuevo tests/fixtures/schemas/
   dian_schema.json con hook before_create que las invoca. SpanTracker capturó
   6 spans reales [jwt rbac hook insert serialize done] vía un tap propio
   (BuildObservableServer DESCARTA RequestTap.Spans → buildSpanServer local).
✅ POST body >1MB → 413 (CORREGIDO post-S38): el handler create ahora distingue
   el overflow de MaxBytesReader (→413) del JSON malformado (→400), igual que
   PUT/PATCH y el contrato OpenAPI (Error413). Aserción E2E usa un JSON VÁLIDO
   con string >1MiB ({"code":"xxx..."}) — un body de bytes 0x00 daría 400 por
   carácter inválido, no por tamaño. Aserción = 413.
⚠️ Cross-tenant (token tenant A → Host tenant B) → 401 "token tenant mismatch"
   (NO 403). JWTMiddleware corre tras TenantMiddleware y compara claims.TenantID.
⚠️ Webhooks: BuildObservableServer arma un HookRunner SIN dispatcher → el escenario
   webhook usa buildWebhookServer local + dispatcher insecure-transport para llegar
   al receptor loopback. SSRF se asegura directo contra NewSSRFSafeClient (bloquea
   loopback + 169.254.169.254). HMAC = "sha256="+hex(HMAC-SHA256(body, secret)),
   header X-Appitools-Signature, evento X-Appitools-Event.
ℹ️ deals.status enum = [pending,won,lost] (el brief decía "closed"): el motor
   valida enums en PATCH (collectUpdate→validateFieldValue) → se usó "won".
```

### Hallazgos S37 (leer antes de S38)

```
✅ Métricas reales (grepeadas): appitools_requests_total{tenant_id,method,path,status},
   appitools_request_duration_seconds{tenant_id,method,path}, appitools_active_tenants,
   appitools_migration_duration_seconds{tenant_id,status}. El `path` de listas es el
   patrón estático "/api/guides" (las rutas /{id} sí llevan param). Se añadió
   Metrics.Gatherer() para testutil.GatherAndCompare.
⚠️ NO existe `security_blocked_total` (Escenario 4): aseverar sobre
   appitools_requests_total{status=401/403/413} o el ErrorStore (/debug/tenant/{id}).
⚠️ NO hay OTel → tracetest.SpanRecorder (Escenario 2) no aplica. El proyecto usa su
   propio SpanTracker (pkg/observability/span.go). Reemplazar esa aserción en S38.
⚠️ SLOs son un motor Go (slo.go), no reglas YAML → promtool N/A (ver
   tests/observability/promtool/README.md).
⚠️ `make test` (-short) aún NO es hermético: pkg/{db,graphql,migration,controlplane},
   internal/handlers y pkg/benchmark usan testcontainers sin guard testing.Short().
   Lo nuevo se aísla con build tags (integration/e2e). Follow-up: añadir guards.
ℹ️ httpexpect/v2 y testify NO son deps todavía (se agregan en S38). El repo usa
   `testing` plano + t.Fatalf; los tests S37 siguen esa convención.
```

---

## CÓMO ABRIR CADA SESIÓN DE TESTING

```
Nueva sesión Claude Code → chat nuevo (no continuar el anterior)

Mensaje de apertura:
@context-docs/PRIMER.md @context-docs/TESTING_PLAN.md

[Leer los archivos referenciados. Luego leer los archivos
 específicos que necesitas para esta tarea. Brief abajo:]

[brief corto de la sesión]
```

Para S37 específicamente, agregar después:
```
@cmd_serve.go @k6-stress.js
```

Para S38:
```
@tests/helpers/server.go @tests/fixtures/ @tests/integration/observability_test.go
```

---

## REGLAS DE CALIDAD PARA TESTS

```
✅ Cada test usa evidencia ejecutada — nunca solo leer código y asumir
✅ Build tags correctos: //go:build integration || e2e || resilience
✅ Tests de observabilidad usan nombres reales de métricas (del grep)
✅ k6 thresholds son alcanzables con el hardware real de prod
✅ Fixtures son datos colombianos reales (NITs reales, estados reales)
✅ Cada sesión termina con commit limpio + PRIMER actualizado

❌ Inventar nombres de métricas
❌ Thresholds irreales (p95<1ms no va a pasar)
❌ Tests que pasan haciendo mock de todo (sin valor real)
❌ BDD/godog (overhead sin beneficio para este stack)
❌ Contract testing Pact (no hay consumers externos independientes)
```
