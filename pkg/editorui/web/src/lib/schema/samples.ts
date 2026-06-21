// Built-in example schemas, embedded so "Load example" works fully offline.
// These are verbatim copies of the canonical engine examples (quickstart,
// erp-demo) plus a feature-rich shop sample (state machine, on_delete,
// relations) — importing one and exporting it round-trips identically.

import type { APISchema } from '../types/schema';
import todo from './samples-todo.json';
import erp from './samples-erp.json';
import shop from './samples-shop.json';

export interface Sample {
	id: string;
	label: string;
	description: string;
	schema: APISchema;
}

export const SAMPLES: Sample[] = [
	{
		id: 'todo',
		label: 'Todo API',
		description: 'The minimal canonical example — one resource.',
		schema: todo as APISchema
	},
	{
		id: 'shop',
		label: 'Shop',
		description: 'Customers, products, orders with a state machine + FKs.',
		schema: shop as APISchema
	},
	{
		id: 'erp',
		label: 'Nimbus ERP',
		description: 'A rich HR/ERP model: relations, m2m, hooks, events, indexes.',
		schema: erp as APISchema
	}
];

/** An empty starter schema for "New". */
export function blankSchema(name = 'my-api'): APISchema {
	return {
		$schema: 'https://appitools.dev/schema/v1',
		version: '1',
		name,
		resources: {},
		rbac: { roles: { admin: { resources: '*', actions: ['*'] } } }
	};
}
