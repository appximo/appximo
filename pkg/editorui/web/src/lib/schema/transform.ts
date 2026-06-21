// Lossless conversion between the engine's MAP form (APISchema — what the engine
// consumes and `appitools validate` accepts) and the editor's array-shaped MODEL
// (SchemaModel). This is the conceptual sibling of the engine's MapToIR/IRToMap
// (pkg/aigen/ir.go): arbitrary-keyed maps (resources, fields, relations) become
// ordered arrays carrying their key as `name`, so the UI can address them by
// index and keep insertion order stable.
//
// ROUND-TRIP CONTRACT: modelToSchema(schemaToModel(x)) is semantically identical
// to x (key order aside). Unmodeled resource-level keys ride along in
// EntityModel.extras; rbac/workflows are preserved verbatim. Editor-only data
// (ids, canvas positions) is never emitted. The round-trip is property-tested in
// schema/transform.test.mts.

import type { APISchema, RBACPolicy, RelationDef, ResourceSchema } from '../types/schema';
import type { EntityModel, FieldModel, RelationModel, SchemaModel } from '../types/editor';
import { newId } from './ids';

// ── map → model ──────────────────────────────────────────────────────────────

export function schemaToModel(schema: APISchema): SchemaModel {
	const entities: EntityModel[] = [];
	const resources = schema.resources ?? {};
	for (const name of Object.keys(resources)) {
		entities.push(resourceToEntity(name, resources[name]));
	}
	return {
		$schema: schema.$schema ?? 'https://appitools.dev/schema/v1',
		version: schema.version ?? '1',
		name: schema.name ?? 'untitled-api',
		entities,
		rbac: schema.rbac ?? { roles: {} },
		workflows: schema.workflows
	};
}

function resourceToEntity(name: string, res: ResourceSchema): EntityModel {
	const fields: FieldModel[] = [];
	const fieldDefs = res.fields ?? {};
	for (const fname of Object.keys(fieldDefs)) {
		fields.push({ id: newId('f'), name: fname, def: { ...fieldDefs[fname] } });
	}

	const relations: RelationModel[] = [];
	const relDefs = res.relations ?? {};
	for (const rname of Object.keys(relDefs)) {
		relations.push({ id: newId('r'), name: rname, def: { ...relDefs[rname] } });
	}

	return {
		id: newId('ent'),
		name,
		fields,
		relations,
		extras: {
			indexes: res.indexes,
			foreign_keys: res.foreign_keys,
			hooks: res.hooks,
			events: res.events,
			renamed_from: res.renamed_from
		},
		position: { x: 0, y: 0 } // laid out by the store (dagre) on import
	};
}

// ── model → map ──────────────────────────────────────────────────────────────

export function modelToSchema(model: SchemaModel): APISchema {
	const resources: Record<string, ResourceSchema> = {};
	for (const ent of model.entities) {
		resources[ent.name] = entityToResource(ent);
	}

	const out: APISchema = {
		$schema: model.$schema,
		version: model.version,
		name: model.name,
		resources
	};
	if (model.rbac && Object.keys(model.rbac.roles ?? {}).length > 0) {
		out.rbac = cleanRBAC(model.rbac);
	}
	if (model.workflows && Object.keys(model.workflows).length > 0) {
		out.workflows = model.workflows;
	}
	return out;
}

function entityToResource(ent: EntityModel): ResourceSchema {
	const res: ResourceSchema = { fields: {} };
	for (const f of ent.fields) {
		res.fields[f.name] = cleanObject(f.def);
	}
	if (ent.relations.length > 0) {
		const rels: Record<string, RelationDef> = {};
		for (const r of ent.relations) rels[r.name] = cleanObject(r.def);
		res.relations = rels;
	}
	const x = ent.extras;
	if (x.indexes && x.indexes.length > 0) res.indexes = x.indexes;
	if (x.foreign_keys && x.foreign_keys.length > 0) res.foreign_keys = x.foreign_keys;
	if (x.hooks && Object.keys(x.hooks).length > 0) res.hooks = x.hooks;
	if (x.events && x.events.length > 0) res.events = x.events;
	if (x.renamed_from) res.renamed_from = x.renamed_from;
	return res;
}

function cleanRBAC(rbac: RBACPolicy): RBACPolicy {
	// RBAC rides through verbatim today (no RBAC visual editing yet); just drop
	// any undefined leaves so the JSON is clean.
	return cleanObject(rbac);
}

// ── helpers ──────────────────────────────────────────────────────────────────

/**
 * Returns a shallow-pruned copy of o with keys whose value is undefined / null /
 * '' / [] removed (the omitempty-equivalent so an edited def doesn't emit dead
 * keys). Nested objects/arrays are kept as-is (they were either parsed from JSON
 * — only present keys — or set deliberately by the store).
 */
export function cleanObject<T extends object>(o: T): T {
	const out: Record<string, unknown> = {};
	for (const [k, v] of Object.entries(o)) {
		if (v === undefined || v === null) continue;
		if (typeof v === 'string' && v === '') continue;
		if (Array.isArray(v) && v.length === 0) continue;
		out[k] = v;
	}
	// Sound: we only ever DROP keys (and never a required one — e.g. FieldDef.type
	// is never empty), so the result still satisfies T. The double cast is needed
	// because Record<string, unknown> doesn't structurally match a precise interface.
	return out as unknown as T;
}

/** Deep, key-order-insensitive structural equality (for round-trip verification). */
export function deepEqual(a: unknown, b: unknown): boolean {
	if (a === b) return true;
	if (typeof a !== typeof b) return false;
	if (a === null || b === null) return a === b;
	if (Array.isArray(a) || Array.isArray(b)) {
		if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
		return a.every((x, i) => deepEqual(x, b[i]));
	}
	if (typeof a === 'object' && typeof b === 'object') {
		const ao = a as Record<string, unknown>;
		const bo = b as Record<string, unknown>;
		const ak = Object.keys(ao);
		const bk = Object.keys(bo);
		if (ak.length !== bk.length) return false;
		return ak.every((k) => Object.prototype.hasOwnProperty.call(bo, k) && deepEqual(ao[k], bo[k]));
	}
	return false;
}
