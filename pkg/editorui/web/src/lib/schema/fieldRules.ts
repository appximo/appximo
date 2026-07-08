// Field-validation fidelity — a faithful TypeScript mirror of the engine's
// load-time field checks (pkg/schema/validator.go: validateFieldRules +
// validateDefault). It exists so the editor gives fast, local feedback BEFORE the
// engine validates authoritatively on deploy, and so the editor only ever offers a
// validation in a type where the engine accepts it.
//
// The engine remains the authority; this is an advisory that catches every
// combination the validator rejects (min > max, default out of enum, a malformed
// pattern, a rule on the wrong type, …) so a bad schema is flagged in the panel and
// never even sent.

import { FIELD_FORMATS, type FieldDef, type FieldType, type StateMachine } from '../types/schema';

/** The field types min/max apply to (engine numericTypes). */
export const NUMERIC_TYPES = new Set<FieldType>(['int', 'int64', 'float64']);
/** The field types minLength/maxLength/pattern/format apply to (engine stringTypes). */
export const STRING_TYPES = new Set<FieldType>(['string', 'text']);

/** Mirror of schema.MaxPatternLength. */
export const MAX_PATTERN_LENGTH = 200;

export function isNumericType(t: FieldType): boolean {
	return NUMERIC_TYPES.has(t);
}
export function isStringType(t: FieldType): boolean {
	return STRING_TYPES.has(t);
}

/**
 * The coarse Postgres compatibility class of a field type — a faithful mirror of
 * pgKindForAPIType (validator.go). Two columns may be joined by a foreign key only
 * when their classes match. string/text/json all collapse to TEXT; the implicit id
 * is uuid. Empty for an unknown type.
 */
export function pgKind(t: string): string {
	switch (t) {
		case 'string':
		case 'text':
		case 'json':
			return 'text';
		case 'int':
			return 'integer';
		case 'int64':
			return 'bigint';
		case 'float64':
			return 'double';
		case 'bool':
			return 'bool';
		case 'uuid':
		case 'file':
			return 'uuid';
		case 'time':
			return 'timestamptz';
		default:
			return '';
	}
}

/**
 * Returns the compile error of a regex, or null when it compiles. JS's engine is
 * not RE2, but it rejects the same structurally-malformed patterns (unbalanced
 * groups/classes, bad quantifiers) the engine would — and where the two grammars
 * differ the engine stays the authority on deploy. We probe without the `u` flag
 * to match Go's byte-oriented RE2 more closely.
 */
export function patternCompileError(pattern: string): string | null {
	try {
		new RegExp(pattern);
		return null;
	} catch (e) {
		return e instanceof Error ? e.message : String(e);
	}
}

/** True when v is a finite number whose value is integral (an int default). */
function isInteger(v: unknown): boolean {
	return typeof v === 'number' && Number.isFinite(v) && Number.isInteger(v);
}

// A pragmatic UUID shape check (8-4-4-4-12 hex), matching the engine accepting any
// parseable uuid. Not a strict version/variant check — the engine's uuid.Parse is
// also lenient about those.
const UUID_RE = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/**
 * Issues with a field's `default` value — mirrors validateDefault. Empty when the
 * default is absent or type-compatible.
 */
export function defaultIssues(def: FieldDef): string[] {
	if (def.default === undefined) return [];
	if (def.auto) return ['default cannot be set on an auto field'];

	// Enum (string-valued): the default must be a declared member.
	if (def.enum && def.enum.length > 0) {
		if (typeof def.default !== 'string') {
			return ['default must be a string matching one of the enum values'];
		}
		return def.enum.includes(def.default)
			? []
			: [`default "${def.default}" is not one of the enum values`];
	}

	const d = def.default;
	switch (def.type) {
		case 'string':
		case 'text':
			return typeof d === 'string' ? [] : ['default must be a string'];
		case 'int':
		case 'int64':
			return isInteger(d) ? [] : ['default must be an integer'];
		case 'float64':
			return typeof d === 'number' ? [] : ['default must be a number'];
		case 'bool':
			return typeof d === 'boolean' ? [] : ['default must be a boolean'];
		case 'uuid':
			if (typeof d !== 'string') return ['default must be a uuid string'];
			return UUID_RE.test(d) ? [] : ['default must be a valid uuid string'];
		case 'time':
			// The engine accepts any string here ("now" or a literal timestamp).
			return typeof d === 'string'
				? []
				: ['default must be a string (an RFC3339 timestamp, or "now")'];
		case 'json':
			return []; // any JSON value is acceptable for a json column
		case 'file':
			// Mirror of validateDefault: file ids are minted per tenant at upload
			// time, so a schema-level default would dangle everywhere.
			return ['default is not valid on a file field — file ids are minted per tenant at upload time'];
		default:
			return [];
	}
}

// ── state machine (G5) ──────────────────────────────────────────────────────

/** Normalize `initial` (a string or array in the schema) to an ordered list. */
export function smInitialList(sm: StateMachine): string[] {
	if (Array.isArray(sm.initial)) return sm.initial;
	return sm.initial ? [sm.initial] : [];
}

/**
 * The universe of valid states (mirror of StateMachine.KnownStates in Go), ORDERED:
 * the transition keys first (declaration order), then any initial or transition
 * target not already a key. So a target-only "leaf" state still surfaces (it is a
 * terminal in the engine), and an imported machine renders faithfully.
 */
export function smKnownStates(sm: StateMachine): string[] {
	const out: string[] = [];
	const seen = new Set<string>();
	const add = (s: string) => {
		if (s && !seen.has(s)) {
			seen.add(s);
			out.push(s);
		}
	};
	for (const k of Object.keys(sm.transitions ?? {})) add(k);
	for (const s of smInitialList(sm)) add(s);
	for (const tos of Object.values(sm.transitions ?? {})) for (const t of tos) add(t);
	return out;
}

/** Outgoing transitions of a state ([] when none → terminal). */
export function smTargets(sm: StateMachine, state: string): string[] {
	return sm.transitions?.[state] ?? [];
}

/** True when a state has no outgoing transition (terminal / immutable). */
export function smIsTerminal(sm: StateMachine, state: string): boolean {
	return smTargets(sm, state).length === 0;
}

/**
 * Issues with a field's `state_machine` — a faithful mirror of validateStateMachine:
 * string/text only, ≥1 initial (each a non-empty, known state), every known state
 * present in `enum` when one is declared (coherence), and a string `default` that is
 * an initial state. Plus a UI-helpful "needs ≥1 state". Empty ⇒ the engine accepts it.
 */
export function stateMachineIssues(def: FieldDef): string[] {
	const sm = def.state_machine;
	if (!sm) return [];
	const out: string[] = [];

	if (!isStringType(def.type)) {
		// The rest of the checks assume string states (as the engine does).
		out.push(`state_machine only applies to string/text fields, not "${def.type}"`);
		return out;
	}

	const known = smKnownStates(sm);
	if (known.length === 0) out.push('state machine needs at least one state');

	const initial = smInitialList(sm);
	if (initial.length === 0) out.push('state machine requires at least one initial state');
	for (const s of initial) {
		if (!s) out.push('an initial state is empty');
		else if (!known.includes(s)) out.push(`initial state "${s}" is not a declared state`);
	}

	// Coherence with enum: every known state must be an enum value.
	if (def.enum && def.enum.length > 0) {
		for (const s of known) {
			if (!def.enum.includes(s)) out.push(`state "${s}" is not one of the field's enum values`);
		}
	}

	// A string default must be an initial state (a row's create-time value).
	if (typeof def.default === 'string') {
		if (!initial.includes(def.default)) {
			out.push(`default "${def.default}" must be one of the initial states`);
		}
	}

	return out;
}

/**
 * All load-time issues with one field's def — a faithful mirror of
 * validateFieldRules + validateDefault (+ validateStateMachine). Each string is a
 * human-readable, panel-ready message. Empty means the engine would accept the field.
 */
export function fieldDefIssues(def: FieldDef): string[] {
	const out: string[] = [];
	const isStr = isStringType(def.type);
	const isNum = isNumericType(def.type);

	// pattern (string/text only; RE2; <= MaxPatternLength)
	if (def.pattern) {
		if (!isStr) {
			out.push(`pattern only applies to string/text fields, not "${def.type}"`);
		} else if (def.pattern.length > MAX_PATTERN_LENGTH) {
			out.push(`pattern is ${def.pattern.length} chars; max is ${MAX_PATTERN_LENGTH}`);
		} else {
			const err = patternCompileError(def.pattern);
			if (err) out.push(`invalid pattern: ${err}`);
		}
	}

	// minLength / maxLength (string/text only; >= 0; min <= max)
	if (def.minLength !== undefined || def.maxLength !== undefined) {
		if (!isStr) out.push(`minLength/maxLength only apply to string/text fields, not "${def.type}"`);
		if (def.minLength !== undefined && def.minLength < 0) out.push('minLength must be >= 0');
		if (def.maxLength !== undefined && def.maxLength < 0) out.push('maxLength must be >= 0');
		if (def.minLength !== undefined && def.maxLength !== undefined && def.minLength > def.maxLength) {
			out.push('minLength must be <= maxLength');
		}
	}

	// min / max (numeric only; min <= max)
	if (def.min !== undefined || def.max !== undefined) {
		if (!isNum) out.push(`min/max only apply to numeric fields (int, int64, float64), not "${def.type}"`);
		if (def.min !== undefined && def.max !== undefined && def.min > def.max) {
			out.push('min must be <= max');
		}
	}

	// file (FILES-LINK-S1): a fixed reference to the engine file store — mirror of
	// the validator's file-field block. The target is not authorable (no
	// relation/references), a file id never changes (no on_update), and cascade
	// would delete the record when its file is deleted (rejected).
	if (def.type === 'file') {
		if (def.relation) out.push('relation is not valid on a file field — it already references the file store');
		if (def.references) out.push("references is not valid on a file field — it always references the file store's id");
		if (def.on_update) out.push("on_update is not valid on a file field — a stored file's id never changes");
		if (def.auto) out.push('auto is not valid on a file field');
		if (def.enum && def.enum.length > 0) out.push('enum is not valid on a file field');
		if (def.on_delete === 'cascade') {
			out.push('on_delete cascade is not valid on a file field — use restrict (default) or set_null');
		}
		if (def.required && def.on_delete === 'set_null') {
			out.push('on_delete set_null requires a nullable column, but this field is required');
		}
	}

	// format (string/text only; from the closed set)
	if (def.format) {
		if (!isStr) {
			out.push(`format only applies to string/text fields, not "${def.type}"`);
		} else if (!(FIELD_FORMATS as readonly string[]).includes(def.format)) {
			out.push(`unknown format "${def.format}": must be one of ${FIELD_FORMATS.join(', ')}`);
		}
	}

	out.push(...defaultIssues(def));
	out.push(...stateMachineIssues(def));
	return out;
}
