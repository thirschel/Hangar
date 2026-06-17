import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it, vi } from 'vitest';

const electronMock = vi.hoisted(() => ({
  app: {
    isPackaged: false,
  },
}));

vi.mock('electron', () => electronMock);

import { buildAsset } from '../assets';

const assetsModuleDir = path.dirname(fileURLToPath(new URL('../assets.ts', import.meta.url)));
const originalResourcesPath = process.resourcesPath;

describe('buildAsset', () => {
  afterEach(() => {
    electronMock.app.isPackaged = false;
    Object.defineProperty(process, 'resourcesPath', {
      value: originalResourcesPath,
      configurable: true,
      writable: true,
    });
  });

  it('returns the dev path when app.isPackaged is false', () => {
    electronMock.app.isPackaged = false;

    expect(buildAsset('icon.ico')).toBe(
      path.join(assetsModuleDir, '..', '..', 'build', 'icon.ico'),
    );
  });

  it('returns the packaged path when app.isPackaged is true', () => {
    electronMock.app.isPackaged = true;
    Object.defineProperty(process, 'resourcesPath', {
      value: 'C:\\resources',
      configurable: true,
      writable: true,
    });

    expect(buildAsset('icon.ico')).toBe(path.join('C:\\resources', 'build', 'icon.ico'));
  });
});
