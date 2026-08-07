// contract.js — /openapi.json ──▶ the UI model. The complete reference reader
// of backoffice-spec §3: NOTHING here names a resource, maps an FK exception,
// mirrors a state machine or lists "which uuids are files" — every one of
// those was a pre-extension blind spot and every one now comes from the
// contract itself (x-appximo-*). This is the embedded /app copy; the teaching
// copy consumers adapt lives in examples/backoffice-guide/web/contract.js.

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

    const fields = Object.entries(read?.properties ?? {}).map(([key, p]) => ({
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
      auto: key === 'created_at' || key === 'updated_at' || (p.format === 'date-time' && !!p.readOnly),
    }));

    return {
      name, fields,
      title: name[0].toUpperCase() + name.slice(1).replace(/_/g, ' '),
      canCreate: colMethods.includes('post'),
      canEdit: itemMethods.includes('patch'),
      canDelete: itemMethods.includes('delete'),
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
  if (f.format === 'date-time') return 'datetime-local';
  if (f.format === 'email') return 'email';
  if (f.type === 'integer' || f.type === 'number') return 'number';
  if (f.maxLength && f.maxLength > 200) return 'textarea';
  return 'text';
}
