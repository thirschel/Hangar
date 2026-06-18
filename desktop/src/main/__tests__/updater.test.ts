import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const electronMock = vi.hoisted(() => ({
  app: {
    isPackaged: true,
    getVersion: () => '0.1.0',
    getName: () => 'hangar-desktop',
  },
  BrowserWindow: class {
    isDestroyed(): boolean {
      return false;
    }
    webContents = { send: vi.fn() };
  },
}));

const mockAutoUpdater = vi.hoisted(() => ({
  logger: null as unknown,
  autoDownload: false,
  autoInstallOnAppQuit: false,
  on: vi.fn(),
  checkForUpdates: vi.fn().mockResolvedValue({ updateInfo: { version: '0.2.0' } }),
  downloadUpdate: vi.fn().mockResolvedValue(undefined),
  quitAndInstall: vi.fn(),
  checkForUpdatesAndNotify: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('electron', () => electronMock);
vi.mock('electron-updater', () => ({ autoUpdater: mockAutoUpdater }));

vi.mock('../settings', () => ({
  getSettings: vi.fn().mockReturnValue({
    autoUpdate: false,
    notifications: true,
    minimizeToTray: true,
    uiRefreshMs: 2000,
  }),
}));

vi.mock('../logger', () => ({
  log: { info: vi.fn(), error: vi.fn() },
}));

import { getSettings } from '../settings';
import { checkForUpdate, downloadUpdate, initAutoUpdate, installUpdate } from '../updater';

function mockSettings(autoUpdate: boolean) {
  return {
    defaultProgram: 'copilot',
    defaultShell: 'cmd',
    autoYes: false,
    branchPrefix: '',
    workspaceDir: '',
    notifications: true,
    notificationSound: true,
    minimizeToTray: true,
    uiRefreshMs: 2000,
    autoUpdate,
  };
}

describe('updater', () => {
  beforeEach(() => {
    electronMock.app.isPackaged = true;
    mockAutoUpdater.logger = null;
    mockAutoUpdater.autoDownload = false;
    mockAutoUpdater.autoInstallOnAppQuit = false;
    vi.mocked(getSettings).mockReturnValue(mockSettings(false));
    mockAutoUpdater.checkForUpdates.mockResolvedValue({ updateInfo: { version: '0.2.0' } });
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it('initAutoUpdate sets autoDownload = false when autoUpdate setting is false', () => {
    vi.mocked(getSettings).mockReturnValue(mockSettings(false));

    initAutoUpdate();

    expect(mockAutoUpdater.autoDownload).toBe(false);
  });

  it('initAutoUpdate sets autoDownload = true when autoUpdate setting is true', () => {
    vi.mocked(getSettings).mockReturnValue(mockSettings(true));

    initAutoUpdate();

    expect(mockAutoUpdater.autoDownload).toBe(true);
  });

  it('initAutoUpdate does nothing when app.isPackaged is false', () => {
    electronMock.app.isPackaged = false;

    initAutoUpdate();

    expect(mockAutoUpdater.on).not.toHaveBeenCalled();
    expect(mockAutoUpdater.checkForUpdatesAndNotify).not.toHaveBeenCalled();
  });

  it("checkForUpdate returns 'available' with version when update exists", async () => {
    mockAutoUpdater.checkForUpdates.mockResolvedValue({ updateInfo: { version: '0.2.0' } });

    await expect(checkForUpdate()).resolves.toEqual({ status: 'available', version: '0.2.0' });
  });

  it("checkForUpdate returns 'not-available' when no update", async () => {
    mockAutoUpdater.checkForUpdates.mockResolvedValue(null);

    await expect(checkForUpdate()).resolves.toEqual({ status: 'not-available' });
  });

  it('downloadUpdate calls autoUpdater.downloadUpdate()', async () => {
    await downloadUpdate();

    expect(mockAutoUpdater.downloadUpdate).toHaveBeenCalledOnce();
  });

  it('installUpdate calls autoUpdater.quitAndInstall()', () => {
    installUpdate();

    expect(mockAutoUpdater.quitAndInstall).toHaveBeenCalledOnce();
  });
});
