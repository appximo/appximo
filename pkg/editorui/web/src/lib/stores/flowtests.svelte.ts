// The flow-tests store (FLOWTEST-S1) — Studio's face of the flow engine: the
// tenant's saved multi-step scenarios, a structured step editor (leveraging the
// schema the canvas knows: resource paths, roles), live-streamed runs
// (PASS/FAIL per step as it executes), the run history, and the post-deploy
// regression entry point ("run the suite after activating v5").
//
// AUTH is the deploy store's platform super-admin session (in memory only) —
// but the FLOWS THEMSELVES authenticate as tenant users (a login step or a
// declared role), exercising the app's real RBAC.

import {
	flowApi,
	streamFlowRun,
	ApiError,
	type FlowDef,
	type FlowStep,
	type FlowStepResult,
	type FlowRunSummary,
	type StoredFlow
} from '../api/admin';
import { deploy } from './deploy.svelte';
import { editor } from './editor.svelte';

export type FlowsView = 'list' | 'edit' | 'run' | 'history';

/** An editable step: FlowStep plus captures as ROWS (a map keyed by the var
 *  name re-creates DOM rows while typing the name — values were lost mid-edit;
 *  live finding). Folded back into the map on save. */
export interface EditStep extends FlowStep {
	captureRows: { k: string; v: string }[];
}

export interface LiveEvent {
	kind: 'flow' | 'step' | 'flow_done' | 'done' | 'error';
	flowName?: string;
	step?: FlowStepResult;
	summary?: Record<string, unknown>;
}

function emptyStep(): EditStep {
	return {
		name: '', method: 'GET', path: '/api/', body: '',
		expect: { status: 200, asserts: [] }, captureRows: []
	};
}

class FlowTestsStore {
	open = $state(false);
	busy = $state(false);
	error = $state<string | null>(null);
	view = $state<FlowsView>('list');

	tenantId = $state<string | null>(null);
	flows = $state<StoredFlow[]>([]);

	// edit state
	editingId = $state<string | null>(null); // null = new flow
	draft = $state<{ name: string; description?: string; role?: string; steps: EditStep[] }>({
		name: '', role: '', steps: [emptyStep()]
	});

	// run state (live)
	running = $state(false);
	runScope = $state<string>('suite');
	events = $state<LiveEvent[]>([]);
	runDone = $state<Record<string, unknown> | null>(null);

	// history
	runs = $state<FlowRunSummary[]>([]);

	// delete confirmation
	confirmTarget = $state<StoredFlow | null>(null);

	openFlows() {
		this.open = true;
		this.error = null;
		this.view = 'list';
		if (deploy.authed) void this.afterAuth();
	}

	/** The post-deploy regression entry: open on the tenant and run the suite. */
	openAndRunSuite(tenantId: string) {
		this.open = true;
		this.error = null;
		this.tenantId = tenantId;
		this.view = 'list';
		if (deploy.authed) {
			void this.afterAuth().then(() => this.runSuite());
		}
	}

	close() {
		if (this.running) return; // never abandon a live run silently
		this.open = false;
		this.error = null;
		this.confirmTarget = null;
	}

	async afterAuth() {
		if (!deploy.authed) return;
		if (deploy.tenants.length === 0) await deploy.refreshTenants();
		if (!this.tenantId || !deploy.tenants.some((t) => t.id === this.tenantId)) {
			this.tenantId = deploy.tenants[0]?.id ?? null;
		}
		if (this.tenantId) await this.refresh();
	}

	select(id: string) {
		if (id === this.tenantId) return;
		this.tenantId = id;
		this.flows = [];
		this.runs = [];
		this.view = 'list';
		void this.refresh();
	}

	async refresh() {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await flowApi.list(deploy.token, this.tenantId);
			this.flows = res.flows ?? [];
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	// ── editor ──────────────────────────────────────────────────────────────

	/** Path suggestions from what Studio knows: the canvas entities' /api routes
	 *  plus the engine's auth/files endpoints — the schema-aware assist. */
	get pathSuggestions(): string[] {
		const out = ['/auth/login', '/api/files'];
		for (const e of editor.entities) {
			out.push(`/api/${e.name}`);
		}
		return out;
	}

	/** Role suggestions from the canvas RBAC. */
	get roleSuggestions(): string[] {
		return Object.keys(editor.rbac?.roles ?? {});
	}

	newFlow() {
		this.editingId = null;
		this.draft = { name: '', role: '', steps: [emptyStep()] };
		this.view = 'edit';
		this.error = null;
	}

	async editFlow(f: StoredFlow) {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		try {
			const full = await flowApi.get(deploy.token, this.tenantId, f.id);
			this.editingId = f.id;
			// normalize optionals + expand captures into editable rows
			const d = full.flow!;
			this.draft = {
				name: d.name,
				description: d.description,
				role: d.role ?? '',
				steps: d.steps.map((st) => ({
					...st,
					body: st.body ?? '',
					expect: { status: st.expect.status, asserts: st.expect.asserts ?? [] },
					captureRows: Object.entries(st.capture ?? {}).map(([k, v]) => ({ k, v }))
				}))
			};
			this.view = 'edit';
			this.error = null;
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	addStep() {
		this.draft.steps.push(emptyStep());
	}
	removeStep(i: number) {
		this.draft.steps.splice(i, 1);
	}
	moveStep(i: number, dir: -1 | 1) {
		const j = i + dir;
		if (j < 0 || j >= this.draft.steps.length) return;
		const s = this.draft.steps;
		[s[i], s[j]] = [s[j], s[i]];
	}

	/** Serialize the draft: drop empty optionals so the stored flow stays lean. */
	private cleanDraft(): FlowDef {
		const d: FlowDef = { name: this.draft.name.trim(), steps: [] };
		if (this.draft.role?.trim()) d.role = this.draft.role.trim();
		if (this.draft.description?.trim()) d.description = this.draft.description.trim();
		for (const st of this.draft.steps) {
			const out: FlowStep = {
				name: st.name.trim() || st.method + ' ' + st.path,
				method: st.method,
				path: st.path.trim(),
				expect: { status: Number(st.expect.status) || 200 }
			};
			if (st.body?.trim()) out.body = st.body;
			if (st.upload?.filename) out.upload = st.upload;
			const asserts = (st.expect.asserts ?? []).filter((a) => a.path.trim());
			if (asserts.length > 0) out.expect.asserts = asserts;
			const cap: Record<string, string> = {};
			for (const row of st.captureRows ?? []) {
				if (row.k.trim() && row.v.trim()) cap[row.k.trim()] = row.v.trim();
			}
			if (Object.keys(cap).length > 0) out.capture = cap;
			d.steps.push(out);
		}
		return d;
	}

	async saveDraft(): Promise<boolean> {
		if (!deploy.token || !this.tenantId) return false;
		this.busy = true;
		this.error = null;
		try {
			const d = this.cleanDraft();
			if (this.editingId) {
				await flowApi.update(deploy.token, this.tenantId, this.editingId, d);
			} else {
				await flowApi.create(deploy.token, this.tenantId, d);
			}
			await this.refresh();
			this.view = 'list';
			return true;
		} catch (e) {
			this.fail(e);
			return false;
		} finally {
			this.busy = false;
		}
	}

	askDelete(f: StoredFlow) {
		this.confirmTarget = f;
	}
	cancelDelete() {
		this.confirmTarget = null;
	}
	async confirmDelete() {
		const f = this.confirmTarget;
		if (!deploy.token || !this.tenantId || !f) return;
		this.busy = true;
		try {
			await flowApi.remove(deploy.token, this.tenantId, f.id);
			this.confirmTarget = null;
			await this.refresh();
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	// ── live runs ───────────────────────────────────────────────────────────

	async runFlow(f: StoredFlow) {
		await this.stream(f.id, f.name);
	}
	async runSuite() {
		await this.stream(null, 'suite');
	}

	private async stream(fid: string | null, scope: string) {
		if (!deploy.token || !this.tenantId || this.running) return;
		this.view = 'run';
		this.running = true;
		this.runScope = scope;
		this.events = [];
		this.runDone = null;
		this.error = null;
		let currentFlow = scope;
		try {
			await streamFlowRun(deploy.token, this.tenantId, fid, (event, data) => {
				switch (event) {
					case 'flow':
						currentFlow = String(data.name ?? '');
						this.events.push({ kind: 'flow', flowName: currentFlow });
						break;
					case 'step':
						this.events.push({ kind: 'step', flowName: currentFlow, step: data as unknown as FlowStepResult });
						break;
					case 'flow_done':
						this.events.push({ kind: 'flow_done', flowName: String(data.name ?? ''), summary: data });
						break;
					case 'done':
						this.runDone = data;
						break;
					case 'error':
						this.events.push({ kind: 'error', summary: data });
						break;
				}
			});
		} catch (e) {
			this.fail(e);
		} finally {
			this.running = false;
		}
	}

	// ── history ─────────────────────────────────────────────────────────────

	async loadRuns() {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await flowApi.runs(deploy.token, this.tenantId);
			this.runs = res.runs ?? [];
			this.view = 'history';
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	private fail(e: unknown) {
		if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
			deploy.logout();
			this.flows = [];
			this.error = 'session expired — sign in again';
			return;
		}
		this.error = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
	}
}

export const flowTests = new FlowTestsStore();
