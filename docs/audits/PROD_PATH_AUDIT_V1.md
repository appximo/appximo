> **HISTORICAL RECORD — every finding below is CLOSED.** This is the
> point-in-time audit (PROD-PATH-AUDIT-V1) that motivated the production path.
> It is kept because its inventory (the env-var table, the deploy procedure, the
> Docker/compose map) is still the most complete snapshot of that surface, and
> because an audit whose findings were acted on is worth preserving. Where each
> finding landed:
>
> | # | Finding | Closed by |
> |---|---|---|
> | 1 | Docker image did not build on HEAD (`go:embed docs/` vs `.dockerignore`) | PROD-PATH-BUILD-S1 — `.dockerignore` now un-ignores `docs/BACKEND_SPEC_LLM.md` |
> | 2 | The CI-published image served empty `/editor` and `/admin` | PROD-PATH-BUILD-S1 — the image build runs the SPA builds |
> | 3 | No "empty server → running app" automation | PROD-PATH-BUILD-S1 + GOLD — `scripts/install.sh`, proven on a real VPS (`api.appitools.com`) |
> | 4 | The binary could not serve a third-party frontend | **LOOSE-ENDS-SWEEP-S1 — `Config.Static`** (docs/PRODUCTION.md §6c) |
> | 5 | No direct TLS (by design) | Unchanged and documented: Caddy terminates TLS |
> | 6 | `GOMEMLIMIT` had no default on a 1 GB box | PROD-PATH-BUILD-S1 — cgroup auto-detection + the installer sets it |
> | 7 | `serve` did not bootstrap the control plane | PROD-PATH-BUILD-S1 — `New` applies the canonical DDL idempotently |
> | 8 | No versioned systemd units, no cut release | Units ship with the installer; **the release tag is Miguel's call** (docs/BACKLOG.md) |
>
> Open items that outlived this audit are tracked in
> [docs/BACKLOG.md](../BACKLOG.md) — not here.

# PROD-PATH-AUDIT-V1 — Camino de producción de Appitools (inventario + gaps)

Auditoría de VERIFICACIÓN pura (cero construcción) del estado real, HOY, de todo
lo que Appitools tiene para producción: Docker, compose, deploy, TLS, dominios,
estáticos, graceful shutdown, health, env vars, Postgres. Cada afirmación está
contrastada contra el código/los archivos y, donde aplica, PROBADA localmente en
puertos scratch (18080/19090) — el fleet real de :8080 nunca se tocó.

- **Repo:** `/root/appitools` (el 105), branch `main`, HEAD `7ada25b` (con
  `backend-spec`, **unpushed**).
- **Build nativo:** `go build ./cmd/appitools` → **OK** (exit 0). Binario canónico
  (`scripts/build-engine.sh`, `-s -w -trimpath`, CGO_ENABLED=0) = **60.6 MB**
  (coincide con el claim del README "~60 MB").
- **`make test`:** unit lane (-race -short) corriendo verde (13+ paquetes `ok`, 0
  FAIL) — es LENTO en este box de 1 vCPU con `-race`; veredicto final abajo.

---

## 0. TL;DR — los hallazgos que mandan

| # | Sev | Hallazgo | Evidencia |
|---|-----|----------|-----------|
| 1 | 🔴 **P0** | **El Docker image NO buildea en HEAD.** `backendspec.go:12` tiene `//go:embed docs/BACKEND_SPEC_LLM.md`, pero `.dockerignore:29` excluye `docs/` → el `go build` del builder stage falla: `pattern docs/BACKEND_SPEC_LLM.md: no matching files found`. **Reproducido** (`docker build` → exit 1 en `Dockerfile:29`). Lo introdujo el último commit (`backend-spec`); rompe también `docker-publish.yml` en el próximo push a main. | `docker build` local; `backendspec.go:12`; `.dockerignore:29` |
| 2 | 🔴 **P1** | **La imagen publicada por CI sirve `/editor` y `/admin` VACÍOS.** Los assets de las SPAs están **gitignored** (`pkg/adminui/web/dist/assets/`, `pkg/editorui/web/build/assets/`); ni el `Dockerfile` ni `docker-publish.yml` corren `make admin-ui/editor-ui`. Un `actions/checkout` limpio no trae los assets → `go:embed` toma solo el `index.html` placeholder. Contradice el README ("the published image ships them"). | `.gitignore:63-74`; `Dockerfile:18-31`; `docker-publish.yml` (sin npm) |
| 3 | 🟠 GAP | **No existe automatización "server vacío → app corriendo".** Cero scripts de provisión (instalar PG, aplicar DDL de control plane, escribir start script, systemd, proxy/TLS). El pipeline del DevHub es **RE-deploy** de binario a un box YA montado a mano. | `grep` de provisión → NONE |
| 4 | 🟠 GAP | **El binario NO puede servir un frontend de terceros.** Solo sirve sus propias SPAs embebidas (`/editor`, `/admin`, `/docs`, `/graphiql`). No hay static-dir/flag/embed para un build React/Vue. | `serve` flags; grep `FileServer` |
| 5 | 🟠 GAP | **Sin TLS directo** (por diseño): el motor es HTTP plano; TLS lo pone Caddy/nginx adelante. Bien documentado, pero es una pieza externa obligatoria. | grep `crypto/tls` (solo SMTP) |
| 6 | 🟠 GAP | **`GOMEMLIMIT` es opt-in y NO está seteado en ningún config shipped.** En el VPS de 1 GB (el target) no hay techo blando de memoria por defecto → una asignación runaway puede OOM-matar el proceso multi-tenant. | `runtime.go:28`; compose (solo `GOMAXPROCS=2`) |
| 7 | 🟠 GAP | **`serve` single-engine NO bootstrapea el control plane.** `public.tenants` la crea el initdb de compose o `fleet.BootstrapControlPlane` — no `app.New`. En un Postgres BYO/manejado hay que aplicar `migrations/001_control_plane.sql` a mano (no enfatizado en DEPLOY.md Nivel 3). | `app.go` New (sin `migrations.ControlPlane`); `pkg/fleet/bootstrap.go` |
| 8 | 🟠 GAP | **No hay unit files de systemd en el repo** — DEPLOY.md los tiene como copy-paste, pero no como artefacto versionado. Y **no hay release cortado** (`release.yml` listo, repo privado, sin tag `v*`) → el "download desde GitHub Releases" del Nivel 3 hoy no funciona. | `.github/workflows/release.yml`; `ls deploy/*.service` → solo devhub |
| ✅ | OK | **Graceful shutdown FUNCIONA** (probado): SIGTERM → `/readyz`→503 → drena → exit limpio en **5.1 s**. Health probes `/healthz` `/readyz` `/health` correctos. Compose (dev/prod/db) completos y coherentes. Pipeline de re-deploy del DevHub sólido (build→push→backup→swap→smoke). | test SIGTERM en :18080; `pkg/shutdown` |

---

## 1. INVENTARIO — qué existe y en qué estado

### 1.1 Docker

| Pieza | Estado | Detalle / evidencia |
|-------|--------|---------------------|
| `Dockerfile` | 🔴 **NO buildea en HEAD** | Multi-stage `golang:1.25-alpine` → `alpine:3.21`; usuario **no-root** (`appitools`); estático CGO_ENABLED=0; **una imagen dos roles** (engine por defecto, `worker` por keyword — `deploy/docker-entrypoint.sh`); `HEALTHCHECK` contra `/healthz`; hornea `examples/quickstart/schema.json`; crea `/var/lib/appitools/{files,obs}`. **Rompe** por el `go:embed docs/` (hallazgo #1). |
| `.dockerignore` | ⚠️ desincronizado | Excluye `docs/` (causa raíz de #1). El comentario cabecera está **stale**: dice que hornea `testdata/logistics/schema.json`, pero el Dockerfile hornea `examples/quickstart/schema.json`. |
| SPAs embebidas | 🔴 vacías en la imagen CI | Assets gitignored + CI no corre `make admin-ui/editor-ui` (hallazgo #2). Nota: un `docker build` LOCAL desde este working-copy (con los assets ya en disco) SÍ los incluiría — `.dockerignore` no los excluye —, pero la imagen que publica el CI no. |
| `docker-compose.yml` (dev) | ✅ completo | engine + worker (echo) + `db` (postgres:16-alpine con el DDL de control plane inline vía `docker-entrypoint-initdb.d`) + perfil `ui` opt-in. Volúmenes `pgdata`/`files_data`/`obs_data`. Puertos 8080/9090 publicados. |
| `docker-compose.prod.yml` | ✅ completo | Caddy (TLS Let's Encrypt automático) + engine + worker + db; **engine y db sin puertos de host** (solo Caddy expone 80/443). Mismo DDL inline. |
| `docker-compose.db.yml` | ✅ completo | Solo Postgres (para el deploy nativo Nivel 3), bind a `127.0.0.1:5432`, con `max_connections=300 shared_buffers=256MB work_mem=16MB` (los settings del benchmark) + DDL inline. |
| Imagen en Docker Hub | 🟡 publicada, atrás + SPAs vacías | `neodevtrix/appitools-engine` vía `docker-publish.yml` (solo en CI verde de main/`v*`). Al estar el trabajo reciente **unpushed**, `:latest` está detrás de HEAD (y por #2 sirve Studio/admin vacíos). |
| Benchmark Docker vs binario | ✅ documentado | `docs/DEPLOY.md §Measured overhead`: bridge Docker **+0.05 ms** p50 (~54 µs), proxy Caddy **+0.48 ms** p50. Datos crudos `benchmarks/data/deploy-overhead-runs.csv`. Conclusión: el contenedor es casi gratis a 500 RPS; se elige binario nativo para quitar una variable hacia el techo de miles de RPS. |

### 1.2 Deploy / graceful / systemd

| Pieza | Estado | Detalle |
|-------|--------|---------|
| Pipeline de deploy real | ✅ funciona (re-deploy) | **DevHub → panel Deploy** (`tools/devhub/api/deploy.go`), SSE: guard árbol-limpio → build **local** canónico → push `binario.new` (scp) → **backup** del binario vivo a `<dir>-rollback/` → swap (`mv` + `pgrep -x` + `kill -TERM` + espera ≤30 s) → start script → smoke (`/health` version-match + `/readyz` por túnel SSH) → registra en SQLite. Prod exige `confirm`=nombre exacto. |
| Graceful shutdown | ✅ **PROBADO** | `pkg/shutdown`: SIGTERM/SIGINT → `ready=0` → `/readyz`→503 → sleep drain (5 s) → `srv.Shutdown(10s)` → cleanup (pool/obs/files). Test en :18080: `/readyz` pasó a 503 en <1 s, proceso salió limpio en **5.1 s**, puertos liberados, log "server shut down cleanly". Cubierto además por `tests/resilience` bajo carga. |
| systemd | 🟡 solo en docs | `docs/DEPLOY.md §3.2/3` trae `appitools.service` + `appitools-worker.service` como copy-paste (con `StateDirectory`, `NoNewPrivileges`, `EnvironmentFile`). **No hay `.service` versionado** para el engine (solo `tools/devhub/devhub.service`, que es del devhub). |
| Zero-downtime (binario) | 🟠 no existe | El swap hace SIGTERM→espera-exit→start: hay una ventana breve sin listener. No hay symlink-swap ni blue/green. El **hot-swap** (`POST /admin/engine/schema`) es para cambios de **schema** (o per-app en el fleet), no para upgrade de binario. |

### 1.3 TLS, dominios, web server

- **TLS directo en el binario:** ❌ no existe. `grep crypto/tls` → solo el sender SMTP del worker. El motor sirve HTTP plano; TLS = Caddy/nginx adelante.
- **Caddy:** `deploy/Caddyfile` (single-domain + subdominios de tenant, opciones A lista-explícita / B wildcard DNS-challenge) + `deploy/Dockerfile.caddy` (build de Caddy con módulo DNS DigitalOcean). Bien documentado en DEPLOY.md Niveles 2 y 3 (incl. keepalive upstream para nginx).
- **Dominio/Host single-app:** el tenant sale del subdominio del `Host`; "configurar dominio" = apuntar DNS + que el proxy pase el `Host` sin tocar. `Config.BareDomains` marca dominios que son la app misma (fleet).
- **CORS:** ✅ configurable, **off por defecto**, por `APPITOOLS_CORS_*` (o `Config`), scope `/api,/auth,/graphql,/openapi`. `app.go:674`.

### 1.4 Frontend estático

- **El binario NO sirve estáticos arbitrarios.** Solo monta SPAs **embebidas** vía `go:embed`: `/editor` (Studio, `pkg/editorui`), `/admin` (panel, `pkg/adminui`), `/docs` (Swagger), `/graphiql`. No hay flag `--static-dir`, ni `APPITOOLS_STATIC_DIR`, ni `http.FileServer` sobre un directorio de usuario.
- **Punto de extensión:** `buildRouter` (`app.go:886+`) monta las SPAs con `adminui.Register(r)/editorui.Register(r)`. Servir un front de terceros sería un cambio de código (un mount nuevo + config). La librería (`App.Register`) permite añadir rutas custom, pero un `Route` sirve un `Handler` — no hay helper de file-server; habría que escribirlo.
- **Consecuencia:** el frontend del usuario se sirve aparte (Caddy `file_server`, Netlify/Vercel, S3+CDN) o same-origin vía el proxy. El motor es API-only para terceros.

### 1.5 Config de producción del motor

| Aspecto | Estado |
|---------|--------|
| `APPITOOLS_ENV=development` | Enciende pprof (`:6060`, override `APPITOOLS_PPROF_PORT`), introspection GraphQL + GraphiQL. Producción (unset/otro) los apaga (GraphiQL solo con `APPITOOLS_GRAPHQL_PLAYGROUND=on`). `app.go:1053,1151`. |
| Rate limit | Default **1000 RPS / 100 burst por tenant** (`RATE_LIMIT_RPS/BURST`). Sensato. `app.go:433`. |
| Timeouts HTTP | `ReadHeader 10s / Read 20s / Write 30s / Idle 120s` — **hardcoded** (no env). `app.go:781`. Sensato. |
| `GOMEMLIMIT`/`GOGC` | **Opt-in, sin default, NO en ningún config shipped** (hallazgo #6). `runtime.go`. `GOMAXPROCS` sí (compose=2 + automaxprocs cgroup-aware). |
| Health/readiness | `/healthz` (liveness, no toca PG), `/readyz` (503 al drenar), `/health` (JSON+version) — todos **sin auth**. `/metrics`, `/debug/*` **admin-gated** (X-Admin-Key). |
| Logs | Structured **JSON a stdout** por defecto (zerolog, `logging.Init(env)`) — apto journald/docker. Confirmado en el test (`{"level":"info",...}`). |
| Backups | `POST /admin/backup?tenant=ID` (pg_dump custom+comprimido) — **requiere `postgresql16-client` en la imagen, NO incluido por defecto** (documentado en el Dockerfile). CLI `appitools backup`. **Sin schedule/tooling automático** (manual/cron). `BACKUP_DIR` opcional. |

### 1.6 Postgres — qué asume el motor

- **Asume una base EXISTENTE** (`DATABASE_URL`); **no crea la base**. Al boot asegura idempotente `public.outbox`, `public.schema_history`, `public.flow_tests/flow_runs`. **No aplica** el DDL de control plane (`public.tenants`, `tenant_policies`, `migration_log`, trigger `schema_updated`) en single-engine (hallazgo #7).
- **Permisos:** necesita crear schemas/tablas (owner de la base, o CREATE en la DB). Un tenant = un schema `tenant_<id>`.
- **Migraciones:** el motor de migración (`pkg/schemadiff`) es para evolución del **schema del tenant** (control plane / CLI `migrate` / editor), con dry-run + gate destructivo + fan-out reanudable. No hay "migraciones-en-deploy" para el control plane (el DDL es idempotente, se aplica una vez).

### 1.7 Inventario de ENV VARS (para la guía de prod)

**Requeridas (3)** — `serve` sale sin ellas: `DATABASE_URL`, `JWT_SECRET`, `ADMIN_KEY`.

**Runtime/infra (opcionales):** `APPITOOLS_ENV`, `APPITOOLS_CONTROL_PORT`, `APPITOOLS_PPROF_PORT`, `GOMAXPROCS`, `GOMEMLIMIT`, `GOGC`, `DB_MAX_CONNS`, `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST`, `APPITOOLS_MAX_TX_OPS`, `APPITOOLS_MAX_SSE_PER_TENANT`, `REDIS_URL`, `OBS_DB_PATH`, `BACKUP_DIR`, `SLACK_WEBHOOK_URL`, `APPITOOLS_SYNTHETIC`, `APPITOOLS_SAFEGO_TIMEOUT`, `APPITOOLS_PUBLIC_ROUTE_RPS/BURST`.

**Files:** `APPITOOLS_FILES_BACKEND`, `APPITOOLS_FILES_DIR`, `APPITOOLS_FILES_MAX_BYTES`, `APPITOOLS_FILES_TOKEN_TTL`, `APPITOOLS_FILES_ALLOWED_EXT`, `APPITOOLS_FILES_S3_{BUCKET,ENDPOINT,REGION,ACCESS_KEY,SECRET_KEY,FORCE_PATH_STYLE,PREFIX,SERVE}`.

**Auth/OAuth/MFA:** `APPITOOLS_AUTH_SIGNUP_ROLE`, `APPITOOLS_AUTH_MIN_PASSWORD`, `APPITOOLS_AUTH_REQUIRE_VERIFIED`, `APPITOOLS_AUTH_BASE_URL`, `APPITOOLS_EMAIL_TOPIC`, `APPITOOLS_OAUTH_{GOOGLE,GITHUB,MICROSOFT}_CLIENT_ID/_CLIENT_SECRET`, `APPITOOLS_OAUTH_CALLBACK_URL`, `APPITOOLS_OAUTH_DEFAULT_ROLE`, `APPITOOLS_OAUTH_SUCCESS_REDIRECT`, `APPITOOLS_MFA_KEY`, `APPITOOLS_MFA_ISSUER`.

**Platform admin / GraphQL / Fleet:** `APPITOOLS_PLATFORM_SUPER_ADMIN_ROLE`, `APPITOOLS_PLATFORM_MFA_ISSUER`, `APPITOOLS_GRAPHQL_PLAYGROUND`, `APPITOOLS_FLEET_OPERATOR_KEY`, `APPITOOLS_FLEET_ADMIN_EMAIL`, `APPITOOLS_FLEET_ADMIN_PASSWORD`.

**Worker (binario separado):** `DATABASE_URL`, `JWT_SECRET`, `APPITOOLS_WORKER_MODE`, `APPITOOLS_ENGINE_URL`, `APPITOOLS_TENANT_DOMAIN`, `APPITOOLS_WORKER_ROLE`, `APPITOOLS_FILES_DIR`, `SMTP_{HOST,PORT,USER,PASS,FROM}`, `APPITOOLS_EMAIL_TOPIC`. Hooks webhook: `hmac_secret_env` NOMBRA env vars arbitrarias por hook.

> **Estado de documentación:** repartido en 4 lugares, ninguno completo. `README §Configuration` (las comunes), `config.go` (doc-comments, completo para lo mapeado a `Config`), `DEPLOY.md` (subconjunto), `.env.example` (solo lo de compose). **Huérfanas** (no en `.env.example`, apenas en README): `RATE_LIMIT_*`, `DB_MAX_CONNS`, `APPITOOLS_MAX_TX_OPS`, `APPITOOLS_MAX_SSE_PER_TENANT`, `APPITOOLS_SYNTHETIC`, `APPITOOLS_PPROF_PORT`, `BACKUP_DIR`, `APPITOOLS_SAFEGO_TIMEOUT`, `GOMEMLIMIT`, `GOGC`. No hay una referencia única autoritativa.

---

## 2. GAPS contra "server vacío → app productiva en minutos, automatizado"

1. **[BLOQUEANTE] Docker roto en HEAD** (#1). Todo el camino Docker (Niveles 1 y 2, quick-start del README, imagen publicada) no compila hasta arreglar el `go:embed docs/` vs `.dockerignore`. Fix trivial (un-ignore selectivo `!docs/BACKEND_SPEC_LLM.md`, o mover el `.md` embebido fuera de `docs/`) — pero es reporte, no se tocó.
2. **[ALTO] Imagen con Studio/admin vacíos** (#2). El CI debe correr `make admin-ui editor-ui` antes del `docker build` (o commitear los assets). Hoy la promesa "el binario trae las UIs" solo se cumple en builds locales/manuales.
3. **[ALTO] Sin provisión de server vacío** (#3). No existe el paso 0: instalar/asegurar Postgres, aplicar el DDL de control plane, escribir el `.env`/start script, instalar systemd, configurar Caddy+DNS, registrar el primer tenant. El DevHub asume el box ya montado. Un tercero (o Miguel con un box nuevo) hace todo eso a mano siguiendo DEPLOY.md. **Este es el gap central del objetivo.**
4. **[MEDIO] El binario no sirve el frontend del usuario** (#4). Para "una app productiva" (API+UI) falta o (a) servir estáticos de terceros desde el binario, o (b) documentar/scriptear el front same-origin vía Caddy. Hoy es DIY.
5. **[MEDIO] Sin release cortado + sin systemd versionado** (#8). El Nivel 3 ("baja el binario de Releases") no funciona hasta el primer `git tag v*` (y repo público). Los `.service` viven solo en la doc.
6. **[MEDIO] `GOMEMLIMIT` sin default en el target de 1 GB** (#6). Riesgo de OOM sin techo blando; hay que setearlo a mano y no está en ningún ejemplo.
7. **[MENOR] Control plane no auto-bootstrap en single-engine** (#7). Con Postgres BYO/manejado hay que aplicar `001_control_plane.sql` a mano; DEPLOY.md Nivel 3 lo cubre solo si usás `docker-compose.db.yml`.
8. **[MENOR] Backups no automatizados**; `pg_dump` no está en la imagen por defecto; sin runbook de restore.
9. **[MENOR] Deploy del binario CUSTOM sin plantilla.** `examples/backend-guide` (main.go + schema.json) no trae Dockerfile/compose/deploy; el único Dockerfile es para `./cmd/appitools`. Un backend custom adapta todo a mano.

---

## 3. El procedimiento de deploy ACTUAL (la línea base a automatizar)

**A. Montaje inicial del box (HOY 100% MANUAL, documentado en DEPLOY.md — no scripteado):**
1. VPS con dominio + `A` record; abrir 80/443 (+ SSH).
2. Postgres: `docker-compose.db.yml` (o PG propio) → aplica el DDL de control plane vía initdb (o `psql < migrations/001_control_plane.sql` si es BYO).
3. Usuario de sistema, `/etc/appitools/schema.json`, `/etc/appitools/engine.env` (los 3 required + tuning), `chmod 600`.
4. Unit `appitools.service` (+ `appitools-worker.service`) copiado de la doc; `systemctl enable --now`.
5. Caddy/nginx: TLS + reverse_proxy (Host passthrough, keepalive); certificados (HTTP challenge o DNS-challenge para wildcard).
6. Registrar el primer tenant (`POST :9090/tenants` con `X-Admin-Key`, desde el box) y mintear el primer JWT.

**B. Re-deploy de una versión (AUTOMATIZADO vía DevHub, panel Deploy):**
1. Guard: árbol git limpio (`status --porcelain -uno`).
2. Build **local** canónico: `scripts/build-engine.sh /tmp/appitools-deploy <shortSHA> <sha>`.
3. Push `scp` como `<binary>.new`.
4. **Backup** del binario vivo → `<dir>-rollback/<base>.<timestamp>` (verifica por tamaño; retiene 10; aborta si no respalda).
5. Swap: `mv .new` → `pgrep -x <base>` + `kill -TERM` + espera exit ≤30 s (graceful).
6. `bash <start_script>` (el script del box, sourcea SUS secretos).
7. Smoke por túnel SSH: `/health` (version==SHA) + `/readyz` == 200.
8. Registra el deploy en SQLite del DevHub.

> El "artesanal" del enunciado = **la parte A** (montaje del box nuevo). La **parte B** ya está bien automatizada, pero solo re-despliega binario a un box previamente montado, y depende del DevHub (systemd en el 105).

---

## 4. Lo aprovechable (la base del camino oficial)

- **`scripts/build-engine.sh`** — build canónico ÚNICO (estático, version-stamped), ya compartido por Dockerfile/release.yml/DevHub. La base de cualquier automatización.
- **Graceful shutdown (`pkg/shutdown`)** — probado; con `/readyz`→503 habilita drain-then-swap detrás de un LB. Zero-downtime real necesita blue/green o symlink encima de esto.
- **Pipeline del DevHub (`deploy.go`)** — build→push→backup→swap→smoke ya resuelve el re-deploy con rollback; es el molde para un script de deploy standalone (sin depender del DevHub).
- **`migrations/001_control_plane.sql` + `migrations.ControlPlane` (embed) + `fleet.BootstrapControlPlane`** — el DDL idempotente ya existe y ya se aplica programáticamente en el fleet; extenderlo a single-engine cierra el gap #7.
- **Compose (dev/prod/db)** — los tres funcionan y ya inyectan el DDL; el `prod.yml`+Caddy es el "Nivel 2" listo (una vez arreglado #1/#2).
- **Verificación post-deploy REUSABLE:** `scripts/acceptance-test.sh` (smoke E2E ~39 checks contra cualquier instancia), `scripts/api-cert.sh` (Newman PASS/FAIL sobre nimbus), y **flow tests in-engine** (FLOWTEST-S1, regresión anclada a la versión de schema).
- **`make fleet-init`** — lo más cercano a "provisión automatizada" que existe: scaffold de manifest + secretos generados + DBs bootstrapeadas. Modelo a mirar para un `appitools init-prod`/`setup` de single-app.
- **Health/readiness + logs JSON + `/metrics`** — todo lo que un proxy/orquestador/journald necesita ya está.

---

## 5. Confirmaciones de la sesión

- `go build ./cmd/appitools` → **OK**. Binario canónico stripped = **60.6 MB**.
- `docker build` → **FALLA** (reproducido; hallazgo #1). Ninguna imagen quedó tageada.
- Graceful SIGTERM → **OK** (probado en :18080: `/readyz`→503, exit limpio 5.1 s).
- Estáticos de terceros → **no soportado** (verificado por código + flags).
- TLS directo → **no existe** (verificado por grep).
- `make test` (unit -race -short) → **VERDE**: exit 0, **42 paquetes `ok`, 0 FAIL, 0 data races** (lento en 1 vCPU: ~15 min).
- Fleet :8080 y servers remotos: **intactos**. Test en puertos scratch 18080/19090 contra la DB de dev; sin tenants creados; scratch limpiado.
