# Back-office generado desde el contrato — patrón, receta y propuesta

**Qué demuestra este documento:** sobre un backend Appximo se construyó un
back-office CRUD completo —7 recursos: tablas, formularios, validación, permisos,
orden, búsqueda y paginación— **sin escribir una sola pantalla específica de
ningún recurso**. Todo se deriva en runtime de `/openapi.json`. Si el schema
cambia y se redespliega, la UI se adapta sola.

Es el mismo movimiento que hace API Platform con su Admin (React-Admin leyendo
Hydra/OpenAPI), pero aquí sale **más barato y más completo**, porque el contrato
que publica Appximo es inusualmente rico y sus errores están escritos para
mostrarse. Este documento es la receta completa, verificada contra un motor
v0.1.2 corriendo, propuesta para que la documentación oficial la adopte.

**Por qué esto le conviene a Appximo especialmente:** el panel `/admin` embebido
no viaja en el módulo de Go (los consumidores que compilan binario propio lo
pierden — hallazgo B1 del reporte de evaluación). Un back-office generado desde
el contrato **dentro del SPA del propio consumidor** es la alternativa soberana:
vive en su binario, con su tema, gobernado por el RBAC de su schema, sin proceso
aparte.

---

## 1. La idea en una línea

```
schema.json ──(motor)──▶ /openapi.json ──(runtime, fetch)──▶ tablas + formularios
```

La UI no sabe nada de "miembros" ni "reservas". Sabe leer un contrato.

## 2. Lo que el contrato ya publica (y alcanza para el ~95 %)

Verificado contra `/openapi.json` de un motor real:

| Del contrato | Se convierte en |
|---|---|
| `components.schemas.<Recurso>` → `properties` con `type`, `format`, `enum`, `maxLength`, `minimum`, `maximum`, `readOnly` | el control correcto por campo, con sus límites nativos |
| `required` (del `<Recurso>Input`) | asteriscos y orden del formulario |
| métodos publicados por ruta (`get/post` en colección, `get/patch/put/delete` en item) | qué botones existen: crear / editar / borrar por recurso |
| rutas de lectura de relación `/api/{r}/{id}/{relacion}` | **descubrimiento de las claves foráneas** y de su recurso destino |
| `x-appximo-custom-route: true` | separar rutas custom del CRUD |
| el RBAC (respondiendo 403) | qué recursos ofrece el menú a este rol — sin codificar la matriz |

Ejemplo real (`Miembros`):

```
codigo_socio   string   max=20
documento      string   (required)
email          string   format=email
foto           string   format=uuid
id             string   format=uuid readOnly
membresia_id   string   format=uuid        ← /api/miembros/{id}/membresia revela la FK
nombre         string   max=120 (required)
```

## 3. La receta

### 3.1 El lector del contrato

Un módulo (~150 líneas) convierte `/openapi.json` en el modelo que la UI dibuja.
Versión de referencia (JavaScript, sin dependencias):

```js
// contrato.js — /openapi.json ──▶ modelo de UI. Nada escrito a mano por recurso.
import { api } from './api.js';

let cache = null;
const NO_RECURSOS = new Set(['files', 'ocupacion', 'retencion']); // rutas del motor que no son CRUD del schema

// ÚNICO punto ciego del contrato (ver §4): a qué COLUMNA apunta cada FK.
const REFERENCIA = {
  'reservas.miembro_id': 'user_id',
  'clases.instructor_id': 'user_id'
};

const singularAPlural = (s, recursos) =>
  [s + 's', s + 'es', s].find((c) => recursos.includes(c)) ?? null;

export async function cargarContrato() {
  if (cache) return cache;
  const doc = await api('/openapi.json');

  const rutas = Object.keys(doc.paths ?? {});
  const nombres = rutas
    .filter((p) => /^\/api\/[a-z_]+$/.test(p))
    .map((p) => p.slice(5))
    .filter((n) => !NO_RECURSOS.has(n))
    .sort();

  // Las rutas /api/{recurso}/{id}/{relacion} revelan cada clave foránea.
  const relaciones = {};
  for (const p of rutas) {
    const m = p.match(/^\/api\/([a-z_]+)\/\{id\}\/([a-z_]+)$/);
    if (m && !NO_RECURSOS.has(m[1])) (relaciones[m[1]] ??= []).push(m[2]);
  }

  const recursos = nombres.map((nombre) => {
    const Pascal = nombre[0].toUpperCase() + nombre.slice(1);
    const lectura = doc.components?.schemas?.[Pascal];
    const entrada = doc.components?.schemas?.[Pascal + 'Input'];
    const mCol = Object.keys(doc.paths['/api/' + nombre] ?? {});
    const mItem = Object.keys(doc.paths[`/api/${nombre}/{id}`] ?? {});
    const requeridos = new Set(entrada?.required ?? lectura?.required ?? []);

    const campos = Object.entries(lectura?.properties ?? {}).map(([clave, p]) => {
      const rel = (relaciones[nombre] ?? []).find((r) => clave === r + '_id');
      return {
        clave,
        tipo: p.type, formato: p.format ?? null, enum: p.enum ?? null,
        maxLength: p.maxLength ?? null, minimum: p.minimum ?? null, maximum: p.maximum ?? null,
        soloLectura: !!p.readOnly || clave === 'id',
        requerido: requeridos.has(clave),
        relacion: rel ? singularAPlural(rel, nombres) : null,
        referencia: REFERENCIA[`${nombre}.${clave}`] ?? 'id',
        automatico: clave === 'created_at' || clave === 'updated_at'
      };
    });

    return {
      nombre,
      titulo: nombre[0].toUpperCase() + nombre.slice(1).replace(/_/g, ' '),
      campos,
      puedeCrear: mCol.includes('post'),
      puedeEditar: mItem.includes('patch'),
      puedeBorrar: mItem.includes('delete')
    };
  });

  cache = { recursos, porNombre: Object.fromEntries(recursos.map((r) => [r.nombre, r])) };
  return cache;
}
```

### 3.2 El mapeo campo → control

```js
export function tipoDeInput(c) {
  if (c.enum) return 'select';
  if (c.relacion) return 'relacion';          // <select> poblado del recurso destino
  if (c.tipo === 'boolean') return 'checkbox';
  if (c.formato === 'date-time') return 'datetime-local';
  if (c.formato === 'email') return 'email';
  if (c.tipo === 'integer' || c.tipo === 'number') return 'number';
  if (c.maxLength && c.maxLength > 200) return 'textarea';
  return 'text';
}
```

`maxlength`, `min` y `max` del input salen del contrato — la validación nativa
del navegador y la del servidor cuentan la misma historia.

### 3.3 Permisos por sonda: el 403 responde por ti

La matriz de roles **no se codifica**. Se pregunta:

```js
// Una fila de cada recurso; deny-by-default hace el resto.
try {
  const r = await papi(`/api/${recurso.nombre}?per_page=1&count=true`);
  estado[recurso.nombre] = { total: r.meta?.total ?? 0 };
} catch (e) {
  estado[recurso.nombre] = e.status === 403 ? { denegado: true } : { error: true };
}
```

Resultado medido: `recepcion` ve 4 de 7 recursos, `admin` los 7 — sin una línea
de configuración. Los recursos denegados se muestran atenuados, no ocultos: el
usuario entiende que existen y que su rol no llega.

### 3.4 El formulario genérico — las cinco reglas que importan

Un solo componente sirve para todos los recursos. Lo no obvio, aprendido contra
el motor real:

1. **Al CREAR, omite los campos vacíos.** `required` en el motor significa
   «presente y no nulo»: enviar `""` lo **pasa** y crea un registro en blanco;
   **omitir la clave** es lo que dispara el 422 correcto. (frontend-spec §4.6 —
   la trampa del formulario vacío.)
2. **Al EDITAR, PATCH parcial** — valida solo lo enviado. Y los números van como
   números JSON: el update rechaza `"7"` aunque el create lo tolere.
3. **`null` explícito limpia** un campo anulable (quitar una FK, una foto).
4. **El 422 trae TODOS los campos que fallan de una vez** con
   `{field, rule, message}`: se pintan todos en una pasada sobre su input, con
   scroll al primero. El 409 (duplicado, conflicto) **jamás descarta el trabajo
   del usuario** — banner honesto y el formulario intacto.
5. **Máquinas de estado: ofrecer solo transiciones legales.** El contrato aún no
   las publica (ver §6), así que se espeja la del schema en un mapa
   `SIGUIENTES = { solicitada: ['confirmada','en_espera','cancelada'], … }` y el
   `<select>` de estado ofrece `[estadoActual, ...SIGUIENTES[estadoActual]]`.
   El 422 de transición ilegal se maneja igualmente: dos operadores pueden
   cruzarse, y entonces se recarga la fila.

Esqueleto del envío:

```js
const cuerpo = {};
for (const c of campos) {
  const v = limpiar(valores[c.clave], c);   // coerción numérica, ISO en fechas, '' → null
  if (!editando && (v === null || v === '')) continue;  // regla 1
  cuerpo[c.clave] = v;
}
try {
  const r = editando
    ? await papi(`/api/${recurso.nombre}/${fila.id}`, { method: 'PATCH', body: cuerpo })
    : await papi(`/api/${recurso.nombre}`,             { method: 'POST',  body: cuerpo });
  alGuardar(r);
} catch (e) {
  if (e.status === 422 && e.fields.length) marcarCampos(e.fieldMap());   // regla 4
  else if (e.status === 409) errorGeneral = e.message;                   // trabajo intacto
  else /* red / 403 / 500: mensaje según §5 del frontend-spec */;
}
```

### 3.5 Las listas

- **Columnas por heurística**: primeras N no-objeto, priorizando
  `nombre/titulo/codigo/documento/estado/email`. Suficiente como defecto; se
  personaliza por override (§5).
- **Orden**: un solo campo (`?sort=x&order=asc` — el motor rechaza `a,b` con un
  400 que lista los ordenables; ese 400 es un bug de la UI, no del usuario).
- **Búsqueda**: `?search=` sobre los campos de texto; **paginación** guiada por
  `meta.has_next/has_prev/total`.
- **Borrado**: confirmación en dos clics; el 409 de borrado referenciado se
  muestra tal cual — el motor nombra la tabla que referencia
  (`still referenced by "clases" record(s)`), y ese mensaje ya está escrito para
  el usuario final.

## 4. El único punto ciego del contrato: `references`

El OpenAPI publica **qué** FKs existen, pero no **a qué columna** apuntan. Y el
patrón `$user_id` del propio framework exige FKs que no apuntan a `id`
(`"references": "user_id"`). Un selector genérico que envíe el `id` de la fila
elegida **viola la FK** en esos campos.

Hoy se resuelve con un mapa manual de excepciones (`REFERENCIA` en §3.1) — dos
entradas en esta app. Es el hallazgo `FE5` del reporte de evaluación, con la
petición concreta: publicar `x-appximo-references` junto a la propiedad.

## 5. Cómo se amplía (el movimiento de API Platform Admin)

El defecto generado cubre el 95 %; el 5 % restante se declara, no se reescribe.
Un registro de overrides por recurso mantiene la casa en orden:

```js
export const OVERRIDES = {
  miembros: {
    columnas: ['codigo_socio', 'nombre', 'documento', 'activo'],  // fija la lista
    etiquetas: { codigo_socio: 'Carnet' },
    celdas:   { foto: (fila) => AvatarFirmado },   // componente propio por celda
    widgets:  { foto: EditorFoto },                // widget propio por campo del form
    acciones: [{ etiqueta: 'Exportar CSV', handler: exportarMiembros }]
  }
};
```

La pantalla genérica consulta `OVERRIDES[recurso] ?? {}` en cada punto de
decisión. Así, personalizar un recurso **no** lo saca del sistema generado: sigue
heredando formularios, errores y permisos, y solo pisa lo declarado. El tema
visual es el del propio SPA (tokens CSS) — nada que ver con el look del panel
stock.

## 6. Lo que el motor podría añadir para que esto sea de serie

En orden de valor, para el creador del framework:

1. **`x-appximo-references`** en la propiedad de cada FK (cierra §4 — sin esto,
   toda herramienta genérica tropieza justo en el patrón que el framework
   recomienda).
2. **`x-appximo-transitions`** en los campos con máquina de estados. El
   frontend-spec ya reconoce que «they are not in the OpenAPI»; publicarlas
   elimina el espejo manual `SIGUIENTES`, el único conocimiento del dominio que
   esta UI aún lleva dentro.
3. **Marcar los campos `file`**. Hoy `foto` llega como `string format=uuid`,
   indistinguible de una FK cualquiera; un `x-appximo-file: true` (con su
   `accept`/`max_bytes`) permitiría generar el widget de subida completo
   (el flujo subir → adjuntar con sus dos capas de error ya está especificado
   en frontend-spec §7).
4. **Declarar los recursos virtuales** (`files`) en el contrato — la misma
   laguna que hace fallar al validador de Studio (`ST1`) le impide a una UI
   genérica ofrecer gestión de archivos.
5. **Empaquetarlo**: o un cuarto documento imprimible
   (`appximo backoffice-spec`) con esta receta —pegable a un agente igual que la
   trilogía—, o un scaffold (`appximo init --backoffice`) que genere el lector
   del contrato, el formulario genérico y la pantalla de lista listos para
   personalizar. Con los puntos 1–4 publicados, el generado deja de necesitar
   siquiera el mapa de excepciones.

## 7. Límites honestos del patrón

- Campos `jsonb`: hoy, un textarea de JSON crudo. Un editor estructurado es
  trabajo específico.
- Sin acciones masivas ni export por defecto (extensiones naturales vía §5).
- La subida de archivos no está generalizada (punto 3 de §6); en esta app el
  widget de foto es un override.
- La sonda de permisos cuesta una petición por recurso al abrir el índice —
  irrelevante en un back-office, medible si hubiera cientos de recursos.

---

*Escrito a partir de una implementación real y verificada (SvelteKit, ~500
líneas totales entre lector, formulario y listas) sobre appximo v0.1.2. Los
detalles de errores y semántica citados están contrastados con el motor
corriendo, no con la documentación.*
