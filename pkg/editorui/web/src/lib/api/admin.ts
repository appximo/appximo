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

export const adminApi = {
	login: (email: string, password: string) =>
		call<LoginResult>('POST', '/admin/auth/login', { body: { email, password } }),

	mfaVerify: (mfa_token: string, code: string) =>
		call<LoginResult>('POST', '/admin/auth/mfa/verify', { body: { mfa_token, code } }),

	listTenants: (token: string) =>
		call<{ tenants: TenantInfo[] }>('GET', '/admin/tenants', { token }),

	/** Resource names the engine serves live (compiled from the boot --schema). A
	 *  deployed resource absent here is provisioned but needs a restart to be served. */
	servedResources: (token: string) =>
		call<{ resources: string[] }>('GET', '/admin/served-resources', { token }),

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
		})
};
