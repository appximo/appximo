// Dot-path → buffer range, by walking the JSON syntax tree CodeMirror already
// maintains (@codemirror/lang-json / lezer). This is how each ValidateReport
// error lands on ITS line: no second parser, no source maps — the CM tree is
// the source of truth for positions.
//
// Path grammar (both validator dialects):
//   resources.tasks.fields.prio.type      — object keys
//   resources.orders.foreign_keys[0].cols — semantic array form (name[idx])
//   resources.orders.foreign_keys.0.cols  — meta-schema array form (.idx.)
//   $                                     — the document root
//
// Resolution is BEST-EFFORT by design: an unresolvable tail (e.g. a key the
// document doesn't literally contain) anchors at the deepest node that DID
// resolve, so an error is never dropped for lack of a position — it just points
// at its nearest container.

import type { EditorState } from '@codemirror/state';
import { syntaxTree } from '@codemirror/language';
import type { SyntaxNode } from '@lezer/common';

export interface Range {
	from: number;
	to: number;
}

/** Split a dot-path into segments, expanding `name[0]` into `name`, `0`. */
export function pathSegments(path: string): string[] {
	const out: string[] = [];
	for (const seg of path.split('.')) {
		const m = /^([^[\]]*)((?:\[\d+\])+)$/.exec(seg);
		if (m) {
			if (m[1]) out.push(m[1]);
			for (const idx of m[2].matchAll(/\[(\d+)\]/g)) out.push(idx[1]);
		} else {
			out.push(seg);
		}
	}
	return out.filter((s) => s !== '');
}

const VALUE_NODES = new Set(['Object', 'Array', 'String', 'Number', 'True', 'False', 'Null']);

/** The value node of a Property — the lezer tree also contains the anonymous
 *  ":" token as a child, so filter to actual value node types. */
function propertyValue(prop: SyntaxNode): SyntaxNode | null {
	for (let child = prop.firstChild; child; child = child.nextSibling) {
		if (VALUE_NODES.has(child.name)) return child;
	}
	return null;
}

function findProperty(state: EditorState, obj: SyntaxNode, key: string): SyntaxNode | null {
	for (const prop of obj.getChildren('Property')) {
		const nameNode = prop.getChild('PropertyName');
		if (!nameNode) continue;
		try {
			if (JSON.parse(state.sliceDoc(nameNode.from, nameNode.to)) === key) return prop;
		} catch {
			/* malformed key while typing — skip */
		}
	}
	return null;
}

/** All value children of an Array node (skips punctuation). */
function arrayItems(arr: SyntaxNode): SyntaxNode[] {
	const out: SyntaxNode[] = [];
	for (let c = arr.firstChild; c; c = c.nextSibling) {
		if (c.name !== '[' && c.name !== ']' && c.name !== ',' && c.name !== '⚠') out.push(c);
	}
	return out;
}

/** Clamp a container node to something visually markable: its first line. */
function containerRange(state: EditorState, node: SyntaxNode): Range {
	const line = state.doc.lineAt(node.from);
	return { from: node.from, to: Math.min(node.to, line.to) };
}

/**
 * Resolve a ValidateReport dot-path to a buffer range. `unknownKey` (the
 * report's `got` on an unknown_key error) refines an object-level path to the
 * offending property itself.
 */
export function pathToRange(state: EditorState, path: string, unknownKey?: string): Range {
	const root = syntaxTree(state).topNode.firstChild; // JsonText → the root value
	const whole: Range = { from: 0, to: Math.min(state.doc.length, state.doc.line(1).to) };
	if (!root) return whole;
	if (!path || path === '$') return whole;

	let node: SyntaxNode = root;
	let lastProp: SyntaxNode | null = null; // deepest property we resolved
	for (const seg of pathSegments(path)) {
		if (node.name === 'Object') {
			const prop = findProperty(state, node, seg);
			if (!prop) break;
			lastProp = prop;
			const val = propertyValue(prop);
			if (!val) break;
			node = val;
		} else if (node.name === 'Array' && /^\d+$/.test(seg)) {
			const item = arrayItems(node)[Number(seg)];
			if (!item) break;
			node = item;
		} else {
			break; // path goes deeper than the document — anchor here
		}
	}

	// unknown_key: the path is the CONTAINING object — point at the key itself.
	if (unknownKey && node.name === 'Object') {
		const bad = findProperty(state, node, unknownKey);
		if (bad) {
			const nameNode = bad.getChild('PropertyName');
			if (nameNode) return { from: nameNode.from, to: nameNode.to };
		}
	}

	if (lastProp) {
		const nameNode = lastProp.getChild('PropertyName');
		const val = propertyValue(lastProp);
		// Key through scalar value reads best; a container value would smear the
		// squiggle over pages, so clamp it to the property's first line.
		const from = nameNode ? nameNode.from : lastProp.from;
		const to = val && (val.name === 'Object' || val.name === 'Array') ? containerRange(state, lastProp).to : (val ?? lastProp).to;
		return { from, to };
	}
	return node === root ? whole : containerRange(state, node);
}
