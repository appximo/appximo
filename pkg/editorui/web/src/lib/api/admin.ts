// Thin client for the engine's admin API (/admin/*). The editor is served
// same-origin by the engine (/editor), so these are same-origin fetches — no CORS,
// no base URL. In dev, vite proxies /admin → the local engine (see vite.config.ts).
//
// The platform token is passed per call (the deploy store holds it in memory only —
// a super-admin JWT is never persisted). Errors are normalized to ApiError so the
// UI can render the engine's actionable message (and field-level schema errors).

import type {
	APISchema
} from '../types/schema';
import type {
	ApplyResponse,
	CreatedTenant,
	LoginResult,
	Preview,
	PreviewResponse,
	TenantInfo
} from '../types/deploy';

export class ApiError extends Error {
	status: number;
	/** Schema validation errors (the engine's `errors[]`), when present. */
	fieldErrors?: string[];
	/** The engine stage that failed ("preview" | "migration"), when present. */
	stage?: string;
	constructor(message: string, status: number, fieldErrors?: string[], stage?: string) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.fieldErrors = fieldErrors;
		this.stage = stage;
	}
}

interface CallOpts {
	token?: string | null;
	body?: unknown;
}

async function call<T>(method: string, path: string, opts: CallOpts = {}): Promise<T> {
	let res: Response;
	try {
		res = await fetch(path, {
			method,
			headers: {
				...(opts.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
				...(opts.token ? { Authorization: `Bearer ${opts.token}` } : {})
			},
			body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined
		});
	} catch {
		throw new ApiError('could not reach the engine — is it running?', 0);
	}

	const text = await res.text();
	let data: Record<string, unknown> | undefined;
	if (text) {
		try {
			data = JSON.parse(text) as Record<string, unknown>;
		} catch {
			/* non-JSON body (shouldn't happen on /admin) */
		}
	}

	if (!res.ok) {
		const fieldErrors = Array.isArray(data?.errors) ? (data!.errors as string[]) : undefined;
		const msg =
			(typeof data?.error === 'string' && data.error) ||
			(fieldErrors ? 'schema validation failed' : `request failed (HTTP ${res.status})`);
		throw new ApiError(msg, res.status, fieldErrors, data?.stage as string | undefined);
	}
	return data as T;
}

export interface CreateTenantBody {
	tenant_id: string;
	display_name: string;
	email: string;
	plan: string;
	schema: APISchema;
}

/** One stored file's metadata (pkg/files.Meta — the authoritative DB row). */
export interface FileMeta {
	id: string;
	sha256: string;
	size: number;
	content_type: string;
	original_name: string;
	created_at: string;
}

/** A page of a tenant's files (GET /admin/tenants/{id}/files). */
export interface FilesPage {
	files: FileMeta[];
	total: number;
	page: number;
	per_page: number;
	/** The active storage backend ("local" | "s3") — informational. */
	backend: string;
}

/** Upload via XHR so large files stream from disk with real progress events —
 *  the browser never reads the File into JS memory (FormData streams it). The
 *  engine's REAL rejections (422 with the OWASP reason, 413 over the cap)
 *  surface as ApiError with the server's message, never masked. */
export function uploadTenantFile(
	token: string,
	tenantId: string,
	file: File,
	onProgress: (pct: number) => void
): Promise<{ file_id: string; sha256: string; size: number }> {
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open('POST', `/admin/tenants/${encodeURIComponent(tenantId)}/files`);
		xhr.setRequestHeader('Authorization', `Bearer ${token}`);
		xhr.upload.onprogress = (e) => {
			if (e.lengthComputable) onProgress(Math.round((e.loaded / e.total) * 100));
		};
		xhr.onerror = () => reject(new ApiError('could not reach the engine — is it running?', 0));
		xhr.onload = () => {
			let data: Record<string, unknown> | undefined;
			try {
				data = JSON.parse(xhr.responseText) as Record<string, unknown>;
			} catch {
				/* non-JSON body */
			}
			if (xhr.status >= 200 && xhr.status < 300) {
				resolve(data as { file_id: string; sha256: string; size: number });
			} else {
				const msg =
					(typeof data?.error === 'string' && data.error) ||
					(xhr.status === 413 ? 'upload too large' : `upload failed (HTTP ${xhr.status})`);
				reject(new ApiError(msg, xhr.status));
			}
		};
		const form = new FormData();
		form.append('file', file);
		xhr.send(form);
	});
}

export const adminApi = {
	login: (email: string, password: string) =>
		call<LoginResult>('POST', '/admin/auth/login', { body: { email, password } }),

	mfaVerify: (mfa_token: string, code: string) =>
		call<LoginResult>('POST', '/admin/auth/mfa/verify', { body: { mfa_token, code } }),

	listTenants: (token: string) =>
		call<{ tenants: TenantInfo[] }>('GET', '/admin/tenants', { token }),

	/** Resource names the engine serves live (compiled from the boot --schema). A
	 *  deployed resource absent here is provisioned but needs a restart to be served.
	 *  self_restart reports whether POST /admin/engine/schema exists (UI-F4-S2). */
	servedResources: (token: string) =>
		call<{ resources: string[]; self_restart?: boolean; activation?: 'restart' | 'hot_swap' }>(
			'GET',
			'/admin/served-resources',
			{ token }
		),

	/** Persist the schema as the engine's new BOOT schema (validated + atomic + the
	 *  previous one backed up) and gracefully restart it (UI-F4-S2) — new resources'
	 *  routes/GraphQL//docs go live on relaunch. Privileged; same auth as the deploy. */
	restartEngine: (token: string, schema: APISchema) =>
		call<{ ok: boolean; restarting: boolean; note?: string }>('POST', '/admin/engine/schema', {
			token,
			body: { schema }
		}),

	getTenantSchema: (token: string, id: string) =>
		call<APISchema | null>('GET', `/admin/tenants/${encodeURIComponent(id)}/schema`, { token }),

	createTenant: (token: string, body: CreateTenantBody) =>
		call<CreatedTenant>('POST', '/admin/tenants', { token, body }),

	previewSchema: (token: string, id: string, schema: APISchema, approved: string[]) =>
		call<PreviewResponse>('PUT', `/admin/tenants/${encodeURIComponent(id)}/schema`, {
			token,
			body: { schema, dry_run: true, approved_drops: approved }
		}),

	applySchema: (token: string, id: string, schema: APISchema, approved: string[]) =>
		call<ApplyResponse>('PUT', `/admin/tenants/${encodeURIComponent(id)}/schema`, {
			token,
			body: { schema, dry_run: false, approved_drops: approved }
		}),

	// ── files manager (UI-F5-S1) — thin delegates into the engine's files.Store ──

	listFiles: (token: string, id: string, page: number, perPage: number) =>
		call<FilesPage>(
			'GET',
			`/admin/tenants/${encodeURIComponent(id)}/files?page=${page}&per_page=${perPage}`,
			{ token }
		),

	/** Short-lived signed download URL — S3 native presigned, or the engine's
	 *  token URL on the local backend. Open it; the storage/engine serves. */
	fileSignedURL: (token: string, id: string, fid: string) =>
		call<{ url: string; expires_in: number }>(
			'GET',
			`/admin/tenants/${encodeURIComponent(id)}/files/${encodeURIComponent(fid)}/url`,
			{ token }
		),

	deleteFile: (token: string, id: string, fid: string) =>
		call<void>(
			'DELETE',
			`/admin/tenants/${encodeURIComponent(id)}/files/${encodeURIComponent(fid)}`,
			{ token }
		),

	// ── schema version history + rollback (VERSION-S1) ────────────────────────

	/** The tenant's deployed-schema timeline (append-only; newest first — the
	 *  latest version IS the current schema). */
	schemaHistory: (token: string, id: string, page: number, perPage: number) =>
		call<HistoryPage>(
			'GET',
			`/admin/tenants/${encodeURIComponent(id)}/schema/history?page=${page}&per_page=${perPage}`,
			{ token }
		),

	/** One recorded version WITH its full schema (view / load into the editor). */
	schemaVersion: (token: string, id: string, version: number) =>
		call<SchemaVersionFull>(
			'GET',
			`/admin/tenants/${encodeURIComponent(id)}/schema/history/${version}`,
			{ token }
		),

	/** DRY-RUN of rolling back to a version: the engine's real migration preview
	 *  of re-applying that schema — what later versions added shows as gated
	 *  destructive drops with measured rows_lost. Applies nothing. */
	rollbackPreview: (token: string, id: string, version: number, approved: string[]) =>
		call<RollbackPreviewResponse>(
			'POST',
			`/admin/tenants/${encodeURIComponent(id)}/schema/rollback`,
			{ token, body: { version, dry_run: true, approved_drops: approved } }
		),

	/** Apply the rollback (only the enumerated drops execute; the history gets a
	 *  NEW version whose content is the target's — append-only). */
	rollbackApply: (token: string, id: string, version: number, approved: string[]) =>
		call<RollbackApplyResponse>(
			'POST',
			`/admin/tenants/${encodeURIComponent(id)}/schema/rollback`,
			{ token, body: { version, dry_run: false, approved_drops: approved } }
		)
};

// ── flow tests (FLOWTEST-S1) — mirrors of pkg/flowtest ───────────────────────

export interface FlowAssert {
	path: string;
	op: 'exists' | 'eq' | 'contains';
	value?: string;
}

export interface FlowUpload {
	field?: string;
	filename: string;
	content: string;
}

export interface FlowStep {
	name: string;
	method: string;
	path: string;
	body?: string;
	headers?: Record<string, string>;
	upload?: FlowUpload;
	expect: { status: number; asserts?: FlowAssert[] };
	capture?: Record<string, string>;
}

export interface FlowDef {
	name: string;
	description?: string;
	role?: string;
	steps: FlowStep[];
}

export interface StoredFlow {
	id: string;
	tenant_id: string;
	name: string;
	steps: number;
	flow?: FlowDef;
	created_at: string;
	updated_at: string;
}

export interface FlowStepResult {
	index: number;
	name: string;
	method: string;
	path: string;
	skipped?: boolean;
	pass: boolean;
	status: number;
	expected: number;
	failures?: string[];
	body_sample?: string;
	captured?: Record<string, string>;
	duration_ms: number;
}

export interface FlowRunSummary {
	id: number;
	schema_version: number;
	scope: string;
	pass: boolean;
	flows_total: number;
	flows_failed: number;
	steps_total: number;
	steps_failed: number;
	results?: unknown;
	created_at: string;
}

export const flowApi = {
	list: (token: string, id: string) =>
		call<{ flows: StoredFlow[] }>('GET', `/admin/tenants/${encodeURIComponent(id)}/flows`, { token }),
	get: (token: string, id: string, fid: string) =>
		call<StoredFlow>('GET', `/admin/tenants/${encodeURIComponent(id)}/flows/${encodeURIComponent(fid)}`, { token }),
	create: (token: string, id: string, flow: FlowDef) =>
		call<StoredFlow>('POST', `/admin/tenants/${encodeURIComponent(id)}/flows`, { token, body: { flow } }),
	update: (token: string, id: string, fid: string, flow: FlowDef) =>
		call<StoredFlow>('PUT', `/admin/tenants/${encodeURIComponent(id)}/flows/${encodeURIComponent(fid)}`, { token, body: { flow } }),
	remove: (token: string, id: string, fid: string) =>
		call<void>('DELETE', `/admin/tenants/${encodeURIComponent(id)}/flows/${encodeURIComponent(fid)}`, { token }),
	runs: (token: string, id: string) =>
		call<{ runs: FlowRunSummary[] }>('GET', `/admin/tenants/${encodeURIComponent(id)}/flows/runs`, { token })
};

/** Stream a run (one flow or the whole suite) — POST + SSE body consumed with
 *  fetch streaming (EventSource can't send Authorization). onEvent receives
 *  every parsed (event, data) pair as it arrives — the live PASS/FAIL. */
export async function streamFlowRun(
	token: string,
	tenantId: string,
	fid: string | null, // null ⇒ the whole suite
	onEvent: (event: string, data: Record<string, unknown>) => void
): Promise<void> {
	const path = fid
		? `/admin/tenants/${encodeURIComponent(tenantId)}/flows/${encodeURIComponent(fid)}/run`
		: `/admin/tenants/${encodeURIComponent(tenantId)}/flows/run`;
	const res = await fetch(path, {
		method: 'POST',
		headers: { Authorization: `Bearer ${token}` }
	});
	if (!res.ok || !res.body) {
		let msg = `run failed (HTTP ${res.status})`;
		try {
			const d = (await res.json()) as { error?: string };
			if (d.error) msg = d.error;
		} catch {
			/* not JSON */
		}
		throw new ApiError(msg, res.status);
	}
	const reader = res.body.getReader();
	const dec = new TextDecoder();
	let buf = '';
	for (;;) {
		const { done, value } = await reader.read();
		if (done) break;
		buf += dec.decode(value, { stream: true });
		let idx;
		while ((idx = buf.indexOf('\n\n')) !== -1) {
			const frame = buf.slice(0, idx);
			buf = buf.slice(idx + 2);
			let event = 'message';
			let data = '';
			for (const line of frame.split('\n')) {
				if (line.startsWith('event: ')) event = line.slice(7).trim();
				else if (line.startsWith('data: ')) data += line.slice(6);
			}
			if (data) {
				try {
					onEvent(event, JSON.parse(data) as Record<string, unknown>);
				} catch {
					/* skip malformed frame */
				}
			}
		}
	}
}

// ── history types (mirrors of pkg/schemahistory + the rollback handler) ───────

/** One recorded schema version (pkg/schemahistory.Version, listing shape). */
export interface SchemaVersionMeta {
	version: number;
	hash: string;
	source: 'register' | 'deploy' | 'rollback' | 'fanout' | 'backfill' | string;
	note?: string;
	created_at: string;
	resources: string[];
}

export interface HistoryPage {
	versions: SchemaVersionMeta[];
	total: number;
	page: number;
	per_page: number;
}

export interface SchemaVersionFull extends SchemaVersionMeta {
	schema: APISchema;
}

export interface RollbackPreviewResponse {
	status: 'dry_run';
	target_version: number;
	target_hash: string;
	preview: Preview;
}

export interface RollbackApplyResponse {
	status: 'rolled_back';
	tenant_id: string;
	target_version: number;
	new_version: number;
	applied_drops?: string[];
	gated_drops?: string[];
	unmatched_approvals?: string[];
	schema?: APISchema;
}
