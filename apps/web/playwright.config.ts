import { defineConfig, devices } from "@playwright/test";

// Points at the already-running compose `web`/`api` services (port 5173),
// not a dev server this config starts itself — see docs/local-dev-walkthrough.md
// for how to bring the stack up. No webServer block here on purpose: this
// is meant to be run against the real stack, the same one a human would
// click through.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  retries: 0,
  reporter: [["list"], ["html", { outputFolder: "test-artifacts/report", open: "never" }]],
  outputDir: "test-artifacts/results",
  // This machine's Docker Desktop VM runs Postgres/MinIO/API/web
  // alongside a fresh Chromium per test; under a full-suite run (not a
  // single isolated test) that's occasionally slower than Playwright's
  // 5s default expect() timeout, even though every individual request
  // completes in well under a second in isolation (verified directly —
  // see docs/implementation-status.md). This is environmental slack,
  // not a retry-away-a-flaky-app-bug — no retries are configured above.
  expect: {
    timeout: 10000,
  },
  use: {
    baseURL: "http://localhost:5173",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    actionTimeout: 10000,
    // Start with the storage notice already acknowledged so its fixed bar
    // never covers buttons in unrelated tests; lifecycle.spec.ts opts out.
    storageState: {
      cookies: [],
      origins: [{ origin: "http://localhost:5173", localStorage: [{ name: "deeparchive.notice_ack", value: "1" }] }],
    },
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
  ],
});
