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
		)
};
