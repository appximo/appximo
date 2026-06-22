import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

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
	plugins: [svelte()],
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
			'/tenants': { target: 'http://localhost:9090', changeOrigin: true }
		}
	}
});
