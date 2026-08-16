import { defineConfig, devices } from '@playwright/test'

// No webServer block: the Go test at internal/e2e/e2e_test.go owns the stack's
// lifetime, because internal/testsupport's Postgres helpers take a *testing.T
// and cannot be called from a plain command. The base URL arrives from it.
export default defineConfig({
  testDir: './e2e',
  // Serial, and no retries: a flaky E2E that passes on retry is a bug report
  // nobody reads. One worker also keeps the per-IP rate limiters — which see
  // every browser as the same client — out of the results.
  workers: 1,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.AIRBG_E2E_BASE_URL,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
