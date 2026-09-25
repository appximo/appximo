# Manual de operación de Appximo

**Para quien tiene que operar una app hecha con Appximo** — el dueño, o el desarrollador de un cliente — y no quiere tener que preguntar. Está escrito en español y en lenguaje llano; la referencia técnica en inglés sigue siendo [docs/PRODUCTION.md](PRODUCTION.md) y [docs/CAPABILITIES.md](CAPABILITIES.md). Este documento no las repite: dice **qué hace el motor, dónde se ve cada cosa, qué se puede cambiar, qué hacer cuando pasa algo, y cómo repetir cualquier escenario con un comando**.

Regla del manual: **ninguna afirmación sin el comando o la ruta que la respalda.** Todos los comandos de este documento se ejecutaron el 2026-08-31 contra un motor real (la caja del laboratorio `tools/lab` — un droplet de 2 vCPU / 2 GB instalado con `scripts/install.sh` — y un motor de desarrollo con datos de una empresa de ejemplo). Las capturas son de esas mismas corridas, con el panel en español. El índice de todo lo que existe, por área, está en [docs/ESTADO_DEL_MOTOR.md](ESTADO_DEL_MOTOR.md).

**Índice**

1. [Qué hace el motor hoy](#1-qué-hace-el-motor-hoy)
2. [Qué ve y dónde](#2-qué-ve-y-dónde)
3. [Qué puede cambiar y qué mueve cada cosa](#3-qué-puede-cambiar-y-qué-mueve-cada-cosa)
4. [Qué hacer cuando pasa algo](#4-qué-hacer-cuando-pasa-algo)
5. [Cómo desplegar y cómo poner al día una caja vieja](#5-cómo-desplegar-y-cómo-poner-al-día-una-caja-vieja)
6. [Repetir cualquier escenario: `appximo drill`](#6-repetir-cualquier-escenario-appximo-drill)
7. [Qué NO hace el motor](#7-qué-no-hace-el-motor)
8. [Apéndice: dónde vive cada cosa en la caja](#8-apéndice-dónde-vive-cada-cosa-en-la-caja)
9. [El centro de mando: toda la operación en una pantalla](#9-el-centro-de-mando-toda-la-operación-en-una-pantalla)

---

## 1. Qué hace el motor hoy

Appximo es **un binario** que, a partir de un archivo `schema.json`, levanta una API completa (REST + GraphQL + su documentación) sobre PostgreSQL, con un cliente (tenant) por subdominio y datos aislados por tenant. Lo que un operador necesita saber que existe, sin adornos:

| Área | Qué hay | Cómo se ve |
|---|---|---|
| **La app** | REST (`/api/<recurso>`), GraphQL (`/graphql`), documentación interactiva (`/docs`), un panel de datos genérico para el dueño (`/app`), un editor visual del schema (`/editor`, «Studio») | `curl https://SU-DOMINIO/health` responde `{"status":"ok","version":"…"}`; `/docs` abre en el navegador |
| **Usuarios** | Registro/login/recuperación de contraseña, login social (Google, GitHub, Microsoft), segundo factor TOTP; roles y permisos por fila declarados en el schema (RBAC) | `/admin` → **Usuarios**; `POST /auth/login` |
| **El panel de administración** | `/admin`: tenants, usuarios, datos (solo lectura), archivos, historial de versiones del schema, **Observabilidad** (latencia, SLO, cada request con sus etapas, los 500 explicados) y **Recursos** (el motor midiéndose a sí mismo y diciendo si el cuello es la app, la base o la caja). En español o inglés (botón `ES`/`EN` arriba a la derecha) | `https://SU-DOMINIO/admin` |
| **Protecciones bajo carga** | Control de admisión (rechaza con 429 antes de volcarse), límite de tasa por tenant, guardia de memoria (503 en escrituras cuando la caja está por quedarse sin RAM), breaker hacia la base (503 rápidos cuando la base no responde) | §3 y `appximo drill saturate` |
| **Backup y restauración** | Un backup nocturno completo (base + archivos subidos + secretos + manifiesto de conteos), con verificación de índices (`pg_amcheck`), copia fuera de la caja opcional; una restauración cronometrada y verificada | `sudo bash /opt/<app>/scripts/backup.sh --app=<app>` · `sudo bash /opt/<app>/scripts/restore.sh --app=<app> --set=…` · `appximo drill restore` |
| **Alertas** | Backup fallido o viejo, disco bajo, SLO quemándose, el primer 500 de cada tipo nuevo, cola varada, workflow vencido — **al celular por Telegram** (`APPXIMO_TELEGRAM_BOT_TOKEN` + `_CHAT_ID`, en español, con qué hacer) y/o a Slack; sin destino, una línea en el journal y un aviso FUERTE al arrancar | §3b · `journalctl -u <app> -o cat \| grep -i alert` |
| **Despliegue** | Un comando que hace backup, cambia el binario, **verifica desde afuera** (versión, lectura, escritura que se deshace) y **se revierte solo** si algo falla | `scripts/deploy-app.sh` (§5) |
| **Auditoría de la caja** | Un comando que dice **qué falta** (timer de backup, copia fuera, destino de alertas, swap, checksums, política de reinicio de PostgreSQL) | `appximo drill audit` |
| **Simulacros** | Diez experimentos de caos, un 500 real, carga, saturación, restauración — cada uno con «qué va a pasar» y «dónde mirarlo» | `appximo drill list` |

Lo que hay debajo (para ubicarse, no para operar): Go 1.25 sin CGO, un solo proceso, PostgreSQL con un schema por tenant, Caddy delante con HTTPS automático, systemd que reinicia el proceso si se cae. Una app es **una caja** (§7).

---

## 2. Qué ve y dónde

Todo lo de esta sección está en `https://SU-DOMINIO/admin`. Se entra con el super-administrador de la plataforma (el primero se crea desde la misma pantalla de login con el `ADMIN_KEY` del servidor, o con `appximo admin create --email … --password …`). Arriba a la derecha se elige el **tenant** sobre el que operan Datos, Usuarios, Archivos, Historial y Observabilidad; Inicio y Recursos son de todo el motor.

Cada sección tiene una línea debajo del título que dice qué pregunta responde. Si se pierde, esa línea es el mapa.

### 2.1 Un 500 con su causa, su consulta y su usuario

**Dónde:** `/admin` → elija el tenant → **Observabilidad** → pestaña **Problemas** (`/admin#/observability?tab=issues`).

![Observabilidad → Problemas: un 500 agrupado por causa, con eventos, usuarios afectados y el enlace a la traza](img/manual/03-observabilidad-problemas.png)

- **Problemas (24 h)**: una fila por *defecto*, no por ocurrencia — los 500 se agrupan por endpoint + mensaje normalizado + la función donde se capturó. La fila trae cuántos eventos, cuántos usuarios distintos, desde cuándo, y un enlace **↗** a una traza de ejemplo.
- Al hacer clic en la traza se abre en **Trazas** con la **cascada**: la etapa que falló marcada `✗ falló aquí`, **el mensaje del error tal como lo dio la base**, **la sentencia SQL que el driver rechazó** (sin los valores), **el usuario y el rol** que la mandaron, y **Copiar como curl** para reproducirla (la autorización nunca se guarda; el curl lleva `$TOKEN`).

![Trazas → la cascada de un 500: la etapa `query` marcada, la sentencia INSERT que falló, usuario y rol](img/manual/04-observabilidad-traza.png)

- La misma información está en el **journal** como una línea JSON por 500, con `trace_id`, `sql` y `site`:

  ```bash
  journalctl -u <app> -o cat --since -1h | grep '"level":"error"' | tail -3
  ```

- La **primera vez** que aparece un tipo nuevo de 500, el motor dispara una alerta (al celular por Telegram y/o a Slack si hay destino configurado — §3b; siempre queda en `journalctl -u <app> -o cat | grep -i alert`).
- **Errores recientes (en memoria)**, más abajo en la misma pestaña, es la lista corta desde el último arranque; se pierde al reiniciar. Los persistidos de 24 h son «Problemas».

Para provocar uno y verlo con sus propios ojos: `appximo drill error --app=<app>` (§6).

### 2.2 Los errores agrupados y las anomalías de latencia

**Dónde:** la misma pestaña **Problemas**. Debajo de los problemas están las **anomalías de latencia** (requests cuya latencia se salió más de 3 desviaciones de la media móvil del tenant) y el estado del **SLO** (sano / alerta / crítico, con la proporción de errores de 5 minutos y la tasa de consumo del presupuesto de error).

### 2.3 El consumo de recursos y el veredicto del cuello de botella

**Dónde:** `/admin` → **Recursos** (`/admin#/resources?tab=live`). No depende del tenant: es el proceso entero.

![Recursos → En vivo: el veredicto, y las tarjetas de requests, memoria y CPU](img/manual/06-recursos-vivo.png)

- La franja negra es **el veredicto**: cada 10 s (1 s mientras la pantalla está abierta) el motor se mide — CPU, GC, locks, memoria, pool de conexiones, latencia de consultas, presión del host — y **dice en una frase de quién es el problema**: `Sano`, `CPU saturada` (la app o el tamaño de la caja), `Pool agotado` (la base o la configuración del pool), `Limitado por la base`, `Presión de memoria`, `CPU limitada (quota)` (el plan del proveedor, no el código), `Presión del GC`, `Contención de locks`. El botón **Evidencia** muestra las señales y sus umbrales — es una regla determinista, no una adivinanza.
- La pestaña **Prueba de carga** (`?tab=load`) muestra la **atribución por tick** durante una corrida y cinco gráficas correlacionadas (latencia, tasa, CPU/GC/locks, pool, presión del host). Es la pantalla para mirar mientras corre `appximo drill load` o `saturate`.

![Recursos → Prueba de carga: la franja de atribución por tick y las gráficas de la corrida](img/manual/08-recursos-carga.png)

- **Instantánea** exporta la corrida entera como JSON, para pegarla en un reporte o compararla con otra.
- Lo mismo, para máquinas: `curl -H "X-Admin-Key: $ADMIN_KEY" http://127.0.0.1:<puerto>/admin/resources?live=1` desde la caja, y las 21 métricas `appximo_selfmon_*` en `/metrics`.

### 2.4 El estado del backup y del disco

**Dónde:** dos lugares. En **Inicio** (`/admin#/`), la franja **Salud ahora** trae cuatro tarjetas — veredicto, último backup, disco, problemas de 24 h del tenant elegido — cada una con un enlace a la pantalla que la explica. Y en **Recursos → En vivo**, la fila **Disco y backup** al final.

![Inicio: la franja «Salud ahora» — veredicto, último backup, disco y problemas del día](img/manual/01-inicio.png)

![Recursos → «Disco y backup»: espacio libre por ruta con su piso, y el último backup con edad, estado y la línea de estado que dejó](img/manual/07-recursos-disco-backup.png)

- **Último backup**: `ok` / `VIEJO` (más de 36 h — `APPXIMO_BACKUP_MAX_AGE`) / `FALLÓ` / `NUNCA CORRIÓ`, con hace cuánto y la línea exacta que dejó `backup.sh` en `last-backup.status`. Si dice *no vigilado*, falta `APPXIMO_BACKUP_DIR` en el env (el instalador lo pone).
- **Disco**: por cada ruta que importa (archivos subidos, la base de observabilidad, el directorio de backups, `/`) el porcentaje libre y `BAJO` cuando cruza el piso (10 % o 1 GiB, `APPXIMO_DISK_MIN_FREE_PCT`/`_MB`).
- Las dos disparan la misma alerta que los 500 (Slack o journal), una vez cada 6 horas por clase.
- Desde la terminal: `cat /var/backups/<app>/last-backup.status` y `df -h /var/backups/<app>`.

### 2.5 Las métricas y los SLO

**Dónde:** **Observabilidad → Métricas** (`?tab=metrics`): p50/p95/p99 sin caché, requests muestreadas, anomalías, y dos gráficas — latencia en el tiempo y consumo del SLO con sus umbrales (6× alerta, 14,4× crítico). El botón **En vivo** actualiza cada 5 s.

![Observabilidad → Métricas: latencia y consumo del SLO del tenant](img/manual/05-observabilidad-metricas.png)

- Cada lectura de la API también trae su tiempo en el encabezado `Server-Timing` (`query;dur=…` es la base de datos). El `/app` lo muestra al pie de cada lista.
- Para Prometheus: `/metrics` con `X-Admin-Key`.

### 2.6 Lo demás del panel, en una línea cada uno

| Sección | Responde | Ruta |
|---|---|---|
| **Inicio** | ¿Qué hay en este motor y cómo está ahora? | `/admin#/` |
| **Tenants** | ¿Qué instancias existen? Crear (con su schema), suspender, borrar (escribiendo el id) | `/admin#/tenants` |
| **Datos** | ¿Qué datos tiene este tenant? Solo lectura; para editar, el `/app` del tenant | `/admin#/data` |
| **Usuarios** | ¿Quién entra y con qué rol? Crear, cambiar rol, suspender | `/admin#/users` |
| **Archivos** | ¿Qué subió este tenant? Descargar (URL firmada), borrar | `/admin#/files` |
| **Historial** | ¿Qué versiones del schema se desplegaron? Ver cualquiera; volver atrás desde Studio | `/admin#/history` |
| **Studio** | Diseñar y desplegar el schema (con vista previa de la migración y aprobación de borrados) | `/editor` |
| **Docs de la API** | El contrato OpenAPI, para probar desde el navegador | `/docs` |
| **Cola de eventos** | ¿Hay eventos esperando o fallados? Cuántos, de qué tema, **hace cuánto el más viejo**, y cada fallado **con su error** (`last_error`); también el estado de los disyuntores | `GET /admin/outbox` (con `X-Admin-Key` o token de plataforma) |
| **Workflows** | ¿Qué automatizaciones existen y cómo les fue? Última corrida (con el detalle por paso y el error), próxima, fallos de 24 h | `GET /admin/workflows` |

**La cola de eventos, en una frase:** un recurso con `events` (o un workflow, o
un webhook agotado) escribe filas en `public.outbox`; **`appximo-worker`** las
consume — y solo las de temas que TIENE consumidor: un tema sin consumidor queda
`pending`, visible, y dispara la alerta de edad
(`APPXIMO_OUTBOX_MAX_PENDING_AGE`, default 15 min). **La métrica que importa es
la edad del pendiente más viejo** (`appximo_outbox_oldest_pending_age_seconds`),
nunca la profundidad: mil que drenan están sanos; uno parado un mes es el
incidente. Un evento que agotó reintentos queda `failed` **con su error en la
fila**; para reintentarlo después de arreglar la causa:
`UPDATE public.outbox SET state='pending', attempts=0 WHERE id=<id>;`.

---

## 3. Qué puede cambiar y qué mueve cada cosa

Todo se configura con variables de entorno en **`/etc/<app>/<app>.env`** (una instalación con `install.sh`) — después de cambiar una, `sudo systemctl restart <app>`. El motor **lee las tres obligatorias** (`DATABASE_URL`, `JWT_SECRET` de ≥ 32 caracteres, `ADMIN_KEY`) y **no arranca sin ellas**, nombrando cuál falta. Un valor inválido en una perilla de seguridad (admisión, guardia de memoria, límite de login, colector) **tampoco arranca** — dice cuál; las demás avisan y siguen con el default.

**De dónde salen los defaults**: cada fila lo dice. «Medido» significa que hay un número en [BENCHMARKS.md](BENCHMARKS.md) detrás; «convención» significa que alguien lo eligió y lo escribió; «Go/PostgreSQL» significa que es el default del runtime.

| Variable | Qué hace | Default | De dónde sale el default | Cuándo tocarla | Si se pasa |
|---|---|---|---|---|---|
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | Límite de requests por segundo **por tenant** (equidad entre tenants y freno de abuso); el exceso recibe `429 rate limit exceeded` | `350 × vCPU` / `100` | **Medido**: 70 % del techo limpio por núcleo de la lectura canónica sin caché en una caja compartida de 2 vCPU (~500 rps/vCPU, [BENCHMARKS §4e](BENCHMARKS.md)); el 70 % es la rodilla de latencia M/M/1 en ρ = 0,7. El motor lo imprime al arrancar: `rate limiter: 700 RPS (derived …)` | Un tenant legítimo con más de 350 rps/vCPU sostenidos (una integración por lotes, una demo pública con una sola cuenta) | El limitador deja de proteger a los demás tenants de uno abusivo; la capacidad real la cuida la admisión, no esto |
| `APPXIMO_MAX_INFLIGHT` | **Control de admisión**: cuántas requests puede haber **en vuelo** a la vez; el exceso recibe `429 server at capacity` + `Retry-After: 1` *antes* de hacer trabajo | `auto` = máx(32, 4 × (vCPU + pool)) — 48 en 2 vCPU con pool 10 | **Medido** ([BENCHMARKS §4e](BENCHMARKS.md)): sin admisión el motor no degrada, **vuelca** (p50 1 728 ms, 79 013 timeouts a 4 800 rps); con ella +20 % de goodput, p50 36 ms, 0 timeouts. La cola de 48 son decenas de ms de espera en el techo | Casi nunca. `0` la apaga (para medir el volcamiento a propósito) | Más alto: la cola crece y las latencias con ella; el volcamiento vuelve. Más bajo: rechaza tráfico que la caja aguantaría |
| `DB_MAX_CONNS` | Tamaño del **pool** de conexiones a PostgreSQL | `10` | **Convención medida**: `núcleos × 2 + 1 = 5` con margen → 10; más conexiones agregan backends ociosos sin más rendimiento cuando la CPU está saturada (comentario en `pkg/db/pool.go`) | Solo si la base vive en **otra** caja más grande | Más RAM en PostgreSQL por backend, y el veredicto `Pool agotado` deja de ser la primera pared — pero la CPU sigue siendo el techo |
| *(tiempos fijos, no son variables)* | Timeout de una consulta **5 s**; de una transacción por lotes **15 s**; de una ruta custom **5 s** (`Route.Timeout`); lectura de cabeceras 10 s, lectura 20 s, escritura 30 s, idle 120 s | — | Convención en código (`pkg/db/tenant.go`, `route.go`, `app.go`) | No se tocan por env. Una consulta que pasa de 5 s es un problema de índice, no de timeout | — |
| `APPXIMO_SAFEGO_TIMEOUT` | Cuánto puede tardar una tarea en segundo plano lanzada por un handler custom (`Ctx.SafeGo`) | `30` s | Convención | Un handler que llama a un servicio externo lento | Goroutines vivas más tiempo tras un pico |
| `APPXIMO_MEMORY_GUARD_MIN_MB` | **Guardia de memoria**: mientras `MemAvailable + SwapFree` esté por debajo, las **escrituras** responden `503` + `Retry-After: 5`; las lecturas siguen | máx(32 MiB, 2 % de la RAM) — 39 MiB en 2 GB | Convención, deliberadamente **bajo**: dispara solo cuando el kernel está a punto de matar procesos; se mide con el swap incluido porque en una caja con PostgreSQL `MemAvailable` vive cerca de cero por los `shared_buffers` (MIGRACION-CONFIANZA-S1) | Subirlo si la caja no tiene swap y una carga masiva la acerca al OOM | `0` apaga la guardia. **Degrada, no aguanta**: un proceso ajeno puede igual disparar el OOM killer (§7). Mejor: agregar swap (§4.4) |
| `GOMEMLIMIT` | Techo blando del heap de Go (el GC trabaja más antes de pasarlo) | El instalador lo fija en **30 % de la RAM** (mínimo 256 MiB); sin instalador, 90 % del límite del cgroup si existe | **Medido** (`scripts/verify-production`, [BENCHMARKS §7](BENCHMARKS.md)): 1 536 MiB en una caja de 2 GB era peor que nada | Una caja de 1 GB que hace thrashing (`< 128 MiB` es demasiado poco) | Muy alto: el proceso compite con PostgreSQL por la RAM; muy bajo: el GC consume CPU |
| `APPXIMO_BACKUP_DIR` | Dónde deja los sets `backup.sh` **y** activa la vigilancia del último backup (capa 5 del colector) | El instalador pone `/var/backups/<app>`; sin la variable, **no se vigila** | Convención del instalador | Si mueve los backups a otro disco | Sin ella, el panel dice «no vigilado» y no hay alerta de backup viejo/fallido |
| `APPXIMO_BACKUP_MAX_AGE` | A partir de qué edad el último backup cuenta como **viejo** (alerta) | `36h` | Convención: un timer nocturno (03:30) con margen para una corrida perdida | Si el timer corre cada hora, bájela a `3h` | Muy larga: dos noches sin backup pasan en silencio |
| `APPXIMO_DISK_MIN_FREE_PCT` / `_MB` | Piso de disco libre bajo el cual el panel marca `BAJO` y sale la alerta (sobre archivos, obs, backups y `/`) | `10` % / `1024` MiB | Convención (RESILIENCIA-S1): PostgreSQL entra en PANIC al llenarse; el aviso tiene que llegar antes | Discos muy grandes (10 % de 2 TB es mucho) → use `_MB` | Muy bajo: cuando avisa ya no cabe el próximo backup |
| `BACKUP_COPY_TO` | Destino **fuera de la caja** del set: `usuario@otra-caja:/ruta` (scp) o `remoto:bucket/ruta` (rclone: Spaces, S3, R2, B2) | vacío = **el backup muere con el disco** | — | **Siempre** en producción. `appximo drill audit` lo marca ✗ mientras falte | — |
| `BACKUP_PASSPHRASE_FILE` | Archivo (0600) con la frase para cifrar el paquete de secretos (`.conf.tar.enc`) antes de salir de la caja | vacío = **los secretos no salen** (dump y archivos sí) | — | Junto con `BACKUP_COPY_TO` | Sin ella, una caja perdida recupera los datos pero no `JWT_SECRET`/`ADMIN_KEY`: todos los tokens y el MFA se invalidan |
| `BACKUP_KEEP` | Cuántos sets se conservan | `14` | Convención: 14 noches ≈ 530 MB en una app de 38 MB por dump | Con timer horario, `48` | Disco |
| `BACKUP_AMCHECK` | Verifica todos los índices y páginas con `pg_amcheck` en cada backup (un índice corrupto es invisible a la app y al `pg_dump`) | `on` | Medido: 0,9 s por 124 MB / 251 k filas (DEPLOY-FLOTA-S1) | `off` solo si `pg_amcheck` no está instalado (el script ya lo salta con un aviso) | — |
| `APPXIMO_TELEGRAM_BOT_TOKEN` + `APPXIMO_TELEGRAM_CHAT_ID` | **Destino de alertas al celular** (§3b): SLO, primer 500 de cada tipo, backup fallido/viejo, disco bajo, cola varada, workflow vencido — en español, con qué hacer | vacío = ver `SLACK_WEBHOOK_URL`; sin NINGÚN destino cada alerta es **una línea en el journal que nadie lee** (era OPS-47) | — | **Siempre** en producción. Los dos o ninguno: a medias o mal formado, **no bootea** nombrando la variable. `drill audit` lo verifica EN VIVO (getMe+getChat, sin mandar mensaje) | El token es una credencial: solo en el `.env` (0600), nunca en un repo ni un log |
| `APPXIMO_ALERT_APP_NAME` | El nombre de la app que lleva cada mensaje (varias apps alertando a UN chat necesitan decir quién habla) | el `name` del schema | — | Siempre que el nombre del schema no sea el que usted usa | — |
| `APPXIMO_ALERT_PANEL_URL` | Origen público (`https://app.ejemplo.com`): con esto cada alerta lleva el enlace «Ver el panel» | vacío = sin enlace | — | Siempre en producción | — |
| `SLACK_WEBHOOK_URL` | El mismo destino, por Slack (convive con Telegram: todos los destinos configurados reciben todo) | — | — | Si usted usa Slack | — |
| `APPXIMO_TRACE_BODY` | Guardar (redactado, 4 KiB) el cuerpo de la request en las trazas con error | `off` | Convención de privacidad (OBSERVABILIDAD-ERRORES-S1): los cuerpos llevan datos personales | Mientras se persigue un 500 que depende del contenido | Cuerpos de clientes en `obs.db` |
| `APPXIMO_SELFMON` / `_INTERVAL` / `_LIVE_INTERVAL` / `_P99_MS` | El colector de Recursos: apagarlo (`off`), su cadencia (`10s`; `1s` mientras el panel mira), y el piso absoluto de «lento» del veredicto (`50` ms) | on / 10s / 1s / 50 | Medido ([BENCHMARKS §4c](BENCHMARKS.md)): 0 asignaciones por request, 1,07 MiB de RAM fija, CPU no distinguible del ruido | El piso, si la app es de por sí lenta (informes de segundos) y todo lee «lento» | Sin colector no hay veredicto ni tarjetas de disco/backup en el panel |
| `APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE` / `_BURST` | Intentos de login por (tenant, correo) por minuto; el 6.º recibe `429` | `5` / `5` | Convención de seguridad (defensa contra fuerza bruta); el motor **avisa al arrancar** si se sube | Una demo pública donde todos entran con la misma cuenta (la tiendita usa `60`) | Debilita proporcionalmente la defensa; el aviso de arranque lo recuerda |
| `APPXIMO_APP_THEME_CSS` | Ruta a un CSS con los tokens `--app-*` del `/app` (marca del cliente): `:root { --app-accent: #FF5A36; }` basta | vacío = tema embebido | — | Para que el panel del dueño tenga el color de su marca | Un archivo ilegible avisa y sirve el default |
| `APPXIMO_APP_BANNER_TEXT` / `_HREF` | Una barra de retorno arriba del `/app` («← volver a …») | vacío = sin barra | — | Una demo enlazada desde un sitio | — |
| `APPXIMO_APP_DEMO_ROLES` | Roles para los que el `/app` **simula** las escrituras en el navegador (nada llega a la base; recargar borra todo) | vacío | — | Una demo pública. **Emparejar con un rol de solo lectura**: el RBAC es la frontera real (una escritura directa a la API recibe 403) | Un rol con permiso de escritura en esta lista sigue pudiendo escribir por la API |
| `APPXIMO_ENV` | `development` enciende pprof (:6060), la introspección de GraphQL y GraphiQL, y logs legibles; cualquier otra cosa = producción (logs JSON) | producción | — | Nunca en una caja pública | Expone pprof e introspección |
| `APPXIMO_CORS_ORIGINS` (+ `_METHODS`, `_HEADERS`, `_CREDENTIALS`, `_MAX_AGE`) | CORS para un frontend servido desde **otro** origen; ponerla lo enciende | vacío = apagado | — | Solo si el frontend NO está embebido en el binario (el camino recomendado es embebido: mismo origen, sin CORS) | `*` con credenciales refleja el origen; el control plane y `/admin` nunca reciben CORS |
| `APPXIMO_MAX_TX_OPS` | Operaciones por `POST /api/transaction` | `100` | Convención; 100 creates ≈ 50–70 ms en 1 vCPU | Importaciones por lotes más grandes | Transacciones largas bloquean filas más tiempo |
| `APPXIMO_FILES_DIR` / `APPXIMO_FILES_BACKEND` / `APPXIMO_FILES_MAX_BYTES` | Dónde viven los archivos subidos (`/var/lib/<app>/files`), disco local o S3 (`s3` + `APPXIMO_FILES_S3_*`), tamaño máximo por archivo (256 MiB) | local / 256 MiB | Convención | S3/R2/Spaces cuando los archivos no deban vivir en la caja | — |

**Lo que no está en esta tabla y también existe** (con su default en [PRODUCTION.md §8](PRODUCTION.md)): signup público (`APPXIMO_AUTH_SIGNUP_ROLE`), login social (`APPXIMO_OAUTH_*`), MFA (`APPXIMO_MFA_KEY`), GraphiQL en producción (`APPXIMO_GRAPHQL_PLAYGROUND`), SSE por tenant (`APPXIMO_MAX_SSE_PER_TENANT`, default 1000), montajes estáticos (`APPXIMO_STATIC_DIR`), el worker de correo (`SMTP_*`, `APPXIMO_WORKER_MODE`), Redis para migraciones en segundo plano (`REDIS_URL`).

**Cómo saber con qué arrancó el motor**: la primera pantalla del journal lo dice todo —

```bash
journalctl -u <app> -b -o cat | grep -E 'rate limiter|admission|memory guard|backup|selfmon|GOMEMLIMIT' | head
```

---

### 3b. Alertas al celular — Telegram, paso a paso

Todo lo que el motor sabe avisar (backup fallido o viejo, disco bajo, SLO
quemándose, el primer 500 de cada tipo, cola varada, workflow vencido) llega
a su celular por Telegram: en español, diciendo **qué pasó, en qué app y qué
hacer**, con la severidad a la vista y el enlace al panel.

**Configurarlo (una vez, ~3 minutos):**

1. **Crear el bot**: en Telegram hable con `@BotFather` → `/newbot` → le da el
   token (`123456789:AA…`). Un solo bot sirve para todas sus apps.
2. **Sacar su chat id**: abra el chat con su bot nuevo, mándele cualquier
   mensaje, y corra
   `curl -s "https://api.telegram.org/bot<TOKEN>/getUpdates"` — el número en
   `"chat":{"id":…}` es su chat id.
3. **Ponerlo en la app** — en `/etc/<app>/<app>.env` (0600; **el token es una
   credencial: va acá y en ningún otro lado** — nunca en un repo, un log ni
   un chat):

   ```
   APPXIMO_TELEGRAM_BOT_TOKEN=123456789:AA…
   APPXIMO_TELEGRAM_CHAT_ID=8851136988
   APPXIMO_ALERT_APP_NAME=La Tiendita
   APPXIMO_ALERT_PANEL_URL=https://tienda.ejemplo.com
   ```

   y `systemctl restart <app>`.
4. **Probar que funciona de verdad**: el journal del arranque tiene que decir
   `telegram alert destination verified (getMe+getChat)`. Después provoque
   UNA alerta real:
   `printf 'failed prueba\n' > /var/backups/<app>/last-backup.status` — en el
   siguiente tick (~10 s) suena el celular y el journal registra
   `alert delivered sink=telegram`. (El próximo backup nocturno reescribe el
   status; si no quiere esperar, corra `backup.sh` a mano.)

**Las reglas que lo protegen:** un token o chat id **mal formado, o solo uno
de los dos, no bootea** y nombra la variable (la misma disciplina del worker).
Un token con forma válida pero revocado sí bootea (el arranque nunca depende
de que api.telegram.org responda) y **grita en el journal**
(`TELEGRAM ALERT DESTINATION NOT WORKING`); `drill audit` /`fleet-audit.sh`
lo verifica EN VIVO con dos llamadas de solo lectura (`getMe` + `getChat`,
sin mandar mensaje) y marca ✗ nombrando el arreglo. **Sin ningún destino la
app arranca pero lo dice fuerte** — ese silencio era OPS-47. **Ninguna alerta
se pierde**: queda en el journal (`alert emitted`) ANTES de intentar la
entrega, se reintenta con retroceso, y el freno de ruido sigue igual (5
errores nuevos por minuto + un resumen de tormenta; una alerta de host cada
6 h por condición; una de cola por hora).

**Si deja de llegar:** `journalctl -u <app> -o cat | grep -i alert` — ¿dice
`alert delivered` o `alert delivery FAILED`? Corra
`sudo bash /opt/<app>/scripts/fleet-audit.sh --app=<app>`: le dice si el
token fue rechazado (rotarlo: @BotFather → `/revoke` → token nuevo → `.env` →
restart) o si el chat no es alcanzable (el chat tiene que haber INICIADO el
bot). `backup.sh` avisa por su cuenta al mismo chat cuando falla, así que la
alerta de backup llega aunque el motor esté caído.

---

### 3c. «Mándame el resumen de hoy» — pedirle al bot por Telegram

El mismo bot que le manda alertas también **recibe** un puñado de comandos y le
contesta con un resumen del día **en lenguaje de dueño**: qué se creó hoy, qué
espera una acción suya, cuántos hay de cada cosa. Solo lectura — escribir
datos por acá llega en una etapa próxima.

**El resumen llega como IMAGEN, con el texto debajo.** Un semáforo y una frase
arriba («13 esperan acción · 2 sin avanzar»), lo que espera acción en grande,
lo que se movió hoy en una lista corta, y lo demás plegado — para leerlo de un
vistazo en el celular, aunque la app tenga veinte recursos. La imagen la
dibuja el motor con los mismos números que cuenta (nada de inteligencia
artificial redactando ni dibujando), y el texto viaja siempre con ella: si la
imagen no carga, el contenido está igual.

**Tres palabras, tres significados.** «Esperan acción» son los estados que su
schema declara como `pending` en la máquina de estados (los que un humano
tiene que mover). «Sin avanzar» aparece cuando no declaró nada: son los
registros que siguen en su estado inicial — el motor lo infiere y lo dice
así, con esa humildad. «En curso» son los demás estados no finales, contados
en las palabras de su schema, sin llamarlos pendientes. Un estado final
(cerrado, cancelado) no cuenta nunca.

**Comandos** (escríbalos al bot, del chat autorizado):

- **`resumen`** — qué pasó hoy (nuevos, actualizados, pendientes por recurso).
- **`estado`** — cuántos hay de cada cosa ahora mismo.
- **`ayuda`** — EJEMPLOS de frases sacados del schema de la app (recursos, estados, sinónimos declarados, banderas): primero las que el parser resuelve al instante y gratis (cada una probada contra el schema antes de mostrarla), después las que piensa el modelo y cuestan unos centavos; termina con los comandos fijos. Nunca es una lista escrita a mano, así que no envejece; cuesta US$ 0 siempre. Cualquier palabra que no entienda devuelve la ayuda,
  nunca un error.

**Cómo se activa** — en `/etc/<app>/<app>.env`, además del token/chat de §3b:

```
APPXIMO_TELEGRAM_SUMMARY_TENANT=<inquilino>   # de qué inquilino es el resumen (y ENCIENDE el canal)
APPXIMO_TELEGRAM_SUMMARY_ROLE=<rol>           # el resumen se calcula COMO este rol (debe existir en el schema)
APPXIMO_TELEGRAM_SUMMARY_USER_ID=<uuid>      # opcional: la IDENTIDAD con la que el canal y el parte actúan — en una app
                                             #   personal (filas por dueno_id) ponga el id del dueño y el chat ES el dueño
```

y `systemctl restart <app>`. El resumen **respeta el RBAC**: muestra exactamente
lo que ese rol puede ver — un rol acotado a sus filas cuenta solo las suyas, y
nunca aparece un recurso que no puede leer. Un día sin movimiento dice «Sin
movimiento hoy», no una lista de ceros.

**El resumen dice qué cambió desde ayer, y el de la mañana calla si no cambió
nada.** Cada resumen se compara con el anterior (el motor guarda UNA foto por
día; no es un historial) y lo dice con números: «16 facturas esperan acción
(+3 desde ayer · 3 llegaron hoy)», «igual que ayer» (plegado en una línea
chica), «Nada que atender — ayer esperaban 26». Tres facturas que llegaron hoy
no pesan lo mismo que dieciséis que llevan meses: el semáforo es rojo solo
cuando hay NOVEDAD (algo creció o llegó hoy), ámbar cuando es el mismo stock
de ayer, verde cuando no hay nada. El primer resumen dice «primer resumen, sin
comparación todavía» — nunca inventa un más-dieciséis.

**El parte automático habla solo cuando importa.** En el schema:

```json
"summary": { "resources": ["ordenes", "pagos", "facturas"], "notify": "changes", "quiet_days": 7 }
```

- `notify: "changes"` (el default): el resumen de las 7 llega solo si algo
  cambió de verdad — un conteo de lo que espera, sus estados, algo que llegó
  hoy, o el semáforo subiendo o quedando en verde. Un «9 nuevos» solo no
  cuenta. `"always"`: el parte todos los días, cambie o no.
- `quiet_days: 7` (default; `0` = nunca): después de siete mañanas calladas
  llega un mensaje corto («🔕 7 días sin novedad. Sigo acá — todo igual») y la
  cuenta arranca de nuevo. Así un canal callado no se confunde con uno roto.
  Además, el comando `estado` termina diciendo cuándo corrió el último parte
  automático y qué decidió («callado a propósito, 3 días sin novedad»).
- El comando `resumen` a mano contesta SIEMPRE. El silencio es del envío
  automático, no de su pregunta.

**Elegir qué entra al resumen.** Si su app tiene muchos recursos, declare en el
schema cuáles entran y en qué orden:

```json
"summary": { "resources": ["ordenes", "pagos", "facturas", "clientes"] }
```

Sin ese bloque entran todos los que el rol puede leer, ordenados por lo que
necesita atención. Un recurso mal escrito ahí **no arranca** — el error lo
nombra. Y en cada máquina de estados, `"pending": ["pagada", "preparando"]`
dice qué estados esperan a alguien (`[]` = ninguno; sin declarar, el motor
infiere «sin avanzar» para los recién creados).

**Quién puede preguntar:** SOLO el chat de `APPXIMO_TELEGRAM_CHAT_ID`. Un
mensaje de cualquier otro chat se **ignora y se registra** — nunca se contesta.
Ese es el control de acceso de todo el canal. Si el canal queda configurado a
medias (falta el rol, o el chat no es numérico), **el motor no arranca y lo
dice**.

**El mismo resumen, agendado.** Un `workflow` de cron manda el resumen solo a la
hora que usted quiera — es el ejemplo canónico de `workflows`
([examples/model-lab/workflows.json](../examples/model-lab/workflows.json)):

```json
"resumen_matinal": {
  "trigger": { "type": "cron", "cron": "0 7 * * 1-5", "timezone": "America/Bogota" },
  "steps": [ { "name": "enviar_resumen", "type": "enqueue",
               "config": { "topic": "summary.telegram", "data": {} } } ],
  "overlap": "skip"
}
```

Con el `appximo-worker` en modo `auto` y el mismo token de Telegram +
`APPXIMO_TELEGRAM_SUMMARY_ROLE`, cada mañana (7 AM Bogotá) el worker evalúa el
resumen y lo manda al chat — con la imagen — solo si cambió algo (o siempre,
según `notify`). Si Telegram no responde, queda `pending` y se reintenta solo
hasta entregarse; si el worker está caído, la métrica `appximo_workflow_overdue_seconds`
crece y su alerta lo delata. **El workflow tiene que estar en el schema
DESPLEGADO del inquilino** (el worker lee el schema guardado, no el archivo de
arranque): `appximo migrate --tenant <inquilino> --schema <archivo>`. En una
caja desplegada con `deploy-app.sh`, agregue `--worker-binary=/ruta/appximo-worker`
y el script instala el worker, escribe su unidad de systemd, completa el env y
verifica que quede activo.

**Desde el iPhone (Siri / Atajos), sin Telegram:** un atajo con una acción
«Obtener contenido de URL» a
`https://<inquilino>.<su-dominio>/api/summary` con el encabezado
`Authorization: Bearer <token>` (uno hecho con `appximo token`), y luego
«Obtener valor `text` del diccionario» → «Mostrar/Decir». Cinco minutos de
armado suyo; el motor ya sirve el endpoint.

---

### 3d. «¿Cuántas órdenes hay hoy?» — preguntarle al bot con sus palabras

Además de los tres comandos, el bot **responde preguntas** sobre sus datos,
escritas o dictadas: «cuántas órdenes hay hoy», «qué pedidos están sin pagar»,
«cuánto vendimos esta semana», «las órdenes de Ana Gómez», «órdenes por
estado», «tenemos cupones vigentes». Cualquier cosa que no sea `resumen`,
`estado` o `ayuda` se toma como una pregunta.

**Cómo funciona, en una frase:** una inteligencia artificial TRADUCE su
pregunta a una consulta cerrada que el motor ya sabe hacer (contar, listar,
sumar sobre un recurso, con filtros y un período); el motor **revisa cada
nombre contra su schema** y rechaza lo que no existe; **el número lo saca de
la base**, nunca lo inventa la IA. La IA no ve sus datos: recibe solo la
lista de recursos, campos y estados de su app, y la pregunta.

**Lo que va a ver en el celular:**

- El número arriba, en negrita, y debajo, en letra chica, **cómo entendió la
  pregunta** («ordenes · esta semana · estado = pagada»). Si la leyó distinto
  a lo que usted quiso, ahí se nota.
- Un nombre mal dictado se **corrige contra los que existen** y se lo dice:
  «Entendí «Ana Gomes» como **Ana Gómez**». Si hay varios parecidos, pregunta
  cuál («¿Cuál? • Ana Gómez • Luis Gómez»). Si no existe, se lo dice («No
  encuentro ningún cliente que se llame «Wilfredo Pacheco»») — nunca le
  devuelve un cero como si fuera la respuesta.
- «**No entendí**» cuando la pregunta no se puede responder con lo que su app
  tiene (o cuando pregunta por algo que no existe), seguido de la lista de
  sobre qué SÍ puede preguntar. Es la respuesta correcta: una cifra
  inventada con cara segura es peor.
- «**Por acá solo leo**» si pide crear, cambiar, cancelar o borrar algo. Las
  escrituras por voz llegan en una etapa próxima, con confirmación.
- Mientras piensa (uno o dos segundos, a veces cuatro) aparece «escribiendo…».
  Si la IA no responde, el bot lo dice y los tres comandos siguen funcionando.
- Una pregunta «por estado» llega también como **imagen**, con la misma
  tarjeta del resumen.

**Respeta los permisos.** La pregunta se responde COMO el rol configurado
(`APPXIMO_TELEGRAM_SUMMARY_ROLE`): un rol acotado a sus filas cuenta solo las
suyas, y un recurso que no puede leer no aparece ni en la lista de lo que
puede preguntar.

**La mayoría de las preguntas no cuestan nada.** Antes de llamar al modelo,
el motor intenta entender la pregunta solo, con las palabras de su schema:
«cuántas órdenes hay hoy», «órdenes pendiente de pago», «cuántos productos
activos», «pagos por método», «las órdenes de Ana Gómez», «cuánto suman las
órdenes de esta semana». Si está seguro, responde al instante y gratis (dos
de cada tres preguntas reales, medido). Si le queda una palabra que no está en
su schema («vendimos», «vigentes»), **no adivina**: pasa la pregunta al
modelo. Y una pregunta repetida (aunque la escriba distinto) reutiliza la
traducción anterior sin llamar a nadie — el número se vuelve a contar
siempre, así que «hoy» mañana es mañana.

**Cuánto cuesta y hasta dónde.** Solo las preguntas que llegan al modelo se
cobran: unas tres décimas de centavo cada una (US$ 0,003). Hay un techo:
**6 preguntas al modelo por minuto** y **US$ 0,50 por día** por inquilino.
Al 80 % del techo diario le llega UNA alerta por Telegram con cuánto va
gastado; al llegar al techo, otra, y **el modelo se apaga hasta mañana** — las
preguntas simples, la caché y `resumen`/`estado`/`ayuda` siguen. Lo peor que
puede pasar en un día, aunque algo se dispare, es gastar el techo. Los topes
se cambian en el env (`APPXIMO_ASK_DAILY_USD`, `APPXIMO_ASK_PER_MINUTE`); un
valor mal escrito no arranca y lo dice. Cuánto lleva gastado hoy y este mes
lo ve en `/admin/ask` (o en `/metrics`), nunca tiene que abrir la consola del
proveedor.

**Saber en qué se gasta** (para quien administra, no para el dueño de la
tienda; todo apagado o discreto por defecto):

- **`gasto`** — un cuarto comando del bot: cuánto va hoy y este mes, cuánto
  falta para el techo, cuántas preguntas resolvió el parser, la caché y el
  modelo, y las frases que más cuestan. Llega como imagen con el texto
  debajo, igual que `resumen`. Solo lo ve un rol administrador (el que tiene
  todos los recursos); un rol acotado recibe «tu rol no puede ver el gasto».
- **La traza en cada respuesta** — con `APPXIMO_ASK_TRACE=on`, cada
  respuesta termina con una línea chica: `⚙︎ parser · 2 ms · US$ 0`, o
  `⚙︎ modelo · 1,1 s · US$ 0,0029 · el parser pasó: palabra fuera del
  schema «vendimos»`. Esa última parte es la que sirve: dice qué palabra
  no está en su schema. **Siri nunca la lee**: el costo se ve, no se escucha.
- **El historial** — el motor guarda cada pregunta (quién la resolvió,
  cuánto costó, cuánto tardó, el plan, por qué cayó al modelo) durante
  `APPXIMO_ASK_HISTORY_DAYS` días (30) y lo poda solo. Nunca guarda la IP.
  **Los nombres propios que la pregunta traía se guardan como `[nombre]`**
  («las órdenes de [nombre]») — queda la forma de la pregunta, no la
  persona; si prefiere no guardar texto, `APPXIMO_ASK_HISTORY_TEXT=none`.
  Las listas útiles están en `/admin/ask?tenant=<inquilino>`: las más
  caras, las más repetidas, las que caen al modelo y por qué.
- **Techo por usuario** — `APPXIMO_ASK_DAILY_USD_PER_USER` (apagado por
  defecto): si varias personas preguntan en la misma app, una sola no puede
  gastarse el día de todas; la que llega a su cupo sigue con las preguntas
  simples y los comandos, las demás normal, y a usted le llega un aviso.

**Cómo se activa** — en `/etc/<app>/<app>.env` (además de lo de §3c):

```
ANTHROPIC_API_KEY=sk-ant-…              # la clave del modelo (nunca en un repo, un log ni un reporte)
APPXIMO_SUMMARY_TIMEZONE=America/Bogota # para que «hoy» sea su hoy (el servidor vive en UTC)
```

y `systemctl restart <app>`. Sin la clave, el bot responde a una pregunta
«Las preguntas libres no están activadas…» y la ayuda; nada más cambia.

**Preguntar por Siri (o cualquier atajo del celular).** Un atajo de tres
pasos:

1. **Dictar texto** (español) — eso es la pregunta.
2. **Obtener contenido de URL** — `https://<inquilino>.<su-dominio>/api/ask`,
   método `POST`, cabecera `Authorization: Bearer <token>` y
   `Content-Type: application/json`, cuerpo JSON con un campo `q` = *Texto
   dictado*.
3. **Obtener valor del diccionario** `speech` → **Leer texto en voz alta**
   (o `headline` para la versión de una línea).
4. *(opcional)* **Obtener valor del diccionario** `display` del mismo
   *Contenido de URL* → **Mostrar resultado**. `display` es la respuesta en
   texto plano (con sus renglones, sin HTML) y, si la app tiene
   `APPXIMO_ASK_TRACE=on`, termina con la línea `⚙︎ parser · 2 ms · US$ 0`:
   **la traza se ve en la pantalla y nunca se escucha** (`speech` no la
   lleva; una voz que dice «tres centavos» después de cada respuesta se
   apaga a los dos días). Una respuesta larga (una lista de diez) entra
   bien: *Mostrar resultado* es una tarjeta que se desplaza.

Diga «cuántas órdenes hay hoy» y Siri le lee el número que contó el motor.

**Sus palabras, declaradas (VOZ-AHORRO-S2).** El motor responde gratis
solo con las palabras del schema; usted dice «pedidos» y el schema dice
`ordenes`, dice «mascotas» y el schema dice `pets`. Esas palabras se
declaran en el schema como `aliases` — en el recurso
(`"ordenes": {"aliases": ["pedidos", "ventas"]}`) y por estado
(`"estado": {"aliases": {"pendiente_pago": ["sin pagar"]}}`) — y desde
ahí el motor las entiende como si fueran el nombre, para preguntar y para
anotar. Un alias que podría significar dos cosas no arranca: el motor le
dice cuál sobra. Con las palabras de sus dos apps declaradas, la porción
que responde el parser pasó del 50 % al 75 % sobre sus preguntas reales.
`gasto` ahora separa el **gasto útil** del **desperdiciado** (lo que se
pagó por un «no entendí»), y una frase que no es una pregunta — «sí pero
mejor el viernes» después de una confirmación, «hola», «qué puedo
preguntar» — se responde sin el modelo.

**Lo que todavía no responde (a propósito):** preguntas que cruzan dos
recursos («clientes de Medellín con órdenes»), comparaciones contra otro
período («¿vendimos más que el mes pasado?»), rankings («el producto más
vendido»), porcentajes, y preguntas encadenadas («¿y ayer?» — cada pregunta
va sola). Para esas responde «No entendí» — y le dice qué sí puede.

### 3e. «Anota llamar a Fabián para mañana» — escribir por voz, confirmando

Desde VOZ-ESCRITURAS-S1 el bot (y el atajo de Siri) también **anota y
cambia** cosas. **Nunca borra.** Y **no escribí nada hasta que usted lee
exactamente qué va a escribir y dice que sí**:

```
usted: Anota llamar a Fabián para arreglar el techo, urgente, para mañana
bot:   📝 Voy a crear tarea:
       • persona: Fabián Gómez
       • prioridad: urgente
       • titulo: llamar a Fabián para arreglar el techo
       • vence en: mañana (dom 20 sep)
       ¿Confirmas? (sí / no)         [✅ Sí] [✖ No]
usted: sí
bot:   ✅ Listo: creé tarea llamar a Fabián para arreglar el techo.

usted: marca como hecha la tarea de Fabián
bot:   ✏️ Voy a cambiar tarea «llamar a Fabián…»:
       • estado: pendiente → hecha
       ¿Confirmas? (sí / no)
usted: dale
bot:   ✅ Listo.
```

**Las reglas, en el orden en que lo protegen:**

- **El sí es un sí.** `sí`, `dale`, `ok`, `confirmo`, `listo`, `de
  acuerdo`. «Sí pero mejor el viernes» o «creo que sí» **no** ejecuta: el
  bot cancela lo pendiente, se lo dice en una línea, y lee la frase como un
  pedido nuevo. `no` cancela. Una confirmación espera **5 minutos**; un
  pedido nuevo reemplaza al anterior y lo dice; una pregunta en el medio lo
  cancela y se responde.
- **Pasa por el mismo camino que la app.** Mismos permisos, mismas
  validaciones, misma máquina de estados, mismos eventos que si lo cargara
  por pantalla. Un rol que no puede crear recibe «por acá solo leo»; un
  cambio que el estado no permite («la tarea ya está hecha») se rechaza
  ANTES de preguntarle; lo que la app rechazaría vuelve como «No pude … No
  escribí nada». El rol `demo` de una vitrina no escribe por voz.
- **Los nombres se buscan antes, y se muestran.** «Fabi» con Fabián Gómez
  y Fabiana Torres en la tabla es una elección numerada («¿Cuál? 1. … 2.
  …»); un nombre que no existe es una oferta: «No encuentro ninguna persona
  "Rocío Paz". sí para crearla» — se crea la persona (con su propio sí) y
  después la tarea, que ya muestra «Rocío Paz (nuevo)». Un nombre dictado
  nunca queda como texto suelto donde va una persona.
- **Lo que no dijo se pregunta, no se inventa.** «anota una tarea para
  Marta» → «Para crear tarea me falta titulo. ¿Qué pongo?». Una prioridad
  que no dijo no se rellena.
- **Las fechas las calcula el motor** en su zona: «mañana», «pasado
  mañana», «el viernes», «el viernes a las 3», «la semana que viene», «fin
  de mes». La confirmación muestra el día.
- **Cuánto cuesta.** Anotar algo nuevo necesita el modelo: **≈ US$ 0,0023 y
  ≈ 1 segundo, una sola vez por frase**: la misma orden repetida («anota
  pagar la luz para mañana», tres veces en un mes) se piensa una vez y se
  vuelve a armar cada vez con los datos del momento — los nombres se
  buscan de nuevo, «mañana» es el de ese día, se pregunta lo que falte —
  antes de pedirle el sí (VOZ-AHORRO-S2; antes cobraba las tres). Cambiar
  un estado («marca como hecha…», «cancela el pedido ORD-1003») lo
  resuelve el motor solo: **US$ 0, al instante**. Decir «sí» no cuesta
  nada, y decir «sí pero…» tampoco: se cancela y se le dice por qué. Aplican los mismos topes y el mismo `gasto` de §3d; el historial
  guarda solo la forma de lo que escribió, nunca el texto.

**En Telegram** la confirmación trae botones **✅ Sí / ✖ No** (y `1 2 3`
cuando hay que elegir); apretar uno es lo mismo que escribirlo, y los
botones desaparecen para que no se apriete dos veces. **En Siri:** después
de *Leer texto en voz alta*, agregue **Pedir entrada** («¿Confirmas?») y
mande la respuesta al mismo `/api/ask` como `q` — el motor sabe que es su
confirmación. El token del atajo tiene que llevar usuario (`--user-id`,
§3d); un token sin usuario lee pero no escribe, y se lo dice.

**Para apagarlo:** `APPXIMO_ASK_WRITES=off` en `/etc/<app>/<app>.env` y
`systemctl restart <app>` — el bot vuelve a solo leer.

**Lo que no hace (a propósito):** borrar (aunque lo pida un administrador),
vaciar un campo, cambiar varias filas de una («cancela todas…»), cargar
archivos. Si el motor se reinicia, una confirmación que estaba esperando se
olvida: lo que no confirmó no pasó.

### 3f. «Agendame una reunión de 4 a 5» — la agenda por voz, con el choque avisado antes (MOTOR-AGENDA-S1)

Si la app declara un **rango de tiempo** (un compromiso con `inicio` y `fin`
que no se puede pisar con otro), el bot lo entiende sin modelo (US$ 0):

```
usted: qué tengo mañana
bot:   📅 3 eventos:
       mar 22 sep
       • 16:00–17:00 reunión con Fabián
       • 16:30–17:30 tentativo: café
       • 17:00–18:00 gimnasio

usted: agenda reunión de planificación mañana de 4 a 5
bot:   ⚠️ Ya tienes «reunión con Fabián» de 16:00 a 17:00.
       📝 Voy a crear evento:
       • inicio: mañana (mar 22 sep) a las 16:00
       • fin: mañana (mar 22 sep) a las 17:00
       • ocupa: no (no bloquea el horario: ya había algo)
       • titulo: planificación
       ¿Igual lo agendo? (sí / no)
usted: sí
bot:   ✅ Listo: creé evento planificación.
```

- **El choque se dice ANTES de escribir.** Si usted confirma igual, el
  compromiso se guarda como que **no ocupa** el horario (así no bloquea lo
  que venga después) y la confirmación lo dice. Si la regla de la app no
  permite esa lectura (por ejemplo «solo los cancelados no bloquean»), el
  bot pide otra hora en vez de prometer algo que la base va a rechazar.
- **La base es la red**: dos escrituras al mismo tiempo sobre el mismo hueco
  → entra una sola; la otra recibe un rechazo que nombra con cuál chocó. No
  hay ventana de carrera; esto lo garantiza PostgreSQL, no la app.
- **Las horas**: «a las 4» es 16:00; «a las 10» es 10:00; «de la tarde» o
  «pm» suma doce; «a las 10» sin fin dura lo que la app declaró por defecto
  (una hora). La confirmación imprime siempre la hora resuelta — si el bot
  leyó mal, usted lo ve antes de decir que sí.
- **Preguntas que entiende**: «qué tengo mañana / el jueves / la semana que
  viene», «tengo algo mañana a las 4», «cuándo estoy libre el jueves»,
  «cuántos compromisos tengo mañana».
- **El recordatorio**: si el schema declara «avísame 15 minutos antes de
  cada compromiso», el worker manda un mensaje al chat del bot 15 minutos
  antes de cada uno — una sola vez, aunque el worker se reinicie en ese
  momento; si usted mueve el compromiso, el aviso se mueve; si lo cancela,
  no llega. Se ve en `/admin` → Automatización (`time:eventos.inicio -15m`).

Detalle técnico: docs/PRODUCTION.md §4.6g; ejemplo de schema
`examples/model-lab/agenda-choques.json`.

### 3g. La agenda del dueño — la primera app REAL sobre todo lo anterior (APP-AGENDA-S1)

> **APP-AGENDA-S2 (2026-09-22):** el parte de las 7 abre con **«📅 Hoy en
> agenda»** — cada compromiso del día con su hora — cuando el recurso declara
> `ranges`; «**tengo que** comprar pintura», «**hay que** llamar al banco»,
> «**acuérdate de** pagar la luz», «**recordame** renovar el seguro» anotan una
> tarea sin el modelo (con confirmación, como todo lo que escribe); y el canal
> de Telegram actúa **como el dueño** (`APPXIMO_TELEGRAM_SUMMARY_USER_ID`),
> así que «anota…» por el chat escribe a su nombre y «qué tengo hoy» ve sus
> filas. El bot de las demos pasó a la agenda: la tiendita y petfriendly ya no
> mandan partes ni contestan comandos — solo alertas de incidente.

Una agenda personal (tareas, compromisos, registros, personas, áreas,
etiquetas — nunca «evento») instalada como TERCERA app en una caja que ya
sirve dos demos, con su propia base, su propio worker y su propia copia
fuera de la caja. Lo que ese caso enseña vale para cualquier app real:

- **Dónde vive.** `install.sh --app=<nombre> --port=<libre> --control-port=<libre>
  --harden --worker-binary=…` sobre la caja de las demos: unidad, usuario,
  base, `/etc/<app>`, `/var/lib/<app>`, sitio Caddy y timer de backup
  propios. El reset nocturno de una demo restaura SOLO la base de esa demo;
  el deploy de una demo reinicia SOLO su unidad. Lo único compartido es
  Caddy (el instalador migra el Caddyfile inline a `import sites/*.caddy`
  preservando lo que había; verifique las otras apps desde afuera antes y
  después) y PostgreSQL (un OOM de una app se lleva el de todas: swap
  obligatorio). Cuando la app sea algo que no se puede perder ni una hora,
  el escalón es un droplet propio con la misma receta.
- **Antes de dormir: la copia fuera de la caja, probada.** `BACKUP_COPY_TO`
  a otra caja por scp con una llave que SOLO puede subir
  (`from="<ip>",restrict,command=<script que acepta únicamente scp -t <dir>>`
  — ojo: scp manda `-d -t` cuando copia varios archivos, el comando forzado
  debe admitirlo), `BACKUP_PASSPHRASE_FILE` para que los secretos viajen
  cifrados, y DOS simulacros: `appximo drill restore --app=<app>` en la
  caja, y el set copiado restaurado en OTRA máquina (un `postgres:18`
  efímero alcanza — el cliente tiene que ser PostgreSQL 18 o más, un `pg_restore`
  16 no lee el dump). Escriba el dominio de falla: dos cajas del mismo
  proveedor en regiones distintas cubren el droplet muerto, no la cuenta.
- **El schema declara solo los `events` que un workflow consume.** Un
  `events: [create]` sin consumidor deja una fila `pending` en el outbox
  para siempre y la alerta de outbox varado suena cada 15 minutos,
  eternamente. (Los recordatorios por fila —trigger `time`— NO necesitan
  `events`.)
- **Qué funciona solo, y pocas veces:** el parte de las 7 y el de las 19
  (lunes a viernes) por cron; «15 min antes» por fila; un aviso al anotar
  algo urgente; el espejo estimado-vs-real al cerrar una tarea (un hook
  rehúsa marcarla hecha sin decir cuánto tomó — por voz: «marca como hecha
  la tarea del techo, tomó 90 minutos»). Nada por hora.
- **Su propio bot.** Un bot de Telegram = un consumidor de `getUpdates`: la
  app real NO comparte el bot de las demos. `@BotFather` → token; `/start`
  en el chat → `chat_id` en `getUpdates`; las cinco líneas
  (`APPXIMO_TELEGRAM_BOT_TOKEN`, `_CHAT_ID`, `APPXIMO_ALERT_APP_NAME`,
  `APPXIMO_TELEGRAM_SUMMARY_TENANT`, `_ROLE`) en `/etc/<app>/<app>.env` y
  `systemctl restart <app> <app>-worker`. Hasta entonces los partes y avisos
  quedan `pending` en `GET /admin/outbox` — nada se pierde, nada sale.
- **Siri con un token acotado:** `appximo token --tenant <t> --role <rol del
  dueño> --user-id <su id> --ttl 365d --paths /api/ask,/api/summary --id
  <nombre>` — nunca admin; se revoca con `APPXIMO_JWT_REVOKED=<nombre>` sin
  rotar el secreto. El atajo de §3d con la URL y el token de la app: un solo
  atajo pregunta, anota, agenda y confirma (el «sí» es un `pending` de 5
  minutos por usuario).
- **Las provocaciones en un laboratorio, nunca en la base real:** el mismo
  schema en un tenant de prueba con un usuario B (el choque es por dueño; B
  no ve ni hereda las filas de A), el aviso 15 min con reinicio del worker
  en la ventana (sale UNA vez), el parte encolado a mano como lo haría el
  cron, y al final `tenant_<app>` con solo lo que el dueño configuró.

### 3h. «Cómo creo algo» — el asistente le enseña a usarlo (AGENDA-ASISTENTE-S1)

El bot (y Siri) le enseña a hablarle, con SUS recursos, SUS campos y SUS
filas — nada escrito a mano: si mañana declara un alias o un recurso, la
guía cambia sola. Siempre **US$ 0** y siempre en partes cortas (seis
ejemplos por página, en la pantalla y en la voz por igual; «más» sigue).

```
usted: ayuda
bot:   Agenda tiene: compromisos, tareas, registros, personas, areas, etiquetas.
       Puedes preguntar («cuántas tareas hay»), crear («crear tarea: …»), cambiar («marca como … la …») y pedir el resumen del día.
       Para aprender: cómo creo algo · qué puedo preguntar · qué campos tiene una tarea · cómo filtro por fecha. Y más para seguir cualquiera.

usted: cómo creo algo
bot:   ✍️ Para crear una tarea di: «crear tarea: [qué], [30 minutos], [urgente], [mañana / el viernes]».
       Por ejemplo: «crear tarea: revisar el contrato, 30 minutos, urgente, el viernes» — la entiendo al instante, gratis.
       Los datos van en cualquier orden, separados por comas o pausas; el que no digas, lo pregunto o lo dejo vacío.
       Antes de escribir te muestro todo y espero tu sí.
       Hay más (1 de 4). Di más para seguir.          ← después: el compromiso, el registro, la persona
```

Cada ejemplo que la guía promete como gratis **se prueba en el motor antes de
mostrarse** (y también dicho como lo dictaría Siri, sin los dos puntos); lo
que el motor no resuelve no se promete. Los otros niveles: «qué puedo
preguntar» (las formas de pregunta sobre su schema), «qué campos tiene una
tarea» (cada campo en palabras, con sus valores y los alias que usted
declaró), «cómo filtro por fecha» (períodos, rangos, combinaciones, «los
últimos 3…»), y «cómo creo una tarea» / «cómo agendo un compromiso» para el
detalle de uno (con «más»: las otras formas gratis — «tengo que…», «anota …
mañana», «acuérdate de…» — y qué dato puede llevar).

**La forma fija para crear: `crear <cosa>: <qué>, <los datos en cualquier
orden>`.** Un verbo (`crear`, `nueva`, `anota`, `agrega`, `registra`…) o la
palabra de la cosa, después QUÉ es, después los datos como salgan, con o sin
la palabra del campo, separados por comas, por «y» o por la pausa que deja el
dictado. El motor reconoce cada dato por su FORMA, no por una palabra suya:
la palabra de un campo («área casa»), un valor o alias declarado, una bandera
por su nombre («urgente»), un día o una hora («el viernes», «mañana a las 3»,
«de 12 a 1», «a las 4 pm», «a las cuatro de la tarde», «9 y media»), un
número con unidad («30 minutos»), un nombre después de «con» / «para», o un
nombre suelto que se prueba contra todo lo que podría ser. Lo que no es un
dato es el título, tal como lo dijo. Tres primos: «anota que <lo que pasó>»
es un registro; «anota <hacer algo>…» / «tengo que…» es una tarea; «reunión
con Fabián mañana a las 3 por una hora» es un compromiso. **La confirmación
sigue igual**: lo que se ahorra es la llamada al modelo, nunca el control.
Lo que la forma no entiende sigue yendo al modelo, como antes.

```
usted: crear tarea: pagar el seguro del carro, 45 minutos, urgente, el lunes
bot:   📝 Voy a crear tarea:
       • duracion estimada min: 45   • titulo: pagar el seguro del carro   • urgente: sí   • vence en: el lunes (lun 28 sep)
       ¿Confirmas? (sí / no)                                                        ← parser, US$ 0, 1 ms
usted: no, mejor el viernes
bot:   Cambié vence en.  📝 Voy a crear tarea: … • vence en: el viernes (vie 25 sep) … ¿Confirmas?   ← la corrección re-pregunta, nunca ejecuta
usted: agenda almuerzo con Marta el jueves de 12 a 1
bot:   📝 Voy a crear compromiso: • inicio: el jueves a las 12:00 • fin: 13:00 • persona: Marta Ruiz • titulo: almuerzo ¿Confirmas?
usted: agenda otra reunión mañana a las 10 y media
bot:   ⚠️ Ya tienes «P1 dentista» de 10:00 a 11:00. … ¿Igual lo agendo?
```

**Otras cosas que ahora entiende sin modelo:** «resumen», «estado» y «gasto»
dichos a Siri (los mismos que el bot); «ya hice…», «terminé de…», «… está
lista» (la tarea pasa a hecha); «pon en curso la declaración de renta» (el
estado dicho ubica el recurso); un nombre en una tarea con área Y persona
(«tareas de trabajo», «anota llamar a Fabián») se prueba contra las dos;
«Gomes» se entiende como Gómez y se dice; «Fabi» con Fabián y Fabiana
pregunta cuál; «la semana que viene»; «antes del viernes» (vence el viernes).

**Cómo se dice un registro (algo que pasó).** La forma es **«anota que»
(o «registra que», «apunta que») + qué pasó + cuándo**, en cualquier orden.
Lo que no es hora ni día queda como texto del registro, tal como lo dijiste.
Todas estas las entiende el motor solo, al instante y sin costo, y siempre
te muestra la confirmación antes de escribir:

```
anota que la plataforma estuvo caída de 7 a 2 de la tarde        → hoy 07:00–14:00
registra que fui al médico de 9 a 10                             → hoy 09:00–10:00
anota que hablé con el banco a las 3                             → hoy 15:00 (una hora por defecto)
anota que se cayó la luz a las 3 y media por dos horas           → hoy 15:30–17:30
anota que ayer hablé con el contador a las 5                     → ayer 17:00
anota que anoche se fue la luz a las 10                          → ayer 22:00
anota que esta mañana fui al gimnasio de 6 a 7                   → hoy 06:00–07:00 (la mañana manda)
anota que el martes hablé con Fabián a las 3                     → el martes PASADO 15:00 (un registro mira atrás), persona Fabián
anota que estudio estuvo caído toda la mañana                    → hoy, sin hora
anota que estuve en el banco desde las 9 hasta las 10 y media    → hoy 09:00–10:30
anota que el estudio estuvo caído entre las 7 y las 2 de la tarde → hoy 07:00–14:00
anota que se cayó la plataforma a las 7 y volvió a las 2         → hoy 07:00–14:00 (la segunda hora es el fin)
anota que llamé al banco tipo 3 / como a las 3 / a eso de las 3  → hoy 15:00
anota que hablé con el banco, área trabajo, a las 3              → con el área
registra: la plataforma estuvo caída de 7 a 2                    → los dos puntos valen por el «que»
```

Y para leerlos: «qué registré hoy», «qué anoté ayer», «registros de esta
semana», «registros de trabajo», «registros con Fabián». Lo que todavía va
al modelo: una duración sin hora («estuve dos horas en el banco» queda como
texto, la hora es la de ahora) y frases muy enredadas.

**Ojo con el nombre del atajo.** Si el atajo de Siri se llama «Anota» o
«Registra», Siri se come esa palabra y al motor le llega la frase sin el
verbo: «que estudio estuvo caído de siete a dos de la tarde». El motor lo
tolera desde el 2026-09-24: una frase que empieza con «que», no nombra
ningún recurso, no tiene palabra de pregunta y trae una hora — o un verbo en
pasado con un día («que ayer se fue el agua toda la tarde») — se lee como
«anota que…» (un registro, con su confirmación). Igual, si el atajo se llama
de otra forma («Agenda», «Mi agenda») la frase viaja entera y no hay
adivinanza. El reloj también entiende lo que escribe el dictado: «siete
A.M.», «2pm», «de siete a dos de la tarde», «el día de hoy».

**La voz.** Lo que Siri lee (`display` con la traza, `speech` sin ella) está
compuesto para escucharse: frases cortas, horas y fechas y cantidades en
palabras («de las diez a las once de la mañana», «el martes veintinueve de
septiembre», «dos tareas»), sin viñetas, comillas, símbolos ni dígitos; una
lista lee como mucho cinco y dice «y N más; mira el panel»; una elección se
lee «Uno, Fabián Gómez. Dos, Fabiana Torres.». Si Siri lo lee demasiado
rápido, baje la velocidad de la acción *Leer texto* del atajo: eso no es del
motor. La regla de «a las 4» = de la tarde se mantiene (en el banco de frases
ninguna hora suelta significaba otra cosa); la confirmación ahora DICE «a las
cuatro de la tarde», así que si alguna vez se equivoca lo oye antes de
confirmar y lo corrige con «mejor a las 4 de la mañana».

## 4. Qué hacer cuando pasa algo

Recetas cortas, en el orden en que suele hacer falta. Todas empiezan igual: **mire antes de tocar** (30 segundos):

```bash
systemctl status <app> postgresql caddy --no-pager | head -30   # ¿qué está caído?
journalctl -u <app> -n 40 --no-pager -o cat                      # ¿qué dijo el motor al final?
curl -s http://127.0.0.1:<puerto>/readyz; echo                   # ¿está listo? (503 = drenando o caído)
```

`<app>` es el nombre con el que se instaló (`appximo` si no se dio `--app`); `<puerto>` es el interno (`8090` por defecto; `systemctl show -p ExecStart --value <app>` lo muestra).

### 4.1 La app está lenta

1. `/admin` → **Recursos** → En vivo. **Lea el veredicto** — es la respuesta a «¿es la app, la base o la caja?»:
   - `CPU saturada` → la caja es chica para lo que le piden, o algo ajeno come CPU (`top`). En una caja compartida puede ser un vecino ruidoso: el veredicto no lo distingue (§7).
   - `Pool agotado` → las consultas retienen las 10 conexiones: mire **Observabilidad → Trazas** ordenadas por duración; casi siempre falta un índice (declárelo en el schema, `indexes`) o una lista trae columnas pesadas (`?fields=`).
   - `Limitado por la base` → la base o la red hacia ella está lenta; `?search=` sin índice trigram cuesta cientos de ms de CPU en PostgreSQL por request ([BACKLOG SCHEMA-9](BACKLOG.md)).
   - `Presión de memoria` / `CPU limitada (quota)` → la caja o el plan; ver 4.4 y hable con el proveedor.
2. `Server-Timing` en la request lenta: `curl -sI -H 'Host: <tenant>.<dominio>' -H "Authorization: Bearer $TOKEN" 'https://…/api/<recurso>?per_page=20' | grep -i server-timing` — si `query` es casi todo, es la base.
3. ¿Hay `429`? Si el body dice `rate limit exceeded` es el **límite por tenant**; si dice `server at capacity` es la **admisión** (la caja está en el techo). Confirme con `appximo drill saturate` (§6) y lea §3 antes de subir un límite.
4. **Tiempo esperado**: diagnosticar, minutos. Un índice nuevo entra en caliente por Studio o `appximo migrate` en segundos (medido: 21 → 4 ms, −79 %, [BENCHMARKS §7](BENCHMARKS.md)).

### 4.2 La app da 500

1. `/admin` → tenant → **Observabilidad → Problemas**: la fila dice el endpoint, el mensaje real y a cuántos usuarios les pasa. Clic en la traza: **la sentencia que falló** y la cascada.
2. Si no aparece nada ahí: `journalctl -u <app> -o cat --since -30min | grep '"level":"error"' | tail -5`.
3. Un 500 que solo aparece con `Host` incorrecto o sin token desde un script suele ser un `400`/`401` mal leído — el body lo dice.
4. Causas típicas: un trigger o constraint puesto a mano en la base (`SQLSTATE P0001`/`23…` en el mensaje), la base caída (`database unavailable`, ver 4.5), el disco lleno (4.4).
5. **Tiempo esperado**: el 500 está explicado en el panel **segundos** después de ocurrir (la traza se escribe al terminar la request). Para verlo funcionar: `appximo drill error --app=<app>` (§6).

### 4.3 La base se corrompió

Señales: el backup nocturno **falló nombrando la tabla** (`cat /var/backups/<app>/last-backup.status` → `failed … cause=…`), o `pg_amcheck` nombró un índice, o lecturas con `invalid page in block N`.

1. Si es **un índice** (`cause=amcheck: btree index "…"`): `sudo -u postgres psql -d <db> -c 'REINDEX INDEX <schema>.<índice>'` y repita el backup: `sudo bash /opt/<app>/scripts/backup.sh --app=<app>`. Tiempo: segundos.
2. Si es **una tabla**: restaure el último set bueno (el anterior al fallo — los sets se llaman `<app>-<fecha>-<hora>`):

   ```bash
   ls -lt /var/backups/<app>/ | head                                   # el más nuevo primero; elija el de ANTES del daño
   sudo bash /opt/<app>/scripts/restore.sh --app=<app> --set=/var/backups/<app>/<app>-<stamp>
   ```

   El script **para la app, restaura secretos + base + archivos, arranca y verifica** conteo por tabla contra el manifiesto, e imprime cada etapa con su tiempo. **Tiempo medido: 13,6 s** para 251 k filas / 124 MB en 2 vCPU (RESILIENCIA-S1). Termina en `RESTORE VERIFIED`; cualquier otra cosa dice qué no cuadró y deja la app **parada** — lea el mensaje, no adivine.
3. Antes de tener que hacerlo de verdad, ensáyelo sin parar nada: `appximo drill restore --app=<app>` (§6) — restaura el set más nuevo en una base de prueba al lado y verifica los conteos (**7,5 s** en la caja del laboratorio).

### 4.4 El disco se llenó

Señales: la tarjeta **Disco** en `BAJO` (Inicio o Recursos), la alerta `disk low`, o PostgreSQL en el journal con `PANIC … No space left on device` seguido de `503`.

1. Libere: sets viejos (`ls -lt /var/backups/<app>`; `BACKUP_KEEP` los rota solo), `journalctl --vacuum-size=200M`, `apt-get clean`, `docker system prune` si hay Docker.
2. Si PostgreSQL entró en pánico: al liberar espacio se reinicia solo (`Restart=on-failure`, 5 s); si no, `systemctl start postgresql@<versión>-main`.
3. **Agregue swap si no hay** (`swapon --show` vacío): en una caja ≤ 2 GB sin swap una carga masiva mata a PostgreSQL por OOM (medido en el campo con 5 apps y 957 MiB):

   ```bash
   fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
   echo '/swapfile none swap sw 0 0' >> /etc/fstab && sysctl -w vm.swappiness=10
   ```

4. Para ver el aviso funcionar sin llenar nada de verdad: `appximo drill chaos 4 --app=<app>` (llena hasta cruzar el piso y limpia solo).
5. **Tiempo esperado**: la alerta llega en el siguiente tick del colector (≤ 10 s) al cruzar el piso; la recuperación tras liberar espacio es automática en segundos.

### 4.5 La base no responde (PostgreSQL caído o inalcanzable)

Señales: `503 database unavailable` rápidos, veredicto `Limitado por la base`, `systemctl status postgresql@…` no activo.

- El motor **no se cae**: el breaker abre tras 20 fallos seguidos y responde `503` en menos de 0,2 s en vez de esperar 5 s por request (ENG-59; medido en el laboratorio: p50 de las fallas 0,01 s, 81 % < 200 ms, recuperación +0,1 s al volver la base).
- PostgreSQL **se reinicia solo** tras un crash (el instalador escribe `Restart=on-failure`): medido 11 s de corte total con `kill -9` al postmaster bajo carga, 0 reinicios del motor.
- Si no vuelve: `journalctl -u postgresql@<versión>-main -n 50`. Un disco lleno (4.4) o una `postgresql.conf` mal editada son las causas de siempre.
- Repetirlo: `appximo drill chaos 2` (mata PostgreSQL) y `chaos 6` (corta la red hacia la base 25 s).

### 4.6 Murió el host (la caja no responde)

Nada automático: **una app es una caja** (§7). El plan es reconstruir en una caja nueva con el último set fuera de la caja (por eso `BACKUP_COPY_TO` y `BACKUP_PASSPHRASE_FILE` no son opcionales). El runbook literal, probado dos veces siguiéndolo al pie de la letra, es [PRODUCTION.md §4.3 escenario B](PRODUCTION.md#43-the-3-am-runbook): instalar vacío con el mismo dominio y nombre de app, traer el set y la frase, `restore.sh` con `BACKUP_PASSPHRASE_FILE`, mover el DNS.

**Tiempo medido: ≈ 4 minutos** (droplet 71 s + `install.sh` 150 s + copiar el set 2 s + restaurar 12 s) **más lo que tarde el DNS** — deje el TTL del registro A en 300 s desde hoy. Se pierde lo escrito después del último set (RPO = la cadencia del timer: 24 h por defecto, 1 h con `--backup-schedule=hourly`).

### 4.7 Hay que restaurar (sin que nada esté roto)

- Ensayo, la app sigue arriba: `appximo drill restore --app=<app>` → `REHEARSAL VERIFIED` con los tiempos.
- De verdad: el comando de 4.3 paso 2. Lo que vuelve: datos, usuarios y contraseñas, MFA, tokens (los secretos viajan en el set). Lo que no vuelve: lo escrito después del set, y `obs.db` (las trazas).
- Un tenant solo (sin tocar los demás): hoy es `pg_restore --schema=tenant_<id>` a mano ([BACKLOG OPS-43](BACKLOG.md)).

### 4.8 Otros que muerden

| Señal | Qué es | Qué hacer |
|---|---|---|
| `502` de Caddy | El motor no está o el puerto no coincide | `systemctl status <app>`; `journalctl -u <app> -n 50` (un schema inválido falla el arranque ahí) |
| El certificado no sale | El DNS no apunta a la caja o el puerto 80 está cerrado | `dig +short <dominio>`; `journalctl -u caddy -f` |
| `429` al hacer login | 5 intentos por minuto por (tenant, correo) | Esperar un minuto; en una demo con cuenta compartida, `APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE=60` (§3) |
| `503 host memory pressure` en escrituras | La guardia de memoria (§3) | Swap (4.4); lotes más chicos; `free -m`, `dmesg \| grep -i oom` |
| `resource_not_loaded` en un recurso nuevo | Un recurso nuevo en el schema exige **reiniciar** el motor (una columna nueva no) | `systemctl restart <app>` o el botón de Studio |
| El servicio queda en `activating (auto-restart)` sin error claro | `/etc/<app>` con permisos `0750` (umask) | `chmod 0755 /etc/<app>` |
| `401 token tenant mismatch` | El `Host` no es el subdominio del tenant del token | `curl -H 'Host: <tenant>.<dominio>' …` |
| El reloj de la caja se movió | Los tokens siguen valiendo (`exp` es lo único que se mira) | Nada que hacer; `appximo drill chaos 8` lo demuestra |
| Alerta «outbox: the oldest pending event is …» | Hay eventos encolados que nadie drena — el worker no corre, o corre sin consumidor para ese tema | `systemctl status <app>-worker`; `GET /admin/outbox` dice el tema; el log del worker nombra los temas ajenos una vez por minuto |
| Alerta «outbox: N event(s) parked state='failed'» | Un evento agotó sus reintentos; **el porqué está en la fila** | `GET /admin/outbox` → `failed[].last_error`; arreglar la causa y re-armar: `UPDATE public.outbox SET state='pending', attempts=0 WHERE id=<id>` |
| `appximo_workflow_overdue_seconds` crece | Ningún scheduler dispara los cron — el worker está caído o ninguno tiene el liderazgo | `systemctl restart <app>-worker`; `journalctl -u <app>-worker` debe decir «cron leadership acquired» |
| `503 … circuit breaker open` en escrituras | La base no estaba sirviendo; el disyuntor corta 8 s y se re-prueba solo | `appximo_breaker_state` en `/metrics` y el log «circuit breaker state change»; si persiste, la base: `systemctl status postgresql` |

---

## 5. Cómo desplegar y cómo poner al día una caja vieja

### 5.1 Instalar en una caja vacía

Desde su máquina, con el binario ya construido (`./scripts/build-engine.sh /tmp/appximo "$(git rev-parse --short HEAD)" "$(git rev-parse HEAD)"`):

```bash
scp /tmp/appximo scripts/install.sh scripts/backup.sh scripts/restore.sh scripts/deploy-update.sh scripts/fleet-audit.sh scripts/drill.sh root@CAJA:/root/
ssh root@CAJA 'bash /root/install.sh --domain=app.ejemplo.com --email=usted@ejemplo.com --binary=/root/appximo --harden'
```

Deja: la unidad systemd (`RestartSec=2`, nunca se rinde), Caddy con HTTPS automático, PostgreSQL nativo con checksums, el timer de backup nocturno (03:30), los scripts compañeros en `/opt/<app>/scripts/`, y **verifica que lo instalado es lo pedido** (sha256 del binario, `/health` local y por Caddy, el schema). Con `--app=NOMBRE` conviven varias apps en la caja. Detalle y flags: [PRODUCTION.md §1–2](PRODUCTION.md). El laboratorio de esta sesión se instaló exactamente así (`tools/lab`, ver [BENCHMARKS §4e](BENCHMARKS.md)).

Después: registrar el tenant (**el id debe ser el primer label del dominio**: `app` para `app.ejemplo.com`) y crear el primer super-admin desde `/admin`:

```bash
curl -s -X POST http://127.0.0.1:9090/tenants -H "X-Admin-Key: $ADMIN_KEY" -H 'Content-Type: application/json' \
  -d "{\"tenant_id\":\"app\",\"display_name\":\"Mi app\",\"schema\":$(cat /etc/<app>/schema.json)}"
```

### 5.2 Desplegar una versión nueva (el comando único)

Desde su máquina:

```bash
scripts/deploy-app.sh --host=root@CAJA --app=<app> --binary=/tmp/appximo --url=https://app.ejemplo.com
```

Qué hace, en orden: exige que el binario responda `version`; inventaría la app desde systemd (nunca adivina puertos); **hace un backup completo primero** (si falla, aborta sin tocar nada); cambia el binario (atómico, reinicio, salud cada 250 ms); **verifica desde afuera por HTTPS** — la versión por el proxy, una lectura autenticada, y una escritura que se deshace sola (`POST /api/transaction` borrando un id inexistente: pasa por auth, RBAC, transacción y rollback sin cambiar una fila); si algo falla, **vuelve al binario anterior y lo re-verifica**; al final corre el audit. Salidas: `0` verificado · `1` revertido y re-verificado · `2` el rollback no recuperó (humano ahora) · `3` desplegado pero el audit encontró huecos.

Medido en el laboratorio (DEPLOY-FLOTA-S1): binario bueno **17 s**; un binario envenenado que responde `/health` pero da 500 en `/api/*` fue cazado a los **15 s** y revertido a los **23 s**. «Un `/health` 200 no es un deploy verificado.» Hay ~0,3–0,6 s de `502` en cada cambio de binario (no es cero-downtime, [BACKLOG ENG-2](BACKLOG.md)).

En la caja, sin el orquestador: `sudo bash /opt/<app>/scripts/deploy-update.sh --binary=/tmp/appximo` (backup del binario, swap, salud, rollback automático).

Dos detalles que muerden: la verificación desde afuera manda `Host: <tenant>.<dominio>` (el `--url` o `--tenant-host`), y ese host **tiene que existir en Caddy** — con un host que Caddy no sirve, responde `200` con cuerpo vacío y el script lo reporta como fallo y revierte (le pasó a esta sesión en el laboratorio hasta agregar el sitio `lab.applab-target-basic.internal`). Y el binario tiene que responder `appximo version` con la versión que `/health` va a mostrar (contrato ADR-023): un `go build` pelado dice `dev` y el script exige que coincida — construya con `scripts/build-engine.sh`.

### 5.3 Poner al día una caja instalada con un instalador viejo

1. `appximo drill audit --app=<app>` (o `sudo bash /opt/<app>/scripts/fleet-audit.sh`) dice **qué falta**: timer, scripts compañeros, `APPXIMO_BACKUP_DIR`, política de reinicio de PostgreSQL, checksums, swap, off-box, alertas.
2. Copias de seguridad primero (`backup.sh` si existe; si no, un `pg_dump -Fc`), y copie la unidad y el env a `/root/*.pre-upgrade`.
3. Vuelva a correr el instalador **con el mismo binario, nombre, dominio y puertos**:

   ```bash
   sudo bash install.sh --app=<app> --domain=<su dominio> --email=<correo> --binary=/opt/<app>/bin/<binario> --port=<puerto> --control-port=<control> --yes
   ```

   Conserva secretos, base, datos y las líneas del env que usted agregó; reemplaza binario, unidad, sitio de Caddy y scripts. Se detiene si el schema que encuentra es de otra app.
4. `drill audit` de nuevo: debe quedar todo ✓ salvo lo que solo usted puede poner (`BACKUP_COPY_TO`, `SLACK_WEBHOOK_URL`). Rollback: restaurar las dos copias `.pre-upgrade` y `systemctl daemon-reload && systemctl restart <app>`.

Probado en el laboratorio degradado a propósito a la forma vieja (CAOS-S1) y aplicado quirúrgicamente a las dos apps de la caja de demos. Detalle: [PRODUCTION.md §4.5b](PRODUCTION.md).

---

## 6. Repetir cualquier escenario: `appximo drill`

Un solo comando, con subcomandos, que **provoca** un escenario y le dice **qué va a pasar y dónde mirarlo** antes de hacerlo. Corre **en la caja** (`ssh` y `appximo drill … --app=<app>`; en una app de consumidor el CLI del motor está en `/opt/<app>/bin/appximo-cli`) y lee la configuración real de la app desde `/etc/<app>/<app>.env` y la unidad de systemd. Las explicaciones salen en español si el `LANG` de la terminal es `es_*`; `--lang es|en` fuerza.

```
$ appximo drill list --lang es
appximo drill — los escenarios que puede repetir, y dónde se ve cada uno:

  error      un 500 real que se explica solo
             seguridad: crea su propio tenant y lo borra — permitido en cualquier caja
             dónde:     /admin → elija el tenant (arriba a la derecha) → Observabilidad → Problemas → «Problemas (24 h)»: una fila, 2 eventos, 1 usuario.

  load       carga sostenida, y el veredicto del propio motor
             seguridad: carga real sobre un tenant real — se niega en producción salvo --production
             dónde:     /admin → Recursos → En vivo: el veredicto y las tarjetas de requests/pool/CPU se mueven mientras esto corre …
  saturate   pasado el techo: quién recorta, y cómo
  probe      una sonda desde afuera con resumen de corte
  chaos      uno de los diez experimentos de CAOS-S1, en esta caja
  restore    un simulacro de restauración cronometrado
  audit      qué FALTA en esta caja
  ask        una pregunta por voz: quién la respondió y qué costó
  voice      el canal de voz de punta a punta, en un tenant efímero
  spend      la tarjeta de gasto del modelo de un tenant
  …
```

**Seguro por construcción.** Un drill que carga, rompe o restaura de verdad **se niega** si el destino parece producción — un dominio público en la configuración de Caddy, o una dirección en el cinturón de IPs protegidas del laboratorio (`/root/.applab-protected`) — salvo que se pase `--production` explícitamente. `error` trabaja sobre un tenant efímero que crea y borra él mismo (el mismo camino que `appximo tenant delete`: schema y filas de control, sin huérfanos). `audit`, `list` y `probe` no cambian nada. Los experimentos de caja (`chaos`) **restauran lo que rompen al salir, también con Ctrl-C** (regla de iptables, retardo de red, reloj, archivo de relleno, lock).

### 6.1 `drill error` — un 500 real, explicado

```
$ appximo drill error --app=appximo --lang es
▶ appximo drill error — un 500 real que se explica solo
  Qué va a provocar:   crea un tenant EFÍMERO con el schema de esta app, rompe una tabla por debajo del motor (un trigger BEFORE INSERT que hace RAISE, o la tabla borrada) y manda dos requests que la tocan.
  Qué debería pasar:   los dos responden HTTP 500 con un X-Trace-ID; la traza lleva el mensaje, la sentencia que falló, el usuario y el rol; las dos ocurrencias se agrupan en UN problema; la primera dispara una alerta …
  Dónde mirarlo:       /admin → elija el tenant (arriba a la derecha) → Observabilidad → Problemas → «Problemas (24 h)»: una fila, 2 eventos, 1 usuario.
                       → Trazas → el 500 → Cascada: la etapa que falló marcada ✗, «Sentencia que falló», «Pila», «Copiar como curl».
                       journal: journalctl -u <unidad> -o cat | grep '"level":"error"'

• ephemeral tenant drill055813 (schema: /etc/appximo/schema.json, role: dueno)
• BEFORE INSERT trigger on tenant_drill055813.categorias RAISEs — the driver will reject every insert (SQLSTATE P0001)
✓ POST /api/categorias → 500  trace 0dd73f3dc5c9946e  body {"error":"internal error"}
✓ POST /api/categorias → 500  trace 353b5c32ed03120d  body {"error":"internal error"}
• reading /admin/observability/tenants/drill055813 (what the panel's Issues tab shows)…
✓ problem group: route=/api/categorias status=500 count=2 users=1
    message: ERROR: provoked by appximo drill error: storage says no (SQLSTATE P0001)
✓ 5xx trace 353b5c32ed03120d: route=/api/categorias user=00000000-0000-4000-8000-00000000d111 role=dueno

Mírelo ahora:
  http://127.0.0.1:8090/admin → tenant «drill055813» (arriba a la derecha) → Observabilidad → Problemas
  → Trazas → 0dd73f3dc5c9946e / 353b5c32ed03120d → Cascada
✓ tenant drill055813 deleted (schema + control-plane rows; schemas left: 0)
```

En una terminal interactiva el tenant se queda hasta que usted pulse Enter (para ir a mirar el panel); `--yes` lo borra de inmediato; `--keep` lo deja e imprime el comando para borrarlo.

### 6.2 `drill load` y `drill saturate` — carga y saturación con el veredicto en vivo

`load` manda una tasa fija de lecturas sin caché (`--tenant`, `--rate` 100, `--duration` 30s) y lee el auto-monitor cada segundo; `saturate` sube una escalera (`--rates 200,400,800,1600,3200`, 10 s por nivel) y se detiene en el primer nivel que recorta, diciendo **quién**: el limitador por tenant (`429 rate limit exceeded`), la admisión (`429 server at capacity`) o el breaker/guardia (`503`). Mire **Recursos → Prueba de carga** mientras corren.

```
$ appximo drill saturate --app=appximo --tenant=lab --resource=categorias --rates 400,800,1600,3200 --step 8s
   level 400 rps: 3163 sent, 3163 ok (100%), 429 limiter=0 admission=0, 503=0, err=0, p50 2.2 ms, p99 63.2 ms
   level 800 rps: 6176 sent, 6176 ok (100%), 429 limiter=0 admission=0, 503=0, err=0, p50 3.2 ms, p99 20.1 ms
   level 1600 rps: 9020 sent, 6105 ok (68%), 429 limiter=0 admission=2915, 503=0, err=0, p50 55.1 ms, p99 102.0 ms
→ shedding began at 1600 rps: the ADMISSION CONTROL (APPXIMO_MAX_INFLIGHT — the box's capacity)
window verdict: cpu_saturated (owner: appximo) over 22 ticks — peak 846 rps, peak p99 99.8 ms, shed 0, 5xx 0
```

(Caja del laboratorio, 2 vCPU compartidas, generador **en la misma caja** — por eso el techo aparece más bajo que el medido desde afuera en [BENCHMARKS §4e](BENCHMARKS.md), ~1 000 rps limpios. Para un número publicable, el generador va en otra máquina: `tools/lab`.) En una caja de 1 vCPU con el limitador por defecto, `saturate` topa primero con el limitador a 350 rps: eso también es una respuesta.

### 6.3 `drill probe` — la sonda desde afuera

Desde **otra** máquina, durante un reinicio o un deploy: `appximo drill probe --url https://app.ejemplo.com --path /healthz --duration 120s`. Al final resume: cuántas fallas, cuándo empezó y terminó el corte, qué tan rápido llegaron las fallas. Con el reinicio del laboratorio (`drill chaos 3 --yes-reboot`): `outage: 19.2 s (first failure → first success after the last failure); first 200 after the last failure: +0.10 s`.

### 6.4 `drill chaos <1-10>` — los diez experimentos, con hipótesis previa

Cada uno imprime `H:` (la hipótesis escrita antes de correr, la de CAOS-S1) y la evidencia después. Resultados de esta sesión en la caja del laboratorio (2 vCPU / 2 GB, 251 k filas):

| # | Experimento | Qué pasó (medido) |
|---|---|---|
| 1 | `kill -9` al motor bajo sonda | corte **2,6 s**, 24 fallas de 276, `NRestarts` 0 → 1, ninguna fila a medias |
| 2 | `kill -9` a PostgreSQL | el motor siguió arriba con 503 rápidos; PostgreSQL volvió solo; corte **11,0 s**; motor sin reinicios |
| 3 | reinicio de la caja (`--yes-reboot`, sonda desde el 105) | corte **19,2 s**; todo arriba solo, nada en `failed` |
| 4 | llenar el disco hasta el piso | alerta `disk low` en el siguiente tick; lecturas 200; el backup siguió cabiendo (con `--full` falla nombrando la causa); relleno borrado solo |
| 5 | memoria hasta el borde del OOM | a 34 MiB (< piso 39) las escrituras dieron **503** con el cuerpo explicando; lecturas 200; `dmesg` sin OOM; volvieron solas al soltar |
| 6 | red a la base en agujero negro 25 s (ENG-59) | 220 fallas, **p50 0,01 s, 81 % < 200 ms**, recuperación +0,11 s; solo el usuario de la app afectado |
| 7 | 200 ms de latencia hacia la base | degradó: veredicto **`pool_exhausted` (base)**, p99 1,5 s; volvió a 5 ms al quitar el retardo |
| 8 | reloj 2 h atrás (demonio de hora pausado) | token viejo 200, token nuevo 200, escritura ok, sin reinicio; reloj restaurado |
| 9 | dos PATCH concurrentes a la misma fila | los dos 200, la fila quedó con **uno** de los valores, nunca una mezcla; valor original restaurado |
| 10 | tabla bloqueada 20 s bajo 60 lectores | pool 10/10, **144 × 429** (admisión) + **276 × 503** (deadline 5 s), veredicto `pool_exhausted`; al soltar, 200 en 3 ms |

Banderas: `--tenant`, `--resource` (por defecto el primero del schema), `--full` (D4 al 100 %), `--yes-reboot` (D3). Las hipótesis originales, escritas antes de correr nada: `evidencia/CAOS-S1/d0-hipotesis.md` (repositorio interno).

### 6.5 `drill restore` — el simulacro cronometrado

```
$ appximo drill restore --app=appximo
• set: /var/backups/appximo/appximo-20260831-025323 (37.6 MB · 2026-08-31T02:53:31Z) · manifest present
  ⏱ create scratch db appximo_drill     0.3 s
  ⏱ pg_restore                            7.0 s
✓ row counts: 22 tables match the manifest exactly (251243 rows)
✓ every foreign key validated
  ⏱ verify                                0.3 s
  ⏱ TOTAL (create + load + verify)    7.5 s   — a REAL restore adds: stop the app (~5 s drain), restore /etc/appximo, files, start (~1 s)
REHEARSAL VERIFIED — the newest set restores and matches its manifest. The real command (stops the app):
  sudo bash /opt/appximo/scripts/restore.sh --app=appximo --set=/var/backups/appximo/appximo-20260831-025323
```

**Lo que encontró la primera vez que corrió**: todos los sets tomados desde que `backup.sh` verifica índices con `pg_amcheck` (DEPLOY-FLOTA-S1) **no se podían restaurar** con `restore.sh` — la extensión `amcheck` quedaba creada en la base, viajaba en el siguiente dump, y `pg_restore` como el rol de servicio fallaba en `CREATE EXTENSION`. Está arreglado en las dos puntas (`backup.sh` la quita tras verificar; `restore.sh` y el drill filtran esas entradas al restaurar sets viejos). Un simulacro que no se ejecuta es una promesa; este se ejecuta.

`--real` corre `restore.sh` de verdad (para la app, reemplaza la base); en producción exige `--production` y escribir el nombre de la app.

### 6.6 `drill audit` — qué falta en esta caja

`fleet-audit.sh` con leyenda: ✓ protegido, ✗ falta (la línea dice qué hacer), ! aviso. En el laboratorio recién instalado marcó, correctamente: sin swap, sin timer de backup, sin set, sin `SLACK_WEBHOOK_URL`, sin `BACKUP_COPY_TO`. Sale `1` si hay al menos un ✗ — úselo como gate en un script.

### 6.7 `drill ask` — una pregunta, y quién la respondió

Manda UNA pregunta a `POST /api/ask` como el tenant y el rol dados — exactamente lo que hacen el bot de Telegram y un atajo de Siri — e imprime la respuesta con su contabilidad: `kind` (answer / unclear / confirm / write_refused…), `source` (**parser** = US$ 0 y milisegundos, **cache**, **model** ≈ US$ 0,003 y ~1 s) y, cuando fue al modelo, la razón exacta por la que el parser pasó («verbo de escritura», «no conozco la palabra …»). Una orden de escritura responde la confirmación y **no escribí nada** (el drill nunca contesta el «sí»). Con `--token` no hace falta `JWT_SECRET`.

```
$ appximo drill ask --app=vetapp --tenant=vetapp --role=dueno --lang=es "cuántas mascotas hay"
• cuántas mascotas hay
  kind=answer source=parser cost=US$ 0.0000 total=1 ms
  12 mascotas
  ⚙︎ parser · 1 ms · US$ 0
  plan: {"kind":"count","resource":"pets"}
```

Si la palabra del dueño NO es la del schema y el drill dice `source=model … el parser pasó: no conozco …`, la respuesta es declarar el sinónimo en el schema (`aliases`, §3d) — no enseñarle al dueño la palabra del programador.

### 6.8 `drill voice` — el canal de voz de punta a punta, en un tenant que crea y borra

Registra un tenant EFÍMERO con el schema de la app (como `drill error`), siembra una fila y maneja `POST /api/ask` de principio a fin: un conteo con la palabra del schema o su alias (debe ser `parser`, US$ 0), un verbo de borrar (`write_refused`, sin llamar al modelo), un «sí pero…» suelto (`unclear`, US$ 0), una **transición de estado confirmada por id** — LA escritura que el parser resuelve solo — y la tarjeta de gasto. Cada paso imprime ✓/✗; al final borra el tenant (schema + filas de control, sin huérfanos; `--keep` para mirarlo en `/admin`). Un create por voz necesita el modelo y se salta sin `ANTHROPIC_API_KEY`.

```
$ appximo drill voice --app=vetapp --lang=es --yes
• ephemeral tenant drille69db5 (schema: /etc/vetapp/schema.json, role: dueno)
• seeded one personas (POST → 201)
✓ question («cuántos contactos hay») kind=answer         source=parser   cost=US$ 0.0000  1 persona
✓ delete verb («borrá…»)             kind=write_refused  source=parser   cost=US$ 0.0000  Eso no lo hago por voz
✓ stray «sí pero…»                   kind=unclear        source=parser   cost=US$ 0.0000  Eso no fue un sí
✓ spend card (GET /api/ask/spend → 200)
✓ 4 verificaciones pasaron; el parser resolvió la pregunta: true
✓ tenant drille69db5 deleted (schema + control-plane rows)
```

El ejemplo canónico de una app con todo el frente declarado — `aliases`, `events`, `pending`, un workflow por evento y uno cron que manda el resumen, `summary` — es [examples/model-lab/agenda-voz.json](../examples/model-lab/agenda-voz.json); `appximo explain --lang es` lo lee en prosa.

### 6.9 `drill spend` — cuánto cuestan las preguntas de este tenant

Lee `GET /api/ask/spend` como un rol admin e imprime la tarjeta: hoy, el mes, el techo y lo que falta, quién respondió cuántas (parser / caché / modelo), **gasto útil vs desperdiciado**, las frases que más cuestan y por qué fueron al modelo. Son los MISMOS números que el `gasto` de Telegram, `/admin/ask` y el centro de mando (una sola fuente, `pkg/askspend`); un rol acotado recibe 403 — el gasto de una plataforma es del administrador.

---

## 7. Qué NO hace el motor

Todo junto y sin adorno. Cada límite tiene su registro en [BACKLOG.md](BACKLOG.md) o su decisión escrita.

- **Una caja es una caja.** No hay clúster, ni réplica, ni failover. Si el host muere, la app está caída hasta que alguien reconstruye (≈ 4 min + DNS, §4.6). Escala vertical: decenas a bajos cientos de tenants por caja. Decisión del dueño (RESILIENCIA-S1).
- **El backup protege la base, no la caja.** Sin `BACKUP_COPY_TO` el set muere con el disco. Sin `BACKUP_PASSPHRASE_FILE` los secretos no salen. RPO = la cadencia del timer; no hay archivado de WAL (PITR, [OPS-41](BACKLOG.md)).
- **Los checksums no ven lo que no se lee.** PostgreSQL detecta una página corrupta solo al leerla; una lista que usa un índice puede seguir respondiendo 200 sobre una tabla dañada. El detector garantizado es el backup nocturno (lee todo) más `pg_amcheck` (índices). Medido y escrito en CAOS-S1.
- **Degradar no es aguantar.** La guardia de memoria y la admisión hacen que la falla sea visible (503/429) en vez de silenciosa; no le dan más capacidad a la caja. Un proceso ajeno que ignore la guardia puede igual disparar el OOM del kernel.
- **El veredicto no distingue un vecino ruidoso de la propia app** en una caja compartida (`CPU saturada` puede ser el vecino). No ve una llamada externa dentro de un handler custom, ni el disco de un PostgreSQL remoto, ni cajas sin cgroup v2/PSI. Una ruta custom no marca la etapa `query` ([ENG-51](BACKLOG.md)).
- **Todo techo es una estimación hasta contrastarlo con tráfico real.** Los números de [BENCHMARKS.md](BENCHMARKS.md) son de una carga de trabajo declarada en una caja declarada; el mismo droplet cae en hosts 2–4× distintos en velocidad por núcleo («lotería de instancia», MOTOR-PRODUCCION-S2), y `?search=` + `count=true` cuesta ~1 rps de CPU de base ([SCHEMA-9](BACKLOG.md)). Mida con su carga: `drill load`, o el laboratorio.
- **GraphQL tiene residuos.** Las variables por `GET` no se leen ([ENG-22](BACKLOG.md)), las variables tipadas `String` aceptan cualquier escalar ([ENG-35](BACKLOG.md)), un pánico dentro de un resolver se recupera sin captura ([ENG-61](BACKLOG.md)). REST es el camino recomendado.
- **No es cero-downtime.** Cada cambio de binario cuesta ~0,3–0,6 s de 502 ([ENG-2](BACKLOG.md)); el drenado siempre espera 5 s ([ENG-58](BACKLOG.md)).
- **Una restauración es total**, no por tenant ([OPS-43](BACKLOG.md)). Suspender un usuario no revoca sus tokens ya emitidos (JWT sin estado; viven hasta `exp`).
- **El límite por tenant no es por IP**: 300 celulares de un mismo tenant comparten el balde. Las rutas públicas sí limitan por IP.
- **Sin importador masivo**: la puerta de lotes es `POST /api/transaction` (100 operaciones, 1 MiB); 46 k filas son ~460 lotes, minutos. Los números JSON pasan por float64 (enteros > 2^53 se truncan, [ENG-50](BACKLOG.md)).
- **Los mensajes del motor están en inglés** (los del panel no): un 500 dice `database unavailable`, un 422 dice `is required`. No son localizables hoy.
- **Windows como servidor no está verificado** ([OPS-20](BACKLOG.md)); el camino de producción es Linux.

---

## 8. Apéndice: dónde vive cada cosa en la caja

Instalación con `install.sh` (sin `--app`, el nombre es `appximo`; con `--app=vetapp`, reemplace):

| Qué | Dónde |
|---|---|
| Binario | `/opt/appximo/bin/appximo` (en una app de consumidor, el CLI del motor es `/opt/<app>/bin/appximo-cli`) |
| Configuración y secretos | `/etc/appximo/appximo.env` (0600) — **nunca** se versiona |
| Schema de arranque | `/etc/appximo/schema.json` |
| Scripts compañeros | `/opt/appximo/scripts/` — `backup.sh`, `restore.sh`, `deploy-update.sh`, `fleet-audit.sh`, `drill.sh` |
| Archivos subidos | `/var/lib/appximo/files/` |
| Trazas y métricas persistidas | `/var/lib/appximo/obs/obs.db` |
| Backups | `/var/backups/appximo/` — sets `appximo-<stamp>.{dump,files.tar.gz,conf.tar,manifest}` + `last-backup.status` |
| Unidad y timer | `appximo.service`, `appximo-backup.timer` (03:30) |
| Sitio de Caddy | `/etc/caddy/sites/appximo.caddy` |
| Puertos | datos `127.0.0.1:8090` (tras Caddy); control plane `127.0.0.1:9090` (**nunca** expuesto) |
| Binario anterior (rollback) | `/opt/appximo/bin-rollback/` |

Comandos que se usan todos los días:

```bash
sudo systemctl status appximo                          # ¿está arriba?
journalctl -u appximo -f -o cat                        # el log en vivo (JSON)
appximo version                                        # qué versión corre
appximo tenant list                                    # inventario de tenants
appximo token --secret "$JWT_SECRET" --tenant app --role admin --schema /etc/appximo/schema.json   # un token para probar
appximo migrate --tenant app --schema nuevo.json --dry-run                                          # ¿qué cambiaría un schema nuevo?
sudo bash /opt/appximo/scripts/backup.sh --app=appximo                                              # un backup ahora
appximo drill audit --app=appximo                                                                   # ¿qué falta?
```

---

## 9. El centro de mando: toda la operación en una pantalla

Todo lo anterior existe en tres lugares distintos: la caja (scripts), el panel de cada app (`/admin`) y el repositorio (backlog, decisiones). El **centro de mando** (CENTRO-MANDO-S1) es una app hecha **con Appximo** — un `schema.json` de inventario más rutas propias en Go, en el repositorio interno `centro-mando/` — que corre en **su propia caja, en otra región que las apps que vigila**, y junta todo eso en una pantalla en español, usable desde el celular. Si Appximo tiene un bug, el centro de mando lo sufre primero.

**Dónde:** la URL y la contraseña están en la caja fuerte del dueño (hoy `https://centro.<ip con guiones>.sslip.io`, provisional hasta que exista `centro.appximo.com`). Se entra con un usuario del tenant `centro` (rol `dueno` opera, rol `lectura` solo mira); los usuarios se crean en su `/admin` → Usuarios.

**La regla de interfaz.** Toda acción, en todo estado, muestra una de tres cosas — nunca un texto suelto:

1. **«Esto lo hago yo»** — un botón, con qué va a pasar y cuánto tarda. Antes de correr, la pantalla «Qué va a hacer» lista los pasos con el comando exacto.
2. **«Esto lo haces tú»** — el comando exacto para copiar, con las IPs y valores ya puestos, una línea que dice por qué no lo hace el panel, y un botón **Verificar** que comprueba (en el servidor, no en el navegador) antes de dejar avanzar.
3. **«Esto está bloqueado»** — por qué, y qué lo destraba.

Y cuando algo falla: **qué falló, qué quedó a medias y cómo volver**, siempre los tres. El botón **Pedir ayuda** arma y copia un paquete con la app, la versión, lo que falló, la traza (la salida del script), el estado de la caja y lo ya intentado — sin secretos — para pegarlo en un chat sin explicar desde cero.

**Lo que se llena solo** (cada dato lleva «leído hace N min»; lo viejo se marca): por app, `/health` en la caja y desde afuera, el veredicto de **Recursos**, el disco, el último backup (`last-backup.status`), los problemas de 24 h de **Observabilidad**, la versión que corre contra `main` («main N commits adelante», con la lista de qué gana) y el último release; por caja, `fleet-audit.sh` con cada ✗ y qué hacer; los pendientes, leídos del **registro estructurado** `docs/backlog/items.json` del repositorio público (cada uno con qué es, por qué importa, qué lo destraba, costo, daño, prioridad y quién decide — CENTRO-MANDO-S2; si el repo no lo publica, cae al parse de prosa de `BACKLOG.md`); las decisiones A-XX, leídas del paquete de traspaso en la caja de build; el costo mensual, sumado del inventario. Nada se marca a mano.

**La pantalla de inicio ordena, no vuelca** (CENTRO-MANDO-S2): arriba UNA sola cosa — «qué sigue», el pendiente de mayor prioridad (daño cruzado con costo, nunca fecha), con por qué es ese y qué lo destraba; debajo, una línea por frente (flota, motor, automatización, comercial, publicación, medición) con su semáforo, **plegada por default** — la salud de las apps y las auditorías viven dentro de la línea «Flota». La pantalla **Pendientes** agrupa por quién decide: los del dueño, los de un agente, y los bloqueados por otro item; cada uno se explica solo al desplegarlo. El estado de **publicación pausada** (decisión A-69) se muestra como deliberado, nunca como un rojo sin contexto.

**El inventario** (lo que su hermana necesitaría si mañana usted no está): servidores (qué son, quién paga, cuánto), apps, dominios (dónde está el DNS, cuándo vencen), clientes (qué cobra, cada cuánto, a quién llamar), contactos y «dónde está cada cosa» (repos, backups, cuentas — y dónde está la caja fuerte). **Las claves no van ahí**, y la pantalla «Caja fuerte» explica cómo configurar el acceso de emergencia de un gestor (Bitwarden/1Password) para un familiar.

**Las acciones**, todas sobre los scripts de este manual, nunca sobre un camino nuevo: **Actualizar** (`deploy-app.sh`: backup → swap → verificación desde afuera → rollback automático; el binario se construye en la caja de build desde `main`, o se descarga del último release con su sha256), **Simulacro** (`drill.sh` / `fleet-audit.sh` / el CLI con `drill`), **Auditar**, **Ensayar restauración** y **Restaurar de verdad** (`restore.sh`, escribiendo el nombre de la app), y **Migrar a otro servidor** — que encadena `backup.sh` en el origen, `install.sh` y `restore.sh` en el destino y la verificación con el `Host` del tenant, y deja como pasos suyos, listados **antes** de empezar, el DNS (registro exacto + Verificar) y apagar el origen (comando exacto + Verificar). Solo un servidor marcado **«libre»** en el inventario puede ser destino: una migración instala PostgreSQL, Caddy y el endurecimiento en él. Cada acción queda registrada con su salida completa; una corrida se puede **cancelar** y el panel corta su proceso.

**Lo que no hace:** no guarda claves; no crea ni borra servidores (no tiene token del proveedor, a propósito); no cambia DNS; no tiene MFA propio (el `/admin` de cada app sí); y si su propia caja muere, se reconstruye como cualquier app (§4.6) desde su set fuera de la caja.

