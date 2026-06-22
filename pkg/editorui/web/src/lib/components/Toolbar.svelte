<script lang="ts">
	import { editor } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';
	import { deploy } from '../stores/deploy.svelte';
	import { SAMPLES } from '../schema/samples';

	let showExport = $state(false);
	let showImport = $state(false);
	let importText = $state('');
	let importError = $state<string | null>(null);
	let copied = $state(false);
	let menuOpen = $state(false);

	const exportJSON = $derived(showExport ? editor.toJSON() : '');

	function loadSample(id: string) {
		const s = SAMPLES.find((x) => x.id === id);
		if (s) editor.loadSchema(structuredClone(s.schema));
		menuOpen = false;
	}

	function onFile(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const f = input.files?.[0];
		if (!f) return;
		f.text().then((txt) => {
			importText = txt;
			tryImport();
		});
	}

	function tryImport() {
		try {
			const parsed = JSON.parse(importText);
			editor.loadSchema(parsed);
			importError = null;
			showImport = false;
			importText = '';
		} catch (err) {
			importError = err instanceof Error ? err.message : String(err);
		}
	}

	function download() {
		const blob = new Blob([editor.toJSON()], { type: 'application/json' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `${editor.schemaName || 'schema'}.json`;
		a.click();
		URL.revokeObjectURL(url);
	}

	async function copyJSON() {
		try {
			await navigator.clipboard.writeText(editor.toJSON());
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			/* clipboard blocked — the modal still shows the JSON to copy by hand */
		}
	}
</script>

<header class="topbar">
	<div class="brand">
		<span class="logo" aria-hidden="true">▤</span>
		<span class="brand-name">Appitools <b>Studio</b></span>
	</div>

	<div class="sep"></div>

	<div class="group">
		<button class="btn subtle" onclick={() => editor.newSchema()}>New</button>

		<div class="menu" class:open={menuOpen}>
			<button class="btn subtle" onclick={() => (menuOpen = !menuOpen)}>Examples ▾</button>
			{#if menuOpen}
				<div class="menu-pop" role="menu">
					{#each SAMPLES as s}
						<button class="menu-item" onclick={() => loadSample(s.id)} role="menuitem">
							<span class="mi-label">{s.label}</span>
							<span class="mi-desc">{s.description}</span>
						</button>
					{/each}
				</div>
			{/if}
		</div>

		<button class="btn subtle" onclick={() => { showImport = true; importError = null; }}>Import</button>
		<button class="btn subtle" onclick={() => (showExport = true)}>Export</button>
	</div>

	<div class="sep"></div>

	<div class="group">
		<button class="btn" onclick={() => editor.addEntity()}>+ Entity</button>
		<button class="btn" onclick={() => editor.autoLayout()} title="Auto-layout (dagre)">Auto-layout</button>
		<button class="btn" onclick={() => (ui.rbacOpen = true)} title="Roles & permissions (RBAC)">🛡 Roles</button>
	</div>

	<div class="spacer"></div>

	<div class="api-name" title="API name">{editor.schemaName}</div>

	<button class="btn primary deploy" onclick={() => deploy.openDeploy()} title="Deploy to the engine">
		▲ Deploy
	</button>

	<button class="btn subtle icon" onclick={() => ui.toggle()} title="Toggle theme" aria-label="Toggle theme">
		{ui.theme === 'light' ? '☾' : '☀'}
	</button>
</header>

{#if showExport}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => { if (e.target === e.currentTarget) showExport = false; }}
		onkeydown={(e) => { if (e.key === 'Escape') showExport = false; }}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Export schema" tabindex="-1">
			<div class="modal-head">
				<span>Export schema JSON</span>
				<div class="modal-actions">
					<button class="btn" onclick={copyJSON}>{copied ? 'Copied ✓' : 'Copy'}</button>
					<button class="btn primary" onclick={download}>Download</button>
					<button class="btn subtle" onclick={() => (showExport = false)}>Close</button>
				</div>
			</div>
			<pre class="code">{exportJSON}</pre>
			<div class="modal-foot">
				This is the exact schema the engine consumes — validate it with
				<code>appitools validate schema.json</code>.
			</div>
		</div>
	</div>
{/if}

{#if showImport}
	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div
		class="overlay"
		onclick={(e) => { if (e.target === e.currentTarget) showImport = false; }}
		onkeydown={(e) => { if (e.key === 'Escape') showImport = false; }}
		role="presentation"
	>
		<div class="modal" role="dialog" aria-modal="true" aria-label="Import schema" tabindex="-1">
			<div class="modal-head">
				<span>Import schema JSON</span>
				<div class="modal-actions">
					<label class="btn">
						Choose file…
						<input type="file" accept=".json,application/json" onchange={onFile} hidden />
					</label>
					<button class="btn subtle" onclick={() => (showImport = false)}>Close</button>
				</div>
			</div>
			<textarea
				class="code editable"
				bind:value={importText}
				spellcheck="false"
				placeholder="Paste an Appitools schema JSON here, or choose a file…"
			></textarea>
			{#if importError}<div class="import-err">Invalid: {importError}</div>{/if}
			<div class="modal-foot">
				<button class="btn primary" onclick={tryImport} disabled={!importText.trim()}>Load schema</button>
			</div>
		</div>
	</div>
{/if}

<style>
	.topbar {
		height: var(--topbar-h);
		flex: 0 0 var(--topbar-h);
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 0 14px;
		background: var(--surface);
		border-bottom: 1px solid var(--border);
		box-shadow: var(--shadow-sm);
		z-index: 5;
	}
	.brand {
		display: flex;
		align-items: center;
		gap: 8px;
	}
	.logo {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		background: var(--brand);
		color: #fff;
		border-radius: 6px;
		font-size: 14px;
	}
	.brand-name {
		font-size: 14px;
		color: var(--text-2);
	}
	.brand-name b {
		color: var(--text);
	}
	.sep {
		width: 1px;
		height: 24px;
		background: var(--border);
		margin: 0 4px;
	}
	.group {
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.spacer {
		flex: 1;
	}
	.api-name {
		font-family: var(--mono);
		font-size: 12.5px;
		color: var(--text-2);
		background: var(--surface-2);
		padding: 4px 10px;
		border-radius: 12px;
		max-width: 220px;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.icon {
		font-size: 15px;
		padding: 6px 9px;
	}

	.menu {
		position: relative;
	}
	.menu-pop {
		position: absolute;
		top: calc(100% + 4px);
		left: 0;
		min-width: 240px;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow-md);
		padding: 4px;
		z-index: 20;
	}
	.menu-item {
		display: flex;
		flex-direction: column;
		gap: 1px;
		width: 100%;
		text-align: left;
		padding: 7px 10px;
		border: none;
		background: none;
		border-radius: var(--radius-sm);
	}
	.menu-item:hover {
		background: var(--surface-2);
	}
	.mi-label {
		font-weight: 600;
		color: var(--text);
	}
	.mi-desc {
		font-size: 11.5px;
		color: var(--text-3);
	}

	.overlay {
		position: fixed;
		inset: 0;
		background: rgba(10, 12, 16, 0.45);
		display: grid;
		place-items: center;
		z-index: 100;
	}
	.modal {
		width: min(720px, 92vw);
		max-height: 84vh;
		display: flex;
		flex-direction: column;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		box-shadow: var(--shadow-md);
		overflow: hidden;
	}
	.modal-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 12px 16px;
		border-bottom: 1px solid var(--border);
		font-weight: 600;
	}
	.modal-actions {
		display: flex;
		gap: 6px;
	}
	.code {
		flex: 1;
		margin: 0;
		padding: 14px 16px;
		overflow: auto;
		font-family: var(--mono);
		font-size: 12px;
		line-height: 1.5;
		color: var(--text);
		background: var(--surface-2);
		white-space: pre;
	}
	textarea.editable {
		resize: none;
		border: none;
		outline: none;
		min-height: 320px;
	}
	.import-err {
		padding: 8px 16px;
		color: var(--danger);
		font-size: 12.5px;
		font-family: var(--mono);
		background: color-mix(in srgb, var(--danger) 8%, transparent);
	}
	.modal-foot {
		padding: 12px 16px;
		border-top: 1px solid var(--border);
		font-size: 12px;
		color: var(--text-3);
	}
	.modal-foot code {
		font-family: var(--mono);
		color: var(--text-2);
	}
</style>
