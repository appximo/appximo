// Stable client-side ids for entities/fields/relations. These are EDITOR-ONLY
// (never exported to the schema) — they key {#each} blocks, SvelteFlow nodes and
// handles, and selection. crypto.randomUUID is available in every browser the
// editor targets; the counter fallback keeps non-secure contexts working.

let counter = 0;

export function newId(prefix = 'e'): string {
	if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
		return `${prefix}_${crypto.randomUUID()}`;
	}
	counter += 1;
	return `${prefix}_${Date.now().toString(36)}_${counter.toString(36)}`;
}
