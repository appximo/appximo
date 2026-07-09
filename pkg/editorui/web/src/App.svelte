<script lang="ts">
	import { onMount } from 'svelte';
	import { SvelteFlowProvider } from '@xyflow/svelte';
	import Toolbar from './lib/components/Toolbar.svelte';
	import Canvas from './lib/components/Canvas.svelte';
	import PropertyPanel from './lib/components/PropertyPanel.svelte';
	import DeployModal from './lib/components/DeployModal.svelte';
	import FilesModal from './lib/components/FilesModal.svelte';
	import HistoryModal from './lib/components/HistoryModal.svelte';
	import FlowsModal from './lib/components/FlowsModal.svelte';
	import RbacModal from './lib/components/RbacModal.svelte';
	import StateMachineModal from './lib/components/StateMachineModal.svelte';
	import { editor } from './lib/stores/editor.svelte';
	import { ui } from './lib/stores/ui.svelte';

	// EDITOR-BOOT-SYNC: the canvas opens on the schema the ENGINE SERVES
	// (GET /editor/current-schema — the boot schema source), never a frontend
	// sample. Booting with your óptica shows your óptica; the demo schemas live
	// in the toolbar's Examples menu, opt-in. The engine's schema is deployed
	// reality, so the default 'declared' rename baseline applies (renames made
	// on the canvas chain from the served names — UI-F4-S1).
	onMount(async () => {
		if (editor.entities.length !== 0) return;
		try {
			const res = await fetch('/editor/current-schema');
			if (!res.ok) throw new Error(`HTTP ${res.status}`);
			editor.loadSchema(await res.json());
		} catch {
			// Engine unreachable (e.g. bare vite dev) — start blank, never a
			// sample that could be mistaken for the running app.
			editor.newSchema();
		}
	});
</script>

<div class="app">
	<SvelteFlowProvider>
		<Toolbar />
		<div class="workspace">
			{#if ui.view === 'code'}
				<!-- Lazy chunk: CodeMirror + json-schema-library load on first use, so
				     the canvas bundle doesn't pay for the Code view (the same pattern
				     as the admin UI's lazy ECharts route). -->
				{#await import('./lib/components/CodeView.svelte') then codeView}
					<codeView.default />
				{/await}
			{:else}
				<Canvas />
				<PropertyPanel />
			{/if}
		</div>
	</SvelteFlowProvider>
	<DeployModal />
	<FilesModal />
	<HistoryModal />
	<FlowsModal />
	<RbacModal />
	<StateMachineModal />
</div>

<style>
	.app {
		display: flex;
		flex-direction: column;
		height: 100vh;
		width: 100vw;
		overflow: hidden;
	}
	.workspace {
		flex: 1;
		display: flex;
		min-height: 0;
	}
	.workspace :global(.canvas-wrap) {
		flex: 1;
		min-width: 0;
	}
</style>
