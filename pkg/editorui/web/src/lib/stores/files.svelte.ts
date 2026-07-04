// The files-manager store (UI-F5-S1) — the runtime face of the file store
// (FILES-V2). Unlike the design stores this operates on a RUNNING tenant's
// DATA: it browses/uploads/downloads/deletes a tenant's files through the thin
// /admin/tenants/{id}/files routes, which delegate into the engine's real
// files.Store — the manager surfaces the engine's behavior (OWASP 422/413,
// signed URLs, dedup-aware delete), it never reimplements or masks it.
//
// AUTH is the deploy store's platform super-admin session (in memory only) —
// one auth, shared, never duplicated. Opening Files without a session shows
// the same login the deploy modal uses.

import { adminApi, ApiError, uploadTenantFile, type FileMeta } from '../api/admin';
import { deploy } from './deploy.svelte';

class FilesStore {
	open = $state(false);
	busy = $state(false);
	error = $state<string | null>(null);

	tenantId = $state<string | null>(null);
	files = $state<FileMeta[]>([]);
	total = $state(0);
	page = $state(1);
	perPage = 50;
	backend = $state<string | null>(null);

	// upload progress (XHR streams the File from disk — never into JS memory)
	uploading = $state(false);
	uploadPct = $state(0);
	uploadName = $state('');

	// delete confirmation (destructive → explicit, named consent)
	confirmTarget = $state<FileMeta | null>(null);
	deleting = $state(false);

	get pages(): number {
		return Math.max(1, Math.ceil(this.total / this.perPage));
	}

	openFiles() {
		this.open = true;
		this.error = null;
		if (deploy.authed) void this.afterAuth();
	}

	close() {
		this.open = false;
		this.error = null;
		this.confirmTarget = null;
	}

	/** Called when a session exists (on open, or right after a login done from
	 *  this modal via the shared deploy auth). Loads tenants + first listing. */
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
		this.page = 1;
		this.files = [];
		this.total = 0;
		void this.refresh();
	}

	async refresh() {
		if (!deploy.token || !this.tenantId) return;
		this.busy = true;
		this.error = null;
		try {
			const res = await adminApi.listFiles(deploy.token, this.tenantId, this.page, this.perPage);
			this.files = res.files ?? [];
			this.total = res.total ?? 0;
			this.backend = res.backend ?? null;
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

	/** Upload the picked files sequentially, surfacing the engine's REAL
	 *  rejection per file (extension allowlist / magic bytes → 422 with the
	 *  reason; over the cap → 413) — actionable, never masked. */
	async upload(list: FileList | File[]) {
		if (!deploy.token || !this.tenantId) return;
		const items = Array.from(list);
		if (items.length === 0) return;
		this.error = null;
		this.uploading = true;
		const rejections: string[] = [];
		try {
			for (const f of items) {
				this.uploadName = f.name;
				this.uploadPct = 0;
				try {
					await uploadTenantFile(deploy.token, this.tenantId, f, (pct) => (this.uploadPct = pct));
				} catch (e) {
					if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
						this.authExpired();
						return;
					}
					rejections.push(`${f.name}: ${e instanceof Error ? e.message : String(e)}`);
				}
			}
		} finally {
			this.uploading = false;
			this.uploadName = '';
		}
		// Refresh FIRST (it clears the error state), then surface the engine's
		// rejections so they stay visible over the fresh listing.
		await this.refresh();
		if (rejections.length > 0) this.error = rejections.join(' · ');
	}

	/** Download via the REAL signed-URL mechanism: mint a short-lived URL
	 *  (native presigned on S3; the engine's token URL on local) and navigate
	 *  to it — the storage/engine streams the bytes, the browser saves. No
	 *  parallel byte path, nothing buffered in JS. */
	async download(f: FileMeta) {
		if (!deploy.token || !this.tenantId) return;
		this.error = null;
		try {
			const res = await adminApi.fileSignedURL(deploy.token, this.tenantId, f.id);
			const a = document.createElement('a');
			a.href = res.url;
			a.download = f.original_name || 'download';
			document.body.appendChild(a);
			a.click();
			a.remove();
		} catch (e) {
			this.fail(e);
		}
	}

	askDelete(f: FileMeta) {
		this.confirmTarget = f;
	}

	cancelDelete() {
		this.confirmTarget = null;
	}

	async confirmDelete() {
		const f = this.confirmTarget;
		if (!deploy.token || !this.tenantId || !f) return;
		this.deleting = true;
		this.error = null;
		try {
			await adminApi.deleteFile(deploy.token, this.tenantId, f.id);
			this.confirmTarget = null;
			// If the page emptied, step back one (the engine's 204 is done — this
			// is just keeping the pagination coherent).
			if (this.files.length === 1 && this.page > 1) this.page -= 1;
			await this.refresh();
		} catch (e) {
			if (e instanceof ApiError && e.status === 404) {
				// Already gone (deleted elsewhere) — refresh reflects reality.
				this.confirmTarget = null;
				await this.refresh();
			} else {
				this.fail(e);
			}
		} finally {
			this.deleting = false;
		}
	}

	private authExpired() {
		deploy.logout(); // the modal falls back to the shared login step
		this.files = [];
		this.total = 0;
		this.error = 'session expired — sign in again';
	}

	private fail(e: unknown) {
		if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
			this.authExpired();
			return;
		}
		this.error = e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);
	}
}

export const filesStore = new FilesStore();

// ── presentation helpers (pure) ──────────────────────────────────────────────

/** "2.4 MB" — humanized size, one decimal below 10. */
export function humanSize(n: number): string {
	if (n < 1024) return `${n} B`;
	const units = ['KB', 'MB', 'GB', 'TB'];
	let v = n;
	let i = -1;
	do {
		v /= 1024;
		i++;
	} while (v >= 1024 && i < units.length - 1);
	return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

/** "2026-07-04 15:04" — compact local timestamp. */
export function fmtDate(iso: string): string {
	const d = new Date(iso);
	if (isNaN(d.getTime())) return iso;
	const p = (x: number) => String(x).padStart(2, '0');
	return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
