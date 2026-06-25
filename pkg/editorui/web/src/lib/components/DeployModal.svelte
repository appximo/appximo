<script lang="ts">
	import { deploy } from '../stores/deploy.svelte';
	import { editor } from '../stores/editor.svelte';

	const eps = $derived(deploy.result ? deploy.endpoints(deploy.result.tenantId) : null);
	const planEmpty = $derived(deploy.preview?.empty === true);

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') deploy.close();
	}
</script>

{#if deploy.open}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) deploy.close();
		}}
		onkeydown={onKey}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Deploy" tabindex="-1">
			<div class="m-head">
				<div class="m-title">
					<span class="m-logo" aria-hidden="true">▤</span>
					Deploy <b>{editor.schemaName}</b>
				</div>
				<div class="m-head-right">
					{#if deploy.authed}
						<span class="who" title="signed in">{deploy.adminEmail}</span>
						<button class="btn subtle xs" onclick={() => deploy.logout()}>Sign out</button>
					{/if}
					<button class="btn subtle xs" onclick={() => deploy.close()} aria-label="Close">✕</button>
				</div>
			</div>

			<!-- step indicator -->
			{#if deploy.authed && deploy.step !== 'result'}
				<div class="steps">
					<span class="step" class:on={deploy.step === 'target'}>1 · Target</span>
					<span class="step" class:on={deploy.step === 'preview'}>2 · Review</span>
					<span class="step">3 · Live</span>
				</div>
			{/if}

			<div class="m-body">
				{#if deploy.error}
					<div class="banner err">
						<div class="banner-msg">{deploy.error}</div>
						{#if deploy.fieldErrors.length > 0}
							<ul class="banner-list">
								{#each deploy.fieldErrors as fe}<li>{fe}</li>{/each}
							</ul>
						{/if}
					</div>
				{/if}

				{#if deploy.step === 'login'}
					<!-- ── LOGIN ── -->
					<p class="lead">
						Sign in as a platform super-admin to deploy. Designing the schema needs no
						sign-in — only deploying does.
					</p>
					<label class="lbl" for="d-email">Email</label>
					<input
						id="d-email"
						class="field-input"
						type="email"
						autocomplete="username"
						bind:value={deploy.loginEmail}
						onkeydown={(e) => e.key === 'Enter' && deploy.doLogin()}
					/>
					<label class="lbl" for="d-pass">Password</label>
					<input
						id="d-pass"
						class="field-input"
						type="password"
						autocomplete="current-password"
						bind:value={deploy.loginPassword}
						onkeydown={(e) => e.key === 'Enter' && deploy.doLogin()}
					/>
					<div class="m-actions">
						<button class="btn primary" onclick={() => deploy.doLogin()} disabled={deploy.busy}>
							{deploy.busy ? 'Signing in…' : 'Sign in'}
						</button>
					</div>
					<p class="hint-sm">
						No super-admin yet? Bootstrap one with
						<code>appitools admin create</code>.
					</p>
				{:else if deploy.step === 'mfa'}
					<!-- ── MFA ── -->
					<p class="lead">Enter the 6-digit code from your authenticator app.</p>
					<label class="lbl" for="d-mfa">Authentication code</label>
					<input
						id="d-mfa"
						class="field-input code-in"
						inputmode="numeric"
						bind:value={deploy.mfaCode}
						onkeydown={(e) => e.key === 'Enter' && deploy.doMfa()}
					/>
					<div class="m-actions">
						<button class="btn subtle" onclick={() => (deploy.step = 'login')}>Back</button>
						<button class="btn primary" onclick={() => deploy.doMfa()} disabled={deploy.busy}>
							{deploy.busy ? 'Verifying…' : 'Verify'}
						</button>
					</div>
				{:else if deploy.step === 'target'}
					<!-- ── TARGET ── -->
					<div class="seg">
						<button class="seg-btn" class:on={deploy.mode === 'new'} onclick={() => (deploy.mode = 'new')}>
							New app
						</button>
						<button
							class="seg-btn"
							class:on={deploy.mode === 'existing'}
							onclick={() => {
								deploy.mode = 'existing';
								deploy.refreshTenants();
							}}
						>
							Update existing
						</button>
					</div>

					<p class="hint-sm model-note">
						The engine serves routes / GraphQL / RBAC from its <b>boot schema</b>: editing
						existing resources (columns, validations, roles…) deploys <b>live</b>; a brand-new
						resource is provisioned but needs an engine restart to be served. You'll be told
						before any restart is needed.
					</p>

					{#if deploy.mode === 'new'}
						<label class="lbl" for="d-tid">Tenant id <span class="muted">(subdomain)</span></label>
						<input
							id="d-tid"
							class="field-input mono"
							placeholder="acme"
							spellcheck="false"
							bind:value={deploy.newId}
						/>
						<div class="grid2">
							<div>
								<label class="lbl" for="d-disp">Display name <span class="muted">(optional)</span></label>
								<input id="d-disp" class="field-input" bind:value={deploy.newDisplay} />
							</div>
							<div>
								<label class="lbl" for="d-plan">Plan</label>
								<input id="d-plan" class="field-input" bind:value={deploy.newPlan} />
							</div>
						</div>
						<label class="lbl" for="d-mail">Contact email <span class="muted">(optional)</span></label>
						<input id="d-mail" class="field-input" type="email" bind:value={deploy.newEmail} />
						<p class="hint-sm">
							Provisions a fresh, isolated tenant from your {editor.entities.length} resource(s).
						</p>
					{:else}
						{#if deploy.tenants.length === 0}
							<p class="muted pad">No tenants yet — deploy a new app first.</p>
						{:else}
							<div class="tenant-list">
								{#each deploy.tenants as t (t.id)}
									<div class="tenant-row" class:sel={deploy.targetId === t.id}>
										<label class="tr-pick">
											<input
												type="radio"
												name="target-tenant"
												value={t.id}
												checked={deploy.targetId === t.id}
												onchange={() => (deploy.targetId = t.id)}
											/>
											<span class="tr-id mono">{t.id}</span>
											<span class="tr-meta">{t.resource_count} resources{t.suspended ? ' · suspended' : ''}</span>
										</label>
										<button class="btn subtle xs" onclick={() => deploy.loadTenant(t.id)} title="Load its schema onto the canvas">
											Load
										</button>
									</div>
								{/each}
							</div>
							<p class="hint-sm">Updating runs a safe migration — you'll preview every change next.</p>
						{/if}
					{/if}

					<div class="m-actions">
						<button class="btn primary" onclick={() => deploy.proceedToPreview()} disabled={deploy.busy}>
							{deploy.busy ? 'Checking…' : 'Continue'}
						</button>
					</div>
				{:else if deploy.step === 'preview'}
					<!-- ── PREVIEW / GATE ── -->
					{#if deploy.mode === 'new'}
						<div class="summary">
							<div class="sum-big">{editor.entities.length}</div>
							<div>resources will be created in a new isolated tenant
								<b class="mono">{deploy.newId}</b>. Nothing to migrate — it's all fresh.</div>
						</div>
					{:else if planEmpty}
						<div class="banner ok"><div class="banner-msg">No changes — the tenant is already up to date.</div></div>
					{:else}
						{#if deploy.preview?.apply && deploy.preview.apply.length > 0}
							<div class="plan-sec">
								<div class="plan-h ok-h">Safe changes ({deploy.preview.apply.length})</div>
								<ul class="plan-list">
									{#each deploy.preview.apply as op}<li class="mono">{op}</li>{/each}
								</ul>
							</div>
						{/if}

						{#if deploy.preview?.destructive && deploy.preview.destructive.length > 0}
							<div class="plan-sec danger-sec">
								<div class="plan-h danger-h">⚠ Destructive — these DESTROY data</div>
								<p class="danger-note">
									Each must be approved explicitly. Unchecked drops are <b>skipped</b> (the
									column/table stays as drift — nothing is lost).
								</p>
								{#each deploy.preview.destructive as d (d.key)}
									<label class="drop-row" class:approved={deploy.approved[d.key]}>
										<input type="checkbox" bind:checked={deploy.approved[d.key]} />
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

						{#if deploy.preview?.concerns && deploy.preview.concerns.length > 0}
							<div class="plan-sec">
								<div class="plan-h warn-h">Concerns</div>
								<ul class="plan-list">
									{#each deploy.preview.concerns as c}<li>{c}</li>{/each}
								</ul>
							</div>
						{/if}

						{#if deploy.preview?.drift && deploy.preview.drift.length > 0}
							<div class="plan-sec">
								<div class="plan-h muted-h">Left as drift (not applied)</div>
								<ul class="plan-list">
									{#each deploy.preview.drift as op}<li class="mono">{op}</li>{/each}
								</ul>
							</div>
						{/if}
					{/if}

					{#if deploy.newResources.length > 0}
						<div class="plan-sec warn-sec">
							<div class="plan-h warn-h">⟳ Needs an engine restart</div>
							<p class="danger-note">
								<b class="mono">{deploy.newResources.join(', ')}</b>
								{deploy.newResources.length === 1 ? 'is a new resource' : 'are new resources'} —
								its tables will be created, but the engine serves REST / GraphQL / RBAC from its
								<b>boot schema</b>, so the API for {deploy.newResources.length === 1 ? 'it' : 'them'}
								is <b>unavailable</b> (a <b>403</b> or <b>404</b>) until the engine restarts with a
								schema that includes {deploy.newResources.length === 1 ? 'it' : 'them'}.
								{#if deploy.liveResources.length > 0}
									Changes to <span class="mono">{deploy.liveResources.join(', ')}</span> are served immediately.
								{/if}
							</p>
							<p class="hint-sm">
								This does <b>not</b> block the deploy — the tables are still provisioned. To serve
								{deploy.newResources.length === 1 ? 'it' : 'them'} now, restart the engine with this
								schema (<code>--schema your-export.json</code>).
							</p>
						</div>
					{/if}

					<div class="m-actions">
						<button class="btn subtle" onclick={() => (deploy.step = 'target')}>Back</button>
						<button class="btn primary" onclick={() => deploy.confirmDeploy()} disabled={deploy.busy || (deploy.mode === 'existing' && planEmpty)}>
							{#if deploy.busy}
								Deploying…
							{:else if deploy.mode === 'new'}
								Deploy new app
							{:else if deploy.approvedKeys.length > 0}
								Apply + drop {deploy.approvedKeys.length}
							{:else}
								Apply safe changes
							{/if}
						</button>
					</div>
				{:else if deploy.step === 'result' && deploy.result}
					<!-- ── RESULT ── -->
					<div class="result-hero">
						<div class="rh-badge">✓</div>
						<div>
							<div class="rh-title">
								<b class="mono">{deploy.result.tenantId}</b> is {deploy.result.created ? 'live' : 'updated'}
							</div>
							<div class="rh-sub">
								{#if deploy.result.restartResources && deploy.result.restartResources.length > 0}
									The existing resources are live; the new ones need an engine restart (below).
								{:else if deploy.result.created}
									Your diagram is now a running REST + GraphQL API.
								{:else}
									The migration was applied safely.
								{/if}
							</div>
						</div>
					</div>

					{#if deploy.result.restartResources && deploy.result.restartResources.length > 0}
						<div class="banner warn">
							<div class="banner-msg">Provisioned — needs a restart to be served: {deploy.result.restartResources.join(', ')}</div>
							<div class="banner-sub">
								Their tables exist, but the REST / GraphQL API is unavailable (<b>403</b>/<b>404</b>)
								until the engine restarts with a schema that includes them (routes, GraphQL and RBAC
								are boot-compiled).
							</div>
						</div>
					{/if}

					{#if deploy.result.appliedDrops && deploy.result.appliedDrops.length > 0}
						<div class="banner warn">
							<div class="banner-msg">Dropped (data removed): {deploy.result.appliedDrops.join(', ')}</div>
						</div>
					{/if}
					{#if deploy.result.gatedDrops && deploy.result.gatedDrops.length > 0}
						<div class="banner ok">
							<div class="banner-msg">Kept as drift (not dropped): {deploy.result.gatedDrops.join(', ')}</div>
						</div>
					{/if}

					{#if eps}
						<div class="ep-list">
							<div class="ep"><span class="ep-k">REST</span><a class="ep-v mono" href={eps.rest} target="_blank" rel="noreferrer">{eps.rest}</a></div>
							<div class="ep"><span class="ep-k">GraphQL</span><a class="ep-v mono" href={eps.graphql} target="_blank" rel="noreferrer">{eps.graphql}</a></div>
							<div class="ep"><span class="ep-k">Swagger</span><a class="ep-v mono" href={eps.docs} target="_blank" rel="noreferrer">{eps.docs}</a></div>
						</div>
						<p class="hint-sm">
							The tenant is addressed by Host subdomain (<span class="mono">{eps.tenantHost}</span>).
							A request needs that Host + a JWT for one of the schema's roles.{#if deploy.result.restartResources && deploy.result.restartResources.length > 0}
								The new resources above respond only after the engine restart.{/if}
						</p>
					{/if}

					<div class="m-actions">
						<button class="btn subtle" onclick={() => deploy.deployAgain()}>Deploy again</button>
						<button class="btn primary" onclick={() => deploy.close()}>Done</button>
					</div>
				{/if}
			</div>
		</div>
	</div>
{/if}

<style>
	.overlay {
		position: fixed;
		inset: 0;
		background: color-mix(in srgb, #0a0c10 46%, transparent);
		backdrop-filter: blur(3px);
		display: grid;
		place-items: center;
		z-index: 200;
	}
	.modal {
		width: min(640px, 94vw);
		max-height: 88vh;
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
		gap: 8px;
		font-size: 14px;
	}
	.m-logo {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		background: var(--brand);
		color: #fff;
		border-radius: 6px;
		font-size: 13px;
	}
	.m-head-right {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.who {
		font-size: 12px;
		color: var(--text-3);
	}
	.btn.xs {
		padding: 3px 7px;
		font-size: 12px;
	}

	.steps {
		display: flex;
		gap: 6px;
		padding: 8px 16px;
		border-bottom: 1px solid var(--border);
		background: var(--surface-2);
	}
	.step {
		font-size: 11px;
		color: var(--text-3);
		font-weight: 600;
	}
	.step.on {
		color: var(--brand);
	}

	.m-body {
		padding: 16px;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 9px;
	}
	.lead {
		margin: 0 0 4px;
		color: var(--text-2);
		font-size: 13px;
		line-height: 1.5;
	}
	.lbl {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-2);
	}
	.muted {
		color: var(--text-3);
		font-weight: 400;
	}
	.mono {
		font-family: var(--mono);
	}
	.grid2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 10px;
	}
	.code-in {
		font-family: var(--mono);
		font-size: 18px;
		letter-spacing: 0.3em;
		text-align: center;
	}
	.m-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
		margin-top: 8px;
	}
	.hint-sm {
		font-size: 12px;
		color: var(--text-3);
		margin: 2px 0 0;
	}
	.hint-sm code {
		font-family: var(--mono);
		background: var(--surface-2);
		padding: 1px 5px;
		border-radius: 4px;
	}
	.model-note {
		background: var(--surface-2);
		border-radius: var(--radius-sm);
		padding: 8px 10px;
		line-height: 1.5;
	}
	.pad {
		padding: 10px 0;
	}

	.banner {
		border-radius: var(--radius-sm);
		padding: 9px 12px;
		font-size: 12.5px;
	}
	.banner.err {
		background: color-mix(in srgb, var(--danger) 10%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 40%, transparent);
		color: var(--danger);
	}
	.banner.ok {
		background: color-mix(in srgb, var(--ok) 10%, transparent);
		border: 1px solid color-mix(in srgb, var(--ok) 40%, transparent);
		color: var(--ok);
	}
	.banner.warn {
		background: color-mix(in srgb, var(--warn) 10%, transparent);
		border: 1px solid color-mix(in srgb, var(--warn) 40%, transparent);
		color: var(--warn);
	}
	.banner-msg {
		font-weight: 600;
	}
	.banner-sub {
		margin-top: 4px;
		font-size: 11.5px;
		font-weight: 400;
		line-height: 1.45;
		opacity: 0.92;
	}
	.banner-list {
		margin: 6px 0 0;
		padding-left: 18px;
	}

	/* segmented control */
	.seg {
		display: flex;
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		overflow: hidden;
		width: fit-content;
	}
	.seg-btn {
		border: none;
		background: var(--surface);
		padding: 7px 16px;
		color: var(--text-2);
		font-weight: 600;
	}
	.seg-btn.on {
		background: var(--brand);
		color: #fff;
	}

	.tenant-list {
		display: flex;
		flex-direction: column;
		gap: 4px;
		max-height: 260px;
		overflow-y: auto;
	}
	.tenant-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 7px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface-2);
	}
	.tenant-row.sel {
		border-color: var(--brand);
		background: var(--brand-100);
	}
	.tr-pick {
		display: flex;
		align-items: center;
		gap: 8px;
		flex: 1;
		cursor: pointer;
	}
	.tr-id {
		font-weight: 700;
	}
	.tr-meta {
		font-size: 11.5px;
		color: var(--text-3);
	}

	.summary {
		display: flex;
		align-items: center;
		gap: 14px;
		padding: 14px;
		background: var(--surface-2);
		border-radius: var(--radius);
		font-size: 13px;
		color: var(--text-2);
		line-height: 1.5;
	}
	.sum-big {
		font-size: 34px;
		font-weight: 800;
		color: var(--brand);
		font-variant-numeric: tabular-nums;
	}

	.plan-sec {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		padding: 10px 12px;
	}
	.plan-h {
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		margin-bottom: 6px;
	}
	.ok-h {
		color: var(--ok);
	}
	.warn-h {
		color: var(--warn);
	}
	.muted-h {
		color: var(--text-3);
	}
	.danger-h {
		color: var(--danger);
	}
	.plan-list {
		margin: 0;
		padding-left: 18px;
		font-size: 12px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.danger-sec {
		border-color: color-mix(in srgb, var(--danger) 45%, var(--border));
		background: color-mix(in srgb, var(--danger) 5%, transparent);
	}
	.warn-sec {
		border-color: color-mix(in srgb, var(--warn) 45%, var(--border));
		background: color-mix(in srgb, var(--warn) 6%, transparent);
	}
	.danger-note {
		font-size: 12px;
		color: var(--text-2);
		margin: 0 0 8px;
		line-height: 1.5;
	}
	.drop-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 7px 8px;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.drop-row:hover {
		background: color-mix(in srgb, var(--danger) 8%, transparent);
	}
	.drop-row.approved {
		background: color-mix(in srgb, var(--danger) 14%, transparent);
	}
	.drop-body {
		flex: 1;
	}
	.drop-sum {
		font-size: 12.5px;
		color: var(--text);
	}
	.drop-key {
		font-size: 11px;
		color: var(--text-3);
	}
	.drop-impact {
		font-size: 11.5px;
		font-weight: 700;
		color: var(--danger);
		white-space: nowrap;
	}
	.drop-impact.zero {
		color: var(--text-3);
	}

	.result-hero {
		display: flex;
		align-items: center;
		gap: 14px;
		padding: 6px 0 4px;
	}
	.rh-badge {
		display: grid;
		place-items: center;
		width: 44px;
		height: 44px;
		border-radius: 50%;
		background: color-mix(in srgb, var(--ok) 18%, transparent);
		color: var(--ok);
		font-size: 22px;
		font-weight: 800;
	}
	.rh-title {
		font-size: 16px;
	}
	.rh-sub {
		font-size: 12.5px;
		color: var(--text-3);
	}
	.ep-list {
		display: flex;
		flex-direction: column;
		gap: 6px;
		background: var(--surface-2);
		border-radius: var(--radius-sm);
		padding: 10px 12px;
	}
	.ep {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	.ep-k {
		font-size: 10px;
		font-weight: 700;
		text-transform: uppercase;
		color: var(--text-3);
		width: 64px;
	}
	.ep-v {
		font-size: 12px;
		color: var(--accent-fk);
		text-decoration: none;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.ep-v:hover {
		text-decoration: underline;
	}
</style>
