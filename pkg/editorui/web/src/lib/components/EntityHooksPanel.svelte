<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { HOOK_EVENTS, type HookEvent } from '../types/schema';
	import type { EntityModel } from '../types/editor';

	let { entity }: { entity: EntityModel } = $props();

	// Present hooks in canonical event order (insertion order is round-trip-safe; this
	// is just for a stable display). Object.entries keeps any imported drift visible too.
	const hooks = $derived(Object.entries(entity.extras.hooks ?? {}));
	const unused = $derived(editor.unusedHookEvents(entity.id));

	let pick = $state<HookEvent | ''>('');

	function add() {
		if (!pick) return;
		editor.addHook(entity.id, pick);
		pick = '';
	}
	// Issues for one hook (reuses the store mirror, scoped by its message prefix).
	function issuesFor(event: string): string[] {
		const p = `entity "${entity.name}" hook "${event}"`;
		return editor.hookIssues().filter((i) => i.startsWith(p));
	}
	// Events selectable for a given hook: all, minus those used by OTHER hooks.
	function eventOptions(current: string): HookEvent[] {
		const usedByOther = new Set(Object.keys(entity.extras.hooks ?? {}).filter((e) => e !== current));
		return HOOK_EVENTS.filter((e) => !usedByOther.has(e));
	}
</script>

<section class="p-sec">
	<div class="sec-title">
		Hooks <span class="count tnum">{hooks.length}</span>
		<div class="add-ctl">
			<select class="mini-sel" bind:value={pick} disabled={unused.length === 0}>
				<option value="">{unused.length ? 'event…' : 'all events used'}</option>
				{#each unused as ev}<option value={ev}>{ev}</option>{/each}
			</select>
			<button class="btn subtle add" disabled={!pick} onclick={add}>+ add</button>
		</div>
	</div>
	<div class="hint muted">
		Lifecycle logic. A <b>before</b> hook can transform/validate the write; an
		<b>after</b> hook must be a <b>webhook</b> (a sandboxed hook can't act post-commit).
	</div>

	{#if hooks.length === 0}
		<div class="muted pad">no hooks</div>
	{/if}

	{#each hooks as [event, cfg] (event)}
		<div class="card">
			<div class="card-head">
				<select
					class="field-select ev"
					value={event}
					onchange={(e) => editor.setHookEvent(entity.id, event, e.currentTarget.value as HookEvent)}
				>
					{#each eventOptions(event) as ev}<option value={ev}>{ev}</option>{/each}
				</select>
				<select
					class="field-select ty"
					value={cfg.type}
					onchange={(e) => editor.setHookType(entity.id, event, e.currentTarget.value as 'js' | 'webhook' | 'wasm')}
				>
					{#each editor.hookTypesFor(event) as t}<option value={t}>{t}</option>{/each}
				</select>
				<button class="btn subtle xs del" onclick={() => editor.removeHook(entity.id, event)} aria-label="remove hook">✕</button>
			</div>

			{#if cfg.type === 'webhook'}
				<div class="lbl">url <span class="muted">(https)</span></div>
				<input
					class="field-input mono"
					placeholder="https://example.com/webhooks/…"
					value={cfg.url ?? ''}
					spellcheck="false"
					onchange={(e) => editor.patchHook(entity.id, event, 'url', e.currentTarget.value)}
				/>
				{#if cfg.url && !cfg.url.startsWith('https://')}
					<div class="warn-note">⚠ The dispatcher is HTTPS-only + SSRF-guarded — an http/LAN URL is never delivered.</div>
				{/if}
				<div class="lbl">hmac_secret_env <span class="muted">(optional — env var NAME)</span></div>
				<input
					class="field-input mono"
					placeholder="WEBHOOK_SECRET_TASKS"
					value={cfg.hmac_secret_env ?? ''}
					spellcheck="false"
					onchange={(e) => editor.patchHook(entity.id, event, 'hmac_secret_env', e.currentTarget.value)}
				/>
				<div class="engine-note">
					The engine signs the POST (<span class="mono">X-Appitools-Signature</span>), sends the
					real event in <span class="mono">X-Appitools-Event</span>, and includes the actor (JWT
					claims). 3 retries with backoff.
				</div>
			{:else if cfg.type === 'js'}
				<div class="lbl">script <span class="muted">(Goja sandbox)</span></div>
				<textarea
					class="code"
					rows="5"
					spellcheck="false"
					placeholder="if (!data.title) {'{'} result.proceed = false; result.error = 'title required'; {'}'}"
					value={cfg.script ?? ''}
					onchange={(e) => editor.patchHook(entity.id, event, 'script', e.currentTarget.value)}
				></textarea>
				<div class="engine-note">
					Pure sandbox (no db/fetch), watchdog-timed. <span class="mono">data</span> is the record,
					<span class="mono">user</span> the actor; set <span class="mono">result.proceed=false</span>
					+ <span class="mono">result.error</span> to reject (422). Built-ins: validateNIT,
					calculateCUFE, isValidEmail, formatMoney.
				</div>
			{:else if cfg.type === 'wasm'}
				<div class="grid2">
					<div>
						<div class="lbl">wasm_module</div>
						<input
							class="field-input mono"
							placeholder="my_module"
							value={cfg.wasm_module ?? ''}
							spellcheck="false"
							onchange={(e) => editor.patchHook(entity.id, event, 'wasm_module', e.currentTarget.value)}
						/>
					</div>
					<div>
						<div class="lbl">wasm_fn <span class="muted">(default transform)</span></div>
						<input
							class="field-input mono"
							placeholder="transform"
							value={cfg.wasm_fn ?? ''}
							spellcheck="false"
							onchange={(e) => editor.patchHook(entity.id, event, 'wasm_fn', e.currentTarget.value)}
						/>
					</div>
				</div>
				<div class="engine-note">A pre-loaded Wazero module (no CGO), 16 MiB limit.</div>
			{/if}

			{#each issuesFor(event) as iss}
				<div class="err">⚠ {iss.replace(`entity "${entity.name}" hook "${event}": `, '')}</div>
			{/each}
		</div>
	{/each}
</section>

<style>
	.p-sec {
		padding: 14px 16px;
		border-bottom: 1px solid var(--border);
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.sec-title {
		display: flex;
		align-items: center;
		gap: 8px;
		font-size: 11px;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-3);
		font-weight: 700;
	}
	.add-ctl {
		margin-left: auto;
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.mini-sel {
		font-size: 11px;
		padding: 2px 4px;
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		background: var(--surface);
		color: var(--text-2);
	}
	.sec-title .add {
		padding: 2px 8px;
		color: var(--brand);
	}
	.count {
		background: var(--surface-2);
		border-radius: 9px;
		padding: 0 7px;
		color: var(--text-2);
	}
	.lbl {
		font-size: 11px;
		font-weight: 600;
		color: var(--text-2);
		margin-top: 2px;
	}
	.muted {
		color: var(--text-3);
		font-weight: 400;
	}
	.hint {
		font-size: 11px;
		line-height: 1.45;
	}
	.pad {
		padding: 4px 0;
	}
	.mono {
		font-family: var(--mono);
	}
	.grid2 {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}
	.card {
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
		background: var(--surface-2);
		padding: 9px 10px;
		display: flex;
		flex-direction: column;
		gap: 6px;
	}
	.card-head {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.card-head .ev {
		flex: 1;
	}
	.card-head .ty {
		width: 96px;
	}
	.del {
		color: var(--danger);
	}
	.btn.xs {
		padding: 2px 6px;
		font-size: 12px;
	}
	.code {
		font-family: var(--mono);
		font-size: 11.5px;
		line-height: 1.5;
		resize: vertical;
		width: 100%;
		box-sizing: border-box;
		padding: 7px 8px;
		border: 1px solid var(--border-strong);
		border-radius: var(--radius-sm);
		background: var(--surface);
		color: var(--text);
	}
	.engine-note {
		font-size: 10.5px;
		line-height: 1.5;
		color: var(--text-3);
		background: var(--surface);
		border-radius: var(--radius-sm);
		padding: 6px 8px;
	}
	.warn-note {
		font-size: 11px;
		color: var(--warn);
		line-height: 1.4;
	}
	.err {
		color: var(--danger);
		font-size: 11.5px;
		line-height: 1.4;
	}
</style>
