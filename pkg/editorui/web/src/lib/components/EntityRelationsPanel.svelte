<script lang="ts">
	// Relations (?include= embeds) — the authorable face of the engine's relations
	// block (UI-F4-S3, RELATIONS-V1/ADR-019). Faithful to validateRelations: kind
	// decides which fields apply and WHICH TABLE each FK column belongs to
	// (has_many → target, belongs_to → self, many_to_many → the through junction);
	// every choice is a dropdown over EXISTING entities/columns, so the editor
	// cannot author a relation the engine would reject or the migration would
	// warn about.
	import { editor } from '../stores/editor.svelte';
	import { RELATION_TYPES } from '../types/schema';
	import type { RelationType } from '../types/schema';
	import type { EntityModel, RelationModel } from '../types/editor';

	let { entity }: { entity: EntityModel } = $props();

	let nameErrors = $state<Record<string, string | null>>({});

	function rename(rel: RelationModel, value: string) {
		nameErrors[rel.id] = editor.renameRelation(entity.id, rel.id, value);
	}
	function setLimit(rel: RelationModel, raw: string) {
		const v = raw.trim() === '' ? undefined : Number(raw);
		editor.patchRelation(
			entity.id,
			rel.id,
			'limit',
			v === undefined || Number.isNaN(v) || v <= 0 ? undefined : Math.floor(v)
		);
	}

	/** Human summary of what the embed serves — mirrors the engine's resolution. */
	function summary(rel: RelationModel): string {
		const d = rel.def;
		const lim = d.limit ? ` (limit ${d.limit})` : '';
		switch (d.type) {
			case 'has_many':
				return `?include=${rel.name} → ${d.target || '…'} rows where ${d.target || '…'}.${d.fk || '…'} = ${entity.name}.id${lim}`;
			case 'belongs_to':
				return `?include=${rel.name} → the ${d.target || '…'} row ${entity.name}.${d.fk || '…'} points at`;
			case 'many_to_many':
				return `?include=${rel.name} → ${d.target || '…'} via ${d.through || '…'} (${d.fk || '…'} ↔ ${d.target_fk || '…'})${lim}`;
		}
	}
</script>

<!-- ── RELATIONS (?include= embeds) ──────────────────────────────────────── -->
<section class="p-sec">
	<div class="sec-title">
		Relations <span class="count tnum">{entity.relations.length}</span>
		<button class="btn subtle add" onclick={() => editor.addRelation(entity.id)}>+ add</button>
	</div>
	<div class="hint muted">
		Nested embeds served on <span class="mono">?include=</span> (REST + GraphQL, one round-trip,
		RBAC-scoped). A plain field FK is the field's “Foreign key”; this declares the read shape.
	</div>
	{#if entity.relations.length === 0}
		<div class="muted pad">no declared relations</div>
	{/if}
	{#each entity.relations as rel (rel.id)}
		{@const issues = editor.relationIssuesFor(entity, rel)}
		<div class="card">
			<div class="card-head">
				<input
					class="field-input name-input mono"
					value={rel.name}
					spellcheck="false"
					aria-label="relation name"
					onchange={(e) => rename(rel, e.currentTarget.value)}
				/>
				<button class="btn subtle xs del" onclick={() => editor.removeRelation(entity.id, rel.id)} aria-label="remove relation">✕</button>
			</div>
			{#if nameErrors[rel.id]}<div class="err">{nameErrors[rel.id]}</div>{/if}

			<div class="grid2">
				<div>
					<div class="lbl">kind</div>
					<select
						class="field-select"
						value={rel.def.type}
						onchange={(e) => editor.patchRelation(entity.id, rel.id, 'type', e.currentTarget.value as RelationType)}
					>
						{#each RELATION_TYPES as t}<option value={t}>{t}</option>{/each}
					</select>
				</div>
				<div>
					<div class="lbl">target</div>
					<select
						class="field-select"
						value={rel.def.target}
						onchange={(e) => editor.patchRelation(entity.id, rel.id, 'target', e.currentTarget.value)}
					>
						<option value="">— choose —</option>
						{#each editor.entityNames as n}<option value={n}>{n}</option>{/each}
					</select>
				</div>
			</div>

			{#if rel.def.type === 'many_to_many'}
				<div class="lbl">through <span class="muted">(junction resource)</span></div>
				<select
					class="field-select"
					value={rel.def.through ?? ''}
					onchange={(e) => editor.patchRelation(entity.id, rel.id, 'through', e.currentTarget.value || undefined)}
				>
					<option value="">— choose —</option>
					{#each editor.entityNames.filter((n) => n !== entity.name) as n}<option value={n}>{n}</option>{/each}
				</select>
				<div class="grid2">
					<div>
						<div class="lbl">fk <span class="muted">({rel.def.through || 'junction'} → {entity.name})</span></div>
						<select
							class="field-select"
							value={rel.def.fk}
							disabled={!rel.def.through}
							onchange={(e) => editor.patchRelation(entity.id, rel.id, 'fk', e.currentTarget.value)}
						>
							<option value="">— choose —</option>
							{#each editor.relationFKColumns(entity, rel.def, 'fk') as c}<option value={c}>{c}</option>{/each}
						</select>
					</div>
					<div>
						<div class="lbl">target_fk <span class="muted">({rel.def.through || 'junction'} → {rel.def.target || 'target'})</span></div>
						<select
							class="field-select"
							value={rel.def.target_fk ?? ''}
							disabled={!rel.def.through}
							onchange={(e) => editor.patchRelation(entity.id, rel.id, 'target_fk', e.currentTarget.value || undefined)}
						>
							<option value="">— choose —</option>
							{#each editor.relationFKColumns(entity, rel.def, 'target_fk') as c}<option value={c}>{c}</option>{/each}
						</select>
					</div>
				</div>
			{:else}
				<div class="lbl">
					fk
					<span class="muted">
						{rel.def.type === 'has_many'
							? `(column on ${rel.def.target || 'the target'} pointing here)`
							: `(own column pointing at ${rel.def.target || 'the target'})`}
					</span>
				</div>
				<select
					class="field-select"
					value={rel.def.fk}
					disabled={rel.def.type === 'has_many' && !rel.def.target}
					onchange={(e) => editor.patchRelation(entity.id, rel.id, 'fk', e.currentTarget.value)}
				>
					<option value="">— choose —</option>
					{#each editor.relationFKColumns(entity, rel.def, 'fk') as c}<option value={c}>{c}</option>{/each}
				</select>
			{/if}

			{#if rel.def.type !== 'belongs_to'}
				<div class="lbl">limit <span class="muted">(children per parent; empty → 50)</span></div>
				<input
					class="field-input"
					type="number"
					min="1"
					value={rel.def.limit ?? ''}
					placeholder="50"
					onchange={(e) => setLimit(rel, e.currentTarget.value)}
				/>
			{/if}

			{#if issues.length > 0}
				<div class="err">{issues[0]}</div>
			{:else}
				<div class="summary mono">{summary(rel)}</div>
			{/if}
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
	.name-input {
		flex: 1;
		font-size: 12px;
	}
	.del {
		color: var(--danger);
	}
	.summary {
		font-size: 11px;
		color: var(--text-2);
		line-height: 1.4;
		word-break: break-all;
	}
	.err {
		color: var(--danger);
		font-size: 11.5px;
	}
	.btn.xs {
		padding: 2px 6px;
		font-size: 12px;
	}
</style>
