<script lang="ts">
	// One flow step's assisted editor (FLOWTEST-POWER-S1): endpoint picker →
	// schema-built body (required marked, valid examples), the full assertion
	// vocabulary as controls with inline help, schema-fed dot-path dropdowns for
	// asserts + captures, GraphQL steps assisted from the derived surface, the
	// OpenAPI doc contextual to the endpoint, and a pre-run lint. Controls that
	// guide — no node canvas.
	import { flowTests, type EditStep } from '../stores/flowtests.svelte';
	import {
		detectEndpoint,
		buildBody,
		fieldHints,
		lintStep,
		ASSERT_OPS,
		assertOpInfo,
		responsePaths,
		captureSuggestions,
		gqlOperations,
		buildGqlQuery,
		detectGqlOperation,
		endpointDoc,
		type EndpointOption
	} from '../flows/assist';
	import type { FlowAssertOp } from '../api/admin';

	let { si }: { si: number } = $props();

	const st = $derived(flowTests.draft.steps[si] as EditStep | undefined);
	const schema = $derived(flowTests.assistSchema);
	const detected = $derived(st ? detectEndpoint(st.method, st.path, schema) : { kind: 'other' as const });
	const resource = $derived(detected.resource ? schema?.resources?.[detected.resource] : undefined);
	const isGql = $derived(st?.path.startsWith('/graphql') ?? false);
	const gqlOps = $derived(gqlOperations(schema));
	const gqlOp = $derived(isGql && st ? detectGqlOperation(st.gqlText ?? '', gqlOps) : null);
	const hasBody = $derived(!!st && !st.upload && !isGql && st.method !== 'GET' && st.method !== 'DELETE');
	const lint = $derived(st && !isGql && !st.upload ? lintStep(st.method, st.path, st.body ?? '', schema) : []);
	const pathOptions = $derived(responsePaths(detected.kind, detected.resource, schema, gqlOp));
	const capSuggestions = $derived(captureSuggestions(detected.kind, detected.resource));
	const doc = $derived(st ? endpointDoc(flowTests.openapi, st.method, st.path) : null);
	const hints = $derived(resource ? fieldHints(resource) : []);

	let showHints = $state(false);
	let gqlWithRelations = $state(false);

	function pickEndpoint(e: Event) {
		const id = (e.currentTarget as HTMLSelectElement).value;
		const ep = flowTests.endpoints.find((o) => o.id === id);
		if (ep) flowTests.applyEndpoint(si, ep);
		(e.currentTarget as HTMLSelectElement).value = '';
	}

	function fillBody(requiredOnly: boolean) {
		if (st && resource) st.body = buildBody(resource, requiredOnly);
	}

	function insertGqlOp(e: Event) {
		const id = (e.currentTarget as HTMLSelectElement).value;
		const op = gqlOps.find((o) => o.id === id);
		if (op && st) st.gqlText = buildGqlQuery(op, schema, gqlWithRelations);
		(e.currentTarget as HTMLSelectElement).value = '';
	}

	function addAssert() {
		st?.expect.asserts!.push({ path: '', op: 'exists', value: '' });
	}
	function removeAssert(ai: number) {
		st?.expect.asserts!.splice(ai, 1);
	}
	function addCapture(k = '', v = '') {
		st?.captureRows.push({ k, v });
	}
	function removeCapture(ci: number) {
		st?.captureRows.splice(ci, 1);
	}
	function hasCapture(k: string, v: string): boolean {
		return !!st?.captureRows.some((r) => r.k === k && r.v === v);
	}

	function toggleUpload() {
		if (!st) return;
		if (st.upload) st.upload = undefined;
		else {
			st.upload = { filename: 'formula.pdf', content: '%PDF-1.4\nflow-test file {{run_id}}' };
			st.method = 'POST';
			if (!st.path.startsWith('/api/files')) st.path = '/api/files';
		}
	}

	function opNeedsValue(op: FlowAssertOp): boolean {
		return assertOpInfo(op)?.needsValue ?? true;
	}

	const groups = $derived.by(() => {
		const map = new Map<string, EndpointOption[]>();
		for (const ep of flowTests.endpoints) {
			const arr = map.get(ep.group) ?? [];
			arr.push(ep);
			map.set(ep.group, arr);
		}
		return [...map.entries()];
	});
</script>

{#if st}
	<div class="step">
		<div class="step-head">
			<span class="step-n">step {si + 1}</span>
			<input class="field-input step-name" placeholder="name (e.g. login optometra)" bind:value={st.name} />
			<button class="btn subtle xs" title="move up" onclick={() => flowTests.moveStep(si, -1)} disabled={si === 0}>↑</button>
			<button class="btn subtle xs" title="move down" onclick={() => flowTests.moveStep(si, 1)} disabled={si === flowTests.draft.steps.length - 1}>↓</button>
			<button class="btn subtle xs danger-link" onclick={() => flowTests.removeStep(si)} disabled={flowTests.draft.steps.length === 1}>remove</button>
		</div>

		<div class="frow">
			<select class="field-input ep-sel" onchange={pickEndpoint} title="Pick an endpoint — method, path, expected status and an example body are pre-filled from the schema">
				<option value="">endpoint…</option>
				{#each groups as [g, eps] (g)}
					<optgroup label={g}>
						{#each eps as ep (ep.id)}<option value={ep.id}>{ep.label}</option>{/each}
					</optgroup>
				{/each}
			</select>
			<select class="field-input method-sel" bind:value={st.method}>
				{#each ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as m}<option>{m}</option>{/each}
			</select>
			<input class="field-input grow mono" list="fl-paths" placeholder="/api/citas o /api/citas/{'{{cita_id}}'}" bind:value={st.path} />
			<label class="chk"><input type="checkbox" checked={!!st.upload} onchange={toggleUpload} /> file upload</label>
		</div>

		{#if doc}
			<details class="doc">
				<summary>
					<span class="tag doc-tag">doc</span>
					<span class="doc-sum">{doc.summary}</span>
					<span class="dim">· auth: {doc.auth}</span>
				</summary>
				<div class="doc-body">
					{#if doc.description}<p class="doc-desc">{doc.description}</p>{/if}
					{#if doc.bodyFields.length > 0}
						<div class="doc-sec">expects {doc.bodyRequired ? '(body required)' : '(body optional)'}:</div>
						<ul class="doc-fields">
							{#each doc.bodyFields as f (f.name)}
								<li><span class="mono">{f.name}</span> <span class="dim">{f.type}</span>{#if f.required}<span class="req">required</span>{/if}{#if f.hint}<span class="dim"> — {f.hint}</span>{/if}</li>
							{/each}
						</ul>
					{/if}
					{#if doc.queryParams.length > 0}
						<div class="doc-sec">query params: <span class="mono dim">{doc.queryParams.slice(0, 12).join(', ')}{doc.queryParams.length > 12 ? '…' : ''}</span></div>
					{/if}
					<div class="doc-sec">returns:
						{#each doc.responses as r (r.code)}
							<span class="code-badge" class:ok={r.code.startsWith('2')}>{r.code}</span><span class="dim"> {r.desc} </span>
						{/each}
					</div>
				</div>
			</details>
		{/if}

		{#if st.upload}
			<div class="frow">
				<input class="field-input mono" placeholder="filename.pdf" bind:value={st.upload.filename} />
				<input class="field-input grow mono" placeholder="inline content" bind:value={st.upload.content} />
			</div>
			<p class="assist-hint">Uploads answer 201 with <span class="mono">file_id</span> — capture it to attach the file to a record's <span class="mono">file</span> field.</p>
		{:else if isGql}
			<div class="frow">
				<select class="field-input ep-sel" onchange={insertGqlOp} title="Insert a query/mutation template — the surface is derived from the schema (same naming the engine generates)">
					<option value="">insert operation…</option>
					{#each gqlOps as op (op.id)}<option value={op.id}>{op.label}</option>{/each}
				</select>
				<label class="chk" title="Include the resource's declared relations as nested selections (served in one query)"><input type="checkbox" bind:checked={gqlWithRelations} /> nest relations</label>
			</div>
			<textarea class="field-input mono body-in" rows="5" placeholder={'{ pacientes { data { id nombre } } }'} bind:value={st.gqlText}></textarea>
			<p class="assist-hint">GraphQL always answers HTTP 200 — expect 200 and assert <span class="mono">errors</span> <b>not exists</b> for success; results live under <span class="mono">data.&lt;operation&gt;</span>.</p>
		{:else if hasBody}
			<div class="frow">
				<span class="lbl inline">body</span>
				<div class="spacer"></div>
				{#if resource}
					<button class="btn subtle xs" title="Generate a complete valid example from the schema (required fields, enums, formats, unique values salted with {'{{run_tag}}'} so re-runs never collide)" onclick={() => fillBody(false)}>fill example</button>
					<button class="btn subtle xs" title="The minimal valid body: required fields without a default" onclick={() => fillBody(true)}>required only</button>
					<button class="btn subtle xs" class:active={showHints} onclick={() => (showHints = !showHints)}>{showHints ? 'hide' : 'show'} fields</button>
				{/if}
			</div>
			<textarea class="field-input mono body-in" rows="4" placeholder={'{"motivo":"control-{{run_id}}"}'} bind:value={st.body}></textarea>
			{#if lint.length > 0}
				<div class="lint">
					{#each lint as w, i (i)}<div class="lint-row">⚠ {w}</div>{/each}
				</div>
			{/if}
			{#if showHints && hints.length > 0}
				<div class="hints">
					{#each hints as h (h.name)}
						<div class="hint-row">
							<span class="mono hint-name" class:required={h.required}>{h.name}</span>
							<span class="hint-type">{h.type}</span>
							{#if h.required}<span class="req">required</span>{/if}
							{#if h.badges.length > 0}<span class="dim hint-badges">{h.badges.join(' · ')}</span>{/if}
						</div>
					{/each}
				</div>
			{/if}
		{/if}

		<div class="frow">
			<label class="lbl inline" for={'fl-status-' + si}>expect status</label>
			<input id={'fl-status-' + si} class="field-input status-in num" type="number" bind:value={st.expect.status} />
			<div class="spacer"></div>
			<button class="btn subtle xs" onclick={addAssert}>+ assert</button>
			<button class="btn subtle xs" onclick={() => addCapture()}>+ capture</button>
		</div>

		{#each st.expect.asserts ?? [] as a, ai (ai)}
			<div class="frow sub">
				<span class="tag">assert</span>
				<input class="field-input mono grow" list={'fl-rp-' + si} placeholder="response path (e.g. data.0.nombre or id)" bind:value={a.path} />
				<select class="field-input op-sel" bind:value={a.op} title={assertOpInfo(a.op)?.help}>
					{#each ASSERT_OPS as o (o.op)}<option value={o.op}>{o.label}</option>{/each}
				</select>
				{#if opNeedsValue(a.op)}
					<input class="field-input mono grow" placeholder="value (vars ok: {'{{cita_id}}'})" bind:value={a.value} />
				{/if}
				<button class="btn subtle xs danger-link" onclick={() => removeAssert(ai)}>✕</button>
			</div>
			{#if assertOpInfo(a.op)}
				<div class="op-help">{assertOpInfo(a.op)?.help}</div>
			{/if}
		{/each}

		{#each st.captureRows as row, ci (ci)}
			<div class="frow sub">
				<span class="tag cap">capture</span>
				<input class="field-input mono" placeholder="variable" bind:value={row.k} />
				<span class="dim">←</span>
				<input class="field-input mono grow" list={'fl-rp-' + si} placeholder="response path (e.g. id, token)" bind:value={row.v} />
				<button class="btn subtle xs danger-link" onclick={() => removeCapture(ci)}>✕</button>
			</div>
		{/each}

		{#if capSuggestions.length > 0}
			<div class="frow sub cap-sug">
				<span class="dim">suggested:</span>
				{#each capSuggestions as s (s.k)}
					{#if !hasCapture(s.k, s.v)}
						<button class="btn subtle xs" title={'Capture the response field "' + s.v + '" into {{' + s.k + '}} for later steps'} onclick={() => addCapture(s.k, s.v)}>
							capture {'{{' + s.k + '}}'} ← {s.v}
						</button>
					{/if}
				{/each}
			</div>
		{/if}

		<datalist id={'fl-rp-' + si}>
			{#each pathOptions as p (p)}<option value={p}></option>{/each}
		</datalist>
	</div>
{/if}

<style>
	.step {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.step-head {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.step-n {
		font-size: 11px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-3);
		white-space: nowrap;
	}
	.step-name {
		flex: 1;
	}
	.frow {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.frow.sub {
		padding-left: 14px;
	}
	.grow {
		flex: 1;
		min-width: 0;
	}
	.spacer {
		flex: 1;
	}
	.mono {
		font-family: var(--mono);
	}
	.dim {
		color: var(--text-3);
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
	.lbl.inline {
		font-size: 12.5px;
		font-weight: 600;
		color: var(--text-2);
	}
	.btn.xs {
		padding: 3px 8px;
		font-size: 11.5px;
	}
	.btn.active {
		background: var(--surface-2);
	}
	.danger-link {
		color: var(--danger);
	}
	.ep-sel {
		width: 240px;
		font-size: 12px;
	}
	.method-sel {
		width: 96px;
		font-family: var(--mono);
	}
	.op-sel {
		width: 120px;
	}
	.status-in {
		width: 80px;
	}
	.body-in {
		resize: vertical;
	}
	.chk {
		display: flex;
		align-items: center;
		gap: 5px;
		font-size: 12px;
		color: var(--text-2);
		white-space: nowrap;
	}
	.tag {
		font-size: 10px;
		font-weight: 600;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		padding: 2px 7px;
		border-radius: 999px;
		background: var(--surface-2);
		border: 1px solid var(--border);
		color: var(--text-3);
		white-space: nowrap;
	}
	.tag.cap {
		color: var(--brand);
	}

	/* the contextual endpoint doc (OpenAPI, in-place) */
	.doc {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface-2);
		font-size: 12px;
	}
	.doc summary {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 6px 10px;
		cursor: pointer;
		list-style: none;
		overflow: hidden;
		white-space: nowrap;
	}
	.doc summary::-webkit-details-marker {
		display: none;
	}
	.doc-tag {
		color: var(--brand);
	}
	.doc-sum {
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.doc-body {
		padding: 4px 12px 10px;
		display: flex;
		flex-direction: column;
		gap: 5px;
		border-top: 1px solid var(--border);
	}
	.doc-desc {
		margin: 4px 0 0;
		color: var(--text-2);
		line-height: 1.5;
	}
	.doc-sec {
		color: var(--text-2);
	}
	.doc-fields {
		margin: 0;
		padding-left: 18px;
		display: flex;
		flex-direction: column;
		gap: 2px;
	}
	.code-badge {
		font-family: var(--mono);
		font-size: 11px;
		padding: 0 5px;
		border-radius: 4px;
		border: 1px solid var(--border);
		color: var(--danger);
		margin-left: 4px;
	}
	.code-badge.ok {
		color: var(--ok, #15803d);
	}
	.req {
		font-size: 10px;
		font-weight: 700;
		text-transform: uppercase;
		color: var(--brand);
		margin-left: 5px;
	}

	/* pre-run lint */
	.lint {
		display: flex;
		flex-direction: column;
		gap: 2px;
		font-size: 12px;
		color: var(--warn, #b45309);
	}
	.lint-row {
		line-height: 1.4;
	}

	/* field hints — what the resource accepts */
	.hints {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 6px 10px;
		display: flex;
		flex-direction: column;
		gap: 3px;
		font-size: 12px;
		max-height: 180px;
		overflow: auto;
	}
	.hint-row {
		display: flex;
		align-items: baseline;
		gap: 8px;
		flex-wrap: wrap;
	}
	.hint-name {
		font-weight: 500;
	}
	.hint-name.required {
		color: var(--brand);
	}
	.hint-type {
		color: var(--text-3);
		font-size: 11px;
	}
	.hint-badges {
		font-size: 11px;
	}

	.assist-hint {
		margin: 0;
		font-size: 11.5px;
		color: var(--text-3);
		line-height: 1.5;
	}
	.op-help {
		padding-left: 68px;
		font-size: 11px;
		color: var(--text-3);
		margin-top: -4px;
	}
	.cap-sug {
		flex-wrap: wrap;
		font-size: 11.5px;
	}
</style>
