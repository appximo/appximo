// contract.js — /openapi.json ──▶ the UI model. The complete reference reader
// of backoffice-spec §3: NOTHING here names a resource, maps an FK exception,
// mirrors a state machine or lists "which uuids are files" — every one of
// those was a pre-extension blind spot and every one now comes from the
// contract itself (x-appximo-*). This is the embedded /app copy; the teaching
// copy consumers adapt lives in examples/backoffice-guide/web/contract.js.
//
// APP-VITRINA-S1 added the derived, still-generic helpers the redesigned
// screens need: the ORDERED state list of a state machine (initial states
// first, then breadth-first along the declared transitions — the order that
// gives lifecycle chips their color BY POSITION and the board its columns),
// the human label of a field key, and the display label of a related row.

let cache = null;

export async function loadContract(fetchJSON) {
  if (cache) return cache;
  const doc = await fetchJSON('/openapi.json');
  const paths = doc.paths ?? {};
  const virtualDecl = doc['x-appximo-virtual-resources'] ?? {};
  const virtual = new Set(Object.keys(virtualDecl));

  const names = Object.keys(paths)
    .filter((p) => /^\/api\/[a-z_]+$/.test(p))
    .map((p) => p.slice(5))
    .filter((n) => !virtual.has(n))
    .filter((n) => !Object.values(paths['/api/' + n] ?? {})
      .some((op) => op && typeof op === 'object' && op['x-appximo-custom-route']))
    .sort();

  const resources = names.map((name) => {
    const Pascal = name.replace(/(^|_)([a-z])/g, (_, __, c) => c.toUpperCase());
    const read = doc.components?.schemas?.[Pascal];
    const input = doc.components?.schemas?.[Pascal + 'Input'];
    const colMethods = Object.keys(paths['/api/' + name] ?? {});
    const itemMethods = Object.keys(paths[`/api/${name}/{id}`] ?? {});
    const required = new Set(input?.required ?? read?.required ?? []);

    const fields = Object.entries(read?.properties ?? {}).map(([key, p]) => {
      const f = {
        key,
        type: p.type ?? null, format: p.format ?? null, enum: p.enum ?? null,
        maxLength: p.maxLength ?? null, minimum: p.minimum ?? null, maximum: p.maximum ?? null,
        default: p.default ?? null,
        readOnly: !!p.readOnly || key === 'id',
        required: required.has(key),
        relation: p['x-appximo-relation'] ?? null,
        references: p['x-appximo-references'] ?? 'id',
        file: p['x-appximo-file'] === true,
        accept: p['x-appximo-accept'] ?? null,
        maxBytes: p['x-appximo-max-bytes'] ?? null,
        transitions: p['x-appximo-transitions'] ?? null,
        initialStates: p['x-appximo-initial'] ?? null,
        // Engine-managed: read from the CONTRACT (x-appximo-auto names the role,
        // and auto fields carry readOnly) — never guessed from English field
        // names, which rendered a Spanish `modificado_en` as an editable field
        // the engine then rejected 422 read_only on save (SILENT-CORRUPTION-S1).
        auto: p['x-appximo-auto'] !== undefined || (p.format === 'date-time' && !!p.readOnly),
        // A jsonb column is published as a TYPE-LESS property (any JSON document,
        // described as such); json (TEXT) is a plain string. Structural again.
        json: p.type === undefined && !p.enum && !p.format && !p['x-appximo-relation'],
      };
      f.label = labelFor(f);
      f.states = f.transitions ? orderedStates(f) : null;
      return f;
    });

    return {
      name, fields,
      title: name[0].toUpperCase() + name.slice(1).replace(/_/g, ' '),
      canCreate: colMethods.includes('post'),
      canEdit: itemMethods.includes('patch'),
      canDelete: itemMethods.includes('delete'),
      stateField: fields.find((f) => f.transitions) ?? null,
    };
  });

  cache = {
    appTitle: doc.info?.title ?? 'app',
    resources,
    byName: Object.fromEntries(resources.map((r) => [r.name, r])),
    virtual: virtualDecl,
  };
  return cache;
}

// backoffice-spec §4 — the field → control mapping.
export function controlFor(f) {
  if (f.transitions) return 'state';
  if (f.enum) return 'select';
  if (f.file) return 'file';
  if (f.relation) return 'relation';
  if (f.type === 'boolean') return 'checkbox';
  if (f.type === 'object' || f.json) return 'json';
  if (f.format === 'date-time') return 'datetime-local';
  if (f.format === 'email') return 'email';
  if (f.type === 'integer' || f.type === 'number') return 'number';
  if (f.maxLength && f.maxLength > 200) return 'textarea';
  if (f.type === 'string' && !f.maxLength && !f.format) return 'textarea-short';
  return 'text';
}

// The lifecycle order of a state machine: initial states (declared order),
// then every state reachable from them breadth-first, then any declared
// state nothing reaches. Purely structural — the names never matter.
export function orderedStates(f) {
  const t = f.transitions ?? {};
  const out = [];
  const seen = new Set();
  const push = (s) => { if (!seen.has(s)) { seen.add(s); out.push(s); } };
  const queue = [].concat(f.initialStates ?? []);
  queue.forEach(push);
  while (queue.length) {
    const s = queue.shift();
    for (const n of t[s] ?? []) if (!seen.has(n)) { push(n); queue.push(n); }
  }
  for (const s of Object.keys(t)) push(s);
  for (const s of f.enum ?? []) push(s);
  return out;
}

export function isTerminal(f, state) {
  return Array.isArray(f.transitions?.[state]) && f.transitions[state].length === 0;
}

// A human label from a key: `fecha_de_entrega` → "Fecha de entrega",
// `cliente_id` (a relation) → "Cliente". Letter-case only — never a translation.
export function labelFor(f) {
  let k = f.key;
  if ((f.relation || f.file) && k.endsWith('_id')) k = k.slice(0, -3);
  k = k.replace(/_(centavos|cents)$/, '');   // money is int64 in the minor unit; the value is rendered as money
  k = k.replace(/_/g, ' ').trim();
  return k ? k[0].toUpperCase() + k.slice(1) : f.key;
}

// The display label of a related row: the first "name-like" text field, else
// the referenced column. A naming CONVENTION (like the spec's §7), not a
// resource list.
const NAMEISH = ['nombre', 'name', 'titulo', 'title', 'razon_social', 'codigo', 'code', 'numero', 'email', 'radicado', 'sku', 'asunto'];
export function rowLabel(row, refCol = 'id') {
  if (!row) return '';
  for (const k of NAMEISH) if (row[k] != null && row[k] !== '') return String(row[k]);
  const firstText = Object.entries(row).find(([k, v]) => typeof v === 'string' && k !== 'id' && !k.endsWith('_id') && !/^\d{4}-\d{2}-\d{2}T/.test(v) && v.length < 80);
  if (firstText) return firstText[1];
  return String(row[refCol] ?? row.id ?? '').slice(0, 8);
}

// The "title" field of a resource: the field a human scans first — a
// name-like key if there is one, else the first REQUIRED free-text field,
// else the first free-text field. Used for the list's primary column, the
// board card title, the drawer title and the label of a related row.
export function titleField(res) {
  const pref = (k) => { const i = NAMEISH.findIndex((p) => k === p || k.includes(p)); return i === -1 ? 99 : i; };
  const cands = res.fields
    .map((f, i) => [f, i])
    .filter(([f]) => !f.readOnly && !f.file && !f.relation && f.type === 'string' && !f.enum && !f.transitions && !f.format);
  cands.sort((a, b) => (pref(a[0].key) - pref(b[0].key)) || ((b[0].required ? 1 : 0) - (a[0].required ? 1 : 0)) || (a[1] - b[1]));
  return cands[0]?.[0] ?? null;
}
