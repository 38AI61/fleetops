import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// Browser-visible origin is 127.0.0.1:10012 or localhost:10012 (GF-153). The
// hub's OriginPatterns allowlist (GF-110) matches it, so Vite listens there.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 10012,
    strictPort: true,
    proxy: {
      '/auth': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      '/map': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      '/jobs': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      '/vehicles': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      '/alerts': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      '/alert-rules': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      // Only the API path: /reports is the SPA route (P21).
      '/reports/daily': { target: 'http://127.0.0.1:10010', changeOrigin: false },
      // XHR only (GF-273): a full page load that asks for HTML is served the SPA, not the API.
      '/geofences': {
        target: 'http://127.0.0.1:10010',
        changeOrigin: false,
        bypass: (req) => (req.headers.accept?.includes('text/html') ? '/index.html' : undefined),
      },
      '/stream': { target: 'http://127.0.0.1:10011', ws: true, changeOrigin: false },
    },
  },
  test: {
    // Pin React's dev build for tests even when the shell exports NODE_ENV=production.
    env: { NODE_ENV: 'test' },
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
  },
})
