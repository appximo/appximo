// Engine schema types — a faithful TypeScript mirror of pkg/schema/types.go.
//
// These types are the CONTRACT between the editor and the Appitools engine: the
// editor imports/exports exactly this shape (the "map form" the engine consumes
// and `appitools validate` accepts). Every key here is audited against types.go
// and the AGENTS.md integration guide. Do NOT invent keys the engine rejects —
// the validator is strict-key.
//
// The editor's own working model (array-shaped, with stable ids + canvas
// geometry) lives in ./editor.ts; the lossless conversion between the two is in
// ../schema/transform.ts.

/** The complete set of field types the engine accepts (pkg/schema/validator.go validFieldTypes). */
export const FIELD_TYPES = [
	'string',
	'text',
	'int',
	'int64',
	'float64',
	'bool',
	'uuid',
	'time',
	'json',
	'jsonb',
	'file'
] as const;
export type FieldType = (typeof FIELD_TYPES)[number];

/** Declarative relation kinds for the ?include= embeds (RELATIONS-V1). */
export const RELATION_TYPES = ['has_many', 'belongs_to', 'many_to_many'] as const;
export type RelationType = (typeof RELATION_TYPES)[number];

/** Referential actions for on_delete / on_update. */
export const REFERENTIAL_ACTIONS = ['restrict', 'cascade', 'set_null'] as const;
export type ReferentialAction = (typeof REFERENTIAL_ACTIONS)[number];

/** RBAC actions (deny by default; "*" is wildcard). */
export const RBAC_ACTIONS = ['read', 'create', 'update', 'delete', '*'] as const;
export type RbacAction = (typeof RBAC_ACTIONS)[number];

/** String/text `format` validators (exactly one allowed per field). */
export const FIELD_FORMATS = ['email', 'uuid', 'url', 'date'] as const;
export type FieldFormat = (typeof FIELD_FORMATS)[number];

/** Lifecycle events a hook can declare. After-hooks must be webhook (see AGENTS.md). */
export const HOOK_EVENTS = [
	'before_create',
	'after_create',
	'before_update',
	'after_update'
] as const;
export type HookEvent = (typeof HOOK_EVENTS)[number];

/** Outbox emit actions (present tense; topic uses past tense). */
export const EVENT_ACTIONS = ['create', 'update', 'delete'] as const;
export type EventAction = (typeof EVENT_ACTIONS)[number];

/** State-machine lifecycle for a status field (G5). `initial` is a string or array. */
export interface StateMachine {
	initial: string | string[];
	transitions: Record<string, string[]>;
}

/** One field within a resource. Mirrors schema.FieldDef. All keys but `type` optional. */
export interface FieldDef {
	type: FieldType;
	required?: boolean;
	unique?: boolean;
	auto?: boolean;
	enum?: string[];
	relation?: string;
	on_delete?: ReferentialAction;
	on_update?: ReferentialAction;
	references?: string;
	renamed_from?: string;
	default?: unknown;
	min?: number;
	max?: number;
	minLength?: number;
	maxLength?: number;
	pattern?: string;
	format?: FieldFormat;
	state_machine?: StateMachine;
}

/** Declarative relation (served via ?include=). Mirrors schema.RelationDef. */
export interface RelationDef {
	type: RelationType;
	target: string;
	fk: string;
	through?: string;
	target_fk?: string;
	limit?: number;
}

/** Index access methods (LIBRARY-GAPS-S1). Empty/absent means btree. */
export const INDEX_METHODS = ['btree', 'gin'] as const;
export type IndexMethod = (typeof INDEX_METHODS)[number];

/** Operator classes a gin index may declare (schema.validIndexOpclasses). */
export const GIN_OPCLASSES = ['jsonb_ops', 'jsonb_path_ops'] as const;
export type IndexOpclass = (typeof GIN_OPCLASSES)[number];

/** One index over one or more columns. Mirrors schema.IndexDef. */
export interface IndexDef {
	fields: string[];
	unique?: boolean;
	/** btree (default) | gin — gin only over jsonb columns, never unique. */
	method?: IndexMethod;
	/** Operator class applied to every column; gin only. */
	opclass?: IndexOpclass;
}

/** Resource-level composite foreign key (MIG-F1-S5). Mirrors schema.ForeignKeyDef. */
export interface ForeignKeyDef {
	columns: string[];
	target: string;
	ref_columns: string[];
	on_delete?: ReferentialAction;
	on_update?: ReferentialAction;
}

/** Lifecycle hook config. Mirrors schema.HookConfig. */
export interface HookConfig {
	type: 'js' | 'webhook' | 'wasm';
	script?: string;
	url?: string;
	hmac_secret_env?: string;
	wasm_module?: string;
	wasm_fn?: string;
	timeout?: string;
}

/** A single resource (table). Mirrors schema.ResourceSchema. */
export interface ResourceSchema {
	fields: Record<string, FieldDef>;
	relations?: Record<string, RelationDef>;
	hooks?: Record<string, HookConfig>;
	indexes?: IndexDef[];
	foreign_keys?: ForeignKeyDef[];
	events?: string[];
	renamed_from?: string;
}

/** Row-level predicate. Mirrors schema.Condition. */
export interface Condition {
	field: string;
	op: string;
	val: string;
}

/** Per-resource grant inside a role's `permissions` map (G2). */
export interface ResourcePermission {
	actions: string[];
	conditions?: Condition;
	condition_actions?: string[];
	fields?: string[];
}

/** A role policy — role-global form OR the per-resource `permissions` form. */
export interface RolePolicy {
	resources?: '*' | string[];
	actions?: string[];
	conditions?: Condition;
	fields?: string[];
	permissions?: Record<string, ResourcePermission>;
}

/** The RBAC block. Mirrors schema.RBACPolicy. */
export interface RBACPolicy {
	roles: Record<string, RolePolicy>;
}

/** The top-level Appitools schema (map form). Mirrors schema.APISchema. */
export interface APISchema {
	$schema: string;
	version: string;
	name: string;
	resources: Record<string, ResourceSchema>;
	rbac?: RBACPolicy;
	// Reserved for the Phase-2 workflow engine; preserved verbatim on round-trip.
	workflows?: Record<string, unknown>;
}

export const SCHEMA_URL = 'https://appitools.dev/schema/v1';

/** Resource/field identifier rule enforced by the engine at load. */
export const IDENT_RE = /^[a-z][a-z0-9_]*$/;

/** Names the engine reserves (cannot be a resource). */
export const RESERVED_RESOURCE_NAMES = new Set(['transaction']);
export const RESERVED_RESOURCE_PREFIX = 'auth_';
