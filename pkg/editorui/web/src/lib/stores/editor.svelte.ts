// The central editor store (Svelte 5 runes). Single source of truth for the
// whole editing session, exported as a singleton.
//
// ARCHITECTURE (designed to grow — see ARCHITECTURE.md):
//   • `entities`  — $state (DEEP reactive): the schema MODEL. The property panel
//                   binds straight into entity/field defs; pushes are reactive.
//                   This is what export reads and what every future panel edits.
//   • `nodes`     — $state.raw: SvelteFlow GEOMETRY only (id + position + a thin
//                   {entityId} payload). Bound to <SvelteFlow bind:nodes> so drag
//                   writes positions here; we sync them back to the model on drag
//                   stop. node.data stays tiny so the canvas never deep-diffs the
//                   model — the node component looks the entity up from the store
//                   (fine-grained reactivity re-renders just that node on edit).
//   • `edges`     — $state.raw: a PROJECTION of the foreign-key fields, rebuilt
//                   imperatively whenever relations change.
//
// This split is why dragging one node never re-renders the others, and why a
// field edit re-renders one node, not the canvas — the performance property the
// research called out (re-renders, not the framework, are the bottleneck).

import { MarkerType, type Edge, type Node } from '@xyflow/svelte';
import dagre from '@dagrejs/dagre';

import type {
	APISchema,
	Condition,
	FieldDef,
	FieldType,
	RBACPolicy,
	ResourcePermission,
	RolePolicy,
	StateMachine
} from '../types/schema';
import { IDENT_RE, RBAC_ACTIONS, HOOK_EVENTS, PUBLIC_ROLE_NAME } from '../types/schema';
import type {
	ForeignKeyDef,
	IndexDef,
	ReferentialAction,
	RelationDef,
	HookConfig,
	HookEvent
} from '../types/schema';
import type { EntityModel, FieldModel, RelationModel, XY } from '../types/editor';
import { schemaToModel, modelToSchema } from '../schema/transform';
import {
	fieldDefIssues,
	pgKind,
	smInitialList,
	smKnownStates,
	NUMERIC_TYPES,
	STRING_TYPES
} from '../schema/fieldRules';
import { blankSchema } from '../schema/samples';
import { newId } from '../schema/ids';

export type EntityNodeData = { entityId: string };
/** Edge payload: a field-FK edge carries fieldName/onDelete; a relations-block
 *  embed edge (UI-F4-S4) carries `embed` instead — the custom edge renders them
 *  distinctly (solid vs dashed, FK label vs name + 1:N/N:1/N:N chip). */
export type RelationEdgeData = {
	fieldName?: string;
	onDelete?: string;
	selfRef?: boolean;
	embed?: { name: string; kind: 'has_many' | 'belongs_to' | 'many_to_many' };
};
export type FlowNode = Node<EntityNodeData, 'entity'>;
export type FlowEdge = Edge<RelationEdgeData, 'relation'>;

export const TARGET_HANDLE = 'in';
export const NEW_SOURCE_HANDLE = '__new';

// Estimated node geometry for dagre (real sizes are measured after render; these
// only need to be close enough to lay boxes out without overlap).
const NODE_WIDTH = 248;
const ROW_HEIGHT = 26;
const HEADER_HEIGHT = 46;
const NODE_PAD = 14;

function estimatedHeight(e: EntityModel): number {
	return HEADER_HEIGHT + Math.max(1, e.fields.length) * ROW_HEIGHT + NODE_PAD;
}

function uniqueName(base: string, taken: Set<string>): string {
	if (!taken.has(base)) return base;
	let i = 2;
	while (taken.has(`${base}_${i}`)) i++;
	return `${base}_${i}`;
}

function fkFieldName(target: string, taken: Set<string>): string {
	const singular = target.endsWith('s') && target.length > 3 ? target.slice(0, -1) : target;
	return uniqueName(`${singular}_id`, taken);
}

/** Order-independent equality of two string lists (a unique key may be referenced
 *  in any order — mirrors sameStringSet in validator.go). */
function sameStringSet(a: string[], b: string[]): boolean {
	if (a.length !== b.length) return false;
	const seen = new Map<string, number>();
	for (const x of a) seen.set(x, (seen.get(x) ?? 0) + 1);
	for (const x of b) {
		const n = (seen.get(x) ?? 0) - 1;
		if (n < 0) return false;
		seen.set(x, n);
	}
	return true;
}

class EditorStore {
	// ── model (deep reactive) ──────────────────────────────────────────────────
	entities = $state<EntityModel[]>([]);
	schemaName = $state('untitled-api');
	version = $state('1');
	schemaUrl = $state('https://appximo.com/schema/v1');
	rbac = $state<RBACPolicy>({ roles: {} });
	workflows = $state<Record<string, unknown> | undefined>(undefined);

	// ── canvas (raw) ─────────────────────────────────────────────────────────
	nodes = $state.raw<FlowNode[]>([]);
	edges = $state.raw<FlowEdge[]>([]);

	// ── selection + signals ──────────────────────────────────────────────────
	selectedEntityId = $state<string | null>(null);
	selectedFieldId = $state<string | null>(null);
	/** Bumped when a node's handle set changes — the canvas calls updateNodeInternals. */
	structureVersion = $state(0);
	/** Bumped when positions are recomputed — the canvas re-runs fitView. */
	layoutVersion = $state(0);
	/** Bumped on any model change — drives the "unsaved" indicator / export freshness. */
	revision = $state(0);

	// ── derived selection ──────────────────────────────────────────────────────
	selectedEntity = $derived.by(() =>
		this.selectedEntityId ? (this.entities.find((e) => e.id === this.selectedEntityId) ?? null) : null
	);
	selectedField = $derived.by(() => {
		const e = this.selectedEntity;
		if (!e || !this.selectedFieldId) return null;
		return e.fields.find((f) => f.id === this.selectedFieldId) ?? null;
	});
	entityNames = $derived(this.entities.map((e) => e.name));

	getEntity(id: string): EntityModel | undefined {
		return this.entities.find((e) => e.id === id);
	}
	getEntityByName(name: string): EntityModel | undefined {
		return this.entities.find((e) => e.name === name);
	}
	/** Virtual engine resources grantable in RBAC — today only the built-in
	 *  `files` store, valid unless shadowed by a real schema resource. Mirrors
	 *  the engine authority (pkg/schema/validator.go validateRBAC); ST1 was this
	 *  exemption missing here while the deploy button trusted the local mirror. */
	isVirtualResource(name: string): boolean {
		return name === 'files' && !this.getEntityByName(name);
	}
	/** Resource names offerable in RBAC pickers: entities + the virtual store. */
	get rbacResourceNames(): string[] {
		return this.isVirtualResource('files') ? [...this.entityNames, 'files'] : this.entityNames;
	}

	private bump() {
		this.revision++;
	}

	/** Public bump for components that mutate reactive state directly (e.g. RbacModal
	 *  editing this.rbac in place) so the revision/export-freshness still advances. */
	touch() {
		this.bump();
	}

	// ── load / export ──────────────────────────────────────────────────────────

	/** Load a schema onto the canvas. `baseline` controls the rename baseline
	 *  (UI-F4-S1): 'declared' (default — an import may describe something already
	 *  deployed, so renames must chain from the declared names / pending
	 *  renamed_from) or 'none' (a fresh design that exists in no tenant — renaming
	 *  never emits a spurious renamed_from). */
	loadSchema(schema: APISchema, opts: { baseline?: 'declared' | 'none' } = {}) {
		const model = schemaToModel(schema);
		this.entities = model.entities;
		if (opts.baseline === 'none') {
			for (const e of this.entities) {
				e.originalName = undefined;
				for (const f of e.fields) f.originalName = undefined;
			}
		}
		this.schemaName = model.name;
		this.version = model.version;
		this.schemaUrl = model.$schema;
		this.rbac = model.rbac;
		this.workflows = model.workflows;
		this.selectedEntityId = null;
		this.selectedFieldId = null;
		this.autoLayout(); // also rebuilds nodes + edges
		this.bump();
	}

	newSchema(name = 'my-api') {
		this.loadSchema(blankSchema(name), { baseline: 'none' });
	}

	/** Re-anchor every rename baseline to the CURRENT names (UI-F4-S1). Called after
	 *  a successful deploy (the tenant now knows each object by its current name —
	 *  the next rename must chain from HERE, not from a name that no longer exists
	 *  live) and when a tenant's stored schema is loaded onto the canvas (its
	 *  declared names ARE the live names). Also stops an applied rename from
	 *  re-emitting: the engine's resolveRenames is idempotent anyway, but the export
	 *  stays clean. */
	commitBaselines() {
		for (const e of this.entities) {
			e.originalName = e.name;
			for (const f of e.fields) f.originalName = f.name;
		}
		this.bump();
	}

	toSchema(): APISchema {
		return modelToSchema({
			$schema: this.schemaUrl,
			version: this.version,
			name: this.schemaName,
			entities: $state.snapshot(this.entities) as EntityModel[],
			rbac: $state.snapshot(this.rbac) as RBACPolicy,
			workflows: this.workflows ? ($state.snapshot(this.workflows) as Record<string, unknown>) : undefined
		});
	}

	toJSON(): string {
		return JSON.stringify(this.toSchema(), null, 2);
	}

	// ── canvas projection ──────────────────────────────────────────────────────

	private rebuildNodes() {
		this.nodes = this.entities.map((e) => ({
			id: e.id,
			type: 'entity' as const,
			position: { x: e.position.x, y: e.position.y },
			data: { entityId: e.id },
			deletable: true
		}));
	}

	/** Recompute the FK edges from the model. Cheap; called on any relation change. */
	rebuildEdges() {
		const edges: FlowEdge[] = [];
		for (const e of this.entities) {
			for (const f of e.fields) {
				const targetName = f.def.relation;
				if (!targetName) continue;
				const target = this.getEntityByName(targetName);
				if (!target) continue; // dangling relation — field kept, edge not drawn
				edges.push({
					id: `fk:${e.id}:${f.id}`,
					source: e.id,
					target: target.id,
					sourceHandle: f.id,
					targetHandle: TARGET_HANDLE,
					type: 'relation',
					markerEnd: { type: MarkerType.ArrowClosed, width: 16, height: 16 },
					data: { fieldName: f.name, onDelete: f.def.on_delete, selfRef: target.id === e.id }
				});
			}
		}
		// Relations-block embeds (UI-F4-S4): each authored relation is projected as
		// its own edge, anchored at the node HEADER handle (vs the field row a FK
		// edge leaves from) so the two never overlap. Derived + informative — the
		// panel is the source of truth; a dangling target draws nothing.
		for (const e of this.entities) {
			for (const r of e.relations) {
				const target = this.getEntityByName(r.def.target);
				if (!target) continue;
				edges.push({
					id: `emb:${e.id}:${r.id}`,
					source: e.id,
					target: target.id,
					sourceHandle: NEW_SOURCE_HANDLE,
					targetHandle: TARGET_HANDLE,
					type: 'relation',
					markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13 },
					data: { embed: { name: r.name, kind: r.def.type }, selfRef: target.id === e.id }
				});
			}
		}
		this.edges = edges;
	}

	/** dagre auto-layout: positions every entity so boxes don't overlap. */
	autoLayout() {
		const g = new dagre.graphlib.Graph();
		g.setGraph({ rankdir: 'LR', nodesep: 40, ranksep: 90, marginx: 30, marginy: 30 });
		g.setDefaultEdgeLabel(() => ({}));
		for (const e of this.entities) {
			g.setNode(e.id, { width: NODE_WIDTH, height: estimatedHeight(e) });
		}
		for (const e of this.entities) {
			for (const f of e.fields) {
				if (!f.def.relation) continue;
				const t = this.getEntityByName(f.def.relation);
				if (t && t.id !== e.id) g.setEdge(e.id, t.id);
			}
		}
		dagre.layout(g);
		for (const e of this.entities) {
			const n = g.node(e.id);
			if (!n) continue;
			// dagre returns the node CENTER; SvelteFlow position is the top-left.
			e.position = { x: Math.round(n.x - NODE_WIDTH / 2), y: Math.round(n.y - estimatedHeight(e) / 2) };
		}
		this.rebuildNodes();
		this.rebuildEdges();
		this.layoutVersion++;
	}

	/** Sync a dragged node's position back into the model (called on drag stop). */
	syncPosition(nodeId: string, pos: XY) {
		const e = this.getEntity(nodeId);
		if (!e) return;
		e.position = { x: Math.round(pos.x), y: Math.round(pos.y) };
	}

	// ── entities ─────────────────────────────────────────────────────────────

	addEntity(at?: XY): EntityModel {
		const taken = new Set(this.entities.map((e) => e.name));
		const name = uniqueName('new_entity', taken);
		const entity: EntityModel = {
			id: newId('ent'),
			name,
			fields: [{ id: newId('f'), name: 'name', def: { type: 'string' } }],
			relations: [],
			extras: {},
			position: at ?? this.spawnPosition()
		};
		this.entities.push(entity);
		this.rebuildNodes();
		this.selectEntity(entity.id);
		this.structureVersion++;
		this.bump();
		return entity;
	}

	private spawnPosition(): XY {
		// Drop new entities in a loose grid offset from existing ones.
		const n = this.entities.length;
		return { x: 80 + (n % 4) * 280, y: 80 + Math.floor(n / 4) * 220 };
	}

	renameEntity(id: string, raw: string): string | null {
		const name = raw.trim();
		const e = this.getEntity(id);
		if (!e) return 'entity not found';
		if (name === e.name) return null;
		const err = this.validateResourceName(name, id);
		if (err) return err;
		const old = e.name;
		e.name = name;
		// Keep referencing relations/FKs pointed at the new name so edges survive —
		// including m2m `through` (the junction is itself a resource name).
		for (const other of this.entities) {
			for (const f of other.fields) if (f.def.relation === old) f.def.relation = name;
			for (const r of other.relations) {
				if (r.def.target === old) r.def.target = name;
				if (r.def.through === old) r.def.through = name;
			}
			for (const fk of other.extras.foreign_keys ?? []) if (fk.target === old) fk.target = name;
		}
		this.rbacOnResourceRenamed(old, name);
		this.rebuildEdges();
		this.bump();
		return null;
	}

	deleteEntity(id: string) {
		const e = this.getEntity(id);
		if (!e) return;
		const name = e.name;
		this.entities = this.entities.filter((x) => x.id !== id);
		// Drop dangling references so the exported schema stays valid: a FK field
		// to the deleted resource becomes a plain column; relations/FKs targeting
		// it are removed.
		for (const other of this.entities) {
			for (const f of other.fields) {
				if (f.def.relation === name) {
					delete f.def.relation;
					delete f.def.on_delete;
					delete f.def.on_update;
					delete f.def.references;
				}
			}
			// A relation TARGETING the deleted resource is meaningless, and so is a
			// many_to_many whose JUNCTION (through) was deleted — drop both.
			other.relations = other.relations.filter(
				(r) => r.def.target !== name && r.def.through !== name
			);
			if (other.extras.foreign_keys) {
				other.extras.foreign_keys = other.extras.foreign_keys.filter((fk) => fk.target !== name);
			}
		}
		this.rbacOnResourceDeleted(name);
		if (this.selectedEntityId === id) this.clearSelection();
		this.rebuildNodes();
		this.rebuildEdges();
		this.structureVersion++;
		this.bump();
	}

	// ── fields ─────────────────────────────────────────────────────────────────

	addField(entityId: string, type: FieldType = 'string'): FieldModel | null {
		const e = this.getEntity(entityId);
		if (!e) return null;
		const taken = new Set(e.fields.map((f) => f.name));
		const field: FieldModel = { id: newId('f'), name: uniqueName('field', taken), def: { type } };
		e.fields.push(field);
		this.selectField(entityId, field.id);
		this.structureVersion++;
		this.bump();
		return field;
	}

	deleteField(entityId: string, fieldId: string) {
		const e = this.getEntity(entityId);
		if (!e) return;
		const gone = e.fields.find((f) => f.id === fieldId);
		const wasFk = !!gone?.def.relation;
		e.fields = e.fields.filter((f) => f.id !== fieldId);
		if (this.selectedFieldId === fieldId) this.selectedFieldId = null;
		if (gone) this.rbacOnFieldDeleted(e.name, gone.name);
		// Drop a deleted field from the entity's import-grant subset; if that
		// empties the subset, fall back to "all governed fields" (absent key) —
		// an explicitly empty list is a load error.
		if (gone && e.extras.import?.fields?.includes(gone.name)) {
			const rest = e.extras.import.fields.filter((c) => c !== gone.name);
			e.extras.import = { ...e.extras.import, fields: rest.length ? rest : undefined };
		}
		if (wasFk) this.rebuildEdges();
		this.structureVersion++;
		this.bump();
	}

	renameField(entityId: string, fieldId: string, raw: string): string | null {
		const e = this.getEntity(entityId);
		const f = e?.fields.find((x) => x.id === fieldId);
		if (!e || !f) return 'field not found';
		const name = raw.trim();
		if (name === f.name) return null;
		if (!IDENT_RE.test(name)) return 'must match ^[a-z][a-z0-9_]*$';
		if (e.fields.some((x) => x.id !== fieldId && x.name === name)) return 'duplicate field name';
		const oldField = f.name;
		f.name = name;
		this.propagateFieldRename(e, oldField, name);
		this.rbacOnFieldRenamed(e.name, oldField, name);
		this.rebuildEdges();
		this.bump();
		return null;
	}

	/** Repoint every schema reference to a renamed column (UI-F4-S1) so nothing is
	 *  left dangling: same-entity indexes and composite-FK source columns, other
	 *  entities' composite-FK ref_columns and field-level `references` targeting
	 *  this column, and the relations blocks whose fk/target_fk IS this column
	 *  (has_many → a column on the target; belongs_to → a column on the declaring
	 *  entity; many_to_many → columns on the junction). */
	private propagateFieldRename(e: EntityModel, oldField: string, newField: string) {
		for (const idx of e.extras.indexes ?? []) {
			idx.fields = (idx.fields ?? []).map((c) => (c === oldField ? newField : c));
		}
		// Import grant subset (WRITE-ASYMMETRY-S1): an auto field renamed while
		// listed keeps its grant under the new name (a stale name would fail
		// validation, import_unknown_field).
		if (e.extras.import?.fields?.includes(oldField)) {
			e.extras.import.fields = e.extras.import.fields.map((c) => (c === oldField ? newField : c));
		}
		for (const fk of e.extras.foreign_keys ?? []) {
			fk.columns = (fk.columns ?? []).map((c) => (c === oldField ? newField : c));
		}
		for (const other of this.entities) {
			for (const fk of other.extras.foreign_keys ?? []) {
				if (fk.target === e.name) {
					fk.ref_columns = (fk.ref_columns ?? []).map((c) => (c === oldField ? newField : c));
				}
			}
			for (const fld of other.fields) {
				if (fld.def.relation === e.name && fld.def.references === oldField) {
					fld.def.references = newField;
				}
			}
			for (const r of other.relations) {
				const d = r.def;
				if (d.type === 'has_many' && d.target === e.name && d.fk === oldField) d.fk = newField;
				if (d.type === 'belongs_to' && other.name === e.name && d.fk === oldField) d.fk = newField;
				if (d.type === 'many_to_many' && d.through === e.name) {
					if (d.fk === oldField) d.fk = newField;
					if (d.target_fk === oldField) d.target_fk = newField;
				}
			}
		}
	}

	/** Set or (when value === undefined) delete one key of a field's def. */
	patchFieldDef<K extends keyof FieldDef>(entityId: string, fieldId: string, key: K, value: FieldDef[K] | undefined) {
		const f = this.getEntity(entityId)?.fields.find((x) => x.id === fieldId);
		if (!f) return;
		const affectsEdges = key === 'relation' || key === 'on_delete';
		if (value === undefined) {
			delete f.def[key];
		} else {
			f.def[key] = value;
		}
		// Un-auto'ing a field removes it from the entity's import-grant subset —
		// only governed fields (id + auto) may be listed there
		// (import_unknown_field at load); empty subset → absent key.
		if (key === 'auto' && !value) {
			const e = this.getEntity(entityId);
			if (e?.extras.import?.fields?.includes(f.name)) {
				const rest = e.extras.import.fields.filter((c) => c !== f.name);
				e.extras.import = { ...e.extras.import, fields: rest.length ? rest : undefined };
			}
		}
		// Changing the type away from uuid clears the relation (a FK must be uuid).
		if (key === 'type' && value !== 'uuid' && f.def.relation) {
			delete f.def.relation;
			delete f.def.on_delete;
			delete f.def.on_update;
			delete f.def.references;
			this.rebuildEdges();
			this.structureVersion++;
		}
		// Changing the type also strips the validation rules that no longer apply to
		// the new type — the engine REJECTS minLength/maxLength/pattern/format on a
		// non-string field and min/max on a non-numeric one, so a stale rule from the
		// old type would make the schema invalid at load. We drop them rather than
		// emit something the validator refuses (string→int with a pattern still set).
		if (key === 'type') {
			const t = value as FieldType;
			if (!NUMERIC_TYPES.has(t)) {
				delete f.def.min;
				delete f.def.max;
			}
			if (!STRING_TYPES.has(t)) {
				delete f.def.minLength;
				delete f.def.maxLength;
				delete f.def.pattern;
				delete f.def.format;
			}
		}
		if (affectsEdges) {
			this.rebuildEdges();
			this.structureVersion++;
		}
		this.bump();
	}

	// ── state machine designer (UI-F2-S3, G5) ──────────────────────────────────
	// The state_machine lives on the field def (deep $state); these are the
	// structural edits. The states' universe = StateMachine.KnownStates (the
	// transition keys ∪ initial ∪ targets), kept faithful to the engine. A field
	// with an enum constrains the states to enum members (coherence is required at
	// load); a field without an enum lets the machine define its own state universe.

	private fieldDef(entityId: string, fieldId: string): FieldDef | undefined {
		return this.getEntity(entityId)?.fields.find((x) => x.id === fieldId)?.def;
	}

	/** Enable a state machine on a string/text field, seeding states from its enum
	 *  (terminals = []) and the initial from the field's default when valid. No-op if
	 *  one already exists or the field is not string/text. */
	enableStateMachine(entityId: string, fieldId: string) {
		const def = this.fieldDef(entityId, fieldId);
		if (!def || def.state_machine) return;
		if (def.type !== 'string' && def.type !== 'text') return;
		const states = def.enum ?? [];
		const transitions: Record<string, string[]> = {};
		for (const s of states) transitions[s] = [];
		let initial = '';
		if (states.length > 0) {
			initial = typeof def.default === 'string' && states.includes(def.default) ? def.default : states[0];
		}
		def.state_machine = { initial, transitions };
		this.bump();
	}

	disableStateMachine(entityId: string, fieldId: string) {
		const def = this.fieldDef(entityId, fieldId);
		if (def?.state_machine) {
			delete def.state_machine;
			this.bump();
		}
	}

	/** Write `initial`: a bare string for exactly one (the common authored form), an
	 *  array otherwise — preserving the typical schema shape on round-trip. */
	private smWriteInitial(sm: StateMachine, states: string[]) {
		sm.initial = states.length === 1 ? states[0] : states;
	}

	smAddState(entityId: string, fieldId: string, raw: string): string | null {
		const def = this.fieldDef(entityId, fieldId);
		const sm = def?.state_machine;
		if (!def || !sm) return null;
		const name = raw.trim();
		if (!name) return 'state name is required';
		if (smKnownStates(sm).includes(name)) return 'duplicate state';
		// With an enum, a state must be an enum member (the engine requires it).
		if (def.enum && def.enum.length > 0 && !def.enum.includes(name)) {
			return 'state must be one of the field’s enum values';
		}
		sm.transitions[name] = [];
		// The first state becomes the initial so the machine is valid immediately.
		if (smInitialList(sm).length === 0) this.smWriteInitial(sm, [name]);
		this.bump();
		return null;
	}

	smRemoveState(entityId: string, fieldId: string, state: string) {
		const sm = this.fieldDef(entityId, fieldId)?.state_machine;
		if (!sm) return;
		delete sm.transitions[state];
		// Strip it as a transition target and from the initial set.
		for (const k of Object.keys(sm.transitions)) {
			sm.transitions[k] = sm.transitions[k].filter((t) => t !== state);
		}
		this.smWriteInitial(sm, smInitialList(sm).filter((s) => s !== state));
		this.bump();
	}

	smToggleInitial(entityId: string, fieldId: string, state: string, on: boolean) {
		const sm = this.fieldDef(entityId, fieldId)?.state_machine;
		if (!sm) return;
		const cur = new Set(smInitialList(sm));
		if (on) cur.add(state);
		else cur.delete(state);
		// Preserve declared order.
		this.smWriteInitial(sm, smKnownStates(sm).filter((s) => cur.has(s)));
		this.bump();
	}

	smToggleTransition(entityId: string, fieldId: string, from: string, to: string, on: boolean) {
		const sm = this.fieldDef(entityId, fieldId)?.state_machine;
		if (!sm || from === to) return;
		const cur = new Set(sm.transitions[from] ?? []);
		if (on) cur.add(to);
		else cur.delete(to);
		sm.transitions[from] = smKnownStates(sm).filter((s) => cur.has(s));
		this.bump();
	}

	// ── relations (drag-to-connect / edge delete) ──────────────────────────────

	/** Create a FK field on `sourceId` pointing at `targetId` (the drag-connect action). */
	createRelation(sourceId: string, targetId: string): FieldModel | null {
		const src = this.getEntity(sourceId);
		const tgt = this.getEntity(targetId);
		if (!src || !tgt) return null;
		const taken = new Set(src.fields.map((f) => f.name));
		const field: FieldModel = {
			id: newId('f'),
			name: fkFieldName(tgt.name, taken),
			def: { type: 'uuid', relation: tgt.name, on_delete: 'restrict' }
		};
		src.fields.push(field);
		this.rebuildEdges();
		this.selectField(sourceId, field.id);
		this.structureVersion++;
		this.bump();
		return field;
	}

	/** Delete the FK behind an edge id ("fk:<entityId>:<fieldId>"). */
	deleteEdge(edgeId: string) {
		const fk = /^fk:([^:]+):(.+)$/.exec(edgeId);
		if (fk) {
			this.deleteField(fk[1], fk[2]);
			return;
		}
		// An embed edge is the relation itself — deleting it removes the relation
		// (same contract as an FK edge removing its field).
		const emb = /^emb:([^:]+):(.+)$/.exec(edgeId);
		if (emb) this.removeRelation(emb[1], emb[2]);
	}

	// ── data model: indexes + composite foreign keys (UI-F2-S4, MIG-F1-S5) ─────
	// These live in entity.extras (deep $state), round-tripped verbatim. The
	// helpers mirror the engine's columnsAreUniqueOnTarget / pgKindForAPIType so the
	// editor only offers what the validator accepts.

	/** Columns of a resource a foreign key may point at: the implicit id + every
	 *  `unique` field (Postgres requires an FK destination to be a PK or unique). */
	referenceableColumns(entityName: string): string[] {
		const e = this.getEntityByName(entityName);
		if (!e) return ['id'];
		return ['id', ...e.fields.filter((f) => f.def.unique).map((f) => f.name)];
	}

	/** Mirror of columnsAreUniqueOnTarget: a column set is a valid FK destination on
	 *  the target when it is exactly ["id"], a single `unique` field, or the column
	 *  set of a declared UNIQUE index (composite or single). */
	columnsFormUniqueOnTarget(targetName: string, cols: string[]): boolean {
		const e = this.getEntityByName(targetName);
		if (!e || cols.length === 0 || cols.some((c) => !c)) return false;
		if (cols.length === 1) {
			if (cols[0] === 'id') return true;
			if (e.fields.find((f) => f.name === cols[0])?.def.unique) return true;
		}
		for (const idx of e.extras.indexes ?? []) {
			if (idx.unique && sameStringSet(idx.fields, cols)) return true;
		}
		return false;
	}

	// composite foreign keys ----------------------------------------------------
	private entityFKs(entityId: string): ForeignKeyDef[] | undefined {
		return this.getEntity(entityId)?.extras.foreign_keys;
	}
	addForeignKey(entityId: string) {
		const e = this.getEntity(entityId);
		if (!e) return;
		if (!e.extras.foreign_keys) e.extras.foreign_keys = [];
		e.extras.foreign_keys.push({ columns: [''], target: '', ref_columns: [''] });
		this.bump();
	}
	removeForeignKey(entityId: string, fkIdx: number) {
		const e = this.getEntity(entityId);
		const fks = e?.extras.foreign_keys;
		if (!e || !fks) return;
		fks.splice(fkIdx, 1);
		if (fks.length === 0) e.extras.foreign_keys = undefined;
		this.bump();
	}
	/** Add one (source ▸ ref) column pair — keeps columns and ref_columns the same length. */
	addFKPair(entityId: string, fkIdx: number) {
		const fk = this.entityFKs(entityId)?.[fkIdx];
		if (!fk) return;
		fk.columns.push('');
		fk.ref_columns.push('');
		this.bump();
	}
	removeFKPair(entityId: string, fkIdx: number, pairIdx: number) {
		const fk = this.entityFKs(entityId)?.[fkIdx];
		if (!fk) return;
		fk.columns.splice(pairIdx, 1);
		fk.ref_columns.splice(pairIdx, 1);
		this.bump();
	}
	setFKPair(entityId: string, fkIdx: number, pairIdx: number, side: 'source' | 'ref', val: string) {
		const fk = this.entityFKs(entityId)?.[fkIdx];
		if (!fk) return;
		if (side === 'source') fk.columns[pairIdx] = val;
		else fk.ref_columns[pairIdx] = val;
		this.bump();
	}
	setFKTarget(entityId: string, fkIdx: number, target: string) {
		const fk = this.entityFKs(entityId)?.[fkIdx];
		if (!fk) return;
		fk.target = target;
		// A new target invalidates the referenced columns — reset them, keeping count.
		fk.ref_columns = fk.columns.map(() => '');
		this.bump();
	}
	setFKAction(entityId: string, fkIdx: number, key: 'on_delete' | 'on_update', val: string) {
		const fk = this.entityFKs(entityId)?.[fkIdx];
		if (!fk) return;
		if (val) fk[key] = val as ReferentialAction;
		else delete fk[key];
		this.bump();
	}

	// indexes -------------------------------------------------------------------
	private entityIndexes(entityId: string): IndexDef[] | undefined {
		return this.getEntity(entityId)?.extras.indexes;
	}
	addIndex(entityId: string) {
		const e = this.getEntity(entityId);
		if (!e) return;
		if (!e.extras.indexes) e.extras.indexes = [];
		e.extras.indexes.push({ fields: [] });
		this.bump();
	}
	removeIndex(entityId: string, idx: number) {
		const e = this.getEntity(entityId);
		const ix = e?.extras.indexes;
		if (!e || !ix) return;
		ix.splice(idx, 1);
		if (ix.length === 0) e.extras.indexes = undefined;
		this.bump();
	}
	toggleIndexField(entityId: string, idx: number, field: string, on: boolean) {
		const e = this.getEntity(entityId);
		const index = e?.extras.indexes?.[idx];
		if (!e || !index) return;
		const cur = new Set(index.fields);
		if (on) cur.add(field);
		else cur.delete(field);
		// Preserve the entity's field order for a stable, deterministic index.
		index.fields = e.fields.map((f) => f.name).filter((n) => cur.has(n));
		this.bump();
	}
	setIndexUnique(entityId: string, idx: number, on: boolean) {
		const index = this.entityIndexes(entityId)?.[idx];
		if (!index) return;
		if (on) index.unique = true;
		else delete index.unique;
		if (on) {
			// A gin index cannot be unique (the engine rejects it at load), so
			// choosing unique drops back to the default btree.
			delete index.method;
			delete index.opclass;
		}
		this.bump();
	}
	// The access method (LIBRARY-GAPS-S1). '' means the engine default (btree) and
	// is stored as an ABSENT key, so an untouched index serializes byte-identically.
	// gin is only valid over jsonb columns and never unique — mirrored here so the
	// editor can only produce schemas the validator accepts.
	setIndexMethod(entityId: string, idx: number, method: string) {
		const index = this.entityIndexes(entityId)?.[idx];
		if (!index) return;
		if (method === 'gin') {
			index.method = 'gin';
			delete index.unique;
		} else {
			delete index.method;
			delete index.opclass; // an opclass is only meaningful with gin
		}
		this.bump();
	}
	setIndexOpclass(entityId: string, idx: number, opclass: string) {
		const index = this.entityIndexes(entityId)?.[idx];
		if (!index || index.method !== 'gin') return;
		if (opclass) index.opclass = opclass as 'jsonb_ops' | 'jsonb_path_ops';
		else delete index.opclass;
		this.bump();
	}

	// ── relations block: the ?include= embeds (UI-F4-S3) ───────────────────────
	// Faithful to the engine grammar (pkg/schema validateRelations): a relation is
	// {type, target, fk[, through, target_fk][, limit]} where fk's MEANING depends
	// on the kind — has_many: a column ON THE TARGET pointing here; belongs_to: an
	// OWN column pointing at the target; many_to_many: fk + target_fk are the two
	// columns OF THE JUNCTION (through). through/target_fk apply to m2m ONLY (the
	// engine rejects them elsewhere); limit bounds children per parent (0 → 50).

	addRelation(entityId: string): RelationModel | null {
		const e = this.getEntity(entityId);
		if (!e) return null;
		const taken = new Set(e.relations.map((r) => r.name));
		const rel: RelationModel = {
			id: newId('r'),
			name: uniqueName('embed', taken),
			def: { type: 'has_many', target: '', fk: '' }
		};
		e.relations.push(rel);
		this.rebuildEdges();
		this.bump();
		return rel;
	}

	removeRelation(entityId: string, relId: string) {
		const e = this.getEntity(entityId);
		if (!e) return;
		e.relations = e.relations.filter((r) => r.id !== relId);
		this.rebuildEdges();
		this.bump();
	}

	/** Rename an embed. Mirrors the engine's checks: valid identifier, unique among
	 *  the entity's relations, and no collision with a field of the same resource
	 *  (the embed keys json_build_object and becomes a GraphQL field). */
	renameRelation(entityId: string, relId: string, raw: string): string | null {
		const e = this.getEntity(entityId);
		const r = e?.relations.find((x) => x.id === relId);
		if (!e || !r) return 'relation not found';
		const name = raw.trim();
		if (name === r.name) return null;
		if (!IDENT_RE.test(name)) return 'must match ^[a-z][a-z0-9_]*$';
		if (e.relations.some((x) => x.id !== relId && x.name === name)) return 'duplicate relation name';
		if (e.fields.some((f) => f.name === name)) return 'collides with a field of the same name';
		r.name = name;
		this.rebuildEdges(); // the embed edge label shows the name
		this.bump();
		return null;
	}

	/** Set one key of a relation def, keeping the shape ENGINE-VALID for its kind:
	 *  a kind change strips the keys that no longer apply and resets fk (its meaning
	 *  changes table); a target/through change resets the column choices that lived
	 *  on the previous table — the dropdowns then only ever offer real columns. */
	patchRelation<K extends keyof RelationDef>(
		entityId: string,
		relId: string,
		key: K,
		value: RelationDef[K] | undefined
	) {
		const r = this.getEntity(entityId)?.relations.find((x) => x.id === relId);
		if (!r) return;
		if (value === undefined) delete r.def[key];
		else r.def[key] = value;

		if (key === 'type') {
			// fk's meaning is per-kind (target column / own column / junction column)
			// — a stale choice from another kind would reference the wrong table.
			r.def.fk = '';
			if (value !== 'many_to_many') {
				delete r.def.through;
				delete r.def.target_fk;
			}
		}
		if (key === 'target') {
			if (r.def.type === 'has_many') r.def.fk = ''; // fk lives on the target
			if (r.def.type === 'many_to_many') delete r.def.target_fk; // junction column → target
		}
		if (key === 'through') {
			// Both junction columns belong to the (new) through table.
			r.def.fk = '';
			delete r.def.target_fk;
		}
		if (key === 'type' || key === 'target') this.rebuildEdges(); // kind chip / edge endpoints
		this.bump();
	}

	/** The columns a relation's fk/target_fk may choose from, per the kind's
	 *  semantics. Empty until the owning table (target/self/through) is chosen. */
	relationFKColumns(entity: EntityModel, def: RelationDef, which: 'fk' | 'target_fk'): string[] {
		let owner: EntityModel | undefined;
		switch (def.type) {
			case 'has_many':
				owner = this.getEntityByName(def.target) ?? undefined;
				break;
			case 'belongs_to':
				owner = entity;
				break;
			case 'many_to_many':
				owner = def.through ? (this.getEntityByName(def.through) ?? undefined) : undefined;
				break;
		}
		if (!owner) return [];
		if (which === 'target_fk' && def.type !== 'many_to_many') return [];
		return owner.fields.map((f) => f.name);
	}

	/** Live issues for ONE relation — mirrors the engine's validateRelations plus
	 *  the column-existence checks the migration would warn about, so an invalid
	 *  embed is surfaced before deploy (shown inline in the panel and aggregated
	 *  into validate()). */
	relationIssuesFor(e: EntityModel, r: RelationModel): string[] {
		const out: string[] = [];
		const d = r.def;
		if (!IDENT_RE.test(r.name)) out.push('name must match ^[a-z][a-z0-9_]*$');
		if (e.fields.some((f) => f.name === r.name)) out.push(`name collides with field "${r.name}"`);
		if (!d.target) {
			out.push('choose a target');
		} else if (!this.getEntityByName(d.target)) {
			out.push(`unknown target "${d.target}"`);
		}
		if ((d.limit ?? 0) < 0) out.push('limit must be >= 0');
		const cols = (name: string) => this.getEntityByName(name)?.fields.map((f) => f.name) ?? [];
		switch (d.type) {
			case 'has_many':
				if (!d.fk) out.push(`choose the FK column on ${d.target || 'the target'} that points here`);
				else if (d.target && this.getEntityByName(d.target) && !cols(d.target).includes(d.fk))
					out.push(`fk "${d.fk}" is not a field of ${d.target}`);
				break;
			case 'belongs_to':
				if (!d.fk) out.push('choose the own FK column that points at the target');
				else if (!e.fields.some((f) => f.name === d.fk)) out.push(`fk "${d.fk}" is not a field of ${e.name}`);
				break;
			case 'many_to_many': {
				if (!d.through) out.push('many_to_many needs a through (junction) resource');
				else if (!this.getEntityByName(d.through)) out.push(`unknown through "${d.through}"`);
				const jcols = d.through ? cols(d.through) : [];
				if (!d.fk) out.push('choose the junction column that points here');
				else if (d.through && this.getEntityByName(d.through) && !jcols.includes(d.fk))
					out.push(`fk "${d.fk}" is not a field of ${d.through}`);
				if (!d.target_fk) out.push(`choose the junction column that points at ${d.target || 'the target'}`);
				else if (d.through && this.getEntityByName(d.through) && !jcols.includes(d.target_fk))
					out.push(`target_fk "${d.target_fk}" is not a field of ${d.through}`);
				break;
			}
		}
		return out;
	}

	/** All relation issues, prefixed per relation (aggregated by validate()). */
	relationIssues(): string[] {
		const out: string[] = [];
		for (const e of this.entities) {
			const seen = new Set<string>();
			for (const r of e.relations) {
				if (seen.has(r.name)) out.push(`entity "${e.name}": duplicate relation "${r.name}"`);
				seen.add(r.name);
				for (const msg of this.relationIssuesFor(e, r)) {
					out.push(`relation "${e.name}.${r.name}": ${msg}`);
				}
			}
		}
		return out;
	}

	// ── hooks (UI-F2-S5) ────────────────────────────────────────────────────────
	// hooks is a MAP keyed by event (one hook per event), in entity.extras.hooks
	// (deep $state). Fidelity to SEC-AUDIT-V2: an after_* hook may ONLY be a webhook
	// (a sandboxed js/wasm hook post-commit is a no-op the engine rejects at load).

	/** True for after_create / after_update (webhook-only events). */
	isAfterEvent(event: string): boolean {
		return event === 'after_create' || event === 'after_update';
	}
	/** Hook types valid for an event: after ⇒ webhook only; before ⇒ js/wasm only
	 * (ENG-19 — a webhook before-hook validated and was never dispatched; the
	 * engine now rejects it at load, so Studio must not offer it). */
	hookTypesFor(event: string): Array<'js' | 'webhook' | 'wasm'> {
		return this.isAfterEvent(event) ? ['webhook'] : ['js', 'wasm'];
	}
	/** Events not yet used by a hook on this entity (a map allows one hook per event). */
	unusedHookEvents(entityId: string): HookEvent[] {
		const used = new Set(Object.keys(this.getEntity(entityId)?.extras.hooks ?? {}));
		return HOOK_EVENTS.filter((e) => !used.has(e));
	}

	addHook(entityId: string, event: HookEvent) {
		const e = this.getEntity(entityId);
		if (!e) return;
		if (!e.extras.hooks) e.extras.hooks = {};
		if (e.extras.hooks[event]) return;
		// Default to a valid type for the event (after ⇒ webhook; before ⇒ js).
		e.extras.hooks[event] = this.isAfterEvent(event) ? { type: 'webhook' } : { type: 'js' };
		this.bump();
	}
	removeHook(entityId: string, event: string) {
		const e = this.getEntity(entityId);
		if (!e?.extras.hooks) return;
		delete e.extras.hooks[event];
		if (Object.keys(e.extras.hooks).length === 0) e.extras.hooks = undefined;
		this.bump();
	}
	/** Move a hook to a different event key (rename), coercing the type to webhook if
	 *  the new event is after_* (fidelity). No-op if the target event is taken. */
	setHookEvent(entityId: string, oldEvent: string, newEvent: HookEvent) {
		const e = this.getEntity(entityId);
		const hooks = e?.extras.hooks;
		if (!hooks || oldEvent === newEvent || hooks[newEvent]) return;
		const cfg = hooks[oldEvent];
		if (!cfg) return;
		if (this.isAfterEvent(newEvent) && cfg.type !== 'webhook') {
			this.coerceHookType(cfg, 'webhook');
		}
		hooks[newEvent] = cfg;
		delete hooks[oldEvent];
		this.bump();
	}
	setHookType(entityId: string, event: string, type: 'js' | 'webhook' | 'wasm') {
		const cfg = this.getEntity(entityId)?.extras.hooks?.[event];
		if (!cfg) return;
		this.coerceHookType(cfg, type);
		this.bump();
	}
	/** Set/clear one config field of a hook (drops the key when empty so the export
	 *  never serializes a dead "" value). */
	patchHook(entityId: string, event: string, key: keyof HookConfig, value: string) {
		const cfg = this.getEntity(entityId)?.extras.hooks?.[event];
		if (!cfg) return;
		if (value) (cfg[key] as string) = value;
		else delete cfg[key];
		this.bump();
	}
	/** Switch a hook's type and drop the fields that no longer apply (so a webhook
	 *  never keeps a stale `script`, etc.). */
	private coerceHookType(cfg: HookConfig, type: 'js' | 'webhook' | 'wasm') {
		cfg.type = type;
		if (type !== 'js') delete cfg.script;
		if (type !== 'webhook') {
			delete cfg.url;
			delete cfg.hmac_secret_env;
		}
		if (type !== 'wasm') {
			delete cfg.wasm_module;
			delete cfg.wasm_fn;
		}
	}

	// ── selection ──────────────────────────────────────────────────────────────

	selectEntity(id: string | null) {
		this.selectedEntityId = id;
		this.selectedFieldId = null;
	}
	selectField(entityId: string, fieldId: string) {
		this.selectedEntityId = entityId;
		this.selectedFieldId = fieldId;
	}
	clearSelection() {
		this.selectedEntityId = null;
		this.selectedFieldId = null;
	}

	// ── RBAC (UI-F2-S1) ──────────────────────────────────────────────────────────
	// this.rbac is deep $state, round-tripped verbatim. These are the STRUCTURAL edits;
	// leaf edits (toggling an action, a fields entry) are done directly on the reactive
	// policy by RbacModal, and the export normalizer (transform.cleanRBACPolicy) drops
	// empties so the exported schema is always engine-clean and valid.

	get roleNames(): string[] {
		return Object.keys(this.rbac.roles).sort();
	}
	getRole(name: string): RolePolicy | undefined {
		return this.rbac.roles[name];
	}
	/** Which form a role uses: per-resource permissions, or the legacy role-global. */
	roleForm(name: string): 'perResource' | 'global' {
		const r = this.rbac.roles[name];
		return r && r.permissions && Object.keys(r.permissions).length > 0 ? 'perResource' : 'global';
	}

	addRole(raw: string): string | null {
		const name = raw.trim();
		if (!name) return 'role name is required';
		const reserved = this.reservedRoleNameError(name);
		if (reserved) return reserved;
		if (this.rbac.roles[name]) return 'duplicate role name';
		// New roles start per-resource (the recommended form) and deny-all until a
		// grant is added — deny-by-default.
		this.rbac.roles[name] = { permissions: {} };
		this.bump();
		return null;
	}
	renameRole(oldName: string, raw: string): string | null {
		const name = raw.trim();
		if (oldName === name) return null;
		if (!name) return 'role name is required';
		const reserved = this.reservedRoleNameError(name);
		if (reserved) return reserved;
		if (this.rbac.roles[name]) return 'duplicate role name';
		if (!this.rbac.roles[oldName]) return 'role not found';
		const next: Record<string, RolePolicy> = {};
		for (const [k, v] of Object.entries(this.rbac.roles)) next[k === oldName ? name : k] = v;
		this.rbac.roles = next;
		// Follow the rename into every entity's import grant (WRITE-ASYMMETRY-S1)
		// — a stale role name there would fail validation (import_unknown_role).
		for (const e of this.entities) {
			const imp = e.extras.import;
			if (imp?.roles.includes(oldName)) {
				imp.roles = imp.roles.map((r) => (r === oldName ? name : r));
			}
		}
		this.bump();
		return null;
	}
	deleteRole(name: string) {
		delete this.rbac.roles[name];
		// Drop the deleted role from import grants; a grant left with no roles is
		// dead config (load error) — remove the whole block.
		for (const e of this.entities) {
			const imp = e.extras.import;
			if (imp?.roles.includes(name)) {
				const rest = imp.roles.filter((r) => r !== name);
				e.extras.import = rest.length ? { ...imp, roles: rest } : undefined;
			}
		}
		this.bump();
	}

	addPermission(roleName: string, resource: string) {
		const role = this.rbac.roles[roleName];
		if (!role || (!this.getEntityByName(resource) && !this.isVirtualResource(resource))) return;
		if (!role.permissions) role.permissions = {};
		if (role.permissions[resource]) return;
		// First permission commits the role to per-resource — drop role-global keys
		// (the two forms are mutually exclusive).
		delete role.resources;
		delete role.actions;
		delete role.conditions;
		delete role.fields;
		role.permissions[resource] = { actions: ['read'] };
		this.bump();
	}
	removePermission(roleName: string, resource: string) {
		const role = this.rbac.roles[roleName];
		if (role?.permissions) {
			delete role.permissions[resource];
			this.bump();
		}
	}

	// ── the anonymous surface — rbac.public (ADR-026, UI-2) ────────────────────
	// Mirrors addPermission/removePermission for the public block: resource →
	// read-only grant. The engine's validatePublicBlock is the authority; the
	// modal's fixed-read UI + rbacIssues() keep the editor from producing what it
	// would reject.

	/** `$public` (and any `$…` name) can never be a schema role — the engine
	 *  reserves the alphabet for the compiled anonymous role (PublicRoleName). */
	private reservedRoleNameError(name: string): string | null {
		if (name === PUBLIC_ROLE_NAME) {
			return `"${PUBLIC_ROLE_NAME}" is reserved — anonymous access is edited in the "Public (anonymous)" entry, not as a role`;
		}
		if (name.startsWith('$')) return 'role names cannot start with "$" (reserved by the engine)';
		return null;
	}

	get publicGrantNames(): string[] {
		return Object.keys(this.rbac.public ?? {}).sort();
	}
	getPublicGrant(resource: string): ResourcePermission | undefined {
		return this.rbac.public?.[resource];
	}
	addPublicGrant(resource: string) {
		if (!this.getEntityByName(resource) && !this.isVirtualResource(resource)) return;
		if (!this.rbac.public) this.rbac.public = {};
		if (this.rbac.public[resource]) return;
		// The anonymous surface is read-only by engine rule — actions are fixed.
		this.rbac.public[resource] = { actions: ['read'] };
		this.bump();
	}
	removePublicGrant(resource: string) {
		if (!this.rbac.public?.[resource]) return;
		delete this.rbac.public[resource];
		if (Object.keys(this.rbac.public).length === 0) delete this.rbac.public;
		this.bump();
	}

	/** Upgrade a role-global role to the per-resource form, expanding its resources. */
	convertToPerResource(roleName: string) {
		const role = this.rbac.roles[roleName];
		if (!role || this.roleForm(roleName) === 'perResource') return;
		const actions = role.actions && role.actions.length > 0 ? [...role.actions] : ['read'];
		const perms: Record<string, ResourcePermission> = {};
		for (const res of this.roleResourceNames(role)) {
			const p: ResourcePermission = { actions: [...actions] };
			if (this.isVirtualResource(res)) {
				// The built-in files grant takes actions only — never carry
				// conditions/fields onto it (the engine rejects them).
				perms[res] = p;
				continue;
			}
			const valid = this.fieldNamesForResource(res);
			if (role.conditions?.field && valid.includes(role.conditions.field)) {
				p.conditions = { field: role.conditions.field, op: 'eq', val: role.conditions.val };
			}
			const here = (role.fields ?? []).filter((f) => valid.includes(f));
			if (here.length > 0) p.fields = here;
			perms[res] = p;
		}
		this.rbac.roles[roleName] = { permissions: perms };
		this.bump();
	}

	/** Resource names a role-global role applies to ('*' → every entity). A
	 *  virtual resource (the built-in `files` store) is kept, not filtered out —
	 *  dropping it silently was ST2. */
	roleResourceNames(role: RolePolicy): string[] {
		if (role.resources === '*') return this.entities.map((e) => e.name);
		if (Array.isArray(role.resources)) return role.resources.filter((r) => !!this.getEntityByName(r) || this.isVirtualResource(r));
		return [];
	}

	/** Valid RBAC field names for a resource: the implicit id + its declared fields. */
	fieldNamesForResource(resource: string): string[] {
		const e = this.getEntityByName(resource);
		return ['id', ...(e ? e.fields.map((f) => f.name) : [])];
	}
	/** Union of valid field names across resources (a role-global condition/allowlist). */
	rbacFieldUnion(resources: string[]): string[] {
		const set = new Set<string>(['id']);
		for (const r of resources) {
			const e = this.getEntityByName(r);
			if (e) for (const f of e.fields) set.add(f.name);
		}
		return [...set].sort();
	}

	// RBAC reference cleanup when the schema changes (keeps the policy valid). Only
	// per-resource permissions + role-global `resources` are touched precisely; a
	// role-global condition/allowlist field is union-scoped, so validate() flags it
	// rather than guessing.
	private rbacOnResourceDeleted(name: string) {
		for (const role of Object.values(this.rbac.roles)) {
			if (role.permissions) delete role.permissions[name];
			if (Array.isArray(role.resources)) role.resources = role.resources.filter((r) => r !== name);
		}
		if (this.rbac.public?.[name]) delete this.rbac.public[name];
	}
	private rbacOnResourceRenamed(oldName: string, newName: string) {
		for (const role of Object.values(this.rbac.roles)) {
			if (role.permissions?.[oldName]) {
				role.permissions[newName] = role.permissions[oldName];
				delete role.permissions[oldName];
			}
			if (Array.isArray(role.resources)) {
				role.resources = role.resources.map((r) => (r === oldName ? newName : r));
			}
		}
		if (this.rbac.public?.[oldName]) {
			this.rbac.public[newName] = this.rbac.public[oldName];
			delete this.rbac.public[oldName];
		}
	}
	private rbacOnFieldDeleted(resource: string, field: string) {
		for (const role of Object.values(this.rbac.roles)) {
			const p = role.permissions?.[resource];
			if (!p) continue;
			if (p.conditions?.field === field) {
				delete p.conditions;
				delete p.condition_actions;
			}
			if (p.fields) p.fields = p.fields.filter((f) => f !== field);
		}
		const pub = this.rbac.public?.[resource];
		if (pub) {
			if (pub.conditions?.field === field) delete pub.conditions;
			if (pub.fields) pub.fields = pub.fields.filter((f) => f !== field);
		}
	}
	private rbacOnFieldRenamed(resource: string, oldField: string, newField: string) {
		for (const role of Object.values(this.rbac.roles)) {
			const p = role.permissions?.[resource];
			if (p) {
				if (p.conditions?.field === oldField) p.conditions.field = newField;
				if (p.fields) p.fields = p.fields.map((f) => (f === oldField ? newField : f));
				continue;
			}
			// Role-global form: the condition/allowlist is union-scoped over every
			// covered resource. Repoint it only in the UNAMBIGUOUS case — the role
			// covers the renamed entity and no other covered entity still declares the
			// old column (else the shared reference is still valid there; validate()
			// flags the ambiguity instead of guessing).
			if (role.permissions) continue;
			const refersOld = role.conditions?.field === oldField || (role.fields ?? []).includes(oldField);
			if (!refersOld) continue;
			const covered = this.roleResourceNames(role);
			if (!covered.includes(resource)) continue;
			const stillHasOld = covered.some(
				(r) => r !== resource && this.getEntityByName(r)?.fields.some((f) => f.name === oldField)
			);
			if (stillHasOld) continue;
			if (role.conditions?.field === oldField) role.conditions.field = newField;
			if (role.fields) role.fields = role.fields.map((f) => (f === oldField ? newField : f));
		}
		// A public grant is per-resource, so the repoint is always unambiguous.
		const pub = this.rbac.public?.[resource];
		if (pub) {
			if (pub.conditions?.field === oldField) pub.conditions.field = newField;
			if (pub.fields) pub.fields = pub.fields.map((f) => (f === oldField ? newField : f));
		}
	}

	/** Live RBAC validation — mirrors the engine's validateRBAC for fast feedback.
	 *  Each message is prefixed `role "<name>"` so a UI can scope them per role. */
	rbacIssues(): string[] {
		const out: string[] = [];
		const actions = RBAC_ACTIONS as readonly string[];
		for (const [name, role] of Object.entries(this.rbac.roles)) {
			// A loaded schema may carry a reserved name (addRole/renameRole already
			// refuse it) — the engine rejects it at load, so surface it here too.
			if (name.startsWith('$')) {
				out.push(
					name === PUBLIC_ROLE_NAME
						? `role "${name}": reserved — anonymous access lives in rbac.public (the "Public (anonymous)" entry), never as a declared role`
						: `role "${name}": names starting with "$" are reserved by the engine`
				);
			}
			const perms = role.permissions ?? {};
			if (Object.keys(perms).length > 0) {
				if (role.resources !== undefined || role.actions !== undefined || role.conditions !== undefined || role.fields !== undefined) {
					out.push(`role "${name}": mixes per-resource and role-global keys — use one form`);
				}
				for (const [res, p] of Object.entries(perms)) {
					if (this.isVirtualResource(res)) {
						// The built-in "files" grant takes actions only — mirrors the
						// engine's files_grant_actions_only rule.
						if (!p.actions || p.actions.length === 0) out.push(`role "${name}" / ${res}: needs at least one action`);
						for (const a of p.actions ?? []) if (!actions.includes(a)) out.push(`role "${name}" / ${res}: unknown action "${a}"`);
						if (p.conditions || p.condition_actions || p.fields) {
							out.push(`role "${name}" / ${res}: the built-in "files" grant takes actions only (no conditions/fields)`);
						}
						continue;
					}
					if (!this.getEntityByName(res)) {
						out.push(`role "${name}": permission over unknown resource "${res}"`);
						continue;
					}
					const valid = this.fieldNamesForResource(res);
					if (!p.actions || p.actions.length === 0) out.push(`role "${name}" / ${res}: needs at least one action`);
					for (const a of p.actions ?? []) if (!actions.includes(a)) out.push(`role "${name}" / ${res}: unknown action "${a}"`);
					if (p.conditions?.field && !valid.includes(p.conditions.field)) {
						out.push(`role "${name}" / ${res}: condition field "${p.conditions.field}" is not a field of ${res}`);
					}
					if (p.condition_actions && p.condition_actions.length > 0 && !p.conditions?.field) {
						out.push(`role "${name}" / ${res}: condition_actions needs a condition`);
					}
					for (const ca of p.condition_actions ?? []) {
						if (ca === '*' || !actions.includes(ca)) out.push(`role "${name}" / ${res}: invalid condition action "${ca}"`);
						else if (!(p.actions ?? []).includes('*') && !(p.actions ?? []).includes(ca)) {
							out.push(`role "${name}" / ${res}: condition action "${ca}" is not in the granted actions`);
						}
					}
					for (const f of p.fields ?? []) if (!valid.includes(f)) out.push(`role "${name}" / ${res}: field "${f}" is not a field of ${res}`);
				}
			} else {
				const union = this.rbacFieldUnion(this.roleResourceNames(role));
				if (role.conditions?.field && !union.includes(role.conditions.field)) {
					out.push(`role "${name}": condition field "${role.conditions.field}" is not on its resources`);
				}
				for (const f of role.fields ?? []) if (!union.includes(f)) out.push(`role "${name}": field "${f}" is not on its resources`);
			}
		}
		// The anonymous surface — mirrors the engine's validatePublicBlock
		// (ADR-026): read-only, no condition_actions, literal condition vals
		// (anonymous has no identity), real resources/fields, files actions-only.
		for (const [res, p] of Object.entries(this.rbac.public ?? {})) {
			const pre = `public grant "${res}"`;
			if (!(p.actions?.length === 1 && p.actions[0] === 'read')) {
				out.push(`${pre}: the anonymous surface is READ-ONLY — actions must be exactly ["read"]`);
			}
			if (p.condition_actions && p.condition_actions.length > 0) {
				out.push(`${pre}: condition_actions is not valid on a public grant (read is the only action)`);
			}
			if (this.isVirtualResource(res)) {
				if (p.conditions || (p.fields && p.fields.length > 0)) {
					out.push(`${pre}: the built-in "files" grant takes actions only (no conditions/fields)`);
				}
				continue;
			}
			if (!this.getEntityByName(res)) {
				out.push(`${pre}: unknown resource "${res}"`);
				continue;
			}
			const valid = this.fieldNamesForResource(res);
			if (p.conditions) {
				if (p.conditions.val === '$user_id' || p.conditions.val === '$external_client_id') {
					out.push(
						`${pre}: condition compares against ${p.conditions.val}, but an anonymous request has no identity — use a literal (e.g. "published")`
					);
				}
				if (!p.conditions.field) out.push(`${pre}: condition field is required`);
				else if (!valid.includes(p.conditions.field)) {
					out.push(`${pre}: condition field "${p.conditions.field}" is not a field of ${res}`);
				}
			}
			for (const f of p.fields ?? []) {
				if (!valid.includes(f)) out.push(`${pre}: field "${f}" is not a field of ${res}`);
			}
		}
		return out;
	}

	// ── validation helpers ─────────────────────────────────────────────────────

	/** Live field-validation issues — mirrors the engine's validateFieldRules +
	 *  validateDefault (see schema/fieldRules.ts) so a rule the validator would
	 *  reject (min > max, default out of enum, a bad pattern, a rule on the wrong
	 *  type) is surfaced before deploy. Each message is prefixed with the field path. */
	fieldIssues(): string[] {
		const out: string[] = [];
		for (const e of this.entities) {
			for (const f of e.fields) {
				for (const msg of fieldDefIssues(f.def)) {
					out.push(`field "${e.name}.${f.name}": ${msg}`);
				}
			}
		}
		return out;
	}

	/** Live validation of the relational forms the engine accepts (UI-F2-S4): the
	 *  field-level `references` target column, composite `foreign_keys`, and declared
	 *  `indexes`. Mirrors validateForeignKeys + the references checks so a form the
	 *  validator would reject is surfaced before deploy. */
	dataModelIssues(): string[] {
		const out: string[] = [];
		for (const e of this.entities) {
			// Pending renames (UI-F4-S1): the engine rejects a renamed_from that still
			// names a DECLARED resource/field — e.g. rename empleados→personal and then
			// create a new "empleados". Surfaced live so it never reaches the deploy.
			if (e.originalName && e.originalName !== e.name && this.entities.some((o) => o.name === e.originalName)) {
				out.push(
					`entity "${e.name}": deploys as a rename of "${e.originalName}", but an entity named "${e.originalName}" still exists — the engine rejects this (rename it or delete the conflicting entity)`
				);
			}
			for (const f of e.fields) {
				if (f.originalName && f.originalName !== f.name && e.fields.some((o) => o.name === f.originalName)) {
					out.push(
						`field "${e.name}.${f.name}": deploys as a rename of "${f.originalName}", but a field named "${f.originalName}" still exists on ${e.name} — the engine rejects this`
					);
				}
			}
			// field-level references (FK to a non-id column).
			for (const f of e.fields) {
				const ref = f.def.references;
				if (!ref) continue;
				if (!f.def.relation) {
					out.push(`field "${e.name}.${f.name}": references needs a relation`);
					continue;
				}
				if (ref === 'id') continue;
				const t = this.getEntityByName(f.def.relation);
				if (!t) continue; // unknown relation already flagged elsewhere
				if (!this.referenceableColumns(f.def.relation).includes(ref)) {
					out.push(`field "${e.name}.${f.name}": references "${ref}" must be the target's id or a unique column of ${f.def.relation}`);
				} else {
					const refType = t.fields.find((x) => x.name === ref)?.def.type ?? '';
					if (pgKind(f.def.type) !== pgKind(refType)) {
						out.push(`field "${e.name}.${f.name}": references "${ref}" type mismatch (field is ${f.def.type}, ${f.def.relation}.${ref} is ${refType})`);
					}
				}
			}
			// composite foreign keys.
			(e.extras.foreign_keys ?? []).forEach((fk, i) => {
				const where = `entity "${e.name}" composite FK #${i + 1}`;
				const cols = fk.columns ?? [];
				const refs = fk.ref_columns ?? [];
				if (cols.length === 0 || cols.some((c) => !c)) out.push(`${where}: choose every source column`);
				if (!fk.target) {
					out.push(`${where}: choose a target`);
					return;
				}
				const t = this.getEntityByName(fk.target);
				if (!t) {
					out.push(`${where}: unknown target "${fk.target}"`);
					return;
				}
				if (cols.length !== refs.length) out.push(`${where}: source and referenced columns must match in count`);
				if (refs.some((c) => !c)) out.push(`${where}: choose every referenced column`);
				const colExists = (n: string) => n === 'id' || e.fields.some((f) => f.name === n);
				const refExists = (n: string) => n === 'id' || t.fields.some((f) => f.name === n);
				for (const c of cols) if (c && !colExists(c)) out.push(`${where}: column "${c}" is not a field of ${e.name}`);
				for (const c of refs) if (c && !refExists(c)) out.push(`${where}: referenced column "${c}" is not a field of ${fk.target}`);
				if (refs.length > 0 && refs.every(Boolean) && !this.columnsFormUniqueOnTarget(fk.target, refs)) {
					out.push(`${where}: referenced columns must form ${fk.target}'s primary key or a unique index`);
				}
				if (cols.length === refs.length) {
					for (let j = 0; j < cols.length; j++) {
						if (!cols[j] || !refs[j]) continue;
						const sk = cols[j] === 'id' ? 'uuid' : (e.fields.find((f) => f.name === cols[j])?.def.type ?? '');
						const rk = refs[j] === 'id' ? 'uuid' : (t.fields.find((f) => f.name === refs[j])?.def.type ?? '');
						if (sk && rk && pgKind(sk) !== pgKind(rk)) out.push(`${where}: type mismatch ${cols[j]} (${sk}) → ${fk.target}.${refs[j]} (${rk})`);
					}
				}
				const anyRequired = cols.some((c) => c !== 'id' && e.fields.find((f) => f.name === c)?.def.required);
				if ((fk.on_delete === 'set_null' || fk.on_update === 'set_null') && anyRequired) {
					out.push(`${where}: set_null requires all source columns to be nullable`);
				}
			});
			// declared indexes.
			(e.extras.indexes ?? []).forEach((idx, i) => {
				if (!idx.fields || idx.fields.length === 0) out.push(`entity "${e.name}" index #${i + 1}: choose at least one column`);
			});
		}
		return out;
	}

	/** Live validation of hooks — mirrors the engine (keys.go ValidHookEvents +
	 *  validator.go): a known event, after_* must be webhook (SEC-AUDIT-V2), and the
	 *  type's required field present (js→script, webhook→url, wasm→wasm_module). */
	hookIssues(): string[] {
		const out: string[] = [];
		for (const e of this.entities) {
			for (const [event, hook] of Object.entries(e.extras.hooks ?? {})) {
				const where = `entity "${e.name}" hook "${event}"`;
				if (!(HOOK_EVENTS as readonly string[]).includes(event)) {
					out.push(`${where}: unknown event (valid: ${HOOK_EVENTS.join(', ')})`);
					continue;
				}
				if (this.isAfterEvent(event) && (hook.type === 'js' || hook.type === 'wasm')) {
					out.push(`${where}: an after hook must be a webhook (a sandboxed ${hook.type} hook can't act post-commit)`);
					continue;
				}
				switch (hook.type) {
					case 'js':
						if (!hook.script?.trim()) out.push(`${where}: a js hook needs a script`);
						break;
					case 'webhook':
						if (!hook.url?.trim()) out.push(`${where}: a webhook needs a url`);
						break;
					case 'wasm':
						if (!hook.wasm_module?.trim()) out.push(`${where}: a wasm hook needs a wasm_module`);
						break;
					default:
						out.push(`${where}: unknown type "${hook.type}" (must be js, webhook or wasm)`);
				}
			}
		}
		return out;
	}

	validateResourceName(name: string, exceptId?: string): string | null {
		if (!IDENT_RE.test(name)) return 'must match ^[a-z][a-z0-9_]*$ (no hyphens)';
		if (name === 'transaction') return '"transaction" is reserved';
		if (name.startsWith('auth_')) return 'the "auth_" prefix is reserved';
		if (this.entities.some((e) => e.name === name && e.id !== exceptId)) return 'duplicate resource name';
		return null;
	}

	/**
	 * Client-side pre-deploy sanity check — fast, local feedback BEFORE the engine
	 * validates authoritatively (the server's parse + schema.Validate is the real
	 * gate; this just catches the obvious so a bad schema is never even sent).
	 * Returns human-readable issues; empty means "looks deployable".
	 */
	validate(): string[] {
		const issues: string[] = [];
		if (!this.schemaName.trim()) issues.push('the API needs a name');
		if (this.entities.length === 0) issues.push('add at least one entity before deploying');
		const seen = new Set<string>();
		for (const e of this.entities) {
			const nameErr = this.validateResourceName(e.name, e.id);
			if (nameErr) issues.push(`entity "${e.name || '(unnamed)'}": ${nameErr}`);
			if (seen.has(e.name)) issues.push(`duplicate entity name "${e.name}"`);
			seen.add(e.name);
			if (e.fields.length === 0) issues.push(`entity "${e.name}" has no fields`);
			const fseen = new Set<string>();
			for (const f of e.fields) {
				if (!IDENT_RE.test(f.name)) issues.push(`field "${e.name}.${f.name}" must match ^[a-z][a-z0-9_]*$`);
				if (fseen.has(f.name)) issues.push(`duplicate field "${e.name}.${f.name}"`);
				fseen.add(f.name);
				if (f.def.relation && !this.getEntityByName(f.def.relation)) {
					issues.push(`field "${e.name}.${f.name}" → unknown resource "${f.def.relation}"`);
				}
			}
		}
		issues.push(...this.fieldIssues());
		issues.push(...this.dataModelIssues());
		issues.push(...this.relationIssues());
		issues.push(...this.hookIssues());
		issues.push(...this.rbacIssues());
		return issues;
	}
}

export const editor = new EditorStore();
