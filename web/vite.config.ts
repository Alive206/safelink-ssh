import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

// Build output is emitted into ../internal/web/dist so that Go's `embed.FS`
// in internal/web/embed.go picks up the static assets at compile time.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: path.resolve(__dirname, '../internal/web/dist'),
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
    },
  },
})
