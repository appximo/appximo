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
}

/** One declarative relation (?include= embed), id + full RelationDef. */
export interface RelationModel {
	id: string;
	name: string; // the embed name
	def: RelationDef;
}

/** Resource-level keys other than fields/relations — preserved losslessly. */
export interface EntityExtras {
	indexes?: IndexDef[];
	foreign_keys?: ForeignKeyDef[];
	hooks?: Record<string, HookConfig>;
	events?: string[];
	renamed_from?: string;
}

/** One resource (table) = one ERD node. */
export interface EntityModel {
	id: string; // editor-only stable id == SvelteFlow node id
	name: string; // the resource key in the schema
	fields: FieldModel[];
	relations: RelationModel[];
	extras: EntityExtras;
	position: XY; // canvas geometry — editor-only, stripped on export
}

/** The whole schema in editor form. */
export interface SchemaModel {
	$schema: string;
	version: string;
	name: string;
	entities: EntityModel[];
	rbac: RBACPolicy; // preserved as-is; the RBAC visual panel is a future increment
	workflows?: Record<string, unknown>; // preserved verbatim
}
