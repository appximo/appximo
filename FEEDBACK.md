# Appximo v0.1.2 — reporte de evaluación de campo

**Período:** 2026-08-05 23:27 → 2026-08-06 18:12 (hora local, −05:00) — **de la
instalación a producción con HTTPS en menos de 24 horas**, en 8 sesiones ·
**Versión:** appximo **v0.1.2** (`f7911af`), módulo Go `v0.1.2` · **Entorno:**
Windows 11 / AMD64 + droplet Ubuntu 22.04 (Docker 29.6.2 · PostgreSQL 16.14) ·
Go 1.26.5 · Node 20.11

Reporte construido **usando** el producto de punta a punta, no leyendo su
documentación. Cada afirmación está verificada contra el motor corriendo; las
sospechas van marcadas como tales, y los errores propios de la evaluación quedan
corregidos a la vista, no borrados.

---

## Cobertura: qué se construyó para evaluar

Una aplicación completa de reservas de gimnasio, de cero a producto:

> instalación del binario → PostgreSQL 16 en Docker (droplet) → schema de 7
> recursos, 2 máquinas de estado y 4 roles → registro de inquilino → datos y
> fotos (file store) → migración en caliente → backend custom en Go (2 rutas de
> agregación con `routes` grants) → frontend SvelteKit embebido en el binario
> (5 pantallas + un back-office CRUD generado desde `/openapi.json`) → panel
> `/admin` y Studio `/editor` → **despliegue a producción** (droplet de 1 GB,
> dominio propio, HTTPS con Let's Encrypt, hardening y backups).

Superficies ejercitadas: CLI (`validate`, `validate-schema`, `migrate`, `tenant`,
`token`, `admin`, `explain`, `serve`), CRUD generado (filtros, sort, paginación,
`include`, `aggregate`, `search`), auth (login, JWT, roles, rama MFA), file store
completo (subir, adjuntar, políticas, URL firmada, servir, borrar, deduplicación),
RBAC en sus tres formas más `routes` grants, servido estático embebido, panel de
administración y Studio (editor de roles y puerta de deploy).

**Volumen:** 13 hallazgos de severidad alta, una treintena de medios y bajos, y
18 elogios — cada uno con su evidencia.

---

## Veredicto ejecutivo

**El gradiente de calidad está invertido respecto de lo habitual, y eso es buena
noticia.** El núcleo —el modelado, las garantías en runtime, los mensajes de
error, los tres documentos para agentes— es riguroso: cada garantía declarada se
verificó contra el motor y ninguna se cayó (máquina de estados → 422 con el campo
nombrado; unicidad compuesta → 409; política de archivo → 422 `file_policy`;
borrado referenciado → 409 nombrando la tabla). La capa de **operación** —alta de
inquilino, bootstrap del panel, panel para binarios de consumidor, los
validadores de Studio— es la parte sin terminar. Es la mitad barata: lo difícil
ya está resuelto.

**El patrón transversal**, si solo cupiera una frase: casi todos los hallazgos
estructurales son **lógica duplicada que diverge**. Dos validadores del
`tenant_id` (T1) · tres jueces de RBAC en desacuerdo (ST1) · el archivo de boot
contra el registro del inquilino (B7) · los metadatos de filtro contra las
columnas reales (M1). La sugerencia transversal: una sola fuente de verdad por
regla, compilada hacia cada superficie que la necesite.

---

## Prioridad sugerida

(Esta tabla supersede las notas de prioridad dispersas en la bitácora.)

| # | ID | Hallazgo | Por qué en este orden |
|---|-----|----------|------------------------|
| 1 | **B1** (+B7, B8) | Compilar un binario propio rompe `/admin` y `/editor`; el rodeo exige 2 procesos, 2 puertos y 2 schemas | Fuerza a elegir entre las dos promesas centrales del producto, sin arreglo posible del lado del consumidor |
| 2 | **ST1** | La puerta de deploy de Studio rechaza el grant `files` que `frontend-spec` §7.1 ordena declarar | El veredicto equivocado es el que tiene el botón Deploy, y empuja a borrar grants que romperían subidas en producción |
| 3 | **M1** | Un campo migrado en caliente se lee y escribe, pero **no filtra** hasta reiniciar | Falla silenciosa con los datos guardándose bien: llega a producción sin que nadie lo note |
| 4 | **C1** | `maxprocs` a stderr en cada invocación, incluso en comandos `--json` | Rompe el scripting en Windows (PowerShell trata stderr nativo como error); el arreglo más barato de la lista |
| 5 | **F1** (+F1-bis) | "or a .env you source": el binario no carga `.env` y la frase no es accionable en Windows | Primer contacto; costó horas de diagnóstico, incluido un BOM invisible |
| 6 | **T2** (+B8) | El alta de inquilino y el bootstrap del super-admin no están en la trilogía que imprime el binario (el `QUICKSTART.md` del repo sí los trae) | Son los pasos 1 y 2 de operar el producto; un `appximo quickstart` imprimible los cerraría de una vez |
| 7 | **W1** | Rutas POSIX sin traducir: los datos de clientes acaban en `C:\var\` | Ruta que nadie espera en Windows, anunciada en un formato que no existe en el sistema |

---

## Correcciones y retractaciones

Para calibrar la confianza en el resto: los errores cometidos durante la propia
evaluación están corregidos en su sitio, no borrados.

- **T4** — se afirmó que `.localhost` no funciona en Windows. Falso: funciona en
  navegadores y en curl (RFC 6761); solo falla el resolutor del sistema.
  Corregido y degradado a 🟢.
- Se atribuyó al motor responder 400 con cuerpo vacío; era un artefacto de
  `Invoke-WebRequest` de PowerShell (nota de herramienta en §7).
- Se puso en duda el `on_delete: set_null` de archivos por una única lectura
  stale que no se reprodujo; quedó como anécdota marcada, no como hallazgo (§8).
- En S2 se diseñó de menos por prudencia: el motor sí genera la navegación de
  relaciones con `references`; lo que falta es documentarlo.
- **T5** — se culpó al motor de responder "invalid JSON" a JSON válido; el JSON
  llegaba roto (comillas comidas por PowerShell al pasarlo inline). Con el
  cuerpo desde archivo responde `{"errors":["schema is required"]}`. Retirado.

---

## Convenciones

**IDs estables por área:** `D` distribución · `W` Windows · `C` CLI · `F`
configuración y arranque · `S` schema y validador · `I` infraestructura · `T`
multi-tenancy · `M` migración y archivos · `B` backend custom · `FE` frontend ·
`ST` Studio. No se reescribe historia: lo corregido se marca, no se borra.

**Severidad:** 🔴 alta · 🟡 media · 🟢 baja · 💚 esto está muy bien hecho

**Regla de la casa:** solo entra lo verificado en la práctica contra el motor
corriendo, y cada ítem dice con qué evidencia. Lo que sea sospecha va marcado
como tal.

---

## Índice

1. Distribución e instalación — `D1`–`D3`
2. Windows — `W1`–`W3`
3. CLI y experiencia de uso — `C1`–`C6`
4. Configuración y arranque — `F1`–`F4`
5. Gramática del schema y validador — `S1`–`S6`
6. Infraestructura alrededor — `I1`–`I3`
7. Multi-tenancy: registro de inquilino y API — `T1`–`T7`
8. Migración en caliente y campos `file` — `M1`–`M10`
9. Backend custom en Go — `B1`–`B8`
10. Frontend embebido (SvelteKit en el binario) — `FE1`–`FE6`
11. Appximo Studio — `ST1`–`ST4`
12. Cosas que no estaban claras — tabla duda → respuesta
13. Propuesta: los primeros 10 minutos — onboarding sin fricción
— Bitácora · Nota metodológica

---

## 1. Distribución e instalación

### D1 🟡 No hay ruta de instalación, solo binarios sueltos
Los assets del release son binarios crudos con la versión en el nombre
(`appximo-v0.1.2-windows-amd64.exe`). Para tenerlo en el PATH hay que:
descargarlo, elegir a mano el asset de tu plataforma, renombrarlo a `appximo.exe`,
moverlo a un directorio, y editar el PATH. Cuatro pasos manuales.

No hay `install.sh`, ni paquete en winget/scoop/Homebrew, ni una URL estable tipo
`releases/latest/download/appximo-windows-amd64.exe` que permita escribir un
instalador de una línea.

**Sugerencia:** publicar el alias `latest/download/...` (GitHub lo soporta gratis)
y un `install.ps1` / `install.sh` de una línea. Es el primer contacto con el
producto y ahora mismo es lo más áspero de toda la experiencia.

### D2 🔴 El checksum no protege contra un release comprometido
`checksums.txt` está bien formado y el hash coincidió. Pero **vive en el mismo
release que el binario**: quien pueda reemplazar el `.exe` también puede
reemplazar el `checksums.txt`. Verificar ahí solo detecta corrupción de
transporte, no manipulación.

**Sugerencia:** firmar con `cosign` (o minisign/GPG) y publicar la clave pública
fuera del release. Para un motor que va a correr con credenciales de base de
datos en producción, es una brecha de cadena de suministro que vale la pena
cerrar temprano.

### D3 🟢 65 MB por binario
No es bloqueante, pero limita distribuirlo en imágenes pequeñas. Vale revisar si
hay assets embebidos (el admin panel SolidJS y el editor van dentro) que puedan
ir a un artefacto aparte.

---

## 2. Windows

> **Contexto (sesión 7):** el `QUICKSTART.md` del repositorio marca su pista de
> Windows como «⚠ NOT YET VERIFIED … If something below is wrong, please open an
> issue». Esta sección —junto con `C1`, `F1`/`F1-bis`, `T5` y las notas de
> PowerShell repartidas por el reporte— **es exactamente esa verificación**,
> ejecutada sobre el binario nativo de v0.1.2.

### W1 🔴 Rutas POSIX hardcodeadas — confirmado, escribe en `C:\var\`
Al arrancar, el motor anuncia:

```
files: local backend at /var/lib/appximo/files
```

En Windows eso se resuelve a la **raíz del disco actual**. Verificado: existe

```
C:\var\lib\appximo\obs.db
C:\var\lib\appximo\obs.db-shm
C:\var\lib\appximo\obs.db-wal
```

Es decir, crea un árbol `C:\var\` en la raíz del sistema de archivos. Además de
sucio, en máquinas con la raíz de `C:` restringida esto falla por permisos.

**Sugerencia:** usar `os.UserConfigDir()` / `%LOCALAPPDATA%\Appximo` en Windows,
y `$XDG_DATA_HOME` o `/var/lib/appximo` en Linux. Es una función de plataforma,
no una constante.

**Alcance real (sesión 3, corregido):** un almacén de archivos **local** es una
decisión de diseño normal y correcta — no es lo que se objeta aquí. Lo que se
objeta es únicamente que **la ruta por defecto es POSIX y no se traduce**. En
Windows, `/var/lib/appximo/files` acaba en `C:\var\lib\appximo\files`, un árbol
creado en la raíz del disco del sistema:

```
C:\var\lib\appximo\files\gimnasiodemo\5d\bd\5dbd1da7…   ← fotos de los socios
C:\var\lib\appximo\obs.db                               ← base de observabilidad
```

El log anuncia `/var/lib/appximo/files`, que no corresponde a ninguna ruta
existente en Windows: el usuario no tiene forma de saber dónde quedaron sus
archivos sin buscarlos. Con `APPXIMO_FILES_DIR` se reubica, pero el defecto
debería resolverse por plataforma.

### W2 🟡 Los mensajes de ayuda asumen `openssl`
Los errores de configuración sugieren `openssl rand -hex 32`. En Windows no
existe por defecto. El equivalente nativo no es obvio.

**Sugerencia:** o dar la alternativa por plataforma, o —mejor— un
`appximo gen-secret` que elimine la pregunta.

### W3 🟡 Un instalador futuro debe emitir `WM_SETTINGCHANGE`
Aplicable si algún día Appximo trae instalador. Escribir el PATH en el registro
**no basta**: Explorer cachea su bloque de entorno al arrancar y se lo hereda a
todo lo que lanza. Sin el broadcast `WM_SETTINGCHANGE`, ni reiniciar la app
(VS Code, terminal) toma el PATH nuevo — hay que cerrar sesión o reiniciar.

En .NET, `[Environment]::SetEnvironmentVariable(..., 'User')` sí emite el
broadcast; escribir el registro directamente no. *(Contexto: fue un error mío en
esta sesión, pero es la clase de detalle que rompe instaladores en Windows.)*

---

## 3. CLI y experiencia de uso

### C1 🔴 El log de `maxprocs` sale en **cada** invocación, por stderr
Toda ejecución —incluidas `appximo version` y `appximo validate --json`— imprime:

```
2026/08/05 23:49:55 maxprocs: Leaving GOMAXPROCS=16: CPU quota undefined
```

Dos problemas:

1. **Va a stderr.** PowerShell trata la salida por stderr de un ejecutable nativo
   como error: convierte cada línea en `NativeCommandError` y pone `$?` en
   `$false` **aunque el exit code sea 0**. Cualquier script que envuelva el CLI
   en Windows aparenta fallar. Me pasó cuatro veces en una sola sesión.
2. **Contamina la salida.** `appximo validate --json` está pensado para que un
   agente lo parsee; un comando `--json` no debería emitir nada que no sea JSON
   por ningún canal.

**Sugerencia:** silenciar el aviso salvo con `--verbose`/`APPXIMO_LOG_LEVEL`, y
que los comandos `--json` garanticen stdout limpio y stderr vacío en el camino
feliz. Es el ítem con mejor relación arreglo/beneficio de toda esta lista.

### C2 🟡 `validate` y `validate-schema` son dos cosas distintas con nombres casi iguales
- `validate` → semántico (relaciones, RBAC, state machines)
- `validate-schema` → estructural (meta-schema JSON Schema)

Por el nombre, uno diría que `validate-schema` es el que valida el schema. No hay
señal de cuál correr, ni de si uno incluye al otro. (Lo descubrí corriendo ambos.)

**Sugerencia:** un solo `validate` que corra estructural→semántico en orden y
reporte todo, con `--structural-only` para el caso raro.

### C3 🟡 `serve --help` no menciona las variables que necesita para arrancar
`appximo serve --help` solo documenta `--port` y `--control-port`. Que hacen falta
`DATABASE_URL`, `JWT_SECRET` y `ADMIN_KEY` solo se descubre **fallando**.

**Sugerencia:** listarlas en el help del subcomando.

### C4 🟢 `explain --lang es` no está documentado
Feature notable —explica el schema en prosa, y en español— que no aparece en
`appximo spec`. Lo encontró el usuario por su cuenta. Es justo lo que uno le
enseña a un cliente no técnico para validar reglas de negocio; merece estar en la
documentación destacada.

### C5 🟢 `appximo version` no tiene `--json`
Menor, pero útil para automatización y para pinnear versiones en CI.

### C6 🟡 Falta el contrato de CICLO DE VIDA: la trilogía enseña a construir, nada imprimible enseña a operar
`appximo specs` le entrega a un agente el contrato completo para **construir**
(schema → backend → frontend), y esa mitad funcionó de forma sobresaliente (`B4`,
`FE2`). Pero el arco que un producto real recorre —instalar, configurar,
registrar el inquilino, crear usuarios, migrar, desplegar, actualizar,
respaldar— no tiene equivalente imprimible: vive repartido entre el
`QUICKSTART.md` del repo, el resumen que imprime `install.sh`, `PRODUCTION.md` y
la experiencia acumulada.

Consecuencia medida en esta evaluación: el humano tuvo que alimentar al agente
documento a documento, descubrimiento a descubrimiento, durante 8 sesiones — y
los tres hallazgos operativos mayores (`T2`, `B8`, la vía alterna cuando Studio
bloquea el deploy) cayeron exactamente en los huecos **entre** documentos. La
capa de código está cubierta con rigor; la operativa se aprende tropezando.

**Sugerencia:** un cuarto documento imprimible — `appximo lifecycle-spec`, o un
`appximo specs --full` que lo incluya — con el arco operativo completo:
instalación por plataforma, las tres variables, alta de inquilino (con el schema
en el body), usuarios y super-admin, qué exige reinicio y qué no, la relación
entre `migrate` y el registro del inquilino, `install.sh` / `deploy-update.sh` /
backup y restore. **No hay que escribirlo: hay que agregarlo** — todo existe ya,
disperso. Con ese documento, el primer prompt de un agente podría abarcar lo que
aquí tomó 8 sesiones de contexto goteado. (Propuesta del evaluador tras
completar el ciclo entero, de la instalación a producción.)

---

## 4. Configuración y arranque

### F1 🔴 Dice "or a .env you source" pero **no carga `.env`** — confirmado
El mensaje de configuración faltante termina con:

> Set the variable(s) in the environment (or a .env you source) and run again

Verificado con entorno completamente vacío y un `.env` válido en el directorio de
trabajo: **no lo lee**, falla igual pidiendo las tres variables. La frase induce a
pensar que hay soporte de `.env` cuando lo que dice literalmente es "un .env que
*tú* sourcees" — y en Windows no existe `source`, así que la instrucción no es
accionable. Hay que expandirlo a mano:

```bat
for /f "usebackq tokens=1,* delims==" %a in (".env") do @set "%a=%b"
```

**Sugerencia:** cargar `.env` del cwd automáticamente (con precedencia del
entorno real por encima). Es una dependencia de una línea y elimina toda esta
fricción. Si se decide no hacerlo, reformular el mensaje para que no sugiera algo
que el binario no hace.

#### F1-bis 🔴 Corolario: un BOM en el `.env` rompe **solo la primera variable**, en silencio
Delegar la carga del `.env` al shell traslada al usuario una clase de error que
el motor podría absorber. Caso real de esta sesión: el `.env` se escribió con
`Set-Content -Encoding utf8` (PowerShell 5.1 antepone BOM `EF BB BF`). El
resultado:

```
DATABASE_URL — falta
JWT_SECRET   — cargada OK
ADMIN_KEY    — cargada OK
```

El `for /f` de cmd define una variable llamada `﻿DATABASE_URL` — con el BOM
pegado al nombre. Es **visualmente idéntica** a la correcta en cualquier `echo` o
`set`, y solo falla la primera línea del archivo, lo que hace el diagnóstico
contraintuitivo: parece un problema del valor (¿el DSN? ¿los `=` del
querystring?) cuando es del byte 0 del archivo.

Un cargador de `.env` en el binario haría `TrimPrefix(BOM)` una vez y nadie
volvería a pisar esto. Es el argumento más fuerte a favor de F1: no se trata de
comodidad, sino de que el shell es un intermediario que introduce fallos propios.

**Nota:** PowerShell 5.1 pone BOM con `Set-Content -Encoding utf8`, `Out-File` y
`>`. Para escribir un `.env` limpio hay que usar
`[System.IO.File]::WriteAllText($p, $t, (New-Object System.Text.UTF8Encoding($false)))`.

### F2 🟡 Falta un `appximo init --env` / `doctor`
Arrancar por primera vez exige generar dos secretos con las reglas correctas
(32+ y 16 bytes), construir un DSN y armar el archivo. Un comando que genere un
`.env` listo —y otro que diagnostique conectividad a la BD antes de arrancar—
convertiría el primer arranque en un solo paso.

### F3 💚 Los mensajes de error de configuración son excelentes
Digno de copiar. No solo dicen qué falta: dicen **por qué importa** y **cómo
resolverlo**. El de `JWT_SECRET` corto:

> HS256 security depends on the secret's length — a short secret makes every
> tenant's tokens forgeable offline.

Explica la consecuencia real de seguridad en vez de decir "valor inválido". El
bloque de las tres variables faltantes hace lo mismo. Es de lo mejor del producto.

### F4 💚 El arranque es informativo sin ser ruidoso
El log de boot dice exactamente qué quedó activado y qué no, y por qué:
CORS deshabilitado y cómo habilitarlo, signup público deshabilitado y con qué
variable, alertas SLO en noop por falta de `SLACK_WEBHOOK_URL`. Uno sabe en qué
estado está el sistema sin leer documentación.

---

## 5. Gramática del schema y validador

### S1 🔴 El validador no detecta el error que el propio spec llama "el más dañino posible"
El spec es enfático sobre `$user_id`: apuntar una condición RBAC a una columna que
no guarda un id de login **valida perfectamente y devuelve cero filas para
siempre**. Lo describe como "the most damaging mistake possible here".

Pero el oracle no lo verifica. Un schema con
`conditions: { field: "instructor_id", val: "$user_id" }` donde `instructor_id`
apunta al `id` de `instructores` (en vez de a su `user_id`) pasa como válido.

Es contradictorio: el documento dedica un bloque entero a advertir de una trampa
que la herramienta de verificación no cubre, y cuyo síntoma —cero filas— es
silencioso y tardío.

**Sugerencia:** emitir un *warning* (no error) cuando una condición `$user_id`
apunta a una columna FK cuyo `references` no es una columna uuid `unique`. Es
heurística, pero atrapa exactamente el caso descrito.

### S2 🟡 `has_many` + `references` a una columna distinta de `id`: sin documentar
Si una FK usa `"references": "user_id"` (lo que el propio spec exige para el
patrón `$user_id`), el spec no dice cómo resuelve el join una relación
`has_many`/`belongs_to` declarada sobre esa FK: ¿contra `id` o contra la columna
referenciada?

Ante la duda me auto-limité y no declaré la relación. Después resultó innecesario:
el motor generó `/api/reservas/{id}/miembro` **por sí solo** desde el campo FK.
O sea que la información existe y se usa — solo falta decirlo en la gramática.

**Sugerencia:** una frase en la sección RELATIONS. La ambigüedad hace que uno
diseñe de menos justo en el patrón que el spec recomienda.

### S3 🟡 `explain` lista las transiciones en orden alfabético, no de flujo
Para `clases` imprime:

```
- un registro nuevo empieza en "programada"
- de "abierta" puede pasar a "en_curso", "cancelada"
- de "en_curso" puede pasar a "finalizada"
- de "programada" puede pasar a "abierta", "cancelada"
```

El estado inicial aparece **tercero**. Para revisar un ciclo de vida —que es el
propósito del comando— el orden natural es topológico desde el inicial:
`programada → abierta → en_curso → finalizada`, y los terminales al final.
Con 5 estados ya cuesta seguirlo; con 10 sería ilegible.

### S4 🟢 La instrucción final del spec choca con el uso conversacional
> Deliver ONLY the JSON object (no prose, no markdown fences).

Correcto para un agente en pipeline, pero el spec se pega en un chat donde el
usuario normalmente **sí** quiere la explicación de las decisiones de diseño.
Convendría marcarla como condicional al modo de uso.

### S5 💚 La gramática está bien especificada — validó a la primera
Un schema de 7 recursos con dos state machines, RBAC de 4 roles en las dos formas
(role-global y per-resource), FKs con `references`, índice GIN con `opclass`,
eventos e índices únicos compuestos: `{"valid": true, "errors": []}` en la
primera pasada, sin iteraciones de corrección.

Lo que lo hace posible: el tipo cerrado y explícito ("number is INVALID"), la
sección **Common mistakes** que enumera cada rechazo, y el empujón a modelar
state machines en vez de enums pelados cuando la descripción menciona pasos. Esa
última es una decisión de diseño con criterio: convierte una regla de negocio
("no se puede saltar pasos") en algo que el motor garantiza.

### S6 💚 El `explain` como herramienta de validación de negocio
Cierra el ciclo: escribís el schema, y el motor te lo devuelve en prosa para que
un no-técnico confirme las reglas. La frase final —"Esto es una lectura literal
del schema — nada de lo de arriba es adivinado"— es exactamente el encuadre
correcto.

---

## 6. Infraestructura alrededor (no es Appximo, pero afecta a quien lo despliega)

### I1 🔴 Docker publica puertos **saltándose ufw**
Trampa clásica que muerde a cualquiera que despliegue Appximo + Postgres en un
VPS. Docker inserta sus reglas en la tabla `nat` de iptables, que se evalúa
**antes** que la cadena `INPUT` donde vive ufw. Resultado:

```bash
ufw status          # "Default: deny (incoming)"  ← mentira para puertos Docker
docker run -p 5432:5432 postgres:16   # expuesto a Internet igualmente
```

La defensa es publicar explícitamente en loopback: `-p 127.0.0.1:5432:5432`.

**Sugerencia:** mencionarlo en la guía de despliegue. Es la diferencia entre una
base de datos privada y una escaneada en minutos, y `ufw status` no te avisa.

### I2 🟡 No hay imagen Docker de Appximo ni `docker-compose.yml` oficial
Un compose con `postgres:16` + el motor + las variables ya cableadas sería el
on-ramp más rápido posible, y de paso documentaría la configuración correcta
(incluido I1). Hoy cada quien lo arma de cero.

### I3 🟢 Las imágenes Docker de DigitalOcean traen 2375/2376 abiertos en ufw
Los puertos de la API de Docker (2375 es **sin cifrar ni autenticar**) quedan
permitidos en el firewall aunque el demonio solo escuche en el socket unix. Hoy
no hay nada detrás, pero es un hueco esperando a que alguien exponga el demonio.
Vale la pena cerrarlos.

---

## 7. Multi-tenancy: registrar un inquilino y usar la API

### T1 🔴 La UI y la API **contradicen** la regla del `tenant_id`
El bundle del panel de admin valida con este mensaje:

> tenant id must be 2–30 chars: a lowercase letter first, then lowercase letters,
> digits or **`_`** (no hyphens/uppercase/spaces)

La API rechaza exactamente eso:

> invalid tenant id "gimnasio_demo": must match `^[a-z][a-z0-9]{1,29}$` — …
> **No hyphens, underscores**, uppercase or spaces

Un `tenant_id` con guion bajo pasa la validación del formulario y muere en el
servidor. Las dos reglas viven en sitios distintos y divergieron.

**Sugerencia:** una sola fuente de verdad para el patrón, servida al front
(p. ej. en `/openapi.json` o un endpoint de metadatos) en vez de duplicada en el
bundle.

### T2 🔴 El registro de inquilinos **no está en ninguna de las tres specs**
`appximo specs` se presenta como el contrato completo ("one paste = the whole
contract"), pero el primer paso operativo —dar de alta un inquilino— no aparece.
Lo único que dicen es:

> register tenants through the control plane / admin API and mint tokens as usual
> (README quick start)

…remitiendo a un README que el binario no trae. `appximo tenant --help` solo
ofrece `list` y `delete`, no `create`. Terminé **desminificando el bundle
JavaScript del panel** para descubrir el contrato:

```
POST /admin/tenants        (header X-Admin-Key)
{ "tenant_id", "display_name", "email", "plan", "schema": { …el schema entero… } }
```

Que el `schema` completo vaya en el body del alta es la pieza no obvia: sin ella
el 400 es inevitable y nada lo sugiere.

**Sugerencia:** o un `appximo tenant create --schema schema.json --id acme`, o
una sección de 10 líneas en el spec. Es literalmente el paso 1 de usar el
producto.

**Actualización (sesión 7):** el flujo SÍ está documentado — en el
`QUICKSTART.md` del repositorio (§4), con el detalle clave incluido («the
registration **carries** the schema», contra `:9090/tenants` con `X-Admin-Key`).
El hallazgo se acota, no se retira: la trilogía que el binario imprime
(`appximo specs`) sigue sin incluirlo ni referenciarlo, y quien instala el
binario suelto no tiene el repositorio. Sugerencia ajustada: un
`appximo quickstart` que imprima esa guía —como ya ocurre con
`spec`/`backend-spec`/`frontend-spec`— cerraría `T2` y `B8` de una vez.

### T3 🟡 Sin el `Host` del inquilino, la API responde **500**
```
curl localhost:8080/api/disciplinas          → 500 {"error":"internal error"}
curl -H "Host: gimnasiodemo.localhost" …     → 200
```
El JWT lleva `tenant_id` en sus claims, pero el enrutado se resuelve por
subdominio y gana el host. Un inquilino inexistente debería ser un `400`/`404`
que lo diga, no un `500` genérico enmascarado.

Justo es decir que el `frontend-spec` **sí** lo advierte (§checklist: *"a 500/401
that only happens from a script and never from the browser is almost always a
missing tenant Host"*). Que exista esa nota es señal de que el síntoma ya mordió
a alguien — razón de más para arreglar el código de estado en vez de documentar
la confusión.

### T4 🟢 Los subdominios `.localhost` en Windows — CORREGIDO, era más leve
> **Corrección (sesión 5).** La versión original de este ítem decía que
> `gimnasiodemo.localhost` **no funciona** en Windows y que el navegador no podría
> abrir la app sin editar el archivo `hosts`. **Es falso**, y la conclusión se
> sacó de una sola prueba mal elegida.

Lo que realmente ocurre:

```
[System.Net.Dns]::GetHostAddresses('gimnasiodemo.localhost')  → "Host desconocido"
curl http://gimnasiodemo.localhost:8080/healthz               → HTTP 200
```

El resolutor DNS del sistema (la API que usa .NET) **no** conoce `.localhost`,
pero curl y los navegadores lo resuelven **internamente** a loopback, como manda
la RFC 6761. Verificado sirviendo la aplicación entera —shell, 15 assets, rutas de
cliente y `/openapi.json`— por `http://gimnasiodemo.localhost:8080` sin tocar el
archivo `hosts` ni pasar cabecera `Host` a mano.

Lo que queda del ítem, ya menor: **los scripts que usen el resolutor del sistema
sí fallan** (PowerShell con `Invoke-WebRequest`, `Test-NetConnection`, cualquier
cosa sobre `System.Net.Dns`). Ahí hay que apuntar a `127.0.0.1` y mandar el header
`Host` explícito. Es exactamente la trampa #8 del `frontend-spec`, que ya lo
advierte.

**Lección para esta bitácora, más que para el creador:** una prueba negativa con
una herramienta no basta para declarar que algo no funciona. La afirmación
original sobrevivió tres sesiones sin que nadie la contrastara.

### T5 🟢 "invalid JSON" para JSON válido — RETIRADO: era artefacto del evaluador
> **Corrección (sesión 7).** La versión original culpaba al motor de responder
> `{"error":"invalid JSON"}` ante un JSON válido al que solo le faltaban campos.
> Falso: el JSON llegaba **realmente roto** — PowerShell 5.1 se come las comillas
> internas al pasar JSON inline a un ejecutable nativo, así que el motor recibía
> `{tenant_id:otro}`. Con el cuerpo enviado desde archivo, responde el error
> nombrado que el propio Quick Start documenta:
>
> ```
> POST :9090/tenants  (JSON válido, sin "schema")
> → 400 {"errors":["schema is required"]}
> ```
>
> El motor hacía lo correcto. Es el tercer artefacto de herramienta de esta
> evaluación (con los 400 "sin cuerpo" y el BOM de F1-bis); la lección operativa
> para Windows: todo JSON hacia un exe nativo va por `--data-binary @archivo`.

### T6 💚 El mensaje de error del `tenant_id` es un ejemplo a seguir
Cuando por fin se ve, explica **la causa física** de la regla, no solo la regla:

> the id is used BOTH as the database schema (which forbids hyphens) AND as the
> first part of the web address (which forbids underscores), so only what works
> as both is accepted … try "gimnasiodemo"

Da el porqué de cada restricción y **sugiere un id válido derivado del que
enviaste**. Ojalá T1 y T2 estuvieran a esta altura.

### T7 💚 Las garantías del schema se cumplen de verdad en runtime
Verificado contra el motor corriendo, no en teoría:

```
PATCH estado: confirmada → solicitada   → 422 invalid transition for "estado":
                                              from "confirmada" to "solicitada" is not allowed
POST reserva duplicada (clase+miembro)  → 409 field "clase_id, miembro_id": value already exists
PATCH estado: confirmada → asistio      → 200
```

La máquina de estados y el índice único compuesto declarados en el JSON se
traducen en 422 y 409 con mensajes que nombran el campo. Esto es exactamente lo
que promete el producto, y lo cumple.

### Nota de herramienta (no es de Appximo)
Perdí varios intentos creyendo que la API devolvía `400` con cuerpo vacío. No era
así: `Invoke-WebRequest` de PowerShell 5.1 **pierde el cuerpo de la respuesta en
las excepciones** de status ≥400, incluso leyendo el stream en el `catch`. Con
`curl.exe -i` los mensajes de error aparecieron completos y detallados desde el
primer intento. Para depurar esta API en Windows: usar `curl.exe`, no los
cmdlets.

---

## 8. Migración en caliente y campos `file`

### M1 🔴 "new fields serve hot" es solo a medias — los campos nuevos NO son filtrables hasta reiniciar
`appximo migrate` termina afirmando:

> ✓ schema persisted to the tenant record (running engine notified — **new fields
> serve hot**; a NEW resource still needs a restart)

Es cierto para escritura y lectura, y falso para consulta. Tras migrar dos campos
nuevos (`codigo_socio`, `foto`) sobre el motor **en marcha**:

```
PATCH /api/miembros/{id} {"codigo_socio":"GYM-0001"}   → 200, lo guarda
GET   /api/miembros?filter[documento]=…                → devuelve codigo_socio y foto
GET   /api/miembros?filter[codigo_socio]=GYM-0001      → 400
   {"error":"unknown filter field: codigo_socio (available: activo, created_at,
    documento, email, fecha_alta, id, membresia_id, nombre, telefono, user_id)"}
```

La lista de campos filtrables se quedó en la versión anterior del schema —
`codigo_socio` y `foto` faltan en ella. Reiniciar el proceso lo arregla
(verificado: el mismo filtro devuelve la fila).

Lo peligroso es la forma del fallo: el dato **se escribe y se lee bien**, así que
todo parece haber migrado; solo se rompe al filtrar, que es justo lo que hace una
pantalla de búsqueda. En producción se manifestaría como un `400` intermitente
según qué instancia atienda, hasta el próximo despliegue.

**Sugerencia:** o el recargado en caliente reconstruye también los metadatos de
filtro/orden, o el mensaje debe decir qué queda pendiente de reinicio. Hoy afirma
más de lo que cumple.

### M2 🟡 La URL firmada es absoluta y sin puerto — inservible tal cual en desarrollo
`GET /api/files/{id}/url` devuelve:

```json
{"expires_in":180,"url":"http://gimnasiodemo.localhost/files/signed/<jwt>"}
```

Host del inquilino, esquema `http`, y **sin puerto** — o sea `:80`. En una
instancia de desarrollo en `:8080` esa URL no resuelve: hay que parsearla y
quedarse con la ruta. El spec la presenta para ponerla directo en un `src`
(`img.src = url`), que es exactamente lo que no funciona en local.

**Sugerencia:** devolver una URL **relativa** (`/files/signed/<jwt>`) — funciona
igual en un `src`, sirve en cualquier puerto y evita fijar el host.

### M3 🟢 El `--dry-run` advierte de backfill en columnas que nacen todas NULL
Al añadir dos columnas nuevas advirtió:

```
⚠ [backfill] ADD UNIQUE miembros (codigo_socio) requires existing rows to already satisfy it
⚠ [backfill] ADD FOREIGN KEY miembros (foto) -> files (id) requires existing rows to already satisfy it
```

Ninguna de las dos aplica: son columnas **recién creadas**, así que todas las
filas existentes quedan en `NULL`, y en PostgreSQL los `NULL` son distintos entre
sí para un UNIQUE y están exentos de un FK. La migración aplicó sin problema, como
era esperable.

La advertencia genérica cuesta credibilidad justo donde más importa: si avisa en
casos inofensivos, uno aprende a ignorarla y se pierde el aviso real (una columna
`NOT NULL` sobre tabla poblada, por ejemplo).

**Sugerencia:** omitir la advertencia cuando la columna se añade en la misma
migración y es nullable.

### M4 💚 El modelo de migración es el punto más fuerte que he visto en el producto
Política **aditiva por defecto** con los destructivos **enumerados uno por uno**
en `--approve-drops` — nunca un "sí a todo". El `--dry-run` lista cada operación
antes de tocar nada, y el fan-out multi-inquilino es reanudable y resiliente.
Es el modelo correcto, y es exactamente lo que suele estar mal resuelto en la
competencia.

### M5 💚 El olfateo de contenido rechaza un archivo disfrazado, en la subida
Un `.txt` renombrado a `.png` y enviado con `Content-Type: image/png`:

```
POST /api/files → {"error":"files: upload rejected: content does not match
                   extension \".png\" (detected text/plain)"}
```

Rechazado en la **primera** capa, sin llegar siquiera a la política `accept` del
campo. El mensaje nombra la extensión declarada y el tipo detectado. El
`Content-Type` del cliente nunca se cree, tal como promete el spec.

### M6 💚 Los archivos son privados por defecto, y la URL firmada funciona
```
GET /api/files/{id}          sin token  → 401
GET /files/signed/<jwt>      sin token  → 200, image/png, 6172 bytes
```
Los bytes descargados por la URL firmada son **idénticos** (SHA-256) al original
subido. Expira en 180 s. El comportamiento por defecto es el seguro.

### M7 🟡 `accept: "image"` deja pasar SVG, y el spec no lo advierte
Un `.svg` legítimo pasa el filtro `accept: "image"` y queda adjunto como foto de
un socio (verificado, `200`). SVG es XML y admite `<script>`.

**No es una vulnerabilidad del motor** — sirve los bytes con
`Content-Disposition: attachment`, CSP `default-src 'none'`, `nosniff` y
`X-Frame-Options: DENY`, así que ni se renderiza inline ni ejecutaría nada (ver
`M9`). Pero el spec presenta las familias (`image`/`audio`/`video`/`text`) sin
mencionar que `image` incluye un formato ejecutable, y el patrón §7.5 del propio
`frontend-spec` invita a servir archivos por una **ruta pública propia**, donde
el desarrollador tendría que replicar esas cabeceras a mano.

**Sugerencia:** una nota en el spec: "`image` incluye `image/svg+xml`; para
excluirlo usa tipos exactos `["image/png","image/jpeg"]`". Una línea evita una
XSS almacenada en el primer proyecto que sirva imágenes públicamente.

### M8 🟡 Los archivos huérfanos no los recoge nadie, y no se menciona
`POST /api/files` crea el archivo **antes** de que exista el registro que lo
referencia. Si el usuario abandona el formulario, el archivo queda para siempre:
fila en `files` y bytes en disco. En esta sesión quedaron 7 huérfanos de 10 filas.

No hay recolector, ni comando de limpieza, ni mención del tema en las specs —
que sí describen con detalle el flujo de tres pasos que los genera.

**Sugerencia:** o un `appximo files gc --tenant X --older-than 24h`, o al menos
un párrafo advirtiendo que el patrón subir→adjuntar acumula basura y que la
limpieza es responsabilidad del operador. Ojo al implementarlo: por la
deduplicación de bytes, hay que agrupar por `sha256` y no por `id`.

### M9 💚 Las cabeceras con las que sirve los archivos son las correctas
Probado contra el caso peor (un SVG):

```
Content-Disposition: attachment; filename="icono.svg"
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Etag: "<sha256>"          ·  Accept-Ranges: bytes
```

Defensa en capas bien pensada, y de regalo el ETag es el propio SHA-256 — la
revalidación sale gratis porque el almacén ya es direccionado por contenido. Los
archivos son privados por defecto (`401` sin token) y la URL firmada dura 180 s.

### M10 💚 El olfateo hace innecesaria una lista blanca de extensiones
Verificado: `.pdf`, `.svg`, `.zip` y `.txt` suben sin restricción **mientras el
contenido corresponda a la extensión**. Es la decisión correcta — una lista
blanca de extensiones es seguridad de teatro, y validar el contenido real cubre
el caso que importa. La restricción por tipo se declara donde tiene sentido: en
el campo, con `accept`.

---

## 9. Backend custom en Go (rutas propias)

### B1 🔴 Compilar un binario propio rompe `/admin` y `/editor` — con un 200, no con un error
Un consumidor que sigue backend-spec §3.1 obtiene un binario **sin los assets del
panel de administración ni de Studio**. El arranque lo avisa:

```
admin UI: serving /admin (WARNING: no built assets embedded — run `make admin-ui` …)
editor: serving /editor (WARNING: no built assets embedded …)
```

Pero el servidor **sigue publicando las rutas**, y lo que sirve está roto:

```
GET /admin                       → 200, 530 bytes (el shell HTML)
GET /admin/assets/index-….js     → 404          ← el bundle que el shell pide
```

O sea: pantalla en blanco. El aviso está en el log del arranque, que en producción
nadie mira; quien abre `/admin` ve un `200 OK` y nada más.

Y el remedio que sugiere —`make admin-ui`— **no es accionable para un consumidor**:
requiere el repositorio del motor y su toolchain de npm, que quien hace
`go get github.com/appximo/appximo` no tiene.

**Sugerencia:** o embeber los assets ya construidos en el módulo publicado (que es
lo que el usuario espera al importar la librería), o —si eso hincha el módulo— no
montar las rutas cuando no hay assets y responder un `404`/`503` honesto que diga
por qué. Servir un shell que no puede cargar es el peor de los tres.

**Confirmado además que no hay escape por configuración:** no existe ninguna
variable `APPXIMO_*` que apunte el panel a un directorio de assets externo (se
buscaron todas en el binario). La única salida documentada es `make admin-ui` en
el repositorio del motor.

**Corroboración externa (sesión 8, verificado):** las demos productivas del
propio proyecto exhiben el mismo síntoma. En `tiendita.appximo.com` —la
referencia que cita el frontend-spec—, `/admin` responde 200 con el shell y su
bundle `/admin/assets/index-D1WjShIB.js` responde **404** (19 bytes,
text/plain). No es solo un problema del consumidor externo: el binario de
referencia del propio autor corre en producción sin los assets del panel.

#### El único rodeo disponible, y por qué no es una solución

El binario genérico *sí* trae los assets, así que puede servir el panel en otro
puerto contra la misma base mientras el binario custom sirve la API. Funciona
—verificado: `/admin` y `/editor` cargan completos (JS de 205 KB y 508 KB, MIME
correctos), `/admin/tenants` responde, el login del panel devuelve 200— pero el
precio deja claro que es un síntoma, no un arreglo. Para tener panel hay que:

1. **Correr dos procesos** del mismo motor, en dos puertos, con dos planos de
   control (`--control-port` distinto o colisionan).
2. **Mantener dos schemas** que solo difieren en los bloques `routes`, porque el
   genérico rechaza arrancar con concesiones a rutas que él no registra.
3. **Crear un super-admin de plataforma aparte** (`appximo admin create`) para
   poder entrar al panel.

Y sobre todo: el panel de administración de tu aplicación **no lo sirve tu
aplicación**. Lo sirve un proceso paralelo que no conoce ninguna de tus rutas, con
una copia divergente de tu schema. Para un producto cuyo argumento es "un binario
que es backend, frontend, admin y docs a la vez" (backend-spec §3.7), esto es
justo lo contrario de lo que promete.

**Lo que conviene verificar del lado del creador:**

- ¿Cuál es el camino previsto para que un consumidor con rutas custom tenga panel?
  Si es `make admin-ui`, no es alcanzable desde `go get`. Si es otro, no está
  documentado en ninguna de las tres specs.
- ¿Cuánto pesarían realmente los assets embebidos en el módulo publicado?
  (205 KB + 508 KB + CSS ≈ 800 KB.) Frente a un binario que ya pesa 78 MB y a un
  grafo de dependencias de 1.2 GB, parece un coste menor comparado con perder el
  panel.
- Si se decide no embeberlos: que las rutas `/admin` y `/editor` **no se monten**
  cuando no hay assets, en vez de servir un shell roto con `200`.

### B7 🔴 Dos schemas divergentes + Studio con Apply/Deploy = riesgo de pisar el bueno
Consecuencia directa del rodeo de B1, y potencialmente destructiva.

El editor (Appximo Studio) se sirve en la instancia del **panel**, que cargó
`schema-panel.json` — la copia **sin** los bloques `routes`. Studio ofrece un
"gated Apply + Deploy" que lleva el schema editado a la API en marcha.

Si alguien edita y aplica desde Studio, estaría desplegando la versión sin las
concesiones `routes`. Las rutas custom del otro proceso seguirían registradas,
pero los roles `recepcion` e `instructor` perderían el permiso: **403 silenciosos
en producción**, sin que nadie haya tocado el schema "real".

> **No verificado.** No ejecuté el Apply de Studio para no arriesgar el entorno.
> Es una hipótesis razonada a partir de dos hechos ciertos (Studio despliega el
> schema que tiene cargado; la instancia del panel tiene cargada la copia
> mutilada), pero **debe comprobarse antes de darla por buena**.

**Lo que conviene verificar del lado del creador:** si el Apply de Studio persiste
el schema al registro del inquilino, y si en ese caso una instancia distinta puede
sobrescribir la configuración RBAC de la que sirve la API. Si es así, el rodeo de
B1 no solo es incómodo: es peligroso, y habría que bloquearlo explícitamente.

**Actualización (sesión 6, observado):** el escenario se acotó y a la vez se
confirmó por otra vía. Lo bueno: el diálogo de deploy de Studio carga el schema
del **registro del inquilino** ("Update existing → gimnasiodemo · 7 resources"),
no el archivo con el que arrancó la instancia del panel — desplegar
`schema-panel.json` no es el flujo real. Lo malo: ese registro estaba
**desactualizado** respecto del schema de arranque de la API (versión 2,
`source: migrate-cli`, de la sesión 3 — con los grants de `files` pero **sin**
las concesiones `routes` añadidas en la sesión 4; verificado contra
`/admin/tenants/{id}/schema/history`). Un deploy desde Studio habría revertido
el RBAC igualmente — por registro stale, no por archivo mutilado. Dos fuentes de
verdad (archivo de boot vs registro del inquilino) sin nada que las reconcilie:
la misma familia de causa que `ST1`. Mitigación de operador: correr
`appximo migrate` tras cada cambio de schema mantiene el registro al día
(aplicado en la sesión 6: el registro quedó en v3, con las concesiones `routes`
incluidas).

### B8 🟡 El panel exige un super-admin cuya creación no está en ninguna spec
`/admin` no es utilizable recién arrancado: pide credenciales y no hay ninguna. El
comando existe —`appximo admin create --email … --password …`— pero **no aparece
en `spec`, `backend-spec` ni `frontend-spec`**; solo se descubre haciendo
`appximo admin --help`.

Es el mismo patrón que `T2` (el alta de inquilino): la capa de **código** está
documentada con rigor, y la de **operación** no lo está. Un operador que instala el
motor y abre el panel se queda en la pantalla de login sin pista de cómo salir.

**Lo que conviene verificar del lado del creador:** si el arranque detecta que no
existe ningún super-admin de plataforma y lo dice en el log —al estilo de los
excelentes mensajes de configuración faltante (`F3`)— el problema desaparece sin
tocar la documentación.

**Actualización (sesión 7):** `QUICKSTART.md` §5 sí documenta
`appximo admin create`, y anuncia para esta versión una pantalla de primer
arranque en `/admin` («Create the first admin», validada con el `ADMIN_KEY`).
No pudo verificarse aquí: cuando se leyó ese documento, el super-admin ya
existía. Si esa pantalla funciona en v0.1.2, `B8` queda resuelto para el binario
stock — y sigue pendiente para el binario de consumidor, cuyo `/admin` está
roto (`B1`).

**El mensaje de rechazo del genérico, en cambio, es ejemplar** y merece decirse:

```
appximo: RBAC grants custom routes that are not registered:
  rbac.roles.recepcion.routes.retencion: no custom route is registered under
    /api/retencion (registered segments: none)
```

Nombra cada concesión huérfana, la ruta que esperaba y lo que hay registrado.
Falla en el arranque y no en un 403 misterioso en producción, que es exactamente
lo que promete backend-spec §3.5.

### B2 🟡 El grafo de dependencias del consumidor es desproporcionado
`go mod tidy` sobre un backend de dos endpoints descargó **más de 1.2 GB** y tardó
más de 15 minutos con caché fría. El binario resultante pesa **78 MB**.

Entran al grafo de **todo** consumidor: el SDK de AWS completo (~18 módulos),
`gocloud.dev`, `google.golang.org/api`, gRPC, Redis, Prometheus, OpenTelemetry
(6 módulos), `modernc.org/sqlite`, Goja, Wazero, GraphQL y MaxMind. Casi todas
corresponden a funciones **opcionales por configuración** —S3, Redis, OTel— que un
backend que no las usa igualmente compila y carga.

No bloquea, pero encarece el primer contacto (los 15 minutos de espera no se
distinguen de un cuelgue) y pesa en CI y en imágenes de contenedor.

**Sugerencia:** mover los backends opcionales a submódulos o detrás de build tags,
para que el grafo por defecto sea el núcleo + pgx.

### B3 🟢 Los enums y las máquinas de estado no existen en la base
`clases.estado` y `reservas.estado` son `text` **sin CHECK constraint**. Todo el
enum y toda la máquina de estados viven en el motor, a nivel de API.

Es defendible por diseño —el motor es el dueño de la validación, y un CHECK
complicaría las migraciones de estados— pero tiene una consecuencia que conviene
documentar: **cualquier escritura por SQL directo** (una migración manual, un
script de importación, una siembra) puede dejar valores que la API jamás habría
aceptado, y nada lo detecta después.

### B4 💚 La superficie documentada en backend-spec es exacta — compiló a la primera
`ParseServeArgs`, `Config{SchemaPath, Port, Version}`, `Route{Method, Path,
Description, Timeout, Handler}`, `Ctx.UnsafeTx()`, `ctx.Context()`, `ctx.JSON`,
`ctx.Error`: todo funcionó tal como está escrito, sin una sola corrección ni una
firma que no coincidiera. Para un documento que se pega en un agente, esto es
exactamente lo que hace falta — y es notablemente raro.

El aviso del propio doc ("Do not invent API surface beyond what is listed — if a
method is not here, the Ctx does not have it") resultó fiable en ambos sentidos.

### B5 💚 Las concesiones `routes` se validan en el arranque, y la matriz sale exacta
Concedido `retencion` + `ocupacion` a `recepcion`, solo `ocupacion` a `instructor`,
nada a `miembro`. Verificado contra el motor corriendo:

| rol | `/api/retencion/riesgo` | `/api/ocupacion` |
|---|---|---|
| `miembro` | 403 | 403 |
| `instructor` | 403 | 200 |
| `recepcion` | 200 | 200 |
| `admin` (comodín) | 200 | 200 |

Deny-by-default real, sin escribir una línea de autorización en el handler. Y como
el arranque valida que cada segmento concedido exista, una concesión muerta no se
convierte en un 403 misterioso: no arranca.

### B6 💚 `Route.Description` sí llega al contrato
Se publica como `summary` de la operación (no como `description`, que el motor
rellena con su propio texto explicando el modelo de autorización). Ambas cosas
aparecen en `/openapi.json` junto a `x-appximo-custom-route: true` y las respuestas
`401`/`403` ya referenciadas. El reparto es sensato: el autor pone el qué, el
motor pone el cómo se autoriza.

---

## 10. Frontend embebido (SvelteKit en el binario)

### FE1 🟢 El snippet de `vite.config.js` omite el import, y la deducción natural es la equivocada
El `frontend-spec` §3 presenta la configuración como copiable:

```js
export default {
  plugins: [sveltekit()],
  server: { proxy: … }
};
```

…pero no muestra de dónde sale `sveltekit`. La deducción natural es
`@sveltejs/vite-plugin-svelte`, que además **está en la lista de dependencias que
el propio documento recomienda** (§2). Es incorrecto: viene de
`@sveltejs/kit/vite`. El error es un fallo de build inmediato y claro
(`does not provide an export named 'sveltekit'`), así que cuesta un minuto, pero
es gratis de evitar añadiendo la línea del import al bloque.

### FE2 💚 Las reglas de servido estático se cumplen exactamente como están escritas
Verificado contra el binario con el SPA embebido, punto por punto:

```
/                 200  1789 b   Cache-Control: no-cache, no-store, must-revalidate
/_app/…/*.js      200           Cache-Control: public, max-age=31536000, immutable
/socios           200  1789 b   ← ruta de cliente: cae al shell
/_app/falta.js    404           ← asset ausente CON extensión: 404 real, nunca el shell
/api/nope         401           ← auth antes que enrutado, como documenta §0.1
/openapi.json     200           ← prefijos del motor no quedan tapados por el montaje
/docs, /healthz   200
```

Y la CSP que instala el montaje trae el endurecimiento por hash que el documento
promete, para el script inline con el que arranca el shell de SvelteKit:

```
script-src 'self' 'sha256-deZ3RAHj6QKoXzuSk2iEWr6zc4g1nUINOwMSevss5u0='
```

Cada regla de la tabla de §1 resultó cierta sin excepción. Para un documento cuyo
valor es que un agente lo siga sin verificar, esto importa.

### FE3 💚 Las trampas de §9 están bien elegidas: dos de tres se materializaron
- **`go:embed` necesita `all:`** — el build emitió `_app/`, así que sin el prefijo
  el índice habría cargado con 404 en cada asset. La trampa es real y silenciosa.
- **El orden `npm run build` → `go build`** — el binario embebe el árbol tal como
  esté en ese momento; invertirlo produce un binario que "despliega bien" y sirve
  una página rota.
- La tercera (CSP y la página en blanco que curl no ve) no se materializó porque
  no se sobrescribió la CSP por defecto, que es justo lo que el documento
  recomienda.

Que un documento acierte en qué va a doler es mejor señal que cualquier lista de
funciones.

### FE4 🟡 La CSP por defecto bloquea la vista previa de imagen, que es el patrón canónico de toda subida
`DefaultStaticCSP` declara:

```
img-src 'self' data:
```

Sin `blob:`. Y la forma estándar de mostrar la imagen que el usuario acaba de
elegir —antes de subirla— es `URL.createObjectURL(file)`, que devuelve
precisamente una URL `blob:`. Resultado: **la vista previa no aparece y la página
no dice nada**; el motivo solo se ve en la consola del navegador.

Es de la misma familia que la trampa #1 de §9 ("una página en blanco con un 200 es
un problema de CSP y curl no puede verlo"), pero aquí no se rompe la página
entera, solo un elemento — así que es aún más fácil culpar al código propio.

Lo que lo hace reportable: el `frontend-spec` **dedica la sección §7.2 entera a
construir una UI de subida de archivos**, con progreso, cancelación y estados de
error. Una vista previa es el compañero inevitable de esa pantalla, y la CSP que
el propio motor instala la bloquea sin mencionarlo en ningún sitio.

`blob:` en `img-src` no relaja nada apreciable: una URL blob es del mismo origen
y la crea el propio documento; no es un canal de exfiltración ni permite cargar
imágenes de terceros.

**Sugerencia:** añadir `blob:` a `img-src` en `DefaultStaticCSP`, o —si se
prefiere no tocar el valor por defecto— una línea en §7.2 diciendo que la vista
previa debe hacerse con `FileReader` → `data:` URL, que es lo que la CSP sí
permite. *(Ese es el arreglo que se aplicó aquí.)*

### FE5 🟡 El OpenAPI no expresa `references`, así que una UI genérica no puede resolver una FK que no apunte a `id`
El contrato publica todo lo necesario para generar un CRUD completo —tipos,
formatos, `enum`, `required`, `readOnly`, longitudes, mínimos/máximos— y hasta
revela **qué** claves foráneas existen, mediante las rutas de lectura de relación:

```
/api/miembros/{id}/membresia      →  miembros.membresia_id  →  membresias
/api/reservas/{id}/miembro        →  reservas.miembro_id    →  miembros
```

Lo que **no** publica es a qué **columna** apunta cada FK. Y el propio spec del
schema empuja a que algunas no apunten a `id`: el patrón `$user_id` exige
`"references": "user_id"`. Aquí ocurre en dos campos
(`reservas.miembro_id` y `clases.instructor_id`).

Consecuencia concreta: un selector de relación generado desde el contrato manda
el `id` de la fila elegida y **viola la clave foránea**, porque el destino
esperaba `user_id`. La UI genérica no tiene forma de saberlo y hubo que declarar
las dos excepciones a mano.

**Sugerencia:** exponerlo como extensión en el esquema de la propiedad, p. ej.
`x-appximo-references: "user_id"` junto a `x-appximo-relation: "miembros"`. Son
dos campos que ya existen en el schema compilado, y sin ellos toda herramienta
genérica —una UI, un generador de clientes, un importador— tiene un punto ciego
justo en el patrón que el framework recomienda.

### FE6 💚 El contrato alcanza para generar un back-office entero sin escribir una pantalla por recurso
Con `/openapi.json` solo se construyó un CRUD completo sobre los 7 recursos:
columnas, formularios con el `<input>` correcto por tipo, selectores para los
`enum`, orden, búsqueda, paginación y borrado. Sin una línea específica de
ningún recurso.

Y hay dos detalles que lo hacen posible y que merecen mención:

- **Los métodos publicados por ruta** dicen qué operaciones existen, así que los
  botones «crear / editar / borrar» se pintan o no según el contrato, no según
  una suposición.
- **El 403 sirve de descubrimiento de permisos.** Preguntando una fila de cada
  recurso, el menú se vuelve consciente del rol sin codificar la matriz:
  `recepcion` ve 4 de 7 recursos y `admin` los 7, y eso sale solo.

Los mensajes de error hacen el resto del trabajo: `422` con **todos** los campos
que fallan de una vez y su regla (`required`, `min`, `enum`), `409` nombrando la
columna duplicada o la tabla que aún referencia la fila
(`still referenced by "clases" record(s)`). Un formulario genérico puede pintarlos
tal cual porque están escritos para mostrarse.

---

## 11. Appximo Studio (editor de roles y puerta de deploy)

### ST1 🔴 La puerta de deploy rechaza el grant sobre `files` que la propia documentación ordena
Con el schema del inquilino cargado (Update existing → gimnasiodemo · 7
resources), el deploy se bloquea:

```
fix these before deploying
  role "instructor": permission over unknown resource "files"
  role "miembro":    permission over unknown resource "files"
```

El grant señalado es **exactamente el que `frontend-spec` §7.1 manda declarar**
para que un rol pueda subir archivos: `"files": {"actions": ["read","create"]}`.

El mismo schema, ante cada juez del ecosistema (verificado el mismo día):

| Juez | Veredicto |
|---|---|
| `appximo validate --json` | ✅ `{"valid": true}` |
| Arranque del motor (binario genérico y custom) | ✅ arranca y **aplica** el grant: subidas 201, roles sin grant 403 |
| Validador de Studio | ❌ `permission over unknown resource "files"` |

La causa es visible en el bundle del editor: su validador comprueba cada permiso
con `getEntityByName(...)` — es decir, solo contra los recursos declarados en el
schema. No conoce los recursos **virtuales** que el motor provee (`files` es el
file store). El mismo mensaje existe también dentro de `appximo.exe`, lo que
sugiere un camino de validación server-side con la misma laguna.

Lo grave no es el bloqueo sino la dirección en que empuja: "fix these" solo puede
obedecerse en Studio **borrando los grants** — lo que rompería la subida de fotos
del rol `miembro` y la lectura de archivos del `instructor` en el sistema vivo,
sin ningún aviso de esa consecuencia.

**Sugerencia:** los validadores deben compartir la lista de recursos virtuales
(o ser el mismo código servido a ambas superficies). Es el mismo patrón de `T1`
—reglas duplicadas que divergen— pero con más daño potencial, porque aquí el
veredicto equivocado es el que tiene el botón de Deploy.

### ST2 🟡 El editor de roles no puede REPRESENTAR ese grant, y lo omite sin decirlo
En el editor, la lista de recursos de un rol muestra solo los 7 declarados. El
rol `recepcion` cargado **tiene** `files` en su lista `resources` (está en el
schema del inquilino, verificado contra el registro) — pero no hay checkbox para
`files`: el grant no se pinta, no puede conservarse deliberadamente, y no genera
error (los errores de ST1 nombran solo a los roles con forma per-resource).

**No verificado:** si completar Guardar → Deploy elimina el grant al persistir
(no se ejecutó el Deploy para no romper el entorno). Pero la mitad observable ya
es problema: un grant legal, invisible en la única UI de edición.

### ST3 🟢 El diálogo de deploy repite la promesa que M1 desmintió
"editing existing resources (columns, validations, roles…) deploys live" — `M1`
demostró que una columna añadida en caliente se lee y escribe pero **no filtra**
hasta reiniciar. La afirmación vive ya en dos superficies (el mensaje de
`migrate` y este diálogo); al arreglar `M1` conviene barrer ambas.

### ST4 💚 La puerta en sí es el diseño correcto
Cargar el schema vivo del inquilino, validar **antes** de tocar nada, y prometer
vista previa de cada cambio con migración segura es exactamente el flujo que uno
quiere — y los mensajes nombran rol y recurso. El problema de ST1 es que uno de
sus jueces está desincronizado, no la arquitectura de la puerta.

---

## 12. Cosas que no estaban claras (y ahora sí)

Resumen práctico de lo que costó trabajo durante toda la evaluación — la lista
de preguntas que un usuario nuevo va a hacerse, con la respuesta que hoy hay que
descubrir a mano:

| Duda | Respuesta |
|---|---|
| ¿Por qué `appximo` no se reconoce si ya está en el PATH? | Explorer cachea el entorno; sin `WM_SETTINGCHANGE` ni reiniciar VS Code alcanza. Ver **W3** |
| ¿Appximo lee el `.env`? | **No.** Hay que expandirlo al entorno a mano. Ver **F1** |
| Cargué el `.env` y solo falla la primera variable | BOM al inicio del archivo. Ver **F1-bis** |
| ¿Qué necesita `serve` para arrancar? | `DATABASE_URL`, `JWT_SECRET` (32+), `ADMIN_KEY`. No están en `--help`. Ver **C3** |
| ¿`validate` o `validate-schema`? | Ambos; son chequeos distintos. Ver **C2** |
| ¿Dónde guarda sus archivos en Windows? | En `C:\var\lib\appximo\`. Ver **W1** |
| ¿Cómo se apunta un rol al usuario logueado? | Columna `user_id` en el catálogo + `"references": "user_id"` en la FK. Ver **S1** |
| ¿Cómo se registra un inquilino? | `POST /admin/tenants` con `X-Admin-Key` y **el schema entero en el body**. Ver **T2** |
| ¿Por qué la API me da 500 desde curl? | Falta el header `Host: <tenant>.localhost`. Ver **T3** |
| ¿Qué caracteres acepta un `tenant_id`? | Solo `^[a-z][a-z0-9]{1,29}$` — ni guiones ni guiones bajos. Ver **T1** |
| Migré un campo y no puedo filtrar por él | Hay que reiniciar el motor; leer/escribir sí funciona sin reiniciar. Ver **M1** |
| ¿Cómo se adjunta una foto? | `POST /api/files` (multipart, campo `file`) → `PATCH` del registro con el `file_id`. Ver **M5** |
| El `src` de la URL firmada no carga en local | Viene absoluta y sin puerto (`:80`); usar solo la ruta. Ver **M2** |
| ¿Se pueden añadir endpoints sin tocar el motor? | No: hay que compilar un binario propio en Go que importe la librería. Ver **§9** |
| Compilé mi binario y `/admin` está en blanco | Los assets del panel no vienen en el módulo. Ver **B1** |
| ¿Cómo entro al panel de administración? | `appximo admin create --email … --password …` (no está en las specs). Ver **B8** |
| Studio bloquea el deploy: «unknown resource "files"» | El grant es legal (el CLI y el motor lo aceptan); el validador de Studio no conoce recursos virtuales. **No borres los grants.** Ver **ST1** |
| ¿Dónde creo usuarios del inquilino? | Panel `/admin` → Users, o `POST /admin/tenants/{id}/users` con `X-Admin-Key`. Ver **T2**/**B8** |
| Cambié RBAC en Studio y no puedo desplegar | Vía que hoy funciona: editar `schema.json` → `validate` → `migrate` → reiniciar el binario. Ver **ST1**, **B7** |
| ¿Cómo autorizo una ruta custom? | Bloque `routes` en el rol, por el primer segmento tras `/api/`. Ver **B5** |

---

## 13. Propuesta: los primeros 10 minutos — el camino «wow»

(Propuesta de diseño, no hallazgo. Nace de haber medido dónde se fue el tiempo
real del ciclo completo, y responde a una pregunta concreta: si el framework
quisiera atrapar a cualquiera en los primeros 10 minutos, ¿qué tendría que
pasar?)

### El dato que la motiva

En esta evaluación, del arranque al primer registro visible pasó **~1 h 30 de
reloj** — y de eso, el schema (el corazón del producto, lo que Appximo hace
mejor) costó **minutos**. Todo lo demás fue fricción operable por un comando:
instalar y PATH, conseguir un Postgres, el `.env`, descubrir el alta de
inquilino (`T2`), el super-admin (`B8`).

Y la ironía que lo hace barato de arreglar: **el onboarding de producción ya es
mejor que el local.** `install.sh` genera los secretos, afina Postgres, registra
el servicio, imprime las URLs y hasta el comando de alta de inquilino con el
schema en el body (sesión 8: HTTPS vivo al primer intento). El primer contacto
local merece exactamente el mismo trato.

### La forma: `appximo up` — una orden, cero decisiones

```
$ appximo up --name gimnasio
  ✓ postgres: no DATABASE_URL — arrancando postgres:16 en Docker (appximo-pg)
  ✓ secretos generados → .env (0600, sin BOM) — y CARGADOS en este proceso
  ✓ schema: starter todo-api (cámbialo: appximo up --schema tuyo.json)
  ✓ inquilino «gimnasio» registrado con el schema
  ✓ primer admin creado — credenciales impresas UNA sola vez abajo
  ✓ sirviendo en :8080

  Tu app:      http://gimnasio.localhost:8080        (API + /docs + /app)
  Admin:       http://gimnasio.localhost:8080/admin  → admin@local / <clave>
  Editor:      http://gimnasio.localhost:8080/editor
  Token dev:   eyJ…   ·   Prueba:  curl -H "Authorization: Bearer …" …/api/tasks
```

Cada paso ataca una fricción medida en este reporte: escribe **y carga** el
`.env` (`F1`, `F1-bis`), auto-registra el inquilino (`T2`), crea el primer admin
(`B8`), resuelve Postgres sin preguntar dos veces (si hay `DATABASE_URL` la usa;
si hay Docker lo levanta; `--embedded-pg` para máquinas sin Docker), y usa rutas
por plataforma (`W1`).

### De la idea al schema: las piezas ya existen

`appximo ai-generate` ya está en el CLI («generate→validate→self-correct AI
loop»), igual que `blueprints`. Lo que falta es **cablearlo al arranque**:

```
appximo new "reservas de clases de un gimnasio"
  = ai-generate → validate --json (hasta valid:true) → up
```

Sin clave de API para `ai-generate`, imprime el prompt listo para el agente del
usuario (la pista B del Quick Start, que esta evaluación siguió entera).

### El vacío visual: un `/app` genérico embebido

En el minuto 8 nadie ha compilado un frontend. Hoy el primer contacto visual son
`/docs`, `/admin` y `/editor` — impresionantes, pero ninguno se siente como *tu
app*. La pieza que falta está especificada en el documento adjunto
**«PATRON-BACKOFFICE.md»**: una UI CRUD dirigida por `/openapi.json` que no sabe
nada de ningún recurso. Como es genérica, **un solo bundle preconstruido sirve
para todos los schemas** — se embebe en el binario igual que ya se embeben el
panel y Studio, y se monta en `/app`. Cero npm, cero build: tu app, visible, en
el minuto 3. (Con `x-appximo-references`, `x-appximo-transitions` y el marcado
de campos `file` del §6 de ese documento, ni siquiera necesita configuración.)

### El bloque de preguntas único

Lo que el humano (o el agente en su nombre) debe responder **una sola vez, todo
junto, al principio** — y nada más:

1. **¿Postgres?** — cadena de conexión si ya hay una; si no, permiso para
   levantarlo en Docker (o `--embedded-pg`).
2. **¿Cómo se llama la app / cuál es la idea?** — una frase; de ahí salen el
   nombre del inquilino y el schema.

Todo lo demás tiene default o se genera: secretos, puertos, roles del starter.
Las credenciales se imprimen una vez y no se vuelven a mostrar.

### Qué acelera a un agente (dicho por uno)

Reglas de DX-para-agentes que esta evaluación confirmó en carne propia:

- **Un solo bloque de preguntas al inicio.** Cada pregunta a mitad de camino
  rompe el flujo; cada default sensato lo mantiene.
- **Oráculos verificables por máquina en cada paso.** `validate --json` es el
  patrón perfecto — replicarlo: `appximo up --json` devolviendo URLs,
  credenciales y token en JSON, para leerlos sin parsear prosa.
- **stdout limpio en comandos de máquina** (`C1`): un solo byte de ruido
  convierte cada invocación en un ejercicio de filtrado.
- **Éxito definido como checklist ejecutable**, no como prosa: «/docs responde
  200, este curl devuelve 201, /app lista el registro». El agente sabe cuándo
  parar.
- **Nada de estado implícito**: cada comando dice qué escribió y dónde —
  `install.sh` ya lo hace; `up` debe heredarlo.

### El prompt que el Quick Start podría traer

```
Objetivo: API + admin + editor + back-office visual corriendo LOCALMENTE
para: <TU IDEA EN UNA FRASE>.

Antes de empezar hazme SOLO estas preguntas, juntas: (1) ¿cadena de conexión
de Postgres, o te doy permiso de levantarlo en Docker? (2) ¿nombre corto de
la app? Después no me preguntes más: usa defaults.

Luego: instala el binario (verifica checksum), genera el schema desde mi idea
con la gramática de `appximo spec`, corrígelo con `appximo validate --json`
hasta valid:true, arráncalo, registra el inquilino, crea el primer admin, y
entrégame: las URLs (/docs, /admin, /editor, /app), las credenciales, y un
curl de ejemplo QUE YA FUNCIONE contra un registro creado por ti.

Criterio de éxito: todo lo anterior responde y puedo editar el schema en
/editor y los datos en /admin sin tocar una terminal.
```

### El guion de los 10 minutos

| Minuto | Qué pasa | Con qué |
|---|---|---|
| 0–2 | instalar + `appximo up` | one-liner de instalación (`D1`) + `up` |
| 2–3 | «existe»: /docs, /admin, /editor abiertos | ya embebidos |
| 3–6 | «es MI app»: schema desde la idea, desplegado | `ai-generate`/agente + Studio o `migrate` |
| 6–8 | «la USO»: crear y editar registros visualmente | el `/app` genérico |
| 8–10 | «la INTEGRO»: token + curl + siguientes pasos | `token`, tarjeta final de `up` |

El deploy productivo **no** entra en los 10 minutos — es la sesión 2, y ya está
resuelto: esta evaluación lo midió en menos de 20 minutos con `install.sh`
(sesión 8). El primer acto vende; el segundo cumple.

### Por qué es barato

Ninguna pieza es nueva: la generación de secretos, el alta con schema y el
bootstrap del admin ya viven en `install.sh`; `ai-generate` y `blueprints` ya
están en el CLI; el panel y Studio ya viajan embebidos (el `/app` genérico es un
bundle más); y el patrón del back-office está especificado y verificado en el
documento adjunto. Igual que `C6`: **no hay que construirlo — hay que
orquestarlo.**

---

## Bitácora

### 2026-08-05 — Sesión 1: instalación, despliegue y primer schema
Instalación de v0.1.2 en Windows 11 (descarga, verificación de checksum, PATH),
PostgreSQL 16 en Docker sobre un droplet, diseño y validación de `schema.json`
(reservas de clases de gimnasio, 7 recursos), y primer arranque de `serve`
generando 31 rutas REST.

Hallazgos abiertos en esta sesión: `D1`–`D3`, `W1`–`W3`, `C1`–`C5`, `F1`,
`F1-bis`, `F2`, `S1`–`S4`, `I1`–`I3`.
Elogios: `F3`, `F4`, `S5`, `S6`.

Prioridad sugerida si el creador solo puede atacar tres: **C1** (ruido en stderr
rompe scripting), **F1** (`.env` no se carga), **W1** (rutas POSIX en Windows).
Las tres son de arreglo barato y alto impacto en el primer contacto.

### 2026-08-06 — Sesión 2: alta de inquilino y ejercicio de la API
Registro del inquilino `gimnasiodemo` (schema PostgreSQL `tenant_gimnasiodemo`),
carga de datos de catálogo, y verificación en runtime de la máquina de estados y
del índice único compuesto. 31 rutas REST sirviendo sobre el schema.

Hallazgos nuevos: `T1`–`T5`. Elogios: `T6`, `T7`.

**Lo que más costó, con diferencia:** descubrir cómo registrar un inquilino
(`T2`). El resto de la sesión —crear datos, filtrar, comprobar las garantías—
fluyó sin fricción una vez resuelto ese paso. La prioridad de la lista sube a
cuatro: **T2** se suma a las tres anteriores, porque bloquea a cualquiera que
instale el motor y no tenga acceso al README del repositorio.

### 2026-08-06 — Sesión 3: identificación del socio (foto + código) y migración en caliente
Se añadieron a `miembros` un campo `file` (`foto`, `accept: "image"`,
`max_bytes: 2 MiB`) y un `codigo_socio` único, más los permisos RBAC sobre el
recurso `files`. Migración aplicada sobre el motor en marcha con `--dry-run`
previo, y verificación del ciclo completo subir → adjuntar → mostrar.

Hallazgos nuevos: `M1`–`M3`, `M7`, `M8`. Elogios: `M4`–`M6`, `M9`, `M10`.
Se corrigió el alcance de `W1`: el almacén local es correcto por diseño; la
objeción es solo la ruta POSIX sin traducir en Windows.

(Durante la evaluación se escribió además una referencia técnica del subsistema
de archivos para uso interno del proyecto; todo lo accionable para el framework
está íntegro en la sección 8 de este reporte.)

**M1 es el más serio de toda la bitácora hasta ahora**, por encima de los 🔴
anteriores: no es fricción de arranque sino un fallo silencioso en caliente, con
los datos escribiéndose y leyéndose bien mientras el filtrado falla. Los demás
ítems cuestan tiempo; este puede llegar a producción sin que nadie lo note.

### 2026-08-06 — Sesión 4: backend custom en Go (retención y ocupación)
Se instaló Go 1.26.5, se compiló un binario propio que importa
`github.com/appximo/appximo v0.1.2` y registra dos rutas custom con los cómputos
que el schema no puede expresar: riesgo de fuga por socio (contra su propia línea
base de frecuencia) y ocupación con sobreventa calibrada por el no-show histórico.
Se sembraron 90 días de historial y se verificó la matriz RBAC completa.

Hallazgos nuevos: `B1`–`B3`, `B7`, `B8`. Elogios: `B4`–`B6`.

Se escribió la hoja de contrato de las rutas custom tal como pide backend-spec
§3.6b — el flujo completo que ese documento describe resultó ser completable de
punta a punta.

**Observación de conjunto:** esta fue la sesión con menos fricción de las cuatro,
y por un margen grande. La causa es concreta: `backend-spec` documenta una
superficie de API **cerrada y exacta**, y lo dice explícitamente ("si un método no
está aquí, el Ctx no lo tiene"). El contraste con `T2` —donde el alta de inquilino
no estaba documentada en ninguna spec y hubo que desminificar un bundle— sugiere
dónde está el trabajo pendiente de documentación: no en la capa de código, que
está muy bien cubierta, sino en la operativa.

**El bloque `B1`+`B7`+`B8` es, en conjunto, el problema más grave de la bitácora.**
Por separado parecen tres molestias; juntos describen que **usar la extensibilidad
del producto cuesta el panel del producto**. Un consumidor que sigue backend-spec
—el camino que el propio framework recomienda para el 10 % no declarativo— acaba
con `/admin` y `/editor` rotos, y el único rodeo exige dos procesos, dos puertos,
dos schemas divergentes y un bootstrap no documentado, con el riesgo de `B7`
encima. No es fricción de arranque como `C1` o `F1`: es una elección forzada entre
dos funciones centrales del producto.

Comparado con `M1` (el otro 🔴 estructural), `M1` es más insidioso —falla en
silencio— pero `B1` es más caro: se choca con él el primer día y no tiene arreglo
del lado del consumidor.

### 2026-08-06 — Sesión 5: frontend SvelteKit embebido en el binario
Panel operativo con cuatro pantallas (retención, clases, socios, reservas) más
login, en SvelteKit + `adapter-static` como SPA puro, embebido con `go:embed` y
servido por `Config.Static`. Se crearon los usuarios del inquilino por la API de
admin (el signup público está en 403 por defecto, como documenta §0.3).

Hallazgos nuevos: `FE1`, `FE4`, `FE5`. Elogios: `FE2`, `FE3`, `FE6`.
**Corregido `T4`**, que era erróneo: `.localhost` sí funciona en navegadores y en
curl; solo falla el resolutor DNS del sistema. Baja de 🟡 a 🟢.

**Observación de conjunto:** el `frontend-spec` fue el documento más fiable de los
tres. Todas las reglas de servido se cumplieron literalmente y dos de sus tres
trampas destacadas se materializaron en el primer intento. El único tropiezo
(`FE1`) fue un import omitido en un snippet.

Es también la sesión donde una afirmación mía llevaba tres sesiones sin
contrastar (`T4`). Vale como recordatorio de la regla de la casa: **una prueba
negativa con una sola herramienta no es evidencia de que algo no funcione.**

### 2026-08-06 — Sesión 6: Studio, la puerta de deploy y reorganización del reporte
Se ejercitó el editor de roles y la puerta de deploy de Studio con una edición
real (dar a `recepcion` lectura/creación/edición sobre `instructores`). La
puerta bloqueó el deploy por los grants de `files` (→ `ST1`), el editor resultó
no poder representar ese grant (→ `ST2`), y el registro del inquilino estaba
desactualizado respecto del schema de arranque (→ actualización de `B7`).

El cambio de RBAC se aplicó por la vía que hoy funciona —`schema.json` →
`validate` → `migrate` → reinicio— y quedó verificado: recepción sobre
instructores pasó de 403 a **200**, el resto de la matriz intacta
(salas 403, retención 200/403 según rol), y el registro del inquilino quedó en
v3 con las concesiones `routes` incluidas.

Hallazgos nuevos: `ST1`–`ST3`. Elogio: `ST4`. Actualizado: `B7`.
El reporte completo se reorganizó a formato entregable: portada con cobertura,
veredicto ejecutivo, tabla de prioridades unificada y registro de correcciones.

### 2026-08-06 — Sesión 7: contraste contra QUICKSTART.md
Se leyó por primera vez el Quick Start del repositorio — y resultó que la
evaluación entera había seguido su pista «With an agent» prompt por prompt (su
plantilla «register a tenant called <name>» explica hasta el nombre de inquilino
vacío de la sesión 2). Consecuencias sobre el reporte:

- **T5 retirado** — tercer artefacto de herramienta, no fallo del motor
  (comillas comidas por PowerShell al pasar JSON inline). Con el cuerpo desde
  archivo, el motor responde el error nombrado que el Quick Start documenta.
  Verificado.
- **T2 y B8 acotados** — ambos flujos están documentados en `QUICKSTART.md`; lo
  que falta es que la trilogía que imprime el binario los incluya
  (`appximo quickstart` los cerraría de una vez).
- **La pista Windows del Quick Start está marcada "NOT YET VERIFIED — please
  open an issue"** — las secciones 2–4 de este reporte son esa verificación.
- **Verificado de paso:** el 403 de signup en v0.1.2 sí nombra su switch, y
  además apunta a la alternativa: «set APPXIMO_AUTH_SIGNUP_ROLE … or have an
  admin create users in the admin panel (/admin → Users)». De la familia de
  `F3`: un error escrito para resolver, no para rechazar.

### 2026-08-06 — Sesión 8: deploy a producción (DigitalOcean + dominio + HTTPS)
Se desplegó el binario de consumidor en el droplet con `scripts/install.sh`:
PostgreSQL nativo afinado para la caja, unidad systemd, Caddy con Let's Encrypt,
hardening (`--harden`: ufw 22/80/443 + fail2ban + unattended-upgrades), secretos
generados en `/etc/appximo/appximo.env` (0600, sin imprimirlos). HTTPS quedó vivo
al primer intento en `api.appitools.com`; el inquilino de producción es `api`
(la primera etiqueta del Host). Verificado desde fuera: `/health` con la versión
del binario custom, el SPA en la raíz, login real y las dos rutas custom con su
RBAC.

Hallazgos de la sesión, todos a favor del producto:

- 💚 **El instalador está a la altura de los mejores mensajes del motor**: no
  interactivo, con `--dry-run` fiel, idempotente ("re-run the same command — it
  resumes"), detecta la caja pequeña y se auto-limita
  (`GOMEMLIMIT=287MiB`, `shared_buffers` afinado), y el flujo de binario de
  consumidor (`--binary` + `--cli` como compañero de operaciones, ADR-023) es
  primera clase, no un caso raro.
- 💚 El resumen final imprime **el alta de inquilino completa con el schema en
  el body** — la cura de `T2` ya existe aquí; solo falta que viva también en la
  trilogía imprimible.
- 🟢 Matiz de orden: el instalador verifica HTTPS **antes** del paso `--harden`
  que abre 80/443; en una caja cuyo ufw ya estaba activo solo con SSH, la
  emisión del certificado habría fallado. Se abrieron 80/443 a mano antes de
  ejecutarlo. Sugerencia: abrir los puertos del hardening antes de la
  verificación pública, o avisar.
- 🟢 `scp` desde Windows pierde el bit de ejecución del binario; el instalador
  lo detecta con un mensaje claro ("is not executable (chmod +x it)").
- **B1 en producción, corroborado dos veces**: se aceptó operar sin panel (vía
  API/CLI), igual que —verificado hoy— lo hacen las demos productivas del
  propio proyecto.

Quedó programado el backup diario (`/etc/cron.d/appximo-backup`) — el instalador
copia `backup.sh` pero no lo agenda (ENG-3, confirmado). El simulacro de restore
sigue pendiente, como el propio Quick Start advierte.

### 2026-08-06 — Sesión 9: dos propuestas nacidas del ciclo completo
Con el producto ya en producción, dos adiciones de documentación:

- **C6** — la propuesta del contrato de ciclo de vida imprimible para agentes:
  la mitad "construir" está magníficamente cubierta por la trilogía; la mitad
  "operar" se aprende tropezando. Agregar lo que ya existe disperso en un cuarto
  documento haría que el primer prompt de un agente abarque el ciclo entero.
- Se escribió aparte **«PATRON-BACKOFFICE.md»** (entregable que acompaña a este
  reporte): la receta completa del back-office generado desde `/openapi.json`
  —cero pantallas escritas a mano, sesión 5— documentada para que la
  documentación oficial pueda adoptarla, al estilo del Admin de API Platform,
  con la lista de extensiones del contrato que la harían de serie
  (`x-appximo-references` de `FE5`, `x-appximo-transitions`, marcar campos
  `file`, declarar recursos virtuales).

### 2026-08-06 — Sesión 10: la propuesta de los 10 minutos
Se añadió la **sección 13**: el diseño del camino «wow» de primer contacto —
`appximo up` (una orden, cero decisiones), `appximo new "<idea>"` cableando el
`ai-generate` que ya existe, el `/app` genérico embebido que cierra el vacío
visual sin npm, el bloque único de preguntas, las reglas de DX-para-agentes
dichas por un agente, y el prompt listo para el Quick Start. La tesis: el
onboarding de producción (`install.sh`) ya es mejor que el local; los 10 minutos
se consiguen orquestando piezas que ya existen, no construyendo nuevas.

---

## Nota metodológica

Evaluación realizada por Miguel Acosta con asistencia de un agente de IA
(Claude Code), usando el `QUICKSTART.md` del repositorio y los tres documentos
del producto (`spec`, `backend-spec`, `frontend-spec`) tal como el propio
framework propone: la pista «With an agent» del Quick Start se siguió prompt a
prompt, de la instalación (§1) al deploy en producción (§8) — el recorrido
completo tomó menos de 24 horas de reloj. Todos los comandos, respuestas HTTP y mensajes de error citados son
reales, se ejecutaron contra appximo v0.1.2 corriendo, y son reproducibles.
Cuando un intento falló por error del evaluador y no del producto, está dicho
(sección «Correcciones y retractaciones»).

El documento es un único archivo autocontenido, pensado para leerse de arriba
hacia abajo: la portada da el juicio, las secciones dan la evidencia, la
bitácora da el orden en que ocurrió todo.
