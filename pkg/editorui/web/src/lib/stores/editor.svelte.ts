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

import type { APISchema, FieldDef, FieldType, RBACPolicy } from '../types/schema';
import { IDENT_RE } from '../types/schema';
import type { EntityModel, FieldModel, XY } from '../types/editor';
import { schemaToModel, modelToSchema } from '../schema/transform';
import { blankSchema } from '../schema/samples';
import { newId } from '../schema/ids';

export type EntityNodeData = { entityId: string };
export type RelationEdgeData = { fieldName: string; onDelete?: string; selfRef?: boolean };
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

class EditorStore {
	// ── model (deep reactive) ──────────────────────────────────────────────────
	entities = $state<EntityModel[]>([]);
	schemaName = $state('untitled-api');
	version = $state('1');
	schemaUrl = $state('https://appitools.dev/schema/v1');
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

	private bump() {
		this.revision++;
	}

	// ── load / export ──────────────────────────────────────────────────────────

	loadSchema(schema: APISchema) {
		const model = schemaToModel(schema);
		this.entities = model.entities;
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
		this.loadSchema(blankSchema(name));
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
		// Keep referencing relations/FKs pointed at the new name so edges survive.
		for (const other of this.entities) {
			for (const f of other.fields) if (f.def.relation === old) f.def.relation = name;
			for (const r of other.relations) if (r.def.target === old) r.def.target = name;
			for (const fk of other.extras.foreign_keys ?? []) if (fk.target === old) fk.target = name;
		}
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
			other.relations = other.relations.filter((r) => r.def.target !== name);
			if (other.extras.foreign_keys) {
				other.extras.foreign_keys = other.extras.foreign_keys.filter((fk) => fk.target !== name);
			}
		}
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
		const wasFk = !!e.fields.find((f) => f.id === fieldId)?.def.relation;
		e.fields = e.fields.filter((f) => f.id !== fieldId);
		if (this.selectedFieldId === fieldId) this.selectedFieldId = null;
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
		f.name = name;
		this.rebuildEdges();
		this.bump();
		return null;
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
		// Changing the type away from uuid clears the relation (a FK must be uuid).
		if (key === 'type' && value !== 'uuid' && f.def.relation) {
			delete f.def.relation;
			delete f.def.on_delete;
			delete f.def.on_update;
			delete f.def.references;
			this.rebuildEdges();
			this.structureVersion++;
		}
		if (affectsEdges) {
			this.rebuildEdges();
			this.structureVersion++;
		}
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
		const m = /^fk:([^:]+):(.+)$/.exec(edgeId);
		if (!m) return;
		this.deleteField(m[1], m[2]);
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

	// ── validation helpers ─────────────────────────────────────────────────────

	validateResourceName(name: string, exceptId?: string): string | null {
		if (!IDENT_RE.test(name)) return 'must match ^[a-z][a-z0-9_]*$ (no hyphens)';
		if (name === 'transaction') return '"transaction" is reserved';
		if (name.startsWith('auth_')) return 'the "auth_" prefix is reserved';
		if (this.entities.some((e) => e.name === name && e.id !== exceptId)) return 'duplicate resource name';
		return null;
	}
}

export const editor = new EditorStore();
