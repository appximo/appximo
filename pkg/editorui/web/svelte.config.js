import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// Plain Vite + Svelte 5 SPA (no SvelteKit). `{ script: true }` runs esbuild over
// <script lang="ts"> so TypeScript is fully stripped — needed because @xyflow/svelte
// ships uncompiled .svelte with complex TS (generics, type predicates) that the
// compiler's built-in light stripping doesn't fully cover. Read by both the Vite
// svelte() plugin and svelte-check.
export default {
	preprocess: vitePreprocess({ script: true })
};
