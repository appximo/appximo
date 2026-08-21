// The editor's working model — array-shaped, index-addressable, with stable
// client ids and canvas geometry. This is the in-memory representation the UI
// manipulates (the conceptual analogue of the engine's array-IR in
// pkg/aigen/ir.go: ordered objects with an explicit name, easy to address by
// index in a UI). The lossless conversion to/from the engine's map form
// (./types/schema.ts) lives in ../schema/transform.ts.
//
// Design for growth: every level keeps the full engine definition (FieldDef,
// RelationDef, …) plus an `extras` bag for resource-level keys without dedicated
// UI yet — so a round-trip is lossless TODAY even though the UI only surfaces a
// subset, and a future panel (RBAC, validations, state machines) just edits more
// of the same model. Nothing here is invented engine surface; it is the schema
// types reshaped for editing.

import type {
	FieldDef,
	RelationDef,
	IndexDef,
	ForeignKeyDef,
	HookConfig,
	ImportConfig,
	RBACPolicy
} from './schema';

export interface XY {
	x: number;
	y: number;
}

/** One field, with a stable id (handle/list key) and its full engine FieldDef. */
export interface FieldModel {
	id: string; // editor-only stable id (SvelteFlow handle id, {#each} key)
	name: string; // the field key in the schema
	def: FieldDef; // the complete engine definition (the property panel edits this)
	/** The column name the deployed baseline knows this field by (UI-F4-S1). Set on
	 *  import — an imported `renamed_from` wins (it IS the declared baseline), else
	 *  the imported name; undefined for a field created in this session (no deployed
	 *  data to preserve). Export DERIVES `renamed_from` from it whenever it differs
	 *  from the current name, so a rename deploys as the engine's data-preserving
	 *  ALTER … RENAME (MIG-F1-S2) instead of drop+create. Chained renames (a→b→c)
	 *  keep pointing at the baseline (a); renaming back to it emits nothing. */
	originalName?: string;
}

/** One declarative relation (?include= embed), id + full RelationDef. */
export interface RelationModel {
	id: string;
	name: string; // the embed name
	def: RelationDef;
}

/** Resource-level keys other than fields/relations — preserved losslessly.
 *  `renamed_from` is NOT here: it is DERIVED at export from EntityModel.originalName
 *  (UI-F4-S1), never edited as a raw extra. */
export interface EntityExtras {
	indexes?: IndexDef[];
	foreign_keys?: ForeignKeyDef[];
	hooks?: Record<string, HookConfig>;
	events?: string[];
	/** Governed-field create grant (WRITE-ASYMMETRY-S1) — authored in the
	 *  entity inspector's Import section, preserved losslessly otherwise. */
	import?: ImportConfig;
}

/** One resource (table) = one ERD node. */
export interface EntityModel {
	id: string; // editor-only stable id == SvelteFlow node id
	name: string; // the resource key in the schema
	fields: FieldModel[];
	relations: RelationModel[];
	extras: EntityExtras;
	position: XY; // canvas geometry — editor-only, stripped on export
	/** The table name the deployed baseline knows this resource by (UI-F4-S1) —
	 *  same contract as FieldModel.originalName: imported `renamed_from` ?? imported
	 *  name; undefined for an entity created in this session. Export derives
	 *  `renamed_from` from it so an entity rename deploys as ALTER TABLE … RENAME
	 *  (data preserved), never as drop+create. */
	originalName?: string;
}

/** The whole schema in editor form. */
export interface SchemaModel {
	$schema: string;
	version: string;
	name: string;
	entities: EntityModel[];
	rbac: RBACPolicy; // roles + the anonymous rbac.public block — both preserved AND authored (RbacModal, UI-F2-S1/UI-2)
	workflows?: Record<string, unknown>; // preserved verbatim
}
