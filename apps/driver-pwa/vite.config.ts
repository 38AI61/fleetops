import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import { defineConfig } from 'vitest/config'

// Browser-visible origin is 127.0.0.1:10013 (GF-225, GF-153). Dev and preview
// share it. Only the driver routes are proxied (GF-225): no dispatcher paths.
const API = 'http://127.0.0.1:10010'
const proxy = {
  '/auth': { target: API, changeOrigin: false },
  '/driver': { target: API, changeOrigin: false },
  '/ingest': { target: API, changeOrigin: false },
}

export default defineConfig({
  plugins: [
    react(),
    // App shell only (GF-226): no runtime caching, so API responses are never stored.
    VitePWA({
      registerType: 'autoUpdate',
      injectRegister: 'script',
      strategies: 'generateSW',
      devOptions: { enabled: false },
      manifest: {
        name: 'FleetOps Driver',
        short_name: 'FleetOps',
        start_url: '/',
        scope: '/',
        display: 'standalone',
        orientation: 'portrait',
        theme_color: '#1f2937',
        background_color: '#ffffff',
        icons: [
          { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
          { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
        ],
      },
      workbox: {
        globPatterns: ['**/*.{js,css,html,svg,png,webmanifest}'],
        navigateFallback: '/index.html',
        navigateFallbackDenylist: [/^\/(auth|driver|ingest)\//],
      },
    }),
  ],
  server: {
    host: '127.0.0.1',
    port: 10013,
    strictPort: true,
    proxy,
  },
  preview: {
    host: '127.0.0.1',
    port: 10013,
    strictPort: true,
    proxy,
  },
  test: {
    // Pin React's dev build for tests even when the shell exports NODE_ENV=production.
    env: { NODE_ENV: 'test' },
    environment: 'jsdom',
    setupFiles: ['src/test/setup.ts'],
  },
})
