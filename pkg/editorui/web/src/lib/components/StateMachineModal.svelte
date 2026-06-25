<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';
	import {
		smKnownStates,
		smInitialList,
		smTargets,
		smIsTerminal,
		stateMachineIssues
	} from '../schema/fieldRules';

	const target = $derived(ui.smTarget);
	const entity = $derived(target ? editor.getEntity(target.entityId) : undefined);
	const field = $derived(
		entity && target ? entity.fields.find((f) => f.id === target.fieldId) : undefined
	);
	const sm = $derived(field?.def.state_machine);

	const states = $derived(sm ? smKnownStates(sm) : []);
	const initial = $derived(sm ? smInitialList(sm) : []);
	const enumVals = $derived(field?.def.enum ?? []);
	const hasEnum = $derived(enumVals.length > 0);
	const addable = $derived(enumVals.filter((v) => !states.includes(v)));
	const issues = $derived(field ? stateMachineIssues(field.def) : []);

	let newState = $state('');
	let pick = $state('');
	let addError = $state<string | null>(null);

	function add(name: string) {
		if (!target) return;
		addError = editor.smAddState(target.entityId, target.fieldId, name);
		if (!addError) {
			newState = '';
			pick = '';
		}
	}
	function remove(state: string) {
		if (target) editor.smRemoveState(target.entityId, target.fieldId, state);
	}
	function toggleInitial(state: string, on: boolean) {
		if (target) editor.smToggleInitial(target.entityId, target.fieldId, state, on);
	}
	function toggleTransition(from: string, to: string, on: boolean) {
		if (target) editor.smToggleTransition(target.entityId, target.fieldId, from, to, on);
	}
	function isTransition(from: string, to: string): boolean {
		return !!sm && smTargets(sm, from).includes(to);
	}
	function close() {
		ui.closeStateMachine();
	}
	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') close();
	}

	// ── diagram layout: rank states by BFS distance from the initial set, lay them
	//    out left→right, draw transitions as arrows (forward straight, back/self bowed). ──
	const NODE_W = 104;
	const NODE_H = 34;
	const COL_GAP = 156;
	const ROW_GAP = 64;
	const TOP = 64;
	const LEFT = 36;

	type LNode = { state: string; x: number; y: number; initial: boolean; terminal: boolean };
	type LEdge = { from: string; to: string; path: string; back: boolean };

	const layout = $derived.by(() => {
		if (!sm || states.length === 0) return { nodes: [] as LNode[], edges: [] as LEdge[], w: 320, h: 120 };
		// BFS ranks from initial states (unreachable → placed after the max rank).
		const rank = new Map<string, number>();
		const q: string[] = [];
		for (const s of initial) {
			if (!rank.has(s)) {
				rank.set(s, 0);
				q.push(s);
			}
		}
		while (q.length) {
			const cur = q.shift()!;
			for (const to of smTargets(sm, cur)) {
				if (!rank.has(to)) {
					rank.set(to, (rank.get(cur) ?? 0) + 1);
					q.push(to);
				}
			}
		}
		let maxRank = 0;
		for (const r of rank.values()) maxRank = Math.max(maxRank, r);
		const unreached = states.filter((s) => !rank.has(s));
		for (const s of unreached) rank.set(s, maxRank + 1);
		// Column buckets in declared order.
		const cols = new Map<number, string[]>();
		for (const s of states) {
			const r = rank.get(s) ?? 0;
			(cols.get(r) ?? cols.set(r, []).get(r)!).push(s);
		}
		const pos = new Map<string, LNode>();
		let maxRows = 0;
		for (const [r, group] of cols) {
			maxRows = Math.max(maxRows, group.length);
			group.forEach((s, i) => {
				pos.set(s, {
					state: s,
					x: LEFT + r * COL_GAP,
					y: TOP + i * ROW_GAP,
					initial: initial.includes(s),
					terminal: smIsTerminal(sm, s)
				});
			});
		}
		const nodes = states.map((s) => pos.get(s)!);
		const edges: LEdge[] = [];
		for (const from of states) {
			const a = pos.get(from)!;
			for (const to of smTargets(sm, from)) {
				const b = pos.get(to);
				if (!b) continue;
				const forward = b.x > a.x;
				let path: string;
				if (forward) {
					const sx = a.x + NODE_W,
						sy = a.y + NODE_H / 2,
						tx = b.x,
						ty = b.y + NODE_H / 2;
					path = `M ${sx} ${sy} C ${sx + 42} ${sy}, ${tx - 42} ${ty}, ${tx} ${ty}`;
				} else {
					// back / same-column / self: bow over the top
					const sx = a.x + NODE_W * 0.65,
						sy = a.y,
						tx = b.x + NODE_W * 0.35,
						ty = b.y,
						lift = 34 + Math.abs(a.y - b.y) * 0.2;
					path = `M ${sx} ${sy} C ${sx} ${sy - lift}, ${tx} ${ty - lift}, ${tx} ${ty}`;
				}
				edges.push({ from, to, path, back: !forward });
			}
		}
		const colCount = maxRank + (unreached.length ? 2 : 1);
		return {
			nodes,
			edges,
			w: Math.max(320, LEFT + colCount * COL_GAP + NODE_W),
			h: Math.max(140, TOP + maxRows * ROW_GAP + 8)
		};
	});
</script>

{#if target && field && sm}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => {
			if (e.target === e.currentTarget) close();
		}}
		onkeydown={onKey}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="State machine designer" tabindex="-1">
			<div class="m-head">
				<div class="m-title">
					<span class="m-logo" aria-hidden="true">⮌</span>
					State machine — <b class="mono">{entity?.name}.{field.name}</b>
				</div>
				<button class="btn subtle xs" onclick={close} aria-label="Close">✕</button>
			</div>

			<div class="m-body">
				<p class="lead">
					The engine forces this lifecycle: a row is created in an <b>initial</b> state and may
					only move along a declared transition. A state with no outgoing arrows is
					<b>terminal</b> (immutable). Enforced race-safely on REST, GraphQL and transactions.
				</p>

				<!-- ── DIAGRAM ── -->
				<div class="diagram-wrap">
					{#if states.length === 0}
						<div class="empty">Add states below to design the machine.</div>
					{:else}
						<svg class="diagram" viewBox={`0 0 ${layout.w} ${layout.h}`} width={layout.w} height={layout.h}>
							<defs>
								<marker id="sm-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
									<path d="M 0 0 L 10 5 L 0 10 z" fill="var(--text-3)" />
								</marker>
							</defs>
							{#each layout.edges as e}
								<path d={e.path} class="edge" class:back={e.back} marker-end="url(#sm-arrow)" />
							{/each}
							{#each layout.nodes as n}
								{#if n.initial}
									<!-- entry marker into an initial state -->
									<circle cx={n.x - 16} cy={n.y + NODE_H / 2} r="3" class="entry-dot" />
									<path d={`M ${n.x - 13} ${n.y + NODE_H / 2} L ${n.x} ${n.y + NODE_H / 2}`} class="edge" marker-end="url(#sm-arrow)" />
								{/if}
								<g class="node" class:terminal={n.terminal} class:is-initial={n.initial}>
									<rect x={n.x} y={n.y} width={NODE_W} height={NODE_H} rx="8" />
									<text x={n.x + NODE_W / 2} y={n.y + NODE_H / 2 + 4} text-anchor="middle">{n.state}</text>
								</g>
							{/each}
						</svg>
					{/if}
				</div>

				<div class="legend">
					<span><span class="lg-swatch init"></span> initial</span>
					<span><span class="lg-swatch term"></span> terminal</span>
					<span><span class="lg-arrow">→</span> allowed transition</span>
				</div>

				<!-- ── STATES ── -->
				<div class="sec">
					<div class="sec-h">States <span class="muted">{states.length}</span></div>
					{#if hasEnum}
						<div class="hint">States are drawn from the field’s <b>enum</b> (engine coherence).</div>
					{/if}
					<div class="state-list">
						{#each states as s (s)}
							<div class="state-row" class:term={smIsTerminal(sm, s)}>
								<label class="init-chk" title="A row may be created in this state">
									<input type="checkbox" checked={initial.includes(s)} onchange={(e) => toggleInitial(s, e.currentTarget.checked)} />
									initial
								</label>
								<span class="st-name mono">{s}</span>
								{#if smIsTerminal(sm, s)}<span class="tag term-tag">terminal</span>{/if}
								<button class="btn subtle xs del" onclick={() => remove(s)} aria-label={`remove ${s}`}>✕</button>
							</div>
						{/each}
						{#if states.length === 0}<div class="muted pad">no states yet</div>{/if}
					</div>

					{#if hasEnum}
						{#if addable.length > 0}
							<div class="add-row">
								<select class="field-select" bind:value={pick}>
									<option value="">— add an enum value as a state —</option>
									{#each addable as v}<option value={v}>{v}</option>{/each}
								</select>
								<button class="btn" disabled={!pick} onclick={() => add(pick)}>Add</button>
							</div>
						{:else}
							<div class="hint">Every enum value is already a state.</div>
						{/if}
					{:else}
						<div class="add-row">
							<input
								class="field-input mono"
								placeholder="new state name"
								bind:value={newState}
								onkeydown={(e) => e.key === 'Enter' && add(newState)}
							/>
							<button class="btn" disabled={!newState.trim()} onclick={() => add(newState)}>Add</button>
						</div>
					{/if}
					{#if addError}<div class="err">{addError}</div>{/if}
				</div>

				<!-- ── TRANSITIONS MATRIX ── -->
				{#if states.length > 0}
					<div class="sec">
						<div class="sec-h">Allowed transitions <span class="muted">from ▸ to</span></div>
						<div class="matrix-scroll">
							<table class="matrix">
								<thead>
									<tr>
										<th class="corner">from ╲ to</th>
										{#each states as to}<th class="mono">{to}</th>{/each}
									</tr>
								</thead>
								<tbody>
									{#each states as from}
										<tr>
											<th class="mono rowh">{from}</th>
											{#each states as to}
												<td class:diag={from === to}>
													{#if from !== to}
														<input
															type="checkbox"
															checked={isTransition(from, to)}
															onchange={(e) => toggleTransition(from, to, e.currentTarget.checked)}
															aria-label={`${from} to ${to}`}
														/>
													{:else}
														<span class="dash">·</span>
													{/if}
												</td>
											{/each}
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					</div>
				{/if}

				<!-- ── ISSUES ── -->
				{#if issues.length > 0}
					<div class="issues" role="alert">
						{#each issues as iss}<div class="issue">⚠ {iss}</div>{/each}
					</div>
				{/if}
			</div>

			<div class="m-foot">
				<button class="btn danger" onclick={() => { if (target) editor.disableStateMachine(target.entityId, target.fieldId); close(); }}>
					Remove state machine
				</button>
				<button class="btn primary" onclick={close}>Done</button>
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
		z-index: 220;
	}
	.modal {
		width: min(720px, 95vw);
		max-height: 90vh;
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
		font-size: 14px;
	}
	.mono {
		font-family: var(--mono);
	}
	.btn.xs {
		padding: 3px 7px;
		font-size: 12px;
	}
	.m-body {
		padding: 14px 16px;
		overflow-y: auto;
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.lead {
		margin: 0;
		font-size: 12.5px;
		color: var(--text-2);
		line-height: 1.5;
	}
	.muted {
		color: var(--text-3);
		font-weight: 400;
	}

	.diagram-wrap {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface-2);
		padding: 8px;
		overflow-x: auto;
	}
	.diagram {
		display: block;
		max-width: 100%;
	}
	.empty {
		padding: 24px;
		text-align: center;
		color: var(--text-3);
		font-size: 12.5px;
	}
	.edge {
		fill: none;
		stroke: var(--text-3);
		stroke-width: 1.5;
	}
	.edge.back {
		stroke-dasharray: 4 3;
	}
	.entry-dot {
		fill: var(--brand);
	}
	.node rect {
		fill: var(--surface);
		stroke: var(--border-strong);
		stroke-width: 1.5;
	}
	.node text {
		font-family: var(--mono);
		font-size: 12px;
		fill: var(--text);
	}
	.node.is-initial rect {
		stroke: var(--brand);
		stroke-width: 2;
	}
	.node.terminal rect {
		fill: color-mix(in srgb, var(--text-3) 14%, var(--surface));
		stroke-dasharray: 3 2;
	}

	.legend {
		display: flex;
		gap: 16px;
		font-size: 11.5px;
		color: var(--text-3);
	}
	.legend span {
		display: inline-flex;
		align-items: center;
		gap: 5px;
	}
	.lg-swatch {
		width: 12px;
		height: 12px;
		border-radius: 3px;
		border: 1.5px solid var(--border-strong);
	}
	.lg-swatch.init {
		border-color: var(--brand);
	}
	.lg-swatch.term {
		background: color-mix(in srgb, var(--text-3) 14%, var(--surface));
		border-style: dashed;
	}
	.lg-arrow {
		color: var(--text-2);
		font-weight: 700;
	}

	.sec {
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.sec-h {
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-3);
	}
	.hint {
		font-size: 11.5px;
		color: var(--text-3);
	}
	.state-list {
		display: flex;
		flex-direction: column;
		gap: 3px;
	}
	.state-row {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 5px 8px;
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface-2);
	}
	.state-row.term {
		border-style: dashed;
	}
	.init-chk {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		font-size: 11px;
		color: var(--text-2);
	}
	.st-name {
		font-size: 13px;
		font-weight: 600;
		flex: 1;
	}
	.tag {
		font-size: 10px;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		padding: 1px 6px;
		border-radius: 9px;
	}
	.term-tag {
		background: color-mix(in srgb, var(--text-3) 16%, transparent);
		color: var(--text-2);
	}
	.del {
		color: var(--danger);
	}
	.add-row {
		display: flex;
		gap: 6px;
	}
	.add-row .field-input,
	.add-row .field-select {
		flex: 1;
	}
	.pad {
		padding: 6px 2px;
	}
	.err {
		color: var(--danger);
		font-size: 11.5px;
	}

	.matrix-scroll {
		overflow-x: auto;
	}
	.matrix {
		border-collapse: collapse;
		font-size: 12px;
	}
	.matrix th,
	.matrix td {
		border: 1px solid var(--border);
		padding: 4px 8px;
		text-align: center;
	}
	.matrix thead th,
	.matrix .rowh {
		background: var(--surface-2);
		font-weight: 600;
		color: var(--text-2);
	}
	.matrix .corner {
		font-size: 10.5px;
		color: var(--text-3);
		text-transform: none;
		letter-spacing: 0;
		font-weight: 600;
	}
	.matrix td.diag {
		background: var(--surface-2);
	}
	.dash {
		color: var(--text-3);
	}

	.issues {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 8px 10px;
		border-radius: var(--radius-sm);
		background: color-mix(in srgb, var(--danger) 8%, transparent);
		border: 1px solid color-mix(in srgb, var(--danger) 35%, transparent);
	}
	.issue {
		font-size: 11.5px;
		color: var(--danger);
		line-height: 1.4;
	}

	.m-foot {
		display: flex;
		justify-content: space-between;
		gap: 8px;
		padding: 12px 16px;
		border-top: 1px solid var(--border);
	}
</style>
