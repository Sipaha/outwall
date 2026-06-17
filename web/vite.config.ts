import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // Dev server proxies the control API to the daemon's UIListen bind so the
    // SPA and /api share an origin in development (matches the embedded prod path).
    proxy: {
      '/api': 'http://localhost:8182',
    },
  },
  build: {
    // Build straight into the Go embed location: internal/daemon/webui.go has
    // `//go:embed all:webdist`. emptyOutDir is required because the target is
    // outside the Vite root (web/). This removes a separate web/dist + copy step.
    outDir: '../internal/daemon/webdist',
    emptyOutDir: true,
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    exclude: ['node_modules/**'],
  },
})
