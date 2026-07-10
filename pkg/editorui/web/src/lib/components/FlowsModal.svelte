<script lang="ts">
	import { deploy } from '../stores/deploy.svelte';
	import { flowTests } from '../stores/flowtests.svelte';
	import { fmtDate } from '../stores/files.svelte';
	import FlowStepCard from './FlowStepCard.svelte';

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			if (flowTests.confirmTarget) flowTests.cancelDelete();
			else flowTests.close();
		}
	}

	async function loginThen() {
		await deploy.doLogin();
		if (deploy.authed) await flowTests.afterAuth();
	}
	async function mfaThen() {
		await deploy.doMfa();
		if (deploy.authed) await flowTests.afterAuth();
	}

	/** The assisted login step: pick a real tenant user (or blank) — the store
	 *  builds the POST /auth/login step with the {{token}} capture wired. */
	function addLoginFrom(e: Event) {
		const sel = e.currentTarget as HTMLSelectElement;
		if (sel.value === '') return;
		flowTests.addLoginStep(sel.value === '·blank·' ? undefined : sel.value);
		sel.value = '';
	}

	/** Pretty-print a step's response for the run log (raw when not JSON). */
	function fmtResponse(s: string | undefined): string {
		if (!s) return '';
		try {
			return JSON.stringify(JSON.parse(s), null, 2);
		} catch {
			return s;
		}
	}

	const doneOk = $derived(flowTests.runDone ? Boolean(flowTests.runDone.pass) : null);
</script>

{#if flowTests.open}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) flowTests.close();
		}}
		onkeydown={onKey}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Flow tests" tabindex="-1">
			<div class="m-head">
				<div class="m-title">
					<span class="m-logo" aria-hidden="true">
						<svg width="13" height="13" viewBox="0 0 16 16" fill="none">
							<path d="M2.5 8h3l2-4.5L10 12l1.8-4H13.5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
						</svg>
					</span>
					Flows
				</div>
				<div class="m-head-right">
					{#if deploy.authed}
						<span class="who" title="signed in">{deploy.adminEmail}</span>
						<button class="btn subtle xs" onclick={() => deploy.logout()}>Sign out</button>
					{/if}
					<button class="btn subtle xs" onclick={() => flowTests.close()} aria-label="Close" disabled={flowTests.running}>✕</button>
				</div>
			</div>

			<div class="m-body">
				{#if !deploy.authed}
					{#if deploy.step === 'mfa'}
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">Enter the 6-digit code from your authenticator app.</p>
						<label class="lbl" for="fl-mfa">Authentication code</label>
						<input id="fl-mfa" class="field-input code-in" inputmode="numeric" bind:value={deploy.mfaCode} onkeydown={(e) => e.key === 'Enter' && mfaThen()} />
						<div class="m-actions">
							<button class="btn subtle" onclick={() => (deploy.step = 'login')}>Back</button>
							<button class="btn primary" onclick={mfaThen} disabled={deploy.busy}>{deploy.busy ? 'Verifying…' : 'Verify'}</button>
						</div>
					{:else}
						{#if deploy.error}<div class="banner err">{deploy.error}</div>{/if}
						<p class="lead">
							Sign in as a platform super-admin to manage a tenant's flow tests. The flows
							themselves run as TENANT users (a login step or a role) — real RBAC, real chain.
						</p>
						<label class="lbl" for="fl-email">Email</label>
						<input id="fl-email" class="field-input" type="email" autocomplete="username" bind:value={deploy.loginEmail} onkeydown={(e) => e.key === 'Enter' && loginThen()} />
						<label class="lbl" for="fl-pass">Password</label>
						<input id="fl-pass" class="field-input" type="password" autocomplete="current-password" bind:value={deploy.loginPassword} onkeydown={(e) => e.key === 'Enter' && loginThen()} />
						<div class="m-actions">
							<button class="btn primary" onclick={loginThen} disabled={deploy.busy}>{deploy.busy ? 'Signing in…' : 'Sign in'}</button>
						</div>
					{/if}
				{:else}
					<div class="controls">
						<label class="lbl inline" for="fl-tenant">Tenant</label>
						<select
							id="fl-tenant"
							class="field-input tenant-sel"
							value={flowTests.tenantId ?? ''}
							disabled={flowTests.running}
							onchange={(e) => flowTests.select((e.currentTarget as HTMLSelectElement).value)}
						>
							{#each deploy.tenants as t}
								<option value={t.id}>{t.id}{t.suspended ? ' (suspended)' : ''}</option>
							{/each}
						</select>
						<button class="btn subtle" class:active={flowTests.view === 'list'} onclick={() => (flowTests.view = 'list')} disabled={flowTests.running}>Flows</button>
						<button class="btn subtle" class:active={flowTests.view === 'history'} onclick={() => flowTests.loadRuns()} disabled={flowTests.running}>History</button>
						<div class="spacer"></div>
						{#if flowTests.view === 'list'}
							<button class="btn" onclick={() => flowTests.newFlow()}>+ Flow</button>
							<button class="btn primary" onclick={() => flowTests.runSuite()} disabled={flowTests.flows.length === 0 || flowTests.running}>
								▶ Run all
							</button>
						{/if}
					</div>

					{#if flowTests.error}
						<div class="banner err"><div class="banner-msg">{flowTests.error}</div></div>
					{/if}

					{#if flowTests.view === 'list'}
						<div class="table-wrap">
							{#if flowTests.flows.length === 0 && !flowTests.busy}
								<div class="empty">
									<div class="empty-title">No flows yet</div>
									<div class="empty-sub">
										A flow is a saved multi-step scenario (login as a role → create → attach →
										assert). Build one, run it after every deploy, trust with evidence.
									</div>
								</div>
							{:else}
								<table class="ftable">
									<thead>
										<tr><th class="c-name">Name</th><th>Auth</th><th class="c-num">Steps</th><th>Updated</th><th class="c-act" aria-label="actions"></th></tr>
									</thead>
									<tbody>
										{#each flowTests.flows as f (f.id)}
											<tr>
												<td class="c-name">{f.name}</td>
												<td class="dim">{f.flow?.role || 'login step'}</td>
												<td class="c-num num">{f.steps}</td>
												<td class="dim num">{fmtDate(f.updated_at)}</td>
												<td class="c-act">
													<button class="btn subtle xs" onclick={() => flowTests.runFlow(f)} disabled={flowTests.running}>▶ Run</button>
													<button class="btn subtle xs" onclick={() => flowTests.editFlow(f)}>Edit</button>
													<button class="btn subtle xs danger-link" onclick={() => flowTests.askDelete(f)}>Delete</button>
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							{/if}
						</div>
						<div class="m-foot">
							<span class="count">{flowTests.flows.length} {flowTests.flows.length === 1 ? 'flow' : 'flows'}</span>
							<div class="spacer"></div>
							<span class="hint">Runs execute against the live app — full chain, tenant-user auth — and are recorded with the schema version.</span>
						</div>

					{:else if flowTests.view === 'edit'}
						<div class="edit-wrap">
							<div class="frow">
								<div class="fcol grow">
									<label class="lbl" for="fl-name">Flow name</label>
									<input id="fl-name" class="field-input" placeholder="optica: cita con formula" bind:value={flowTests.draft.name} />
								</div>
								<div class="fcol">
									<label class="lbl" for="fl-role" title="Pre-mints a tenant JWT for this RBAC role as {'{{token}}'}. Leave empty when the flow does its own /auth/login step.">Role (optional)</label>
									<input id="fl-role" class="field-input" list="fl-roles" placeholder="use a login step" bind:value={flowTests.draft.role} />
									<datalist id="fl-roles">
										{#each flowTests.roleSuggestions as r}<option value={r}></option>{/each}
									</datalist>
								</div>
							</div>

							{#each flowTests.draft.steps as _, si (si)}
								<FlowStepCard {si} />
							{/each}
							<datalist id="fl-paths">
								{#each flowTests.pathSuggestions as p}<option value={p}></option>{/each}
							</datalist>

							<div class="frow">
								<button class="btn" onclick={() => flowTests.addStep()}>+ Step</button>
								<select class="field-input login-sel" onchange={addLoginFrom} title="Insert an assisted login step at the top: POST /auth/login as a real tenant user, with the token captured into {'{{token}}'} — later steps authenticate with it automatically">
									<option value="">+ Login step as…</option>
									{#each flowTests.users as u (u.email)}
										<option value={u.email}>{u.email} ({u.role})</option>
									{/each}
									<option value="·blank·">type the email myself</option>
								</select>
								<div class="spacer"></div>
								<button class="btn subtle" onclick={() => (flowTests.view = 'list')}>Cancel</button>
								<button class="btn primary" onclick={() => flowTests.saveDraft()} disabled={flowTests.busy || !flowTests.draft.name.trim()}>
									{flowTests.busy ? 'Saving…' : 'Save flow'}
								</button>
							</div>
							<p class="hint-sm">
								Variables captured in one step ({'{{token}}'}, {'{{cita_id}}'}…) substitute into later
								paths/bodies/asserts. {'{{run_id}}'} (and the short {'{{run_tag}}'}) are unique per run — use
								them for unique values. {'{{token}}'} authenticates every later step automatically (or set a
								flow Role to pre-mint one).
							</p>
						</div>

					{:else if flowTests.view === 'run'}
						<div class="run-wrap">
							<div class="run-head">
								<span class="run-title">Run: <b>{flowTests.runScope}</b></span>
								{#if flowTests.running}<span class="badge live">running…</span>
								{:else if doneOk === true}<span class="badge pass">PASS</span>
								{:else if doneOk === false}<span class="badge failb">FAIL</span>{/if}
							</div>
							<div class="run-log">
								{#each flowTests.events as ev, i (i)}
									{#if ev.kind === 'flow'}
										<div class="log-flow">▸ {ev.flowName}</div>
									{:else if ev.kind === 'step' && ev.step}
										<div class="log-step" class:fail={!ev.step.pass && !ev.step.skipped} class:skip={ev.step.skipped}>
											<span class="log-badge">{ev.step.skipped ? '–' : ev.step.pass ? '✓' : '✗'}</span>
											<span class="log-name">{ev.step.name}</span>
											<span class="log-req mono">{ev.step.method} {ev.step.path}</span>
											{#if ev.step.skipped}
												<span class="dim">skipped</span>
											{:else}
												<span class="mono dim">{ev.step.status}</span>
												<span class="dim num">{ev.step.duration_ms}ms</span>
											{/if}
											{#if ev.step.failures && ev.step.failures.length > 0}
												<div class="log-failures">
													{#each ev.step.failures as f}<div class="mono">{f}</div>{/each}
												</div>
											{/if}
											{#if ev.step.captured && Object.keys(ev.step.captured).length > 0}
												<div class="log-captured">
													{#each Object.entries(ev.step.captured) as [k, v] (k)}
														<span class="mono">{'{{' + k + '}}'} = {v}</span>
													{/each}
												</div>
											{/if}
											{#if !ev.step.skipped && ev.step.body_sample}
												<details class="log-resp" open={!ev.step.pass}>
													<summary class="dim">response</summary>
													<pre class="mono resp-pre">{fmtResponse(ev.step.body_sample)}</pre>
												</details>
											{/if}
										</div>
									{:else if ev.kind === 'flow_done' && ev.summary}
										<div class="log-flowdone" class:fail={!ev.summary.pass}>
											{ev.summary.pass ? '✓' : '✗'} {ev.flowName}: {Number(ev.summary.steps_total) - Number(ev.summary.steps_failed)}/{ev.summary.steps_total} steps
										</div>
									{:else if ev.kind === 'error' && ev.summary}
										<div class="log-step fail mono">{ev.summary.error}</div>
									{/if}
								{/each}
							</div>
							{#if flowTests.runDone}
								<div class="banner" class:ok={doneOk} class:err={!doneOk}>
									{doneOk ? 'All green' : 'Regression detected'} —
									{Number(flowTests.runDone.flows_total) - Number(flowTests.runDone.flows_failed)}/{flowTests.runDone.flows_total} flows,
									{Number(flowTests.runDone.steps_total) - Number(flowTests.runDone.steps_failed)}/{flowTests.runDone.steps_total} steps
									· recorded as run #{flowTests.runDone.run_id} against schema v{flowTests.runDone.schema_version}
								</div>
							{/if}
							<div class="m-actions">
								<button class="btn subtle" onclick={() => (flowTests.view = 'list')} disabled={flowTests.running}>Back</button>
								<button class="btn" onclick={() => flowTests.runSuite()} disabled={flowTests.running || flowTests.flows.length === 0}>Run again</button>
							</div>
						</div>

					{:else if flowTests.view === 'history'}
						<div class="table-wrap">
							{#if flowTests.runs.length === 0 && !flowTests.busy}
								<div class="empty">
									<div class="empty-title">No runs recorded</div>
									<div class="empty-sub">Run a flow (or the suite) — every run lands here with its verdict, anchored to the schema version.</div>
								</div>
							{:else}
								<table class="ftable">
									<thead>
										<tr><th>Run</th><th>When</th><th>Scope</th><th>Schema</th><th>Verdict</th><th class="c-num">Flows</th><th class="c-num">Steps</th></tr>
									</thead>
									<tbody>
										{#each flowTests.runs as r (r.id)}
											<tr>
												<td class="num">#{r.id}</td>
												<td class="dim num">{fmtDate(r.created_at)}</td>
												<td class="dim">{r.scope}</td>
												<td class="num">v{r.schema_version}</td>
												<td>{#if r.pass}<span class="badge pass">PASS</span>{:else}<span class="badge failb">FAIL</span>{/if}</td>
												<td class="c-num num">{r.flows_total - r.flows_failed}/{r.flows_total}</td>
												<td class="c-num num">{r.steps_total - r.steps_failed}/{r.steps_total}</td>
											</tr>
										{/each}
									</tbody>
								</table>
							{/if}
						</div>
						<div class="m-foot">
							<span class="count">{flowTests.runs.length} {flowTests.runs.length === 1 ? 'run' : 'runs'}</span>
							<div class="spacer"></div>
							<span class="hint">The regression trail: deploy a version → run the suite → the verdict is anchored to it.</span>
						</div>
					{/if}
				{/if}
			</div>
		</div>

		{#if flowTests.confirmTarget}
			<div class="confirm" role="alertdialog" aria-modal="true" aria-label="Confirm delete">
				<div class="confirm-box">
					<div class="confirm-title">Delete flow?</div>
					<div class="confirm-msg">
						<span class="mono">{flowTests.confirmTarget.name}</span> will be removed from tenant
						<b>{flowTests.tenantId}</b>. Its past runs stay in the history.
					</div>
					<div class="confirm-actions">
						<button class="btn subtle" onclick={() => flowTests.cancelDelete()} disabled={flowTests.busy}>Cancel</button>
						<button class="btn danger" onclick={() => flowTests.confirmDelete()} disabled={flowTests.busy}>
							{flowTests.busy ? 'Deleting…' : 'Delete'}
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
		width: min(980px, 95vw);
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
	.btn.active {
		background: var(--surface-2);
	}
	.m-body {
		display: flex;
		flex-direction: column;
		padding: 14px 16px;
		gap: 10px;
		overflow: auto;
		min-height: 300px;
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
		min-width: 150px;
		font-family: var(--mono);
		font-size: 12.5px;
	}
	.spacer {
		flex: 1;
	}
	.hint,
	.hint-sm {
		font-size: 12px;
		color: var(--text-3);
	}
	.hint {
		text-align: right;
	}
	.hint-sm {
		margin: 0;
	}

	.table-wrap {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		overflow: auto;
		min-height: 200px;
		max-height: 52vh;
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
		max-width: 280px;
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
	.c-act {
		text-align: right;
	}
	.danger-link {
		color: var(--danger);
	}
	.mono {
		font-family: var(--mono);
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
		max-width: 420px;
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

	/* editor */
	.edit-wrap {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.frow {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.fcol {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.grow {
		flex: 1;
		min-width: 0;
	}
	.login-sel {
		width: auto;
		max-width: 280px;
		font-size: 12px;
	}

	/* live run */
	.run-wrap {
		display: flex;
		flex-direction: column;
		gap: 10px;
	}
	.run-head {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	.run-title {
		font-size: 13px;
	}
	.badge {
		font-size: 10px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		padding: 2px 8px;
		border-radius: 999px;
		border: 1px solid var(--border);
	}
	.badge.pass {
		color: var(--ok, #15803d);
		border-color: color-mix(in srgb, var(--ok, #15803d) 40%, transparent);
		background: color-mix(in srgb, var(--ok, #15803d) 9%, transparent);
	}
	.badge.failb {
		color: var(--danger);
		border-color: color-mix(in srgb, var(--danger) 40%, transparent);
		background: color-mix(in srgb, var(--danger) 9%, transparent);
	}
	.badge.live {
		color: var(--brand);
		border-color: color-mix(in srgb, var(--brand) 40%, transparent);
		background: color-mix(in srgb, var(--brand) 9%, transparent);
	}
	.run-log {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 8px 10px;
		max-height: 46vh;
		overflow: auto;
		display: flex;
		flex-direction: column;
		gap: 3px;
		font-size: 12.5px;
	}
	.log-flow {
		font-weight: 600;
		color: var(--text-2);
		margin-top: 4px;
	}
	.log-step {
		display: flex;
		align-items: baseline;
		gap: 8px;
		flex-wrap: wrap;
		padding: 2px 4px;
		border-radius: 4px;
	}
	.log-step.fail {
		background: color-mix(in srgb, var(--danger) 7%, transparent);
	}
	.log-step.skip {
		opacity: 0.55;
	}
	.log-badge {
		width: 14px;
		text-align: center;
		font-weight: 700;
	}
	.log-step.fail .log-badge {
		color: var(--danger);
	}
	.log-step:not(.fail):not(.skip) .log-badge {
		color: var(--ok, #15803d);
	}
	.log-name {
		font-weight: 500;
	}
	.log-req {
		font-size: 11.5px;
		color: var(--text-3);
	}
	.log-failures {
		flex-basis: 100%;
		padding-left: 22px;
		font-size: 11.5px;
		color: var(--danger);
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.log-captured {
		flex-basis: 100%;
		padding-left: 22px;
		font-size: 11px;
		color: var(--brand);
		display: flex;
		gap: 12px;
		flex-wrap: wrap;
	}
	.log-resp {
		flex-basis: 100%;
		padding-left: 22px;
		font-size: 11px;
	}
	.log-resp summary {
		cursor: pointer;
		user-select: none;
	}
	.resp-pre {
		margin: 3px 0 4px;
		padding: 7px 9px;
		background: var(--surface-2);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		max-height: 220px;
		overflow: auto;
		white-space: pre-wrap;
		word-break: break-word;
		font-size: 11px;
		line-height: 1.5;
	}
	.log-flowdone {
		font-size: 12px;
		color: var(--ok, #15803d);
		padding: 2px 4px;
	}
	.log-flowdone.fail {
		color: var(--danger);
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
		width: min(440px, 90vw);
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
	.confirm-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
</style>
