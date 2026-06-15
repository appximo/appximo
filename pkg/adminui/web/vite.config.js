import { defineConfig } from 'vite'
import solidPlugin from 'vite-plugin-solid'

// base '/admin/' makes every emitted asset URL absolute under /admin/assets/…,
// so the Go engine serves them from one prefix and the SPA never collides with
// the /admin/* ADMIN-API-V1 routes (the client uses HASH routing — /admin#/…).
export default defineConfig({
  base: '/admin/',
  plugins: [solidPlugin()],
  server: {
    port: 5174,
    // Dev proxy: the SPA talks to a locally-running engine on :8080.
    proxy: {
      '/admin/auth': { target: 'http://localhost:8080', changeOrigin: true },
      '/admin/tenants': { target: 'http://localhost:8080', changeOrigin: true },
      '/admin/observability': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
  build: { outDir: 'dist', target: 'esnext', emptyOutDir: true },
})
