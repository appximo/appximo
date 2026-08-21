// Round-trip pin for the rbac block — above all `rbac.public` (ADR-026), which
// Studio used to DROP silently on every export/deploy (backlog UI-2): parse kept
// it only by accident and cleanRBACPolicy re-emitted `{ roles }` alone, so a
// public-only schema lost its WHOLE rbac block. Everything (export JSON,
// clipboard, CodeView, deploy PUT /admin/tenants/{id}/schema, POST
// /admin/engine/schema, validate-before-deploy) flows through modelToSchema, so
// these tests pin the contract at its single choke point.

import { describe, expect, it } from 'vitest';
import { deepEqual, modelToSchema, schemaToModel } from './transform';
import type { APISchema } from '../types/schema';

/** Canonical JSON: keys sorted recursively, so equality is byte-comparable. */
function canonical(v: unknown): string {
	return JSON.stringify(sortKeys(v));
}
function sortKeys(v: unknown): unknown {
	if (Array.isArray(v)) return v.map(sortKeys);
	if (v !== null && typeof v === 'object') {
		const out: Record<string, unknown> = {};
		for (const k of Object.keys(v as Record<string, unknown>).sort()) {
			out[k] = sortKeys((v as Record<string, unknown>)[k]);
		}
		return out;
	}
	return v;
}

const roundTrip = (s: APISchema): APISchema => modelToSchema(schemaToModel(s));

/** A schema exercising roles + the anonymous public surface together. */
function blogSchema(): APISchema {
	return {
		$schema: 'https://appximo.com/schema/v1',
		version: '1',
		name: 'blog-api',
		resources: {
			articulos: {
				fields: {
					titulo: { type: 'string', required: true, minLength: 1 },
					estado: { type: 'string', enum: ['borrador', 'publicado'], default: 'borrador' },
					autor_id: { type: 'uuid' }
				}
			}
		},
		rbac: {
			roles: {
				admin: { resources: '*', actions: ['*'] },
				autor: {
					permissions: {
						articulos: {
							actions: ['read', 'create', 'update'],
							conditions: { field: 'autor_id', op: 'eq', val: '$user_id' },
							condition_actions: ['update']
						}
					}
				}
			},
			public: {
				articulos: {
					actions: ['read'],
					conditions: { field: 'estado', op: 'eq', val: 'publicado' },
					fields: ['id', 'titulo', 'estado']
				},
				files: { actions: ['read'] }
			}
		}
	};
}

describe('rbac.public round-trip (UI-2)', () => {
	it('preserves rbac.public alongside roles — deep-equal and canonical-byte-equal', () => {
		const input = blogSchema();
		const out = roundTrip(input);
		expect(out.rbac).toBeDefined();
		expect(deepEqual(out.rbac, input.rbac)).toBe(true);
		expect(canonical(out.rbac)).toBe(canonical(input.rbac));
	});

	it('emits the rbac block for a roles-less, public-only schema', () => {
		const input: APISchema = {
			$schema: 'https://appximo.com/schema/v1',
			version: '1',
			name: 'catalogo',
			resources: {
				productos: {
					fields: {
						nombre: { type: 'string', required: true },
						visible: { type: 'string', enum: ['si', 'no'], default: 'si' }
					}
				}
			},
			// No roles at all — the public block is the entire rbac (legal: ADR-026).
			rbac: {
				public: {
					productos: { actions: ['read'], conditions: { field: 'visible', op: 'eq', val: 'si' } }
				}
			} as unknown as APISchema['rbac']
		};
		const out = roundTrip(input);
		expect(out.rbac).toBeDefined();
		expect(deepEqual(out.rbac?.public, input.rbac?.public)).toBe(true);
		// No empty `roles: {}` is invented either — byte-identical rbac block.
		expect(canonical(out.rbac)).toBe(canonical(input.rbac));
	});

	it('invents no public key on a schema without one (byte-stability for existing users)', () => {
		const input: APISchema = {
			$schema: 'https://appximo.com/schema/v1',
			version: '1',
			name: 'todo-api',
			resources: {
				tasks: {
					fields: {
						title: { type: 'string', required: true, minLength: 1, maxLength: 200 },
						status: { type: 'string', enum: ['open', 'done'], default: 'open' }
					}
				}
			},
			rbac: {
				roles: {
					admin: { resources: '*', actions: ['*'] },
					viewer: { resources: ['tasks'], actions: ['read'], fields: ['id', 'title', 'status'] }
				}
			}
		};
		const out = roundTrip(input);
		expect(out.rbac).toBeDefined();
		expect('public' in (out.rbac as object)).toBe(false);
		expect(canonical(out)).toBe(canonical(input));
	});

	it('keeps grant variants intact: literal condition, fields allowlist, files actions-only, relation limit', () => {
		const input: APISchema = {
			$schema: 'https://appximo.com/schema/v1',
			version: '1',
			name: 'tienda',
			resources: {
				categorias: { fields: { nombre: { type: 'string', required: true } } },
				productos: {
					fields: {
						nombre: { type: 'string', required: true },
						estado: { type: 'string', enum: ['oculto', 'publicado'] },
						categoria_id: { type: 'uuid', relation: 'categorias' }
					},
					relations: {
						// A relations-block embed with an explicit limit must ride along too.
						categoria: { type: 'belongs_to', target: 'categorias', fk: 'categoria_id', limit: 10 }
					}
				}
			},
			rbac: {
				roles: {},
				public: {
					productos: {
						actions: ['read'],
						conditions: { field: 'estado', op: 'eq', val: 'publicado' },
						fields: ['id', 'nombre', 'estado']
					},
					categorias: { actions: ['read'] },
					files: { actions: ['read'] }
				}
			}
		};
		const out = roundTrip(input);
		expect(deepEqual(out.rbac?.public, input.rbac?.public)).toBe(true);
		expect(out.resources.productos.relations?.categoria.limit).toBe(10);
		// The condition survives as a LITERAL (op pinned to eq, val untouched).
		expect(out.rbac?.public?.productos.conditions).toEqual({
			field: 'estado',
			op: 'eq',
			val: 'publicado'
		});
		// The files grant stays actions-only.
		expect(out.rbac?.public?.files).toEqual({ actions: ['read'] });
	});

	it('never emits condition_actions on a public grant (the engine rejects it)', () => {
		const input = blogSchema();
		// Simulate a hand-edited/imported grant carrying the forbidden key.
		input.rbac!.public!.articulos.condition_actions = ['read'];
		const out = roundTrip(input);
		expect(out.rbac?.public?.articulos.condition_actions).toBeUndefined();
		// The authenticated role's condition_actions is untouched by the rule.
		expect(out.rbac?.roles?.autor.permissions?.articulos.condition_actions).toEqual(['update']);
	});
});

// WRITE-ASYMMETRY-S1: the governed-field import grant round-trips faithfully —
// Studio must never silently drop a declared import (the UI-2 lesson, applied
// to the new key at birth instead of after a field report).
describe('import declaration round-trip', () => {
	function importSchema(): APISchema {
		return {
			$schema: 'https://appximo.com/schema/v1',
			version: '1',
			name: 'import-api',
			resources: {
				legacy_rows: {
					fields: {
						titulo: { type: 'string', required: true, minLength: 1 },
						creado_en: { type: 'time', auto: 'create' }
					},
					import: { roles: ['admin'], fields: ['id', 'creado_en'] }
				}
			},
			rbac: { roles: { admin: { resources: '*', actions: ['*'] } } }
		};
	}

	it('keeps roles and the fields subset byte-identically', () => {
		const out = roundTrip(importSchema());
		expect(canonical(out)).toBe(canonical(importSchema()));
	});

	it('keeps a full-set grant (no fields key) without inventing one', () => {
		const s = importSchema();
		delete s.resources.legacy_rows.import!.fields;
		const out = roundTrip(s);
		expect(out.resources.legacy_rows.import).toEqual({ roles: ['admin'] });
	});
});
