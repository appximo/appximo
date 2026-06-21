<script lang="ts">
	import {
		SvelteFlow,
		Background,
		BackgroundVariant,
		Controls,
		MiniMap,
		Panel,
		useSvelteFlow,
		useUpdateNodeInternals,
		type Connection,
		type OnDelete
	} from '@xyflow/svelte';
	import '@xyflow/svelte/dist/style.css';
	import { tick } from 'svelte';

	import EntityNode from './EntityNode.svelte';
	import RelationEdge from './RelationEdge.svelte';
	import { editor, type FlowNode, type FlowEdge } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';

	// Stable type maps (module-ish consts — created once, never reactive).
	const nodeTypes = { entity: EntityNode };
	const edgeTypes = { relation: RelationEdge };

	// These hooks REQUIRE the SvelteFlowProvider context — Canvas is rendered inside
	// <SvelteFlowProvider> in +page.svelte, so they resolve.
	const { fitView } = useSvelteFlow();
	const updateNodeInternals = useUpdateNodeInternals();

	// Re-register handles whenever a node's field/relation set changes (handles are
	// added/removed in the DOM, so Svelte Flow must re-measure them).
	$effect(() => {
		editor.structureVersion; // track
		const ids = editor.entities.map((e) => e.id);
		tick().then(() => updateNodeInternals(ids));
	});

	// Frame the graph after an auto-layout / fresh import.
	$effect(() => {
		editor.layoutVersion; // track
		tick().then(() => fitView({ padding: 0.18, duration: 300 }));
	});

	function handleConnect(conn: Connection) {
		if (conn.source && conn.target) editor.createRelation(conn.source, conn.target);
	}

	const handleDelete: OnDelete<FlowNode, FlowEdge> = ({ nodes, edges }) => {
		for (const e of edges) editor.deleteEdge(e.id);
		for (const n of nodes) editor.deleteEntity(n.id);
	};

	function handleDragStop(detail: { targetNode: FlowNode | null; nodes: FlowNode[] }) {
		for (const n of detail.nodes) editor.syncPosition(n.id, n.position);
	}
</script>

<div class="canvas-wrap">
	<SvelteFlow
		bind:nodes={editor.nodes}
		bind:edges={editor.edges}
		{nodeTypes}
		{edgeTypes}
		fitView
		onlyRenderVisibleElements
		colorMode={ui.theme}
		minZoom={0.1}
		maxZoom={2.5}
		deleteKey={['Backspace', 'Delete']}
		nodeDragThreshold={2}
		onconnect={handleConnect}
		ondelete={handleDelete}
		onnodedragstop={handleDragStop}
		onpaneclick={() => editor.clearSelection()}
	>
		<Background variant={BackgroundVariant.Dots} gap={18} size={1} bgColor="var(--bg)" />
		<Controls showLock={false} />
		<MiniMap pannable zoomable nodeColor="var(--brand)" maskColor="color-mix(in srgb, var(--bg) 78%, transparent)" />
		<Panel position="bottom-center">
			<div class="hint">
				drag from a node's <b style:color="var(--brand)">●</b> handle to another to create a relation ·
				select + <kbd>Del</kbd> to remove
			</div>
		</Panel>
	</SvelteFlow>

	{#if editor.entities.length === 0}
		<div class="empty-state">
			<div class="es-card">
				<div class="es-title">Empty canvas</div>
				<p>Add an entity, or load an example from the toolbar, to start modelling.</p>
				<button class="btn primary" onclick={() => editor.addEntity()}>+ Add entity</button>
			</div>
		</div>
	{/if}
</div>

<style>
	.canvas-wrap {
		position: relative;
		width: 100%;
		height: 100%;
		min-height: 0;
	}
	:global(.canvas-wrap .svelte-flow) {
		background: var(--bg);
	}

	.hint {
		font-size: 11px;
		color: var(--text-3);
		background: color-mix(in srgb, var(--surface) 86%, transparent);
		border: 1px solid var(--border);
		border-radius: 20px;
		padding: 4px 12px;
		backdrop-filter: blur(4px);
	}
	.hint kbd {
		font-family: var(--mono);
		font-size: 10px;
		border: 1px solid var(--border-strong);
		border-radius: 3px;
		padding: 0 4px;
		background: var(--surface-2);
	}

	.empty-state {
		position: absolute;
		inset: 0;
		display: grid;
		place-items: center;
		pointer-events: none;
	}
	.es-card {
		pointer-events: all;
		text-align: center;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow-md);
		padding: 28px 36px;
		max-width: 340px;
	}
	.es-title {
		font-weight: 700;
		font-size: 16px;
		margin-bottom: 6px;
	}
	.es-card p {
		color: var(--text-2);
		margin: 0 0 16px;
		font-size: 13px;
	}

	/* Svelte Flow control buttons themed to our tokens. */
	:global(.canvas-wrap .svelte-flow__controls) {
		box-shadow: var(--shadow-sm);
	}
	:global(.canvas-wrap .svelte-flow__controls-button) {
		background: var(--surface);
		border-bottom: 1px solid var(--border);
		fill: var(--text-2);
	}
	:global(.canvas-wrap .svelte-flow__controls-button:hover) {
		background: var(--surface-2);
	}
	:global(.canvas-wrap .svelte-flow__minimap) {
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius-sm);
	}
</style>
