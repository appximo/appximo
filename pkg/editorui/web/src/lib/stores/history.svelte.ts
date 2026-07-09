// The schema-history store (VERSION-S1) — the state behind Studio's "History"
// view: the tenant's append-only deploy timeline, viewing any recorded version,
// and the HONEST rollback flow. A rollback is a re-deploy of a stored version
// through the engine's real migration machinery: the preview shows exactly what
// reverting costs (what later versions added is DROPPED, gated with measured
// rows_lost), it never promises to recover data a forward drop already
// destroyed, and applying appends a NEW version (the trace is never rewritten).
//
// AUTH is the deploy store's platform super-admin session (in memory only) —
// the same shared login the Files manager uses.

import {
	adminApi,
	ApiError,
	type SchemaVersionMeta,
	type SchemaVersionFull
} from '../api/admin';
import type { Preview } from '../types/deploy';
import type { APISchema } from '../types/schema';
import { deploy } from './deploy.svelte';
import { editor } from './editor.svelte';

export type RollbackStep = 'preview' | 'result';

class HistoryStore {
	open = $state(false);
	busy = $state(false);
	error = $state<string | null>(null);

	tenantId = $state<string | null>(null);
	versions = $state<SchemaVersionMeta[]>([]);
	total = $state(0);
	page = $state(1);
	perPage = 50;

	// view-a-version panel
	viewing = $state<SchemaVersionFull | null>(null);

	// rollback flow (a sub-dialog over the timeline)
	rollbackTarget = $state<SchemaVersionMeta | null>(null);
	rollbackStep = $state<RollbackStep>('preview');
	rollbackPreview = $state<Preview | null>(null);
	approved = $state<Record<string, boolean>>({});
	rollbackResult = $state<{
		targetVersion: number;
		newVersion: number;
		appliedDrops?: string[];
		gatedDrops?: string[];
		schema?: APISchema;
	} | null>(null);

	get pages(): number {
		return Math.max(1, Math.ceil(this.total / this.perPage));
	}

	/** The latest version IS the tenant's current schema (append-only invariant). */
	get currentVersion(): number {
		return this.versions.reduce((m, v) => Math.max(m, v.version), 0);
	}

	get approvedKeys(): string[] {
		return (this.rollbackPreview?.destructive ?? [])
			.filter((d) => this.approved[d.key])
			.map((d) => d.key);
	}

	get hasPendingDestructive(): boolean {
		return (this.rollbackPreview?.destructive ?? []).some((d) => !this.approved[d.key]);
	}

	openHistory() {
		this.open = true;
		this.error = null;
		this.viewing = null;
		this.cancelRollback();
		if (deploy.authed) void this.afterAuth();
	}

	close() {
		this.open = false;
		this.error = null;
		this.viewing = null;
		this.cancelRollback();
	}

	async afterAuth() {
		if (!deploy.authed) return;
		if (deploy.tenants.length === 0) await deploy.refreshTenants();
		if (!this.tenantId || !deploy.tenants.some((t) => t.id === this.tenantId)) {
			this.tenantId = deploy.tenants[0]?.id ?? null;
		}
		// The activation mode (restart vs hot-swap) words the post-rollback step.
		await deploy.loadServedResources();
		if (this.tenantId) await this.refresh();
	}

	select(id: string) {
		if (id === this.tenantId) return;
		this.tenantId = id;
		this.page = 1;
		this.versions = [];
		this.total = 0;
		this.viewing = null;
		this.cancelRollback();
		void this.refresh();
	}

	async refresh() {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.schemaHistory(deploy.token, this.tenantId, this.page, this.perPage);
			this.versions = res.versions ?? [];
			this.total = res.total ?? 0;
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	async goto(page: number) {
		if (page < 1 || page > this.pages || page === this.page) return;
		this.page = page;
		await this.refresh();
	}

	/** Diff summary vs the previous version in the loaded page: resource names
	 *  added/removed (the timeline's "what changed" hint; field-level detail is
	 *  what the rollback preview computes against the live DB). */
	changeSummary(v: SchemaVersionMeta): string {
		const prev = this.versions.find((x) => x.version === v.version - 1);
		if (!prev) return v.version === 1 ? 'initial schema' : '';
		const cur = new Set(v.resources);
		const old = new Set(prev.resources);
		const added = v.resources.filter((r) => !old.has(r));
		const removed = prev.resources.filter((r) => !cur.has(r));
		const parts: string[] = [];
		if (added.length > 0) parts.push(`+${added.join(', +')}`);
		if (removed.length > 0) parts.push(`−${removed.join(', −')}`);
		return parts.length > 0 ? parts.join('  ') : 'field-level changes';
	}

	// ── view a version ─────────────────────────────────────────────────────────

	async view(v: SchemaVersionMeta) {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		this.error = null;
		try {
			this.viewing = await adminApi.schemaVersion(deploy.token, this.tenantId, v.version);
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	closeView() {
		this.viewing = null;
	}

	/** Load a recorded version onto the canvas (to inspect or re-work it). */
	loadIntoEditor() {
		if (!this.viewing?.schema?.resources) return;
		editor.loadSchema(this.viewing.schema);
		editor.commitBaselines();
		this.close();
	}

	// ── rollback ───────────────────────────────────────────────────────────────

	/** Open the rollback dialog on version v: fetch the REAL migration preview of
	 *  reverting to it (computed against the live DB — the same dry-run a deploy
	 *  gets), so the operator sees exactly what reverting costs before approving. */
	async startRollback(v: SchemaVersionMeta) {
		if (!deploy.token || !this.tenantId) return;
		this.rollbackTarget = v;
		this.rollbackStep = 'preview';
		this.rollbackPreview = null;
		this.rollbackResult = null;
		this.approved = {};
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.rollbackPreview(deploy.token, this.tenantId, v.version, []);
			this.rollbackPreview = res.preview;
		} catch (e) {
			this.fail(e);
			this.rollbackTarget = null;
		} finally {
			this.busy = false;
		}
	}

	cancelRollback() {
		this.rollbackTarget = null;
		this.rollbackPreview = null;
		this.rollbackResult = null;
		this.approved = {};
		this.rollbackStep = 'preview';
	}

	/** Apply the rollback with ONLY the explicitly-approved drops. */
	async confirmRollback() {
		if (!deploy.token || !this.tenantId || !this.rollbackTarget) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.rollbackApply(
				deploy.token,
				this.tenantId,
				this.rollbackTarget.version,
				this.approvedKeys
			);
			this.rollbackResult = {
				targetVersion: res.target_version,
				newVersion: res.new_version,
				appliedDrops: res.applied_drops,
				gatedDrops: res.gated_drops,
				schema: res.schema
			};
			this.rollbackStep = 'result';
			deploy.restartPhase = 'idle';
			deploy.restartError = null;
			await this.refresh();
		} catch (e) {
			this.fail(e);
		} finally {
			this.busy = false;
		}
	}

	/** Resources the rolled-back-to schema serves vs what the engine serves NOW —
	 *  the activation expectations for the restart/hot-swap verify. */
	private activationExpectations(target: APISchema): { served: string[]; absent: string[] } {
		const targetNames = Object.keys(target.resources ?? {});
		const now = deploy.servedResources ?? [];
		return {
			served: targetNames,
			absent: now.filter((n) => !targetNames.includes(n))
		};
	}

	/** Whether the definition-derived surface differs from what the engine serves
	 *  (a resource appears or disappears) — when false, the rollback's column-level
	 *  changes are live already and activation is optional. */
	get activationChangesSurface(): boolean {
		const sc = this.rollbackResult?.schema;
		if (!sc) return false;
		const { served, absent } = this.activationExpectations(sc);
		const now = new Set(deploy.servedResources ?? []);
		return absent.length > 0 || served.some((n) => !now.has(n));
	}

	/** Activate the rolled-back schema as the engine's boot/served surface —
	 *  the EXISTING restart (single-engine) / hot-swap (fleet) machinery, with
	 *  the verify pointed at the target version's surface. */
	async activate() {
		const sc = this.rollbackResult?.schema;
		if (!sc) return;
		const { served, absent } = this.activationExpectations(sc);
		await deploy.restartEngine({ schema: sc, expectServed: served, expectAbsent: absent });
	}

	private fail(e: unknown) {
		if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
			deploy.logout();
			this.versions = [];
			this.total = 0;
			this.error = 'session expired — sign in again';
			return;
		}
		this.error = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
	}
}

export const historyStore = new HistoryStore();
