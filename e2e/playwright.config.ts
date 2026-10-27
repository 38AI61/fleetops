import { defineConfig, devices } from '@playwright/test'

// No webServer: scripts/stack.sh starts compose, migrates, seeds and runs the
// Go services and Vite servers, so one orchestration path serves local and CI (GF-255).
export default defineConfig({
  testDir: '.',
  outputDir: 'test-results',
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
  use: {
    trace: 'retain-on-failure',
    screenshot: 'retain-on-failure',
    video: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
