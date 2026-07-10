// Flow-step assistance (FLOWTEST-POWER-S1) — everything the engine already
// KNOWS about the API, brought into the Flows editor as pure functions:
//
//   - the endpoint catalog (from the SERVED schema — /editor/current-schema —
//     because flows run against the live router, not the unsaved canvas);
//   - schema-driven request bodies (real fields, required marked, enum/format/
//     default/pattern respected — "fill a valid example", "required only");
//   - response dot-path suggestions per endpoint kind (for asserts + captures);
//   - the full assertion vocabulary with inline help (mirror of
//     pkg/flowtest validOps — do not invent ops the runner rejects);
//   - the GraphQL surface derived from the schema (introspection is blocked in
//     production, but queries/mutations are a deterministic function of the
//     schema — the naming mirrors pkg/graphql/handler.go exactly);
//   - a pre-run lint (missing required fields, unknown fields, bad enum
//     values, invalid JSON) so a red step is caught BEFORE running;
//   - OpenAPI doc lookup (/openapi.json) so each step shows what the endpoint
//     expects/returns without leaving to Swagger.
//
// Pure module: no stores, no fetches — testable and reusable.

import type { APISchema, ResourceSchema, FieldDef } from '../types/schema';

// ── naming (mirror of pkg/graphql/handler.go singular/toPascalCase) ──────────

export function gqlSingular(name: string): string {
	if (name.endsWith('ches')) return name.slice(0, -2); // dispatches → dispatch
	if (name.endsWith('ses')) return name.slice(0, -3) + 's'; // uses → use
	if (name.endsWith('ies')) return name.slice(0, -3) + 'y'; // categories → category
	return name.endsWith('s') ? name.slice(0, -1) : name;
}

export function toPascal(name: string): string {
	return name
		.split('_')
		.map((p) => (p ? p[0].toUpperCase() + p.slice(1) : ''))
		.join('');
}

// ── endpoint catalog ──────────────────────────────────────────────────────────

export type EndpointKind =
	| 'list'
	| 'get'
	| 'create'
	| 'replace'
	| 'patch'
	| 'delete'
	| 'aggregate'
	| 'login'
	| 'signup'
	| 'refresh'
	| 'upload'
	| 'download'
	| 'graphql'
	| 'transaction'
	| 'other';

export interface EndpointOption {
	id: string;
	group: string;
	label: string;
	method: string;
	path: string; // may contain a {{var}} placeholder for /{id} routes
	kind: EndpointKind;
	resource?: string;
}

/** The endpoint picker's catalog, built from the SERVED schema. */
export function endpointCatalog(schema: APISchema | null): EndpointOption[] {
	const out: EndpointOption[] = [
		{ id: 'auth:login', group: 'auth', label: 'POST /auth/login — sign in, returns {user, token}', method: 'POST', path: '/auth/login', kind: 'login' },
		{ id: 'auth:signup', group: 'auth', label: 'POST /auth/signup — create a user (if enabled)', method: 'POST', path: '/auth/signup', kind: 'signup' },
		{ id: 'auth:refresh', group: 'auth', label: 'POST /auth/refresh — re-mint the token', method: 'POST', path: '/auth/refresh', kind: 'refresh' }
	];
	for (const [name, res] of Object.entries(schema?.resources ?? {})) {
		const sing = gqlSingular(name);
		const idVar = `{{${sing}_id}}`;
		const g = name;
		out.push(
			{ id: `list:${name}`, group: g, label: `GET /api/${name} — list`, method: 'GET', path: `/api/${name}`, kind: 'list', resource: name },
			{ id: `create:${name}`, group: g, label: `POST /api/${name} — create`, method: 'POST', path: `/api/${name}`, kind: 'create', resource: name },
			{ id: `get:${name}`, group: g, label: `GET /api/${name}/{id} — get one`, method: 'GET', path: `/api/${name}/${idVar}`, kind: 'get', resource: name },
			{ id: `patch:${name}`, group: g, label: `PATCH /api/${name}/{id} — partial update`, method: 'PATCH', path: `/api/${name}/${idVar}`, kind: 'patch', resource: name },
			{ id: `replace:${name}`, group: g, label: `PUT /api/${name}/{id} — full replace`, method: 'PUT', path: `/api/${name}/${idVar}`, kind: 'replace', resource: name },
			{ id: `delete:${name}`, group: g, label: `DELETE /api/${name}/{id} — delete`, method: 'DELETE', path: `/api/${name}/${idVar}`, kind: 'delete', resource: name },
			{ id: `aggregate:${name}`, group: g, label: `GET /api/${name}/aggregate — count/sum/avg…`, method: 'GET', path: `/api/${name}/aggregate?count`, kind: 'aggregate', resource: name }
		);
		void res;
	}
	out.push(
		{ id: 'files:upload', group: 'files', label: 'POST /api/files — upload (multipart)', method: 'POST', path: '/api/files', kind: 'upload' },
		{ id: 'files:get', group: 'files', label: 'GET /api/files/{id} — download', method: 'GET', path: '/api/files/{{file_id}}', kind: 'download' },
		{ id: 'graphql', group: 'graphql', label: 'POST /graphql — GraphQL query/mutation', method: 'POST', path: '/graphql', kind: 'graphql' }
	);
	return out;
}

/** Identify what a step's method+path point at (works on hand-typed paths too). */
export function detectEndpoint(
	method: string,
	path: string,
	schema: APISchema | null
): { kind: EndpointKind; resource?: string } {
	const p = (path || '').split('?')[0];
	const m = method.toUpperCase();
	if (p.startsWith('/graphql')) return { kind: 'graphql' };
	if (p === '/auth/login') return { kind: 'login' };
	if (p === '/auth/signup') return { kind: 'signup' };
	if (p === '/auth/refresh') return { kind: 'refresh' };
	if (p === '/api/transaction') return { kind: 'transaction' };
	if (p === '/api/files') return { kind: m === 'POST' ? 'upload' : 'other' };
	if (p.startsWith('/api/files/')) return { kind: 'download' };
	const seg = p.replace(/^\/api\//, '').split('/');
	const name = seg[0] ?? '';
	if (!schema?.resources?.[name]) return { kind: 'other' };
	if (seg.length === 1) {
		if (m === 'GET') return { kind: 'list', resource: name };
		if (m === 'POST') return { kind: 'create', resource: name };
		return { kind: 'other', resource: name };
	}
	if (seg.length === 2 && seg[1] === 'aggregate') return { kind: 'aggregate', resource: name };
	if (seg.length === 2) {
		if (m === 'GET') return { kind: 'get', resource: name };
		if (m === 'PUT') return { kind: 'replace', resource: name };
		if (m === 'PATCH') return { kind: 'patch', resource: name };
		if (m === 'DELETE') return { kind: 'delete', resource: name };
	}
	return { kind: 'other', resource: name };
}

// ── schema-driven bodies ──────────────────────────────────────────────────────

/** Best-effort sample for a subset of RE2: literals, \d/\w, [classes] with
 *  ranges, quantifiers {n}/{m,n}/+/?/* and anchors. Unsupported → null (the
 *  field falls back to a plain placeholder and the hint shows the pattern). */
export function patternSample(pattern: string): string | null {
	let src = pattern;
	if (src.startsWith('^')) src = src.slice(1);
	if (src.endsWith('$')) src = src.slice(0, -1);
	let out = '';
	let i = 0;
	const pick = (chars: string): string => chars[Math.floor(Math.random() * chars.length)] ?? '';
	const expand = (cls: string): string | null => {
		// expand a [...] body into its member chars ("A-Z0-9_-")
		if (cls.startsWith('^')) return null; // negated class: don't guess
		let chars = '';
		for (let j = 0; j < cls.length; j++) {
			const c = cls[j];
			if (c === '\\' && j + 1 < cls.length) {
				const n = cls[j + 1];
				if (n === 'd') chars += '0123456789';
				else if (n === 'w') chars += 'abcdefghijklmnopqrstuvwxyz0123456789_';
				else chars += n;
				j++;
			} else if (cls[j + 1] === '-' && j + 2 < cls.length && cls[j + 2] !== ']') {
				const from = cls.charCodeAt(j);
				const to = cls.charCodeAt(j + 2);
				if (to < from || to - from > 200) return null;
				for (let k = from; k <= to; k++) chars += String.fromCharCode(k);
				j += 2;
			} else {
				chars += c;
			}
		}
		return chars || null;
	};
	const readQuant = (): number | null => {
		// returns repetitions for the quantifier at i (consuming it), 1 if none
		if (i >= src.length) return 1;
		const c = src[i];
		if (c === '{') {
			const close = src.indexOf('}', i);
			if (close === -1) return null;
			const body = src.slice(i + 1, close);
			i = close + 1;
			const m = body.match(/^(\d+)(,(\d+)?)?$/);
			if (!m) return null;
			return parseInt(m[1], 10); // the minimum satisfies the pattern
		}
		if (c === '+') {
			i++;
			return 1;
		}
		if (c === '*' || c === '?') {
			i++;
			return 0;
		}
		return 1;
	};
	while (i < src.length) {
		const c = src[i];
		let chars: string | null = null;
		if (c === '[') {
			const close = src.indexOf(']', i + 1);
			if (close === -1) return null;
			chars = expand(src.slice(i + 1, close));
			if (chars === null) return null;
			i = close + 1;
		} else if (c === '\\' && i + 1 < src.length) {
			const n = src[i + 1];
			if (n === 'd') chars = '0123456789';
			else if (n === 'w') chars = 'abcdefghijklmnopqrstuvwxyz0123456789_';
			else if (n === 's') chars = ' ';
			else chars = n; // escaped literal (\. \+ …)
			i += 2;
		} else if ('(|)^$.?*+'.includes(c)) {
			return null; // groups/alternation/dot: don't guess
		} else {
			chars = c;
			i++;
		}
		const reps = readQuant();
		if (reps === null) return null;
		for (let r = 0; r < reps; r++) out += pick(chars);
	}
	return out;
}

/** A valid example value for one field — enum/default/format/pattern/min/max
 *  respected; FK fields become {{capture}} placeholders (chain from a previous
 *  step); unique strings get a {{run_id}} suffix so re-runs never collide. */
export function exampleValue(name: string, def: FieldDef): unknown {
	if (def.enum && def.enum.length > 0) {
		if (typeof def.default === 'string' && def.enum.includes(def.default)) return def.default;
		return def.enum[0];
	}
	if (def.default !== undefined && def.default !== null) {
		if (def.type === 'time' && def.default === 'now') return new Date().toISOString();
		return def.default;
	}
	if (def.relation) {
		// an FK: the real value comes from a previous step's capture
		return `{{${gqlSingular(def.relation)}_id}}`;
	}
	switch (def.type) {
		case 'file':
			return '{{file_id}}';
		case 'uuid':
			return crypto.randomUUID();
		case 'bool':
			return true;
		case 'int':
		case 'int64': {
			let v = 1;
			if (def.min !== undefined) v = Math.max(v, Math.ceil(def.min));
			if (def.max !== undefined) v = Math.min(v, Math.floor(def.max));
			return v;
		}
		case 'float64': {
			let v = 1;
			if (def.min !== undefined) v = Math.max(v, def.min);
			if (def.max !== undefined) v = Math.min(v, def.max);
			return v;
		}
		case 'time':
			return new Date().toISOString();
		case 'json':
			return {};
		default: {
			// string / text
			if (def.format === 'email') return 'flow-{{run_id}}@example.com';
			if (def.format === 'url') return 'https://example.com/demo';
			if (def.format === 'uuid') return crypto.randomUUID();
			if (def.format === 'date') return new Date().toISOString().slice(0, 10);
			if (def.pattern) {
				const s = patternSample(def.pattern);
				if (s !== null) return s;
				return ''; // unsupported pattern: leave visible for the user to fill
			}
			let s = `demo ${name.replace(/_/g, ' ')}`;
			if (def.unique) {
				// {{run_tag}} expands to 8 chars at run time (the runner's short
				// unique salt) — size the prefix so the RUNTIME value fits maxLength
				const cap = def.maxLength ?? Infinity;
				if (cap >= 10) {
					s = `${name.slice(0, Math.min(name.length, cap - 9))}-{{run_tag}}`;
				} else {
					// too tight even for the salt: a random token (re-runs may 409)
					s = Math.random().toString(36).slice(2, 2 + Math.max(1, Math.min(cap, 8)));
				}
				return s;
			}
			if (def.minLength !== undefined && s.length < def.minLength)
				s = s.padEnd(def.minLength, 'x');
			if (def.maxLength !== undefined && s.length > def.maxLength)
				s = s.slice(0, def.maxLength);
			return s;
		}
	}
}

/** Writable fields of a resource (skips engine-managed auto columns). */
function writableFields(res: ResourceSchema): [string, FieldDef][] {
	return Object.entries(res.fields).filter(([, d]) => !d.auto);
}

/** Build a ready-to-edit JSON body from the schema. requiredOnly = the minimal
 *  valid body: required fields WITHOUT a default (a required field with a
 *  default is satisfied when omitted). */
export function buildBody(res: ResourceSchema, requiredOnly: boolean): string {
	const body: Record<string, unknown> = {};
	for (const [name, def] of writableFields(res)) {
		if (requiredOnly && (!def.required || def.default !== undefined)) continue;
		body[name] = exampleValue(name, def);
	}
	return JSON.stringify(body, null, 2);
}

/** One field's hint row for the "what can I send" panel. */
export interface FieldHint {
	name: string;
	type: string;
	required: boolean;
	badges: string[]; // compact validation summary
}

export function fieldHints(res: ResourceSchema): FieldHint[] {
	const out: FieldHint[] = [];
	for (const [name, def] of Object.entries(res.fields)) {
		const badges: string[] = [];
		if (def.auto) badges.push('auto (engine-managed — do not send)');
		if (def.unique) badges.push('unique');
		if (def.enum) badges.push(`enum: ${def.enum.join(' | ')}`);
		if (def.default !== undefined) badges.push(`default: ${JSON.stringify(def.default)}`);
		if (def.relation) badges.push(`FK → ${def.relation} (expects an existing id — capture it from a previous step)`);
		if (def.type === 'file') badges.push('file_id from POST /api/files');
		if (def.format) badges.push(`format: ${def.format}`);
		if (def.pattern) badges.push(`pattern: ${def.pattern}`);
		if (def.min !== undefined || def.max !== undefined)
			badges.push(`range: ${def.min ?? '−∞'}…${def.max ?? '∞'}`);
		if (def.minLength !== undefined || def.maxLength !== undefined)
			badges.push(`length: ${def.minLength ?? 0}…${def.maxLength ?? '∞'}`);
		if (def.state_machine) badges.push('state machine (create only in an initial state)');
		out.push({ name, type: def.type, required: !!def.required, badges });
	}
	return out;
}

// ── pre-run lint ─────────────────────────────────────────────────────────────

/** Parse a body that may contain {{var}} holes (they substitute at run time). */
function parseBodyWithVars(text: string): Record<string, unknown> | null {
	for (const replaced of [text, text.replace(/\{\{\s*[a-zA-Z_][a-zA-Z0-9_]*\s*\}\}/g, '0')]) {
		try {
			const v = JSON.parse(replaced) as unknown;
			if (v && typeof v === 'object' && !Array.isArray(v)) return v as Record<string, unknown>;
			return null;
		} catch {
			/* try next */
		}
	}
	return null;
}

/** Pre-run warnings for a step: caught here, not as a 422 after running. */
export function lintStep(
	method: string,
	path: string,
	bodyText: string,
	schema: APISchema | null
): string[] {
	const warns: string[] = [];
	const { kind, resource } = detectEndpoint(method, path, schema);
	const res = resource ? schema?.resources?.[resource] : undefined;
	const needsBody = kind === 'create' || kind === 'replace' || kind === 'patch';
	if (!needsBody || !res) return warns;
	if (!bodyText.trim()) {
		warns.push('body is empty — this endpoint expects a JSON body');
		return warns;
	}
	const body = parseBodyWithVars(bodyText);
	if (body === null) {
		warns.push('body is not valid JSON');
		return warns;
	}
	const fields = res.fields;
	for (const key of Object.keys(body)) {
		if (key !== 'id' && !fields[key]) warns.push(`"${key}" is not a field of ${resource} (would 422 unknown_field)`);
		const def = fields[key];
		if (def?.auto) warns.push(`"${key}" is auto (engine-managed) — remove it`);
		if (def?.enum && typeof body[key] === 'string' && !/\{\{/.test(String(body[key])) && !def.enum.includes(String(body[key])))
			warns.push(`"${key}": "${body[key]}" is not in enum [${def.enum.join(', ')}]`);
	}
	if (kind === 'create' || kind === 'replace') {
		for (const [name, def] of Object.entries(fields)) {
			if (!def.required || def.auto) continue;
			if (kind === 'create' && def.default !== undefined) continue; // satisfied by the default
			if (!(name in body)) warns.push(`required field "${name}" is missing`);
		}
	}
	return warns;
}

// ── assertions: the full vocabulary, with help (mirror of pkg/flowtest) ──────

export interface AssertOpInfo {
	op: string;
	label: string;
	needsValue: boolean;
	help: string;
}

export const ASSERT_OPS: AssertOpInfo[] = [
	{ op: 'exists', label: 'exists', needsValue: false, help: 'The field is present in the response. E.g. assert "id" exists after a create.' },
	{ op: 'not_exists', label: 'not exists', needsValue: false, help: 'The field is absent. GraphQL success = "errors" not exists (GraphQL always answers HTTP 200).' },
	{ op: 'eq', label: 'equals', needsValue: true, help: 'The value equals exactly. {{vars}} work — compare a response field against a captured variable.' },
	{ op: 'ne', label: 'not equals', needsValue: true, help: 'The value differs from the given one.' },
	{ op: 'contains', label: 'contains', needsValue: true, help: 'The stringified value contains this substring.' },
	{ op: 'gt', label: '> greater', needsValue: true, help: 'Numeric: field > value. Both sides must be numbers.' },
	{ op: 'gte', label: '≥ at least', needsValue: true, help: 'Numeric: field ≥ value.' },
	{ op: 'lt', label: '< less', needsValue: true, help: 'Numeric: field < value.' },
	{ op: 'lte', label: '≤ at most', needsValue: true, help: 'Numeric: field ≤ value.' },
	{ op: 'len', label: 'length =', needsValue: true, help: 'The field is an array (or string) with exactly N elements. E.g. "data" len 2.' }
];

export function assertOpInfo(op: string): AssertOpInfo | undefined {
	return ASSERT_OPS.find((o) => o.op === op);
}

// ── response paths (for assert + capture dropdowns) ──────────────────────────

/** Dot-path suggestions for what THIS endpoint's response contains. */
export function responsePaths(
	kind: EndpointKind,
	resource: string | undefined,
	schema: APISchema | null,
	gqlOp?: GqlOperation | null
): string[] {
	const res = resource ? schema?.resources?.[resource] : undefined;
	const fields = res ? Object.keys(res.fields) : [];
	switch (kind) {
		case 'list':
			return [
				'data',
				'data.0.id',
				...fields.map((f) => `data.0.${f}`),
				'meta.page',
				'meta.per_page',
				'meta.has_next',
				'meta.total'
			];
		case 'get':
		case 'create':
		case 'replace':
		case 'patch':
			return ['id', ...fields, 'error', 'fields.0.field'];
		case 'delete':
			return ['error'];
		case 'aggregate':
			return ['count', 'groups', 'groups.0.count', ...fields.map((f) => `sum.${f}`)];
		case 'login':
			return ['token', 'user.email', 'user.role', 'mfa_required', 'mfa_token', 'error'];
		case 'signup':
			return ['token', 'user.email', 'error'];
		case 'refresh':
			return ['token', 'error'];
		case 'upload':
			return ['file_id', 'sha256', 'size', 'error'];
		case 'graphql': {
			// the resource comes from the DETECTED OPERATION (the path is always
			// /graphql — it carries no resource itself)
			const opRes = gqlOp ? schema?.resources?.[gqlOp.resource] : undefined;
			if (gqlOp && opRes) {
				return gqlAssertPaths(gqlOp, Object.keys(opRes.fields));
			}
			return ['errors', 'data'];
		}
		default:
			return ['id', 'error'];
	}
}

/** Suggested capture rows per endpoint kind (variable name → response path). */
export function captureSuggestions(
	kind: EndpointKind,
	resource: string | undefined
): { k: string; v: string }[] {
	const sing = resource ? gqlSingular(resource) : 'record';
	switch (kind) {
		case 'create':
			return [{ k: `${sing}_id`, v: 'id' }];
		case 'login':
		case 'signup':
			return [{ k: 'token', v: 'token' }];
		case 'upload':
			return [{ k: 'file_id', v: 'file_id' }];
		case 'list':
			return [{ k: `${sing}_id`, v: 'data.0.id' }];
		default:
			return [];
	}
}

// ── GraphQL: the surface derived from the schema ─────────────────────────────

export interface GqlOperation {
	id: string;
	kind: 'list' | 'get' | 'aggregate' | 'create' | 'update' | 'delete';
	resource: string;
	/** the root field name in the response: data.<field> */
	field: string;
	label: string;
}

/** Every query/mutation the live handler generates (pkg/graphql/handler.go):
 *  <name>(…), <singular>(id), <name>Aggregate, create/update/delete<Title>. */
export function gqlOperations(schema: APISchema | null): GqlOperation[] {
	const out: GqlOperation[] = [];
	for (const name of Object.keys(schema?.resources ?? {})) {
		const sing = gqlSingular(name);
		const title = toPascal(sing);
		out.push(
			{ id: `q:${name}`, kind: 'list', resource: name, field: name, label: `query ${name} — list (data + meta)` },
			{ id: `q1:${name}`, kind: 'get', resource: name, field: sing, label: `query ${sing}(id) — one record` },
			{ id: `qa:${name}`, kind: 'aggregate', resource: name, field: `${name}Aggregate`, label: `query ${name}Aggregate — count/sum/…` },
			{ id: `mc:${name}`, kind: 'create', resource: name, field: `create${title}`, label: `mutation create${title}(input)` },
			{ id: `mu:${name}`, kind: 'update', resource: name, field: `update${title}`, label: `mutation update${title}(id, input) — partial` },
			{ id: `md:${name}`, kind: 'delete', resource: name, field: `delete${title}`, label: `mutation delete${title}(id)` }
		);
	}
	return out;
}

/** Encode a JS value as a GraphQL literal (keys unquoted in objects). */
function gqlLiteral(v: unknown): string {
	if (v === null) return 'null';
	if (typeof v === 'string') return JSON.stringify(v);
	if (typeof v === 'number' || typeof v === 'boolean') return String(v);
	if (Array.isArray(v)) return `[${v.map(gqlLiteral).join(', ')}]`;
	if (typeof v === 'object') {
		const parts = Object.entries(v as Record<string, unknown>).map(([k, x]) => `${k}: ${gqlLiteral(x)}`);
		return `{${parts.join(', ')}}`;
	}
	return 'null';
}

/** Build a ready-to-run GraphQL document for an operation, selecting the
 *  resource's real fields; relations become nested selections (the engine
 *  serves them in ONE LATERAL query — same as ?include=). */
export function buildGqlQuery(op: GqlOperation, schema: APISchema | null, withRelations: boolean): string {
	const res = schema?.resources?.[op.resource];
	if (!res) return '';
	const fieldSel = ['id', ...Object.keys(res.fields)];
	let sel = fieldSel.join(' ');
	if (withRelations && res.relations) {
		for (const [rname, rdef] of Object.entries(res.relations)) {
			const target = schema?.resources?.[rdef.target];
			const tfields = target ? ['id', ...Object.keys(target.fields)].slice(0, 6) : ['id'];
			sel += ` ${rname} { ${tfields.join(' ')} }`;
		}
	}
	switch (op.kind) {
		case 'list':
			return `{ ${op.field}(per_page: 20) { data { ${sel} } meta { page has_next } } }`;
		case 'get':
			return `{ ${op.field}(id: "{{${gqlSingular(op.resource)}_id}}") { ${sel} } }`;
		case 'aggregate':
			return `{ ${op.field}(count: true) { count } }`;
		case 'create': {
			const input: Record<string, unknown> = {};
			for (const [name, def] of writableFields(res)) {
				if (!def.required && def.default !== undefined) continue;
				input[name] = exampleValue(name, def);
			}
			return `mutation { ${op.field}(input: ${gqlLiteral(input)}) { id ${Object.keys(res.fields).join(' ')} } }`;
		}
		case 'update':
			return `mutation { ${op.field}(id: "{{${gqlSingular(op.resource)}_id}}", input: {}) { id } }`;
		case 'delete':
			return `mutation { ${op.field}(id: "{{${gqlSingular(op.resource)}_id}}") }`;
	}
}

/** Assert-path suggestions for a GraphQL operation's response. */
export function gqlAssertPaths(op: GqlOperation, fields: string[]): string[] {
	const base = `data.${op.field}`;
	switch (op.kind) {
		case 'list':
			return ['errors', `${base}.data`, `${base}.data.0.id`, ...fields.map((f) => `${base}.data.0.${f}`), `${base}.meta.has_next`];
		case 'aggregate':
			return ['errors', `${base}.count`, `${base}.groups`];
		case 'delete':
			return ['errors', base];
		default:
			return ['errors', `${base}.id`, ...fields.map((f) => `${base}.${f}`)];
	}
}

/** Identify which generated operation a GraphQL document targets (by its root
 *  field) — feeds the assert-path suggestions. null = unknown/free-form. */
export function detectGqlOperation(text: string, ops: GqlOperation[]): GqlOperation | null {
	const m = /(?:mutation|query)?\s*\{\s*([A-Za-z_][A-Za-z0-9_]*)/.exec(text ?? '');
	if (!m) return null;
	return ops.find((o) => o.field === m[1]) ?? null;
}

/** Wrap a GraphQL document as the POST /graphql body. */
export function gqlBody(query: string): string {
	return JSON.stringify({ query }, null, 2);
}

/** Extract the query back from a stored /graphql step body (for re-editing). */
export function gqlQueryFromBody(body: string): string | null {
	try {
		const v = JSON.parse(body) as { query?: unknown };
		return typeof v.query === 'string' ? v.query : null;
	} catch {
		return null;
	}
}

// ── OpenAPI doc lookup (the spec, contextual to the step) ────────────────────

export interface EndpointDoc {
	summary: string;
	description?: string;
	auth: string; // "Bearer JWT (tenant user)" | "none (public)"
	bodyFields: { name: string; type: string; required: boolean; hint?: string }[];
	bodyRequired: boolean;
	queryParams: string[];
	responses: { code: string; desc: string }[];
}

type Dict = Record<string, unknown>;
const asDict = (v: unknown): Dict => (v && typeof v === 'object' ? (v as Dict) : {});

function resolveRef(spec: Dict, node: unknown): Dict {
	const d = asDict(node);
	const ref = d['$ref'];
	if (typeof ref === 'string' && ref.startsWith('#/')) {
		let cur: unknown = spec;
		for (const seg of ref.slice(2).split('/')) cur = asDict(cur)[seg];
		return asDict(cur);
	}
	return d;
}

/** Normalize a step path to the spec's template: {{var}} segments → {id}. */
export function specPath(path: string): string {
	const p = (path || '').split('?')[0];
	return p
		.split('/')
		.map((seg) => (/\{\{.*\}\}/.test(seg) || /^[0-9a-f-]{36}$/i.test(seg) ? '{id}' : seg))
		.join('/');
}

/** Look up one endpoint's doc in the served OpenAPI spec. null = not in spec. */
export function endpointDoc(spec: Dict | null, method: string, path: string): EndpointDoc | null {
	if (!spec) return null;
	const paths = asDict(spec['paths']);
	const item = asDict(paths[specPath(path)]);
	const op = asDict(item[method.toLowerCase()]);
	if (Object.keys(op).length === 0) return null;

	const security = op['security'];
	const auth = Array.isArray(security) && security.length === 0 ? 'none (public endpoint)' : 'Bearer JWT (tenant user — the flow token)';

	const bodyFields: EndpointDoc['bodyFields'] = [];
	let bodyRequired = false;
	const reqBody = asDict(op['requestBody']);
	if (Object.keys(reqBody).length > 0) {
		bodyRequired = reqBody['required'] === true;
		const content = asDict(reqBody['content']);
		const media = asDict(content['application/json'] ?? content['multipart/form-data']);
		const bodySchema = resolveRef(spec, media['schema']);
		const required = new Set(Array.isArray(bodySchema['required']) ? (bodySchema['required'] as string[]) : []);
		for (const [name, raw] of Object.entries(asDict(bodySchema['properties']))) {
			const f = resolveRef(spec, raw);
			const type = String(f['type'] ?? f['format'] ?? 'any');
			const parts: string[] = [];
			if (f['enum']) parts.push(`enum: ${(f['enum'] as unknown[]).join('|')}`);
			if (f['format']) parts.push(String(f['format']));
			if (f['description']) parts.push(String(f['description']));
			bodyFields.push({ name, type, required: required.has(name), hint: parts.join(' · ') || undefined });
		}
	}

	const queryParams: string[] = [];
	if (Array.isArray(op['parameters'])) {
		for (const raw of op['parameters'] as unknown[]) {
			const p = resolveRef(spec, raw);
			if (p['in'] === 'query') queryParams.push(String(p['name']));
		}
	}

	const responses: EndpointDoc['responses'] = [];
	for (const [code, raw] of Object.entries(asDict(op['responses']))) {
		const r = resolveRef(spec, raw);
		responses.push({ code, desc: String(r['description'] ?? '') });
	}
	responses.sort((a, b) => a.code.localeCompare(b.code));

	return {
		summary: String(op['summary'] ?? ''),
		description: op['description'] ? String(op['description']) : undefined,
		auth,
		bodyFields,
		bodyRequired,
		queryParams,
		responses
	};
}
