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
</script>

<BaseEdge {id} path={path[0]} {markerEnd} class={selected ? 'rel-edge sel' : 'rel-edge'} />

<EdgeLabel x={path[1]} y={path[2]}>
	<div class="rel-label {tint}" class:sel={selected}>
		<span class="rl-name">{d.fieldName}</span>
		{#if d.onDelete && d.onDelete !== 'restrict'}
			<span class="rl-od">{d.onDelete === 'cascade' ? '⇊' : '∅'}</span>
		{/if}
	</div>
</EdgeLabel>

<style>
	:global(.svelte-flow__edge .rel-edge) {
		stroke: var(--border-strong);
		stroke-width: 1.5;
	}
	:global(.svelte-flow__edge .rel-edge.sel) {
		stroke: var(--brand);
		stroke-width: 2;
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
