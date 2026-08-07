// Lossless conversion between the engine's MAP form (APISchema — what the engine
// consumes and `appximo validate` accepts) and the editor's array-shaped MODEL
// (SchemaModel). This is the conceptual sibling of the engine's MapToIR/IRToMap
// (pkg/aigen/ir.go): arbitrary-keyed maps (resources, fields, relations) become
// ordered arrays carrying their key as `name`, so the UI can address them by
// index and keep insertion order stable.
//
// ROUND-TRIP CONTRACT: modelToSchema(schemaToModel(x)) is semantically identical
// to x (key order aside). Unmodeled resource-level keys ride along in
// EntityModel.extras; rbac — BOTH rbac.roles and the anonymous rbac.public
// block (ADR-026, UI-2) — and workflows are preserved. Editor-only data
// (ids, canvas positions) is never emitted. `renamed_from` (resource + field) is
// NOT stored raw: import lifts it into the model's `originalName` baseline and
// export derives it back (originalName !== name ⇒ renamed_from=originalName), so
// the identity holds AND session renames chain correctly from the deployed
// baseline (UI-F4-S1).

import type {
	APISchema,
	Condition,
	HookConfig,
	RBACPolicy,
	RelationDef,
	ResourcePermission,
	ResourceSchema,
	RolePolicy
} from '../types/schema';
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
		$schema: schema.$schema ?? 'https://appximo.com/schema/v1',
		version: schema.version ?? '1',
		name: schema.name ?? 'untitled-api',
		entities,
		rbac: normalizeRBAC(schema.rbac),
		workflows: schema.workflows
	};
}

// normalizeRBAC keeps rbac.roles AND rbac.public (UI-2 — public used to survive
// parse only by accident and was then dropped at serialize). `roles` is always
// materialized (a public-only schema has none, and the store indexes
// rbac.roles unconditionally); `public` rides along only when present.
function normalizeRBAC(rbac?: RBACPolicy): RBACPolicy {
	if (!rbac) return { roles: {} };
	const out: RBACPolicy = { roles: rbac.roles ?? {} };
	if (rbac.public) out.public = rbac.public;
	return out;
}

function resourceToEntity(name: string, res: ResourceSchema): EntityModel {
	const fields: FieldModel[] = [];
	const fieldDefs = res.fields ?? {};
	for (const fname of Object.keys(fieldDefs)) {
		// renamed_from is lifted OUT of the def into the baseline (originalName):
		// export derives it back, so the round-trip is identical and a session
		// rename CHAINS from the declared baseline instead of an intermediate name.
		const { renamed_from, ...def } = fieldDefs[fname];
		fields.push({ id: newId('f'), name: fname, def, originalName: renamed_from ?? fname });
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
			events: res.events
		},
		position: { x: 0, y: 0 }, // laid out by the store (dagre) on import
		originalName: res.renamed_from ?? name
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
	if (
		model.rbac &&
		(Object.keys(model.rbac.roles ?? {}).length > 0 ||
			Object.keys(model.rbac.public ?? {}).length > 0)
	) {
		out.rbac = cleanRBACPolicy(model.rbac);
	}
	if (model.workflows && Object.keys(model.workflows).length > 0) {
		out.workflows = model.workflows;
	}
	return out;
}

function entityToResource(ent: EntityModel): ResourceSchema {
	const res: ResourceSchema = { fields: {} };
	for (const f of ent.fields) {
		const def = cleanObject(f.def);
		// Derive renamed_from from the baseline (UI-F4-S1): emitted only when the
		// name changed against the deployed baseline, so the engine's ALTER … RENAME
		// COLUMN preserves the data (MIG-F1-S2). A session-created field has no
		// baseline (undefined) and never emits one; renaming back to the baseline
		// emits nothing (no-op).
		if (f.originalName && f.originalName !== f.name) def.renamed_from = f.originalName;
		else delete def.renamed_from;
		res.fields[f.name] = def;
	}
	if (ent.relations.length > 0) {
		const rels: Record<string, RelationDef> = {};
		for (const r of ent.relations) rels[r.name] = cleanObject(r.def);
		res.relations = rels;
	}
	const x = ent.extras;
	if (x.indexes && x.indexes.length > 0) res.indexes = x.indexes;
	if (x.foreign_keys && x.foreign_keys.length > 0) res.foreign_keys = x.foreign_keys;
	if (x.hooks && Object.keys(x.hooks).length > 0) {
		// Prune each hook's empty optional fields (a webhook never keeps a stale ""
		// script, etc.) so the exported schema is engine-clean and minimal.
		const hooks: Record<string, HookConfig> = {};
		for (const [ev, h] of Object.entries(x.hooks)) hooks[ev] = cleanObject(h);
		res.hooks = hooks;
	}
	if (x.events && x.events.length > 0) res.events = x.events;
	// Table-level rename intent, derived from the baseline exactly like fields.
	if (ent.originalName && ent.originalName !== ent.name) res.renamed_from = ent.originalName;
	return res;
}

// cleanRBACPolicy normalizes the edited RBAC into the exact, minimal shape the
// engine accepts (UI-F2-S1): each role is serialized in ONE form only (per-resource
// `permissions` when non-empty, else role-global), empty arrays/conditions are
// dropped, and the row-condition op is pinned to "eq" (the only operator the engine
// enforces — SEC-AUDIT-V1). Faithful round-trip: a role with no empties (e.g. the
// erp-demo roles) is reproduced identically. The anonymous `public` block
// (ADR-026) is re-emitted when non-empty — each grant cleaned like a permission
// but NEVER carrying condition_actions (the engine rejects it on a public grant:
// read is the only action, there is no subset to scope). A roles-less,
// public-only policy emits `public` alone (no invented empty `roles`), and a
// policy with no public block emits none (byte-stability for existing schemas).
function cleanRBACPolicy(rbac: RBACPolicy): RBACPolicy {
	const roles: Record<string, RolePolicy> = {};
	for (const [name, role] of Object.entries(rbac.roles ?? {})) {
		roles[name] = cleanRole(role);
	}
	const out = {} as RBACPolicy;
	if (Object.keys(roles).length > 0) out.roles = roles;
	const pub: Record<string, ResourcePermission> = {};
	for (const [res, p] of Object.entries(rbac.public ?? {})) {
		pub[res] = cleanPermission(p, { allowConditionActions: false });
	}
	if (Object.keys(pub).length > 0) out.public = pub;
	return out;
}

function cleanRole(role: RolePolicy): RolePolicy {
	const perms = role.permissions ?? {};
	// Per-resource form wins when it has entries; role-global keys are dropped
	// (the two forms are mutually exclusive in the engine).
	if (Object.keys(perms).length > 0) {
		const permissions: Record<string, ResourcePermission> = {};
		for (const [res, p] of Object.entries(perms)) permissions[res] = cleanPermission(p);
		return { permissions };
	}
	const out: RolePolicy = {};
	if (role.resources === '*') out.resources = '*';
	else if (Array.isArray(role.resources) && role.resources.length > 0) out.resources = [...role.resources];
	if (role.actions && role.actions.length > 0) out.actions = [...role.actions];
	const cond = cleanCondition(role.conditions);
	if (cond) out.conditions = cond;
	if (role.fields && role.fields.length > 0) out.fields = [...role.fields];
	return out;
}

function cleanPermission(
	p: ResourcePermission,
	opts: { allowConditionActions: boolean } = { allowConditionActions: true }
): ResourcePermission {
	const out: ResourcePermission = { actions: [...(p.actions ?? [])] };
	const cond = cleanCondition(p.conditions);
	if (cond) {
		out.conditions = cond;
		// condition_actions is meaningless without a condition (the engine rejects
		// it) — and never valid at all on a public grant (allowConditionActions=false).
		if (opts.allowConditionActions && p.condition_actions && p.condition_actions.length > 0) {
			out.condition_actions = [...p.condition_actions];
		}
	}
	if (p.fields && p.fields.length > 0) out.fields = [...p.fields];
	return out;
}

function cleanCondition(c?: Condition): Condition | undefined {
	if (!c || !c.field) return undefined; // a condition with no field is dropped
	return { field: c.field, op: 'eq', val: c.val ?? '' }; // op is always eq
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
