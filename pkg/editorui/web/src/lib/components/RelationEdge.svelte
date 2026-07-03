<script lang="ts">
	import { BaseEdge, EdgeLabel, getBezierPath, type EdgeProps } from '@xyflow/svelte';
	import type { RelationEdgeData } from '../stores/editor.svelte';

	let {
		id,
		sourceX,
		sourceY,
		targetX,
		targetY,
		sourcePosition,
		targetPosition,
		markerEnd,
		selected,
		data
	}: EdgeProps = $props();

	const d = $derived((data ?? {}) as RelationEdgeData);

	// getBezierPath returns [path, labelX, labelY, offsetX, offsetY].
	const path = $derived(
		getBezierPath({ sourceX, sourceY, sourcePosition, targetX, targetY, targetPosition })
	);

	// on_delete drives the label tint: cascade = danger, set_null = warn, restrict/default = neutral.
	const tint = $derived(
		d.onDelete === 'cascade' ? 'cascade' : d.onDelete === 'set_null' ? 'setnull' : 'restrict'
	);

	// Relations-block embed edge (UI-F4-S4): dashed + a cardinality chip, quieter
	// than a field FK so a relation-heavy schema reads ordered, not saturated.
	const cardinality = $derived(
		d.embed?.kind === 'has_many' ? '1:N' : d.embed?.kind === 'belongs_to' ? 'N:1' : 'N:N'
	);
</script>

{#if d.embed}
	<BaseEdge {id} path={path[0]} {markerEnd} class={selected ? 'emb-edge sel' : 'emb-edge'} />
	<EdgeLabel x={path[1]} y={path[2]}>
		<div
			class="rel-label embed"
			class:sel={selected}
			title={`?include=${d.embed.name} — ${d.embed.kind}`}
		>
			<span class="rl-name">{d.embed.name}</span>
			<span class="rl-card">{cardinality}</span>
		</div>
	</EdgeLabel>
{:else}
	<BaseEdge {id} path={path[0]} {markerEnd} class={selected ? 'rel-edge sel' : 'rel-edge'} />
	<EdgeLabel x={path[1]} y={path[2]}>
		<div class="rel-label {tint}" class:sel={selected}>
			<span class="rl-name">{d.fieldName}</span>
			{#if d.onDelete && d.onDelete !== 'restrict'}
				<span class="rl-od">{d.onDelete === 'cascade' ? '⇊' : '∅'}</span>
			{/if}
		</div>
	</EdgeLabel>
{/if}

<style>
	:global(.svelte-flow__edge .rel-edge) {
		stroke: var(--border-strong);
		stroke-width: 1.5;
	}
	:global(.svelte-flow__edge .rel-edge.sel) {
		stroke: var(--brand);
		stroke-width: 2;
	}

	/* Embed (relations-block) edge: dashed and one step quieter than a field FK —
	   both themes inherit the token, so light/dark stay coherent. */
	:global(.svelte-flow__edge .emb-edge) {
		stroke: color-mix(in srgb, var(--border-strong) 72%, transparent);
		stroke-width: 1.25;
		stroke-dasharray: 5 4;
	}
	:global(.svelte-flow__edge .emb-edge.sel) {
		stroke: var(--brand);
		stroke-width: 1.75;
		stroke-dasharray: 5 4;
	}

	.rel-label {
		display: inline-flex;
		align-items: center;
		gap: 4px;
		padding: 1px 7px;
		font-family: var(--mono);
		font-size: 10.5px;
		font-weight: 600;
		color: var(--text-2);
		background: var(--surface);
		border: 1px solid var(--border-strong);
		border-radius: 10px;
		box-shadow: var(--shadow-sm);
		white-space: nowrap;
	}
	.rel-label.sel {
		border-color: var(--brand);
		color: var(--brand);
	}
	.rel-label.cascade {
		border-color: color-mix(in srgb, var(--danger) 55%, var(--border-strong));
	}
	.rel-label.setnull {
		border-color: color-mix(in srgb, var(--warn) 55%, var(--border-strong));
	}
	/* Embed label: dashed border echoes the edge; the cardinality chip is the
	   double channel (text, not colour alone) that names the kind at a glance. */
	.rel-label.embed {
		border-style: dashed;
		color: var(--text-3);
		font-weight: 500;
	}
	.rel-label.embed.sel {
		color: var(--brand);
	}
	.rl-card {
		padding: 0 4px;
		border-radius: 6px;
		font-size: 9.5px;
		font-weight: 700;
		letter-spacing: 0.03em;
		background: var(--surface-2);
		color: var(--text-2);
	}
	.rel-label.embed.sel .rl-card {
		color: var(--brand);
	}
	.rl-od {
		font-weight: 800;
	}
	.rel-label.cascade .rl-od {
		color: var(--danger);
	}
	.rel-label.setnull .rl-od {
		color: var(--warn);
	}
</style>
