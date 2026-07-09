// Client for the engine's validation surface (JSON-EDITOR-S1):
//   POST /editor/validate    → schema.ValidationReport (the AI-F0-S2 unified
//                              structural + semantic report)
//   GET  /editor/meta-schema → the embedded formal JSON Schema (Draft 2020-12)
//
// Same-origin (Studio is served by the engine at /editor); in dev, vite proxies
// both paths to the local engine. Types mirror pkg/schema/report.go exactly.

export interface StructuredError {
	/** Dotted location, e.g. resources.tasks.fields.prio.type (arrays as
	 *  `foreign_keys[0]` from the semantic validator or `foreign_keys.0` from the
	 *  meta-schema — pathToRange handles both). `$` is the document root. */
	path: string;
	/** Machine-readable category, e.g. unknown_key, invalid_enum_value. */
	rule: string;
	message: string;
	expected?: string[];
	got?: string;
	fix?: string;
	/** "metaschema" (structural) | "semantic" (cross-reference). */
	source: string;
}

export interface ValidationReport {
	valid: boolean;
	errors: StructuredError[];
}

export async function validateSchemaText(text: string): Promise<ValidationReport> {
	const res = await fetch('/editor/validate', { method: 'POST', body: text });
	if (!res.ok) throw new Error(`validate: HTTP ${res.status}`);
	return (await res.json()) as ValidationReport;
}

export async function fetchMetaSchema(): Promise<Record<string, unknown>> {
	const res = await fetch('/editor/meta-schema');
	if (!res.ok) throw new Error(`meta-schema: HTTP ${res.status}`);
	return (await res.json()) as Record<string, unknown>;
}
