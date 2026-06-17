import path from 'node:path';
import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test';

let app: ElectronApplication;

test.beforeAll(async () => {
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, CS_EXE: 'nonexistent' },
  });
});

test.afterAll(async () => {
  await app?.close();
});

test('renders the core shell and a connection status', async () => {
  test.setTimeout(15_000);

  const window = await app.firstWindow();
  await window.waitForLoadState('domcontentloaded');

  await expect(window.locator('.sidebar')).toBeVisible();
  await expect(window.locator('.center-pane')).toBeVisible();

  const connectionStatus = window.locator('.connection');
  await expect(connectionStatus).toBeVisible();
  await expect
    .poll(async () => ((await connectionStatus.textContent()) ?? '').toLowerCase())
    .toMatch(/connecting|cannot reach session-host|error/);
});
