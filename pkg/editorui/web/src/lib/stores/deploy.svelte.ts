// The deploy store (UI-F1-S1) — the state machine behind "deploy from the editor".
// It closes the loop: design on the canvas → authenticate → provision/migrate via
// the engine's admin API → the app is running.
//
// AUTH: the platform super-admin token lives ONLY in memory (this $state). A
// super-admin JWT is powerful and the editor is a static, shared app, so it is
// NEVER written to localStorage/sessionStorage — a page reload requires logging in
// again (the safe trade-off). Designing needs no auth; deploying does.

import { adminApi, ApiError, type CreateTenantBody } from '../api/admin';
import type { Preview, TenantInfo } from '../types/deploy';
import { editor } from './editor.svelte';

export type DeployStep = 'login' | 'mfa' | 'target' | 'preview' | 'result';

export interface DeployResult {
	tenantId: string;
	created: boolean; // true = new tenant provisioned; false = existing tenant migrated
	appliedDrops?: string[];
	gatedDrops?: string[];
	// Resources provisioned but NOT served by the running engine (absent from the boot
	// --schema) — their tables exist but the API is 403 until the engine restarts with
	// a schema that includes them. Snapshotted at deploy time so the result is honest.
	restartResources?: string[];
}

export interface Endpoints {
	rest: string;
	graphql: string;
	docs: string;
	tenantHost: string;
}

const TENANT_ID_RE = /^[a-z0-9][a-z0-9-]{1,29}$/;

class DeployStore {
	// modal + flow
	open = $state(false);
	step = $state<DeployStep>('login');
	busy = $state(false);
	error = $state<string | null>(null);
	fieldErrors = $state<string[]>([]); // schema validation issues (client or server)

	// auth — IN MEMORY ONLY (never persisted)
	token = $state<string | null>(null);
	adminEmail = $state<string | null>(null);
	mfaToken = $state<string | null>(null);

	// login form
	loginEmail = $state('');
	loginPassword = $state('');
	mfaCode = $state('');

	// target selection
	mode = $state<'new' | 'existing'>('new');
	tenants = $state<TenantInfo[]>([]);
	newId = $state('');
	newDisplay = $state('');
	newEmail = $state('');
	newPlan = $state('free');
	targetId = $state<string | null>(null);

	// migration preview + destructive approval (existing-tenant deploys)
	preview = $state<Preview | null>(null);
	approved = $state<Record<string, boolean>>({});

	// The resources the engine serves live (from its boot --schema), fetched once per
	// session. null = not yet known (the restart hint stays hidden until loaded).
	servedResources = $state<string[] | null>(null);

	// result
	result = $state<DeployResult | null>(null);

	get authed(): boolean {
		return !!this.token;
	}

	/** Destructive ops the operator has explicitly approved to drop. */
	get approvedKeys(): string[] {
		return (this.preview?.destructive ?? []).filter((d) => this.approved[d.key]).map((d) => d.key);
	}

	/** A destructive op present but NOT approved → it will stay gated (drift). */
	get hasPendingDestructive(): boolean {
		return (this.preview?.destructive ?? []).some((d) => !this.approved[d.key]);
	}

	/** Resources in the schema being deployed that the running engine does NOT serve
	 *  (absent from its boot --schema). Their tables get provisioned, but the REST/
	 *  GraphQL/RBAC for them is boot-compiled, so the API is 403 until a restart. Empty
	 *  while servedResources is unknown (the hint stays hidden rather than guess). */
	get newResources(): string[] {
		if (!this.servedResources) return [];
		const served = new Set(this.servedResources);
		return editor.entities.map((e) => e.name).filter((n) => !served.has(n));
	}
	/** Resources in the deploy that the engine already serves — changes to these
	 *  (columns, validations, RBAC…) take effect live, no restart. */
	get liveResources(): string[] {
		if (!this.servedResources) return editor.entities.map((e) => e.name);
		const served = new Set(this.servedResources);
		return editor.entities.map((e) => e.name).filter((n) => served.has(n));
	}

	/** Fetch the engine's served-resource set once (non-fatal: on failure the restart
	 *  hint simply stays hidden — never blocks a deploy). */
	async loadServedResources() {
		if (!this.token || this.servedResources !== null) return;
		try {
			const res = await adminApi.servedResources(this.token);
			this.servedResources = res.resources ?? [];
		} catch {
			/* non-fatal — the hint is advisory, the engine remains the authority */
		}
	}

	openDeploy() {
		this.error = null;
		this.fieldErrors = [];
		this.result = null;
		this.open = true;
		if (this.authed) {
			this.step = 'target';
			void this.refreshTenants();
		} else {
			this.step = 'login';
		}
	}

	close() {
		this.open = false;
		this.busy = false;
		this.error = null;
		this.fieldErrors = [];
	}

	logout() {
		this.token = null;
		this.adminEmail = null;
		this.mfaToken = null;
		this.tenants = [];
		this.step = 'login';
	}

	private fail(e: unknown) {
		if (e instanceof ApiError) {
			this.error = e.message;
			this.fieldErrors = e.fieldErrors ?? [];
		} else {
			this.error = e instanceof Error ? e.message : String(e);
			this.fieldErrors = [];
		}
	}

	// ── auth ──────────────────────────────────────────────────────────────────

	async doLogin() {
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.login(this.loginEmail.trim(), this.loginPassword);
			if (res.mfa_required && res.mfa_token) {
				this.mfaToken = res.mfa_token;
				this.step = 'mfa';
			} else if (res.token) {
				this.token = res.token;
				this.adminEmail = res.admin?.email ?? this.loginEmail.trim();
				this.loginPassword = '';
				this.step = 'target';
				await this.refreshTenants();
			} else {
				this.error = 'unexpected login response';
			}
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	async doMfa() {
		if (!this.mfaToken) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.mfaVerify(this.mfaToken, this.mfaCode.trim());
			if (res.token) {
				this.token = res.token;
				this.adminEmail = res.admin?.email ?? this.loginEmail.trim();
				this.mfaToken = null;
				this.mfaCode = '';
				this.loginPassword = '';
				this.step = 'target';
				await this.refreshTenants();
			} else {
				this.error = 'unexpected mfa response';
			}
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	// ── tenants ─────────────────────────────────────────────────────────────────

	async refreshTenants() {
		if (!this.token) return;
		try {
			const res = await adminApi.listTenants(this.token);
			this.tenants = res.tenants ?? [];
		} catch (e) {
			// A 401/expired token here → drop to login.
			if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
				this.logout();
			}
			this.fail(e);
		}
	}

	/** Load an already-deployed tenant's schema onto the canvas (the iterative loop). */
	async loadTenant(id: string) {
		if (!this.token) return;
		this.busy = true;
		this.error = null;
		try {
			const sc = await adminApi.getTenantSchema(this.token, id);
			if (!sc || !sc.resources) {
				this.error = `tenant "${id}" has no schema to load`;
				return;
			}
			editor.loadSchema(sc);
			this.close();
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	// ── deploy ────────────────────────────────────────────────────────────────

	/** Step from target → preview: pre-validate, then (for existing) fetch the plan. */
	async proceedToPreview() {
		this.error = null;
		this.fieldErrors = [];

		const issues = editor.validate();
		if (issues.length > 0) {
			this.fieldErrors = issues;
			this.error = 'fix these before deploying';
			return;
		}

		// Learn which resources the engine serves live, so the preview can honestly
		// flag any new resource as "provisioned but needs a restart".
		await this.loadServedResources();

		if (this.mode === 'new') {
			const id = this.newId.trim();
			if (!TENANT_ID_RE.test(id)) {
				this.error = 'tenant id must be 2–30 chars: lowercase letters, digits, hyphens';
				return;
			}
			this.preview = null; // a new tenant is all creation — no migration/destructives
			this.step = 'preview';
			return;
		}

		// existing: dry-run the migration against the live tenant
		if (!this.targetId) {
			this.error = 'pick a tenant to update';
			return;
		}
		this.busy = true;
		try {
			const res = await adminApi.previewSchema(this.token!, this.targetId, editor.toSchema(), []);
			this.preview = res.preview;
			this.approved = {};
			this.step = 'preview';
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	/** Apply the deploy: create a new tenant, or migrate an existing one (with the
	 *  explicitly-approved destructive drops only). */
	async confirmDeploy() {
		this.error = null;
		this.fieldErrors = [];
		this.busy = true;
		try {
			if (this.mode === 'new') {
				const id = this.newId.trim();
				const body: CreateTenantBody = {
					tenant_id: id,
					display_name: this.newDisplay.trim() || id,
					email: this.newEmail.trim() || `${id}@example.com`,
					plan: this.newPlan.trim() || 'free',
					schema: editor.toSchema()
				};
				await adminApi.createTenant(this.token!, body);
				this.result = { tenantId: id, created: true, restartResources: this.newResources };
				this.step = 'result';
				await this.refreshTenants();
			} else {
				const id = this.targetId!;
				const res = await adminApi.applySchema(this.token!, id, editor.toSchema(), this.approvedKeys);
				this.result = {
					tenantId: id,
					created: false,
					appliedDrops: res.applied_drops,
					gatedDrops: res.gated_drops,
					restartResources: this.newResources
				};
				this.step = 'result';
			}
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	/** The running app's endpoints (tenant is addressed by Host subdomain). */
	endpoints(tenantId: string): Endpoints {
		const loc = window.location;
		const tenantHost = `${tenantId}.${loc.host}`;
		return {
			tenantHost,
			rest: `${loc.protocol}//${tenantHost}/api`,
			graphql: `${loc.protocol}//${tenantHost}/graphql`,
			docs: `${loc.origin}/docs`
		};
	}

	/** Back to the target step for an iterative re-deploy without re-login. */
	deployAgain() {
		this.result = null;
		this.preview = null;
		this.error = null;
		this.fieldErrors = [];
		this.step = 'target';
		void this.refreshTenants();
	}
}

export const deploy = new DeployStore();
