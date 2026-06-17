import { defineConfig } from 'vitest/config';
import { resolve } from 'node:path';

export default defineConfig({
  test: {
    globals: true,
    include: ['src/**/__tests__/**/*.test.{ts,tsx}'],
    environment: 'node',
    environmentMatchGlobs: [
      ['src/renderer/**', 'jsdom'],
    ],
    setupFiles: ['src/renderer/src/__tests__/setup.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/__tests__/**', 'src/**/vite-env.d.ts'],
    },
  },
  resolve: {
    alias: {
      electron: resolve(__dirname, 'src/renderer/src/__tests__/__mocks__/electron.ts'),
    },
  },
});
