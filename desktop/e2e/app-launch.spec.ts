import { once } from 'node:events';
import path from 'node:path';
import { test, expect, _electron as electron, type ElectronApplication } from '@playwright/test';

let app: ElectronApplication | undefined;

async function closeApp(): Promise<void> {
  if (!app) return;

  const appProcess = app.process();
  const exitPromise = appProcess ? once(appProcess, 'exit') : Promise.resolve();

  await app.close();
  await exitPromise;
  app = undefined;
}

test.beforeAll(async () => {
  app = await electron.launch({
    args: [path.join(__dirname, '..', 'out', 'main', 'index.js')],
    env: { ...process.env, CS_EXE: 'nonexistent' },
  });
});

test.afterAll(async () => {
  await closeApp();
});

test('launches the app window with expected title and minimum size', async () => {
  test.setTimeout(15_000);

  expect(app).toBeDefined();

  const window = await app!.firstWindow();
  await window.waitForLoadState('domcontentloaded');

  await expect(window).toHaveTitle(/Hangar/i);

  const browserWindow = await app!.browserWindow(window);
  const { minWidth, minHeight } = await browserWindow.evaluate((win) => {
    const [width, height] = win.getMinimumSize();
    return { minWidth: width, minHeight: height };
  });

  expect(minWidth).toBeGreaterThanOrEqual(1080);
  expect(minHeight).toBeGreaterThanOrEqual(680);

  await closeApp();
});
