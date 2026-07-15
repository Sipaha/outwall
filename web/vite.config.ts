import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Content-Security-Policy for the operator UI. The desktop app renders this bundle in a WebKitGTK
// webview, and outwall reaches external authenticated resources — an XSS in the operator UI must not
// become a foothold. script-src 'self' (no 'unsafe-inline'): the served document carries no inline
// scripts (the anti-FOUC uses an inline <style>, allowed by style-src 'unsafe-inline'; React inline
// style props also need it). connect-src 'self' covers /api fetches + the /api/events SSE stream.
// Injected at BUILD time only so `vite dev` (HMR needs inline/eval) is unaffected — the webview only
// ever loads the built bundle. Defense-in-depth alongside ADR-0041's operator-session gate.
const CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
].join('; ')

function cspMeta(): Plugin {
  return {
    name: 'outwall-csp-meta',
    apply: 'build',
    transformIndexHtml(html) {
      return html.replace(
        '</head>',
        `    <meta http-equiv="Content-Security-Policy" content="${CSP}" />\n  </head>`,
      )
    },
  }
}

export default defineConfig({
  plugins: [react(), tailwindcss(), cspMeta()],
  server: {
    // Dev server proxies the control API to the daemon's UIListen bind so the
    // SPA and /api share an origin in development (matches the embedded prod path).
    proxy: {
      '/api': 'http://localhost:8182',
    },
  },
  build: {
    // Build straight into the Go release embed location (internal/daemon/webui_prod.go,
    // `-tags prod` → `//go:embed all:webdist`). webdist/ is entirely gitignored, so this output
    // is never tracked; the committed dev placeholder lives in ../internal/daemon/webseed/ and is
    // never written here. emptyOutDir is required because the target is outside the Vite root.
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
