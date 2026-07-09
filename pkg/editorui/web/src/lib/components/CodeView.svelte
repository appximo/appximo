<script lang="ts">
	// The "Code" view (JSON-EDITOR-S2): the schema as editable JSON in CodeMirror 6
	// with three assistance layers —
	//   1. SYNTAX     — lang-json parse errors, instant (jsonParseLinter).
	//   2. STRUCTURE  — the engine's formal meta-schema (GET /editor/meta-schema)
	//                   drives key/value AUTOCOMPLETION and HOVER docs, client-side,
	//                   instant, offline.
	//   3. SEMANTICS  — the buffer is debounce-POSTed to /editor/validate
	//                   (schema.ValidateReport: structural + semantic, one authority)
	//                   and every error lands on ITS line with its fix (engineLint).
	// Apply is GATED on that report: an invalid document never reaches the model —
	// this kills the silent-drop the JSON-AUDIT-V1 probe found (unknown keys used to
	// be discarded by the model transform; now they are located errors BEFORE Apply).
	//
	// Rename semantics (UI-F4-S1) are preserved: the buffer opens as editor.toJSON()
	// (which derives renamed_from from the canvas baselines) and Apply goes through
	// the SAME loadSchema path as Import (renamed_from lifts back into the baseline).
	// Renaming a resource/field in JSON WITHOUT renamed_from is honestly a
	// delete+create — exactly what the engine itself would do with that document.
	import { onDestroy, onMount } from 'svelte';
	import { EditorState, type Extension } from '@codemirror/state';
	import {
		EditorView,
		keymap,
		lineNumbers,
		highlightActiveLine,
		highlightActiveLineGutter,
		drawSelection,
		hoverTooltip
	} from '@codemirror/view';
	import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
	import { bracketMatching, foldGutter, foldKeymap, indentOnInput, syntaxHighlighting, defaultHighlightStyle } from '@codemirror/language';
	import { closeBrackets, closeBracketsKeymap, autocompletion, completionKeymap } from '@codemirror/autocomplete';
	import { linter, lintGutter, lintKeymap, forceLinting } from '@codemirror/lint';
	import { json, jsonLanguage, jsonParseLinter } from '@codemirror/lang-json';
	import { jsonCompletion, jsonSchemaHover, stateExtensions, updateSchema } from 'codemirror-json-schema';
	import type { JSONSchema7 } from 'json-schema';

	import { editor } from '../stores/editor.svelte';
	import { ui } from '../stores/ui.svelte';
	import { studioTheme } from '../codeview/cmTheme';
	import { engineLinter, type EngineLintStatus } from '../codeview/engineLint';
	import { pathToRange } from '../codeview/pathToRange';
	import { fetchMetaSchema, validateSchemaText, type ValidationReport } from '../codeview/validateApi';

	let host: HTMLDivElement;
	let view: EditorView | null = null;

	let status = $state<EngineLintStatus>({ kind: 'checking' });
	let dirty = $state(false);
	let metaSchemaActive = $state(false);
	let applyMsg = $state<string | null>(null);
	let applying = $state(false);

	/** The model serialization this buffer started from (dirty = buffer !== it). */
	let baseText = '';

	const errorCount = $derived(status.kind === 'report' ? status.report.errors.length : 0);

	function currentText(): string {
		return view ? view.state.doc.toString() : '';
	}

	onMount(() => {
		baseText = editor.toJSON();
		const opening = ui.codeBuffer ?? baseText;

		const extensions: Extension[] = [
			lineNumbers(),
			foldGutter(),
			history(),
			drawSelection(),
			indentOnInput(),
			bracketMatching(),
			closeBrackets(),
			highlightActiveLine(),
			highlightActiveLineGutter(),
			autocompletion(),
			keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...foldKeymap, ...completionKeymap, ...lintKeymap, indentWithTab]),
			json(),
			syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
			// Layer 1 — syntax (instant).
			linter(jsonParseLinter()),
			// Layer 2 — structure: meta-schema-driven completion + hover (the schema
			// itself arrives async below via updateSchema).
			stateExtensions(),
			jsonLanguage.data.of({ autocomplete: jsonCompletion() }),
			hoverTooltip(jsonSchemaHover()),
			// Layer 3 — the engine's ValidateReport, debounced, mapped to lines.
			engineLinter((s) => (status = s)),
			lintGutter(),
			studioTheme,
			EditorView.updateListener.of((u) => {
				if (u.docChanged) {
					dirty = u.state.doc.toString() !== baseText;
					applyMsg = null;
				}
			})
		];

		view = new EditorView({
			state: EditorState.create({ doc: opening, extensions }),
			parent: host
		});
		forceLinting(view);

		fetchMetaSchema()
			.then((ms) => {
				if (view) {
					updateSchema(view, ms as JSONSchema7);
					metaSchemaActive = true;
				}
			})
			.catch(() => (metaSchemaActive = false)); // layers 1+3 still cover everything

		dirty = opening !== baseText;
	});

	onDestroy(() => {
		// Preserve unapplied work across view switches; a clean buffer re-snapshots.
		ui.codeBuffer = dirty ? currentText() : null;
		view?.destroy();
		view = null;
	});

	function jumpToFirstError(report: ValidationReport) {
		if (!view || report.errors.length === 0) return;
		const e = report.errors[0];
		const r = pathToRange(view.state, e.path, e.rule === 'unknown_key' ? e.got : undefined);
		view.dispatch({ selection: { anchor: r.from }, scrollIntoView: true });
		view.focus();
	}

	function format() {
		if (!view) return;
		try {
			const pretty = JSON.stringify(JSON.parse(currentText()), null, 2);
			view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: pretty } });
		} catch {
			applyMsg = 'cannot format: the document is not valid JSON';
		}
	}

	/** The gated Apply (S2c): validate with the ENGINE's report first; load the
	 *  model only when the document is fully valid. */
	async function apply() {
		if (!view || applying) return;
		const text = currentText();
		let parsed: unknown;
		try {
			parsed = JSON.parse(text);
		} catch {
			applyMsg = 'not applied: fix the JSON syntax first';
			return;
		}
		applying = true;
		applyMsg = null;
		let report: ValidationReport;
		try {
			report = await validateSchemaText(text);
		} catch {
			applying = false;
			applyMsg = 'not applied: the engine is unreachable (validation is server-side)';
			return;
		}
		applying = false;
		if (!report.valid) {
			status = { kind: 'report', report };
			applyMsg = `not applied: ${report.errors.length} validation error${report.errors.length === 1 ? '' : 's'} — fix them first`;
			forceLinting(view);
			jumpToFirstError(report);
			return;
		}
		// Same path as Import: renamed_from lifts into the rename baseline (UI-F4-S1).
		editor.loadSchema(parsed as Parameters<typeof editor.loadSchema>[0]);
		ui.codeBuffer = null;
		dirty = false;
		ui.view = 'canvas';
	}
</script>

<div class="codeview" data-testid="code-view">
	<div class="codebar">
		<span class="status" data-testid="code-status">
			{#if status.kind === 'checking'}
				<span class="dot neutral"></span> validating…
			{:else if status.kind === 'syntax'}
				<span class="dot bad"></span> JSON syntax error
			{:else if status.kind === 'unreachable'}
				<span class="dot warn"></span> engine unreachable — semantic layer offline
			{:else if errorCount > 0}
				<span class="dot bad"></span> {errorCount} error{errorCount === 1 ? '' : 's'}
			{:else}
				<span class="dot ok"></span> valid schema
			{/if}
		</span>
		{#if status.kind === 'report' && errorCount > 0}
			<button class="linklike" onclick={() => status.kind === 'report' && jumpToFirstError(status.report)}>first error</button>
		{/if}
		<span class="hint" title="Autocompletion & hover docs from the engine's formal meta-schema">
			{metaSchemaActive ? 'meta-schema assistance on' : 'meta-schema assistance loading…'}
		</span>
		<span class="spacer"></span>
		{#if dirty}<span class="chip" data-testid="code-dirty">unapplied changes</span>{/if}
		{#if applyMsg}<span class="applymsg" data-testid="apply-msg">{applyMsg}</span>{/if}
		<button class="btn" onclick={format}>Format</button>
		<button class="btn primary" data-testid="code-apply" onclick={apply} disabled={applying}>
			{applying ? 'Validating…' : 'Apply'}
		</button>
	</div>
	<div class="cm-host" bind:this={host}></div>
</div>

<style>
	.codeview {
		flex: 1;
		min-width: 0;
		display: flex;
		flex-direction: column;
		background: var(--surface);
	}
	.codebar {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 6px 12px;
		border-bottom: 1px solid var(--border);
		background: var(--surface);
		font-size: 12px;
		color: var(--text-2);
	}
	.status {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		font-weight: 600;
		color: var(--text);
	}
	.dot {
		width: 8px;
		height: 8px;
		border-radius: 50%;
		display: inline-block;
	}
	.dot.ok { background: var(--ok); }
	.dot.bad { background: var(--danger); }
	.dot.warn { background: var(--warn); }
	.dot.neutral { background: var(--text-3); }
	.hint {
		color: var(--text-3);
		font-size: 11.5px;
	}
	.linklike {
		border: none;
		background: none;
		color: var(--brand);
		font-size: 11.5px;
		padding: 0;
		cursor: pointer;
	}
	.spacer { flex: 1; }
	.chip {
		font-size: 11px;
		padding: 2px 8px;
		border-radius: 999px;
		background: color-mix(in srgb, var(--warn) 12%, transparent);
		color: var(--warn);
		border: 1px solid color-mix(in srgb, var(--warn) 30%, transparent);
	}
	.applymsg {
		color: var(--danger);
		font-size: 12px;
	}
	.cm-host {
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
	.cm-host :global(.cm-editor) {
		height: 100%;
	}
</style>
