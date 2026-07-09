<script lang="ts">
	import { deploy } from '../stores/deploy.svelte';
	import { historyStore } from '../stores/history.svelte';
	import { fmtDate } from '../stores/files.svelte';

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (historyStore.viewing) historyStore.closeView();
			else if (historyStore.rollbackTarget) historyStore.cancelRollback();
			else historyStore.close();
		}
	}

	async function loginThen() {
		await deploy.doLogin();
		if (deploy.authed) await historyStore.afterAuth();
	}

	async function mfaThen() {
		await deploy.doMfa();
		if (deploy.authed) await historyStore.afterAuth();
	}

	const prettySchema = $derived(
		historyStore.viewing ? JSON.stringify(historyStore.viewing.schema, null, 2) : ''
	);
</script>

{#if historyStore.open}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) historyStore.close();
		}}
		onkeydown={onKey}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Schema history" tabindex="-1">
			<div class="m-head">
				<div class="m-title">
					<span class="m-logo" aria-hidden="true">
						<svg width="13" height="13" viewBox="0 0 16 16" fill="none">
							<path
								d="M8 4.5V8l2.4 1.6M14 8A6 6 0 1 1 8 2a6 6 0 0 1 6 6z"
								stroke="currentColor"
								stroke-width="1.4"
								stroke-linecap="round"
								stroke-linejoin="round"
							/>
						</svg>
					</span>
					History
				</div>
				<div class="m-head-right">
					{#if deploy.authed}
						<span class="who" title="signed in">{deploy.adminEmail}</span>
						<button class="btn subtle xs" onclick={() => deploy.logout()}>Sign out</button>
					{/if}
					<button class="btn subtle xs" onclick={() => historyStore.close()} aria-label="Close">✕</button>
				</div>
			</div>

			<div class="m-body">
				{#if !deploy.authed}
					{#if deploy.step === 'mfa'}
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">Enter the 6-digit code from your authenticator app.</p>
						<label class="lbl" for="h-mfa">Authentication code</label>
						<input
							id="h-mfa"
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
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">
							Sign in as a platform super-admin to browse a tenant's schema versions. Same
							in-memory session as the Deploy flow.
						</p>
						<label class="lbl" for="h-email">Email</label>
						<input
							id="h-email"
							class="field-input"
							type="email"
							autocomplete="username"
							bind:value={deploy.loginEmail}
							onkeydown={(e) => e.key === 'Enter' && loginThen()}
						/>
						<label class="lbl" for="h-pass">Password</label>
						<input
							id="h-pass"
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
					{/if}
				{:else}
					<!-- ── TIMELINE ── -->
					<div class="controls">
						<label class="lbl inline" for="h-tenant">Tenant</label>
						<select
							id="h-tenant"
							class="field-input tenant-sel"
							value={historyStore.tenantId ?? ''}
							onchange={(e) => historyStore.select((e.currentTarget as HTMLSelectElement).value)}
						>
							{#each deploy.tenants as t}
								<option value={t.id}>{t.id}{t.suspended ? ' (suspended)' : ''}</option>
							{/each}
						</select>
						<button
							class="btn subtle"
							onclick={() => historyStore.refresh()}
							disabled={historyStore.busy || !historyStore.tenantId}
						>
							Refresh
						</button>
						<div class="spacer"></div>
						<span class="hint">Every deploy is a version. A rollback appends a new one — the trail is never rewritten.</span>
					</div>

					{#if historyStore.error}
						<div class="banner err"><div class="banner-msg">{historyStore.error}</div></div>
					{/if}

					<div class="table-wrap">
						{#if deploy.tenants.length === 0}
							<div class="empty">
								<div class="empty-title">No tenants yet</div>
								<div class="empty-sub">Deploy a schema first — history begins with a tenant's first version.</div>
							</div>
						{:else if historyStore.versions.length === 0 && !historyStore.busy}
							<div class="empty">
								<div class="empty-title">No versions recorded</div>
								<div class="empty-sub">This tenant predates versioning and has no stored schema, or has never been deployed.</div>
							</div>
						{:else}
							<table class="ftable">
								<thead>
									<tr>
										<th>Version</th>
										<th>Deployed</th>
										<th>Source</th>
										<th class="c-name">Changes</th>
										<th class="c-num">Resources</th>
										<th>Hash</th>
										<th class="c-act" aria-label="actions"></th>
									</tr>
								</thead>
								<tbody>
									{#each historyStore.versions as v (v.version)}
										<tr>
											<td class="vcell">
												<span class="vnum">v{v.version}</span>
												{#if v.version === historyStore.currentVersion}
													<span class="badge current">current</span>
												{/if}
											</td>
											<td class="dim num">{fmtDate(v.created_at)}</td>
											<td><span class="badge src-{v.source}">{v.source}</span>{#if v.note}<span class="note" title={v.note}> {v.note}</span>{/if}</td>
											<td class="c-name changes" title={historyStore.changeSummary(v)}>{historyStore.changeSummary(v)}</td>
											<td class="c-num num">{v.resources.length}</td>
											<td class="sha" title={v.hash}>{v.hash.slice(0, 12)}…</td>
											<td class="c-act">
												<button class="btn subtle xs" onclick={() => historyStore.view(v)}>View</button>
												{#if v.version !== historyStore.currentVersion}
													<button
														class="btn subtle xs danger-link"
														onclick={() => historyStore.startRollback(v)}
														title="Re-deploy this version through the migration engine (with the destructive gate)"
													>
														Roll back
													</button>
												{/if}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						{/if}
					</div>

					<div class="m-foot">
						<span class="count">{historyStore.total} {historyStore.total === 1 ? 'version' : 'versions'}</span>
						{#if historyStore.pages > 1}
							<div class="pager">
								<button class="btn subtle xs" disabled={historyStore.page <= 1} onclick={() => historyStore.goto(historyStore.page - 1)}>‹</button>
								<span class="pg">page {historyStore.page} / {historyStore.pages}</span>
								<button class="btn subtle xs" disabled={historyStore.page >= historyStore.pages} onclick={() => historyStore.goto(historyStore.page + 1)}>›</button>
							</div>
						{/if}
					</div>
				{/if}
			</div>
		</div>

		<!-- ── VIEW A VERSION ── -->
		{#if historyStore.viewing}
			<div class="confirm" role="dialog" aria-modal="true" aria-label="Schema version">
				<div class="confirm-box wide">
					<div class="confirm-title">
						v{historyStore.viewing.version}
						<span class="badge src-{historyStore.viewing.source}">{historyStore.viewing.source}</span>
						<span class="dim vmeta">{fmtDate(historyStore.viewing.created_at)} · {historyStore.viewing.resources.join(', ')}</span>
					</div>
					<pre class="schema-view">{prettySchema}</pre>
					<div class="confirm-actions">
						<button class="btn subtle" onclick={() => historyStore.closeView()}>Close</button>
						<button class="btn" onclick={() => historyStore.loadIntoEditor()} title="Replace the canvas with this version">
							Load into editor
						</button>
					</div>
				</div>
			</div>
		{/if}

		<!-- ── ROLLBACK ── -->
		{#if historyStore.rollbackTarget}
			<div class="confirm" role="alertdialog" aria-modal="true" aria-label="Roll back">
				<div class="confirm-box wide">
					{#if historyStore.rollbackStep === 'preview'}
						<div class="confirm-title">Roll back {historyStore.tenantId} to v{historyStore.rollbackTarget.version}?</div>

						<div class="banner warn">
							A rollback is a <b>re-deploy of v{historyStore.rollbackTarget.version}</b> through the
							migration engine — not an undo. What later versions <b>added</b> is reverted as
							<b>drops</b> (data loss, gated below, approve each one). Data already lost to a drop
							you approved in a past deploy is <b>gone — no rollback recovers it</b>.
						</div>

						{#if historyStore.error}
							<div class="banner err"><div class="banner-msg">{historyStore.error}</div></div>
						{/if}

						{#if historyStore.busy && !historyStore.rollbackPreview}
							<p class="lead">Computing the migration plan against the live database…</p>
						{:else if historyStore.rollbackPreview}
							{#if historyStore.rollbackPreview.empty}
								<p class="lead">
									The live database already matches v{historyStore.rollbackTarget.version} — applying
									only re-marks it as the current version.
								</p>
							{/if}

							{#if historyStore.rollbackPreview.apply && historyStore.rollbackPreview.apply.length > 0}
								<div class="plan-sec">
									<div class="plan-h">Reverts cleanly (no data loss)</div>
									<ul class="plan-list">
										{#each historyStore.rollbackPreview.apply as op}<li class="mono">{op}</li>{/each}
									</ul>
								</div>
							{/if}

							{#if historyStore.rollbackPreview.destructive && historyStore.rollbackPreview.destructive.length > 0}
								<div class="plan-sec danger-sec">
									<div class="plan-h danger-h">⚠ Reverting these DESTROYS data added after v{historyStore.rollbackTarget.version}</div>
									<p class="danger-note">
										Each must be approved explicitly. Unchecked drops are <b>skipped</b> (the
										column/table stays as drift — nothing is lost).
									</p>
									{#each historyStore.rollbackPreview.destructive as d (d.key)}
										<label class="drop-row" class:approved={historyStore.approved[d.key]}>
											<input type="checkbox" bind:checked={historyStore.approved[d.key]} />
											<div class="drop-body">
												<div class="drop-sum">{d.summary}</div>
												<div class="drop-key mono">{d.key}</div>
											</div>
											<span class="drop-impact tnum" class:zero={d.rows_lost === 0}>
												{d.rows_lost} / {d.table_rows} rows
											</span>
										</label>
									{/each}
								</div>
							{/if}

							{#if historyStore.rollbackPreview.concerns && historyStore.rollbackPreview.concerns.length > 0}
								<div class="plan-sec">
									<div class="plan-h warn-h">Concerns</div>
									<ul class="plan-list">
										{#each historyStore.rollbackPreview.concerns as c}<li>{c}</li>{/each}
									</ul>
								</div>
							{/if}
						{/if}

						<div class="confirm-actions">
							<button class="btn subtle" onclick={() => historyStore.cancelRollback()} disabled={historyStore.busy}>Cancel</button>
							<button
								class="btn danger"
								onclick={() => historyStore.confirmRollback()}
								disabled={historyStore.busy || !historyStore.rollbackPreview}
							>
								{historyStore.busy
									? 'Rolling back…'
									: historyStore.approvedKeys.length > 0
										? `Roll back + drop ${historyStore.approvedKeys.length}`
										: 'Roll back'}
							</button>
						</div>
					{:else if historyStore.rollbackResult}
						<div class="confirm-title">
							Rolled back to v{historyStore.rollbackResult.targetVersion}
							{#if historyStore.rollbackResult.newVersion > 0}
								<span class="badge current">now v{historyStore.rollbackResult.newVersion}</span>
							{/if}
						</div>
						<div class="confirm-msg">
							The tenant's tables now match v{historyStore.rollbackResult.targetVersion}. The history
							is append-only: the rollback was recorded as a new version with the old content.
						</div>
						{#if historyStore.rollbackResult.appliedDrops && historyStore.rollbackResult.appliedDrops.length > 0}
							<div class="banner warn">Dropped (approved): {historyStore.rollbackResult.appliedDrops.join(', ')}</div>
						{/if}
						{#if historyStore.rollbackResult.gatedDrops && historyStore.rollbackResult.gatedDrops.length > 0}
							<div class="banner info">Kept as drift (not dropped): {historyStore.rollbackResult.gatedDrops.join(', ')}</div>
						{/if}

						{#if deploy.restartPhase === 'live'}
							<div class="banner ok">
								The engine now serves v{historyStore.rollbackResult.targetVersion}'s surface — verified.
							</div>
						{:else if deploy.restartPhase === 'restarting' || deploy.restartPhase === 'waiting'}
							<div class="banner info">
								{deploy.activation === 'hot_swap'
									? 'Hot-swapping this app and verifying the surface…'
									: 'Engine draining and relaunching — verifying the surface when it returns…'}
							</div>
						{:else if deploy.restartPhase === 'failed'}
							<div class="banner err"><div class="banner-msg">{deploy.restartError}</div></div>
						{:else}
							<div class="banner info">
								Tables are rolled back <b>now</b>. Everything compiled from the schema definition
								(validation, filters, GraphQL, docs{historyStore.activationChangesSurface ? ', routes' : ''})
								still runs the previous version until you activate —
								{deploy.activation === 'hot_swap'
									? 'hot-swap recompiles only this app, no downtime, other apps untouched.'
									: 'a graceful engine restart (~6 s).'}
							</div>
						{/if}

						<div class="confirm-actions">
							<button class="btn subtle" onclick={() => historyStore.cancelRollback()}>Close</button>
							{#if deploy.selfRestartAvailable && deploy.restartPhase !== 'live' && deploy.restartPhase !== 'restarting' && deploy.restartPhase !== 'waiting'}
								<button class="btn primary" onclick={() => historyStore.activate()}>
									{deploy.activation === 'hot_swap'
										? `Activate v${historyStore.rollbackResult.targetVersion} now (hot-swap)`
										: `Activate v${historyStore.rollbackResult.targetVersion} now (restart)`}
								</button>
							{/if}
						</div>
					{/if}
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
		width: min(940px, 94vw);
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
	.banner.warn {
		background: color-mix(in srgb, var(--warn, #b45309) 9%, transparent);
		border: 1px solid color-mix(in srgb, var(--warn, #b45309) 35%, transparent);
		color: var(--warn, #b45309);
	}
	.banner.info {
		background: var(--surface-2);
		border: 1px solid var(--border);
		color: var(--text-2);
	}
	.banner.ok {
		background: color-mix(in srgb, var(--ok, #15803d) 9%, transparent);
		border: 1px solid color-mix(in srgb, var(--ok, #15803d) 35%, transparent);
		color: var(--ok, #15803d);
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
	.hint {
		font-size: 12px;
		color: var(--text-3);
		text-align: right;
	}

	.table-wrap {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: auto;
		min-height: 180px;
		max-height: 46vh;
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
	.vcell {
		display: flex;
		align-items: center;
		gap: 7px;
	}
	.vnum {
		font-family: var(--mono);
		font-weight: 600;
	}
	.badge {
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		padding: 1.5px 7px;
		border-radius: 999px;
		border: 1px solid var(--border);
		color: var(--text-3);
		background: var(--surface-2);
	}
	.badge.current {
		color: var(--ok, #15803d);
		border-color: color-mix(in srgb, var(--ok, #15803d) 40%, transparent);
		background: color-mix(in srgb, var(--ok, #15803d) 9%, transparent);
	}
	.badge.src-rollback {
		color: var(--warn, #b45309);
		border-color: color-mix(in srgb, var(--warn, #b45309) 40%, transparent);
		background: color-mix(in srgb, var(--warn, #b45309) 9%, transparent);
	}
	.note {
		font-size: 11px;
		color: var(--text-3);
		font-family: var(--mono);
	}
	.changes {
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
		color: var(--text-2);
		font-family: var(--mono);
		font-size: 11.5px;
	}
	.c-name {
		max-width: 240px;
		overflow: hidden;
		text-overflow: ellipsis;
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

	.confirm {
		position: fixed;
		inset: 0;
		display: grid;
		place-items: center;
		background: color-mix(in srgb, #0a0c10 30%, transparent);
		z-index: 110;
	}
	.confirm-box {
		width: min(460px, 90vw);
		max-height: 84vh;
		overflow: auto;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-lg);
		padding: 16px;
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.confirm-box.wide {
		width: min(660px, 92vw);
	}
	.confirm-title {
		font-weight: 600;
		display: flex;
		align-items: center;
		gap: 8px;
		flex-wrap: wrap;
	}
	.vmeta {
		font-size: 11.5px;
		font-weight: 400;
	}
	.confirm-msg {
		font-size: 13px;
		color: var(--text-2);
		line-height: 1.55;
		word-break: break-word;
	}
	.confirm-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
	.schema-view {
		margin: 0;
		font-family: var(--mono);
		font-size: 11.5px;
		line-height: 1.5;
		background: var(--surface-2);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		overflow: auto;
		max-height: 52vh;
		white-space: pre;
	}

	.plan-sec {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.plan-sec.danger-sec {
		border-color: color-mix(in srgb, var(--danger) 35%, transparent);
	}
	.plan-h {
		font-size: 11.5px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-2);
	}
	.plan-h.danger-h {
		color: var(--danger);
	}
	.plan-h.warn-h {
		color: var(--warn, #b45309);
	}
	.plan-list {
		margin: 0;
		padding-left: 18px;
		font-size: 12px;
		color: var(--text-2);
		line-height: 1.6;
	}
	.mono {
		font-family: var(--mono);
	}
	.danger-note {
		margin: 0;
		font-size: 12px;
		color: var(--text-2);
		line-height: 1.5;
	}
	.drop-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 7px 9px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		cursor: pointer;
	}
	.drop-row:hover {
		background: var(--surface-2);
	}
	.drop-row.approved {
		border-color: color-mix(in srgb, var(--danger) 45%, transparent);
		background: color-mix(in srgb, var(--danger) 6%, transparent);
	}
	.drop-body {
		flex: 1;
		min-width: 0;
	}
	.drop-sum {
		font-size: 12.5px;
		line-height: 1.45;
	}
	.drop-key {
		font-size: 11px;
		color: var(--text-3);
	}
	.drop-impact {
		font-size: 11.5px;
		color: var(--danger);
		white-space: nowrap;
	}
	.drop-impact.zero {
		color: var(--text-3);
	}
	.tnum {
		font-variant-numeric: tabular-nums;
	}
</style>
