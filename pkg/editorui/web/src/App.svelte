<script lang="ts">
	import { onMount } from 'svelte';
	import { SvelteFlowProvider } from '@xyflow/svelte';
	import Toolbar from './lib/components/Toolbar.svelte';
	import Canvas from './lib/components/Canvas.svelte';
	import PropertyPanel from './lib/components/PropertyPanel.svelte';
	import DeployModal from './lib/components/DeployModal.svelte';
	import RbacModal from './lib/components/RbacModal.svelte';
	import StateMachineModal from './lib/components/StateMachineModal.svelte';
	import { editor } from './lib/stores/editor.svelte';
	import { SAMPLES } from './lib/schema/samples';

	// First impression: load a rich example so the canvas isn't blank. The user can
	// New / Import / load another example from the toolbar.
	onMount(() => {
		if (editor.entities.length === 0) {
			const seed = SAMPLES.find((s) => s.id === 'erp') ?? SAMPLES[0];
			if (seed) editor.loadSchema(structuredClone(seed.schema));
		}
	});
</script>

<div class="app">
	<SvelteFlowProvider>
		<Toolbar />
		<div class="workspace">
			<Canvas />
			<PropertyPanel />
		</div>
	</SvelteFlowProvider>
	<DeployModal />
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
