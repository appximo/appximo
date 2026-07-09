// CodeMirror 6 theme wired to Studio's design tokens (app.css CSS variables,
// UI-F3-S2 sober palette). Colors are var() references, so the SAME extension
// follows the light/dark toggle live — no editor rebuild on theme change.

import { EditorView } from '@codemirror/view';
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { tags } from '@lezer/highlight';

const structure = EditorView.theme({
	'&': {
		height: '100%',
		fontSize: '12.5px',
		backgroundColor: 'var(--surface)',
		color: 'var(--text)'
	},
	'.cm-scroller': {
		fontFamily: 'var(--mono)',
		lineHeight: '1.55'
	},
	'.cm-content': { caretColor: 'var(--text)' },
	'.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--text)' },
	'&.cm-focused': { outline: 'none' },
	'&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground': {
		backgroundColor: 'color-mix(in srgb, var(--brand) 18%, transparent)'
	},
	'.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--brand) 5%, transparent)' },
	'.cm-gutters': {
		backgroundColor: 'var(--surface)',
		color: 'var(--text-3)',
		border: 'none',
		borderRight: '1px solid var(--border)'
	},
	'.cm-activeLineGutter': { backgroundColor: 'color-mix(in srgb, var(--brand) 7%, transparent)' },
	'.cm-lintRange-error': {
		backgroundImage: 'none',
		textDecoration: 'underline wavy var(--danger) 1px',
		textUnderlineOffset: '3px'
	},
	'.cm-lintRange-warning': {
		backgroundImage: 'none',
		textDecoration: 'underline wavy var(--warn) 1px',
		textUnderlineOffset: '3px'
	},
	'.cm-tooltip': {
		backgroundColor: 'var(--surface)',
		color: 'var(--text)',
		border: '1px solid var(--border-strong)',
		borderRadius: 'var(--radius-sm)',
		boxShadow: 'var(--shadow-lg)',
		fontFamily: 'var(--sans, inherit)',
		fontSize: '12px',
		maxWidth: '440px'
	},
	'.cm-tooltip.cm-tooltip-autocomplete > ul': { fontFamily: 'var(--mono)', fontSize: '12px' },
	'.cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]': {
		backgroundColor: 'color-mix(in srgb, var(--brand) 14%, transparent)',
		color: 'var(--text)'
	},
	'.cm-tooltip-lint': { padding: '2px' },
	'.cm-diagnostic': {
		padding: '5px 8px',
		borderLeft: '3px solid var(--danger)',
		fontFamily: 'var(--sans, inherit)'
	},
	'.cm-diagnostic-warning': { borderLeftColor: 'var(--warn)' },
	'.cm-diagnostic code': { fontFamily: 'var(--mono)', fontSize: '11px' },
	'.cm-panels': { backgroundColor: 'var(--surface-2)', color: 'var(--text)' },
	'.cm-panel.cm-panel-lint ul [aria-selected]': {
		backgroundColor: 'color-mix(in srgb, var(--brand) 12%, transparent)'
	},
	'.cm-panel.cm-panel-lint ul': { maxHeight: '140px' },
	'.cm-lint-marker-error': { content: 'none' },
	'.cm-foldPlaceholder': {
		backgroundColor: 'var(--surface-2)',
		border: '1px solid var(--border)',
		color: 'var(--text-3)'
	},
	'.cm-matchingBracket, &.cm-focused .cm-matchingBracket': {
		backgroundColor: 'color-mix(in srgb, var(--brand) 16%, transparent)',
		outline: 'none'
	}
});

// JSON is four token kinds — keys, strings, numbers, atoms. Keys carry the
// brand; values stay neutral (data-ink first, no rainbow).
const jsonHighlight = HighlightStyle.define([
	{ tag: tags.propertyName, color: 'var(--brand)' },
	{ tag: tags.string, color: 'var(--text-2)' },
	{ tag: tags.number, color: 'var(--warn)' },
	{ tag: [tags.bool, tags.null], color: 'var(--warn)' },
	{ tag: tags.punctuation, color: 'var(--text-3)' },
	{ tag: tags.invalid, color: 'var(--danger)' }
]);

export const studioTheme = [structure, syntaxHighlighting(jsonHighlight)];
