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
import { IDENT_RE, RBAC_ACTIONS, HOOK_EVENTS } from '../types/schema';
import type { ForeignKeyDef, IndexDef, ReferentialAction, HookConfig, HookEvent } from '../types/schema';
import type { EntityModel, FieldModel, XY } from '../types/editor';
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
			other.relations = other.relations.filter((r) => r.def.target !== name);
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
		const m = /^fk:([^:]+):(.+)$/.exec(edgeId);
		if (!m) return;
		this.deleteField(m[1], m[2]);
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
		this.bump();
	}

	// ── hooks (UI-F2-S5) ────────────────────────────────────────────────────────
	// hooks is a MAP keyed by event (one hook per event), in entity.extras.hooks
	// (deep $state). Fidelity to SEC-AUDIT-V2: an after_* hook may ONLY be a webhook
	// (a sandboxed js/wasm hook post-commit is a no-op the engine rejects at load).

	/** True for after_create / after_update (webhook-only events). */
	isAfterEvent(event: string): boolean {
		return event === 'after_create' || event === 'after_update';
	}
	/** Hook types valid for an event: after ⇒ webhook only; before ⇒ js/webhook/wasm. */
	hookTypesFor(event: string): Array<'js' | 'webhook' | 'wasm'> {
		return this.isAfterEvent(event) ? ['webhook'] : ['js', 'webhook', 'wasm'];
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
		if (this.rbac.roles[name]) return 'duplicate role name';
		if (!this.rbac.roles[oldName]) return 'role not found';
		const next: Record<string, RolePolicy> = {};
		for (const [k, v] of Object.entries(this.rbac.roles)) next[k === oldName ? name : k] = v;
		this.rbac.roles = next;
		this.bump();
		return null;
	}
	deleteRole(name: string) {
		delete this.rbac.roles[name];
		this.bump();
	}

	addPermission(roleName: string, resource: string) {
		const role = this.rbac.roles[roleName];
		if (!role || !this.getEntityByName(resource)) return;
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

	/** Upgrade a role-global role to the per-resource form, expanding its resources. */
	convertToPerResource(roleName: string) {
		const role = this.rbac.roles[roleName];
		if (!role || this.roleForm(roleName) === 'perResource') return;
		const actions = role.actions && role.actions.length > 0 ? [...role.actions] : ['read'];
		const perms: Record<string, ResourcePermission> = {};
		for (const res of this.roleResourceNames(role)) {
			const valid = this.fieldNamesForResource(res);
			const p: ResourcePermission = { actions: [...actions] };
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

	/** Resource names a role-global role applies to ('*' → every entity). */
	roleResourceNames(role: RolePolicy): string[] {
		if (role.resources === '*') return this.entities.map((e) => e.name);
		if (Array.isArray(role.resources)) return role.resources.filter((r) => !!this.getEntityByName(r));
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
	}

	/** Live RBAC validation — mirrors the engine's validateRBAC for fast feedback.
	 *  Each message is prefixed `role "<name>"` so a UI can scope them per role. */
	rbacIssues(): string[] {
		const out: string[] = [];
		const actions = RBAC_ACTIONS as readonly string[];
		for (const [name, role] of Object.entries(this.rbac.roles)) {
			const perms = role.permissions ?? {};
			if (Object.keys(perms).length > 0) {
				if (role.resources !== undefined || role.actions !== undefined || role.conditions !== undefined || role.fields !== undefined) {
					out.push(`role "${name}": mixes per-resource and role-global keys — use one form`);
				}
				for (const [res, p] of Object.entries(perms)) {
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
		issues.push(...this.hookIssues());
		issues.push(...this.rbacIssues());
		return issues;
	}
}

export const editor = new EditorStore();
