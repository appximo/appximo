import { defineConfig } from 'vite'
import solidPlugin from 'vite-plugin-solid'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [solidPlugin(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: 'http://localhost:3099', changeOrigin: true }
    }
  },
  build: { outDir: 'dist', target: 'esnext' }
})
