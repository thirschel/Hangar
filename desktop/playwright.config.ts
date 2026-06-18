import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: 1,
  // Each spec launches its own Electron app in beforeAll. Running spec files in
  // parallel spins up multiple Electron instances that contend for the software
  // renderer on headless CI runners, which intermittently leaves a window blank
  // past the per-test timeout. Serialize to one worker for deterministic startup.
  workers: 1,
  use: {
    trace: 'on-first-retry',
  },
});
