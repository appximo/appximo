import { fileURLToPath } from 'node:url';
import { defineConfig, type Plugin } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// codemirror-json-schema renders hover/lint markdown through markdown-it +
// shiki (its utils/markdown module) — a multi-MB dependency chain we don't want
// in the Studio bundle for plain-sentence tooltips. Resolve that ONE module
// (only when imported from inside codemirror-json-schema) to a tiny
// escape+<code> stub (src/lib/codeview/markdown-stub.ts).
function cmJsonSchemaMarkdownStub(): Plugin {
	const stub = fileURLToPath(new URL('./src/lib/codeview/markdown-stub.ts', import.meta.url));
	return {
		name: 'cm-json-schema-markdown-stub',
		enforce: 'pre',
		resolveId(source, importer) {
			if (
				importer?.includes('codemirror-json-schema') &&
				(source.endsWith('utils/markdown') || source.endsWith('utils/markdown.js'))
			) {
				return stub;
			}
		}
	};
}

// Plain Vite + Svelte 5 client-only SPA. `vite build` emits fully static
// HTML/JS/CSS into build/ — the Go engine serves them under /editor with zero
// Node runtime in production. `base: '/editor/'` makes every emitted asset URL
// absolute under /editor so the engine serves them from one prefix.
//
// (We use plain Vite rather than SvelteKit because the editor is a single
// client-side page: SvelteKit's SSR/prerender build externalizes @xyflow/svelte's
// uncompiled TypeScript .svelte source, which a client-only Vite build compiles
// natively — this is also the official @xyflow/svelte project layout.)
export default defineConfig({
	base: '/editor/',
	plugins: [cmJsonSchemaMarkdownStub(), svelte()],
	build: {
		outDir: 'build',
		emptyOutDir: true,
		target: 'esnext'
	},
	server: {
		port: 5175,
		proxy: {
			'/admin': { target: 'http://localhost:8080', changeOrigin: true },
			'/api': { target: 'http://localhost:8080', changeOrigin: true },
			'/docs': { target: 'http://localhost:8080', changeOrigin: true },
			'/editor/validate': { target: 'http://localhost:8080', changeOrigin: true },
			'/editor/meta-schema': { target: 'http://localhost:8080', changeOrigin: true },
			'/tenants': { target: 'http://localhost:9090', changeOrigin: true }
		}
	}
});
