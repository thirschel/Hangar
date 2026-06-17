import { app, type BrowserWindow } from 'electron';
import { autoUpdater } from 'electron-updater';
import { log } from './logger';
import { getSettings } from './settings';

export type UpdateStatus = {
  status: 'idle' | 'checking' | 'available' | 'not-available' | 'downloading' | 'downloaded' | 'error';
  version?: string;
  progress?: number;
  error?: string;
};

let mainWindowRef: BrowserWindow | null = null;
let currentStatus: UpdateStatus = { status: 'idle' };

function setStatus(status: UpdateStatus): void {
  currentStatus = status;
  if (mainWindowRef && !mainWindowRef.isDestroyed()) {
    mainWindowRef.webContents.send('cs:update-status', status);
  }
}

export function getUpdateStatus(): UpdateStatus {
  return currentStatus;
}

export async function checkForUpdate(): Promise<UpdateStatus> {
  if (!app.isPackaged) return { status: 'not-available' };
  try {
    setStatus({ status: 'checking' });
    const result = await autoUpdater.checkForUpdates();
    if (result?.updateInfo?.version && result.updateInfo.version !== app.getVersion()) {
      const status: UpdateStatus = { status: 'available', version: result.updateInfo.version };
      setStatus(status);
      return status;
    }
    setStatus({ status: 'not-available' });
    return { status: 'not-available' };
  } catch (err) {
    const status: UpdateStatus = {
      status: 'error',
      error: err instanceof Error ? err.message : String(err),
    };
    setStatus(status);
    return status;
  }
}

export async function downloadUpdate(): Promise<void> {
  if (!app.isPackaged) return;
  setStatus({ status: 'downloading', version: currentStatus.version });
  await autoUpdater.downloadUpdate();
}

export function installUpdate(): void {
  autoUpdater.quitAndInstall(false, true);
}

export function initAutoUpdate(win?: BrowserWindow | null): void {
  if (win) mainWindowRef = win;
  if (!app.isPackaged) return;

  try {
    autoUpdater.logger = {
      info: (...a: unknown[]) => log.info('updater', ...a),
      warn: (...a: unknown[]) => log.info('updater:warn', ...a),
      error: (...a: unknown[]) => log.error('updater', ...a),
      debug: () => {},
    } as never;

    autoUpdater.autoDownload = false;
    autoUpdater.autoInstallOnAppQuit = false;

    autoUpdater.on('error', (err) => {
      log.error('autoUpdater error', err);
      setStatus({ status: 'error', error: err?.message || String(err) });
    });

    autoUpdater.on('update-available', (info) => {
      log.info('update available', info?.version);
      setStatus({ status: 'available', version: info?.version });
    });

    autoUpdater.on('update-not-available', () => {
      setStatus({ status: 'not-available' });
    });

    autoUpdater.on('download-progress', (progress) => {
      setStatus({
        status: 'downloading',
        version: currentStatus.version,
        progress: progress.percent,
      });
    });

    autoUpdater.on('update-downloaded', (info) => {
      log.info('update downloaded', info?.version);
      setStatus({ status: 'downloaded', version: info?.version });
    });

    const settings = getSettings();
    autoUpdater.autoDownload = settings.autoUpdate;
    void autoUpdater.checkForUpdates().catch((err) => log.error('checkForUpdates', err));
  } catch (err) {
    log.error('initAutoUpdate', err);
  }
}
