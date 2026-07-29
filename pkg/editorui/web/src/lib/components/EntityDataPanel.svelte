<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { REFERENTIAL_ACTIONS } from '../types/schema';
	import type { EntityModel } from '../types/editor';

	let { entity }: { entity: EntityModel } = $props();

	const fks = $derived(entity.extras.foreign_keys ?? []);
	const indexes = $derived(entity.extras.indexes ?? []);
	const fieldNames = $derived(entity.fields.map((f) => f.name));
	// gin is only valid over jsonb columns (schema.validateIndexes) — the panel
	// mirrors that so the editor can only author schemas the validator accepts.
	const jsonbFields = $derived(entity.fields.filter((f) => f.def.type === 'jsonb').map((f) => f.name));
	const sourceCols = $derived(['id', ...fieldNames]);

	function refCols(target: string): string[] {
		const t = editor.getEntityByName(target);
		return ['id', ...(t ? t.fields.map((f) => f.name) : [])];
	}
	function pairCount(fk: { columns: string[] }): number {
		return fk.columns.length;
	}
</script>

<!-- ── INDEXES ────────────────────────────────────────────────────────────── -->
<section class="p-sec">
	<div class="sec-title">
		Indexes <span class="count tnum">{indexes.length}</span>
		<button class="btn subtle add" onclick={() => editor.addIndex(entity.id)}>+ add</button>
	</div>
	{#if indexes.length === 0}
		<div class="muted pad">no declared indexes</div>
	{/if}
	{#each indexes as idx, i (i)}
		<div class="card">
			<div class="card-head">
				<span class="card-title">idx #{i + 1}</span>
				<label class="chk"
					><input
						type="checkbox"
						checked={!!idx.unique}
						onchange={(e) => editor.setIndexUnique(entity.id, i, e.currentTarget.checked)}
					/> unique</label
				>
				<button class="btn subtle xs del" onclick={() => editor.removeIndex(entity.id, i)} aria-label="remove index">✕</button>
			</div>
			<div class="chips">
				{#each fieldNames as fn}
					<button
						class="chip"
						class:on={idx.fields.includes(fn)}
						onclick={() => editor.toggleIndexField(entity.id, i, fn, !idx.fields.includes(fn))}
					>
						{fn}
					</button>
				{/each}
				{#if fieldNames.length === 0}<span class="muted">add fields first</span>{/if}
			</div>
			<div class="grid2">
				<div>
					<div class="lbl">method</div>
					<select
						class="field-select"
						value={idx.method ?? ''}
						onchange={(e) => editor.setIndexMethod(entity.id, i, e.currentTarget.value)}
					>
						<option value="">btree (default)</option>
						<option value="gin">gin (jsonb containment)</option>
					</select>
				</div>
				{#if idx.method === 'gin'}
					<div>
						<div class="lbl">opclass</div>
						<select
							class="field-select"
							value={idx.opclass ?? ''}
							onchange={(e) => editor.setIndexOpclass(entity.id, i, e.currentTarget.value)}
						>
							<option value="">jsonb_ops (default)</option>
							<option value="jsonb_path_ops">jsonb_path_ops (@> only)</option>
						</select>
					</div>
				{/if}
			</div>
			{#if idx.method === 'gin' && idx.fields.some((f) => !jsonbFields.includes(f))}
				<div class="err">gin requires every column to be jsonb</div>
			{/if}
			{#if idx.fields.length > 0}
				<div class="summary mono">
					{idx.unique ? 'UNIQUE ' : ''}{idx.method === 'gin' ? 'USING gin ' : ''}({idx.fields.join(', ')}{idx.opclass
						? ' ' + idx.opclass
						: ''})
				</div>
			{:else}
				<div class="err">choose at least one column</div>
			{/if}
		</div>
	{/each}
</section>

<!-- ── COMPOSITE FOREIGN KEYS ─────────────────────────────────────────────── -->
<section class="p-sec">
	<div class="sec-title">
		Composite FKs <span class="count tnum">{fks.length}</span>
		<button class="btn subtle add" onclick={() => editor.addForeignKey(entity.id)}>+ add</button>
	</div>
	<div class="hint muted">Multi-column foreign keys. A single-column FK is a field's “Foreign key”.</div>
	{#each fks as fk, i (i)}
		<div class="card">
			<div class="card-head">
				<span class="card-title">FK #{i + 1}</span>
				<button class="btn subtle xs del" onclick={() => editor.removeForeignKey(entity.id, i)} aria-label="remove fk">✕</button>
			</div>

			<div class="lbl">target</div>
			<select class="field-select" value={fk.target} onchange={(e) => editor.setFKTarget(entity.id, i, e.currentTarget.value)}>
				<option value="">— choose —</option>
				{#each editor.entityNames as n}<option value={n}>{n}</option>{/each}
			</select>

			<div class="lbl">column pairs <span class="muted">(source ▸ {fk.target || "target"})</span></div>
			{#each fk.columns as _, p (p)}
				<div class="pair">
					<select class="field-select" value={fk.columns[p]} onchange={(e) => editor.setFKPair(entity.id, i, p, 'source', e.currentTarget.value)}>
						<option value="">— source —</option>
						{#each sourceCols as c}<option value={c}>{c}</option>{/each}
					</select>
					<span class="arrow">▸</span>
					<select class="field-select" value={fk.ref_columns[p]} disabled={!fk.target} onchange={(e) => editor.setFKPair(entity.id, i, p, 'ref', e.currentTarget.value)}>
						<option value="">— ref —</option>
						{#each refCols(fk.target) as c}<option value={c}>{c}</option>{/each}
					</select>
					<button class="btn subtle xs del" disabled={pairCount(fk) <= 1} onclick={() => editor.removeFKPair(entity.id, i, p)} aria-label="remove pair">✕</button>
				</div>
			{/each}
			<button class="btn subtle xs addpair" onclick={() => editor.addFKPair(entity.id, i)}>+ column pair</button>

			<div class="grid2">
				<div>
					<div class="lbl">on_delete</div>
					<select class="field-select" value={fk.on_delete ?? 'restrict'} onchange={(e) => editor.setFKAction(entity.id, i, 'on_delete', e.currentTarget.value)}>
						{#each REFERENTIAL_ACTIONS as a}<option value={a}>{a}</option>{/each}
					</select>
				</div>
				<div>
					<div class="lbl">on_update</div>
					<select class="field-select" value={fk.on_update ?? ''} onchange={(e) => editor.setFKAction(entity.id, i, 'on_update', e.currentTarget.value)}>
						<option value="">no action</option>
						{#each REFERENTIAL_ACTIONS as a}<option value={a}>{a}</option>{/each}
					</select>
				</div>
			</div>
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
	.sec-title .add {
		margin-left: auto;
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
		line-height: 1.4;
	}
	.pad {
		padding: 4px 0;
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
		gap: 10px;
	}
	.card-title {
		font-size: 11px;
		font-weight: 700;
		color: var(--text-2);
	}
	.card-head .chk {
		margin-left: auto;
	}
	.chk {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		font-size: 12px;
		color: var(--text);
	}
	.del {
		color: var(--danger);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: 4px;
	}
	.chip {
		font-family: var(--mono);
		font-size: 11.5px;
		padding: 2px 8px;
		border: 1px solid var(--border-strong);
		border-radius: 12px;
		background: var(--surface);
		color: var(--text-2);
	}
	.chip.on {
		background: var(--brand);
		border-color: var(--brand);
		color: #fff;
	}
	.summary {
		font-size: 11.5px;
		color: var(--text-2);
	}
	.err {
		color: var(--danger);
		font-size: 11.5px;
	}
	.pair {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.pair .field-select {
		flex: 1;
	}
	.arrow {
		color: var(--text-3);
	}
	.addpair {
		align-self: flex-start;
		color: var(--brand);
		padding: 2px 8px;
	}
	.btn.xs {
		padding: 2px 6px;
		font-size: 12px;
	}
</style>
