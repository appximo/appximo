<script lang="ts">
	import { deploy } from '../stores/deploy.svelte';
	import { filesStore, humanSize, fmtDate } from '../stores/files.svelte';

	let fileInput = $state<HTMLInputElement | null>(null);
	let dragging = $state(false);

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (filesStore.confirmTarget) filesStore.cancelDelete();
			else filesStore.close();
		}
	}

	async function loginThen() {
		await deploy.doLogin();
		if (deploy.authed) await filesStore.afterAuth();
	}

	async function mfaThen() {
		await deploy.doMfa();
		if (deploy.authed) await filesStore.afterAuth();
	}

	function pick(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		if (input.files && input.files.length > 0) void filesStore.upload(input.files);
		input.value = ''; // allow re-picking the same file
	}

	function onDrop(e: DragEvent) {
		e.preventDefault();
		dragging = false;
		if (e.dataTransfer?.files && e.dataTransfer.files.length > 0)
			void filesStore.upload(e.dataTransfer.files);
	}
</script>

{#if filesStore.open}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) filesStore.close();
		}}
		onkeydown={onKey}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Files" tabindex="-1">
			<div class="m-head">
				<div class="m-title">
					<span class="m-logo" aria-hidden="true">
						<svg width="13" height="13" viewBox="0 0 16 16" fill="none">
							<path
								d="M3 2.8h5l1.6 2H13a1.2 1.2 0 0 1 1.2 1.2v6.2A1.3 1.3 0 0 1 12.9 13.5H3.1A1.3 1.3 0 0 1 1.8 12.2V4.1A1.3 1.3 0 0 1 3 2.8z"
								stroke="currentColor"
								stroke-width="1.4"
							/>
						</svg>
					</span>
					Files
					{#if deploy.authed && filesStore.backend}
						<span class="backend" title="active storage backend">
							{filesStore.backend === 'local' ? 'local disk' : filesStore.backend}
						</span>
					{/if}
				</div>
				<div class="m-head-right">
					{#if deploy.authed}
						<span class="who" title="signed in">{deploy.adminEmail}</span>
						<button class="btn subtle xs" onclick={() => deploy.logout()}>Sign out</button>
					{/if}
					<button class="btn subtle xs" onclick={() => filesStore.close()} aria-label="Close">✕</button>
				</div>
			</div>

			<div class="m-body">
				{#if !deploy.authed}
					{#if deploy.step === 'mfa'}
						<!-- ── MFA (shared platform session — same as Deploy) ── -->
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">Enter the 6-digit code from your authenticator app.</p>
						<label class="lbl" for="f-mfa">Authentication code</label>
						<input
							id="f-mfa"
							class="field-input code-in"
							inputmode="numeric"
							bind:value={deploy.mfaCode}
							onkeydown={(e) => e.key === 'Enter' && mfaThen()}
						/>
						<div class="m-actions">
							<button class="btn subtle" onclick={() => (deploy.step = 'login')}>Back</button>
							<button class="btn primary" onclick={mfaThen} disabled={deploy.busy}>
								{deploy.busy ? 'Verifying…' : 'Verify'}
							</button>
						</div>
					{:else}
						<!-- ── LOGIN (shared platform session — same as Deploy) ── -->
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">
							Sign in as a platform super-admin to manage a tenant's files. This is the same
							session the Deploy flow uses — held in memory only.
						</p>
						<label class="lbl" for="f-email">Email</label>
						<input
							id="f-email"
							class="field-input"
							type="email"
							autocomplete="username"
							bind:value={deploy.loginEmail}
							onkeydown={(e) => e.key === 'Enter' && loginThen()}
						/>
						<label class="lbl" for="f-pass">Password</label>
						<input
							id="f-pass"
							class="field-input"
							type="password"
							autocomplete="current-password"
							bind:value={deploy.loginPassword}
							onkeydown={(e) => e.key === 'Enter' && loginThen()}
						/>
						<div class="m-actions">
							<button class="btn primary" onclick={loginThen} disabled={deploy.busy}>
								{deploy.busy ? 'Signing in…' : 'Sign in'}
							</button>
						</div>
						<p class="hint-sm">
							No super-admin yet? Bootstrap one with <code>appximo admin create</code>.
						</p>
					{/if}
				{:else}
					<!-- ── MANAGER ── -->
					<div class="controls">
						<label class="lbl inline" for="f-tenant">Tenant</label>
						<select
							id="f-tenant"
							class="field-input tenant-sel"
							value={filesStore.tenantId ?? ''}
							onchange={(e) => filesStore.select((e.currentTarget as HTMLSelectElement).value)}
						>
							{#each deploy.tenants as t}
								<option value={t.id}>{t.id}{t.suspended ? ' (suspended)' : ''}</option>
							{/each}
						</select>
						<button
							class="btn subtle"
							onclick={() => filesStore.refresh()}
							disabled={filesStore.busy || !filesStore.tenantId}
							title="Refresh"
						>
							Refresh
						</button>
						<div class="spacer"></div>
						<label class="btn primary" class:disabled={filesStore.uploading || !filesStore.tenantId}>
							<svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
								<path
									d="M8 12.4V3.6M8 3.6 4.8 6.8M8 3.6l3.2 3.2M3 13.4h10"
									stroke="currentColor"
									stroke-width="1.6"
									stroke-linecap="round"
									stroke-linejoin="round"
								/>
							</svg>
							Upload
							<input
								type="file"
								multiple
								bind:this={fileInput}
								onchange={pick}
								disabled={filesStore.uploading || !filesStore.tenantId}
								hidden
							/>
						</label>
					</div>

					{#if filesStore.error}
						<div class="banner err">
							<div class="banner-msg">{filesStore.error}</div>
						</div>
					{/if}

					{#if filesStore.uploading}
						<div class="progress-row" aria-live="polite">
							<span class="p-name">{filesStore.uploadName}</span>
							<div class="p-bar">
								<div class="p-fill" style={`width:${filesStore.uploadPct}%`}></div>
							</div>
							<span class="p-pct">{filesStore.uploadPct}%</span>
						</div>
					{/if}

					<!-- svelte-ignore a11y_no_static_element_interactions -->
					<div
						class="table-wrap"
						class:dragging
						ondragover={(e) => {
							e.preventDefault();
							dragging = true;
						}}
						ondragleave={() => (dragging = false)}
						ondrop={onDrop}
					>
						{#if deploy.tenants.length === 0}
							<div class="empty">
								<div class="empty-title">No tenants yet</div>
								<div class="empty-sub">Deploy a schema first — files belong to a running tenant.</div>
							</div>
						{:else if filesStore.files.length === 0 && !filesStore.busy}
							<div class="empty">
								<div class="empty-title">This tenant has no files</div>
								<div class="empty-sub">
									Upload one, or POST to <code>/api/files</code> with a tenant token. Drag & drop
									works too.
								</div>
							</div>
						{:else}
							<table class="ftable">
								<thead>
									<tr>
										<th class="c-name">Name</th>
										<th>Type</th>
										<th class="c-num">Size</th>
										<th>Uploaded</th>
										<th>SHA-256</th>
										<th class="c-act" aria-label="actions"></th>
									</tr>
								</thead>
								<tbody>
									{#each filesStore.files as f (f.id)}
										<tr>
											<td class="c-name" title={f.original_name}>{f.original_name || '(unnamed)'}</td>
											<td class="dim">{f.content_type || '—'}</td>
											<td class="c-num num">{humanSize(f.size)}</td>
											<td class="dim num">{fmtDate(f.created_at)}</td>
											<td class="sha" title={f.sha256}>{f.sha256.slice(0, 12)}…</td>
											<td class="c-act">
												<button class="btn subtle xs" onclick={() => filesStore.download(f)} title="Download via a short-lived signed URL">
													Download
												</button>
												<button class="btn subtle xs danger-link" onclick={() => filesStore.askDelete(f)}>
													Delete
												</button>
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						{/if}
					</div>

					<div class="m-foot">
						<span class="count">
							{filesStore.total}
							{filesStore.total === 1 ? 'file' : 'files'}
						</span>
						{#if filesStore.pages > 1}
							<div class="pager">
								<button class="btn subtle xs" disabled={filesStore.page <= 1} onclick={() => filesStore.goto(filesStore.page - 1)}>‹</button>
								<span class="pg">page {filesStore.page} / {filesStore.pages}</span>
								<button class="btn subtle xs" disabled={filesStore.page >= filesStore.pages} onclick={() => filesStore.goto(filesStore.page + 1)}>›</button>
							</div>
						{/if}
						<div class="spacer"></div>
						<span class="hint">
							Uploads pass the engine's validation (allowlist + content check + size cap) — a
							rejected file shows the engine's reason.
						</span>
					</div>
				{/if}
			</div>
		</div>

		{#if filesStore.confirmTarget}
			<div class="confirm" role="alertdialog" aria-modal="true" aria-label="Confirm delete">
				<div class="confirm-box">
					<div class="confirm-title">Delete file?</div>
					<div class="confirm-msg">
						<span class="fname">{filesStore.confirmTarget.original_name || filesStore.confirmTarget.id}</span>
						will be permanently removed from tenant
						<b>{filesStore.tenantId}</b>. This cannot be undone.
					</div>
					<div class="confirm-actions">
						<button class="btn subtle" onclick={() => filesStore.cancelDelete()} disabled={filesStore.deleting}>
							Cancel
						</button>
						<button class="btn danger" onclick={() => filesStore.confirmDelete()} disabled={filesStore.deleting}>
							{filesStore.deleting ? 'Deleting…' : 'Delete'}
						</button>
					</div>
				</div>
			</div>
		{/if}
	</div>
{/if}

<style>
	.overlay {
		position: fixed;
		inset: 0;
		background: color-mix(in srgb, #0a0c10 42%, transparent);
		backdrop-filter: blur(3px);
		display: grid;
		place-items: center;
		z-index: 100;
	}
	.modal {
		width: min(880px, 94vw);
		max-height: 86vh;
		display: flex;
		flex-direction: column;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-lg);
		overflow: hidden;
	}
	.m-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border);
	}
	.m-title {
		display: flex;
		align-items: center;
		gap: 9px;
		font-weight: 600;
	}
	.m-logo {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		background: var(--brand);
		color: #fff;
		border-radius: 6px;
	}
	.backend {
		font-family: var(--mono);
		font-size: 10.5px;
		color: var(--text-2);
		background: var(--surface-2);
		border: 1px solid var(--border);
		padding: 2px 8px;
		border-radius: 999px;
		font-weight: 500;
	}
	.m-head-right {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.who {
		font-size: 12px;
		color: var(--text-3);
		font-family: var(--mono);
	}
	.btn.xs {
		padding: 3px 8px;
		font-size: 11.5px;
	}
	.m-body {
		display: flex;
		flex-direction: column;
		padding: 14px 16px;
		gap: 10px;
		overflow: auto;
		min-height: 260px;
	}
	.lead {
		margin: 0;
		font-size: 13px;
		color: var(--text-2);
		line-height: 1.55;
	}
	.lbl {
		font-size: 11.5px;
		font-weight: 600;
		color: var(--text-2);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}
	.lbl.inline {
		text-transform: none;
		letter-spacing: 0;
		font-size: 12.5px;
	}
	.code-in {
		font-family: var(--mono);
		letter-spacing: 0.2em;
	}
	.m-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
		margin-top: 4px;
	}
	.hint-sm {
		margin: 0;
		font-size: 12px;
		color: var(--text-3);
	}
	.hint-sm code {
		font-family: var(--mono);
		color: var(--text-2);
	}
	.banner {
		border-radius: var(--radius);
		padding: 9px 12px;
		font-size: 12.5px;
		line-height: 1.5;
	}
	.banner.err {
		background: color-mix(in srgb, var(--danger) 9%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
		color: var(--danger);
	}
	.banner-msg {
		word-break: break-word;
	}

	.controls {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.tenant-sel {
		width: auto;
		min-width: 160px;
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.spacer {
		flex: 1;
	}
	label.btn {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		cursor: pointer;
	}
	label.btn.disabled {
		opacity: 0.55;
		pointer-events: none;
	}

	.progress-row {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 12px;
		color: var(--text-2);
	}
	.p-name {
		font-family: var(--mono);
		max-width: 240px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.p-bar {
		flex: 1;
		height: 5px;
		background: var(--surface-2);
		border-radius: 999px;
		overflow: hidden;
	}
	.p-fill {
		height: 100%;
		background: var(--brand);
		border-radius: 999px;
		transition: width 0.15s ease;
	}
	.p-pct {
		font-family: var(--mono);
		font-size: 11px;
		min-width: 34px;
		text-align: right;
	}

	.table-wrap {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: auto;
		min-height: 180px;
		max-height: 46vh;
	}
	.table-wrap.dragging {
		outline: 2px dashed var(--brand);
		outline-offset: -2px;
	}
	.ftable {
		width: 100%;
		border-collapse: collapse;
		font-size: 12.5px;
	}
	.ftable th {
		position: sticky;
		top: 0;
		background: var(--surface-2);
		text-align: left;
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-3);
		padding: 7px 12px;
		border-bottom: 1px solid var(--border);
		white-space: nowrap;
	}
	.ftable td {
		padding: 7px 12px;
		border-bottom: 1px solid var(--border);
		white-space: nowrap;
		vertical-align: middle;
	}
	.ftable tbody tr:last-child td {
		border-bottom: none;
	}
	.ftable tbody tr:hover {
		background: var(--surface-2);
	}
	.c-name {
		max-width: 240px;
		overflow: hidden;
		text-overflow: ellipsis;
		font-weight: 500;
	}
	.c-num,
	.num {
		font-variant-numeric: tabular-nums;
	}
	.c-num {
		text-align: right;
	}
	.dim {
		color: var(--text-3);
	}
	.sha {
		font-family: var(--mono);
		font-size: 11px;
		color: var(--text-3);
	}
	.c-act {
		text-align: right;
	}
	.danger-link {
		color: var(--danger);
	}

	.empty {
		display: grid;
		place-content: center;
		gap: 4px;
		text-align: center;
		padding: 44px 20px;
	}
	.empty-title {
		font-weight: 600;
		color: var(--text-2);
	}
	.empty-sub {
		font-size: 12.5px;
		color: var(--text-3);
	}
	.empty-sub code {
		font-family: var(--mono);
	}

	.m-foot {
		display: flex;
		align-items: center;
		gap: 12px;
		font-size: 12px;
		color: var(--text-3);
	}
	.count {
		font-variant-numeric: tabular-nums;
	}
	.pager {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.pg {
		font-variant-numeric: tabular-nums;
	}
	.hint {
		text-align: right;
	}

	.confirm {
		position: fixed;
		inset: 0;
		display: grid;
		place-items: center;
		background: color-mix(in srgb, #0a0c10 30%, transparent);
		z-index: 110;
	}
	.confirm-box {
		width: min(420px, 90vw);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-lg);
		padding: 16px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.confirm-title {
		font-weight: 600;
	}
	.confirm-msg {
		font-size: 13px;
		color: var(--text-2);
		line-height: 1.55;
		word-break: break-word;
	}
	.fname {
		font-family: var(--mono);
		color: var(--text);
	}
	.confirm-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
</style>
