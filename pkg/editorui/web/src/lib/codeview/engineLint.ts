// Layer 3 — the ENGINE's semantics in the buffer. A debounced CodeMirror lint
// source that POSTs the document to /editor/validate (schema.ValidateReport —
// the same unified structural+semantic authority behind `appitools validate
// --json`) and renders every error at its line via the dot-path → syntax-tree
// mapping. Nothing semantic is reimplemented client-side: the frontend only
// POSITIONS the report (JSON-AUDIT-V1 §2 design consequence).
//
// The report also carries the STRUCTURAL (meta-schema) errors, so unknown keys
// and closed-set violations are marked here too — one authority, no dupes with
// a second client-side structural linter, and the Draft-2020-12 conditionals
// (if/then, dependentRequired) are evaluated by the real Go validator instead
// of a client library's older draft.

import { linter, type Diagnostic } from '@codemirror/lint';
import type { Extension } from '@codemirror/state';
import { validateSchemaText, type StructuredError, type ValidationReport } from './validateApi';
import { pathToRange } from './pathToRange';

export type EngineLintStatus =
	| { kind: 'checking' }
	| { kind: 'syntax' } // the syntax layer owns the message
	| { kind: 'unreachable' }
	| { kind: 'report'; report: ValidationReport };

function renderDiagnostic(e: StructuredError): () => HTMLElement {
	return () => {
		const root = document.createElement('div');
		const msg = document.createElement('div');
		msg.textContent = e.message;
		root.appendChild(msg);
		if (e.expected && e.expected.length > 0) {
			const exp = document.createElement('div');
			exp.style.marginTop = '3px';
			exp.appendChild(document.createTextNode('expected: '));
			const code = document.createElement('code');
			code.textContent = e.expected.join(' | ');
			exp.appendChild(code);
			root.appendChild(exp);
		}
		if (e.fix) {
			const fix = document.createElement('div');
			fix.style.marginTop = '3px';
			fix.style.opacity = '0.75';
			fix.textContent = `fix: ${e.fix}`;
			root.appendChild(fix);
		}
		return root;
	};
}

/**
 * Build the lint extension. `onStatus` feeds the Code view's status bar (it
 * fires on every completed run, including "valid").
 */
export function engineLinter(onStatus: (s: EngineLintStatus) => void): Extension {
	return linter(
		async (view): Promise<Diagnostic[]> => {
			const text = view.state.doc.toString();
			try {
				JSON.parse(text);
			} catch {
				onStatus({ kind: 'syntax' }); // layer 1 (jsonParseLinter) marks the spot
				return [];
			}
			onStatus({ kind: 'checking' });
			let report: ValidationReport;
			try {
				report = await validateSchemaText(text);
			} catch {
				onStatus({ kind: 'unreachable' });
				return [];
			}
			onStatus({ kind: 'report', report });
			return report.errors.map((e) => {
				const r = pathToRange(view.state, e.path, e.rule === 'unknown_key' ? e.got : undefined);
				return {
					from: r.from,
					to: Math.max(r.to, r.from),
					severity: 'error' as const,
					source: `appitools · ${e.rule}`,
					message: e.message + (e.fix ? ` — fix: ${e.fix}` : ''),
					renderMessage: renderDiagnostic(e)
				};
			});
		},
		{ delay: 400 }
	);
}
