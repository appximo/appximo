<script lang="ts">
	import { Handle, Position, type NodeProps } from '@xyflow/svelte';
	import { editor, TARGET_HANDLE, NEW_SOURCE_HANDLE, type FlowNode } from '../stores/editor.svelte';

	let { id, data }: NodeProps<FlowNode> = $props();

	// Look the entity up from the central store (deep-reactive): editing a field in
	// the panel re-renders just this node, without churning the SvelteFlow node
	// array. This is the fine-grained-reactivity property that keeps the canvas fast.
	const entity = $derived(editor.getEntity(data.entityId));
	const isSelected = $derived(editor.selectedEntityId === id);

	function pickEntity(e: Event) {
		e.stopPropagation();
		editor.selectEntity(id);
	}
	function pickField(e: Event, fieldId: string) {
		e.stopPropagation();
		editor.selectField(id, fieldId);
	}
	function onKey(e: KeyboardEvent, fn: () => void) {
		if (e.key === 'Enter' || e.key === ' ') {
			e.preventDefault();
			e.stopPropagation();
			fn();
		}
	}
</script>

{#if entity}
	<div class="entity-node" class:selected={isSelected}>
		<!-- incoming relations land here (left edge) -->
		<Handle type="target" position={Position.Left} id={TARGET_HANDLE} class="h h-target" />

		<header
			class="en-header"
			onclick={pickEntity}
			onkeydown={(e) => onKey(e, () => editor.selectEntity(id))}
			role="button"
			tabindex="-1"
		>
			<span class="en-icon" aria-hidden="true">▤</span>
			<span class="en-title">{entity.name}</span>
			<span class="en-count tnum">{entity.fields.length}</span>
			<!-- drag from here to create a new relation -->
			<Handle type="source" position={Position.Right} id={NEW_SOURCE_HANDLE} class="h h-new" />
		</header>

		<div class="en-fields">
			{#each entity.fields as f (f.id)}
				<div
					class="en-field"
					class:fk={!!f.def.relation}
					class:sel={editor.selectedFieldId === f.id}
					onclick={(e) => pickField(e, f.id)}
					onkeydown={(e) => onKey(e, () => editor.selectField(id, f.id))}
					role="button"
					tabindex="-1"
				>
					<span class="en-fname" title={f.name}>{f.name}</span>
					<span class="en-flags">
						{#if f.def.required}<span class="badge b-req" title="required">REQ</span>{/if}
						{#if f.def.unique}<span class="badge b-uniq" title="unique">UQ</span>{/if}
						{#if f.def.auto}<span class="badge b-auto" title="auto">auto</span>{/if}
						{#if f.def.state_machine}<span class="badge b-sm" title="state machine">SM</span>{/if}
						{#if f.def.relation}<span class="badge b-fk" title={`→ ${f.def.relation}`}>FK</span>{/if}
					</span>
					<span class="en-ftype">{f.def.type}</span>
					{#if f.def.relation}
						<Handle type="source" position={Position.Right} id={f.id} class="h h-fk" />
					{/if}
				</div>
			{/each}
			{#if entity.fields.length === 0}
				<div class="en-empty">no fields</div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.entity-node {
		position: relative;
		width: 248px;
		background: var(--surface);
		border: 1px solid var(--border-strong);
		border-radius: var(--radius);
		box-shadow: var(--shadow-sm);
		font-size: 12.5px;
		overflow: hidden;
		transition: border-color 0.12s ease, box-shadow 0.12s ease;
	}
	.entity-node.selected {
		border-color: var(--brand);
		box-shadow: 0 0 0 2px var(--brand-100), var(--shadow-md);
	}

	.en-header {
		display: flex;
		align-items: center;
		gap: 7px;
		padding: 9px 12px;
		background: var(--surface-2);
		border-bottom: 1px solid var(--border);
		cursor: pointer;
		position: relative;
	}
	.en-icon {
		color: var(--text-3);
		font-size: 11px;
	}
	.en-title {
		font-weight: 600;
		font-family: var(--mono);
		font-size: 13px;
		letter-spacing: -0.01em;
		color: var(--text);
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.en-count {
		font-size: 11px;
		color: var(--text-3);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 10px;
		padding: 0 7px;
		min-width: 20px;
		text-align: center;
	}

	.en-fields {
		display: flex;
		flex-direction: column;
	}
	.en-field {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 12px;
		min-height: 26px;
		cursor: pointer;
		position: relative;
		border-bottom: 1px solid color-mix(in srgb, var(--border) 55%, transparent);
	}
	.en-field:last-child {
		border-bottom: none;
	}
	.en-field:hover {
		background: var(--surface-2);
	}
	.en-field.sel {
		background: var(--brand-100);
	}
	.en-field.fk .en-fname {
		color: var(--accent-fk);
	}
	.en-fname {
		font-family: var(--mono);
		flex: 1;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.en-flags {
		display: inline-flex;
		gap: 3px;
	}
	.en-ftype {
		color: var(--text-3);
		font-family: var(--mono);
		font-size: 11px;
	}
	.en-empty {
		padding: 6px 12px;
		color: var(--text-3);
		font-style: italic;
		font-size: 11px;
	}

	/* Sober badge scheme: structural facts (REQ/UQ/auto) stay monochrome so a dense
	   schema reads calm; only the relational/lifecycle signals (FK, SM) carry a
	   whisper of accent — minimal colour, maximal scannability. */
	.badge.b-req {
		background: var(--surface-3);
		color: var(--text);
	}
	.badge.b-uniq {
		background: var(--surface-3);
		color: var(--text-2);
	}
	.badge.b-auto {
		background: transparent;
		color: var(--text-3);
		box-shadow: inset 0 0 0 1px var(--border);
	}
	.badge.b-sm {
		background: var(--brand-100);
		color: var(--brand);
	}
	.badge.b-fk {
		background: color-mix(in srgb, var(--accent-fk) 12%, transparent);
		color: var(--accent-fk);
	}

	/* Handles — small, brand-coloured. The :global wrappers reach the class we
	   pass to <Handle> (Svelte Flow renders the actual div). */
	:global(.entity-node .h) {
		width: 9px;
		height: 9px;
		border: 2px solid var(--surface);
		background: var(--border-strong);
	}
	:global(.entity-node .h-target) {
		background: var(--text-3);
	}
	:global(.entity-node .h-new) {
		background: var(--brand);
	}
	:global(.entity-node .h-fk) {
		background: var(--accent-fk);
	}
</style>
